package dbutil_test

import (
	"context"
	"dexunifiedapi/internal/testdb"
	"testing"
)

func TestContextMethodsRebindPlaceholders(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)

	if _, err := db.ExecContext(ctx, `CREATE TABLE items (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO items (id, name) VALUES (?, ?)`, 1, "alpha"); err != nil {
		t.Fatalf("exec context: %v", err)
	}

	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM items WHERE id = ?`, 1).Scan(&name); err != nil {
		t.Fatalf("query row context: %v", err)
	}
	if name != "alpha" {
		t.Fatalf("name = %q, want alpha", name)
	}

	rows, err := db.QueryContext(ctx, `SELECT name FROM items WHERE id = ?`, 1)
	if err != nil {
		t.Fatalf("query context: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected query row")
	}

	stmt, err := db.PrepareContext(ctx, `SELECT name FROM items WHERE id = ?`)
	if err != nil {
		t.Fatalf("prepare context: %v", err)
	}
	defer stmt.Close()
	if err := stmt.QueryRowContext(ctx, 1).Scan(&name); err != nil {
		t.Fatalf("prepared query: %v", err)
	}
}

func TestTxContextMethodsRebindPlaceholders(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)

	if _, err := db.ExecContext(ctx, `CREATE TABLE items (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `INSERT INTO items (id, name) VALUES (?, ?)`, 1, "alpha"); err != nil {
		t.Fatalf("tx exec context: %v", err)
	}
	stmt, err := tx.PrepareContext(ctx, `SELECT name FROM items WHERE id = ?`)
	if err != nil {
		t.Fatalf("tx prepare context: %v", err)
	}
	defer stmt.Close()

	var name string
	if err := stmt.QueryRowContext(ctx, 1).Scan(&name); err != nil {
		t.Fatalf("tx prepared query: %v", err)
	}
	if name != "alpha" {
		t.Fatalf("name = %q, want alpha", name)
	}
	if err := tx.QueryRowContext(ctx, `SELECT name FROM items WHERE id = ?`, 1).Scan(&name); err != nil {
		t.Fatalf("tx query row context: %v", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT name FROM items WHERE id = ?`, 1)
	if err != nil {
		t.Fatalf("tx query context: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected tx query row")
	}
}
