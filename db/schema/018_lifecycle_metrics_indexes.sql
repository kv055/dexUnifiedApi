-- Indexes to support the live lifecycle read path and the dbmetrics collector.
--
-- order_lifecycle grew to ~1.7M rows. Two hot access patterns lacked a covering
-- index:
--   * LoadByOfferIDs returns the most recent points per offer
--     (PARTITION BY offer_db_id ORDER BY snapshot_time DESC), and the dbmetrics
--     `dirstats` query does DISTINCT ON (offer_db_id ... ORDER BY snapshot_time DESC).
--     idx_lifecycle_offer (offer_db_id only) forces a sort; the composite below
--     serves both directly.
--   * The dbmetrics collector resolves the latest snapshot via
--     `snapshot_log ORDER BY timestamp DESC LIMIT 1`, which full-scanned the wide
--     JSONB table on every scrape and blew the query timeout.
--
-- Idempotent (CREATE INDEX IF NOT EXISTS). Built non-concurrently because
-- EnsureUnifiedSchema runs each file in a transaction; the brief write lock at
-- startup is acceptable during a deploy.
CREATE INDEX IF NOT EXISTS idx_lifecycle_offer_time
    ON order_lifecycle (offer_db_id, snapshot_time DESC);

CREATE INDEX IF NOT EXISTS idx_snapshot_log_timestamp
    ON snapshot_log (timestamp DESC);
