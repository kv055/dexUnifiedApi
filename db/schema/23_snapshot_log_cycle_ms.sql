-- snapshot_log.cycle_ms: total wall time of the snapshot worker run that wrote
-- the row. The worker is an ephemeral one-shot binary, so run timings are
-- persisted here and surfaced as Prometheus gauges by unified-api's dbmetrics
-- collector (dex_snapshot_cycle_duration_seconds). Per-exchange fetch timings
-- live inside the exchanges JSONB payload (duration_ms / failed_fetches keys).
ALTER TABLE snapshot_log ADD COLUMN IF NOT EXISTS cycle_ms BIGINT;
