CREATE TABLE IF NOT EXISTS accounts (
    id BIGSERIAL PRIMARY KEY,
    provider TEXT NOT NULL UNIQUE,
    email TEXT,
    password TEXT,
    phone TEXT,
    last_health_check TIMESTAMPTZ,
    is_healthy BOOLEAN NOT NULL DEFAULT FALSE,
    category TEXT GENERATED ALWAYS AS (
        CASE
            WHEN provider IN ('WISE', 'PayPal', 'Revolut', 'N26') THEN 'Fiat Transmitter'
            WHEN provider IN ('Robosats', 'Bisq', 'HodlHodl') THEN 'DEX'
            WHEN provider IN ('Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi') THEN 'CEX'
            WHEN provider = 'LND wallet' THEN 'Internal Infrastructure'
        END
    ) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT accounts_provider_allowed_check CHECK (
        provider IN (
            'WISE',
            'PayPal',
            'Revolut',
            'N26',
            'Robosats',
            'Bisq',
            'HodlHodl',
            'Kraken',
            'Coinbase',
            'Bitstamp',
            'Binance',
            'ByBit',
            'KuCoin',
            'Huobi',
            'LND wallet'
        )
    ),
    CONSTRAINT accounts_category_allowed_check CHECK (
        category IN (
            'Fiat Transmitter',
            'DEX',
            'CEX',
            'Internal Infrastructure'
        )
    )
);
