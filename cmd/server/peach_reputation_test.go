package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dexunifiedapi/internal/sellerrating"
	"dexunifiedapi/internal/testdb"
)

func TestMapPeachReputation(t *testing.T) {
	t.Parallel()

	// Established maker, perfect rating, many ratings → high & verified.
	// (1.0*100 + 0.50*20)/120 = 0.9167 → 91.67.
	var good peachUserReputation
	good.Rating = 1.0
	good.RatingCount = 100
	s := mapPeachReputation(good)
	if s.Value == nil || *s.Value < 90 || *s.Value > 94 {
		t.Fatalf("good maker Value = %v, want ~91.67", s.Value)
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

	// No ratings yet (sentinel -1 / count 0) → unavailable, not a prior-only 85.
	var fresh peachUserReputation
	fresh.Rating = -1
	fresh.RatingCount = 0
	sf := mapPeachReputation(fresh)
	if sf.Value != nil || sf.Confidence != sellerrating.ConfidenceUnavailable {
		t.Fatalf("fresh maker value=%v conf=%q, want nil/unavailable", sf.Value, sf.Confidence)
	}

	// Sentinel -1 with a non-zero count must not be treated as a 0.0 rating.
	var sentinel peachUserReputation
	sentinel.Rating = -1
	sentinel.RatingCount = 3
	ss := mapPeachReputation(sentinel)
	if ss.Value != nil || ss.Confidence != sellerrating.ConfidenceUnavailable {
		t.Fatalf("sentinel value=%v conf=%q, want nil/unavailable", ss.Value, ss.Confidence)
	}
}

func TestPeachMakersWithID(t *testing.T) {
	t.Parallel()
	outOffers := []liveOutOffer{
		{ID: 1, Exchange: "peachbitcoin", NetPremiumPct: 2.5}, // profitable, has maker
		{ID: 2, Exchange: "peachbitcoin", NetPremiumPct: -1},  // not profitable, still scored
		{ID: 3, Exchange: "peachbitcoin", NetPremiumPct: 5},   // profitable, no maker id
		{ID: 4, Exchange: "hodlhodl", NetPremiumPct: 9},       // not Peach
	}
	makerByID := map[int64]string{1: "m1", 2: "m2", 3: "", 4: "m4"}

	got := peachMakersWithID(outOffers, makerByID)
	if len(got) != 2 || got[1] != "m1" || got[2] != "m2" {
		t.Fatalf("got %v, want {1:m1, 2:m2}", got)
	}
}

func TestPickUncachedFetchQueue(t *testing.T) {
	t.Parallel()
	cached := map[string]bool{"cached": true}
	isCached := func(id string) bool { return cached[id] }

	got := pickUncachedFetchQueue([]string{"cached", "a", "a", "", "b", "c", "d"}, isCached, 2)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v, want [a b]", got)
	}

	none := pickUncachedFetchQueue([]string{"cached", "cached"}, isCached, 15)
	if len(none) != 0 {
		t.Fatalf("got %v, want empty (all cached)", none)
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

	rep := peachUserReputation{
		Rating: 0.9, RatingCount: 40, Trades: 100,
		CreationDate: "2023-03-01T13:39:55.942Z",
		UserRating:   1, PeachRating: 0.9, RecentRating: 0.91, HistoryRating: 0.95,
		Medals: []string{"superTrader"},
	}
	rep.Disputes.Opened = 4
	rep.Disputes.Won = 1
	rep.Disputes.Lost = 1
	score := mapPeachReputation(rep)
	signals := mapPeachSignals(rep, nil, time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC))
	persistPeachRatings(ctx, map[int64]peachMakerRating{id: {score: score, signals: signals}})

	var gotScore *float64
	var gotConf *string
	var gotRaw *string
	var gotSignals *string
	if err := db.QueryRow(`SELECT seller_rating_score, seller_rating_confidence, seller_rating_raw::text, seller_rating_signals::text
		FROM offers WHERE id = ?`, id).Scan(&gotScore, &gotConf, &gotRaw, &gotSignals); err != nil {
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
	if gotSignals == nil {
		t.Fatal("seller_rating_signals = NULL, want a JSON blob")
	}
}

func TestMapPeachSignals(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	var r peachUserReputation
	r.CreationDate = "2023-03-01T13:39:55.942Z"
	r.Trades = 1738
	r.RatingCount = 45
	r.UserRating = 1
	r.PeachRating = 0.924
	r.RecentRating = 0.926
	r.HistoryRating = 0.975
	r.Medals = []string{"superTrader", "ambassador"}
	r.Disputes.Opened = 4
	r.Disputes.Won = 1
	r.Disputes.Lost = 1

	raw := mapPeachSignals(r, nil, now)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal signals: %v", err)
	}
	if got["source"] != "peach" {
		t.Fatalf("source = %v, want peach", got["source"])
	}
	if got["trades"] != float64(1738) {
		t.Fatalf("trades = %v, want 1738", got["trades"])
	}
	created, ok := parsePeachCreationDate(r.CreationDate)
	if !ok {
		t.Fatal("creation date should parse")
	}
	wantDays := int(now.Sub(created).Hours() / 24)
	if got["account_age_days"] != float64(wantDays) {
		t.Fatalf("account_age_days = %v, want %d", got["account_age_days"], wantDays)
	}
	if got["disputes_lost"] != float64(1) {
		t.Fatalf("disputes_lost = %v, want 1", got["disputes_lost"])
	}
}

