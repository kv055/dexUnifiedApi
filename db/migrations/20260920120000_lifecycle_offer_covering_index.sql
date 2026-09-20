-- Covering index for the per-offer lifecycle aggregates
-- (AVG(price), AVG(ref_price), ... GROUP BY offer_db_id) behind the
-- past-offers profitability stats. Lets Postgres answer the aggregate as an
-- index-only scan instead of heap-fetching every lifecycle row.
-- Plain CREATE INDEX (not CONCURRENTLY): the migration runner applies each
-- file inside a transaction, where CONCURRENTLY is forbidden. The build
-- briefly blocks lifecycle snapshot writes; run during a low-traffic window
-- on large installations.
CREATE INDEX IF NOT EXISTS idx_lifecycle_offer_covering
    ON order_lifecycle (offer_db_id)
    INCLUDE (price, ref_price, premium_pct, trade_cost_pct, trade_cost_btc,
             net_premium_pct, net_premium_btc, btc_amount,
             hedge_notional_fiat, leverage_used, recommended_collateral_fiat);
