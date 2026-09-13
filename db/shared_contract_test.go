package dbutil_test

import (
	"database/sql"
	"sort"
	"strings"
	"testing"

	"dexunifiedapi/internal/testdb"
)

// TestSharedServiceSchemaContract protects the tables that are read directly
// by Monitor, MM Controller, and Fiat Controller. Unified API remains the sole
// schema owner; consumers may evolve independently as long as these columns
// stay available or are changed through a coordinated migration.
func TestSharedServiceSchemaContract(t *testing.T) {
	db := testdb.Open(t)
	required := map[string][]string{
		"offers": {
			"id", "exchange", "offer_id", "venue_node_pubkey", "venue_node_name", "source_event_id", "source_event_created_at", "venue_protocol_version",
		},
		"mostro_identities": {
			"id", "label", "encrypted_mnemonic", "next_trade_index", "is_active", "created_at", "updated_at",
		},
		"mostro_trade_sessions": {
			"id", "transaction_id", "identity_id", "trade_index", "trade_pubkey", "node_pubkey", "order_id", "request_id", "protocol_version", "state", "last_event_id", "last_error", "session_data",
		},
		"mostro_payments": {
			"id", "session_id", "purpose", "invoice", "payment_hash", "amount_sat", "status",
		},
		"mostro_outbox": {
			"id", "session_id", "request_id", "event_id", "event_json", "action", "relay_acks", "status", "attempts", "next_attempt_at", "last_error",
		},
		"mostro_chat_events": {"session_id", "event_id", "inner_event_id", "event_json", "created_at"},
		"mostro_inbox_events": {
			"event_id", "session_id", "node_pubkey", "event_created_at", "event_json", "plaintext_ciphertext", "processed_at", "processing_error", "received_at",
		},
		"accounts": {
			"id", "provider", "email", "password", "phone", "last_health_check",
			"is_healthy", "is_active", "payment_priority", "payment_tag", "notification_channels", "category", "created_at", "updated_at",
		},
		"api_keys": {
			"id", "account_id", "name", "api_key", "api_secret", "passphrase", "is_active",
			"permissions", "permissions_checked_at", "permission_check_error", "created_at", "updated_at",
		},
		"bank_accounts": {
			"id", "account_id", "label", "owner_name", "currency", "country", "scheme",
			"details_encrypted", "is_active", "created_at", "updated_at",
		},
		"payment_methods": {
			"id", "name", "merge_regex", "category", "account_id",
			"is_active", "is_linked", "priority", "match_order", "alias_list",
			"sources", "currencies", "popularity",
		},
		"transactions": {
			"id", "direction", "fiat_amount", "fiat_currency", "cex", "dex", "status", "status_reason", "payment_method",
			"dex_offer_id", "dex_offer_db_id", "dex_fiat_arrival", "transaction_complete",
			"created_at", "updated_at", "deleted_at",
		},
		"order_lifecycle": {
			"id", "offer_db_id", "snapshot_time", "price", "ref_price", "premium_pct",
		},
		"telegram_settings": {
			"id", "bot_token", "chat_id", "dexmm_webhook_url", "pushpush_webhook_url",
			"config_version", "updated_at",
		},
		"transactional_liquidity_settings": {
			"id", "fiat_currency", "amount_source", "amount_fiat", "trade_mode",
			"target_direction", "lightning_liquidity_source", "supported_routing_profiles",
			"fee_tier", "coupled_hedge", "config_version", "updated_at",
		},
		"fiat_payment_events": {
			"id", "source", "provider", "account_id", "source_event_id", "amount_minor",
			"currency", "status", "matched_transaction_id", "raw_message_hash",
		},
		"fiat_transaction_confirmations": {
			"transaction_id", "account_id", "channel", "payment_event_id", "confirmed_at",
		},
	}

	for table, wantedColumns := range required {
		t.Run(table, func(t *testing.T) {
			rows, err := db.Query(`
				SELECT column_name
				FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1
			`, table)
			if err != nil {
				t.Fatalf("query columns: %v", err)
			}
			defer rows.Close()

			available := make(map[string]struct{})
			for rows.Next() {
				var column string
				if err := rows.Scan(&column); err != nil {
					t.Fatalf("scan column: %v", err)
				}
				available[column] = struct{}{}
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("iterate columns: %v", err)
			}

			var missing []string
			for _, column := range wantedColumns {
				if _, ok := available[column]; !ok {
					missing = append(missing, column)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Fatalf("shared schema table %q is missing columns: %v", table, missing)
			}
		})
	}
}

func TestGlobalEnumTypesAndColumns(t *testing.T) {
	db := testdb.Open(t)

	types := []string{
		"account_provider", "account_category", "payment_method_category",
		"dex_provider", "cex_provider", "fiat_currency", "notification_channel", "wallet_address_category",
		"trade_direction", "transaction_trade_mode", "offer_price_type",
		"routing_profile", "seller_rating_confidence",
	}
	for _, typeName := range types {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM pg_type WHERE typname = $1 AND typtype = 'e'
		)`, typeName).Scan(&exists); err != nil {
			t.Fatalf("check enum %s: %v", typeName, err)
		}
		if !exists {
			t.Errorf("missing enum type %q", typeName)
		}
	}

	columns := map[string]string{
		"accounts.provider":                             "account_provider",
		"accounts.category":                             "account_category",
		"offers.exchange":                               "dex_provider",
		"offers.direction":                              "trade_direction",
		"offers.fiat_currency":                          "fiat_currency",
		"offers.best_route_cex":                         "cex_provider",
		"offers.routing_profile":                        "routing_profile",
		"offers.seller_rating_confidence":               "seller_rating_confidence",
		"snapshots.exchange":                            "dex_provider",
		"snapshots.fiat_currency":                       "fiat_currency",
		"ohlc.fiat_currency":                            "fiat_currency",
		"bank_accounts.currency":                        "fiat_currency",
		"transactions.cex":                              "cex_provider",
		"transactions.direction":                        "trade_direction",
		"transactions.dex":                              "dex_provider",
		"transactions.trade_mode":                       "transaction_trade_mode",
		"transactions.fiat_currency":                    "fiat_currency",
		"payment_methods.category":                      "payment_method_category",
		"payment_methods.sources":                       "_dex_provider",
		"payment_methods.currencies":                    "_fiat_currency",
		"payment_method_chat_templates.exchange":        "dex_provider",
		"payment_method_chat_templates.trade_direction": "text",
		"fiat_payment_events.source":                    "notification_channel",
		"fiat_payment_events.currency":                  "fiat_currency",
		"fiat_tx_push_logs.currency":                    "fiat_currency",
		"fiat_transaction_confirmations.channel":        "notification_channel",
	}
	for qualified, wantType := range columns {
		parts := strings.SplitN(qualified, ".", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid qualified column %q", qualified)
		}
		table, column := parts[0], parts[1]
		var gotType string
		if err := db.QueryRow(`
			SELECT udt_name
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		`, table, column).Scan(&gotType); err != nil {
			t.Fatalf("read type for %s: %v", qualified, err)
		}
		if gotType != wantType {
			t.Errorf("%s type = %q, want %q", qualified, gotType, wantType)
		}
	}

	if _, err := db.Exec(`INSERT INTO offers (exchange, fiat_currency, direction) VALUES ('not-a-dex', 'EUR', 'bid')`); err == nil {
		t.Error("invalid dex provider was accepted")
	}
	if _, err := db.Exec(`INSERT INTO offers (exchange, fiat_currency, direction) VALUES ('mostro', 'EUR', 'ask')`); err != nil {
		t.Errorf("mostro dex provider rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO offers (exchange, fiat_currency, direction) VALUES ('bisq', 'XYZ', 'bid')`); err == nil {
		t.Error("invalid fiat currency was accepted")
	}
}

