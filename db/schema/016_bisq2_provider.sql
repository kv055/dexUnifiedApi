-- Add Bisq2 as an allowed provider. Idempotent: safe to re-run on every startup.
--
-- WARNING: this file is applied on EVERY service startup. It must never drop and
-- re-add a column unconditionally: Postgres counts dropped columns toward the hard
-- 1600-column-per-table limit and never reclaims those catalog slots, so an
-- unconditional DROP/ADD of the generated `category` column eventually exhausts the
-- `accounts` table and crash-loops the service (observed in prod: 1590 dropped
-- `category` slots). Each block below therefore changes the schema only when the
-- desired Bisq2 state is not already present.

DO $$
BEGIN
    -- 1. Ensure the provider check constraint allows 'Bisq2'.
    --    Rebuild only when missing or when it does not yet allow Bisq2.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'accounts_provider_allowed_check'
          AND conrelid = 'accounts'::regclass
          AND pg_get_constraintdef(oid) LIKE '%''Bisq2''%'
    ) THEN
        IF EXISTS (
            SELECT 1 FROM pg_constraint
            WHERE conname = 'accounts_provider_allowed_check'
              AND conrelid = 'accounts'::regclass
        ) THEN
            ALTER TABLE accounts DROP CONSTRAINT accounts_provider_allowed_check;
        END IF;

        ALTER TABLE accounts ADD CONSTRAINT accounts_provider_allowed_check CHECK (
            provider IN (
                'WISE', 'PayPal', 'Revolut', 'N26',
                'Robosats', 'Bisq', 'Bisq2', 'HodlHodl',
                'Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi',
                'LND wallet'
            )
        );
    END IF;

    -- 2. Ensure the generated `category` column maps Bisq2 → 'DEX'.
    --    Generated columns cannot be altered in place, so a change needs drop+re-add
    --    — but only do so when the column is missing or its definition does not yet
    --    reference Bisq2. A no-op restart must NOT touch the column (see warning).
    IF NOT EXISTS (
        SELECT 1
        FROM pg_attribute a
        JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
        WHERE a.attrelid = 'accounts'::regclass
          AND a.attname = 'category'
          AND NOT a.attisdropped
          AND pg_get_expr(ad.adbin, ad.adrelid) LIKE '%Bisq2%'
    ) THEN
        IF EXISTS (
            SELECT 1 FROM pg_attribute
            WHERE attrelid = 'accounts'::regclass
              AND attname = 'category'
              AND NOT attisdropped
        ) THEN
            ALTER TABLE accounts DROP COLUMN category;
        END IF;

        ALTER TABLE accounts ADD COLUMN category TEXT GENERATED ALWAYS AS (
            CASE
                WHEN provider IN ('WISE', 'PayPal', 'Revolut', 'N26') THEN 'Fiat Transmitter'
                WHEN provider IN ('Robosats', 'Bisq', 'Bisq2', 'HodlHodl') THEN 'DEX'
                WHEN provider IN ('Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi') THEN 'CEX'
                WHEN provider = 'LND wallet' THEN 'Internal Infrastructure'
            END
        ) STORED;
    END IF;
END $$;
