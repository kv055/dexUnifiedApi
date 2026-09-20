package main

import (
	"database/sql"
	"fmt"
	"math"
	"time"
)

const deleteBelowPremiumPct = -3.0

type closedOfferDB interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type closedOfferRecord struct {
	id        int64
	exchange  string
	direction string
	price     float64
	btcMin    sql.NullFloat64
	btcMax    sql.NullFloat64
	firstSeen string
	lastSeen  string
}

func analyzeClosedOffers(sdb closedOfferDB) {
	resetIgnoredClosedOffersWithLifecycle(sdb)

	graceCutoff := closedOfferAnalysisGraceCutoff(time.Now())

	rows, err := sdb.Query(`
		SELECT id, exchange, direction, price, btc_min, btc_max,
		       to_char(first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
		       to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM offers
		WHERE is_live = FALSE
		  AND last_seen < ?
		  AND (
			historical_analysis_status IS NULL
			OR historical_analysis_status = 'ignored_no_lifecycle'
		  )
	`, graceCutoff)
	if err != nil {
		fmt.Printf("[ClosedAnalysis] query error: %v\n", err)
		return
	}
	defer rows.Close()

	var batch []closedOfferRecord
	for rows.Next() {
		var o closedOfferRecord
		if err := rows.Scan(&o.id, &o.exchange, &o.direction, &o.price, &o.btcMin, &o.btcMax, &o.firstSeen, &o.lastSeen); err != nil {
			continue
		}
		batch = append(batch, o)
	}

	analyzed := 0
	deleted := 0
	ignored := 0
	errors := 0

	for _, offer := range batch {
		status, removed := analyzeSingleClosedOffer(sdb, offer)
		switch status {
		case "analyzed", "non_premium":
			analyzed++
		case "ignored_no_lifecycle":
			ignored++
		case "analysis_error":
			errors++
		}
		if removed {
			deleted++
		}
	}

	if len(batch) > 0 {
		fmt.Printf("[ClosedAnalysis] processed=%d analyzed=%d ignored=%d errors=%d deleted=%d\n", len(batch), analyzed, ignored, errors, deleted)
	}
}

