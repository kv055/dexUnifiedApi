package main

import (
	"bytes"
	"commons/tradecost"
	"database/sql"
	"dexunifiedapi/db"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
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

const internalLiveOffersSQLiteSchema = `
CREATE TABLE offers (
	id INTEGER PRIMARY KEY,
	best_route_cex TEXT,
	best_route_method TEXT,
	hedge_open_fee_btc REAL,
	hedge_close_fee_btc REAL,
	hedge_funding_fee_btc REAL,
	hedge_fee_sum_btc REAL,
	hedge_venue TEXT,
	hedge_instrument TEXT,
	hedge_transfer_minutes INTEGER,
	cex_taker_fee_btc REAL,
	cex_withdraw_fee_btc REAL,
	network_fee_btc REAL,
	dex_fee_btc REAL
);

CREATE TABLE order_lifecycle (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	offer_db_id INTEGER NOT NULL,
	snapshot_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	price REAL NOT NULL,
	ref_price REAL NOT NULL,
	premium_pct REAL NOT NULL,
	trade_cost_pct REAL,
	trade_cost_btc REAL,
	net_premium_pct REAL,
	net_premium_btc REAL,
	btc_amount REAL
);
`

func TestInternalLiveOffersWritesLifecycleBeforeReturning(t *testing.T) {
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

	testSQLDB, err := sql.Open("sqlite", t.TempDir()+"/live-offers.db")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		testSQLDB.Close()
	})
	if _, err := testSQLDB.Exec(internalLiveOffersSQLiteSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := testSQLDB.Exec(`INSERT INTO offers (id) VALUES (1)`); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	db = &dbutil.DB{DB: testSQLDB}

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
	var lifecycleRows int
	if err := testSQLDB.QueryRow(`SELECT COUNT(*) FROM order_lifecycle WHERE offer_db_id = 1`).Scan(&lifecycleRows); err != nil {
		t.Fatalf("count lifecycle rows: %v", err)
	}
	if lifecycleRows != 1 {
		t.Fatalf("lifecycle rows = %d, want 1", lifecycleRows)
	}
	var takerFee, withdrawalFee, networkFee, dexFee float64
	if err := testSQLDB.QueryRow(`
		SELECT cex_taker_fee_btc, cex_withdraw_fee_btc, network_fee_btc, dex_fee_btc
		FROM offers WHERE id = 1
	`).Scan(&takerFee, &withdrawalFee, &networkFee, &dexFee); err != nil {
		t.Fatalf("select offer fees: %v", err)
	}
	assertFloat(t, takerFee, 0.00025)
	assertFloat(t, withdrawalFee, 0.00015)
	assertFloat(t, networkFee, 0.0001)
	assertFloat(t, dexFee, 0.0001)
}
