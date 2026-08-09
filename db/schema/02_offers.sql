CREATE TABLE IF NOT EXISTS offers (
    id BIGSERIAL PRIMARY KEY,
    exchange dex_provider NOT NULL,
    fiat_currency fiat_currency NOT NULL,
    direction trade_direction NOT NULL,
    price DOUBLE PRECISION,
    btc_min DOUBLE PRECISION CHECK (btc_min IS NULL OR btc_min >= 0),
    btc_max DOUBLE PRECISION CHECK (btc_max IS NULL OR btc_max >= 0),
    fiat_min BIGINT CHECK (fiat_min IS NULL OR fiat_min >= 0),
    fiat_max BIGINT CHECK (fiat_max IS NULL OR fiat_max >= 0),
    payment_methods TEXT[],
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
    abort_cost_btc DOUBLE PRECISION CHECK (abort_cost_btc IS NULL OR abort_cost_btc >= 0),
    abort_cost_pct DOUBLE PRECISION CHECK (abort_cost_pct IS NULL OR abort_cost_pct >= 0),
    cex_taker_fee_btc DOUBLE PRECISION CHECK (cex_taker_fee_btc IS NULL OR cex_taker_fee_btc >= 0),
    cex_withdraw_fee_btc DOUBLE PRECISION CHECK (cex_withdraw_fee_btc IS NULL OR cex_withdraw_fee_btc >= 0),
    routing_fee_sum DOUBLE PRECISION CHECK (routing_fee_sum IS NULL OR routing_fee_sum >= 0),
    routing_profile routing_profile,
    routing_fee_detail JSONB,
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
    avg_min_trade_cost_pct DOUBLE PRECISION,
    avg_min_trade_cost_btc DOUBLE PRECISION CHECK (avg_min_trade_cost_btc IS NULL OR avg_min_trade_cost_btc >= 0),
    avg_min_net_premium_pct DOUBLE PRECISION,
    avg_min_estimated_profit_btc DOUBLE PRECISION,
    avg_min_estimated_profit_fiat DOUBLE PRECISION,
    avg_min_btc_amount DOUBLE PRECISION CHECK (avg_min_btc_amount IS NULL OR avg_min_btc_amount >= 0),
    min_volume_analyzed BOOLEAN NOT NULL DEFAULT FALSE,
    price_type offer_price_type,
    best_route_cex cex_provider,
    security_deposit_btc DOUBLE PRECISION CHECK (security_deposit_btc IS NULL OR security_deposit_btc >= 0),
    security_deposit_pct DOUBLE PRECISION CHECK (security_deposit_pct IS NULL OR security_deposit_pct >= 0),
    seller_rating_score DOUBLE PRECISION CHECK (
        seller_rating_score IS NULL OR (seller_rating_score >= 0 AND seller_rating_score <= 100)
    ),
    seller_rating_confidence seller_rating_confidence,
    seller_rating_raw JSONB,
    CHECK (btc_max IS NULL OR btc_min IS NULL OR btc_max >= btc_min),
    CHECK (fiat_max IS NULL OR fiat_min IS NULL OR fiat_max >= fiat_min),
    CHECK (last_seen >= first_seen)
);

CREATE INDEX IF NOT EXISTS idx_offers_exchange ON offers(exchange);
CREATE INDEX IF NOT EXISTS idx_offers_direction ON offers(direction);
CREATE INDEX IF NOT EXISTS idx_offers_last_seen ON offers(last_seen);
CREATE INDEX IF NOT EXISTS idx_offers_live_fiat
    ON offers(fiat_currency, id) WHERE is_live = TRUE;
CREATE INDEX IF NOT EXISTS idx_offers_fingerprint
    ON offers(exchange, fiat_currency, direction, btc_min, btc_max);
CREATE UNIQUE INDEX IF NOT EXISTS idx_offers_offer_id
    ON offers(exchange, fiat_currency, offer_id) WHERE offer_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_offers_payment_methods ON offers USING GIN (payment_methods);
