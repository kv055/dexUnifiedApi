CREATE TABLE IF NOT EXISTS bank_accounts (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    label TEXT,
    owner_name TEXT NOT NULL,
    currency fiat_currency NOT NULL,
    country TEXT,
    scheme TEXT NOT NULL,
    details_encrypted TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT bank_accounts_account_currency_key UNIQUE (account_id, currency)
);

CREATE INDEX IF NOT EXISTS bank_accounts_account_id_idx ON bank_accounts(account_id);
