CREATE TABLE IF NOT EXISTS transactions (
    id BIGSERIAL PRIMARY KEY,
    direction trade_direction NOT NULL,
    fiat_amount DOUBLE PRECISION NOT NULL CHECK (fiat_amount > 0),
    fiat_currency fiat_currency NOT NULL,
    cex cex_provider,
    cex_hedging_position cex_provider,
    dex dex_provider,
    status VARCHAR(32),
    payment_method TEXT,
    trade_mode transaction_trade_mode,
    btc_pool_target_btc DOUBLE PRECISION CHECK (btc_pool_target_btc IS NULL OR btc_pool_target_btc >= 0),
    btc_required_liquidity DOUBLE PRECISION CHECK (btc_required_liquidity IS NULL OR btc_required_liquidity > 0),
    btc_pool_refill_status TEXT CHECK (
        btc_pool_refill_status IS NULL OR btc_pool_refill_status IN (
            'pending', 'spot_buy_pending', 'spot_bought', 'withdrawal_initiated', 'restored', 'needs_review'
        )
    ),
    btc_pool_refill_details JSONB,
    cex_transaction_details JSONB,
    cex_bitcoin_amount DOUBLE PRECISION CHECK (cex_bitcoin_amount IS NULL OR cex_bitcoin_amount >= 0),
    cex_transaction_timestamp TIMESTAMPTZ,
    cex_transaction_fee DOUBLE PRECISION CHECK (cex_transaction_fee IS NULL OR cex_transaction_fee >= 0),
    cex_to_dex_withdrawal_tx_details JSONB,
    cex_withdrawal_fee DOUBLE PRECISION CHECK (cex_withdrawal_fee IS NULL OR cex_withdrawal_fee >= 0),
    destination_address VARCHAR(128),
    hub_address VARCHAR(128),
    cex_counter_position_open_details JSONB,
    cex_counter_position_close_details JSONB,
    cex_counter_position_fee DOUBLE PRECISION CHECK (cex_counter_position_fee IS NULL OR cex_counter_position_fee >= 0),
    dex_offer_id VARCHAR(128),
    dex_offer_db_id BIGINT REFERENCES offers(id) ON DELETE SET NULL,
    dex_offer_creation JSONB,
    dex_offer_taken TIMESTAMPTZ,
    dex_fiat_arrival JSONB,
    dex_btc_release TIMESTAMPTZ,
    dex_fees DOUBLE PRECISION CHECK (dex_fees IS NULL OR dex_fees >= 0),
    counterparty_payment_info JSONB,
    transfer_fiat_to_cex_start_time TIMESTAMPTZ,
    transfer_fiat_to_cex_arrival_time TIMESTAMPTZ,
    transaction_complete BOOLEAN NOT NULL DEFAULT FALSE,
    total_transaction_pnl DOUBLE PRECISION,
    total_transaction_time_elapsed INTEGER CHECK (total_transaction_time_elapsed IS NULL OR total_transaction_time_elapsed >= 0),
    total_transaction_volatility_in_btc_price DOUBLE PRECISION,
    premium_over_cex_price_to_break_even_prediction DOUBLE PRECISION,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT transactions_status_check CHECK (
        status IS NULL OR status IN (
            'FiatToCexInitiated', 'FiatToCexArrived',
            'CexTradeflowStarted', 'HedgeFailedNeedsReview', 'CexTradeflowNeedsReview',
            'HedgelessTradeStarted', 'HedgelessFundingInitiated', 'HedgelessFundingNeedsReview',
            'CexTradeSucessfull',
            'CexCryptoWithdrawalInitiated', 'CexCryptoWithdrawalArrived',
            'DexPositionOpen', 'DexPositionTaken', 'DexFiatHasArrived',
            'DexPositionReleased'
        )
    )
);

CREATE INDEX IF NOT EXISTS idx_transactions_direction ON transactions(direction);
CREATE INDEX IF NOT EXISTS idx_transactions_cex ON transactions(cex);
CREATE INDEX IF NOT EXISTS idx_transactions_dex ON transactions(dex);
CREATE INDEX IF NOT EXISTS idx_transactions_status ON transactions(status);
CREATE INDEX IF NOT EXISTS idx_transactions_created_at ON transactions(created_at);
CREATE INDEX IF NOT EXISTS idx_transactions_deleted_at ON transactions(deleted_at);
CREATE INDEX IF NOT EXISTS idx_transactions_dex_offer_id
    ON transactions(dex_offer_id) WHERE dex_offer_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_transactions_dex_offer_db_id
    ON transactions(dex_offer_db_id) WHERE dex_offer_db_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_one_active_hedgeless_refill
    ON transactions ((1))
    WHERE trade_mode = 'hedgeless_double_collateral'
      AND btc_pool_refill_status IS DISTINCT FROM 'restored'
      AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_transactions_pool_refill_status
    ON transactions (btc_pool_refill_status)
    WHERE trade_mode = 'hedgeless_double_collateral';
