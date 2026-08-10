-- transactional_liquidity_settings stores the operator's singleton target
-- profile. Derived BTC targets are calculated from the live market reference
-- price by the monitor; the amount source is inferred from custom_amount.
CREATE TABLE IF NOT EXISTS transactional_liquidity_settings (
    id              INT PRIMARY KEY DEFAULT 1,
    fiat_currency   fiat_currency NOT NULL DEFAULT 'EUR',
    custom_amount   DOUBLE PRECISION CHECK (custom_amount IS NULL OR custom_amount > 0),
    trade_mode      transaction_trade_mode NOT NULL DEFAULT 'hedgefull_single_collateral',
    config_version  BIGINT NOT NULL DEFAULT 1,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT transactional_liquidity_settings_single_row CHECK (id = 1)
);

INSERT INTO transactional_liquidity_settings (id)
VALUES (1)
ON CONFLICT DO NOTHING;
