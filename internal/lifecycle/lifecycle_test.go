package lifecycle

import (
	"context"
	"database/sql"
	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/testdb"
	"math"
	"strings"
	"testing"

	"commons/tradecost"
)

func openTestDB(t *testing.T) *dbutil.DB {
	t.Helper()
	return testdb.Open(t)
}

func insertOffer(t *testing.T, db *dbutil.DB) int64 {
	t.Helper()

	var id int64
	if err := db.QueryRow(`
		INSERT INTO offers (exchange, fiat_currency, direction, price)
		VALUES ('bisq', 'EUR', 'buy', 100)
		RETURNING id
	`).Scan(&id); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	return id
}

func insertBaselineRow(t *testing.T, db *dbutil.DB, offerID int64, price, refPrice, premiumPct float64) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO order_lifecycle (offer_db_id, price, ref_price, premium_pct)
		VALUES (?, ?, ?, ?)
	`, offerID, price, refPrice, premiumPct); err != nil {
		t.Fatalf("insert baseline: %v", err)
	}
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func TestPersistSnapshotEnrichesBaselineAndStoresStaticCostsOnOffer(t *testing.T) {
	db := openTestDB(t)
	offerID := insertOffer(t, db)
	// Baseline written by the snapshot job: price/ref/premium only, costs NULL.
	insertBaselineRow(t, db, offerID, 105, 100, 5)
	repo := NewRepository(db)

	err := repo.PersistSnapshot(context.Background(), []Record{{
		ID:            offerID,
		Price:         105,
		RefPrice:      100,
		PriceDiffPct:  5,
		NetPremiumPct: 4,
		CostBreakdown: tradecost.CostBreakdown{
			BTCAmount:          0.1,
			RouteCEX:           "kraken",
			RouteMethod:        "onchain",
			TradeDirection:     "bid",
			CexTakerFeeBTC:     0.00025,
			WithdrawalFeeBTC:   0.00015,
			NetworkFeeBTC:      0.0001,
			DexFeeBTC:          0.0005,
			TotalCostPct:       1,
			TotalCostBTC:       0.001,
			EstimatedProfitBTC: 0.004,
			Hedge: tradecost.HedgeBreakdown{
				Position: &tradecost.HedgePositionDetail{
					HedgeNotionalEUR:         10.5,
					LeverageUsed:             5,
					RecommendedCollateralEUR: 3.25,
				},
			},
		},
	}})
	if err != nil {
		t.Fatalf("PersistSnapshot: %v", err)
	}

	// Enriched in place — still exactly one row, not duplicated.
	var lifecycleRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM order_lifecycle`).Scan(&lifecycleRows); err != nil {
		t.Fatalf("count lifecycle: %v", err)
	}
	if lifecycleRows != 1 {
		t.Fatalf("lifecycle rows = %d, want 1", lifecycleRows)
	}

	// Snapshot's price/ref/premium untouched; cost cols filled; net derived from
	// the stored premium (5 - 1 = 4); net BTC = 0.1 * 4 / 100 = 0.004.
	var price, refPrice, premium, tradeCostPct, tradeCostBTC, netPct, netBTC, btcAmount float64
	if err := db.QueryRow(`
		SELECT price, ref_price, premium_pct, trade_cost_pct, trade_cost_btc,
		       net_premium_pct, net_premium_btc, btc_amount
		FROM order_lifecycle WHERE offer_db_id = ?
	`, offerID).Scan(&price, &refPrice, &premium, &tradeCostPct, &tradeCostBTC, &netPct, &netBTC, &btcAmount); err != nil {
		t.Fatalf("select lifecycle: %v", err)
	}
	assertFloat(t, price, 105)
	assertFloat(t, refPrice, 100)
	assertFloat(t, premium, 5)
	assertFloat(t, tradeCostPct, 1)
	assertFloat(t, tradeCostBTC, 0.001)
	assertFloat(t, netPct, 4)
	assertFloat(t, netBTC, 0.004)
	assertFloat(t, btcAmount, 0.1)

	var hedgeNotionalEUR, leverageUsed, recommendedCollateralEUR float64
	if err := db.QueryRow(`
		SELECT hedge_notional_eur, leverage_used, recommended_collateral_eur
		FROM order_lifecycle WHERE offer_db_id = ?
	`, offerID).Scan(&hedgeNotionalEUR, &leverageUsed, &recommendedCollateralEUR); err != nil {
		t.Fatalf("select hedge position: %v", err)
	}
	assertFloat(t, hedgeNotionalEUR, 10.5)
	assertFloat(t, leverageUsed, 5)
	assertFloat(t, recommendedCollateralEUR, 3.25)

	var taker, withdrawal, network, dexFee, abortBTC, abortPct float64
	if err := db.QueryRow(`
		SELECT cex_taker_fee_btc, cex_withdraw_fee_btc, network_fee_btc,
		       dex_fee_btc, abort_cost_btc, abort_cost_pct
		FROM offers WHERE id = ?
	`, offerID).Scan(&taker, &withdrawal, &network, &dexFee, &abortBTC, &abortPct); err != nil {
		t.Fatalf("select offer costs: %v", err)
	}
	assertFloat(t, taker, 0.00025)
	assertFloat(t, withdrawal, 0.00015)
	assertFloat(t, network, 0.0001)
	assertFloat(t, dexFee, 0.0005)
	assertFloat(t, abortBTC, 0.00075)
	assertFloat(t, abortPct, 0.75)
}

