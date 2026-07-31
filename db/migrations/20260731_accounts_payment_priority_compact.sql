-- Close gaps in accounts.payment_priority left by deletes or category changes
-- that predated compaction. Relative order is preserved; ranks become 1..N.
WITH ranked AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY payment_priority NULLS LAST, id) AS priority
    FROM accounts
    WHERE category IN ('Fiat Transmitter', 'Bank', 'Wallet addresses')
)
UPDATE accounts
SET payment_priority = ranked.priority
FROM ranked
WHERE accounts.id = ranked.id;
