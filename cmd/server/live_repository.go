package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	dbutil "dexunifiedapi/db"
)

// liveOfferRepository loads the authoritative live book from PostgreSQL. The
// latest lifecycle sample supplies the reference price captured for that offer's
// fiat currency; all static analytics and rating inputs come from offers.
type liveOfferRepository struct {
	db *dbutil.DB
}

func newLiveOfferRepository(db *dbutil.DB) liveOfferRepository {
	return liveOfferRepository{db: db}
}

func (r liveOfferRepository) Load(ctx context.Context) ([]LiveOffer, error) {
	if r.db == nil {
		return nil, fmt.Errorf("live-offer repository is not configured")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			o.id,
			o.exchange,
			o.fiat_currency,
			o.direction,
			o.price,
			latest.ref_price,
			o.btc_min,
			o.btc_max,
			o.fiat_min,
			o.fiat_max,
			COALESCE(to_json(o.payment_methods)::text, '[]'),
			o.offer_id,
			to_char(o.first_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
			to_char(o.last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
			o.price_type,
			o.security_deposit_pct,
			o.seller_rating_score,
			o.seller_rating_confidence,
			o.seller_rating_raw,
			COALESCE(o.maker_id, '')
		FROM offers o
		LEFT JOIN LATERAL (
			SELECT ol.ref_price
			FROM order_lifecycle ol
			WHERE ol.offer_db_id = o.id
			ORDER BY ol.snapshot_time DESC, ol.id DESC
			LIMIT 1
		) latest ON TRUE
		WHERE o.is_live = TRUE
		ORDER BY o.fiat_currency, o.exchange, o.direction, o.id
	`)
	if err != nil {
		return nil, fmt.Errorf("query live offers: %w", err)
	}
	defer rows.Close()

	offers := make([]LiveOffer, 0)
	for rows.Next() {
		var (
			o                  LiveOffer
			refPrice           sql.NullFloat64
			paymentMethodsJSON string
			ratingRaw          []byte
		)
		if err := rows.Scan(
			&o.ID,
			&o.Exchange,
			&o.FiatCurrency,
			&o.Direction,
			&o.Price,
			&refPrice,
			&o.BTCMin,
			&o.BTCMax,
			&o.FiatMin,
			&o.FiatMax,
			&paymentMethodsJSON,
			&o.OfferID,
			&o.FirstSeen,
			&o.LastSeen,
			&o.PriceType,
			&o.SecurityDepositPct,
			&o.SellerRatingScore,
			&o.SellerRatingConfidence,
			&ratingRaw,
			&o.MakerID,
		); err != nil {
			return nil, fmt.Errorf("scan live offer: %w", err)
		}
		if refPrice.Valid {
			o.ReferencePrice = refPrice.Float64
		}
		if err := json.Unmarshal([]byte(paymentMethodsJSON), &o.PaymentMethods); err != nil {
			return nil, fmt.Errorf("decode payment methods for live offer %d: %w", o.ID, err)
		}
		if len(ratingRaw) > 0 {
			o.SellerRatingRaw = append(o.SellerRatingRaw[:0], ratingRaw...)
		}
		offers = append(offers, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate live offers: %w", err)
	}
	return offers, nil
}
