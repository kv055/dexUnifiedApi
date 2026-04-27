# `cmd/pgbackup/main.go`

Source package: `main`  
Source lines: `368`

## Package Purpose

This file contributes to the `main` package.

## File Role

This file defines 1 type(s), implements 16 function or method entry point(s).

## Runtime Behavior

- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `Config`

Config represents a config.

Fields:

- `Enabled` `bool`: Enabled is the enabled value for the config.
- `PgDumpPath` `string`: PgDumpPath is the pg dump path value for the config.
- `PgRestorePath` `string`: PgRestorePath is the pg restore path value for the config.
- `RclonePath` `string`: RclonePath is the rclone path value for the config.
- `Host` `string`: Host is the host value for the config.
- `Port` `int`: Port is the port value for the config.
- `Database` `string`: Database is the database value for the config.
- `User` `string`: User is the user value for the config.
- `Password` `string`: Password is the password value for the config.
- `BackupDir` `string`: BackupDir is the backup dir value for the config.
- `FilePrefix` `string`: FilePrefix is the file prefix value for the config.
- `RetentionDays` `int`: RetentionDays is the retention days value for the config.
- `Upload` `bool`: Upload is the upload value for the config.
- `UploadRequired` `bool`: UploadRequired is the upload required value for the config.
- `RcloneRemote` `string`: RcloneRemote is the rclone remote value for the config.
- `CommandTimeout` `time.Duration`: CommandTimeout records the relevant time for the config.
- `RetentionNow` `time.Time`: RetentionNow is the retention now value for the config.
- `TimestampNow` `time.Time`: TimestampNow records the relevant time for the config.

## Functions and Methods

### `main`

This function is part of the `main` package implementation.

Signature: `func main()`

### `loadConfigFromEnv`

This function is part of the `main` package implementation.

Signature: `func loadConfigFromEnv() (Config, error)`

### `validateConfig`

This function is part of the `main` package implementation.

Signature: `func validateConfig(cfg Config) error`

### `runBackup`

This function is part of the `main` package implementation.

Signature: `func runBackup(ctx context.Context, cfg Config) error`

### `createPgpassFile`

This function is part of the `main` package implementation.

Signature: `func createPgpassFile(cfg Config) (string, func(), error)`

### `escapePgpassField`

This function is part of the `main` package implementation.

Signature: `func escapePgpassField(value string) string`

### `backupFilename`

This function is part of the `main` package implementation.

Signature: `func backupFilename(cfg Config) string`

### `pgDumpCommand`

This function is part of the `main` package implementation.

Signature: `func pgDumpCommand(cfg Config, outFile, pgpassPath string) *exec.Cmd`

### `pgRestoreListCommand`

This function is part of the `main` package implementation.

Signature: `func pgRestoreListCommand(cfg Config, backupFile string) *exec.Cmd`

### `rcloneCopyCommand`

This function is part of the `main` package implementation.

Signature: `func rcloneCopyCommand(cfg Config, backupFile string) *exec.Cmd`

### `runCommand`

This function is part of the `main` package implementation.

Signature: `func runCommand(ctx context.Context, timeout time.Duration, cmd *exec.Cmd) error`

### `cleanupOldBackups`

This function is part of the `main` package implementation.

Signature: `func cleanupOldBackups(cfg Config) error`

### `envOrDefault`

This function is part of the `main` package implementation.

Signature: `func envOrDefault(key, fallback string) string`

### `envInt`

This function is part of the `main` package implementation.

Signature: `func envInt(key, fallback string) (int, error)`

### `envDuration`

This function is part of the `main` package implementation.

Signature: `func envDuration(key string, fallback time.Duration) (time.Duration, error)`

### `envBool`

This function is part of the `main` package implementation.

Signature: `func envBool(key string, fallback bool) (bool, error)`

## Key Dependencies

- `context`
- `errors`
- `fmt`
- `io/fs`
- `log`
- `os`
- `os/exec`
- `path/filepath`
- `strconv`
- `strings`
- `time`

