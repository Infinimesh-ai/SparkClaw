-- Management deletion keeps a redacted identity fence against stale registration.
ALTER TABLE clients ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE clients ADD CONSTRAINT client_deletion_requires_revocation
  CHECK (deleted_at IS NULL OR (revoked_at IS NOT NULL AND deleted_at >= revoked_at AND name = '' AND last_seen_at IS NULL));
