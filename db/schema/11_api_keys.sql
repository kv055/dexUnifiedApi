CREATE TABLE IF NOT EXISTS api_keys (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    api_key TEXT NOT NULL,
    api_secret TEXT,
    passphrase TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    permissions JSONB,
    permissions_checked_at TIMESTAMPTZ,
    permission_check_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_api_keys_account_updated_at
    ON api_keys (account_id, updated_at DESC);
