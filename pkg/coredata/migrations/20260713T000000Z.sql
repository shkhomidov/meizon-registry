-- Copyright (c) 2026 Meizon Inc.
--
-- Initial Meizon Registry schema: identities and role memberships, the
-- framework catalog with immutable versions and their controls, approvals,
-- distribution tokens, ed25519 signing keys, an append-only audit log and a
-- download ledger. Every table carries tenant_id for isolation; GID columns are
-- stored as TEXT (base64url).

CREATE TABLE identities (
  tenant_id       TEXT NOT NULL,
  id              TEXT PRIMARY KEY,
  email           TEXT NOT NULL,
  full_name       TEXT NOT NULL,
  hashed_password BYTEA,
  status          TEXT NOT NULL,
  created_at      TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at      TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT identities_email_unique UNIQUE (email)
);

CREATE TABLE memberships (
  tenant_id   TEXT NOT NULL,
  id          TEXT PRIMARY KEY,
  identity_id TEXT NOT NULL REFERENCES identities (id),
  role        TEXT NOT NULL,
  regions     TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at  TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT memberships_identity_unique UNIQUE (identity_id)
);

CREATE TABLE frameworks (
  tenant_id    TEXT NOT NULL,
  id           TEXT PRIMARY KEY,
  reference_id TEXT NOT NULL,
  name         TEXT NOT NULL,
  short_name   TEXT NOT NULL DEFAULT '',
  region       TEXT NOT NULL,
  authority    TEXT NOT NULL DEFAULT '',
  license      TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  public       BOOLEAN NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at   TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT frameworks_tenant_ref_unique UNIQUE (tenant_id, reference_id)
);

CREATE INDEX frameworks_tenant_idx ON frameworks (tenant_id);

CREATE TABLE framework_versions (
  tenant_id    TEXT NOT NULL,
  id           TEXT PRIMARY KEY,
  framework_id TEXT NOT NULL REFERENCES frameworks (id),
  version      TEXT NOT NULL,
  status       TEXT NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  signature    JSONB,
  key_id       TEXT NOT NULL DEFAULT '',
  changelog    TEXT NOT NULL DEFAULT '',
  quorum       INTEGER NOT NULL DEFAULT 1,
  author_id    TEXT NOT NULL,
  published_at TIMESTAMP WITH TIME ZONE,
  created_at   TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at   TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT framework_versions_fw_version_unique UNIQUE (framework_id, version)
);

CREATE INDEX framework_versions_framework_idx ON framework_versions (framework_id);

CREATE TABLE controls (
  tenant_id            TEXT NOT NULL,
  id                   TEXT PRIMARY KEY,
  framework_version_id TEXT NOT NULL REFERENCES framework_versions (id),
  ref_id               TEXT NOT NULL,
  name                 TEXT NOT NULL,
  description          TEXT NOT NULL DEFAULT '',
  section              TEXT NOT NULL DEFAULT '',
  parent_ref_id        TEXT,
  guidance             TEXT NOT NULL DEFAULT '',
  refs                 JSONB NOT NULL DEFAULT '[]',
  mappings             JSONB NOT NULL DEFAULT '[]',
  position             INTEGER NOT NULL DEFAULT 0,
  created_at           TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at           TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT controls_version_ref_unique UNIQUE (framework_version_id, ref_id)
);

CREATE INDEX controls_version_idx ON controls (framework_version_id);

CREATE TABLE approvals (
  tenant_id            TEXT NOT NULL,
  id                   TEXT PRIMARY KEY,
  framework_version_id TEXT NOT NULL REFERENCES framework_versions (id),
  reviewer_id          TEXT NOT NULL,
  decision             TEXT NOT NULL,
  comment              TEXT NOT NULL DEFAULT '',
  created_at           TIMESTAMP WITH TIME ZONE NOT NULL,
  CONSTRAINT approvals_version_reviewer_unique UNIQUE (framework_version_id, reviewer_id)
);

CREATE INDEX approvals_version_idx ON approvals (framework_version_id);

CREATE TABLE distribution_tokens (
  tenant_id    TEXT NOT NULL,
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  hashed_token TEXT NOT NULL,
  regions      TEXT NOT NULL DEFAULT '',
  revoked      BOOLEAN NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMP WITH TIME ZONE NOT NULL,
  last_used_at TIMESTAMP WITH TIME ZONE,
  CONSTRAINT distribution_tokens_hash_unique UNIQUE (hashed_token)
);

CREATE TABLE signing_keys (
  tenant_id             TEXT NOT NULL,
  id                    TEXT PRIMARY KEY,
  key_id                TEXT NOT NULL,
  public_key            BYTEA NOT NULL,
  encrypted_private_key BYTEA NOT NULL,
  active                BOOLEAN NOT NULL DEFAULT TRUE,
  created_at            TIMESTAMP WITH TIME ZONE NOT NULL,
  rotated_at            TIMESTAMP WITH TIME ZONE,
  CONSTRAINT signing_keys_keyid_unique UNIQUE (key_id)
);

CREATE TABLE audit_log (
  tenant_id  TEXT NOT NULL,
  id         TEXT PRIMARY KEY,
  actor_id   TEXT NOT NULL DEFAULT '',
  action     TEXT NOT NULL,
  target_id  TEXT NOT NULL DEFAULT '',
  detail     TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX audit_log_tenant_created_idx ON audit_log (tenant_id, created_at);

CREATE TABLE downloads (
  tenant_id            TEXT NOT NULL,
  id                   TEXT PRIMARY KEY,
  framework_version_id TEXT NOT NULL,
  token_id             TEXT NOT NULL DEFAULT '',
  format               TEXT NOT NULL,
  created_at           TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX downloads_version_idx ON downloads (framework_version_id);
