ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS seller_rating_signals JSONB;
