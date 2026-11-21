package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRobosatsEmptyBookMemoBacksOffAfterRepeatedEmptyAnswers(t *testing.T) {
	memo := newRobosatsEmptyBookMemo("", 30*time.Minute)
	key := robosatsEmptyBookKey("http://a.onion", 2, 1)
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	memo.recordEmpty(key, t0)
	if memo.shouldSkip(key, t0.Add(time.Second)) {
		t.Fatal("skipped after a single empty answer; one empty response may be a blip")
	}

	memo.recordEmpty(key, t0)
	if !memo.shouldSkip(key, t0.Add(9*time.Minute)) {
		t.Fatal("not skipped 9m after the second consecutive empty answer")
	}
	if memo.shouldSkip(key, t0.Add(10*time.Minute)) {
		t.Fatal("still skipped once the 10m back-off elapsed")
	}

	for i := 0; i < 10; i++ {
		memo.recordEmpty(key, t0)
	}
	if !memo.shouldSkip(key, t0.Add(29*time.Minute)) || memo.shouldSkip(key, t0.Add(30*time.Minute)) {
		t.Fatal("back-off not capped at maxSkip (30m)")
	}

	memo.forget(key)
	if memo.shouldSkip(key, t0.Add(time.Second)) {
		t.Fatal("still skipped after forget")
	}
}

func TestRobosatsEmptyBookMemoDisabledWithZeroMaxSkip(t *testing.T) {
	memo := newRobosatsEmptyBookMemo("", 0)
	key := robosatsEmptyBookKey("http://a.onion", 2, 1)
	now := time.Now()
	for i := 0; i < 5; i++ {
		memo.recordEmpty(key, now)
	}
	if memo.shouldSkip(key, now) {
		t.Fatal("skipped with ROBOSATS_EMPTY_BOOK_MAX_SKIP=0")
	}
}

func TestRobosatsEmptyBookMemoPersistsAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty-books.json")
	now := time.Now().UTC()
	fresh := robosatsEmptyBookKey("http://fresh.onion", 2, 0)
	stale := robosatsEmptyBookKey("http://stale.onion", 2, 0)

	first := newRobosatsEmptyBookMemo(path, 30*time.Minute)
	first.recordEmpty(fresh, now)
	first.recordEmpty(fresh, now)
	first.recordEmpty(stale, now.Add(-25*time.Hour))
	first.recordEmpty(stale, now.Add(-25*time.Hour))
	first.save()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("memo file = %v, %v; want 0600 file", info, err)
	}

	// The worker is one process per cycle: a new memo must pick up the file.
	second := newRobosatsEmptyBookMemo(path, 30*time.Minute)
	if !second.shouldSkip(fresh, now.Add(time.Minute)) {
		t.Fatal("persisted empty book not skipped by the next run")
	}
	second.mu.Lock()
	_, kept := second.books[stale]
	second.mu.Unlock()
	if kept {
		t.Fatal("entry not probed for over a day was not forgotten")
	}
}

// After two consecutive not_found answers a coordinator book is skipped, and
// the market stays a complete census; a coordinator that errors is never
// skipped, so outages stay visible as incomplete fetches.
func TestFetchRobosatsSkipsRepeatedlyEmptyBooks(t *testing.T) {
	var emptyHits, bookHits, brokenHits atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "empty.test":
			emptyHits.Add(1)
			return jsonResponse(http.StatusNotFound, `{"not_found":"No orders found, be the first to make one"}`), nil
		case "broken.test":
			brokenHits.Add(1)
			return jsonResponse(http.StatusBadGateway, `{"detail":"upstream unavailable"}`), nil
		default:
			bookHits.Add(1)
			return jsonResponse(http.StatusOK, `[{"id": 7, "price": 61000, "premium": 0, "amount": 500,
				"min_amount": 0, "max_amount": 0, "payment_method": "SEPA", "bond_size": 3}]`), nil
		}
	})}
	coordinators := []robosatsCoordinator{{Alias: "empty", Onion: "http://empty.test"}, {Alias: "lake", Onion: "http://book.test"}}
	memo := newRobosatsEmptyBookMemo("", 30*time.Minute)

	for run := 1; run <= 3; run++ {
		result := fetchRobosatsCurrencyWithMemo(context.Background(), "ask", "EUR", 61000, client, coordinators, memo)
		if result == nil || result.status() != fetchStatusComplete {
			t.Fatalf("run %d: result = %#v, want complete census", run, result)
		}
		if len(result.Offers) != 1 || result.Offers[0].OfferID != "lake:7" {
			t.Fatalf("run %d: offers = %#v, want lake:7", run, result.Offers)
		}
	}
	if got := emptyHits.Load(); got != 2 {
		t.Fatalf("empty book probed %d times over 3 runs, want 2 (skipped on run 3)", got)
	}
	if got := bookHits.Load(); got != 3 {
		t.Fatalf("real book probed %d times, want every run", got)
	}

	broken := []robosatsCoordinator{{Alias: "broken", Onion: "http://broken.test"}, {Alias: "lake", Onion: "http://book.test"}}
	for run := 1; run <= 3; run++ {
		result := fetchRobosatsCurrencyWithMemo(context.Background(), "bid", "EUR", 61000, client, broken, memo)
		if result == nil || result.status() != fetchStatusIncomplete {
			t.Fatalf("broken run %d: result = %#v, want incomplete", run, result)
		}
	}
	if got := brokenHits.Load(); got != 3 {
		t.Fatalf("failing coordinator probed %d times, want every run", got)
	}
}
