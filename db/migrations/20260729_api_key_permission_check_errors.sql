ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS permission_check_error TEXT;
