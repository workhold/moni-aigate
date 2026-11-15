package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

// A seal has nonzero size: Go may coalesce pointers to zero-sized objects.
type processorSeal struct{ identity byte }
type leaseBinding struct {
	seal       *processorSeal
	id, kind   string
	generation int64
	until      time.Time
}
type MutationLease struct{ binding leaseBinding }
type QueryLease struct{ binding leaseBinding }
type EvidenceLease struct{ binding leaseBinding }
type MutationWork struct {
	binding   leaseBinding
	canonical []byte
	digest    string
}
type QueryWork struct {
	binding                               leaseBinding
	child                                 string
	request                               paymentproto.QueryRequest
	canonical                             []byte
	digest                                string
	createdGeneration, mutationGeneration int64
	createdAt                             time.Time
}
type MutationResponse struct {
	seal          *processorSeal
	binding       leaseBinding
	requestDigest string
	canonical     []byte
	failure       string
}
type transportFailure struct {
	seal        *processorSeal
	binding     leaseBinding
	child, code string
}

type Processor struct {
	pool      *pgxpool.Pool
	runtime   *paymentRuntime
	funding   *billing.Store
	seal      *processorSeal
	mu        sync.Mutex
	closing   bool
	active    sync.WaitGroup
	lifetime  context.Context
	cancel    context.CancelFunc
	closeDone chan struct{}
	ownsPool  bool
}

func (MutationLease) String() string      { return "payments.MutationLease(redacted)" }
func (MutationLease) GoString() string    { return "payments.MutationLease(redacted)" }
func (QueryLease) String() string         { return "payments.QueryLease(redacted)" }
func (QueryLease) GoString() string       { return "payments.QueryLease(redacted)" }
func (EvidenceLease) String() string      { return "payments.EvidenceLease(redacted)" }
func (EvidenceLease) GoString() string    { return "payments.EvidenceLease(redacted)" }
func (MutationWork) String() string       { return "payments.MutationWork(redacted)" }
func (MutationWork) GoString() string     { return "payments.MutationWork(redacted)" }
func (QueryWork) String() string          { return "payments.QueryWork(redacted)" }
func (QueryWork) GoString() string        { return "payments.QueryWork(redacted)" }
func (MutationResponse) String() string   { return "payments.MutationResponse(redacted)" }
func (MutationResponse) GoString() string { return "payments.MutationResponse(redacted)" }
func (*Processor) String() string         { return "payments.Processor(redacted)" }
func (*Processor) GoString() string       { return "payments.Processor(redacted)" }

func NewProcessor(ctx context.Context, pool *pgxpool.Pool, opts Options) (*Processor, error) {
	r, err := newPaymentRuntime(ctx, pool, opts, workerComponent)
	if err != nil {
		return nil, err
	}
	if !r.hasEnvironment() {
		return nil, ErrPaymentsEnvironment
	}
	life, cancel := context.WithCancel(context.Background())
	return &Processor{pool: pool, runtime: r, funding: billing.New(pool), seal: &processorSeal{1}, lifetime: life, cancel: cancel, closeDone: make(chan struct{})}, nil
}

func OpenProcessor(ctx context.Context, opts Options) (*Processor, error) {
	cfg, err := workerPoolConfig(ctx, opts)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, ErrPaymentsUnavailable
	}
	p, err := NewProcessor(ctx, pool, opts)
	if err != nil {
		pool.Close()
		return nil, err
	}
	p.ownsPool = true
	return p, nil
}

