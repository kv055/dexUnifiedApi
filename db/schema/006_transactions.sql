CREATE TABLE IF NOT EXISTS transactions (
    id BIGSERIAL PRIMARY KEY,
    direction VARCHAR(4) NOT NULL CHECK (direction IN ('BUY', 'SELL')),
    fiat_amount DOUBLE PRECISION NOT NULL CHECK (fiat_amount > 0),
    fiat_currency CHAR(3) NOT NULL CHECK (fiat_currency = UPPER(fiat_currency)),
    cex VARCHAR(16) CHECK (cex IS NULL OR cex IN ('Binance', 'Kraken', 'Coinbase', 'Bybit', 'OKX', 'Bitfinex', 'Gemini', 'Bitstamp')),
    cex_hedging_position VARCHAR(16) CHECK (cex_hedging_position IS NULL OR cex_hedging_position IN ('Binance', 'Kraken', 'Coinbase', 'Bybit', 'OKX', 'Bitfinex', 'Gemini', 'Bitstamp')),
    dex VARCHAR(16) CHECK (dex IS NULL OR dex IN ('Bisq', 'HodlHodl', 'RoboSats')),
    status VARCHAR(32) CHECK (
        status IS NULL OR status IN (
            'FiatToCexInitiated',
            'FiatToCexArrived',
            'CexTradeSucessfull',
            'CexCryptoWithdrawalInitiated',
            'CexCryptoWithdrawalArrived',
            'DexPositionOpen',
            'DexPositionTaken',
            'DexFiatHasArrived',
            'DexPositionReleased'
        )
    ),
    cex_transaction_details JSONB,
    cex_bitcoin_amount DOUBLE PRECISION CHECK (cex_bitcoin_amount IS NULL OR cex_bitcoin_amount >= 0),
    cex_transaction_timestamp TIMESTAMPTZ,
    cex_transaction_fee DOUBLE PRECISION CHECK (cex_transaction_fee IS NULL OR cex_transaction_fee >= 0),
    cex_to_dex_withdrawal_tx_details JSONB,
    cex_withdrawal_fee DOUBLE PRECISION CHECK (cex_withdrawal_fee IS NULL OR cex_withdrawal_fee >= 0),
    dex_btc_address VARCHAR(128),
    cex_counter_position_open_details JSONB,
    cex_counter_position_close_details JSONB,
    cex_counter_position_fee DOUBLE PRECISION CHECK (cex_counter_position_fee IS NULL OR cex_counter_position_fee >= 0),
    dex_offer_creation JSONB,
    dex_offer_taken TIMESTAMPTZ,
    dex_fiat_arrival JSONB,
    dex_btc_release TIMESTAMPTZ,
    dex_fees DOUBLE PRECISION CHECK (dex_fees IS NULL OR dex_fees >= 0),
    transfer_fiat_to_cex_start_time TIMESTAMPTZ,
    transfer_fiat_to_cex_arrival_time TIMESTAMPTZ,
    transaction_complete BOOLEAN NOT NULL DEFAULT FALSE,
    open_claw_llm_inference_cost DOUBLE PRECISION CHECK (open_claw_llm_inference_cost IS NULL OR open_claw_llm_inference_cost >= 0),
    vps_hosting_cost DOUBLE PRECISION CHECK (vps_hosting_cost IS NULL OR vps_hosting_cost >= 0),
    total_transaction_pnl DOUBLE PRECISION,
    total_transaction_time_elapsed INTEGER CHECK (total_transaction_time_elapsed IS NULL OR total_transaction_time_elapsed >= 0),
    total_transaction_volatility_in_btc_price DOUBLE PRECISION,
    premium_over_cex_price_to_break_even_prediction DOUBLE PRECISION,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_transactions_direction ON transactions(direction);
CREATE INDEX IF NOT EXISTS idx_transactions_cex ON transactions(cex);
CREATE INDEX IF NOT EXISTS idx_transactions_dex ON transactions(dex);
CREATE INDEX IF NOT EXISTS idx_transactions_status ON transactions(status);
CREATE INDEX IF NOT EXISTS idx_transactions_created_at ON transactions(created_at);
CREATE INDEX IF NOT EXISTS idx_transactions_deleted_at ON transactions(deleted_at);
