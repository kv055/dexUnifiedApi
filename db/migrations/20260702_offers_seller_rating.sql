-- Normalized seller/maker reputation, computed per-offer during snapshot
-- ingestion from each exchange's own reputation signal (see internal/sellerrating).
--
-- seller_rating_score      0-100 normalized reputation, NULL when no signal is obtainable.
-- seller_rating_confidence which methodology produced the score, so a bond-derived
--                          estimate is never silently compared as a verified feedback score.
-- seller_rating_raw        the exchange-specific inputs behind the score, for re-derivation.
--
-- ADD COLUMN IF NOT EXISTS is idempotent, so this is safe on databases that
-- already have the columns.
ALTER TABLE offers
    ADD COLUMN IF NOT EXISTS seller_rating_score DOUBLE PRECISION
        CHECK (seller_rating_score IS NULL OR (seller_rating_score >= 0 AND seller_rating_score <= 100)),
    ADD COLUMN IF NOT EXISTS seller_rating_confidence VARCHAR(32)
        CHECK (seller_rating_confidence IS NULL OR seller_rating_confidence IN
            ('feedback_verified', 'feedback_new', 'stake_bonded',
             'reputation_bonded', 'unavailable', 'account_signing')),
    ADD COLUMN IF NOT EXISTS seller_rating_raw JSONB;
