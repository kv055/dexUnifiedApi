# `closed_offer_analysis.go`

## Purpose

This file performs the historical profitability pass for closed offers. It
turns the raw `order_lifecycle` samples collected while an offer was live into
stable average fields on the matching `offers` row.

Those historical fields are later read by `dexMonitor` to build the
Taker Opportunities backtesting view.

## Runtime Role

The snapshot command records offer lifecycle samples while offers are live. Once
an offer closes and stays unseen for a configurable grace window, this file
analyzes all lifecycle rows for that offer and updates the closed `offers` record
with averaged premium, trade cost, net premium, BTC profit, fiat profit, route,
hedge, and size values.

Grace is `CLOSED_OFFER_ANALYSIS_GRACE_CYCLES` (default `3`) completed snapshot
cycles, measured from `snapshot_log` (one row per finished run). Closed offers
are not analyzed until `last_seen` is older than the end of the cycle before
that window, which avoids marking short-lived flickers as `analyzed` before they
reappear. `grace_cycles × SNAPSHOT_INTERVAL_MIN` minutes is only a floor and the
fallback when `snapshot_log` has too little history: the worker runs one cycle
per process, back to back, and a real pass (~3 min) is longer than
`SNAPSHOT_INTERVAL_MIN` (`1`), which dexMonitor also uses as its default OHLC
candle interval and so cannot be raised to match.

When a previously analyzed offer becomes live again, the snapshot bulk `is_live`
update in `main.go` clears `historical_analysis_status` and closure `avg_*`
fields so `analyzed` cannot coexist with `is_live = true`.

The analysis is database-driven. It reads from `offers` and `order_lifecycle`,
writes the derived `avg_*` fields back to `offers`, marks offers that cannot be
analyzed, and deletes closed offers whose historical premium is below the
configured cleanup threshold.

## Function Map

### `analyzeClosedOffers`

Signature:

```go
func analyzeClosedOffers(sdb closedOfferDB)
```

Description:
Finds closed offers that still need historical analysis. Before scanning, it
calls `resetIgnoredClosedOffersWithLifecycle` so offers that were previously
ignored can be retried after lifecycle rows arrive.

The query selects closed offers where `is_live = FALSE`,
`last_seen` is before the grace cutoff (`closedOfferAnalysisGraceCutoff`: the
older of the snapshot_log cycle bound and `grace_cycles × SNAPSHOT_INTERVAL_MIN`),
and `historical_analysis_status` is either missing or `ignored_no_lifecycle`.
Each row is converted into a `closedOfferRecord`, then passed to
`analyzeSingleClosedOffer`.

Called by:
- `main` in `dexUnifiedApi/cmd/snapshot/main.go`: runs the historical pass as
  part of the snapshot process after offers have been refreshed.

Calls:
- `resetIgnoredClosedOffersWithLifecycle`: reopens previously ignored offers
  that now have lifecycle data.
- `analyzeSingleClosedOffer`: computes and persists the per-offer historical
  result.

Side effects:
- Reads closed offers from `offers`.
- Updates, deletes, or marks offers indirectly through `analyzeSingleClosedOffer`.
- Logs a batch summary when at least one offer was processed.

Failure behavior:
- If the initial query fails, it logs the error and skips the pass.
- If scanning an individual row fails, that row is skipped and the rest of the
  batch continues.

### `resetIgnoredClosedOffersWithLifecycle`

Signature:

```go
func resetIgnoredClosedOffersWithLifecycle(sdb closedOfferDB)
```

Description:
Clears `historical_analysis_status` for offers marked
`ignored_no_lifecycle` once at least one `order_lifecycle` row exists for the
same offer. This lets the next analysis scan retry offers that closed before
their lifecycle rows were available.

Called by:
- `analyzeClosedOffers`: always runs before selecting the batch to analyze.

Side effects:
- Updates `offers.historical_analysis_status` from `ignored_no_lifecycle` to
  `NULL` for recoverable rows.
- Logs how many stale ignored offers were reset.

Failure behavior:
- Logs update errors and returns without stopping the rest of the snapshot
  command.

### `analyzeSingleClosedOffer`

Signature:

```go
func analyzeSingleClosedOffer(sdb closedOfferDB, offer closedOfferRecord) (string, bool)
```

Description:
Computes historical profitability for one closed offer. The core query runs one
aggregate over `order_lifecycle` for the offer:

- Overall averages use all lifecycle rows for reference price, raw premium, and
  BTC amount.
