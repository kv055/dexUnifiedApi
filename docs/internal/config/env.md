# `internal/config/env.go`

Source package: `config`  
Source lines: `123`

## Package Purpose

This file contributes to the `config` package.

## File Role

This file implements 7 function or method entry point(s).

## Runtime Behavior

- Reads from or writes to persistent storage.

## Functions and Methods

### `LoadRequiredDotEnv`

LoadRequiredDotEnv loads key=value pairs from path and fails when the file
is missing because secrets must come from a local .env file.

Signature: `func LoadRequiredDotEnv(path string) error`

### `EnvOrDefault`

EnvOrDefault returns an environment value or the supplied default.

Signature: `func EnvOrDefault(key, fallback string) string`

### `RequireEnv`

RequireEnv returns an environment value or exits when it is missing.

Signature: `func RequireEnv(key string) string`

### `RequireEnvSet`

RequireEnvSet loads a group of required environment variables.

Signature: `func RequireEnvSet(keys ...string)`

### `PostgresURLFromEnv`

PostgresURLFromEnv builds the application Postgres URL from environment variables.

Signature: `func PostgresURLFromEnv() string`

### `PostgresAdminURLFromEnv`

PostgresAdminURLFromEnv builds the admin Postgres URL from environment variables.

Signature: `func PostgresAdminURLFromEnv() string`

### `PostgresURL`

PostgresURL formats a Postgres connection URL.

Signature: `func PostgresURL(host, port, database, user, password string) string`

## Key Dependencies

- `bufio`
- `fmt`
- `net/url`
- `os`
- `sort`
- `strings`

