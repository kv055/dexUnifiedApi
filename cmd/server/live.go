package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// LiveOffer holds a single live offer pushed by the snapshot tool.
type LiveOffer struct {
	ID            int64    `json:"id"`
	Exchange      string   `json:"exchange"`
	Direction     string   `json:"direction"`
	Price         float64  `json:"price"`
	BTCMin        *float64 `json:"btc_min"`
	BTCMax        *float64 `json:"btc_max"`
	FiatMin       *int64   `json:"fiat_min"`
	FiatMax       *int64   `json:"fiat_max"`
	PaymentMethod *string  `json:"payment_method"`
	OfferID       *string  `json:"offer_id"`
	FirstSeen     string   `json:"first_seen"`
	LastSeen      string   `json:"last_seen"`
	PriceType     *string  `json:"price_type"`
}

// LifecyclePoint is a single price observation from the order_lifecycle table.
type LifecyclePoint struct {
	Time          string   `json:"time"`
	Price         float64  `json:"price"`
	NetPremiumPct *float64 `json:"net_premium_pct,omitempty"`
}

// liveOutOffer is the shape sent to clients — includes computed premium fields.
type liveOutOffer struct {
	ID            int64            `json:"id"`
	Exchange      string           `json:"exchange"`
	Direction     string           `json:"direction"`
	Price         float64          `json:"price"`
	PriceDiffPct  float64          `json:"price_diff_percent"`
	RefPrice      float64          `json:"ref_price"`
	TradeCostPct  float64          `json:"trade_cost_percent"`  // estimated CEX→DEX arbitrage cost as % of trade value
	NetPremiumPct float64          `json:"net_premium_percent"` // PriceDiffPct minus TradeCostPct
	CostBreakdown       CostBreakdown  `json:"cost_breakdown"`               // per-component cost detail (full size)
	CostBreakdownCapped *CostBreakdown `json:"cost_breakdown_capped,omitempty"` // capped to max fiat limit (only set when limit < offer size)
	TradeCostPctCapped  *float64       `json:"trade_cost_percent_capped,omitempty"`
	NetPremiumPctCapped *float64       `json:"net_premium_percent_capped,omitempty"`
	BTCMin        *float64         `json:"btc_min"`
	BTCMax        *float64         `json:"btc_max"`
	FiatMin       *int64           `json:"fiat_min"`
	FiatMax       *int64           `json:"fiat_max"`
	PaymentMethod *string          `json:"payment_method"`
	OfferID       *string          `json:"offer_id"`
	FirstSeen     string           `json:"first_seen"`
	LastSeen      string           `json:"last_seen"`
	IsLive        bool             `json:"is_live"`
	PriceType     *string          `json:"price_type"`
	Lifecycle     []LifecyclePoint `json:"lifecycle,omitempty"`
}

func computePriceDiffPct(offerPrice, refPrice float64) float64 {
	if refPrice <= 0 {
		return 0
	}
	return math.Round((offerPrice/refPrice-1)*10000) / 100
}

func computeDirectionalPremiumPct(direction string, offerPrice, refPrice float64) float64 {
	raw := computePriceDiffPct(offerPrice, refPrice)
	if direction == "sell" {
		return -raw
	}
	return raw
}

func selectBTCTradeSize(btcMax, btcMin *float64) float64 {
	if btcMax != nil && *btcMax > 0 {
		return *btcMax
	}
	if btcMin != nil && *btcMin > 0 {
		return *btcMin
	}
	return defaultBTCTradeSize
}

func computeNetPremiumFields(premiumPct float64, btcSize float64, dexExchange string, direction string) (float64, float64, CostBreakdown) {
	return computeNetPremiumFieldsWithConfirmations(premiumPct, btcSize, dexExchange, direction, 0)
}

func computeNetPremiumFieldsWithConfirmations(premiumPct float64, btcSize float64, dexExchange string, direction string, requiredConfirmations int) (float64, float64, CostBreakdown) {
	return computeNetPremiumFieldsFull(premiumPct, btcSize, 0, dexExchange, direction, requiredConfirmations, nil, "", true)
}

func computeNetPremiumFieldsFull(premiumPct float64, btcSize float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedCEXes []string, feeTier string, coupledHedge bool) (float64, float64, CostBreakdown) {
	if tradeRouteOptimizer == nil || premiumPct <= 0 {
		return 0, 0, CostBreakdown{}
	}

	requiredConfirmations = resolveRequiredConfirmations(direction, requiredConfirmations)
	bd := tradeRouteOptimizer.BestBreakdownFull(btcSize, refPriceEUR, dexExchange, direction, requiredConfirmations, excludedCEXes, feeTier, coupledHedge)
	if bd.RouteCEX == "" {
		return 0, 0, CostBreakdown{}
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
	return ch
}

// unsubscribe removes a client and closes its channel.
func (h *LiveHub) unsubscribe(ch chan []byte) {
	h.subsMu.Lock()
	delete(h.subscribers, ch)
	h.subsMu.Unlock()
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
		tradeCostPct, netPremium, bd := computeNetPremiumFieldsFull(pct, btcSize, price, o.Exchange, o.Direction, sub.requiredConfirmations, excludedCEXes, sub.feeTier, sub.coupledHedge)

		// If a max fiat limit is set and the offer's full size exceeds it, compute a second
		// breakdown capped to that fiat amount so the frontend can show both side by side.
		var bdCapped *CostBreakdown
		var tcCapped, npCapped *float64
		if sub.maxFiatLimitEUR > 0 && price > 0 {
			btcCap := sub.maxFiatLimitEUR / price
			if btcSize > btcCap {
				tc2, np2, bd2 := computeNetPremiumFieldsFull(pct, btcCap, price, o.Exchange, o.Direction, sub.requiredConfirmations, excludedCEXes, sub.feeTier, sub.coupledHedge)
				bdCapped = &bd2
				tcCapped = &tc2
				npCapped = &np2
			}
		}

		result = append(result, liveOutOffer{
			ID:                  o.ID,
			Exchange:            o.Exchange,
			Direction:           o.Direction,
			Price:               o.Price,
			PriceDiffPct:        pct,
			RefPrice:            price,
			TradeCostPct:        tradeCostPct,
			NetPremiumPct:       netPremium,
			CostBreakdown:       bd,
			CostBreakdownCapped: bdCapped,
			TradeCostPctCapped:  tcCapped,
			NetPremiumPctCapped: npCapped,
			BTCMin:              o.BTCMin,
			BTCMax:        o.BTCMax,
			FiatMin:       o.FiatMin,
			FiatMax:       o.FiatMax,
			PaymentMethod: o.PaymentMethod,
			OfferID:       o.OfferID,
			FirstSeen:     o.FirstSeen,
			LastSeen:      o.LastSeen,
			IsLive:        true,
			PriceType:     o.PriceType,
			Lifecycle:     lc[o.ID],
		})
	}
	return result
}

// computePayload builds the JSON payload for a given subscription config.
// Returns nil on marshal error.
func (h *LiveHub) computePayload(price float64, sub liveSubscription) []byte {
	result := h.computeOutOffers(price, sub)
	data, err := json.Marshal(result)
	if err != nil {
		fmt.Printf("[LiveHub] marshal error: %v\n", err)
		return nil
	}
	return data
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
