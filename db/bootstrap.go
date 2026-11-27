package dbutil

import (
	"fmt"
	"strings"
)

const (
	roleUnifiedAPI     = "dex_unified_api"
	roleMonitor        = "dex_monitor"
	roleMMController   = "dex_mm_controller"
	roleFiatController = "dex_fiat_controller"
	roleGrafanaRO      = "dex_grafana_ro"
	roleMonitorPublic  = "dex_monitor_public"
)

// monitorPublicTables are the market tables the public monitor
// (dexmonitor-public) reads in full. Nothing else is readable for that role.
var monitorPublicTables = []string{
	"offers",
	"order_lifecycle",
	"ohlc",
	"snapshots",
	"snapshot_log",
	"payment_methods",
	"mm_cex_provider_policies",
}

// monitorPublicColumns are the only columns of operator tables the public
// monitor reads: the capability store needs which exchanges have an active,
// recently checked key, never the key, secret, login or contact details.
var monitorPublicColumns = []struct {
	table   string
	columns string
}{
	{table: "accounts", columns: "id, provider, category, is_active"},
	{table: "api_keys", columns: "id, account_id, is_active, permissions, permissions_checked_at, permission_check_error"},
}

// RoleBootstrapConfig represents a role bootstrap config.
type RoleBootstrapConfig struct {
	// DatabaseName is the database name value for the role bootstrap config.
	DatabaseName string
	// UnifiedAPIPassword is the unified apipassword value for the role bootstrap config.
	UnifiedAPIPassword string
	// MonitorPassword is the monitor password value for the role bootstrap config.
	MonitorPassword string
	// MMControllerPassword is the mmcontroller password value for the role bootstrap config.
	MMControllerPassword string
	// FiatControllerPassword is a fiat-denominated fiat controller password value.
	FiatControllerPassword string
	// GrafanaROPassword is the password for the read-only Grafana datasource
	// role. Optional: when empty the role is skipped entirely, so deploys work
	// before the secret is provisioned.
	GrafanaROPassword string
	// MonitorPublicPassword is the password for the public monitor's
	// restricted read role (dexmonitor-public). Optional like Grafana's.
	MonitorPublicPassword string
}

