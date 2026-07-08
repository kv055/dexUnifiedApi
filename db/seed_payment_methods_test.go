package dbutil_test

import (
	"context"
	"testing"

	dbutil "dexunifiedapi/db"
	"dexunifiedapi/internal/testdb"

	"commons/paymentmethods"
)

// TestSeedPaymentMethodsIdempotent proves re-seeding never overwrites operator
// edits: testdb.Open already seeded once; an edit followed by a second
// SeedPaymentMethods run must leave the edit intact and add no rows.
func TestSeedPaymentMethodsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)

	var before int
	if err := db.QueryRow(`SELECT count(*) FROM payment_methods`).Scan(&before); err != nil {
		t.Fatalf("count seeded rows: %v", err)
	}
	if want := len(paymentmethods.SeedRows()); before != want {
		t.Fatalf("seeded rows = %d, want %d", before, want)
	}

	// Panel-style edit: recategorize, activate, and add manual aliases to SEPA.
	if _, err := db.ExecContext(ctx,
		`UPDATE payment_methods SET priority = 42, is_active = TRUE, alias_list = ARRAY['National Bank'] WHERE name = 'SEPA'`); err != nil {
		t.Fatalf("edit row: %v", err)
	}

	if err := dbutil.SeedPaymentMethods(db); err != nil {
		t.Fatalf("re-seed: %v", err)
	}

	var after, priority int
	var isActive bool
	var aliasList paymentmethods.TextArray
	if err := db.QueryRow(`SELECT count(*) FROM payment_methods`).Scan(&after); err != nil {
		t.Fatalf("count after re-seed: %v", err)
	}
	if after != before {
		t.Errorf("row count changed on re-seed: %d -> %d", before, after)
	}
	if err := db.QueryRow(`SELECT priority, is_active, alias_list FROM payment_methods WHERE name = 'SEPA'`).
		Scan(&priority, &isActive, &aliasList); err != nil {
		t.Fatalf("reload edited row: %v", err)
	}
	if priority != 42 || !isActive {
		t.Errorf("re-seed overwrote panel edit: priority=%d is_active=%v", priority, isActive)
	}
	if len(aliasList) != 1 || aliasList[0] != "National Bank" {
		t.Errorf("re-seed overwrote alias_list: %v", aliasList)
	}
}

// TestSeedPaymentMethodsLinksTransmitters proves the one-shot link pass
// activates and links the known transmitters once their accounts row exists,
// and that LoadRows surfaces the generated is_linked column.
func TestSeedPaymentMethodsLinksTransmitters(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)

	var accountID int64
	if err := db.QueryRow(
		`INSERT INTO accounts (provider, category, phone) VALUES ('Revolut', 'Fiat Transmitter', '@revtag') RETURNING id`).
		Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	if err := dbutil.SeedPaymentMethods(db); err != nil {
		t.Fatalf("re-seed with account present: %v", err)
	}

	rows, err := paymentmethods.LoadRows(ctx, db.DB)
	if err != nil {
		t.Fatalf("LoadRows: %v", err)
	}
	var revolut *paymentmethods.MethodRow
	for i := range rows {
		if rows[i].Name == "Revolut" {
			revolut = &rows[i]
			break
		}
	}
	if revolut == nil {
		t.Fatal("Revolut row missing")
	}
	if revolut.AccountID == nil || *revolut.AccountID != accountID {
		t.Errorf("Revolut account link = %v, want %d", revolut.AccountID, accountID)
	}
	if !revolut.IsActive || !revolut.IsLinked {
		t.Errorf("Revolut is_active=%v is_linked=%v, want both true", revolut.IsActive, revolut.IsLinked)
	}

	// A registry built from these rows must still canonicalize.
	reg := paymentmethods.NewRegistry(rows)
	if got := reg.Canonicalize("REVOLUT"); len(got) != 1 || got[0] != "Revolut" {
		t.Errorf("Canonicalize(REVOLUT) via DB rows = %v", got)
	}
	if len(reg.Warnings()) != 0 {
		t.Errorf("unexpected registry warnings from seeded rows: %v", reg.Warnings())
	}

	if _, err := db.ExecContext(ctx, `UPDATE payment_methods SET alias_list = ARRAY['Revoult Manual'] WHERE name = 'Revolut'`); err != nil {
		t.Fatalf("update alias_list: %v", err)
	}
	rows, err = paymentmethods.LoadRows(ctx, db.DB)
	if err != nil {
		t.Fatalf("LoadRows after alias update: %v", err)
	}
	reg = paymentmethods.NewRegistry(rows)
	if got := reg.Canonicalize("revoult_manual"); len(got) != 1 || got[0] != "Revolut" {
		t.Errorf("Canonicalize alias via DB rows = %v, want [Revolut]", got)
	}
}
