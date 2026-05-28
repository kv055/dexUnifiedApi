-- Add Bisq2 as an allowed provider. Idempotent: safe to re-run on every startup.

DO $$
BEGIN
    -- 1. Rebuild provider check constraint to include 'Bisq2'.
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

    -- 2. Rebuild the category generated column to map Bisq2 → 'DEX'.
    --    Generated columns cannot be altered in-place; drop and re-add.
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'accounts' AND column_name = 'category'
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
END $$;
