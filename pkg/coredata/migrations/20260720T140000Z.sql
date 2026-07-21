-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 18: background jobs survive the browser and the process.
--
-- Generation already ran asynchronously, but the job registry was in memory
-- only. Two consequences, both bad for a run that takes minutes: closing the
-- tab lost the result (the job id lived in React state), and restarting
-- registryd lost every job in flight. A 106-page OCR plus 58 control batches is
-- far too much work to hold somewhere that fragile.
--
-- Progress is written as it happens, so "what is running and how far along" is
-- answerable by anyone, from any tab, at any time.

CREATE TABLE ingest_jobs (
  tenant_id      TEXT NOT NULL,
  id             TEXT PRIMARY KEY,
  kind           TEXT NOT NULL,                 -- generate | next_version | translate | automap
  status         TEXT NOT NULL,                 -- running | done | error
  step           TEXT NOT NULL DEFAULT '',
  current        INTEGER NOT NULL DEFAULT 0,
  total          INTEGER NOT NULL DEFAULT 0,
  -- What the job is about, for a list a human can read: the framework it
  -- targets and the file it came from.
  label          TEXT NOT NULL DEFAULT '',
  framework_ref  TEXT NOT NULL DEFAULT '',
  -- The finished proposal. Held so a reviewer can come back to it later
  -- instead of re-running an expensive pipeline.
  result         JSONB,
  error          TEXT NOT NULL DEFAULT '',
  actor_id       TEXT NOT NULL DEFAULT '',
  created_at     TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at     TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT ingest_jobs_status_check CHECK (status IN ('running', 'done', 'error'))
);
CREATE INDEX ingest_jobs_actor_idx ON ingest_jobs (actor_id, created_at DESC);
CREATE INDEX ingest_jobs_status_idx ON ingest_jobs (status) WHERE status = 'running';
