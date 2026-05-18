ALTER TABLE offers DROP CONSTRAINT IF EXISTS offers_exchange_check;
ALTER TABLE offers ADD CONSTRAINT offers_exchange_check
    CHECK (exchange IN ('bisq', 'hodlhodl', 'robosats', 'peachbitcoin'));
