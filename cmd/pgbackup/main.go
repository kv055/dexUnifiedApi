package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config represents a config.
type Config struct {
	// Enabled is the enabled value for the config.
	Enabled bool
	// PgDumpPath is the pg dump path value for the config.
	PgDumpPath string
	// PgRestorePath is the pg restore path value for the config.
	PgRestorePath string
	// RclonePath is the rclone path value for the config.
	RclonePath string
	// Host is the host value for the config.
	Host string
	// Port is the port value for the config.
	Port int
	// Database is the database value for the config.
	Database string
	// User is the user value for the config.
	User string
	// Password is the password value for the config.
	Password string
	// BackupDir is the backup dir value for the config.
	BackupDir string
	// FilePrefix is the file prefix value for the config.
	FilePrefix string
	// RetentionDays is the retention days value for the config.
	RetentionDays int
	// Upload is the upload value for the config.
	Upload bool
	// UploadRequired is the upload required value for the config.
	UploadRequired bool
	// RcloneRemote is the rclone remote value for the config.
	RcloneRemote string
	// CommandTimeout records the relevant time for the config.
	CommandTimeout time.Duration
	// RetentionNow is the retention now value for the config.
	RetentionNow time.Time
	// TimestampNow records the relevant time for the config.
	TimestampNow time.Time
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := loadConfigFromEnv()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	if err := runBackup(context.Background(), cfg); err != nil {
		log.Fatalf("backup failed: %v", err)
	}
}

func loadConfigFromEnv() (Config, error) {
	enabled, err := envBool("PG_BACKUP_ENABLED", true)
	if err != nil {
		return Config{}, err
	}

	database := envOrDefault("POSTGRES_DB", "dexarb")
	host := envOrDefault("PG_BACKUP_POSTGRES_HOST", envOrDefault("POSTGRES_HOST", "postgres"))
	port, err := envInt("PG_BACKUP_POSTGRES_PORT", envOrDefault("POSTGRES_PORT", "5432"))
	if err != nil {
		return Config{}, err
	}

	user := envOrDefault("PG_BACKUP_POSTGRES_USER", envOrDefault("POSTGRES_ADMIN_USER", "postgres"))
	password := envOrDefault("PG_BACKUP_POSTGRES_PASSWORD", os.Getenv("POSTGRES_ADMIN_PASSWORD"))

	retentionDays, err := envInt("PG_BACKUP_RETENTION_DAYS", "14")
	if err != nil {
		return Config{}, err
	}
	timeout, err := envDuration("PG_BACKUP_COMMAND_TIMEOUT", 2*time.Hour)
	if err != nil {
		return Config{}, err
	}
	upload, err := envBool("PG_BACKUP_UPLOAD", true)
	if err != nil {
		return Config{}, err
	}
	uploadRequired, err := envBool("PG_BACKUP_UPLOAD_REQUIRED", true)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Enabled:        enabled,
		PgDumpPath:     envOrDefault("PG_BACKUP_PG_DUMP_PATH", "pg_dump"),
		PgRestorePath:  envOrDefault("PG_BACKUP_PG_RESTORE_PATH", "pg_restore"),
		RclonePath:     envOrDefault("PG_BACKUP_RCLONE_PATH", "rclone"),
		Host:           host,
		Port:           port,
		Database:       database,
		User:           user,
		Password:       password,
		BackupDir:      envOrDefault("PG_BACKUP_DIR", "/backups"),
		FilePrefix:     envOrDefault("PG_BACKUP_FILE_PREFIX", database),
		RetentionDays:  retentionDays,
		Upload:         upload,
		UploadRequired: uploadRequired,
		RcloneRemote:   strings.TrimSpace(os.Getenv("PG_BACKUP_RCLONE_REMOTE")),
		CommandTimeout: timeout,
		RetentionNow:   time.Now(),
		TimestampNow:   time.Now().UTC(),
	}
	return cfg, validateConfig(cfg)
}

func validateConfig(cfg Config) error {
	if !cfg.Enabled {
		return nil
	}

	switch {
	case strings.TrimSpace(cfg.Host) == "":
		return errors.New("PG_BACKUP_POSTGRES_HOST or POSTGRES_HOST is required")
	case cfg.Port <= 0:
		return errors.New("PG_BACKUP_POSTGRES_PORT or POSTGRES_PORT must be > 0")
	case strings.TrimSpace(cfg.Database) == "":
		return errors.New("POSTGRES_DB is required")
	case strings.TrimSpace(cfg.User) == "":
		return errors.New("PG_BACKUP_POSTGRES_USER or POSTGRES_ADMIN_USER is required")
	case strings.TrimSpace(cfg.Password) == "":
		return errors.New("PG_BACKUP_POSTGRES_PASSWORD or POSTGRES_ADMIN_PASSWORD is required")
	case strings.TrimSpace(cfg.BackupDir) == "":
		return errors.New("PG_BACKUP_DIR is required")
	case strings.TrimSpace(cfg.FilePrefix) == "":
		return errors.New("PG_BACKUP_FILE_PREFIX or POSTGRES_DB is required")
	case cfg.CommandTimeout <= 0:
		return errors.New("PG_BACKUP_COMMAND_TIMEOUT must be > 0")
	case cfg.Upload && strings.TrimSpace(cfg.RcloneRemote) == "":
		return errors.New("PG_BACKUP_RCLONE_REMOTE is required when PG_BACKUP_UPLOAD=true")
	}
	return nil
}

