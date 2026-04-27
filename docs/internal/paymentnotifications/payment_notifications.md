# `internal/paymentnotifications/payment_notifications.go`

Source package: `paymentnotifications`  
Source lines: `142`

## Package Purpose

This file contributes to the `paymentnotifications` package.

## File Role

This file defines 4 type(s), implements 4 function or method entry point(s), declares 2 package value(s).

## Runtime Behavior

- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.

## Types

### `Type`

Type identifies a type.

Definition: `string`

### `Notification`

Notification represents a notification.

Fields:

- `Amount` `float64`: Amount is the amount value for the notification.
- `Currency` `string`: Currency names the asset or network for the notification.
- `Type` `Type`: Type is the type value for the notification.
- `TransactionID` `string`: TransactionID is the transaction id value for the notification.
- `DEX` `string`: DEX is the dex value for the notification.
- `Connector` `string`: Connector is the connector value for the notification.
- `TransferMessage` `string`: TransferMessage carries a human-readable message for the notification.
- `Metadata` `map[string]any`: Metadata preserves additional source data for the notification.
- `Timestamp` `time.Time`: Timestamp records the relevant time for the notification.

### `StoredNotification`

StoredNotification represents a stored notification.

Fields:

- `ID` `string`: ID identifies the stored notification.
- `ReceivedAt` `time.Time`: ReceivedAt records the relevant time for the stored notification.
- `Event` `Notification`: Event is the event value for the stored notification.

### `Service`

Service represents a service.

Fields:

- `mu` `sync.RWMutex`: Stores the mu value for the service.
- `recent` `[]StoredNotification`: Stores the recent value for the service.
- `limit` `int`: Stores the limit value for the service.

## Functions and Methods

### `NewService`

NewService creates a service.

Signature: `func NewService(limit int) *Service`

### `(*Service).Add`

Add validates and stores a payment notification in the recent-event buffer.

Signature: `func (s *Service) Add(event Notification) (StoredNotification, error)`

### `(*Service).List`

List returns recent records, capped by the requested limit.

Signature: `func (s *Service) List(limit int) []StoredNotification`

### `Validate`

Validate validates the provided value.

Signature: `func Validate(event *Notification) error`

## Constants and Variables

- `Incoming`: Incoming is the incoming value.
- `Outgoing`: Outgoing is the outgoing value.

## Key Dependencies

- `fmt`
- `strings`
- `sync`
- `time`
- `github.com/google/uuid`

