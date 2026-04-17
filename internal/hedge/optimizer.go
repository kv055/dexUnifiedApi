package hedge

import (
	"errors"
	"math"
	"slices"
)

// Config contains all externally supplied parameters needed by the optimizer.
// No prices, fees, or funding rates are fetched internally.
type Config struct {
	// TotalEUR is the total capital available to split between:
	// 1) buying BTC spot to transfer, and
	// 2) retaining collateral for the hedge.
	TotalEUR float64

	// BTCPriceEUR is the BTC spot price in EUR at optimization time.
	BTCPriceEUR float64

	// TransferMinutes is accepted as an input because it is expected to come
	// from an external prediction source. The optimizer does not transform it
	// into pricing assumptions; callers may use it to derive FundingCostRate.
	TransferMinutes float64

	// SpotBuyFeeRate is the fee rate for buying BTC spot on the source exchange.
	// Example: 0.006 for 0.6%
	SpotBuyFeeRate float64

	// PerpOpenFeeRate is the fee rate paid when opening the short perp hedge.
	PerpOpenFeeRate float64

	// PerpCloseFeeRate is the fee rate paid when closing the short perp hedge.
	PerpCloseFeeRate float64

	// FundingCostRate is the total expected funding rate over the holding window.
	// It may be positive (cost) or negative (expected receipt).
	// When live perp inputs are available, this should be the prorated rate over
	// TransferMinutes rather than the raw funding rate per interval.
	FundingCostRate float64

	// MaintenanceMarginRate is the maintenance margin rate on the hedge venue.
	// Example: 0.005 means 0.5%
	MaintenanceMarginRate float64

	// SafetyBufferEUR is extra collateral you want to keep beyond the strict minimum.
	SafetyBufferEUR float64

	// StressMovePct is the hardcoded adverse move against the short hedge.
	// For the current model this would typically be 0.10 for +10%.
	StressMovePct float64

	// MaxLeverage is capped internally at 10x. Values <= 0 are invalid.
	MaxLeverage float64

	// TargetHedgeRatio is the preferred hedge ratio, typically 1.0 for full hedge.
	// The optimizer tries this ratio first.
	TargetHedgeRatio float64

	// AllowPartialHedge enables fallback to smaller positive hedge ratios if the
	// target hedge ratio is infeasible.
	AllowPartialHedge bool

	// HedgeRatioStep is the decrement step used when falling back from the target
	// hedge ratio. Example: 0.05. Ignored if AllowPartialHedge is false.
	HedgeRatioStep float64

	// SearchStepEUR is the search granularity over transfer notional.
	// Smaller values give a more precise result but require more iterations.
	SearchStepEUR float64

	// LeverageStep controls leverage search granularity from 1x up to MaxLeverage.
	// Values <= 0 default to 1x increments plus the exact MaxLeverage endpoint.
	LeverageStep float64
}

// Result contains the optimizer output.
type Result struct {
	Feasible bool

	// Recommended transfer notional in EUR spent on BTC before conversion to quantity.
	TransferNotionalEUR float64

	// BTC quantity bought and sent.
	BTCToSend float64

	// Selected hedge ratio and hedge notional in EUR.
	HedgeRatio            float64
	HedgeNotionalEUR      float64
	LeverageUsed          float64
	RetainedCollateralEUR float64

	// Cost breakdown.
	SpotBuyFeeEUR    float64
	PerpOpenFeeEUR   float64
	PerpCloseFeeEUR  float64
	FundingCostEUR   float64
	InitialMarginEUR float64
	StressLossEUR    float64

	// Post-stress collateral check values.
	EquityAfterStressEUR         float64
	MaintenanceRequirementEUR    float64
	FreeCollateralAfterStressEUR float64

	// Informational flags.
	UsedPartialHedge bool
}

// Optimize returns the maximum BTC transfer notional that remains feasible under:
// - leverage capped at 10x
// - hedge-ratio policy with optional partial fallback
// - deterministic adverse stress move
// - maintenance margin survival with a safety buffer
//
// The model is intentionally deterministic: it does not fetch prices, fees, or funding.
func Optimize(cfg Config) (Result, error) {
	cfg = applyDefaultOptimizerConfig(cfg)
	if err := validateConfig(cfg); err != nil {
		return Result{}, err
	}

	best := Result{}
	for _, hedgeRatio := range hedgeRatioCandidates(cfg) {
		candidate := optimizeForHedgeRatio(cfg, hedgeRatio)
		if !candidate.Feasible {
			continue
		}

		if !best.Feasible ||
			candidate.HedgeRatio > best.HedgeRatio ||
			(almostEqual(candidate.HedgeRatio, best.HedgeRatio) &&
				candidate.TransferNotionalEUR > best.TransferNotionalEUR) {
			best = candidate
		}

		if almostEqual(hedgeRatio, clamp(cfg.TargetHedgeRatio, 0.0, 1.0)) {
			break
		}
	}

	if !best.Feasible {
		return Result{Feasible: false}, nil
	}

	return best, nil
}

