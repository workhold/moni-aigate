package payments

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type schedulingProcessor struct {
	mutationClaims, queryClaims, evidenceClaims            atomic.Int32
	networkActive, networkMax, evidenceActive, evidenceMax atomic.Int32
	expirations, records                                   atomic.Int32
	queryEmpty, mutationEmpty                              bool
	claimError, expireError, sendError                     error
	networkStarted, evidenceStarted                        chan struct{}
	release                                                <-chan struct{}
}

func observeConcurrency(active, max *atomic.Int32) {
	n := active.Add(1)
	for old := max.Load(); n > old && !max.CompareAndSwap(old, n); old = max.Load() {
	}
}
func (p *schedulingProcessor) ClaimMutation(context.Context) (MutationLease, bool, error) {
	p.mutationClaims.Add(1)
	return MutationLease{}, !p.mutationEmpty, p.claimError
}
func (p *schedulingProcessor) ClaimQuery(context.Context) (QueryLease, bool, error) {
	p.queryClaims.Add(1)
	return QueryLease{}, !p.queryEmpty, p.claimError
}
func (p *schedulingProcessor) ClaimEvidence(context.Context) (EvidenceLease, bool, error) {
	p.evidenceClaims.Add(1)
	return EvidenceLease{}, true, nil
}
func (*schedulingProcessor) PrepareMutation(context.Context, MutationLease) (MutationWork, error) {
	return MutationWork{}, nil
}
func (*schedulingProcessor) PrepareQuery(context.Context, QueryLease) (QueryWork, error) {
	return QueryWork{}, nil
}
func (p *schedulingProcessor) network(ctx context.Context) error {
	observeConcurrency(&p.networkActive, &p.networkMax)
	defer p.networkActive.Add(-1)
	if p.networkStarted != nil {
		p.networkStarted <- struct{}{}
	}
	if p.release != nil {
		<-p.release
	} else if p.networkStarted != nil {
		<-ctx.Done()
		return ctx.Err()
	}
	return p.sendError
}
func (p *schedulingProcessor) sendMutation(ctx context.Context, _ MutationWork) (MutationResponse, error) {
	return MutationResponse{}, p.network(ctx)
}
func (p *schedulingProcessor) sendQuery(ctx context.Context, _ QueryWork) (paymentproto.SignedHeaders, []byte, *transportFailure, error) {
	return paymentproto.SignedHeaders{}, nil, nil, p.network(ctx)
}
func (p *schedulingProcessor) RecordMutation(context.Context, MutationLease, MutationResponse) error {
	p.records.Add(1)
	return nil
}
func (p *schedulingProcessor) RecordQuery(context.Context, QueryWork, paymentproto.SignedHeaders, []byte) error {
	p.records.Add(1)
	return nil
}
func (p *schedulingProcessor) recordQueryFailure(context.Context, QueryWork, transportFailure) error {
	p.records.Add(1)
	return nil
}
func (p *schedulingProcessor) ProcessEvidence(ctx context.Context, _ EvidenceLease) (ProcessResult, error) {
	observeConcurrency(&p.evidenceActive, &p.evidenceMax)
	defer p.evidenceActive.Add(-1)
	if p.evidenceStarted != nil {
		p.evidenceStarted <- struct{}{}
		<-ctx.Done()
		return ProcessResult{}, ctx.Err()
	}
	return ProcessResult{}, nil
}
func (p *schedulingProcessor) ExpireLeases(context.Context) error {
	p.expirations.Add(1)
	return p.expireError
}
func schedulingWait(t *testing.T, ch <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("slot did not start")
		}
	}
}

