-- fiat_phone_callback_attempts is a safe operational audit log for every
-- PushPush phone callback. It records blocked attempts without persisting
-- credentials or notification bodies.
CREATE TABLE IF NOT EXISTS fiat_phone_callback_attempts (
    id BIGSERIAL PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    endpoint VARCHAR(32) NOT NULL,
    outcome VARCHAR(16) NOT NULL,
    status_code SMALLINT NOT NULL,
    device_id VARCHAR(128),
    app VARCHAR(64),
    detail TEXT
);

CREATE INDEX IF NOT EXISTS idx_fiat_phone_callback_attempts_received_at
    ON fiat_phone_callback_attempts (received_at DESC);
