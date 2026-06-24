-- Routing rework: offers.best_route_method now stores the per-DEX/side routing
-- method kind (onchain_direct | onchain_hub | onchain_swap) instead of the old
-- onchain | lightning settlement labels. Historical rows may still hold the old
-- values, so keep them allowed too. Idempotent: drop-then-add on every boot.
ALTER TABLE offers DROP CONSTRAINT IF EXISTS offers_best_route_method_check;
ALTER TABLE offers ADD CONSTRAINT offers_best_route_method_check
    CHECK (best_route_method IS NULL OR best_route_method IN (
        'onchain', 'lightning', 'onchain_swap',
        'onchain_direct', 'onchain_hub'
    ));
