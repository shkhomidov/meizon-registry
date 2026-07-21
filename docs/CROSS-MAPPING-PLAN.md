# Plan — Universal Framework Structure & Cross-Mapping

> Source: `framework-db-schema.sql` (Universal Compliance Framework template v2.0.0,
> PostgreSQL). Target: the Meizon Registry (`go.meizon.cloud/registry`). This plan
> adapts the template's hierarchy and its **stub-capable cross-mapping** design to
> the registry's existing architecture: GID/tenant coredata, immutable **signed**
> framework versions, the `fwschema` exchange contract, and the lifecycle
> (DRAFT → IN_REVIEW → APPROVED → PUBLISHED).

---

## 0. What the template gives us vs. what the registry has

| Template (SQL) | Registry today | Gap |
|---|---|---|
| `framework` (rich metadata: kind, scoring, audit/cert block) | `frameworks` (reference, name, region, authority, license, public) | metadata enrichment (deferrable) |
| `requirement_category → requirement → section → requirement_item` (4-level) | flat `controls` (ref, name, section string, parent ref) | **real hierarchy** |
| `item_cross_mapping` with **stubs** (`target_framework_code/target_item_code`) + `resolve_cross_mapping_stubs()` | `controls.mappings` JSONB (code pairs, opaque, unqueryable) | **first-class mapping graph + resolver** |
| `control_library`, profiles, maturity scale, assessment procedures, evidence/policy refs | — | deferrable catalogs |
| TEXT PKs (`fw_pci_dss_v4_0_1`), PG `ENUM` types, plpgsql function | GID PKs + `tenant_id` Scoper, Go string enums + `IsValid()`, SQL lives in coredata | adapt, don't adopt verbatim |

## 1. Design decisions (settle these first)

**D1 — Adapt to house conventions, keep the template's semantics.**
GID primary keys + `tenant_id` on every table (Scoper injected); Go `type X string`
enums with `IsValid()` instead of PG `ENUM`; the template's plpgsql resolver becomes
a single `UPDATE … FROM` inside a coredata method (SQL never leaves coredata; unit-testable;
no DB functions to migrate). Human codes (`PCI_DSS`+`4.0.1`, item `7.2.1`) remain the
**natural keys used for stub matching**, exactly as in the template.

**D2 — Immutability vs. resolution (the key reconciliation).**
Published versions are immutable and ed25519-signed, but stub resolution mutates state.
Resolution of the conflict:
- The **signed content** stores mappings **by code only** — `(relation, targetFrameworkCode,
  targetVersion?, targetItemCode, notes)`. Codes are stable; signed bytes never change.
- The **mapping table** (`item_cross_mappings`) is *derived, re-computable linkage state*:
  rows are (re)generated from the signed content at publish time, and `target_item_id`
  is filled by the resolver. Re-running the resolver is idempotent and never touches
  signed bytes. `is_resolved` is a generated column, as in the template.

