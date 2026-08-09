-- fiat_tx_push_logs records every inbound push payment notification received by the
-- dexFiatController pushpush server and tracks it through the match → release pipeline.
-- One row per POST /notify; status advances as the pipeline progresses. Owned by the
-- dexFiatController service but lives in the shared schema so operators can query it
-- alongside transactions.
CREATE TABLE IF NOT EXISTS fiat_tx_push_logs (
    id BIGSERIAL PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- source notification
    app VARCHAR(64),
    title TEXT,
    text TEXT,
    raw JSONB,
    normalized TEXT,

    -- derived/normalized fields used by the matcher
    amount DOUBLE PRECISION,
    currency fiat_currency,
    sender TEXT,

    -- pipeline state
    status VARCHAR(40) NOT NULL,
    matched_tx_id BIGINT,           -- transactions.id when a match is made (no FK: loose coupling)
    rule_decision VARCHAR(16),      -- allow / deny / null
    telegram_pending_id VARCHAR(32),-- pending store id when escalated to Telegram
    error TEXT,                     -- last error message at this stage, if any

    CONSTRAINT fiat_tx_push_logs_status_check CHECK (
        status IN (
            'received',
            'denied',
            'unmatched',
            'escalated_classify',
            'escalated_pick',
            'unclassified_no_escalation',
            'ambiguous_no_escalation',
            'released',
            'already_released',
            'release_error',
            'mark_error',
            'list_error',
            'telegram_error',
            'user_allowed',
            'user_denied',
            'picked'
        )
    )
);

CREATE INDEX IF NOT EXISTS idx_fiat_tx_push_logs_received_at
    ON fiat_tx_push_logs (received_at DESC);
CREATE INDEX IF NOT EXISTS idx_fiat_tx_push_logs_status
    ON fiat_tx_push_logs (status);
CREATE INDEX IF NOT EXISTS idx_fiat_tx_push_logs_app
    ON fiat_tx_push_logs (app);
CREATE INDEX IF NOT EXISTS idx_fiat_tx_push_logs_matched_tx
    ON fiat_tx_push_logs (matched_tx_id) WHERE matched_tx_id IS NOT NULL;
