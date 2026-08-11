CREATE TABLE IF NOT EXISTS lsp_channel_config (
    id TEXT PRIMARY KEY DEFAULT 'default' CHECK (id = 'default'),
    channel_point TEXT NOT NULL DEFAULT '', remote_pubkey TEXT NOT NULL DEFAULT '',
    provider_label TEXT NOT NULL DEFAULT '', enabled BOOLEAN NOT NULL DEFAULT FALSE,
    headroom_sat BIGINT NOT NULL DEFAULT 0 CHECK (headroom_sat >= 0),
    max_swap_fee_sat BIGINT NOT NULL DEFAULT 0 CHECK (max_swap_fee_sat >= 0),
    max_swap_fee_ppm BIGINT NOT NULL DEFAULT 0 CHECK (max_swap_fee_ppm >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO lsp_channel_config (id) VALUES ('default') ON CONFLICT DO NOTHING;
