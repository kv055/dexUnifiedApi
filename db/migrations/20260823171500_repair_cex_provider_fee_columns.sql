-- Repair databases where 33_cex_provider_fees.sql / 35_cex_provider_fee_units.sql
-- were recorded by the baseline squash without executing: mm_cex_provider_policies
-- exists (policy columns live) but every fee column is missing, so the CEX fee
-- refresh fails with SQLSTATE 42703. Adds the final post-35 column set directly;
-- environments that ran 33/35 properly already have these names, making the
-- ADD COLUMN IF NOT EXISTS statements no-ops there.

ALTER TABLE mm_cex_provider_policies
    ADD COLUMN IF NOT EXISTS spot_taker_fee_percent DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS withdrawal_fee_btc DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS futures_open_taker_fee_percent DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS futures_close_taker_fee_percent DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS futures_funding_fee_percent DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS futures_funding_interval_seconds BIGINT,
    ADD COLUMN IF NOT EXISTS fees_fetched_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS fees_last_attempted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS fees_last_error TEXT;

-- PostgreSQL has no ADD CONSTRAINT IF NOT EXISTS; tolerate pre-existing
-- constraints from databases that applied migration 33 normally.
DO $$
BEGIN
    BEGIN
        ALTER TABLE mm_cex_provider_policies
            ADD CONSTRAINT mm_cex_provider_policies_spot_taker_fee_nonnegative
            CHECK (spot_taker_fee_percent IS NULL OR spot_taker_fee_percent >= 0);
    EXCEPTION WHEN duplicate_object THEN NULL;
    END;
    BEGIN
        ALTER TABLE mm_cex_provider_policies
            ADD CONSTRAINT mm_cex_provider_policies_btc_withdrawal_fee_nonnegative
            CHECK (withdrawal_fee_btc IS NULL OR withdrawal_fee_btc >= 0);
    EXCEPTION WHEN duplicate_object THEN NULL;
    END;
    BEGIN
        ALTER TABLE mm_cex_provider_policies
            ADD CONSTRAINT mm_cex_provider_policies_futures_open_fee_nonnegative
            CHECK (futures_open_taker_fee_percent IS NULL OR futures_open_taker_fee_percent >= 0);
    EXCEPTION WHEN duplicate_object THEN NULL;
    END;
    BEGIN
        ALTER TABLE mm_cex_provider_policies
            ADD CONSTRAINT mm_cex_provider_policies_futures_close_fee_nonnegative
            CHECK (futures_close_taker_fee_percent IS NULL OR futures_close_taker_fee_percent >= 0);
    EXCEPTION WHEN duplicate_object THEN NULL;
    END;
    BEGIN
        ALTER TABLE mm_cex_provider_policies
            ADD CONSTRAINT mm_cex_provider_policies_futures_interval_positive
            CHECK (futures_funding_interval_seconds IS NULL OR futures_funding_interval_seconds > 0);
    EXCEPTION WHEN duplicate_object THEN NULL;
    END;
END $$;
