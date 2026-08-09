-- Global domain types. Keep this file before table schemas so every table
-- consumes the same canonical vocabulary instead of duplicating CHECK lists.

CREATE TYPE account_provider AS ENUM (
    'WISE', 'PayPal', 'Revolut', 'N26',
    'Robosats', 'Bisq', 'Bisq2', 'HodlHodl',
    'Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi',
    'LND wallet'
);

CREATE TYPE account_category AS ENUM ('Fiat Transmitter', 'DEX', 'CEX', 'Bank', 'Wallet addresses');
CREATE TYPE payment_method_category AS ENUM ('Cash', 'Bank', 'Fiat Transmitter', 'Gift Cards', 'Crypto', 'Others');

-- Provider identifiers are persisted and exposed as lowercase slugs.
CREATE TYPE dex_provider AS ENUM ('bisq', 'bisq2', 'hodlhodl', 'robosats', 'peachbitcoin');
CREATE TYPE cex_provider AS ENUM ('binance', 'kraken', 'coinbase', 'bybit', 'okx', 'bitfinex', 'gemini', 'bitstamp');
CREATE TYPE fiat_currency AS ENUM ('EUR', 'USD', 'GBP', 'AUD', 'CHF', 'SEK', 'NOK', 'CAD');

CREATE TYPE notification_channel AS ENUM ('push', 'email');
CREATE TYPE wallet_address_category AS ENUM ('cex_deposit', 'hop_deposit');

CREATE TYPE trade_direction AS ENUM ('buy', 'sell');
CREATE TYPE transaction_trade_mode AS ENUM ('hedgefull_single_collateral', 'hedgeless_double_collateral');
CREATE TYPE offer_price_type AS ENUM ('fixed', 'floating', 'unknown');
CREATE TYPE routing_profile AS ENUM ('Direct_main2main', 'Hub_main2main', 'Swap_main2lightning');
CREATE TYPE seller_rating_confidence AS ENUM (
    'feedback_verified', 'feedback_new', 'stake_bonded',
    'reputation_bonded', 'unavailable', 'account_signing'
);
