-- Upgrade existing transactional settings to the backend-owned routing model.
-- Custom values can be backfilled safely. Historical capped values lived only
-- in browser storage, so they intentionally remain NULL until the operator
-- saves the form again.
ALTER TABLE transactional_liquidity_settings
    ADD COLUMN IF NOT EXISTS amount_source TEXT,
    ADD COLUMN IF NOT EXISTS amount_fiat DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS fee_tier TEXT,
    ADD COLUMN IF NOT EXISTS coupled_hedge BOOLEAN;

DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'transactional_liquidity_settings'
          AND column_name = 'custom_amount'
    ) THEN
        UPDATE transactional_liquidity_settings
        SET amount_source = CASE WHEN custom_amount IS NOT NULL THEN 'custom' ELSE 'capped_volume' END
        WHERE amount_source IS NULL;
        UPDATE transactional_liquidity_settings
        SET amount_fiat = custom_amount
        WHERE amount_fiat IS NULL AND custom_amount IS NOT NULL;
        ALTER TABLE transactional_liquidity_settings DROP COLUMN custom_amount;
    END IF;
END $$;

UPDATE transactional_liquidity_settings
SET amount_source = 'capped_volume'
WHERE amount_source IS NULL;

UPDATE transactional_liquidity_settings SET fee_tier = 'economic' WHERE fee_tier IS NULL;
UPDATE transactional_liquidity_settings SET coupled_hedge = FALSE WHERE coupled_hedge IS NULL;

ALTER TABLE transactional_liquidity_settings
    ALTER COLUMN amount_source SET DEFAULT 'capped_volume',
    ALTER COLUMN amount_source SET NOT NULL,
    ALTER COLUMN fee_tier SET DEFAULT 'economic',
    ALTER COLUMN fee_tier SET NOT NULL,
    ALTER COLUMN coupled_hedge SET DEFAULT FALSE,
    ALTER COLUMN coupled_hedge SET NOT NULL;

DO $$ BEGIN
    ALTER TABLE transactional_liquidity_settings
        ADD CONSTRAINT transactional_liquidity_amount_source_check
        CHECK (amount_source IN ('custom', 'capped_volume'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    ALTER TABLE transactional_liquidity_settings
        ADD CONSTRAINT transactional_liquidity_amount_fiat_check
        CHECK (amount_fiat IS NULL OR amount_fiat > 0);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    ALTER TABLE transactional_liquidity_settings
        ADD CONSTRAINT transactional_liquidity_fee_tier_check
        CHECK (fee_tier = 'economic');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE OR REPLACE FUNCTION bump_transactional_routing_version()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.fiat_currency, NEW.amount_source, NEW.amount_fiat, NEW.trade_mode,
        NEW.target_direction, NEW.fee_tier, NEW.coupled_hedge)
       IS DISTINCT FROM
       (OLD.fiat_currency, OLD.amount_source, OLD.amount_fiat, OLD.trade_mode,
        OLD.target_direction, OLD.fee_tier, OLD.coupled_hedge) THEN
        NEW.config_version := OLD.config_version + 1;
        NEW.updated_at := CURRENT_TIMESTAMP;
    END IF;
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS transactional_routing_version ON transactional_liquidity_settings;
CREATE TRIGGER transactional_routing_version
BEFORE UPDATE ON transactional_liquidity_settings
FOR EACH ROW EXECUTE FUNCTION bump_transactional_routing_version();

CREATE OR REPLACE FUNCTION notify_snapshot_routing_change()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('snapshot_routing_changed', TG_TABLE_NAME);
    RETURN NULL;
END $$;

DO $$
DECLARE table_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'transactional_liquidity_settings', 'mm_cex_provider_policies',
        'accounts', 'api_keys'
    ] LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS snapshot_routing_changed ON %I', table_name);
        EXECUTE format(
            'CREATE TRIGGER snapshot_routing_changed AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH STATEMENT EXECUTE FUNCTION notify_snapshot_routing_change()',
            table_name
        );
    END LOOP;
END $$;
