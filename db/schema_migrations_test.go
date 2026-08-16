package dbutil_test

import (
	"os"
	"sort"
	"strings"
	"testing"

	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/testdb"
)

// countSchemaFiles counts the table-local schema files without hard-coding a count.
func countSchemaFiles(t *testing.T) int {
	return countSQLFiles(t, "schema")
}

func countMigrationFiles(t *testing.T) int {
	return countSQLFiles(t, "migrations")
}

func countSQLFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s directory: %v", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			n++
		}
	}
	if n == 0 {
		t.Fatalf("no SQL files found in %s", dir)
	}
	return n
}

func markExistingSchemaFilesApplied(t *testing.T, db *dbutil.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := os.ReadDir("schema")
	if err != nil {
		t.Fatalf("read schema directory: %v", err)
	}
	var filenames []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			filenames = append(filenames, entry.Name())
		}
	}
	sort.Strings(filenames)
	for _, filename := range filenames {
		if _, err := db.Exec(`INSERT INTO schema_migrations (filename) VALUES ($1)`, filename); err != nil {
			t.Fatalf("record prior schema %s: %v", filename, err)
		}
	}
}

// TestEnsureUnifiedSchemaRecordsEverySchemaFile verifies that the first run
// applies and records every schema file exactly once.
func TestEnsureUnifiedSchemaRecordsEverySchemaFile(t *testing.T) {
	db := testdb.Open(t) // applies the schema once

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if want := countSchemaFiles(t); count != want {
		t.Fatalf("schema_migrations has %d rows, want %d", count, want)
	}
	if err := db.QueryRow(`SELECT count(*) FROM migration_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migration_migrations: %v", err)
	}
	if want := countMigrationFiles(t); count != want {
		t.Fatalf("migration_migrations has %d rows, want %d", count, want)
	}
}

// TestEnsureUnifiedSchemaIsRunOnce verifies that a second EnsureUnifiedSchema call
// re-applies nothing: row count and applied_at timestamps are unchanged.
func TestEnsureUnifiedSchemaIsRunOnce(t *testing.T) {
	db := testdb.Open(t) // first apply

	var count1 int
	var maxApplied1 string
	if err := db.QueryRow(`SELECT count(*), coalesce(max(applied_at)::text, '') FROM schema_migrations`).Scan(&count1, &maxApplied1); err != nil {
		t.Fatalf("snapshot before second run: %v", err)
	}

	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		t.Fatalf("second EnsureUnifiedSchema: %v", err)
	}

	var count2 int
	var maxApplied2 string
	if err := db.QueryRow(`SELECT count(*), coalesce(max(applied_at)::text, '') FROM schema_migrations`).Scan(&count2, &maxApplied2); err != nil {
		t.Fatalf("snapshot after second run: %v", err)
	}

	if count1 != count2 {
		t.Fatalf("row count changed across runs: %d -> %d", count1, count2)
	}
	if maxApplied1 != maxApplied2 {
		t.Fatalf("applied_at changed across runs (%q -> %q): schema files were re-applied", maxApplied1, maxApplied2)
	}
}

func TestEnsureUnifiedSchemaRepairsLegacyVarcharDirections(t *testing.T) {
	db := testdb.OpenUninitialized(t)
	if _, err := db.Exec(`
		CREATE TABLE snapshots (id BIGSERIAL PRIMARY KEY, direction VARCHAR(8) NOT NULL,
			CONSTRAINT snapshots_direction_check CHECK (direction IN ('buy', 'sell', 'BUY', 'SELL')));
		CREATE TABLE offers (id BIGSERIAL PRIMARY KEY, direction VARCHAR(8) NOT NULL,
			CONSTRAINT offers_direction_check CHECK (direction IN ('buy', 'sell', 'BUY', 'SELL')));
		CREATE TABLE transactions (id BIGSERIAL PRIMARY KEY, direction VARCHAR(8) NOT NULL,
			CONSTRAINT transactions_direction_check CHECK (direction IN ('buy', 'sell', 'BUY', 'SELL')));
		INSERT INTO snapshots (direction) VALUES ('buy'), ('sell'), ('BUY'), ('SELL');
		INSERT INTO offers (direction) VALUES ('buy'), ('sell'), ('BUY'), ('SELL');
		INSERT INTO transactions (direction) VALUES ('buy'), ('sell'), ('BUY'), ('SELL');
	`); err != nil {
		t.Fatalf("create legacy direction tables: %v", err)
	}
	markExistingSchemaFilesApplied(t, db)

	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		t.Fatalf("apply legacy direction repair: %v", err)
	}

	for _, table := range []string{"snapshots", "offers", "transactions"} {
		rows, err := db.Query(`SELECT direction FROM ` + table + ` ORDER BY id`)
		if err != nil {
			t.Fatalf("read %s directions: %v", table, err)
		}
		var values []string
		for rows.Next() {
			var direction string
			if err := rows.Scan(&direction); err != nil {
				rows.Close()
				t.Fatalf("scan %s direction: %v", table, err)
			}
			values = append(values, direction)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("iterate %s directions: %v", table, err)
		}
		rows.Close()
		if got, want := strings.Join(values, ","), "bid,ask,bid,ask"; got != want {
			t.Fatalf("%s directions = %q, want %q", table, got, want)
		}
		if _, err := db.Exec(`INSERT INTO ` + table + ` (direction) VALUES ('bid'), ('ask')`); err != nil {
			t.Errorf("%s canonical directions rejected: %v", table, err)
		}
		if _, err := db.Exec(`INSERT INTO ` + table + ` (direction) VALUES ('buy')`); err == nil {
			t.Errorf("%s legacy direction was accepted", table)
		}
	}

	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		t.Fatalf("reapply schema after direction repair: %v", err)
	}
	for _, table := range []string{"snapshots", "offers", "transactions"} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s rows after reapply: %v", table, err)
		}
		if count != 6 {
			t.Errorf("%s row count after reapply = %d, want 6", table, count)
		}
	}
}
