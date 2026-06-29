CREATE TABLE IF NOT EXISTS bisq_account_file_backups (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    file_name TEXT NOT NULL,
    source_path TEXT,
    hash_algorithm TEXT NOT NULL DEFAULT 'sha256',
    file_sha256 TEXT NOT NULL,
    file_size_bytes BIGINT NOT NULL,
    modified_at TIMESTAMPTZ,
    contents BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT bisq_account_file_backups_account_hash_size_key
        UNIQUE (account_id, file_sha256, file_size_bytes)
);

CREATE INDEX IF NOT EXISTS idx_bisq_account_file_backups_account_created_at
    ON bisq_account_file_backups (account_id, created_at DESC);
