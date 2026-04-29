package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

// LoadRequiredDotEnv loads key=value pairs from path and fails when the file
// is missing because secrets must come from a local .env file.
func LoadRequiredDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("missing secrets: required .env file not found at %s", path)
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid .env line: %q", line)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			return fmt.Errorf("invalid .env line with empty key: %q", line)
		}
		if os.Getenv(key) != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env %s: %w", key, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// EnvOrDefault returns an environment value or the supplied default.
func EnvOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// RequireEnv returns an environment value or exits when it is missing.
func RequireEnv(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		panic(fmt.Sprintf("missing secrets: %s", key))
	}
	return value
}

// RequireEnvSet loads a group of required environment variables.
func RequireEnvSet(keys ...string) {
	var missing []string
	for _, key := range keys {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	panic(fmt.Sprintf("missing secrets: %s", strings.Join(missing, ", ")))
}

// PostgresURLFromEnv builds the application Postgres URL from environment variables.
func PostgresURLFromEnv() string {
	host := EnvOrDefault("POSTGRES_HOST", "localhost")
	port := EnvOrDefault("POSTGRES_PORT", "5432")
	database := EnvOrDefault("POSTGRES_DB", "dexarb")
	RequireEnvSet("POSTGRES_USER", "POSTGRES_PASSWORD")
	user := RequireEnv("POSTGRES_USER")
	password := RequireEnv("POSTGRES_PASSWORD")

	return PostgresURL(host, port, database, user, password)
}

// PostgresAdminURLFromEnv builds the admin Postgres URL from environment variables.
func PostgresAdminURLFromEnv() string {
	host := EnvOrDefault("POSTGRES_HOST", "localhost")
	port := EnvOrDefault("POSTGRES_PORT", "5432")
	database := EnvOrDefault("POSTGRES_DB", "dexarb")
	user := EnvOrDefault("POSTGRES_ADMIN_USER", RequireEnv("POSTGRES_USER"))
	password := EnvOrDefault("POSTGRES_ADMIN_PASSWORD", RequireEnv("POSTGRES_PASSWORD"))

	return PostgresURL(host, port, database, user, password)
}

// PostgresURL formats a Postgres connection URL.
func PostgresURL(host, port, database, user, password string) string {
	return (&url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   host + ":" + port,
		Path:   "/" + database,
		RawQuery: url.Values{
			"sslmode": []string{"disable"},
		}.Encode(),
	}).String()
}
