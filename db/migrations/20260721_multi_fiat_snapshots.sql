-- Preserve historical EUR candles while allowing one candle series per fiat.
ALTER TABLE ohlc
    ADD COLUMN IF NOT EXISTS fiat_currency CHAR(3) NOT NULL DEFAULT 'EUR'
    CHECK (fiat_currency = UPPER(fiat_currency));

ALTER TABLE ohlc DROP CONSTRAINT IF EXISTS ohlc_pkey;
ALTER TABLE ohlc ADD PRIMARY KEY (time, fiat_currency);

-- Offer IDs are only unique inside a DEX market. Keeping currency in the
-- identity avoids collapsing otherwise distinct offers from different books.
DROP INDEX IF EXISTS idx_offers_offer_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_offers_offer_id
    ON offers(exchange, fiat_currency, offer_id) WHERE offer_id IS NOT NULL;
