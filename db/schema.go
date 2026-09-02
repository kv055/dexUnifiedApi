package dbutil

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed schema/*.sql migrations/*.sql
var schemaFiles embed.FS

var baselineMigrationFiles = []string{
	"28_transactional_liquidity_target_direction.sql",
	"29_lsp_channel_config.sql",
	"30_submarine_swaps.sql",
	"31_dex_taken_funding_review.sql",
	"32_submarine_swap_reservation_id.sql",
	"33_authoritative_snapshot_routing.sql",
	"33_cex_provider_fees.sql",
	"34_payment_method_chat_template_directions.sql",
	"34_transactional_liquidity_lightning_source.sql",
	"35_cex_provider_fee_units.sql",
}

// EnsureUnifiedSchema applies the base schema and initialized forward-only
// migrations at most once.
func EnsureUnifiedSchema(db *DB) error {
	if _, err := db.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename   TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}

	applied, err := appliedSchemaFiles(db)
	if err != nil {
		return err
	}

	if err := applyDir(db, schemaFiles, "schema", "schema_migrations", applied); err != nil {
		return err
	}
	return ensureUnifiedMigrations(db)
}

// ensureUnifiedMigrations initializes and applies forward-only migrations from
// db/migrations. The historical files predate this runner and are recorded as
// a baseline because their effects are already represented in db/schema.
func ensureUnifiedMigrations(db *DB) error {
	if _, err := db.DB.Exec(`CREATE TABLE IF NOT EXISTS migration_migrations (
		filename   TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("ensure migration_migrations table: %w", err)
	}
	for _, filename := range baselineMigrationFiles {
		if _, err := db.DB.Exec(`INSERT INTO migration_migrations (filename) VALUES ($1) ON CONFLICT DO NOTHING`, filename); err != nil {
			return fmt.Errorf("initialize migration baseline %s: %w", filename, err)
		}
	}

	applied, err := appliedMigrationFiles(db)
	if err != nil {
		return err
	}
	return applyDir(db, schemaFiles, "migrations", "migration_migrations", applied)
}

// applyDir reads .sql files from an embedded directory, sorts them, and applies
// any that haven't been recorded in its migration tracking table yet.
func applyDir(db *DB, fsys embed.FS, dir, migrationTable string, applied map[string]bool) error {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s directory: %w", dir, err)
	}

	var filenames []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		filenames = append(filenames, entry.Name())
	}
	sort.Strings(filenames)

	for _, filename := range filenames {
		if applied[filename] {
			continue
		}

		sqlBytes, err := fs.ReadFile(fsys, path.Join(dir, filename))
		if err != nil {
			return fmt.Errorf("read %s/%s: %w", dir, filename, err)
		}

		tx, err := db.DB.Begin()
		if err != nil {
			return fmt.Errorf("begin %s/%s: %w", dir, filename, err)
		}
		if dir == "migrations" && filename == legacyVarcharEnumRepairFile {
			if err := prepareLegacyVarcharEnumRepair(tx); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("prepare %s/%s: %w", dir, filename, err)
			}
		}

		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s/%s: %w", dir, filename, err)
		}
		if dir == "migrations" && filename == legacyVarcharEnumRepairFile {
			if err := finalizeLegacyVarcharEnumRepair(tx); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("finalize %s/%s: %w", dir, filename, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO `+migrationTable+` (filename) VALUES ($1)`, filename); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record %s/%s: %w", dir, filename, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s/%s: %w", dir, filename, err)
		}

		applied[filename] = true
	}

	return nil
}

const legacyVarcharEnumRepairFile = "20260830153000_repair_legacy_varchar_enums.sql"

// prepareLegacyVarcharEnumRepair runs in the same transaction as the immutable
// varchar-to-enum repair. Prod still stores display-case leftovers such as
// RoboSats; the SQL file only accepts exact enum labels, so fold case-only
// aliases onto those labels first. Also drop the bpchar EUR default on ohlc
// that PostgreSQL cannot coerce during the type change.
func prepareLegacyVarcharEnumRepair(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE IF EXISTS ohlc ALTER COLUMN fiat_currency DROP DEFAULT`); err != nil {
		return err
	}
	// Legacy installations constrained this text column to display labels
	// (RoboSats/Bisq2). Remove that constraint before folding the values to
	// canonical enum labels; it is restored after the type conversion below.
	if _, err := tx.Exec(`
ALTER TABLE IF EXISTS payment_method_chat_templates
    DROP CONSTRAINT IF EXISTS payment_method_chat_templates_exchange_allowed_check
`); err != nil {
		return err
	}
	_, err := tx.Exec(`
DO $$
DECLARE
    rec RECORD;
    current_type OID;
    target_type OID;
	conflicting_keys TEXT;
BEGIN
    IF to_regtype('dex_provider') IS NULL THEN
        RETURN;
    END IF;

	-- A legacy display label and its canonical equivalent can coexist under a
	-- case-sensitive unique constraint. Do not silently choose which template
	-- survives normalization.
	IF to_regclass('payment_method_chat_templates') IS NOT NULL
	   AND EXISTS (
	       SELECT 1 FROM pg_attribute
	       WHERE attrelid = 'payment_method_chat_templates'::regclass
	         AND attname = 'payment_method_id' AND attnum > 0 AND NOT attisdropped
	   )
	   AND EXISTS (
	       SELECT 1 FROM pg_attribute
	       WHERE attrelid = 'payment_method_chat_templates'::regclass
	         AND attname = 'trade_direction' AND attnum > 0 AND NOT attisdropped
	   ) THEN
	    SELECT string_agg(format('%s/%s/%s', exchange_key, payment_method_id, trade_direction), ', ')
	    INTO conflicting_keys
	    FROM (
	        SELECT lower(trim(exchange::text)) AS exchange_key, payment_method_id, trade_direction
	        FROM payment_method_chat_templates
	        GROUP BY lower(trim(exchange::text)), payment_method_id, trade_direction
	        HAVING count(*) > 1
	    ) conflicts;
	    IF conflicting_keys IS NOT NULL THEN
	        RAISE EXCEPTION 'cannot normalize payment_method_chat_templates.exchange: conflicting template keys: %',
	            conflicting_keys;
	    END IF;
	END IF;

    FOR rec IN
        SELECT * FROM (VALUES
            ('offers', 'exchange'),
            ('snapshots', 'exchange'),
            ('transactions', 'dex'),
            ('payment_method_chat_templates', 'exchange')
        ) AS t(table_name, column_name)
    LOOP
        IF to_regclass(format('%I', rec.table_name)) IS NULL THEN
            CONTINUE;
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

        target_type := 'dex_provider'::regtype;
        IF current_type = target_type THEN
            CONTINUE;
        END IF;

        EXECUTE format(
            'UPDATE %I SET %I = lower(trim(%I::text))
             WHERE %I IS NOT NULL
               AND lower(trim(%I::text)) IN (
                   SELECT enumlabel FROM pg_enum WHERE enumtypid = %L::regtype
               )
               AND trim(%I::text) NOT IN (
                   SELECT enumlabel FROM pg_enum WHERE enumtypid = %L::regtype
               )',
            rec.table_name, rec.column_name, rec.column_name,
            rec.column_name, rec.column_name, 'dex_provider',
            rec.column_name, 'dex_provider'
        );
    END LOOP;
END $$;
`)
	return err
}

func finalizeLegacyVarcharEnumRepair(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE IF EXISTS ohlc ALTER COLUMN fiat_currency SET DEFAULT 'EUR'::fiat_currency`); err != nil {
		return err
	}
	_, err := tx.Exec(`
ALTER TABLE IF EXISTS payment_method_chat_templates
    ADD CONSTRAINT payment_method_chat_templates_exchange_allowed_check
    CHECK (exchange IN ('robosats', 'bisq2'))
`)
	return err
}

// appliedSchemaFiles returns the set of schema filenames already recorded in
// the schema_migrations tracking table.
func appliedSchemaFiles(db *DB) (map[string]bool, error) {
	return appliedFiles(db, "schema_migrations")
}

func appliedMigrationFiles(db *DB) (map[string]bool, error) {
	return appliedFiles(db, "migration_migrations")
}

func appliedFiles(db *DB, table string) (map[string]bool, error) {
	rows, err := db.DB.Query(`SELECT filename FROM ` + table)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", table, err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var filename string
		if err := rows.Scan(&filename); err != nil {
			return nil, fmt.Errorf("scan schema_migrations row: %w", err)
		}
		applied[filename] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return applied, nil
}
