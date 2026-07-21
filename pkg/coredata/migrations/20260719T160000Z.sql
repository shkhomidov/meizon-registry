-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 13: the stored structure becomes Category → Requirement → Control.
--
-- The generator emits the flat meizon-framework/v2 shape, but the old importer
-- padded every requirement with exactly one synthetic section and one item
-- (measured on the 49-page CBU ingest: 142 requirements → 142 sections → 142
-- items, min 1 / max 1 at both levels). Those two levels carried no information
-- of their own, so this migration lifts everything item-addressed up onto the
-- requirement and removes the padding.
--
-- Requirement becomes the assessable leaf: it absorbs the item-level fields,
-- controls link to it directly, and cross-mappings address it.
--
-- The backfills below are lossless for any 1:1:1 data. Where a requirement
-- somehow held more than one item, the FIRST item (by position, then code)
-- wins for the scalar fields, while links and mappings from every item are
-- unioned onto the parent — a merge, never a silent drop. The assertion at the
-- end fails the migration loudly if that merge would have lost a distinct
-- obligation, rather than quietly changing the meaning of a framework.

-- 1. Requirement absorbs the item-level fields ------------------------------

ALTER TABLE requirements ADD COLUMN item_type               TEXT NOT NULL DEFAULT 'control_requirement';
ALTER TABLE requirements ADD COLUMN legal_citation          TEXT NOT NULL DEFAULT '';
ALTER TABLE requirements ADD COLUMN validation_approaches   TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE requirements ADD COLUMN effective_from          TEXT NOT NULL DEFAULT '';
ALTER TABLE requirements ADD COLUMN retired_at              TEXT NOT NULL DEFAULT '';
ALTER TABLE requirements ADD COLUMN guidance                TEXT NOT NULL DEFAULT '';
ALTER TABLE requirements ADD COLUMN tags                    TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE requirements ADD COLUMN applicability_roles     TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE requirements ADD COLUMN applicability_condition TEXT NOT NULL DEFAULT '';

-- First item per requirement, by authored order.
CREATE TEMP TABLE _lift AS
SELECT DISTINCT ON (s.requirement_id)
       s.requirement_id,
       ri.id   AS item_id,
       ri.description, ri.item_type, ri.legal_citation, ri.validation_approaches,
       ri.effective_from, ri.retired_at, ri.guidance, ri.tags,
       ri.applicability_roles, ri.applicability_condition
FROM   requirement_items ri
JOIN   sections s ON s.id = ri.section_id
ORDER  BY s.requirement_id, ri.position ASC, ri.code ASC;

UPDATE requirements r
SET    item_type               = l.item_type,
       legal_citation          = l.legal_citation,
       validation_approaches   = l.validation_approaches,
       effective_from          = l.effective_from,
       retired_at              = l.retired_at,
       guidance                = l.guidance,
       tags                    = l.tags,
       applicability_roles     = l.applicability_roles,
       applicability_condition = l.applicability_condition,
       -- The obligation text lives on both today; keep the item's copy only
       -- where the requirement has none, so an edited requirement is not
       -- reverted to its generated original.
       description             = CASE WHEN r.description = '' THEN l.description ELSE r.description END
FROM   _lift l
WHERE  r.id = l.requirement_id;

-- 2. Controls link to requirements ------------------------------------------

CREATE TABLE control_requirements (
  tenant_id      TEXT NOT NULL,
  control_id     TEXT NOT NULL REFERENCES control_library (id) ON DELETE CASCADE,
  requirement_id TEXT NOT NULL REFERENCES requirements (id) ON DELETE CASCADE,
  PRIMARY KEY (control_id, requirement_id)
);
CREATE INDEX cr_requirement_idx ON control_requirements (requirement_id);

-- Union of every item's links onto the parent requirement.
INSERT INTO control_requirements (tenant_id, control_id, requirement_id)
SELECT DISTINCT cri.tenant_id, cri.control_id, s.requirement_id
FROM   control_requirement_items cri
JOIN   requirement_items ri ON ri.id = cri.item_id
JOIN   sections s           ON s.id = ri.section_id
ON CONFLICT DO NOTHING;

