package sellerrating

import (
	"encoding/json"
	"testing"
	"time"
)

// fptr returns a *float64 for a literal, for terse table entries.
func fptr(v float64) *float64 { return &v }

// wantValue asserts a Score's Value matches want (nil-aware) within a small
// epsilon, and returns for chaining further assertions.
func wantValue(t *testing.T, name string, s Score, want *float64) {
	t.Helper()
	switch {
	case want == nil && s.Value != nil:
		t.Fatalf("%s: Value = %v, want nil", name, *s.Value)
	case want != nil && s.Value == nil:
		t.Fatalf("%s: Value = nil, want %v", name, *want)
	case want != nil && s.Value != nil:
		if diff := *s.Value - *want; diff > 0.01 || diff < -0.01 {
			t.Fatalf("%s: Value = %v, want %v", name, *s.Value, *want)
		}
	}
}

func TestUnavailable(t *testing.T) {
	s := Unavailable()
	wantValue(t, "Unavailable", s, nil)
	if s.Confidence != ConfidenceUnavailable {
		t.Fatalf("Confidence = %q, want %q", s.Confidence, ConfidenceUnavailable)
	}
	if s.Raw != nil {
		t.Fatalf("Raw = %v, want nil", s.Raw)
	}
}

func TestNormalizeFeedbackRating(t *testing.T) {
	priorMean := DefaultFeedbackPriorMean
	priorWeight := DefaultFeedbackPriorWeight
	threshold := DefaultFeedbackVerifiedThreshold
	tests := []struct {
		name      string
		rating    *float64
		sampleN   int
		wantValue *float64
		wantConf  Confidence
	}{
		// No sample at all: unavailable, not a prior-only 50/85.
		{"no ratings yet (nil)", nil, 0, nil, ConfidenceUnavailable},
		{"zero samples with a rating pointer", fptr(1.0), 0, nil, ConfidenceUnavailable},
		// A single lucky perfect rating is pulled toward the conservative prior, NOT ~100:
		// (1.0*1 + 0.50*20) / 21 = 0.5238... -> 52.38.
		{"one perfect trade stays near prior", fptr(1.0), 1, fptr(52.38), ConfidenceFeedbackNew},
		// Below threshold: still "new". (1.0*4 + 0.50*20)/24 = 0.5833 -> 58.33.
		{"four good trades below threshold", fptr(1.0), 4, fptr(58.33), ConfidenceFeedbackNew},
		// At threshold: flips to verified. (0.95*20 + 0.50*20)/40 = 0.725 -> 72.50.
		{"twenty trades reaches verified", fptr(0.95), 20, fptr(72.50), ConfidenceFeedbackVerified},
		// Large sample dominates the prior: (0.6*200 + 0.50*20)/220 = 0.5909 -> 59.09.
		{"large sample tracks the real rating", fptr(0.6), 200, fptr(59.09), ConfidenceFeedbackVerified},
		// A genuinely bad rating with enough samples scores low, not near the prior.
		// (0.1*100 + 0.50*20)/120 = 0.1667 -> 16.67.
		{"bad rating verified", fptr(0.1), 100, fptr(16.67), ConfidenceFeedbackVerified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NormalizeFeedbackRating(tt.rating, tt.sampleN, priorMean, priorWeight, threshold, nil)
			wantValue(t, tt.name, s, tt.wantValue)
			if s.Confidence != tt.wantConf {
				t.Fatalf("Confidence = %q, want %q", s.Confidence, tt.wantConf)
			}
		})
	}
}

func TestNormalizeStakeBond(t *testing.T) {
	tests := []struct {
		name    string
		bondPct float64
		want    *float64
	}{
		{"at min -> 0", 2, fptr(0)},
		{"at max -> 100", 15, fptr(100)},
		{"midpoint", 8.5, fptr(50)},
		{"below min clamps to 0", 1, fptr(0)},
		{"above max clamps to 100", 30, fptr(100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NormalizeStakeBond(tt.bondPct, 2, 15, nil)
			wantValue(t, tt.name, s, tt.want)
			if s.Confidence != ConfidenceStakeBonded {
				t.Fatalf("Confidence = %q, want %q", s.Confidence, ConfidenceStakeBonded)
			}
		})
	}

	// Degenerate range must not divide by zero.
	if s := NormalizeStakeBond(5, 10, 10, nil); s.Value == nil {
		t.Fatal("degenerate range: Value = nil, want a number")
	}
}

