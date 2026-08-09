-- Source-neutral, body-free audit records for incoming payment signals. Raw
-- MIME remains in Maddy; Postgres only stores parsed evidence and a SHA-256.
CREATE TABLE IF NOT EXISTS fiat_payment_events (
    id BIGSERIAL PRIMARY KEY,
    source notification_channel NOT NULL,
    provider TEXT NOT NULL,
    account_id BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    source_event_id TEXT NOT NULL,
    sender TEXT,
    amount_minor BIGINT,
    currency fiat_currency,
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
    channel notification_channel NOT NULL,
    payment_event_id BIGINT NOT NULL REFERENCES fiat_payment_events(id) ON DELETE RESTRICT,
    confirmed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (transaction_id, channel),
    UNIQUE (payment_event_id)
);

-- Human ownership decisions are durable so Telegram buttons remain actionable
-- across Fiat Controller restarts. The event is not added to
-- fiat_transaction_confirmations until the request is approved.
CREATE TABLE IF NOT EXISTS fiat_payment_confirmation_requests (
    id BIGSERIAL PRIMARY KEY,
    transaction_id BIGINT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    payment_event_id BIGINT NOT NULL REFERENCES fiat_payment_events(id) ON DELETE RESTRICT,
    reason VARCHAR(32) NOT NULL CHECK (reason IN ('single_channel','channel_mismatch','user_selection')),
    comparison JSONB NOT NULL DEFAULT '{}'::jsonb,
    message_text TEXT NOT NULL,
    telegram_chat_id TEXT,
    telegram_message_id BIGINT,
    delivery_status VARCHAR(16) NOT NULL DEFAULT 'pending'
        CHECK (delivery_status IN ('pending','sent','error')),
    decision_status VARCHAR(16) NOT NULL DEFAULT 'pending'
        CHECK (decision_status IN ('pending','approved','rejected','expired')),
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    decided_at TIMESTAMPTZ,
    UNIQUE (payment_event_id)
);

CREATE INDEX IF NOT EXISTS fiat_payment_confirmation_requests_pending_idx
    ON fiat_payment_confirmation_requests (decision_status, delivery_status)
    WHERE decision_status = 'pending';
