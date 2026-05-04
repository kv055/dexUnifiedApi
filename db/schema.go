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
func EnsureUnifiedSchema(db *DB) error {
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
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schema file %s: %w", filename, err)
		}
	}

	return nil
}
