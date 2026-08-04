-- Copyright (c) 2026 Meizon Inc.
--
-- Distribute audit templates and translations.
--
-- The change feed announced framework and mapping publications; now a consumer
-- can also learn when a published version's audit (QA) template became available
-- or its translations changed, so it can pull those over the distribution API
-- rather than polling. Two new event kinds are added to the constraint.

ALTER TABLE distribution_events DROP CONSTRAINT distribution_events_kind_check;
ALTER TABLE distribution_events ADD CONSTRAINT distribution_events_kind_check
    CHECK (kind IN (
        'published', 'deprecated',
        'mapping_published', 'mapping_deprecated',
        'qa_published', 'translation_published'
    ));
