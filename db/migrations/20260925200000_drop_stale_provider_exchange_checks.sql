-- Legacy prod databases converted offers.exchange and snapshots.exchange from
-- VARCHAR to the dex_provider enum (20260830153000) but kept the varchar-era
-- CHECK constraints, which whitelist only the five pre-mostro providers.
-- Every mostro write since has failed with SQLSTATE 23514 even though the
-- enum itself allows 'mostro' (20260907192500). The enum is the allowlist
-- now; drop the redundant checks instead of extending them so the next new
-- provider needs no second change. Fresh installs never had these
-- constraints, so this is a no-op there.
ALTER TABLE IF EXISTS offers DROP CONSTRAINT IF EXISTS offers_exchange_check;
ALTER TABLE IF EXISTS snapshots DROP CONSTRAINT IF EXISTS snapshots_exchange_check;
