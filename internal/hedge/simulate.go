package hedge

// PositionSimulation describes how a short perp hedge would be sized for a
// fixed BTC transfer amount. Unlike Optimize, this helper does not search for
// the maximum feasible transfer under a capital budget — the trade size is
// already known, so it only reports the position parameters the trader would
// need to execute.
type PositionSimulation struct {
	HedgeRatio                   float64
	LeverageUsed                 float64
	HedgeNotionalEUR             float64
	InitialMarginEUR             float64
	RecommendedCollateralEUR     float64
	StressMovePct                float64
	StressLossEUR                float64
	MaintenanceRequirementEUR    float64
	FreeCollateralAfterStressEUR float64
	LiquidationMovePct           float64
	BTCPriceEUR                  float64
}

// SimulatePosition computes the short-perp hedge sizing for a fixed BTC
// transfer amount. It assumes the trader uses the venue's maximum leverage
// (most capital-efficient) and sizes collateral to survive the configured
// adverse stress move plus maintenance margin.
//
// cfg is expected to already have MaxLeverage, MaintenanceMarginRate, and perp
// fee/funding rates populated (via ApplyVenueProfile + ApplyLivePerpInputs).
// StressMovePct, TargetHedgeRatio, and SafetyBufferEUR fall back to the
// optimizer config defaults when unset.
func SimulatePosition(cfg Config, btcAmount, btcPriceEUR float64) PositionSimulation {
	if btcAmount <= 0 || btcPriceEUR <= 0 {
		return PositionSimulation{}
	}

	cfg = applyDefaultOptimizerConfig(cfg)

	hedgeRatio := clamp(cfg.TargetHedgeRatio, 0.0, 1.0)
	if hedgeRatio == 0 {
		hedgeRatio = 1.0
	}

	leverage := cfg.MaxLeverage
	maxSupported := configuredMaxSupportedLeverage()
	if leverage <= 0 || leverage > maxSupported {
		leverage = maxSupported
	}

	notionalEUR := btcAmount * btcPriceEUR * hedgeRatio
	initialMargin := notionalEUR / leverage
	openFee := notionalEUR * cfg.PerpOpenFeeRate
	closeFee := notionalEUR * cfg.PerpCloseFeeRate
	funding := notionalEUR * cfg.FundingCostRate
	stressLoss := notionalEUR * cfg.StressMovePct
	maintenanceReq := notionalEUR * cfg.MaintenanceMarginRate

	// Collateral sized to survive the configured adverse move: initial margin
	// plus realized costs (open + close + worst-case funding) plus the stress
	// loss and maintenance requirement, plus any extra safety buffer.
	recommendedCollateral := initialMargin + openFee + closeFee +
		maxFloat64(funding, 0) + stressLoss + maintenanceReq + cfg.SafetyBufferEUR

	equityAfterStress := recommendedCollateral - stressLoss - openFee - closeFee - funding
	freeCollateralAfterStress := equityAfterStress - maintenanceReq

	// Liquidation threshold for a short at leverage L with maintenance m: the
	// position is liquidated when the adverse move consumes the margin down to
	// the maintenance requirement, i.e. at (1/L − m).
	liquidationMove := 0.0
	if leverage > 0 {
		liquidationMove = (1.0 / leverage) - cfg.MaintenanceMarginRate
		if liquidationMove < 0 {
			liquidationMove = 0
		}
	}

	return PositionSimulation{
		HedgeRatio:                   hedgeRatio,
		LeverageUsed:                 leverage,
		HedgeNotionalEUR:             notionalEUR,
		InitialMarginEUR:             initialMargin,
		RecommendedCollateralEUR:     recommendedCollateral,
		StressMovePct:                cfg.StressMovePct,
		StressLossEUR:                stressLoss,
		MaintenanceRequirementEUR:    maintenanceReq,
		FreeCollateralAfterStressEUR: freeCollateralAfterStress,
		LiquidationMovePct:           liquidationMove,
		BTCPriceEUR:                  btcPriceEUR,
	}
}
