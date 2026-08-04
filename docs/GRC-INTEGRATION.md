# GRC ↔ Registry integration guide

How a GRC (governance / risk / compliance) platform synchronises with the Meizon
Framework Registry and keeps every artifact current: **frameworks**,
**cross-mappings**, **audit (QA) templates**, and **translations**.

This is the complete integration reference. For the framework-only quick start
see [API-SYNC.md](API-SYNC.md); for running the registry see
[INSTALL-PRODUCTION.md](INSTALL-PRODUCTION.md).

---

## 0. What syncs where — read this first

There are **two** API families, and they are not interchangeable:

| Surface | Base path | Auth | Purpose |
|---|---|---|---|
| **Distribution API** | `/api/registry/v1` | Bearer token (`mzt_…`) | Machine-to-machine sync. Read-only, region-scoped, signed. |
| **Console API** | `/api/console/v1` (+ `/api/connect/v1`) | Session cookie (user sign-in) | Authoring UI + anything not yet distributed. |

What each artifact supports **today**:

| Artifact | On the distribution API? | Signed? | Translations over the wire? | How changes are announced |
|---|---|---|---|---|
| **Framework version** | ✅ `/frameworks/{id}/versions/{v}` (+ `/seed`) | ✅ ed25519 | ❌ canonical language only | `published` / `deprecated` change events |
| **Cross-mapping set** | ✅ `/mappings/{s}/{sv}/{t}/{tv}` | ✅ ed25519 | n/a (refs only) | `mapping_published` / `mapping_deprecated` change events |
| **Audit (QA) template** | ❌ **console-only** | ❌ | ✅ via console `?lang` | no distribution event — see §6 |
| **Translations** | ❌ **console-only** (`?lang` on console reads) | ❌ | — | translation is a console job — see §7 |

> **Be honest with your GRC roadmap:** frameworks and mappings are fully
> machine-syncable and cryptographically verifiable. **QA templates and
> translations are authoring-side features and are not exposed over the
> distribution API.** A GRC that needs them today must call the console API with
> user credentials (§6, §7). §9 covers what it would take to distribute them
> properly.

The contract in one line: **the registry publishes signed, immutable framework
versions and mapping sets; a consumer verifies every signature before importing,
and resumes from a cursor rather than re-downloading the world.**

---

## 1. Authentication

### Distribution API — bearer token

Every distribution request carries a token:

```
Authorization: Bearer mzt_<token>
```

- Tokens are plaintext-prefixed `mzt_`; the server stores only a SHA-256 digest and
  refuses revoked tokens. Each use stamps `last_used_at`.
- A token is **region-scoped**. An empty region set means *all regions*; a
  non-empty set restricts visibility — anything outside scope is `403`, never a
  filtered `404`, so "not yours" is not a discovery channel.
- **Two gates, both must pass** on every framework and mapping:

  | Gate | Rule |
  |---|---|
  | Copyright | A non-public (`proprietary`) framework is visible only to its owning tenant. `public-domain` / `statutory` are publicly distributable. |
  | Region | If the token has regions, the resource's region must be among them. |

- **Token issuance/revocation is not on the distribution API** — it is a console,
  superadmin-only operation:

  ```sh
  registryctl token issue --actor root@yourco.com --name "grc-eu" --regions EU
  # empty --regions ⇒ all regions
  ```

### Console API — session cookie

QA and translations (§6, §7) live behind the console API, which authenticates a
**user** via `POST /api/connect/v1/signin` (`{email, password}` → sets a session
cookie). There is no machine token for the console; a GRC that consumes QA or
translations must hold a service-account user login and send the cookie. This is
the main reason those two are flagged as "not machine-syncable yet."

---

## 2. The sync loop (frameworks + mappings)

```
  ┌─ cold start (cursor == 0) ───────────────────────────────────┐
  │  GET /catalog                → every framework you may see    │
  │    → per item: GET /frameworks/{id}/versions/latest → verify  │
  │  GET /mappings               → every mapping set you may see  │
  │    → per item: GET /mappings/{s}/{sv}/{t}/{tv}       → verify  │
  │  adopt headSeq (from GET /changes?since=0&limit=1) as cursor  │
  └──────────────────────────────────────────────────────────────┘
  ┌─ every run after ────────────────────────────────────────────┐
  │  GET /changes?since=<cursor> → what changed, in commit order  │
  │    dispatch on kind:                                          │
  │      published          → fetch + verify + import framework    │
  │      deprecated         → mark held framework retired          │
  │      mapping_published  → fetch + verify + import mapping set   │
  │      mapping_deprecated → retire mapping set                   │
  │      (unknown kind)     → ignore; the set will grow            │
  │  persist nextSeq ONLY after the whole page is processed        │
  └──────────────────────────────────────────────────────────────┘
```

