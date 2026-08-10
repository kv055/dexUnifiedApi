-- Keep the intended market side alongside the transactional liquidity target.
-- This is a separate migration because the original table may already exist.
ALTER TABLE transactional_liquidity_settings
    ADD COLUMN IF NOT EXISTS target_direction TEXT NOT NULL DEFAULT 'bid'
    CHECK (target_direction IN ('bid', 'ask'));
