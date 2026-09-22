# `cmd/server/main.go`

Source package: `main`  
Source lines: `498`

## Package Purpose

This file contributes to the `main` package.

## File Role

This file implements 24 function or method entry point(s), declares 19 package value(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Reads from or writes to persistent storage.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Functions and Methods

### `seedHubFromDB`

This function is part of the `main` package implementation.

Signature: `func seedHubFromDB()`

### `coreTablesReady`

This function is part of the `main` package implementation.

Signature: `func coreTablesReady() bool`

### `getCexPrice`

This function is part of the `main` package implementation.

Signature: `func getCexPrice(cex string) float64`

### `setCexPrice`

This function is part of the `main` package implementation.

Signature: `func setCexPrice(cex string, p float64)`

### `init`

This function is part of the `main` package implementation.

Signature: `func init()`

### `runningUnderGoTest`

This function is part of the `main` package implementation.

Signature: `func runningUnderGoTest() bool`

### `main`

This function is part of the `main` package implementation.

Signature: `func main()`

### `corsMiddleware`

This function is part of the `main` package implementation.

Signature: `func corsMiddleware(next http.Handler) http.Handler`

### `handleLiveOffersREST`

This function is part of the `main` package implementation.

Signature: `func handleLiveOffersREST(w http.ResponseWriter, r *http.Request)`

### `fetchOfferLifecycle`

fetchOfferLifecycle returns lifecycle (snapshot_time, price) points grouped by offer_db_id.

Signature: `func fetchOfferLifecycle(ids []int64) map[int64][]LifecyclePoint`

### `writeLifecycleEntries`

writeLifecycleEntries persists a computed snapshot of each live offer's
metrics into the local order_lifecycle table, and updates best_route_cex /
best_route_method on the offers table.

Signature: `func writeLifecycleEntries(offers []liveOutOffer)`

### `handleInternalLiveOffers`

handleInternalLiveOffers receives the live offer list from the snapshot tool,
updates the hub, and persists lifecycle to the DB.

Signature: `func handleInternalLiveOffers(w http.ResponseWriter, r *http.Request)`

### `handleTradeCostConfig`

This function is part of the `main` package implementation.

Signature: `func handleTradeCostConfig(w http.ResponseWriter, r *http.Request)`

### `handleHealth`

This function is part of the `main` package implementation.

Signature: `func handleHealth(w http.ResponseWriter, r *http.Request)`

### `queryCex`

This function is part of the `main` package implementation.

Signature: `func queryCex(r *http.Request) string`

### `queryConfirmations`

This function is part of the `main` package implementation.

Signature: `func queryConfirmations(r *http.Request) int`

### `queryFeeTier`

This function is part of the `main` package implementation.

Signature: `func queryFeeTier(r *http.Request) string`

### `queryExcludedCEXes`

This function is part of the `main` package implementation.

Signature: `func queryExcludedCEXes(r *http.Request) []string`

### `queryCoupledHedge`

This function is part of the `main` package implementation.

Signature: `func queryCoupledHedge(r *http.Request) bool`

### `queryMaxFiatLimitEUR`

This function is part of the `main` package implementation.

Signature: `func queryMaxFiatLimitEUR(r *http.Request) float64`

### `parseFloat`

This function is part of the `main` package implementation.

Signature: `func parseFloat(s string) float64`

### `writeJSON`

This function is part of the `main` package implementation.

Signature: `func writeJSON(w http.ResponseWriter, data any)`

### `writeError`

This function is part of the `main` package implementation.

Signature: `func writeError(w http.ResponseWriter, err error)`

## Constants and Variables

- `defaultCEX`: No inline declaration comment is present.
- `adminDatabaseURL`: No inline declaration comment is present.
- `databaseURL`: No inline declaration comment is present.
- `listenAddr`: No inline declaration comment is present.
- `paymentNotificationSecret`: No inline declaration comment is present.
- `bisqGRPCHost`: No inline declaration comment is present.
- `bisqAPIPassword`: No inline declaration comment is present.
- `accountHealthInterval`: No inline declaration comment is present.
- `db`: No inline declaration comment is present.
- `priceManager`: No inline declaration comment is present.
- `lifecycleRepo`: No inline declaration comment is present.
- `paymentNotifications`: No inline declaration comment is present.
- `cexPricesMu`: No inline declaration comment is present.
- `cexPrices`: No inline declaration comment is present.
- `supportedCEXes`: No inline declaration comment is present.
- `tradeCostEstimators`: No inline declaration comment is present.
- `tradeRouteOptimizer`: No inline declaration comment is present.
- `validFeeTiers`: No inline declaration comment is present.

## Key Dependencies

- `commons/prices`
- `commons/tradecost`
- `context`
- `dexunifiedapi/db`
- `dexunifiedapi/internal/accounthealth`
- `dexunifiedapi/internal/config`
- `dexunifiedapi/internal/lifecycle`
- `dexunifiedapi/internal/paymentnotifications`
- `encoding/json`
- `fmt`
- `net/http`
- `os`
- `strconv`
- `strings`
- `sync`
- `time`

