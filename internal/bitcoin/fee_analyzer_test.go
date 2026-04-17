package bitcoin

import (
	"testing"
	"time"
)

func TestAnalyzeAndPredictUsesMempoolBlocksForTierTimes(t *testing.T) {
	analyzer := NewFeeAnalyzer()
	err := analyzer.AnalyzeFeeDistribution(&MempoolData{
		ProjectedBlocks: []MempoolBlockProjection{
			{FeeRange: []int64{40, 60}},
			{FeeRange: []int64{20, 39}},
			{FeeRange: []int64{10, 19}},
			{FeeRange: []int64{5, 9}},
		},
		AverageFeeRate: 20,
		MinFeeRate:     5,
		MaxFeeRate:     60,
	})
	if err != nil {
		t.Fatalf("AnalyzeFeeDistribution returned error: %v", err)
	}

	estimate := analyzer.AnalyzeAndPredictWithConfirmations(&FeeEstimate{
		EconomicFee: 5,
		StandardFee: 20,
		FastFee:     40,
		ExpressFee:  60,
		Confidence:  0.95,
	}, &NetworkStats{
		AverageBlockTime: 10 * time.Minute,
		MempoolSize:      60 * 1024 * 1024,
	}, 120, "P2WPKH", 6)

	if estimate == nil {
		t.Fatal("AnalyzeAndPredict returned nil estimate")
	}

	if estimate.ExpressInclusionTime != 10*time.Minute {
		t.Fatalf("ExpressInclusionTime = %v, want 10m", estimate.ExpressInclusionTime)
	}
	if estimate.FastInclusionTime != 10*time.Minute {
		t.Fatalf("FastInclusionTime = %v, want 10m", estimate.FastInclusionTime)
	}
	if estimate.StandardInclusionTime != 20*time.Minute {
		t.Fatalf("StandardInclusionTime = %v, want 20m", estimate.StandardInclusionTime)
	}
	if estimate.EconomicInclusionTime != 40*time.Minute {
		t.Fatalf("EconomicInclusionTime = %v, want 40m", estimate.EconomicInclusionTime)
	}

	if estimate.ExpressTime != 60*time.Minute {
		t.Fatalf("ExpressTime = %v, want 60m", estimate.ExpressTime)
	}
	if estimate.StandardTime != 70*time.Minute {
		t.Fatalf("StandardTime = %v, want 70m", estimate.StandardTime)
	}
	if estimate.EconomicTime != 90*time.Minute {
		t.Fatalf("EconomicTime = %v, want 90m", estimate.EconomicTime)
	}
}

func TestEstimateProjectedInclusionBlocksExtrapolatesBelowVisibleRange(t *testing.T) {
	analyzer := NewFeeAnalyzer()

	blocks := analyzer.estimateProjectedInclusionBlocks(2, &MempoolData{
		ProjectedBlocks: []MempoolBlockProjection{
			{FeeRange: []int64{30, 40}},
			{FeeRange: []int64{20, 29}},
			{FeeRange: []int64{10, 19}},
		},
	})

	if blocks != 7 {
		t.Fatalf("projected blocks = %d, want 7", blocks)
	}
}