func TestRunnerSlotsNeverPrefetchAndCloseWaitsForIO(t *testing.T) {
	release := make(chan struct{})
	p := &schedulingProcessor{networkStarted: make(chan struct{}, 4), evidenceStarted: make(chan struct{}, 8), release: release}
	r := newRunner(p, false)
	r.interval = time.Millisecond
	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	schedulingWait(t, p.networkStarted, 2)
	schedulingWait(t, p.evidenceStarted, 4)
	time.Sleep(15 * time.Millisecond)
	if p.networkMax.Load() != 2 || p.evidenceMax.Load() != 4 || p.queryClaims.Load()+p.mutationClaims.Load() != 2 || p.evidenceClaims.Load() != 4 {
		t.Fatal("slot limit or no-prefetch violated")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := r.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Close returned before bounded IO joined", err)
	}
	select {
	case <-r.Done():
		t.Fatal("Done closed while IO in flight")
	default:
	}
	close(release)
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := r.Close(ctx2); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if p.networkActive.Load() != 0 || p.evidenceActive.Load() != 0 || p.expirations.Load() == 0 || p.records.Load() != 0 {
		t.Fatal("drain incomplete")
	}
	if err := r.Run(context.Background()); !errors.Is(err, ErrPaymentsLifecycleClosed) {
		t.Fatal("Run restarted")
	}
}
func TestRunnerFallbackOnlyOnConfirmedNoDue(t *testing.T) {
	p := &schedulingProcessor{queryEmpty: true}
	r := newRunner(p, false)
	if found, err := r.networkStep(context.Background(), true); err != nil || !found || p.mutationClaims.Load() != 1 || p.records.Load() != 1 {
		t.Fatal("no-due fallback missing", err)
	}
	p = &schedulingProcessor{claimError: ErrPaymentsOutcomeUnknown}
	r = newRunner(p, false)
	if _, err := r.networkStep(context.Background(), true); !errors.Is(err, ErrPaymentsOutcomeUnknown) || p.mutationClaims.Load() != 0 {
		t.Fatal("claim failure fell back", err)
	}
	p = &schedulingProcessor{sendError: ErrPaymentsLeaseLost}
	r = newRunner(p, false)
	if _, err := r.networkStep(context.Background(), false); !errors.Is(err, ErrPaymentsLeaseLost) || p.queryClaims.Load() != 0 || p.records.Load() != 0 {
		t.Fatal("local send failure recorded or fell back", err)
	}
}
func TestRunnerRecoveryOnlyQueriesAndExpiry(t *testing.T) {
	p := &schedulingProcessor{}
	r := newRunner(p, true)
	r.interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	deadline := time.After(time.Second)
	for p.queryClaims.Load() < 4 {
		select {
		case <-deadline:
			t.Fatal("no recovery queries")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if p.mutationClaims.Load() != 0 || p.evidenceClaims.Load() != 0 || p.expirations.Load() == 0 {
		t.Fatal("Recovery started mutations/evidence or omitted expiry")
	}
}
func TestRunnerFatalGuardCancelsPeersAndDoneCloses(t *testing.T) {
	p := &schedulingProcessor{expireError: ErrPaymentsGuard}
	r := newRunner(p, false)
	if err := r.Run(context.Background()); !errors.Is(err, ErrPaymentsGuard) {
		t.Fatal(err)
	}
	select {
	case <-r.Done():
	default:
		t.Fatal("Done missing")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{ErrPaymentsLeaseLost, ErrPaymentsProtocol, ErrPaymentsQueryConflict, ErrPaymentsUnavailable, ErrPaymentsOutcomeUnknown, ErrPaymentsRecovery, context.Canceled, context.DeadlineExceeded} {
		if runnerFatal(err) {
			t.Fatal("recoverable failure fenced Runner", err)
		}
	}
}
func TestRunnerCloseBeforeRunAndFairPreference(t *testing.T) {
	r := newRunner(&schedulingProcessor{}, false)
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background()); !errors.Is(err, ErrPaymentsLifecycleClosed) {
		t.Fatal("closed Runner started")
	}
	p := &schedulingProcessor{}
	r = newRunner(p, false)
	r.interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	deadline := time.After(time.Second)
	for p.queryClaims.Load() < 3 || p.mutationClaims.Load() < 3 {
		select {
		case <-deadline:
			t.Fatal("one network kind starved")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
