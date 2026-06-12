ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS permissions JSONB,
    ADD COLUMN IF NOT EXISTS permissions_checked_at TIMESTAMPTZ;
