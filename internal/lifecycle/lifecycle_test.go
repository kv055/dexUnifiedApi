package lifecycle

import (
	"context"
	"database/sql"
	dbutil "dexunifiedapi/db"
	"math"
	"testing"

	"commons/tradecost"

	_ "modernc.org/sqlite"
)

const testSchema = `
CREATE TABLE offers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	best_route_cex TEXT,
	best_route_method TEXT,
	hedge_open_fee_btc REAL,
	hedge_close_fee_btc REAL,
	hedge_funding_fee_btc REAL,
	hedge_fee_sum_btc REAL,
	hedge_venue TEXT,
	hedge_instrument TEXT,
	hedge_transfer_minutes INTEGER,
	cex_taker_fee_btc REAL,
	cex_withdraw_fee_btc REAL,
	network_fee_btc REAL,
	dex_fee_btc REAL
);

CREATE TABLE order_lifecycle (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	offer_db_id INTEGER NOT NULL,
	snapshot_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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

func openTestDB(t *testing.T) *dbutil.DB {
	t.Helper()

	raw, err := sql.Open("sqlite", t.TempDir()+"/lifecycle.db")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		raw.Close()
	})
	if _, err := raw.Exec(testSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return &dbutil.DB{DB: raw}
}

func insertOffer(t *testing.T, db *dbutil.DB) int64 {
	t.Helper()

	res, err := db.Exec(`INSERT INTO offers DEFAULT VALUES`)
	if err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
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
