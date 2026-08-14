package main

import (
	"testing"
	"time"
)

func TestLiveOfferStaleGrace(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("LIVE_OFFER_STALE_GRACE", "")
		if got := liveOfferStaleGrace(); got != 5*time.Minute {
			t.Fatalf("grace = %s, want 5m", got)
		}
	})

	t.Run("configured", func(t *testing.T) {
		t.Setenv("LIVE_OFFER_STALE_GRACE", "90s")
		if got := liveOfferStaleGrace(); got != 90*time.Second {
			t.Fatalf("grace = %s, want 90s", got)
		}
	})

	t.Run("invalid falls back", func(t *testing.T) {
		t.Setenv("LIVE_OFFER_STALE_GRACE", "0")
		if got := liveOfferStaleGrace(); got != defaultLiveOfferStaleGrace {
			t.Fatalf("grace = %s, want default %s", got, defaultLiveOfferStaleGrace)
		}
	})
}
