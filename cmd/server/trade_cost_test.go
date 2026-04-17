package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func seededCachedFee(value float64) *cachedFee {
	return &cachedFee{
		value:     value,
		fetchedAt: time.Now(),
		ttl:       feeTTL,
	}
}

func fixedCostEstimator(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC float64) *TradeCostEstimator {
	withdrawal := newWithdrawalFeeFetcher(exchange)
	withdrawal.caches[MethodOnchain] = seededCachedFee(withdrawalFeeBTC)
	withdrawal.caches[MethodLightning] = seededCachedFee(withdrawalFeeBTC)

	return &TradeCostEstimator{
		exchange:   exchange,
		taker:      &takerFeeFetcher{exchange: exchange, cache: seededCachedFee(takerFeeFrac)},
		withdrawal: withdrawal,
		dexFees: map[string]*cachedFee{
			dexExchange: seededCachedFee(dexFeeFrac),
		},
		cexProfile: cexProfileFor(exchange),
		networkFeeOverride: func(method WithdrawalMethod, btcAmount float64) networkFeeDetails {
			return networkFeeDetails{
				feeBTC:     networkFeeBTC,
				method:     method,
				tier:       "standard",
				confidence: 0.875,
				detail: &BTCNetworkFeeDetail{
					SelectedFullConfirmationMinutes: 30,
				},
			}
		},
		hedgeCostOverride: func(btcAmount float64, refPriceEUR float64, transferMinutes float64) hedgeCostDetails {
			return hedgeCostDetails{}
		},
	}
}

func fixedCostEstimatorWithHedge(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC, hedgeFeeBTC float64) *TradeCostEstimator {
	est := fixedCostEstimator(exchange, dexExchange, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC)
	est.hedgeCostOverride = func(btcAmount float64, refPriceEUR float64, transferMinutes float64) hedgeCostDetails {
		return hedgeCostDetails{
			feeBTC:          hedgeFeeBTC,
			feePct:          hedgeFeeBTC / btcAmount * 100,
			venue:           "coinbase_futures",
			instrument:      "BTC-PERP",
			transferMinutes: transferMinutes,
		}
	}
	return est
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func TestTradeCostEstimatorCostPctUsesAllComponents(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)

	got := est.CostPct(0.1, "bisq")

	assertFloat(t, got, 0.55)
}

func TestTradeCostEstimatorBreakdownMatchesComponentSum(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)

	bd := est.Breakdown(0.1, "bisq", "buy")

	assertFloat(t, bd.BTCAmount, 0.1)
	assertFloat(t, bd.CexTakerFeeBTC, 0.00025)
	assertFloat(t, bd.CexTakerFeePct, 0.25)
	assertFloat(t, bd.WithdrawalFeeBTC, 0.00015)
	assertFloat(t, bd.WithdrawalFeePct, 0.15)
	assertFloat(t, bd.NetworkFeeBTC, 0.0001) // 2 txs × 0.00005
	assertFloat(t, bd.NetworkFeePct, 0.1)
	assertFloat(t, bd.DexFeeBTC, 0.0001)
	assertFloat(t, bd.DexFeePct, 0.1)
	assertFloat(t, bd.TotalCostBTC, 0.0006)
	assertFloat(t, bd.TotalCostPct, 0.6)
	if bd.NetworkFeeMethod != "onchain" {
		t.Fatalf("network method = %q, want onchain", bd.NetworkFeeMethod)
	}
	if bd.NetworkFeeConfidence != 0.875 {
		t.Fatalf("network confidence = %.12f, want 0.875", bd.NetworkFeeConfidence)
	}
}

func TestTradeCostEstimatorBreakdownIncludesHedgeCostOnBidSide(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	bd := est.Breakdown(0.1, "bisq", "buy")

	assertFloat(t, bd.Hedge.FeeSumBTC, 0.0002)
	assertFloat(t, bd.Hedge.FeeSumPct, 0.2)
	assertFloat(t, bd.TotalCostBTC, 0.0008)
	assertFloat(t, bd.TotalCostPct, 0.8)
	if bd.Hedge.Venue != "coinbase_futures" {
		t.Fatalf("hedge venue = %q, want coinbase_futures", bd.Hedge.Venue)
	}
	if bd.Hedge.TransferMinutes != 60 {
		t.Fatalf("hedge transfer minutes = %d, want 60", bd.Hedge.TransferMinutes)
	}
}

func TestTradeCostEstimatorDoesNotIncludeHedgeCostOnAskSide(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	bd := est.Breakdown(0.1, "bisq", "sell")

	assertFloat(t, bd.Hedge.FeeSumBTC, 0)
	assertFloat(t, bd.Hedge.FeeSumPct, 0)
}

