CREATE TABLE IF NOT EXISTS totp_secrets (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    secret TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    rotated_at TIMESTAMPTZ,
    UNIQUE (account_id, created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_totp_secrets_one_active_per_account
    ON totp_secrets (account_id)
    WHERE is_active = TRUE;