**Read `headSeq` *after* walking the catalog** on cold start, so anything
published mid-walk is re-delivered by the feed rather than missed.

**Persist the cursor after importing, never before.** `nextSeq` advances only
over events actually returned, so a consumer that crashes mid-page re-processes
instead of skipping. Imports must therefore be **idempotent** — key on
`framework`+`version` (immutable once published) and, for mappings, on
`source@sourceVersion → target@targetVersion`.

The reference client (`registryctl pull`, §8) implements exactly this loop.

---

## 3. Frameworks

### 3.1 `GET /catalog` — discovery

Every framework whose latest version is `PUBLISHED` and the token may see.
Supports `HEAD`.

| Query | Meaning |
|---|---|
| `region` | Restrict to one region (intersected with the token scope). |
| `since` | RFC3339 timestamp. **Not a safe cursor** — use `/changes`. |

```json
[
  {
    "id": "pci-dss-4.0.1",
    "name": "PCI DSS",
    "region": "GLOBAL",
    "license": "public-domain",
    "latestVersion": "4.0.1",
    "contentHash": "sha256:9d4e2b8f…",
    "updatedAt": "2026-07-21T12:15:03Z"
  }
]
```

### 3.2 `GET /frameworks/{id}` — published versions

```json
{ "id":"pci-dss-4.0.1", "name":"PCI DSS", "region":"GLOBAL",
  "authority":"PCI SSC", "license":"public-domain",
  "versions":[ { "version":"4.0.1", "status":"PUBLISHED",
                 "contentHash":"sha256:…", "publishedAt":"…" } ] }
```

Only `PUBLISHED` versions appear; drafts are never visible.

### 3.3 `GET /frameworks/{id}/versions/{version}` — the signed bundle

The artifact you verify and archive. `version` accepts a concrete value or
`latest`. Supports `HEAD`.

- Sends `ETag: "<contentHash>"`. Pass it back as `If-None-Match` to get **`304 Not
  Modified`** and skip the transfer.
- Body is a `fwschema.Framework` (see §5.1) with a `signature` block.

```sh
curl -H "Authorization: Bearer $TOKEN" \
     -H 'If-None-Match: "sha256:9d4e2b8f…"' \
  https://framework.example.com/api/registry/v1/frameworks/pci-dss-4.0.1/versions/latest
```

**Bundle schema versions:** `schemaVersion` is `1.0` (flat `controls[]`), `2.0`
(`categories[].requirements[]` with inline `mappings[]` per requirement), or
`3.0` (same hierarchy but mappings stripped out into separate signed mapping
sets — see §4). **Branch on `schemaVersion`.**

### 3.4 `GET /frameworks/{id}/versions/{version}/seed` — flat import shape

The same content flattened to `{ id, name, controls:[{id,name,description}] }` —
convenient for a direct import once you have verified the bundle. **The seed drops
rich fields** (guidance, mappings, itemType, citations, tags…) and carries **no
signature**; verify the bundle and derive the seed from it, or accept that you are
trusting TLS + the operator.

### 3.5 How a framework CHANGE reaches you

Versions are **immutable once published** — content behind `framework`+`version`
never changes. A correction ships as a **new version** plus a `deprecated` event
for the old one. So "regenerate with change" on the registry side (re-generate a
framework from a revised document → review → publish a new version) surfaces to a
GRC as **change-feed events**, not in-place edits:

| Feed event `kind` | What happened | What the GRC does |
|---|---|---|
| `published` | A new (or first) framework version was published. | `GET …/versions/{version}` (conditional), verify, import as a **new version**; keep the old one resolving. |
| `deprecated` | A published version was retired. | Stop offering it to new users; keep existing imports resolving. Not a delete — the version stays fetchable. |

Because both ends are immutable, a version delta is computable client-side by
diffing the newly-fetched bundle against the previously-held one (the reference
client writes a `*.delta.json`).

---

## 4. Cross-mappings

Cross-mappings between frameworks (e.g. *PCI 7.2.1 ≡ ISO A.5.15*) are distributed
as **separate signed documents** (`meizon-mappingset/v1`), so a mapping can be
published or corrected without re-signing every framework that participates in it.

