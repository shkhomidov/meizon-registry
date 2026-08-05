-- Copyright (c) 2026 Meizon Inc.
--
-- Distribute the audit templates of already-published frameworks.
--
-- Audit (QA) templates are generated as 'draft' and only distribute once marked
-- 'ready'. Publishing a framework now readies its audit automatically, but
-- frameworks published before that change left their audits stuck in draft — so
-- a consumer pulling, say, PCI DSS got "no published questionnaire" even though
-- one was fully authored. Backfill: every audit template (all languages) whose
-- framework version is already published becomes ready.
--
-- This does not emit change-feed events for the backfilled audits; a consumer's
-- first catalog walk discovers them, and every future publish announces its own
-- via the application. The point here is only that a direct pull now succeeds.

UPDATE qa_templates
   SET status = 'ready', updated_at = now()
 WHERE status <> 'ready'
   AND framework_version_id IN (
       SELECT id FROM framework_versions WHERE status = 'PUBLISHED'
   );
