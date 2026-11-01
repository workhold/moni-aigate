package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"golang.org/x/time/rate"
)

const periodicBudgetPrivateFailure = "PRIVATE_PERIODIC_SQL_DSN_CURSOR_BODY_SENTINEL"

func periodicBudgetErrorResponse(t *testing.T, err error, status int, code string) string {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/wallet/periodic-budget", nil)
	c.Set("request_id", "periodic-budget-error-request")
	(&Server{}).periodicBudgetError(c, err)
	return periodicBudgetAssertFailure(t, w, status, code)
}

func periodicBudgetAssertFailure(t *testing.T, w *httptest.ResponseRecorder, status int, code string) string {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP status = %d, want %d", w.Code, status)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	wantRetry := ""
	if status == http.StatusTooManyRequests {
		wantRetry = "1"
	}
	if got := w.Header().Get("Retry-After"); got != wantRetry {
		t.Fatalf("Retry-After = %q, want %q", got, wantRetry)
	}
	if strings.Contains(w.Body.String(), periodicBudgetPrivateFailure) {
		t.Fatal("error response exposed a private failure")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal("error response is not one complete JSON object")
	}
	if len(envelope) != 2 || envelope["error"] == nil || envelope["request_id"] == nil {
		t.Fatal("error response does not have the exact public envelope")
	}
	var requestID string
	if err := json.Unmarshal(envelope["request_id"], &requestID); err != nil || requestID != "periodic-budget-error-request" {
		t.Fatal("error response lost the request ID")
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(envelope["error"], &detail); err != nil || len(detail) != 2 || detail["code"] == nil || detail["message"] == nil {
		t.Fatal("error detail does not have the exact public fields")
	}
	var gotCode, message string
	if err := json.Unmarshal(detail["code"], &gotCode); err != nil || gotCode != code {
		t.Fatalf("safe error code = %q, want %q", gotCode, code)
	}
	if err := json.Unmarshal(detail["message"], &message); err != nil || strings.TrimSpace(message) == "" {
		t.Fatal("error response must have a nonempty safe message")
	}
	return message
}

func periodicBudgetMustAcquire(t *testing.T, a *periodicBudgetAdmission, actor string, kind periodicBudgetAdmissionKind, now time.Time) func() {
	t.Helper()
	release, ok := a.acquire(actor, kind, now)
	if !ok || release == nil {
		t.Fatalf("actor %q kind %d unexpectedly rejected", actor, kind)
	}
	t.Cleanup(release)
	return release
}

func periodicBudgetMustReject(t *testing.T, a *periodicBudgetAdmission, actor string, kind periodicBudgetAdmissionKind, now time.Time) {
	t.Helper()
	if release, ok := a.acquire(actor, kind, now); ok || release != nil {
		if release != nil {
			release()
		}
		t.Fatalf("actor %q kind %d exceeded admission bounds", actor, kind)
	}
}

func periodicBudgetAssertActive(t *testing.T, a *periodicBudgetAdmission, reads, operations, writes int) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reads != reads || a.operations != operations || a.writes != writes {
		t.Fatalf("active read/operation/write = %d/%d/%d, want %d/%d/%d", a.reads, a.operations, a.writes, reads, operations, writes)
	}
}

