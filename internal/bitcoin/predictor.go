package bitcoin

import (
	"fmt"
	"net/http"
	"time"
)

// lightningTypicalHops is the assumed number of routing hops for a Lightning payment.
const lightningTypicalHops = 3

// failedFetchCooldown is how long to wait before retrying after a failed fetch.
// Much shorter than the normal TTL so transient errors recover quickly.
const failedFetchCooldown = 2 * time.Minute

// BitcoinFeePredictor orchestrates fee fetching, analysis, and recommendations.
type BitcoinFeePredictor struct {
	fetcher         *NetworkDataFetcher
	analyzer        *FeeAnalyzer
	recommender     *FeeRecommender
	lastFeeData     *FeeEstimate
	lastNetStats    *NetworkStats
	lastMempoolData *MempoolData
	lastLightning   *LightningFeeStats
	lastUpdateTime  time.Time // last successful fetch
	lastAttemptTime time.Time // last fetch attempt (success or failure)
}

// NewBitcoinFeePredictor creates a fee predictor with a plain HTTP client.
func NewBitcoinFeePredictor() *BitcoinFeePredictor {
	return NewBitcoinFeePredictorWithClient(nil)
}

// NewBitcoinFeePredictorWithClient creates a fee predictor using the given HTTP client.
// Pass a Tor-proxied client when running in an environment that routes clearnet through Tor.
// A nil client falls back to the default plain HTTP client.
func NewBitcoinFeePredictorWithClient(client *http.Client) *BitcoinFeePredictor {
	var fetcher *NetworkDataFetcher
	if client != nil {
		fetcher = NewNetworkDataFetcherWithClient(client)
	} else {
		fetcher = NewNetworkDataFetcher()
	}
	analyzer := NewFeeAnalyzer()
	return &BitcoinFeePredictor{
		fetcher:     fetcher,
		analyzer:    analyzer,
		recommender: NewFeeRecommender(analyzer),
	}
}

// FetchLatestData fetches fresh fee rates and network stats from mempool.space.
// Call this before any prediction methods, or rely on IsDataStale to gate refreshes.
func (bp *BitcoinFeePredictor) FetchLatestData() error {
	// Always stamp the attempt time so we don't hammer the API on repeated failures.
	// On failure, IsDataStale will allow a retry after failedFetchCooldown (2 min).
	// On success, lastUpdateTime is set and the normal maxAge TTL applies.
	bp.lastAttemptTime = time.Now()

	feeData, err := bp.fetcher.FetchRecommendedFees()
	if err != nil {
		return fmt.Errorf("failed to fetch fee data: %w", err)
	}

	stats, err := bp.fetcher.FetchNetworkStats()
	if err != nil {
		return fmt.Errorf("failed to fetch network stats: %w", err)
	}

	mempool, err := bp.fetcher.FetchMempoolData()
	if err != nil {
		return fmt.Errorf("failed to fetch mempool data: %w", err)
	}
	if err := bp.analyzer.AnalyzeFeeDistribution(mempool); err != nil {
		return fmt.Errorf("failed to analyze mempool data: %w", err)
	}

	bp.lastFeeData = feeData
	bp.lastNetStats = stats
	bp.lastMempoolData = mempool
	bp.lastUpdateTime = time.Now()

	// Lightning stats are best-effort — a failure here does not block onchain predictions.
	if ln, err := bp.fetcher.FetchLightningFeeStats(); err == nil {
		bp.lastLightning = ln
	}

	return nil
}

// PredictTransactionCost predicts the cost of a Bitcoin transaction across all fee tiers.
//   - inputCount:  number of UTXOs being spent
//   - outputCount: number of outputs
//   - txType:      "P2PKH", "P2SH", "P2WPKH", "P2WSH", or "P2TR"
func (bp *BitcoinFeePredictor) PredictTransactionCost(inputCount int, outputCount int, txType string) (*TransactionCostEstimate, error) {
	return bp.PredictTransactionCostWithConfirmations(inputCount, outputCount, txType, defaultRequiredConfirmations)
}

// PredictTransactionCostWithConfirmations predicts fees and confirmation timing for a
// caller-selected number of confirmations.
func (bp *BitcoinFeePredictor) PredictTransactionCostWithConfirmations(inputCount int, outputCount int, txType string, requiredConfirmations int) (*TransactionCostEstimate, error) {
	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available, call FetchLatestData first")
	}

	virtualSize := bp.analyzer.CalculateVirtualSize(inputCount, outputCount, txType)
	estimate := bp.analyzer.AnalyzeAndPredictWithConfirmations(bp.lastFeeData, bp.lastNetStats, virtualSize, txType, requiredConfirmations)
	return estimate, nil
}