func optimizeForHedgeRatio(cfg Config, hedgeRatio float64) Result {
	best := Result{}
	for _, leverage := range leverageCandidates(cfg) {
		candidate := optimizeForLeverage(cfg, leverage, hedgeRatio)
		if !candidate.Feasible {
			continue
		}
		if !best.Feasible || candidate.TransferNotionalEUR > best.TransferNotionalEUR ||
			(almostEqual(candidate.TransferNotionalEUR, best.TransferNotionalEUR) && candidate.LeverageUsed < best.LeverageUsed) {
			best = candidate
		}
	}
	return best
}

func optimizeForLeverage(cfg Config, leverage, hedgeRatio float64) Result {
	best := Result{}
	maxAffordableSpot := cfg.TotalEUR / (1.0 + cfg.SpotBuyFeeRate)

	for transferNotional := 0.0; transferNotional <= maxAffordableSpot; transferNotional += cfg.SearchStepEUR {
		res := evaluateCandidate(cfg, leverage, hedgeRatio, transferNotional)
		if res.Feasible && (!best.Feasible || res.TransferNotionalEUR > best.TransferNotionalEUR) {
			best = res
		}
	}

	endpoint := evaluateCandidate(cfg, leverage, hedgeRatio, maxAffordableSpot)
	if endpoint.Feasible && (!best.Feasible || endpoint.TransferNotionalEUR > best.TransferNotionalEUR) {
		best = endpoint
	}

	return best
}

func evaluateCandidate(cfg Config, leverage, hedgeRatio, transferNotionalEUR float64) Result {
	if transferNotionalEUR <= 0 {
		return Result{Feasible: false}
	}

	spotBuyFeeEUR := transferNotionalEUR * cfg.SpotBuyFeeRate
	hedgeNotionalEUR := transferNotionalEUR * hedgeRatio

	initialMarginEUR := 0.0
	if hedgeNotionalEUR > 0 {
		initialMarginEUR = hedgeNotionalEUR / leverage
	}
	perpOpenFeeEUR := hedgeNotionalEUR * cfg.PerpOpenFeeRate
	perpCloseFeeEUR := hedgeNotionalEUR * cfg.PerpCloseFeeRate
	fundingCostEUR := hedgeNotionalEUR * cfg.FundingCostRate
	stressLossEUR := hedgeNotionalEUR * cfg.StressMovePct

	retainedCollateralEUR := cfg.TotalEUR - transferNotionalEUR - spotBuyFeeEUR
	if retainedCollateralEUR < 0 {
		return Result{Feasible: false}
	}

	// Upfront feasibility only reserves deterministic cash outflows. Expected funding
	// receipts should not relax the upfront collateral requirement.
	requiredUpfrontEUR := initialMarginEUR +
		perpOpenFeeEUR +
		perpCloseFeeEUR +
		maxFloat64(fundingCostEUR, 0) +
		cfg.SafetyBufferEUR
	if retainedCollateralEUR < requiredUpfrontEUR {
		return Result{Feasible: false}
	}

	// Equity after stress is reduced by losses and realized carrying costs.
	equityAfterStressEUR := retainedCollateralEUR -
		stressLossEUR -
		perpOpenFeeEUR -
		perpCloseFeeEUR -
		fundingCostEUR
	maintenanceRequirementEUR := hedgeNotionalEUR * cfg.MaintenanceMarginRate
	freeCollateralAfterStressEUR := equityAfterStressEUR - maintenanceRequirementEUR
	if freeCollateralAfterStressEUR < cfg.SafetyBufferEUR {
		return Result{Feasible: false}
	}

	btcToSend := transferNotionalEUR / cfg.BTCPriceEUR
	return Result{
		Feasible:                     true,
		TransferNotionalEUR:          transferNotionalEUR,
		BTCToSend:                    btcToSend,
		HedgeRatio:                   hedgeRatio,
		HedgeNotionalEUR:             hedgeNotionalEUR,
		LeverageUsed:                 leverage,
		RetainedCollateralEUR:        retainedCollateralEUR,
		SpotBuyFeeEUR:                spotBuyFeeEUR,
		PerpOpenFeeEUR:               perpOpenFeeEUR,
		PerpCloseFeeEUR:              perpCloseFeeEUR,
		FundingCostEUR:               fundingCostEUR,
		InitialMarginEUR:             initialMarginEUR,
		StressLossEUR:                stressLossEUR,
		EquityAfterStressEUR:         equityAfterStressEUR,
		MaintenanceRequirementEUR:    maintenanceRequirementEUR,
		FreeCollateralAfterStressEUR: freeCollateralAfterStressEUR,
		UsedPartialHedge:             hedgeRatio < clamp(cfg.TargetHedgeRatio, 0.0, 1.0),
	}
}

