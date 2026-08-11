ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_status_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_status_check CHECK (
    status IS NULL OR status IN (
        'FiatToCexInitiated', 'FiatToCexArrived',
        'CexTradeflowStarted', 'HedgeFailedNeedsReview', 'CexTradeflowNeedsReview',
        'HedgelessTradeStarted', 'HedgelessFundingInitiated', 'HedgelessFundingNeedsReview',
        'DexTakenFundingNeedsReview', 'CexTradeSucessfull',
        'CexCryptoWithdrawalInitiated', 'CexCryptoWithdrawalArrived',
        'DexPositionOpen', 'DexPositionTaken', 'DexFiatHasArrived', 'DexPositionReleased'
    )
);
