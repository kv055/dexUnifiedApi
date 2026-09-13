-- Bind an executable Mostro quote to the Nostr node and signed event that
-- advertised it. Mostro order ids are only unique within a node.
ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS venue_node_pubkey VARCHAR(64),
    ADD COLUMN IF NOT EXISTS venue_node_name VARCHAR(128),
    ADD COLUMN IF NOT EXISTS source_event_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS source_event_created_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS venue_protocol_version SMALLINT CHECK (venue_protocol_version IN (1, 2));

CREATE UNIQUE INDEX IF NOT EXISTS offers_mostro_node_order_uidx
    ON offers (exchange, venue_node_pubkey, offer_id)
    WHERE exchange = 'mostro' AND venue_node_pubkey IS NOT NULL AND offer_id IS NOT NULL;

-- The encrypted secret is an application-encrypted BIP-39 mnemonic. The
-- counter is allocated under a row lock so a trade key is never reused.
CREATE TABLE IF NOT EXISTS mostro_identities (
    id BIGSERIAL PRIMARY KEY,
    label VARCHAR(128) NOT NULL UNIQUE,
    encrypted_mnemonic BYTEA NOT NULL,
    next_trade_index BIGINT NOT NULL DEFAULT 0 CHECK (next_trade_index >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS mostro_trade_sessions (
    id BIGSERIAL PRIMARY KEY,
    transaction_id BIGINT NOT NULL UNIQUE REFERENCES transactions(id) ON DELETE CASCADE,
    identity_id BIGINT NOT NULL REFERENCES mostro_identities(id),
    trade_index BIGINT NOT NULL,
    trade_pubkey VARCHAR(64) NOT NULL,
    node_pubkey VARCHAR(64) NOT NULL,
    order_id VARCHAR(128) NOT NULL,
    request_id BIGINT NOT NULL UNIQUE CHECK (request_id > 0),
    protocol_version SMALLINT NOT NULL CHECK (protocol_version IN (1, 2)),
    state VARCHAR(48) NOT NULL DEFAULT 'created',
    peer_pubkey VARCHAR(64),
    last_event_id VARCHAR(64),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (identity_id, trade_index),
    UNIQUE (node_pubkey, order_id, trade_pubkey)
);

CREATE TABLE IF NOT EXISTS mostro_payments (
    id BIGSERIAL PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES mostro_trade_sessions(id) ON DELETE CASCADE,
    purpose VARCHAR(16) NOT NULL CHECK (purpose IN ('bond', 'escrow')),
    invoice TEXT NOT NULL,
    payment_hash VARCHAR(64),
    amount_sat BIGINT NOT NULL CHECK (amount_sat >= 0),
    status VARCHAR(24) NOT NULL DEFAULT 'requested',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (session_id, purpose)
);

CREATE TABLE IF NOT EXISTS mostro_outbox (
    id BIGSERIAL PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES mostro_trade_sessions(id) ON DELETE CASCADE,
    request_id BIGINT NOT NULL CHECK (request_id > 0),
    event_id VARCHAR(64) NOT NULL,
    event_json JSONB NOT NULL,
    relay_acks JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (request_id, event_id)
);

CREATE TABLE IF NOT EXISTS mostro_inbox_events (
    event_id VARCHAR(64) PRIMARY KEY,
    session_id BIGINT REFERENCES mostro_trade_sessions(id) ON DELETE SET NULL,
    node_pubkey VARCHAR(64) NOT NULL,
    event_created_at TIMESTAMPTZ NOT NULL,
    event_json JSONB NOT NULL,
    plaintext_ciphertext BYTEA,
    processed_at TIMESTAMPTZ,
    processing_error TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS mostro_outbox_ready_idx
    ON mostro_outbox (next_attempt_at, id) WHERE status IN ('pending', 'retry');
CREATE INDEX IF NOT EXISTS mostro_inbox_unprocessed_idx
    ON mostro_inbox_events (received_at, event_id) WHERE processed_at IS NULL;
