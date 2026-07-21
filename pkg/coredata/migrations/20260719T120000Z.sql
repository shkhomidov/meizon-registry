-- Copyright (c) 2026 Meizon Inc.
--
-- Generation runs as several LLM steps; each gets its own superadmin-editable
-- instruction. generation_instruction (added earlier) steers the EXTRACT step;
-- these cover the identify and QA steps. Empty = use the built-in default.

ALTER TABLE llm_settings ADD COLUMN identify_instruction TEXT NOT NULL DEFAULT '';
ALTER TABLE llm_settings ADD COLUMN qa_instruction       TEXT NOT NULL DEFAULT '';
