package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeriodicCEXFeeRefreshRepeatsAndReportsFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		runPeriodicCEXFeeRefresh(ctx, time.Millisecond, func(context.Context) error {
			calls.Add(1)
			return errors.New("refresh failed")
		}, func(error) {
			if calls.Load() >= 2 {
				cancel()
			}
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic fee refresh did not repeat")
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("refresh calls = %d, want at least 2", got)
	}
}
