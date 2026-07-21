# Document → `meizon-framework/v2` — ingestion guide

How to turn a compliance-standard document (PDF, scanned PDF, images, DOCX, HTML)
into a valid **`meizon-framework/v2`** file using a chain of small, verifiable LLM
requests.

Each step has **one job, a strict input and a strict JSON output**, so failures
are local and re-runnable and no single request has to "understand the whole
standard at once". Every merge/validation step is **plain code**, not an LLM, so
the final file is reproducible.

> **Scope note.** This guide targets the **flat exchange format** below —
> `categories[] / requirements[] / controls[]` addressed by `ref`, one framework
> per file, **no cross-framework mappings** (those are joined in the database
> after import). This is *not* the registry's internal nested `fwschema` v2
> (`categories → requirements → sections → items`). Don't mix the two.

---

## Target schema

```jsonc
{
  "$schema": "meizon-framework/v2",     // literal marker
  "id": "acme-baseline",                // required — kebab-case slug
  "name": "Acme Baseline Security",     // required
  "version": "1.0",                     // recommended
  "category": "cybersecurity",          // recommended — enum, see Step 1
  "regions": ["GLOBAL"],                // recommended — GLOBAL|<continent>|<ISO-3166-1 alpha-2>

  "categories": [                       // optional — top-level groupings
    { "ref": "AC",  "name": "Access Control" },
    { "ref": "LOG", "name": "Logging & Monitoring" }
  ],

  "requirements": [                     // required — the framework items
    { "ref": "AC-1", "category": "AC",
      "name": "Access Control Policy",
      "description": "Maintain a documented access control policy, reviewed at least annually.",
      "controls": ["access-policy"] }   // refs into controls[]
  ],

  "controls": [                         // optional — suggested implementations
    { "ref": "access-policy", "name": "Access control policy",
      "description": "A written, approved policy governing how access is granted, reviewed and revoked.",
      "category": "Policy" }
  ]
}
```

**Minimum valid file:**
`{ "$schema": "meizon-framework/v2", "id": "…", "name": "…", "requirements": [ { "ref": "…", "name": "…", "description": "…" } ] }`

---

## Pipeline

```
 ┌── Step 0 ── Document → text/Markdown, chunked        (vision model if scanned)
 │
 ├── Step 1 ── Identify: id, name, version, category, regions          (1 call)
 │
 ├── Step 2 ── Extract: categories + requirements       (1 call per chunk)
 │             └── merge: dedupe by ref                       (code)
 │
 ├── Step 3 ── Controls: ≥1 control per requirement     (1 call per ~15 reqs)
 │             └── merge: union by ref, apply links           (code)
 │
 └── Step 4 ── Assemble + validate → one v2 JSON file         (code)
               └── optional QA read-back                (1 call, advisory)
```

Cost shape for a 300-requirement standard: `n` chunk calls (Step 2) + 1 (Step 1)
+ ~20 batch calls (Step 3). Steps 0 and 2 dominate.

---

## Global conventions

- **JSON only.** Enforce with a response schema / tool call where the provider
  supports it. Never accept free text. Reject and retry on parse failure.
- **Temperature `0`** for identify / extract / merge; `0.2` max for control
  drafting (Step 3) where a little phrasing variety is fine.
- **Refs are slugs, not prose.** `ref` must be stable, unique and taken from the
  document's own numbering (`AC-1`, `3.3.2`, `Art.5`, `GV.OC-01`). If the
  document has no numbering, slugify the title (`access-control-policy`).
  **Never invent numbering.**
- **Idempotency.** Re-running a step on the same input must yield the same refs,
  so merges are stable and re-imports upsert cleanly.
- **Language.** Write `name` / `description` in the **source document's
  language**. Never translate.
- **Token headroom.** Give every call a generous `max_tokens`. "Thinking" models
  (e.g. `gemini-2.5-*`) spend output tokens on internal reasoning *before*
  emitting text — a small budget is consumed entirely and you get a `200` with
  **zero content**. If a step returns empty, raise the budget before suspecting
  the prompt.
- **Retry policy.** Each step retries up to 2× on invalid JSON or schema miss,
  appending the validation error to the prompt. After that, fail *that step* and
  record which chunk/batch failed — the rest of the pipeline still yields a
  partial file for a human to finish.

> **Copyright.** Requirement text copied verbatim from SOC 2 / ISO / PCI / CIS is
> the standards body's IP. Ingesting a licensed copy into a customer's own tenant
> is fine; **do not** publish the result as a redistributable public seed. For
> public seeds, paraphrase or author original summaries.