-- 3. Cross-mappings address requirements ------------------------------------
--
-- Same stub design as before: either target_requirement_id is set (resolved)
-- or the (code, code) pair identifies a target that is not loaded yet. Signed
-- content stores only codes, so resolution still never changes signed bytes.
--
-- Unlike the old table, the unique constraint now includes
-- target_framework_version: two mappings to the same target requirement pinned
-- at different versions are legitimately distinct rows, and omitting the column
-- made the second one fail as a duplicate.

CREATE TABLE requirement_cross_mappings (
  tenant_id                   TEXT NOT NULL,
  id                          TEXT PRIMARY KEY,
  source_requirement_id       TEXT NOT NULL REFERENCES requirements (id) ON DELETE CASCADE,
  source_framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  relation                    TEXT NOT NULL,
  target_requirement_id       TEXT REFERENCES requirements (id) ON DELETE SET NULL,
  target_framework_code       TEXT NOT NULL DEFAULT '',
  target_framework_version    TEXT NOT NULL DEFAULT '',
  target_requirement_code     TEXT NOT NULL DEFAULT '',
  is_resolved                 BOOLEAN GENERATED ALWAYS AS (target_requirement_id IS NOT NULL) STORED,
  notes                       TEXT NOT NULL DEFAULT '',
  created_at                  TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT rxmap_target_check CHECK (
    target_requirement_id IS NOT NULL OR (target_framework_code <> '' AND target_requirement_code <> '')
  ),
  CONSTRAINT rxmap_unique UNIQUE (
    source_requirement_id, relation, target_framework_code, target_framework_version, target_requirement_code
  )
);
CREATE INDEX rxmap_source_idx  ON requirement_cross_mappings (source_requirement_id);
CREATE INDEX rxmap_version_idx ON requirement_cross_mappings (source_framework_version_id);
CREATE INDEX rxmap_target_idx  ON requirement_cross_mappings (target_requirement_id);
CREATE INDEX rxmap_stub_idx    ON requirement_cross_mappings (target_framework_code, target_requirement_code)
  WHERE target_requirement_id IS NULL;

-- Carry mappings over, repointing both ends at requirements. The target item
-- code becomes the target requirement code: for anything the flat pipeline
-- created they are the same string, and a hand-authored mapping to a genuinely
-- item-level code simply stays an unresolved stub — visible in the admin
-- unresolved view, which is the correct outcome rather than a wrong link.
INSERT INTO requirement_cross_mappings (
  tenant_id, id, source_requirement_id, source_framework_version_id, relation,
  target_requirement_id, target_framework_code, target_framework_version,
  target_requirement_code, notes, created_at
)
SELECT DISTINCT ON (ss.requirement_id, x.relation, x.target_framework_code, x.target_framework_version, x.target_item_code)
       x.tenant_id, x.id, ss.requirement_id, x.source_framework_version_id, x.relation,
       ts.requirement_id, x.target_framework_code, x.target_framework_version,
       x.target_item_code, x.notes, x.created_at
FROM   item_cross_mappings x
JOIN   requirement_items sri ON sri.id = x.source_item_id
JOIN   sections ss           ON ss.id = sri.section_id
LEFT   JOIN requirement_items tri ON tri.id = x.target_item_id
LEFT   JOIN sections ts           ON ts.id = tri.section_id
ORDER  BY ss.requirement_id, x.relation, x.target_framework_code,
          x.target_framework_version, x.target_item_code, x.created_at ASC;

-- 4. Assert the lift was lossless -------------------------------------------
--
-- A requirement holding two items with DIFFERENT obligation text means the
-- padding was not padding, and collapsing it would silently discard a real
-- obligation. Refuse to migrate rather than corrupt the framework.
DO $$
DECLARE lost INTEGER;
BEGIN
  SELECT COUNT(*) INTO lost FROM (
    SELECT s.requirement_id
    FROM   requirement_items ri
    JOIN   sections s ON s.id = ri.section_id
    WHERE  ri.description <> ''
    GROUP  BY s.requirement_id
    HAVING COUNT(DISTINCT ri.description) > 1
  ) x;
  IF lost > 0 THEN
    RAISE EXCEPTION
      'cannot collapse sections/items: % requirement(s) hold multiple distinct obligations; migrate them by hand first', lost;
  END IF;
END $$;

-- 5. Drop the padding --------------------------------------------------------

DROP TABLE control_requirement_items;
DROP TABLE item_cross_mappings;
DROP TABLE requirement_items;
DROP TABLE sections;
