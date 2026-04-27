# `cmd/snapshot/closed_offer_analysis.go`

Source package: `main`  
Source lines: `291`

## Package Purpose

This file contributes to the `main` package.

## File Role

This file defines 2 type(s), implements 8 function or method entry point(s), declares 1 package value(s).

## Runtime Behavior

- Reads from or writes to persistent storage.

## Types

### `closedOfferDB`

Definition: `interface { Exec(query string, args ...any) (sql.Result, error) Query(query string, args ...any) (*sql.Rows, error) QueryRow(query string, args ...any) *sql.Row }`

### `closedOfferRecord`

Fields:

- `id` `int64`: Stores the id value for the closed offer record.
- `exchange` `string`: Stores the exchange value for the closed offer record.
- `direction` `string`: Stores the direction value for the closed offer record.
- `price` `float64`: Stores the price-related value for the closed offer record.
- `btcMin` `sql.NullFloat64`: Stores a Bitcoin-denominated amount or limit for the closed offer record.
- `btcMax` `sql.NullFloat64`: Stores a Bitcoin-denominated amount or limit for the closed offer record.
- `firstSeen` `string`: Stores timing information for the closed offer record.
- `lastSeen` `string`: Stores timing information for the closed offer record.

## Functions and Methods

### `analyzeClosedOffers`

This function is part of the `main` package implementation.

Signature: `func analyzeClosedOffers(sdb closedOfferDB)`

### `resetIgnoredClosedOffersWithLifecycle`

This function is part of the `main` package implementation.

Signature: `func resetIgnoredClosedOffersWithLifecycle(sdb closedOfferDB)`

### `analyzeSingleClosedOffer`

This function is part of the `main` package implementation.

Signature: `func analyzeSingleClosedOffer(sdb closedOfferDB, offer closedOfferRecord) (string, bool)`

### `historicalRound4`

This function is part of the `main` package implementation.

Signature: `func historicalRound4(v float64) float64`

### `historicalRound8`

This function is part of the `main` package implementation.

Signature: `func historicalRound8(v float64) float64`

### `nullHistoricalRound8`

This function is part of the `main` package implementation.

Signature: `func nullHistoricalRound8(v sql.NullFloat64) any`

### `nullHistoricalString`

This function is part of the `main` package implementation.

Signature: `func nullHistoricalString(v sql.NullString) any`

### `nullHistoricalInt`

This function is part of the `main` package implementation.

Signature: `func nullHistoricalInt(v sql.NullInt64) any`

## Constants and Variables

- `deleteBelowPremiumPct`: No inline declaration comment is present.

## Key Dependencies

- `database/sql`
- `fmt`
- `math`