func resetIgnoredClosedOffersWithLifecycle(sdb closedOfferDB) {
	res, err := sdb.Exec(`
		UPDATE offers
		SET historical_analysis_status = NULL
		WHERE historical_analysis_status = 'ignored_no_lifecycle'
		  AND EXISTS (
			SELECT 1
			FROM order_lifecycle
			WHERE order_lifecycle.offer_db_id = offers.id
		  )
	`)
	if err != nil {
		fmt.Printf("[ClosedAnalysis] stale ignored recovery error: %v\n", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		fmt.Printf("[ClosedAnalysis] reset %d ignored_no_lifecycle offers with lifecycle rows\n", n)
	}
}

func analyzeSingleClosedOffer(sdb closedOfferDB, offer closedOfferRecord) (string, bool) {
	// Single query: overall aggregates + profitable-only conditional aggregates.
	// AVG(CASE WHEN net_premium_pct > 0 THEN col END) returns NULL when no row matches,
	// which maps cleanly to sql.NullFloat64.
	var totalCount, profitableCount, rawPositiveCount int
	var overallAvgPremium sql.NullFloat64 // all entries — used for delete threshold
	var avgRefPrice sql.NullFloat64       // all entries — market reference
	var avgBTCAmount sql.NullFloat64      // all entries — stored for non-premium entries too
	var profAvgPremium sql.NullFloat64    // profitable entries only
	var profAvgTradeCostPct sql.NullFloat64
	var profAvgTradeCostBTC sql.NullFloat64
	var profAvgNetPremiumPct sql.NullFloat64
	var profAvgNetPremiumBTC sql.NullFloat64
	var profAvgBTCAmount sql.NullFloat64
	var minAvgTradeCostPct sql.NullFloat64
	var minAvgTradeCostBTC sql.NullFloat64
	var minAvgNetPremiumPct sql.NullFloat64
	var minAvgNetPremiumBTC sql.NullFloat64
	var minAvgBTCAmount sql.NullFloat64
	// Overall (all-rows) lifecycle aggregates persisted for the monitor's
	// past-offers profitability stats, matching its GROUP BY semantics.
	var lifecycleAvgPrice sql.NullFloat64
	var lifecycleAvgHedgeNotional sql.NullFloat64
	var lifecycleAvgLeverage sql.NullFloat64
	var lifecycleAvgCollateral sql.NullFloat64

	err := sdb.QueryRow(`
		SELECT
			COUNT(*),
			AVG(ref_price),
			AVG(premium_pct),
			AVG(btc_amount),
			AVG(price),
			AVG(hedge_notional_fiat),
			AVG(leverage_used),
			AVG(recommended_collateral_fiat),
			COUNT(CASE WHEN net_premium_pct > 0 THEN 1 END),
			AVG(CASE WHEN net_premium_pct > 0 THEN premium_pct    END),
			AVG(CASE WHEN net_premium_pct > 0 THEN trade_cost_pct END),
			AVG(CASE WHEN net_premium_pct > 0 THEN trade_cost_btc END),
			AVG(CASE WHEN net_premium_pct > 0 THEN net_premium_pct END),
			AVG(CASE WHEN net_premium_pct > 0 THEN net_premium_btc END),
			AVG(CASE WHEN net_premium_pct > 0 THEN btc_amount     END),
			COUNT(CASE WHEN premium_pct > 0 THEN 1 END),
			AVG(CASE WHEN min_net_premium_pct > 0 THEN min_trade_cost_pct END),
			AVG(CASE WHEN min_net_premium_pct > 0 THEN min_trade_cost_btc END),
			AVG(CASE WHEN min_net_premium_pct > 0 THEN min_net_premium_pct END),
			AVG(CASE WHEN min_net_premium_pct > 0 THEN min_net_premium_btc END),
			AVG(CASE WHEN min_net_premium_pct > 0 THEN min_btc_amount END)
		FROM order_lifecycle
		WHERE offer_db_id = ?
	`, offer.id).Scan(
		&totalCount,
		&avgRefPrice,
		&overallAvgPremium,
		&avgBTCAmount,
		&lifecycleAvgPrice,
		&lifecycleAvgHedgeNotional,
		&lifecycleAvgLeverage,
		&lifecycleAvgCollateral,
		&profitableCount,
		&profAvgPremium,
		&profAvgTradeCostPct,
		&profAvgTradeCostBTC,
		&profAvgNetPremiumPct,
		&profAvgNetPremiumBTC,
		&profAvgBTCAmount,
		&rawPositiveCount,
		&minAvgTradeCostPct,
		&minAvgTradeCostBTC,
		&minAvgNetPremiumPct,
		&minAvgNetPremiumBTC,
		&minAvgBTCAmount,
	)
	if err != nil {
		fmt.Printf("[ClosedAnalysis] aggregate query error for offer %d: %v\n", offer.id, err)
		return "analysis_error", false
	}
	if totalCount == 0 || !avgRefPrice.Valid {
		_, _ = sdb.Exec(`UPDATE offers SET historical_analysis_status = ? WHERE id = ?`, "ignored_no_lifecycle", offer.id)
		return "ignored_no_lifecycle", false
	}

	// Only ever-junk offers are deleted: never net-profitable (profitableCount),
	// never even favorably priced (rawPositiveCount), AND deeply unprofitable on
	// average. Any offer that was ever premium falls through to the analyzed /
	// non_premium branches below so the cascade never erases a premium offer's
	// price-path history. The raw-positive guard matters during a server outage,
	// when baseline rows have NULL net_premium_pct (so profitableCount is 0) for
	// offers that were genuinely premium.
	if profitableCount == 0 && rawPositiveCount == 0 &&
		overallAvgPremium.Valid && overallAvgPremium.Float64 < deleteBelowPremiumPct {
		_, _ = sdb.Exec(`DELETE FROM offers WHERE id = ?`, offer.id)
		return "deleted_non_premium", true
	}

	// No profitable snapshots → non_premium (store basic stats only).
	if profitableCount == 0 {
		_, _ = sdb.Exec(`
			UPDATE offers SET
				historical_analysis_status = ?,
				avg_ref_price              = ?,
				avg_premium_pct            = ?,
				avg_btc_amount             = ?,
				avg_lifecycle_price        = ?,
				avg_hedge_notional_fiat    = ?,
				avg_leverage_used          = ?,
				avg_recommended_collateral_fiat = ?,
				lifecycle_observation_count     = ?
			WHERE id = ?
		`, "non_premium",
			historicalRound4(avgRefPrice.Float64),
			historicalRound4(overallAvgPremium.Float64),
			historicalRound8(avgBTCAmount.Float64),
			nullHistoricalRound4(lifecycleAvgPrice),
			nullHistoricalRound2(lifecycleAvgHedgeNotional),
			nullHistoricalRound4(lifecycleAvgLeverage),
			nullHistoricalRound2(lifecycleAvgCollateral),
			totalCount,
			offer.id,
		)
		return "non_premium", false
	}

	// Best route is maintained live on the offers table by the server.
	var bestRouteCEX, routingProfile sql.NullString
	_ = sdb.QueryRow(`SELECT best_route_cex, routing_profile FROM offers WHERE id = ?`, offer.id).
		Scan(&bestRouteCEX, &routingProfile)

	var cexTakerFeeBTC, cexWithdrawFeeBTC, routingFeeSum, hedgeOpenFeeBTC, hedgeCloseFeeBTC, hedgeFundingFeeBTC, hedgeFeeSumBTC, dexFeeBTC sql.NullFloat64
	var hedgeVenue, hedgeInstrument sql.NullString
	var hedgeTransferMinutes sql.NullInt64
	var routingFeeDetail []byte
	_ = sdb.QueryRow(`
		SELECT cex_taker_fee_btc, cex_withdraw_fee_btc, routing_fee_sum,
		       hedge_open_fee_btc, hedge_close_fee_btc, hedge_funding_fee_btc, hedge_fee_sum_btc, hedge_venue, hedge_instrument, hedge_transfer_minutes,
		       dex_fee_btc, routing_fee_detail
		FROM offers
		WHERE id = ?
	`, offer.id).Scan(
		&cexTakerFeeBTC,
		&cexWithdrawFeeBTC,
		&routingFeeSum,
		&hedgeOpenFeeBTC,
		&hedgeCloseFeeBTC,
		&hedgeFundingFeeBTC,
		&hedgeFeeSumBTC,
		&hedgeVenue,
		&hedgeInstrument,
		&hedgeTransferMinutes,
		&dexFeeBTC,
		&routingFeeDetail,
	)

	profitFiat := profAvgNetPremiumBTC.Float64 * avgRefPrice.Float64
	minProfitFiat := minAvgNetPremiumBTC.Float64 * avgRefPrice.Float64

	_, _ = sdb.Exec(`
		UPDATE offers SET
			historical_analysis_status = ?,
			avg_ref_price              = ?,
			avg_premium_pct            = ?,
			avg_trade_cost_pct         = ?,
			avg_trade_cost_btc         = ?,
			avg_hedge_fee_sum_btc      = ?,
			cex_taker_fee_btc          = ?,
			cex_withdraw_fee_btc       = ?,
			routing_fee_sum            = ?,
			hedge_open_fee_btc         = ?,
			hedge_close_fee_btc        = ?,
			hedge_funding_fee_btc      = ?,
			hedge_fee_sum_btc          = ?,
			hedge_venue                = ?,
			hedge_instrument           = ?,
			hedge_transfer_minutes     = ?,
			dex_fee_btc                = ?,
			avg_net_premium_pct        = ?,
			avg_estimated_profit_btc   = ?,
			avg_estimated_profit_fiat  = ?,
			avg_btc_amount             = ?,
			avg_min_trade_cost_pct      = ?,
			avg_min_trade_cost_btc      = ?,
			avg_min_net_premium_pct     = ?,
			avg_min_estimated_profit_btc  = ?,
			avg_min_estimated_profit_fiat = ?,
			avg_min_btc_amount          = ?,
			min_volume_analyzed         = TRUE,
			avg_lifecycle_price        = ?,
			avg_hedge_notional_fiat    = ?,
			avg_leverage_used          = ?,
			avg_recommended_collateral_fiat = ?,
			lifecycle_observation_count     = ?,
			best_route_cex             = ?,
			routing_profile            = ?,
			routing_fee_detail         = ?
		WHERE id = ?
	`,
		"analyzed",
		historicalRound4(avgRefPrice.Float64),
		historicalRound4(profAvgPremium.Float64),
		historicalRound4(profAvgTradeCostPct.Float64),
		historicalRound8(profAvgTradeCostBTC.Float64),
		nullHistoricalRound8(hedgeFeeSumBTC),
		nullHistoricalRound8(cexTakerFeeBTC),
		nullHistoricalRound8(cexWithdrawFeeBTC),
		nullHistoricalRound8(routingFeeSum),
		nullHistoricalRound8(hedgeOpenFeeBTC),
		nullHistoricalRound8(hedgeCloseFeeBTC),
		nullHistoricalRound8(hedgeFundingFeeBTC),
		nullHistoricalRound8(hedgeFeeSumBTC),
		nullHistoricalString(hedgeVenue),
		nullHistoricalString(hedgeInstrument),
		nullHistoricalInt(hedgeTransferMinutes),
		nullHistoricalRound8(dexFeeBTC),
		historicalRound4(profAvgNetPremiumPct.Float64),
		historicalRound8(profAvgNetPremiumBTC.Float64),
		historicalRound4(profitFiat),
		historicalRound8(profAvgBTCAmount.Float64),
		nullHistoricalRound4(minAvgTradeCostPct),
		nullHistoricalRound8(minAvgTradeCostBTC),
		nullHistoricalRound4(minAvgNetPremiumPct),
		nullHistoricalRound8(minAvgNetPremiumBTC),
		nullHistoricalRound4IfValid(minAvgNetPremiumBTC, minProfitFiat),
		nullHistoricalRound8(minAvgBTCAmount),
		nullHistoricalRound4(lifecycleAvgPrice),
		nullHistoricalRound2(lifecycleAvgHedgeNotional),
		nullHistoricalRound4(lifecycleAvgLeverage),
		nullHistoricalRound2(lifecycleAvgCollateral),
		totalCount,
		bestRouteCEX.String,
		nullHistoricalString(routingProfile),
		nullHistoricalJSON(routingFeeDetail),
		offer.id,
	)
	return "analyzed", false
}

func historicalRound2(v float64) float64 {
	return math.Round(v*1e2) / 1e2
}

func historicalRound4(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

func historicalRound8(v float64) float64 {
	return math.Round(v*1e8) / 1e8
}

func nullHistoricalRound8(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return historicalRound8(v.Float64)
}

func nullHistoricalRound4(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return historicalRound4(v.Float64)
}

func nullHistoricalRound2(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return historicalRound2(v.Float64)
}

func nullHistoricalRound4IfValid(valid sql.NullFloat64, value float64) any {
	if !valid.Valid {
		return nil
	}
	return historicalRound4(value)
}

func nullHistoricalString(v sql.NullString) any {
	if !v.Valid || v.String == "" {
		return nil
	}
	return v.String
}

func nullHistoricalInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func nullHistoricalJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
