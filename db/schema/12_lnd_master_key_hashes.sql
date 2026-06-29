CREATE TABLE IF NOT EXISTS lnd_master_key_hashes (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    hash_algorithm TEXT NOT NULL DEFAULT 'sha256',
    master_key_hash TEXT NOT NULL,
    source_path TEXT,
    file_size_bytes BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_lnd_master_key_hashes_account_created_at
    ON lnd_master_key_hashes (account_id, created_at DESC);
