package bitcoin

import (
	"fmt"
	"time"
)

// FeeRecommender generates user-friendly fee recommendations
type FeeRecommender struct {
	analyzer *FeeAnalyzer
}

// NewFeeRecommender creates a new fee recommender
func NewFeeRecommender(analyzer *FeeAnalyzer) *FeeRecommender {
	return &FeeRecommender{analyzer: analyzer}
}

// GetEconomicFee returns the most cost-effective fee recommendation (1+ hour confirmation).
func (fr *FeeRecommender) GetEconomicFee(cost *TransactionCostEstimate, estimate *FeeEstimate) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}
	return &FeeRecommendation{
		Tier:                          "economic",
		FeeRateSatVB:                  estimate.EconomicFee,
		TotalFeeBTC:                   cost.EconomicFee,
		TotalFeeSat:                   cost.EconomicFeeSat,
		EstimatedTime:                 cost.EconomicTime,
		EstimatedInclusionTime:        cost.EconomicInclusionTime,
		EstimatedFullConfirmationTime: cost.EconomicTime,
		Rationale:                     fmt.Sprintf("Slowest option but cheapest. %.8f BTC total.", cost.EconomicFee),
		RiskLevel:                     "medium",
		ProbabilityByBlock:            fr.analyzer.CalculateConfirmationProbability(estimate.EconomicFee, estimate),
	}
}

// GetStandardFee returns the balanced default recommendation (10-60 min confirmation).
func (fr *FeeRecommender) GetStandardFee(cost *TransactionCostEstimate, estimate *FeeEstimate) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}
	return &FeeRecommendation{
		Tier:                          "standard",
		FeeRateSatVB:                  estimate.StandardFee,
		TotalFeeBTC:                   cost.StandardFee,
		TotalFeeSat:                   cost.StandardFeeSat,
		EstimatedTime:                 cost.StandardTime,
		EstimatedInclusionTime:        cost.StandardInclusionTime,
		EstimatedFullConfirmationTime: cost.StandardTime,
		Rationale:                     fmt.Sprintf("Balanced option. Recommended for most transactions. %.8f BTC total.", cost.StandardFee),
		RiskLevel:                     "low",
		ProbabilityByBlock:            fr.analyzer.CalculateConfirmationProbability(estimate.StandardFee, estimate),
	}
}

// GetFastFee returns a faster confirmation recommendation (<10 min).
func (fr *FeeRecommender) GetFastFee(cost *TransactionCostEstimate, estimate *FeeEstimate) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}
	return &FeeRecommendation{
		Tier:                          "fast",
		FeeRateSatVB:                  estimate.FastFee,
		TotalFeeBTC:                   cost.FastFee,
		TotalFeeSat:                   cost.FastFeeSat,
		EstimatedTime:                 cost.FastTime,
		EstimatedInclusionTime:        cost.FastInclusionTime,
		EstimatedFullConfirmationTime: cost.FastTime,
		Rationale:                     fmt.Sprintf("Fast confirmation. Premium of %.8f BTC vs. standard.", cost.FastFee-cost.StandardFee),
		RiskLevel:                     "low",
		ProbabilityByBlock:            fr.analyzer.CalculateConfirmationProbability(estimate.FastFee, estimate),
	}
}

// GetExpressFee returns the fastest confirmation recommendation (next block, ~2 min).
func (fr *FeeRecommender) GetExpressFee(cost *TransactionCostEstimate, estimate *FeeEstimate) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}
	premium := cost.ExpressFee - cost.StandardFee
	return &FeeRecommendation{
		Tier:                          "express",
		FeeRateSatVB:                  estimate.ExpressFee,
		TotalFeeBTC:                   cost.ExpressFee,
		TotalFeeSat:                   cost.ExpressFeeSat,
		EstimatedTime:                 cost.ExpressTime,
		EstimatedInclusionTime:        cost.ExpressInclusionTime,
		EstimatedFullConfirmationTime: cost.ExpressTime,
		Rationale:                     fmt.Sprintf("Fastest option. Premium of %.8f BTC vs. standard.", premium),
		RiskLevel:                     "very_low",
		ProbabilityByBlock:            fr.analyzer.CalculateConfirmationProbability(estimate.ExpressFee, estimate),
	}
}

