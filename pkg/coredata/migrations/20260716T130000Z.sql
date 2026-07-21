-- Copyright (c) 2026 Meizon Inc.
--
-- Template catalogs (universal template §6–7): the implementable control
-- library (shareable across items), evidence guidance per control, and policy
-- templates with a markdown body (our extension — the template stores only a
-- name/ref). Catalog rows are registry-side annotations, not signed version
-- content; origin tracks manual|ai|import provenance.

CREATE TABLE control_library (
  tenant_id    TEXT NOT NULL,
  id           TEXT PRIMARY KEY,
  framework_id TEXT REFERENCES frameworks (id) ON DELETE CASCADE, -- NULL = shared/global
  code         TEXT NOT NULL,
  name         TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  domain       TEXT NOT NULL DEFAULT '',
  tags         TEXT[] NOT NULL DEFAULT '{}',
  origin       TEXT NOT NULL DEFAULT 'manual',
  created_at   TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT control_library_code_unique UNIQUE (tenant_id, code)
);
CREATE INDEX control_library_framework_idx ON control_library (framework_id);

CREATE TABLE control_requirement_items (
  tenant_id  TEXT NOT NULL,
  control_id TEXT NOT NULL REFERENCES control_library (id) ON DELETE CASCADE,
  item_id    TEXT NOT NULL REFERENCES requirement_items (id) ON DELETE CASCADE,
  PRIMARY KEY (control_id, item_id)
);
CREATE INDEX cri_item_idx ON control_requirement_items (item_id);

CREATE TABLE policy_templates (
  tenant_id    TEXT NOT NULL,
  id           TEXT PRIMARY KEY,
  framework_id TEXT REFERENCES frameworks (id) ON DELETE CASCADE, -- NULL = shared/global
  name         TEXT NOT NULL,
  body         TEXT NOT NULL DEFAULT '',
  origin       TEXT NOT NULL DEFAULT 'manual',
  created_at   TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX policy_templates_framework_idx ON policy_templates (framework_id);

CREATE TABLE policy_template_controls (
  tenant_id          TEXT NOT NULL,
  policy_template_id TEXT NOT NULL REFERENCES policy_templates (id) ON DELETE CASCADE,
  control_id         TEXT NOT NULL REFERENCES control_library (id) ON DELETE CASCADE,
  PRIMARY KEY (policy_template_id, control_id)
);

CREATE TABLE evidence_guidance (
  tenant_id              TEXT NOT NULL,
  id                     TEXT PRIMARY KEY,
  control_id             TEXT NOT NULL REFERENCES control_library (id) ON DELETE CASCADE,
  type                   TEXT NOT NULL, -- automated_test|document|policy|interview|observation
  hint                   TEXT NOT NULL DEFAULT '',
  renewal_cadence_months INTEGER,
  policy_template_id     TEXT REFERENCES policy_templates (id) ON DELETE SET NULL,
  origin                 TEXT NOT NULL DEFAULT 'manual',
  created_at             TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX evidence_control_idx ON evidence_guidance (control_id);
