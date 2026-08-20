-- Last Complete census per independently fetched market. Stale-grace expiry
-- keys off this timestamp so Incomplete fetches cannot age out offers they
-- simply failed to list.
CREATE TABLE IF NOT EXISTS live_market_observation (
    exchange dex_provider NOT NULL,
    fiat_currency fiat_currency NOT NULL,
    direction trade_direction NOT NULL,
    last_complete_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (exchange, fiat_currency, direction)
);

-- Peach maker identity used by live reputation lookup. Persisting it lets the
-- hub reload from Postgres without the snapshot POST body.
ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS maker_id VARCHAR(128);
