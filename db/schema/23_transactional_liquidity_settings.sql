-- transactional_liquidity_settings stores the operator's singleton target
-- profile. Derived BTC targets are calculated from the live market reference
-- price by the monitor. amount_fiat stores the persisted target; amount_source
-- records whether it was entered directly or derived from the configured cap.
CREATE TABLE IF NOT EXISTS transactional_liquidity_settings (
    id              INT PRIMARY KEY DEFAULT 1,
    fiat_currency   fiat_currency NOT NULL DEFAULT 'EUR',
    amount_source   TEXT NOT NULL DEFAULT 'custom' CHECK (amount_source IN ('custom', 'capped_volume')),
    amount_fiat     DOUBLE PRECISION CHECK (amount_fiat IS NULL OR amount_fiat > 0),
    trade_mode      transaction_trade_mode NOT NULL DEFAULT 'hedgefull_single_collateral',
    target_direction TEXT NOT NULL DEFAULT 'bid' CHECK (target_direction IN ('bid', 'ask')),
    config_version  BIGINT NOT NULL DEFAULT 1,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT transactional_liquidity_settings_single_row CHECK (id = 1)
);

INSERT INTO transactional_liquidity_settings (id)
VALUES (1)
ON CONFLICT DO NOTHING;
