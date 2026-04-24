ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS phone TEXT,
    ADD COLUMN IF NOT EXISTS last_health_check TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS is_healthy BOOLEAN NOT NULL DEFAULT FALSE;

WITH provider_map(old_provider, new_provider) AS (
    VALUES
        ('wise', 'WISE'),
        ('paypal', 'PayPal'),
        ('revolut', 'Revolut'),
        ('n26', 'N26'),
        ('robosats', 'Robosats'),
        ('bisq', 'Bisq'),
        ('hodlhodl', 'HodlHodl'),
        ('kraken', 'Kraken'),
        ('coinbase', 'Coinbase'),
        ('bitstamp', 'Bitstamp'),
        ('binance', 'Binance'),
        ('bybit', 'ByBit'),
        ('kucoin', 'KuCoin'),
        ('huobi', 'Huobi'),
        ('lnd wallet', 'LND wallet'),
        ('lnd_wallet', 'LND wallet'),
        ('lnd', 'LND wallet')
),
canonical_accounts AS (
    SELECT m.new_provider,
           COALESCE(
               MIN(a.id) FILTER (WHERE a.provider = m.new_provider),
               MIN(a.id)
           ) AS keep_id
    FROM provider_map m
    JOIN accounts a ON lower(a.provider) = m.old_provider
    GROUP BY m.new_provider
),
duplicate_accounts AS (
    SELECT a.id AS duplicate_id, c.keep_id
    FROM accounts a
    JOIN provider_map m ON lower(a.provider) = m.old_provider
    JOIN canonical_accounts c ON c.new_provider = m.new_provider
    WHERE a.id <> c.keep_id
)
UPDATE totp_secrets t
SET account_id = d.keep_id
FROM duplicate_accounts d
WHERE t.account_id = d.duplicate_id;

WITH provider_map(old_provider, new_provider) AS (
    VALUES
        ('wise', 'WISE'), ('paypal', 'PayPal'), ('revolut', 'Revolut'), ('n26', 'N26'),
        ('robosats', 'Robosats'), ('bisq', 'Bisq'), ('hodlhodl', 'HodlHodl'),
        ('kraken', 'Kraken'), ('coinbase', 'Coinbase'), ('bitstamp', 'Bitstamp'),
        ('binance', 'Binance'), ('bybit', 'ByBit'), ('kucoin', 'KuCoin'), ('huobi', 'Huobi'),
        ('lnd wallet', 'LND wallet'), ('lnd_wallet', 'LND wallet'), ('lnd', 'LND wallet')
),
canonical_accounts AS (
    SELECT m.new_provider, COALESCE(MIN(a.id) FILTER (WHERE a.provider = m.new_provider), MIN(a.id)) AS keep_id
    FROM provider_map m
    JOIN accounts a ON lower(a.provider) = m.old_provider
    GROUP BY m.new_provider
),
duplicate_accounts AS (
    SELECT a.id AS duplicate_id, c.keep_id
    FROM accounts a
    JOIN provider_map m ON lower(a.provider) = m.old_provider
    JOIN canonical_accounts c ON c.new_provider = m.new_provider
    WHERE a.id <> c.keep_id
)
UPDATE passkey_credentials p
SET account_id = d.keep_id
FROM duplicate_accounts d
WHERE p.account_id = d.duplicate_id;

WITH provider_map(old_provider, new_provider) AS (
    VALUES
        ('wise', 'WISE'), ('paypal', 'PayPal'), ('revolut', 'Revolut'), ('n26', 'N26'),
        ('robosats', 'Robosats'), ('bisq', 'Bisq'), ('hodlhodl', 'HodlHodl'),
        ('kraken', 'Kraken'), ('coinbase', 'Coinbase'), ('bitstamp', 'Bitstamp'),
        ('binance', 'Binance'), ('bybit', 'ByBit'), ('kucoin', 'KuCoin'), ('huobi', 'Huobi'),
        ('lnd wallet', 'LND wallet'), ('lnd_wallet', 'LND wallet'), ('lnd', 'LND wallet')
),
canonical_accounts AS (
    SELECT m.new_provider, COALESCE(MIN(a.id) FILTER (WHERE a.provider = m.new_provider), MIN(a.id)) AS keep_id
    FROM provider_map m
    JOIN accounts a ON lower(a.provider) = m.old_provider
    GROUP BY m.new_provider
),
duplicate_accounts AS (
    SELECT a.id AS duplicate_id, c.keep_id
    FROM accounts a
    JOIN provider_map m ON lower(a.provider) = m.old_provider
    JOIN canonical_accounts c ON c.new_provider = m.new_provider
    WHERE a.id <> c.keep_id
)
UPDATE seeds s
SET account_id = d.keep_id
FROM duplicate_accounts d
WHERE s.account_id = d.duplicate_id;

