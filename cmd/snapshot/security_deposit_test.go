package main

import (
	"encoding/json"
	"math"
	"testing"
)

func TestSecurityDepositGuardsZeroAmountOffer(t *testing.T) {
	// A zero-amount offer must not produce a deposit: an unguarded division
	// would yield +Inf, and json.Marshal(+Inf) fails — which failed the whole
	// live-offer push, not just this offer.
	btc, pct := securityDeposit(500000, 0)
	if btc != nil || pct != nil {
		t.Fatalf("zero btcMax must yield nil deposit, got btc=%v pct=%v", btc, pct)
	}

	// The push payload must always be marshalable even for a zero-amount offer.
	if _, err := json.Marshal([]livePayload{{SecurityDepositBTC: btc, SecurityDepositPct: pct}}); err != nil {
		t.Fatalf("payload with guarded (nil) deposit must marshal: %v", err)
	}

	// Sanity: a real amount yields a finite fraction.
	btc, pct = securityDeposit(1000000, 0.1) // 0.01 BTC deposit on a 0.1 BTC trade
	if btc == nil || pct == nil {
		t.Fatal("expected a deposit for a positive amount")
	}
	if math.Abs(*pct-0.1) > 1e-9 {
		t.Fatalf("deposit pct = %v, want 0.1", *pct)
	}
}
