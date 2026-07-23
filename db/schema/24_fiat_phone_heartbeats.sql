-- fiat_phone_heartbeats is an append-only audit log of successful MacroDroid
-- phone check-ins received by dexFiatController PushPush.
CREATE TABLE IF NOT EXISTS fiat_phone_heartbeats (
    id BIGSERIAL PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    device_id VARCHAR(128) NOT NULL,
    remote_addr TEXT,
    user_agent TEXT
);

CREATE INDEX IF NOT EXISTS idx_fiat_phone_heartbeats_received_at
    ON fiat_phone_heartbeats (received_at DESC);
