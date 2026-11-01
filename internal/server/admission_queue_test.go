package server

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// admissionQueueDepth is shared by test-only integration barriers. It includes
// the owner and never exposes production metrics or mutable lane state.
func admissionQueueDepth(q *admissionQueue, key string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if lane := q.lanes[key]; lane != nil {
		return len(lane.tickets)
	}
	return 0
}

func waitAdmissionQueueDepth(t *testing.T, q *admissionQueue, key string, want int) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for admissionQueueDepth(q, key) != want {
		select {
		case <-timer.C:
			t.Fatalf("queue depth=%d want=%d", admissionQueueDepth(q, key), want)
		default:
			runtime.Gosched()
		}
	}
}

type admissionResult struct {
	index   int
	release func()
	err     error
}

func receiveAdmission(t *testing.T, result <-chan admissionResult) admissionResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("admission did not return")
		return admissionResult{}
	}
}

func assertNoAdmissionLanes(t *testing.T, q *admissionQueue) {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.lanes) != 0 {
		t.Fatalf("retained %d empty or occupied lanes", len(q.lanes))
	}
}

func TestAdmissionQueueFIFOAndIndependentKeys(t *testing.T) {
	var q admissionQueue
	owner, err := q.acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.acquire(context.Background(), "b")
	if err != nil || admissionQueueDepth(&q, "b") != 1 {
		t.Fatal("different Key blocked", err)
	}
	other()
	results := make(chan admissionResult, 3)
	for i := range 3 {
		go func(i int) {
			release, err := q.acquire(context.Background(), "a")
			results <- admissionResult{i, release, err}
		}(i)
		waitAdmissionQueueDepth(t, &q, "a", i+2)
	}
	select {
	case got := <-results:
		t.Fatalf("waiter acquired before handoff: %+v", got)
	default:
	}
	owner()
	for i := range 3 {
		got := receiveAdmission(t, results)
		if got.err != nil || got.index != i || got.release == nil {
			t.Fatalf("FIFO handoff=%+v want index=%d", got, i)
		}
		// A stale owner's release cannot remove or re-grant this owner.
		owner()
		if admissionQueueDepth(&q, "a") != 3-i {
			t.Fatal("repeated release changed the current lane")
		}
		got.release()
		got.release()
	}
	assertNoAdmissionLanes(t, &q)
	newOwner, err := q.acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	owner()
	if admissionQueueDepth(&q, "a") != 1 {
		t.Fatal("old release affected a recreated lane")
	}
	newOwner()
	assertNoAdmissionLanes(t, &q)
}

func TestAdmissionQueueCancellationRemovesHeadMiddleAndTail(t *testing.T) {
	for _, cancelled := range []int{0, 1, 2} {
		t.Run(string(rune('0'+cancelled)), func(t *testing.T) {
			var q admissionQueue
			owner, err := q.acquire(context.Background(), "key")
			if err != nil {
				t.Fatal(err)
			}
			results := make(chan admissionResult, 3)
			cancels := make([]context.CancelFunc, 3)
			for i := range 3 {
				ctx, cancel := context.WithCancel(context.Background())
				cancels[i] = cancel
				defer cancel()
				go func(i int) {
					release, err := q.acquire(ctx, "key")
					results <- admissionResult{i, release, err}
				}(i)
				waitAdmissionQueueDepth(t, &q, "key", i+2)
			}
			cancels[cancelled]()
			got := receiveAdmission(t, results)
			if got.index != cancelled || !errors.Is(got.err, context.Canceled) || got.release != nil {
				t.Fatalf("cancelled waiter=%+v", got)
			}
			if admissionQueueDepth(&q, "key") != 3 {
				t.Fatal("cancelled ticket retained")
			}
			owner()
			for i := range 3 {
				if i == cancelled {
					continue
				}
				got := receiveAdmission(t, results)
				if got.index != i || got.err != nil || got.release == nil {
					t.Fatalf("cancelled ticket disrupted FIFO: %+v", got)
				}
				got.release()
			}
			assertNoAdmissionLanes(t, &q)
		})
	}
}

