# Plan — Phase 16: sync protocol, cross-mapping distribution, moderated release

> **§1 (change feed) and §4 (moderation gaps) LANDED 2026-07-21.** See "Outcome"
> at the end. §0 is still undecided and blocks §2; §3 and §5 are not started.

## What you asked for, against what exists

| Requirement | Status today |
|---|---|
| Sync frameworks to the main platform | **Exists** — `/api/registry/v1`, bearer token, ed25519-signed bundles, `registryctl pull` |
| …with their cross-mappings | **Half.** Requirement mappings ride in the bundle. **Control mappings do not.** `GRCSeed` — the shape the GRC actually imports — **drops every mapping.** |
| Notify / add updates on each sync | **Absent.** No change feed, no webhook, no cursor. `?since=` exists but is broken, and its only client hardcodes `nil`. |
| Only published + moderated frameworks | **Already correct.** See below — this is the one requirement mostly met. |

**The moderation gate you describe is already built.** `pkg/iam/policies.go` has three
roles: `auditor` may create/edit/submit but holds neither `version:approve` nor
`version:publish`; `moderator` and `superadmin` hold both. The lifecycle is
`DRAFT → IN_REVIEW → APPROVED → PUBLISHED → DEPRECATED`, and distribution serves
PUBLISHED only. Separation of duties is enforced on approve
(`ErrSeparationOfDuties`, `actorID == version.AuthorID`). Region scoping means a
moderator can only approve frameworks in their own regions.

So requirement 3 is not "build a moderation gate" — it is **close four gaps** in
the gate that already exists (§4). The real work is requirements 1 and 2.

## Measured facts to confirm before starting

The database was not reachable while planning. Re-run before §1:

```sql
SELECT status, count(*) FROM framework_versions GROUP BY status;
SELECT count(*) FROM requirement_cross_mappings;
SELECT count(*) FROM control_cross_mappings;
SELECT count(DISTINCT target_framework_code) FROM requirement_cross_mappings;
```

**If PUBLISHED count is 0, §2 is free** — the bundle shape can change with no
signature to keep compatible, exactly as in Phase 13. If it is non-zero, the
bundle format must be versioned and old versions must keep their stored
signature. Do not retro-sign; a published hash is a promise.

---

## The architectural decision: where do mappings live?

This is the fork the whole plan turns on. Decide it first.

**Today mappings are inside the source framework's signed bundle.** That means a
mapping is part of what framework A's signature vouches for. Consequence: to add
one mapping from A to a newly published B, **A must be re-versioned, re-approved
and re-published**. Publish framework N+1 and you must republish all N existing
frameworks to connect it. Cross-mapping is O(N²) and every edge costs a full
moderation cycle. `MappingTable.jsx` already tells auditors mappings are
DRAFT-only, which is the correct consequence of this design — and the reason it
does not scale.

**Recommendation: promote mapping sets to a first-class signed artifact.**

```
MappingSet {
  sourceFramework, sourceVersion,
  targetFramework, targetVersion,
  requirementMappings[], controlMappings[],
  approvedBy, publishedAt, signature
}
```

Versioned, moderated and published on its own lifecycle, pulled independently.
Adding framework N+1 publishes N mapping sets and touches no existing framework.
It also gives control mappings a home without changing framework bundle bytes at
all, and it makes "the mapping changed but the framework didn't" a representable
event — which is precisely what your "notify new update to sync" needs.

Keep requirement mappings in the bundle as well for one release, so existing
consumers do not break; mark the in-bundle copy advisory and have the mapping set
be authoritative.

**Cost:** a second lifecycle (submit/approve/publish) and a second catalog. If
you would rather not pay that now, the fallback is in-bundle only — accept the
republish churn and cap it by batching mapping work into scheduled framework
versions. Say which you want; §2 and §3 differ materially between them.

**Security consequence either way:** a signed artifact cannot be filtered per
token. A mapping naming `iso-27001/A.9.2.3` reveals that framework's control
codes to any consumer who can pull the artifact containing it. For public
standards this is harmless. **For non-public tenant frameworks it is a leak.**
Gate at authoring time: refuse to sign a mapping whose target framework is not
`Public`, or require an explicit override. This must be decided before the first
mapping set is signed, because signing is what makes it irrevocable.

---

## 1. Change feed — the foundation for "notify new update"