WITH provider_map(old_provider, new_provider) AS (
    VALUES
        ('wise', 'WISE'), ('paypal', 'PayPal'), ('revolut', 'Revolut'), ('n26', 'N26'),
        ('robosats', 'Robosats'), ('bisq', 'Bisq'), ('hodlhodl', 'HodlHodl'),
        ('kraken', 'Kraken'), ('coinbase', 'Coinbase'), ('bitstamp', 'Bitstamp'),
        ('binance', 'Binance'), ('bybit', 'ByBit'), ('kucoin', 'KuCoin'), ('huobi', 'Huobi'),
        ('lnd wallet', 'LND wallet'), ('lnd_wallet', 'LND wallet'), ('lnd', 'LND wallet')
),
canonical_accounts AS (
    SELECT m.new_provider, COALESCE(MIN(a.id) FILTER (WHERE a.provider = m.new_provider), MIN(a.id)) AS keep_id
    FROM provider_map m
    JOIN accounts a ON lower(a.provider) = m.old_provider
    GROUP BY m.new_provider
),
duplicate_accounts AS (
    SELECT a.id AS duplicate_id, c.keep_id
    FROM accounts a
    JOIN provider_map m ON lower(a.provider) = m.old_provider
    JOIN canonical_accounts c ON c.new_provider = m.new_provider
    WHERE a.id <> c.keep_id
)
UPDATE api_keys k
SET account_id = d.keep_id
FROM duplicate_accounts d
WHERE k.account_id = d.duplicate_id;

UPDATE lnd_master_key_hashes l
SET account_id = d.keep_id
FROM (
    WITH provider_map(old_provider, new_provider) AS (
        VALUES
            ('wise', 'WISE'), ('paypal', 'PayPal'), ('revolut', 'Revolut'), ('n26', 'N26'),
            ('robosats', 'Robosats'), ('bisq', 'Bisq'), ('hodlhodl', 'HodlHodl'),
            ('kraken', 'Kraken'), ('coinbase', 'Coinbase'), ('bitstamp', 'Bitstamp'),
            ('binance', 'Binance'), ('bybit', 'ByBit'), ('kucoin', 'KuCoin'), ('huobi', 'Huobi'),
            ('lnd wallet', 'LND wallet'), ('lnd_wallet', 'LND wallet'), ('lnd', 'LND wallet')
    ),
    canonical_accounts AS (
        SELECT m.new_provider, COALESCE(MIN(a.id) FILTER (WHERE a.provider = m.new_provider), MIN(a.id)) AS keep_id
        FROM provider_map m
        JOIN accounts a ON lower(a.provider) = m.old_provider
        GROUP BY m.new_provider
    )
    SELECT a.id AS duplicate_id, c.keep_id
    FROM accounts a
    JOIN provider_map m ON lower(a.provider) = m.old_provider
    JOIN canonical_accounts c ON c.new_provider = m.new_provider
    WHERE a.id <> c.keep_id
) d
WHERE l.account_id = d.duplicate_id;

DELETE FROM bisq_account_file_backups b
USING (
    WITH provider_map(old_provider, new_provider) AS (
        VALUES
            ('wise', 'WISE'), ('paypal', 'PayPal'), ('revolut', 'Revolut'), ('n26', 'N26'),
            ('robosats', 'Robosats'), ('bisq', 'Bisq'), ('hodlhodl', 'HodlHodl'),
            ('kraken', 'Kraken'), ('coinbase', 'Coinbase'), ('bitstamp', 'Bitstamp'),
            ('binance', 'Binance'), ('bybit', 'ByBit'), ('kucoin', 'KuCoin'), ('huobi', 'Huobi'),
            ('lnd wallet', 'LND wallet'), ('lnd_wallet', 'LND wallet'), ('lnd', 'LND wallet')
    ),
    canonical_accounts AS (
        SELECT m.new_provider, COALESCE(MIN(a.id) FILTER (WHERE a.provider = m.new_provider), MIN(a.id)) AS keep_id
        FROM provider_map m
        JOIN accounts a ON lower(a.provider) = m.old_provider
        GROUP BY m.new_provider
    )
    SELECT a.id AS duplicate_id, c.keep_id
    FROM accounts a
    JOIN provider_map m ON lower(a.provider) = m.old_provider
    JOIN canonical_accounts c ON c.new_provider = m.new_provider
    WHERE a.id <> c.keep_id
) d
WHERE b.account_id = d.duplicate_id
  AND EXISTS (
      SELECT 1
      FROM bisq_account_file_backups existing
      WHERE existing.account_id = d.keep_id
        AND existing.file_sha256 = b.file_sha256
        AND existing.file_size_bytes = b.file_size_bytes
  );

