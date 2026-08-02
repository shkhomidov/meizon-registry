-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 16 (§2 cont.): store the signed mapping-set bytes verbatim.
--
-- A mapping set is signed over content that includes its own provenance —
-- crucially a publish timestamp. That makes the signed bytes impossible to
-- re-derive from normalized rows: formatting a stored timestamptz back to the
-- exact RFC3339 string that was signed is precision-dependent and fragile, and
-- any drift makes every consumer reject the set as tampered. This is the same
-- re-assembly hazard that forced mappings out of the framework bundle.
--
-- Unlike a framework bundle (deliberately rebuilt from live tables so it always
-- reflects the current structure), a mapping set is small and immutable once
-- signed. So the honest thing is to keep the exact signed document and serve it
-- byte for byte. The other columns (content_hash, counts, signature) remain as
-- queryable metadata about that document.

ALTER TABLE mapping_sets
  ADD COLUMN document BYTEA;
