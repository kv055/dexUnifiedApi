# `cmd/snapshot/main.go`

Source package: `main`  
Source lines: `965`

## Package Purpose

This file contributes to the `main` package.

## File Role

This file defines 4 type(s), implements 27 function or method entry point(s), declares 7 package value(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Reads from or writes to persistent storage.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.
- Communicates with a gRPC service.

## Types

### `bisqCreds`

bisqCreds injects the Bisq API password header on every gRPC call.

Fields:

- `password` `string`: Stores the password value for the bisq creds.

### `offer`

Fields:

- `OfferID` `string`: Stores the offer i d value for the offer.
- `FiatCurrency` `string`: Stores fiat currency data for the offer.
- `Price` `float64`: Stores the price-related value for the offer.
- `PriceDiffPct` `*float64`: Stores the price-related value for the offer.
- `BTCMin` `*float64`: Stores a Bitcoin-denominated amount or limit for the offer.
- `BTCMax` `*float64`: Stores a Bitcoin-denominated amount or limit for the offer.
- `FiatMin` `*int`: Stores fiat currency data for the offer.
- `FiatMax` `*int`: Stores fiat currency data for the offer.
- `PaymentMethod` `string`: Stores the payment method value for the offer.
- `PriceType` `*string`: Stores the price-related value for the offer.

### `fetchResult`

Fields:

- `BTCPrice` `float64`: Stores the price-related value for the fetch result.
- `Offers` `[]offer`: Stores the offers value for the fetch result.

### `livePayload`

Fields:

- `ID` `int64`: Identifies the live payload.
- `Exchange` `string`: Stores the exchange value for the live payload.
- `FiatCurrency` `string`: Stores fiat currency data for the live payload.
- `Direction` `string`: Stores the direction value for the live payload.
- `Price` `float64`: Stores the price-related value for the live payload.
- `BTCMin` `*float64`: Stores a Bitcoin-denominated amount or limit for the live payload.
- `BTCMax` `*float64`: Stores a Bitcoin-denominated amount or limit for the live payload.
- `FiatMin` `*int64`: Stores fiat currency data for the live payload.
- `FiatMax` `*int64`: Stores fiat currency data for the live payload.
- `PaymentMethod` `*string`: Stores the payment method value for the live payload.
- `OfferID` `*string`: Stores the offer i d value for the live payload.
- `FirstSeen` `string`: Stores timing information for the live payload.
- `LastSeen` `string`: Stores timing information for the live payload.
- `PriceType` `*string`: Stores the price-related value for the live payload.

## Functions and Methods

### `init`

This function is part of the `main` package implementation.

Signature: `func init()`

### `runningUnderGoTest`

This function is part of the `main` package implementation.

Signature: `func runningUnderGoTest() bool`

### `(bisqCreds).GetRequestMetadata`

GetRequestMetadata retrieves a request metadata.

Signature: `func (c bisqCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error)`

### `(bisqCreds).RequireTransportSecurity`

RequireTransportSecurity reports whether the credentials require transport security.

Signature: `func (c bisqCreds) RequireTransportSecurity() bool`

### `priceTypeFixed`

This function is part of the `main` package implementation.

Signature: `func priceTypeFixed() *string`

### `priceTypeFloating`

This function is part of the `main` package implementation.

Signature: `func priceTypeFloating() *string`

### `torClient`

This function is part of the `main` package implementation.

Signature: `func torClient() *http.Client`

### `clearnetClient`

This function is part of the `main` package implementation.

Signature: `func clearnetClient() *http.Client`

### `priceBisqTor`

This function is part of the `main` package implementation.

Signature: `func priceBisqTor(fiat string) (int, error)`

### `priceKraken`

This function is part of the `main` package implementation.

Signature: `func priceKraken(fiat string) (int, error)`

### `priceCoinGecko`

This function is part of the `main` package implementation.

Signature: `func priceCoinGecko(fiat string) (int, error)`

### `fetchReferencePrice`

This function is part of the `main` package implementation.

Signature: `func fetchReferencePrice(fiat string) int`

### `fetchBisq`

This function is part of the `main` package implementation.

Signature: `func fetchBisq(direction string, btcPrice int) *fetchResult`

### `fetchHodlHodl`

This function is part of the `main` package implementation.

Signature: `func fetchHodlHodl(direction string, btcPrice int) *fetchResult`

### `fetchRobosats`

This function is part of the `main` package implementation.

Signature: `func fetchRobosats(direction string, btcPrice int) *fetchResult`

### `saveSnapshot`

This function is part of the `main` package implementation.

Signature: `func saveSnapshot(sdb *dbutil.DB, exchange, fiat, direction string, btcPrice float64, offers []offer) []livePayload`

### `writeSnapshotLog`

This function is part of the `main` package implementation.

Signature: `func writeSnapshotLog(sdb *dbutil.DB, exchangeData map[string]map[string]bool)`

### `main`

This function is part of the `main` package implementation.

Signature: `func main()`

### `fetchAndStoreKrakenOHLC`

fetchAndStoreKrakenOHLC fetches 1-min Kraken OHLC candles and upserts them into kraken_ohlc.

Signature: `func fetchAndStoreKrakenOHLC(sdb *dbutil.DB)`

### `settleClosedOffers`

settleClosedOffers computes avg_ref_price for offers that just closed and do
not have a settled historical reference price yet.

Signature: `func settleClosedOffers(sdb *dbutil.DB)`

### `pushLiveOffersToServer`

pushLiveOffersToServer posts the freshly fetched live offers to the server's
/internal/live_offers endpoint so the hub can serve them from memory without
any DB reads.

Signature: `func pushLiveOffersToServer(offers []livePayload, serverURL string)`

### `fetchAndStoreKrakenOHLCSince`

fetchAndStoreKrakenOHLCSince fetches 1-min candles starting from sinceUnix
and upserts them into kraken_ohlc. Used to backfill history for offers whose
lifetime predates the standard OHLC cache window (~7.5 days).

Signature: `func fetchAndStoreKrakenOHLCSince(sdb *dbutil.DB, sinceUnix int64)`

### `parseDBTimeUnix`

parseDBTimeUnix parses a SQLite timestamp string and returns its Unix timestamp.
Returns 0 if parsing fails.

Signature: `func parseDBTimeUnix(s string) int64`

### `round2`

This function is part of the `main` package implementation.

Signature: `func round2(v float64) float64`

### `round8`

This function is part of the `main` package implementation.

Signature: `func round8(v float64) float64`

### `parseFloatStr`

This function is part of the `main` package implementation.

Signature: `func parseFloatStr(s string) float64`

### `nilIfEmpty`

This function is part of the `main` package implementation.

Signature: `func nilIfEmpty(s string) any`

## Constants and Variables

- `adminDatabaseURL`: No inline declaration comment is present.
- `databaseURL`: No inline declaration comment is present.
- `bisqGRPCHost`: No inline declaration comment is present.
- `bisqAPIPassword`: No inline declaration comment is present.
- `priceProvider`: No inline declaration comment is present.
- `torProxy`: No inline declaration comment is present.
- `robosatsOnions`: No inline declaration comment is present.

## Key Dependencies

- `context`
- `dexunifiedapi/db`
- `dexunifiedapi/internal/config`
- `encoding/json`
- `fmt`
- `io`
- `math`
- `net`
- `net/http`
- `net/url`
- `os`
- `strconv`
- `strings`
- `time`
- `dexunifiedapi/bisq_grpc/go`
- `google.golang.org/grpc`
- `google.golang.org/grpc/credentials/insecure`

