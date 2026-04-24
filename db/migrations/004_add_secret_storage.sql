CREATE TABLE IF NOT EXISTS secret_providers (
    id BIGSERIAL PRIMARY KEY,
    provider VARCHAR(32) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS totp_secrets (
    id BIGSERIAL PRIMARY KEY,
    provider_id BIGINT NOT NULL REFERENCES secret_providers(id) ON DELETE CASCADE,
    secret TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    rotated_at TIMESTAMPTZ,
    UNIQUE (provider_id, created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_totp_secrets_one_active_per_provider
    ON totp_secrets (provider_id)
    WHERE is_active = TRUE;

CREATE TABLE IF NOT EXISTS passkey_credentials (
    id BIGSERIAL PRIMARY KEY,
    provider_id BIGINT NOT NULL REFERENCES secret_providers(id) ON DELETE CASCADE,
    credential_id TEXT NOT NULL UNIQUE,
    rp_id TEXT NOT NULL,
    private_key_pkcs8 TEXT NOT NULL,
    user_handle TEXT NOT NULL,
    sign_count BIGINT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_passkey_credentials_provider_active
    ON passkey_credentials (provider_id, is_active);
