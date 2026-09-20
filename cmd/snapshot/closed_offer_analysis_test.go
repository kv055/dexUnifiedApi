package main

import (
	"database/sql"
	"dexunifiedapi/db"
	"dexunifiedapi/internal/testdb"
	"math"
	"testing"
	"time"
)

func openClosedOfferAnalysisDB(t *testing.T) *dbutil.DB {
	t.Helper()
	return testdb.Open(t)
}

func assertAnalysisFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func insertClosedOfferTimed(t *testing.T, db *dbutil.DB, status any, isLive bool, lastSeen time.Time) int64 {
	t.Helper()

	var id int64
	if err := db.QueryRow(`
		INSERT INTO offers (
			exchange, fiat_currency, direction, price,
			historical_analysis_status, avg_estimated_profit_btc,
			hedge_open_fee_btc, hedge_close_fee_btc, hedge_funding_fee_btc, hedge_fee_sum_btc,
			hedge_venue, hedge_instrument, hedge_transfer_minutes,
			best_route_cex, routing_profile, is_live, first_seen, last_seen
		)
		VALUES ('bisq', 'EUR', 'bid', 100, ?, NULL, 0.0001, 0.0002, 0.0003, 0.0006, 'coinbase_futures', 'BTC-PERP', 45, 'coinbase', 'Direct_main2main', ?, ?, ?)
		RETURNING id
	`, status, isLive, lastSeen.UTC().Add(-time.Hour), lastSeen.UTC()).Scan(&id); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	return id
}

func insertClosedOffer(t *testing.T, db *dbutil.DB, status any) int64 {
	t.Helper()

	var id int64
	if err := db.QueryRow(`
		INSERT INTO offers (
			exchange, fiat_currency, direction, price,
			historical_analysis_status, avg_estimated_profit_btc,
			hedge_open_fee_btc, hedge_close_fee_btc, hedge_funding_fee_btc, hedge_fee_sum_btc,
			hedge_venue, hedge_instrument, hedge_transfer_minutes,
			best_route_cex, routing_profile
		)
		VALUES ('bisq', 'EUR', 'bid', 100, ?, NULL, 0.0001, 0.0002, 0.0003, 0.0006, 'coinbase_futures', 'BTC-PERP', 45, 'coinbase', 'Direct_main2main')
		RETURNING id
	`, status).Scan(&id); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	return id
}

