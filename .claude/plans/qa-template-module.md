# Plan — QA audit-template module (`meizon-qa-template/v1`)

Generate an ordered, requirement-keyed audit questionnaire from a **published**
framework: walk its requirements one by one through the LLM to produce sections
of questions, store them editably, view/edit in the console, and preview the run
as a chat conversation.

## What already exists to build on (verified)

- **`QAInstruction`** column is already in `coredata.LLMSetting` — the settings
  slot for this step was reserved. No settings migration needed for the prompt.
- **Job infrastructure**: `ingest_jobs` + `jobStore` write-through mirror, live
  progress, orphan reconciliation, resume across restart. `JobKind*` enum
  (`generate|next_version|translate|automap`).
- **Batched-per-item LLM pattern**: `mapping_ingest.go` `adjudicateBatchSize=4`,
  batch loop with progress callback — the exact shape for "requirements one by
  one".
- **Reading a published framework**: `ExportFlat(ref)` → `fwflat.Framework`
  (flat, categories + requirements + controls by code); `StructureOf(versionID)`
  for the richer tree with control links.
- Console route + handler conventions in `pkg/server/api/console/v1`.

## Decisions to lock first

1. **Storage shape → normalized, not one JSONB blob.** The core requirement is
   *view and edit individual questions and reorder them*, so questions are rows.
   Type-specific fields (options, thresholds, followUps, assessment) vary by
   type, so each question carries a JSONB `spec` rather than 20 sparse columns.
   - `qa_templates` — one per (framework version), metadata + status.
   - `qa_questions` — id, template_id, section_ref, requirement_ref, control_ref,
     order, type, text, weight, required, conditional, `spec` JSONB.
   A whole-document JSONB would make reorder/edit a read-modify-write of the
   entire template and lose row-level history; rejected.

2. **Generation granularity → per requirement, batched, async job.** One LLM
   call covers a small batch of requirements (start at 4, tune), each returning
   its questions. Runs as an `ingest_jobs` job (`JobKindQATemplate`) with
   progress `requirement N/total` — a 454-requirement GOST framework must not
   block a request. Sections come from the framework's **categories**; a question
   addresses a **requirement by `ref`**.

