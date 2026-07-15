-- Payment accounts are ordered by the operator on the Accounts page. The rank
-- is separate from payment_methods.priority: it ranks saved accounts, where 1
-- is the preferred account.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS payment_priority INTEGER;

WITH ranked AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY category, provider, id) AS priority
    FROM accounts
    WHERE category IN ('Fiat Transmitter', 'Bank', 'Wallet addresses')
)
UPDATE accounts
SET payment_priority = ranked.priority
FROM ranked
WHERE accounts.id = ranked.id
  AND accounts.payment_priority IS NULL;
