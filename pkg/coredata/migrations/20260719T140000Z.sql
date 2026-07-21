-- Copyright (c) 2026 Meizon Inc.
--
-- Step 3 (controls) of the document ingestion pipeline gets its own editable
-- instruction, alongside identify / extract / qa. Empty = built-in default.

ALTER TABLE llm_settings ADD COLUMN controls_instruction TEXT NOT NULL DEFAULT '';
