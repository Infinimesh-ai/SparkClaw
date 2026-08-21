UPDATE connector_settings
SET owner_id = 'owner'
WHERE btrim(owner_id) = '';

UPDATE notification_bindings
SET actor_id = owner_id
WHERE btrim(actor_id) = '';

ALTER TABLE notification_bindings
  ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;

ALTER TABLE notification_bindings
  ADD COLUMN IF NOT EXISTS credential_kind TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS notification_bindings_active_default_unique
  ON notification_bindings (owner_id, channel)
  WHERE status = 'active' AND default_for_channel;

CREATE UNIQUE INDEX IF NOT EXISTS notification_bindings_vault_ref_unique
  ON notification_bindings (credential_ref)
  WHERE btrim(credential_ref) <> '' AND credential_ref NOT LIKE 'config:%';
