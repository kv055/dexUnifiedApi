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
)

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
		fmt.Sprintf(`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %s`, roleList),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s`,
			roleUnifiedAPI, roleList),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO %s`,
			roleUnifiedAPI, roleList),
	}

	for _, stmt := range statements {
		if _, err := admin.DB.Exec(stmt); err != nil {
			return fmt.Errorf("bootstrap roles/grants with %q: %w", stmt, err)
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
