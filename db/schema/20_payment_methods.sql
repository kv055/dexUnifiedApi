-- payment_methods: the catalog of canonical payment methods.
--
-- Replaces the hardcoded provider registry in commons/paymentmethods as the
-- source of truth for merging raw offer labels (merge_regex), categorization,
-- and linking a method to the operator's saved payment account:
--   Bank             -> accounts(id) with category Bank
--   Fiat Transmitter -> accounts(id) with category Fiat Transmitter
-- Cash, Gift Cards, Crypto and Others are structurally unlinkable (CHECK below).
-- Bank IBAN details are resolved via bank_accounts for the linked account
-- (at most one bank_accounts row per account + currency).
--
-- Rows are seeded from commons/paymentmethods.SeedRows() on boot
-- (ON CONFLICT DO NOTHING — panel edits are never overwritten); unknown raw
-- labels are auto-inserted as Others/inactive by the snapshot writer.
--
-- priority:    operator preference among an offer's accepted methods (higher wins).
-- match_order: canonicalization scan order (lower first, e.g. "SEPA Instant"
--              before "SEPA") — deliberately separate from priority.
-- popularity:  how often this method appears on newly inserted offers (starts at 0).
CREATE TABLE IF NOT EXISTS payment_methods (
    id                BIGSERIAL PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    merge_regex       TEXT,
    category          TEXT NOT NULL DEFAULT 'Others',
    account_id        BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    is_active         BOOLEAN NOT NULL DEFAULT FALSE,
    is_linked         BOOLEAN GENERATED ALWAYS AS (account_id IS NOT NULL) STORED,
    priority          INTEGER NOT NULL DEFAULT 0,
    match_order       INTEGER NOT NULL DEFAULT 1000,
    popularity        INTEGER NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_methods_category_allowed_check CHECK (
        category IN ('Cash', 'Bank', 'Fiat Transmitter', 'Gift Cards', 'Crypto', 'Others')
    ),
    CONSTRAINT payment_methods_link_category_check CHECK (
        account_id IS NULL OR category IN ('Bank', 'Fiat Transmitter')
    )
);

CREATE INDEX IF NOT EXISTS payment_methods_category_idx ON payment_methods(category);
