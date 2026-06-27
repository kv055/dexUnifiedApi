-- Second-leg tradeflow address split (2026-06-26): the old dex_btc_address column
-- did double duty — first holding our LND hub address (the CEX withdrawal target),
-- then being overwritten with the DEX-side destination at relay time. Split it into
-- two direction-agnostic columns so the second-leg worker can route correctly:
--   destination_address — final DEX-side on-chain target (bid: counterparty/escrow
--                         per-trade addr; ask: our CEX deposit; bisq v1: our bisq wallet)
--   hub_address         — our LND wallet address the CEX withdraws to when the route
--                         needs a hop (bisq2/hodlhodl/peach HubWallet, robosats Swap)
-- Idempotent; safe on every boot.

-- 1. Rename dex_btc_address -> destination_address (guarded so re-runs are no-ops).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'transactions' AND column_name = 'dex_btc_address')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'transactions' AND column_name = 'destination_address') THEN
        ALTER TABLE transactions RENAME COLUMN dex_btc_address TO destination_address;
    END IF;
END $$;

-- 2. New hub address column (the LND wallet target for hub/swap routes).
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS hub_address VARCHAR(128);
