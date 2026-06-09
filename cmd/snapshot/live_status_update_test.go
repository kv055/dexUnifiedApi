package main

import (
	"database/sql"
	"testing"
	"time"

	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/testdb"
)

func openLiveStatusDB(t *testing.T) *dbutil.DB {
	t.Helper()
	return testdb.Open(t)
}

func applyLiveStatusBulkUpdate(t *testing.T, db *dbutil.DB, snapshotStart time.Time) {
	t.Helper()

	_, err := db.Exec(`
		UPDATE offers SET
			is_live = (last_seen >= ?),
			duration = CASE WHEN last_seen < ? THEN EXTRACT(EPOCH FROM (last_seen - first_seen))::BIGINT ELSE NULL END,
			historical_analysis_status = CASE WHEN last_seen >= ? THEN NULL ELSE historical_analysis_status END,
			avg_ref_price              = CASE WHEN last_seen >= ? THEN NULL ELSE avg_ref_price END,
			avg_premium_pct            = CASE WHEN last_seen >= ? THEN NULL ELSE avg_premium_pct END,
			avg_trade_cost_pct         = CASE WHEN last_seen >= ? THEN NULL ELSE avg_trade_cost_pct END,
			avg_trade_cost_btc         = CASE WHEN last_seen >= ? THEN NULL ELSE avg_trade_cost_btc END,
			avg_hedge_fee_sum_btc      = CASE WHEN last_seen >= ? THEN NULL ELSE avg_hedge_fee_sum_btc END,
			avg_net_premium_pct        = CASE WHEN last_seen >= ? THEN NULL ELSE avg_net_premium_pct END,
			avg_estimated_profit_btc   = CASE WHEN last_seen >= ? THEN NULL ELSE avg_estimated_profit_btc END,
			avg_estimated_profit_fiat  = CASE WHEN last_seen >= ? THEN NULL ELSE avg_estimated_profit_fiat END,
			avg_btc_amount             = CASE WHEN last_seen >= ? THEN NULL ELSE avg_btc_amount END`,
		snapshotStart, snapshotStart,
		snapshotStart, snapshotStart, snapshotStart, snapshotStart, snapshotStart,
		snapshotStart, snapshotStart, snapshotStart, snapshotStart, snapshotStart,
	)
	if err != nil {
		t.Fatalf("bulk live status update: %v", err)
	}
}

func TestLiveStatusBulkUpdateClearsClosureFieldsOnRelive(t *testing.T) {
	db := openLiveStatusDB(t)

	var id int64
	if err := db.QueryRow(`
		INSERT INTO offers (
			exchange, fiat_currency, direction, price, is_live, first_seen, last_seen,
			historical_analysis_status, avg_ref_price, avg_premium_pct,
			avg_trade_cost_pct, avg_trade_cost_btc, avg_hedge_fee_sum_btc,
			avg_net_premium_pct, avg_estimated_profit_btc, avg_estimated_profit_fiat, avg_btc_amount,
			best_route_cex, best_route_method
		)
		VALUES (
			'bisq', 'EUR', 'buy', 100, FALSE, NOW() - INTERVAL '20 minutes', NOW() - INTERVAL '10 minutes',
			'analyzed', 100, 5, 1, 0.001, 0.0006, 4, 0.004, 0.4, 0.1,
			'kraken', 'onchain'
		)
		RETURNING id
	`).Scan(&id); err != nil {
		t.Fatalf("insert analyzed closed offer: %v", err)
	}

	snapshotStart := time.Now().UTC().Add(-1 * time.Minute)
	if _, err := db.Exec(`UPDATE offers SET last_seen = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
		t.Fatalf("bump last_seen: %v", err)
	}
	applyLiveStatusBulkUpdate(t, db, snapshotStart)

	var isLive bool
	var status sql.NullString
	var avgProfit sql.NullFloat64
	var bestRouteCEX sql.NullString
	if err := db.QueryRow(`
		SELECT is_live, historical_analysis_status, avg_estimated_profit_btc, best_route_cex
		FROM offers WHERE id = ?
	`, id).Scan(&isLive, &status, &avgProfit, &bestRouteCEX); err != nil {
		t.Fatalf("select offer: %v", err)
	}
	if !isLive {
		t.Fatal("is_live = false, want true after relive")
	}
	if status.Valid {
		t.Fatalf("historical_analysis_status = %q, want NULL", status.String)
	}
	if avgProfit.Valid {
		t.Fatalf("avg_estimated_profit_btc = %v, want NULL", avgProfit.Float64)
	}
	if !bestRouteCEX.Valid || bestRouteCEX.String != "kraken" {
		t.Fatalf("best_route_cex = %v, want kraken preserved", bestRouteCEX)
	}
}