func TestPeriodicBudgetAdmissionReadLimits(t *testing.T) {
	now := time.Now()
	t.Run("actor_shared_read_and_operation", func(t *testing.T) {
		var a periodicBudgetAdmission
		first := periodicBudgetMustAcquire(t, &a, "actor", periodicBudgetRead, now)
		for range 2 {
			periodicBudgetMustAcquire(t, &a, "actor", periodicBudgetRead, now)
		}
		operation := periodicBudgetMustAcquire(t, &a, "actor", periodicBudgetOperation, now)
		periodicBudgetMustReject(t, &a, "actor", periodicBudgetRead, now)
		periodicBudgetMustReject(t, &a, "actor", periodicBudgetOperation, now)
		periodicBudgetAssertActive(t, &a, 4, 1, 0)
		first()
		// A free shared-read slot does not bypass the one-operation actor limit.
		periodicBudgetMustReject(t, &a, "actor", periodicBudgetOperation, now)
		periodicBudgetMustAcquire(t, &a, "actor", periodicBudgetRead, now)
		operation()
		periodicBudgetMustAcquire(t, &a, "actor", periodicBudgetOperation, now)
		periodicBudgetAssertActive(t, &a, 4, 1, 0)
	})
	t.Run("global_shared_32", func(t *testing.T) {
		var a periodicBudgetAdmission
		var first func()
		for i := range 32 {
			release := periodicBudgetMustAcquire(t, &a, fmt.Sprintf("read-%d", i/4), periodicBudgetRead, now)
			if i == 0 {
				first = release
			}
		}
		periodicBudgetMustReject(t, &a, "extra-read", periodicBudgetRead, now)
		periodicBudgetMustReject(t, &a, "extra-operation", periodicBudgetOperation, now)
		periodicBudgetMustAcquire(t, &a, "read-0", periodicBudgetWrite, now)
		first()
		periodicBudgetMustAcquire(t, &a, "extra-operation", periodicBudgetOperation, now)
		periodicBudgetAssertActive(t, &a, 32, 1, 1)
	})
	t.Run("operation_global_8_within_shared_32", func(t *testing.T) {
		var a periodicBudgetAdmission
		var firstOperation func()
		for i := range 8 {
			release := periodicBudgetMustAcquire(t, &a, fmt.Sprintf("operation-%d", i), periodicBudgetOperation, now)
			if i == 0 {
				firstOperation = release
			}
		}
		periodicBudgetMustReject(t, &a, "ninth-operation", periodicBudgetOperation, now)
		var firstRead func()
		for i := range 24 {
			release := periodicBudgetMustAcquire(t, &a, fmt.Sprintf("read-%d", i/4), periodicBudgetRead, now)
			if i == 0 {
				firstRead = release
			}
		}
		periodicBudgetMustReject(t, &a, "extra-read", periodicBudgetRead, now)
		firstRead()
		periodicBudgetMustReject(t, &a, "ninth-operation", periodicBudgetOperation, now)
		firstOperation()
		periodicBudgetMustAcquire(t, &a, "ninth-operation", periodicBudgetOperation, now)
		periodicBudgetAssertActive(t, &a, 31, 8, 0)
	})
}

func TestPeriodicBudgetAdmissionWritesIndependentAndReleaseOnce(t *testing.T) {
	now := time.Now()
	var a periodicBudgetAdmission
	var first func()
	for i := range 8 {
		release := periodicBudgetMustAcquire(t, &a, fmt.Sprintf("actor-%d", i), periodicBudgetWrite, now)
		if i == 0 {
			first = release
		}
	}
	periodicBudgetMustReject(t, &a, "actor-0", periodicBudgetWrite, now)
	periodicBudgetMustReject(t, &a, "actor-8", periodicBudgetWrite, now)
	periodicBudgetMustAcquire(t, &a, "actor-0", periodicBudgetRead, now)
	periodicBudgetMustAcquire(t, &a, "actor-0", periodicBudgetOperation, now)
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() { defer wait.Done(); first() }()
	}
	wait.Wait()
	periodicBudgetAssertActive(t, &a, 2, 1, 7)
	periodicBudgetMustAcquire(t, &a, "actor-0", periodicBudgetWrite, now)
	periodicBudgetMustReject(t, &a, "actor-0", periodicBudgetWrite, now)
	periodicBudgetAssertActive(t, &a, 2, 1, 8)
}

