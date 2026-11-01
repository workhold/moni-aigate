package mailworker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/maildeliver"
)

type fakeRepository struct {
	mu          sync.Mutex
	queue       []identity.MailClaim
	preparation identity.MailPreparation
	results     []identity.MailResult
	limits      []int
	cleanups    int
	err         error
}

func (f *fakeRepository) ClaimMail(_ context.Context, purposes identity.MailPurposes, limit int) ([]identity.MailClaim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limits = append(f.limits, limit)
	n := min(limit, len(f.queue))
	claims := append([]identity.MailClaim(nil), f.queue[:n]...)
	f.queue = f.queue[n:]
	return claims, f.err
}
func (f *fakeRepository) PrepareMail(_ context.Context, claim identity.MailClaim) (identity.MailPreparation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.preparation
	p.Delivery.ID, p.Delivery.Purpose = claim.ID, claim.Purpose
	return p, f.err
}
func (f *fakeRepository) FinishMail(_ context.Context, _ identity.MailClaim, result identity.MailResult) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, result)
	return true, f.err
}
func (f *fakeRepository) CleanupMail(context.Context, int) (identity.MailCleanup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanups++
	return identity.MailCleanup{}, f.err
}

type senderFunc func(context.Context, maildeliver.Message) error

func (f senderFunc) Send(ctx context.Context, m maildeliver.Message) error { return f(ctx, m) }

func workerFixture(t *testing.T, repo *fakeRepository, sender Sender) *Worker {
	t.Helper()
	w, err := New(repo, sender, Options{Origin: "https://portal.fixture.invalid", Enabled: func() identity.MailPurposes { return identity.MailPurposes{Registration: true, PasswordReset: true} }})
	if err != nil {
		t.Fatal(err)
	}
	w.interval, w.cleanup = 5*time.Millisecond, 10*time.Millisecond
	return w
}

func readyPreparation() identity.MailPreparation {
	return identity.MailPreparation{Status: "ready", RemainingLease: time.Minute, Delivery: identity.MailDelivery{To: "customer@fixture.invalid", Link: "https://portal.fixture.invalid/register/complete#token=ic1_" + strings.Repeat("A", 43)}}
}

func TestWorkerPreparationAndSafeSenderFailures(t *testing.T) {
	for _, tc := range []struct {
		name, state, code, outcome, resultCode string
		err                                    error
		remaining                              time.Duration
		panic                                  bool
	}{
		{"accepted", "ready", "", "accepted", "", nil, time.Minute, false},
		{"temporary", "ready", "", "retry", "mail_temporary", &maildeliver.Failure{Code: "mail_temporary", Retryable: true}, time.Minute, false},
		{"permanent", "ready", "", "dead", "mail_rejected", &maildeliver.Failure{Code: "mail_rejected"}, time.Minute, false},
		{"raw error", "ready", "", "retry", "mail_network", errors.New("private-token-raw-smtp"), time.Minute, false},
		{"unknown failure", "ready", "", "dead", "mail_network", &maildeliver.Failure{Code: "private-token-raw-smtp"}, time.Minute, false},
		{"sender panic", "ready", "", "retry", "mail_network", nil, time.Minute, true},
		{"cancelled proof", "cancelled", "", "cancelled", "mail_proof_inactive", nil, 0, false},
		{"missing key", "dead", "mail_decrypt", "dead", "mail_decrypt", nil, 0, false},
		{"invalid payload", "dead", "private-token-raw-smtp", "dead", "mail_payload_invalid", nil, 0, false},
		{"short lease", "lease_short", "", "retry", "mail_network", nil, 0, false},
		{"elapsed lease", "ready", "", "retry", "mail_network", nil, 24 * time.Second, false},
		{"stale generation", "stale", "", "", "", nil, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{preparation: readyPreparation()}
			repo.preparation.Status, repo.preparation.Code, repo.preparation.RemainingLease = tc.state, tc.code, tc.remaining
			var calls atomic.Int64
			sender := senderFunc(func(ctx context.Context, m maildeliver.Message) error {
				calls.Add(1)
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 20*time.Second || m.ID == "" {
					t.Error("SMTP attempt missing bounded context/stable message identity")
				}
				if tc.panic {
					panic("private-token-raw-smtp")
				}
				return tc.err
			})
			w := workerFixture(t, repo, sender)
			w.deliver(context.Background(), identity.MailClaim{ID: uuid.NewString(), Purpose: "registration", Generation: 1, Attempt: 1})
			if tc.outcome == "" {
				if len(repo.results) != 0 || calls.Load() != 0 {
					t.Fatal("stale claim caused a delivery or ACK")
				}
				return
			}
			if len(repo.results) != 1 || repo.results[0].Outcome != tc.outcome || repo.results[0].Code != tc.resultCode || strings.Contains(repo.results[0].Code, "private") {
				t.Fatal("unsafe or incorrect result", repo.results)
			}
			if tc.state != "ready" || tc.remaining < 25*time.Second {
				if calls.Load() != 0 {
					t.Fatal("ineligible proof/lease reached SMTP")
				}
			}
		})
	}
}

