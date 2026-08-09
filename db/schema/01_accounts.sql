CREATE TABLE IF NOT EXISTS accounts (
    id BIGSERIAL PRIMARY KEY,
    provider account_provider NOT NULL,
    email TEXT,
    password TEXT,
    phone TEXT,
    last_health_check TIMESTAMPTZ,
    is_healthy BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    payment_priority INTEGER,
    payment_tag TEXT,
    notification_channels notification_channel[],
    category account_category,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT accounts_provider_category_key UNIQUE (provider, category)
);
