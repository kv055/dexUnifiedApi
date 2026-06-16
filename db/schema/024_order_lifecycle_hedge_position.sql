-- Per-snapshot hedge position sizing for live offers, enriched onto the
-- order_lifecycle time series alongside trade_cost_pct/btc_amount (see
-- lifecycle.PersistSnapshot). Sourced from CostBreakdown.Hedge.Position
-- (commons/tradecost.HedgePositionDetail), which is already computed at that
-- point in the snapshot enrichment cycle. NULL whenever no hedge position was
-- simulated that cycle (no chosen route / no ref price).
ALTER TABLE order_lifecycle
    ADD COLUMN IF NOT EXISTS hedge_notional_eur DOUBLE PRECISION
        CHECK (hedge_notional_eur IS NULL OR hedge_notional_eur >= 0),
    ADD COLUMN IF NOT EXISTS leverage_used DOUBLE PRECISION
        CHECK (leverage_used IS NULL OR leverage_used >= 0),
    ADD COLUMN IF NOT EXISTS recommended_collateral_eur DOUBLE PRECISION
        CHECK (recommended_collateral_eur IS NULL OR recommended_collateral_eur >= 0);
