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

| Artifact | On the distribution API? | Signed? | Localized (`?lang`)? | How changes are announced |
|---|---|---|---|---|
| **Framework version** | ✅ `/frameworks/{id}/versions/{v}` (+ `/seed`) | ✅ ed25519 (bundle) | ✅ `seed?lang` | `published` / `deprecated` events |
| **Cross-mapping set** | ✅ `/mappings/{s}/{sv}/{t}/{tv}` | ✅ ed25519 | n/a (refs only) | `mapping_published` / `mapping_deprecated` events |
| **Audit (QA) template** | ✅ `/frameworks/{id}/versions/{v}/qa` | ❌ (unsigned) | ✅ `qa?lang` | `qa_published` events |
| **Translations** | ✅ `seed?lang` + `qa?lang` (+ languages discovery) | ❌ overlay | ✅ | `translation_published` events |

All four are now machine-syncable over the bearer-token distribution API. The
**signed** artifacts are the framework bundle and the mapping set (verify those
for integrity); the **seed**, **QA template**, and **translation overlay** are
derived/unsigned — a consumer that has verified the canonical bundle can trust
the derived shapes, otherwise it is trusting TLS + the operator (same posture as
the seed has always had).

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
  │      published             → fetch+verify+import framework      │
  │      deprecated            → mark held framework retired        │
  │      mapping_published     → fetch+verify+import mapping set    │
  │      mapping_deprecated    → retire mapping set                 │
  │      qa_published          → fetch qa[?lang] for that version   │
  │      translation_published → re-fetch languages + seed?lang     │
  │      (unknown kind)        → ignore; the set will grow          │
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

### 3.4 `GET /frameworks/{id}/versions/{version}/seed[?lang=<code>]` — flat import shape

The same content flattened to `{ id, name, controls:[{id,name,description}] }` —
convenient for a direct import once you have verified the bundle. **The seed drops
rich fields** (guidance, mappings, itemType, citations, tags…) and carries **no
signature**; verify the bundle and derive the seed from it, or accept that you are
trusting TLS + the operator.

**`?lang=<code>`** applies the stored translation overlay to the seed's
names/descriptions (canonical fallback per node). This is how a GRC imports
localized framework text — see §7. The signed **bundle** is always canonical
(translating it would break its signature), so verify the bundle for integrity
and take localized display text from `seed?lang`.

Discovery: `GET /frameworks/{id}` lists each version's available `languages`
(source + every translation), so you know which `?lang` values exist.

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

> **Distribution status: available.** A published version's audit template is
> served over the bearer-token distribution API, region/copyright-gated like the
> bundle. Only a template marked **`ready`** is distributed — a draft is
> authoring-in-progress and returns `404`.

### 6.1 `GET /frameworks/{id}/versions/{version}/qa[?lang=<code>]`

```
GET /api/registry/v1/frameworks/pci-dss-4.0.1/versions/latest/qa?lang=fr
Authorization: Bearer mzt_<token>
```

- `?lang` selects a translation; omit it (or pass the source language) for the
  canonical template. An untranslated language **falls back to canonical**.
- `404 {"error":"not found"}` if the version has no template marked `ready`;
  `403 {"error":"not distributable"}` outside the token's region/copyright scope
  or if the version is not `PUBLISHED`.
- The template is **unsigned** (derived from live rows). Consumers that require
  provenance should treat it like the seed — trust it because the framework
  bundle it belongs to verified.
- Response is a `meizon-qa-template/v1` document (the console read adds
  `templateId`/`status` fields; the distribution read returns the plain schema):

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

### 6.2 How a QA CHANGE reaches you

The change feed emits **`qa_published`** (framework + version) when a published
version's template becomes distributable — either the template is marked `ready`
on an already-published version, or a version is published while its template is
already `ready`. On that event, fetch `…/versions/{version}/qa[?lang]`.

| Feed event `kind` | What happened | What the GRC does |
|---|---|---|
| `qa_published` | A ready audit template is available for this published version. | `GET …/versions/{version}/qa[?lang]`, import. |

