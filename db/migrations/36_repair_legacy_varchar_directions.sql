-- Some older installations created these columns as VARCHAR with buy/sell
-- checks. Enum value renames cannot repair those tables, so repair them here.
DO $$
DECLARE
    table_name TEXT;
    constraint_name TEXT;
    direction_type OID;
    unexpected_values TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['snapshots', 'offers', 'transactions']
    LOOP
        -- The old check prevents changing buy/sell values to bid/ask, so remove
        -- only its known name before normalizing the data.
        FOR constraint_name IN
            SELECT conname
            FROM pg_constraint
            WHERE conrelid = table_name::regclass
              AND contype = 'c'
              AND conname = table_name || '_direction_check'
        LOOP
            EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', table_name, constraint_name);
        END LOOP;

        SELECT a.atttypid
        INTO direction_type
        FROM pg_attribute a
        WHERE a.attrelid = table_name::regclass
          AND a.attname = 'direction'
          AND a.attnum > 0
          AND NOT a.attisdropped;

        IF direction_type = 'character varying'::regtype THEN
            EXECUTE format(
                'UPDATE %I
                 SET direction = CASE lower(direction)
                     WHEN ''buy'' THEN ''bid''
                     WHEN ''sell'' THEN ''ask''
                     ELSE direction
                 END
                 WHERE lower(direction) IN (''buy'', ''sell'')',
                table_name
            );
        END IF;

        EXECUTE format(
            'SELECT string_agg(DISTINCT direction::text, '', '' ORDER BY direction::text)
             FROM %I
             WHERE direction IS NOT NULL
               AND direction::text NOT IN (''bid'', ''ask'')',
            table_name
        ) INTO unexpected_values;
        IF unexpected_values IS NOT NULL THEN
            RAISE EXCEPTION 'cannot repair %.direction: unexpected non-null direction values remain: %',
                table_name, unexpected_values;
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM pg_constraint
            WHERE conrelid = table_name::regclass
              AND conname = table_name || '_direction_check'
        ) THEN
            EXECUTE format(
                'ALTER TABLE %I ADD CONSTRAINT %I CHECK (direction IN (''bid'', ''ask''))',
                table_name, table_name || '_direction_check'
            );
        END IF;
    END LOOP;
END $$;
