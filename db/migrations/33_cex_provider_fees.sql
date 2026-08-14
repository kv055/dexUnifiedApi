-- Exchange-derived CEX fee inputs. Values remain NULL until a successful
-- authenticated refresh; pricing must never substitute a static fee.
ALTER TABLE mm_cex_provider_policies
    ADD COLUMN IF NOT EXISTS spot_taker_fee_rate DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS btc_withdrawal_fee_btc DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS futures_open_taker_fee_rate DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS futures_close_taker_fee_rate DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS futures_funding_rate DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS futures_funding_interval_seconds BIGINT,
    ADD COLUMN IF NOT EXISTS fees_fetched_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS fees_last_attempted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS fees_last_error TEXT;

ALTER TABLE mm_cex_provider_policies
    ADD CONSTRAINT mm_cex_provider_policies_spot_taker_fee_nonnegative
        CHECK (spot_taker_fee_rate IS NULL OR spot_taker_fee_rate >= 0),
    ADD CONSTRAINT mm_cex_provider_policies_btc_withdrawal_fee_nonnegative
        CHECK (btc_withdrawal_fee_btc IS NULL OR btc_withdrawal_fee_btc >= 0),
    ADD CONSTRAINT mm_cex_provider_policies_futures_open_fee_nonnegative
        CHECK (futures_open_taker_fee_rate IS NULL OR futures_open_taker_fee_rate >= 0),
    ADD CONSTRAINT mm_cex_provider_policies_futures_close_fee_nonnegative
        CHECK (futures_close_taker_fee_rate IS NULL OR futures_close_taker_fee_rate >= 0),
    ADD CONSTRAINT mm_cex_provider_policies_futures_interval_positive
        CHECK (futures_funding_interval_seconds IS NULL OR futures_funding_interval_seconds > 0);
