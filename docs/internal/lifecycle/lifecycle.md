# `internal/lifecycle/lifecycle.go`

Source package: `lifecycle`  
Source lines: `189`

## Package Purpose

This file contributes to the `lifecycle` package.

## File Role

This file defines 3 type(s), implements 5 function or method entry point(s).

## Runtime Behavior

- Reads from or writes to persistent storage.
- Serializes or deserializes JSON payloads.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `Point`

Point represents a point.

Fields:

- `Time` `string`: Time records the relevant time for the point.
- `Price` `float64`: Price is the price value used by the point.
- `NetPremiumPct` `*float64`: NetPremiumPct is a percentage value.

### `Record`

Record represents a record.

Fields:

- `ID` `int64`: ID identifies the record.
- `Price` `float64`: Price is the price value used by the record.
- `RefPrice` `float64`: RefPrice is the price value used by the record.
- `PriceDiffPct` `float64`: PriceDiffPct is a percentage value.
- `NetPremiumPct` `float64`: NetPremiumPct is a percentage value.
- `CostBreakdown` `tradecost.CostBreakdown`: CostBreakdown is the cost breakdown value for the record.

### `Repository`

Repository represents a repository.

Fields:

- `db` `*dbutil.DB`: Stores the db value for the repository.

## Functions and Methods

### `NewRepository`

NewRepository creates a repository.

Signature: `func NewRepository(db *dbutil.DB) Repository`

### `(Repository).LoadByOfferIDs`

LoadByOfferIDs loads a by offer ids.

Signature: `func (r Repository) LoadByOfferIDs(ctx context.Context, ids []int64) (map[int64][]Point, error)`

### `(Repository).PersistSnapshot`

PersistSnapshot writes lifecycle samples and derived route costs.

Signature: `func (r Repository) PersistSnapshot(ctx context.Context, offers []Record) error`

### `nullIfEmpty`

This function is part of the `lifecycle` package implementation.

Signature: `func nullIfEmpty(s string) any`

### `nullIfZeroInt`

This function is part of the `lifecycle` package implementation.

Signature: `func nullIfZeroInt(v int) any`

## Key Dependencies

- `context`
- `database/sql`
- `fmt`
- `strings`
- `commons/tradecost`
- `dexunifiedapi/db`

