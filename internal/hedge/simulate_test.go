package hedge

import (
	"math"
	"testing"
)

func simulateBaseConfig() Config {
	return Config{
		TransferMinutes:       20,
		PerpOpenFeeRate:       0.0005,
		PerpCloseFeeRate:      0.0005,
		FundingCostRate:       0.0001,
		MaintenanceMarginRate: 0.01,
		MaxLeverage:           10,
		StressMovePct:         0.10,
		TargetHedgeRatio:      1.0,
		SafetyBufferEUR:       0,
	}
}

func TestSimulatePositionSizesNotionalAndMargin(t *testing.T) {
	cfg := simulateBaseConfig()
	sim := SimulatePosition(cfg, 0.1, 50000)

	assertClose(t, sim.HedgeRatio, 1.0)
	assertClose(t, sim.LeverageUsed, 10)
	assertClose(t, sim.HedgeNotionalEUR, 5000)    // 0.1 BTC * 50000 * 1.0
	assertClose(t, sim.InitialMarginEUR, 500)     // 5000 / 10
	assertClose(t, sim.StressLossEUR, 500)        // 5000 * 0.10
	assertClose(t, sim.MaintenanceRequirementEUR, 50) // 5000 * 0.01
	assertClose(t, sim.BTCPriceEUR, 50000)
}

func TestSimulatePositionRecommendedCollateralSurvivesStress(t *testing.T) {
	cfg := simulateBaseConfig()
	sim := SimulatePosition(cfg, 0.1, 50000)

	// Recommended collateral should be enough that after the stress move and
	// all fees, equity remains above the maintenance requirement (i.e. free
	// collateral after stress >= 0).
	if sim.FreeCollateralAfterStressEUR < 0 {
		t.Fatalf("free collateral after stress should be >= 0, got %.4f", sim.FreeCollateralAfterStressEUR)
	}
	// Recommended collateral strictly exceeds the initial margin alone.
	if sim.RecommendedCollateralEUR <= sim.InitialMarginEUR {
		t.Fatalf("recommended collateral %.2f should exceed initial margin %.2f",
			sim.RecommendedCollateralEUR, sim.InitialMarginEUR)
	}
}

func TestSimulatePositionLiquidationMoveMatchesLeverage(t *testing.T) {
	cfg := simulateBaseConfig()
	sim := SimulatePosition(cfg, 0.1, 50000)

	want := (1.0 / 10) - 0.01 // 1/L - maintenance
	if math.Abs(sim.LiquidationMovePct-want) > 1e-9 {
		t.Fatalf("liquidation move = %.6f, want %.6f", sim.LiquidationMovePct, want)
	}
}

func TestSimulatePositionZeroInputsReturnEmpty(t *testing.T) {
	cfg := simulateBaseConfig()
	if got := SimulatePosition(cfg, 0, 50000); got != (PositionSimulation{}) {
		t.Fatalf("expected empty simulation for zero btcAmount, got %+v", got)
	}
	if got := SimulatePosition(cfg, 0.1, 0); got != (PositionSimulation{}) {
		t.Fatalf("expected empty simulation for zero price, got %+v", got)
	}
}
