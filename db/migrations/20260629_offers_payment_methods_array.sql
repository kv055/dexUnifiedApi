-- Convert offers.payment_method (scalar VARCHAR) to payment_methods (TEXT[]) on
-- existing databases. On fresh DBs where 02_offers.sql already created the column
-- as TEXT[], this is a no-op (the column check guard skips everything).

-- Drop the old fingerprint index that includes the scalar column.
DROP INDEX IF EXISTS idx_offers_fingerprint;

-- Convert VARCHAR → TEXT[] if the old scalar column still exists.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'offers'
          AND column_name = 'payment_method'
          AND data_type = 'character varying'
    ) THEN
        ALTER TABLE offers ALTER COLUMN payment_method TYPE TEXT[]
            USING CASE
                WHEN payment_method IS NOT NULL AND payment_method != ''
                THEN ARRAY[payment_method]
                ELSE NULL
            END;
    END IF;
END $$;

-- Rename to plural if the old name still exists.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'offers' AND column_name = 'payment_method'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'offers' AND column_name = 'payment_methods'
    ) THEN
        ALTER TABLE offers RENAME COLUMN payment_method TO payment_methods;
    END IF;
END $$;

-- Ensure the GIN index exists (created by 02_offers.sql on fresh DBs, but not
-- on existing DBs until this migration converts the column).
CREATE INDEX IF NOT EXISTS idx_offers_payment_methods ON offers USING GIN (payment_methods);

-- Rebuild fingerprint without payment methods.
CREATE INDEX IF NOT EXISTS idx_offers_fingerprint
    ON offers(exchange, fiat_currency, direction, btc_min, btc_max);
