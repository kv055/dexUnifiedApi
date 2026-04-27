# `db/postgres.go`

Source package: `dbutil`  
Source lines: `124`

## Package Purpose

This file contributes to the `dbutil` package.

## File Role

This file defines 2 type(s), implements 12 function or method entry point(s).

## Runtime Behavior

- Reads from or writes to persistent storage.

## Types

### `DB`

DB represents a db.

Fields:

- `*sql.DB` `embedded`: Stores the *sql. d b value for the d b.

### `Tx`

Tx represents a tx.

Fields:

- `*sql.Tx` `embedded`: Stores the *sql. tx value for the tx.

## Functions and Methods

### `Open`

Open opens the requested resource.

Signature: `func Open(databaseURL string) (*DB, error)`

### `(*DB).Exec`

Exec executes a SQL statement after rebinding placeholders for PostgreSQL.

Signature: `func (db *DB) Exec(query string, args ...any) (sql.Result, error)`

### `(*DB).Query`

Query runs a SQL query after rebinding placeholders for PostgreSQL.

Signature: `func (db *DB) Query(query string, args ...any) (*sql.Rows, error)`

### `(*DB).QueryRow`

QueryRow runs a single-row SQL query after rebinding placeholders for PostgreSQL.

Signature: `func (db *DB) QueryRow(query string, args ...any) *sql.Row`

### `(*DB).Prepare`

Prepare prepares a SQL statement after rebinding placeholders for PostgreSQL.

Signature: `func (db *DB) Prepare(query string) (*sql.Stmt, error)`

### `(*DB).Begin`

Begin starts a transaction wrapped with placeholder-rebinding helpers.

Signature: `func (db *DB) Begin() (*Tx, error)`

### `(*Tx).Exec`

Exec executes a SQL statement after rebinding placeholders for PostgreSQL.

Signature: `func (tx *Tx) Exec(query string, args ...any) (sql.Result, error)`

### `(*Tx).Query`

Query runs a SQL query after rebinding placeholders for PostgreSQL.

Signature: `func (tx *Tx) Query(query string, args ...any) (*sql.Rows, error)`

### `(*Tx).QueryRow`

QueryRow runs a single-row SQL query after rebinding placeholders for PostgreSQL.

Signature: `func (tx *Tx) QueryRow(query string, args ...any) *sql.Row`

### `(*Tx).Prepare`

Prepare prepares a SQL statement after rebinding placeholders for PostgreSQL.

Signature: `func (tx *Tx) Prepare(query string) (*sql.Stmt, error)`

### `rebind`

This function is part of the `dbutil` package implementation.

Signature: `func rebind(query string) string`

### `strconvItoa`

This function is part of the `dbutil` package implementation.

Signature: `func strconvItoa(v int) string`

## Key Dependencies

- `database/sql`
- `strings`
- `github.com/jackc/pgx/v5/stdlib`

