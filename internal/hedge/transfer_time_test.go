package hedge

import (
	"testing"
	"time"
)

func TestFundingCostRateForTransfer(t *testing.T) {
	got := FundingCostRateForTransfer(0.0006, 8*time.Hour, 120)
	want := 0.00015
	assertFloatClose(t, got, want)
}

func TestFundingCostRateForTransferHandlesZeroInputs(t *testing.T) {
	assertFloatClose(t, FundingCostRateForTransfer(0.0006, 0, 120), 0)
	assertFloatClose(t, FundingCostRateForTransfer(0.0006, 8*time.Hour, 0), 0)
}

func TestApplyLivePerpInputsUsesTransferMinutes(t *testing.T) {
	cfg := Config{
		TransferMinutes: 120,
	}
	inputs := LivePerpInputs{
		OpenFeeRate:     0.0004,
		CloseFeeRate:    0.0004,
		FundingRate:     0.0006,
		FundingInterval: 8 * time.Hour,
	}

	got := ApplyLivePerpInputs(cfg, inputs)
	assertFloatClose(t, got.PerpOpenFeeRate, 0.0004)
	assertFloatClose(t, got.PerpCloseFeeRate, 0.0004)
	assertFloatClose(t, got.FundingCostRate, 0.00015)
}
