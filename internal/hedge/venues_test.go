package hedge

import "testing"

func TestDefaultVenueProfileByNameKraken(t *testing.T) {
	got, err := DefaultVenueProfileByName("kraken")
	if err != nil {
		t.Fatalf("DefaultVenueProfileByName returned error: %v", err)
	}
	if got.Instrument != "PI_XBTUSD" {
		t.Fatalf("instrument = %q, want PI_XBTUSD", got.Instrument)
	}
	assertFloatClose(t, got.MaxLeverage, 5)
	assertFloatClose(t, got.MaintenanceMarginRate, 0.01)
}

func TestDefaultVenueProfileByNameCoinbase(t *testing.T) {
	got, err := DefaultVenueProfileByName("coinbase")
	if err != nil {
		t.Fatalf("DefaultVenueProfileByName returned error: %v", err)
	}
	if got.Instrument != "BTC-PERP" {
		t.Fatalf("instrument = %q, want BTC-PERP", got.Instrument)
	}
	assertFloatClose(t, got.MaxLeverage, 10)
	assertFloatClose(t, got.MaintenanceMarginRate, 0.01)
}

func TestApplyVenueProfile(t *testing.T) {
	cfg := Config{}
	profile, err := DefaultVenueProfile(VenueCoinbaseFutures)
	if err != nil {
		t.Fatalf("DefaultVenueProfile returned error: %v", err)
	}

	got := ApplyVenueProfile(cfg, profile)
	assertFloatClose(t, got.MaxLeverage, 10)
	assertFloatClose(t, got.MaintenanceMarginRate, 0.01)
}

func TestDefaultVenueProfileByNameRejectsUnknown(t *testing.T) {
	if _, err := DefaultVenueProfileByName("binance"); err == nil {
		t.Fatal("expected unsupported venue error")
	}
}
