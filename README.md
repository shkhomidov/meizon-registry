# Meizon Framework Registry (`registryd`)

A Go cloud service for the full compliance-framework lifecycle —
**register → author → review → approve → version → publish → distribute** — that
mirrors the Meizon/`probod` GRC platform's stack so a published framework imports
into a GRC instance with **zero manual editing**. Both sides share one canonical
schema (`pkg/fwschema`).

This repository is **Phase 1: a runnable backend slice**. It compiles, runs, and
proves the core end-to-end. The React authoring console, console/session GraphQL,
and the GRC-side sync worker are deliberately deferred (see *Roadmap*).

## What works today

- **Trust anchor** (`pkg/fwschema`) — rich exchange schema v1, strict validation,
  deterministic canonical-JSON hashing, **ed25519 sign/verify**, and `Flatten()`
  to the flat GRC seed. Fully unit-tested (determinism, tamper rejection,
  round-trip).
- **Lifecycle** (`pkg/registry`) — `DRAFT → IN_REVIEW → APPROVED → PUBLISHED →
  DEPRECATED` with immutable published snapshots, publish-time ed25519 signing,
  and an append-only audit log.
- **RBAC + region isolation** (`pkg/iam`) — policy engine (`service:resource:op`,
  deny > allow > implicit-deny) with a region-scoping condition. Roles:
  `superadmin` (global, env-allowlisted), `moderator` (review/approve/publish in
  region), `auditor` (author/submit in region). **Separation of duties**: the
  sole author can never approve.
- **Distribution API** (`/api/registry/v1`) — read-only, per-instance bearer
  token, region-scoped, ETag/HEAD, rate-limited: `/catalog`, `/frameworks/{id}`,
  `/frameworks/{id}/versions/{version}` (signed bundle) and `.../seed` (flat GRC
  seed). Enforces the **copyright gate** (proprietary frameworks served only to
  the owning tenant).
- **Data layer** (`pkg/coredata`) — GID-keyed, tenant-scoped entities with
  embedded migrations that auto-apply on boot.
- **Reference consumer** (`pkg/distclient`, `registryctl pull`/`verify`) — the
  GRC-side path: discover via catalog, download, **verify the ed25519 signature
  against pinned keys**, and emit import-ready seeds. Realizes online sync and
  air-gapped import without modifying the GRC.
- **Authoring console** — session-cookie auth (`/api/connect/v1`) + a role-gated
  console API (`/api/console/v1`), and a React SPA (`apps/registry`) to sign in
  and drive the whole lifecycle in a browser. The console API is REST+JSON (the
  spec's gqlgen GraphQL is a documented follow-up; the GRC-interop contract is
  unaffected since it is REST/token by design).
- **Ops** — `registryd` (daemon, unit framework), `registryd-bootstrap`
  (env → YAML config), `registryctl` (cobra CLI that drives the whole lifecycle),
  plus a `Dockerfile`, `compose.prod.yaml`, a Helm chart and
  `docs/INSTALL-PRODUCTION.md`.

## Layout

```
cmd/registryd            daemon (unit framework: metrics :8081, api :8080)
cmd/registryd-bootstrap  REGISTRYD_* env -> YAML config
cmd/registryctl          admin/ops CLI (authoring + governance)
pkg/fwschema             canonical schema + sign/verify + Flatten   <- the contract
pkg/registry             service layer (lifecycle, SoD, signing, distribution)
pkg/iam                  policy engine + role policies + region scope
pkg/coredata             entities + embedded SQL migrations
pkg/crypto               passwdhash (PBKDF2+pepper), cipher (AES-256-GCM), hash
pkg/gid pkg/page         24-byte ids, keyset pagination
pkg/server               chi HTTP: /api/registry/v1 + /api/admin/v1
pkg/registryconfig pkg/bootstrap   config types + env renderer
pkg/distclient           reference GRC-side consumer (pull + verify)
pkg/securecookie         HMAC-signed session cookie
pkg/server/api/{connect,console}/v1   session auth + role-gated console API
apps/registry            React console (Vite, plain-fetch, credentials:include)
Dockerfile compose.prod.yaml helm/ docs/INSTALL-PRODUCTION.md   deployment
```

## Quick start

