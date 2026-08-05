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
// Linking a catalog row to an accounts row is intentional operator work on
// the Accounts Management payment tab — seeding never sets account_id.
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit payment_methods seed: %w", err)
	}
	return nil
}
