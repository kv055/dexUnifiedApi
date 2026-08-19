-- Persist which CEX→DEX routing profiles the operator supports for trades.
ALTER TABLE transactional_liquidity_settings
    ADD COLUMN IF NOT EXISTS supported_routing_profiles TEXT[] NOT NULL
    DEFAULT ARRAY['Direct_main2main', 'Hub_main2main', 'Swap_main2lightning']::TEXT[];

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'transactional_liquidity_supported_routing_profiles_check'
    ) THEN
        ALTER TABLE transactional_liquidity_settings
            ADD CONSTRAINT transactional_liquidity_supported_routing_profiles_check
            CHECK (
                cardinality(supported_routing_profiles) >= 1
                AND supported_routing_profiles <@ ARRAY[
                    'Direct_main2main',
                    'Hub_main2main',
                    'Swap_main2lightning'
                ]::TEXT[]
            );
    END IF;
END $$;
