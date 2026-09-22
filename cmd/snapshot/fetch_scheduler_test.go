package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildMarketFetchJobsPreservesSerialOrder(t *testing.T) {
	fetch := func(context.Context, string, string, int) *fetchResult {
		return completeFetch(1, nil)
	}
	fetchers := []exchFetcher{
		{name: "bisq", fn: fetch},
		{name: "bisq2", fn: fetch},
		{name: "hodlhodl", fn: fetch},
		{name: "robosats", fn: fetch},
		{name: "peachbitcoin", fn: fetch},
	}

	jobs := buildMarketFetchJobs(map[string]int{"EUR": 60000, "USD": 65000}, fetchers)
	if len(jobs) != 18 {
		t.Fatalf("jobs = %d, want 18", len(jobs))
	}
	unique := make(map[liveMarket]struct{}, len(jobs))
	for _, job := range jobs {
		if _, exists := unique[job.market]; exists {
			t.Fatalf("market scheduled twice: %+v", job.market)
		}
		unique[job.market] = struct{}{}
	}
	wants := []liveMarket{
		{exchange: "bisq", fiat: "EUR", direction: "ask"},
		{exchange: "peachbitcoin", fiat: "EUR", direction: "ask"},
		{exchange: "bisq", fiat: "EUR", direction: "bid"},
		{exchange: "peachbitcoin", fiat: "EUR", direction: "bid"},
		{exchange: "bisq", fiat: "USD", direction: "ask"},
		{exchange: "robosats", fiat: "USD", direction: "bid"},
	}
	indexes := []int{0, 4, 5, 9, 10, 17}
	for i, index := range indexes {
		if jobs[index].index != index || jobs[index].market != wants[i] {
			t.Fatalf("job[%d] = index:%d market:%+v, want index:%d market:%+v", index, jobs[index].index, jobs[index].market, index, wants[i])
		}
	}
}

func TestFetchMarketsSlowJobDoesNotBlockAvailableWorker(t *testing.T) {
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	fastCompleted := make(chan string, 2)
	fetch := func(_ context.Context, _ string, currency string, _ int) *fetchResult {
		if currency == "EUR" {
			close(slowStarted)
			<-releaseSlow
		} else {
			fastCompleted <- currency
		}
		return completeFetch(60000, nil)
	}
	jobs := []marketFetchJob{
		{index: 0, market: liveMarket{exchange: "bisq", fiat: "EUR", direction: "ask"}, fetcher: exchFetcher{name: "bisq", fn: fetch}},
		{index: 1, market: liveMarket{exchange: "bisq", fiat: "USD", direction: "ask"}, fetcher: exchFetcher{name: "bisq", fn: fetch}},
		{index: 2, market: liveMarket{exchange: "bisq", fiat: "GBP", direction: "ask"}, fetcher: exchFetcher{name: "bisq", fn: fetch}},
	}
	done := make(chan []marketFetchResult, 1)
	go func() { done <- fetchMarkets(context.Background(), jobs) }()

	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow fetch did not start")
	}
	for range 2 {
		select {
		case <-fastCompleted:
		case <-time.After(time.Second):
			t.Fatal("available worker did not continue past the slow fetch")
		}
	}
	close(releaseSlow)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fetch scheduler did not finish")
	}
}

func TestFetchMarketsBoundsConcurrencyAndReturnsOriginalOrder(t *testing.T) {
	const jobCount = 6
	started := make(chan struct{}, jobCount)
	release := make(chan struct{})
	var active atomic.Int32
	var maxActive atomic.Int32
	fetch := func(context.Context, string, string, int) *fetchResult {
		current := active.Add(1)
		updateAtomicMax(&maxActive, current)
		started <- struct{}{}
		<-release
		active.Add(-1)
		return completeFetch(60000, nil)
	}

	jobs := make([]marketFetchJob, 0, jobCount)
	for i := range jobCount {
		jobs = append(jobs, marketFetchJob{
			index:          i,
			market:         liveMarket{exchange: "robosats", fiat: "EUR", direction: "ask"},
			referencePrice: 60000,
			fetcher:        exchFetcher{name: "robosats", fn: fetch},
		})
	}

	done := make(chan []marketFetchResult, 1)
	go func() { done <- fetchMarkets(context.Background(), jobs) }()
	for range snapshotFetchConcurrency {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for initial concurrent fetches")
		}
	}
	select {
	case <-started:
		t.Fatalf("more than %d fetches started before a slot was released", snapshotFetchConcurrency)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	var results []marketFetchResult
	select {
	case results = <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fetch results")
	}
	if got := maxActive.Load(); got != snapshotFetchConcurrency {
		t.Fatalf("max active fetches = %d, want %d", got, snapshotFetchConcurrency)
	}
	if got := maxActive.Load(); got > robosatsFetchConcurrency {
		t.Fatalf("max active RoboSats fetches = %d, limit %d", got, robosatsFetchConcurrency)
	}
	if len(results) != jobCount {
		t.Fatalf("results = %d, want %d", len(results), jobCount)
	}
	for i, result := range results {
		if result.job.index != i {
			t.Fatalf("result[%d] has job index %d", i, result.job.index)
		}
	}
}

func TestFetchMarketsPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	seen := make(chan error, 1)
	fetch := func(ctx context.Context, _, _ string, _ int) *fetchResult {
		seen <- ctx.Err()
		return nil
	}
	jobs := []marketFetchJob{{
		index:          0,
		market:         liveMarket{exchange: "bisq", fiat: "EUR", direction: "ask"},
		referencePrice: 60000,
		fetcher:        exchFetcher{name: "bisq", fn: fetch},
	}}

	results := fetchMarkets(ctx, jobs)
	if len(results) != 1 || results[0].result.status() != fetchStatusFailed {
		t.Fatalf("results = %#v, want one failed result", results)
	}
	if err := <-seen; err != context.Canceled {
		t.Fatalf("worker context error = %v, want context.Canceled", err)
	}
}

func TestHTTPMarketFetchersUseParentContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != context.Canceled {
			t.Fatalf("request context error = %v, want context.Canceled", err)
		}
		return nil, req.Context().Err()
	})}

	if result := fetchHodlHodlCurrencyFrom(ctx, client, "https://hodlhodl.test", "ask", "EUR", 60000); result != nil {
		t.Fatalf("HodlHodl result = %#v, want failed fetch", result)
	}
	if result := fetchPeachBitcoinCurrencyFrom(ctx, client, "https://peach.test", "ask", "EUR", 60000); result != nil {
		t.Fatalf("Peach result = %#v, want failed fetch", result)
	}
	if result := fetchRobosatsCurrencyFrom(ctx, "ask", "EUR", 60000, client, []robosatsCoordinator{{Onion: "http://robosats.test"}}); result != nil {
		t.Fatalf("RoboSats result = %#v, want failed fetch", result)
	}
}

func updateAtomicMax(max *atomic.Int32, candidate int32) {
	for current := max.Load(); candidate > current; current = max.Load() {
		if max.CompareAndSwap(current, candidate) {
			return
		}
	}
}
