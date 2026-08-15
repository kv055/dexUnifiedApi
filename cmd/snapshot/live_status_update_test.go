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
	err := reconcileLiveOfferStatus(db, snapshotStart, nil, defaultLiveOfferStaleGrace)
	if err != nil {
		t.Fatalf("reconcile live offer status: %v", err)
	}
}

func insertLiveOffer(t *testing.T, db *dbutil.DB, exchange, fiat, direction string, lastSeen time.Time) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`
		INSERT INTO offers (exchange, fiat_currency, direction, price, is_live, first_seen, last_seen)
		VALUES (?, ?, ?, 100, TRUE, ?, ?)
		RETURNING id`, exchange, fiat, direction, lastSeen.Add(-time.Hour), lastSeen).Scan(&id); err != nil {
		t.Fatalf("insert live offer: %v", err)
	}
	return id
}

func offerIsLive(t *testing.T, db *dbutil.DB, id int64) bool {
	t.Helper()
	var isLive bool
	if err := db.QueryRow(`SELECT is_live FROM offers WHERE id = ?`, id).Scan(&isLive); err != nil {
		t.Fatalf("select offer live state: %v", err)
	}
	return isLive
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
			best_route_cex, routing_profile
		)
		VALUES (
			'bisq', 'EUR', 'bid', 100, FALSE, NOW() - INTERVAL '20 minutes', NOW() - INTERVAL '10 minutes',
			'analyzed', 100, 5, 1, 0.001, 0.0006, 4, 0.004, 0.4, 0.1,
			'kraken', 'Direct_main2main'
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

func TestReconcileLiveOfferStatusRetainsFailedMarketWithinGrace(t *testing.T) {
	db := openLiveStatusDB(t)
	snapshotStart := time.Now().UTC()
	id := insertLiveOffer(t, db, "bisq", "EUR", "bid", snapshotStart.Add(-time.Minute))

	if err := reconcileLiveOfferStatus(db, snapshotStart, nil, 5*time.Minute); err != nil {
		t.Fatalf("reconcile failed market: %v", err)
	}
	if !offerIsLive(t, db, id) {
		t.Fatal("offer disappeared after failed market fetch within grace")
	}
}

func TestReconcileLiveOfferStatusClosesOnlyConfirmedMarketAbsences(t *testing.T) {
	db := openLiveStatusDB(t)
	snapshotStart := time.Now().UTC()
	missing := insertLiveOffer(t, db, "bisq", "EUR", "bid", snapshotStart.Add(-time.Minute))
	otherMarket := insertLiveOffer(t, db, "bisq", "EUR", "ask", snapshotStart.Add(-time.Minute))
	confirmed := map[liveMarket]struct{}{{exchange: "bisq", fiat: "EUR", direction: "bid"}: {}}

	if err := reconcileLiveOfferStatus(db, snapshotStart, confirmed, 5*time.Minute); err != nil {
		t.Fatalf("reconcile confirmed market: %v", err)
	}
	if offerIsLive(t, db, missing) {
		t.Fatal("offer missing from a confirmed market remained live")
	}
	if !offerIsLive(t, db, otherMarket) {
		t.Fatal("offer from an unconfirmed market was closed")
	}
}

func TestReconcileLiveOfferStatusExpiresFailedMarketAfterGrace(t *testing.T) {
	db := openLiveStatusDB(t)
	snapshotStart := time.Now().UTC()
	id := insertLiveOffer(t, db, "bisq2", "USD", "ask", snapshotStart.Add(-6*time.Minute))

	if err := reconcileLiveOfferStatus(db, snapshotStart, nil, 5*time.Minute); err != nil {
		t.Fatalf("reconcile stale market: %v", err)
	}
	if offerIsLive(t, db, id) {
		t.Fatal("offer from failed market remained live after grace")
	}
}
