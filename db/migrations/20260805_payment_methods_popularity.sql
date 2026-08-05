-- Count how often a payment method appears on newly inserted offers.
-- Starts at 0; the snapshot writer increments on each first-seen offer.
ALTER TABLE payment_methods
    ADD COLUMN IF NOT EXISTS popularity INTEGER NOT NULL DEFAULT 0;