---

## Step 0 — Convert to text/Markdown, then chunk

**Goal:** clean Markdown that preserves the heading hierarchy and requirement
tables, split into context-sized parts with overlap.

**Input:** the raw document. Digital PDF → extract text first (`pdftotext
-layout`, or a library reader). **Scanned/image PDF → pass page images to a
vision model and OCR page by page.**

**Chunking rules (code, not an LLM):**
- Target **≤ 12k tokens** of Markdown per chunk (≈ 48k characters).
- Split on the **highest heading boundary** that keeps chunks under budget.
  Never split mid-table or mid-requirement.
- Carry a **one-heading overlap** so a requirement straddling a boundary is
  fully present in at least one chunk. Dedupe by `ref` later handles the repeat.
- Preserve **page boundaries** (emit a form feed `\f` between pages) if you want
  to trace a requirement back to a page later.
- Label each chunk `part i of n`.

**System prompt**

```
You are a document-conversion tool. Convert the supplied page(s) of a
compliance standard into clean GitHub-flavored Markdown.

Rules:
- Preserve the heading hierarchy exactly (#, ##, ### by the document's own
  section levels). Keep the original section/requirement numbers in the heading
  text (e.g. "## 3.3.2 SAD not stored").
- Render requirement tables as Markdown tables; do not summarize or drop rows.
- Transcribe text faithfully. Do NOT interpret, renumber, or add commentary.
- Drop running headers/footers, page numbers, and watermarks.
- Output Markdown only — no code fences around the whole document, no preamble.
```

**User message**

```
Document: {filename}
Part {i} of {n}. Convert the following to Markdown:

{raw_text_or_page_images}
```

**Output:** one Markdown string per chunk. Persist as `work/{id}.part{i}.md` so
steps can be re-run individually.

---

## Step 1 — Identify the document

**Goal:** one small object of framework-level metadata. Runs **once**, on the
first chunk plus any cover/title page.

**System prompt**

```
You identify compliance standards. From the document excerpt, extract the
framework's identity. Return ONLY JSON matching the schema — no markdown
fences, no commentary.

Fields:
- id: kebab-case slug for the framework (e.g. "nist-csf-2.0", "gdpr",
  "pci-dss-4.0.1")
- name: short display name (e.g. "NIST CSF 2.0", "GDPR")
- version: the edition/version string exactly as printed, else null
- category: one of cybersecurity | data_protection | payment_security |
  healthcare_privacy | financial_resilience | privacy | other
- regions: array of GLOBAL | a continent (AFRICA, ASIA, EUROPE, NORTH_AMERICA,
  SOUTH_AMERICA, OCEANIA) | a bloc (EU, EEA) | ISO 3166-1 alpha-2 country codes.
  Choose by the standard's jurisdiction (GDPR -> ["EU","EEA"], HIPAA -> ["US"],
  PCI DSS -> ["GLOBAL"]). Default ["GLOBAL"] if not jurisdiction-bound.
- issuingBody: the publishing organization, else null

If a field is not stated in the text, use null. NEVER guess a version or date.
```

**User message**

```
Identify this standard:

{part_1_markdown}
{title_page_markdown_if_any}
```

**Output contract**

```json
{
  "id": "acme-baseline",
  "name": "Acme Baseline Security",
  "version": "1.0",
  "category": "cybersecurity",
  "regions": ["GLOBAL"],
  "issuingBody": "Acme Standards Group"
}
```

**Validation:** `id` and `name` non-empty; `category` in the enum; `regions`
non-empty. On failure retry once with the enum list appended.

`issuingBody` is kept only to aid identification — **drop it in Step 4**, it is
not a v2 field.

---

## Step 2 — Extract categories and requirements

**Goal:** the top-level groupings and the flat requirements list. Runs **once per
chunk**; results are concatenated then deduped by `ref`.

**System prompt**

```
You extract the structure of a compliance standard. From this part of the
document, return the categories (top-level groupings) and the requirements
(the individual, testable obligations). Return ONLY JSON — no markdown fences,
no commentary.

Rules:
- category: { ref, name }.
    - ref  = the group's own code/letters (e.g. "AC", "GV", "CH2", "3").
    - name = the group's title.
- requirement: { ref, category, name, description }.
    - ref  = the requirement's own number from the document, verbatim and
             unique (e.g. "AC-1", "3.3.2", "Art.5", "GV.OC-01"). NEVER invent
             numbering. If the document has no numbering, slugify the title.
    - category = the ref of the category it belongs to, or null if this part
             shows no grouping.
    - name = a short title (≤ 90 chars).
    - description = the obligation text, faithfully and completely. One
             requirement per distinct obligation; do NOT merge two numbered
             items into one, and do NOT split one obligation across two.
- Write name and description in the SAME language as the source document.
  Never translate.
- Extract ONLY what appears in this part. Do not carry over or infer items from
  other parts. Emit [] for a section that has none.
- Preserve document order.
```

