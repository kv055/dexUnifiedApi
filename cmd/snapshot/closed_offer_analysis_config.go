package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func envIntOrDefault(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}

func closedOfferAnalysisGraceCycles() int {
	return envIntOrDefault("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", 3)
}

// closedOfferAnalysisIntervalCutoff is the clock-based floor of the grace
// window: CLOSED_OFFER_ANALYSIS_GRACE_CYCLES × SNAPSHOT_INTERVAL_MIN minutes.
func closedOfferAnalysisIntervalCutoff(now time.Time) time.Time {
	intervalMin := envIntOrDefault("SNAPSHOT_INTERVAL_MIN", 1)
	if intervalMin < 1 {
		intervalMin = 1
	}
	return now.UTC().Add(-time.Duration(closedOfferAnalysisGraceCycles()*intervalMin) * time.Minute)
}

// closedOfferAnalysisGraceCutoff returns the last_seen bound below which a
// non-live offer has been absent for CLOSED_OFFER_ANALYSIS_GRACE_CYCLES
// snapshot cycles, so a single missed fetch never triggers analysis.
//
// The cycle length is measured, not assumed. The worker runs one cycle per
// process, back to back, and a full pass takes ~3 min (longer when Tor-backed
// sources stall), while SNAPSHOT_INTERVAL_MIN is 1 — and is also dexMonitor's
// default OHLC candle interval, so it cannot simply be raised to match. Each
// completed run writes one snapshot_log row after this analysis, so the row
// graceCycles-1 back from the newest marks the end of the cycle before the
// grace window: an offer last seen before it was missing from the last
// graceCycles cycles, the current one included. The clock-based interval
// cutoff stays as a floor (whichever is older wins) and is the fallback when
// snapshot_log has too little history or cannot be read.
func closedOfferAnalysisGraceCutoff(sdb closedOfferDB, now time.Time) time.Time {
	floor := closedOfferAnalysisIntervalCutoff(now)
	graceCycles := closedOfferAnalysisGraceCycles()
	if graceCycles < 1 {
		return floor
	}
	var cycleEnd sql.NullTime
	err := sdb.QueryRow(`
		SELECT timestamp FROM snapshot_log
		ORDER BY timestamp DESC
		OFFSET ? LIMIT 1
	`, graceCycles-1).Scan(&cycleEnd)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			fmt.Printf("[ClosedAnalysis] grace cutoff from snapshot_log failed, using interval floor: %v\n", err)
		}
		return floor
	}
	if !cycleEnd.Valid {
		return floor
	}
	if observed := cycleEnd.Time.UTC(); observed.Before(floor) {
		return observed
	}
	return floor
}
