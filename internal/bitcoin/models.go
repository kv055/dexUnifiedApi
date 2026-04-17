package bitcoin

import "time"

// NetworkStats represents real-time Bitcoin network state
type NetworkStats struct {
	MempoolSize        int64         `json:"mempool_size"`       // Bytes in mempool
	TransactionCount   int64         `json:"transaction_count"`  // Unconfirmed transactions
	AverageBlockTime   time.Duration `json:"average_block_time"` // Average block production time
	CongestionLevel    string        `json:"congestion_level"`   // low/medium/high
	LastBlockHeight    int32         `json:"last_block_height"`
	LastBlockTime      time.Time     `json:"last_block_time"`
	TotalFeesInMempool float64       `json:"total_fees_in_mempool"` // BTC
}

// FeeEstimate represents recommended fee rates in sat/vB
type FeeEstimate struct {
	EconomicFee int64     `json:"economic_fee"` // 1-3 sat/vB, 1+ hour
	StandardFee int64     `json:"standard_fee"` // 3-10 sat/vB, 10-60 min
	FastFee     int64     `json:"fast_fee"`     // 10-20 sat/vB, <10 min
	ExpressFee  int64     `json:"express_fee"`  // 20+ sat/vB, next block
	MinimumFee  int64     `json:"minimum_fee"`  // Network minimum
	LastUpdated time.Time `json:"last_updated"`
	Confidence  float64   `json:"confidence"` // 0-1.0 confidence score
	Source      string    `json:"source"`     // "mempool.space" or other
}

// TransactionCostEstimate represents the predicted cost of a transaction
type TransactionCostEstimate struct {
	TransactionSize       int64         `json:"transaction_size"` // Virtual bytes (vB)
	EconomicFee           float64       `json:"economic_fee_btc"` // BTC
	StandardFee           float64       `json:"standard_fee_btc"` // BTC
	FastFee               float64       `json:"fast_fee_btc"`     // BTC
	ExpressFee            float64       `json:"express_fee_btc"`  // BTC
	EconomicFeeSat        int64         `json:"economic_fee_sat"` // Satoshis
	StandardFeeSat        int64         `json:"standard_fee_sat"` // Satoshis
	FastFeeSat            int64         `json:"fast_fee_sat"`     // Satoshis
	ExpressFeeSat         int64         `json:"express_fee_sat"`  // Satoshis
	EconomicInclusionTime time.Duration `json:"economic_inclusion_time"`
	StandardInclusionTime time.Duration `json:"standard_inclusion_time"`
	FastInclusionTime     time.Duration `json:"fast_inclusion_time"`
	ExpressInclusionTime  time.Duration `json:"express_inclusion_time"`
	EconomicTime          time.Duration `json:"economic_time"`
	StandardTime          time.Duration `json:"standard_time"`
	FastTime              time.Duration `json:"fast_time"`
	ExpressTime           time.Duration `json:"express_time"`
	NetworkCongestion     string        `json:"network_congestion"`
	EstimatedConfidence   float64       `json:"estimated_confidence"`
	RecommendedTier       string        `json:"recommended_tier"` // "economic", "standard", "fast"
	Timestamp             time.Time     `json:"timestamp"`
}

// FeeRecommendation represents a fee recommendation with rationale
type FeeRecommendation struct {
	Tier                          string        `json:"tier"` // economic, standard, fast, express
	FeeRateSatVB                  int64         `json:"fee_rate_sat_vb"`
	TotalFeeBTC                   float64       `json:"total_fee_btc"`
	TotalFeeSat                   int64         `json:"total_fee_sat"`
	EstimatedTime                 time.Duration `json:"estimated_time"`
	EstimatedInclusionTime        time.Duration `json:"estimated_inclusion_time"`
	EstimatedFullConfirmationTime time.Duration `json:"estimated_full_confirmation_time"`
	Rationale                     string        `json:"rationale"`
	RiskLevel                     string        `json:"risk_level"`           // low, medium, high
	ProbabilityByBlock            []float64     `json:"probability_by_block"` // Confirmation probability per block
}

// MempoolData represents parsed mempool information
type MempoolData struct {
	BlockTarget         map[string]int64         `json:"block_target"` // "1": 50, "2": 30, etc (sat/vB)
	AverageFeeRate      int64                    `json:"average_fee_rate"`
	MinFeeRate          int64                    `json:"min_fee_rate"`
	MaxFeeRate          int64                    `json:"max_fee_rate"`
	MedianFeeRate       int64                    `json:"median_fee_rate"`
	PercentileFeeRates  map[string]int64         `json:"percentile_fee_rates"` // "10": 5, "25": 10, "75": 20, etc
	ProjectedBlocks     []MempoolBlockProjection `json:"projected_blocks"`
	TotalUnconfirmedTxs int64                    `json:"total_unconfirmed_txs"`
	TotalMemoryUsed     int64                    `json:"total_memory_used"`
	FetchedAt           time.Time                `json:"fetched_at"`
}

// MempoolBlockProjection is mempool.space's estimate for one upcoming block template.
type MempoolBlockProjection struct {
	BlockVSize    int64   `json:"block_vsize"`
	NTransactions int64   `json:"n_transactions"`
	MedianFee     int64   `json:"median_fee"`
	FeeRange      []int64 `json:"fee_range"`
}

// BitcoinFeeResponse represents the raw response from mempool.space API
type BitcoinFeeResponse struct {
	FastestFee  int64 `json:"fastestFee"`  // sat/vB
	HalfHourFee int64 `json:"halfHourFee"` // sat/vB
	HourFee     int64 `json:"hourFee"`     // sat/vB
	EconomyFee  int64 `json:"economyFee"`  // sat/vB
	MinimumFee  int64 `json:"minimumFee"`  // sat/vB
}

// FeeHistoryEntry tracks historical fee data
type FeeHistoryEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	FeeRate     int64     `json:"fee_rate"`     // sat/vB
	Tier        string    `json:"tier"`         // economic, standard, fast
	MempoolSize int64     `json:"mempool_size"` // Bytes
	BlockHeight int32     `json:"block_height"`
}