func TestTradeCostEstimatorBreakdownRoundsBTCAndPercentFields(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq", 0.00123456789, 0.000000004, 0.00098765432, 0.000000006)

	bd := est.Breakdown(0.123456789, "bisq", "buy")

	assertFloat(t, bd.BTCAmount, 0.12345679)
	assertFloat(t, bd.CexTakerFeeBTC, 0.00015242)
	assertFloat(t, bd.WithdrawalFeeBTC, 0)
	assertFloat(t, bd.NetworkFeeBTC, 0.00000001)
	assertFloat(t, bd.DexFeeBTC, 0.00012193)
	assertFloat(t, bd.CexTakerFeePct, 0.1235)
	assertFloat(t, bd.DexFeePct, 0.0988)
	assertFloat(t, bd.TotalCostPct, 0.2222)
}

func TestTradeCostEstimatorZeroAmountReturnsEmptyCost(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)

	assertFloat(t, est.CostPct(0, "bisq"), 0)
	if bd := est.Breakdown(0, "bisq", "buy"); bd != (CostBreakdown{}) {
		t.Fatalf("zero amount breakdown = %#v, want empty breakdown", bd)
	}
}

func TestSelectBTCTradeSizePrefersMaxThenMinThenDefault(t *testing.T) {
	min := 0.02
	max := 0.05

	assertFloat(t, selectBTCTradeSize(&max, &min), 0.05)
	assertFloat(t, selectBTCTradeSize(nil, &min), 0.02)
	assertFloat(t, selectBTCTradeSize(nil, nil), defaultBTCTradeSize)
}

func TestDirectionalPremiumPct(t *testing.T) {
	assertFloat(t, computeDirectionalPremiumPct("buy", 105, 100), 5)
	assertFloat(t, computeDirectionalPremiumPct("sell", 95, 100), 5)
	assertFloat(t, computeDirectionalPremiumPct("buy", 100, 0), 0)
}

func TestComputeNetPremiumFields(t *testing.T) {
	oldOptimizer := tradeRouteOptimizer
	oldEstimators := tradeCostEstimators
	defer func() {
		tradeRouteOptimizer = oldOptimizer
		tradeCostEstimators = oldEstimators
	}()

	est := fixedCostEstimator("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = NewTradeRouteOptimizer(tradeCostEstimators)

	tradeCostPct, netPremium, bd := computeNetPremiumFields(2.5, 0.1, "bisq", "buy")

	assertFloat(t, tradeCostPct, 0.6)
	assertFloat(t, netPremium, 1.9)
	assertFloat(t, bd.EstimatedProfitPct, 1.9)
	assertFloat(t, bd.EstimatedProfitBTC, 0.0019)

	tradeCostPct, netPremium, bd = computeNetPremiumFields(-1, 0.1, "bisq", "buy")
	assertFloat(t, tradeCostPct, 0)
	assertFloat(t, netPremium, 0)
	if bd != (CostBreakdown{}) {
		t.Fatalf("negative premium breakdown = %#v, want empty breakdown", bd)
	}
}

func TestCachedFeeUsesFallbackThenKeepsCachedValueOnFailure(t *testing.T) {
	cache := newCachedFee(time.Hour)
	refreshErr := errors.New("boom")

	got := cache.get(0.123, func() (float64, error) {
		return 0, refreshErr
	}, "test fee")
	assertFloat(t, got, 0.123)

	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	got = cache.get(0.123, func() (float64, error) {
		return 0.456, nil
	}, "test fee")
	assertFloat(t, got, 0.456)

	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	got = cache.get(0.123, func() (float64, error) {
		return 0, refreshErr
	}, "test fee")
	assertFloat(t, got, 0.456)
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

	est := fixedCostEstimator("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)
	tradeCostEstimators = map[string]*TradeCostEstimator{"kraken": est}
	tradeRouteOptimizer = NewTradeRouteOptimizer(tradeCostEstimators)

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

	var offers []liveOutOffer
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}

	offer := offers[0]
	assertFloat(t, offer.PriceDiffPct, 2.5)
	assertFloat(t, offer.TradeCostPct, 0.6)
	assertFloat(t, offer.NetPremiumPct, 1.9)
	assertFloat(t, offer.CostBreakdown.BTCAmount, 0.1)
	assertFloat(t, offer.CostBreakdown.TotalCostPct, 0.6)
	assertFloat(t, offer.CostBreakdown.TotalCostBTC, 0.0006)
	assertFloat(t, offer.CostBreakdown.EstimatedProfitBTC, 0.0019)
}

