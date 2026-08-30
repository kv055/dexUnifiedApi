# `cmd/snapshot`

## Purpose

The snapshot command collects DEX offer books, persists observed offers in
PostgreSQL, reconciles authoritative live membership, records lifecycle data,
and asks the unified API to reload its live hub from the database.

## Main Flows

1. `main` loads reference prices and OHLC data before building every supported
   `(DEX, fiat, direction)` market job.
2. [`fetch_scheduler.go`](fetch_scheduler.md) runs at most two remote market
   censuses concurrently. Workers only fetch and map offers.
3. [`main.go`](main.md) restores the original market order and serially applies
   dust filtering, database transactions, outcome aggregation, and live-state
   reconciliation.
4. Only a complete, committed market may prove an offer absent. Incomplete and
   failed markets retain prior membership according to the stale-grace rules.
5. The command records lifecycle samples, triggers a PostgreSQL-backed hub
   refresh, performs closed-offer analysis, writes `snapshot_log`, and exits.

## Entry Points

- `main`: executes one complete snapshot pass.
- `buildMarketFetchJobs`: creates the deterministic market census list.
- `fetchMarkets`: performs bounded concurrent network retrieval.
- `reconcileLiveOfferStatus`: commits live membership and complete-observation
  timestamps transactionally.

## Operational Notes

- The container starts a new process one minute after the previous process
  exits. Snapshot passes never overlap; the interval is not a fixed wall-clock
  cadence.
- Fetch concurrency is fixed at two to preserve the worker's 64 MiB memory
  budget. RoboSats is also limited to two simultaneous market censuses.
- Reference-price requests, federation discovery, OHLC retrieval, persistence,
  reconciliation, and downstream analysis remain serial.
- Each market fetch emits a structured completion log and trace span containing
  its exchange, fiat, direction, outcome, offer count, and duration.
- Source-specific HTTP/gRPC timeouts remain in force. Cancellation from the
  parent snapshot context propagates to all concurrent requests.
