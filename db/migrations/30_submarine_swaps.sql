CREATE TABLE IF NOT EXISTS submarine_swaps (
    id TEXT PRIMARY KEY, reservation_id TEXT NOT NULL DEFAULT '', trade_id BIGINT, state TEXT NOT NULL,
    lightning_output_sat BIGINT NOT NULL, lockup_amount_sat BIGINT NOT NULL,
    lockup_txid TEXT NOT NULL DEFAULT '', invoice_hash TEXT NOT NULL DEFAULT '',
    service_fee_sat BIGINT NOT NULL DEFAULT 0, miner_fee_sat BIGINT NOT NULL DEFAULT 0,
    quote_expires_at TIMESTAMPTZ, rescue_material_encrypted BYTEA NOT NULL DEFAULT ''::bytea,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_submarine_swaps_state ON submarine_swaps(state, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_submarine_swaps_lockup_txid ON submarine_swaps(lockup_txid) WHERE lockup_txid <> '';
CREATE TABLE IF NOT EXISTS liquidity_reservations (
    id TEXT PRIMARY KEY, trade_id BIGINT, outbound_sat BIGINT NOT NULL DEFAULT 0,
    inbound_sat BIGINT NOT NULL DEFAULT 0, onchain_sat BIGINT NOT NULL DEFAULT 0,
    state TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_liquidity_reservations_active ON liquidity_reservations(expires_at) WHERE state = 'active';
