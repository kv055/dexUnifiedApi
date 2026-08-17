package main

import (
	"reflect"
	"testing"

	"commons/tradecost"
)

func TestResolvedRouteFromBreakdownCarriesExecutionSnapshot(t *testing.T) {
	bd := tradecost.CostBreakdown{
		BTCAmount:          0.01,
		RouteCEX:           "kraken",
		RouteMethod:        "onchain_hub",
		TradeDirection:     "bid",
		EstimatedProfitPct: 3.25,
		TotalCostBTC:       0.00012,
		TotalCostPct:       1.2,
		Hedge: tradecost.HedgeBreakdown{
			Venue: "kraken_futures", Side: "short",
			Position: &tradecost.HedgePositionDetail{HedgeRatio: 1, LeverageUsed: 3},
		},
		Capital: tradecost.CapitalRequirement{BTCPrice: 50000, TotalFiat: 650, SecurityDepositBTC: 0.001},
	}
	route := resolvedRouteFromBreakdown(bd, "EUR", transactionalRoutingSettings{
		ConfigVersion: 4, TradeMode: tradecost.TradeModeHedgefullSingleCollateral, TargetDirection: "bid",
	})
	if route == nil || route.Provider != "kraken" || !route.NeedsHub || route.NeedsLightning {
		t.Fatalf("route = %+v", route)
	}
	if route.HedgeVenue != "kraken_futures" || route.HedgeNotional != 0.01 || route.Leverage != 3 {
		t.Fatalf("hedge route = %+v", route)
	}
	if route.RequiredBTCLiquidity != 0 {
		t.Fatalf("non-swap hedgefull liquidity = %.8f, want 0", route.RequiredBTCLiquidity)
	}
}

func TestSnapshotRouteBookIsAuthoritativeAcrossClientParameters(t *testing.T) {
	oldOptimizer := tradeRouteOptimizer
	t.Cleanup(func() { tradeRouteOptimizer = oldOptimizer })
	est := tradecost.NewFixedEstimatorWithHedgeForTesting("kraken", "bisq", 0.002, 0.0001, 0.001, 0.00005, 0.00002)
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(map[string]*tradecost.TradeCostEstimator{"kraken": est})
	amount := 1000.0
	min, max := 0.001, 0.1
	h := &LiveHub{subscribers: make(map[chan []byte]liveSubscription), lifecycle: make(map[int64][]LifecyclePoint)}
	h.setTransactionalRoutingSettings(transactionalRoutingSettings{
		FiatCurrency: "EUR", AmountSource: "capped_volume", AmountFiat: &amount,
		TradeMode: tradecost.TradeModeHedgefullSingleCollateral, TargetDirection: "bid",
		FeeTier: "economic", CoupledHedge: false, ConfigVersion: 7,
	})
	h.setOffers([]LiveOffer{{ID: 1, Exchange: "bisq", FiatCurrency: "EUR", Direction: "bid", Price: 52000, ReferencePrice: 50000, BTCMin: &min, BTCMax: &max}})

	a := h.computeOutOffers(1, liveSubscription{cex: "kraken"})
	b := h.computeOutOffers(999999, liveSubscription{cex: "coinbase"})
	if len(a) != 1 || len(b) != 1 || a[0].RouteResolution.Status != "resolved" || a[0].ResolvedRoute == nil {
		t.Fatalf("route book = %#v / %#v", a, b)
	}
	if !reflect.DeepEqual(a[0].ResolvedRoute, b[0].ResolvedRoute) {
		t.Fatalf("client parameters changed route: %#v != %#v", a[0].ResolvedRoute, b[0].ResolvedRoute)
	}
	if a[0].ResolvedRoute.BTCAmount != 0.02 || a[0].ResolvedRoute.ConfigVersion != 7 || a[0].ResolvedRoute.SnapshotRevision <= 0 || a[0].ResolvedRoute.ResolvedAt == "" {
		t.Fatalf("incomplete resolved route: %+v", a[0].ResolvedRoute)
	}
	if a[0].ResolvedRoute.CostInputs.FeeTier != "economic" || a[0].ResolvedRoute.CostInputs.FundingCostAvailable || a[0].ResolvedRoute.CostInputs.ProjectedFundingFeeBTC != 0 {
		t.Fatalf("cost inputs = %+v", a[0].ResolvedRoute.CostInputs)
	}
}

