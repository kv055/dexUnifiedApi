package hedge

import (
	"fmt"
	"time"

	"dexunifiedapi/internal/bitcoin"
)

// EstimateTransferMinutes uses the existing bitcoin predictor to estimate the
// full confirmation time for the specified transaction profile and fee tier.
// This gives the hedge module a transfer-duration input without introducing any
// BTC price forecasting.
func EstimateTransferMinutes(
	predictor *bitcoin.BitcoinFeePredictor,
	inputCount int,
	outputCount int,
	txType string,
	requiredConfirmations int,
	feeTier string,
) (float64, error) {
	if predictor == nil {
		return 0, fmt.Errorf("bitcoin predictor is required")
	}

	ctx, err := predictor.GetDetailedRecommendationForTier(inputCount, outputCount, txType, requiredConfirmations, feeTier)
	if err != nil {
		return 0, err
	}
	if ctx == nil || ctx.Recommendation == nil {
		return 0, fmt.Errorf("bitcoin predictor returned no recommendation")
	}

	return ctx.Recommendation.EstimatedFullConfirmationTime.Minutes(), nil
}

// FundingCostRateForTransfer prorates a live funding rate over the expected
// transfer duration. The result is the total funding rate to use for the
// optimizer's holding window.
func FundingCostRateForTransfer(fundingRate float64, fundingInterval time.Duration, transferMinutes float64) float64 {
	if fundingInterval <= 0 || transferMinutes <= 0 {
		return 0
	}
	return fundingRate * (transferMinutes / fundingInterval.Minutes())
}

// ApplyLivePerpInputs copies live perp fees into the optimizer config and
// converts the venue funding rate into a holding-window funding cost using the
// config's TransferMinutes.
func ApplyLivePerpInputs(cfg Config, inputs LivePerpInputs) Config {
	cfg.PerpOpenFeeRate = inputs.OpenFeeRate
	cfg.PerpCloseFeeRate = inputs.CloseFeeRate
	cfg.FundingCostRate = FundingCostRateForTransfer(inputs.FundingRate, inputs.FundingInterval, cfg.TransferMinutes)
	return cfg
}

// PrepareConfigForVenue applies hardcoded venue defaults and live perp inputs to
// the optimizer config. The caller remains responsible for supplying TotalEUR,
// BTCPriceEUR, TransferMinutes, stress assumptions, and hedge-ratio policy.
func PrepareConfigForVenue(cfg Config, venue Venue) (Config, LivePerpInputs, error) {
	profile, err := DefaultVenueProfile(venue)
	if err != nil {
		return Config{}, LivePerpInputs{}, err
	}

	inputs, err := FetchDefaultVenueInputs(venue)
	if err != nil {
		return Config{}, LivePerpInputs{}, err
	}

	cfg = ApplyVenueProfile(cfg, profile)
	cfg = ApplyLivePerpInputs(cfg, inputs)
	return cfg, inputs, nil
}