func (p *Processor) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if !p.closing {
		p.closing = true
		p.cancel()
		go func() {
			p.active.Wait()
			if p.ownsPool {
				p.pool.Close()
			}
			close(p.closeDone)
		}()
	}
	done := p.closeDone
	p.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Processor) enter(ctx context.Context) (context.Context, func(), error) {
	if p == nil || p.runtime == nil {
		return nil, nil, ErrPaymentsInvalidWork
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		return nil, nil, ErrPaymentsLifecycleClosed
	}
	p.active.Add(1)
	p.mu.Unlock()
	joined, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.lifetime, cancel)
	return joined, func() { stop(); cancel(); p.active.Done() }, nil
}
func (p *Processor) valid(b leaseBinding, kind string) bool {
	return p != nil && b.seal == p.seal && b.id != "" && b.kind == kind && b.generation > 0 && !b.until.IsZero()
}
func (p *Processor) validMutation(b leaseBinding) bool {
	return (b.kind == "checkout" || b.kind == "cancel") && p.valid(b, b.kind)
}
func processorDigest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func processorCanonical(raw, canonical []byte, digest string) bool {
	return len(raw) > 0 && bytes.Equal(raw, canonical) && processorDigest(raw) == digest
}

// afterCommit is used only for a durable stale append or a confirmed no-IO
// terminal transition. Returning it from inside the transaction would undo it.
type processorTxFunc func(context.Context, pgx.Tx) (afterCommit error, err error)

func (p *Processor) transaction(parent context.Context, retry bool, fn processorTxFunc) error {
	ctx, leave, err := p.enter(parent)
	if err != nil {
		return err
	}
	defer leave()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		if err = p.runtime.check(ctx); err != nil {
			return err
		}
		conn, e := p.pool.Acquire(ctx)
		if e != nil {
			return processorError(ctx, e)
		}
		tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if e != nil {
			conn.Release()
			return processorError(ctx, e)
		}
		_, e = tx.Exec(ctx, `SET LOCAL lock_timeout='3s'; SET LOCAL statement_timeout='5s'`)
		var after error
		if e == nil {
			after, e = fn(ctx, tx)
		}
		if e == nil {
			e = p.runtime.check(ctx)
		}
		if e != nil {
			cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
			rb := tx.Rollback(cleanup)
			c()
			if rb != nil && !errors.Is(rb, pgx.ErrTxClosed) {
				raw := conn.Hijack()
				closeCtx, cc := context.WithTimeout(context.Background(), 5*time.Second)
				_ = raw.Close(closeCtx)
				cc()
			} else {
				conn.Release()
			}
			if retry && attempt < 2 && rb == nil && processorRetryable(e) && ctx.Err() == nil {
				continue
			}
			return processorError(ctx, e)
		}
		e = tx.Commit(ctx)
		if e != nil {
			// A server error is a definite failure; a missing COMMIT response is not.
			var pg *pgconn.PgError
			if errors.As(e, &pg) {
				conn.Release()
				return processorError(ctx, e)
			}
			raw := conn.Hijack()
			closeCtx, cc := context.WithTimeout(context.Background(), 5*time.Second)
			_ = raw.Close(closeCtx)
			cc()
			return ErrPaymentsOutcomeUnknown
		}
		conn.Release()
		return after
	}
	return ErrPaymentsUnavailable
}
func processorRetryable(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (pg.Code == "40P01" || pg.Code == "40001")
}
func processorError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{ErrPaymentsInvalidWork, ErrPaymentsLeaseLost, ErrPaymentsRecovery, ErrPaymentsGuard, ErrPaymentsIntegrity, ErrPaymentsUnavailable, ErrPaymentsOutcomeUnknown, ErrPaymentsProtocol, ErrPaymentsQueryConflict, ErrPaymentsLifecycleClosed} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPaymentsIntegrity
	}
	var funding *billing.Error
	if errors.As(err, &funding) && (funding.Code == "funding_integrity" || funding.Code == "invalid_funding_source") {
		return ErrPaymentsIntegrity
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "23514" || pg.Code == "23503" || pg.Code == "23505" || pg.Code == "22003" || pg.Code == "22P02") {
		return ErrPaymentsIntegrity
	}
	return ErrPaymentsUnavailable
}

