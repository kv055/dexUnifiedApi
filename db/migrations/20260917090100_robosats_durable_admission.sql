-- Additive only: preserve the historical index and all session data. If legacy
-- reservations conflict, index creation deliberately fails rather than guessing
-- which robot/funds are safe to release. Resolve such rows before rollout.
ALTER TABLE robosats_trade_sessions ADD COLUMN IF NOT EXISTS reservation_active BOOLEAN NOT NULL DEFAULT TRUE;
CREATE UNIQUE INDEX IF NOT EXISTS robosats_reserved_robot_uidx ON robosats_trade_sessions(credential_id, coordinator_id) WHERE reservation_active;

CREATE TABLE IF NOT EXISTS robosats_execution_jobs (
    transaction_id BIGINT PRIMARY KEY REFERENCES transactions(id),
    quote_id UUID NOT NULL UNIQUE REFERENCES execution_quotes(id),
    order_data JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','done','needs_review')),
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE execution_quotes ADD COLUMN IF NOT EXISTS native_terms JSONB;
