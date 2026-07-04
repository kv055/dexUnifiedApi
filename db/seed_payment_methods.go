package dbutil

import (
	"fmt"

	"commons/paymentmethods"
)

// SeedPaymentMethods inserts the built-in payment-method catalog
// (commons/paymentmethods.SeedRows) into payment_methods. ON CONFLICT DO
// NOTHING makes it idempotent and guarantees operator edits made through the
// categorization panel are never overwritten. Call right after
// EnsureUnifiedSchema.
//
// After seeding it links the known fiat transmitters (Revolut, Wise, PayPal,
// PayPal F&F, N26) to their accounts row and activates them — but only rows
// that are still in the freshly-seeded state (unlinked AND inactive), so a
// deliberate unlink/deactivate in the panel sticks. This one-shot name match
// against the CHECK-constrained accounts.provider column is the only place
// the schema matches by name; all runtime lookups follow the foreign keys.
func SeedPaymentMethods(db *DB) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin payment_methods seed: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO payment_methods (name, merge_regex, category, match_order)
		VALUES ($1, NULLIF($2, ''), $3, $4)
		ON CONFLICT (name) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("prepare payment_methods seed: %w", err)
	}
	defer stmt.Close()

	for _, row := range paymentmethods.SeedRows() {
		if _, err := stmt.Exec(row.Name, row.MergeRegex, row.Category, row.MatchOrder); err != nil {
			return fmt.Errorf("seed payment method %q: %w", row.Name, err)
		}
	}

	if _, err := tx.Exec(`
		UPDATE payment_methods pm
		SET account_id = a.id, is_active = TRUE, updated_at = CURRENT_TIMESTAMP
		FROM accounts a
		WHERE pm.account_id IS NULL
		  AND pm.is_active = FALSE
		  AND pm.category = 'Fiat Transmitter'
		  AND a.category = 'Fiat Transmitter'
		  AND ((pm.name = 'Revolut'    AND a.provider = 'Revolut')
		    OR (pm.name = 'Wise'       AND a.provider = 'WISE')
		    OR (pm.name = 'PayPal'     AND a.provider = 'PayPal')
		    OR (pm.name = 'PayPal F&F' AND a.provider = 'PayPal')
		    OR (pm.name = 'N26'        AND a.provider = 'N26'))`); err != nil {
		return fmt.Errorf("link seeded fiat transmitters: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit payment_methods seed: %w", err)
	}
	return nil
}