func runBackup(ctx context.Context, cfg Config) error {
	if !cfg.Enabled {
		log.Printf("backup disabled by PG_BACKUP_ENABLED=false")
		return nil
	}

	if err := os.MkdirAll(cfg.BackupDir, 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	pgpassPath, cleanup, err := createPgpassFile(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	backupFile := backupFilename(cfg)
	log.Printf("starting pg_dump to %s", backupFile)
	if err := runCommand(ctx, cfg.CommandTimeout, pgDumpCommand(cfg, backupFile, pgpassPath)); err != nil {
		return fmt.Errorf("pg_dump: %w", err)
	}

	info, err := os.Stat(backupFile)
	if err != nil {
		return fmt.Errorf("stat backup: %w", err)
	}
	log.Printf("pg_dump complete: %s (%d bytes)", backupFile, info.Size())

	log.Printf("verifying archive with pg_restore --list")
	if err := runCommand(ctx, cfg.CommandTimeout, pgRestoreListCommand(cfg, backupFile)); err != nil {
		return fmt.Errorf("pg_restore --list: %w", err)
	}

	if cfg.Upload {
		log.Printf("uploading backup with rclone to %s", cfg.RcloneRemote)
		if err := runCommand(ctx, cfg.CommandTimeout, rcloneCopyCommand(cfg, backupFile)); err != nil {
			if cfg.UploadRequired {
				return fmt.Errorf("rclone copy: %w", err)
			}
			log.Printf("rclone copy failed but PG_BACKUP_UPLOAD_REQUIRED=false: %v", err)
		} else {
			log.Printf("rclone upload complete")
		}
	}

	if cfg.RetentionDays > 0 {
		if err := cleanupOldBackups(cfg); err != nil {
			return fmt.Errorf("cleanup old backups: %w", err)
		}
	}

	log.Printf("backup run complete")
	return nil
}

func createPgpassFile(cfg Config) (string, func(), error) {
	file, err := os.CreateTemp("", "pgbackup-pgpass-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create pgpass file: %w", err)
	}

	cleanup := func() {
		_ = os.Remove(file.Name())
	}

	line := fmt.Sprintf("%s:%d:%s:%s:%s\n",
		escapePgpassField(cfg.Host),
		cfg.Port,
		escapePgpassField(cfg.Database),
		escapePgpassField(cfg.User),
		escapePgpassField(cfg.Password),
	)
	if _, err := file.WriteString(line); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("write pgpass file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("chmod pgpass file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close pgpass file: %w", err)
	}
	return file.Name(), cleanup, nil
}

func escapePgpassField(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, ":", `\:`)
}

func backupFilename(cfg Config) string {
	ts := cfg.TimestampNow.UTC().Format("2006-01-02T15-04-05Z")
	return filepath.Join(cfg.BackupDir, fmt.Sprintf("%s_%s.dump", cfg.FilePrefix, ts))
}

func pgDumpCommand(cfg Config, outFile, pgpassPath string) *exec.Cmd {
	args := []string{
		"-h", cfg.Host,
		"-p", strconv.Itoa(cfg.Port),
		"-U", cfg.User,
		"-d", cfg.Database,
		"-Fc",
		"-f", outFile,
	}
	cmd := exec.Command(cfg.PgDumpPath, args...)
	cmd.Env = append(os.Environ(), "PGPASSFILE="+pgpassPath, "PGAPPNAME=dexarb-pgbackup")
	return cmd
}

func pgRestoreListCommand(cfg Config, backupFile string) *exec.Cmd {
	return exec.Command(cfg.PgRestorePath, "--list", backupFile)
}

func rcloneCopyCommand(cfg Config, backupFile string) *exec.Cmd {
	return exec.Command(cfg.RclonePath, "copy", backupFile, cfg.RcloneRemote, "--stats", "30s", "--log-level", "INFO")
}

func runCommand(ctx context.Context, timeout time.Duration, cmd *exec.Cmd) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if cmd.Path != "" && !filepath.IsAbs(cmd.Path) {
		if _, err := exec.LookPath(cmd.Path); err != nil {
			return err
		}
	}

	cmdWithCtx := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmdWithCtx.Env = cmd.Env
	cmdWithCtx.Dir = cmd.Dir
	cmdWithCtx.Stdout = os.Stdout
	cmdWithCtx.Stderr = os.Stderr

	if err := cmdWithCtx.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out after %s", timeout)
		}
		return err
	}
	return nil
}

func cleanupOldBackups(cfg Config) error {
	cutoff := cfg.RetentionNow.Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour)
	prefix := cfg.FilePrefix + "_"

	return filepath.WalkDir(cfg.BackupDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(d.Name(), prefix) || !strings.HasSuffix(d.Name(), ".dump") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(cutoff) {
			log.Printf("deleting old local backup: %s", path)
			if err := os.Remove(path); err != nil {
				return err
			}
		}
		return nil
	})
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key, fallback string) (int, error) {
	raw := envOrDefault(key, fallback)
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration like 30m, 6h, or 2h30m: %w", key, err)
	}
	return value, nil
}

func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false: %w", key, err)
	}
	return value, nil
}
