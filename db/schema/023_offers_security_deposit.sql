-- Per-offer security deposit / bond the taker must lock to take this offer.
-- Bisq v1: whichever of buyer_security_deposit / seller_security_deposit
-- corresponds to the side we'd take (see fetchBisq in cmd/snapshot/main.go).
-- RoboSats: the maker's bond_size, which applies symmetrically to the taker.
-- NULL for exchanges with no deposit/bond concept (bisq2, hodlhodl, peachbitcoin).
ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS security_deposit_btc DOUBLE PRECISION
        CHECK (security_deposit_btc IS NULL OR security_deposit_btc >= 0),
    ADD COLUMN IF NOT EXISTS security_deposit_pct DOUBLE PRECISION
        CHECK (security_deposit_pct IS NULL OR security_deposit_pct >= 0);
