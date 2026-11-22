-- Legacy VPS databases carry allowed-value CHECK constraints on the
-- payment_methods arrays:
--   payment_methods_currencies_allowed_check  CHECK (currencies <@ ARRAY['EUR'::text, ...])
--   payment_methods_sources_allowed_check     CHECK (sources <@ ARRAY['bisq'::text, ...])
-- 20261120180000 converts both columns to fiat_currency[] / dex_provider[].
-- PostgreSQL re-checks the constraints after ALTER COLUMN TYPE, where they
-- compare fiat_currency[] <@ text[] and abort with SQLSTATE 42883 ("operator
-- does not exist") — this rolled back the e111d85 release. The enum types are
-- the allowlist now (the stale sources check even rejects mostro), so drop the
-- redundant checks instead of rewriting them, as 20260925200000 did for
-- offers / snapshots.
-- Fresh installs never had these constraints, so this is a no-op there.
--
-- BACKDATED ON PURPOSE. AGENTS.md requires new timestamps to be newer than
-- every existing migration, but 20261120180000 is immutable and has never
-- applied on the legacy database: the runner applies pending files in
-- filename order and stops at the first failure, so a repair sorting after it
-- would never run. This file sorts directly before it. Databases that already
-- applied 20261120180000 (fresh / typed installs) run it afterwards as a no-op.
--
-- compat: backward-compatible
-- rollback: the previous release never relies on these checks; the enum
-- types it binds (?::fiat_currency[] / ?::dex_provider[]) bound the values.

ALTER TABLE IF EXISTS payment_methods DROP CONSTRAINT IF EXISTS payment_methods_currencies_allowed_check;
ALTER TABLE IF EXISTS payment_methods DROP CONSTRAINT IF EXISTS payment_methods_sources_allowed_check;
