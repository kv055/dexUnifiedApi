package dbutil_test

import (
	"os"
	"strings"
	"testing"

	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/testdb"
)

// countSchemaFiles counts the table-local schema files without hard-coding a count.
func countSchemaFiles(t *testing.T) int {
	t.Helper()
	n := 0
	entries, err := os.ReadDir("schema")
	if err != nil {
		t.Fatalf("read schema directory: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			n++
		}
	}
	if n == 0 {
		t.Fatal("no schema/migration files found")
	}
	return n
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
