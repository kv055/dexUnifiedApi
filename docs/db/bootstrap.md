# `db/bootstrap.go`

Source package: `dbutil`  
Source lines: `105`

## Package Purpose

This file contributes to the `dbutil` package.

## File Role

This file defines 1 type(s), implements 3 function or method entry point(s), declares 4 package value(s).

## Runtime Behavior

- Reads from or writes to persistent storage.

## Types

### `RoleBootstrapConfig`

RoleBootstrapConfig represents a role bootstrap config.

Fields:

- `DatabaseName` `string`: DatabaseName is the database name value for the role bootstrap config.
- `UnifiedAPIPassword` `string`: UnifiedAPIPassword is the unified apipassword value for the role bootstrap config.
- `MonitorPassword` `string`: MonitorPassword is the monitor password value for the role bootstrap config.
- `MMControllerPassword` `string`: MMControllerPassword is the mmcontroller password value for the role bootstrap config.
- `FiatControllerPassword` `string`: FiatControllerPassword is a fiat-denominated fiat controller password value.

## Functions and Methods

### `EnsureAppRoles`

EnsureAppRoles ensures the required state exists.

Signature: `func EnsureAppRoles(admin *DB, cfg RoleBootstrapConfig) error`

### `ensureRole`

This function is part of the `dbutil` package implementation.

Signature: `func ensureRole(admin *DB, roleName, password string) error`

### `quoteLiteral`

This function is part of the `dbutil` package implementation.

Signature: `func quoteLiteral(value string) string`

## Constants and Variables

- `roleUnifiedAPI`: No inline declaration comment is present.
- `roleMonitor`: No inline declaration comment is present.
- `roleMMController`: No inline declaration comment is present.
- `roleFiatController`: No inline declaration comment is present.

## Key Dependencies

- `fmt`
- `strings`

