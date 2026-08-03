-- payment_methods: the catalog of canonical payment methods.
--
-- Replaces the hardcoded provider registry in commons/paymentmethods as the
-- source of truth for merging raw offer labels (merge_regex), categorization,
-- and linking a method to the operator's saved payment solution:
--   Bank             -> bank_accounts(id)
--   Fiat Transmitter -> accounts(id)
--   Crypto           -> wallet_addresses(id)
-- Cash, Gift Cards and Others are structurally unlinkable (CHECK below).
--
-- Rows are seeded from commons/paymentmethods.SeedRows() on boot
-- (ON CONFLICT DO NOTHING — panel edits are never overwritten); unknown raw
-- labels are auto-inserted as Others/inactive by the snapshot writer.
--
-- priority:    operator preference among an offer's accepted methods (higher wins).
-- match_order: canonicalization scan order (lower first, e.g. "SEPA Instant"
--              before "SEPA") — deliberately separate from priority.
CREATE TABLE IF NOT EXISTS payment_methods (
    id                BIGSERIAL PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    merge_regex       TEXT,
    category          TEXT NOT NULL DEFAULT 'Others',
    bank_account_id   BIGINT REFERENCES bank_accounts(id)    ON DELETE SET NULL,
    account_id        BIGINT REFERENCES accounts(id)         ON DELETE SET NULL,
    wallet_address_id BIGINT REFERENCES wallet_addresses(id) ON DELETE SET NULL,
    is_active         BOOLEAN NOT NULL DEFAULT FALSE,
    is_linked         BOOLEAN GENERATED ALWAYS AS (
                          bank_account_id IS NOT NULL
                          OR account_id IS NOT NULL
                          OR wallet_address_id IS NOT NULL) STORED,
    priority          INTEGER NOT NULL DEFAULT 0,
    match_order       INTEGER NOT NULL DEFAULT 1000,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_methods_category_allowed_check CHECK (
        category IN ('Cash', 'Bank', 'Fiat Transmitter', 'Gift Cards', 'Crypto', 'Others')
    ),
    CONSTRAINT payment_methods_single_link_check CHECK (
        (bank_account_id IS NOT NULL)::int
        + (account_id IS NOT NULL)::int
        + (wallet_address_id IS NOT NULL)::int <= 1
    ),
    CONSTRAINT payment_methods_link_category_check CHECK (
        (bank_account_id   IS NULL OR category = 'Bank')
        AND (account_id        IS NULL OR category = 'Fiat Transmitter')
        AND (wallet_address_id IS NULL OR category = 'Crypto')
    )
);

CREATE INDEX IF NOT EXISTS payment_methods_category_idx ON payment_methods(category);
