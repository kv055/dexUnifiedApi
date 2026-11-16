package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dexunifiedapi/internal/testdb"
)

// hodlHodlReviewPageHTML mirrors the live markup of hodlhodl.com/reviews
// (2026-11-15): one div.review per review with a div.timestamp, newest first,
// pagination links with &amp;-escaped query strings.
func hodlHodlReviewPageHTML(login string, timestamps []string, pageLinks ...int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<html><head><title>All reviews for %s | Hodl Hodl</title></head><body>", login)
	fmt.Fprintf(&b, "<h1>All reviews for %s (rating: 100%%)</h1>\n", login)
	for _, ts := range timestamps {
		fmt.Fprintf(&b, "<div class='review'>\n<div class='credits'>left by <a href=\"/users/x\">x</a></div>\n<div class='timestamp'>true %s UTC</div>\n</div>\n", ts)
	}
	for _, p := range pageLinks {
		fmt.Fprintf(&b, "<a href=\"/reviews?page=%d&amp;user=%s\">%d</a>", p, login, p)
	}
	b.WriteString("<footer>Hodlex Ltd © 2016-2026</footer></body></html>")
	return b.String()
}

func TestParseHodlHodlReviewPage(t *testing.T) {
	t.Parallel()
	page := hodlHodlReviewPageHTML("manex", []string{"2026-11-15 14:12:22", "2026-10-16 16:20:17"}, 2, 3, 67)
	times, last, err := parseHodlHodlReviewPage(page)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(times) != 2 || !times[1].Equal(time.Date(2026, 10, 16, 16, 20, 17, 0, time.UTC)) {
		t.Fatalf("times = %v", times)
	}
	if last != 67 {
		t.Fatalf("last page = %d, want 67", last)
	}

	_, last, err = parseHodlHodlReviewPage(hodlHodlReviewPageHTML("new", nil))
	if err != nil || last != 1 {
		t.Fatalf("no reviews: last = %d, err = %v; want 1, nil", last, err)
	}

	if _, _, err := parseHodlHodlReviewPage("<html>maintenance</html>"); err == nil {
		t.Fatal("want layout error for a page without the review list marker")
	}
}

func TestFetchHodlHodlReviewStats(t *testing.T) {
	pages := map[string]string{
		// Paginated: page 1 full (3 per page in this fixture), last page 3.
		"big|":  hodlHodlReviewPageHTML("big", []string{"2026-11-15 10:00:00", "2026-11-14 10:00:00", "2026-11-13 10:00:00"}, 2, 3),
		"big|3": hodlHodlReviewPageHTML("big", []string{"2025-01-11 21:31:16", "2024-11-28 22:22:25"}),
		"one|":  hodlHodlReviewPageHTML("one", []string{"2026-11-01 08:00:00", "2026-10-16 16:20:17"}),
		"none|": hodlHodlReviewPageHTML("none", nil),
		"gap|":  hodlHodlReviewPageHTML("gap", []string{"2026-11-15 10:00:00"}, 2),
		"gap|2": hodlHodlReviewPageHTML("gap", nil),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Query().Get("user")+"|"+r.URL.Query().Get("page")]
		if r.URL.Path != "/reviews" || !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	prev := hodlHodlWebBaseURL
	hodlHodlWebBaseURL = srv.URL
	defer func() { hodlHodlWebBaseURL = prev }()
	ctx := context.Background()

	big, err := fetchHodlHodlReviewStats(ctx, "big")
	if err != nil {
		t.Fatalf("big: %v", err)
	}
	if big.ReviewPages != 3 || big.ReviewsCount != 8 || !big.OldestReviewAt.Equal(time.Date(2024, 11, 28, 22, 22, 25, 0, time.UTC)) {
		t.Fatalf("big = %+v, want 3 pages, 8 reviews, oldest 2024-11-28 22:22:25", big)
	}

	one, err := fetchHodlHodlReviewStats(ctx, "one")
	if err != nil {
		t.Fatalf("one: %v", err)
	}
	if one.ReviewPages != 1 || one.ReviewsCount != 2 || !one.OldestReviewAt.Equal(time.Date(2026, 10, 16, 16, 20, 17, 0, time.UTC)) {
		t.Fatalf("one = %+v", one)
	}

	none, err := fetchHodlHodlReviewStats(ctx, "none")
	if err != nil || none.ReviewsCount != 0 || !none.OldestReviewAt.IsZero() {
		t.Fatalf("none = %+v, err = %v; want zero reviews", none, err)
	}

	if _, err := fetchHodlHodlReviewStats(ctx, "gap"); err == nil {
		t.Fatal("want error when the linked last page has no reviews")
	}
	if _, err := fetchHodlHodlReviewStats(ctx, "unknown"); err == nil {
		t.Fatal("want error for HTTP 404")
	}
}

