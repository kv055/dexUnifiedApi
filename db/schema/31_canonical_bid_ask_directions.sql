-- Standardize persisted trade directions on bid/ask. PostgreSQL enum value
-- renames update every referencing row in offers, snapshots, and transactions
-- without rewriting those tables.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_enum e
        JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = 'trade_direction' AND e.enumlabel = 'buy'
    ) THEN
        ALTER TYPE trade_direction RENAME VALUE 'buy' TO 'bid';
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_enum e
        JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = 'trade_direction' AND e.enumlabel = 'sell'
    ) THEN
        ALTER TYPE trade_direction RENAME VALUE 'sell' TO 'ask';
    END IF;
END $$;
