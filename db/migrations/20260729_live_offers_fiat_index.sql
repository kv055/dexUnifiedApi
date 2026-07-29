CREATE INDEX IF NOT EXISTS idx_offers_live_fiat
    ON offers(fiat_currency, id) WHERE is_live = TRUE;
