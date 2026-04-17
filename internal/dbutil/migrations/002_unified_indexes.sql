CREATE INDEX IF NOT EXISTS idx_snapshots_time ON snapshots(timestamp);
CREATE INDEX IF NOT EXISTS idx_snapshots_exchange ON snapshots(exchange);

CREATE INDEX IF NOT EXISTS idx_offers_exchange ON offers(exchange);
CREATE INDEX IF NOT EXISTS idx_offers_direction ON offers(direction);
CREATE INDEX IF NOT EXISTS idx_offers_last_seen ON offers(last_seen);
CREATE INDEX IF NOT EXISTS idx_offers_fingerprint ON offers(exchange, fiat_currency, direction, btc_min, btc_max, payment_method);
CREATE UNIQUE INDEX IF NOT EXISTS idx_offers_offer_id ON offers(offer_id) WHERE offer_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_lifecycle_offer ON order_lifecycle(offer_db_id);
CREATE INDEX IF NOT EXISTS idx_lifecycle_time ON order_lifecycle(snapshot_time);

CREATE INDEX IF NOT EXISTS idx_transactions_direction ON transactions(direction);
CREATE INDEX IF NOT EXISTS idx_transactions_cex ON transactions(cex);
CREATE INDEX IF NOT EXISTS idx_transactions_dex ON transactions(dex);
CREATE INDEX IF NOT EXISTS idx_transactions_status ON transactions(status);
CREATE INDEX IF NOT EXISTS idx_transactions_created_at ON transactions(created_at);
CREATE INDEX IF NOT EXISTS idx_transactions_deleted_at ON transactions(deleted_at);
