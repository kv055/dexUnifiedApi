-- Legacy VPS databases created offers/snapshots/transactions/ohlc columns as
-- VARCHAR/BPCHAR. Newer tables (live_market_observation) use the enum types, so
-- joins like m.exchange = o.exchange fail with SQLSTATE 42883. Convert the
-- remaining text columns in place. Fresh installs already have the enums and
-- this block is a no-op.
DO $$
DECLARE
    rec RECORD;
    current_type OID;
    target_type OID;
    unexpected_values TEXT;
BEGIN
    FOR rec IN
        SELECT * FROM (VALUES
            ('offers', 'exchange', 'dex_provider'),
            ('offers', 'direction', 'trade_direction'),
            ('offers', 'fiat_currency', 'fiat_currency'),
            ('snapshots', 'exchange', 'dex_provider'),
            ('snapshots', 'direction', 'trade_direction'),
            ('snapshots', 'fiat_currency', 'fiat_currency'),
            ('transactions', 'dex', 'dex_provider'),
            ('transactions', 'direction', 'trade_direction'),
            ('transactions', 'fiat_currency', 'fiat_currency'),
            ('ohlc', 'fiat_currency', 'fiat_currency'),
            ('payment_method_chat_templates', 'exchange', 'dex_provider')
        ) AS t(table_name, column_name, enum_type)
    LOOP
        IF to_regclass(format('%I', rec.table_name)) IS NULL THEN
            CONTINUE;
        END IF;
        IF to_regtype(rec.enum_type) IS NULL THEN
            RAISE EXCEPTION 'enum type % does not exist', rec.enum_type;
        END IF;

        SELECT a.atttypid
        INTO current_type
        FROM pg_attribute a
        WHERE a.attrelid = rec.table_name::regclass
          AND a.attname = rec.column_name
          AND a.attnum > 0
          AND NOT a.attisdropped;

        IF current_type IS NULL THEN
            CONTINUE;
        END IF;

        target_type := rec.enum_type::regtype;
        IF current_type = target_type THEN
            CONTINUE;
        END IF;

        EXECUTE format(
            'SELECT string_agg(DISTINCT trim(%I::text), '', '' ORDER BY trim(%I::text))
             FROM %I
             WHERE %I IS NOT NULL
               AND trim(%I::text) NOT IN (
                   SELECT enumlabel FROM pg_enum WHERE enumtypid = %L::regtype
               )',
            rec.column_name, rec.column_name, rec.table_name,
            rec.column_name, rec.column_name, rec.enum_type
        ) INTO unexpected_values;
        IF unexpected_values IS NOT NULL THEN
            RAISE EXCEPTION 'cannot convert %.% to %: unexpected values remain: %',
                rec.table_name, rec.column_name, rec.enum_type, unexpected_values;
        END IF;

        IF rec.column_name = 'direction' THEN
            EXECUTE format(
                'ALTER TABLE %I DROP CONSTRAINT IF EXISTS %I',
                rec.table_name, rec.table_name || '_direction_check'
            );
        END IF;

        EXECUTE format(
            'ALTER TABLE %I ALTER COLUMN %I TYPE %s USING trim(%I::text)::%s',
            rec.table_name, rec.column_name, rec.enum_type, rec.column_name, rec.enum_type
        );
    END LOOP;
END $$;
