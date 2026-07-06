-- telegram_settings stores the shared Telegram bot credentials and per-service
-- webhook URLs used by dexMmController (trade-chat relay + anomaly alerts, DEXMM
-- webhook) and dexFiatController pushpush (fiat-arrival escalation, PUSHPUSH
-- webhook). Single-row table; both services read it at startup and poll
-- config_version for hot-reload. Owned by the unified schema so either service
-- can read/write the shared bot token.
CREATE TABLE IF NOT EXISTS telegram_settings (
    id                   INT PRIMARY KEY DEFAULT 1,
    bot_token            TEXT NOT NULL DEFAULT '',
    chat_id              TEXT NOT NULL DEFAULT '',
    dexmm_webhook_url    TEXT NOT NULL DEFAULT '',
    pushpush_webhook_url TEXT NOT NULL DEFAULT '',
    config_version       BIGINT NOT NULL DEFAULT 1,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT telegram_settings_single_row CHECK (id = 1)
);

INSERT INTO telegram_settings (id) VALUES (1) ON CONFLICT DO NOTHING;
