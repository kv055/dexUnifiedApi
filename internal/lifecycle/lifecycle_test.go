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

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func TestPersistSnapshotStoresStaticCostsOnOfferOnly(t *testing.T) {
	db := openTestDB(t)
	offerID := insertOffer(t, db)
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
	if err != nil {
		t.Fatalf("PersistSnapshot: %v", err)
	}

	var lifecycleRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM order_lifecycle`).Scan(&lifecycleRows); err != nil {
		t.Fatalf("count lifecycle: %v", err)
	}
	if lifecycleRows != 1 {
		t.Fatalf("lifecycle rows = %d, want 1", lifecycleRows)
	}

	var taker, withdrawal, network, dexFee float64
	if err := db.QueryRow(`
		SELECT cex_taker_fee_btc, cex_withdraw_fee_btc, network_fee_btc, dex_fee_btc
		FROM offers WHERE id = ?
	`, offerID).Scan(&taker, &withdrawal, &network, &dexFee); err != nil {
		t.Fatalf("select offer costs: %v", err)
	}
	assertFloat(t, taker, 0.00025)
	assertFloat(t, withdrawal, 0.00015)
	assertFloat(t, network, 0.0001)
	assertFloat(t, dexFee, 0.0005)
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
}

func TestPersistSnapshotReturnsInsertErrorAndRollsBack(t *testing.T) {
	db := openTestDB(t)
	offerID := insertOffer(t, db)
	repo := NewRepository(db)

	err := repo.PersistSnapshot(context.Background(), []Record{{
		ID:            offerID,
		Price:         0,
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
		t.Fatal("PersistSnapshot returned nil, want insert error")
	}
	if !strings.Contains(err.Error(), "insert lifecycle") {
		t.Fatalf("error = %v, want insert lifecycle context", err)
	}

	var lifecycleRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM order_lifecycle`).Scan(&lifecycleRows); err != nil {
		t.Fatalf("count lifecycle: %v", err)
	}
	if lifecycleRows != 0 {
		t.Fatalf("lifecycle rows = %d, want 0", lifecycleRows)
	}

	var route sql.NullString
	if err := db.QueryRow(`SELECT best_route_cex FROM offers WHERE id = ?`, offerID).Scan(&route); err != nil {
		t.Fatalf("select route: %v", err)
	}
	if route.Valid {
		t.Fatalf("best_route_cex = %q, want NULL", route.String)
	}
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

	var lifecycleRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM order_lifecycle`).Scan(&lifecycleRows); err != nil {
		t.Fatalf("count lifecycle: %v", err)
	}
	if lifecycleRows != 0 {
		t.Fatalf("lifecycle rows = %d, want rollback to 0", lifecycleRows)
	}
}