func TestPersistSnapshotLeavesAskWithdrawalFeeNull(t *testing.T) {
	db := openTestDB(t)
	offerID := insertOffer(t, db)
	repo := NewRepository(db)

	err := repo.PersistSnapshot(context.Background(), []Record{{
		ID:            offerID,
		Price:         95,
		RefPrice:      100,
		PriceDiffPct:  5,
		NetPremiumPct: 4,
		CostBreakdown: tradecost.CostBreakdown{
			BTCAmount:          0.1,
			RouteCEX:           "kraken",
			RouteMethod:        "onchain",
			TradeDirection:     "ask",
			CexTakerFeeBTC:     0.00025,
			WithdrawalFeeBTC:   0.00015,
			NetworkFeeBTC:      0.0001,
			DexFeeBTC:          0.0005,
			TotalCostPct:       1,
			TotalCostBTC:       0.001,
			EstimatedProfitBTC: 0.004,
		},
	}})
	if err != nil {
		t.Fatalf("PersistSnapshot: %v", err)
	}

	var withdrawal sql.NullFloat64
	if err := db.QueryRow(`SELECT cex_withdraw_fee_btc FROM offers WHERE id = ?`, offerID).Scan(&withdrawal); err != nil {
		t.Fatalf("select withdrawal: %v", err)
	}
	if withdrawal.Valid {
		t.Fatalf("ask withdrawal fee = %v, want NULL", withdrawal.Float64)
	}

	var abortBTC, abortPct float64
	if err := db.QueryRow(`SELECT abort_cost_btc, abort_cost_pct FROM offers WHERE id = ?`, offerID).Scan(&abortBTC, &abortPct); err != nil {
		t.Fatalf("select abort cost: %v", err)
	}
	assertFloat(t, abortBTC, 0.0002)
	assertFloat(t, abortPct, 0.2)
}

func TestPersistSnapshotSkipsEnrichmentWithoutBaseline(t *testing.T) {
	db := openTestDB(t)
	offerID := insertOffer(t, db)
	repo := NewRepository(db)

	// No baseline row exists yet for this offer.
	err := repo.PersistSnapshot(context.Background(), []Record{{
		ID:            offerID,
		Price:         105,
		RefPrice:      100,
		PriceDiffPct:  5,
		NetPremiumPct: 4,
		CostBreakdown: tradecost.CostBreakdown{
			BTCAmount: 0.1, RouteCEX: "kraken", RouteMethod: "onchain", TradeDirection: "bid",
			CexTakerFeeBTC: 0.00025, NetworkFeeBTC: 0.0001, DexFeeBTC: 0.0005,
			TotalCostPct: 1, TotalCostBTC: 0.001, EstimatedProfitBTC: 0.004,
		},
	}})
	if err != nil {
		t.Fatalf("PersistSnapshot: %v", err)
	}

	// Enrichment is a no-op without a baseline — no lifecycle rows are created.
	var lifecycleRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM order_lifecycle`).Scan(&lifecycleRows); err != nil {
		t.Fatalf("count lifecycle: %v", err)
	}
	if lifecycleRows != 0 {
		t.Fatalf("lifecycle rows = %d, want 0", lifecycleRows)
	}

	// The offers-table route costs are still updated independently.
	var route sql.NullString
	if err := db.QueryRow(`SELECT best_route_cex FROM offers WHERE id = ?`, offerID).Scan(&route); err != nil {
		t.Fatalf("select route: %v", err)
	}
	if !route.Valid || route.String != "kraken" {
		t.Fatalf("best_route_cex = %v, want kraken", route)
	}
}

func TestInsertBaselineWritesRowsAndSkipsInvalid(t *testing.T) {
	db := openTestDB(t)
	goodID := insertOffer(t, db)
	badID := insertOffer(t, db)
	repo := NewRepository(db)

	err := repo.InsertBaseline(context.Background(), []Baseline{
		{OfferID: goodID, Price: 105, RefPrice: 100, PremiumPct: 5},
		{OfferID: badID, Price: 0, RefPrice: 100, PremiumPct: 5}, // invalid price — skipped
	})
	if err != nil {
		t.Fatalf("InsertBaseline: %v", err)
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM order_lifecycle`).Scan(&rows); err != nil {
		t.Fatalf("count lifecycle: %v", err)
	}
	if rows != 1 {
		t.Fatalf("lifecycle rows = %d, want 1 (invalid skipped)", rows)
	}

	// The baseline row carries price/ref/premium and leaves cost columns NULL.
	var price, refPrice, premium float64
	var tradeCostPct, netPct, btcAmount sql.NullFloat64
	if err := db.QueryRow(`
		SELECT price, ref_price, premium_pct, trade_cost_pct, net_premium_pct, btc_amount
		FROM order_lifecycle WHERE offer_db_id = ?
	`, goodID).Scan(&price, &refPrice, &premium, &tradeCostPct, &netPct, &btcAmount); err != nil {
		t.Fatalf("select baseline: %v", err)
	}
	assertFloat(t, price, 105)
	assertFloat(t, refPrice, 100)
	assertFloat(t, premium, 5)
	if tradeCostPct.Valid || netPct.Valid || btcAmount.Valid {
		t.Fatalf("cost columns should be NULL on a baseline row, got trade_cost=%v net=%v btc=%v", tradeCostPct, netPct, btcAmount)
	}
}

