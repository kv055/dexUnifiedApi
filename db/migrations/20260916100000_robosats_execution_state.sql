-- Bind RoboSats offers to a stable federation coordinator alias. Numeric
-- order ids are only unique within a coordinator.
ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS venue_coordinator_id VARCHAR(64);

CREATE UNIQUE INDEX IF NOT EXISTS offers_robosats_coordinator_order_uidx
    ON offers (exchange, venue_coordinator_id, offer_id)
    WHERE exchange = 'robosats' AND venue_coordinator_id IS NOT NULL AND offer_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS execution_quotes (
    id UUID PRIMARY KEY,
    offer_db_id BIGINT REFERENCES offers(id) ON DELETE SET NULL,
    exchange VARCHAR(32) NOT NULL,
    coordinator_id VARCHAR(64),
    offer_id VARCHAR(128) NOT NULL,
    payment_method VARCHAR(128) NOT NULL,
    input_unit VARCHAR(8) NOT NULL CHECK (input_unit IN ('btc', 'fiat')),
    input_amount NUMERIC(18, 8) NOT NULL,
    fiat_amount NUMERIC(18, 8) NOT NULL,
    btc_amount NUMERIC(18, 8) NOT NULL,
    max_bond_sat BIGINT NOT NULL CHECK (max_bond_sat >= 0),
    max_escrow_sat BIGINT NOT NULL CHECK (max_escrow_sat > 0),
    bond_fee_sat BIGINT NOT NULL CHECK (bond_fee_sat >= 0),
    escrow_fee_sat BIGINT NOT NULL CHECK (escrow_fee_sat >= 0),
    route JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    transaction_id BIGINT UNIQUE REFERENCES transactions(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS execution_quotes_offer_idx
    ON execution_quotes (exchange, coordinator_id, offer_id, created_at DESC);

CREATE TABLE IF NOT EXISTS robosats_trade_sessions (
    id BIGSERIAL PRIMARY KEY,
    transaction_id BIGINT NOT NULL UNIQUE REFERENCES transactions(id) ON DELETE CASCADE,
    credential_id BIGINT NOT NULL REFERENCES api_keys(id),
    coordinator_id VARCHAR(64) NOT NULL,
    order_id VARCHAR(128) NOT NULL,
    quote_id UUID REFERENCES execution_quotes(id),
    phase VARCHAR(48) NOT NULL DEFAULT 'created',
    last_error TEXT,
    session_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS robosats_active_robot_coordinator_uidx
    ON robosats_trade_sessions (credential_id, coordinator_id)
    WHERE phase NOT IN ('chat_sent', 'needs_review');

CREATE TABLE IF NOT EXISTS robosats_payments (
    id BIGSERIAL PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES robosats_trade_sessions(id) ON DELETE CASCADE,
    purpose VARCHAR(16) NOT NULL CHECK (purpose IN ('bond', 'escrow')),
    invoice TEXT NOT NULL,
    payment_hash VARCHAR(64),
    amount_sat BIGINT NOT NULL CHECK (amount_sat >= 0),
    status VARCHAR(24) NOT NULL DEFAULT 'requested',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (session_id, purpose)
);
