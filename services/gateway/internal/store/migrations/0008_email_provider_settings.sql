CREATE TABLE IF NOT EXISTS email_provider_settings (
  owner_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT false,
  is_default BOOLEAN NOT NULL DEFAULT false,
  account TEXT NOT NULL DEFAULT 'default',
  account_hint TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'not_configured',
  last_checked_at TIMESTAMPTZ,
  error_code TEXT NOT NULL DEFAULT '',
  version BIGINT NOT NULL DEFAULT 1,
  updated_by TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (owner_id, provider)
);

CREATE UNIQUE INDEX IF NOT EXISTS email_provider_settings_default_unique
  ON email_provider_settings (owner_id)
  WHERE enabled AND is_default;
