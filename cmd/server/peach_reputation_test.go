package main

import (
	"context"
	"testing"

	"dexunifiedapi/internal/sellerrating"
	"dexunifiedapi/internal/testdb"
)

func TestMapPeachReputation(t *testing.T) {
	t.Parallel()

	// Established maker, perfect rating, many ratings → high & verified.
	var good peachUserReputation
	good.Rating = 1.0
	good.RatingCount = 100
	s := mapPeachReputation(good)
	if s.Value == nil || *s.Value < 95 || *s.Value > 100 {
		t.Fatalf("good maker Value = %v, want ~98", s.Value)
	}
	if s.Confidence != sellerrating.ConfidenceFeedbackVerified {
		t.Fatalf("Confidence = %q, want feedback_verified", s.Confidence)
	}

	// Lost disputes apply a bounded penalty below the un-penalized score.
	withDisputes := good
	withDisputes.Disputes.Lost = 3
	sd := mapPeachReputation(withDisputes)
	if sd.Value == nil || *sd.Value >= *s.Value {
		t.Fatalf("dispute penalty not applied: %v vs %v", sd.Value, s.Value)
	}

	// No ratings yet → prior dominates, tagged new (rating value ignored at count 0).
	var fresh peachUserReputation
	fresh.Rating = -1
	fresh.RatingCount = 0
	sf := mapPeachReputation(fresh)
	if sf.Value == nil || *sf.Value < 80 || *sf.Value > 90 {
		t.Fatalf("fresh maker Value = %v, want ~85 (prior)", sf.Value)
	}
	if sf.Confidence != sellerrating.ConfidenceFeedbackNew {
		t.Fatalf("Confidence = %q, want feedback_new", sf.Confidence)
	}
}

func TestProfitablePeachMakers(t *testing.T) {
	t.Parallel()
	outOffers := []liveOutOffer{
		{ID: 1, Exchange: "peachbitcoin", NetPremiumPct: 2.5}, // profitable, has maker
		{ID: 2, Exchange: "peachbitcoin", NetPremiumPct: -1},  // not profitable
		{ID: 3, Exchange: "peachbitcoin", NetPremiumPct: 5},   // profitable, no maker id
		{ID: 4, Exchange: "hodlhodl", NetPremiumPct: 9},       // not Peach
	}
	makerByID := map[int64]string{1: "m1", 3: "", 4: "m4"}

	got := profitablePeachMakers(outOffers, makerByID)
	if len(got) != 1 || got[1] != "m1" {
		t.Fatalf("got %v, want {1:m1}", got)
	}
}

func TestPersistPeachRatings(t *testing.T) {
	prev := db
	db = testdb.Open(t)
	defer func() { db = prev }()
	ctx := context.Background()

	var id int64
	if err := db.QueryRow(`INSERT INTO offers (exchange, fiat_currency, direction, price)
		VALUES (?, ?, ?, ?) RETURNING id`, "peachbitcoin", "EUR", "ask", 61000).Scan(&id); err != nil {
		t.Fatalf("insert offer: %v", err)
	}

	score := mapPeachReputation(peachUserReputation{Rating: 0.9, RatingCount: 40})
	persistPeachRatings(ctx, map[int64]sellerrating.Score{id: score})

	var gotScore *float64
	var gotConf *string
	var gotRaw *string
	if err := db.QueryRow(`SELECT seller_rating_score, seller_rating_confidence, seller_rating_raw::text
		FROM offers WHERE id = ?`, id).Scan(&gotScore, &gotConf, &gotRaw); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if gotScore == nil || *gotScore != *score.Value {
		t.Fatalf("seller_rating_score = %v, want %v", gotScore, *score.Value)
	}
	if gotConf == nil || *gotConf != string(score.Confidence) {
		t.Fatalf("seller_rating_confidence = %v, want %q", gotConf, score.Confidence)
	}
	if gotRaw == nil {
		t.Fatal("seller_rating_raw = NULL, want a JSON blob")
	}
}