3. **Lifecycle → light (draft → ready), not full moderation.** A QA template is
   an internal audit artifact, not a signed distributed one. `status: draft|ready`
   is enough; skip submit/approve/publish. (If audit templates later need to be
   distributed or version-frozen, revisit — but don't pay for it now.)

4. **One `when`-evaluator, server-side.** `followUps`, `skipTo`, and
   `assessment.rules` share a tiny expression grammar. Implement it **once** in Go
   and have the chat preview call a `/qa/evaluate` endpoint, rather than porting
   the grammar to JS. This is the same "don't duplicate the logic that decides
   things" rule the mapping refactor enforced — two evaluators would drift, and
   this one gates question flow.

5. **`ref` is the identity, as everywhere else.** A question binds to a
   requirement `ref`. This is what makes regeneration-after-upgrade cheap (see
   Interaction with version deltas).

## 1. `pkg/fwqa` — schema, validation, `when`-evaluator (pure, testable)

- The `meizon-qa-template/v1` structs: `Template`, `Section`, `Question`,
  `Assessment`, `FollowUp`, `Scale`, `VerdictModel`. Mirror `pkg/fwflat`/`fwmap`
  house style.
- `Validate()`: every `followUp.askId`/`skipTo` resolves to a question in the
  template; every `requirementRef` is non-empty; `order` unique within a section;
  `conditional` questions are reachable only via a follow-up (not left orphaned);
  choice questions have options; the referenced `scaleRef` exists.
- `Eval(rule string, ctx EvalContext) (bool, error)` — the `when` grammar over a
  **fixed** variable set (`answer`, `score`, `value`, `verdict`, `ageDays`,
  `evidence.count`, `selected.count`, `attested`) with `==`, `!=`, `<`, `<=`,
  `>`, `>=`, `&&`, `||`, `in [...]`, `superset [...]`. No arbitrary code — a
  reviewer must be able to read a rule and the agent must evaluate it safely.
- Fully unit-tested with no database: validation rejects a dangling `askId`; the
  evaluator returns the right branch for each operator.

## 2. Migration + coredata

- Migration: `qa_templates`, `qa_questions` (indexes on template_id, and
  (template_id, section_ref, order)). FK `qa_questions.template_id →
  qa_templates ON DELETE CASCADE`. New entity types registered.
- `coredata.QATemplate` / `QAQuestion` with Insert/LoadByFramework/Update/Delete,
  `LoadQuestions(templateID)` ordered, `Reorder`, and a `Replace` that swaps a
  template's questions transactionally (used when regenerating).

## 3. Generation service (`pkg/registry/qa_template_service.go`)

- `StartQATemplateJob(actor, frameworkRef)`:
  - Loads the **published** version via `ExportFlat`/`StructureOf` (refuse if the
    framework has no published version — an audit is of published requirements,
    not a draft).
  - Preflight the LLM (`ingestPreflight`) exactly as generation does.
  - Async goroutine, batched: for each batch of requirements, one LLM call using
    `QAInstruction` + a `qaContract` system prompt returning strict JSON
    (questions per requirement ref); merge into a `fwqa.Template`; write progress
    `req N/total`.
  - On completion, `Replace` the template's questions in one transaction and mark
    `status=ready`; the reviewable result is the template id.
- `qaContract`: "For each requirement, produce ordered audit questions a
  compliance auditor would ask to verify it, choosing an appropriate question
  type, with assessment criteria. Return ONLY JSON." Instruction overridable from
  settings, mirroring the other steps.
- Guardrails reused from the ingest pipeline: `docextract.Sanitize` every string
  before it can reach jsonb (the NUL-byte lesson), and the model's chosen `type`
  validated against the known set or coerced to `free_text`.

## 4. Console API

Mirror the existing framework routes:

- `POST /frameworks/{ref}/qa/generate` → starts the job, returns `{jobId}`.
- `GET  /qa/jobs/{jobId}` → progress (reuse the job status shape).
- `GET  /frameworks/{ref}/qa-template` → the assembled `fwqa.Template`.
- `PATCH /qa-template/{id}/questions/{qid}` → edit one question (text, type,
  weight, spec).
- `POST  /qa-template/{id}/questions` / `DELETE …/{qid}` → add/remove.
- `POST  /qa-template/{id}/reorder` → new order within a section.
- `POST  /qa/evaluate` → `{questionSpec, answer} → {verdict, score, nextQuestionId}`
  the single shared runner the preview calls.

All DRAFT-editable; a `ready` template is editable too (no signature to protect),
but generation-`Replace` warns before discarding manual edits.

## 5. UI — template editor (`apps/registry/src/pages/QATemplate.jsx`)

- Entry from the framework detail page: "Generate audit template" → job progress
  (reuse `IngestProgress`/`Jobs` components) → editor.
- Left: sections (categories) → ordered question list, drag to reorder.
- Right: per-question editor keyed by `type` — a small component per type
  (yes/no, scale with rubric, choice options, numeric thresholds, evidence,
  attestation, free-text), plus follow-up rules and the assessment block.
- Each question shows its `requirementRef` as a chip linking back to the
  requirement — the reviewer always sees *what this question is auditing*.
- Regenerate button (re-runs the job; confirms before replacing edited questions).

## 6. UI — chat-based preview (`apps/registry/src/components/QAPreview.jsx`)

- A **session runner** that walks the template by `cursor`, rendering the current
  question as a chat turn: the agent "asks", the user answers with a
  type-appropriate control (yes/no buttons, slider, choice chips, evidence
  stub, text box).
- On each answer, POST `/qa/evaluate` → verdict + `nextQuestionId`; the evaluator
  applies `followUps`/`skipTo`, so conditional questions appear inline exactly as
  a real audit would, and the running verdict is shown per requirement.
- It is a **dry run**, not a persisted audit session — but it exercises the exact
  ordering, follow-up, and scoring logic, using the same server evaluator a real
  session would. This is the cheapest possible validation that a generated
  template actually flows sensibly.
- Reset / restart; a compact summary at the end (per-requirement verdicts,
  overall score from `verdictModel`).

## Interaction with version deltas (why `ref`-keying pays off)

When the source framework upgrades, the existing `DiffBundles` tells you exactly
which requirement `ref`s were added/removed/modified. Regeneration then needs to
touch **only** those: keep every question whose `requirementRef` is unchanged
(and its manual edits), regenerate questions for changed refs, drop questions for
removed refs. So an audit template survives a framework upgrade the same way a
consumer's evidence does — this is the direct reason questions bind to `ref`, not
to a name or a row id. Ship the module first; wire delta-aware regeneration once
both exist.

## Verification

- `pkg/fwqa` unit: validation rejects dangling `askId` / orphaned conditional /
  missing options; `Eval` covers every operator and the `in`/`superset` forms.
- Service e2e (DB): generate against a small published framework with a mock LLM
  (the `llm.Fake` step-router pattern — key QA responses by the `qaContract`
  system prompt), assert one template with questions bound to the right refs,
  ordered, `status=ready`.
- Refuse-on-unpublished: generating for a draft-only framework errors clearly.
- Edit/reorder round-trips through the DB.
- Preview: a scripted answer sequence drives the cursor through a follow-up and a
  `skipTo`, and the final `verdictModel` roll-up matches a hand computation.
- Browser: generate → edit a question → preview the chat flow end to end.

## Sequencing

`pkg/fwqa` (schema + evaluator + tests) → migration + coredata → generation
service + job → console API (incl. `/qa/evaluate`) → editor UI → chat preview.
Land `fwqa` first: it is pure, and both the service and the preview depend on the
evaluator.

## Risks

- **LLM output quality per requirement.** A vague requirement yields vague
  questions. Mitigate with a firm `qaContract`, type-coercion on bad output, and
  the human editor as the backstop — never auto-publish an audit from the model
  alone.
- **Batch desync in the mock test.** The next-version test just taught this:
  key fake responses by prompt (`llm.Fake.Route`), never by call order.
- **Two evaluators drifting.** Avoided by keeping `when` evaluation server-side
  only; do not reimplement it in JS for the preview.
- **Regeneration clobbering edits.** `Replace` must warn; delta-aware
  regeneration (later) is the real fix.
