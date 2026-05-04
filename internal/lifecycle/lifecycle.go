package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"commons/tradecost"
	dbutil "dexunifiedapi/db"
)

// Point represents a point.
type Point struct {
	// Time records the relevant time for the point.
	Time string `json:"time"`
	// Price is the price value used by the point.
	Price float64 `json:"price"`
	// NetPremiumPct is a percentage value.
	NetPremiumPct *float64 `json:"net_premium_pct,omitempty"`
}

// Record represents a record.
type Record struct {
	// ID identifies the record.
	ID int64
	// Price is the price value used by the record.
	Price float64
	// RefPrice is the price value used by the record.
	RefPrice float64
	// PriceDiffPct is a percentage value.
	PriceDiffPct float64
	// NetPremiumPct is a percentage value.
	NetPremiumPct float64
	// CostBreakdown is the cost breakdown value for the record.
	CostBreakdown tradecost.CostBreakdown
}

// Repository represents a repository.
type Repository struct {
	db *dbutil.DB
}

// NewRepository creates a repository.
func NewRepository(db *dbutil.DB) Repository {
	return Repository{db: db}
}

// LoadByOfferIDs loads a by offer ids.
func (r Repository) LoadByOfferIDs(ctx context.Context, ids []int64) (map[int64][]Point, error) {
	result := make(map[int64][]Point, len(ids))
	if r.db == nil || len(ids) == 0 {
		return result, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT offer_db_id,
		        to_char(snapshot_time AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
		        price,
		        net_premium_pct
		   FROM order_lifecycle
		  WHERE offer_db_id IN (%s)
		  ORDER BY offer_db_id, snapshot_time`,
		placeholders,
	), args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var offerID int64
		var snapTime string
		var price float64
		var netPremiumPct sql.NullFloat64
		if err := rows.Scan(&offerID, &snapTime, &price, &netPremiumPct); err != nil {
			continue
		}
		pt := Point{Time: snapTime, Price: price}
		if netPremiumPct.Valid {
			pt.NetPremiumPct = &netPremiumPct.Float64
		}
		result[offerID] = append(result[offerID], pt)
	}
	return result, nil
}

// PersistSnapshot writes lifecycle samples and derived route costs.
func (r Repository) PersistSnapshot(ctx context.Context, offers []Record) error {
	if len(offers) == 0 || r.db == nil {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	insStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO order_lifecycle
			(offer_db_id, price, ref_price, premium_pct,
			 trade_cost_pct, trade_cost_btc, net_premium_pct, net_premium_btc, btc_amount)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer insStmt.Close()

	updStmt, err := tx.PrepareContext(ctx, `
		UPDATE offers SET
			best_route_cex = ?,
			best_route_method = ?,
			hedge_open_fee_btc = ?,
			hedge_close_fee_btc = ?,
			hedge_funding_fee_btc = ?,
			hedge_fee_sum_btc = ?,
			hedge_venue = ?,
			hedge_instrument = ?,
			hedge_transfer_minutes = ?,
			cex_taker_fee_btc = ?,
			cex_withdraw_fee_btc = ?,
			network_fee_btc = ?,
			dex_fee_btc = ?
		WHERE id = ?
	`)
	if err != nil {
		return fmt.Errorf("prepare update: %w", err)
	}
	defer updStmt.Close()

	for _, o := range offers {
		if o.RefPrice <= 0 {
			continue
		}
		bd := o.CostBreakdown
		var cexWithdrawFee any
		if bd.TradeDirection != "ask" {
			cexWithdrawFee = tradecost.Round8(bd.WithdrawalFeeBTC)
		}
		_, _ = insStmt.ExecContext(ctx,
			o.ID,
			tradecost.Round4(o.Price),
			tradecost.Round4(o.RefPrice),
			tradecost.Round4(o.PriceDiffPct),
			tradecost.Round4(bd.TotalCostPct),
			tradecost.Round8(bd.TotalCostBTC),
			tradecost.Round4(o.NetPremiumPct),
			tradecost.Round8(bd.EstimatedProfitBTC),
			tradecost.Round8(bd.BTCAmount),
		)
		if bd.RouteCEX != "" {
			_, _ = updStmt.ExecContext(ctx,
				bd.RouteCEX,
				bd.RouteMethod,
				tradecost.Round8(bd.Hedge.OpenFeeBTC),
				tradecost.Round8(bd.Hedge.CloseFeeBTC),
				tradecost.Round8(bd.Hedge.FundingFeeBTC),
				tradecost.Round8(bd.Hedge.FeeSumBTC),
				nullIfEmpty(bd.Hedge.Venue),
				nullIfEmpty(bd.Hedge.Instrument),
				nullIfZeroInt(bd.Hedge.TransferMinutes),
				tradecost.Round8(bd.CexTakerFeeBTC),
				cexWithdrawFee,
				tradecost.Round8(bd.NetworkFeeBTC),
				tradecost.Round8(bd.DexFeeBTC),
				o.ID,
			)
		}
	}

	return tx.Commit()
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullIfZeroInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