func TestTransactionalRoutingVersionOnlyBumpsForRoutingChanges(t *testing.T) {
	db := testdb.Open(t)
	var source, feeTier string
	var amount sql.NullFloat64
	var coupled bool
	if err := db.QueryRow(`SELECT amount_source, amount_fiat, fee_tier, coupled_hedge FROM transactional_liquidity_settings WHERE id = 1`).Scan(&source, &amount, &feeTier, &coupled); err != nil {
		t.Fatalf("read routing defaults: %v", err)
	}
	if source != "capped_volume" || amount.Valid || feeTier != "economic" || coupled {
		t.Fatalf("routing defaults = source=%q amount=%v fee=%q coupled=%v", source, amount, feeTier, coupled)
	}
	var before int64
	if err := db.QueryRow(`SELECT config_version FROM transactional_liquidity_settings WHERE id = 1`).Scan(&before); err != nil {
		t.Fatalf("read initial routing version: %v", err)
	}
	if _, err := db.Exec(`UPDATE transactional_liquidity_settings SET amount_source = 'custom', amount_fiat = 500 WHERE id = 1`); err != nil {
		t.Fatalf("update routing amount: %v", err)
	}
	var changed int64
	if err := db.QueryRow(`SELECT config_version FROM transactional_liquidity_settings WHERE id = 1`).Scan(&changed); err != nil {
		t.Fatalf("read changed routing version: %v", err)
	}
	if changed != before+1 {
		t.Fatalf("routing config version = %d, want %d", changed, before+1)
	}
	if _, err := db.Exec(`UPDATE transactional_liquidity_settings SET updated_at = CURRENT_TIMESTAMP WHERE id = 1`); err != nil {
		t.Fatalf("touch routing settings: %v", err)
	}
	var touched int64
	if err := db.QueryRow(`SELECT config_version FROM transactional_liquidity_settings WHERE id = 1`).Scan(&touched); err != nil {
		t.Fatalf("read touched routing version: %v", err)
	}
	if touched != changed {
		t.Fatalf("non-routing update changed version from %d to %d", changed, touched)
	}
}
