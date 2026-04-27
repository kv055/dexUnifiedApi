# `internal/accounthealth/account_health.go`

Source package: `accounthealth`  
Source lines: `283`

## Package Purpose

This file contributes to the `accounthealth` package.

## File Role

This file defines 8 type(s), implements 16 function or method entry point(s), declares 2 package value(s).

## Runtime Behavior

- Reads from or writes to persistent storage.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.
- Communicates with a gRPC service.

## Types

### `Recorder`

Recorder defines the behavior required by this package.

Definition: `interface { UpdateAccountHealth(ctx context.Context, provider string, healthy bool) error }`

### `DBRecorder`

DBRecorder represents a dbrecorder.

Fields:

- `db` `*dbutil.DB`: Stores the db value for the d b recorder.

### `Checker`

Checker defines the behavior required by this package.

Definition: `interface { Check(ctx context.Context) error }`

### `BisqPaymentAccountChecker`

BisqPaymentAccountChecker defines the behavior required by this package.

Definition: `interface { PaymentAccountCount(ctx context.Context) (int, error) }`

### `BisqGRPCAccountChecker`

BisqGRPCAccountChecker represents a bisq grpcaccount checker.

Fields:

- `host` `string`: Stores the host value for the bisq g r p c account checker.
- `password` `string`: Stores the password value for the bisq g r p c account checker.
- `timeout` `time.Duration`: Stores timing information for the bisq g r p c account checker.

### `bisqAccountCreds`

Fields:

- `password` `string`: Stores the password value for the bisq account creds.

### `Runner`

Runner represents a runner.

Fields:

- `recorder` `Recorder`: Stores the recorder value for the runner.
- `checker` `Checker`: Stores the checker value for the runner.
- `provider` `string`: Stores the provider value for the runner.
- `interval` `time.Duration`: Stores the interval value for the runner.

### `CheckFunc`

CheckFunc identifies a check func.

Definition: `func(context.Context) error`

## Functions and Methods

### `NewDBRecorder`

NewDBRecorder creates a dbrecorder.

Signature: `func NewDBRecorder(db *dbutil.DB) DBRecorder`

### `(DBRecorder).UpdateAccountHealth`

UpdateAccountHealth records the latest health state for a provider.

Signature: `func (s DBRecorder) UpdateAccountHealth(ctx context.Context, provider string, healthy bool) error`

### `NewBisqGRPCAccountChecker`

NewBisqGRPCAccountChecker creates a bisq grpcaccount checker.

Signature: `func NewBisqGRPCAccountChecker(host string, password string, timeout time.Duration) BisqGRPCAccountChecker`

### `(BisqGRPCAccountChecker).PaymentAccountCount`

PaymentAccountCount returns the number of Bisq payment accounts visible through gRPC.

Signature: `func (c BisqGRPCAccountChecker) PaymentAccountCount(ctx context.Context) (int, error)`

### `(bisqAccountCreds).GetRequestMetadata`

GetRequestMetadata retrieves a request metadata.

Signature: `func (c bisqAccountCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error)`

### `(bisqAccountCreds).RequireTransportSecurity`

RequireTransportSecurity reports whether the credentials require transport security.

Signature: `func (c bisqAccountCreds) RequireTransportSecurity() bool`

### `CheckBisqAccountHealth`

CheckBisqAccountHealth checks a bisq account health.

Signature: `func CheckBisqAccountHealth(ctx context.Context, checker BisqPaymentAccountChecker) error`

### `RunAccountHealthCheck`

RunAccountHealthCheck runs an account health check.

Signature: `func RunAccountHealthCheck( ctx context.Context, recorder Recorder, provider string, check func(context.Context) error, )`

### `RunPeriodicAccountHealthCheck`

RunPeriodicAccountHealthCheck runs a periodic account health check.

Signature: `func RunPeriodicAccountHealthCheck( ctx context.Context, interval time.Duration, recorder Recorder, provider string, check func(context.Context) error, )`

### `ParseInterval`

ParseInterval parses an interval.

Signature: `func ParseInterval(raw string) time.Duration`

### `NewRunner`

NewRunner creates a runner.

Signature: `func NewRunner(recorder Recorder, checker Checker, interval time.Duration) *Runner`

### `(*Runner).Start`

Start starts the configured worker.

Signature: `func (r *Runner) Start(ctx context.Context)`

### `(CheckFunc).Check`

Check checks value.

Signature: `func (f CheckFunc) Check(ctx context.Context) error`

### `newBisqAccountMessage`

This function is part of the `accounthealth` package implementation.

Signature: `func newBisqAccountMessage(name protoreflect.Name) *dynamicpb.Message`

### `strptr`

This function is part of the `accounthealth` package implementation.

Signature: `func strptr(v string) *string`

### `int32ptr`

This function is part of the `accounthealth` package implementation.

Signature: `func int32ptr(v int32) *int32`

## Constants and Variables

- `BisqAccountProvider`: BisqAccountProvider is the bisq account provider value.
- `bisqAccountFileDesc`: No inline declaration comment is present.

## Key Dependencies

- `context`
- `database/sql`
- `errors`
- `fmt`
- `log`
- `time`
- `dexunifiedapi/db`
- `google.golang.org/grpc`
- `google.golang.org/grpc/credentials/insecure`
- `google.golang.org/protobuf/reflect/protodesc`
- `google.golang.org/protobuf/reflect/protoreflect`
- `google.golang.org/protobuf/types/descriptorpb`
- `google.golang.org/protobuf/types/dynamicpb`

