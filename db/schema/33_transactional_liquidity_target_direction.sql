-- Older deployments applied the original singleton table before
-- target_direction became part of the runtime schema sequence. Keep this
-- upgrade separate so schema_migrations applies it to those databases.
ALTER TABLE transactional_liquidity_settings
    ADD COLUMN IF NOT EXISTS target_direction TEXT NOT NULL DEFAULT 'bid'
    CHECK (target_direction IN ('bid', 'ask'));