func TestWorkerRejectsUntrustedDeliveryBeforeSender(t *testing.T) {
	for _, change := range []func(*identity.MailDelivery){
		func(m *identity.MailDelivery) {
			m.Link = strings.Replace(m.Link, "portal.fixture.invalid", "attacker.invalid", 1)
		},
		func(m *identity.MailDelivery) { m.Link += "&private=token" },
		func(m *identity.MailDelivery) { m.To += "\r\nBcc: attacker@fixture.invalid" },
		func(m *identity.MailDelivery) {
			m.Link = strings.Replace(m.Link, "/register/complete", "/password/reset", 1)
		},
	} {
		repo := &fakeRepository{preparation: readyPreparation()}
		change(&repo.preparation.Delivery)
		var sent bool
		w := workerFixture(t, repo, senderFunc(func(context.Context, maildeliver.Message) error { sent = true; return nil }))
		w.deliver(context.Background(), identity.MailClaim{ID: uuid.NewString(), Purpose: "registration", Generation: 1})
		if sent || len(repo.results) != 1 || repo.results[0].Outcome != "dead" || repo.results[0].Code != "mail_payload_invalid" {
			t.Fatal("untrusted payload reached sender")
		}
	}
}

func TestWorkerUsesOnlyTwoSlotsAndCancellationLeavesLease(t *testing.T) {
	repo := &fakeRepository{preparation: readyPreparation()}
	for range 5 {
		repo.queue = append(repo.queue, identity.MailClaim{ID: uuid.NewString(), Purpose: "registration", Generation: 1, Attempt: 1})
	}
	entered := make(chan struct{}, 2)
	var active, maximum atomic.Int64
	w := workerFixture(t, repo, senderFunc(func(ctx context.Context, _ maildeliver.Message) error {
		n := active.Add(1)
		defer active.Add(-1)
		for current := maximum.Load(); n > current && !maximum.CompareAndSwap(current, n); current = maximum.Load() {
		}
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer cancel()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("two SMTP slots did not start")
		}
	}
	time.Sleep(30 * time.Millisecond)
	repo.mu.Lock()
	queued, claims := len(repo.queue), append([]int(nil), repo.limits...)
	repo.mu.Unlock()
	if queued != 3 || maximum.Load() != 2 || len(claims) != 1 || claims[0] != 2 {
		t.Fatal("worker preclaimed a queue or exceeded SMTP capacity", queued, claims, maximum.Load())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not drain IO")
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.results) != 0 {
		t.Fatal("cancelled SMTP claim was ACKed instead of lease recovery")
	}
}

func TestWorkerGuardAndFeaturePauseCancelActiveIO(t *testing.T) {
	for _, guardFailure := range []bool{false, true} {
		repo := &fakeRepository{preparation: readyPreparation(), queue: []identity.MailClaim{{ID: uuid.NewString(), Purpose: "registration", Generation: 1}}}
		var enabled, guardOK atomic.Bool
		enabled.Store(true)
		guardOK.Store(true)
		entered, cancelled := make(chan struct{}), make(chan struct{})
		w := workerFixture(t, repo, senderFunc(func(ctx context.Context, _ maildeliver.Message) error {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			return ctx.Err()
		}))
		w.options.Enabled = func() identity.MailPurposes { return identity.MailPurposes{Registration: enabled.Load()} }
		w.options.Guard = func(context.Context) error {
			if !guardOK.Load() {
				return errors.New("private-guard-secret")
			}
			return nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("SMTP did not start")
		}
		if guardFailure {
			guardOK.Store(false)
		} else {
			enabled.Store(false)
		}
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("guard/feature pause left IO active")
		}
		if !guardFailure {
			cancel()
		}
		select {
		case err := <-done:
			if guardFailure && !errors.Is(err, ErrGuard) {
				t.Fatal("guard failure did not fence worker", err)
			}
		case <-time.After(time.Second):
			cancel()
			t.Fatal("worker failed to stop")
		}
		cancel()
		repo.mu.Lock()
		if len(repo.results) != 0 {
			t.Error("paused IO changed the pending generation")
		}
		repo.mu.Unlock()
	}
}

func TestCleanupOnlyWorkerAndStrictConstruction(t *testing.T) {
	repo := &fakeRepository{}
	w, err := NewCleanup(repo, nil, nil)
	if err != nil || w.sender != nil || w.options.Origin != "" {
		t.Fatal("cleanup-only construction required invented delivery configuration")
	}
	w.interval, w.cleanup = time.Millisecond, 2*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if w.Run(ctx) != nil {
		t.Fatal("cleanup worker failed")
	}
	if len(repo.limits) != 0 || repo.cleanups == 0 {
		t.Fatal("cleanup-only worker claimed delivery or skipped cleanup")
	}
	sender := senderFunc(func(context.Context, maildeliver.Message) error { return nil })
	for _, origin := range []string{"http://localhost:8080", "https://portal.fixture.invalid#", "https://portal.fixture.invalid/", "https://portal.fixture.invalid?", "https://user@portal.fixture.invalid", "https://portal.fixture.invalid#token=private"} {
		if _, err := New(repo, sender, Options{Origin: origin, Enabled: func() identity.MailPurposes { return identity.MailPurposes{} }}); !errors.Is(err, ErrConfiguration) {
			t.Fatal("noncanonical origin accepted", origin)
		}
	}
	if _, err := NewCleanup(nil, nil, nil); !errors.Is(err, ErrConfiguration) {
		t.Fatal("missing cleanup repository accepted")
	}
}
