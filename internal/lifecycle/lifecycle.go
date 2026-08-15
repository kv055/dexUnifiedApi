package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"commons/tradecost"
	dbutil "dexunifiedapi/db"
)

func routingProfileForPersist(bd tradecost.CostBreakdown) any {
	profile := strings.TrimSpace(bd.RoutingProfile)
	if profile == "" {
		profile = tradecost.ProfileForMethod(bd.RouteMethod)
	}
	return nullIfEmpty(profile)
}

func routingFeeDetailForPersist(bd tradecost.CostBreakdown) (any, error) {
	raw, err := tradecost.MarshalRoutingFeeDetail(tradecost.BuildRoutingFeeDetail(bd))
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// PriceDiffPct returns the signed percentage difference of offerPrice versus
// refPrice, rounded to two decimals. Positive means offerPrice is above ref.
func PriceDiffPct(offerPrice, refPrice float64) float64 {
	if refPrice <= 0 {
		return 0
	}
	return math.Round((offerPrice/refPrice-1)*10000) / 100
}

// DirectionalPremiumPct returns the premium from the taker's perspective:
// positive means the offer is profitable to take. For an "ask" offer (maker
// sells BTC, we buy) profit grows as the price drops below ref, so the sign is
// flipped; for any other direction the raw difference is used as-is. This is the
// single source of truth for the premium sign, shared by the server and the
// snapshot job.
func DirectionalPremiumPct(direction string, offerPrice, refPrice float64) float64 {
	raw := PriceDiffPct(offerPrice, refPrice)
	if direction == "ask" {
		return -raw
	}
	return raw
}

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
	// MinCostBreakdown is the independently calculated cost at the offer's
	// minimum executable amount. It is nil when min and full size are equal.
	MinCostBreakdown *tradecost.CostBreakdown
}

// Repository represents a repository.
type Repository struct {
	db *dbutil.DB
}

// NewRepository creates a repository.
func NewRepository(db *dbutil.DB) Repository {
	return Repository{db: db}
}

// maxLifecyclePointsPerOffer caps how many recent price-path points LoadByOfferIDs
// returns per offer. The live chart only needs the recent path; an unbounded load of
// every offer's full history (1.6M rows across all live offers in prod) is what
// OOM-killed unified-api.
const maxLifecyclePointsPerOffer = 200

// LoadByOfferIDs loads the most recent lifecycle points (capped per offer) for the
// given offer DB ids, returned oldest-first per offer.
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
		`SELECT offer_db_id, t, price, net_premium_pct
		   FROM (
		     SELECT offer_db_id,
		            to_char(snapshot_time AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') AS t,
		            price,
		            net_premium_pct,
		            ROW_NUMBER() OVER (PARTITION BY offer_db_id ORDER BY snapshot_time DESC) AS rn
		       FROM order_lifecycle
		      WHERE offer_db_id IN (%s)
		   ) recent
		  WHERE rn <= %d
		  ORDER BY offer_db_id, t`,
		placeholders, maxLifecyclePointsPerOffer,
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

// Baseline is a price-path sample written directly by the snapshot job. It
// captures the columns that do not depend on the server's trade-cost engine, so
// a live offer always gets a lifecycle point even when the server is down.
type Baseline struct {
	// OfferID is the offers.id this sample belongs to.
	OfferID int64
	// Price is the offer's price.
	Price float64
	// RefPrice is the reference (market) price used for the premium.
	RefPrice float64
	// PremiumPct is the directional premium (positive = profitable).
	PremiumPct float64
}

// InsertBaseline writes one baseline lifecycle row per offer in a single
// transaction. Cost columns are left NULL for the server to enrich. Rows with a
// non-positive price or reference price are skipped (the schema requires both
// to be > 0) so one bad offer never drops the whole batch.
func (r Repository) InsertBaseline(ctx context.Context, baselines []Baseline) error {
	if len(baselines) == 0 || r.db == nil {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO order_lifecycle (offer_db_id, price, ref_price, premium_pct)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare baseline insert: %w", err)
	}
	defer stmt.Close()

	for _, b := range baselines {
		if b.Price <= 0 || b.RefPrice <= 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx,
			b.OfferID,
			tradecost.Round4(b.Price),
			tradecost.Round4(b.RefPrice),
			tradecost.Round4(b.PremiumPct),
		); err != nil {
			return fmt.Errorf("insert baseline lifecycle for offer %d: %w", b.OfferID, err)
		}
	}

	return tx.Commit()
}

