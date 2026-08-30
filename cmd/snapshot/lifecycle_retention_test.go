package main

import (
	"testing"
	"time"

	"dexunifiedapi/internal/testdb"
)

func TestLifecycleRetentionDisabledWhenDaysZero(t *testing.T) {
	t.Setenv("LIFECYCLE_RETENTION_DAYS", "0")
	if got := lifecycleRetention(); got != 0 {
		t.Fatalf("retention = %s, want 0", got)
	}
}

func TestLifecycleRetentionDefaultIsThirtyDays(t *testing.T) {
	t.Setenv("LIFECYCLE_RETENTION_DAYS", "")
	if got := lifecycleRetention(); got != 30*24*time.Hour {
		t.Fatalf("retention = %s, want 30 days", got)
	}
}

func TestPruneOrderLifecycleDeletesOnlyOldRows(t *testing.T) {
	t.Setenv("LIFECYCLE_RETENTION_DAYS", "30")
	db := testdb.Open(t)
	if _, err := db.Exec(`
		INSERT INTO offers (id, exchange, fiat_currency, direction, price, is_live)
		VALUES (1, 'bisq', 'EUR', 'bid', 100, FALSE)
	`); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	keep := now.Add(-10 * 24 * time.Hour)
	if _, err := db.Exec(`
		INSERT INTO order_lifecycle (offer_db_id, snapshot_time, price, ref_price, premium_pct)
		VALUES (1, ?, 100, 100, 0), (1, ?, 101, 100, 1)
	`, old, keep); err != nil {
		t.Fatalf("insert lifecycle: %v", err)
	}

	pruneOrderLifecycleUntil(db, now, time.Second)

	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM order_lifecycle`).Scan(&remaining); err != nil {
		t.Fatalf("count lifecycle: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining rows = %d, want 1", remaining)
	}
	var kept time.Time
	if err := db.QueryRow(`SELECT snapshot_time FROM order_lifecycle`).Scan(&kept); err != nil {
		t.Fatalf("select kept row: %v", err)
	}
	if !kept.UTC().Equal(keep) {
		t.Fatalf("kept snapshot_time = %s, want %s", kept.UTC(), keep)
	}
}

func TestPruneOrderLifecycleNoopWhenDisabled(t *testing.T) {
	t.Setenv("LIFECYCLE_RETENTION_DAYS", "0")
	db := testdb.Open(t)
	if _, err := db.Exec(`
		INSERT INTO offers (id, exchange, fiat_currency, direction, price, is_live)
		VALUES (1, 'bisq', 'EUR', 'bid', 100, FALSE)
	`); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`
		INSERT INTO order_lifecycle (offer_db_id, snapshot_time, price, ref_price, premium_pct)
		VALUES (1, ?, 100, 100, 0)
	`, old); err != nil {
		t.Fatalf("insert lifecycle: %v", err)
	}

	pruneOrderLifecycleUntil(db, time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC), time.Second)

	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM order_lifecycle`).Scan(&remaining); err != nil {
		t.Fatalf("count lifecycle: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining rows = %d, want 1 when retention is disabled", remaining)
	}
}