**User message**

```
Framework: {name} {version}
Part {i} of {n}. Extract categories and requirements from:

{part_i_markdown}
```

**Output contract (per chunk)**

```json
{
  "categories": [ { "ref": "AC", "name": "Access Control" } ],
  "requirements": [
    { "ref": "AC-1", "category": "AC",
      "name": "Access Control Policy",
      "description": "Maintain a documented access control policy, reviewed at least annually." }
  ]
}
```

**Merge across chunks — plain code, not an LLM:**
1. Concatenate all `categories`, dedupe by `ref` (**first wins**).
2. Concatenate all `requirements`, dedupe by `ref`. When the same `ref` appears
   in two chunks (the overlap), **keep the longer `description`** — the truncated
   copy is the one that straddled the boundary.
3. Drop any `requirement.category` that doesn't match a known category `ref`
   (set it to `null`) — prevents dangling references.
4. Preserve first-seen order.

---

## Step 3 — Generate controls for each requirement

**Goal:** at least one suggested implementation **control** per requirement, plus
the requirement→control links. Batch **~15 requirements per request** to keep
each call focused and cheap.

**System prompt**

```
You are a GRC implementation expert. For each requirement, propose the concrete
control(s) an organization would implement to satisfy it. Return ONLY JSON —
no markdown fences, no commentary.

Rules:
- Every requirement MUST get at least one control ref.
- Prefer REUSING an existing control across requirements when the same
  implementation satisfies several (return its ref again) rather than creating
  near-duplicates.
- control: { ref, name, description, category }.
    - ref = kebab-case slug, stable and unique (e.g. "mfa-enforced",
            "access-control-policy", "central-audit-logging").
    - name = short imperative title (≤ 80 chars).
    - description = 1–2 sentences on what implementing it means. Generic and
            reusable — do NOT reference a specific company, tool, or the
            requirement's exact wording.
    - category = one of: Policy | Access | Logging | Encryption | Network |
            Endpoint | Vulnerability | Incident | Vendor | HR | Physical |
            Governance | Monitoring | Backup | Other.
- Output the requirement->control mapping as `links`.
```

**User message**

```
Framework: {name}

Requirements (batch {b}):
{json_array_of_{ref,name,description}_for_this_batch}

Existing control refs you may reuse (from earlier batches):
{known_control_refs}
```

**Output contract (per batch)**

```json
{
  "controls": [
    { "ref": "access-policy", "name": "Access control policy",
      "description": "A written, approved policy governing how access is granted, reviewed and revoked.",
      "category": "Policy" }
  ],
  "links": [ { "requirement": "AC-1", "controls": ["access-policy"] } ]
}
```

**Merge — code:**
1. Union all `controls` by `ref` (**first definition wins**).
2. Apply `links`: set `requirement.controls = links[ref].controls`.
3. **Guarantee ≥1:** any requirement still without a control gets a synthesized
   fallback `{ ref: "<reqref>-control", name: "Implement <req name>",
   category: "Other" }`. **Log every synthesized control** so a human can refine
   them — they are the weakest output of the pipeline.

Feeding `known_control_refs` forward is what keeps the control library from
exploding into near-duplicates; don't skip it.

---

## Step 4 — Assemble and validate

**Goal:** one v2 file. This step is **pure code (no LLM)** so the output is
reproducible.

**Assembly**

```
{
  "$schema": "meizon-framework/v2",
  ...Step 1 (id, name, version, category, regions),   // drop issuingBody
  "categories":   merged Step 2 categories,           // omit if empty
  "requirements": merged Step 2 requirements + Step 3 control refs,
  "controls":     merged Step 3 controls              // omit if empty
}
```

**Validation gate — reject the file unless all pass:**

