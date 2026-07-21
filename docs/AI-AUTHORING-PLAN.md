# Plan — Guided Framework Authoring with LLM Assist (Human-in-the-Loop)

> Goal: an auditor builds a framework **step by step** in the console. At every step they can
> **generate content with an LLM**, then **edit / accept / discard each proposal**, or add
> everything **manually**: requirements hierarchy, controls, evidence guidance and policy
> templates — the full universal-template JSON surface (`framework-db-schema.sql` §5–§7).
> The LLM proposes; the auditor decides. Nothing enters the draft without human action.

Builds on what exists today: the v2 hierarchy + cross-mappings, the StructureTree editor,
the Import JSON dialog, and the DRAFT → review → publish lifecycle. This plan adds (a) the
remaining template catalogs (control library, evidence guidance, policy templates), (b) a
step-wizard authoring UX, and (c) a provider-pluggable LLM proposal pipeline with full
provenance.

---

## 1. Design principles

- **P1 — Proposal, not mutation.** The LLM never writes to the draft. It produces a
  *proposal document* (validated JSON fragments of the exchange schema). The auditor
  accepts/edits/rejects per element; acceptance goes through the existing structure
  services, so DRAFT-only, RBAC, region scope and code-uniqueness all still apply.
- **P2 — Manual parity.** Every step is fully usable with no LLM configured. AI assist is
  a feature flag (`REGISTRYD_LLM_API_KEY` absent → assist UI hidden, AI endpoints 404).
- **P3 — Provenance is first-class.** Every row created in the wizard records
  `origin: manual | ai | import`, and every LLM exchange (prompt, model, raw output,
  decision) is persisted — an auditor/regulator can always answer "who wrote this?".
- **P4 — Copyright guardrail.** For `license: proprietary` frameworks the system prompt
  mandates *paraphrased structure and titles, never verbatim standard text*; the UI shows
  the same warning. (Extends the existing distribution copyright gate to generation.)
- **P5 — Signed content unchanged.** Bundles stay deterministic and code-addressed;
  AI/manual provenance and session data are registry-side metadata, not signed content
  (like mapping resolution state).

## 2. Data model additions

### 2.1 Template catalogs (deferred Phase-4 items, now required)
New tables (tenant + GID conventions; entity types 17–21):

| Table | Shape (adapted from template §6–7) |
|---|---|
| `control_library` | id, code (unique), name, description, domain, source_framework_id (nullable FK), tags TEXT[], origin |
| `control_requirement_items` | control_id ↔ item_id (M:N), both CASCADE |
| `policy_templates` | id, name, **body TEXT (markdown — our extension**, the template stores only name/ref**)**, origin |
| `policy_template_controls` | policy_template_id ↔ control_id (M:N) |
| `evidence_guidance` | id, control_id FK, type (`automated_test\|document\|policy\|interview\|observation`), hint, renewal_cadence_months INT NULL, policy_template_id (nullable FK), origin |

### 2.2 Authoring sessions + AI provenance
| Table | Purpose |
|---|---|
| `authoring_sessions` | id, framework_version_id FK, current_step, status (`active\|finished`), created_by, timestamps — resumable wizard state |
| `ai_generations` | id, session_id FK, step, model, prompt, raw_output JSONB, status (`proposed\|accepted\|partially_accepted\|rejected`), accepted_count INT, created_at — the audit trail of every LLM call |

`origin TEXT NOT NULL DEFAULT 'manual'` is also added to the existing hierarchy tables
(categories/requirements/sections/items) via a small migration, backfilled `import` where
the row came from `framework.import` audit lineage (best-effort: default `manual`).

### 2.3 Exchange schema v2.1 (minor bump)
Bundle gains optional top-level `controls[]` (code, name, description, domain, tags,
items[] by code), `policyTemplates[]` (name, body, controls[] by code) and per-control
`evidenceGuidance[]`. v2.0 documents remain valid; `Flatten()` unchanged. Import dialog
accepts v2.1 and populates the catalogs.

## 3. LLM integration (`pkg/llm`)

- **Provider interface**: `Generate(ctx, req) (text, usage, error)` with three built-in
  providers — **OpenAI** (chat completions), **Anthropic** (messages) and **Google
  Gemini** (generateContent) — implemented over plain HTTP (uniform, dependency-light),
  plus a `fake` provider for tests. Each supports an optional **base URL override**
  (Azure OpenAI, proxies, gateways, mock endpoints).
- **Configuration lives in the database, managed from a superadmin *Settings* page** in
  the console (not env vars): provider select, API key (stored **AES-256-GCM encrypted**
  via `pkg/crypto/cipher`, never returned — only a "configured" flag), model, max tokens,
  base URL, and a **Test connection** button. When unconfigured, AI assist is hidden and
  AI endpoints refuse. Suggested default models per provider: `gpt-4o` /
  `claude-sonnet-5` / `gemini-2.5-pro` (free-text, operator-editable).
- **Structured output**: each step has a JSON schema (reusing `fwschema` fragments). The
  service validates the model output with the existing validator; on invalid JSON it
  retries once with the error appended, then surfaces a clean failure. Oversized outputs
  truncated server-side; token/row caps per step (e.g. ≤ 50 items per generation).
