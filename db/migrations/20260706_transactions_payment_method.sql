-- Persist the canonical payment method selected for a trade so the chat-venue
-- payment-details flow can look up the per-method template in payment_method_chat_templates.
--
-- The value is the catalog name resolved by the bid-side pre-trade check (explicitly
-- requested, or highest-priority linked+active pick from payment_methods).
ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS payment_method TEXT;
