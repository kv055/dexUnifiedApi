-- Hedgeless execution has two concurrent lifecycles on one transaction row:
-- `status` follows the DEX trade, while `btc_pool_refill_status` follows the
-- replacement BTC until LND is back at its pre-trade target.
ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS trade_mode TEXT,
    ADD COLUMN IF NOT EXISTS btc_pool_target_btc DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS btc_required_liquidity DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS btc_pool_refill_status TEXT,
    ADD COLUMN IF NOT EXISTS btc_pool_refill_details JSONB;

ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_trade_mode_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_trade_mode_check CHECK (
    trade_mode IS NULL OR trade_mode IN ('hedgefull_single_collateral', 'hedgeless_double_collateral')
);

ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_btc_pool_refill_status_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_btc_pool_refill_status_check CHECK (
    btc_pool_refill_status IS NULL OR btc_pool_refill_status IN (
        'pending', 'spot_buy_pending', 'spot_bought', 'withdrawal_initiated', 'restored', 'needs_review'
    )
);

ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_status_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_status_check CHECK (
    status IS NULL OR status IN (
        'FiatToCexInitiated', 'FiatToCexArrived',
        'CexTradeflowStarted', 'HedgeFailedNeedsReview', 'CexTradeflowNeedsReview',
        'HedgelessTradeStarted', 'HedgelessFundingInitiated', 'HedgelessFundingNeedsReview',
        'CexTradeSucessfull', 'CexCryptoWithdrawalInitiated', 'CexCryptoWithdrawalArrived',
        'DexPositionOpen', 'DexPositionTaken', 'DexFiatHasArrived', 'DexPositionReleased'
    )
);

ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_btc_pool_target_nonnegative;
ALTER TABLE transactions ADD CONSTRAINT transactions_btc_pool_target_nonnegative
    CHECK (btc_pool_target_btc IS NULL OR btc_pool_target_btc >= 0);
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_btc_required_liquidity_positive;
ALTER TABLE transactions ADD CONSTRAINT transactions_btc_required_liquidity_positive
    CHECK (btc_required_liquidity IS NULL OR btc_required_liquidity > 0);

-- A needs-review cycle keeps the reservation until an operator reconciles it.
CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_one_active_hedgeless_refill
    ON transactions ((1))
    WHERE trade_mode = 'hedgeless_double_collateral'
      AND btc_pool_refill_status IS DISTINCT FROM 'restored'
      AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_transactions_pool_refill_status
    ON transactions (btc_pool_refill_status)
    WHERE trade_mode = 'hedgeless_double_collateral';
