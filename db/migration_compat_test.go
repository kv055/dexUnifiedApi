package dbutil

import (
	"regexp"
	"strings"
	"testing"
)

// grandfatheredMigrations predates the rollback-compatibility rule: these
// files are already applied everywhere, so re-litigating them would fail the
// suite on day one. Every new migration must declare its rollback story and
// avoid unambiguously breaking DDL instead.
var grandfatheredMigrations = map[string]bool{
	"20260817000000_repair_transactional_liquidity_lightning_source.sql":    true,
	"20260818120000_lsp_magma_orders.sql":                                   true,
	"20260819015200_transactional_liquidity_supported_routing_profiles.sql": true,
	"20260819220000_live_market_observation_and_maker_id.sql":               true,
	"20260823171500_repair_cex_provider_fee_columns.sql":                    true,
	"20260829120000_hedgeless_security_deposit_return.sql":                  true,
	"20260829210000_transaction_status_contract.sql":                        true,
	"20260830153000_repair_legacy_varchar_enums.sql":                        true,
	"20260831120000_cex_fee_components_and_observations.sql":                true,
	"20260907192500_add_mostro_dex_provider.sql":                            true,
	"20260910132000_counterparty_stake.sql":                                 true,
	"20260910165400_seller_rating_signals.sql":                              true,
	"20260913120000_mostro_execution_state.sql":                             true,
	"20260913162653_mostro_session_recovery.sql":                            true,
	"20260916100000_robosats_execution_state.sql":                           true,
	"20260916190000_bisq_execution_safety.sql":                              true,
	"20260917090000_bisq_durable_actions.sql":                               true,
	"20260917090100_robosats_durable_admission.sql":                         true,
	"20260920120000_lifecycle_offer_covering_index.sql":                     true,
	"20260920130000_lifecycle_observation_aggregates.sql":                   true,
	"20260925120000_app_settings.sql":                                       true,
	"20260925200000_drop_stale_provider_exchange_checks.sql":                true,
	"28_transactional_liquidity_target_direction.sql":                       true,
	"29_lsp_channel_config.sql":                                             true,
	"30_submarine_swaps.sql":                                                true,
	"31_dex_taken_funding_review.sql":                                       true,
	"32_submarine_swap_reservation_id.sql":                                  true,
	"33_authoritative_snapshot_routing.sql":                                 true,
	"33_cex_provider_fees.sql":                                              true,
	"34_payment_method_chat_template_directions.sql":                        true,
	"34_transactional_liquidity_lightning_source.sql":                       true,
	"35_cex_provider_fee_units.sql":                                         true,
	"36_repair_legacy_varchar_directions.sql":                               true,
}

var (
	compatTrailer  = regexp.MustCompile(`(?im)^--\s*compat:\s*backward-compatible\s*$`)
	rollbackNote   = regexp.MustCompile(`(?im)^--\s*rollback:\s*\S.*$`)
	dropTable      = regexp.MustCompile(`(?i)\bdrop\s+table\b`)
	dropColumn     = regexp.MustCompile(`(?i)\bdrop\s+column\b`)
	renameWarn     = regexp.MustCompile(`(?i)\brename\b`)
	alterTypeWarn  = regexp.MustCompile(`(?i)\balter\s+column\b[^;]*?\btype\b`)
	notNullAddWarn = regexp.MustCompile(`(?i)\badd\s+column\b[^;]*?\bnot\s+null\b`)
)

// Rollback restores code, environment, and images, but never the database, so
// a new migration must keep working with the previous release's queries.
// This lints only the shape of new files: it cannot prove compatibility, but
// it blocks the careless cases and forces an explicit rollback statement.
func TestNewMigrationsDeclareRollbackCompatibility(t *testing.T) {
	entries, err := schemaFiles.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations directory: %v", err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		seen[name] = true
		if grandfatheredMigrations[name] {
			continue
		}
		content, err := schemaFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Errorf("read migrations/%s: %v", name, err)
			continue
		}
		sql := string(content)
		if !compatTrailer.MatchString(sql) {
			t.Errorf("migrations/%s: missing '-- compat: backward-compatible' trailer", name)
		}
		if !rollbackNote.MatchString(sql) {
			t.Errorf("migrations/%s: missing '-- rollback: <why the previous release keeps working>' note", name)
		}
		code := stripLineComments(sql)
		if dropTable.MatchString(code) {
			t.Errorf("migrations/%s: DROP TABLE breaks rollback; remove it in a later expand/contract deploy", name)
		}
		if dropColumn.MatchString(code) {
			t.Errorf("migrations/%s: DROP COLUMN breaks rollback; remove it in a later expand/contract deploy", name)
		}
		if renameWarn.MatchString(code) {
			t.Logf("migrations/%s: RENAME needs a two-deploy expand/contract cycle to stay rollback-safe", name)
		}
		if alterTypeWarn.MatchString(code) {
			t.Logf("migrations/%s: ALTER COLUMN TYPE is only rollback-safe when it widens the type", name)
		}
		if notNullAddWarn.MatchString(code) {
			t.Logf("migrations/%s: ADD COLUMN NOT NULL needs a DEFAULT or backfill the old release's inserts survive", name)
		}
	}
	for name := range grandfatheredMigrations {
		if !seen[name] {
			t.Errorf("grandfathered migration %s no longer exists; remove it from the list", name)
		}
	}
}

// stripLineComments removes -- comments so the DDL patterns only match real
// statements. String literals are left alone: a quoted 'DROP TABLE' still
// trips the denylist, and rewording the literal is the intended fix.
func stripLineComments(sql string) string {
	var kept []string
	for _, line := range strings.Split(sql, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
