package hedge

import (
	"math"
	"testing"
)

func baseConfig() Config {
	return Config{
		TotalEUR:              500,
		BTCPriceEUR:           50000,
		TransferMinutes:       20,
		SpotBuyFeeRate:        0.006,
		PerpOpenFeeRate:       0.0005,
		PerpCloseFeeRate:      0.0005,
		FundingCostRate:       0.0001,
		MaintenanceMarginRate: 0.005,
		SafetyBufferEUR:       10,
		StressMovePct:         0.10,
		MaxLeverage:           10,
		TargetHedgeRatio:      1,
		AllowPartialHedge:     false,
		HedgeRatioStep:        0.05,
		SearchStepEUR:         1,
		LeverageStep:          1,
	}
}

func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func TestOptimizeFindsFeasibleFullHedge(t *testing.T) {
	res, err := Optimize(baseConfig())
	if err != nil {
		t.Fatalf("Optimize returned error: %v", err)
	}
	if !res.Feasible {
		t.Fatal("expected feasible result")
	}
	assertClose(t, res.HedgeRatio, 1)
	assertClose(t, res.LeverageUsed, 10)
	if res.TransferNotionalEUR <= 0 {
		t.Fatalf("expected positive transfer notional, got %.2f", res.TransferNotionalEUR)
	}
	if res.FreeCollateralAfterStressEUR < 10 {
		t.Fatalf("expected buffer to survive, free collateral %.2f", res.FreeCollateralAfterStressEUR)
	}
}

func TestOptimizeAllowsNegativeFundingRate(t *testing.T) {
	cfg := baseConfig()
	cfg.FundingCostRate = -0.0002

	res, err := Optimize(cfg)
	if err != nil {
		t.Fatalf("Optimize returned error: %v", err)
	}
	if !res.Feasible {
		t.Fatal("expected feasible result")
	}
	if res.FundingCostEUR >= 0 {
		t.Fatalf("expected funding receipt, got %.6f", res.FundingCostEUR)
	}
}

func TestOptimizeFallsBackToPartialHedgeButNotZero(t *testing.T) {
	cfg := baseConfig()
	cfg.TotalEUR = 11.5
	cfg.SafetyBufferEUR = 10
	cfg.MaxLeverage = 1
	cfg.AllowPartialHedge = true
	cfg.HedgeRatioStep = 0.25

	res, err := Optimize(cfg)
	if err != nil {
		t.Fatalf("Optimize returned error: %v", err)
	}
	if !res.Feasible {
		t.Fatal("expected partial hedge to be feasible")
	}
	if !(res.HedgeRatio > 0 && res.HedgeRatio < 1) {
		t.Fatalf("expected positive partial hedge ratio, got %.4f", res.HedgeRatio)
	}
	if !res.UsedPartialHedge {
		t.Fatal("expected UsedPartialHedge to be true")
	}
}

func TestOptimizeReturnsInfeasibleWhenEvenSmallPositiveHedgeFails(t *testing.T) {
	cfg := baseConfig()
	cfg.TotalEUR = 15
	cfg.MaxLeverage = 1
	cfg.AllowPartialHedge = true
	cfg.HedgeRatioStep = 0.5
	cfg.SafetyBufferEUR = 15

	res, err := Optimize(cfg)
	if err != nil {
		t.Fatalf("Optimize returned error: %v", err)
	}
	if res.Feasible {
		t.Fatalf("expected infeasible result, got %+v", res)
	}
}

func TestValidateConfigRejectsBadInputs(t *testing.T) {
	cfg := baseConfig()
	cfg.TotalEUR = 0
	if _, err := Optimize(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}
