-- Decouple accounts.category from a GENERATED expression so open-ended "Bank"
-- provider names can be stored without whitelisting them in a CASE.
-- Also swaps UNIQUE(provider) → UNIQUE(provider, category) so the same institution
-- (e.g. Revolut) can appear as both 'Fiat Transmitter' and 'Bank'.
-- Creates the bank_accounts child table.
--
-- Idempotent: each block guards on current DB state so re-running on boot is a no-op.

DO $$
BEGIN
    -- 1. Convert generated category → plain stored column (runs once).
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name   = 'accounts'
          AND column_name  = 'category'
          AND is_generated = 'ALWAYS'
    ) THEN
        ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_category_allowed_check;
        ALTER TABLE accounts DROP COLUMN category;

        ALTER TABLE accounts ADD COLUMN category TEXT;

        -- Backfill existing rows using the canonical mapping.
        UPDATE accounts SET category =
            CASE
                WHEN lower(provider) IN ('wise', 'paypal', 'revolut', 'n26')
                    THEN 'Fiat Transmitter'
                WHEN lower(provider) IN ('robosats', 'bisq', 'bisq2', 'hodlhodl', 'peach', 'peach bitcoin', 'peachbitcoin')
                    THEN 'DEX'
                WHEN lower(provider) IN ('kraken', 'coinbase', 'bitstamp', 'binance', 'bybit', 'kucoin', 'huobi')
                    THEN 'CEX'
                ELSE NULL
            END;

        ALTER TABLE accounts ADD CONSTRAINT accounts_category_allowed_check CHECK (
            category IS NULL OR category IN ('Fiat Transmitter', 'DEX', 'CEX', 'Bank')
        );
    END IF;

    -- 2. Expand the allowed-category check to include 'Bank' if it doesn't yet.
    IF EXISTS (
        SELECT 1
        FROM information_schema.table_constraints tc
        JOIN information_schema.check_constraints cc USING (constraint_name)
        WHERE tc.table_name       = 'accounts'
          AND tc.constraint_name  = 'accounts_category_allowed_check'
          AND cc.check_clause NOT LIKE '%Bank%'
    ) THEN
        ALTER TABLE accounts DROP CONSTRAINT accounts_category_allowed_check;
        ALTER TABLE accounts ADD CONSTRAINT accounts_category_allowed_check CHECK (
            category IS NULL OR category IN ('Fiat Transmitter', 'DEX', 'CEX', 'Bank')
        );
    END IF;

    -- 3. Swap UNIQUE(provider) → UNIQUE(provider, category).
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'accounts' AND constraint_name = 'accounts_provider_key'
    ) THEN
        ALTER TABLE accounts DROP CONSTRAINT accounts_provider_key;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'accounts' AND constraint_name = 'accounts_provider_category_key'
    ) THEN
        ALTER TABLE accounts ADD CONSTRAINT accounts_provider_category_key UNIQUE (provider, category);
    END IF;
END $$;

-- 4. Bank payable-account table (FK cascades on account deletion).
CREATE TABLE IF NOT EXISTS bank_accounts (
    id           BIGSERIAL PRIMARY KEY,
    account_id   BIGINT      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    label        TEXT,
    owner_name   TEXT        NOT NULL,
    currency     TEXT        NOT NULL,
    country      TEXT,
    scheme       TEXT        NOT NULL,
    details      JSONB       NOT NULL DEFAULT '{}',
    is_active    BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS bank_accounts_account_id_idx ON bank_accounts(account_id);