UPDATE bisq_account_file_backups b
SET account_id = d.keep_id
FROM (
    WITH provider_map(old_provider, new_provider) AS (
        VALUES
            ('wise', 'WISE'), ('paypal', 'PayPal'), ('revolut', 'Revolut'), ('n26', 'N26'),
            ('robosats', 'Robosats'), ('bisq', 'Bisq'), ('hodlhodl', 'HodlHodl'),
            ('kraken', 'Kraken'), ('coinbase', 'Coinbase'), ('bitstamp', 'Bitstamp'),
            ('binance', 'Binance'), ('bybit', 'ByBit'), ('kucoin', 'KuCoin'), ('huobi', 'Huobi'),
            ('lnd wallet', 'LND wallet'), ('lnd_wallet', 'LND wallet'), ('lnd', 'LND wallet')
    ),
    canonical_accounts AS (
        SELECT m.new_provider, COALESCE(MIN(a.id) FILTER (WHERE a.provider = m.new_provider), MIN(a.id)) AS keep_id
        FROM provider_map m
        JOIN accounts a ON lower(a.provider) = m.old_provider
        GROUP BY m.new_provider
    )
    SELECT a.id AS duplicate_id, c.keep_id
    FROM accounts a
    JOIN provider_map m ON lower(a.provider) = m.old_provider
    JOIN canonical_accounts c ON c.new_provider = m.new_provider
    WHERE a.id <> c.keep_id
) d
WHERE b.account_id = d.duplicate_id;

WITH provider_map(old_provider, new_provider) AS (
    VALUES
        ('wise', 'WISE'), ('paypal', 'PayPal'), ('revolut', 'Revolut'), ('n26', 'N26'),
        ('robosats', 'Robosats'), ('bisq', 'Bisq'), ('hodlhodl', 'HodlHodl'),
        ('kraken', 'Kraken'), ('coinbase', 'Coinbase'), ('bitstamp', 'Bitstamp'),
        ('binance', 'Binance'), ('bybit', 'ByBit'), ('kucoin', 'KuCoin'), ('huobi', 'Huobi'),
        ('lnd wallet', 'LND wallet'), ('lnd_wallet', 'LND wallet'), ('lnd', 'LND wallet')
),
canonical_accounts AS (
    SELECT m.new_provider, COALESCE(MIN(a.id) FILTER (WHERE a.provider = m.new_provider), MIN(a.id)) AS keep_id
    FROM provider_map m
    JOIN accounts a ON lower(a.provider) = m.old_provider
    GROUP BY m.new_provider
),
duplicate_accounts AS (
    SELECT a.id AS duplicate_id
    FROM accounts a
    JOIN provider_map m ON lower(a.provider) = m.old_provider
    JOIN canonical_accounts c ON c.new_provider = m.new_provider
    WHERE a.id <> c.keep_id
)
DELETE FROM accounts a
USING duplicate_accounts d
WHERE a.id = d.duplicate_id;

UPDATE accounts a
SET provider = m.new_provider
FROM (
    VALUES
        ('wise', 'WISE'),
        ('paypal', 'PayPal'),
        ('revolut', 'Revolut'),
        ('n26', 'N26'),
        ('robosats', 'Robosats'),
        ('bisq', 'Bisq'),
        ('hodlhodl', 'HodlHodl'),
        ('kraken', 'Kraken'),
        ('coinbase', 'Coinbase'),
        ('bitstamp', 'Bitstamp'),
        ('binance', 'Binance'),
        ('bybit', 'ByBit'),
        ('kucoin', 'KuCoin'),
        ('huobi', 'Huobi'),
        ('lnd wallet', 'LND wallet'),
        ('lnd_wallet', 'LND wallet'),
        ('lnd', 'LND wallet')
) AS m(old_provider, new_provider)
WHERE lower(a.provider) = m.old_provider
  AND a.provider <> m.new_provider;

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS category TEXT GENERATED ALWAYS AS (
        CASE
            WHEN provider IN ('WISE', 'PayPal', 'Revolut', 'N26') THEN 'Fiat Transmitter'
            WHEN provider IN ('Robosats', 'Bisq', 'HodlHodl') THEN 'DEX'
            WHEN provider IN ('Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi') THEN 'CEX'
            WHEN provider = 'LND wallet' THEN 'Internal Infrastructure'
        END
    ) STORED;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'accounts'::regclass
          AND conname = 'accounts_provider_allowed_check'
    ) THEN
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
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'accounts'::regclass
          AND conname = 'accounts_category_allowed_check'
    ) THEN
        ALTER TABLE accounts
            ADD CONSTRAINT accounts_category_allowed_check CHECK (
                category IN (
                    'Fiat Transmitter',
                    'DEX',
                    'CEX',
                    'Internal Infrastructure'
                )
            );
    END IF;
END $$;

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
