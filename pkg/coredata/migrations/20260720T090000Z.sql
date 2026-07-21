-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 17: keep the source document a framework was generated from.
--
-- Until now the uploaded PDF was read for its text and the bytes were dropped.
-- That makes a published framework unauditable in the way that matters most:
-- there is no way to check a requirement against the document it came from.
--
-- The file is stored as a row rather than on disk deliberately — it inherits
-- the database's backup, restore and access control instead of needing a
-- parallel story for each. Standards documents are a few MB; at registry scale
-- (tens of frameworks, not millions of uploads) that is the simpler correct
-- choice, and it keeps the bytes inside the same transaction as the framework.
--
-- framework_id is nullable: the file is stored when it is UPLOADED, before the
-- auditor has decided whether to accept the generated draft. job_id links it to
-- the generation run in the meantime, and unlinked rows are swept.

CREATE TABLE source_documents (
  tenant_id     TEXT NOT NULL,
  id            TEXT PRIMARY KEY,
  framework_id  TEXT REFERENCES frameworks (id) ON DELETE CASCADE,
  job_id        TEXT NOT NULL DEFAULT '',
  filename      TEXT NOT NULL,
  content_type  TEXT NOT NULL DEFAULT 'application/pdf',
  byte_size     BIGINT NOT NULL,
  -- sha256 of the bytes: proves the stored file is the one that was read, and
  -- lets a re-upload of the same document be recognised.
  sha256        TEXT NOT NULL,
  page_count    INTEGER NOT NULL DEFAULT 0,
  ocr_pages     INTEGER NOT NULL DEFAULT 0,
  content       BYTEA NOT NULL,
  uploaded_by   TEXT NOT NULL DEFAULT '',
  uploaded_at   TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX source_documents_framework_idx ON source_documents (framework_id);
CREATE INDEX source_documents_job_idx ON source_documents (job_id) WHERE framework_id IS NULL;
