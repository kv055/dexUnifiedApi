package hedge

import "testing"

func TestLoadHedgeConfig(t *testing.T) {
	cfg, err := loadHedgeConfig()
	if err != nil {
		t.Fatalf("loadHedgeConfig returned error: %v", err)
	}
	if cfg.API.KrakenFuturesBaseURL == "" {
		t.Fatal("expected Kraken API base URL in config")
	}
	if cfg.OptimizerDefaults.MaxSupportedLeverage <= 0 {
		t.Fatal("expected max supported leverage in config")
	}
	if _, ok := cfg.Venues[string(VenueKrakenFutures)]; !ok {
		t.Fatal("expected Kraken venue config")
	}
	if _, ok := cfg.Venues[string(VenueCoinbaseFutures)]; !ok {
		t.Fatal("expected Coinbase venue config")
	}
}

func TestApplyDefaultOptimizerConfig(t *testing.T) {
	got := applyDefaultOptimizerConfig(Config{})
	assertFloatClose(t, got.StressMovePct, 0.1)
	assertFloatClose(t, got.TargetHedgeRatio, 1)
	assertFloatClose(t, got.SearchStepEUR, 1)
	assertFloatClose(t, got.LeverageStep, 1)
}
