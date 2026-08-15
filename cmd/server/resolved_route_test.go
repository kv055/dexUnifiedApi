package main

import (
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
	if route.SnapshotRevision <= 0 || route.ResolvedAt == "" || route.CostInputs.ReferencePrice != 50000 || route.CostInputs.FeeTier != "economic" {
		t.Fatalf("authoritative snapshot = %+v", route)
	}
	if route.CostInputs.FundingCostAvailable || route.CostInputs.ProjectedFundingFeeBTC != 0 {
		t.Fatalf("unsupported projected funding leaked into route = %+v", route.CostInputs)
	}
}

func TestRouteSizeForSettingsCapsFiatAmount(t *testing.T) {
	max := 0.05
	min := 0.01
	custom := 1000.0
	size, ok := routeSizeForSettings(LiveOffer{BTCMin: &min, BTCMax: &max}, 50000, transactionalRoutingSettings{AmountFiat: &custom})
	if !ok || size != 0.02 {
		t.Fatalf("size, ok = %v, %v; want 0.02, true", size, ok)
	}
}

func TestOfferMatchesTargetDirectionNormalizesOfferVocabulary(t *testing.T) {
	tests := []struct {
		offer, target string
		want          bool
	}{
		{offer: "bid", target: "bid", want: true},
		{offer: "ask", target: "ask", want: true},
		{offer: "bid", target: "ask", want: false},
		{offer: "ask", target: "bid", want: false},
	}
	for _, tt := range tests {
		if got := offerMatchesTargetDirection(tt.offer, tt.target); got != tt.want {
			t.Errorf("offerMatchesTargetDirection(%q, %q) = %v; want %v", tt.offer, tt.target, got, tt.want)
		}
	}
}

func TestResolvedSwapRouteCarriesExecutionFeeAllowances(t *testing.T) {
	bd := tradecost.CostBreakdown{
		BTCAmount: 0.01, RouteCEX: "kraken", RouteMethod: "onchain_swap", TradeDirection: "bid",
		WithdrawalFeeBTC: 0.000002,
		Conversion:       tradecost.ConversionBreakdown{RoutingFeeBTC: 0.000003},
		Capital:          tradecost.CapitalRequirement{BTCPrice: 50000, TotalFiat: 500, BTCLiquidityBTC: 0.01},
	}
	route := resolvedRouteFromBreakdown(bd, "EUR", transactionalRoutingSettings{ConfigVersion: 1, TradeMode: tradecost.TradeModeHedgelessDoubleCollateral, TargetDirection: "bid"})
	if route == nil || !route.NeedsLightning || route.RoutingFeeAllowanceSat != 300 || route.CEXWithdrawalFeeBTC != bd.WithdrawalFeeBTC || route.SwapSafetyHeadroomBTC <= 0 {
		t.Fatalf("swap route allowances = %+v", route)
	}
}