func TestPeriodicBudgetAdmissionRatesIndependentAndNotRefunded(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   periodicBudgetAdmissionKind
		burst  int
		refill time.Duration
	}{
		{"read", periodicBudgetRead, 10, 200 * time.Millisecond},
		{"operation", periodicBudgetOperation, 4, 500 * time.Millisecond},
		{"write", periodicBudgetWrite, 2, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a periodicBudgetAdmission
			now := time.Now()
			for range tc.burst {
				periodicBudgetMustAcquire(t, &a, "actor", tc.kind, now)()
			}
			periodicBudgetMustReject(t, &a, "actor", tc.kind, now)
			periodicBudgetAssertActive(t, &a, 0, 0, 0)
			for _, other := range []periodicBudgetAdmissionKind{periodicBudgetRead, periodicBudgetOperation, periodicBudgetWrite} {
				if other != tc.kind {
					periodicBudgetMustAcquire(t, &a, "actor", other, now)()
				}
			}
			periodicBudgetMustReject(t, &a, "actor", tc.kind, now.Add(tc.refill-time.Millisecond))
			periodicBudgetMustAcquire(t, &a, "actor", tc.kind, now.Add(tc.refill))()
			periodicBudgetMustReject(t, &a, "actor", tc.kind, now.Add(tc.refill))
			periodicBudgetMustAcquire(t, &a, "another-actor", tc.kind, now)()
		})
	}
}

func periodicBudgetIdleActor(seen time.Time) *periodicBudgetActor {
	return &periodicBudgetActor{read: rate.NewLimiter(5, 10), operation: rate.NewLimiter(2, 4), write: rate.NewLimiter(1, 2), seen: seen}
}

func TestPeriodicBudgetAdmissionBoundedActorsAndIdleBoundary(t *testing.T) {
	now := time.Now()
	a := periodicBudgetAdmission{actors: make(map[string]*periodicBudgetActor)}
	for i := range 4096 {
		a.actors[fmt.Sprintf("actor-%d", i)] = periodicBudgetIdleActor(now.Add(-10*time.Minute + time.Nanosecond))
	}
	periodicBudgetMustReject(t, &a, "new-actor", periodicBudgetRead, now)
	if len(a.actors) != 4096 || a.actors["new-actor"] != nil {
		t.Fatal("actor capacity grew or rejected actor was retained")
	}
	periodicBudgetMustAcquire(t, &a, "actor-0", periodicBudgetRead, now)()
	a.actors["actor-1"].seen = now.Add(-10 * time.Minute)
	periodicBudgetMustAcquire(t, &a, "new-actor", periodicBudgetRead, now)()
	if len(a.actors) != 4096 || a.actors["actor-1"] != nil || a.actors["actor-2"] == nil {
		t.Fatal("idle eviction did not preserve the exact ten-minute boundary")
	}
	for _, kind := range []periodicBudgetAdmissionKind{periodicBudgetRead, periodicBudgetOperation, periodicBudgetWrite} {
		periodicBudgetMustReject(t, &a, "", kind, now)
	}
	periodicBudgetMustReject(t, &a, "actor-0", periodicBudgetAdmissionKind(255), now)
}

func TestPeriodicBudgetAdmissionNeverEvictsActiveActors(t *testing.T) {
	now := time.Now()
	old := now.Add(-10 * time.Minute)
	var a periodicBudgetAdmission
	read := periodicBudgetMustAcquire(t, &a, "active-read", periodicBudgetRead, old)
	periodicBudgetMustAcquire(t, &a, "active-operation", periodicBudgetOperation, old)
	periodicBudgetMustAcquire(t, &a, "active-write", periodicBudgetWrite, old)
	for i := range 4093 {
		a.actors[fmt.Sprintf("idle-%d", i)] = periodicBudgetIdleActor(now)
	}
	periodicBudgetMustReject(t, &a, "new-actor", periodicBudgetRead, now)
	for _, actor := range []string{"active-read", "active-operation", "active-write"} {
		if a.actors[actor] == nil {
			t.Fatal("active actor was evicted")
		}
	}
	read()
	periodicBudgetMustAcquire(t, &a, "new-actor", periodicBudgetRead, now)
	if a.actors["active-read"] != nil || a.actors["active-operation"] == nil || a.actors["active-write"] == nil {
		t.Fatal("eviction did not distinguish released and still-active actors")
	}
	periodicBudgetAssertActive(t, &a, 2, 1, 1)
}

