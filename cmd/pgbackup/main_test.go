package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("POSTGRES_DB", "dexarb_test")
	t.Setenv("POSTGRES_HOST", "ignored-host")
	t.Setenv("POSTGRES_PORT", "5433")
	t.Setenv("POSTGRES_ADMIN_USER", "dex_admin")
	t.Setenv("POSTGRES_ADMIN_PASSWORD", "secret")
	t.Setenv("PG_BACKUP_POSTGRES_HOST", "postgres")
	t.Setenv("PG_BACKUP_POSTGRES_PORT", "5432")
	t.Setenv("PG_BACKUP_DIR", "/tmp/backups")
	t.Setenv("PG_BACKUP_FILE_PREFIX", "dexarb")
	t.Setenv("PG_BACKUP_RETENTION_DAYS", "21")
	t.Setenv("PG_BACKUP_UPLOAD", "true")
	t.Setenv("PG_BACKUP_UPLOAD_REQUIRED", "true")
	t.Setenv("PG_BACKUP_RCLONE_REMOTE", "proton:postgres-backups/dexarb")
	t.Setenv("PG_BACKUP_COMMAND_TIMEOUT", "30m")

	cfg, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("loadConfigFromEnv() error = %v", err)
	}

	if cfg.Host != "postgres" {
		t.Fatalf("Host = %q, want postgres", cfg.Host)
	}
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if cfg.Port != 5432 {
		t.Fatalf("Port = %d, want 5432", cfg.Port)
	}
	if cfg.Database != "dexarb_test" {
		t.Fatalf("Database = %q, want dexarb_test", cfg.Database)
	}
	if cfg.User != "dex_admin" || cfg.Password != "secret" {
		t.Fatalf("credentials = %q/%q, want dex_admin/secret", cfg.User, cfg.Password)
	}
	if cfg.BackupDir != "/tmp/backups" || cfg.FilePrefix != "dexarb" {
		t.Fatalf("backup path config = %q/%q", cfg.BackupDir, cfg.FilePrefix)
	}
	if cfg.RetentionDays != 21 {
		t.Fatalf("RetentionDays = %d, want 21", cfg.RetentionDays)
	}
	if !cfg.Upload || !cfg.UploadRequired {
		t.Fatalf("Upload/UploadRequired = %v/%v, want true/true", cfg.Upload, cfg.UploadRequired)
	}
	if cfg.RcloneRemote != "proton:postgres-backups/dexarb" {
		t.Fatalf("RcloneRemote = %q", cfg.RcloneRemote)
	}
	if cfg.CommandTimeout != 30*time.Minute {
		t.Fatalf("CommandTimeout = %s, want 30m", cfg.CommandTimeout)
	}
}

func TestLoadConfigDisabledSkipsValidation(t *testing.T) {
	t.Setenv("PG_BACKUP_ENABLED", "false")
	t.Setenv("PG_BACKUP_UPLOAD", "true")

	cfg, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("loadConfigFromEnv() error = %v", err)
	}
	if cfg.Enabled {
		t.Fatal("Enabled = true, want false")
	}
}

func TestLoadConfigUploadRequiresRemote(t *testing.T) {
	t.Setenv("POSTGRES_ADMIN_PASSWORD", "secret")
	t.Setenv("PG_BACKUP_UPLOAD", "true")

	_, err := loadConfigFromEnv()
	if err == nil {
		t.Fatal("loadConfigFromEnv() error = nil, want missing remote error")
	}
	if !strings.Contains(err.Error(), "PG_BACKUP_RCLONE_REMOTE") {
		t.Fatalf("error = %q, want PG_BACKUP_RCLONE_REMOTE", err)
	}
}

func TestBackupFilenameUsesUTCTimestamp(t *testing.T) {
	cfg := Config{
		BackupDir:    "/backups",
		FilePrefix:   "dexarb",
		TimestampNow: time.Date(2026, 4, 23, 5, 6, 7, 0, time.FixedZone("MYT", 8*60*60)),
	}

	got := backupFilename(cfg)
	want := filepath.Join("/backups", "dexarb_2026-04-22T21-06-07Z.dump")
	if got != want {
		t.Fatalf("backupFilename() = %q, want %q", got, want)
	}
}

