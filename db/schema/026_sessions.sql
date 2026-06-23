-- Server-side session store for the dexMonitor login, backing
-- github.com/alexedwards/scs/postgresstore. The columns and types below are exactly
-- what postgresstore expects (it queries this table but does NOT create it itself).
--
-- Created by the dex_unified_api role, so DML is granted to dex_monitor automatically
-- via the ALTER DEFAULT PRIVILEGES in bootstrap.go (same pattern as 022_bank_accounts).
--
-- Idempotent: CREATE TABLE / CREATE INDEX IF NOT EXISTS make re-running on boot a no-op.

CREATE TABLE IF NOT EXISTS sessions (
    token  TEXT         PRIMARY KEY,
    data   BYTEA        NOT NULL,
    expiry TIMESTAMPTZ  NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expiry);
