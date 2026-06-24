-- Ensure accounts has a usable unique constraint on (provider, category).
-- Prod may be missing it if 022's step 3 partially ran (dropped the old
-- UNIQUE(provider) but failed to add the composite one). Idempotent.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'accounts' AND constraint_name = 'accounts_provider_category_key'
    ) THEN
        -- Also drop the old single-column constraint if it's still hanging around.
        IF EXISTS (
            SELECT 1 FROM information_schema.table_constraints
            WHERE table_name = 'accounts' AND constraint_name = 'accounts_provider_key'
        ) THEN
            ALTER TABLE accounts DROP CONSTRAINT accounts_provider_key;
        END IF;
        ALTER TABLE accounts ADD CONSTRAINT accounts_provider_category_key UNIQUE (provider, category);
    END IF;
END $$;