func TestAdmissionQueueLimitAndInvalidInputs(t *testing.T) {
	var q admissionQueue
	if release, err := q.acquire(context.Background(), ""); !errors.Is(err, errAdmissionQueueKey) || release != nil {
		t.Fatal("empty Key accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := q.acquire(ctx, "cancelled"); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatal("cancelled request queued", err)
	}
	expired, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if release, err := q.acquire(expired, "expired"); !errors.Is(err, context.DeadlineExceeded) || release != nil {
		t.Fatal("expired request queued", err)
	}
	assertNoAdmissionLanes(t, &q)
	owner, err := q.acquire(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancelWaiters := context.WithCancel(context.Background())
	results := make(chan admissionResult, admissionQueueLimit-1)
	for i := range admissionQueueLimit - 1 {
		go func(i int) {
			release, err := q.acquire(waitCtx, "key")
			results <- admissionResult{i, release, err}
		}(i)
	}
	waitAdmissionQueueDepth(t, &q, "key", admissionQueueLimit)
	if release, err := q.acquire(context.Background(), "key"); !errors.Is(err, errAdmissionQueueFull) || release != nil || admissionQueueDepth(&q, "key") != admissionQueueLimit {
		t.Fatal("limit did not include the owner", err)
	}
	cancelWaiters()
	for range admissionQueueLimit - 1 {
		got := receiveAdmission(t, results)
		if !errors.Is(got.err, context.Canceled) || got.release != nil {
			t.Fatalf("waiter cancellation=%+v", got)
		}
	}
	owner()
	assertNoAdmissionLanes(t, &q)
}

// The barrier arms only after enqueueing, so it intercepts the final context
// check after grant without relying on sleeps or on the count of earlier calls.
type admissionGrantContext struct {
	context.Context
	armed   atomic.Bool
	once    sync.Once
	atGrant chan struct{}
	proceed chan struct{}
}

func (c *admissionGrantContext) Err() error {
	if c.armed.Load() {
		c.once.Do(func() { close(c.atGrant); <-c.proceed })
	}
	return c.Context.Err()
}

func TestAdmissionQueueCancelledGrantHandsOff(t *testing.T) {
	var q admissionQueue
	owner, err := q.acquire(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &admissionGrantContext{Context: base, atGrant: make(chan struct{}), proceed: make(chan struct{})}
	results := make(chan admissionResult, 2)
	go func() {
		release, err := q.acquire(ctx, "key")
		results <- admissionResult{0, release, err}
	}()
	waitAdmissionQueueDepth(t, &q, "key", 2)
	go func() {
		release, err := q.acquire(context.Background(), "key")
		results <- admissionResult{1, release, err}
	}()
	waitAdmissionQueueDepth(t, &q, "key", 3)
	ctx.armed.Store(true)
	owner()
	select {
	case <-ctx.atGrant:
	case <-time.After(5 * time.Second):
		t.Fatal("grant did not reach the final cancellation check")
	}
	cancel()
	close(ctx.proceed)
	for range 2 {
		got := receiveAdmission(t, results)
		if got.index == 0 {
			if !errors.Is(got.err, context.Canceled) || got.release != nil {
				t.Fatalf("cancelled grant escaped: %+v", got)
			}
		} else {
			if got.err != nil || got.release == nil {
				t.Fatalf("cancelled grant blocked successor: %+v", got)
			}
			got.release()
		}
	}
	assertNoAdmissionLanes(t, &q)
}

func TestAdmissionQueueConcurrentCancelAndHandoff(t *testing.T) {
	var q admissionQueue
	for range 128 {
		owner, err := q.acquire(context.Background(), "key")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		results := make(chan admissionResult, 1)
		go func() {
			release, err := q.acquire(ctx, "key")
			results <- admissionResult{0, release, err}
		}()
		waitAdmissionQueueDepth(t, &q, "key", 2)
		start := make(chan struct{})
		var racers sync.WaitGroup
		racers.Add(2)
		go func() { defer racers.Done(); <-start; owner() }()
		go func() { defer racers.Done(); <-start; cancel() }()
		close(start)
		got := receiveAdmission(t, results)
		if got.err == nil && got.release != nil {
			var releases sync.WaitGroup
			for range 4 {
				releases.Add(1)
				go func() { defer releases.Done(); got.release() }()
			}
			releases.Wait()
		} else if !errors.Is(got.err, context.Canceled) || got.release != nil {
			t.Fatalf("invalid cancellation/grant result: %+v", got)
		}
		racers.Wait()
		assertNoAdmissionLanes(t, &q)
	}
}

func TestAdmissionQueueDeferredReleaseOnPanic(t *testing.T) {
	var q admissionQueue
	func() {
		defer func() { _ = recover() }()
		release, err := q.acquire(context.Background(), "key")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		panic("test admission failure")
	}()
	assertNoAdmissionLanes(t, &q)
}
