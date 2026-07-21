-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 14: multi-language frameworks. English is the canonical language.
--
-- A framework's STRUCTURE — refs, category assignment, control links,
-- cross-mappings — exists exactly once, in the canonical record. Every other
-- language is a text-only overlay: it supplies name/description strings keyed
-- by ref and can add, remove, reorder or re-link nothing.
--
-- Overlays are keyed by REF, never by position. Ordinal keying would look
-- equivalent and silently corrupt: inserting one requirement shifts every later
-- index, re-pointing each subsequent translation onto the wrong obligation with
-- no error at all. Refs survive insertion, reordering and editing.

-- Source language of the authored content (the document it was generated from).
-- Empty means unknown/legacy; 'en' is the canonical target everything maps in.
ALTER TABLE frameworks ADD COLUMN source_language TEXT NOT NULL DEFAULT '';

-- One row per translated node. node_kind distinguishes the two ref spaces —
-- a requirement and a control may legitimately share a ref string.
CREATE TABLE framework_translations (
  tenant_id            TEXT NOT NULL,
  id                   TEXT PRIMARY KEY,
  framework_version_id TEXT NOT NULL REFERENCES framework_versions (id) ON DELETE CASCADE,
  language             TEXT NOT NULL,               -- ISO-639-1 / BCP-47
  node_kind            TEXT NOT NULL,               -- framework | category | requirement | control
  node_ref             TEXT NOT NULL DEFAULT '',    -- '' for the framework itself
  name                 TEXT NOT NULL DEFAULT '',
  description          TEXT NOT NULL DEFAULT '',
  origin               TEXT NOT NULL DEFAULT 'ai',  -- ai | manual
  created_at           TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT translations_unique UNIQUE (framework_version_id, language, node_kind, node_ref),
  CONSTRAINT translations_kind_check CHECK (node_kind IN ('framework', 'category', 'requirement', 'control'))
);
CREATE INDEX translations_version_lang_idx ON framework_translations (framework_version_id, language);

-- Translation is its own pipeline step, so it gets its own editable instruction
-- alongside identify / extract / controls / qa.
ALTER TABLE llm_settings ADD COLUMN translate_instruction TEXT NOT NULL DEFAULT '';
