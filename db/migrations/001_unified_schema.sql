CREATE TABLE IF NOT EXISTS snapshots (
    id BIGSERIAL PRIMARY KEY,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    exchange VARCHAR(16) NOT NULL CHECK (exchange IN ('bisq', 'hodlhodl', 'robosats')),
    fiat_currency CHAR(3) NOT NULL CHECK (fiat_currency = UPPER(fiat_currency)),
    direction VARCHAR(4) NOT NULL CHECK (direction IN ('buy', 'sell')),
    btc_price DOUBLE PRECISION NOT NULL CHECK (btc_price > 0)
);

CREATE TABLE IF NOT EXISTS offers (
    id BIGSERIAL PRIMARY KEY,
    exchange VARCHAR(16) NOT NULL CHECK (exchange IN ('bisq', 'hodlhodl', 'robosats')),
    fiat_currency CHAR(3) NOT NULL CHECK (fiat_currency = UPPER(fiat_currency)),
    direction VARCHAR(4) NOT NULL CHECK (direction IN ('buy', 'sell')),
    btc_min DOUBLE PRECISION CHECK (btc_min IS NULL OR btc_min >= 0),
    btc_max DOUBLE PRECISION CHECK (btc_max IS NULL OR btc_max >= 0),
    fiat_min BIGINT CHECK (fiat_min IS NULL OR fiat_min >= 0),
    fiat_max BIGINT CHECK (fiat_max IS NULL OR fiat_max >= 0),
    payment_method VARCHAR(128),
    offer_id VARCHAR(128),
    first_seen TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_live BOOLEAN NOT NULL DEFAULT FALSE,
    duration INTEGER CHECK (duration IS NULL OR duration >= 0),
    historical_analysis_status VARCHAR(32) CHECK (
        historical_analysis_status IS NULL
        OR historical_analysis_status IN ('ignored_no_lifecycle', 'non_premium', 'analyzed')
    ),
    avg_ref_price DOUBLE PRECISION,
    avg_premium_pct DOUBLE PRECISION,
    avg_trade_cost_pct DOUBLE PRECISION,
    avg_trade_cost_btc DOUBLE PRECISION CHECK (avg_trade_cost_btc IS NULL OR avg_trade_cost_btc >= 0),
    avg_hedge_fee_sum_btc DOUBLE PRECISION CHECK (avg_hedge_fee_sum_btc IS NULL OR avg_hedge_fee_sum_btc >= 0),
    cex_taker_fee_btc DOUBLE PRECISION CHECK (cex_taker_fee_btc IS NULL OR cex_taker_fee_btc >= 0),
    cex_withdraw_fee_btc DOUBLE PRECISION CHECK (cex_withdraw_fee_btc IS NULL OR cex_withdraw_fee_btc >= 0),
    network_fee_btc DOUBLE PRECISION CHECK (network_fee_btc IS NULL OR network_fee_btc >= 0),
    hedge_open_fee_btc DOUBLE PRECISION CHECK (hedge_open_fee_btc IS NULL OR hedge_open_fee_btc >= 0),
    hedge_close_fee_btc DOUBLE PRECISION CHECK (hedge_close_fee_btc IS NULL OR hedge_close_fee_btc >= 0),
    hedge_funding_fee_btc DOUBLE PRECISION CHECK (hedge_funding_fee_btc IS NULL OR hedge_funding_fee_btc >= 0),
    hedge_fee_sum_btc DOUBLE PRECISION CHECK (hedge_fee_sum_btc IS NULL OR hedge_fee_sum_btc >= 0),
    hedge_venue VARCHAR(32),
    hedge_instrument VARCHAR(64),
    hedge_transfer_minutes INTEGER CHECK (hedge_transfer_minutes IS NULL OR hedge_transfer_minutes >= 0),
    dex_fee_btc DOUBLE PRECISION CHECK (dex_fee_btc IS NULL OR dex_fee_btc >= 0),
    avg_net_premium_pct DOUBLE PRECISION,
    avg_estimated_profit_btc DOUBLE PRECISION,
    avg_estimated_profit_fiat DOUBLE PRECISION,
    avg_btc_amount DOUBLE PRECISION CHECK (avg_btc_amount IS NULL OR avg_btc_amount >= 0),
    price_type VARCHAR(16) CHECK (price_type IS NULL OR price_type IN ('fixed', 'floating')),
    best_route_cex VARCHAR(16) CHECK (best_route_cex IS NULL OR best_route_cex IN ('kraken', 'binance', 'coinbase', 'bitstamp')),
    best_route_method VARCHAR(16) CHECK (best_route_method IS NULL OR best_route_method IN ('onchain', 'lightning')),
    CHECK (btc_max IS NULL OR btc_min IS NULL OR btc_max >= btc_min),
    CHECK (fiat_max IS NULL OR fiat_min IS NULL OR fiat_max >= fiat_min),
    CHECK (last_seen >= first_seen)
);

CREATE TABLE IF NOT EXISTS order_lifecycle (
    id BIGSERIAL PRIMARY KEY,
    offer_db_id BIGINT NOT NULL REFERENCES offers(id) ON DELETE CASCADE,
    snapshot_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    price DOUBLE PRECISION NOT NULL CHECK (price > 0),
    ref_price DOUBLE PRECISION NOT NULL CHECK (ref_price > 0),
    premium_pct DOUBLE PRECISION NOT NULL,
    trade_cost_pct DOUBLE PRECISION,
    trade_cost_btc DOUBLE PRECISION CHECK (trade_cost_btc IS NULL OR trade_cost_btc >= 0),
    cex_taker_fee_btc DOUBLE PRECISION CHECK (cex_taker_fee_btc IS NULL OR cex_taker_fee_btc >= 0),
    cex_withdraw_fee_btc DOUBLE PRECISION CHECK (cex_withdraw_fee_btc IS NULL OR cex_withdraw_fee_btc >= 0),
    network_fee_btc DOUBLE PRECISION CHECK (network_fee_btc IS NULL OR network_fee_btc >= 0),
    dex_fee_btc DOUBLE PRECISION CHECK (dex_fee_btc IS NULL OR dex_fee_btc >= 0),
    net_premium_pct DOUBLE PRECISION,
    net_premium_btc DOUBLE PRECISION,
    btc_amount DOUBLE PRECISION CHECK (btc_amount IS NULL OR btc_amount >= 0)
);

CREATE TABLE IF NOT EXISTS snapshot_log (
    id BIGSERIAL PRIMARY KEY,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    exchanges JSONB NOT NULL,
    offers_count INTEGER NOT NULL CHECK (offers_count >= 0)
);

CREATE TABLE IF NOT EXISTS kraken_ohlc (
    time TIMESTAMPTZ PRIMARY KEY,
    open DOUBLE PRECISION NOT NULL CHECK (open > 0),
    high DOUBLE PRECISION NOT NULL CHECK (high > 0),
    low DOUBLE PRECISION NOT NULL CHECK (low > 0),
    close DOUBLE PRECISION NOT NULL CHECK (close > 0),
    CHECK (high >= low),
    CHECK (high >= open),
    CHECK (high >= close),
    CHECK (low <= open),
    CHECK (low <= close)
);

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

CREATE TABLE IF NOT EXISTS wallet_addresses (
    id BIGSERIAL PRIMARY KEY,
    label VARCHAR(64) NOT NULL UNIQUE,
    address VARCHAR(128) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