func TestNormalizeBisq2TotalScore(t *testing.T) {
	tests := []struct {
		name      string
		total     int64
		wantValue *float64
		wantConf  Confidence
	}{
		{"zero stake is unavailable, not score 0", 0, nil, ConfidenceUnavailable},
		{"negative is unavailable", -1, nil, ConfidenceUnavailable},
		{"dust below 1200", 1, fptr(20), ConfidenceReputationBonded},
		{"just below 1200", 1199, fptr(20), ConfidenceReputationBonded},
		{"1200 bucket", 1200, fptr(35), ConfidenceReputationBonded},
		{"4999 still 35", 4999, fptr(35), ConfidenceReputationBonded},
		{"5000 bucket", 5000, fptr(50), ConfidenceReputationBonded},
		{"15000 bucket", 15000, fptr(62), ConfidenceReputationBonded},
		{"25000 bucket", 25000, fptr(72), ConfidenceReputationBonded},
		{"40000 bucket", 40000, fptr(82), ConfidenceReputationBonded},
		{"80000 bucket", 80000, fptr(92), ConfidenceReputationBonded},
		{"199999 still 92", 199999, fptr(92), ConfidenceReputationBonded},
		{"200000 caps at 100", 200000, fptr(100), ConfidenceReputationBonded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := json.RawMessage(`{"total_score":1}`)
			s := NormalizeBisq2TotalScore(tt.total, raw)
			wantValue(t, tt.name, s, tt.wantValue)
			if s.Confidence != tt.wantConf {
				t.Fatalf("Confidence = %q, want %q", s.Confidence, tt.wantConf)
			}
			if string(s.Raw) != string(raw) {
				t.Fatalf("Raw = %s, want passthrough", s.Raw)
			}
		})
	}
}

func TestNormalizeReputationScore(t *testing.T) {
	tests := []struct {
		name  string
		stars float64
		want  *float64
	}{
		{"zero stars is an honest 0", 0, fptr(0)},
		{"half a star", 0.5, fptr(10)},
		{"three stars", 3, fptr(60)},
		{"max stars -> 100", 5, fptr(100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NormalizeReputationScore(tt.stars, 5, nil)
			wantValue(t, tt.name, s, tt.want)
			if s.Confidence != ConfidenceReputationBonded {
				t.Fatalf("Confidence = %q, want %q", s.Confidence, ConfidenceReputationBonded)
			}
		})
	}
}

func TestApplyDisputePenalty(t *testing.T) {
	base := func() Score { v := 80.0; return Score{Value: &v, Confidence: ConfidenceFeedbackVerified} }

	// Zero disputes is a no-op.
	if s := ApplyDisputePenalty(base(), 0, 5); *s.Value != 80 {
		t.Fatalf("zero disputes: Value = %v, want 80", *s.Value)
	}
	// N disputes subtract N*pointsPerLoss.
	if s := ApplyDisputePenalty(base(), 3, 5); *s.Value != 65 {
		t.Fatalf("three disputes: Value = %v, want 65", *s.Value)
	}
	// Floors at 0, never negative.
	if s := ApplyDisputePenalty(base(), 100, 5); *s.Value != 0 {
		t.Fatalf("many disputes: Value = %v, want 0", *s.Value)
	}
	// No-op when Value is nil (Unavailable path) rather than panicking.
	if s := ApplyDisputePenalty(Unavailable(), 3, 5); s.Value != nil {
		t.Fatalf("nil value: Value = %v, want nil", *s.Value)
	}
}

