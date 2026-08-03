# Plan — org approval + keyless cloud sync

> **BACKEND SECURITY CORE LANDED 2026-07-21, DB-verified.** organizations table,
> coredata, the lifecycle service, and `SyncContextForOrg` (the access gate) are
> done and tested against a live database. Remaining: the HTTP session mount
> (needs multi-tenant org-user signup, since `createIdentityTx` currently pins the
> platform tenant) and the console UI. See "Outcome" at the end.



## The ask

Today a consuming instance authenticates with a manually-issued bearer token
(`mzt_…`): a superadmin runs `registryctl token issue` and hands the string over
to be pasted into the consumer's config. On the cloud that copy-paste step is
friction. Replace it, for the cloud, with:

1. An organization registers (lands **pending**).
2. A superadmin **approves** the org.
3. From then on the org syncs **instantly, with no key** — its authenticated
   cloud session is the credential.

Decided (2026-07-21): the session is the identity (no token anywhere), and
approval grants **all published public frameworks** (region/copyright rules
unchanged).

## Why this is small, not a rewrite

The whole distribution stack — `/catalog`, `/changes`, `/frameworks/{…}`,
`/mappings`, region + copyright gating — is driven off one value: a
`TokenContext{OwnerTenant, Regions}`. Nothing in the handlers cares *how* that
context was produced. Today it comes from resolving a bearer token. The only new
thing is a second producer:

```
approved-org session  ──▶  TokenContext{ OwnerTenant: orgTenant, Regions: nil }
```

`OwnerTenant = orgTenant` is the key trick: the org owns none of the registry's
frameworks, so the existing copyright predicate `public = TRUE OR tenant_id =
@owner` admits exactly the published public frameworks and nothing tenant-private.
`Regions: nil` means all regions. **Zero changes to catalog/changes/bundle/mapping
logic** — they already do the right thing given that context.

## Model

`organizations` — one row per consuming org, keyed by the org's tenant:

```sql
CREATE TABLE organizations (
  tenant_id    TEXT PRIMARY KEY,        -- the org's tenant; its users live here
  name         TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'pending',  -- pending | approved | suspended
  requested_by TEXT NOT NULL DEFAULT '',         -- identity that registered it
  approved_by  TEXT NOT NULL DEFAULT '',
  approved_at  TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL,
  updated_at   TIMESTAMPTZ NOT NULL,
  CONSTRAINT organizations_status_check CHECK (status IN ('pending','approved','suspended'))
);
```

An org *is* a tenant; its users are identities under that tenant (the existing
identity/membership model, unchanged). Registry staff (superadmins, auditors)
stay under the platform tenant and are not organizations.

## Lifecycle service

- `RegisterOrganization(name, firstUser)` — creates the org (pending) and its
  first admin identity under a fresh tenant. Self-serve or superadmin-created;
  either way it lands pending.
- `ApproveOrganization(actor, tenantID)` — superadmin only; pending→approved,
  records approver + time, audit line. Instant: the org's next request passes.
- `SuspendOrganization(actor, tenantID)` — approved→suspended; revokes sync
  without deleting. Checked per request, so it takes effect immediately.
- `ListOrganizations(actor, statusFilter)` — superadmin review queue.
- `OrganizationForIdentity(identityID)` — resolves the caller's org for the
  bridge.

## The security boundary — session → TokenContext bridge

A new middleware, mounted **after** session auth:

```
NewOrgSyncMiddleware:
  id  := IdentityFrom(ctx)                 // already authenticated by session
  org := OrganizationForIdentity(id)
  if org == nil || org.Status != approved  -> 403 (fail closed)
  inject TokenContext{ OwnerTenant: org.TenantID, Regions: nil }
```

Mount the **same distribution handlers** under a session-authed group, e.g.
`/api/sync/v1/*` (session cookie → org-sync middleware → handlers). The handlers
read `TokenContextFrom(ctx)` and are reused verbatim.

The existing bearer-token API (`/api/registry/v1`) stays for self-hosted /
external consumers. Cloud orgs use the session path and never touch a key.

**Fail closed is the whole game here.** Unknown org, pending, suspended, or a
missing session → deny. This middleware is the entire access-control surface for
keyless sync; it gets its own negative tests.

