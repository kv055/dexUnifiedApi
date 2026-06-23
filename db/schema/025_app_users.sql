-- App login accounts for the dexMonitor dashboard sign-in.
-- Distinct from the trading `accounts` table: these are humans who log into the UI,
-- not payment/exchange accounts. First-user registration bootstraps the first admin;
-- registration is then disabled in the app while any row exists here.
--
-- Created by the dex_unified_api role, so DML is granted to dex_monitor automatically
-- via the ALTER DEFAULT PRIVILEGES in bootstrap.go (same pattern as 022_bank_accounts).
--
-- Idempotent: CREATE TABLE IF NOT EXISTS makes re-running on boot a no-op.

CREATE TABLE IF NOT EXISTS app_users (
    id            BIGSERIAL    PRIMARY KEY,
    username      TEXT         NOT NULL UNIQUE,
    password_hash TEXT         NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_login    TIMESTAMPTZ
);
