-- Copyright (c) 2026 Meizon Inc.
--
-- Superadmin-editable generation instruction: the steerable portion of the
-- system prompt used when generating a framework from a document. Empty = use
-- the built-in default. The mandatory output contract (JSON shape, provenance,
-- schema version) stays in code and is always applied on top of this.

ALTER TABLE llm_settings ADD COLUMN generation_instruction TEXT NOT NULL DEFAULT '';
