-- Add bid/ask variants to existing chat templates. Existing rows remain the
-- bid template; operators can create the ask template independently.
ALTER TABLE payment_method_chat_templates
    ADD COLUMN IF NOT EXISTS trade_direction TEXT NOT NULL DEFAULT 'bid';

ALTER TABLE payment_method_chat_templates
    DROP CONSTRAINT IF EXISTS payment_method_chat_templates_unique;

ALTER TABLE payment_method_chat_templates
    DROP CONSTRAINT IF EXISTS payment_method_chat_templates_trade_direction_check;

ALTER TABLE payment_method_chat_templates
    ADD CONSTRAINT payment_method_chat_templates_trade_direction_check
    CHECK (trade_direction IN ('bid', 'ask'));

ALTER TABLE payment_method_chat_templates
    ADD CONSTRAINT payment_method_chat_templates_unique
    UNIQUE (exchange, payment_method_id, trade_direction);
