# `cmd/snapshot/main.go`

## Purpose

This file contains the DEX adapters and the serial side-effect phase of one
snapshot pass. Network scheduling is isolated in
[`fetch_scheduler.go`](fetch_scheduler.md).

## Runtime Role

`main` initializes tracing and PostgreSQL, loads the payment-method registry,
discovers RoboSats coordinators, and obtains reference prices and Kraken OHLC
data. It then delegates remote offer retrieval to the bounded scheduler.

When all fetches finish, `main` processes their results in the same stable order
used before concurrency was introduced. It filters dust, persists observed
offers, records outcomes, identifies complete committed markets, and runs the
transactional live-state reconciliation. All database and shared-state mutation
therefore stays on the main goroutine.

The final phases write baseline lifecycle rows, request a live-hub reload from
PostgreSQL, analyze closed offers, record the aggregate snapshot log, and exit.

## Function Map

### `main`

Builds and executes one snapshot pass. Fatal initialization errors exit the
process. Individual market failures are recorded and do not abort unrelated
markets or authorize missing-offer closure.

### DEX fetchers

Signatures follow this internal contract:

```go
func fetchDEXCurrency(ctx context.Context, direction, currency string, btcPrice int) *fetchResult
```

Bisq, Bisq2, HodlHodl, Peach, and RoboSats return a `fetchResult` classified as
complete, incomplete, or failed. Only complete results are candidates for
authoritative reconciliation. HTTP and gRPC requests derive from the scheduler
context while retaining their existing source-specific timeout behavior.

### `saveSnapshotPersisted`

Signature:

```go
func saveSnapshotPersisted(sdb *dbutil.DB, exchange, fiat, direction string, btcPrice float64, offers []offer) ([]livePayload, bool)
```

Writes the market snapshot marker and offer upserts in one transaction. A
failed transaction returns `false`; callers downgrade that market to incomplete
so it cannot close absent offers.

### `reconcileLiveOfferStatus`

Signature:

```go
func reconcileLiveOfferStatus(sdb *dbutil.DB, snapshotStart time.Time, confirmed map[liveMarket]struct{}, staleGrace time.Duration) error
```

Marks freshly persisted offers live, closes offers absent from complete
committed markets, records `live_market_observation.last_complete_at`, and
applies stale-grace expiry in one transaction. Incomplete and failed censuses do
not advance the last-complete timestamp.

### `writeBaselineLifecycle`

Persists price, reference-price, and directional-premium samples for observed
offers before the API refresh. This preserves price-path data even when the API
is unavailable.

### `pushLiveRefreshToServer`

Posts an authenticated refresh trigger to `/internal/live_offers`. The request
does not carry the authoritative book; the unified API reloads rows where
`offers.is_live = TRUE` from PostgreSQL.

### `writeSnapshotLog`

Writes per-exchange offer IDs, summed market-fetch effort, aggregate census
status, hard-failure count, and total cycle wall time to `snapshot_log`.

## Failure Behavior

- A failed census persists nothing and cannot confirm absence.
- A non-empty incomplete census may persist offers it did observe, but cannot
  confirm missing offers or an empty book.
- A complete empty census may close all prior offers in that exact market.
- A complete non-empty census becomes authoritative only after its transaction
  commits.
- API refresh and lifecycle failures are logged without retroactively changing
  the committed live membership.
