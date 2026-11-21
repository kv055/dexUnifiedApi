package main

import (
	"testing"
	"time"

	"dexunifiedapi/internal/testdb"
)

func TestClosedOfferAnalysisIntervalCutoffUsesEnv(t *testing.T) {
	t.Setenv("SNAPSHOT_INTERVAL_MIN", "2")
	t.Setenv("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", "4")

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	got := closedOfferAnalysisIntervalCutoff(now)
	want := now.Add(-8 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", got, want)
	}
}

func TestClosedOfferAnalysisIntervalCutoffDefaults(t *testing.T) {
	t.Setenv("SNAPSHOT_INTERVAL_MIN", "")
	t.Setenv("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", "")

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	got := closedOfferAnalysisIntervalCutoff(now)
	want := now.Add(-3 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", got, want)
	}
}

// Real cycles (~3 min) are longer than SNAPSHOT_INTERVAL_MIN (1), so the grace
// window must come from completed snapshot_log cycles, not the configured
// interval: with three-minute cycles a 3-minute window is a single cycle.
func TestClosedOfferAnalysisGraceCutoffCountsCompletedCycles(t *testing.T) {
	t.Setenv("SNAPSHOT_INTERVAL_MIN", "1")
	t.Setenv("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", "3")
	db := testdb.Open(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, ago := range []time.Duration{3 * time.Minute, 6 * time.Minute, 9 * time.Minute, 12 * time.Minute} {
		if _, err := db.Exec(`INSERT INTO snapshot_log (timestamp, exchanges, offers_count, cycle_ms) VALUES (?, '{}'::jsonb, 0, 180000)`, now.Add(-ago)); err != nil {
			t.Fatalf("insert snapshot_log: %v", err)
		}
	}

	got := closedOfferAnalysisGraceCutoff(db, now)
	if want := now.Add(-9 * time.Minute); !got.Equal(want) {
		t.Fatalf("cutoff = %v, want end of the cycle before the last three (%v)", got, want)
	}
}

func TestClosedOfferAnalysisGraceCutoffKeepsIntervalFloor(t *testing.T) {
	t.Setenv("SNAPSHOT_INTERVAL_MIN", "10")
	t.Setenv("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", "3")
	db := testdb.Open(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, ago := range []time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute} {
		if _, err := db.Exec(`INSERT INTO snapshot_log (timestamp, exchanges, offers_count, cycle_ms) VALUES (?, '{}'::jsonb, 0, 60000)`, now.Add(-ago)); err != nil {
			t.Fatalf("insert snapshot_log: %v", err)
		}
	}

	got := closedOfferAnalysisGraceCutoff(db, now)
	if want := now.Add(-30 * time.Minute); !got.Equal(want) {
		t.Fatalf("cutoff = %v, want interval floor %v when cycles are faster than configured", got, want)
	}
}

func TestClosedOfferAnalysisGraceCutoffFallsBackWithoutHistory(t *testing.T) {
	t.Setenv("SNAPSHOT_INTERVAL_MIN", "1")
	t.Setenv("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", "3")
	db := testdb.Open(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO snapshot_log (timestamp, exchanges, offers_count) VALUES (?, '{}'::jsonb, 0)`, now.Add(-time.Hour)); err != nil {
		t.Fatalf("insert snapshot_log: %v", err)
	}

	got := closedOfferAnalysisGraceCutoff(db, now)
	if want := now.Add(-3 * time.Minute); !got.Equal(want) {
		t.Fatalf("cutoff = %v, want interval fallback %v with fewer than three logged cycles", got, want)
	}
}
