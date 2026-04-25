package accounthealth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeBisqPaymentAccountChecker struct {
	count int
	err   error
}

func (f fakeBisqPaymentAccountChecker) PaymentAccountCount(context.Context) (int, error) {
	return f.count, f.err
}

type fakeAccountHealthRecorder struct {
	provider string
	healthy  bool
	err      error
}

func (f *fakeAccountHealthRecorder) UpdateAccountHealth(_ context.Context, provider string, healthy bool) error {
	f.provider = provider
	f.healthy = healthy
	return f.err
}

func TestCheckBisqAccountHealth(t *testing.T) {
	tests := []struct {
		name    string
		checker fakeBisqPaymentAccountChecker
		wantErr bool
	}{
		{name: "healthy with payment account", checker: fakeBisqPaymentAccountChecker{count: 1}},
		{name: "unhealthy with zero payment accounts", checker: fakeBisqPaymentAccountChecker{}, wantErr: true},
		{name: "unhealthy on rpc error", checker: fakeBisqPaymentAccountChecker{err: errors.New("rpc failed")}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckBisqAccountHealth(context.Background(), tc.checker)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

func TestRunAccountHealthCheckPersistsProviderHealth(t *testing.T) {
	recorder := &fakeAccountHealthRecorder{}
	RunAccountHealthCheck(context.Background(), recorder, BisqAccountProvider, func(context.Context) error {
		return nil
	})

	if recorder.provider != BisqAccountProvider {
		t.Fatalf("provider = %q, want %q", recorder.provider, BisqAccountProvider)
	}
	if !recorder.healthy {
		t.Fatal("healthy = false, want true")
	}

	RunAccountHealthCheck(context.Background(), recorder, BisqAccountProvider, func(context.Context) error {
		return errors.New("not ready")
	})
	if recorder.healthy {
		t.Fatal("healthy = true, want false")
	}
}

func TestParseAccountHealthInterval(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{raw: "", want: time.Minute},
		{raw: "15s", want: 15 * time.Second},
		{raw: "0s", want: time.Minute},
		{raw: "nope", want: time.Minute},
	}

	for _, tc := range tests {
		if got := ParseInterval(tc.raw); got != tc.want {
			t.Fatalf("ParseInterval(%q) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}