// PersistSnapshot enriches the latest baseline lifecycle row for each offer with
// the server-computed cost fields, and stores derived route costs on the offers
// table. It never inserts lifecycle rows or touches price/ref_price/premium_pct:
// the snapshot job owns the price path, so the premium series stays consistent
// regardless of server health. net_premium is derived from the row's stored
// premium_pct so the enriched row remains internally consistent. Offers without
// a baseline row yet (or with a non-positive stored premium) are left untouched.
func (r Repository) PersistSnapshot(ctx context.Context, offers []Record) error {
	if len(offers) == 0 || r.db == nil {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	latestStmt, err := tx.PrepareContext(ctx, `
		SELECT id, premium_pct
		  FROM order_lifecycle
		 WHERE offer_db_id = ?
		 ORDER BY snapshot_time DESC, id DESC
		 LIMIT 1
	`)
	if err != nil {
		return fmt.Errorf("prepare latest lifecycle: %w", err)
	}
	defer latestStmt.Close()

	enrichStmt, err := tx.PrepareContext(ctx, `
		UPDATE order_lifecycle SET
			trade_cost_pct  = ?,
			trade_cost_btc  = ?,
			net_premium_pct = ?,
			net_premium_btc = ?,
			btc_amount      = ?,
			hedge_notional_fiat = ?,
			leverage_used = ?,
			recommended_collateral_fiat = ?,
			min_trade_cost_pct = ?,
			min_trade_cost_btc = ?,
			min_net_premium_pct = ?,
			min_net_premium_btc = ?,
			min_btc_amount = ?
		WHERE id = ?
	`)
	if err != nil {
		return fmt.Errorf("prepare enrich lifecycle: %w", err)
	}
	defer enrichStmt.Close()

	updStmt, err := tx.PrepareContext(ctx, `
		UPDATE offers SET
			best_route_cex = ?,
			routing_profile = ?,
			hedge_open_fee_btc = ?,
			hedge_close_fee_btc = ?,
			hedge_funding_fee_btc = ?,
			hedge_fee_sum_btc = ?,
			hedge_venue = ?,
			hedge_instrument = ?,
			hedge_transfer_minutes = ?,
			cex_taker_fee_btc = ?,
			cex_withdraw_fee_btc = ?,
			routing_fee_sum = ?,
			routing_fee_detail = ?,
			dex_fee_btc = ?,
			abort_cost_btc = ?,
			abort_cost_pct = ?
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

		// Enrich the latest baseline row the snapshot wrote for this offer. Cost
		// data only exists for premium offers, so gate on the row's stored
		// premium and derive net from it to keep the row self-consistent.
		var rowID int64
		var storedPremium float64
		switch err := latestStmt.QueryRowContext(ctx, o.ID).Scan(&rowID, &storedPremium); err {
		case nil:
			if storedPremium > 0 && bd.RouteCEX != "" {
				net := tradecost.Round4(storedPremium - bd.TotalCostPct)
				netBTC := tradecost.Round8(bd.BTCAmount * net / 100)
				var hedgeNotionalEUR, leverageUsed, recommendedCollateralEUR any
				if pos := bd.Hedge.Position; pos != nil {
					hedgeNotionalEUR = tradecost.Round2(pos.HedgeNotionalEUR)
					leverageUsed = tradecost.Round4(pos.LeverageUsed)
					recommendedCollateralEUR = tradecost.Round2(pos.RecommendedCollateral)
				}
				var minTradeCostPct, minTradeCostBTC, minNetPremiumPct, minNetPremiumBTC, minBTCAmount any
				if minBD := o.MinCostBreakdown; minBD != nil && minBD.BTCAmount > 0 {
					minNet := tradecost.Round4(storedPremium - minBD.TotalCostPct)
					minTradeCostPct = tradecost.Round4(minBD.TotalCostPct)
					minTradeCostBTC = tradecost.Round8(minBD.TotalCostBTC)
					minNetPremiumPct = minNet
					minNetPremiumBTC = tradecost.Round8(minBD.BTCAmount * minNet / 100)
					minBTCAmount = tradecost.Round8(minBD.BTCAmount)
				}
				if _, err := enrichStmt.ExecContext(ctx,
					tradecost.Round4(bd.TotalCostPct),
					tradecost.Round8(bd.TotalCostBTC),
					net,
					netBTC,
					tradecost.Round8(bd.BTCAmount),
					hedgeNotionalEUR, leverageUsed, recommendedCollateralEUR,
					minTradeCostPct, minTradeCostBTC, minNetPremiumPct, minNetPremiumBTC, minBTCAmount,
					rowID,
				); err != nil {
					return fmt.Errorf("enrich lifecycle for offer %d: %w", o.ID, err)
				}
			}
		case sql.ErrNoRows:
			// No baseline row yet (e.g. brand-new offer this cycle) — nothing to enrich.
		default:
			return fmt.Errorf("load latest lifecycle for offer %d: %w", o.ID, err)
		}

		if bd.RouteCEX != "" {
			var cexWithdrawFee any
			if bd.TradeDirection != "ask" {
				cexWithdrawFee = tradecost.Round8(bd.WithdrawalFeeBTC)
			}
			detailJSON, err := routingFeeDetailForPersist(bd)
			if err != nil {
				return fmt.Errorf("marshal routing_fee_detail for offer %d: %w", o.ID, err)
			}
			abort := tradecost.AbortCostFromBreakdown(bd)
			if _, err := updStmt.ExecContext(ctx,
				bd.RouteCEX,
				routingProfileForPersist(bd),
				tradecost.Round8(bd.Hedge.OpenFeeBTC),
				tradecost.Round8(bd.Hedge.CloseFeeBTC),
				tradecost.Round8(bd.Hedge.FundingFeeBTC),
				tradecost.Round8(bd.Hedge.FeeSumBTC),
				nullIfEmpty(bd.Hedge.Venue),
				nullIfEmpty(bd.Hedge.Instrument),
				nullIfZeroInt(bd.Hedge.TransferMinutes),
				tradecost.Round8(bd.CexTakerFeeBTC),
				cexWithdrawFee,
				tradecost.RoutingFeeSumBTC(bd),
				detailJSON,
				tradecost.Round8(bd.DexFeeBTC),
				tradecost.Round8(abort.Total.BTC),
				tradecost.Round4(abort.Total.Pct),
				o.ID,
			); err != nil {
				return fmt.Errorf("update lifecycle route costs for offer %d: %w", o.ID, err)
			}
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
