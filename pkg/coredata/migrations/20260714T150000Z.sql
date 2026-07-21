-- Copyright (c) 2026 Meizon Inc.
--
-- LLM-assisted authoring: provider settings (API key encrypted at rest),
-- per-generation provenance records, and an origin marker (manual|ai|import)
-- on every hierarchy row so reviewers always know who authored what.

CREATE TABLE llm_settings (
  tenant_id         TEXT NOT NULL,
  id                TEXT PRIMARY KEY,
  provider          TEXT NOT NULL,               -- openai | anthropic | gemini
  encrypted_api_key BYTEA NOT NULL,
  model             TEXT NOT NULL,
  base_url          TEXT NOT NULL DEFAULT '',
  max_tokens        INTEGER NOT NULL DEFAULT 4096,
  updated_by        TEXT NOT NULL DEFAULT '',
  updated_at        TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT llm_settings_tenant_unique UNIQUE (tenant_id)
);

CREATE TABLE ai_generations (
  tenant_id            TEXT NOT NULL,
  id                   TEXT PRIMARY KEY,
  framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  step                 TEXT NOT NULL,             -- categories | requirements | items | mappings
  provider             TEXT NOT NULL,
  model                TEXT NOT NULL,
  prompt               TEXT NOT NULL,
  raw_output           TEXT NOT NULL,
  status               TEXT NOT NULL,             -- proposed | accepted | partially_accepted | rejected
  accepted_count       INTEGER NOT NULL DEFAULT 0,
  created_by           TEXT NOT NULL,
  created_at           TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX ai_generations_version_idx ON ai_generations (framework_version_id);

ALTER TABLE requirement_categories ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE requirements           ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE sections               ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE requirement_items      ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual';
