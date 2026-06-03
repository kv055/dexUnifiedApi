-- NOTE: every schema/*.sql file is re-applied in order on EVERY service startup
-- (see db/schema.go — there is no migration ledger). That means this file must stay
-- forward-compatible with data that LATER migrations + the running app produce.
-- 017 widens this constraint to also allow 'bisq2', and the snapshot worker has since
-- written bisq2 offer rows. If 015 re-added the narrower set it would fail validation
-- against those existing rows on every boot, crash-looping before 017 ever runs.
-- Keep 'bisq2' here so 015 and 017 describe the same allowed set.
ALTER TABLE offers DROP CONSTRAINT IF EXISTS offers_exchange_check;
ALTER TABLE offers ADD CONSTRAINT offers_exchange_check
    CHECK (exchange IN ('bisq', 'bisq2', 'hodlhodl', 'robosats', 'peachbitcoin'));
