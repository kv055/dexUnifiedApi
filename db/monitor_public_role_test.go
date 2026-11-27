package dbutil_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/testdb"
)

func publicRoleConfig(db *dbutil.DB, t *testing.T, password string) dbutil.RoleBootstrapConfig {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatalf("current database: %v", err)
	}
	return dbutil.RoleBootstrapConfig{
		DatabaseName:           name,
		UnifiedAPIPassword:     "unified",
		MonitorPassword:        "monitor",
		MMControllerPassword:   "mm",
		FiatControllerPassword: "fiat",
		MonitorPublicPassword:  password,
	}
}

// asRole runs query as dex_monitor_public on one connection and returns its error.
func asRole(t *testing.T, db *dbutil.DB, query string) error {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET ROLE dex_monitor_public`); err != nil {
		t.Fatalf("set role: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, `RESET ROLE`) }()
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

func TestMonitorPublicRoleReadsOnlyMarketData(t *testing.T) {
	db := testdb.Open(t)
	cfg := publicRoleConfig(db, t, "public-secret")
	if err := dbutil.EnsureAppRoles(db, cfg); err != nil {
		t.Fatalf("bootstrap roles: %v", err)
	}
	// Reconciliation runs on every start; it must stay exact when repeated.
	if err := dbutil.EnsureAppRoles(db, cfg); err != nil {
		t.Fatalf("re-bootstrap roles: %v", err)
	}

	allowed := []string{
		`SELECT * FROM offers`,
		`SELECT * FROM order_lifecycle`,
		`SELECT * FROM ohlc`,
		`SELECT * FROM snapshots`,
		`SELECT * FROM snapshot_log`,
		`SELECT * FROM payment_methods`,
		`SELECT * FROM mm_cex_provider_policies`,
		// What the capability store reads.
		`SELECT a.id, a.provider, k.id, k.permissions, k.permissions_checked_at, k.permission_check_error
		   FROM accounts a LEFT JOIN api_keys k ON k.account_id = a.id AND k.is_active = TRUE
		  WHERE a.category = 'CEX' AND a.is_active = TRUE`,
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN ('offers', 'snapshot_log')`,
	}
	for _, query := range allowed {
		if err := asRole(t, db, query); err != nil {
			t.Errorf("public role cannot run %q: %v", query, err)
		}
	}

	denied := []string{
		`SELECT api_key FROM api_keys`,
		`SELECT api_secret FROM api_keys`,
		`SELECT passphrase FROM api_keys`,
		`SELECT * FROM api_keys`,
		`SELECT password FROM accounts`,
		`SELECT email FROM accounts`,
		`SELECT * FROM accounts`,
		`SELECT * FROM seeds`,
		`SELECT * FROM totp_secrets`,
		`SELECT * FROM passkey_credentials`,
		`SELECT * FROM bank_accounts`,
		`SELECT * FROM app_users`,
		`SELECT * FROM sessions`,
		`SELECT * FROM bisq_account_file_backups`,
		`SELECT * FROM lnd_channel_backups`,
		`SELECT * FROM telegram_settings`,
		`SELECT * FROM transactions`,
		`SELECT * FROM wallet_addresses`,
		`SELECT * FROM transactional_liquidity_settings`,
		`INSERT INTO ohlc (time, open, high, low, close) VALUES (now(), 1, 1, 1, 1)`,
		`UPDATE offers SET is_live = FALSE`,
		`DELETE FROM snapshot_log`,
		`UPDATE api_keys SET permissions = NULL`,
	}
	for _, query := range denied {
		err := asRole(t, db, query)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("public role must not run %q (err %v)", query, err)
		}
	}
}

func TestMonitorPublicRoleIsOptional(t *testing.T) {
	db := testdb.Open(t)
	if err := dbutil.EnsureAppRoles(db, publicRoleConfig(db, t, "")); err != nil {
		t.Fatalf("bootstrap roles without a public password: %v", err)
	}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = 'dex_monitor_public')`).Scan(&exists); err != nil && err != sql.ErrNoRows {
		t.Fatalf("check role: %v", err)
	}
	// Roles are cluster-wide and tests share one container, so only assert the
	// role has no access to this database when it was not provisioned here.
	if exists {
		if err := asRole(t, db, `SELECT * FROM offers`); err == nil {
			t.Fatal("unprovisioned public role can read offers in a fresh database")
		}
	}
}
