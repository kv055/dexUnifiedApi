-- Recreate the accounts table when it has exhausted PostgreSQL's 1600-column
-- limit from repeated DROP/ADD cycles on the generated category column (016
-- was not originally idempotent). VACUUM FULL cannot reclaim dropped-column
-- catalog slots; only a table rebuild resets attnum. Must run before 020
-- which needs to DROP+ADD category one more time.

DO $$
DECLARE
    slot_count INTEGER;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name = 'accounts' AND table_schema = 'public'
    ) THEN
        RETURN;
    END IF;

    SELECT COUNT(*) INTO slot_count
    FROM pg_attribute
    WHERE attrelid = 'accounts'::regclass AND attnum > 0;

    IF slot_count <= 1500 THEN
        RETURN;
    END IF;

    CREATE TEMP TABLE _acct_bak AS
        SELECT id, provider, email, password, phone,
               last_health_check, is_healthy, created_at, updated_at
        FROM accounts;

    DROP TABLE accounts CASCADE;

    CREATE TABLE accounts (
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
                WHEN provider IN ('Robosats', 'Bisq', 'Bisq2', 'HodlHodl') THEN 'DEX'
                WHEN provider IN ('Kraken', 'Coinbase', 'Bitstamp', 'Binance', 'ByBit', 'KuCoin', 'Huobi') THEN 'CEX'
                WHEN provider = 'LND wallet' THEN 'Internal Infrastructure'
            END
        ) STORED,
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
            category IN (
                'Fiat Transmitter',
                'DEX',
                'CEX',
                'Internal Infrastructure'
            )
        )
    );

    INSERT INTO accounts (id, provider, email, password, phone,
                          last_health_check, is_healthy, created_at, updated_at)
    SELECT * FROM _acct_bak;

    PERFORM setval('accounts_id_seq', COALESCE((SELECT MAX(id) FROM accounts), 0));

    DROP TABLE _acct_bak;
END $$;
