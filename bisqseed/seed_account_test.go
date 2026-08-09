package bisqseed

// These tests guard the bisq/bisq2 startup account-seed SQL against the live
// migrated schema. The accounts table's unique constraint was changed from
// UNIQUE(provider) to UNIQUE(provider, category) by migrations 022/028, which
// broke the scripts' old `INSERT ... ON CONFLICT (provider) DO NOTHING` (Postgres:
// "there is no unique or exclusion constraint matching the ON CONFLICT
// specification"). That only surfaces against a real, migrated database — no static
// check sees SQL embedded in a shell heredoc — so it slipped through to deploy.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"dexunifiedapi/internal/testdb"
)

var (
	// First `<<'SQL' ... SQL` heredoc in a startup script that inserts into accounts
	// (the ensure_account_row_in_database seed).
	seedHeredocRE = regexp.MustCompile(`(?s)<<'SQL'\n(.*?INSERT INTO accounts.*?)\nSQL`)
	psqlSetRE     = regexp.MustCompile(`(?m)^\\set .*$`)
	providerVarRE = regexp.MustCompile(`:'[A-Z0-9_]*ACCOUNT_PROVIDER_SQL'`)
	onConflictRE  = regexp.MustCompile(`ON CONFLICT\s*\(\s*provider\s*\)`)
)

// extractAccountSeedSQL pulls the account-seed statement out of the shipped startup
// script and rewrites psql's :'VAR' param to $1 so the EXACT statement that ships can
// be executed by database/sql. Reading it from the script (rather than copying it)
// means the test can't silently drift from what the container actually runs.
func extractAccountSeedSQL(t *testing.T, script string) string {
	t.Helper()
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	m := seedHeredocRE.FindSubmatch(b)
	if m == nil {
		t.Fatalf("no accounts-seed heredoc found in %s", script)
	}
	sql := psqlSetRE.ReplaceAllLiteralString(string(m[1]), "")     // drop \set ON_ERROR_STOP
	sql = providerVarRE.ReplaceAllLiteralString(sql, "$1")         // :'..._ACCOUNT_PROVIDER_SQL' -> $1
	return strings.TrimSpace(sql)
}

// TestAccountSeedRunsAgainstMigratedSchema executes the real shipped seed statement
// against a freshly migrated Postgres. Fails loudly if the statement is incompatible
// with the current accounts constraints (e.g. a return to ON CONFLICT (provider)).
func TestAccountSeedRunsAgainstMigratedSchema(t *testing.T) {
	db := testdb.Open(t) // real PG 17 + EnsureUnifiedSchema (incl. 022/028); t.Skip if no Docker
	ctx := context.Background()

	cases := []struct{ name, script, provider string }{
		{"bisq", "bisq-startup.sh", "Bisq"},
		{"bisq2", "bisq2-startup.sh", "Bisq2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt := extractAccountSeedSQL(t, tc.script)

			// Idempotent: running twice must still succeed and leave exactly one row.
			for i := 0; i < 2; i++ {
				if _, err := db.DB.ExecContext(ctx, stmt, tc.provider); err != nil {
					t.Fatalf("seed for %s failed against migrated schema: %v\nSQL:\n%s", tc.provider, err, stmt)
				}
			}

			var n int
			var category string
			if err := db.DB.QueryRowContext(ctx,
				`SELECT count(*), coalesce(max(category)::text, '') FROM accounts WHERE provider = $1`,
				tc.provider).Scan(&n, &category); err != nil {
				t.Fatalf("read back %s account: %v", tc.provider, err)
			}
			if n != 1 {
				t.Fatalf("%s: want exactly 1 account row, got %d", tc.provider, n)
			}
			if category != "DEX" {
				t.Fatalf("%s: want category DEX, got %q", tc.provider, category)
			}
		})
	}
}

// TestNoProviderOnConflictInScripts is the always-on backstop (no Docker needed). It
// covers every account-seed site in the scripts, including the backup-block heredocs
// the behavioral test doesn't execute. accounts is UNIQUE(provider, category) now, so
// ON CONFLICT (provider) must never come back.
func TestNoProviderOnConflictInScripts(t *testing.T) {
	scripts, err := filepath.Glob("*.sh")
	if err != nil {
		t.Fatalf("glob scripts: %v", err)
	}
	if len(scripts) == 0 {
		t.Fatal("no *.sh scripts found in bisqseed")
	}
	for _, f := range scripts {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if onConflictRE.Match(b) {
			t.Errorf("%s uses ON CONFLICT (provider); accounts is UNIQUE(provider, category) since migration 022 — "+
				"use the WHERE NOT EXISTS seed instead", f)
		}
	}
}
