ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS counterparty_stake_btc DOUBLE PRECISION
        CHECK (counterparty_stake_btc IS NULL OR counterparty_stake_btc >= 0),
    ADD COLUMN IF NOT EXISTS counterparty_stake_pct DOUBLE PRECISION
        CHECK (counterparty_stake_pct IS NULL OR counterparty_stake_pct >= 0);
