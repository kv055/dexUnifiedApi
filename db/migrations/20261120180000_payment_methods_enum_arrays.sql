-- Legacy VPS databases kept payment_methods.currencies / sources as TEXT[].
-- db/schema/20_payment_methods.sql is CREATE TABLE IF NOT EXISTS, so the typed
-- schema consolidation (fiat_currency[] / dex_provider[]) never reached tables
-- that already existed. The snapshot worker's popularity touch appends
-- ?::fiat_currency[] / ?::dex_provider[] and therefore failed for every payment
-- method on every cycle: "operator does not exist: text[] || fiat_currency[]"
-- (SQLSTATE 42883). Convert the columns in place.
--
-- Values outside the enum cannot live in the typed column. Both arrays are
-- observed metadata the snapshot worker re-learns from live offers every cycle,
-- so such values are dropped with a NOTICE instead of failing the deploy.
-- Fresh installs already have the enum arrays and every block is a no-op.
--
-- compat: backward-compatible
-- rollback: the previous release already binds ?::fiat_currency[] and
-- ?::dex_provider[] (the queries that fail today), and dexMonitor scans these
-- columns as text arrays, which decode enum arrays unchanged. Only values the
-- typed contract forbids are lost.

CREATE FUNCTION pg_temp.pm_fiat_currencies(arr TEXT[]) RETURNS fiat_currency[]
LANGUAGE sql AS $$
    SELECT COALESCE(array_agg(DISTINCT v::fiat_currency ORDER BY v::fiat_currency), '{}')
    FROM (SELECT trim(x) AS v FROM unnest(arr) AS x) s
    WHERE v IN (SELECT enumlabel FROM pg_enum WHERE enumtypid = 'fiat_currency'::regtype)
$$;

CREATE FUNCTION pg_temp.pm_dex_providers(arr TEXT[]) RETURNS dex_provider[]
LANGUAGE sql AS $$
    SELECT COALESCE(array_agg(DISTINCT v::dex_provider ORDER BY v::dex_provider), '{}')
    FROM (SELECT trim(x) AS v FROM unnest(arr) AS x) s
    WHERE v IN (SELECT enumlabel FROM pg_enum WHERE enumtypid = 'dex_provider'::regtype)
$$;

DO $$
DECLARE
    rec RECORD;
    current_type REGTYPE;
    dropped TEXT;
BEGIN
    IF to_regclass('payment_methods') IS NULL THEN
        RETURN;
    END IF;

    FOR rec IN
        SELECT * FROM (VALUES
            ('currencies', 'fiat_currency', 'pg_temp.pm_fiat_currencies'),
            ('sources', 'dex_provider', 'pg_temp.pm_dex_providers')
        ) AS t(column_name, enum_type, converter)
    LOOP
        SELECT a.atttypid::regtype
        INTO current_type
        FROM pg_attribute a
        WHERE a.attrelid = 'payment_methods'::regclass
          AND a.attname = rec.column_name
          AND a.attnum > 0
          AND NOT a.attisdropped;

        IF current_type IS NULL OR current_type = (rec.enum_type || '[]')::regtype THEN
            CONTINUE;
        END IF;

        EXECUTE format(
            'SELECT string_agg(DISTINCT v, '', '' ORDER BY v)
             FROM (SELECT trim(x) AS v FROM payment_methods, unnest(%I::text[]) AS x) s
             WHERE v NOT IN (SELECT enumlabel FROM pg_enum WHERE enumtypid = %L::regtype)',
            rec.column_name, rec.enum_type
        ) INTO dropped;
        IF dropped IS NOT NULL THEN
            RAISE NOTICE 'payment_methods.%: dropping values outside %: %',
                rec.column_name, rec.enum_type, dropped;
        END IF;

        -- The TEXT[] default cannot be cast to the enum array automatically.
        EXECUTE format('ALTER TABLE payment_methods ALTER COLUMN %I DROP DEFAULT', rec.column_name);
        EXECUTE format(
            'ALTER TABLE payment_methods ALTER COLUMN %I TYPE %s[] USING %s(%I::text[])',
            rec.column_name, rec.enum_type, rec.converter, rec.column_name
        );
        EXECUTE format(
            'ALTER TABLE payment_methods ALTER COLUMN %I SET DEFAULT ''{}''::%s[]',
            rec.column_name, rec.enum_type
        );
    END LOOP;
END $$;