func processorCurrent(b leaseBinding, state string, generation int64, until *time.Time, now time.Time) bool {
	return state == "leased" && generation == b.generation && until != nil && until.After(now)
}
func processorSendable(b leaseBinding, state string, generation int64, until *time.Time, now time.Time) bool {
	return processorCurrent(b, state, generation, until, now) && until.Sub(now) >= 15*time.Second
}

type processorOutbox struct {
	id, order, kind, state             string
	generation                         int64
	attempts                           int
	until                              *time.Time
	command, original, current, digest *string
	canonical                          []byte
	unknownAt                          *time.Time
}

func processorReadOutbox(ctx context.Context, tx pgx.Tx, id, lock string) (processorOutbox, error) {
	var b processorOutbox
	err := tx.QueryRow(ctx, `SELECT id,order_id,kind,state,generation,attempt_count,lease_until,external_command_id,original_outbox_id,current_query_id,parameter_digest,canonical_parameters,last_submission_unknown_at FROM payment_command_outbox WHERE id=$1 `+lock, id).Scan(&b.id, &b.order, &b.kind, &b.state, &b.generation, &b.attempts, &b.until, &b.command, &b.original, &b.current, &b.digest, &b.canonical, &b.unknownAt)
	return b, err
}

type processorOrder struct {
	id, tenant, environment, merchant, merchantOrder, currency string
	amount                                                     billing.FiatMoney
	credit                                                     billing.Money
	created, expires                                           time.Time
}

func processorLockOrder(ctx context.Context, tx pgx.Tx, id string) (processorOrder, error) {
	var o processorOrder
	err := tx.QueryRow(ctx, `SELECT id,tenant_id,environment_id,merchant_id,merchant_order_id,currency,amount,wallet_credit_amount,created_at,expires_at FROM payment_orders WHERE id=$1 FOR NO KEY UPDATE`, id).Scan(&o.id, &o.tenant, &o.environment, &o.merchant, &o.merchantOrder, &o.currency, &o.amount, &o.credit, &o.created, &o.expires)
	return o, err
}
func (p *Processor) boundMutation(ctx context.Context, tx pgx.Tx, b processorOutbox, o processorOrder) error {
	if b.order != o.id || b.command == nil || b.digest == nil || b.original != nil || b.current != nil || b.generation < 1 {
		return ErrPaymentsIntegrity
	}
	env := p.runtime.environment()
	if o.environment != env.ID || o.merchant != env.MerchantID {
		return ErrPaymentsIntegrity
	}
	switch b.kind {
	case "checkout":
		r, e := paymentproto.ParseCheckout(b.canonical)
		if e != nil {
			return ErrPaymentsIntegrity
		}
		canonical, e := paymentproto.CanonicalCheckout(r)
		if e != nil || !processorCanonical(b.canonical, canonical, *b.digest) || r.EnvironmentID != o.environment || r.MerchantID != o.merchant || r.CommandID != *b.command || r.MerchantOrderID != o.merchantOrder || r.Currency != o.currency || r.Amount != processorInt(int64(o.amount)) || r.CreatedAt != processorTime(o.created) || r.ExpiresAt != processorTime(o.expires) {
			return ErrPaymentsIntegrity
		}
	case "cancel":
		r, e := paymentproto.ParseCancel(b.canonical)
		if e != nil {
			return ErrPaymentsIntegrity
		}
		canonical, e := paymentproto.CanonicalCancel(r)
		if e != nil || !processorCanonical(b.canonical, canonical, *b.digest) || r.EnvironmentID != o.environment || r.MerchantID != o.merchant || r.CommandID != *b.command || r.MerchantOrderID != o.merchantOrder {
			return ErrPaymentsIntegrity
		}
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_cancel_intents WHERE order_id=$1)`, o.id).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return ErrPaymentsIntegrity
		}
	default:
		return ErrPaymentsIntegrity
	}
	return nil
}
