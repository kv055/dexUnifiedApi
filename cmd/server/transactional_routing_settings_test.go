package main

import (
	"context"
	"dexunifiedapi/internal/testdb"
	"testing"
)

func TestRefreshTransactionalRoutingSettingsUsesCanonicalAmountFiatColumn(t *testing.T) {
	oldDB := db
	oldHub := hub
	t.Cleanup(func() {
		db = oldDB
		hub = oldHub
	})

	testDB := testdb.Open(t)
	if _, err := testDB.Exec(`
		UPDATE transactional_liquidity_settings
		SET fiat_currency = 'EUR', amount_source = 'capped_volume', amount_fiat = 300,
			trade_mode = 'hedgeless_double_collateral', target_direction = 'bid', config_version = 42
		WHERE id = 1
	`); err != nil {
		t.Fatalf("seed transactional routing settings: %v", err)
	}

	db = testDB
	hub = &LiveHub{subscribers: make(map[chan []byte]liveSubscription)}
	if err := refreshTransactionalRoutingSettings(context.Background()); err != nil {
		t.Fatalf("refresh transactional routing settings: %v", err)
	}

	settings := hub.transactionalRoutingSettings()
	if settings.AmountFiat == nil || *settings.AmountFiat != 300 {
		t.Fatalf("amount_fiat = %v, want 300", settings.AmountFiat)
	}
	if settings.ConfigVersion <= 0 || settings.TradeMode != "hedgeless_double_collateral" || settings.TargetDirection != "bid" {
		t.Fatalf("settings = %#v", settings)
	}
}
