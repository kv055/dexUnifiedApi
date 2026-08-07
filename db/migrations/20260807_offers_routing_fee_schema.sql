-- Persist BTC routing as a profile enum + summary sum + JSON detail.
-- No backfill: existing rows keep renamed routing_fee_sum as-is; profile/detail stay NULL.

ALTER TABLE offers RENAME COLUMN network_fee_btc TO routing_fee_sum;

ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS routing_profile VARCHAR(32),
    ADD COLUMN IF NOT EXISTS routing_fee_detail JSONB;

ALTER TABLE offers DROP CONSTRAINT IF EXISTS offers_best_route_method_check;
ALTER TABLE offers DROP COLUMN IF EXISTS best_route_method;

ALTER TABLE offers DROP CONSTRAINT IF EXISTS offers_routing_profile_check;
ALTER TABLE offers
    ADD CONSTRAINT offers_routing_profile_check CHECK (
        routing_profile IS NULL OR routing_profile IN (
            'Direct_main2main',
            'Hub_main2main',
            'Swap_main2lightning'
        )
    );
