package alertworker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/alerts"
)

type repositoryFunc func(context.Context) (alerts.EvaluationResult, error)

func (f repositoryFunc) EvaluateDue(ctx context.Context) (alerts.EvaluationResult, error) {
	return f(ctx)
}
func testGuard(context.Context) error { return nil }
func TestWorkerRequiresGuardAndRepository(t *testing.T) {
	if _, e := New(nil, Options{Guard: testGuard}); e == nil {
		t.Fatal("nil repository")
	}
	if _, e := New(repositoryFunc(nil), Options{}); e == nil {
		t.Fatal("nil guard")
	}
}
func TestWorkerTwoSlotsCancelJoinWithoutQueuedWork(t *testing.T) {
	entered := make(chan struct{}, 8)
	var active, finished, calls atomic.Int32
	w, e := New(repositoryFunc(func(ctx context.Context) (alerts.EvaluationResult, error) {
		calls.Add(1)
		if active.Add(1) > 2 {
			t.Error("unbounded slots")
		}
		entered <- struct{}{}
		<-ctx.Done()
		active.Add(-1)
		finished.Add(1)
		return alerts.EvaluationResult{}, ctx.Err()
	}), Options{Guard: testGuard})
	if e != nil {
		t.Fatal(e)
	}
	w.interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("missing slot")
		}
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not join")
	}
	if calls.Load() != 2 || finished.Load() != 2 || active.Load() != 0 {
		t.Fatal("leaked or queued evaluation", calls.Load(), finished.Load(), active.Load())
	}
}
func TestWorkerGuardFailureStopsOtherSlotAndNeverExposesError(t *testing.T) {
	var calls atomic.Int32
	codes := make(chan string, 4)
	w, _ := New(repositoryFunc(func(context.Context) (alerts.EvaluationResult, error) {
		calls.Add(1)
		return alerts.EvaluationResult{Outcome: "idle"}, nil
	}), Options{Guard: func(context.Context) error { return errors.New("credential-canary") }, OnError: func(code string) { codes <- code }})
	if !errors.Is(w.Run(context.Background()), ErrGuard) {
		t.Fatal("lost ownership did not stop")
	}
	if calls.Load() != 0 {
		t.Fatal("evaluated without Guard")
	}
	close(codes)
	for code := range codes {
		if code != "alert_guard" {
			t.Fatal("unsafe code", code)
		}
	}
}
func TestWorkerFailedIOBacksOffAndDoesNotStopService(t *testing.T) {
	var calls atomic.Int32
	observed := make(chan struct{}, 8)
	codes := make(chan string, 16)
	w, _ := New(repositoryFunc(func(context.Context) (alerts.EvaluationResult, error) {
		calls.Add(1)
		observed <- struct{}{}
		return alerts.EvaluationResult{}, errors.New("database-password-canary")
	}), Options{Guard: testGuard, OnError: func(code string) { codes <- code }})
	w.initialBackoff = 10 * time.Millisecond
	w.maxBackoff = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	for range 4 {
		select {
		case <-observed:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("no bounded retry")
		}
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	close(codes)
	for code := range codes {
		if code != "evaluation_failed" {
			t.Fatal("unsafe code", code)
		}
	}
	if calls.Load() > 6 {
		t.Fatal("hot retry", calls.Load())
	}
}
