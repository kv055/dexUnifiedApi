# `cmd/snapshot/fetch_scheduler.go`

## Purpose

This file bounds concurrent DEX network retrieval while keeping persistence and
live-state decisions deterministic and race-free.

## Runtime Role

The scheduler receives the reference prices and configured DEX adapters after
snapshot initialization. It creates one job for every supported exchange,
currency, and direction, executes at most two jobs concurrently, and returns
immutable results sorted into their original serial order.

Workers make network calls and map source responses only. They do not write to
PostgreSQL or mutate run-level maps. Each worker creates a child trace span and
structured start/completion logs for its market.

## Function Map

### `buildMarketFetchJobs`

Signature:

```go
func buildMarketFetchJobs(refPrices map[string]int, fetchers []exchFetcher) []marketFetchJob
```

Builds the supported market matrix in stable currency, direction, and DEX
order. Currencies without a reference price and unsupported DEX/currency pairs
are omitted.

### `fetchMarkets`

Signature:

```go
func fetchMarkets(ctx context.Context, jobs []marketFetchJob) []marketFetchResult
```

Runs a fixed two-worker pool, enforces the RoboSats concurrency ceiling, waits
for every scheduled job, and sorts results by their assigned sequence index.
Cancellation is passed to each adapter; every started job still produces a
result classified through the existing fetch-status contract.

### `runMarketFetch`

Executes one adapter call, measures its duration, records status and offer count
on the trace span, and emits the structured completion log. It has no database
or shared aggregation side effects.

## Operational Notes

- `snapshotFetchConcurrency` and `robosatsFetchConcurrency` are intentionally
  fixed at two; there are no environment overrides.
- Per-exchange duration in `snapshot_log` is summed fetch effort. The independent
  `cycle_ms` field remains total wall-clock duration.
- Completion order never controls persistence or offer closure.

