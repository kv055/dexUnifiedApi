-- Align transactions.dex with the 5-venue routing matrix (txroute) and
-- offers.exchange (widened in 017). The old 3-venue CHECK rejected bisq2 and
-- peachbitcoin bid transactions at INSERT, even though both are first-class Hub
-- routes. Idempotent; safe on every boot.
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_dex_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_dex_check
    CHECK (dex IS NULL OR dex IN ('Bisq', 'Bisq2', 'HodlHodl', 'RoboSats', 'PeachBitcoin'));
