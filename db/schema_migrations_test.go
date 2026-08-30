package dbutil_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/testdb"
)

var legacyMigrationFiles = map[string]struct{}{
	"28_transactional_liquidity_target_direction.sql": {},
	"29_lsp_channel_config.sql":                       {},
	"30_submarine_swaps.sql":                          {},
	"31_dex_taken_funding_review.sql":                 {},
	"32_submarine_swap_reservation_id.sql":            {},
	"33_authoritative_snapshot_routing.sql":           {},
	"33_cex_provider_fees.sql":                        {},
	"34_payment_method_chat_template_directions.sql":  {},
	"34_transactional_liquidity_lightning_source.sql": {},
	"35_cex_provider_fee_units.sql":                   {},
	"36_repair_legacy_varchar_directions.sql":         {},
}

var timestampMigrationFilename = regexp.MustCompile(`^\d{14}_[a-z0-9_]+\.sql$`)

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

// markExistingMigrationFilesAppliedExcept models a legacy database that has
// already applied every migration except the one under test. This keeps a
// focused migration test independent of unrelated forward migrations.
func markExistingMigrationFilesAppliedExcept(t *testing.T, db *dbutil.DB, excluded string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE migration_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create migration_migrations: %v", err)
	}
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, entry := range entries {
		filename := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(filename, ".sql") || filename == excluded {
			continue
		}
		if _, err := db.Exec(`INSERT INTO migration_migrations (filename) VALUES ($1)`, filename); err != nil {
			t.Fatalf("record prior migration %s: %v", filename, err)
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

func TestEnsureUnifiedSchemaAppliesLightningLiquiditySourceMigration(t *testing.T) {
	db := testdb.Open(t)

	var source string
	if err := db.QueryRow(`SELECT lightning_liquidity_source FROM transactional_liquidity_settings WHERE id = 1`).Scan(&source); err != nil {
		t.Fatalf("read lightning liquidity source: %v", err)
	}
	if source != "own_channels" {
		t.Errorf("lightning liquidity source = %q, want own_channels", source)
	}
}

func TestEnsureUnifiedSchemaAppliesSupportedRoutingProfilesMigration(t *testing.T) {
	db := testdb.Open(t)

	var matches bool
	if err := db.QueryRow(`
		SELECT supported_routing_profiles = ARRAY['Direct_main2main', 'Hub_main2main', 'Swap_main2lightning']::TEXT[]
		FROM transactional_liquidity_settings
		WHERE id = 1
	`).Scan(&matches); err != nil {
		t.Fatalf("read supported routing profiles: %v", err)
	}
	if !matches {
		t.Fatal("supported_routing_profiles default is not all three canonical profiles")
	}
}

func TestNewMigrationFilenamesUseTimestamps(t *testing.T) {
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if _, legacy := legacyMigrationFiles[entry.Name()]; legacy {
			continue
		}
		if !timestampMigrationFilename.MatchString(entry.Name()) {
			t.Errorf("new migration filename %q must use YYYYMMDDHHMMSS_description.sql", entry.Name())
		}
	}
}

func TestEnsureUnifiedSchemaRepairsRecordedLightningLiquiditySourceMigration(t *testing.T) {
	db := testdb.Open(t)

	if _, err := db.Exec(`ALTER TABLE transactional_liquidity_settings DROP COLUMN lightning_liquidity_source`); err != nil {
		t.Fatalf("remove lightning liquidity source for legacy fixture: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM migration_migrations WHERE filename = '20260817000000_repair_transactional_liquidity_lightning_source.sql'`); err != nil {
		t.Fatalf("reset repair migration record: %v", err)
	}

	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		t.Fatalf("repair recorded lightning liquidity source migration: %v", err)
	}

	var source string
	if err := db.QueryRow(`SELECT lightning_liquidity_source FROM transactional_liquidity_settings WHERE id = 1`).Scan(&source); err != nil {
		t.Fatalf("read repaired lightning liquidity source: %v", err)
	}
	if source != "own_channels" {
		t.Errorf("repaired lightning liquidity source = %q, want own_channels", source)
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
	markExistingMigrationFilesAppliedExcept(t, db, "36_repair_legacy_varchar_directions.sql")

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

func TestEnsureUnifiedSchemaCanonicalizesTransactionStatuses(t *testing.T) {
	db := testdb.OpenUninitialized(t)
	if _, err := db.Exec(`
		CREATE TABLE transactions (
			id BIGSERIAL PRIMARY KEY,
			status VARCHAR(32),
			transaction_complete BOOLEAN NOT NULL DEFAULT FALSE,
			CONSTRAINT transactions_status_check CHECK (
				status IS NULL OR status IN (
					'FiatToCexInitiated', 'FiatToCexArrived',
					'CexTradeSucessfull', 'DexPositionOpen'
				)
			)
		);
		INSERT INTO transactions (status) VALUES
			(NULL), ('FiatToCexInitiated'), ('FiatToCexArrived'),
			('CexTradeSucessfull'), ('DexPositionOpen');
	`); err != nil {
		t.Fatalf("create legacy transaction statuses: %v", err)
	}
	markExistingSchemaFilesApplied(t, db)
	markExistingMigrationFilesAppliedExcept(t, db, "20260829210000_transaction_status_contract.sql")

	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		t.Fatalf("apply transaction status migration: %v", err)
	}

	rows, err := db.Query(`SELECT status, COALESCE(status_reason, '') FROM transactions ORDER BY id`)
	if err != nil {
		t.Fatalf("read migrated transaction statuses: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var status, reason string
		if err := rows.Scan(&status, &reason); err != nil {
			t.Fatalf("scan migrated transaction status: %v", err)
		}
		got = append(got, status+"|"+reason)
	}
	want := []string{
		"TransactionNeedsReview|Migrated from legacy NULL transaction status",
		"TransactionNeedsReview|Migrated from legacy transaction status FiatToCexInitiated",
		"TransactionNeedsReview|Migrated from legacy transaction status FiatToCexArrived",
		"CexTradeSuccessful|",
		"DexPositionTaken|",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("migrated statuses:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := db.Exec(`INSERT INTO transactions (status) VALUES ('HedgelessPreparationFailed'), ('LiquidityPreparationNeedsReview')`); err != nil {
		t.Fatalf("canonical review statuses rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO transactions (status) VALUES ('DexPositionOpen')`); err == nil {
		t.Fatal("removed DexPositionOpen status was accepted")
	}
	if _, err := db.Exec(`INSERT INTO transactions (status) VALUES (NULL)`); err == nil {
		t.Fatal("NULL transaction status was accepted")
	}
}

func TestEnsureUnifiedSchemaConvertsLegacyVarcharEnums(t *testing.T) {
	db := testdb.OpenUninitialized(t)
	if _, err := db.Exec(`
		CREATE TYPE dex_provider AS ENUM ('bisq', 'bisq2', 'hodlhodl', 'robosats', 'peachbitcoin');
		CREATE TYPE trade_direction AS ENUM ('bid', 'ask');
		CREATE TYPE fiat_currency AS ENUM ('EUR', 'USD', 'GBP', 'AUD', 'CHF', 'SEK', 'NOK', 'CAD');
		CREATE TABLE offers (
			id BIGSERIAL PRIMARY KEY,
			exchange VARCHAR(32) NOT NULL,
			direction VARCHAR(8) NOT NULL,
			fiat_currency CHAR(3) NOT NULL,
			CONSTRAINT offers_direction_check CHECK (direction IN ('bid', 'ask'))
		);
		CREATE TABLE snapshots (
			id BIGSERIAL PRIMARY KEY,
			exchange VARCHAR(32) NOT NULL,
			direction VARCHAR(8) NOT NULL,
			fiat_currency CHAR(3) NOT NULL
		);
		CREATE TABLE transactions (
			id BIGSERIAL PRIMARY KEY,
			dex VARCHAR(32) NOT NULL,
			direction VARCHAR(8) NOT NULL,
			fiat_currency CHAR(3) NOT NULL
		);
		CREATE TABLE ohlc (
			id BIGSERIAL PRIMARY KEY,
			fiat_currency CHAR(3) NOT NULL
		);
		CREATE TABLE payment_method_chat_templates (
			id BIGSERIAL PRIMARY KEY,
			exchange TEXT NOT NULL
		);
		INSERT INTO offers (exchange, direction, fiat_currency) VALUES ('bisq', 'bid', 'EUR');
		INSERT INTO snapshots (exchange, direction, fiat_currency) VALUES ('peachbitcoin', 'ask', 'USD');
		INSERT INTO transactions (dex, direction, fiat_currency) VALUES ('hodlhodl', 'bid', 'GBP');
		INSERT INTO ohlc (fiat_currency) VALUES ('CHF');
		INSERT INTO payment_method_chat_templates (exchange) VALUES ('robosats');
	`); err != nil {
		t.Fatalf("create legacy varchar enum tables: %v", err)
	}
	markExistingSchemaFilesApplied(t, db)
	markExistingMigrationFilesAppliedExcept(t, db, "20260830153000_repair_legacy_varchar_enums.sql")

	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		t.Fatalf("apply varchar enum repair: %v", err)
	}

	want := []struct {
		table  string
		column string
		udt    string
	}{
		{"offers", "exchange", "dex_provider"},
		{"offers", "direction", "trade_direction"},
		{"offers", "fiat_currency", "fiat_currency"},
		{"snapshots", "exchange", "dex_provider"},
		{"snapshots", "direction", "trade_direction"},
		{"snapshots", "fiat_currency", "fiat_currency"},
		{"transactions", "dex", "dex_provider"},
		{"transactions", "direction", "trade_direction"},
		{"transactions", "fiat_currency", "fiat_currency"},
		{"ohlc", "fiat_currency", "fiat_currency"},
		{"payment_method_chat_templates", "exchange", "dex_provider"},
	}
	for _, col := range want {
		var udt string
		if err := db.QueryRow(`
			SELECT udt_name FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		`, col.table, col.column).Scan(&udt); err != nil {
			t.Fatalf("read %s.%s type: %v", col.table, col.column, err)
		}
		if udt != col.udt {
			t.Errorf("%s.%s type = %q, want %q", col.table, col.column, udt, col.udt)
		}
	}
	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		t.Fatalf("reapply varchar enum repair: %v", err)
	}
}