**D3 — Mapping ownership & version semantics.**
A mapping belongs to the **source framework version** (authored on the draft, locked at
publish). Targets: a stub may pin `targetVersion` or leave it open. The resolver links
open stubs to items of the **latest published** target version and re-runs on every
publish (both directions: new version resolves its own stubs *and* satisfies other
frameworks' stubs pointing at its code). Pinned stubs only match the pinned version.

**D4 — Hierarchy: 4 typed tables, version-scoped.**
Mirror the template with distinct `requirement_categories / requirements / sections /
requirement_items` tables (typed fields like `validation_approaches`, `legal_citation`,
`effective_from` live only where they belong). Every row carries `tenant_id` **and
`framework_version_id`** (denormalized, per the coredata rule that authorization
attributes avoid JOINs). The template's 1:1 `item_applicability` is folded into
`requirement_items` as nullable `applicability_roles TEXT[]` / `applicability_condition`
(deviation noted; avoids a 1-row satellite table).

**D5 — Exchange schema v2 (`fwschema`).**
`schemaVersion: "2.0"` adds the nested hierarchy and structured mappings; v1 bundles
remain parseable/verifiable (switch on `schemaVersion`). `Flatten()` still emits the flat
GRC seed (walk items in order → `{id,name,description}`), so the GRC import path is
untouched. Canonicalization/signing mechanics unchanged.

**D6 — Migration of existing data.**
One-time migration synthesizes the hierarchy for existing versions: one `GENERAL`
category → one requirement per distinct `controls.section` → one section → items from
controls (codes preserved). `controls.mappings` JSONB becomes stub rows. A follow-up
migration retires the `controls` table once a round-trip test proves the seed output is
byte-identical.

## 2. Phases

### Phase 1 — Hierarchy data model (backend)
1. **Migration `2`**: create
   - `requirement_categories(tenant_id, id, framework_version_id, code, name, description, parent_category_id, sort_order, is_optional, applicability_note)` — `UNIQUE(framework_version_id, code)`
   - `requirements(…, category_id, code, number, title, description, sort_order)`
   - `sections(…, requirement_id, code, title, description, parent_section_id, sort_order)`
   - `requirement_items(…, section_id, code, title, description, item_type, sort_order, legal_citation, validation_approaches TEXT[], effective_from, retired_at, guidance, tags TEXT[], applicability_roles TEXT[], applicability_condition)` — `UNIQUE(framework_version_id, code)`
   - `assessment_procedures(…, item_id, code, description, sort_order)` — `UNIQUE(item_id, code)`
2. **Entity types** 12–17 in `entity_type_reg.go` (+ `NewEntityFromID` cases):
   RequirementCategory, Requirement, Section, RequirementItem, AssessmentProcedure, ItemCrossMapping.
3. **coredata** one file per entity following `control.go`'s shape (Insert / LoadAllByVersion /
   Delete / Count). Enums: `ItemType`, `MappingRelation` (`equivalent|partial|superset|subset`)
   as Go string enums.
4. **Service**: `AddCategory/AddRequirement/AddSection/AddItem` (draft-only, region-authorized,
   same authz actions as controls today) + bulk `ImportStructure` (see Phase 3 import).
5. **Data migration** per D6 + seed/flatten byte-compat test.

### Phase 2 — Cross-mapping core (the centerpiece)
1. **Migration `3`**: `item_cross_mappings`
   ```sql
   tenant_id TEXT NOT NULL,
   id TEXT PRIMARY KEY,
   source_item_id TEXT NOT NULL REFERENCES requirement_items(id) ON DELETE CASCADE,
   source_framework_version_id TEXT NOT NULL,           -- denormalized for scoping
   relation TEXT NOT NULL,                              -- equivalent|partial|superset|subset
   target_item_id TEXT REFERENCES requirement_items(id) ON DELETE SET NULL,
   target_framework_code TEXT, target_framework_version TEXT, target_item_code TEXT,
   is_resolved BOOLEAN GENERATED ALWAYS AS (target_item_id IS NOT NULL) STORED,
   notes TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL,
   CHECK (target_item_id IS NOT NULL OR (target_framework_code IS NOT NULL AND target_item_code IS NOT NULL)),
   UNIQUE (source_item_id, relation, target_framework_code, target_item_code)
   ```
   + `idx_xmap_source`, `idx_xmap_target`, partial `idx_xmap_stub … WHERE target_item_id IS NULL` (as template).
2. **coredata `item_cross_mapping.go`**:
   - CRUD + `LoadBySourceItem`, `LoadByVersion`, `LoadUnresolved(limit)`
   - `ResolveForFramework(frameworkCode, version, versionID)` — the template's
     `resolve_cross_mapping_stubs` as one `UPDATE … FROM requirement_items …` (join is one
     table now — items carry `framework_version_id`), version-pinning honored, returns count.
   - `CoverageByVersionPair(sourceVersionID, targetCode)` — counts per relation + unresolved.
