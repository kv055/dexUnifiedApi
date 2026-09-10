package main

import (
	"encoding/json"
	"testing"
)

func TestMapHodlHodlSignals(t *testing.T) {
	t.Parallel()
	payload := []byte(`{
		"trades_count": 42,
		"rating": 0.98,
		"strong_hodler": true,
		"days_since_last_trade": 3,
		"average_payment_time_minutes": 15,
		"average_release_time_minutes": 20,
		"blocked_by": 1,
		"verified": true,
		"verified_by": "hodlhodl",
		"online_status": "online"
	}`)
	var trader hodlHodlTrader
	if err := json.Unmarshal(payload, &trader); err != nil {
		t.Fatalf("unmarshal trader: %v", err)
	}
	if trader.TradesCount != 42 || !trader.Verified || trader.DaysSinceLastTrade != 3 {
		t.Fatalf("trader extras not decoded: %#v", trader)
	}

	raw := mapHodlHodlSignals(trader)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal signals: %v", err)
	}
	if got["source"] != "hodlhodl" {
		t.Fatalf("source = %v, want hodlhodl", got["source"])
	}
	if got["trades_count"] != float64(42) {
		t.Fatalf("trades_count = %v, want 42", got["trades_count"])
	}
	if got["average_payment_time_minutes"] != float64(15) {
		t.Fatalf("average_payment_time_minutes = %v, want 15", got["average_payment_time_minutes"])
	}
	if got["verified_by"] != "hodlhodl" {
		t.Fatalf("verified_by = %v, want hodlhodl", got["verified_by"])
	}
	if got["online_status"] != "online" {
		t.Fatalf("online_status = %v, want online", got["online_status"])
	}
}

func TestBisq2OfficialFiveSystemScore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		total int64
		want  float64
	}{
		{0, 0},
		{1199, 0},
		{1200, 0.5},
		{4999, 0.5},
		{5000, 1},
		{39999, 3.5},
		{40000, 4},
		{99999, 4.5},
		{100000, 5},
	}
	for _, tc := range tests {
		if got := bisq2OfficialFiveSystemScore(tc.total); got != tc.want {
			t.Fatalf("bisq2OfficialFiveSystemScore(%d) = %v, want %v", tc.total, got, tc.want)
		}
	}
}

func TestMapBisq2Signals(t *testing.T) {
	t.Parallel()
	fiatMax := 150
	raw := mapBisq2Signals(bisq2Reputation{
		TotalScore:      40000,
		FiveSystemScore: 4,
		Ranking:         12,
	}, &fiatMax)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal signals: %v", err)
	}
	if got["source"] != "bisq2" {
		t.Fatalf("source = %v, want bisq2", got["source"])
	}
	if got["max_sell_usd"] != float64(200) {
		t.Fatalf("max_sell_usd = %v, want 200", got["max_sell_usd"])
	}
	if got["offer_fiat_max"] != float64(150) {
		t.Fatalf("offer_fiat_max = %v, want 150", got["offer_fiat_max"])
	}
	if got["within_reputation_limit"] != true {
		t.Fatalf("within_reputation_limit = %v, want true", got["within_reputation_limit"])
	}
	if got["five_system_matches_buckets"] != true {
		t.Fatalf("five_system_matches_buckets = %v, want true", got["five_system_matches_buckets"])
	}

	over := 250
	overRaw := mapBisq2Signals(bisq2Reputation{TotalScore: 40000, FiveSystemScore: 3, Ranking: 1}, &over)
	var overGot map[string]any
	if err := json.Unmarshal(overRaw, &overGot); err != nil {
		t.Fatalf("unmarshal over-limit signals: %v", err)
	}
	if overGot["within_reputation_limit"] != false {
		t.Fatalf("within_reputation_limit = %v, want false", overGot["within_reputation_limit"])
	}
	if overGot["five_system_matches_buckets"] != false {
		t.Fatalf("five_system_matches_buckets = %v, want false for mismatched stars", overGot["five_system_matches_buckets"])
	}
}