// GetFeeRecommendations returns all four standard tier recommendations for a transaction.
func (bp *BitcoinFeePredictor) GetFeeRecommendations(inputCount int, outputCount int, txType string) ([]*FeeRecommendation, error) {
	costEstimate, err := bp.PredictTransactionCost(inputCount, outputCount, txType)
	if err != nil {
		return nil, err
	}
	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available")
	}
	return bp.recommender.GetAllRecommendations(costEstimate, bp.lastFeeData), nil
}

// GetRecommendedFee returns the best tier recommendation based on current congestion.
func (bp *BitcoinFeePredictor) GetRecommendedFee(inputCount int, outputCount int, txType string) (*FeeRecommendation, error) {
	costEstimate, err := bp.PredictTransactionCost(inputCount, outputCount, txType)
	if err != nil {
		return nil, err
	}
	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available")
	}
	return bp.recommender.RecommendBestTier(costEstimate, bp.lastFeeData, bp.lastNetStats), nil
}

// GetCurrentNetworkState returns the cached NetworkStats. Requires a prior FetchLatestData call.
func (bp *BitcoinFeePredictor) GetCurrentNetworkState() (*NetworkStats, error) {
	if bp.lastNetStats == nil {
		return nil, fmt.Errorf("no network data available, call FetchLatestData first")
	}
	return bp.lastNetStats, nil
}

// GetCurrentFeeEstimate returns the cached FeeEstimate. Requires a prior FetchLatestData call.
func (bp *BitcoinFeePredictor) GetCurrentFeeEstimate() (*FeeEstimate, error) {
	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available, call FetchLatestData first")
	}
	return bp.lastFeeData, nil
}

// GetSimpleFeeEstimate returns all four tier rates in sat/vB without a cost breakdown.
// Returns: (economicSatVB, standardSatVB, fastSatVB, expressSatVB, error)
func (bp *BitcoinFeePredictor) GetSimpleFeeEstimate() (int64, int64, int64, int64, error) {
	if bp.lastFeeData == nil {
		return 0, 0, 0, 0, fmt.Errorf("no fee data available")
	}
	return bp.lastFeeData.EconomicFee, bp.lastFeeData.StandardFee, bp.lastFeeData.FastFee, bp.lastFeeData.ExpressFee, nil
}

// GetRecommendedFeeWithConfidence returns the congestion-adaptive fee recommendation
// together with the overall confidence score derived from the current network estimate.
// This avoids two separate round-trips through PredictTransactionCost.
func (bp *BitcoinFeePredictor) GetRecommendedFeeWithConfidence(inputCount int, outputCount int, txType string) (*FeeRecommendation, float64, error) {
	estimate, err := bp.PredictTransactionCost(inputCount, outputCount, txType)
	if err != nil {
		return nil, 0, err
	}
	rec := bp.recommender.RecommendBestTier(estimate, bp.lastFeeData, bp.lastNetStats)
	return rec, estimate.EstimatedConfidence, nil
}

// EstimateCustomFee estimates cost for a caller-specified sat/vB rate.
func (bp *BitcoinFeePredictor) EstimateCustomFee(inputCount int, outputCount int, txType string, customSatVB int64) (*FeeRecommendation, error) {
	costEstimate, err := bp.PredictTransactionCost(inputCount, outputCount, txType)
	if err != nil {
		return nil, err
	}
	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available")
	}
	return bp.recommender.GetCustomFee(costEstimate, bp.lastFeeData, customSatVB), nil
}

// GetNetworkConditionsSummary returns a human-readable snapshot of current network state.
func (bp *BitcoinFeePredictor) GetNetworkConditionsSummary() (string, error) {
	if bp.lastNetStats == nil || bp.lastFeeData == nil {
		return "", fmt.Errorf("no data available")
	}
	return fmt.Sprintf(
		"Bitcoin Network Status:\n"+
			"  Congestion: %s\n"+
			"  Active Transactions: %d\n"+
			"  Mempool Size: %.2f MB\n"+
			"  Block Height: %d\n"+
			"  Fee Rates (sat/vB):\n"+
			"    Economic: %d\n"+
			"    Standard: %d\n"+
			"    Fast: %d\n"+
			"    Express: %d",
		bp.lastNetStats.CongestionLevel,
		bp.lastNetStats.TransactionCount,
		float64(bp.lastNetStats.MempoolSize)/(1024*1024),
		bp.lastNetStats.LastBlockHeight,
		bp.lastFeeData.EconomicFee,
		bp.lastFeeData.StandardFee,
		bp.lastFeeData.FastFee,
		bp.lastFeeData.ExpressFee,
	), nil
}

// DetailedFeeContext bundles everything needed to build a rich fee breakdown for display.
type DetailedFeeContext struct {
	Recommendation *FeeRecommendation
	Estimate       *TransactionCostEstimate
	FeeRates       *FeeEstimate
	NetworkStats   *NetworkStats
	DataAgeMinutes int
	TxInputs       int
	TxOutputs      int
	TxType         string
}

