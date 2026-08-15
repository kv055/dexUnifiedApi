-- Global domain types. Keep this file before table schemas so every table
-- consumes the same canonical vocabulary instead of duplicating CHECK lists.

DO $$
BEGIN
    IF to_regtype('account_provider') IS NULL THEN
        CREATE TYPE account_provider AS ENUM (
            'WISE', 'PayPal', 'Revolut', 'N26',
            'Robosats', 'Bisq', 'Bisq2', 'HodlHodl',
            'Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi',
            'LND wallet'
        );
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('account_category') IS NULL THEN
        CREATE TYPE account_category AS ENUM ('Fiat Transmitter', 'DEX', 'CEX', 'Bank', 'Wallet addresses');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('payment_method_category') IS NULL THEN
        CREATE TYPE payment_method_category AS ENUM ('Cash', 'Bank', 'Fiat Transmitter', 'Gift Cards', 'Crypto', 'Others');
    END IF;
END
$$;

-- Provider identifiers are persisted and exposed as lowercase slugs.
DO $$
BEGIN
    IF to_regtype('dex_provider') IS NULL THEN
        CREATE TYPE dex_provider AS ENUM ('bisq', 'bisq2', 'hodlhodl', 'robosats', 'peachbitcoin');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('cex_provider') IS NULL THEN
        CREATE TYPE cex_provider AS ENUM ('binance', 'kraken', 'coinbase', 'bybit', 'okx', 'bitfinex', 'gemini', 'bitstamp');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('fiat_currency') IS NULL THEN
        CREATE TYPE fiat_currency AS ENUM ('EUR', 'USD', 'GBP', 'AUD', 'CHF', 'SEK', 'NOK', 'CAD');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('notification_channel') IS NULL THEN
        CREATE TYPE notification_channel AS ENUM ('push', 'email');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('wallet_address_category') IS NULL THEN
        CREATE TYPE wallet_address_category AS ENUM ('cex_deposit', 'hop_deposit');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('trade_direction') IS NULL THEN
        CREATE TYPE trade_direction AS ENUM ('bid', 'ask');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('transaction_trade_mode') IS NULL THEN
        CREATE TYPE transaction_trade_mode AS ENUM ('hedgefull_single_collateral', 'hedgeless_double_collateral');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('offer_price_type') IS NULL THEN
        CREATE TYPE offer_price_type AS ENUM ('fixed', 'floating', 'unknown');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('routing_profile') IS NULL THEN
        CREATE TYPE routing_profile AS ENUM ('Direct_main2main', 'Hub_main2main', 'Swap_main2lightning');
    END IF;
END
$$;

DO $$
BEGIN
    IF to_regtype('seller_rating_confidence') IS NULL THEN
        CREATE TYPE seller_rating_confidence AS ENUM (
            'feedback_verified', 'feedback_new', 'stake_bonded',
            'reputation_bonded', 'unavailable', 'account_signing'
        );
    END IF;
END
$$;
