CREATE TABLE IF NOT EXISTS mm_cex_provider_policies (
    provider       TEXT PRIMARY KEY,
    spot_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    hedge_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT mm_cex_provider_policies_provider_check CHECK (
        provider IN ('kraken', 'binance', 'coinbase', 'bitstamp')
    )
);
