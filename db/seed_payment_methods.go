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
// Names that already appear in another row's alias_list are not re-inserted:
// degrading a seeded method (e.g. Junk Method → alias of PayPal) must survive
// subsequent boots. Any catalog row that is already represented as an alias
// is removed so earlier boots cannot resurrect degraded methods.
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
		SELECT $1, NULLIF($2, ''), $3, $4
		WHERE NOT EXISTS (
			SELECT 1
			FROM payment_methods pm
			WHERE EXISTS (
				SELECT 1
				FROM unnest(pm.alias_list) AS existing_alias
				WHERE lower(trim(replace(existing_alias, '_', ' '))) =
				      lower(trim(replace($1, '_', ' ')))
			)
		)
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

	// Heal rows resurrected by older seeders (or left behind when an alias was
	// typed without going through DegradeToAlias): a name that is already an
	// alias of another provider must not keep its own catalog row.
	if _, err := tx.Exec(`
		DELETE FROM payment_methods candidate
		WHERE EXISTS (
			SELECT 1
			FROM payment_methods provider
			WHERE provider.id <> candidate.id
			  AND EXISTS (
				SELECT 1
				FROM unnest(provider.alias_list) AS alias_name
				WHERE lower(trim(replace(candidate.name, '_', ' '))) =
				      lower(trim(replace(alias_name, '_', ' ')))
			  )
		)`); err != nil {
		return fmt.Errorf("remove payment methods already represented as aliases: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE accounts
		SET is_active = FALSE, updated_at = CURRENT_TIMESTAMP
		WHERE category IN ('Fiat Transmitter', 'Bank', 'Wallet addresses')
		  AND is_active = TRUE
		  AND NOT EXISTS (
			SELECT 1 FROM payment_methods WHERE account_id = accounts.id
		  )`); err != nil {
		return fmt.Errorf("deactivate unlinked payment accounts after alias cleanup: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit payment_methods seed: %w", err)
	}
	return nil
}
