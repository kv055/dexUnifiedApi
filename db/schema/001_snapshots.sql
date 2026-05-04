CREATE TABLE IF NOT EXISTS snapshots (
    id BIGSERIAL PRIMARY KEY,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    exchange VARCHAR(16) NOT NULL CHECK (exchange IN ('bisq', 'hodlhodl', 'robosats')),
    fiat_currency CHAR(3) NOT NULL CHECK (fiat_currency = UPPER(fiat_currency)),
    direction VARCHAR(4) NOT NULL CHECK (direction IN ('buy', 'sell')),
    btc_price DOUBLE PRECISION NOT NULL CHECK (btc_price > 0)
);

CREATE INDEX IF NOT EXISTS idx_snapshots_time ON snapshots(timestamp);
CREATE INDEX IF NOT EXISTS idx_snapshots_exchange ON snapshots(exchange);