Authoring-side edits before publish (auto-generation on framework create,
merge-on-requirement-add, regeneration) do **not** emit events — they are
invisible until the version is published and the template is `ready`. Because a
template is tied to a framework **version**, a new published version means
re-fetching its template.

### 6.3 Scoring

The registry exposes a single evaluator so a GRC can score consistently:
`POST /api/console/v1/frameworks/{ref}/qa/evaluate` with `{questionId, answer}`
returns the verdict, score, and which follow-ups fired. (This scoring helper is
still console-side; the template itself is distributed.)

---

## 7. Translations

English (or the framework's authored language) is **canonical**: the structure —
refs, control links, cross-mappings — lives once, and every other language is a
**text overlay keyed by ref**. Translating never changes what a framework *is*,
only the display text (names, descriptions, and — since the audit work — QA
question text).

> **Distribution status: available (as overlays).** Localized text is served over
> the distribution API by applying the overlay to the **seed** and **QA template**
> via `?lang`. The signed **bundle** stays canonical (translating it would break
> the signature): verify the bundle for integrity, take localized display text
> from `seed?lang` / `qa?lang`.

### 7.1 Reading in a language over distribution

| Read | Endpoint | Language |
|---|---|---|
| Flat framework (localized) | `GET …/frameworks/{id}/versions/{v}/seed?lang=<code>` | overlay applied; absent ⇒ canonical |
| Audit template (localized) | `GET …/frameworks/{id}/versions/{v}/qa?lang=<code>` | §6 |
| Available languages | `GET …/frameworks/{id}` → each version's `languages[]` | discovery |

An untranslated language falls back to canonical text per node, so a partial
translation is safe to request. (The console additionally offers `export?lang`
and `structure?lang` for the authoring UI, but a GRC syncs off the distribution
endpoints above.)

### 7.2 How a translation CHANGE reaches you

The change feed emits **`translation_published`** (framework + version) when a
translation job completes for a **published** version. On that event, re-read the
version's `languages` (from `GET /frameworks/{id}`) and pull `seed?lang` /
`qa?lang` for the new/changed language.

| Feed event `kind` | What happened | What the GRC does |
|---|---|---|
| `translation_published` | Translations changed for this published version. | Re-fetch `languages[]`, then `seed?lang` / `qa?lang` for each. |

Translating a framework is a single job that localizes the framework text **and**
its audit template together; only human text is translated (refs, relations,
verdicts and `when` expressions are preserved). Translations added to a *draft*
version emit no event — they ship with the version's `published` event.

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
| QA template | `/changes` → `qa_published` | `/frameworks/{id}/versions/{v}/qa[?lang]` | — (unsigned; bundle vouches) | replace held template for that version/language |
| Translation | `/changes` → `translation_published` | `/frameworks/{id}` (languages) → `seed?lang` / `qa?lang` | — (overlay) | replace localized text |

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

## 11. Notes & remaining choices

All four artifacts — frameworks, cross-mappings, audit templates, translations —
are now machine-to-machine over `/api/registry/v1`, bearer-authed and
cursor-synced. Two things are deliberately **not** signed, and integrators should
know why:

- **The QA template and the translated seed are unsigned** (derived from live rows
  at request time, like the canonical seed). A consumer that needs cryptographic
  provenance verifies the framework **bundle** (which is signed and immutable) and
  trusts the derived shapes because they belong to a verified version. Signing the
  QA template on its own is a possible future step if a GRC requires it
  independently of the framework.
- **The bundle stays canonical-language.** Translating the signed bundle would
  invalidate its signature, so localized text is delivered via `seed?lang` /
  `qa?lang` overlays rather than a translated bundle. This keeps the signed
  artifact stable and the localization a display concern.

Everything scored or cross-referenced should still key off frameworks + mappings
(the signed source of truth); QA and translations enrich them for a localized,
audit-ready import.

---

## Before you start (deployment-specific)

Three values are per-deployment and must be supplied to any integration:

| Value | Example | Where from |
|---|---|---|
| **Base URL** | `https://framework.yourco.com` | your registry operator |
| **Bearer token** | `mzt_…` | a superadmin issues it (`registryctl token issue`) |
| **Pinned public key(s)** | `reg-2026:<base64-ed25519>` | delivered **out of band**, never trust a key served next to the content it signs |

Everything else in this guide (paths, shapes, semantics) is stable across
deployments.

---

## Appendix A — Canonicalization & signature (byte-exact)

A framework **bundle** and a **mapping set** are each signed the same way. To
verify (or to reproduce `contentHash`) you must build the *exact same canonical
bytes* the registry signed. The algorithm is intentionally simple but the details
matter — a single differing byte fails the hash.

### The canonical form

Given the document object (bundle or mapping set):

1. **Remove the `signature` field entirely** — not set to `null`, *absent*. (The
   registry marshals the struct with `signature` omitted, so the key does not
   appear.)
2. Serialize to JSON with these exact rules (this is what Go's `encoding/json`
   produces after a round-trip through a generic map):
   - **Object keys sorted** ascending by UTF-8 byte order, at every nesting level.
   - **Compact** — no spaces, no newlines between tokens.
   - **HTML-escaped**: Go's `encoding/json` escapes the three HTML-sensitive
     characters `<`, `>`, `&` (code points U+003C, U+003E, U+0026) to their
     six-character JSON unicode-escape form (backslash-`u003c`, backslash-`u003e`,
     backslash-`u0026`). *(If your JSON library does not do this, enable HTML
     escaping or post-process; otherwise your bytes differ and the hash fails.)*
   - Other non-ASCII is emitted as raw UTF-8 (not `\u`-escaped); standard JSON
     escaping applies to control characters and the `"` and `\` characters.
   - Numbers use the shortest round-trippable form (integers with no decimal
     point). Numeric values pass through IEEE-754 `float64` during normalization.
   - `omitempty` fields that are empty/zero are **absent** from the output.
3. `contentHash = "sha256:" + lowercase_hex( sha256( canonical_bytes ) )`.
4. The signature value is `base64_standard( ed25519_sign( privateKey,
   canonical_bytes ) )` — a **detached** signature over the canonical bytes (not
   over the hash).

### To verify

```
1. pub = pinnedKeys[ document.signature.keyId ]      # else: reject (unknown key)
2. require document.signature.alg == "ed25519"
3. canonical = canonicalize(document without signature)   # steps 1–2 above
4. require "sha256:"+hex(sha256(canonical)) == document.signature.contentHash
                                                     # else: reject (tampering)
5. require ed25519.Verify(pub, canonical, base64decode(document.signature.value))
                                                     # else: reject (bad signature)
```

Only step 5's success means the document is authentic **and** untampered. Never
import a document that fails any step. The **seed**, **QA template**, and
**translated overlays** are unsigned by design — verify the framework **bundle**
they derive from, then trust the derived shapes.

> **Practical note.** Reproducing Go's `encoding/json` output exactly in another
> language is the only hard part (key sorting + HTML-escaping + compaction). If in
> doubt, verify with the reference implementation
> (`registryctl verify --file <bundle> --pubkey <keyId:base64>`), or port
> `pkg/fwschema/canonical.go` (≈15 lines): marshal → unmarshal into a generic map
> → marshal again.

---

## Appendix B — Audit template schema & `when` grammar (`meizon-qa-template/v1`)

Enough to render an audit and score answers deterministically.

### B.1 Shape

```
Template
├─ $schema: "meizon-qa-template/v1"
├─ id, title, description?
├─ framework: { id, version?, language? }
├─ generatedBy?, model?, generatedAt?
├─ scale?:        { kind, levels: [ { value:int, label } ] }
├─ verdictModel?: { verdicts[], scoreOf{verdict→number|null},
│                   requirementRollup?, notApplicablePolicy? }
├─ defaults?:     { required?, weight?, allowNotApplicable?, evidenceTypes[]? }
└─ sections: [ Section ]

Section = { ref, name, order:int, questions: [ Question ] }
```

### B.2 Question

```
Question
├─ id                    unique within the template; flow targets address it
├─ order:int            unique within its section (ask in this order)
├─ requirementRef       binds the question to a framework requirement (STABLE
│                       across languages — join on this, not id)
├─ controlRef?
├─ text, intent?        the question, and a one-line hint
├─ type                 one of the 11 types below
├─ required?, weight?:int
├─ conditional?:bool    true ⇒ only reached via a follow-up (skip in the main run)
├─ expectedEvidence?[]  evidence hints
├─ assessment?          how an answer → verdict (B.4)
└─ followUps?[]         branching (B.5)
```

### B.3 The 11 types and their type-specific fields

| `type` | Answer shape | Extra fields |
|---|---|---|
| `yes_no` | `"yes"` / `"no"` | — |
| `yes_no_na` | `"yes"` / `"no"` / `"na"` | — |
| `yes_no_evidence` | yes/no + evidence | `expectedEvidence[]` |
| `single_choice` | one option `value` | `options:[{value,label}]` |
| `multi_choice` | list of `value`s | `options[]`, `minSelections?:int` |
| `scale` | an integer level | `scaleRef?` (a `scale.kind`); rubric in `assessment.rubric` |
| `numeric` | a number | `unit?`, `min?`, `max?` (thresholds in `assessment.thresholds`) |
| `date` | a date | thresholds may use `ageDays` |
| `evidence` | evidence items | `evidenceTypes?[]`, `minEvidence?:int` |
| `attestation` | signed statement | `attestation:{ statement, requireSignatory? }` |
| `free_text` | text | `placeholder?`, `maxLength?:int` |

> **Values are machine-meaningful, labels are display.** `options[].value` and the
> literals in `when` expressions are identical across languages; only `label`,
> `text`, `intent`, criteria, etc. are translated.

### B.4 Assessment → verdict → score

```
assessment = {
  criteria?: string,                       # human description
  rules?:      [ { when, verdict } ],       # ordered; FIRST match wins
  thresholds?: [ { when, verdict } ],       # same shape, for numeric/date
  rubric?:     [ { level:int, descriptor } ]# scale descriptors
}
```

- **Verdicts** (closed set): `compliant`, `partial`, `non_compliant`,
  `not_applicable`.
- Evaluate `rules` (then `thresholds`) in order; the first whose `when` is true
  yields the question's verdict.
- **Scoring** via `verdictModel`: `scoreOf` maps each verdict to a number, or
  `null` to exclude it. A requirement's score is the `requirementRollup`
  (`weighted_mean` by question `weight`) of its questions' scores.
  `notApplicablePolicy` is `exclude` (drop `not_applicable` from the mean) or
  `zero`.

### B.5 Follow-ups (branching)

```
followUps: [ { when, askId? , skipTo? } ]   # exactly one of askId / skipTo
```

When `when` is true after answering: `askId` inserts that (conditional) question
next; `skipTo` jumps ahead to that question. Targets are question `id`s in the
same template.

### B.6 The `when` expression language

A tiny, fixed, side-effect-free grammar — **no arbitrary code**. Evaluated after a
question is answered.

**Variables** (type):

| Variable | Type | Meaning |
|---|---|---|
| `answer` | string | the raw answer (`'yes'`, a choice value, …) |
| `verdict` | string | the verdict this question just resolved to |
| `score` | number | this question's score |
| `value` | number | numeric-question value |
| `ageDays` | number | age of a date answer, in days |
| `evidence.count` | number | evidence items provided |
| `selected.count` | number | choices selected (multi_choice) |
| `selected` | list | the selected choice values |
| `attested` | bool | attestation signed |
| `true` / `false` | bool | literals |

**Operators**: `==` `!=` `<` `<=` `>` `>=` `&&` `||`, plus `in` (membership) and
`superset` (list ⊇ list). Comparisons coerce: numeric if both sides look numeric,
else string. String literals use single quotes; lists use `[ … ]`.

**Examples**

```
answer == 'no'
verdict == 'partial' || score < 0.5
selected.count >= 2 && 'mfa' in selected
selected superset ['fw','ids','edr']
value > 90
ageDays > 365
attested == true
```

An expression referencing any variable not in the table above is rejected at
generation/edit time, so a stored template's expressions are always evaluable.
