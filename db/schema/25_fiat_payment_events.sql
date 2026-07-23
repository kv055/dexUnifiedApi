-- Source-neutral, body-free audit records for incoming payment signals. Raw
-- MIME remains in Maddy; Postgres only stores parsed evidence and a SHA-256.
CREATE TABLE IF NOT EXISTS fiat_payment_events (
    id BIGSERIAL PRIMARY KEY,
    source VARCHAR(16) NOT NULL CHECK (source IN ('push', 'email')),
    provider TEXT NOT NULL,
    account_id BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    source_event_id TEXT NOT NULL,
    sender TEXT,
    amount_minor BIGINT,
    currency CHAR(3),
    description TEXT,
    provider_timestamp TIMESTAMPTZ,
    received_timestamp TIMESTAMPTZ NOT NULL,
    audit_metadata JSONB,
    template_version TEXT,
    authentication_outcome TEXT,
    status VARCHAR(40) NOT NULL,
    matched_transaction_id BIGINT REFERENCES transactions(id) ON DELETE SET NULL,
    raw_message_hash CHAR(64) NOT NULL,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fiat_payment_events_source_event_key UNIQUE (source, source_event_id)
);

CREATE INDEX IF NOT EXISTS fiat_payment_events_received_idx
    ON fiat_payment_events (received_timestamp DESC);
CREATE INDEX IF NOT EXISTS fiat_payment_events_match_idx
    ON fiat_payment_events (matched_transaction_id)
    WHERE matched_transaction_id IS NOT NULL;

-- At most one signal from each channel can confirm a transaction. The unique
-- payment_event_id also prevents one source event confirming two trades.
CREATE TABLE IF NOT EXISTS fiat_transaction_confirmations (
    transaction_id BIGINT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    channel VARCHAR(16) NOT NULL CHECK (channel IN ('push', 'email')),
    payment_event_id BIGINT NOT NULL REFERENCES fiat_payment_events(id) ON DELETE RESTRICT,
    confirmed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (transaction_id, channel),
    UNIQUE (payment_event_id)
);