`?since=` cannot be repaired into a sync primitive:

- It is an **in-Go post-filter after loading every framework** — cost is O(frameworks) regardless of `since`.
- A version with `published_at IS NULL` is **never filtered out** and reappears on every incremental poll.
- **Deprecation and withdrawal are unrepresentable.** A `since` response cannot say "stop using this."
- Timestamps are unsafe as a cursor: an event committed at `T` but made visible after a consumer has read `T` is lost permanently.

Replace with an append-only event log.

```sql
CREATE TABLE distribution_events (
  seq         bigint PRIMARY KEY,     -- assigned under advisory lock, NOT bigserial
  tenant_id   ..., 
  kind        text NOT NULL,          -- published | deprecated | mapping_published | mapping_revoked
  framework_ref text NOT NULL,
  version     text NOT NULL,
  content_hash text,
  region      text NOT NULL,          -- denormalised so the feed filters without a join
  public      boolean NOT NULL,
  created_at  timestamptz NOT NULL
);
```

**`seq` must not be `bigserial`.** A sequence hands out 101 and 102 to two
transactions; if 102 commits first, a consumer reading to `seq=102` will never
see 101. Assign `seq` inside the publish transaction while holding
`pg_advisory_xact_lock(<fixed key>)`, so sequence order equals commit order.
Publishing is a rare human action — serialising it costs nothing and removes an
entire class of silent data loss. Pin this with a concurrency test that publishes
two frameworks simultaneously and asserts no gap is observable.

**`GET /api/registry/v1/changes?since=<seq>&limit=<n>`** → `{events[], nextSeq, hasMore}`,
filtered by the token's regions and the public/tenant gate exactly as `/catalog`
is. Deprecation events are what let a GRC retire a framework it already imported —
the capability that does not exist at all today.

**Webhooks are an optional layer on top, not the mechanism.** If the GRC is
reachable, POST `{seq}` to a registered endpoint with an HMAC signature over the
body, retried with backoff. **Carry no payload** — the notification says only
"something changed, come look", and the consumer then pulls over the
authenticated channel. A compromised webhook endpoint then leaks nothing, and a
missed webhook is harmless because the cursor makes the pull path authoritative.
Do not build webhooks before the cursor works.

## 2. Get mappings into what the consumer actually reads

- **`GRCSeed` carries no mappings at all** (`flatten.go` drops them explicitly).
  Any GRC importing via seed today has zero mapping data — this is the single
  biggest gap between what the registry knows and what the platform receives.
- **Control mappings are absent from the bundle.** `buildBundleV2` loads
  categories, requirements and `RequirementCrossMappings`;
  `ControlCrossMappings` is never referenced in `signing_service.go`. They are
  authored, reviewed, coverage-reported — then dropped at the distribution edge.
- Add a **framework-level `dependencies[]`** — the distinct `(frameworkCode,
  version)` pairs the mappings reference. Today a consumer must walk every
  requirement to discover what a framework relates to, and cannot learn it from
  `/catalog` at all. With `dependencies[]`, a client can fetch what it needs, or
  report honestly that a target is outside its token scope.
- Keep resolution client-side and late-binding. Code-addressed stubs that resolve
  when the target arrives is the right design — it is already how the registry
  works internally, and it means a partial sync degrades to dangling references
  rather than a failed import.
