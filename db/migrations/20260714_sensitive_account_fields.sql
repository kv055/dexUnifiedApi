-- Keep bank details in a separate encrypted text column. The legacy JSONB
-- column remains as an empty compatibility container for existing readers.
ALTER TABLE bank_accounts
    ADD COLUMN IF NOT EXISTS details_encrypted TEXT;
