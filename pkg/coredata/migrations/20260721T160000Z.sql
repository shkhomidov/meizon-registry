-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 16 (§2): cross-mappings become their own signed, moderated artifact.
--
-- THE PROBLEM
-- -----------
-- Cross-mappings lived inside the source framework's signed bundle. Two
-- consequences, both bad:
--
--   1. Connecting a newly published framework B to an existing A required
--      re-versioning, re-approving and re-publishing A. Publishing framework
--      N+1 meant republishing all N others — O(N²) full moderation cycles.
--
--   2. Worse, and the reason there is no compatible half-measure: the
--      distributed bundle is RE-ASSEMBLED FROM LIVE TABLES on every request
--      while the signature served with it is the stored one. Editing a mapping
--      after publication therefore changes the assembled bytes, the content
--      hash stops matching the stored signature, and every consumer rejects the
--      bundle as tampered. Mappings could never be edited post-publication
--      without breaking verification.
--
-- So mappings move out of the framework bundle and into a `mapping_sets`
-- artifact with its own publish gate and its own signature.
--
-- WHY THE BUNDLE FORMAT IS VERSIONED RATHER THAN CHANGED
-- -----------------------------------------------------
-- Versions already published were signed over v2 content, mappings included.
-- Re-assembling them under the new rules would compute a different hash and
-- invalidate a signature that was a promise to whoever already pulled it. So
-- each version records the schema it was signed under, and assembly branches on
-- that: existing rows keep emitting v2 with mappings, everything published from
-- now on emits v3 without them. A published hash is never recomputed.

ALTER TABLE framework_versions
  ADD COLUMN bundle_schema TEXT NOT NULL DEFAULT '2.0';

-- Anything already PUBLISHED was signed under v2 and must stay that way. Drafts
-- have no signature to protect, so they will be published as v3.
UPDATE framework_versions SET bundle_schema = '2.0'
WHERE status IN ('PUBLISHED', 'DEPRECATED');

UPDATE framework_versions SET bundle_schema = '3.0'
WHERE status NOT IN ('PUBLISHED', 'DEPRECATED');

-- A mapping set is the set of cross-mappings from one source framework version
-- to one target framework at one version. It is signed and distributed on its
-- own, so adding a mapping never touches the frameworks it connects.
CREATE TABLE mapping_sets (
  tenant_id                   TEXT NOT NULL,
  id                          TEXT PRIMARY KEY,
  source_framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  -- The target is addressed by code, exactly as the mappings themselves are, so
  -- a set can be authored and published before the target is loaded here.
  target_framework_code       TEXT NOT NULL,
  target_framework_version    TEXT NOT NULL,
  status                      TEXT NOT NULL DEFAULT 'DRAFT',
  content_hash                TEXT NOT NULL DEFAULT '',
  signature                   BYTEA,
  key_id                      TEXT NOT NULL DEFAULT '',
  -- Counts frozen at publication, so a catalogue listing does not have to
  -- re-assemble the artifact to say how big it is.
  requirement_mapping_count   INTEGER NOT NULL DEFAULT 0,
  control_mapping_count       INTEGER NOT NULL DEFAULT 0,
  author_id                   TEXT NOT NULL DEFAULT '',
  approved_by                 TEXT NOT NULL DEFAULT '',
  approved_at                 TIMESTAMP WITH TIME ZONE,
  published_by                TEXT NOT NULL DEFAULT '',
  published_at                TIMESTAMP WITH TIME ZONE,
  created_at                  TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at                  TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT mapping_sets_status_check
    CHECK (status IN ('DRAFT', 'IN_REVIEW', 'APPROVED', 'PUBLISHED', 'DEPRECATED'))
);

-- One set per (source version → target framework@version). A second set for the
-- same pair would leave consumers with two signed artifacts disagreeing about
-- the same relationship and no rule for choosing.
CREATE UNIQUE INDEX mapping_sets_pair_unique
  ON mapping_sets (source_framework_version_id, target_framework_code, target_framework_version);

CREATE INDEX mapping_sets_source_idx ON mapping_sets (source_framework_version_id);
CREATE INDEX mapping_sets_published_idx ON mapping_sets (target_framework_code, status);

-- The change feed learns two new event kinds. Rebuilt rather than altered
-- because Postgres cannot widen a CHECK constraint in place.
ALTER TABLE distribution_events DROP CONSTRAINT distribution_events_kind_check;
ALTER TABLE distribution_events ADD CONSTRAINT distribution_events_kind_check
  CHECK (kind IN ('published', 'deprecated', 'mapping_published', 'mapping_deprecated'));

-- A mapping event names the target alongside the source, so a consumer can tell
-- whether it holds both ends before fetching.
ALTER TABLE distribution_events
  ADD COLUMN target_framework_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN target_version       TEXT NOT NULL DEFAULT '';
