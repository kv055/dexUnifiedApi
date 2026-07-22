ALTER TABLE order_lifecycle
    ADD COLUMN IF NOT EXISTS min_trade_cost_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS min_trade_cost_btc DOUBLE PRECISION CHECK (min_trade_cost_btc IS NULL OR min_trade_cost_btc >= 0),
    ADD COLUMN IF NOT EXISTS min_net_premium_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS min_net_premium_btc DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS min_btc_amount DOUBLE PRECISION CHECK (min_btc_amount IS NULL OR min_btc_amount >= 0);

ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS avg_min_trade_cost_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS avg_min_trade_cost_btc DOUBLE PRECISION CHECK (avg_min_trade_cost_btc IS NULL OR avg_min_trade_cost_btc >= 0),
    ADD COLUMN IF NOT EXISTS avg_min_net_premium_pct DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS avg_min_estimated_profit_btc DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS avg_min_estimated_profit_fiat DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS avg_min_btc_amount DOUBLE PRECISION CHECK (avg_min_btc_amount IS NULL OR avg_min_btc_amount >= 0),
    ADD COLUMN IF NOT EXISTS min_volume_analyzed BOOLEAN NOT NULL DEFAULT FALSE;
