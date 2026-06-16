package main

import (
	"bytes"
	"commons/tradecost"
	"database/sql"
	"dexunifiedapi/internal/testdb"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
		Direction: "buy",
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
	assertFloat(t, offer.BTCNetworkFee.BTC, 0.0001)
	assertFloat(t, offer.BTCNetworkFee.Confidence, 0.875)
	if offer.BTCNetworkFee.Method != "onchain" {
		t.Fatalf("network fee method = %q, want onchain", offer.BTCNetworkFee.Method)
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
	assertFloat(t, offer.Analysis.FullVolume.Premium.TradeCostPercent, 0.6)
	assertFloat(t, offer.Analysis.FullVolume.Premium.NetPercent, 1.9)
	assertFloat(t, offer.Analysis.FullVolume.Profit.BTC, 0.0019)
	assertFloat(t, offer.Analysis.FullVolume.Profit.Fiat, 0.19)
	assertFloat(t, offer.Analysis.FullVolume.Costs.CexTaker.BTC, 0.00025)
	assertFloat(t, offer.Analysis.FullVolume.Costs.Withdrawal.BTC, 0.00015)
	assertFloat(t, offer.Analysis.FullVolume.Costs.Network.Pct, 0.1)
	assertFloat(t, offer.Analysis.FullVolume.Costs.Total.BTC, 0.0006)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.CexBuyTaker.BTC, 0.00025)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.CexSellTaker.BTC, 0.00025)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.Withdrawal.BTC, 0.00015)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.Network.BTC, 0.0001)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.Total.BTC, 0.00075)
	assertFloat(t, offer.Analysis.FullVolume.AbortCost.Total.Pct, 0.75)
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
		Direction:          "buy",
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
	assertFloat(t, cap.SecurityDepositEUR, 1.8)   // 0.018 * 100
	if cap.DepositEstimated {
		t.Fatal("DepositEstimated = true, want false for a real measured deposit")
	}
}

func TestHandleLiveOffersRESTIncludesCappedProfitFields(t *testing.T) {
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
		Direction: "buy",
		Price:     102.5,
		BTCMin:    &btcMin,
		BTCMax:    &btcMax,
		FirstSeen: "2026-04-01 12:00:00",
		LastSeen:  "2026-04-01 12:00:00",
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/live_offers?cex=kraken&max_fiat_limit=5", nil)
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
	assertFloat(t, offer.BTCNetworkFee.BTC, 0.0001)
	if offer.Analysis.CappedVolume.CapFiat == nil {
		t.Fatal("expected capped volume cap_fiat")
	}
	assertFloat(t, *offer.Analysis.CappedVolume.CapFiat, 5)
	assertFloat(t, offer.Analysis.CappedVolume.TradeSizeBTC, 0.05)
	assertFloat(t, offer.Analysis.CappedVolume.TradeSizeFiat, 5)
	assertFloat(t, offer.Analysis.CappedVolume.Premium.TradeCostPercent, 0.85)
	assertFloat(t, offer.Analysis.CappedVolume.Premium.NetPercent, 1.65)
	assertFloat(t, offer.Analysis.CappedVolume.Profit.BTC, 0.000825)
	assertFloat(t, offer.Analysis.CappedVolume.Profit.Fiat, 0.08)
	assertFloat(t, offer.Analysis.CappedVolume.Costs.Network.Pct, 0.2)
	assertFloat(t, offer.Analysis.CappedVolume.AbortCost.Total.BTC, 0.0005)
	assertFloat(t, offer.Analysis.CappedVolume.AbortCost.Total.Pct, 1)
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
		INSERT INTO offers (id, exchange, fiat_currency, direction, price)
		VALUES (1, 'bisq', 'EUR', 'buy', 102.5)
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
		Direction: "buy",
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
		SELECT cex_taker_fee_btc, cex_withdraw_fee_btc, network_fee_btc, dex_fee_btc
		FROM offers WHERE id = ?
	`, 1).Scan(&takerFee, &withdrawalFee, &networkFee, &dexFee); err != nil {
		t.Fatalf("select offer fees: %v", err)
	}
	assertFloat(t, takerFee, 0.00025)
	assertFloat(t, withdrawalFee, 0.00015)
	assertFloat(t, networkFee, 0.0001)
	assertFloat(t, dexFee, 0.0001)
}
