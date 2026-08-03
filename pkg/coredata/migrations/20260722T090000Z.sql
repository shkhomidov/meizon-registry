-- Copyright (c) 2026 Meizon Inc.
--
-- Phase 17: keyless cloud sync — organizations gated by superadmin approval.
--
-- A consuming instance used to authenticate with a hand-issued bearer token,
-- copy-pasted into its config. On the cloud that step is friction. Instead, a
-- consuming ORGANIZATION registers, a superadmin approves it once, and from then
-- on the org's authenticated cloud session IS the sync credential — no key.
--
-- An organization is a tenant: its users are identities under that tenant. This
-- row carries only the approval gate. Registry staff (superadmins, auditors)
-- live under the platform tenant and are not organizations.

CREATE TABLE organizations (
  tenant_id    TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  -- pending: registered, awaiting approval — no sync access.
  -- approved: syncs every published public framework, instantly.
  -- suspended: access revoked without deleting the org.
  status       TEXT NOT NULL DEFAULT 'pending',
  requested_by TEXT NOT NULL DEFAULT '',
  approved_by  TEXT NOT NULL DEFAULT '',
  approved_at  TIMESTAMP WITH TIME ZONE,
  created_at   TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at   TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT organizations_status_check CHECK (status IN ('pending', 'approved', 'suspended'))
);

-- The review queue is browsed by status, newest first.
CREATE INDEX organizations_status_idx ON organizations (status, created_at DESC);
