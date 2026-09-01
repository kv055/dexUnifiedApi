package dbutil

import (
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
		if dir == "migrations" && filename == "20260830153000_repair_legacy_varchar_enums.sql" {
			// Legacy installations define ohlc.fiat_currency as CHAR(3) with a
			// bpchar-typed EUR default. PostgreSQL cannot implicitly convert that
			// default while the immutable migration changes the column to the
			// fiat_currency enum, so remove and restore it in the same transaction.
			if _, err := tx.Exec(`ALTER TABLE IF EXISTS ohlc ALTER COLUMN fiat_currency DROP DEFAULT`); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("prepare %s/%s: %w", dir, filename, err)
			}
		}

		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s/%s: %w", dir, filename, err)
		}
		if dir == "migrations" && filename == "20260830153000_repair_legacy_varchar_enums.sql" {
			if _, err := tx.Exec(`ALTER TABLE IF EXISTS ohlc ALTER COLUMN fiat_currency SET DEFAULT 'EUR'::fiat_currency`); err != nil {
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
