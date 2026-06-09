package main

import (
	"testing"
	"time"
)

func TestClosedOfferAnalysisGraceCutoffUsesEnv(t *testing.T) {
	t.Setenv("SNAPSHOT_INTERVAL_MIN", "2")
	t.Setenv("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", "4")

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	got := closedOfferAnalysisGraceCutoff(now)
	want := now.Add(-8 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", got, want)
	}
}

func TestClosedOfferAnalysisGraceCutoffDefaults(t *testing.T) {
	t.Setenv("SNAPSHOT_INTERVAL_MIN", "")
	t.Setenv("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", "")

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	got := closedOfferAnalysisGraceCutoff(now)
	want := now.Add(-3 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", got, want)
	}
}
