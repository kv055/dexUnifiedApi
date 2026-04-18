ALTER TABLE secret_providers
    RENAME TO accounts;

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS email TEXT,
    ADD COLUMN IF NOT EXISTS password TEXT;

ALTER INDEX IF EXISTS idx_totp_secrets_one_active_per_provider
    RENAME TO idx_totp_secrets_one_active_per_account;

ALTER INDEX IF EXISTS idx_passkey_credentials_provider_active
    RENAME TO idx_passkey_credentials_account_active;

ALTER TABLE totp_secrets
    RENAME COLUMN provider_id TO account_id;

ALTER TABLE passkey_credentials
    RENAME COLUMN provider_id TO account_id;

ALTER TABLE totp_secrets
    DROP CONSTRAINT IF EXISTS totp_secrets_provider_id_created_at_key;

ALTER TABLE totp_secrets
    ADD CONSTRAINT totp_secrets_account_id_created_at_key UNIQUE (account_id, created_at);
