-- Record which unit each offer's volume is fixed in. Makers fix the volume in
-- one unit and the other floats with the price:
--   'BTC'          the maker fixed a bitcoin amount (Bisq 1, Peach); fiat_min /
--                  fiat_max are only the conversion at the last snapshot's price.
--   fiat code      the maker fixed a fiat amount (Hodl Hodl, RoboSats, Bisq 2,
--                  Mostro); btc_min / btc_max are only the conversion at the
--                  last snapshot's price and drift as the price moves.
-- Consumers must read the fixed pair as the offer's volume and treat the other
-- pair as a point-in-time conversion.
ALTER TABLE offers ADD COLUMN IF NOT EXISTS volume_denomination VARCHAR(8);

-- Backfill from the venue: every venue fixes the same unit for all offers the
-- snapshot worker ingests (Bisq 2 is only ingested with fiat-side amounts).
UPDATE offers SET volume_denomination = CASE
        WHEN exchange IN ('bisq', 'peachbitcoin') THEN 'BTC'
        ELSE fiat_currency::text
    END
WHERE volume_denomination IS NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'offers'::regclass
          AND conname = 'offers_volume_denomination_check'
    ) THEN
        ALTER TABLE offers ADD CONSTRAINT offers_volume_denomination_check CHECK (
            volume_denomination IS NULL
            OR volume_denomination = 'BTC'
            OR volume_denomination = fiat_currency::text
        );
    END IF;
END $$;

-- compat: backward-compatible
-- rollback: the previous release neither reads nor writes volume_denomination;
-- its inserts leave the column NULL, which the constraint allows and dexMonitor
-- reads as the legacy BTC-sized behaviour.
