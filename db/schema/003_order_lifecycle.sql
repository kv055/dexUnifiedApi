CREATE TABLE IF NOT EXISTS order_lifecycle (
    id BIGSERIAL PRIMARY KEY,
    offer_db_id BIGINT NOT NULL REFERENCES offers(id) ON DELETE CASCADE,
    snapshot_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    price DOUBLE PRECISION NOT NULL CHECK (price > 0),
    ref_price DOUBLE PRECISION NOT NULL CHECK (ref_price > 0),
    premium_pct DOUBLE PRECISION NOT NULL,
    trade_cost_pct DOUBLE PRECISION,
    trade_cost_btc DOUBLE PRECISION CHECK (trade_cost_btc IS NULL OR trade_cost_btc >= 0),
    net_premium_pct DOUBLE PRECISION,
    net_premium_btc DOUBLE PRECISION,
    btc_amount DOUBLE PRECISION CHECK (btc_amount IS NULL OR btc_amount >= 0)
);

CREATE INDEX IF NOT EXISTS idx_lifecycle_offer ON order_lifecycle(offer_db_id);
CREATE INDEX IF NOT EXISTS idx_lifecycle_time ON order_lifecycle(snapshot_time);
