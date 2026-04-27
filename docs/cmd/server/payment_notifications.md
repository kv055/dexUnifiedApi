# `cmd/server/payment_notifications.go`

Source package: `main`  
Source lines: `65`

## Package Purpose

This file contributes to the `main` package.

## File Role

This file implements 2 function or method entry point(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Reads from or writes to persistent storage.
- Serializes or deserializes JSON payloads.

## Functions and Methods

### `handleInternalPaymentNotifications`

This function is part of the `main` package implementation.

Signature: `func handleInternalPaymentNotifications(w http.ResponseWriter, r *http.Request)`

### `handlePaymentNotifications`

This function is part of the `main` package implementation.

Signature: `func handlePaymentNotifications(w http.ResponseWriter, r *http.Request)`

## Key Dependencies

- `encoding/json`
- `fmt`
- `net/http`
- `strconv`
- `dexunifiedapi/internal/paymentnotifications`

