-- Component-specific freshness prevents an unrelated hedge failure from making
-- valid spot pricing look stale, and lets bid/ask readiness be evaluated
-- independently. Legacy aggregate columns remain for old clients.
ALTER TABLE mm_cex_provider_policies
    ADD COLUMN IF NOT EXISTS spot_fees_fetched_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS spot_fees_last_attempted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS spot_fees_last_error TEXT,
    ADD COLUMN IF NOT EXISTS withdrawal_fees_fetched_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS withdrawal_fees_last_attempted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS withdrawal_fees_last_error TEXT,
    ADD COLUMN IF NOT EXISTS withdrawal_fee_source TEXT,
    ADD COLUMN IF NOT EXISTS withdrawal_fee_observed_btc DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS withdrawal_fee_observed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS hedge_fees_fetched_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS hedge_fees_last_attempted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS hedge_fees_last_error TEXT;

CREATE TABLE IF NOT EXISTS cex_fee_observations (
    id BIGSERIAL PRIMARY KEY,
    provider TEXT NOT NULL,
    component TEXT NOT NULL,
    external_id TEXT NOT NULL,
    value DOUBLE PRECISION NOT NULL CHECK (value >= 0),
    observed_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (provider, component, external_id)
);

CREATE INDEX IF NOT EXISTS idx_cex_fee_observations_recent
    ON cex_fee_observations (provider, component, observed_at DESC);

UPDATE mm_cex_provider_policies
SET spot_fees_fetched_at = COALESCE(spot_fees_fetched_at, fees_fetched_at),
    spot_fees_last_attempted_at = COALESCE(spot_fees_last_attempted_at, fees_last_attempted_at),
    withdrawal_fees_fetched_at = COALESCE(withdrawal_fees_fetched_at, fees_fetched_at),
    withdrawal_fees_last_attempted_at = COALESCE(withdrawal_fees_last_attempted_at, fees_last_attempted_at),
    hedge_fees_fetched_at = COALESCE(hedge_fees_fetched_at, fees_fetched_at),
    hedge_fees_last_attempted_at = COALESCE(hedge_fees_last_attempted_at, fees_last_attempted_at)
WHERE fees_fetched_at IS NOT NULL OR fees_last_attempted_at IS NOT NULL;