func periodicBudgetUnitContext(ctx context.Context, actor string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/wallet/periodic-budget", nil).WithContext(ctx)
	c.Set("session", &identity.Session{User: identity.User{ID: actor}})
	c.Set("request_id", "periodic-budget-error-request")
	return c, w
}

func TestPeriodicBudgetAdmissionWrapperDeadlinesAndExplicitRelease(t *testing.T) {
	for _, tc := range []struct {
		kind     periodicBudgetAdmissionKind
		duration time.Duration
	}{
		{periodicBudgetRead, 5 * time.Second},
		{periodicBudgetOperation, 5 * time.Second},
		{periodicBudgetWrite, 10 * time.Second},
	} {
		t.Run(fmt.Sprint(tc.kind), func(t *testing.T) {
			s := &Server{}
			s.Ready(true)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := periodicBudgetUnitContext(parent, "actor")
			before := time.Now()
			done, ok := s.admitPeriodicBudget(c, tc.kind)
			if !ok || done == nil {
				t.Fatal("ready request was rejected")
			}
			defer done()
			deadline, present := c.Request.Context().Deadline()
			if !present || deadline.Before(before.Add(tc.duration)) || deadline.After(time.Now().Add(tc.duration)) {
				t.Fatal("B deadline was missing or reset outside its admission budget")
			}
			cancel()
			wantReads, wantOperations, wantWrites := 1, 0, 0
			if tc.kind == periodicBudgetOperation {
				wantOperations = 1
			} else if tc.kind == periodicBudgetWrite {
				wantReads, wantWrites = 0, 1
			}
			periodicBudgetAssertActive(t, &s.periodicAdmission, wantReads, wantOperations, wantWrites)
			done()
			done()
			periodicBudgetAssertActive(t, &s.periodicAdmission, 0, 0, 0)
		})
	}
	t.Run("earlier_parent_deadline_wins", func(t *testing.T) {
		s := &Server{}
		s.Ready(true)
		parent, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c, _ := periodicBudgetUnitContext(parent, "actor")
		done, ok := s.admitPeriodicBudget(c, periodicBudgetWrite)
		if !ok {
			t.Fatal("earlier usable parent deadline was rejected")
		}
		defer done()
		got, _ := c.Request.Context().Deadline()
		want, _ := parent.Deadline()
		if !got.Equal(want) {
			t.Fatal("B wrapper extended the parent deadline")
		}
	})
}

func TestPeriodicBudgetAdmissionWrapperRejectsBeforeGuard(t *testing.T) {
	for _, name := range []string{"cancelled", "short_deadline", "not_ready", "concurrency"} {
		t.Run(name, func(t *testing.T) {
			s := &Server{}
			s.Ready(name != "not_ready")
			var calls atomic.Int32
			s.Guard = func(context.Context) error { calls.Add(1); return nil }
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "cancelled" {
				cancel()
			} else if name == "short_deadline" {
				var shortCancel context.CancelFunc
				parent, shortCancel = context.WithTimeout(parent, 500*time.Millisecond)
				defer shortCancel()
			} else if name == "concurrency" {
				for range 4 {
					periodicBudgetMustAcquire(t, &s.periodicAdmission, "actor", periodicBudgetRead, time.Now())
				}
			}
			c, w := periodicBudgetUnitContext(parent, "actor")
			if done, ok := s.admitPeriodicBudget(c, periodicBudgetRead); ok || done != nil {
				if done != nil {
					done()
				}
				t.Fatal("unavailable request was admitted")
			}
			if calls.Load() != 0 {
				t.Fatal("unavailable request started an ownership Guard")
			}
			if name == "concurrency" {
				periodicBudgetAssertFailure(t, w, 429, "rate_limited")
				periodicBudgetAssertActive(t, &s.periodicAdmission, 4, 0, 0)
			} else {
				if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("unavailable request lost its controlled response")
				}
				periodicBudgetAssertActive(t, &s.periodicAdmission, 0, 0, 0)
			}
			if name != "not_ready" && !s.ready.Load() {
				t.Fatal("request admission failure fenced a ready instance")
			}
		})
	}
}

