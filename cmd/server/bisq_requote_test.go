package main

import (
	"commons/apierror"
	"commons/bisqroute"
	"commons/tradecost"
	"strings"
	"testing"
	"time"
)

func TestBisqRequoteUsesServerPolicyAndExactSize(t *testing.T) {
	old := tradeRouteOptimizer
	t.Cleanup(func() { tradeRouteOptimizer = old })
	est := tradecost.NewFixedEstimatorWithHedgeForTesting("kraken", "bisq", 0.002, 0.0001, 0.001, 0.00005, 0.00002)
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(map[string]*tradecost.TradeCostEstimator{"kraken": est})
	now := time.Now().UTC()
	amount, min, max := 1000.0, 0.001, 0.1
	id := "offer-one"
	h := &LiveHub{subscribers: make(map[chan []byte]liveSubscription), lifecycle: make(map[int64][]LifecyclePoint)}
	h.setTransactionalRoutingSettings(transactionalRoutingSettings{FiatCurrency: "EUR", AmountFiat: &amount, TradeMode: tradecost.TradeModeHedgefullSingleCollateral, TargetDirection: "bid", FeeTier: "economic", ConfigVersion: 7})
	h.setOffers([]LiveOffer{{ID: 1, OfferID: &id, Exchange: "bisq", FiatCurrency: "EUR", Direction: "bid", Price: 52000, ReferencePrice: 50000, BTCMin: &min, BTCMax: &max, LastSeen: now.Format(time.RFC3339Nano)}})
	req := bisqroute.Request{OfferID: id, Provider: "kraken", TradeMode: tradecost.TradeModeHedgefullSingleCollateral, FiatCurrency: "EUR", TradeAmountSats: 1_000_000, TotalToPaySats: 1_160_000, SecurityDepositSats: 150_000, TakerFeeSats: 8000, MiningFeeSats: 2000, VolumeAsLong: 5_200_000}
	route, err := h.requoteBisq(req, now)
	if err != nil {
		t.Fatal(err)
	}
	if route.BTCAmount != 0.01 || route.HedgeNotional != 0.01 || route.RequiredBTCLiquidity != 0.0116 || route.SecurityDepositBTC != 0.0015 || route.ConfigVersion != 7 || route.CEXWithdrawalFeeBTC == 0 {
		t.Fatalf("route=%+v", route)
	}
	// Each refusal says what changed, since the Taker Controller shows it to the operator.
	for name, tc := range map[string]struct {
		change func(*bisqroute.Request)
		reason string
	}{
		"provider": {func(q *bisqroute.Request) { q.Provider = "binance" }, "kraken is now the cheapest exchange for this trade instead of binance"},
		"offer":    {func(q *bisqroute.Request) { q.OfferID = "unknown" }, "the offer is no longer in the live order book"},
		"amount":   {func(q *bisqroute.Request) { q.TradeAmountSats = 0 }, "the Bisq daemon's quote doesn't add up"},
		"currency": {func(q *bisqroute.Request) { q.FiatCurrency = "USD" }, "the liquidity target's currency is EUR, but this offer is in USD"},
		"funding":  {func(q *bisqroute.Request) { q.TotalToPaySats++ }, "should equal the 1160001 sats total"},
	} {
		t.Run(name, func(t *testing.T) {
			q := req
			tc.change(&q)
			_, err := h.requoteBisq(q, now)
			if err == nil {
				t.Fatal("invalid quote accepted")
			}
			if apierror.CodeOf(err) != "bisq_requote_failed" || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("error = %v, want reason containing %q", err, tc.reason)
			}
		})
	}
	if _, err := h.requoteBisq(req, now.Add(6*time.Minute)); err == nil || !strings.Contains(err.Error(), "last confirmed in the order book 6m0s ago") {
		t.Fatalf("stale snapshot: %v", err)
	}
}
