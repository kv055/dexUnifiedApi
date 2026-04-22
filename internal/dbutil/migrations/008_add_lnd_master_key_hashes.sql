ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS phone TEXT,
    ADD COLUMN IF NOT EXISTS last_health_check TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS is_healthy BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE accounts
SET provider = CASE lower(provider)
    WHEN 'wise' THEN 'WISE'
    WHEN 'paypal' THEN 'PayPal'
    WHEN 'revolut' THEN 'Revolut'
    WHEN 'n26' THEN 'N26'
    WHEN 'robosats' THEN 'Robosats'
    WHEN 'bisq' THEN 'Bisq'
    WHEN 'hodlhodl' THEN 'HodlHodl'
    WHEN 'kraken' THEN 'Kraken'
    WHEN 'coinbase' THEN 'Coinbase'
    WHEN 'bitstamp' THEN 'Bitstamp'
    WHEN 'binance' THEN 'Binance'
    WHEN 'bybit' THEN 'ByBit'
    WHEN 'kucoin' THEN 'KuCoin'
    WHEN 'huobi' THEN 'Huobi'
    WHEN 'lnd wallet' THEN 'LND wallet'
    WHEN 'lnd_wallet' THEN 'LND wallet'
    WHEN 'lnd' THEN 'LND wallet'
    ELSE provider
END;

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS category TEXT GENERATED ALWAYS AS (
        CASE
            WHEN provider IN ('WISE', 'PayPal', 'Revolut', 'N26') THEN 'Fiat Transmitter'
            WHEN provider IN ('Robosats', 'Bisq', 'HodlHodl') THEN 'DEX'
            WHEN provider IN ('Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi') THEN 'CEX'
            WHEN provider = 'LND wallet' THEN 'Internal Infrastructure'
        END
    ) STORED;

ALTER TABLE accounts
    ADD CONSTRAINT accounts_provider_allowed_check CHECK (
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
    );

ALTER TABLE accounts
    ADD CONSTRAINT accounts_category_allowed_check CHECK (
        category IN (
            'Fiat Transmitter',
            'DEX',
            'CEX',
            'Internal Infrastructure'
        )
    );

INSERT INTO accounts (provider)
VALUES
    ('WISE'),
    ('PayPal'),
    ('Revolut'),
    ('N26'),
    ('Robosats'),
    ('Bisq'),
    ('HodlHodl'),
    ('Kraken'),
    ('Coinbase'),
    ('Bitstamp'),
    ('Binance'),
    ('ByBit'),
    ('KuCoin'),
    ('Huobi'),
    ('LND wallet')
ON CONFLICT (provider) DO NOTHING;

CREATE TABLE IF NOT EXISTS lnd_master_key_hashes (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    hash_algorithm TEXT NOT NULL DEFAULT 'sha256',
    master_key_hash TEXT NOT NULL,
    source_path TEXT,
    file_size_bytes BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_lnd_master_key_hashes_account_created_at
    ON lnd_master_key_hashes (account_id, created_at DESC);

CREATE TABLE IF NOT EXISTS bisq_account_file_backups (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    file_name TEXT NOT NULL,
    source_path TEXT,
    hash_algorithm TEXT NOT NULL DEFAULT 'sha256',
    file_sha256 TEXT NOT NULL,
    file_size_bytes BIGINT NOT NULL,
    modified_at TIMESTAMPTZ,
    contents BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT bisq_account_file_backups_account_hash_size_key
        UNIQUE (account_id, file_sha256, file_size_bytes)
);

CREATE INDEX IF NOT EXISTS idx_bisq_account_file_backups_account_created_at
    ON bisq_account_file_backups (account_id, created_at DESC);
