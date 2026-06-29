package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"commons/tradecost"
	"dexunifiedapi/internal/lifecycle"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// LiveOffer holds a single live offer pushed by the snapshot tool.
type LiveOffer struct {
	// ID identifies the live offer.
	ID int64 `json:"id"`
	// Exchange is the exchange value for the live offer.
	Exchange string `json:"exchange"`
	// FiatCurrency is a fiat-denominated fiat currency value.
	FiatCurrency string `json:"fiat_currency"`
	// Direction is the direction value for the live offer.
	Direction string `json:"direction"`
	// Price is the price value used by the live offer.
	Price float64 `json:"price"`
	// BTCMin is a BTC-denominated btcmin value.
	BTCMin *float64 `json:"btc_min"`
	// BTCMax is a BTC-denominated btcmax value.
	BTCMax *float64 `json:"btc_max"`
	// FiatMin is a fiat-denominated fiat min value.
	FiatMin *int64 `json:"fiat_min"`
	// FiatMax is a fiat-denominated fiat max value.
	FiatMax *int64 `json:"fiat_max"`
	// PaymentMethods lists the payment methods accepted by this offer.
	PaymentMethods []string `json:"payment_methods"`
	// OfferID is the offer id value for the live offer.
	OfferID *string `json:"offer_id"`
	// FirstSeen is the first seen value for the live offer.
	FirstSeen string `json:"first_seen"`
	// LastSeen is the last seen value for the live offer.
	LastSeen string `json:"last_seen"`
	// PriceType is the price value used by the live offer.
	PriceType *string `json:"price_type"`
	// SecurityDepositPct is the real per-offer security deposit/bond fraction
	// (offers.security_deposit_pct), already picked for the taker's side at
	// snapshot time. Nil for exchanges with no deposit concept, or offers
	// snapshotted before this was persisted.
	SecurityDepositPct *float64 `json:"security_deposit_pct"`
}

// LifecyclePoint is a single price observation from the order_lifecycle table.
type LifecyclePoint struct {
	// Time records the relevant time for the lifecycle point.
	Time string `json:"time"`
	// Price is the price value used by the lifecycle point.
	Price float64 `json:"price"`
	// NetPremiumPct is a percentage value.
	NetPremiumPct *float64 `json:"net_premium_pct,omitempty"`
}