func TestCommandArgs(t *testing.T) {
	cfg := Config{
		PgDumpPath:    "pg_dump",
		PgRestorePath: "pg_restore",
		RclonePath:    "rclone",
		Host:          "postgres",
		Port:          5432,
		Database:      "dexarb",
		User:          "dex_admin",
		RcloneRemote:  "dropbox:postgres-backups/dexarb",
	}

	pgDump := pgDumpCommand(cfg, "/backups/dexarb.dump", "/tmp/pgpass")
	wantDumpArgs := []string{
		"pg_dump",
		"-h", "postgres",
		"-p", "5432",
		"-U", "dex_admin",
		"-d", "dexarb",
		"-Fc",
		"-f", "/backups/dexarb.dump",
	}
	if !reflect.DeepEqual(pgDump.Args, wantDumpArgs) {
		t.Fatalf("pgDump.Args = %#v, want %#v", pgDump.Args, wantDumpArgs)
	}
	if !envContains(pgDump.Env, "PGPASSFILE=/tmp/pgpass") {
		t.Fatalf("pgDump.Env missing PGPASSFILE")
	}
	if !envContains(pgDump.Env, "PGAPPNAME=dexarb-pgbackup") {
		t.Fatalf("pgDump.Env missing PGAPPNAME")
	}

	pgRestore := pgRestoreListCommand(cfg, "/backups/dexarb.dump")
	wantRestoreArgs := []string{"pg_restore", "--list", "/backups/dexarb.dump"}
	if !reflect.DeepEqual(pgRestore.Args, wantRestoreArgs) {
		t.Fatalf("pgRestore.Args = %#v, want %#v", pgRestore.Args, wantRestoreArgs)
	}

	rclone := rcloneCopyCommand(cfg, "/backups/dexarb.dump")
	wantRcloneArgs := []string{
		"rclone",
		"copy",
		"/backups/dexarb.dump",
		"dropbox:postgres-backups/dexarb",
		"--stats",
		"30s",
		"--log-level",
		"INFO",
	}
	if !reflect.DeepEqual(rclone.Args, wantRcloneArgs) {
		t.Fatalf("rclone.Args = %#v, want %#v", rclone.Args, wantRcloneArgs)
	}
}

func TestCleanupOldBackups(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)

	oldMatching := filepath.Join(dir, "dexarb_2026-04-01T00-00-00Z.dump")
	newMatching := filepath.Join(dir, "dexarb_2026-04-22T00-00-00Z.dump")
	oldOtherPrefix := filepath.Join(dir, "other_2026-04-01T00-00-00Z.dump")
	oldText := filepath.Join(dir, "dexarb_2026-04-01T00-00-00Z.txt")

	for _, path := range []string{oldMatching, newMatching, oldOtherPrefix, oldText} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	oldTime := now.Add(-20 * 24 * time.Hour)
	newTime := now.Add(-1 * 24 * time.Hour)
	for _, path := range []string{oldMatching, oldOtherPrefix, oldText} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}
	if err := os.Chtimes(newMatching, newTime, newTime); err != nil {
		t.Fatalf("chtimes %s: %v", newMatching, err)
	}

	cfg := Config{
		BackupDir:     dir,
		FilePrefix:    "dexarb",
		RetentionDays: 14,
		RetentionNow:  now,
	}
	if err := cleanupOldBackups(cfg); err != nil {
		t.Fatalf("cleanupOldBackups() error = %v", err)
	}

	if _, err := os.Stat(oldMatching); !os.IsNotExist(err) {
		t.Fatalf("old matching backup still exists or stat errored: %v", err)
	}
	for _, path := range []string{newMatching, oldOtherPrefix, oldText} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to remain: %v", path, err)
		}
	}
}

func TestEscapePgpassField(t *testing.T) {
	got := escapePgpassField(`pa:ss\word`)
	want := `pa\:ss\\word`
	if got != want {
		t.Fatalf("escapePgpassField() = %q, want %q", got, want)
	}
}

func envContains(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}
