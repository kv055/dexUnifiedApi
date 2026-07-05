-- Add provider and category columns to wallet_addresses.
--
-- provider: where the wallet is hosted (e.g. Kraken, Binance).
-- category: wallet_address_category enum, either 'cex_deposit' (exchange deposit
--           wallet) or 'hop_deposit' (intermediate/hop wallet).

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'wallet_address_category'
          AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE wallet_address_category AS ENUM ('cex_deposit', 'hop_deposit');
    END IF;
END $$;

ALTER TABLE wallet_addresses
    ADD COLUMN IF NOT EXISTS provider TEXT,
    ADD COLUMN IF NOT EXISTS category wallet_address_category;
