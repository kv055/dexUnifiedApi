package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"dexunifiedapi/db"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const bisqAccountProvider = "Bisq"

type accountHealthRecorder interface {
	UpdateAccountHealth(ctx context.Context, provider string, healthy bool) error
}

type accountHealthDB struct {
	db *dbutil.DB
}

func (s accountHealthDB) UpdateAccountHealth(ctx context.Context, provider string, healthy bool) error {
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

type bisqPaymentAccountChecker interface {
	PaymentAccountCount(ctx context.Context) (int, error)
}

type bisqGRPCAccountChecker struct {
	host     string
	password string
	timeout  time.Duration
}

func (c bisqGRPCAccountChecker) PaymentAccountCount(ctx context.Context) (int, error) {
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

func (c bisqAccountCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"password": c.password}, nil
}

func (c bisqAccountCreds) RequireTransportSecurity() bool {
	return false
}

func checkBisqAccountHealth(ctx context.Context, checker bisqPaymentAccountChecker) error {
	count, err := checker.PaymentAccountCount(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("bisq account health: no payment accounts configured")
	}
	return nil
}

func runAccountHealthCheck(
	ctx context.Context,
	recorder accountHealthRecorder,
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

func runPeriodicAccountHealthCheck(
	ctx context.Context,
	interval time.Duration,
	recorder accountHealthRecorder,
	provider string,
	check func(context.Context) error,
) {
	if interval <= 0 {
		interval = time.Minute
	}
	runAccountHealthCheck(ctx, recorder, provider, check)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runAccountHealthCheck(ctx, recorder, provider, check)
		}
	}
}

func parseAccountHealthInterval(raw string) time.Duration {
	if raw == "" {
		return time.Minute
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return time.Minute
	}
	return interval
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