func TestPeriodicBudgetAdmissionWrapperRechecksGuardAndReady(t *testing.T) {
	for _, name := range []string{"failed_guard", "ready_lost", "parent_cancelled"} {
		t.Run(name, func(t *testing.T) {
			s := &Server{}
			s.Ready(true)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.Guard = func(ctx context.Context) error {
				periodicBudgetAssertActive(t, &s.periodicAdmission, 1, 0, 0)
				deadline, bounded := ctx.Deadline()
				if !bounded || time.Until(deadline) > time.Second || ctx.Err() != nil {
					t.Fatal("fresh Guard did not receive an independent bounded context")
				}
				switch name {
				case "failed_guard":
					return errors.New(periodicBudgetPrivateFailure)
				case "ready_lost":
					s.Ready(false)
				case "parent_cancelled":
					cancel()
					if ctx.Err() != nil {
						t.Fatal("parent cancellation propagated into the ownership Guard")
					}
				}
				return nil
			}
			c, w := periodicBudgetUnitContext(parent, "actor")
			if done, ok := s.admitPeriodicBudget(c, periodicBudgetRead); ok || done != nil {
				if done != nil {
					done()
				}
				t.Fatal("request passed a failed post-Guard gate")
			}
			code := "instance_unavailable"
			if name == "parent_cancelled" {
				code = "periodic_budget_unavailable"
			}
			periodicBudgetAssertFailure(t, w, 503, code)
			periodicBudgetAssertActive(t, &s.periodicAdmission, 0, 0, 0)
			if s.ready.Load() != (name == "parent_cancelled") {
				t.Fatal("ownership status did not distinguish failure from request cancellation")
			}
		})
	}
}

