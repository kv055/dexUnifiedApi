CREATE TABLE IF NOT EXISTS lsp_magma_orders (
    id                    UUID PRIMARY KEY,
    pool                  TEXT NOT NULL,
    order_id              TEXT NOT NULL UNIQUE,
    public_key            TEXT NOT NULL,
    lsp_balance_sat       BIGINT NOT NULL,
    announce_channel      BOOLEAN NOT NULL,
    channel_expiry_blocks INT NOT NULL,
    order_state           TEXT NOT NULL,
    payment_state         TEXT NOT NULL DEFAULT '',
    bolt11_invoice        TEXT NOT NULL DEFAULT '',
    order_total_sat       BIGINT NOT NULL DEFAULT 0,
    fee_total_sat         BIGINT NOT NULL DEFAULT 0,
    funding_outpoint      TEXT NOT NULL DEFAULT '',
    remote_pubkey         TEXT NOT NULL DEFAULT '',
    payment_hash          TEXT NOT NULL DEFAULT '',
    auto_registered       BOOLEAN NOT NULL DEFAULT FALSE,
    failure_reason        TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_lsp_magma_orders_state ON lsp_magma_orders(order_state, updated_at DESC);
