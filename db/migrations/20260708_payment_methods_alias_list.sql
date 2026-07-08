ALTER TABLE payment_methods
    ADD COLUMN IF NOT EXISTS alias_list TEXT[] NOT NULL DEFAULT '{}';
