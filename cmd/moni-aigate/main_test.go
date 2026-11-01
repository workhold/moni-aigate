package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestMailLifecycleCancellationDoesNotCancelAnInFlightOwnershipCheck(t *testing.T) {
	lifecycle, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	guarded := mailLifecycleGuard(lifecycle, func(ctx context.Context) error { calls.Add(1); close(entered); <-release; return ctx.Err() })
	done := make(chan error, 1)
	go func() { done <- guarded(lifecycle) }()
	<-entered
	cancel()
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("normal mail shutdown was forwarded as ownership failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ownership check did not finish")
	}
	if err := guarded(context.Background()); !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatal("shutdown started new ownership work")
	}
}
func TestMailLifecyclePreservesRealGuardFailuresAndOwnBound(t *testing.T) {
	sentinel := errors.New("ownership failure")
	var calls int
	guarded := mailLifecycleGuard(context.Background(), func(ctx context.Context) error {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Second || ctx.Err() != nil {
			t.Fatal("ownership proof has no independent bound")
		}
		return sentinel
	})
	for i := 0; i < 2; i++ {
		if !errors.Is(guarded(context.Background()), sentinel) {
			t.Fatal("real guard failure suppressed")
		}
	}
	if calls != 2 {
		t.Fatal("guard reused an earlier proof")
	}
}
