package main

import (
	"context"
	"math"
	"testing"

	"commons/tradecost"
	"dexunifiedapi/internal/testdb"
)

// These are the same successive quotes covered by the snapshot mapper and
// persistence regression: a 60-300 EUR range at a 20% reduction in sats.
func checkMostroLiveProfit(t *testing.T, o LiveOffer) {
	t.Helper()
	oldOptimizer := tradeRouteOptimizer
	t.Cleanup(func() { tradeRouteOptimizer = oldOptimizer })
	est := tradecost.NewFixedEstimatorForTesting("kraken", "mostro", 0.0025, 0.00015, 0.001, 0.00005)
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(map[string]*tradecost.TradeCostEstimator{"kraken": est})

	premium := computeDirectionalPremiumPct(o.Direction, o.Price, o.ReferencePrice)
	if math.Abs(premium-25) > 1e-8 {
		t.Fatalf("effective premium = %v, want 25", premium)
	}
	size := selectBTCTradeSize(o.BTCMax, o.BTCMin)
	cost, net, bd := computeNetPremiumFieldsFull(premium, size, o.ReferencePrice, o.Exchange, o.Direction, 0, nil, nil, "", false, nil)
	if bd.RouteCEX == "" || cost <= 0 || net <= 0 || net >= premium {
		t.Fatalf("expected positive spread after costs: cost=%v net=%v breakdown=%+v", cost, net, bd)
	}
	scenario := buildScenarioFromBreakdown(bd, o.ReferencePrice, nil, cost, net, tradecost.ManualRouteOptions{})
	if scenario.TradeSizeFiat != 240 || scenario.TradeSizeBTC != size {
		t.Fatalf("wrong live size: %+v", scenario)
	}
	// Gross spread is EUR 60 in both snapshots. Costs must reduce it, and
	// profit must use this snapshot's size and captured reference price.
	wantProfit := tradecost.Round2(tradecost.Round8(size*net/100) * o.ReferencePrice)
	if scenario.Profit.Fiat != wantProfit || scenario.Profit.Fiat <= 0 || scenario.Profit.Fiat >= 60 {
		t.Fatalf("live profit = %v, want %v below EUR 60", scenario.Profit.Fiat, wantProfit)
	}
}

func TestMostroLiveProfitUsesConsistentQuote(t *testing.T) {
	for _, tc := range []struct{ price, spot, size float64 }{{75000, 60000, 0.004}, {80000, 64000, 0.00375}} {
		checkMostroLiveProfit(t, LiveOffer{Exchange: "mostro", Direction: "bid", Price: tc.price, ReferencePrice: tc.spot, BTCMax: &tc.size})
	}
}

func TestMostroPersistedQuoteFeedsLiveProfit(t *testing.T) {
	db := testdb.Open(t)
	var id int64
	if err := db.QueryRow(`INSERT INTO offers
		(exchange, fiat_currency, direction, offer_id, price, btc_min, btc_max, fiat_min, fiat_max, price_type, is_live)
		VALUES ('mostro', 'EUR', 'bid', 'pricing-order', 75000, 0.0008, 0.004, 60, 300, 'floating', TRUE)
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO order_lifecycle (offer_db_id, price, ref_price, snapshot_time)
		VALUES (?, 75000, 60000, CURRENT_TIMESTAMP - INTERVAL '1 minute')`, id); err != nil {
		t.Fatal(err)
	}
	// The next snapshot changes spot and fills amt for a take slice. The
	// mapper still produces range pricing; persistence updates both fields.
	if _, err := db.Exec(`UPDATE offers SET price = 80000, btc_min = 0.00075, btc_max = 0.00375 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO order_lifecycle (offer_db_id, price, ref_price, snapshot_time)
		VALUES (?, 80000, 64000, CURRENT_TIMESTAMP)`, id); err != nil {
		t.Fatal(err)
	}
	offers, err := newLiveOfferRepository(db).Load(context.Background())
	if err != nil || len(offers) != 1 {
		t.Fatalf("load live book: offers=%+v err=%v", offers, err)
	}
	checkMostroLiveProfit(t, offers[0])
}