func TestHodlHodlReviewSignals(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 15, 16, 0, 0, 0, time.UTC)
	got := hodlHodlReviewSignals(hodlHodlReviewStats{
		OldestReviewAt: time.Date(2026, 10, 16, 16, 20, 17, 0, time.UTC),
		ReviewPages:    1,
		ReviewsCount:   16,
	}, now)
	if got["min_account_age_days"] != 29 || got["oldest_review_at"] != "2026-10-16T16:20:17Z" || got["reviews_count"] != 16 {
		t.Fatalf("signals = %v", got)
	}
	empty := hodlHodlReviewSignals(hodlHodlReviewStats{ReviewPages: 1}, now)
	if _, ok := empty["min_account_age_days"]; ok || empty["reviews_count"] != 0 {
		t.Fatalf("no-review signals = %v, want reviews_count 0 and no age", empty)
	}
}

func TestMergeSignals(t *testing.T) {
	t.Parallel()
	got := mergeSignals(json.RawMessage(`{"source":"hodlhodl","trades_count":30}`), map[string]any{"reviews_count": 16})
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["source"] != "hodlhodl" || m["trades_count"] != float64(30) || m["reviews_count"] != float64(16) {
		t.Fatalf("merged = %s", got)
	}
	for _, base := range []json.RawMessage{nil, json.RawMessage(`null`)} {
		if got := string(mergeSignals(base, map[string]any{"a": 1})); got != `{"a":1}` {
			t.Fatalf("mergeSignals(%q) = %s, want {\"a\":1}", base, got)
		}
	}
}

func TestPersistSellerSignalPatches(t *testing.T) {
	prev := db
	db = testdb.Open(t)
	defer func() { db = prev }()
	ctx := context.Background()

	var id int64
	if err := db.QueryRow(`INSERT INTO offers (exchange, fiat_currency, direction, price, seller_rating_signals)
		VALUES (?, ?, ?, ?, ?::jsonb) RETURNING id`, "hodlhodl", "EUR", "ask", 61000, `{"source":"hodlhodl","trades_count":30}`).Scan(&id); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	var bare int64
	if err := db.QueryRow(`INSERT INTO offers (exchange, fiat_currency, direction, price)
		VALUES (?, ?, ?, ?) RETURNING id`, "hodlhodl", "EUR", "ask", 62000).Scan(&bare); err != nil {
		t.Fatalf("insert bare offer: %v", err)
	}

	persistSellerSignalPatches(ctx, map[int64]map[string]any{
		id:   {"reviews_count": 16, "min_account_age_days": 29},
		bare: {"reviews_count": 0},
	})

	read := func(offerID int64) map[string]any {
		var raw string
		if err := db.QueryRow(`SELECT seller_rating_signals::text FROM offers WHERE id = ?`, offerID).Scan(&raw); err != nil {
			t.Fatalf("read back %d: %v", offerID, err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("unmarshal %d: %v", offerID, err)
		}
		return m
	}
	if m := read(id); m["source"] != "hodlhodl" || m["trades_count"] != float64(30) || m["min_account_age_days"] != float64(29) {
		t.Fatalf("merged row = %v, want snapshot keys kept and review keys added", m)
	}
	if m := read(bare); m["reviews_count"] != float64(0) {
		t.Fatalf("bare row = %v, want reviews_count 0", m)
	}
}
