package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	dbutil "dexunifiedapi/db"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Exercise the same connections as the command: a superuser for role bootstrap
// and a distinct application role for DDL and default grants.
func TestMigrateDatabaseLifecycle(t *testing.T) {
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("migrate_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("test-admin"),
		postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	adminURL, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	oldAdminURL, oldAppURL := adminDatabaseURL, databaseURL
	t.Cleanup(func() { adminDatabaseURL, databaseURL = oldAdminURL, oldAppURL })
	adminDatabaseURL = adminURL
	appURL, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	setPassword := func(password string) {
		t.Helper()
		t.Setenv("POSTGRES_UNIFIED_API_PASSWORD", password)
		appURL.User = url.UserPassword("dex_unified_api", password)
		databaseURL = appURL.String()
	}
	t.Setenv("POSTGRES_DB", "migrate_test")
	t.Setenv("POSTGRES_MONITOR_PASSWORD", "test-monitor")
	t.Setenv("POSTGRES_MM_CONTROLLER_PASSWORD", "test-mm")
	t.Setenv("POSTGRES_FIAT_CONTROLLER_PASSWORD", "test-fiat")
	t.Setenv("POSTGRES_GRAFANA_RO_PASSWORD", "")
	setPassword("test-unified")

	// Ordinary startup can bootstrap roles, but must not apply schema.
	admin, app, err := prepareDatabase()
	if err != nil {
		t.Fatalf("fresh role bootstrap: %v", err)
	}
	if err := dbutil.CheckUnifiedSchema(app); err == nil {
		t.Fatal("startup unexpectedly applied schema")
	}
	var role string
	if err := app.QueryRow(`SELECT current_user`).Scan(&role); err != nil || role != "dex_unified_api" {
		t.Fatalf("application connection user = %q, err = %v", role, err)
	}
	admin.Close()
	app.Close()

	// Each command invocation must work on a new connection, including after
	// a password rotation and after rollback restores the previous password.
	for _, password := range []string{"test-unified", "test-unified", "rotated-unified", "test-unified"} {
		setPassword(password)
		if err := runMigrate(); err != nil {
			t.Fatalf("migration/bootstrap failed: %v", err)
		}
	}
	admin, app, err = prepareDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	defer app.Close()
	if err := dbutil.CheckUnifiedSchema(app); err != nil {
		t.Fatalf("migrated schema is not ready: %v", err)
	}
	var owner string
	if err := app.QueryRow(`SELECT tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename = 'offers'`).Scan(&owner); err != nil || owner != "dex_unified_api" {
		t.Fatalf("offers owner = %q, err = %v", owner, err)
	}
	var seeds int
	if err := app.QueryRow(`SELECT count(*) FROM payment_methods`).Scan(&seeds); err != nil || seeds == 0 {
		t.Fatalf("payment method seeds = %d, err = %v", seeds, err)
	}
	// Default grants must make newly migrated tables usable by the monitor.
	monitorURL := *appURL
	monitorURL.User = url.UserPassword("dex_monitor", "test-monitor")
	monitor, err := dbutil.Open(monitorURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	if _, err := monitor.Exec(`SELECT * FROM app_settings LIMIT 0`); err != nil {
		t.Fatalf("monitor cannot access migrated app_settings: %v", err)
	}

	// Readiness must use SELECTs only, and allow newer migration records during
	// rollback. These databases are disposable; no production history is changed.
	if _, err := app.Exec(`INSERT INTO migration_migrations(filename) VALUES ('20990101000000_future.sql')`); err != nil {
		t.Fatal(err)
	}
	app.SetMaxOpenConns(1)
	if _, err := app.Exec(`SET default_transaction_read_only = on`); err != nil {
		t.Fatal(err)
	}
	if err := dbutil.CheckUnifiedSchema(app); err != nil {
		t.Fatalf("read-only readiness on a newer schema: %v", err)
	}
	if _, err := app.Exec(`SET default_transaction_read_only = off`); err != nil {
		t.Fatal(err)
	}
	var missing string
	if err := app.QueryRow(`DELETE FROM migration_migrations WHERE filename = (SELECT filename FROM migration_migrations WHERE filename <> '20990101000000_future.sql' ORDER BY filename DESC LIMIT 1) RETURNING filename`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	// Startup must report the missing migration instead of silently applying it.
	startupAdmin, startupApp, err := prepareDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer startupAdmin.Close()
	defer startupApp.Close()
	if err := dbutil.CheckUnifiedSchema(startupApp); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing migration %q not reported: %v", missing, err)
	}
}
