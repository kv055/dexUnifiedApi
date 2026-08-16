package testdb

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	dbutil "dexunifiedapi/db"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	testDBUser     = "postgres"
	testDBPassword = "postgres"
	testDBName     = "postgres"
)

var (
	containerOnce sync.Once
	container     *postgres.PostgresContainer
	containerErr  error
	dbCounter     int
	dbCounterMu   sync.Mutex
	usersMu       sync.Mutex
	users         int
)

// Open starts an isolated Postgres database for a test and applies the unified schema.
func Open(t *testing.T) *dbutil.DB {
	t.Helper()

	db := OpenUninitialized(t)
	if err := dbutil.EnsureUnifiedSchema(db); err != nil {
		t.Fatalf("apply postgres test schema: %v", err)
	}
	if err := dbutil.SeedPaymentMethods(db); err != nil {
		t.Fatalf("seed payment methods: %v", err)
	}
	return db
}

// OpenUninitialized starts an isolated Postgres database without applying the
// unified schema. Migration tests use it to construct historical schemas.
func OpenUninitialized(t *testing.T) *dbutil.DB {
	t.Helper()

	acquireContainer(t)

	ctx := context.Background()
	baseURL := startContainer(t, ctx)
	dbName := createDatabase(t, ctx, baseURL)
	t.Cleanup(func() {
		dropDatabase(t, context.Background(), baseURL, dbName)
	})

	dbURL := strings.Replace(baseURL, "/"+testDBName+"?", "/"+dbName+"?", 1)
	db, err := dbutil.Open(dbURL)
	if err != nil {
		t.Fatalf("open postgres test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})
	return db
}

// acquireContainer tracks shared-container users and terminates it when the last
// test in this process finishes. Cleanup is LIFO, so registering first means
// Terminate runs after per-test DB drops and connection closes.
func acquireContainer(t *testing.T) {
	t.Helper()

	usersMu.Lock()
	users++
	usersMu.Unlock()

	t.Cleanup(func() {
		usersMu.Lock()
		defer usersMu.Unlock()
		users--
		if users != 0 || container == nil {
			return
		}
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(termCtx); err != nil {
			t.Logf("terminate postgres test container: %v", err)
		}
		container = nil
		containerErr = nil
		containerOnce = sync.Once{}
	})
}

func startContainer(t *testing.T, ctx context.Context) string {
	t.Helper()

	containerOnce.Do(func() {
		container, containerErr = postgres.Run(
			ctx,
			"postgres:17-alpine",
			postgres.WithDatabase(testDBName),
			postgres.WithUsername(testDBUser),
			postgres.WithPassword(testDBPassword),
			postgres.BasicWaitStrategies(),
		)
	})
	if containerErr != nil {
		t.Skipf("postgres test container unavailable: %v", containerErr)
	}

	baseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres test connection string: %v", err)
	}
	return baseURL
}

func createDatabase(t *testing.T, ctx context.Context, baseURL string) string {
	t.Helper()

	dbCounterMu.Lock()
	dbCounter++
	dbName := fmt.Sprintf("dexunifiedapi_test_%d", dbCounter)
	dbCounterMu.Unlock()

	admin, err := dbutil.Open(baseURL)
	if err != nil {
		t.Fatalf("open postgres admin database: %v", err)
	}
	defer admin.Close()

	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+dbName); err != nil {
		t.Fatalf("create postgres test database %s: %v", dbName, err)
	}
	return dbName
}

func dropDatabase(t *testing.T, ctx context.Context, baseURL, dbName string) {
	t.Helper()

	admin, err := dbutil.Open(baseURL)
	if err != nil {
		t.Logf("open postgres admin database for cleanup: %v", err)
		return
	}
	defer admin.Close()

	if _, err := admin.ExecContext(ctx, `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`); err != nil {
		t.Logf("drop postgres test database %s: %v", dbName, err)
	}
}
