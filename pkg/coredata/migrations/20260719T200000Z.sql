-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 15: LLM-assisted cross-mapping.
--
-- Three additions:
--   1. control_cross_mappings — controls become mappable in their own right,
--      with the same code-addressed stub design as requirements.
--   2. mapping_proposals    — nothing the model produces is committed. Proposals
--      carry a confidence and a rationale and wait for an auditor.
--   3. mapping_runs         — the ledger that makes re-running cheap and stops
--      the same pair being paid for twice.
--
-- Mappings address nodes by REF, never by row id. A stub must be able to name a
-- target in a framework that is not loaded yet (no row exists), row ids are
-- per-version, and signed bundle content stores codes only — which is exactly
-- what lets resolution fill in the row id later without changing signed bytes.

-- 1. Control-to-control mappings --------------------------------------------

CREATE TABLE control_cross_mappings (
  tenant_id                   TEXT NOT NULL,
  id                          TEXT PRIMARY KEY,
  source_control_id           TEXT NOT NULL REFERENCES control_library (id) ON DELETE CASCADE,
  source_framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  relation                    TEXT NOT NULL,
  target_control_id           TEXT REFERENCES control_library (id) ON DELETE SET NULL,
  target_framework_code       TEXT NOT NULL DEFAULT '',
  target_framework_version    TEXT NOT NULL DEFAULT '',
  target_control_code         TEXT NOT NULL DEFAULT '',
  is_resolved                 BOOLEAN GENERATED ALWAYS AS (target_control_id IS NOT NULL) STORED,
  confidence                  REAL NOT NULL DEFAULT 0,
  rationale                   TEXT NOT NULL DEFAULT '',
  review_state                TEXT NOT NULL DEFAULT 'current',
  notes                       TEXT NOT NULL DEFAULT '',
  created_at                  TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT cxmap_target_check CHECK (
    target_control_id IS NOT NULL OR (target_framework_code <> '' AND target_control_code <> '')
  ),
  CONSTRAINT cxmap_unique UNIQUE (
    source_control_id, relation, target_framework_code, target_framework_version, target_control_code
  ),
  CONSTRAINT cxmap_review_check CHECK (review_state IN ('current', 'needs_review', 'orphaned'))
);
CREATE INDEX cxmap_source_idx  ON control_cross_mappings (source_control_id);
CREATE INDEX cxmap_version_idx ON control_cross_mappings (source_framework_version_id);
CREATE INDEX cxmap_stub_idx    ON control_cross_mappings (target_framework_code, target_control_code)
  WHERE target_control_id IS NULL;

-- Requirement mappings gain the same review/confidence columns, so a mapping
-- keeps the reasoning that justified it. When a target version changes, "this
-- was a 0.55 partial match because X" is what tells an auditor whether to look
-- again — without it, re-review is guesswork.
ALTER TABLE requirement_cross_mappings ADD COLUMN confidence   REAL NOT NULL DEFAULT 0;
ALTER TABLE requirement_cross_mappings ADD COLUMN rationale    TEXT NOT NULL DEFAULT '';
ALTER TABLE requirement_cross_mappings ADD COLUMN review_state TEXT NOT NULL DEFAULT 'current';
ALTER TABLE requirement_cross_mappings ADD CONSTRAINT rxmap_review_check
  CHECK (review_state IN ('current', 'needs_review', 'orphaned'));

-- 2. Proposals ---------------------------------------------------------------
--
-- The model proposes; an auditor decides. Rejected rows are KEPT: they are what
-- stops the next run re-proposing something a human already turned down.

CREATE TABLE mapping_proposals (
  tenant_id                   TEXT NOT NULL,
  id                          TEXT PRIMARY KEY,
  source_framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  node_kind                   TEXT NOT NULL,            -- requirement | control
  source_ref                  TEXT NOT NULL,
  target_framework_code       TEXT NOT NULL,
  target_framework_version    TEXT NOT NULL DEFAULT '',
  target_ref                  TEXT NOT NULL,
  relation                    TEXT NOT NULL,
  confidence                  REAL NOT NULL DEFAULT 0,
  rationale                   TEXT NOT NULL DEFAULT '',
  status                      TEXT NOT NULL DEFAULT 'pending',
  decided_by                  TEXT NOT NULL DEFAULT '',
  created_at                  TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT proposals_unique UNIQUE (
    source_framework_version_id, node_kind, source_ref, target_framework_code, target_ref, relation
  ),
  CONSTRAINT proposals_kind_check   CHECK (node_kind IN ('requirement', 'control')),
  CONSTRAINT proposals_status_check CHECK (status IN ('pending', 'accepted', 'rejected'))
);
CREATE INDEX proposals_version_status_idx ON mapping_proposals (source_framework_version_id, status);

-- 3. The run ledger ----------------------------------------------------------
--
-- adjudicated_refs records every source ref the model actually considered, not
-- just the ones that produced a mapping. Without it a re-run re-pays for every
-- requirement the model correctly said "no match" for — which is most of them.
--
-- It is also what lets the UI distinguish "not mapped" from "considered, no
-- match". Conflating those two makes an auditor see a gap and re-run forever.

CREATE TABLE mapping_runs (
  tenant_id                   TEXT NOT NULL,
  id                          TEXT PRIMARY KEY,
  source_framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  target_framework_code       TEXT NOT NULL,
  target_framework_version    TEXT NOT NULL DEFAULT '',
  node_kind                   TEXT NOT NULL DEFAULT 'requirement',
  adjudicated_refs            TEXT[] NOT NULL DEFAULT '{}',
  pairs_considered            INTEGER NOT NULL DEFAULT 0,
  proposed                    INTEGER NOT NULL DEFAULT 0,
  model                       TEXT NOT NULL DEFAULT '',
  completed_at                TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT mapping_runs_unique UNIQUE (
    source_framework_version_id, target_framework_code, target_framework_version, node_kind
  )
);
CREATE INDEX mapping_runs_version_idx ON mapping_runs (source_framework_version_id);

-- Cross-mapping adjudication is its own pipeline step, editable in Settings
-- alongside identify / extract / controls / qa / translate.
ALTER TABLE llm_settings ADD COLUMN mapping_instruction TEXT NOT NULL DEFAULT '';