type liveOfferStatus struct {
	IsLive    bool   `json:"is_live"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

type liveOfferPricing struct {
	PriceType         string  `json:"price_type"`
	FiatCurrency      string  `json:"fiat_currency"`
	OfferPrice        float64 `json:"offer_price"`
	RefPrice          float64 `json:"ref_price"`
	RawPremiumPercent float64 `json:"raw_premium_percent"`
}

type liveOfferVolumeLimits struct {
	BTCMin  *float64 `json:"btc_min"`
	BTCMax  *float64 `json:"btc_max"`
	FiatMin *int64   `json:"fiat_min"`
	FiatMax *int64   `json:"fiat_max"`
}

type liveOfferBTCNetworkFee struct {
	BTC                     float64                        `json:"btc"`
	Method                  string                         `json:"method"`
	Tier                    string                         `json:"tier"`
	Confidence              float64                        `json:"confidence"`
	NumTxs                  int                            `json:"num_txs"`
	RequiredConfirmations   int                            `json:"required_confirmations"`
	EstimatedArrivalMinutes int                            `json:"estimated_arrival_minutes"`
	Detail                  *tradecost.BTCNetworkFeeDetail `json:"detail,omitempty"`
}

type liveOfferRoute struct {
	CEX        string `json:"cex"`
	Settlement string `json:"settlement"`
}

type liveOfferPremium struct {
	TradeCostPercent float64 `json:"trade_cost_percent"`
	NetPercent       float64 `json:"net_percent"`
}

type liveOfferProfit struct {
	BTC  float64 `json:"btc"`
	Fiat float64 `json:"fiat"`
}

type liveOfferCostComponent struct {
	Pct float64 `json:"pct"`
	BTC float64 `json:"btc"`
}

type liveOfferNetworkCost struct {
	Pct float64 `json:"pct"`
}

type liveOfferScenarioCosts struct {
	CexTaker   liveOfferCostComponent        `json:"cex_taker"`
	Withdrawal liveOfferCostComponent        `json:"withdrawal"`
	DexFee     liveOfferCostComponent        `json:"dex_fee"`
	Network    liveOfferNetworkCost          `json:"network"`
	Conversion tradecost.ConversionBreakdown `json:"conversion"`
	Hedge      tradecost.HedgeBreakdown      `json:"hedge"`
	Total      liveOfferCostComponent        `json:"total"`
}

type liveOfferScenario struct {
	CapFiat       *float64                     `json:"cap_fiat,omitempty"`
	TradeSizeBTC  float64                      `json:"trade_size_btc"`
	TradeSizeFiat float64                      `json:"trade_size_fiat"`
	Route         liveOfferRoute               `json:"route"`
	Premium       liveOfferPremium             `json:"premium"`
	Profit        liveOfferProfit              `json:"profit"`
	Costs         liveOfferScenarioCosts       `json:"costs"`
	AbortCost     tradecost.AbortCostBreakdown `json:"abort_cost"`
	Capital       tradecost.CapitalRequirement `json:"capital"` // capital to commit for this scenario's trade size
}

type liveOfferAnalysis struct {
	FullVolume   liveOfferScenario  `json:"full_volume"`
	CappedVolume *liveOfferScenario `json:"capped_volume,omitempty"`
}

type liveOfferAPI struct {
	ID            int64                  `json:"id"`
	Exchange      string                 `json:"exchange"`
	OfferID       string                 `json:"offer_id"`
	PaymentMethods []string              `json:"payment_methods"`
	Direction     string                 `json:"direction"`
	Status        liveOfferStatus        `json:"status"`
	Pricing       liveOfferPricing       `json:"pricing"`
	VolumeLimits  liveOfferVolumeLimits  `json:"volume_limits"`
	BTCNetworkFee liveOfferBTCNetworkFee `json:"btc_network_fee"`
	Analysis      liveOfferAnalysis      `json:"analysis"`
}

// liveOutOffer is the shape sent to clients — includes computed premium fields.
type liveOutOffer struct {
	ID                        int64                         `json:"id"`
	Exchange                  string                        `json:"exchange"`
	FiatCurrency              string                        `json:"fiat_currency"`
	Direction                 string                        `json:"direction"`
	Price                     float64                       `json:"price"`
	RawPremiumPct             float64                       `json:"raw_premium_percent"` // explicit alias for PriceDiffPct
	PriceDiffPct              float64                       `json:"price_diff_percent"`
	RefPrice                  float64                       `json:"ref_price"`
	TradeCostPct              float64                       `json:"trade_cost_percent"`              // estimated CEX→DEX arbitrage cost as % of trade value
	NetPremiumPct             float64                       `json:"net_premium_percent"`             // PriceDiffPct minus TradeCostPct
	EstimatedProfitBTC        float64                       `json:"estimated_profit_btc"`            // estimated BTC profit for the offer's full trade size
	EstimatedProfitFiat       float64                       `json:"estimated_profit_fiat"`           // estimated fiat profit for the offer's full trade size
	CostBreakdown             tradecost.CostBreakdown       `json:"cost_breakdown"`                  // per-component cost detail (full size)
	CostBreakdownCapped       *tradecost.CostBreakdown      `json:"cost_breakdown_capped,omitempty"` // capped to max fiat limit (only set when limit < offer size)
	AbortCost                 tradecost.AbortCostBreakdown  `json:"abort_cost"`
	AbortCostCapped           *tradecost.AbortCostBreakdown `json:"abort_cost_capped,omitempty"`
	TradeCostPctCapped        *float64                      `json:"trade_cost_percent_capped,omitempty"`
	NetPremiumPctCapped       *float64                      `json:"net_premium_percent_capped,omitempty"`
	EstimatedProfitBTCCapped  *float64                      `json:"estimated_profit_btc_capped,omitempty"`  // estimated BTC profit at the max fiat cap
	EstimatedProfitFiatCapped *float64                      `json:"estimated_profit_fiat_capped,omitempty"` // estimated fiat profit at the max fiat cap
	BTCMin                    *float64                      `json:"btc_min"`
	BTCMax                    *float64                      `json:"btc_max"`
	FiatMin                   *int64                        `json:"fiat_min"`
	FiatMax                   *int64                        `json:"fiat_max"`
	PaymentMethods            []string                      `json:"payment_methods"`
	OfferID                   *string                       `json:"offer_id"`
	FirstSeen                 string                        `json:"first_seen"`
	LastSeen                  string                        `json:"last_seen"`
	IsLive                    bool                          `json:"is_live"`
	PriceType                 *string                       `json:"price_type"`
	Lifecycle                 []LifecyclePoint              `json:"lifecycle,omitempty"`
}

func normalizePriceType(priceType *string) string {
	if priceType == nil {
		return "unknown"
	}
	switch strings.ToLower(strings.TrimSpace(*priceType)) {
	case "fixed":
		return "fixed"
	case "floating":
		return "floating"
	default:
		return "unknown"
	}
}

func derefStringOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func buildScenarioFromBreakdown(bd tradecost.CostBreakdown, refPrice float64, capFiat *float64, tradeCostPct float64, netPremiumPct float64) liveOfferScenario {
	return liveOfferScenario{
		CapFiat:       capFiat,
		TradeSizeBTC:  bd.BTCAmount,
		TradeSizeFiat: tradecost.Round2(bd.BTCAmount * refPrice),
		Route: liveOfferRoute{
			CEX:        bd.RouteCEX,
			Settlement: bd.RouteMethod,
		},
		Premium: liveOfferPremium{
			TradeCostPercent: tradeCostPct,
			NetPercent:       netPremiumPct,
		},
		Profit: liveOfferProfit{
			BTC:  bd.EstimatedProfitBTC,
			Fiat: tradecost.Round2(bd.EstimatedProfitBTC * refPrice),
		},
		Costs: liveOfferScenarioCosts{
			CexTaker: liveOfferCostComponent{
				Pct: bd.CexTakerFeePct,
				BTC: bd.CexTakerFeeBTC,
			},
			Withdrawal: liveOfferCostComponent{
				Pct: bd.WithdrawalFeePct,
				BTC: bd.WithdrawalFeeBTC,
			},
			DexFee: liveOfferCostComponent{
				Pct: bd.DexFeePct,
				BTC: bd.DexFeeBTC,
			},
			Network: liveOfferNetworkCost{
				Pct: bd.NetworkFeePct,
			},
			Conversion: bd.Conversion,
			Hedge:      bd.Hedge,
			Total: liveOfferCostComponent{
				Pct: bd.TotalCostPct,
				BTC: bd.TotalCostBTC,
			},
		},
		AbortCost: tradecost.AbortCostFromBreakdown(bd),
		Capital:   bd.Capital,
	}
}

func buildBTCLiveNetworkFee(bd tradecost.CostBreakdown, requiredConfirmations int) liveOfferBTCNetworkFee {
	etaMinutes := 0
	if bd.NetworkFeeDetail != nil {
		etaMinutes = bd.NetworkFeeDetail.SelectedFullConfirmationMinutes
		if bd.NetworkFeeDetail.RequiredConfirmations > 0 {
			requiredConfirmations = bd.NetworkFeeDetail.RequiredConfirmations
		}
	}
	return liveOfferBTCNetworkFee{
		BTC:                     bd.NetworkFeeBTC,
		Method:                  bd.NetworkFeeMethod,
		Tier:                    bd.NetworkFeeTier,
		Confidence:              bd.NetworkFeeConfidence,
		NumTxs:                  bd.NumNetworkTxs,
		RequiredConfirmations:   requiredConfirmations,
		EstimatedArrivalMinutes: etaMinutes,
		Detail:                  bd.NetworkFeeDetail,
	}
}

func buildLiveOfferAPI(o liveOutOffer, sub liveSubscription) liveOfferAPI {
	requiredConfirmations := tradecost.ResolveRequiredConfirmations(o.Direction, sub.requiredConfirmations)
	api := liveOfferAPI{
		ID:            o.ID,
		Exchange:      o.Exchange,
		OfferID:       derefStringOrEmpty(o.OfferID),
		PaymentMethods: o.PaymentMethods,
		Direction:     o.Direction,
		Status: liveOfferStatus{
			IsLive:    o.IsLive,
			FirstSeen: o.FirstSeen,
			LastSeen:  o.LastSeen,
		},
		Pricing: liveOfferPricing{
			PriceType:         normalizePriceType(o.PriceType),
			FiatCurrency:      o.FiatCurrency,
			OfferPrice:        o.Price,
			RefPrice:          o.RefPrice,
			RawPremiumPercent: o.RawPremiumPct,
		},
		VolumeLimits: liveOfferVolumeLimits{
			BTCMin:  o.BTCMin,
			BTCMax:  o.BTCMax,
			FiatMin: o.FiatMin,
			FiatMax: o.FiatMax,
		},
		BTCNetworkFee: buildBTCLiveNetworkFee(o.CostBreakdown, requiredConfirmations),
		Analysis: liveOfferAnalysis{
			FullVolume: buildScenarioFromBreakdown(o.CostBreakdown, o.RefPrice, nil, o.TradeCostPct, o.NetPremiumPct),
		},
	}
	if o.CostBreakdownCapped != nil && o.TradeCostPctCapped != nil && o.NetPremiumPctCapped != nil {
		capFiat := tradecost.Round2(o.CostBreakdownCapped.BTCAmount * o.RefPrice)
		api.Analysis.CappedVolume = ptrScenario(buildScenarioFromBreakdown(*o.CostBreakdownCapped, o.RefPrice, &capFiat, *o.TradeCostPctCapped, *o.NetPremiumPctCapped))
	}
	return api
}

func ptrScenario(s liveOfferScenario) *liveOfferScenario {
	return &s
}

func computePriceDiffPct(offerPrice, refPrice float64) float64 {
	return lifecycle.PriceDiffPct(offerPrice, refPrice)
}

func computeDirectionalPremiumPct(direction string, offerPrice, refPrice float64) float64 {
	return lifecycle.DirectionalPremiumPct(direction, offerPrice, refPrice)
}

func selectBTCTradeSize(btcMax, btcMin *float64) float64 {
	if btcMax != nil && *btcMax > 0 {
		return *btcMax
	}
	if btcMin != nil && *btcMin > 0 {
		return *btcMin
	}
	return tradecost.DefaultBTCTradeSize()
}

func computeNetPremiumFields(premiumPct float64, btcSize float64, dexExchange string, direction string) (float64, float64, tradecost.CostBreakdown) {
	return computeNetPremiumFieldsWithConfirmations(premiumPct, btcSize, dexExchange, direction, 0)
}

func computeNetPremiumFieldsWithConfirmations(premiumPct float64, btcSize float64, dexExchange string, direction string, requiredConfirmations int) (float64, float64, tradecost.CostBreakdown) {
	return computeNetPremiumFieldsFull(premiumPct, btcSize, 0, dexExchange, direction, requiredConfirmations, nil, "", true, nil)
}

func computeNetPremiumFieldsFull(premiumPct float64, btcSize float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedCEXes []string, feeTier string, coupledHedge bool, realDepositPct *float64) (float64, float64, tradecost.CostBreakdown) {
	if tradeRouteOptimizer == nil || premiumPct <= 0 {
		return 0, 0, tradecost.CostBreakdown{}
	}

	requiredConfirmations = tradecost.ResolveRequiredConfirmations(direction, requiredConfirmations)
	bd := tradeRouteOptimizer.BestBreakdownFull(btcSize, refPriceEUR, dexExchange, direction, requiredConfirmations, excludedCEXes, feeTier, coupledHedge, realDepositPct)
	if bd.RouteCEX == "" {
		return 0, 0, tradecost.CostBreakdown{}
	}
	netPremium := math.Round((premiumPct-bd.TotalCostPct)*100) / 100
	bd.EstimatedProfitPct = netPremium
	bd.EstimatedProfitBTC = math.Round((bd.BTCAmount*(netPremium/100))*1e8) / 1e8
	return bd.TotalCostPct, netPremium, bd
}

// LiveHub holds live offers in memory and broadcasts to WebSocket subscribers
// on every CEX price tick.
type LiveHub struct {
	offersMu  sync.RWMutex
	offers    []LiveOffer
	lifecycle map[int64][]LifecyclePoint // keyed by offer DB ID; protected by offersMu

	subsMu      sync.Mutex
	subscribers map[chan []byte]liveSubscription
}

type liveSubscription struct {
	cex                   string
	requiredConfirmations int
	excludedCEXes         string // comma-joined for map key comparability
	feeTier               string
	coupledHedge          bool
	maxFiatLimitEUR       float64 // 0 = disabled
}

var hub = &LiveHub{
	lifecycle:   make(map[int64][]LifecyclePoint),
	subscribers: make(map[chan []byte]liveSubscription),
}

// setLifecycle replaces the in-memory lifecycle cache (called before setOffers
// so that the subsequent broadcast already includes fresh lifecycle points).
func (h *LiveHub) setLifecycle(lc map[int64][]LifecyclePoint) {
	h.offersMu.Lock()
	h.lifecycle = lc
	h.offersMu.Unlock()
}

// setOffers replaces the in-memory offer list and broadcasts to all subscribers.
func (h *LiveHub) setOffers(offers []LiveOffer) {
	h.offersMu.Lock()
	h.offers = offers
	h.offersMu.Unlock()
	appMetrics.setOffersInHub(len(offers))
	h.broadcastAll()
}

func (h *LiveHub) getOffers() []LiveOffer {
	h.offersMu.RLock()
	defer h.offersMu.RUnlock()
	cp := make([]LiveOffer, len(h.offers))
	copy(cp, h.offers)
	return cp
}

// subscribe registers a new WebSocket client with a CEX preference.
func (h *LiveHub) subscribe(sub liveSubscription) chan []byte {
	ch := make(chan []byte, 8)
	h.subsMu.Lock()
	h.subscribers[ch] = sub
	h.subsMu.Unlock()
	appMetrics.incWSClient()
	return ch
}

// unsubscribe removes a client and closes its channel.
func (h *LiveHub) unsubscribe(ch chan []byte) {
	h.subsMu.Lock()
	delete(h.subscribers, ch)
	h.subsMu.Unlock()
	appMetrics.decWSClient()
	close(ch)
}

// broadcastForCex broadcasts to all subscribers that use the given CEX,
// regardless of their other settings. Called on every price tick.
func (h *LiveHub) broadcastForCex(cex string) {
	h.subsMu.Lock()
	configSet := make(map[liveSubscription]struct{}, 4)
	for _, sub := range h.subscribers {
		if sub.cex == cex {
			configSet[sub] = struct{}{}
		}
	}
	h.subsMu.Unlock()

	for sub := range configSet {
		h.broadcastForConfig(sub)
	}
}

// broadcastForConfig computes premiums for the given config and pushes to matching subscribers.
func (h *LiveHub) broadcastForConfig(sub liveSubscription) {
	price := getCexPrice(sub.cex)
	data := h.computePayload(price, sub)
	if data == nil {
		return
	}
	appMetrics.observeBroadcast()
	h.subsMu.Lock()
	for ch, s := range h.subscribers {
		if s == sub {
			select {
			case ch <- data:
			default: // drop if subscriber is too slow
			}
		}
	}
	h.subsMu.Unlock()
}

// broadcastAll broadcasts to every subscriber grouped by their config.
// Called after offer data changes (new snapshot).
func (h *LiveHub) broadcastAll() {
	// Collect unique configs among active subscribers
	h.subsMu.Lock()
	configSet := make(map[liveSubscription]struct{}, 4)
	for _, sub := range h.subscribers {
		configSet[sub] = struct{}{}
	}
	h.subsMu.Unlock()

	// Always include the default so seedHubFromDB works even before any subscriber
	if len(configSet) == 0 {
		configSet[liveSubscription{cex: defaultCEX}] = struct{}{}
	}

	for sub := range configSet {
		h.broadcastForConfig(sub)
	}
}

// parseExcludedCEXes splits the comma-joined excluded CEX string back into a slice.
func parseExcludedCEXes(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ",")
}

// computeOutOffers returns the enriched offer slice for a given price and subscription config.
// If the selected CEX has no price yet, falls back to the default CEX price so
// that price_diff_percent is always meaningful and offers remain visible.
func (h *LiveHub) computeOutOffers(price float64, sub liveSubscription) []liveOutOffer {
	if price <= 0 {
		price = getCexPrice(defaultCEX)
	}
	offers := h.getOffers()
	h.offersMu.RLock()
	lc := h.lifecycle
	h.offersMu.RUnlock()
	excludedCEXes := parseExcludedCEXes(sub.excludedCEXes)
	result := make([]liveOutOffer, 0, len(offers))
	for _, o := range offers {
		pct := computeDirectionalPremiumPct(o.Direction, o.Price, price)
		btcSize := selectBTCTradeSize(o.BTCMax, o.BTCMin)
		tradeCostPct, netPremium, bd := computeNetPremiumFieldsFull(pct, btcSize, price, o.Exchange, o.Direction, sub.requiredConfirmations, excludedCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct)

		// If a max fiat limit is set and the offer's full size exceeds it, compute a second
		// breakdown capped to that fiat amount so the frontend can show both side by side.
		var bdCapped *tradecost.CostBreakdown
		var abortCapped *tradecost.AbortCostBreakdown
		var tcCapped, npCapped *float64
		var profitBTCCapped, profitFiatCapped *float64
		if sub.maxFiatLimitEUR > 0 && price > 0 {
			btcCap := sub.maxFiatLimitEUR / price
			if btcSize > btcCap {
				tc2, np2, bd2 := computeNetPremiumFieldsFull(pct, btcCap, price, o.Exchange, o.Direction, sub.requiredConfirmations, excludedCEXes, sub.feeTier, sub.coupledHedge, o.SecurityDepositPct)
				bdCapped = &bd2
				abort2 := tradecost.AbortCostFromBreakdown(bd2)
				abortCapped = &abort2
				tcCapped = &tc2
				npCapped = &np2
				profitBTC := bd2.EstimatedProfitBTC
				profitFiat := tradecost.Round2(profitBTC * price)
				profitBTCCapped = &profitBTC
				profitFiatCapped = &profitFiat
			}
		}

		result = append(result, liveOutOffer{
			ID:                        o.ID,
			Exchange:                  o.Exchange,
			FiatCurrency:              o.FiatCurrency,
			Direction:                 o.Direction,
			Price:                     o.Price,
			RawPremiumPct:             pct,
			PriceDiffPct:              pct,
			RefPrice:                  price,
			TradeCostPct:              tradeCostPct,
			NetPremiumPct:             netPremium,
			EstimatedProfitBTC:        bd.EstimatedProfitBTC,
			EstimatedProfitFiat:       tradecost.Round2(bd.EstimatedProfitBTC * price),
			CostBreakdown:             bd,
			CostBreakdownCapped:       bdCapped,
			AbortCost:                 tradecost.AbortCostFromBreakdown(bd),
			AbortCostCapped:           abortCapped,
			TradeCostPctCapped:        tcCapped,
			NetPremiumPctCapped:       npCapped,
			EstimatedProfitBTCCapped:  profitBTCCapped,
			EstimatedProfitFiatCapped: profitFiatCapped,
			BTCMin:                    o.BTCMin,
			BTCMax:                    o.BTCMax,
			FiatMin:                   o.FiatMin,
			FiatMax:                   o.FiatMax,
			PaymentMethods:            o.PaymentMethods,
			OfferID:                   o.OfferID,
			FirstSeen:                 o.FirstSeen,
			LastSeen:                  o.LastSeen,
			IsLive:                    true,
			PriceType:                 o.PriceType,
			Lifecycle:                 lc[o.ID],
		})
	}
	return result
}

// computePayload builds the JSON payload for a given subscription config.
// Returns nil on marshal error.
func (h *LiveHub) computePayload(price float64, sub liveSubscription) []byte {
	internal := h.computeOutOffers(price, sub)
	result := make([]liveOfferAPI, 0, len(internal))
	for _, offer := range internal {
		result = append(result, buildLiveOfferAPI(offer, sub))
	}
	data, err := json.Marshal(result)
	if err != nil {
		fmt.Printf("[LiveHub] marshal error: %v\n", err)
		return nil
	}
	return data
}

func handleLiveOfferLifecycleREST(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid offer id", http.StatusBadRequest)
		return
	}

	hub.offersMu.RLock()
	if pts, ok := hub.lifecycle[id]; ok {
		hub.offersMu.RUnlock()
		if pts == nil {
			pts = []LifecyclePoint{}
		}
		writeJSON(w, pts)
		return
	}
	hub.offersMu.RUnlock()

	lifecycle := fetchOfferLifecycle(r.Context(), []int64{id})
	pts := lifecycle[id]
	if pts == nil {
		pts = []LifecyclePoint{}
	}
	writeJSON(w, pts)
}

// handleLiveOffersWS is the WebSocket handler. It sends the current state
// immediately on connect, then streams every subsequent broadcast.
// Accepts optional ?cex=, ?fee_tier=, ?exclude_cexes= query params.
func handleLiveOffersWS(w http.ResponseWriter, r *http.Request) {
	sub := liveSubscription{
		cex:                   queryCex(r),
		requiredConfirmations: queryConfirmations(r),
		excludedCEXes:         strings.Join(queryExcludedCEXes(r), ","),
		feeTier:               queryFeeTier(r),
		coupledHedge:          queryCoupledHedge(r),
		maxFiatLimitEUR:       queryMaxFiatLimitEUR(r),
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[LiveWS] upgrade error: %v\n", err)
		return
	}
	defer ws.Close()

	ch := hub.subscribe(sub)
	defer hub.unsubscribe(ch)

	// Respond to pings so the browser doesn't drop the connection.
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Read loop — needed to process control frames (ping/close) from the client.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Send current state immediately on connect.
	if payload := hub.computePayload(getCexPrice(sub.cex), sub); payload != nil {
		if err := ws.WriteMessage(websocket.TextMessage, payload); err != nil {
			return
		}
	}

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-done:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ping.C:
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
