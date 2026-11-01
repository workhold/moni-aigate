package payments

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

// This private interface lets scheduling tests exercise real slot and shutdown
// behavior without minting publicly constructible trusted payment responses.
type runnerProcessor interface {
	ClaimMutation(context.Context) (MutationLease, bool, error)
	ClaimQuery(context.Context) (QueryLease, bool, error)
	ClaimEvidence(context.Context) (EvidenceLease, bool, error)
	PrepareMutation(context.Context, MutationLease) (MutationWork, error)
	PrepareQuery(context.Context, QueryLease) (QueryWork, error)
	sendMutation(context.Context, MutationWork) (MutationResponse, error)
	sendQuery(context.Context, QueryWork) (paymentproto.SignedHeaders, []byte, *transportFailure, error)
	RecordMutation(context.Context, MutationLease, MutationResponse) error
	RecordQuery(context.Context, QueryWork, paymentproto.SignedHeaders, []byte) error
	recordQueryFailure(context.Context, QueryWork, transportFailure) error
	ProcessEvidence(context.Context, EvidenceLease) (ProcessResult, error)
	ExpireLeases(context.Context) error
}

type Runner struct {
	processor        runnerProcessor
	recovery         bool
	interval         time.Duration
	mu               sync.Mutex
	started, closing bool
	cancel           context.CancelFunc
	done             chan struct{}
}

func (*Runner) String() string   { return "payments.Runner(redacted)" }
func (*Runner) GoString() string { return "payments.Runner(redacted)" }

func NewRunner(p *Processor) (*Runner, error) {
	if p == nil || p.runtime == nil || !p.runtime.hasEnvironment() {
		return nil, ErrPaymentsConfiguration
	}
	p.mu.Lock()
	closed := p.closing
	p.mu.Unlock()
	if closed {
		return nil, ErrPaymentsLifecycleClosed
	}
	return newRunner(p, p.runtime.recovery()), nil
}
func newRunner(p runnerProcessor, recovery bool) *Runner {
	return &Runner{processor: p, recovery: recovery, interval: time.Second, done: make(chan struct{})}
}
func (r *Runner) Done() <-chan struct{} { return r.done }

// Close joins every slot; it never closes the Processor's pool or fences the
// Service's Guard. A timed-out caller may wait again for the continuing drain.
func (r *Runner) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.closing {
		r.closing = true
		if r.cancel != nil {
			r.cancel()
		}
		if !r.started {
			close(r.done)
		}
	}
	done := r.done
	r.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runnerFatal(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ErrPaymentsGuard), errors.Is(err, ErrPaymentsIntegrity),
		errors.Is(err, ErrPaymentsInvalidWork), errors.Is(err, ErrPaymentsConfiguration),
		errors.Is(err, ErrPaymentsEnvironment), errors.Is(err, ErrPaymentsLifecycleClosed):
		return true
	case errors.Is(err, ErrPaymentsLeaseLost), errors.Is(err, ErrPaymentsRecovery),
		errors.Is(err, ErrPaymentsProtocol), errors.Is(err, ErrPaymentsQueryConflict),
		errors.Is(err, ErrPaymentsUnavailable), errors.Is(err, ErrPaymentsOutcomeUnknown),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	default:
		// An unknown internal error cannot silently stop supervising funds.
		return true
	}
}

// Each fixed slot starts a single operation and then waits a full interval.
// Slow work cannot queue ticks, prefetch leases, or grow physical concurrency.
func (r *Runner) Run(parent context.Context) error {
	if r == nil || r.processor == nil {
		return ErrPaymentsConfiguration
	}
	r.mu.Lock()
	if r.started || r.closing {
		r.mu.Unlock()
		return ErrPaymentsLifecycleClosed
	}
	r.started = true
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.mu.Unlock()
	defer func() { cancel(); r.mu.Lock(); r.closing = true; close(r.done); r.mu.Unlock() }()
	var jobs sync.WaitGroup
	var once sync.Once
	var failure error
	start := func(step func(context.Context) error) {
		jobs.Add(1)
		go func() {
			defer jobs.Done()
			for ctx.Err() == nil {
				err := step(ctx)
				if ctx.Err() != nil {
					return
				}
				if runnerFatal(err) {
					once.Do(func() { failure = err; cancel() })
					return
				}
				timer := time.NewTimer(r.interval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	for slot := 0; slot < 2; slot++ {
		queryFirst := slot == 0 || r.recovery
		start(func(ctx context.Context) error {
			found, err := r.networkStep(ctx, queryFirst)
			if found && !r.recovery {
				queryFirst = !queryFirst
			}
			return err
		})
	}
	if !r.recovery {
		for range 4 {
			start(func(ctx context.Context) error {
				lease, found, err := r.processor.ClaimEvidence(ctx)
				if err != nil || !found {
					return err
				}
				_, err = r.processor.ProcessEvidence(ctx, lease)
				return err
			})
		}
	}
	start(r.processor.ExpireLeases)
	jobs.Wait()
	return failure
}

func (r *Runner) networkStep(ctx context.Context, queryFirst bool) (bool, error) {
	query := func() (bool, error) {
		lease, found, err := r.processor.ClaimQuery(ctx)
		if err != nil || !found {
			return false, err
		}
		work, err := r.processor.PrepareQuery(ctx, lease)
		if err != nil {
			return true, err
		}
		headers, raw, failure, err := r.processor.sendQuery(ctx, work)
		if err != nil {
			return true, err
		}
		if err = ctx.Err(); err != nil {
			return true, err
		}
		if failure != nil {
			return true, r.processor.recordQueryFailure(ctx, work, *failure)
		}
		return true, r.processor.RecordQuery(ctx, work, headers, raw)
	}
	mutation := func() (bool, error) {
		lease, found, err := r.processor.ClaimMutation(ctx)
		if err != nil || !found {
			return false, err
		}
		work, err := r.processor.PrepareMutation(ctx, lease)
		if err != nil {
			return true, err
		}
		response, err := r.processor.sendMutation(ctx, work)
		if err != nil {
			return true, err
		}
		if err = ctx.Err(); err != nil {
			return true, err
		}
		return true, r.processor.RecordMutation(ctx, lease, response)
	}
	if r.recovery {
		return query()
	}
	first, second := mutation, query
	if queryFirst {
		first, second = query, mutation
	}
	found, err := first()
	if found || err != nil {
		return found, err
	}
	return second()
}
