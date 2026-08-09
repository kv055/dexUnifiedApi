-- payment_method_chat_templates: per-payment-method chat request templates for DEX venues
-- where the counterparty's fiat details are exchanged via the trade chat (robosats, bisq2).
--
-- exchange:            chat-style DEX venue. Currently limited to the two chat venues.
-- payment_method_id:   canonical catalog row the template applies to.
-- template:            message body sent to the counterparty. Supports placeholders:
--                      {{amount}}    - expected fiat amount (e.g. "100.00")
--                      {{currency}}  - fiat currency (e.g. "EUR")
-- is_active:           whether this template should be used; inactive rows fall back
--                      to the default hardcoded template.
CREATE TABLE IF NOT EXISTS payment_method_chat_templates (
    id                  BIGSERIAL PRIMARY KEY,
    exchange            dex_provider NOT NULL,
    payment_method_id   BIGINT NOT NULL REFERENCES payment_methods(id) ON DELETE CASCADE,
    template            TEXT NOT NULL,
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_method_chat_templates_exchange_allowed_check CHECK (
        exchange IN ('robosats', 'bisq2')
    ),
    CONSTRAINT payment_method_chat_templates_unique UNIQUE (exchange, payment_method_id)
);

CREATE INDEX IF NOT EXISTS payment_method_chat_templates_exchange_idx ON payment_method_chat_templates(exchange);
CREATE INDEX IF NOT EXISTS payment_method_chat_templates_payment_method_id_idx ON payment_method_chat_templates(payment_method_id);
