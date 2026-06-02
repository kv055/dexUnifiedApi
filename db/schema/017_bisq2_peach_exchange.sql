-- Allow bisq2 and peachbitcoin offers/snapshots to persist.
--
-- The snapshot worker fetches 5 exchanges, but only the original three were
-- permitted by these CHECK constraints: 015 widened `offers` to add
-- peachbitcoin (never bisq2), and `snapshots` was never widened at all. Because
-- the per-exchange snapshot transaction writes `snapshots` first, a rejected
-- bisq2/peach row poisoned the whole transaction, so their `offers` rows silently
-- failed too — they lived only in the in-memory hub, invisible to analytics.
--
-- Idempotent: drops then re-adds the CHECK constraints on every startup. Unlike
-- the generated `category` column in 016, CHECK constraints do not consume
-- per-table column slots, so an unconditional DROP/ADD is safe to re-run.
ALTER TABLE offers DROP CONSTRAINT IF EXISTS offers_exchange_check;
ALTER TABLE offers ADD CONSTRAINT offers_exchange_check
    CHECK (exchange IN ('bisq', 'bisq2', 'hodlhodl', 'robosats', 'peachbitcoin'));

ALTER TABLE snapshots DROP CONSTRAINT IF EXISTS snapshots_exchange_check;
ALTER TABLE snapshots ADD CONSTRAINT snapshots_exchange_check
    CHECK (exchange IN ('bisq', 'bisq2', 'hodlhodl', 'robosats', 'peachbitcoin'));