```sh
make pg-up                                   # disposable Postgres on :55432
export REGISTRYD_ENCRYPTION_KEY=$(openssl rand -base64 32)
export REGISTRYD_AUTH_PASSWORD_PEPPER="a-pepper-at-least-32-bytes-long-xx"
export REGISTRYD_SUPER_ADMINS=root@example.com
export REGISTRYD_PG_ADDR=localhost:55432 REGISTRYD_API_ADDR=localhost:8088 REGISTRYD_METRICS_ADDR=localhost:8089

make build
./bin/registryd-bootstrap -output /tmp/registryd.yml
./bin/registryd -cfg-file /tmp/registryd.yml &   # migrations auto-apply on boot

C=./bin/registryctl
$C superadmin bootstrap --email root@example.com --name Root --password password12345
$C user create --actor root@example.com --email eu-auditor@example.com --name "EU Auditor" --password password12345 --role auditor --regions EU
$C user create --actor root@example.com --email eu-mod@example.com --name "EU Mod" --password password12345 --role moderator --regions EU
$C signing-key generate --actor root@example.com --key-id reg-2026
$C framework create --actor eu-auditor@example.com --reference nist-800-171-r2 --name "NIST SP 800-171 Rev 2" --region EU --license public-domain
$C framework add-control --actor eu-auditor@example.com --framework nist-800-171-r2 --ref 3.1.1 --name "Limit system access"
$C framework submit  --actor eu-auditor@example.com --framework nist-800-171-r2
$C framework approve --actor eu-mod@example.com     --framework nist-800-171-r2   # auditor would be denied
$C framework publish --actor eu-mod@example.com     --framework nist-800-171-r2

TOKEN=$($C token issue --actor root@example.com --name grc-eu --regions EU | tail -1)
curl -H "Authorization: Bearer $TOKEN" localhost:8088/api/registry/v1/catalog
curl -H "Authorization: Bearer $TOKEN" localhost:8088/api/registry/v1/frameworks/nist-800-171-r2/versions/latest      # signed bundle
curl -H "Authorization: Bearer $TOKEN" localhost:8088/api/registry/v1/frameworks/nist-800-171-r2/versions/latest/seed # flat GRC seed
```

## Authoring console (browser)

Run the daemon with a session-cookie secret, then start the React console (its
Vite dev server proxies `/api` to the daemon, so the session cookie is
same-origin):

```sh
export REGISTRYD_AUTH_COOKIE_SECRET=$(openssl rand -base64 32)   # + the vars above
./bin/registryd -cfg-file /tmp/registryd.yml &

cd apps/registry && npm install && npm run dev   # http://localhost:5173
```

Sign in as a user created via `registryctl user create`. Auditors see authoring
actions; moderators/superadmins additionally see approve/publish. The UI is
role-gated and the server enforces RBAC, region scope and separation of duties.

## Consuming from a GRC instance

Pin the registry's ed25519 public key(s) and pull. Every bundle is verified
before its seed is trusted; unsigned/wrongly-keyed/tampered bundles are rejected.

```sh
# PUBKEY = base64 of the signing key's public half (distributed out of band)
./bin/registryctl pull --url http://localhost:8088 --token "$TOKEN" \
  --pubkey reg-2026:$PUBKEY --out-dir ./synced          # online sync -> verified seeds
./bin/registryctl verify --file ./synced/nist-800-171-r2.mzfw.json \
  --pubkey reg-2026:$PUBKEY                              # air-gapped import check
```

## Tests

```sh
make test-unit                              # pure Go: schema, signing, RBAC/region
make pg-up && make test REGISTRYD_TEST_PG_ADDR=localhost:55432   # + coredata & lifecycle e2e
```

The database-backed suite covers: register → assign roles → EU auditor authors &
submits (and is denied approve/publish, and denied cross-region) → EU moderator
approves (SoD enforced) & publishes (signed) → distribution round-trip where the
served seed is byte-identical to `Flatten()` → a tampered bundle is rejected →
tenant isolation and the copyright gate.

## Roadmap (deferred)

Refactor the console/connect API to gqlgen GraphQL (currently REST+JSON); expand
the console (invites, admin: users/roles/keys/tokens, versions timeline, download
bundle/seed buttons); wire the reference consumer (`pkg/distclient`) into the
actual `probo` GRC as a background sync worker + "Import framework bundle" screen
+ `framework_sources` migration (kept out of `meizon` by decision); OAuth2/SSO; AWS
Secrets Manager / SSM secret refs (only `file://` + inline today).

See `docs/FRAMEWORK-REGISTRY-SPEC.md` (in the sibling `meizon` repo) for the full
specification.
