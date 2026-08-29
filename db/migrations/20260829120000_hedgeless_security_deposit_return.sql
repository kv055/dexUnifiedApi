ALTER TABLE transactions
    DROP CONSTRAINT IF EXISTS transactions_btc_pool_refill_status_check;

ALTER TABLE transactions
    ADD CONSTRAINT transactions_btc_pool_refill_status_check CHECK (
        btc_pool_refill_status IS NULL OR btc_pool_refill_status IN (
            'pending',
            'spot_buy_pending',
            'spot_bought',
            'withdrawal_initiated',
            'awaiting_security_deposit',
            'restored',
            'needs_review'
        )
    );
