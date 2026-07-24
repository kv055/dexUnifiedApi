-- Allow payment accounts to be temporarily disabled without deleting them.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;
