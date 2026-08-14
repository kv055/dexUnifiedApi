-- Make the persisted fee units explicit. Percentage values are decimal
-- fractions (for example 0.0005 means 0.05%), while withdrawal fees are BTC.
ALTER TABLE mm_cex_provider_policies
    RENAME COLUMN spot_taker_fee_rate TO spot_taker_fee_percent;

ALTER TABLE mm_cex_provider_policies
    RENAME COLUMN btc_withdrawal_fee_btc TO withdrawal_fee_btc;

ALTER TABLE mm_cex_provider_policies
    RENAME COLUMN futures_open_taker_fee_rate TO futures_open_taker_fee_percent;

ALTER TABLE mm_cex_provider_policies
    RENAME COLUMN futures_close_taker_fee_rate TO futures_close_taker_fee_percent;

ALTER TABLE mm_cex_provider_policies
    RENAME COLUMN futures_funding_rate TO futures_funding_fee_percent;