func TestSnapshotRouteBookReturnsStructuredFailureStatuses(t *testing.T) {
	oldOptimizer := tradeRouteOptimizer
	t.Cleanup(func() { tradeRouteOptimizer = oldOptimizer })
	est := tradecost.NewHedgelessOnlyEstimatorForTesting("kraken", "bisq", 0.002, 0.0001, 0.001, 0.00005)
	tradeRouteOptimizer = tradecost.NewTradeRouteOptimizer(map[string]*tradecost.TradeCostEstimator{"kraken": est})
	amount := 100.0
	min, max := 0.01, 0.1
	h := &LiveHub{subscribers: make(map[chan []byte]liveSubscription), lifecycle: make(map[int64][]LifecyclePoint)}
	h.setTransactionalRoutingSettings(transactionalRoutingSettings{
		FiatCurrency: "EUR", AmountSource: "custom", AmountFiat: &amount,
		TradeMode: tradecost.TradeModeHedgefullSingleCollateral, TargetDirection: "bid",
		FeeTier: "economic", ConfigVersion: 2,
	})
	h.setOffers([]LiveOffer{
		{ID: 1, Exchange: "bisq", FiatCurrency: "EUR", Direction: "bid", Price: 52000, ReferencePrice: 50000, BTCMin: &min, BTCMax: &max},
		{ID: 2, Exchange: "bisq", FiatCurrency: "USD", Direction: "bid", Price: 52000, ReferencePrice: 50000, BTCMin: &min, BTCMax: &max},
	})
	book := h.computeOutOffers(0, liveSubscription{})
	if book[0].RouteResolution.Code != "amount_below_minimum" || book[0].ResolvedRoute != nil {
		t.Fatalf("blocked offer = %+v", book[0])
	}
	if book[1].RouteResolution.Status != "out_of_scope" || book[1].RouteResolution.Code != "fiat_currency" {
		t.Fatalf("out-of-scope offer = %+v", book[1].RouteResolution)
	}
}

func TestRouteSizeForSettingsCapsCustomFiatAmount(t *testing.T) {
	max := 0.05
	min := 0.01
	custom := 1000.0
	size, ok := routeSizeForSettings(LiveOffer{BTCMin: &min, BTCMax: &max}, 50000, transactionalRoutingSettings{AmountFiat: &custom})
	if !ok || size != 0.02 {
		t.Fatalf("size, ok = %v, %v; want 0.02, true", size, ok)
	}
}

func TestResolvedSwapRouteCarriesExecutionFeeAllowances(t *testing.T) {
	bd := tradecost.CostBreakdown{
		BTCAmount: 0.01, RouteCEX: "kraken", RouteMethod: "onchain_swap", TradeDirection: "bid",
		WithdrawalFeeBTC: 0.000002,
		Conversion:       tradecost.ConversionBreakdown{RoutingFeeBTC: 0.000003},
		Capital:          tradecost.CapitalRequirement{BTCPrice: 50000, TotalFiat: 500, SecurityDepositBTC: 0.0015, BTCLiquidityBTC: 0.0115},
	}
	route := resolvedRouteFromBreakdown(bd, "EUR", transactionalRoutingSettings{ConfigVersion: 1, TradeMode: tradecost.TradeModeHedgelessDoubleCollateral, TargetDirection: "bid"})
	if route == nil || !route.NeedsLightning || route.RoutingFeeAllowanceSat != 300 || route.CEXWithdrawalFeeBTC != bd.WithdrawalFeeBTC || route.SwapSafetyHeadroomBTC <= 0 {
		t.Fatalf("swap route allowances = %+v", route)
	}
	if route.RequiredBTCLiquidity != 0.0115 {
		t.Fatalf("required BTC liquidity = %.8f, want 0.01150000", route.RequiredBTCLiquidity)
	}
}

func TestResolvedHedgefullSwapRouteDerivesRequiredBTCLiquidity(t *testing.T) {
	bd := tradecost.CostBreakdown{
		BTCAmount: 0.01, RouteCEX: "kraken", RouteMethod: "onchain_swap", TradeDirection: "bid",
		Hedge: tradecost.HedgeBreakdown{
			Venue: "kraken_futures", Side: "short",
			Position: &tradecost.HedgePositionDetail{HedgeRatio: 1, LeverageUsed: 5},
		},
		Capital: tradecost.CapitalRequirement{
			BTCPrice: 50000, TotalFiat: 550, SecurityDepositBTC: 0.0015,
		},
	}
	route := resolvedRouteFromBreakdown(bd, "EUR", transactionalRoutingSettings{
		ConfigVersion: 1, TradeMode: tradecost.TradeModeHedgefullSingleCollateral, TargetDirection: "bid",
	})
	if route == nil {
		t.Fatal("resolved route is nil")
	}
	if route.RequiredBTCLiquidity != 0.0115 {
		t.Fatalf("required BTC liquidity = %.8f, want 0.01150000", route.RequiredBTCLiquidity)
	}
}
