-- Associate each payment method with the DEX sources and fiat currencies it
-- supports. Both are arrays: one catalog row (unique by name) can cover
-- multiple venues and currencies.
ALTER TABLE payment_methods
    ADD COLUMN IF NOT EXISTS sources TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE payment_methods
    ADD COLUMN IF NOT EXISTS currencies TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_sources_allowed_check;
ALTER TABLE payment_methods DROP CONSTRAINT IF EXISTS payment_methods_currencies_allowed_check;

ALTER TABLE payment_methods ADD CONSTRAINT payment_methods_sources_allowed_check CHECK (
    sources <@ ARRAY['bisq', 'bisq2', 'hodlhodl', 'robosats', 'peachbitcoin']::text[]
);

ALTER TABLE payment_methods ADD CONSTRAINT payment_methods_currencies_allowed_check CHECK (
    currencies <@ ARRAY['EUR', 'USD', 'GBP', 'AUD', 'CHF', 'SEK', 'NOK', 'CAD']::text[]
);
