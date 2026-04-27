# `cmd/server/live.go`

Source package: `main`  
Source lines: `693`

## Package Purpose

This file contributes to the `main` package.

## File Role

This file defines 19 type(s), implements 25 function or method entry point(s), declares 2 package value(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Communicates over a websocket connection.

## Types

### `LiveOffer`

LiveOffer holds a single live offer pushed by the snapshot tool.

Fields:

- `ID` `int64`: ID identifies the live offer.
- `Exchange` `string`: Exchange is the exchange value for the live offer.
- `FiatCurrency` `string`: FiatCurrency is a fiat-denominated fiat currency value.
- `Direction` `string`: Direction is the direction value for the live offer.
- `Price` `float64`: Price is the price value used by the live offer.
- `BTCMin` `*float64`: BTCMin is a BTC-denominated btcmin value.
- `BTCMax` `*float64`: BTCMax is a BTC-denominated btcmax value.
- `FiatMin` `*int64`: FiatMin is a fiat-denominated fiat min value.
- `FiatMax` `*int64`: FiatMax is a fiat-denominated fiat max value.
- `PaymentMethod` `*string`: PaymentMethod is the payment method value for the live offer.
- `OfferID` `*string`: OfferID is the offer id value for the live offer.
- `FirstSeen` `string`: FirstSeen is the first seen value for the live offer.
- `LastSeen` `string`: LastSeen is the last seen value for the live offer.
- `PriceType` `*string`: PriceType is the price value used by the live offer.

### `LifecyclePoint`

LifecyclePoint is a single price observation from the order_lifecycle table.

Fields:

- `Time` `string`: Time records the relevant time for the lifecycle point.
- `Price` `float64`: Price is the price value used by the lifecycle point.
- `NetPremiumPct` `*float64`: NetPremiumPct is a percentage value.

### `liveOfferStatus`

Fields:

- `IsLive` `bool`: Reports a boolean condition for the live offer status.
- `FirstSeen` `string`: Stores timing information for the live offer status.
- `LastSeen` `string`: Stores timing information for the live offer status.

### `liveOfferPricing`

Fields:

- `PriceType` `string`: Stores the price-related value for the live offer pricing.
- `FiatCurrency` `string`: Stores fiat currency data for the live offer pricing.
- `OfferPrice` `float64`: Stores the price-related value for the live offer pricing.
- `RefPrice` `float64`: Stores the price-related value for the live offer pricing.
- `RawPremiumPercent` `float64`: Stores a percentage or premium metric for the live offer pricing.

### `liveOfferVolumeLimits`

Fields:

- `BTCMin` `*float64`: Stores a Bitcoin-denominated amount or limit for the live offer volume limits.
- `BTCMax` `*float64`: Stores a Bitcoin-denominated amount or limit for the live offer volume limits.
- `FiatMin` `*int64`: Stores fiat currency data for the live offer volume limits.
- `FiatMax` `*int64`: Stores fiat currency data for the live offer volume limits.

### `liveOfferBTCNetworkFee`

Fields:

- `BTC` `float64`: Stores a Bitcoin-denominated amount or limit for the live offer b t c network fee.
- `Method` `string`: Stores the method value for the live offer b t c network fee.
- `Tier` `string`: Stores the tier value for the live offer b t c network fee.
- `Confidence` `float64`: Stores the confidence value for the live offer b t c network fee.
- `NumTxs` `int`: Stores the num txs value for the live offer b t c network fee.
- `RequiredConfirmations` `int`: Stores the required confirmations value for the live offer b t c network fee.
- `EstimatedArrivalMinutes` `int`: Stores the estimated arrival minutes value for the live offer b t c network fee.
- `Detail` `*tradecost.BTCNetworkFeeDetail`: Stores the detail value for the live offer b t c network fee.

### `liveOfferRoute`

Fields:

- `CEX` `string`: Stores the c e x value for the live offer route.
- `Settlement` `string`: Stores the settlement value for the live offer route.

### `liveOfferPremium`

Fields:

- `TradeCostPercent` `float64`: Stores a percentage or premium metric for the live offer premium.
- `NetPercent` `float64`: Stores a percentage or premium metric for the live offer premium.

### `liveOfferProfit`

Fields:

- `BTC` `float64`: Stores a Bitcoin-denominated amount or limit for the live offer profit.
- `Fiat` `float64`: Stores fiat currency data for the live offer profit.

### `liveOfferCostComponent`

Fields:

- `Pct` `float64`: Stores a percentage or premium metric for the live offer cost component.
- `BTC` `float64`: Stores a Bitcoin-denominated amount or limit for the live offer cost component.

### `liveOfferNetworkCost`

Fields:

- `Pct` `float64`: Stores a percentage or premium metric for the live offer network cost.

### `liveOfferHedgeCost`

Fields:

- `Pct` `float64`: Stores a percentage or premium metric for the live offer hedge cost.
- `BTC` `float64`: Stores a Bitcoin-denominated amount or limit for the live offer hedge cost.
- `Venue` `string`: Stores the venue value for the live offer hedge cost.
- `Instrument` `string`: Stores the instrument value for the live offer hedge cost.
- `TransferMinutes` `int`: Stores the transfer minutes value for the live offer hedge cost.
- `Position` `*tradecost.HedgePositionDetail`: Stores the position value for the live offer hedge cost.

### `liveOfferScenarioCosts`

Fields:

- `CexTaker` `liveOfferCostComponent`: Stores the cex taker value for the live offer scenario costs.
- `Withdrawal` `liveOfferCostComponent`: Stores the withdrawal value for the live offer scenario costs.
- `DexFee` `liveOfferCostComponent`: Stores the dex fee value for the live offer scenario costs.
- `Network` `liveOfferNetworkCost`: Stores the network value for the live offer scenario costs.
- `Hedge` `liveOfferHedgeCost`: Stores the hedge value for the live offer scenario costs.
- `Total` `liveOfferCostComponent`: Stores the total value for the live offer scenario costs.

### `liveOfferScenario`

Fields:

- `CapFiat` `*float64`: Stores fiat currency data for the live offer scenario.
- `TradeSizeBTC` `float64`: Stores a Bitcoin-denominated amount or limit for the live offer scenario.
- `TradeSizeFiat` `float64`: Stores fiat currency data for the live offer scenario.
- `Route` `liveOfferRoute`: Stores the route value for the live offer scenario.
- `Premium` `liveOfferPremium`: Stores a percentage or premium metric for the live offer scenario.
- `Profit` `liveOfferProfit`: Stores the profit value for the live offer scenario.
- `Costs` `liveOfferScenarioCosts`: Stores the costs value for the live offer scenario.

### `liveOfferAnalysis`

Fields:

- `FullVolume` `liveOfferScenario`: Stores the full volume value for the live offer analysis.
- `CappedVolume` `*liveOfferScenario`: Stores the capped volume value for the live offer analysis.

### `liveOfferAPI`

Fields:

- `ID` `int64`: Identifies the live offer a p i.
- `Exchange` `string`: Stores the exchange value for the live offer a p i.
- `OfferID` `string`: Stores the offer i d value for the live offer a p i.
- `PaymentMethod` `string`: Stores the payment method value for the live offer a p i.
- `Direction` `string`: Stores the direction value for the live offer a p i.
- `Status` `liveOfferStatus`: Stores the current status for the live offer a p i.
- `Pricing` `liveOfferPricing`: Stores the pricing value for the live offer a p i.
- `VolumeLimits` `liveOfferVolumeLimits`: Stores the volume limits value for the live offer a p i.
- `BTCNetworkFee` `liveOfferBTCNetworkFee`: Stores a Bitcoin-denominated amount or limit for the live offer a p i.
- `Analysis` `liveOfferAnalysis`: Stores the analysis value for the live offer a p i.

### `liveOutOffer`

liveOutOffer is the shape sent to clients — includes computed premium fields.

Fields:

- `ID` `int64`: Identifies the live out offer.
- `Exchange` `string`: Stores the exchange value for the live out offer.
- `FiatCurrency` `string`: Stores fiat currency data for the live out offer.
- `Direction` `string`: Stores the direction value for the live out offer.
- `Price` `float64`: Stores the price-related value for the live out offer.
- `RawPremiumPct` `float64`: Stores a percentage or premium metric for the live out offer.
- `PriceDiffPct` `float64`: Stores the price-related value for the live out offer.
- `RefPrice` `float64`: Stores the price-related value for the live out offer.
- `TradeCostPct` `float64`: Stores a percentage or premium metric for the live out offer.
- `NetPremiumPct` `float64`: Stores a percentage or premium metric for the live out offer.
- `EstimatedProfitBTC` `float64`: Stores a Bitcoin-denominated amount or limit for the live out offer.
- `EstimatedProfitFiat` `float64`: Stores fiat currency data for the live out offer.
- `CostBreakdown` `tradecost.CostBreakdown`: Stores the cost breakdown value for the live out offer.
- `CostBreakdownCapped` `*tradecost.CostBreakdown`: Stores the cost breakdown capped value for the live out offer.
- `TradeCostPctCapped` `*float64`: Stores a percentage or premium metric for the live out offer.
- `NetPremiumPctCapped` `*float64`: Stores a percentage or premium metric for the live out offer.
- `EstimatedProfitBTCCapped` `*float64`: Stores a Bitcoin-denominated amount or limit for the live out offer.
- `EstimatedProfitFiatCapped` `*float64`: Stores fiat currency data for the live out offer.
- `BTCMin` `*float64`: Stores a Bitcoin-denominated amount or limit for the live out offer.
- `BTCMax` `*float64`: Stores a Bitcoin-denominated amount or limit for the live out offer.
- `FiatMin` `*int64`: Stores fiat currency data for the live out offer.
- `FiatMax` `*int64`: Stores fiat currency data for the live out offer.
- `PaymentMethod` `*string`: Stores the payment method value for the live out offer.
- `OfferID` `*string`: Stores the offer i d value for the live out offer.
- `FirstSeen` `string`: Stores timing information for the live out offer.
- `LastSeen` `string`: Stores timing information for the live out offer.
- `IsLive` `bool`: Reports a boolean condition for the live out offer.
- `PriceType` `*string`: Stores the price-related value for the live out offer.
- `Lifecycle` `[]LifecyclePoint`: Stores the lifecycle value for the live out offer.

### `LiveHub`

LiveHub holds live offers in memory and broadcasts to WebSocket subscribers
on every CEX price tick.

Fields:

- `offersMu` `sync.RWMutex`: Stores the offers mu value for the live hub.
- `offers` `[]LiveOffer`: Stores the offers value for the live hub.
- `lifecycle` `map[int64][]LifecyclePoint`: Stores the lifecycle value for the live hub.
- `subsMu` `sync.Mutex`: Stores the subs mu value for the live hub.
- `subscribers` `map[chan []byte]liveSubscription`: Stores the subscribers value for the live hub.

### `liveSubscription`

Fields:

- `cex` `string`: Stores the cex value for the live subscription.
- `requiredConfirmations` `int`: Stores the required confirmations value for the live subscription.
- `excludedCEXes` `string`: Stores the excluded c e xes value for the live subscription.
- `feeTier` `string`: Stores the fee tier value for the live subscription.
- `coupledHedge` `bool`: Stores the coupled hedge value for the live subscription.
- `maxFiatLimitEUR` `float64`: Stores fiat currency data for the live subscription.

## Functions and Methods

### `normalizePriceType`

This function is part of the `main` package implementation.

Signature: `func normalizePriceType(priceType *string) string`

### `derefStringOrEmpty`

This function is part of the `main` package implementation.

Signature: `func derefStringOrEmpty(v *string) string`

### `buildScenarioFromBreakdown`

This function is part of the `main` package implementation.

Signature: `func buildScenarioFromBreakdown(bd tradecost.CostBreakdown, refPrice float64, capFiat *float64, tradeCostPct float64, netPremiumPct float64) liveOfferScenario`

### `buildBTCLiveNetworkFee`

This function is part of the `main` package implementation.

Signature: `func buildBTCLiveNetworkFee(bd tradecost.CostBreakdown, requiredConfirmations int) liveOfferBTCNetworkFee`

### `buildLiveOfferAPI`

This function is part of the `main` package implementation.

Signature: `func buildLiveOfferAPI(o liveOutOffer, sub liveSubscription) liveOfferAPI`

### `ptrScenario`

This function is part of the `main` package implementation.

Signature: `func ptrScenario(s liveOfferScenario) *liveOfferScenario`

### `computePriceDiffPct`

This function is part of the `main` package implementation.

Signature: `func computePriceDiffPct(offerPrice, refPrice float64) float64`

### `computeDirectionalPremiumPct`

This function is part of the `main` package implementation.

Signature: `func computeDirectionalPremiumPct(direction string, offerPrice, refPrice float64) float64`

### `selectBTCTradeSize`

This function is part of the `main` package implementation.

Signature: `func selectBTCTradeSize(btcMax, btcMin *float64) float64`

### `computeNetPremiumFields`

This function is part of the `main` package implementation.

Signature: `func computeNetPremiumFields(premiumPct float64, btcSize float64, dexExchange string, direction string) (float64, float64, tradecost.CostBreakdown)`

### `computeNetPremiumFieldsWithConfirmations`

This function is part of the `main` package implementation.

Signature: `func computeNetPremiumFieldsWithConfirmations(premiumPct float64, btcSize float64, dexExchange string, direction string, requiredConfirmations int) (float64, float64, tradecost.CostBreakdown)`

### `computeNetPremiumFieldsFull`

This function is part of the `main` package implementation.

Signature: `func computeNetPremiumFieldsFull(premiumPct float64, btcSize float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedCEXes []string, feeTier string, coupledHedge bool) (float64, float64, tradecost.CostBreakdown)`

### `(*LiveHub).setLifecycle`

setLifecycle replaces the in-memory lifecycle cache (called before setOffers
so that the subsequent broadcast already includes fresh lifecycle points).

Signature: `func (h *LiveHub) setLifecycle(lc map[int64][]LifecyclePoint)`

### `(*LiveHub).setOffers`

setOffers replaces the in-memory offer list and broadcasts to all subscribers.

Signature: `func (h *LiveHub) setOffers(offers []LiveOffer)`

### `(*LiveHub).getOffers`

This function is part of the `main` package implementation.

Signature: `func (h *LiveHub) getOffers() []LiveOffer`

### `(*LiveHub).subscribe`

subscribe registers a new WebSocket client with a CEX preference.

Signature: `func (h *LiveHub) subscribe(sub liveSubscription) chan []byte`

### `(*LiveHub).unsubscribe`

unsubscribe removes a client and closes its channel.

Signature: `func (h *LiveHub) unsubscribe(ch chan []byte)`

### `(*LiveHub).broadcastForCex`

broadcastForCex broadcasts to all subscribers that use the given CEX,
regardless of their other settings. Called on every price tick.

Signature: `func (h *LiveHub) broadcastForCex(cex string)`

### `(*LiveHub).broadcastForConfig`

broadcastForConfig computes premiums for the given config and pushes to matching subscribers.

Signature: `func (h *LiveHub) broadcastForConfig(sub liveSubscription)`

### `(*LiveHub).broadcastAll`

broadcastAll broadcasts to every subscriber grouped by their config.
Called after offer data changes (new snapshot).

Signature: `func (h *LiveHub) broadcastAll()`

### `parseExcludedCEXes`

parseExcludedCEXes splits the comma-joined excluded CEX string back into a slice.

Signature: `func parseExcludedCEXes(joined string) []string`

### `(*LiveHub).computeOutOffers`

computeOutOffers returns the enriched offer slice for a given price and subscription config.
If the selected CEX has no price yet, falls back to the default CEX price so
that price_diff_percent is always meaningful and offers remain visible.

Signature: `func (h *LiveHub) computeOutOffers(price float64, sub liveSubscription) []liveOutOffer`

### `(*LiveHub).computePayload`

computePayload builds the JSON payload for a given subscription config.
Returns nil on marshal error.

Signature: `func (h *LiveHub) computePayload(price float64, sub liveSubscription) []byte`

### `handleLiveOfferLifecycleREST`

This function is part of the `main` package implementation.

Signature: `func handleLiveOfferLifecycleREST(w http.ResponseWriter, r *http.Request)`

### `handleLiveOffersWS`

handleLiveOffersWS is the WebSocket handler. It sends the current state
immediately on connect, then streams every subsequent broadcast.
Accepts optional ?cex=, ?fee_tier=, ?exclude_cexes= query params.

Signature: `func handleLiveOffersWS(w http.ResponseWriter, r *http.Request)`

## Constants and Variables

- `upgrader`: No inline declaration comment is present.
- `hub`: No inline declaration comment is present.

## Key Dependencies

- `encoding/json`
- `fmt`
- `math`
- `net/http`
- `strconv`
- `strings`
- `sync`
- `time`
- `commons/tradecost`
- `github.com/gorilla/websocket`

