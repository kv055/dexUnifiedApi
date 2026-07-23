-- Record the independently confirmed ways an incoming payment can be detected.
-- NULL means that no notification channel has been confirmed for the account.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'notification_channel'
          AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE notification_channel AS ENUM ('push', 'email');
    END IF;
END $$;

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS notification_channels notification_channel[];
