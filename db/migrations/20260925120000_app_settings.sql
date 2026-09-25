-- Db-backed operator settings for dexMonitor (app_settings table). Created
-- here, through the privileged schema runner (dex_unified_api role), because
-- the dex_monitor role intentionally holds no CREATE privilege on the public
-- schema: a runtime CREATE TABLE IF NOT EXISTS from the monitor fails with
-- SQLSTATE 42501 and keeps the service unhealthy. Default privileges already
-- grant SELECT/INSERT/UPDATE/DELETE on dex_unified_api-owned tables to every
-- app role, so no extra GRANTs are needed. Must stay in sync with
-- dexMonitor/internal/appsettings/store.go EnsureSchema DDL.
CREATE TABLE IF NOT EXISTS app_settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL DEFAULT '',
	encrypted BOOLEAN NOT NULL DEFAULT FALSE,
	updated_by TEXT NOT NULL DEFAULT '',
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
