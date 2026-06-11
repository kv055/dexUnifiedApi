-- Drop Internal Infrastructure category; LND wallet is uncategorized (NULL).

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'accounts'
          AND column_name = 'category'
    ) THEN
        ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_category_allowed_check;
        ALTER TABLE accounts DROP COLUMN category;

        ALTER TABLE accounts ADD COLUMN category TEXT GENERATED ALWAYS AS (
            CASE
                WHEN provider IN ('WISE', 'PayPal', 'Revolut', 'N26') THEN 'Fiat Transmitter'
                WHEN provider IN ('Robosats', 'Bisq', 'Bisq2', 'HodlHodl') THEN 'DEX'
                WHEN provider IN ('Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi') THEN 'CEX'
            END
        ) STORED;

        ALTER TABLE accounts ADD CONSTRAINT accounts_category_allowed_check CHECK (
            category IS NULL OR category IN (
                'Fiat Transmitter',
                'DEX',
                'CEX'
            )
        );
    END IF;
END $$;
