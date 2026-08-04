-- Copyright (c) 2026 Meizon Inc.
--
-- Framework ownership.
--
-- Auditors are scoped to the frameworks they created; that needs a creator on
-- the framework row. Authorship was only recorded per version
-- (framework_versions.author_id), so this denormalizes the creator onto the
-- framework for a cheap "is this mine" check and an owner-filtered listing.
--
-- created_by holds a gid (stored as text, like author_id). It is backfilled from
-- each framework's EARLIEST version author — the identity that first drafted it.
-- New rows set it at insert time. A framework with no versions (should not occur)
-- keeps the empty default and is treated as owned by no one.

ALTER TABLE frameworks ADD COLUMN created_by TEXT NOT NULL DEFAULT '';

UPDATE frameworks f
SET created_by = sub.author_id
FROM (
    SELECT DISTINCT ON (framework_id) framework_id, author_id
    FROM framework_versions
    ORDER BY framework_id, created_at ASC
) sub
WHERE sub.framework_id = f.id;

CREATE INDEX IF NOT EXISTS frameworks_created_by_idx ON frameworks (tenant_id, created_by);

-- Deleting a framework must cascade. Three FKs were created without ON DELETE
-- CASCADE (framework_versions -> frameworks, controls/approvals ->
-- framework_versions); recreate them with cascade so a superadmin delete removes
-- the framework and everything hanging off it. All other dependants
-- (requirements, cross-mappings, qa_templates, ...) already cascade from
-- framework_versions.
ALTER TABLE framework_versions
    DROP CONSTRAINT framework_versions_framework_id_fkey,
    ADD CONSTRAINT framework_versions_framework_id_fkey
        FOREIGN KEY (framework_id) REFERENCES frameworks (id) ON DELETE CASCADE;

ALTER TABLE controls
    DROP CONSTRAINT controls_framework_version_id_fkey,
    ADD CONSTRAINT controls_framework_version_id_fkey
        FOREIGN KEY (framework_version_id) REFERENCES framework_versions (id) ON DELETE CASCADE;

ALTER TABLE approvals
    DROP CONSTRAINT approvals_framework_version_id_fkey,
    ADD CONSTRAINT approvals_framework_version_id_fkey
        FOREIGN KEY (framework_version_id) REFERENCES framework_versions (id) ON DELETE CASCADE;
