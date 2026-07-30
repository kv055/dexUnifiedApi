package cexpermissions

import (
	"testing"
	"time"
)

func TestAggregateComplementaryKeysAndOptimisticUnknown(t *testing.T) {
	now := time.Now().UTC()
	got := aggregate(12, "Kraken", []keyResult{
		{checkedAt: &now, permissions: map[string]string{
			FetchDepositAddress: Granted,
			SpotTrading:         Granted,
			ReadBalances:        Denied,
			Withdraw:            Denied,
			HedgePositions:      Unsupported,
		}},
		{checkedAt: &now, permissions: map[string]string{
			FetchDepositAddress: Denied,
			SpotTrading:         Denied,
			ReadBalances:        Granted,
			Withdraw:            Granted,
			HedgePositions:      Unsupported,
		}},
		{checkFailed: true, permissions: map[string]string{}},
	})

	for _, permission := range []string{FetchDepositAddress, SpotTrading, ReadBalances, Withdraw} {
		if got.Permissions[permission] != Granted {
			t.Errorf("%s = %q, want granted", permission, got.Permissions[permission])
		}
	}
	if got.Permissions[HedgePositions] != Unknown {
		t.Errorf("hedge_positions = %q, want unknown because one check failed", got.Permissions[HedgePositions])
	}
	if !got.Spot.Available || !got.Hedge.Available {
		t.Fatalf("unknown must not lock categories: spot=%+v hedge=%+v", got.Spot, got.Hedge)
	}
}

func TestAggregateDeniedUnsupportedAndNoKeys(t *testing.T) {
	got := aggregate(1, "Coinbase", []keyResult{{permissions: map[string]string{
		FetchDepositAddress: Granted,
		SpotTrading:         Granted,
		ReadBalances:        Granted,
		Withdraw:            Denied,
		HedgePositions:      Unsupported,
	}}})
	if got.Spot.Available || len(got.Spot.BlockingPermissions) != 1 || got.Spot.BlockingPermissions[0] != Withdraw {
		t.Fatalf("spot = %+v", got.Spot)
	}
	if got.Hedge.Available || got.Permissions[HedgePositions] != Unsupported {
		t.Fatalf("hedge = %+v permissions=%+v", got.Hedge, got.Permissions)
	}

	empty := aggregate(2, "Bitstamp", nil)
	if empty.ActiveKeyCount != 0 || empty.Spot.Available || empty.Hedge.Available || empty.Spot.Reason == "" {
		t.Fatalf("empty account = %+v", empty)
	}
	if empty.OptimizerSupported {
		t.Fatal("Bitstamp must be returned but marked optimizer-unsupported")
	}
}
