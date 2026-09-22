-- Persistent Bisq v1 execution contracts: payment-account mapping, designated
-- funding destination, accepted quotes/idempotency, and per-daemon worker claims.
CREATE TABLE IF NOT EXISTS bisq_payment_account_mappings (
    id BIGSERIAL PRIMARY KEY,
    local_account_id BIGINT NOT NULL REFERENCES accounts(id),
    payment_method_name TEXT NOT NULL,
    currency TEXT NOT NULL,
    bisq_payment_account_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (local_account_id, payment_method_name, currency),
    UNIQUE (bisq_payment_account_id)
);

CREATE TABLE IF NOT EXISTS bisq_funding_destinations (
    id BIGSERIAL PRIMARY KEY,
    address TEXT NOT NULL UNIQUE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS bisq_funding_destinations_one_active_uidx
    ON bisq_funding_destinations (is_active) WHERE is_active;

CREATE TABLE IF NOT EXISTS bisq_execution_quotes (
    id UUID PRIMARY KEY,
    offer_id TEXT NOT NULL,
    trade_mode TEXT NOT NULL,
    payment_account_id TEXT NOT NULL,
    local_account_id BIGINT REFERENCES accounts(id),
    quote JSONB NOT NULL,
    limits JSONB NOT NULL,
    resolved_route JSONB NOT NULL,
    selected_offer JSONB,
    payment_method TEXT,
    funding_address TEXT NOT NULL,
    idempotency_key TEXT UNIQUE,
    transaction_id BIGINT REFERENCES transactions(id),
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    deadline_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS bisq_execution_quotes_offer_idx
    ON bisq_execution_quotes(offer_id, created_at DESC);

CREATE TABLE IF NOT EXISTS bisq_execution_claims (
    daemon_key TEXT PRIMARY KEY,
    transaction_id BIGINT NOT NULL REFERENCES transactions(id),
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ NOT NULL
);
