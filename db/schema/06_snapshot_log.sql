CREATE TABLE IF NOT EXISTS snapshot_log (
    id BIGSERIAL PRIMARY KEY,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    exchanges JSONB NOT NULL,
    offers_count INTEGER NOT NULL CHECK (offers_count >= 0),
    cycle_ms BIGINT
);

CREATE INDEX IF NOT EXISTS idx_snapshot_log_timestamp ON snapshot_log (timestamp DESC);
