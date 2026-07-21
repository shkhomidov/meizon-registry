-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 16: a change feed consumers can trust, and provenance for the release.
--
-- Consuming GRC instances had no way to learn that anything changed. The only
-- incremental primitive was `?since=<timestamp>` on the catalog, which cannot
-- carry a retirement ("stop using this version") and is unsafe as a cursor: an
-- event committed at T but made visible after a consumer has already read T is
-- lost permanently, and the consumer reports success.
--
-- So: an append-only log, read by sequence number rather than by clock.
--
-- WHY seq IS NOT bigserial
-- ------------------------
-- A sequence hands out 101 and 102 to two concurrent transactions with no
-- ordering guarantee on commit. If 102 commits first, a consumer that reads to
-- seq=102 and stores that cursor will never see 101 — silent, permanent data
-- loss presented as a successful sync. seq is therefore assigned inside the
-- publishing transaction while holding pg_advisory_xact_lock, so sequence order
-- is commit order by construction. Publishing is a rare, human-initiated action;
-- serialising it costs nothing and removes the entire failure class.
--
-- region and public are denormalised onto the event so the feed can apply the
-- same visibility rules as /catalog without joining frameworks — the filter must
-- survive the framework row changing later.

CREATE TABLE distribution_events (
  tenant_id     TEXT NOT NULL,
  seq           BIGINT PRIMARY KEY,
  kind          TEXT NOT NULL,
  framework_ref TEXT NOT NULL,
  version       TEXT NOT NULL,
  content_hash  TEXT NOT NULL DEFAULT '',
  -- Visibility as it stood when the event was emitted.
  region        TEXT NOT NULL DEFAULT '',
  public        BOOLEAN NOT NULL DEFAULT FALSE,
  created_at    TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT distribution_events_kind_check
    CHECK (kind IN ('published', 'deprecated'))
);

-- The feed is always read as "everything after my cursor", in order.
CREATE INDEX distribution_events_seq_idx ON distribution_events (seq);
CREATE INDEX distribution_events_ref_idx ON distribution_events (framework_ref, seq DESC);

-- Release provenance. approvals already records who approved and when, but a
-- consumer verifying a bundle should not have to reconstruct the moderation
-- trail from a join and an audit log. published_by had no home at all — the only
-- record of who published was an audit line.
ALTER TABLE framework_versions
  ADD COLUMN published_by TEXT NOT NULL DEFAULT '',
  ADD COLUMN approved_by  TEXT NOT NULL DEFAULT '',
  ADD COLUMN approved_at  TIMESTAMP WITH TIME ZONE;

-- Backfill what is recoverable for versions published before this migration:
-- the approving reviewer and the moment of approval are in `approvals`.
-- published_by is NOT backfillable — it lived only in the audit log, whose
-- actor is not reliably joinable to a version here. It stays empty for those
-- rows, which is honest: we do not know, rather than guessing.
UPDATE framework_versions v
SET    approved_by = a.reviewer_id,
       approved_at = a.created_at
FROM   (
  SELECT DISTINCT ON (framework_version_id)
         framework_version_id, reviewer_id, created_at
  FROM   approvals
  WHERE  decision = 'approved'
  ORDER  BY framework_version_id, created_at ASC
) a
WHERE  v.id = a.framework_version_id
  AND  v.approved_by = '';

-- Seed the feed from history so a consumer starting at cursor 0 receives the
-- frameworks that were already published, rather than believing the registry is
-- empty until the next publish happens. Ordered by publication time so the
-- replayed sequence matches the order events actually occurred in.
INSERT INTO distribution_events (
  tenant_id, seq, kind, framework_ref, version, content_hash, region, public, created_at
)
SELECT v.tenant_id,
       ROW_NUMBER() OVER (ORDER BY v.published_at ASC, v.id ASC),
       'published',
       f.reference_id,
       v.version,
       COALESCE(v.content_hash, ''),
       COALESCE(f.region, ''),
       COALESCE(f.public, FALSE),
       v.published_at
FROM   framework_versions v
JOIN   frameworks f ON f.id = v.framework_id
WHERE  v.status = 'PUBLISHED'
  AND  v.published_at IS NOT NULL;