func insertLifecycleRow(t *testing.T, db *dbutil.DB, offerID int64, premiumPct, tradeCostPct, netPremiumPct, netPremiumBTC, btcAmount float64) {
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

func offerAnalysisStatus(t *testing.T, db *dbutil.DB, offerID int64) string {
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

func TestAnalyzeClosedOffersSkipsOffersWithinGraceWindow(t *testing.T) {
	t.Setenv("SNAPSHOT_INTERVAL_MIN", "1")
	t.Setenv("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", "3")

	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOfferTimed(t, db, nil, false, time.Now().UTC().Add(-2*time.Minute))
	insertLifecycleRow(t, db, offerID, 5, 1, 4, 0.004, 0.1)

	analyzeClosedOffers(db)

	if got := offerAnalysisStatus(t, db, offerID); got != "" {
		t.Fatalf("status = %q, want empty (grace should defer analysis)", got)
	}
}

func TestAnalyzeClosedOffersProcessesOffersPastGraceWindow(t *testing.T) {
	t.Setenv("SNAPSHOT_INTERVAL_MIN", "1")
	t.Setenv("CLOSED_OFFER_ANALYSIS_GRACE_CYCLES", "3")

	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOfferTimed(t, db, nil, false, time.Now().UTC().Add(-4*time.Minute))
	insertLifecycleRow(t, db, offerID, 5, 1, 4, 0.004, 0.1)

	analyzeClosedOffers(db)

	if got := offerAnalysisStatus(t, db, offerID); got != "analyzed" {
		t.Fatalf("status = %q, want analyzed", got)
	}
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

func TestClosedOfferAnalysisPersistsProfitableMinimumVolumeAverages(t *testing.T) {
	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOffer(t, db, nil)
	insertLifecycleRow(t, db, offerID, 5, 1, 4, 0.004, 0.1)
	insertLifecycleRow(t, db, offerID, 4, 1, 3, 0.003, 0.1)
	if _, err := db.Exec(`
		UPDATE order_lifecycle SET
			min_trade_cost_pct = CASE WHEN premium_pct = 5 THEN 2 ELSE 5 END,
			min_trade_cost_btc = CASE WHEN premium_pct = 5 THEN 0.0004 ELSE 0.001 END,
			min_net_premium_pct = CASE WHEN premium_pct = 5 THEN 3 ELSE -1 END,
			min_net_premium_btc = CASE WHEN premium_pct = 5 THEN 0.0006 ELSE -0.0002 END,
			min_btc_amount = 0.02
		WHERE offer_db_id = ?
	`, offerID); err != nil {
		t.Fatalf("seed minimum lifecycle values: %v", err)
	}

	status, removed := analyzeSingleClosedOffer(db, closedOfferRecord{id: offerID})
	if removed || status != "analyzed" {
		t.Fatalf("analysis = (%q, removed=%v), want analyzed", status, removed)
	}

	var analyzed bool
	var tradeCostPct, tradeCostBTC, netPct, profitBTC, profitFiat, btcAmount float64
	if err := db.QueryRow(`
		SELECT min_volume_analyzed, avg_min_trade_cost_pct, avg_min_trade_cost_btc,
		       avg_min_net_premium_pct, avg_min_estimated_profit_btc,
		       avg_min_estimated_profit_fiat, avg_min_btc_amount
		FROM offers WHERE id = ?
	`, offerID).Scan(&analyzed, &tradeCostPct, &tradeCostBTC, &netPct, &profitBTC, &profitFiat, &btcAmount); err != nil {
		t.Fatalf("select minimum analysis: %v", err)
	}
	if !analyzed {
		t.Fatal("minimum volume analysis was not marked complete")
	}
	assertAnalysisFloat(t, tradeCostPct, 2)
	assertAnalysisFloat(t, tradeCostBTC, 0.0004)
	assertAnalysisFloat(t, netPct, 3)
	assertAnalysisFloat(t, profitBTC, 0.0006)
	assertAnalysisFloat(t, profitFiat, 0.06)
	assertAnalysisFloat(t, btcAmount, 0.02)
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

func offerExists(t *testing.T, db *dbutil.DB, offerID int64) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM offers WHERE id = ?)`, offerID).Scan(&exists); err != nil {
		t.Fatalf("offer exists check: %v", err)
	}
	return exists
}

// Average premium below -3% but the offer was net-profitable at least once: it
// must be kept (analyzed), never deleted — its history is valuable.
func TestClosedOfferEverNetProfitableIsNotDeleted(t *testing.T) {
	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOffer(t, db, nil)
	insertLifecycleRow(t, db, offerID, 5, 2, 3, 0.003, 0.1)     // profitable snapshot
	insertLifecycleRow(t, db, offerID, -12, 0, -12, -0.01, 0.1) // drags avg premium to -3.5%

	status, removed := analyzeSingleClosedOffer(db, closedOfferRecord{id: offerID})

	if removed {
		t.Fatal("ever-net-profitable offer was deleted, want kept")
	}
	if status != "analyzed" {
		t.Fatalf("status = %q, want analyzed", status)
	}
	if !offerExists(t, db, offerID) {
		t.Fatal("offer row was removed, want kept")
	}
}

// Average premium below -3% and never net-profitable, but favorably priced at
// least once (raw premium > 0): still kept (non_premium), not deleted. Guards
// the server-outage case where baseline rows have NULL net_premium_pct.
func TestClosedOfferEverRawPositiveIsNotDeleted(t *testing.T) {
	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOffer(t, db, nil)
	insertLifecycleRow(t, db, offerID, 1, 2, -0.5, -0.0005, 0.1) // raw positive, net negative
	insertLifecycleRow(t, db, offerID, -10, 0, -10, -0.01, 0.1)  // avg premium -4.5%

	status, removed := analyzeSingleClosedOffer(db, closedOfferRecord{id: offerID})

	if removed {
		t.Fatal("ever-raw-positive offer was deleted, want kept")
	}
	if status != "non_premium" {
		t.Fatalf("status = %q, want non_premium", status)
	}
	if !offerExists(t, db, offerID) {
		t.Fatal("offer row was removed, want kept")
	}
}

// Analyzer-persisted lifecycle aggregates feed the monitor's past-offers
// profitability stats without re-aggregating order_lifecycle per request.
func TestClosedOfferAnalysisPersistsLifecycleObservationAggregates(t *testing.T) {
	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOffer(t, db, nil)
	if _, err := db.Exec(`
		INSERT INTO order_lifecycle (
			offer_db_id, price, ref_price, premium_pct,
			trade_cost_pct, trade_cost_btc, net_premium_pct, net_premium_btc, btc_amount,
			hedge_notional_fiat, leverage_used, recommended_collateral_fiat
		)
		VALUES
			(?, 100, 100, 5, 1, 0.001, 4, 0.004, 0.1, 1000, 2, 500),
			(?, 110, 100, 5, 1, 0.001, 3, 0.003, 0.1, 2000, 4, 1500)
	`, offerID, offerID); err != nil {
		t.Fatalf("insert lifecycle: %v", err)
	}

	status, removed := analyzeSingleClosedOffer(db, closedOfferRecord{id: offerID})
	if removed || status != "analyzed" {
		t.Fatalf("analysis = (%q, removed=%v), want analyzed", status, removed)
	}

	var count int
	var avgPrice, avgNotional, avgLeverage, avgCollateral float64
	if err := db.QueryRow(`
		SELECT lifecycle_observation_count, avg_lifecycle_price,
		       avg_hedge_notional_fiat, avg_leverage_used,
		       avg_recommended_collateral_fiat
		FROM offers WHERE id = ?
	`, offerID).Scan(&count, &avgPrice, &avgNotional, &avgLeverage, &avgCollateral); err != nil {
		t.Fatalf("select observation aggregates: %v", err)
	}
	if count != 2 {
		t.Fatalf("lifecycle_observation_count = %d, want 2", count)
	}
	assertAnalysisFloat(t, avgPrice, 105)
	assertAnalysisFloat(t, avgNotional, 1500)
	assertAnalysisFloat(t, avgLeverage, 3)
	assertAnalysisFloat(t, avgCollateral, 1000)

	// Rows without hedge samples persist NULL hedge aggregates, not zeros.
	bareID := insertClosedOffer(t, db, nil)
	insertLifecycleRow(t, db, bareID, 5, 1, 4, 0.004, 0.1)
	if status, removed := analyzeSingleClosedOffer(db, closedOfferRecord{id: bareID}); removed || status != "analyzed" {
		t.Fatalf("analysis = (%q, removed=%v), want analyzed", status, removed)
	}
	var bareNotional, bareCollateral sql.NullFloat64
	var bareCount int
	if err := db.QueryRow(`
		SELECT lifecycle_observation_count, avg_hedge_notional_fiat,
		       avg_recommended_collateral_fiat
		FROM offers WHERE id = ?
	`, bareID).Scan(&bareCount, &bareNotional, &bareCollateral); err != nil {
		t.Fatalf("select bare aggregates: %v", err)
	}
	if bareCount != 1 {
		t.Fatalf("lifecycle_observation_count = %d, want 1", bareCount)
	}
	if bareNotional.Valid || bareCollateral.Valid {
		t.Fatalf("hedge aggregates = (%v, %v), want NULL", bareNotional, bareCollateral)
	}
}

// Never premium (always unfavorable) and deeply unprofitable on average: this is
// genuine junk and is deleted.
func TestClosedOfferNeverPremiumIsDeleted(t *testing.T) {
	db := openClosedOfferAnalysisDB(t)
	offerID := insertClosedOffer(t, db, nil)
	insertLifecycleRow(t, db, offerID, -4, 0, -4, -0.004, 0.1)
	insertLifecycleRow(t, db, offerID, -5, 0, -5, -0.005, 0.1)

	status, removed := analyzeSingleClosedOffer(db, closedOfferRecord{id: offerID})

	if !removed {
		t.Fatal("never-premium junk offer was kept, want deleted")
	}
	if status != "deleted_non_premium" {
		t.Fatalf("status = %q, want deleted_non_premium", status)
	}
	if offerExists(t, db, offerID) {
		t.Fatal("offer row still present, want deleted")
	}
}
