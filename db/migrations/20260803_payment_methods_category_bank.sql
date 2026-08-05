-- Rename payment_methods.category value 'Bank Transfer' → 'Bank'.
ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_category_allowed_check;
ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_link_category_check;

UPDATE payment_methods
SET category = 'Bank'
WHERE category = 'Bank Transfer';

ALTER TABLE payment_methods ADD CONSTRAINT payment_methods_category_allowed_check CHECK (
    category IN ('Cash', 'Bank', 'Fiat Transmitter', 'Gift Cards', 'Crypto', 'Others')
);

-- Link category rule: only Bank and Fiat Transmitter may set account_id.
-- (Historical builds also had bank_account_id / wallet_address_id; those columns
-- are removed by 20260805_payment_methods_account_only.sql.)
ALTER TABLE payment_methods ADD CONSTRAINT payment_methods_link_category_check CHECK (
    account_id IS NULL OR category IN ('Bank', 'Fiat Transmitter')
);