3. **Service `mapping_service.go`**:
   - `AddItemMapping(actor, itemID, relation, targetCode, targetVersion?, targetItemCode, notes)` —
     DRAFT-only, validates relation/codes, region-authorized (reuses `ActionControlEdit`).
   - `RemoveItemMapping`.
   - `ResolveStubs(actor?, frameworkVersionID)` — called **inside the Publish transaction**
     after signing: (a) materialize this version's mapping rows from its items,
     (b) resolve its own stubs against already-published frameworks,
     (c) resolve *other* frameworks' stubs that point at this framework's code.
     Audit rows `mapping.materialize` / `mapping.resolve (n=…)`.
4. **`fwschema` v2**: nested `categories → requirements → sections → items`, each item with
   `mappings: [{relation, framework, version?, item, notes?}]`; `Validate()` v2 rules
   (unique codes per level, known relations, resolvable parents); `Flatten()` walks items;
   v1 kept behind `schemaVersion` switch. Round-trip + tamper tests extended.
5. **Publish flow** (`lifecycle_service.Publish`): assemble v2 bundle from hierarchy →
   validate → sign → store → `ResolveStubs` (same tx).

### Phase 3 — Surfaces
1. **Console** (design-system UI):
   - Framework detail: hierarchy tree editor (category/req/section/item CRUD in DRAFT),
     item drawer with **Mappings** panel (add stub by code; show resolved ✓ / stub ◌ badge).
   - New **Coverage** page: pick source framework/version × target framework → matrix of
     relation counts (`equivalent/partial/superset/subset`), % items mapped, unresolved list.
   - Admin: **Unresolved stubs** table + "Resolve now" action.
2. **APIs**:
   - Console: `GET/POST /frameworks/{ref}/items/{code}/mappings`, `GET /coverage?source=&target=`,
     `POST /admin/mappings/resolve`.
   - Distribution (`/api/registry/v1`): `GET /frameworks/{id}/versions/{v}/mappings[?target=CODE]`;
     bundles already carry mappings as signed content — consumers get them for free.
3. **UI-first mandate (decision)**: every process is managed in the console UI — structure
   authoring, **framework import (JSON upload dialog)**, mapping authoring, stub
   resolution and coverage. `registryctl` remains an ops fallback only and gains no new
   mapping commands in this phase.
4. **distclient/GRC**: verified bundles expose `Mappings()`; a GRC can compute
   "adopting ISO 27001 — you already satisfy N controls via PCI DSS".

### Phase 4 — Deferred template features (separate workstream)
`control_library` + `control_requirement_item` + control-level cross-mapping;
`profiles`/`item_profile` (SAQ, SoA); maturity scale + per-item expectations;
`evidence_guidance` + `policy_template` refs; framework metadata block
(`kind`, `scoring_model`, audit/cert cadence, validation methods, regions M:N).
All follow the same coredata/entity-type/exchange-v2.x extension pattern; none block
item-level cross-mapping.

## 3. Acceptance criteria
1. Import the template's §11 PCI slice → publish → 6 stub mappings stored, `is_resolved=false`.
2. Import an ISO 27001 slice (`A.5.15`, `A.5.18`) → publish → resolver fills
   `target_item_id` on the PCI→ISO stubs; SOC 2 / NIST stubs stay unresolved; audit logged.
3. Version pinning: stub pinned `ISO27001@2022` does **not** resolve to a later `2025` publish;
   unpinned stubs re-resolve to the latest published version.
4. Signed v2 bundle verifies; a tampered mapping (relation flipped) is rejected;
   resolution never changes `contentHash`.
5. Coverage endpoint returns correct per-relation counts and reverse (ISO→PCI) lookups.
6. `Flatten()` of a v2 bundle imports into the GRC unchanged (byte-stable seed);
   migrated legacy versions produce identical seeds pre/post migration.
7. Mappings editable in DRAFT only; RBAC/region/SoD unchanged; full suite green.

## 4. Sequencing & effort
- **Phase 1 + 2** are one backend increment (~migration ×2, ~7 coredata files, 1 service,
  fwschema v2 + tests) — deliverable and verifiable via CLI alone.
- **Phase 3** rides on them (console + endpoints + import command).
- **Phase 4** is optional depth, feature-by-feature.

Out of scope (unchanged from project charter): tenant-side state (the template explicitly
excludes it too), GraphQL refactor, probo-side changes.