// GetCustomFee returns a recommendation for a user-specified fee rate in sat/vB.
func (fr *FeeRecommender) GetCustomFee(cost *TransactionCostEstimate, estimate *FeeEstimate, customSatVB int64) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}

	customFeeSat := customSatVB * cost.TransactionSize
	customFeeBTC := float64(customFeeSat) / 100_000_000

	tier := "custom"
	riskLevel := "medium"
	estimatedTime := 30 * time.Minute

	switch {
	case customSatVB >= estimate.ExpressFee:
		tier, riskLevel, estimatedTime = "express", "very_low", 2*time.Minute
	case customSatVB >= estimate.FastFee:
		tier, riskLevel, estimatedTime = "fast", "low", 10*time.Minute
	case customSatVB >= estimate.StandardFee:
		tier, riskLevel, estimatedTime = "standard", "low", 30*time.Minute
	case customSatVB >= estimate.EconomicFee:
		tier, riskLevel, estimatedTime = "economic", "medium", 60*time.Minute
	default:
		riskLevel = "high"
		estimatedTime = 120 * time.Minute
	}

	rationale := fmt.Sprintf("Custom fee at %d sat/vB", customSatVB)
	if customSatVB < estimate.EconomicFee {
		rationale += " (below economic tier, may not confirm)"
	} else if customSatVB > estimate.ExpressFee {
		rationale += fmt.Sprintf(" (%.8f BTC overpayment)", customFeeBTC-cost.ExpressFee)
	}

	return &FeeRecommendation{
		Tier:                          tier,
		FeeRateSatVB:                  customSatVB,
		TotalFeeBTC:                   customFeeBTC,
		TotalFeeSat:                   customFeeSat,
		EstimatedTime:                 estimatedTime,
		EstimatedInclusionTime:        estimatedTime,
		EstimatedFullConfirmationTime: estimatedTime,
		Rationale:                     rationale,
		RiskLevel:                     riskLevel,
		ProbabilityByBlock:            fr.analyzer.CalculateConfirmationProbability(customSatVB, estimate),
	}
}

// GetAllRecommendations returns recommendations for all four standard tiers.
func (fr *FeeRecommender) GetAllRecommendations(cost *TransactionCostEstimate, estimate *FeeEstimate) []*FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}
	return []*FeeRecommendation{
		fr.GetEconomicFee(cost, estimate),
		fr.GetStandardFee(cost, estimate),
		fr.GetFastFee(cost, estimate),
		fr.GetExpressFee(cost, estimate),
	}
}

// RecommendForTier returns the recommendation for a specific tier name.
// Valid tiers: "economic", "standard", "fast", "express".
// Returns nil for unknown tiers or nil inputs.
func (fr *FeeRecommender) RecommendForTier(cost *TransactionCostEstimate, estimate *FeeEstimate, tier string) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}
	switch tier {
	case "economic":
		return fr.GetEconomicFee(cost, estimate)
	case "standard":
		return fr.GetStandardFee(cost, estimate)
	case "fast":
		return fr.GetFastFee(cost, estimate)
	case "express":
		return fr.GetExpressFee(cost, estimate)
	default:
		return nil
	}
}

// RecommendBestTier selects the optimal tier based on current network congestion.
// Low/low_medium → economic; medium → standard; high → fast.
func (fr *FeeRecommender) RecommendBestTier(cost *TransactionCostEstimate, estimate *FeeEstimate, stats *NetworkStats) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}
	if stats == nil {
		return fr.GetStandardFee(cost, estimate)
	}

	switch stats.CongestionLevel {
	case "low", "low_medium":
		return fr.GetEconomicFee(cost, estimate)
	case "medium":
		return fr.GetStandardFee(cost, estimate)
	case "high":
		return fr.GetFastFee(cost, estimate)
	default:
		return fr.GetStandardFee(cost, estimate)
	}
}
