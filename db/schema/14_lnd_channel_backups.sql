CREATE TABLE IF NOT EXISTS lnd_channel_backups (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    backup_sha256 TEXT NOT NULL,
    backup_size_bytes BIGINT NOT NULL,
    channel_count INT NOT NULL DEFAULT 0,
    multi_chan_backup BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT lnd_channel_backups_account_hash_size_key
        UNIQUE (account_id, backup_sha256, backup_size_bytes)
);

CREATE INDEX IF NOT EXISTS idx_lnd_channel_backups_account_created_at
    ON lnd_channel_backups (account_id, created_at DESC);
