package dbutil_test

import (
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
			"id", "direction", "fiat_amount", "fiat_currency", "cex", "dex", "status", "payment_method",
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
		"accounts.provider":                      "account_provider",
		"accounts.category":                      "account_category",
		"offers.exchange":                        "dex_provider",
		"offers.direction":                       "trade_direction",
		"offers.fiat_currency":                   "fiat_currency",
		"offers.best_route_cex":                  "cex_provider",
		"offers.routing_profile":                 "routing_profile",
		"offers.seller_rating_confidence":        "seller_rating_confidence",
		"snapshots.exchange":                     "dex_provider",
		"snapshots.fiat_currency":                "fiat_currency",
		"ohlc.fiat_currency":                     "fiat_currency",
		"bank_accounts.currency":                 "fiat_currency",
		"transactions.cex":                       "cex_provider",
		"transactions.direction":                 "trade_direction",
		"transactions.dex":                       "dex_provider",
		"transactions.trade_mode":                "transaction_trade_mode",
		"transactions.fiat_currency":             "fiat_currency",
		"payment_methods.category":               "payment_method_category",
		"payment_methods.sources":                "_dex_provider",
		"payment_methods.currencies":             "_fiat_currency",
		"payment_method_chat_templates.exchange": "dex_provider",
		"fiat_payment_events.source":             "notification_channel",
		"fiat_payment_events.currency":           "fiat_currency",
		"fiat_tx_push_logs.currency":             "fiat_currency",
		"fiat_transaction_confirmations.channel": "notification_channel",
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

	if _, err := db.Exec(`INSERT INTO offers (exchange, fiat_currency, direction) VALUES ('not-a-dex', 'EUR', 'buy')`); err == nil {
		t.Error("invalid dex provider was accepted")
	}
	if _, err := db.Exec(`INSERT INTO offers (exchange, fiat_currency, direction) VALUES ('bisq', 'XYZ', 'buy')`); err == nil {
		t.Error("invalid fiat currency was accepted")
	}
}
