# `db/schema.go`

Source package: `dbutil`  
Source lines: `79`

## Package Purpose

This file contributes to the `dbutil` package.

## File Role

This file implements 1 function or method entry point(s), declares 1 package value(s).

## Runtime Behavior

- Reads from or writes to persistent storage.

## Functions and Methods

### `EnsureUnifiedSchema`

EnsureUnifiedSchema ensures the required state exists.

Signature: `func EnsureUnifiedSchema(db *DB) error`

## Constants and Variables

- `migrationFiles`: No inline declaration comment is present.

## Key Dependencies

- `embed`
- `fmt`
- `path`
- `sort`
- `strings`

