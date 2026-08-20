package main

import (
	"bytes"
	"commons/tradecost"
	"context"
	"database/sql"
	"dexunifiedapi/internal/testdb"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestRefreshLiveHubLoadsCompleteMultiFiatBookAndRetainsItOnFailure(t *testing.T) {
	oldDB := db
	oldHub := hub
	t.Cleanup(func() {
		db = oldDB
		hub = oldHub
	})

	testDB := testdb.Open(t)
	if _, err := testDB.Exec(`
		INSERT INTO offers (
			id, exchange, fiat_currency, direction, price, payment_methods,
			offer_id, is_live, security_deposit_pct, seller_rating_score,
			seller_rating_confidence, seller_rating_raw
		) VALUES
			(1, 'bisq', 'EUR', 'bid', 61000, ARRAY['SEPA'], 'eur-1', TRUE, 0.15, 91, 'feedback_verified', '{"rating":0.95}'::jsonb),
			(2, 'hodlhodl', 'USD', 'ask', 67000, ARRAY['Wise'], 'usd-1', TRUE, NULL, 88, 'feedback_verified', '{"rating":0.9}'::jsonb),
			(3, 'bisq', 'GBP', 'bid', 52000, ARRAY['Faster Payments'], 'closed-1', FALSE, NULL, NULL, NULL, NULL)
	`); err != nil {
		t.Fatalf("insert offers: %v", err)
	}
	if _, err := testDB.Exec(`
		INSERT INTO order_lifecycle (offer_db_id, price, ref_price, premium_pct, snapshot_time)
		VALUES
			(1, 61000, 60000, 1.67, CURRENT_TIMESTAMP - INTERVAL '1 minute'),
			(1, 61000, 60500, 0.83, CURRENT_TIMESTAMP),
			(2, 67000, 66500, 0.75, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("insert lifecycle: %v", err)
	}

	db = testDB
	hub = &LiveHub{
		lifecycle:   make(map[int64][]LifecyclePoint),
		subscribers: make(map[chan []byte]liveSubscription),
	}
	if err := refreshLiveHub(context.Background(), nil); err != nil {
		t.Fatalf("refresh live hub: %v", err)
	}
	offers := hub.getOffers()
	if len(offers) != 2 {
		t.Fatalf("offers = %d, want 2 live offers", len(offers))
	}
	if offers[0].FiatCurrency != "EUR" || offers[0].ReferencePrice != 60500 {
		t.Fatalf("EUR offer = %#v, want latest EUR lifecycle reference", offers[0])
	}
	if offers[1].FiatCurrency != "USD" || offers[1].ReferencePrice != 66500 {
		t.Fatalf("USD offer = %#v, want USD lifecycle reference", offers[1])
	}

	if err := testDB.Close(); err != nil {
		t.Fatalf("close test database: %v", err)
	}
	if err := refreshLiveHub(context.Background(), nil); err == nil {
		t.Fatal("refresh after database close returned nil error")
	}
	if got := hub.getOffers(); len(got) != 2 || got[0].OfferID == nil || *got[0].OfferID != "eur-1" {
		t.Fatalf("cached offers changed after failed refresh: %#v", got)
	}
}

func TestLiveHubFiltersSubscriptionsByFiatAndUsesStoredReferencePrice(t *testing.T) {
	oldHub := hub
	oldPrices := cexPrices
	t.Cleanup(func() {
		hub = oldHub
		cexPrices = oldPrices
	})
	hub = &LiveHub{
		offers: []LiveOffer{
			{ID: 1, Exchange: "bisq", FiatCurrency: "EUR", Direction: "bid", Price: 61000, ReferencePrice: 60000},
			{ID: 2, Exchange: "hodlhodl", FiatCurrency: "USD", Direction: "ask", Price: 67000, ReferencePrice: 66500},
		},
		lifecycle:   make(map[int64][]LifecyclePoint),
		subscribers: make(map[chan []byte]liveSubscription),
	}
	cexPrices = map[string]float64{"kraken": 60000}

	offers := hub.computeOutOffers(60000, liveSubscription{cex: "kraken", fiat: "USD"})
	if len(offers) != 1 || offers[0].ID != 2 {
		t.Fatalf("USD subscription offers = %#v, want only offer 2", offers)
	}
	if offers[0].RefPrice != 66500 {
		t.Fatalf("USD reference price = %v, want 66500", offers[0].RefPrice)
	}
}

func TestLiveHubCapabilityExclusionsAreIndependentFromSubscription(t *testing.T) {
	h := &LiveHub{subscribers: make(map[chan []byte]liveSubscription)}
	h.setCapabilityExclusions([]string{"kraken"}, []string{"coinbase"})

	spot, hedge := h.capabilityExclusions()
	if strings.Join(spot, ",") != "kraken" || strings.Join(hedge, ",") != "coinbase" {
		t.Fatalf("capability exclusions = spot %v hedge %v", spot, hedge)
	}
	// Re-enabling a provider updates the overlay without reconnecting the client.
	h.setCapabilityExclusions(nil, nil)
	spot, hedge = h.capabilityExclusions()
	if len(spot) != 0 || len(hedge) != 0 {
		t.Fatalf("cleared capability exclusions = spot %v hedge %v", spot, hedge)
	}
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func TestLiveOffersRESTReturnsComputedCostFields(t *testing.T) {
	oldHub := hub
	oldPrices := cexPrices
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	defer func() {
		hub = oldHub
		cexPrices = oldPrices
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
	}()

	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}
	cexPrices = map[string]float64{"kraken": 100}

	est := tradecost.NewFixedEstimatorForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	btcMin := 0.01
	btcMax := 0.1
	hub.offers = []LiveOffer{{
		ID:        1,
		Exchange:  "bisq",
		Direction: "bid",
		Price:     102.5,
		BTCMin:    &btcMin,
		BTCMax:    &btcMax,
		FirstSeen: "2026-04-01 12:00:00",
		LastSeen:  "2026-04-01 12:00:00",
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken", nil)
	rec := httptest.NewRecorder()

	handleLiveOffersREST(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var offers []liveOfferAPI
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}

	offer := offers[0]
	assertFloat(t, offer.Pricing.RawPremiumPercent, 2.5)
	assertFloat(t, offer.Pricing.OfferPrice, 102.5)
	assertFloat(t, offer.Pricing.RefPrice, 100)
	if offer.Pricing.PriceType != "unknown" {
		t.Fatalf("price type = %q, want unknown", offer.Pricing.PriceType)
	}
	assertFloat(t, offer.BTCNetworkFee.BTC, 0.00005)
	assertFloat(t, offer.BTCNetworkFee.Confidence, 0.875)
	if offer.BTCNetworkFee.Method != "onchain_direct" {
		t.Fatalf("network fee method = %q, want onchain_direct", offer.BTCNetworkFee.Method)
	}
	if offer.BTCNetworkFee.RequiredConfirmations != 2 {
		t.Fatalf("required confirmations = %d, want 2", offer.BTCNetworkFee.RequiredConfirmations)
	}
	if offer.BTCNetworkFee.EstimatedArrivalMinutes != 30 {
		t.Fatalf("estimated arrival minutes = %d, want 30", offer.BTCNetworkFee.EstimatedArrivalMinutes)
	}
	if offer.BTCNetworkFee.Detail == nil {
		t.Fatal("expected btc_network_fee.detail")
	}
	assertFloat(t, offer.Analysis.FullVolume.TradeSizeBTC, 0.1)
	assertFloat(t, offer.Analysis.FullVolume.TradeSizeFiat, 10)
	assertFloat(t, offer.Analysis.FullVolume.Premium.TradeCostPercent, 0.55)
	assertFloat(t, offer.Analysis.FullVolume.Premium.NetPercent, 1.95)
	assertFloat(t, offer.Analysis.FullVolume.Profit.BTC, 0.00195)
	assertFloat(t, offer.Analysis.FullVolume.Profit.Fiat, 0.19)
	assertFloat(t, offer.Analysis.FullVolume.Costs.CexTaker.BTC, 0.00025)
	assertFloat(t, offer.Analysis.FullVolume.Costs.Withdrawal.BTC, 0.00015)
	assertFloat(t, offer.Analysis.FullVolume.Costs.Network.Pct, 0.05)
	assertFloat(t, offer.Analysis.FullVolume.Costs.Total.BTC, 0.00055)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.CexBuyTaker.BTC, 0.00025)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.CexSellTaker.BTC, 0.00025)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.Withdrawal.BTC, 0.00015)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.Network.BTC, 0.00005)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.Total.BTC, 0.0007)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.Total.Pct, 0.7)
}

// Non-positive premium skips cost routing, but trade_size must still reflect the
// offer's volume_limits — otherwise the live Size column renders 0€.
func TestLiveOffersRESTPreservesTradeSizeWhenPremiumNonPositive(t *testing.T) {
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	oldHubOffers := hub.getOffers()
	oldKrakenPrice := getCexPrice("kraken")
	defer func() {
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
		setCexPrice("kraken", oldKrakenPrice)
		hub.setOffers(oldHubOffers)
	}()

	setCexPrice("kraken", 100)
	est := tradecost.NewFixedEstimatorForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	btcMin := 0.01
	btcMax := 0.1
	fiatMax := int64(10)
	hub.setOffers([]LiveOffer{{
		ID:        1,
		Exchange:  "bisq",
		Direction: "bid",
		Price:     98, // below ref → negative premium → no cost route
		BTCMin:    &btcMin,
		BTCMax:    &btcMax,
		FiatMax:   &fiatMax,
		FirstSeen: "2026-04-01 12:00:00",
		LastSeen:  "2026-04-01 12:00:00",
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken", nil)
	rec := httptest.NewRecorder()
	handleLiveOffersREST(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var offers []liveOfferAPI
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}
	offer := offers[0]
	if offer.Pricing.RawPremiumPercent > 0 {
		t.Fatalf("raw premium = %v, want <= 0", offer.Pricing.RawPremiumPercent)
	}
	assertFloat(t, offer.Analysis.FullVolume.TradeSizeBTC, 0.1)
	assertFloat(t, offer.Analysis.FullVolume.TradeSizeFiat, 10)
	if offer.Analysis.FullVolume.Route.CEX != "" {
		t.Fatalf("route cex = %q, want empty when premium non-positive", offer.Analysis.FullVolume.Route.CEX)
	}
	assertFloat(t, offer.Analysis.FullVolume.Premium.NetPercent, 0)
}

func TestLiveOffersRESTIncludesSellerRating(t *testing.T) {
	oldHub := hub
	oldPrices := cexPrices
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	defer func() {
		hub = oldHub
		cexPrices = oldPrices
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
	}()

	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}
	cexPrices = map[string]float64{"kraken": 100}

	est := tradecost.NewFixedEstimatorForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	btcMin := 0.01
	btcMax := 0.1
	score := 82.5
	conf := "feedback_verified"
	hub.offers = []LiveOffer{{
		ID:                     1,
		Exchange:               "bisq",
		Direction:              "bid",
		Price:                  102.5,
		BTCMin:                 &btcMin,
		BTCMax:                 &btcMax,
		FirstSeen:              "2026-04-01 12:00:00",
		LastSeen:               "2026-04-01 12:00:00",
		SellerRatingScore:      &score,
		SellerRatingConfidence: &conf,
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken", nil)
	rec := httptest.NewRecorder()
	handleLiveOffersREST(rec, req)

	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := raw[0]["seller_rating_score"]; !ok {
		t.Fatalf("seller_rating_score missing from live offers API response: %s", rec.Body.String())
	}
	if _, ok := raw[0]["seller_rating_confidence"]; !ok {
		t.Fatalf("seller_rating_confidence missing from live offers API response: %s", rec.Body.String())
	}
}

func TestLiveOffersRESTUsesRealSecurityDepositFromOffer(t *testing.T) {
	oldHub := hub
	oldPrices := cexPrices
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	defer func() {
		hub = oldHub
		cexPrices = oldPrices
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
	}()

	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}
	cexPrices = map[string]float64{"kraken": 100}

	est := tradecost.NewFixedEstimatorForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	btcMin := 0.01
	btcMax := 0.1
	// A real measured deposit (18%) different from the 15% bisq estimate —
	// the real value must win and DepositEstimated must be false.
	realDepositPct := 0.18
	hub.offers = []LiveOffer{{
		ID:                 1,
		Exchange:           "bisq",
		Direction:          "bid",
		Price:              102.5,
		BTCMin:             &btcMin,
		BTCMax:             &btcMax,
		FirstSeen:          "2026-04-01 12:00:00",
		LastSeen:           "2026-04-01 12:00:00",
		SecurityDepositPct: &realDepositPct,
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken", nil)
	rec := httptest.NewRecorder()

	handleLiveOffersREST(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var offers []liveOfferAPI
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}

	cap := offers[0].Analysis.FullVolume.Capital
	assertFloat(t, cap.SecurityDepositPct, 0.18)
	assertFloat(t, cap.SecurityDepositBTC, 0.018) // 0.1 * 0.18
	assertFloat(t, cap.SecurityDepositFiat, 1.8)  // 0.018 * 100
	if cap.DepositEstimated {
		t.Fatal("DepositEstimated = true, want false for a real measured deposit")
	}
}

func TestLiveOffersRESTIncludesHedgelessDoubleCollateral(t *testing.T) {
	oldHub := hub
	oldPrices := cexPrices
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	defer func() {
		hub = oldHub
		cexPrices = oldPrices
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
	}()

	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}
	cexPrices = map[string]float64{"kraken": 100}

	const hedgeFeeBTC = 0.0002
	est := tradecost.NewFixedEstimatorWithHedgeForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, hedgeFeeBTC)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	btcMin := 0.01
	btcMax := 0.1
	realDepositPct := 0.18
	hub.offers = []LiveOffer{{
		ID:                 1,
		Exchange:           "bisq",
		Direction:          "bid",
		Price:              102.5,
		BTCMin:             &btcMin,
		BTCMax:             &btcMax,
		FirstSeen:          "2026-04-01 12:00:00",
		LastSeen:           "2026-04-01 12:00:00",
		SecurityDepositPct: &realDepositPct,
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken", nil)
	rec := httptest.NewRecorder()
	handleLiveOffersREST(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var offers []liveOfferAPI
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}

	full := offers[0].Analysis.FullVolume
	if full.HedgelessDoubleCollateral == nil {
		t.Fatal("hedgeless_double_collateral missing from full_volume")
	}
	hl := full.HedgelessDoubleCollateral

	assertFloat(t, hl.Costs.Total.BTC, full.Costs.Total.BTC-hedgeFeeBTC+full.Costs.Network.BTC)
	if hl.Route.Settlement != "Hub_main2main" || hl.NumNetworkTxs != 2 {
		t.Fatalf("hedgeless route = %+v / %d txs", hl.Route, hl.NumNetworkTxs)
	}
	assertFloat(t, hl.Costs.Hedge.FeeSumBTC, 0)
	if hl.Capital.TradeMode != tradecost.TradeModeHedgelessDoubleCollateral {
		t.Fatalf("hedgeless trade_mode = %q", hl.Capital.TradeMode)
	}
	assertFloat(t, hl.Capital.HedgeCollateral, 0)
	assertFloat(t, hl.Capital.BTCLiquidityBTC, 0.118) // 0.1 + 0.018
	assertFloat(t, hl.Capital.FiatCollateral, 10)     // position × 100
	// total = (pos+deposit)*price + pos*price = 11.8 + 10 = 21.8
	assertFloat(t, hl.Capital.TotalFiat, 21.8)
	assertFloat(t, hl.Capital.TotalFiat, 2*hl.Capital.SpotNotional+hl.Capital.SecurityDepositFiat)
	if full.Capital.TradeMode != tradecost.TradeModeHedgefullSingleCollateral {
		t.Fatalf("hedgefull trade_mode = %q", full.Capital.TradeMode)
	}
}

func TestAskScenarioOmitsHedgelessExecutionVariant(t *testing.T) {
	bd := tradecost.CostBreakdown{BTCAmount: 0.01, RouteCEX: "kraken", RouteDEX: "bisq", TradeDirection: "ask"}
	scenario := buildScenarioFromBreakdown(bd, 100, nil, 0, 0, tradecost.ManualRouteOptions{})
	if scenario.HedgelessDoubleCollateral != nil {
		t.Fatal("ask-side scenario must not advertise hedgeless execution")
	}
}

func TestLiveOffersRESTAttachesHedgelessOnlyWhenHedgefullUnavailable(t *testing.T) {
	oldHub := hub
	oldPrices := cexPrices
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	defer func() {
		hub = oldHub
		cexPrices = oldPrices
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
	}()

	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}
	cexPrices = map[string]float64{"kraken": 100}

	est := tradecost.NewHedgelessOnlyEstimatorForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	btcMin := 0.01
	btcMax := 0.1
	hub.offers = []LiveOffer{{
		ID:        1,
		Exchange:  "bisq",
		Direction: "bid",
		Price:     102.5,
		BTCMin:    &btcMin,
		BTCMax:    &btcMax,
		FirstSeen: "2026-04-01 12:00:00",
		LastSeen:  "2026-04-01 12:00:00",
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken", nil)
	rec := httptest.NewRecorder()
	handleLiveOffersREST(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var offers []liveOfferAPI
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}

	full := offers[0].Analysis.FullVolume
	if full.Route.CEX != "" {
		t.Fatalf("hedgefull route cex = %q, want empty when hedges unavailable", full.Route.CEX)
	}
	if full.HedgelessDoubleCollateral == nil {
		t.Fatal("expected hedgeless_double_collateral when only spot routing works")
	}
	hl := full.HedgelessDoubleCollateral
	if hl.Route.CEX != "kraken" {
		t.Fatalf("hedgeless route cex = %q, want kraken", hl.Route.CEX)
	}
	if hl.Premium.NetPercent <= 0 {
		t.Fatalf("hedgeless net premium = %v, want positive", hl.Premium.NetPercent)
	}
	if hl.Capital.TradeMode != tradecost.TradeModeHedgelessDoubleCollateral {
		t.Fatalf("hedgeless trade_mode = %q", hl.Capital.TradeMode)
	}
}

func TestHandleLiveOffersRESTIncludesCappedProfitFields(t *testing.T) {
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	oldHubOffers := hub.getOffers()
	oldRoutingSettings := hub.transactionalRoutingSettings()
	oldKrakenPrice := getCexPrice("kraken")
	defer func() {
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
		setCexPrice("kraken", oldKrakenPrice)
		hub.setOffers(oldHubOffers)
		hub.setTransactionalRoutingSettings(oldRoutingSettings)
	}()

	setCexPrice("kraken", 100)

	est := tradecost.NewFixedEstimatorForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)
	capAmount := 5.0
	hub.setTransactionalRoutingSettings(transactionalRoutingSettings{AmountSource: "capped_volume", AmountFiat: &capAmount, FeeTier: "economic"})

	btcMin := 0.02
	btcMax := 0.1
	hub.setOffers([]LiveOffer{{
		ID:        1,
		Exchange:  "bisq",
		Direction: "bid",
		Price:     102.5,
		BTCMin:    &btcMin,
		BTCMax:    &btcMax,
		FirstSeen: "2026-04-01 12:00:00",
		LastSeen:  "2026-04-01 12:00:00",
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken", nil)
	rec := httptest.NewRecorder()

	handleLiveOffersREST(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var offers []liveOfferAPI
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}

	offer := offers[0]
	if offer.Analysis.CappedVolume == nil {
		t.Fatal("expected capped cost breakdown")
	}
	assertFloat(t, offer.BTCNetworkFee.BTC, 0.00005)
	if offer.Analysis.CappedVolume.CapFiat == nil {
		t.Fatal("expected capped volume cap_fiat")
	}
	assertFloat(t, *offer.Analysis.CappedVolume.CapFiat, 5)
	assertFloat(t, offer.Analysis.CappedVolume.TradeSizeBTC, 0.05)
	assertFloat(t, offer.Analysis.CappedVolume.TradeSizeFiat, 5)
	assertFloat(t, offer.Analysis.CappedVolume.Premium.TradeCostPercent, 0.75)
	assertFloat(t, offer.Analysis.CappedVolume.Premium.NetPercent, 1.75)
	assertFloat(t, offer.Analysis.CappedVolume.Profit.BTC, 0.000875)
	assertFloat(t, offer.Analysis.CappedVolume.Profit.Fiat, 0.09)
	assertFloat(t, offer.Analysis.CappedVolume.Costs.Network.Pct, 0.1)
	assertFloat(t, offer.Analysis.CappedVolume.AbortCost.Total.BTC, 0.00045)
	assertFloat(t, offer.Analysis.CappedVolume.AbortCost.Total.Pct, 0.9)
}

func TestHandleLiveOffersRESTIncludesMinVolumeScenario(t *testing.T) {
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	oldHubOffers := hub.getOffers()
	oldKrakenPrice := getCexPrice("kraken")
	defer func() {
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
		setCexPrice("kraken", oldKrakenPrice)
		hub.setOffers(oldHubOffers)
	}()

	setCexPrice("kraken", 100)

	est := tradecost.NewFixedEstimatorForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	btcMin := 0.02
	btcMax := 0.1
	hub.setOffers([]LiveOffer{{
		ID:        1,
		Exchange:  "bisq",
		Direction: "bid",
		Price:     102.5,
		BTCMin:    &btcMin,
		BTCMax:    &btcMax,
		FirstSeen: "2026-04-01 12:00:00",
		LastSeen:  "2026-04-01 12:00:00",
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken", nil)
	rec := httptest.NewRecorder()

	handleLiveOffersREST(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var offers []liveOfferAPI
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}

	offer := offers[0]
	if offer.Analysis.MinVolume == nil {
		t.Fatal("expected min-volume scenario")
	}
	if offer.Analysis.MinVolume.CapFiat != nil {
		t.Fatalf("min volume cap_fiat = %v, want nil", *offer.Analysis.MinVolume.CapFiat)
	}
	assertFloat(t, offer.Analysis.MinVolume.TradeSizeBTC, 0.02)
	assertFloat(t, offer.Analysis.MinVolume.TradeSizeFiat, 2)
	// Fixed per-trade fees weigh more at the min size: withdrawal 0.00015 BTC
	// (0.75%) + network 0.00005 BTC (0.25%) + taker 0.25% + dex 0.1% = 1.35%.
	assertFloat(t, offer.Analysis.MinVolume.Premium.TradeCostPercent, 1.35)
	assertFloat(t, offer.Analysis.MinVolume.Premium.NetPercent, 1.15)
	assertFloat(t, offer.Analysis.MinVolume.Profit.BTC, 0.00023)
	assertFloat(t, offer.Analysis.MinVolume.Profit.Fiat, 0.02)

	// The full-size scenario stays untouched, and no fiat cap was requested.
	assertFloat(t, offer.Analysis.FullVolume.TradeSizeBTC, 0.1)
	if offer.Analysis.CappedVolume != nil {
		t.Fatal("expected no capped scenario without a max fiat limit")
	}
}

func TestHandleLiveOffersRESTOmitsMinVolumeWhenMinEqualsFullSize(t *testing.T) {
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	oldHubOffers := hub.getOffers()
	oldKrakenPrice := getCexPrice("kraken")
	defer func() {
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
		setCexPrice("kraken", oldKrakenPrice)
		hub.setOffers(oldHubOffers)
	}()

	setCexPrice("kraken", 100)

	est := tradecost.NewFixedEstimatorForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	btcMin := 0.1
	btcMax := 0.1
	hub.setOffers([]LiveOffer{{
		ID:        1,
		Exchange:  "bisq",
		Direction: "bid",
		Price:     102.5,
		BTCMin:    &btcMin,
		BTCMax:    &btcMax,
		FirstSeen: "2026-04-01 12:00:00",
		LastSeen:  "2026-04-01 12:00:00",
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken", nil)
	rec := httptest.NewRecorder()

	handleLiveOffersREST(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var offers []liveOfferAPI
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}
	if offers[0].Analysis.MinVolume != nil {
		t.Fatal("expected no min-volume scenario when the min equals the full size")
	}
}

func TestHandleLiveOfferLifecycleRESTReturnsLifecyclePoints(t *testing.T) {
	oldLifecycle := hub.lifecycle
	defer func() {
		hub.lifecycle = oldLifecycle
	}()

	value := 1.23
	hub.lifecycle = map[int64][]LifecyclePoint{
		7: {
			{Time: "2026-04-19 10:00:00", Price: 101.5, NetPremiumPct: &value},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/live_offers/{id}/lifecycle", handleLiveOfferLifecycleREST)

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers/7/lifecycle", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var points []LifecyclePoint
	if err := json.Unmarshal(rec.Body.Bytes(), &points); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("point count = %d, want 1", len(points))
	}
	if points[0].Time != "2026-04-19 10:00:00" {
		t.Fatalf("time = %q, want %q", points[0].Time, "2026-04-19 10:00:00")
	}
	assertFloat(t, points[0].Price, 101.5)
	if points[0].NetPremiumPct == nil {
		t.Fatal("expected net premium pct")
	}
	assertFloat(t, *points[0].NetPremiumPct, 1.23)
}

func TestInternalLiveOffersRequiresSecret(t *testing.T) {
	old := internalAPISecret
	internalAPISecret = "topsecret"
	t.Cleanup(func() { internalAPISecret = old })

	// Missing header → 401, before any body decode or DB access.
	req := httptest.NewRequest(http.MethodPost, "/internal/live_offers", bytes.NewReader([]byte(`[]`)))
	rec := httptest.NewRecorder()
	handleInternalLiveOffers(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing secret: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// Wrong header → 401.
	req = httptest.NewRequest(http.MethodPost, "/internal/live_offers", bytes.NewReader([]byte(`[]`)))
	req.Header.Set("X-Internal-Secret", "wrong")
	rec = httptest.NewRecorder()
	handleInternalLiveOffers(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// Correct header passes the gate; invalid JSON then yields 400 (not 401),
	// proving the secret check let the request through without needing the DB.
	req = httptest.NewRequest(http.MethodPost, "/internal/live_offers", bytes.NewReader([]byte(`not-json`)))
	req.Header.Set("X-Internal-Secret", "topsecret")
	rec = httptest.NewRecorder()
	handleInternalLiveOffers(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("correct secret + bad body: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestInternalLiveOffersWSGate covers the secret-gated service-to-service WS
// route. The browser-facing /ws/live_offers stays session-gated; internal
// callers (e.g. mm-controller's premium-offer watcher) authenticate with
// X-Internal-Secret instead.
func TestInternalLiveOffersWSGate(t *testing.T) {
	oldSecret := internalAPISecret
	oldHub := hub
	internalAPISecret = "topsecret"
	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}
	t.Cleanup(func() {
		internalAPISecret = oldSecret
		hub = oldHub
	})

	handler := internalSecretGate(http.HandlerFunc(handleLiveOffersWS))
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/internal/ws/live_offers"

	// Missing header → 401 before the WS upgrade.
	dialer := websocket.Dialer{}
	_, resp, err := dialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatalf("missing secret: dial succeeded, want failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing secret: status = %v, want %d", respStatus(resp), http.StatusUnauthorized)
	}
	resp.Body.Close()

	// Wrong header → 401.
	_, resp, err = dialer.Dial(wsURL, http.Header{"X-Internal-Secret": {"wrong"}})
	if err == nil {
		t.Fatalf("wrong secret: dial succeeded, want failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong secret: status = %v, want %d", respStatus(resp), http.StatusUnauthorized)
	}
	resp.Body.Close()

	// Correct header → upgrade completes (HTTP 101) and the connection is live.
	conn, resp, err := dialer.Dial(wsURL, http.Header{"X-Internal-Secret": {"topsecret"}})
	if err != nil {
		t.Fatalf("correct secret: dial failed: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("correct secret: status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}
	conn.Close()
}

func respStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func TestInternalLiveOffersEnrichesLifecycleBeforeReturning(t *testing.T) {
	oldDB := db
	oldHub := hub
	oldPrices := cexPrices
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	t.Cleanup(func() {
		db = oldDB
		hub = oldHub
		cexPrices = oldPrices
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
	})

	testDB := testdb.Open(t)
	if _, err := testDB.Exec(`
		INSERT INTO offers (id, exchange, fiat_currency, direction, price, btc_min, btc_max, is_live)
		VALUES (1, 'bisq', 'EUR', 'bid', 102.5, 0.01, 0.1, TRUE)
	`); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	// The snapshot job writes the baseline row; the push enriches it in place.
	if _, err := testDB.Exec(`
		INSERT INTO order_lifecycle (offer_db_id, price, ref_price, premium_pct)
		VALUES (1, 102.5, 100, 2.5)
	`); err != nil {
		t.Fatalf("insert baseline lifecycle: %v", err)
	}
	db = testDB

	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}
	cexPrices = map[string]float64{"kraken": 100}
	est := tradecost.NewFixedEstimatorForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*tradecost.TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(tradeCostEstimators)

	btcMin := 0.01
	btcMax := 0.1
	payload, err := json.Marshal([]LiveOffer{{
		ID:        1,
		Exchange:  "bisq",
		Direction: "bid",
		Price:     102.5,
		BTCMin:    &btcMin,
		BTCMax:    &btcMax,
		FirstSeen: "2026-04-01 12:00:00",
		LastSeen:  "2026-04-01 12:00:00",
	}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/live_offers", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	handleInternalLiveOffers(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	// Enriched in place — still one row, with cost fields now filled and premium
	// untouched (net derived from the stored premium).
	var lifecycleRows int
	if err := testDB.QueryRow(`SELECT COUNT(*) FROM order_lifecycle WHERE offer_db_id = ?`, 1).Scan(&lifecycleRows); err != nil {
		t.Fatalf("count lifecycle rows: %v", err)
	}
	if lifecycleRows != 1 {
		t.Fatalf("lifecycle rows = %d, want 1", lifecycleRows)
	}
	var premium float64
	var tradeCostPct, netPremiumPct sql.NullFloat64
	if err := testDB.QueryRow(`
		SELECT premium_pct, trade_cost_pct, net_premium_pct FROM order_lifecycle WHERE offer_db_id = ?
	`, 1).Scan(&premium, &tradeCostPct, &netPremiumPct); err != nil {
		t.Fatalf("select enriched lifecycle: %v", err)
	}
	assertFloat(t, premium, 2.5)
	if !tradeCostPct.Valid || !netPremiumPct.Valid {
		t.Fatalf("baseline row not enriched: trade_cost=%v net=%v", tradeCostPct, netPremiumPct)
	}
	var takerFee, withdrawalFee, networkFee, dexFee float64
	if err := testDB.QueryRow(`
		SELECT cex_taker_fee_btc, cex_withdraw_fee_btc, routing_fee_sum, dex_fee_btc
		FROM offers WHERE id = ?
	`, 1).Scan(&takerFee, &withdrawalFee, &networkFee, &dexFee); err != nil {
		t.Fatalf("select offer fees: %v", err)
	}
	assertFloat(t, takerFee, 0.00025)
	assertFloat(t, withdrawalFee, 0.00015)
	assertFloat(t, networkFee, 0.00005) // bisq bid is a single on-chain tx
	assertFloat(t, dexFee, 0.0001)
}

func TestInternalLiveOffersAcceptsRefreshTrigger(t *testing.T) {
	oldDB := db
	oldHub := hub
	t.Cleanup(func() {
		db = oldDB
		hub = oldHub
	})
	testDB := testdb.Open(t)
	if _, err := testDB.Exec(`
		INSERT INTO offers (id, exchange, fiat_currency, direction, price, is_live, maker_id)
		VALUES (11, 'peachbitcoin', 'EUR', 'bid', 61000, TRUE, 'peach-maker-1')
	`); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	db = testDB
	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}

	req := httptest.NewRequest(http.MethodPost, "/internal/live_offers", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	handleInternalLiveOffers(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	offers := hub.getOffers()
	if len(offers) != 1 || offers[0].MakerID != "peach-maker-1" {
		t.Fatalf("hub offers = %#v, want peach maker id from postgres", offers)
	}
}

func TestLiveHubKeepsLastPayloadOnMarshalError(t *testing.T) {
	oldHub := hub
	t.Cleanup(func() { hub = oldHub })
	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}
	hub.setOffers([]LiveOffer{{
		ID: 1, Exchange: "bisq", FiatCurrency: "EUR", Direction: "bid",
		Price: 100, ReferencePrice: 100,
	}})
	sub := liveSubscription{cex: "kraken", fiat: "EUR"}
	first := hub.computePayload(100, sub)
	if first == nil || !bytes.Contains(first, []byte(`"id":1`)) {
		t.Fatalf("first payload = %s", first)
	}
	hub.setOffers([]LiveOffer{{
		ID: 1, Exchange: "bisq", FiatCurrency: "EUR", Direction: "bid",
		Price: math.Inf(1), ReferencePrice: 100,
	}})
	second := hub.computePayload(100, sub)
	if !bytes.Equal(first, second) {
		t.Fatalf("marshal failure replaced last payload")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?fiat=EUR", nil)
	rec := httptest.NewRecorder()
	handleLiveOffersREST(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want cached 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), first) {
		t.Fatalf("REST body changed after marshal failure")
	}
}

func TestLiveOffersRESTReturns503WhenPayloadUnavailable(t *testing.T) {
	oldHub := hub
	t.Cleanup(func() { hub = oldHub })
	hub = &LiveHub{subscribers: map[chan []byte]liveSubscription{}}
	hub.setOffers([]LiveOffer{{
		ID: 1, Exchange: "bisq", FiatCurrency: "EUR", Direction: "bid",
		Price: math.Inf(1), ReferencePrice: 100,
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?fiat=EUR", nil)
	rec := httptest.NewRecorder()
	handleLiveOffersREST(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if rec.Body.String() == "[]\n" || rec.Body.String() == "[]" {
		t.Fatal("REST marshal failure returned empty offer list")
	}
}
