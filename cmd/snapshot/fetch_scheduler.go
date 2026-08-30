package main

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"commons/fiat"
	"commons/tracing"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	// snapshotFetchConcurrency bounds response buffering and JSON decoding so
	// the snapshot worker remains inside its 64 MiB container limit.
	snapshotFetchConcurrency = 2
	// robosatsFetchConcurrency documents the independent Tor budget. It equals
	// the global limit today, but remains an explicit scheduler invariant.
	robosatsFetchConcurrency = 2
)

// marketFetchJob is one independently fetchable DEX/fiat/direction census.
// index preserves the historical serial order for deterministic persistence.
type marketFetchJob struct {
	index          int
	market         liveMarket
	referencePrice int
	fetcher        exchFetcher
}

// marketFetchResult is immutable worker output. Only the main goroutine may
// apply it to aggregate maps or PostgreSQL.
type marketFetchResult struct {
	job      marketFetchJob
	result   *fetchResult
	duration time.Duration
}

func buildMarketFetchJobs(refPrices map[string]int, fetchers []exchFetcher) []marketFetchJob {
	jobs := make([]marketFetchJob, 0)
	for _, currency := range fiat.Enabled {
		refPrice, ok := refPrices[currency]
		if !ok {
			continue
		}
		for _, direction := range []string{"ask", "bid"} {
			for _, fetcher := range fetchers {
				if !fiat.SupportsDEX(fetcher.name, currency) {
					continue
				}
				jobs = append(jobs, marketFetchJob{
					index:          len(jobs),
					market:         liveMarket{exchange: fetcher.name, fiat: currency, direction: direction},
					referencePrice: refPrice,
					fetcher:        fetcher,
				})
			}
		}
	}
	return jobs
}

// fetchMarkets runs remote censuses concurrently but does not mutate database
// or run-level state. Results are returned in the original job order.
func fetchMarkets(ctx context.Context, jobs []marketFetchJob) []marketFetchResult {
	if len(jobs) == 0 {
		return nil
	}

	jobCh := make(chan marketFetchJob, len(jobs))
	resultCh := make(chan marketFetchResult, len(jobs))
	robosatsSlots := make(chan struct{}, robosatsFetchConcurrency)
	for _, job := range jobs {
		jobCh <- job
	}
	close(jobCh)

	workerCount := snapshotFetchConcurrency
	if len(jobs) < workerCount {
		workerCount = len(jobs)
	}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobCh {
				if job.market.exchange == "robosats" {
					robosatsSlots <- struct{}{}
				}
				resultCh <- runMarketFetch(ctx, job)
				if job.market.exchange == "robosats" {
					<-robosatsSlots
				}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(resultCh)
	}()

	results := make([]marketFetchResult, 0, len(jobs))
	for result := range resultCh {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].job.index < results[j].job.index })
	return results
}

func runMarketFetch(ctx context.Context, job marketFetchJob) marketFetchResult {
	attrs := []attribute.KeyValue{
		attribute.String("exchange", job.market.exchange),
		attribute.String("fiat", job.market.fiat),
		attribute.String("direction", job.market.direction),
	}
	jobCtx, span := tracing.Tracer("snapshot").Start(ctx, "snapshot.fetch_market", oteltrace.WithAttributes(attrs...))
	started := time.Now()
	slog.InfoContext(jobCtx, "fetching market offers",
		"exchange", job.market.exchange,
		"fiat", job.market.fiat,
		"direction", job.market.direction)

	result := job.fetcher.fn(jobCtx, job.market.direction, job.market.fiat, job.referencePrice)
	duration := time.Since(started)
	status := result.status()
	count := 0
	if result != nil {
		count = len(result.Offers)
	}
	span.SetAttributes(
		attribute.String("status", string(status)),
		attribute.Int("offer_count", count),
		attribute.Int64("duration_ms", duration.Milliseconds()),
	)
	if err := jobCtx.Err(); err != nil {
		span.RecordError(err)
	}
	span.End()

	slog.InfoContext(jobCtx, "market offer fetch complete",
		"exchange", job.market.exchange,
		"fiat", job.market.fiat,
		"direction", job.market.direction,
		"status", status,
		"offer_count", count,
		"duration_ms", duration.Milliseconds(),
		"context_error", jobCtx.Err())
	return marketFetchResult{job: job, result: result, duration: duration}
}
