package dbutil

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

//go:embed schema/*.sql
var schemaFiles embed.FS

// EnsureUnifiedSchema ensures the required state exists.
//
// Each migration in schema/*.sql is applied at most once: an applied filename is
// recorded in the schema_migrations tracking table, in the same transaction as
// the migration itself, so the record is atomic with the change. Already-recorded
// files are skipped on subsequent boots. Migrations should still be written
// idempotently (the existing files are), but correctness no longer depends on it.
func EnsureUnifiedSchema(db *DB) error {
	if _, err := db.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename   TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}

	applied, err := appliedMigrations(db)
	if err != nil {
		return err
	}

	entries, err := schemaFiles.ReadDir("schema")
	if err != nil {
		return fmt.Errorf("read schema directory: %w", err)
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

		sqlBytes, err := schemaFiles.ReadFile(path.Join("schema", filename))
		if err != nil {
			return fmt.Errorf("read schema file %s: %w", filename, err)
		}

		tx, err := db.DB.Begin()
		if err != nil {
			return fmt.Errorf("begin schema file %s: %w", filename, err)
		}

		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply schema file %s: %w", filename, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (filename) VALUES ($1)`, filename); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record schema file %s: %w", filename, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schema file %s: %w", filename, err)
		}
	}

	return nil
}

// appliedMigrations returns the set of migration filenames already recorded in
// the schema_migrations tracking table.
func appliedMigrations(db *DB) (map[string]bool, error) {
	rows, err := db.DB.Query(`SELECT filename FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
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
