# Sync API — distributing frameworks to client platforms

How a GRC platform (or any consumer) pulls frameworks out of the Meizon
Framework Registry and keeps them current. For running the registry itself see
[INSTALL-PRODUCTION.md](INSTALL-PRODUCTION.md).

> For the **complete** integration guide — cross-mappings, audit (QA) templates,
> translations, and the keep-current model for each — see
> [GRC-INTEGRATION.md](GRC-INTEGRATION.md). This page is the framework-only quick
> start.

Base path: **`/api/registry/v1`**. Read-only, bearer-token authenticated,
region-scoped, ETag-aware. Served by the same origin and port as the console.

The contract in one line: **the registry publishes signed, immutable framework
versions; a consumer verifies every signature before importing, and resumes from
a cursor rather than re-downloading the world.**

## 1. Authentication

Every request carries a distribution token:

```
Authorization: Bearer <token>
```

Tokens are issued by a superadmin and scoped to zero or more regions:

```sh
registryctl token issue --actor root@yourco.com --name "grc-eu" --regions EU
```

An **empty region list means all regions**. A non-empty list restricts the token
to those regions, and anything outside the scope is invisible — a framework in a
region you cannot see returns `403`, not a filtered-out `404`, when addressed
directly.

Two separate visibility rules apply, and both must pass:

| Gate | Rule |
|---|---|
| Copyright | A framework marked non-public is visible only to the owning tenant. |
| Region | If the token has regions, the framework's region must be among them. |

## 2. The sync loop

The intended shape of a client integration:

```
  ┌─ first run ──────────────────────────────────────────┐
  │  GET /catalog                → what exists           │
  │  GET /frameworks/{id}/versions/{v}/seed  (per item)  │
  │  persist headSeq as your cursor                      │
  └──────────────────────────────────────────────────────┘
  ┌─ every run after ────────────────────────────────────┐
  │  GET /changes?since=<cursor> → what changed          │
  │  fetch + verify each event's version                 │
  │  persist nextSeq ONLY after a successful import      │
  └──────────────────────────────────────────────────────┘
```

**Persist the cursor after importing, never before.** `nextSeq` advances only
over events actually returned, so a consumer that crashes mid-import re-delivers
the event on the next run instead of skipping it. Imports must therefore be
idempotent — key on `framework` + `version`, both immutable once published.

## 3. Endpoints

### `GET /catalog`

Every published framework the token may see. Supports `HEAD`.

| Query | Meaning |
|---|---|
| `region` | Restrict to one region. Intersected with the token's scope. |
| `since` | RFC3339 timestamp. Superseded by `/changes` — see the note below. |

```sh
curl -H "Authorization: Bearer $TOKEN" \
  https://framework.meizon.io/api/registry/v1/catalog?region=RU
```

```json
[
  {
    "id": "gost-r-iso-iec-27006-2008",
    "name": "ГОСТ Р ИСО/МЭК 27006—2008",
    "region": "RU",
    "license": "public-domain",
    "latestVersion": "2008",
    "contentHash": "sha256:3f9a1c7e5b2d8a04f16c9b73e0d5a8127c4f6e39b0a2d7c85e13f4b96a0c8d72",
    "updatedAt": "2026-07-21T11:02:44Z"
  }
]
```

> **`?since=` on the catalog is not a safe cursor.** It cannot represent a
> retirement, and an event committed at T but visible at T+1 is missed by a
> consumer that already advanced past T. Use `/changes`.

### `GET /changes`

The cursor-based feed: *what happened after seq N that I am allowed to see*.
This is the endpoint a recurring sync should be built on.

| Query | Default | Meaning |
|---|---|---|
| `since` | `0` | Resume after this sequence number. |
| `limit` | `100` | Page size. Values above `500` are clamped to `500`. |

```sh
curl -H "Authorization: Bearer $TOKEN" \
  "https://framework.meizon.io/api/registry/v1/changes?since=41&limit=100"
```

```json
{
  "events": [
    {
      "seq": 42,
      "kind": "published",
      "framework": "pci-dss-4.0.1",
      "version": "4.0.1",
      "contentHash": "sha256:9d4e2b8f1a7c3056e9b4d1f8a2c67e05b3f9d8a41c62e7b05d9f31a8c47e260b",
      "region": "GLOBAL",
      "occurredAt": "2026-07-21T12:15:03Z"
    },
    {
      "seq": 43,
      "kind": "deprecated",
      "framework": "pci-dss-4.0",
      "version": "4.0",
      "region": "GLOBAL",
      "occurredAt": "2026-07-21T12:15:04Z"
    }
  ],
  "nextSeq": 43,
  "headSeq": 43,
  "hasMore": false
}
```

