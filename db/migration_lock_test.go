package dbutil_test

import (
	"errors"
	"testing"
	"testing/fstest"
	"time"

	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/testdb"

	"github.com/jackc/pgx/v5/pgconn"
)

const lockProbeTimeout = 100 * time.Millisecond

func lockProbeFiles() fstest.MapFS {
	return fstest.MapFS{
		"migrations/20990101000000_lock_probe.sql": {Data: []byte(`ALTER TABLE lock_probe ADD COLUMN extra INTEGER`)},
	}
}

func openLockProbe(t *testing.T) *dbutil.DB {
	t.Helper()
	db := testdb.OpenUninitialized(t)
	for _, stmt := range []string{
		`CREATE TABLE lock_probe (id INTEGER)`,
		`CREATE TABLE lock_probe_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("prepare lock probe: %v", err)
		}
	}
	return db
}

// holdProbeLock models a live application transaction reading lock_probe.
// ALTER TABLE needs an ACCESS EXCLUSIVE lock, which conflicts with it.
func holdProbeLock(t *testing.T, db *dbutil.DB) (release func()) {
	t.Helper()
	tx, err := db.DB.Begin()
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	if _, err := tx.Exec(`LOCK TABLE lock_probe IN ACCESS SHARE MODE`); err != nil {
		t.Fatalf("hold lock_probe lock: %v", err)
	}
	return func() { _ = tx.Rollback() }
}

func probeState(t *testing.T, db *dbutil.DB) (recorded, columns int) {
	t.Helper()
	if err := db.QueryRow(`SELECT count(*) FROM lock_probe_migrations`).Scan(&recorded); err != nil {
		t.Fatalf("count recorded migrations: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'lock_probe' AND column_name = 'extra'`).Scan(&columns); err != nil {
		t.Fatalf("inspect lock_probe columns: %v", err)
	}
	return recorded, columns
}

// A lock that outlives every attempt must fail the migration within a bounded
// time instead of queueing live queries behind an indefinite lock wait.
func TestMigrationLockWaitIsBounded(t *testing.T) {
	db := openLockProbe(t)
	release := holdProbeLock(t, db)
	defer release()

	const attempts = 3
	start := time.Now()
	err := dbutil.ApplyDirWithLockRetry(db, lockProbeFiles(), "migrations", "lock_probe_migrations",
		lockProbeTimeout, attempts, 10*time.Millisecond)
	elapsed := time.Since(start)

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("migration error = %v, want lock_not_available (55P03)", err)
	}
	if elapsed < attempts*lockProbeTimeout {
		t.Fatalf("gave up after %s; want %d lock waits of %s", elapsed, attempts, lockProbeTimeout)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("bounded lock wait took %s", elapsed)
	}
	if recorded, columns := probeState(t, db); recorded != 0 || columns != 0 {
		t.Fatalf("failed migration left recorded=%d columns=%d", recorded, columns)
	}
}

// A transient lock, such as a snapshot write finishing, must be retried
// rather than failing the deployment.
func TestMigrationRetriesTransientLock(t *testing.T) {
	db := openLockProbe(t)
	release := holdProbeLock(t, db)

	done := make(chan error, 1)
	go func() {
		done <- dbutil.ApplyDirWithLockRetry(db, lockProbeFiles(), "migrations", "lock_probe_migrations",
			lockProbeTimeout, 100, 10*time.Millisecond)
	}()
	time.Sleep(3 * lockProbeTimeout)
	select {
	case err := <-done:
		release()
		t.Fatalf("migration finished while the lock was held: %v", err)
	default:
	}
	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("migration after lock release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("migration did not finish after the lock was released")
	}
	if recorded, columns := probeState(t, db); recorded != 1 || columns != 1 {
		t.Fatalf("retried migration recorded=%d columns=%d, want 1 and 1", recorded, columns)
	}
}