1. Validates against the `meizon-framework/v2` JSON schema.
2. `requirements` is non-empty; every `requirement.ref` is unique.
3. Every `requirement.category` (when present) matches a `categories[].ref`.
4. Every ref in `requirement.controls` matches a `controls[].ref`.
5. Every requirement has ≥ 1 control (per the Step 3 guarantee).
6. Every `controls[].category` is in the enum.
7. No `requirement.description` is an exact duplicate of another's beyond N
   chars — catches accidental row-merges.
8. Requirement count is within ±10% of the count of numbered headings found in
   Step 0's Markdown — catches a chunk that silently returned `[]`.

Check 8 is the one that catches the failure mode this pipeline exists to
prevent: a dropped chunk. Do not skip it.

**Optional QA read-back (1 call, advisory only)**

```
You QA an extracted compliance framework. Framework "{name}" ({version}) was
extracted into {n_categories} categories and {n_requirements} requirements.
Sample requirement refs: {10 sampled refs}.

In ONE short sentence, note anything that looks missing, mis-grouped or
duplicated for a standard of this type. If it looks complete, reply exactly
"Looks complete." Return plain text, one sentence.
```

Surface the answer to a human. **Never auto-edit the file from it.**

**Output:** `seeds/v2/{id}.json`, plus an index entry (id, name, version,
category, regions, counts).

---

## Orchestration notes

- **Where it runs.** Either as an offline script that emits the file for review,
  or as a multi-tool agent run (one tool per step) with a plan-first gate so a
  human approves before the file is written/imported.
- **Human-in-the-loop.** Always review Step 3 output before publishing a seed —
  generated controls are *suggestions*, and copyright/paraphrase judgement is a
  human call.
- **Re-runs.** Because refs are deterministic, re-running a single failed chunk
  and re-merging is safe. Persist each step's raw output.
- **Traceability (optional).** Ask Step 2 for a short verbatim `sourceExcerpt`
  per requirement and keep it *outside* the published file — it lets a reviewer
  jump to the passage a requirement came from, but it is not part of the schema
  and must not ship in a public seed.

---

## Worked example

The end state for a small standard:

```json
{
  "$schema": "meizon-framework/v2",
  "id": "acme-baseline",
  "name": "Acme Baseline Security",
  "version": "1.0",
  "category": "cybersecurity",
  "regions": ["GLOBAL"],

  "categories": [
    { "ref": "AC",  "name": "Access Control" },
    { "ref": "LOG", "name": "Logging & Monitoring" }
  ],

  "requirements": [
    { "ref": "AC-1", "category": "AC",
      "name": "Access Control Policy",
      "description": "Maintain a documented access control policy, reviewed at least annually.",
      "controls": ["access-policy"] },
    { "ref": "AC-2", "category": "AC",
      "name": "Multi-Factor Authentication",
      "description": "Enforce MFA for all administrative and remote access to production systems.",
      "controls": ["mfa-enforced"] },
    { "ref": "LOG-1", "category": "LOG",
      "name": "Audit Logging",
      "description": "Enable audit logging on production systems; retain logs ≥12 months.",
      "controls": ["central-logging"] }
  ],

  "controls": [
    { "ref": "access-policy",   "name": "Access control policy",        "category": "Policy" },
    { "ref": "mfa-enforced",    "name": "MFA enforced on admin access", "category": "Access" },
    { "ref": "central-logging", "name": "Centralized audit logging",    "category": "Logging" }
  ]
}
```

Trace it back: `AC`/`LOG` came from Step 2 categories; `AC-1`/`AC-2`/`LOG-1` from
Step 2 requirements (refs taken from the document's own numbering); the three
controls and the `controls: [...]` links from Step 3; the top-level `id`, `name`,
`version`, `category`, `regions` from Step 1; and the whole file was assembled
and gate-checked in Step 4.

---

## Failure modes and what they look like

| Symptom | Cause | Fix |
|---|---|---|
| A whole section of the standard is missing | one chunk returned `[]` or its call failed | validation check 8; re-run that chunk |
| Requirements truncated mid-sentence | requirement straddled a chunk boundary | one-heading overlap + "keep longer description" merge |
| Two requirements merged into one | model collapsed adjacent numbered items | "one requirement per distinct obligation" rule; check 7 |
| Dozens of near-identical controls | `known_control_refs` not fed forward | pass earlier refs into every Step 3 batch |
| Empty response, HTTP 200 | thinking model consumed the whole token budget | raise `max_tokens` (see Global conventions) |
| Refs like `req-1`, `req-2` | document numbering ignored | reject and retry; refs must come from the document |
| Dangling `category` on a requirement | category appeared in another chunk | merge rule 3 (null it) or re-run with wider overlap |
