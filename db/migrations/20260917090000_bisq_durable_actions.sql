-- V2 is deliberately separate from the unsafe v1 worker. Never replay v1 rows.
CREATE TABLE bisq_execution_runs (
    transaction_id BIGINT PRIMARY KEY REFERENCES transactions(id),
    quote_id UUID NOT NULL UNIQUE REFERENCES bisq_execution_quotes(id),
    request_key TEXT NOT NULL UNIQUE,
    version INTEGER NOT NULL DEFAULT 2 CHECK (version = 2),
    stage TEXT NOT NULL DEFAULT 'accepted',
    deadline_at TIMESTAMPTZ NOT NULL,
    review_reason TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE bisq_execution_reservations (
    resource TEXT PRIMARY KEY,
    transaction_id BIGINT NOT NULL REFERENCES bisq_execution_runs(transaction_id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE bisq_execution_leases (
    transaction_id BIGINT PRIMARY KEY REFERENCES bisq_execution_runs(transaction_id),
    owner UUID NOT NULL,
    fence BIGINT NOT NULL DEFAULT 1,
    lease_until TIMESTAMPTZ NOT NULL
);
CREATE TABLE bisq_execution_actions (
    transaction_id BIGINT NOT NULL REFERENCES bisq_execution_runs(transaction_id),
    kind TEXT NOT NULL,
    external_key TEXT NOT NULL UNIQUE,
    request JSONB NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('intent', 'confirmed', 'review_required')),
    receipt JSONB,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (transaction_id, kind)
);