func TestPeriodicBudgetAdmissionCancelledRequestHoldsPermitUntilGuardJoins(t *testing.T) {
	s := &Server{}
	s.Ready(true)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan context.Context, 1)
	resume := make(chan struct{})
	var resumeOnce sync.Once
	t.Cleanup(func() { resumeOnce.Do(func() { close(resume) }) })
	s.Guard = func(ctx context.Context) error {
		entered <- ctx
		select {
		case <-resume:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c, w := periodicBudgetUnitContext(parent, "actor")
	result := make(chan bool, 1)
	go func() {
		done, ok := s.admitPeriodicBudget(c, periodicBudgetRead)
		if done != nil {
			done()
		}
		result <- ok
	}()
	var guardCtx context.Context
	select {
	case guardCtx = <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("fresh Guard did not start")
	}
	cancel()
	periodicBudgetAssertActive(t, &s.periodicAdmission, 1, 0, 0)
	if guardCtx.Err() != nil {
		t.Fatal("request cancellation cancelled the independent Guard")
	}
	select {
	case <-result:
		t.Fatal("admission returned before the active Guard joined")
	default:
	}
	resumeOnce.Do(func() { close(resume) })
	select {
	case ok := <-result:
		if ok {
			t.Fatal("cancelled request was admitted after Guard completion")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Guard completion did not release admission")
	}
	periodicBudgetAssertFailure(t, w, 503, "periodic_budget_unavailable")
	periodicBudgetAssertActive(t, &s.periodicAdmission, 0, 0, 0)
	if !s.ready.Load() {
		t.Fatal("normal request cancellation fenced the instance")
	}
}

func TestPeriodicBudgetAdmissionGuardTimeoutReleasesPermit(t *testing.T) {
	s := &Server{}
	s.Ready(true)
	s.Guard = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	c, w := periodicBudgetUnitContext(context.Background(), "actor")
	result := make(chan bool, 1)
	go func() {
		done, ok := s.admitPeriodicBudget(c, periodicBudgetOperation)
		if done != nil {
			done()
		}
		result <- ok
	}()
	select {
	case ok := <-result:
		if ok {
			t.Fatal("expired ownership Guard admitted the operation lookup")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("independent Guard did not finish within its bounded context")
	}
	periodicBudgetAssertFailure(t, w, 503, "instance_unavailable")
	periodicBudgetAssertActive(t, &s.periodicAdmission, 0, 0, 0)
	if s.ready.Load() {
		t.Fatal("failed ownership Guard did not fence the instance")
	}
}

type periodicBudgetUnitJSON struct{ marshal func() ([]byte, error) }

func (v periodicBudgetUnitJSON) MarshalJSON() ([]byte, error) { return v.marshal() }

func TestPeriodicBudgetJSONBoundedAndAtomic(t *testing.T) {
	const maximum = 128 << 10
	for _, delta := range []int{0, 1} {
		t.Run(fmt.Sprintf("boundary_%d", delta), func(t *testing.T) {
			// A single JSON string adds two quote bytes and the encoder newline.
			value := strings.Repeat("a", maximum-3+delta)
			c, w := periodicBudgetUnitContext(context.Background(), "actor")
			(&Server{}).periodicBudgetJSON(c, http.StatusCreated, value)
			if delta == 0 {
				if w.Code != 201 || w.Body.Len() != maximum || w.Header().Get("Cache-Control") != "no-store" || !json.Valid(w.Body.Bytes()) {
					t.Fatal("exact 128KiB encoded response was rejected or damaged")
				}
			} else {
				periodicBudgetAssertFailure(t, w, 503, "periodic_budget_invariant")
				if strings.Contains(w.Body.String(), strings.Repeat("a", 100)) {
					t.Fatal("oversized response leaked a partial value")
				}
			}
		})
	}
	t.Run("encode_before_headers", func(t *testing.T) {
		c, w := periodicBudgetUnitContext(context.Background(), "actor")
		value := periodicBudgetUnitJSON{marshal: func() ([]byte, error) {
			if c.Writer.Written() || w.Body.Len() != 0 {
				t.Fatal("response was committed before complete encoding")
			}
			return []byte(`{"data":[]}`), nil
		}}
		(&Server{}).periodicBudgetJSON(c, 200, value)
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"data":[]}` || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("complete encoded response was not delivered")
		}
	})
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"unsupported_value", struct {
			Data string   `json:"data"`
			Fail chan int `json:"fail"`
		}{Data: periodicBudgetPrivateFailure, Fail: make(chan int)}},
		{"broken_json", json.RawMessage(`{"data":"` + periodicBudgetPrivateFailure + `"`)},
		{"marshal_failure", periodicBudgetUnitJSON{marshal: func() ([]byte, error) {
			return []byte(`{"data":"` + periodicBudgetPrivateFailure + `"}`), errors.New(periodicBudgetPrivateFailure)
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w := periodicBudgetUnitContext(context.Background(), "actor")
			(&Server{}).periodicBudgetJSON(c, 200, tc.value)
			periodicBudgetAssertFailure(t, w, 503, "periodic_budget_invariant")
		})
	}
	for _, during := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled_during_encoding_%v", during), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !during {
				cancel()
			}
			c, w := periodicBudgetUnitContext(ctx, "actor")
			value := periodicBudgetUnitJSON{marshal: func() ([]byte, error) {
				cancel()
				return []byte(`{"data":"` + periodicBudgetPrivateFailure + `"}`), nil
			}}
			(&Server{}).periodicBudgetJSON(c, 200, value)
			periodicBudgetAssertFailure(t, w, 503, "periodic_budget_unavailable")
		})
	}
}

func TestPeriodicBudgetErrorFixedMappings(t *testing.T) {
	for _, tc := range []struct {
		err    error
		code   string
		status int
	}{
		{periodicbudget.ErrInvalidRequest, "invalid_request", http.StatusBadRequest},
		{periodicbudget.ErrInvalidCursor, "invalid_cursor", http.StatusBadRequest},
		{periodicbudget.ErrRequestTooLarge, "request_too_large", http.StatusRequestEntityTooLarge},
		{periodicbudget.ErrForbidden, "forbidden", http.StatusForbidden},
		{periodicbudget.ErrNotFound, "not_found", http.StatusNotFound},
		{periodicbudget.ErrConflict, "periodic_budget_conflict", http.StatusConflict},
		{periodicbudget.ErrCalendarImmutable, "periodic_calendar_immutable", http.StatusConflict},
		{periodicbudget.ErrMaintenance, "periodic_legacy_import_requires_maintenance", http.StatusConflict},
		{periodicbudget.ErrOperationConflict, "policy_operation_conflict", http.StatusConflict},
		{periodicbudget.ErrRevisionExhausted, "periodic_revision_exhausted", http.StatusConflict},
		{periodicbudget.ErrRateLimited, "rate_limited", http.StatusTooManyRequests},
		{periodicbudget.ErrClockRegression, "periodic_clock_regression", http.StatusServiceUnavailable},
		{periodicbudget.ErrInvariant, "periodic_budget_invariant", http.StatusServiceUnavailable},
		{periodicbudget.ErrOutcomeUnknown, "periodic_budget_outcome_unknown", http.StatusServiceUnavailable},
		{periodicbudget.ErrUnavailable, "periodic_budget_unavailable", http.StatusServiceUnavailable},
	} {
		t.Run(tc.code, func(t *testing.T) {
			message := periodicBudgetErrorResponse(t, tc.err, tc.status, tc.code)
			wrapped := fmt.Errorf("%s: %w", periodicBudgetPrivateFailure, tc.err)
			if got := periodicBudgetErrorResponse(t, wrapped, tc.status, tc.code); got != message {
				t.Fatal("wrapped domain error changed its fixed safe message")
			}
			for _, counterfeit := range []error{
				errors.New(tc.code),
				fmt.Errorf("%s: %w", periodicBudgetPrivateFailure, errors.New(tc.code)),
			} {
				periodicBudgetErrorResponse(t, counterfeit, http.StatusServiceUnavailable, "periodic_budget_unavailable")
			}
		})
	}
}

func TestPeriodicBudgetErrorUnknownFailuresStayUnavailable(t *testing.T) {
	wantMessage := periodicBudgetErrorResponse(t, periodicbudget.ErrUnavailable, http.StatusServiceUnavailable, "periodic_budget_unavailable")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"private_driver_error", errors.New(periodicBudgetPrivateFailure)},
		{"cancelled", context.Canceled},
		{"deadline", context.DeadlineExceeded},
		{"check_violation", &pgconn.PgError{Code: "23514", Message: periodicBudgetPrivateFailure, ConstraintName: "periodic_budget_conflict", Detail: periodicBudgetPrivateFailure}},
		{"unique_violation", &pgconn.PgError{Code: "23505", Message: periodicBudgetPrivateFailure, ConstraintName: "policy_operation_conflict", Detail: periodicBudgetPrivateFailure}},
		{"wrapped_check_violation", fmt.Errorf("%s: %w", periodicBudgetPrivateFailure, &pgconn.PgError{Code: "23514", Message: "periodic_calendar_immutable"})},
		{"non_http_customer_budget_error", periodicbudget.ErrBudgetExceeded},
		{"non_http_procurement_budget_error", periodicbudget.ErrProcurementBudgetExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := periodicBudgetErrorResponse(t, tc.err, http.StatusServiceUnavailable, "periodic_budget_unavailable"); got != wantMessage {
				t.Fatal("unclassified error changed the fixed unavailable message")
			}
		})
	}
}