func TestNormalizeBisqAccountSigning(t *testing.T) {
	now := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	// daysAgo returns the witness-creation time in epoch millis for an account
	// whose account-age witness was created d days before now.
	daysAgo := func(d float64) int64 {
		return now.Add(-time.Duration(d * 24 * float64(time.Hour))).UnixMilli()
	}

	tests := []struct {
		name    string
		state   string
		witness int64
		want    *float64
	}{
		// BANNED is a hard floor: no age bonus, even for an ancient account.
		{"banned floors at 0 regardless of age", "BANNED", daysAgo(1000), fptr(0)},
		// No witness (-1) is treated as zero age -> just the band.
		{"unsigned no witness", "UNSIGNED", -1, fptr(20)},
		{"peer initial fresh", "PEER_INITIAL", daysAgo(0), fptr(45)},
		{"peer limit lifted fresh", "PEER_LIMIT_LIFTED", daysAgo(0), fptr(60)},
		{"peer signer fresh", "PEER_SIGNER", daysAgo(0), fptr(80)},
		{"arbitrator fresh", "ARBITRATOR", daysAgo(0), fptr(90)},
		// Age bonus: 10 * min(ageDays,365)/365. 73d -> +2, 365d -> +10.
		{"peer signer 73d adds +2", "PEER_SIGNER", daysAgo(73), fptr(82)},
		{"peer signer full year adds +10", "PEER_SIGNER", daysAgo(365), fptr(90)},
		{"arbitrator full year caps at 100", "ARBITRATOR", daysAgo(365), fptr(100)},
		{"arbitrator very old still caps at 100", "ARBITRATOR", daysAgo(1000), fptr(100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NormalizeBisqAccountSigning(tt.state, tt.witness, now)
			wantValue(t, tt.name, s, tt.want)
			if s.Confidence != ConfidenceAccountSigning {
				t.Fatalf("Confidence = %q, want %q", s.Confidence, ConfidenceAccountSigning)
			}
			if s.Raw == nil {
				t.Fatalf("Raw = nil, want sign-state json")
			}
		})
	}

	// Empty or unrecognized sign-state (e.g. an unpatched daemon sending "")
	// degrades to Unavailable rather than scoring.
	for _, bad := range []string{"", "WAT", "signed"} {
		s := NormalizeBisqAccountSigning(bad, 123, now)
		if s.Value != nil || s.Confidence != ConfidenceUnavailable {
			t.Fatalf("state %q: got value=%v conf=%q, want nil/unavailable", bad, s.Value, s.Confidence)
		}
	}
}

// TestValueAlwaysInRange asserts the DB CHECK invariant (0-100 or nil) holds
// across a spread of inputs before any Score ever reaches Postgres.
func TestValueAlwaysInRange(t *testing.T) {
	scores := []Score{
		Unavailable(),
		NormalizeFeedbackRating(nil, 0, DefaultFeedbackPriorMean, DefaultFeedbackPriorWeight, DefaultFeedbackVerifiedThreshold, nil),
		NormalizeFeedbackRating(fptr(1.0), 1000, DefaultFeedbackPriorMean, DefaultFeedbackPriorWeight, DefaultFeedbackVerifiedThreshold, nil),
		NormalizeFeedbackRating(fptr(0.0), 1000, DefaultFeedbackPriorMean, DefaultFeedbackPriorWeight, DefaultFeedbackVerifiedThreshold, nil),
		NormalizeBisq2TotalScore(0, nil),
		NormalizeBisq2TotalScore(200000, nil),
		NormalizeStakeBond(-5, 2, 15, nil),
		NormalizeStakeBond(99, 2, 15, nil),
		NormalizeReputationScore(9, 5, nil),
		NormalizeReputationScore(-1, 5, nil),
		ApplyDisputePenalty(NormalizeReputationScore(5, 5, nil), 1000, 5),
		NormalizeBisqAccountSigning("ARBITRATOR", time.Now().Add(-5000*24*time.Hour).UnixMilli(), time.Now()),
		NormalizeBisqAccountSigning("BANNED", time.Now().UnixMilli(), time.Now()),
	}
	for i, s := range scores {
		if s.Value == nil {
			continue
		}
		if *s.Value < 0 || *s.Value > 100 {
			t.Fatalf("score[%d]: Value = %v out of [0,100]", i, *s.Value)
		}
	}
}

// TestRawPassthrough confirms the Raw blob is carried through verbatim.
func TestRawPassthrough(t *testing.T) {
	raw := json.RawMessage(`{"rating":0.9,"trades":42}`)
	s := NormalizeFeedbackRating(fptr(0.9), 42, DefaultFeedbackPriorMean, DefaultFeedbackPriorWeight, DefaultFeedbackVerifiedThreshold, raw)
	if string(s.Raw) != string(raw) {
		t.Fatalf("Raw = %s, want %s", s.Raw, raw)
	}
	// ApplyDisputePenalty must not clobber Raw.
	s2 := ApplyDisputePenalty(s, 1, 5)
	if string(s2.Raw) != string(raw) {
		t.Fatalf("after penalty Raw = %s, want %s", s2.Raw, raw)
	}
}