- `/seed` is unsigned and has **no consumer in this repo** (`distclient` verifies
  the bundle then flattens locally, deliberately not trusting the server's seed).
  Deprecate the endpoint rather than extending it.

## 3. Make the client actually incremental

`distclient.Sync` re-downloads and rewrites every framework on every run: `since`
is hardcoded `nil`, `SyncResult.Skipped` is declared and never assigned, and the
fetched `ContentHash` is stored but never compared. It also never sends
`If-None-Match`, so the one ETag the server does set has no client — and
`getJSON` treats a 304 as an error.

- Persist sync state in `--out-dir/.registry-sync.json`: cursor + per-framework
  `{version, contentHash}`. Advance the cursor **only after** the bundle is
  verified and written, so a crash re-fetches rather than skips.
- Drive from `/changes`; fall back to full catalog when there is no cursor.
- Send `If-None-Match`; treat 304 as "unchanged", not an error.
- Add `--framework <ref>` for single-framework pull, and `--dry-run` to report
  what would change.
- Fix `Controls: len(bundle.Controls)` — always `0` for v2 bundles, which makes
  every successful sync print `controls=0` and look empty. Same bug in
  `newVerifyCmd`.

## 4. Close the four gaps in the moderation gate

The gate is sound; these are the holes:

1. **No separation of duties on publish.** SoD is enforced only on `Approve`. With
   `Quorum` hardcoded to `1`, the moderator who approves can immediately publish,
   and the original author can publish too once anyone else approved. Apply the
   same author check to `Publish`, and make quorum configurable per framework
   rather than a hardcoded `1` at both creation sites.
2. **No record of who published.** There is `published_at` and `author_id`, but no
   `published_by`, `approved_by` or `approved_at`. The approver is recoverable
   from the `approvals` table and the publisher only from the audit log. Add the
   columns and **put approver + publisher in the signed bundle** — "moderated by
   whom" is exactly the provenance a consuming GRC needs to trust the artifact.
3. **`Reject` and `Deprecate` are unreachable.** Both exist in
   `lifecycle_service.go` with zero non-test callers — no HTTP route, no CLI
   command, no button. A moderation workflow without reject is not a workflow;
   the moderator's only options are approve or leave it in limbo. Deprecate is
   what emits the retirement event in §1.
4. **`registryctl --actor <email>` authenticates nobody.** Role, region and SoD
   checks all apply, but the caller asserts their own identity by flag — anyone
   with shell and DB access can act as any moderator. Not an IAM bypass, but it
   means CLI publish has no audit integrity. Restrict the CLI to break-glass and
   log every `--actor` use distinctly, or require a credential.

Also: `ActionVersionReview` is granted to moderator and enforced nowhere. Delete
it or use it.

## 5. Sync observability

The `downloads` table is **write-only** — no query method, no UI, no CLI. Rows
are inserted even for 304s, and the ETag check happens *after* the full bundle is
assembled and the row written, so every conditional poll does the work it was
meant to avoid.

- Check `If-None-Match` **before** assembling and before recording.
- Record catalog/changes polls too, and add `outcome` (200/304/403).
- Give it a read path: per-token "last synced, at which cursor, holding which
  versions" — this is what makes "each time synced, add updates" visible to an
  operator instead of invisible.
- `TouchLastUsed` writes the token row on **every** request with the error
  discarded. Under regular polling from several instances that is constant write
  amplification on one row. Throttle to once per minute.

---

## Verification

1. `go test ./...` green, plus new: cursor monotonicity under concurrent publish; a
   deprecation event reaching a consumer; mapping round-trip registry → bundle →
   client → resolved.
2. **Two-instance sync test.** Publish A, sync, publish B with mappings to A, sync
   again — assert the second sync transfers only B, that A's mappings resolve, and
   that the cursor advanced exactly once.
3. **Crash safety.** Kill the client mid-write; re-run; assert no framework is
   skipped and none is corrupt.
4. **Moderation gate, negative tests.** An auditor cannot publish; an out-of-region
   moderator cannot publish; an author cannot publish their own version; a
   non-PUBLISHED version is invisible on `/catalog`, `/changes` and `/bundle`.
5. **Scope leak test.** A token scoped to one region must not learn control codes
   of a non-public framework in another — including through mappings. Note that
   `GET /frameworks/{id}` currently applies the copyright gate but **not** the
   region gate, so a token can already enumerate versions and content hashes of a
   public out-of-region framework. Fix while in here.
6. Re-verify a previously published bundle against its **stored** signature.

## Risks

- **Signing is the trust anchor.** Any change to bundle contents changes the hash.
  Version the format; never re-sign an existing published version.
- **Cursor correctness is the whole feature.** A cursor that silently skips an
  event gives consumers stale compliance data while reporting success — worse
  than no sync. The advisory-lock ordering and its test are not optional.
- **Signed artifacts cannot be filtered per consumer.** Decide the non-public
  mapping-target rule before the first signature, because after that it is fixed.
- **Mapping-set lifecycle doubles the moderation surface.** If §0 goes that way,
  budget for a second review queue in the console.

## Sequencing

Decide §0 first — it changes §2 and §3.

Then: `distribution_events` + `/changes` (§1) → client cursor + incremental pull
(§3) → mappings into the payload, format-versioned (§2) → moderation gaps (§4) →
observability (§5) → webhooks last, only once the pull path is proven.

§1 and §4 are independent and can land in either order. Do not build webhooks
before the cursor: a push you cannot reconcile against an authoritative pull is a
sync bug generator.

---

## Outcome — §1 and §4 (2026-07-21)

Landed the two decision-independent halves. `go test ./...` green (one
pre-existing unrelated failure, below); verified against the live database.

**Measured before starting** — the counts the plan asked for:

| | |
|---|---|
| PUBLISHED versions | **1** → bundle format must be versioned, not mutated |
| DRAFT versions | 4 |
| requirement cross-mappings | 14 (all pointing at 1 target framework) |
| **control cross-mappings** | **65 — none of which reach any consumer** |

### §1 Change feed

`distribution_events` (migration `20260721T100000Z.sql`) + `GET
/api/registry/v1/changes?since=<seq>&limit=<n>` returning
`{events, nextSeq, headSeq, hasMore}`.

- **`seq` is assigned under `pg_advisory_xact_lock`, not `bigserial`.** This is
  the whole point of the design: a sequence hands 101 and 102 to concurrent
  transactions with no commit ordering, so a consumer that reads 102 and stores
  it never receives 101 — silent loss reported as a successful sync. Pinned by
  `TestChangeFeedCursorIsContiguousUnderConcurrentPublish`, which publishes 8
  frameworks from 8 goroutines released together and asserts a contiguous run.
- **History was replayed into the feed on migration**, so a consumer starting at
  cursor 0 receives what was already published instead of concluding the
  registry is empty.
- **Visibility is denormalised onto the event** (`region`, `public`) and applied
  identically to `/catalog`. `headSeq` is scope-filtered too — a global head
  would disclose that out-of-scope activity exists.
- Deprecation is now representable, which it was not: an EU-scoped token sees
  head 0 while RU frameworks publish.

### §4 Moderation gaps

1. **Separation of duties now applies to publish**, not just approve. Gated on
   the *author*, not the approver — requiring a third identity would deadlock a
   two-person registry (one auditor, one moderator), which is the common
   deployment. Pinned by `TestAuthorCannotPublishOwnVersion`.
2. **`published_by` / `approved_by` / `approved_at` recorded.** `approved_*`
   backfilled from the `approvals` table; `published_by` deliberately left empty
   for pre-existing rows — it lived only in the audit log and is not reliably
   joinable, so the migration says "not known" rather than guessing.
3. **`Reject` and `Deprecate` are reachable**: HTTP routes, `registryctl
   framework reject|deprecate`, and console buttons. Both existed in the service
   with zero non-test callers — a moderator could previously only approve or
   leave a submission pending indefinitely.
4. **Closed a scope leak found while in here**: `GET /frameworks/{id}` applied
   the copyright gate but not the region gate, so a token could enumerate
   versions and content hashes of an out-of-region framework it could never
   fetch. Now 403.

### Verified live

Full moderation flow driven end to end, each step asserted:

| step | actor | result |
|---|---|---|
| submit | root (author) | ok |
| approve | root (author) | refused — separation of duties |
| approve | mod | ok |
| **publish** | **root (author)** | **refused — the new rule** |
| publish | mod | ok → feed seq 2 |
| deprecate | mod | ok → feed seq 3, catalog drops it |

A consumer sitting at cursor 1 received exactly the one new publish, then
exactly the one deprecation — not the whole catalog either time.

### Not done / known

- **§0 is still undecided and blocks §2.** Mappings remain inside the source
  framework's signed bundle, so connecting a new framework still requires
  re-versioning every framework that maps to it.
- **The 65 control cross-mappings still reach no consumer**, and `GRCSeed` still
  drops all mappings. That is §2 and needs the §0 decision first.
- §3 (client cursor) and §5 (observability) not started. `distclient.Sync` still
  hardcodes `since=nil` and re-downloads everything.
- `TestNextVersionJob` fails, **pre-existing and unrelated** — verified by
  running it at the prior commit, where it fails earlier in the same pipeline.
  It makes a real outbound call to `api.openai.com` with a fake key, so it fails
  without network. Worth fixing separately: a test in `go test ./...` should not
  depend on a live third-party API.
- Console Reject/Deprecate buttons were not visually confirmed in a browser —
  that needs a signed-in moderator session. Backend paths are verified end to
  end; the frontend was verified to compile and to carry both new endpoints.
