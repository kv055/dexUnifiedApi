-- Analyzer-persisted per-offer lifecycle aggregates so the monitor can serve
-- past-offers profitability stats without re-aggregating order_lifecycle on
-- every request. Written by the closed-offer analysis job; backfilled here
-- for offers that already have lifecycle rows.
ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS avg_lifecycle_price DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS avg_hedge_notional_fiat DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS avg_leverage_used DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS avg_recommended_collateral_fiat DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS lifecycle_observation_count INTEGER;

-- Backfill overall (all-rows, matching the monitor's GROUP BY semantics)
-- averages for every offer with lifecycle rows. Rounding mirrors the
-- analyzer's historicalRound4/2 helpers.
UPDATE offers SET
    avg_lifecycle_price = agg.avg_price,
    avg_hedge_notional_fiat = agg.avg_hedge_notional,
    avg_leverage_used = agg.avg_leverage,
    avg_recommended_collateral_fiat = agg.avg_collateral,
    lifecycle_observation_count = agg.cnt
FROM (
    SELECT offer_db_id,
           COUNT(*) AS cnt,
           ROUND(AVG(price)::numeric, 4)::float8 AS avg_price,
           ROUND(AVG(hedge_notional_fiat)::numeric, 2)::float8 AS avg_hedge_notional,
           ROUND(AVG(leverage_used)::numeric, 4)::float8 AS avg_leverage,
           ROUND(AVG(recommended_collateral_fiat)::numeric, 2)::float8 AS avg_collateral
    FROM order_lifecycle
    GROUP BY offer_db_id
) agg
WHERE offers.id = agg.offer_db_id
  AND offers.lifecycle_observation_count IS NULL;
