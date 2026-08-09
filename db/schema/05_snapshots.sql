CREATE TABLE IF NOT EXISTS snapshots (
    id BIGSERIAL PRIMARY KEY,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    exchange dex_provider NOT NULL,
    fiat_currency fiat_currency NOT NULL,
    direction trade_direction NOT NULL,
    btc_price DOUBLE PRECISION NOT NULL CHECK (btc_price > 0)
);

CREATE INDEX IF NOT EXISTS idx_snapshots_time ON snapshots(timestamp);
CREATE INDEX IF NOT EXISTS idx_snapshots_exchange ON snapshots(exchange);
