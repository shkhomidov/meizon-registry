-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 16: OCR for image-based (scanned) PDFs.
--
-- OCR is a different service from the LLM provider and needs its own
-- credential — reusing the LLM key would send a Mistral key to Gemini. The key
-- is AES-256-GCM encrypted with the same path as the LLM key and is never
-- returned by the API; the console only ever learns whether one is configured.
--
-- Empty ocr_provider means OCR is not configured, and a scanned upload fails
-- with a message telling the superadmin to set it up — rather than silently
-- producing an empty framework.

ALTER TABLE llm_settings ADD COLUMN ocr_provider          TEXT NOT NULL DEFAULT '';
ALTER TABLE llm_settings ADD COLUMN encrypted_ocr_api_key BYTEA;
ALTER TABLE llm_settings ADD COLUMN ocr_model             TEXT NOT NULL DEFAULT '';
