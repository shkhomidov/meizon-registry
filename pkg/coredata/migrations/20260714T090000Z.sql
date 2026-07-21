-- Copyright (c) 2026 Meizon Inc.
--
-- Universal framework hierarchy (categories → requirements → sections → items)
-- and relation-typed item cross-mappings with stub support, adapted from the
-- universal compliance framework template v2.0.0. All rows are tenant- and
-- framework-version-scoped; GID columns are TEXT (base64url); enums are TEXT
-- validated in Go. Legacy flat `controls` remain for v1 versions (dual read
-- path); new authoring uses this hierarchy.

CREATE TABLE requirement_categories (
  tenant_id            TEXT NOT NULL,
  id                   TEXT PRIMARY KEY,
  framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  code                 TEXT NOT NULL,
  name                 TEXT NOT NULL,
  description          TEXT NOT NULL DEFAULT '',
  is_optional          BOOLEAN NOT NULL DEFAULT FALSE,
  applicability_note   TEXT NOT NULL DEFAULT '',
  position             INTEGER NOT NULL DEFAULT 0,
  created_at           TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT categories_version_code_unique UNIQUE (framework_version_id, code)
);
CREATE INDEX categories_version_idx ON requirement_categories (framework_version_id);

CREATE TABLE requirements (
  tenant_id            TEXT NOT NULL,
  id                   TEXT PRIMARY KEY,
  framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  category_id          TEXT NOT NULL REFERENCES requirement_categories (id) ON DELETE CASCADE,
  code                 TEXT NOT NULL,
  number               TEXT NOT NULL DEFAULT '',
  title                TEXT NOT NULL,
  description          TEXT NOT NULL DEFAULT '',
  position             INTEGER NOT NULL DEFAULT 0,
  created_at           TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT requirements_version_code_unique UNIQUE (framework_version_id, code)
);
CREATE INDEX requirements_category_idx ON requirements (category_id);

CREATE TABLE sections (
  tenant_id            TEXT NOT NULL,
  id                   TEXT PRIMARY KEY,
  framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  requirement_id       TEXT NOT NULL REFERENCES requirements (id) ON DELETE CASCADE,
  code                 TEXT NOT NULL,
  title                TEXT NOT NULL,
  description          TEXT NOT NULL DEFAULT '',
  position             INTEGER NOT NULL DEFAULT 0,
  created_at           TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT sections_version_code_unique UNIQUE (framework_version_id, code)
);
CREATE INDEX sections_requirement_idx ON sections (requirement_id);

CREATE TABLE requirement_items (
  tenant_id               TEXT NOT NULL,
  id                      TEXT PRIMARY KEY,
  framework_version_id    TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  section_id              TEXT NOT NULL REFERENCES sections (id) ON DELETE CASCADE,
  code                    TEXT NOT NULL,
  title                   TEXT NOT NULL,
  description             TEXT NOT NULL DEFAULT '',
  item_type               TEXT NOT NULL DEFAULT 'control_requirement',
  legal_citation          TEXT NOT NULL DEFAULT '',
  validation_approaches   TEXT[] NOT NULL DEFAULT '{}',
  effective_from          TEXT NOT NULL DEFAULT '',
  retired_at              TEXT NOT NULL DEFAULT '',
  guidance                TEXT NOT NULL DEFAULT '',
  tags                    TEXT[] NOT NULL DEFAULT '{}',
  applicability_roles     TEXT[] NOT NULL DEFAULT '{}',
  applicability_condition TEXT NOT NULL DEFAULT '',
  position                INTEGER NOT NULL DEFAULT 0,
  created_at              TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT items_version_code_unique UNIQUE (framework_version_id, code)
);
CREATE INDEX items_section_idx ON requirement_items (section_id);
CREATE INDEX items_version_idx ON requirement_items (framework_version_id);
CREATE INDEX items_code_idx ON requirement_items (code);

-- Cross-mappings. Either target_item_id is set (resolved) or the pair
-- (target_framework_code, target_item_code) is set (stub). Resolution fills
-- target_item_id when the target framework publishes; is_resolved is derived.
CREATE TABLE item_cross_mappings (
  tenant_id                   TEXT NOT NULL,
  id                          TEXT PRIMARY KEY,
  source_item_id              TEXT NOT NULL REFERENCES requirement_items (id) ON DELETE CASCADE,
  source_framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  relation                    TEXT NOT NULL,
  target_item_id              TEXT REFERENCES requirement_items (id) ON DELETE SET NULL,
  target_framework_code       TEXT NOT NULL DEFAULT '',
  target_framework_version    TEXT NOT NULL DEFAULT '',
  target_item_code            TEXT NOT NULL DEFAULT '',
  is_resolved                 BOOLEAN GENERATED ALWAYS AS (target_item_id IS NOT NULL) STORED,
  notes                       TEXT NOT NULL DEFAULT '',
  created_at                  TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT xmap_target_check CHECK (
    target_item_id IS NOT NULL
    OR (target_framework_code <> '' AND target_item_code <> '')
  ),
  CONSTRAINT xmap_unique UNIQUE
    (source_item_id, relation, target_framework_code, target_item_code)
);
CREATE INDEX xmap_source_idx ON item_cross_mappings (source_item_id);
CREATE INDEX xmap_version_idx ON item_cross_mappings (source_framework_version_id);
CREATE INDEX xmap_target_idx ON item_cross_mappings (target_item_id);
CREATE INDEX xmap_stub_idx ON item_cross_mappings (target_framework_code, target_item_code)
  WHERE target_item_id IS NULL;
