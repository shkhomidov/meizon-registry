# Meizon Framework Registry — Production Install

> For getting a build from GitHub onto a server — CI, image publishing to GHCR,
> release tags and rollback — see [DEPLOY-GITHUB.md](DEPLOY-GITHUB.md). This
> document covers the server itself.

`registryd` is a Go service backed by PostgreSQL. Minimum footprint:
**registryd + PostgreSQL 15 + an HTTPS reverse proxy**. Migrations auto-apply on
boot; there is no separate migrate step.

The **web console is embedded in the binary** — the Docker build compiles the
React app and serves it from `/`, with the API under `/api`. One container, one
origin, so there is no CORS to configure and no second web server to run.

| Path | Serves |
|---|---|
| `/` | the console (SPA, deep links fall back to `index.html`) |
| `/api/console/v1`, `/api/connect/v1` | console API (session cookie) |
| `/api/registry/v1` | distribution API (bearer token) |
| `/api/admin/v1` | read-only audit surface (admin token) |
| `/healthz` | liveness probe |

State is **not** limited to frameworks any more: uploaded source documents are
stored in PostgreSQL as well (see §6), so size the volume accordingly.

## 1. Secrets

Generate and store these in your secret manager. They must be identical across
every `registryd` replica and stable over the life of the deployment.

| Variable | How to generate | Notes |
|---|---|---|
| `REGISTRYD_ENCRYPTION_KEY` | `openssl rand -base64 32` | base64 of exactly 32 bytes. Encrypts signing-key private material. **Never rotate without a migration plan.** |
| `REGISTRYD_AUTH_PASSWORD_PEPPER` | `openssl rand -base64 48` | ≥ 32 bytes. Server-side password pepper. |
| `REGISTRYD_PG_PASSWORD` | your DB password | |
| `REGISTRYD_ADMIN_TOKEN` | `openssl rand -hex 32` | optional; bearer for the read-only `/api/admin/v1` audit surface. Empty disables it. |
| `REGISTRYD_AUTH_COOKIE_SECRET` | `openssl rand -base64 48` | signs console session cookies. **Empty disables the console and its API entirely** — the distribution API still works. |

Any value may be given as `file:///run/secrets/name` to read it from a mounted
file instead of an inline env var.

## 2. Governance allowlist

`REGISTRYD_SUPER_ADMINS` is a comma-separated list of emails permitted to hold
the superadmin role. Bootstrapping any other superadmin is refused. Set it before
first boot.

## 3. Deploy

### Docker Compose

```sh
export REGISTRYD_ENCRYPTION_KEY=$(openssl rand -base64 32)
export REGISTRYD_AUTH_PASSWORD_PEPPER=$(openssl rand -base64 48)
export REGISTRYD_SUPER_ADMINS=root@yourco.com
export REGISTRYD_ADMIN_TOKEN=$(openssl rand -hex 32)
export REGISTRYD_PG_PASSWORD=$(openssl rand -hex 16)

docker compose -f compose.prod.yaml up -d --build
```

The `bootstrap` one-shot service renders the config onto a shared volume; the
`registryd` service then loads it and runs migrations. Put a TLS-terminating
reverse proxy in front of `:8080` and forward `X-Forwarded-Proto`.

### Kubernetes (Helm)

```sh
helm install registry ./helm/registryd \
  --set-string secrets.encryptionKey="$(openssl rand -base64 32)" \
  --set-string secrets.passwordPepper="$(openssl rand -base64 48)" \
  --set-string secrets.pgPassword="$(openssl rand -hex 16)" \
  --set-string config.superAdmins="root@yourco.com" \
  --set-string config.baseURL="https://registry.yourco.com" \
  --set-string postgres.addr="my-postgres:5432"
```

An init container runs `registryd-bootstrap`; the main container runs `registryd`.
Provide PostgreSQL separately (managed service or an in-cluster operator) and
point `postgres.addr` at it. Probes target `/healthz`.

### Reverse proxy

Terminate TLS in front of `:8080` and forward `X-Forwarded-Proto`. Everything —
console and API — is one upstream, so no path-based split is needed. Two things
worth setting:

- **Upload body limit ≥ 32 MB.** Scanned standards are large, and the default
  1 MB in most proxies rejects them with an opaque 413.
- **Proxy read timeout ≥ 20 minutes** on `/api/console/v1/frameworks/generate`
  and the other job-starting routes. They return immediately with a job id, but
  OCR of a 100-page scan runs for minutes inside the request that starts it.

## 4. AI features (optional, configured in the UI)

Document generation, translation and cross-mapping need an LLM; scanned PDFs
additionally need OCR. Both keys are entered by a superadmin in
**Settings**, stored AES-256-GCM encrypted with `REGISTRYD_ENCRYPTION_KEY`, and
never returned by the API. They are deliberately not environment variables —
they are tenant configuration, changed without a redeploy.

- **LLM provider** — Gemini / Anthropic / OpenAI. Without it the registry still
  works for hand-authored and imported frameworks.
- **OCR (Mistral)** — a strict fallback used only for pages with no text layer.
  Scanned documents are uploaded to Mistral to be read and deleted afterwards;
  do not configure it if documents may not leave your infrastructure.

Egress: `generativelanguage.googleapis.com` (or your chosen provider) and
`api.mistral.ai`.

## 5. First-run governance

Using `registryctl` (shipped in the image; point it at the DB or run it as a Job):

```sh
registryctl superadmin bootstrap --email root@yourco.com --name "Root" --password '...'
registryctl signing-key generate --actor root@yourco.com --key-id reg-2026
registryctl user create --actor root@yourco.com --email author@yourco.com \
  --name "EU Auditor" --password '...' --role auditor --regions EU
registryctl token issue --actor root@yourco.com --name "grc-eu" --regions EU
```

## 6. Consuming instances

A GRC instance pulls with the reference consumer, pinning the registry's public
key(s):

```sh
# Publish the public half of each signing key to consumers out of band.
registryctl pull --url https://registry.yourco.com --token "$GRC_TOKEN" \
  --pubkey reg-2026:<base64-ed25519-public-key> --out-dir /var/lib/grc/frameworks
```

Every downloaded bundle is ed25519-verified before its flattened seed is written;
an unsigned, wrongly-keyed or tampered bundle is rejected and never imported. The
same verification backs the air-gapped path — hand-carry a `.mzfw.json` and run
`registryctl verify --file bundle.mzfw.json --pubkey reg-2026:<pubkey>`.

## 7. Operations

- **Metrics**: Prometheus at `:8081` (`REGISTRYD_METRICS_ADDR`).
- **Rate limiting**: token-bucket per bearer token / IP, `429 + Retry-After`.
- **Audit log**: immutable; read via `registryctl audit` or `GET /api/admin/v1/audit`.
- **Backups**: back up PostgreSQL. All state lives there — frameworks, versions,
  signing keys, tokens, audit, translations, cross-mappings, background jobs and
  the uploaded **source documents**.
- **Sizing**: source documents are stored as rows, so the database grows by
  roughly the size of every PDF ingested (a scanned standard is a few MB).
  Unaccepted uploads are swept after 24 hours.
- **Interrupted jobs**: a run whose process dies is marked failed on next boot,
  so nothing shows as permanently "running". Long runs are safe to interrupt —
  they are re-runnable, and results already stored are kept.
