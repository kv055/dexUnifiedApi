CREATE TABLE IF NOT EXISTS seeds (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    seed_phrase TEXT NOT NULL,
    wallet_created_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_seeds_account_created_at
    ON seeds (account_id, created_at DESC);
