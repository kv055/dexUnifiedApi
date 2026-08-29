ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS status_reason TEXT;

ALTER TABLE transactions
    DROP CONSTRAINT IF EXISTS transactions_status_check;

UPDATE transactions
SET status = 'CexTradeSuccessful'
WHERE status = 'CexTradeSucessfull';

UPDATE transactions
SET status = 'DexPositionTaken'
WHERE status = 'DexPositionOpen';

UPDATE transactions
SET status_reason = CASE
        WHEN status IS NULL THEN 'Migrated from legacy NULL transaction status'
        ELSE 'Migrated from legacy transaction status ' || status
    END,
    status = 'TransactionNeedsReview',
    transaction_complete = FALSE
WHERE status IS NULL
   OR status IN ('FiatToCexInitiated', 'FiatToCexArrived');

ALTER TABLE transactions
    ALTER COLUMN status SET NOT NULL;

ALTER TABLE transactions
    ADD CONSTRAINT transactions_status_check CHECK (
        status IN (
            'TransactionNeedsReview',
            'CexTradeflowStarted', 'HedgeFailedNeedsReview', 'CexTradeflowNeedsReview',
            'HedgelessTradeStarted', 'HedgelessPreparationFailed',
            'LiquidityPreparationNeedsReview', 'HedgelessFundingInitiated',
            'HedgelessFundingNeedsReview', 'DexTakenFundingNeedsReview',
            'CexTradeSuccessful',
            'CexCryptoWithdrawalInitiated', 'CexCryptoWithdrawalArrived',
            'DexPositionTaken', 'DexFiatHasArrived', 'DexPositionReleased'
        )
    );
