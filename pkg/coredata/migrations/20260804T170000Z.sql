-- Copyright (c) 2026 Meizon Inc.
--
-- Audit templates become per-language.
--
-- A QA audit template was one row per framework version. To translate the audit
-- questions alongside the framework, each language gets its own template copy
-- (canonical is language ''), so the unique key moves from the version alone to
-- (version, language). Questions hang off template_id and are unaffected.

ALTER TABLE qa_templates ADD COLUMN language TEXT NOT NULL DEFAULT '';

ALTER TABLE qa_templates DROP CONSTRAINT qa_templates_version_unique;
ALTER TABLE qa_templates
    ADD CONSTRAINT qa_templates_version_lang_unique UNIQUE (framework_version_id, language);