// EnsureAppRoles ensures the required state exists.
func EnsureAppRoles(admin *DB, cfg RoleBootstrapConfig) error {
	if strings.TrimSpace(cfg.DatabaseName) == "" {
		return fmt.Errorf("bootstrap database name is required")
	}

	roles := []struct {
		name     string
		password string
	}{
		{name: roleUnifiedAPI, password: cfg.UnifiedAPIPassword},
		{name: roleMonitor, password: cfg.MonitorPassword},
		{name: roleMMController, password: cfg.MMControllerPassword},
		{name: roleFiatController, password: cfg.FiatControllerPassword},
	}

	for _, role := range roles {
		if strings.TrimSpace(role.password) == "" {
			return fmt.Errorf("bootstrap password missing for role %s", role.name)
		}
		if err := ensureRole(admin, role.name, role.password); err != nil {
			return err
		}
	}

	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		roleNames = append(roleNames, role.name)
	}
	roleList := strings.Join(roleNames, ", ")

	statements := []string{
		`REVOKE CREATE ON SCHEMA public FROM PUBLIC`,
		fmt.Sprintf(`GRANT CONNECT ON DATABASE "%s" TO %s`, cfg.DatabaseName, roleList),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s`, roleList),
		fmt.Sprintf(`GRANT CREATE ON SCHEMA public TO %s`, roleUnifiedAPI),
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s`, roleList),
		// Schema creation includes foreign keys to existing tables. PostgreSQL
		// requires REFERENCES separately from the ordinary DML privileges. Route
		// invalidation also installs triggers on shared tables.
		fmt.Sprintf(`GRANT REFERENCES, TRIGGER ON ALL TABLES IN SCHEMA public TO %s`, roleUnifiedAPI),
		fmt.Sprintf(`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %s`, roleList),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s`,
			roleUnifiedAPI, roleList),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT REFERENCES, TRIGGER ON TABLES TO %s`,
			roleUnifiedAPI, roleUnifiedAPI),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO %s`,
			roleUnifiedAPI, roleList),
	}

	for _, stmt := range statements {
		if _, err := admin.DB.Exec(stmt); err != nil {
			return fmt.Errorf("bootstrap roles/grants with %q: %w", stmt, err)
		}
	}

	// Schema initialization runs as dex_unified_api. Older databases may have
	// been initialized by dex_admin, which leaves enum types owned by the admin
	// role and prevents migrations such as ALTER TYPE ... RENAME VALUE. Transfer
	// ownership of the type that is changed by the schema runner so upgrades do
	// not fail on an otherwise healthy database. On a fresh database the type is
	// not present yet and will be created by the application role.
	if _, err := admin.DB.Exec(`
		DO $$
		BEGIN
			IF to_regtype('trade_direction') IS NOT NULL THEN
				ALTER TYPE trade_direction OWNER TO dex_unified_api;
			END IF;
		END
		$$`); err != nil {
		return fmt.Errorf("ensure trade_direction ownership: %w", err)
	}

	if err := ensureGrafanaRORole(admin, cfg); err != nil {
		return err
	}
	return ensureMonitorPublicRole(admin, cfg)
}

// ensureMonitorPublicRole creates/updates the public monitor's read role. It
// is rebuilt from nothing on every reconciliation (REVOKE ALL, then explicit
// grants), so a table added later is never readable by accident, unlike the
// Grafana role's default privileges. Tables that do not exist yet (the first
// bootstrap runs before the schema) are granted on the next reconciliation.
func ensureMonitorPublicRole(admin *DB, cfg RoleBootstrapConfig) error {
	if strings.TrimSpace(cfg.MonitorPublicPassword) == "" {
		return nil
	}
	if err := ensureRole(admin, roleMonitorPublic, cfg.MonitorPublicPassword); err != nil {
		return err
	}

	statements := []string{
		fmt.Sprintf(`GRANT CONNECT ON DATABASE "%s" TO %s`, cfg.DatabaseName, roleMonitorPublic),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s`, roleMonitorPublic),
		// Also revokes column privileges, so the grants below are exact.
		fmt.Sprintf(`REVOKE ALL ON ALL TABLES IN SCHEMA public FROM %s`, roleMonitorPublic),
		fmt.Sprintf(`REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM %s`, roleMonitorPublic),
	}
	for _, table := range monitorPublicTables {
		if tableExists(admin, table) {
			statements = append(statements, fmt.Sprintf(`GRANT SELECT ON public.%s TO %s`, table, roleMonitorPublic))
		}
	}
	for _, grant := range monitorPublicColumns {
		if tableExists(admin, grant.table) {
			statements = append(statements,
				fmt.Sprintf(`GRANT SELECT (%s) ON public.%s TO %s`, grant.columns, grant.table, roleMonitorPublic))
		}
	}
	for _, stmt := range statements {
		if _, err := admin.DB.Exec(stmt); err != nil {
			return fmt.Errorf("bootstrap public monitor role with %q: %w", stmt, err)
		}
	}
	return nil
}

func tableExists(admin *DB, table string) bool {
	var exists bool
	if err := admin.DB.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
		return false
	}
	return exists
}

// ensureGrafanaRORole creates/updates the SELECT-only role Grafana's Postgres
// datasource logs in with. Unlike the app roles it is optional — skipped
// without error while POSTGRES_GRAFANA_RO_PASSWORD is unprovisioned.
func ensureGrafanaRORole(admin *DB, cfg RoleBootstrapConfig) error {
	if strings.TrimSpace(cfg.GrafanaROPassword) == "" {
		return nil
	}
	if err := ensureRole(admin, roleGrafanaRO, cfg.GrafanaROPassword); err != nil {
		return err
	}

	statements := []string{
		fmt.Sprintf(`GRANT CONNECT ON DATABASE "%s" TO %s`, cfg.DatabaseName, roleGrafanaRO),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s`, roleGrafanaRO),
		fmt.Sprintf(`GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s`, roleGrafanaRO),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT SELECT ON TABLES TO %s`,
			roleUnifiedAPI, roleGrafanaRO),
	}
	for _, stmt := range statements {
		if _, err := admin.DB.Exec(stmt); err != nil {
			return fmt.Errorf("bootstrap grafana read-only role with %q: %w", stmt, err)
		}
	}
	return nil
}

func ensureRole(admin *DB, roleName, password string) error {
	var exists bool
	if err := admin.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)`, roleName).Scan(&exists); err != nil {
		return fmt.Errorf("check role %s: %w", roleName, err)
	}

	passwordLiteral := quoteLiteral(password)
	if !exists {
		if _, err := admin.DB.Exec(fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD %s`, roleName, passwordLiteral)); err != nil {
			return fmt.Errorf("create role %s: %w", roleName, err)
		}
		return nil
	}

	if _, err := admin.DB.Exec(fmt.Sprintf(`ALTER ROLE %s WITH LOGIN PASSWORD %s`, roleName, passwordLiteral)); err != nil {
		return fmt.Errorf("alter role %s: %w", roleName, err)
	}
	return nil
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