- Profitable averages use only rows where `net_premium_pct > 0`.
- Profitable-only averages include premium, trade cost percent, trade cost BTC,
  net premium percent, net premium BTC, and BTC amount.

The function uses the overall average premium only for cleanup. If it falls
below `deleteBelowPremiumPct`, the closed offer is deleted as non-premium noise.
If no lifecycle rows exist, the offer is marked `ignored_no_lifecycle`. If
lifecycle rows exist but none are profitable, the offer is marked `non_premium`
and keeps only basic historical averages.

For profitable offers, the function copies the current stored route and hedge
cost fields from `offers`, computes fiat profit as:

```text
profitable average net premium BTC * average reference price
```

It then writes the final historical result back to `offers`, including
`avg_ref_price`, `avg_premium_pct`, `avg_trade_cost_pct`,
`avg_trade_cost_btc`, `avg_net_premium_pct`, `avg_estimated_profit_btc`,
`avg_estimated_profit_fiat`, `avg_btc_amount`, best route fields, hedge cost
fields, CEX fees, network fee, and DEX fee.

Called by:
- `analyzeClosedOffers`: processes each closed offer in the selected batch.

Calls:
- `historicalRound4` and `historicalRound8`: normalize persisted averages.
- `nullHistoricalRound8`, `nullHistoricalString`, and `nullHistoricalInt`:
  preserve absent optional route and fee values as SQL `NULL`.

Side effects:
- Reads aggregate lifecycle history from `order_lifecycle`.
- Reads route and hedge fields from `offers`.
- Updates `offers` with historical analysis status and derived averages.
- Deletes offers whose overall average premium is below the cleanup threshold.
- Logs aggregate query errors.

Failure behavior:
- Returns `analysis_error` when the aggregate query fails.
- Returns `ignored_no_lifecycle` after marking the offer when no lifecycle rows
  or no average reference price are available.
- Returns `deleted_non_premium` and `removed = true` after deleting an offer
  below the cleanup threshold.
- Returns `non_premium` when lifecycle rows exist but none are profitable.
- Returns `analyzed` when profitable historical values are persisted.

Notes:
The profitable-only conditional averages are important: later monitor views
should explain profitable historical trades, not dilute those values with
unprofitable lifecycle samples from the same closed offer.

## One-time cleanup for inconsistent rows

If live offers already carry closure analysis from before the relive reset shipped,
run once against production:

```sql
UPDATE offers SET
  historical_analysis_status = NULL,
  avg_ref_price = NULL,
  avg_premium_pct = NULL,
  avg_trade_cost_pct = NULL,
  avg_trade_cost_btc = NULL,
  avg_hedge_fee_sum_btc = NULL,
  avg_net_premium_pct = NULL,
  avg_estimated_profit_btc = NULL,
  avg_estimated_profit_fiat = NULL,
  avg_btc_amount = NULL
WHERE is_live = TRUE
  AND historical_analysis_status IS NOT NULL;
```

### `historicalRound4`

Signature:

```go
func historicalRound4(v float64) float64
```

Description:
Rounds percentage and fiat/reference-price style values to four decimal places
before storing them on `offers`.

Called by:
- `analyzeSingleClosedOffer`: normalizes historical price, premium, percent, and
  fiat profit fields.

### `historicalRound8`

Signature:

```go
func historicalRound8(v float64) float64
```

Description:
Rounds BTC-denominated values to eight decimal places before storage.

Called by:
- `analyzeSingleClosedOffer`: normalizes BTC amount, BTC cost, and BTC profit
  fields.
- `nullHistoricalRound8`: applies the same BTC precision to optional values.

### `nullHistoricalRound8`

Signature:

```go
func nullHistoricalRound8(v sql.NullFloat64) any
```

Description:
Converts an optional BTC-denominated database value into either `nil` or an
eight-decimal rounded value for update statements.

Called by:
- `analyzeSingleClosedOffer`: writes optional route, hedge, network, CEX, and
  DEX fee values back to `offers`.

### `nullHistoricalString`

Signature:

```go
func nullHistoricalString(v sql.NullString) any
```

Description:
Converts an optional string into either `nil` or the stored string. Empty strings
are treated as absent so database fields remain `NULL` instead of containing
meaningless route or hedge metadata.

Called by:
- `analyzeSingleClosedOffer`: writes optional hedge venue and instrument fields.

### `nullHistoricalInt`

Signature:

```go
func nullHistoricalInt(v sql.NullInt64) any
```

Description:
Converts an optional integer into either `nil` or the stored value.

Called by:
- `analyzeSingleClosedOffer`: writes optional hedge transfer minutes.
