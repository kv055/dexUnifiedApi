-- Persist Telegram payment-ownership decisions so a Fiat Controller restart
-- does not invalidate an unanswered inline button.
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