- **Grounding**: prompts include the current draft state (existing codes to avoid
  duplicates) and, for the mapping step, the item catalogs (code + title) of published
  frameworks in the registry so proposals reference real targets.
- **Safety/ops**: per-identity rate limit on AI endpoints; decision + token usage in the
  audit log (`ai.generate`, `ai.accept`); no framework content ever sent anywhere except
  the configured provider.

## 4. The wizard — steps and endpoints

Step rail (design-system: mono eyebrow steps, sage active; resumable via
`authoring_sessions`):

| # | Step | Manual UI | LLM assist ("Generate" → proposal list with per-row **Accept / Edit / Discard**, and **Accept all**) |
|---|---|---|---|
| 1 | **Profile** | metadata form (id, name, version, region, license, authority, kind) | prefill from a one-line brief ("PCI DSS v4.0.1 payment security standard") |
| 2 | **Categories** | add/edit/delete (existing tree ops) | propose the goal/domain list |
| 3 | **Requirements** | per-category forms | propose numbered requirements per category |
| 4 | **Sections & items** | existing StructureTree editing | propose sections + items (code, title, description, type, guidance) for a chosen requirement |
| 5 | **Cross-mappings** | existing mapping panel | propose mappings to *published* frameworks (grounded in their real item codes); stubs allowed for absent ones |
| 6 | **Controls** | control table: code, name, domain, link items (multi-select by code) | propose implementable controls covering the items, with item links |
| 7 | **Evidence** | per-control evidence rows (type select, hint, cadence) | propose evidence guidance per control |
| 8 | **Policy templates** | name + markdown body editor + linked controls | draft policy template bodies from linked controls |
| 9 | **Review & finish** | validation summary, counts by origin (manual/ai/import), then → normal submit/approve/publish | — |

Console API (all session-cookie, DRAFT-only, author-role + region authorized):
```
POST   /frameworks/{ref}/authoring                 start/resume session
GET    /frameworks/{ref}/authoring                 session state + step data
POST   /frameworks/{ref}/authoring/step            set current step
POST   /frameworks/{ref}/ai/generate               {step, brief, scope} → {generationId, proposals[]}
POST   /frameworks/{ref}/ai/accept                 {generationId, accepted[(possibly edited) elements]}
-- catalogs (manual CRUD, also used by ai/accept):
POST/DELETE /frameworks/{ref}/controls-library     + PUT link items
POST/DELETE /frameworks/{ref}/policy-templates     + PUT link controls
POST/DELETE /controls-library/{code}/evidence
```
`ai/accept` re-validates every element and applies it through the same services as manual
adds — one enforcement path (P1).

## 5. Review/publish integration

- Framework detail gains an **"Authoring"** badge/link while a session is active.
- The **reviewer (moderator)** sees provenance in the review screen: counts per origin and
  an "AI-assisted" chip on the version; `ai_generations` are inspectable from the audit
  log. Approval/publish flow unchanged (SoD intact — the LLM is never an approver;
  authorship stays with the human who accepted).
- v2.1 bundles carry the catalogs; signing/verification untouched.

## 6. Phasing

- **Phase A — catalogs + manual wizard** (no LLM): migrations + coredata + services +
  console endpoints for control library / evidence / policy templates; wizard shell with
  all 9 steps operating manually; v2.1 import/export; origin column. *Fully verifiable in
  the browser.*
- **Phase B — LLM pipeline**: `pkg/llm` (anthropic + fake), `ai/generate` + `ai/accept`,
  proposal UI (list + inline edit + accept), provenance records, copyright guardrail,
  rate limits. *Verified with the fake provider in tests + real key in the browser.*
- **Phase C — polish**: grounded mapping suggestions (step 5), regenerate-with-feedback
  ("more granular", "split section 7.2"), streaming progress, per-step token budgets.

## 7. Acceptance criteria

1. With **no LLM key**, an auditor completes all 9 steps manually — hierarchy, mappings,
   controls, evidence, policy templates — and publishes; the v2.1 bundle round-trips
   through import (byte-stable seed unchanged).
2. With the **fake provider**: generate at step 2 → proposals appear; accept 2, edit 1,
   discard 1 → draft contains exactly the accepted/edited rows with `origin=ai`;
   `ai_generations` records prompt, raw output and decision.
3. AI endpoints are DRAFT-only, author-role + region enforced, rate-limited; invalid model
   JSON never reaches the draft (validation rejects, one retry).
4. Proprietary license → generation prompt includes the paraphrase-only instruction
   (asserted in tests); UI shows the copyright warning.
5. Reviewer sees origin counts + AI chip; SoD unchanged; publish/sign/distribute and
   stub resolution behave exactly as today (regression suite green).
6. Browser e2e: guided-create a framework, mix AI + manual content across steps, finish,
   submit → approve → publish, then confirm coverage/distribution work on it.

## 8. Out of scope (this plan)

Auto-accept / agentic authoring without human review; LLM-based mapping *inference* across
already-published frameworks (charter non-goal); fine-tuning; tenant-side assessment
content (template excludes it too); gqlgen refactor.