## Instant, by construction

Approval flips one row. The next sync request from the org's session re-reads
that status in the bridge and passes. No token to mint, deliver, or propagate —
"instant" falls out of there being no artifact between approval and access.

## Console

- **Superadmin**: an Organizations page — pending review queue, approve /
  suspend, with who-approved-when shown.
- **Org user**: once approved, the frameworks/sync surface works against
  `/api/sync/v1`; while pending, a clear "awaiting approval" state rather than an
  opaque 403.

## Verification (DB is up)

- Pending org session → 403 on every sync endpoint.
- Approved org session → catalog returns exactly the published **public**
  frameworks; a tenant-private framework is absent.
- Suspended org → 403 immediately after suspension (same session, next request).
- Approval is superadmin-gated: a non-superadmin approving → forbidden.
- Provenance: approved_by/approved_at set; audit line emitted.
- The bearer-token path still works unchanged (no regression).

## Sequencing

Migration + coredata → lifecycle service → the bridge middleware + session mount
(the security core) → negative/positive tests against the DB → console UI last.
Land the backend with its tests before any UI, since the middleware is the
security boundary and must be proven first.

## Risks

- **The bridge is fail-closed access control.** A bug that admits a pending or
  suspended org leaks the catalog. Negative tests are not optional.
- **Session identity must be an org user, not registry staff.** A platform-tenant
  identity has no org row → denied by the same check, which is correct (staff use
  the console, not the sync surface).
- **Self-serve registration is a new unauthenticated surface** if built that way.
  If registration is superadmin-created instead, that surface doesn't exist. Decide
  before exposing a public register endpoint; the approval gate is the same either
  way.

---

## Outcome — backend security core (2026-07-21)

Built and verified against a live database:

- **Migration** `20260722T090000Z.sql` — `organizations` (tenant_id PK, status
  pending|approved|suspended, approval provenance).
- **coredata** `organization.go` — Insert, LoadByTenant (PK lookup, the bridge
  hot path), UpdateStatus, LoadByStatus (review queue).
- **Service** `organization_service.go` — RegisterOrganization (pending),
  ApproveOrganization / SuspendOrganization (superadmin-gated, immediate),
  ListOrganizations, and `SyncContextForOrg` — the access boundary.

**The design held exactly as planned: zero changes to distribution logic.**
`SyncContextForOrg` returns `TokenContext{OwnerTenant: orgTenant, Regions: nil}`
for an approved org, and the existing `Catalog`/`Changes`/`Bundle`/mappings serve
the right data because the org's tenant owns no frameworks, so the copyright gate
admits only public ones.

`TestOrgApprovalKeylessSync` proves the whole gate against real rows:

| step | result |
|---|---|
| pending org → sync | denied (`ErrOrgNotApproved`) |
| moderator approves | forbidden (superadmin only) |
| superadmin approves | ok |
| approved → catalog | exactly the public framework, **not** the proprietary one |
| approved → fetch private-fw | refused |
| approved → change feed | only the public framework |
| suspend → sync | denied immediately (same tenant, next request) |
| unknown tenant → sync | denied with the same error (no existence disclosure) |

## Remaining

1. **HTTP session mount.** A middleware: session identity → its org tenant →
   `SyncContextForOrg` → inject the TokenContext → reuse the distribution handlers
   under `/api/sync/v1`. Blocked on (2), because "identity → org tenant" requires
   org users to live under the org's tenant.
2. **Multi-tenant org-user signup.** `createIdentityTx` hardcodes
   `s.cfg.PlatformTenant`; org users must be created under their org's tenant so
   the session→org linkage is just `identity.ID.TenantID()`. This is the one
   invasive change (it touches the identity/scope model) and should be done
   carefully with its own tests — logins, memberships and authorize must all work
   under a non-platform tenant.
3. **Console UI.** Superadmin: an Organizations review queue (approve/suspend).
   Org user: an "awaiting approval" state while pending, the sync/frameworks
   surface once approved.

Do (2) before (1); (3) last. The security gate — the part that could leak the
catalog if wrong — is the part that is already done and tested.