func leverageCandidates(cfg Config) []float64 {
	maxLeverage := math.Min(cfg.MaxLeverage, configuredMaxSupportedLeverage())
	step := cfg.LeverageStep
	if step <= 0 {
		step = 1
	}

	var vals []float64
	for lev := 1.0; lev <= maxLeverage; lev += step {
		vals = append(vals, roundTo(lev, 10))
	}
	vals = append(vals, roundTo(maxLeverage, 10))
	slices.Sort(vals)
	return dedupeSortedAscending(vals)
}

func hedgeRatioCandidates(cfg Config) []float64 {
	target := clamp(cfg.TargetHedgeRatio, 0.0, 1.0)
	if !cfg.AllowPartialHedge || almostEqual(target, 0) {
		return []float64{target}
	}

	step := cfg.HedgeRatioStep
	if step <= 0 || step >= 1 {
		step = 0.05
	}

	var out []float64
	for h := target; h > 0; h -= step {
		out = append(out, roundTo(h, 10))
		if h-step <= 0 {
			break
		}
	}
	return dedupeSortedDescending(out)
}

func validateConfig(cfg Config) error {
	switch {
	case cfg.TotalEUR <= 0:
		return errors.New("TotalEUR must be > 0")
	case cfg.BTCPriceEUR <= 0:
		return errors.New("BTCPriceEUR must be > 0")
	case cfg.TransferMinutes < 0:
		return errors.New("TransferMinutes must be >= 0")
	case cfg.StressMovePct < 0:
		return errors.New("StressMovePct must be >= 0")
	case cfg.MaxLeverage <= 0:
		return errors.New("MaxLeverage must be > 0")
	case cfg.TargetHedgeRatio < 0 || cfg.TargetHedgeRatio > 1:
		return errors.New("TargetHedgeRatio must be between 0 and 1")
	case cfg.SpotBuyFeeRate < 0:
		return errors.New("SpotBuyFeeRate must be >= 0")
	case cfg.PerpOpenFeeRate < 0:
		return errors.New("PerpOpenFeeRate must be >= 0")
	case cfg.PerpCloseFeeRate < 0:
		return errors.New("PerpCloseFeeRate must be >= 0")
	case cfg.MaintenanceMarginRate < 0:
		return errors.New("MaintenanceMarginRate must be >= 0")
	case cfg.SafetyBufferEUR < 0:
		return errors.New("SafetyBufferEUR must be >= 0")
	case cfg.SearchStepEUR <= 0:
		return errors.New("SearchStepEUR must be > 0")
	case cfg.LeverageStep < 0:
		return errors.New("LeverageStep must be >= 0")
	}
	return nil
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func roundTo(v float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(v*pow) / pow
}

func almostEqual(a, b float64) bool {
	const eps = 1e-9
	return math.Abs(a-b) < eps
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func dedupeSortedDescending(vals []float64) []float64 {
	if len(vals) == 0 {
		return vals
	}
	out := []float64{vals[0]}
	for i := 1; i < len(vals); i++ {
		if !almostEqual(vals[i], out[len(out)-1]) {
			out = append(out, vals[i])
		}
	}
	return out
}

func dedupeSortedAscending(vals []float64) []float64 {
	if len(vals) == 0 {
		return vals
	}
	out := []float64{vals[0]}
	for i := 1; i < len(vals); i++ {
		if !almostEqual(vals[i], out[len(out)-1]) {
			out = append(out, vals[i])
		}
	}
	return out
}
