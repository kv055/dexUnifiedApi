-- Rename payment_methods.category value 'Bank Transfer' → 'Bank'.
ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_category_allowed_check;
ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_link_category_check;

UPDATE payment_methods
SET category = 'Bank'
WHERE category = 'Bank Transfer';

ALTER TABLE payment_methods ADD CONSTRAINT payment_methods_category_allowed_check CHECK (
    category IN ('Cash', 'Bank', 'Fiat Transmitter', 'Gift Cards', 'Crypto', 'Others')
);

ALTER TABLE payment_methods ADD CONSTRAINT payment_methods_link_category_check CHECK (
    (bank_account_id   IS NULL OR category = 'Bank')
    AND (account_id        IS NULL OR category = 'Fiat Transmitter')
    AND (wallet_address_id IS NULL OR category = 'Crypto')
);