func TestSummarizePeachRatings(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ms := func(t time.Time) int64 { return t.UnixMilli() }
	ratings := []peachRating{
		// Dated before the account existed: inconsistent data.
		{Timestamp: ms(created.Add(-time.Hour)), Rating: 1, RatedBy: "a"},
		// Burst: three ratings inside 24h (the window is half-open, so the
		// fourth at +72h starts a new one).
		{Timestamp: ms(created.Add(48 * time.Hour)), Rating: 1, RatedBy: "a"},
		{Timestamp: ms(created.Add(60 * time.Hour)), Rating: 1, RatedBy: "a"},
		{Timestamp: ms(created.Add(71 * time.Hour)), Rating: -1, RatedBy: "b"},
		{Timestamp: ms(created.Add(72 * time.Hour)), Rating: 1, RatedBy: "c"},
		// No timestamp: falls back to creationDate.
		{CreationDate: "2026-02-01T00:00:00.000Z", Rating: 1, RatedBy: "c"},
		// Unparsable date and no rater: counted as fetched only.
		{CreationDate: "garbage", Rating: 1},
	}
	got := summarizePeachRatings(ratings, created, true)
	if got.Fetched != 7 {
		t.Fatalf("Fetched = %d, want 7", got.Fetched)
	}
	if got.DistinctRaters != 3 || got.TopRaterRatings != 3 {
		t.Fatalf("raters = %d distinct, top %d; want 3 and 3", got.DistinctRaters, got.TopRaterRatings)
	}
	if got.MaxIn24h != 3 {
		t.Fatalf("MaxIn24h = %d, want 3", got.MaxIn24h)
	}
	if got.BeforeAccountCreation != 1 {
		t.Fatalf("BeforeAccountCreation = %d, want 1", got.BeforeAccountCreation)
	}
	if !got.FirstAt.Equal(created.Add(-time.Hour)) || !got.LastAt.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("first/last = %v/%v", got.FirstAt, got.LastAt)
	}

	if none := summarizePeachRatings(nil, created, true); none.Fetched != 0 || none.MaxIn24h != 0 || !none.FirstAt.IsZero() {
		t.Fatalf("empty summary = %+v, want zero value", none)
	}
	if noCreated := summarizePeachRatings(ratings, time.Time{}, false); noCreated.BeforeAccountCreation != 0 {
		t.Fatalf("BeforeAccountCreation without creation date = %d, want 0", noCreated.BeforeAccountCreation)
	}
}

func TestMapPeachSignalsWithRatings(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	stats := &peachRatingStats{
		Fetched: 55, DistinctRaters: 35, TopRaterRatings: 7, MaxIn24h: 20,
		FirstAt: time.Date(2026, 11, 9, 13, 7, 49, 0, time.UTC),
		LastAt:  time.Date(2026, 11, 14, 20, 20, 4, 0, time.UTC),
	}
	var got map[string]any
	if err := json.Unmarshal(mapPeachSignals(peachUserReputation{RatingCount: 55}, stats, now), &got); err != nil {
		t.Fatalf("unmarshal signals: %v", err)
	}
	want := map[string]any{
		"ratings_fetched":                 float64(55),
		"distinct_raters":                 float64(35),
		"top_rater_ratings":               float64(7),
		"top_rater_share":                 0.127,
		"max_ratings_24h":                 float64(20),
		"ratings_before_account_creation": float64(0),
		"first_rating_at":                 "2026-11-09T13:07:49Z",
		"last_rating_at":                  "2026-11-14T20:20:04Z",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, want %v", k, got[k], v)
		}
	}

	var without map[string]any
	if err := json.Unmarshal(mapPeachSignals(peachUserReputation{}, nil, now), &without); err != nil {
		t.Fatalf("unmarshal signals: %v", err)
	}
	if _, ok := without["distinct_raters"]; ok {
		t.Fatal("distinct_raters present without a ratings fetch")
	}
}

func TestFetchPeachUserRatings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/user/02abc/ratings" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"creationDate":"2026-09-22T13:41:44.534Z","timestamp":1790084504534,"rating":1,"ratedBy":"03ff","signature":"8e02"}]`))
	}))
	defer srv.Close()
	prev := peachAPIBaseURL
	peachAPIBaseURL = srv.URL
	defer func() { peachAPIBaseURL = prev }()

	got, err := fetchPeachUserRatings(context.Background(), "02abc")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 1 || got[0].RatedBy != "03ff" || got[0].Rating != 1 || got[0].Timestamp != 1790084504534 {
		t.Fatalf("ratings = %+v", got)
	}
	if _, err := fetchPeachUserRatings(context.Background(), "unknown"); err == nil {
		t.Fatal("want error for HTTP 404")
	}
}
