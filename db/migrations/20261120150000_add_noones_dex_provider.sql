-- Snapshot-only fiat P2P venue (offer ingestion + inline maker reputation;
-- no taker connector yet). Public book is POST offer/all on api.noones.com.
-- compat: backward-compatible
-- rollback: an added enum value is inert for the previous release — it never
-- writes 'noones' rows, and the offers/snapshots/payment_methods queries it
-- does run are unaffected by a wider dex_provider allowlist. IF NOT EXISTS
-- also makes the statement idempotent for a rolled-back-then-redeployed DB.
ALTER TYPE dex_provider ADD VALUE IF NOT EXISTS 'noones';
