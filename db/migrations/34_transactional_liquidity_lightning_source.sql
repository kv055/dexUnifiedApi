-- Require each liquidity target to name the Lightning capacity it manages.
ALTER TABLE transactional_liquidity_settings
    ADD COLUMN IF NOT EXISTS lightning_liquidity_source TEXT NOT NULL DEFAULT 'own_channels'
    CHECK (lightning_liquidity_source IN ('own_channels', 'lsp_channel'));
