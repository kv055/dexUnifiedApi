CREATE TABLE IF NOT EXISTS accounts (
    id BIGSERIAL PRIMARY KEY,
    provider TEXT NOT NULL,
    email TEXT,
    password TEXT,
    phone TEXT,
    last_health_check TIMESTAMPTZ,
    is_healthy BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    payment_priority INTEGER,
    category TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT accounts_provider_allowed_check CHECK (
        provider IN (
            'WISE', 'PayPal', 'Revolut', 'N26',
            'Robosats', 'Bisq', 'Bisq2', 'HodlHodl',
            'Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi',
            'LND wallet'
        )
    ),
    CONSTRAINT accounts_category_allowed_check CHECK (
        category IS NULL OR category IN ('Fiat Transmitter', 'DEX', 'CEX', 'Bank')
    ),
    CONSTRAINT accounts_provider_category_key UNIQUE (provider, category)
);
