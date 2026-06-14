-- Drop Internal Infrastructure category; LND wallet is uncategorized (NULL).
--
-- Idempotent: EnsureUnifiedSchema re-applies every schema file on each boot, so the
-- guard must skip once the column is already the new generated form. The previous
-- guard ("category column exists") was always true, so every boot dropped and re-added
-- the column — and Postgres keeps dropped columns counting toward the 1600-column
-- limit until a table rewrite, which eventually crash-looped unified-api. We now only
-- rebuild when category is still the OLD definition (not generated, or its expression
-- still references the removed 'Internal Infrastructure' category).

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'accounts'
          AND column_name = 'category'
          AND (is_generated <> 'ALWAYS'
               OR generation_expression LIKE '%Internal Infrastructure%')
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
