# `db/schema.go`

Source package: `dbutil`  
Source lines: `52`

## Package Purpose

This file contributes to the `dbutil` package.

## File Role

This file embeds and applies the split Postgres schema files in sorted order.

## Runtime Behavior

- Reads `db/schema/*.sql` from the embedded filesystem and executes each file in its own transaction.

## Functions and Methods

### `EnsureUnifiedSchema`

EnsureUnifiedSchema applies the table-local schema files for a fresh or already-initialized Postgres database.

Signature: `func EnsureUnifiedSchema(db *DB) error`

## Constants and Variables

- `schemaFiles`: Embedded SQL schema files.

## Key Dependencies

- `embed`
- `fmt`
- `path`
- `sort`
- `strings`
