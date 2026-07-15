ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS payment_tag TEXT;