`headSeq` is the newest sequence visible to the token, so a consumer can report
how far behind it is (`headSeq - nextSeq`) without draining the feed.
Drain by looping while `hasMore` is true, passing the previous `nextSeq`.

**Event kinds.** `published`, `deprecated`, `mapping_published`,
`mapping_deprecated`, `qa_published`, `translation_published`. Treat unknown
kinds as ignorable rather than an error — the set will grow. The last two (audit
templates and translations) are covered in
[GRC-INTEGRATION.md](GRC-INTEGRATION.md#6-audit-qa-templates--meizon-qa-templatev1).

**Mapping events carry their target.** A `mapping_published` /
`mapping_deprecated` event serialises `targetFramework` and `targetVersion` (in
addition to the source in `framework`/`version`), so incremental mapping sync is
built straight off this feed — no framework re-scan needed. See
[GRC-INTEGRATION.md §4.3](GRC-INTEGRATION.md#43-how-a-mapping-change-reaches-you).

### `GET /frameworks/{id}`

Published versions of one framework.

```json
{
  "id": "pci-dss-4.0.1",
  "name": "PCI DSS",
  "region": "GLOBAL",
  "authority": "PCI Security Standards Council",
  "license": "public-domain",
  "versions": [
    {
      "version": "4.0.1",
      "status": "PUBLISHED",
      "contentHash": "sha256:9d4e2b8f1a7c3056e9b4d1f8a2c67e05b3f9d8a41c62e7b05d9f31a8c47e260b",
      "publishedAt": "2026-07-21T12:15:03Z"
    }
  ]
}
```

Unpublished versions are omitted entirely — a draft is never visible here.

### `GET /frameworks/{id}/versions/{version}`

The **signed bundle**: full content plus the signature block. This is what you
verify and archive. `version` accepts `latest`. Supports `HEAD`.

Sends `ETag: "<contentHash>"`; pass it back as `If-None-Match` to get `304 Not
Modified` and skip the transfer.

```sh
curl -H "Authorization: Bearer $TOKEN" \
     -H 'If-None-Match: "sha256:9d4e2b8f..."' \
  https://framework.meizon.io/api/registry/v1/frameworks/pci-dss-4.0.1/versions/latest
```

```json
{
  "schemaVersion": "2.0",
  "id": "pci-dss-4.0.1",
  "name": "PCI DSS",
  "shortName": "PCI DSS",
  "version": "4.0.1",
  "status": "PUBLISHED",
  "region": "GLOBAL",
  "authority": "PCI Security Standards Council",
  "license": "public-domain",
  "description": "Payment Card Industry Data Security Standard.",
  "publishedAt": "2026-07-21T12:15:03Z",
  "categories": [
    {
      "code": "req-7",
      "name": "Restrict Access by Need to Know",
      "requirements": [
        {
          "code": "7.2.1",
          "number": "7.2.1",
          "title": "An access control model is defined",
          "description": "An access control model is defined and includes granting access as follows: appropriate access depending on the entity's business and access needs.",
          "itemType": "requirement",
          "validationApproaches": ["interview", "document-review"],
          "guidance": "Review the access control model against documented job functions.",
          "mappings": [
            {
              "relation": "equivalent",
              "framework": "iso-27001-2022",
              "version": "2022",
              "item": "A.5.15",
              "notes": "Both require least-privilege access control."
            }
          ]
        }
      ]
    }
  ],
  "signature": {
    "alg": "ed25519",
    "keyId": "reg-2026",
    "value": "MEUCIQDf8s...base64-detached-signature...==",
    "contentHash": "sha256:9d4e2b8f1a7c3056e9b4d1f8a2c67e05b3f9d8a41c62e7b05d9f31a8c47e260b"
  }
}
```

Schema v1 bundles use a flat `controls[]` array instead of
`categories[].requirements[]`. Consumers should branch on `schemaVersion`.

### `GET /frameworks/{id}/versions/{version}/seed`

The same content **flattened for direct import** — no signature, no nesting.
Convenient when the consumer has already verified the bundle, or trusts the
transport.

```json
{
  "id": "pci-dss-4.0.1",
  "name": "PCI DSS",
  "controls": [
    {
      "id": "7.2.1",
      "name": "An access control model is defined",
      "description": "An access control model is defined and includes granting access as follows: appropriate access depending on the entity's business and access needs."
    }
  ]
}
```

> Fetching the seed alone skips signature verification. Either verify the bundle
> and derive the seed from it, or accept that you are trusting TLS and the
> registry operator. `registryctl pull` does the former.

## 4. Verifying signatures

Every bundle is signed detached-ed25519 over the canonicalised content — the
document with the `signature` block removed. `contentHash` is a sha256 digest of
those same canonical bytes, letting a verifier detect tampering independently of
the signature check.

**Pin the public key out of band.** Publishing a key alongside the content it
signs authenticates nothing. Distribute `keyId:base64` to consumers separately,
and pin it:

```sh
registryctl pull \
  --url https://framework.meizon.io \
  --token "$GRC_TOKEN" \
  --pubkey reg-2026:<base64-ed25519-public-key> \
  --region GLOBAL \
  --out-dir /var/lib/grc/frameworks
```

```
verified pci-dss-4.0.1            4.0.1    controls=264 -> /var/lib/grc/frameworks/pci-dss-4.0.1.seed.json
verified gost-r-iso-iec-27006-2008 2008    controls=41  -> /var/lib/grc/frameworks/gost-r-iso-iec-27006-2008.seed.json
synced 2, rejected 0
```

A bundle that is unsigned, signed by an unpinned key, or altered in transit is
**rejected and never imported**. A rejection does not abort the run — other
frameworks still sync, failures are reported on stderr, and the command exits
non-zero:

```
REJECTED pci-dss-4.0.1: signature verification failed: unknown key id "reg-2025"
synced 1, rejected 1
```

At least one `--pubkey` is required; the command refuses to run without one
rather than silently importing unverified content.

**Air-gapped:** hand-carry the `.mzfw.json` bundle and verify offline:

```sh
registryctl verify --file pci-dss-4.0.1.mzfw.json --pubkey reg-2026:<pubkey>
```

## 5. Errors

| Status | Body | When |
|---|---|---|
| `400` | `{"error":"invalid since cursor; expected a non-negative integer sequence number"}` | Malformed `since` or `limit`. |
| `401` | `unauthorized` (**`text/plain`**) | Missing, malformed or unknown bearer token. Sends `WWW-Authenticate: Bearer`. |
| `403` | `{"error":"not distributable"}` | Outside the token's region scope, non-public framework owned by another tenant, or the version is not `PUBLISHED`. |
| `404` | `{"error":"not found"}` | No such framework or version. |
| `429` | `rate limit exceeded` | Token bucket exceeded. Sends `Retry-After`. |
| `500` | `{"error":"cannot load catalog"}` | Server-side failure. |

> **Inconsistency to code around:** `401` and `429` come from middleware and are
> `text/plain`; every other error is JSON `{"error": "..."}`. Do not assume a
> JSON body on failure — check `Content-Type`, or key off the status code.

**Rate limits** default to 600 requests/minute with a burst of 60, per token or
IP (`REGISTRYD_API_RATE_LIMIT_RPM` / `_BURST`). Honour `Retry-After`.

`403` versus `404` is deliberate: addressing a framework you may not see returns
`403`, so the distinction between "does not exist" and "not yours" is not a
discovery channel.

## 6. Operational notes

- **Versions are immutable once published.** `framework` + `version` is a safe
  idempotency key; content behind it never changes. A correction ships as a new
  version plus a `deprecated` event for the old one.
- **Downloads are recorded.** Every bundle and seed fetch writes an audit row —
  useful for licence reporting, and visible to the registry operator.
- **`deprecated` is not `deleted`.** A deprecated version stays fetchable so an
  existing import keeps resolving; treat the event as "stop offering this to new
  users", not "purge it".
- **Poll interval:** the feed is cheap and cursor-based. Every 15 minutes is
  ample for a standards registry; every few seconds wastes your rate budget.

## 7. Current state of this deployment

As of 2026-07-21 on `framework.meizon.io`, **nothing is distributable yet**:

| | |
|---|---|
| Frameworks | 4 (`pci-dss-4.0.1`, `pci-dss-4.0`, two GOST) |
| Published versions | **0** — all are `DRAFT` or `IN_REVIEW` |
| Distribution events | 0 |
| Distribution tokens | 0 |

So `/catalog` returns `[]` and `/changes` returns an empty feed with
`headSeq: 0` for any token. Before a client platform can sync, a superadmin must
generate a signing key, publish at least one version, and issue a token:

```sh
registryctl signing-key generate --actor root@yourco.com --key-id reg-2026
registryctl token issue --actor root@yourco.com --name "grc-eu" --regions EU
```

The response examples above are therefore illustrative of a published
framework — the ids, names, regions and licences are real; the hashes,
signatures and requirement bodies are representative.