### 4.1 `GET /mappings` — mapping catalog

Every published mapping set the token may see (visibility follows the **source**
framework's region + copyright gate). No query params.

```json
[
  {
    "source": "pci-dss-4.0.1", "sourceVersion": "4.0.1",
    "target": "iso-27001-2022", "targetVersion": "2022",
    "requirementMappings": 143, "controlMappings": 0,
    "contentHash": "sha256:…", "publishedAt": "2026-07-21T12:16:00Z"
  }
]
```

### 4.2 `GET /mappings/{source}/{sourceVersion}/{target}/{targetVersion}`

Returns the **exact stored signed bytes, verbatim** (`Content-Type:
application/json`) — never re-derived — so you verify precisely what was signed.
`sourceVersion` accepts `latest`. Supports `HEAD`.

```json
{
  "$schema": "meizon-mappingset/v1",
  "source": { "framework": "pci-dss-4.0.1", "version": "4.0.1" },
  "target": { "framework": "iso-27001-2022", "version": "2022" },
  "requirementMappings": [
    { "sourceRef": "7.2.1", "targetRef": "A.5.15",
      "relation": "equivalent", "notes": "Both require least-privilege access." }
  ],
  "controlMappings": [],
  "approvedBy": "…", "publishedBy": "…", "publishedAt": "2026-07-21T12:16:00Z",
  "signature": { "alg":"ed25519", "keyId":"reg-2026",
                 "value":"base64…", "contentHash":"sha256:…" }
}
```

- Both ends are addressed by **code** (`sourceRef`/`targetRef`), resolved late on
  the consumer against the frameworks it holds.
- **`relation` ∈ `equivalent | partial | superset | subset`.**
- Signed exactly like a framework (detached ed25519 over canonical bytes); verify
  the same way (§5.2).

### 4.3 How a mapping CHANGE reaches you

Mapping sets get their **own** change-feed events, and those events carry the
target so you can sync incrementally without re-scanning frameworks:

| Feed event `kind` | Extra fields on the event | What the GRC does |
|---|---|---|
| `mapping_published` | `targetFramework`, `targetVersion` | `GET /mappings/{source}/{sourceVersion}/{targetFramework}/{targetVersion}`, verify, import. |
| `mapping_deprecated` | `targetFramework`, `targetVersion` | Retire the held mapping set for that pair. |

```json
{
  "seq": 44, "kind": "mapping_published",
  "framework": "pci-dss-4.0.1", "version": "4.0.1",
  "targetFramework": "iso-27001-2022", "targetVersion": "2022",
  "region": "GLOBAL", "occurredAt": "2026-07-21T12:16:00Z"
}
```

> A mapping set whose target framework you do not hold yet is still valid — store
> it and let it dangle until you import that target, then resolve the codes.

---

## 5. Verifying signatures

Every framework bundle and every mapping set is signed **detached ed25519 over the
canonicalised content** — the document with the `signature` block removed.
`contentHash` is a sha256 digest of those same canonical bytes, so a verifier can
detect tampering independently of the signature.

### 5.1 The verify steps

1. Remove the `signature` block; canonicalise the remaining JSON.
2. Recompute sha256; it must equal `signature.contentHash` (else *content-hash
   mismatch*).
3. ed25519-verify `signature.value` against the **pinned** public key identified by
   `signature.keyId` (else *unknown key* / *bad signature*).

### 5.2 Pin the key out of band

Publishing a key next to the content it signs authenticates nothing. Distribute
`keyId:base64` to consumers **separately** and pin it:

```sh
registryctl pull \
  --url https://framework.example.com \
  --token "$GRC_TOKEN" \
  --pubkey reg-2026:<base64-ed25519-public-key> \
  --region GLOBAL \
  --out-dir /var/lib/grc/frameworks
```

A bundle or mapping set that is unsigned, signed by an unpinned key, or altered in
transit is **rejected and never imported** (the run continues for other artifacts
and exits non-zero). Air-gapped verification of a hand-carried file:

```sh
registryctl verify --file pci-dss-4.0.1.mzfw.json --pubkey reg-2026:<pubkey>
```

---

## 6. Audit (QA) templates — `meizon-qa-template/v1`

An **audit template** is an ordered, per-requirement questionnaire an auditor
answers to establish compliance (question types, branching follow-ups, declarative
scoring). It is generated from a framework's requirements and lives beside them.

> **Distribution status: console-only.** QA templates are **not** exposed on the
> `/api/registry/v1` distribution API — there is no bearer-token endpoint and they
> are not part of the signed bundle or seed. A GRC consumes them today through the
> **console API** with user credentials.

### 6.1 Reading a template (console API)

```
GET /api/console/v1/frameworks/{ref}/qa-template?lang=<code>
Cookie: <session>            # from POST /api/connect/v1/signin
```

- `?lang` selects a translation; omit it (or pass the source language) for the
  canonical template. An untranslated language **falls back to canonical**.
- Response is a `meizon-qa-template/v1` document plus `templateId`, `status`
  (`draft` | `ready`), and `language`:

```jsonc
{
  "templateId": "…", "status": "ready", "language": "",
  "$schema": "meizon-qa-template/v1",
  "id": "pci-dss-4.0.1-audit",
  "framework": { "id": "pci-dss-4.0.1", "version": "4.0.1", "language": "en" },
  "scale": { "kind": "maturity", "levels": [ … ] },
  "verdictModel": { "verdicts": ["compliant","partial","non_compliant","not_applicable"], … },
  "sections": [
    { "ref": "req-7", "name": "Restrict Access", "order": 1,
      "questions": [
        { "id": "…", "requirementRef": "7.2.1", "text": "Is an access-control model defined?",
          "intent": "Confirm least-privilege is documented and enforced.",
          "type": "yes_no_evidence", "weight": 3,
          "assessment": { "rules": [ { "when": "answer == 'yes'", "verdict": "compliant" } ] },
          "followUps": [ { "when": "answer == 'no'", "askId": "…" } ] }
      ] }
  ]
}
```

**What is stable vs language-specific:** `id`, `requirementRef`, `type`, `verdict`
values, `when` expressions and choice option **values** are machine-meaningful and
identical across languages. `text`, `intent`, section names, option **labels**,
criteria, etc. are translated. Question `id`s are **per-language** (each language
copy has its own row ids) — bind questions to requirements by `requirementRef`,
not by `id`, when cross-referencing languages.

### 6.2 How a QA CHANGE happens

There is **no change-feed event** for QA templates. A template changes when:

| Trigger | Effect |
|---|---|
| Framework generated / imported (LLM configured) | Template auto-generated for the new draft. |
| Requirement added manually | Its questions are generated and merged in. |
| Author edits / regenerates / marks `ready` | Template content / status changes. |
| Framework translated into language *L* (§7) | A language-*L* copy of the template is produced. |

A GRC that mirrors QA should **re-poll** `GET …/qa-template` (per framework, per
language) on its own cadence, keyed on the framework version it holds; there is no
incremental cursor for QA. Because a template is tied to a framework **version**,
a new published version means re-fetching its template.

### 6.3 Scoring

The registry exposes a single evaluator so a GRC can score consistently:
`POST /api/console/v1/frameworks/{ref}/qa/evaluate` with `{questionId, answer}`
returns the verdict, score, and which follow-ups fired. (Also console-only.)

---

## 7. Translations

English (or the framework's authored language) is **canonical**: the structure —
refs, control links, cross-mappings — lives once, and every other language is a
**text overlay keyed by ref**. Translating never changes what a framework *is*,
only the display text (names, descriptions, and — since the audit work — QA
question text).

> **Distribution status: console-only.** The signed bundle and the seed are
> **canonical-language only** — there is no `lang` parameter anywhere on
> `/api/registry/v1`, and translations are never embedded. A GRC that needs
> localized text consumes it through the console API.

### 7.1 Reading in a language (console API)

| Read | Endpoint | Language |
|---|---|---|
| Flat framework export | `GET /api/console/v1/frameworks/{ref}/export?lang=<code>` | overlay applied; absent ⇒ canonical |
| Structure (categories → requirements) | `GET …/frameworks/{ref}/structure?lang=<code>` | same |
| Audit template | `GET …/frameworks/{ref}/qa-template?lang=<code>` | §6 |
| Available languages | `GET …/frameworks/{ref}/translations` | lists language codes + node counts |

An untranslated language falls back to canonical text per node, so a partial
translation is safe to request.

### 7.2 How a translation CHANGE happens

Translation is a **background job on the console**, not a distributed artifact:

```
POST /api/console/v1/frameworks/{ref}/translations   { "language": "fr" }
  → { "jobId": "…" }        # poll GET …/frameworks/generate/status/{jobId}
```

- The job translates the framework's names/descriptions **and** — since the audit
  work — its QA audit template into the same language (§6). Only human text is
  translated; refs, relations, verdicts and `when` expressions are preserved.
- A GRC mirroring translations re-fetches `export?lang=` / `qa-template?lang=`
  after a translation job completes (or on its own cadence). There is no
  change-feed event for translations.

---

## 8. Reference client

`registryctl` ships the intended consumer; `pkg/distclient` is the library behind
it.

- **`registryctl pull`** — cold-start + incremental sync into a directory. Flags:
  `--url`, `--token`, `--region`, `--out-dir`, `--pubkey keyId:base64` (repeatable,
  at least one required). Per framework it writes `<id>.mzfw.json` (verified
  bundle), `<id>.seed.json` (flattened), and `<id>.delta.json` (version diff);
  per mapping it writes `<source>__<target>.mzmap.json`. State lives in
  `.registry-sync.json` (cursor + held content hashes).
- **`registryctl verify`** — offline air-gapped check of a local bundle.

The library loop: **catalog → changes (cursor) → conditional GET bundle → verify
ed25519 → flatten to seed → import**, with content-hash short-circuiting and
crash-safe cursor persistence (cursor saved only after a page is fully processed).

---

## 9. Change model at a glance

| Artifact | Detect a change | Fetch | Verify | Apply |
|---|---|---|---|---|
| Framework | `/changes` → `published` / `deprecated` | `/frameworks/{id}/versions/{v}` (conditional, ETag) | ed25519 + contentHash | import as new immutable version / retire old |
| Mapping set | `/changes` → `mapping_published` / `mapping_deprecated` (carries target) | `/mappings/{s}/{sv}/{t}/{tv}` | ed25519 + contentHash | import / retire by pair |
| QA template | **poll** `qa-template?lang` per held version (no event) | console API | — (unsigned) | replace held template for that version/language |
| Translation | **poll** `export?lang` / `qa-template?lang` after a translate job (no event) | console API | — (unsigned) | replace localized text |

---

## 10. Errors, limits, operational notes (distribution API)

| Status | Body | When |
|---|---|---|
| `400` | `{"error":"invalid since cursor…"}` | Malformed `since` / `limit` / `region` / timestamp. |
| `401` | `unauthorized` (**text/plain**) | Missing/unknown bearer token. Sends `WWW-Authenticate: Bearer`. |
| `403` | `{"error":"not distributable"}` | Outside region scope, non-public framework of another tenant, or version not `PUBLISHED`. |
| `404` | `{"error":"not found"}` | No such framework / version / mapping. |
| `429` | `rate limit exceeded` (**text/plain**) | Token bucket exceeded. Sends `Retry-After`. |
| `500` | `{"error":"…"}` | Server-side failure. |

> `401` and `429` come from middleware and are `text/plain`; every other error is
> JSON `{"error":…}`. Don't assume a JSON body on failure — check the status code.

- **Rate limits** default to 600 req/min, burst 60, per token or IP. Honour
  `Retry-After`.
- **Poll interval:** the change feed is cheap and cursor-based — every ~15 minutes
  is ample for a standards registry.
- **Downloads are recorded.** Every bundle/seed/mapping fetch writes an audit row
  (licence reporting).
- **`deprecated` ≠ deleted.** A deprecated version stays fetchable so existing
  imports keep resolving.

---

## 11. Gaps & roadmap (be explicit with integrators)

Today, a GRC gets **frameworks and cross-mappings** fully machine-to-machine,
signed and cursor-synced. **Audit (QA) templates and translations are
authoring-side and reachable only through the console API with user
credentials.** To make them first-class distribution artifacts would mean:

- **QA over distribution:** add `GET /api/registry/v1/frameworks/{id}/versions/{v}/qa[?lang]`
  (bearer-authed, region/copyright-gated), and emit `qa_published` change events
  when a template is marked `ready`. Sign the template like a bundle so a GRC can
  verify it.
- **Translations over distribution:** either add `?lang` to the bundle/seed
  endpoints (applying the overlay server-side, canonical fallback) or ship a
  per-language overlay artifact + `translation_published` events. Decide whether a
  translation is signed (it changes display text but not structure/refs).

Until then, mirror QA and translations by polling the console API per held
framework version, and treat frameworks + mappings as the source of truth for
anything scored or cross-referenced.
