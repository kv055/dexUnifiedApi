CREATE TABLE IF NOT EXISTS mm_cex_provider_policies (
    provider       cex_provider PRIMARY KEY,
    spot_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    hedge_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
