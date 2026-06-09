package main

import (
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

func closedOfferAnalysisGraceCutoff(now time.Time) time.Time {
	intervalMin := envIntOrDefault("SNAPSHOT_INTERVAL_MIN", 1)
	if intervalMin < 1 {
		intervalMin = 1
	}
	graceCycles := envIntOrDefault("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", 3)
	return now.UTC().Add(-time.Duration(graceCycles*intervalMin) * time.Minute)
}