// GetDetailedRecommendation returns the congestion-adaptive recommendation together with
// all intermediate data (vsize, all tier rates, mempool state) for display purposes.
func (bp *BitcoinFeePredictor) GetDetailedRecommendation(inputCount, outputCount int, txType string) (*DetailedFeeContext, error) {
	return bp.GetDetailedRecommendationWithConfirmations(inputCount, outputCount, txType, defaultRequiredConfirmations)
}

// GetDetailedRecommendationForTier returns the recommendation for a specific fee tier
// together with all intermediate data. If tier is empty, falls back to congestion-adaptive.
func (bp *BitcoinFeePredictor) GetDetailedRecommendationForTier(inputCount, outputCount int, txType string, requiredConfirmations int, tier string) (*DetailedFeeContext, error) {
	estimate, err := bp.PredictTransactionCostWithConfirmations(inputCount, outputCount, txType, requiredConfirmations)
	if err != nil {
		return nil, err
	}

	var rec *FeeRecommendation
	if tier != "" {
		rec = bp.recommender.RecommendForTier(estimate, bp.lastFeeData, tier)
	}
	if rec == nil {
		rec = bp.recommender.RecommendBestTier(estimate, bp.lastFeeData, bp.lastNetStats)
	}

	ageMinutes := 0
	if !bp.lastUpdateTime.IsZero() {
		ageMinutes = int(time.Since(bp.lastUpdateTime).Minutes())
	}

	return &DetailedFeeContext{
		Recommendation: rec,
		Estimate:       estimate,
		FeeRates:       bp.lastFeeData,
		NetworkStats:   bp.lastNetStats,
		DataAgeMinutes: ageMinutes,
		TxInputs:       inputCount,
		TxOutputs:      outputCount,
		TxType:         txType,
	}, nil
}

// GetDetailedRecommendationWithConfirmations returns the congestion-adaptive
// recommendation for a caller-selected confirmation target.
func (bp *BitcoinFeePredictor) GetDetailedRecommendationWithConfirmations(inputCount, outputCount int, txType string, requiredConfirmations int) (*DetailedFeeContext, error) {
	estimate, err := bp.PredictTransactionCostWithConfirmations(inputCount, outputCount, txType, requiredConfirmations)
	if err != nil {
		return nil, err
	}
	rec := bp.recommender.RecommendBestTier(estimate, bp.lastFeeData, bp.lastNetStats)

	ageMinutes := 0
	if !bp.lastUpdateTime.IsZero() {
		ageMinutes = int(time.Since(bp.lastUpdateTime).Minutes())
	}

	return &DetailedFeeContext{
		Recommendation: rec,
		Estimate:       estimate,
		FeeRates:       bp.lastFeeData,
		NetworkStats:   bp.lastNetStats,
		DataAgeMinutes: ageMinutes,
		TxInputs:       inputCount,
		TxOutputs:      outputCount,
		TxType:         txType,
	}, nil
}

// EstimateLightningRoutingFee returns an approximate Lightning routing fee in BTC
// for a payment of amountBTC, based on current network median fee rates.
// Formula: (amount_sat × med_fee_rate_ppm / 1e6 + avg_base_fee_sat) × hops
// Falls back to 500 sats if no Lightning data has been fetched yet.
func (bp *BitcoinFeePredictor) EstimateLightningRoutingFee(amountBTC float64) float64 {
	const fallbackSats = 500.0

	if bp.lastLightning == nil {
		return fallbackSats / 1e8
	}

	amountSat := amountBTC * 1e8
	baseFeeSat := float64(bp.lastLightning.AvgBaseFeeMsat) / 1000.0
	feeSat := (amountSat*float64(bp.lastLightning.MedFeeRatePPM)/1_000_000.0 + baseFeeSat) * lightningTypicalHops
	return feeSat / 1e8
}

// IsDataStale reports whether the cached data is older than maxAge.
// After a failed fetch (lastUpdateTime not set or older than lastAttemptTime),
// it returns true only after failedFetchCooldown has elapsed since the last attempt,
// preventing the caller from hammering the API on repeated failures.
func (bp *BitcoinFeePredictor) IsDataStale(maxAge time.Duration) bool {
	// Have good data and it's fresh enough → not stale.
	if !bp.lastUpdateTime.IsZero() && time.Since(bp.lastUpdateTime) <= maxAge {
		return false
	}
	// Data is stale or missing. If we recently attempted a fetch, respect the cooldown.
	if !bp.lastAttemptTime.IsZero() && time.Since(bp.lastAttemptTime) < failedFetchCooldown {
		return false
	}
	return true
}

// GetLastUpdateTime returns when data was last successfully fetched.
func (bp *BitcoinFeePredictor) GetLastUpdateTime() time.Time {
	return bp.lastUpdateTime
}

// GetDataAge returns how long ago the data was fetched, or -1 if never fetched.
func (bp *BitcoinFeePredictor) GetDataAge() time.Duration {
	if bp.lastUpdateTime.IsZero() {
		return -1
	}
	return time.Since(bp.lastUpdateTime)
}
