package dbutil

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed schema/*.sql
var schemaFiles embed.FS

// EnsureUnifiedSchema applies each table-local schema file at most once.
// Files are tracked in schema_migrations so repeated boots do not reapply them.
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

	if err := applyDir(db, schemaFiles, "schema", applied); err != nil {
		return err
	}
	return nil
}

// applyDir reads .sql files from an embedded directory, sorts them, and applies
// any that haven't been recorded in schema_migrations yet.
func applyDir(db *DB, fsys embed.FS, dir string, applied map[string]bool) error {
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

		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s/%s: %w", dir, filename, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (filename) VALUES ($1)`, filename); err != nil {
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
