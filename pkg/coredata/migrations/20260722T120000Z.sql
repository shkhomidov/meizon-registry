-- Copyright (c) 2026 Meizon Inc.
--
-- QA audit templates (meizon-qa-template/v1).
--
-- An audit template is an ordered questionnaire generated from a PUBLISHED
-- framework's requirements. It is stored normalized so the console can edit and
-- reorder individual questions — a whole-document blob would make every edit a
-- read-modify-write of the entire template and lose row-level ordering.
--
-- Questions carry their section denormalized (a section is just ref+name+order),
-- avoiding a third table, and the type-specific payload — options, thresholds,
-- assessment rules, follow-ups — lives in a `body` JSONB rather than dozens of
-- sparse columns that differ by question type. The columns pulled out
-- (section, order, requirement_ref, type) are the ones queried, ordered by, or
-- used to regenerate only the questions for changed requirements after a
-- framework upgrade.

CREATE TABLE qa_templates (
  tenant_id             TEXT NOT NULL,
  id                    TEXT PRIMARY KEY,
  framework_version_id  TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  framework_ref         TEXT NOT NULL,
  title                 TEXT NOT NULL DEFAULT '',
  status                TEXT NOT NULL DEFAULT 'draft',
  generated_by          TEXT NOT NULL DEFAULT '',
  model                 TEXT NOT NULL DEFAULT '',
  -- Template-level config as authored/generated: rating scale, the deterministic
  -- verdict→score model, and question defaults.
  scale                 JSONB,
  verdict_model         JSONB,
  defaults              JSONB,
  created_at            TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at            TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT qa_templates_status_check CHECK (status IN ('draft', 'ready')),
  -- One template per framework version. Regeneration replaces its questions in
  -- place rather than accumulating duplicates.
  CONSTRAINT qa_templates_version_unique UNIQUE (framework_version_id)
);

CREATE TABLE qa_questions (
  tenant_id       TEXT NOT NULL,
  id              TEXT NOT NULL,
  template_id     TEXT NOT NULL REFERENCES qa_templates (id) ON DELETE CASCADE,
  section_ref     TEXT NOT NULL DEFAULT '',
  section_name    TEXT NOT NULL DEFAULT '',
  section_order   INTEGER NOT NULL DEFAULT 0,
  ord             INTEGER NOT NULL DEFAULT 0,
  requirement_ref TEXT NOT NULL DEFAULT '',
  control_ref     TEXT NOT NULL DEFAULT '',
  type            TEXT NOT NULL,
  -- Everything else about the question: text, intent, weight, conditional,
  -- expectedEvidence, the type-specific fields, assessment and follow-ups.
  body            JSONB NOT NULL,
  created_at      TIMESTAMP WITH TIME ZONE NOT NULL,
  PRIMARY KEY (template_id, id)
);

CREATE INDEX qa_questions_template_idx ON qa_questions (template_id, section_order, ord);
CREATE INDEX qa_questions_req_idx ON qa_questions (template_id, requirement_ref);