func TestLoadByOfferIDsCapsPointsPerOffer(t *testing.T) {
	db := openTestDB(t)
	offerID := insertOffer(t, db)

	// More history than the cap, each row a distinct minute and price so we can assert
	// which window is returned. An unbounded load of all of these is what OOM'd prod.
	const total = maxLifecyclePointsPerOffer + 5
	for i := 1; i <= total; i++ {
		if _, err := db.Exec(`
			INSERT INTO order_lifecycle (offer_db_id, price, ref_price, premium_pct, snapshot_time)
			VALUES (?, ?, 100, 0, now() - (? * interval '1 minute'))
		`, offerID, float64(i), total-i); err != nil {
			t.Fatalf("insert lifecycle row %d: %v", i, err)
		}
	}

	got, err := NewRepository(db).LoadByOfferIDs(context.Background(), []int64{offerID})
	if err != nil {
		t.Fatalf("LoadByOfferIDs: %v", err)
	}
	pts := got[offerID]
	if len(pts) != maxLifecyclePointsPerOffer {
		t.Fatalf("points = %d, want %d (capped per offer)", len(pts), maxLifecyclePointsPerOffer)
	}
	// Returned oldest-first, keeping only the most recent window: prices are 1..total,
	// the 5 oldest are dropped, so the first kept is 6 and the last is total.
	assertFloat(t, pts[0].Price, 6)
	assertFloat(t, pts[len(pts)-1].Price, float64(total))
}

func TestPersistSnapshotReturnsUpdateErrorAndRollsBack(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		ALTER TABLE offers
		ADD CONSTRAINT reject_kraken_route
		CHECK (best_route_cex IS NULL OR best_route_cex <> 'kraken')
	`); err != nil {
		t.Fatalf("add test constraint: %v", err)
	}
	offerID := insertOffer(t, db)
	insertBaselineRow(t, db, offerID, 105, 100, 5)
	repo := NewRepository(db)

	err := repo.PersistSnapshot(context.Background(), []Record{{
		ID:            offerID,
		Price:         105,
		RefPrice:      100,
		PriceDiffPct:  5,
		NetPremiumPct: 4,
		CostBreakdown: tradecost.CostBreakdown{
			BTCAmount:          0.1,
			RouteCEX:           "kraken",
			RouteMethod:        "onchain",
			TradeDirection:     "bid",
			CexTakerFeeBTC:     0.00025,
			WithdrawalFeeBTC:   0.00015,
			NetworkFeeBTC:      0.0001,
			DexFeeBTC:          0.0005,
			TotalCostPct:       1,
			TotalCostBTC:       0.001,
			EstimatedProfitBTC: 0.004,
		},
	}})
	if err == nil {
		t.Fatal("PersistSnapshot returned nil, want update error")
	}
	if !strings.Contains(err.Error(), "update lifecycle route costs") {
		t.Fatalf("error = %v, want update lifecycle route costs context", err)
	}

	// The committed baseline row survives; the enrichment UPDATE was rolled back
	// with the failed offers-route UPDATE, so its cost columns stay NULL.
	var lifecycleRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM order_lifecycle`).Scan(&lifecycleRows); err != nil {
		t.Fatalf("count lifecycle: %v", err)
	}
	if lifecycleRows != 1 {
		t.Fatalf("lifecycle rows = %d, want 1 (baseline survives)", lifecycleRows)
	}
	var tradeCostPct sql.NullFloat64
	if err := db.QueryRow(`SELECT trade_cost_pct FROM order_lifecycle WHERE offer_db_id = ?`, offerID).Scan(&tradeCostPct); err != nil {
		t.Fatalf("select trade_cost_pct: %v", err)
	}
	if tradeCostPct.Valid {
		t.Fatalf("trade_cost_pct = %v, want NULL (enrichment rolled back)", tradeCostPct.Float64)
	}
	var route sql.NullString
	if err := db.QueryRow(`SELECT best_route_cex FROM offers WHERE id = ?`, offerID).Scan(&route); err != nil {
		t.Fatalf("select route: %v", err)
	}
	if route.Valid {
		t.Fatalf("best_route_cex = %q, want NULL (rolled back)", route.String)
	}
}
