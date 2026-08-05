package dbutil_test

import (
	"sort"
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
			"is_healthy", "is_active", "payment_priority", "category", "created_at", "updated_at",
		},
		"api_keys": {
			"id", "account_id", "name", "api_key", "api_secret", "passphrase", "is_active",
			"permissions", "permissions_checked_at", "permission_check_error", "created_at", "updated_at",
		},
		"bank_accounts": {
			"id", "account_id", "label", "owner_name", "currency", "country", "scheme",
			"details", "is_active", "created_at", "updated_at",
		},
		"payment_methods": {
			"id", "name", "merge_regex", "category", "account_id",
			"is_active", "is_linked", "priority", "match_order",
			"sources", "currencies", "popularity",
		},
		"transactions": {
			"id", "direction", "fiat_amount", "fiat_currency", "cex", "dex", "status",
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
