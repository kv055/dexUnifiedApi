-- Transactions schema review (2026-06-26): link each transaction to its DEX
-- offer, drop two never-used cost columns, widen status CHECK to the recovery
-- states the controller now writes. Idempotent; safe on every boot.

-- 1. Offer link: external id (durable anchor) + internal FK (for joins).
ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS dex_offer_id    VARCHAR(128),
    ADD COLUMN IF NOT EXISTS dex_offer_db_id BIGINT;

ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_dex_offer_db_id_fkey;
ALTER TABLE transactions ADD CONSTRAINT transactions_dex_offer_db_id_fkey
    FOREIGN KEY (dex_offer_db_id) REFERENCES offers(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_transactions_dex_offer_id
    ON transactions(dex_offer_id) WHERE dex_offer_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_transactions_dex_offer_db_id
    ON transactions(dex_offer_db_id) WHERE dex_offer_db_id IS NOT NULL;

-- 2. Drop two columns no code path ever wrote.
ALTER TABLE transactions
    DROP COLUMN IF EXISTS vps_hosting_cost,
    DROP COLUMN IF EXISTS open_claw_llm_inference_cost;

-- 3. Widen status CHECK to include the recovery/needs-review states.
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_status_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_status_check
    CHECK (status IS NULL OR status IN (
        'FiatToCexInitiated', 'FiatToCexArrived',
        'CexTradeflowStarted', 'HedgeFailedNeedsReview', 'CexTradeflowNeedsReview',
        'CexTradeSucessfull',
        'CexCryptoWithdrawalInitiated', 'CexCryptoWithdrawalArrived',
        'DexPositionOpen', 'DexPositionTaken', 'DexFiatHasArrived',
        'DexPositionReleased'
    ));
