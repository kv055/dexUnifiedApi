package accounthealth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"commons/accounts"
	"dexunifiedapi/db"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// BisqAccountProvider is the bisq account provider value.
const BisqAccountProvider = "Bisq"

// Recorder defines the behavior required by this package.
type Recorder interface {
	UpdateAccountHealth(ctx context.Context, provider string, healthy bool) error
}

// DBRecorder represents a dbrecorder.
type DBRecorder struct {
	db *dbutil.DB
}

// NewDBRecorder creates a dbrecorder.
func NewDBRecorder(db *dbutil.DB) DBRecorder {
	return DBRecorder{db: db}
}

// EnsureAccount creates a provider account row if it does not already exist.
func (s DBRecorder) EnsureAccount(ctx context.Context, provider string) error {
	category := accounts.CategoryForProvider(provider)
	if _, err := s.db.DB.ExecContext(ctx, `
		INSERT INTO accounts (provider, category)
		SELECT $1, NULLIF($2, '')
		WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE provider = $1)
	`, provider, category); err != nil {
		return fmt.Errorf("ensure account %s: %w", provider, err)
	}
	return nil
}

// UpdateAccountHealth records the latest health state for a provider.
func (s DBRecorder) UpdateAccountHealth(ctx context.Context, provider string, healthy bool) error {
	result, err := s.db.DB.ExecContext(ctx, `
		UPDATE accounts
		SET last_health_check = CURRENT_TIMESTAMP,
		    is_healthy = $1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE provider = $2
	`, healthy, provider)
	if err != nil {
		return fmt.Errorf("update account health for %s: %w", provider, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read account health update count for %s: %w", provider, err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Checker defines the behavior required by this package.
type Checker interface {
	Check(ctx context.Context) error
}

// BisqPaymentAccountChecker defines the behavior required by this package.
type BisqPaymentAccountChecker interface {
	PaymentAccountCount(ctx context.Context) (int, error)
}

// BisqGRPCAccountChecker represents a bisq grpcaccount checker.
type BisqGRPCAccountChecker struct {
	host     string
	password string
	timeout  time.Duration
}

// NewBisqGRPCAccountChecker creates a bisq grpcaccount checker.
func NewBisqGRPCAccountChecker(host string, password string, timeout time.Duration) BisqGRPCAccountChecker {
	return BisqGRPCAccountChecker{host: host, password: password, timeout: timeout}
}

// PaymentAccountCount returns the number of Bisq payment accounts visible through gRPC.
func (c BisqGRPCAccountChecker) PaymentAccountCount(ctx context.Context) (int, error) {
	timeout := c.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		c.host,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(bisqAccountCreds{password: c.password}),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithBlock(),
	)
	if err != nil {
		return 0, fmt.Errorf("bisq account health: connect: %w", err)
	}
	defer conn.Close()

	req := newBisqAccountMessage("GetPaymentAccountsRequest")
	resp := newBisqAccountMessage("GetPaymentAccountsReply")
	if err := conn.Invoke(ctx, "/io.bisq.protobuffer.PaymentAccounts/GetPaymentAccounts", req, resp); err != nil {
		return 0, fmt.Errorf("bisq account health: get payment accounts: %w", err)
	}

	field := resp.Descriptor().Fields().ByName("payment_accounts")
	return resp.Get(field).List().Len(), nil
}

type bisqAccountCreds struct {
	password string
}

// GetRequestMetadata retrieves a request metadata.
func (c bisqAccountCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"password": c.password}, nil
}

// RequireTransportSecurity reports whether the credentials require transport security.
func (c bisqAccountCreds) RequireTransportSecurity() bool {
	return false
}

// CheckBisqAccountHealth checks a bisq account health.
func CheckBisqAccountHealth(ctx context.Context, checker BisqPaymentAccountChecker) error {
	count, err := checker.PaymentAccountCount(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("bisq account health: no payment accounts configured")
	}
	return nil
}

// RunAccountHealthCheck runs an account health check.
func RunAccountHealthCheck(
	ctx context.Context,
	recorder Recorder,
	provider string,
	check func(context.Context) error,
) {
	err := check(ctx)
	healthy := err == nil
	if updateErr := recorder.UpdateAccountHealth(ctx, provider, healthy); updateErr != nil {
		log.Printf("[AccountHealth] %s: failed to persist health=%t: %v", provider, healthy, updateErr)
	}
	if err != nil {
		log.Printf("[AccountHealth] %s unhealthy: %v", provider, err)
		return
	}
	log.Printf("[AccountHealth] %s healthy", provider)
}

// RunPeriodicAccountHealthCheck runs a periodic account health check.
func RunPeriodicAccountHealthCheck(
	ctx context.Context,
	interval time.Duration,
	recorder Recorder,
	provider string,
	check func(context.Context) error,
) {
	if interval <= 0 {
		interval = time.Minute
	}
	RunAccountHealthCheck(ctx, recorder, provider, check)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			RunAccountHealthCheck(ctx, recorder, provider, check)
		}
	}
}

// ParseInterval parses an interval.
func ParseInterval(raw string) time.Duration {
	if raw == "" {
		return time.Minute
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return time.Minute
	}
	return interval
}

// Runner represents a runner.
type Runner struct {
	recorder Recorder
	checker  Checker
	provider string
	interval time.Duration
}

// NewRunner creates a runner.
func NewRunner(recorder Recorder, checker Checker, interval time.Duration) *Runner {
	return &Runner{
		recorder: recorder,
		checker:  checker,
		provider: BisqAccountProvider,
		interval: interval,
	}
}

// Start starts the configured worker.
func (r *Runner) Start(ctx context.Context) {
	RunPeriodicAccountHealthCheck(ctx, r.interval, r.recorder, r.provider, r.checker.Check)
}

// CheckFunc identifies a check func.
type CheckFunc func(context.Context) error

// Check checks value.
func (f CheckFunc) Check(ctx context.Context) error {
	return f(ctx)
}

var bisqAccountFileDesc protoreflect.FileDescriptor

func newBisqAccountMessage(name protoreflect.Name) *dynamicpb.Message {
	if bisqAccountFileDesc == nil {
		fd := &descriptorpb.FileDescriptorProto{
			Syntax:  strptr("proto3"),
			Name:    strptr("bisq_account_health.proto"),
			Package: strptr("io.bisq.protobuffer"),
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: strptr("GetPaymentAccountsRequest")},
				{
					Name: strptr("GetPaymentAccountsReply"),
					Field: []*descriptorpb.FieldDescriptorProto{
						{
							Name:     strptr("payment_accounts"),
							Number:   int32ptr(1),
							Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
							Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
							TypeName: strptr(".io.bisq.protobuffer.PaymentAccount"),
						},
					},
				},
				{Name: strptr("PaymentAccount")},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: strptr("PaymentAccounts"),
					Method: []*descriptorpb.MethodDescriptorProto{
						{
							Name:       strptr("GetPaymentAccounts"),
							InputType:  strptr(".io.bisq.protobuffer.GetPaymentAccountsRequest"),
							OutputType: strptr(".io.bisq.protobuffer.GetPaymentAccountsReply"),
						},
					},
				},
			},
		}
		desc, err := protodesc.NewFile(fd, nil)
		if err != nil {
			panic(err)
		}
		bisqAccountFileDesc = desc
	}
	md := bisqAccountFileDesc.Messages().ByName(name)
	if md == nil {
		panic("bisq account health descriptor missing message: " + string(name))
	}
	return dynamicpb.NewMessage(md)
}

func strptr(v string) *string {
	return &v
}

func int32ptr(v int32) *int32 {
	return &v
}
