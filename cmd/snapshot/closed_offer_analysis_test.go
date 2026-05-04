package main

import (
	"database/sql"
	"math"
	"testing"

	_ "modernc.org/sqlite"
)

const closedOfferAnalysisTestSchema = `
CREATE TABLE offers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	exchange TEXT NOT NULL DEFAULT 'bisq',
	direction TEXT NOT NULL DEFAULT 'buy',
	price REAL NOT NULL DEFAULT 100,
	btc_min REAL,
	btc_max REAL,
	first_seen TEXT DEFAULT CURRENT_TIMESTAMP,
	last_seen TEXT DEFAULT CURRENT_TIMESTAMP,
	is_live BOOLEAN NOT NULL DEFAULT 0,
	historical_analysis_status TEXT,
	avg_ref_price REAL,
	avg_premium_pct REAL,
	avg_trade_cost_pct REAL,
	avg_trade_cost_btc REAL,
	avg_hedge_fee_sum_btc REAL,
	cex_taker_fee_btc REAL,
	cex_withdraw_fee_btc REAL,
	network_fee_btc REAL,
	hedge_open_fee_btc REAL,
	hedge_close_fee_btc REAL,
	hedge_funding_fee_btc REAL,
	hedge_fee_sum_btc REAL,
	hedge_venue TEXT,
	hedge_instrument TEXT,
	hedge_transfer_minutes INTEGER,
	dex_fee_btc REAL,
	avg_net_premium_pct REAL,
	avg_estimated_profit_btc REAL,
	avg_estimated_profit_fiat REAL,
	avg_btc_amount REAL,
	best_route_cex TEXT,
	best_route_method TEXT
);

CREATE TABLE order_lifecycle (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	offer_db_id INTEGER NOT NULL,
	price REAL NOT NULL,
	ref_price REAL NOT NULL,
	premium_pct REAL NOT NULL,
	trade_cost_pct REAL,
	trade_cost_btc REAL,
	net_premium_pct REAL,
	net_premium_btc REAL,
	btc_amount REAL
);
`

func openClosedOfferAnalysisDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", t.TempDir()+"/closed-offer-analysis.db")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})
	if _, err := db.Exec(closedOfferAnalysisTestSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func assertAnalysisFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func insertClosedOffer(t *testing.T, db *sql.DB, status any) int64 {
	t.Helper()

	res, err := db.Exec(`
		INSERT INTO offers (
			historical_analysis_status, avg_estimated_profit_btc,
			hedge_open_fee_btc, hedge_close_fee_btc, hedge_funding_fee_btc, hedge_fee_sum_btc,
			hedge_venue, hedge_instrument, hedge_transfer_minutes,
			best_route_cex, best_route_method
		)
		VALUES (?, NULL, 0.0001, 0.0002, 0.0003, 0.0006, 'coinbase_futures', 'BTC-PERP', 45, 'coinbase', 'onchain')
	`, status)
	if err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func insertLifecycleRow(t *testing.T, db *sql.DB, offerID int64, premiumPct, tradeCostPct, netPremiumPct, netPremiumBTC, btcAmount float64) {
	t.Helper()

	if _, err := db.Exec(`
		INSERT INTO order_lifecycle (
			offer_db_id, price, ref_price, premium_pct,
			trade_cost_pct, trade_cost_btc, net_premium_pct, net_premium_btc, btc_amount
		)
		VALUES (?, 105, 100, ?, ?, 0.0002, ?, ?, ?)
	`, offerID, premiumPct, tradeCostPct, netPremiumPct, netPremiumBTC, btcAmount); err != nil {
		t.Fatalf("insert lifecycle: %v", err)
	}
}

func offerAnalysisStatus(t *testing.T, db *sql.DB, offerID int64) string {
	t.Helper()

	var status sql.NullString
	if err := db.QueryRow(`SELECT historical_analysis_status FROM offers WHERE id = ?`, offerID).Scan(&status); err != nil {
		t.Fatalf("select status: %v", err)
	}
	if !status.Valid {
		return ""
	}
	return status.String
}

func TestAnalyzeSingleClosedOfferMarksMissingLifecycleAsRetryable(t *testing.T) {
	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOffer(t, db, nil)

	status, removed := analyzeSingleClosedOffer(db, closedOfferRecord{id: offerID})

	if removed {
		t.Fatal("expected offer to remain")
	}
	if status != "ignored_no_lifecycle" {
		t.Fatalf("status result = %q, want ignored_no_lifecycle", status)
	}
	if got := offerAnalysisStatus(t, db, offerID); got != "ignored_no_lifecycle" {
		t.Fatalf("stored status = %q, want ignored_no_lifecycle", got)
	}
}

func TestIgnoredClosedOfferWithProfitableLifecycleIsAnalyzed(t *testing.T) {
	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOffer(t, db, "ignored_no_lifecycle")
	insertLifecycleRow(t, db, offerID, 5, 1, 4, 0.004, 0.1)
	insertLifecycleRow(t, db, offerID, 7, 2, 5, 0.006, 0.2)

	resetIgnoredClosedOffersWithLifecycle(db)
	status, removed := analyzeSingleClosedOffer(db, closedOfferRecord{id: offerID})

	if removed {
		t.Fatal("expected offer to remain")
	}
	if status != "analyzed" {
		t.Fatalf("status result = %q, want analyzed", status)
	}
	var storedStatus string
	var avgNetPct, avgProfitBTC, avgProfitFiat, avgHedgeFee float64
	if err := db.QueryRow(`
		SELECT historical_analysis_status, avg_net_premium_pct, avg_estimated_profit_btc,
		       avg_estimated_profit_fiat, avg_hedge_fee_sum_btc
		FROM offers WHERE id = ?
	`, offerID).Scan(&storedStatus, &avgNetPct, &avgProfitBTC, &avgProfitFiat, &avgHedgeFee); err != nil {
		t.Fatalf("select analyzed fields: %v", err)
	}
	if storedStatus != "analyzed" {
		t.Fatalf("stored status = %q, want analyzed", storedStatus)
	}
	assertAnalysisFloat(t, avgNetPct, 4.5)
	assertAnalysisFloat(t, avgProfitBTC, 0.005)
	assertAnalysisFloat(t, avgProfitFiat, 0.5)
	assertAnalysisFloat(t, avgHedgeFee, 0.0006)
}

func TestClosedOfferWithNonProfitableLifecycleBecomesNonPremium(t *testing.T) {
	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOffer(t, db, "ignored_no_lifecycle")
	insertLifecycleRow(t, db, offerID, 1, 2, -1, -0.001, 0.1)
	insertLifecycleRow(t, db, offerID, 0.5, 2, -1.5, -0.003, 0.2)

	status, removed := analyzeSingleClosedOffer(db, closedOfferRecord{id: offerID})

	if removed {
		t.Fatal("expected offer to remain")
	}
	if status != "non_premium" {
		t.Fatalf("status result = %q, want non_premium", status)
	}
	var storedStatus string
	var avgRefPrice, avgPremiumPct, avgBTCAmount float64
	var avgProfitBTC sql.NullFloat64
	if err := db.QueryRow(`
		SELECT historical_analysis_status, avg_ref_price, avg_premium_pct,
		       avg_btc_amount, avg_estimated_profit_btc
		FROM offers WHERE id = ?
	`, offerID).Scan(&storedStatus, &avgRefPrice, &avgPremiumPct, &avgBTCAmount, &avgProfitBTC); err != nil {
		t.Fatalf("select non-premium fields: %v", err)
	}
	if storedStatus != "non_premium" {
		t.Fatalf("stored status = %q, want non_premium", storedStatus)
	}
	assertAnalysisFloat(t, avgRefPrice, 100)
	assertAnalysisFloat(t, avgPremiumPct, 0.75)
	assertAnalysisFloat(t, avgBTCAmount, 0.15)
	if avgProfitBTC.Valid {
		t.Fatalf("avg_estimated_profit_btc = %v, want NULL", avgProfitBTC.Float64)
	}
}
