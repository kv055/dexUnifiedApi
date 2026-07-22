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
    btc_amount DOUBLE PRECISION CHECK (btc_amount IS NULL OR btc_amount >= 0),
    min_trade_cost_pct DOUBLE PRECISION,
    min_trade_cost_btc DOUBLE PRECISION CHECK (min_trade_cost_btc IS NULL OR min_trade_cost_btc >= 0),
    min_net_premium_pct DOUBLE PRECISION,
    min_net_premium_btc DOUBLE PRECISION,
    min_btc_amount DOUBLE PRECISION CHECK (min_btc_amount IS NULL OR min_btc_amount >= 0),
    hedge_notional_eur DOUBLE PRECISION CHECK (hedge_notional_eur IS NULL OR hedge_notional_eur >= 0),
    leverage_used DOUBLE PRECISION CHECK (leverage_used IS NULL OR leverage_used >= 0),
    recommended_collateral_eur DOUBLE PRECISION CHECK (recommended_collateral_eur IS NULL OR recommended_collateral_eur >= 0)
);

CREATE INDEX IF NOT EXISTS idx_lifecycle_offer ON order_lifecycle(offer_db_id);
CREATE INDEX IF NOT EXISTS idx_lifecycle_time ON order_lifecycle(snapshot_time);
CREATE INDEX IF NOT EXISTS idx_lifecycle_offer_time
    ON order_lifecycle (offer_db_id, snapshot_time DESC);
