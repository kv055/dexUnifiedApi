-- Collapse payment_methods linking to account_id only.
-- Drop bank_account_id and wallet_address_id without porting those links;
-- the operator re-links Bank / Fiat Transmitter methods manually afterward.
-- Also enforce one bank_accounts row per (account_id, currency).

-- Deactivate methods whose only link was bank_account_id or wallet_address_id
-- (upgrade path where those columns still exist).
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'payment_methods'
          AND column_name = 'bank_account_id'
    ) AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'payment_methods'
          AND column_name = 'wallet_address_id'
    ) THEN
        UPDATE payment_methods
        SET is_active = FALSE,
            updated_at = CURRENT_TIMESTAMP
        WHERE account_id IS NULL
          AND (bank_account_id IS NOT NULL OR wallet_address_id IS NOT NULL);
    END IF;
END $$;

ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_single_link_check;
ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_link_category_check;

ALTER TABLE payment_methods DROP COLUMN IF EXISTS is_linked;
ALTER TABLE payment_methods DROP COLUMN IF EXISTS bank_account_id;
ALTER TABLE payment_methods DROP COLUMN IF EXISTS wallet_address_id;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'payment_methods'
          AND column_name = 'is_linked'
    ) THEN
        ALTER TABLE payment_methods
            ADD COLUMN is_linked BOOLEAN GENERATED ALWAYS AS (account_id IS NOT NULL) STORED;
    END IF;
END $$;

ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_link_category_check;
ALTER TABLE payment_methods ADD CONSTRAINT payment_methods_link_category_check CHECK (
    account_id IS NULL OR category IN ('Bank', 'Fiat Transmitter')
);

-- Keep the lowest id when duplicate (account_id, currency) rows exist.
DELETE FROM bank_accounts ba
USING bank_accounts newer
WHERE ba.account_id = newer.account_id
  AND upper(ba.currency) = upper(newer.currency)
  AND ba.id > newer.id;

UPDATE bank_accounts
SET currency = upper(currency)
WHERE currency <> upper(currency);

ALTER TABLE bank_accounts DROP CONSTRAINT IF EXISTS bank_accounts_account_currency_key;
ALTER TABLE bank_accounts ADD CONSTRAINT bank_accounts_account_currency_key UNIQUE (account_id, currency);
