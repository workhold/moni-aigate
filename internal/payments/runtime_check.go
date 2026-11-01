package payments

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Only an actual ownership failure fences a runtime. Caller cancellation and a
// caller budget too short to complete the independent check remain local.
func (r *paymentRuntime) check(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if r == nil || !r.options.valid || !r.options.bound || r.options.lifecycle.Guard == nil {
		return ErrPaymentsConfiguration
	}
	if r.fenced.Load() {
		return ErrPaymentsGuard
	}
	if end, ok := ctx.Deadline(); ok && time.Until(end) < 3*time.Second {
		return context.DeadlineExceeded
	}
	guard, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	e := r.options.lifecycle.Guard(guard)
	timeout := guard.Err()
	cancel()
	if e != nil || timeout != nil {
		r.fenced.Store(true)
		return ErrPaymentsGuard
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if r.fenced.Load() {
		return ErrPaymentsGuard
	}
	return nil
}

func newPaymentRuntime(ctx context.Context, pool *pgxpool.Pool, o Options, component runtimeComponent) (*paymentRuntime, error) {
	if !o.valid || !o.bound || o.component != component || o.lifecycle.Guard == nil || pool == nil || (component != applicationComponent && component != workerComponent) || o.disabled && (component != applicationComponent || o.lifecycle.Mode != ModeOff) {
		return nil, ErrPaymentsConfiguration
	}
	if o.lifecycle.Mode != ModeOff && o.lifecycle.Mode != ModeSandbox {
		return nil, ErrPaymentsConfiguration
	}
	if !o.disabled && (o.keyring == nil || runtimeValidateProof(o.proof) != nil) {
		return nil, ErrPaymentsConfiguration
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r := &paymentRuntime{pool: pool, options: o}
	if e := r.check(startup); e != nil {
		return nil, e
	}
	role := operations.PaymentApplicationRole
	if component == workerComponent {
		role = operations.PaymentWorkerRole
	}
	if operations.VerifyPaymentRole(startup, pool, role) != nil {
		return nil, ErrPaymentsEnvironment
	}
	tx, e := pool.BeginTx(startup, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, ErrPaymentsEnvironment
	}
	defer tx.Rollback(startup)
	if migrations.VerifyReadOnlyTx(startup, tx) != nil {
		return nil, ErrPaymentsEnvironment
	}
	if o.disabled {
		e = runtimeVerifyOrdinaryOff(startup, tx)
	} else {
		e = runtimeVerifyCatalog(startup, tx, o, component)
		if e == nil {
			e = runtimeVerifyAttestation(startup, tx, o)
		}
	}
	if e != nil {
		return nil, e
	}
	if e = r.check(startup); e != nil {
		return nil, e
	}
	if tx.Commit(startup) != nil {
		return nil, ErrPaymentsEnvironment
	}
	return r, nil
}

func (r *paymentRuntime) databaseTime(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	if e := r.check(ctx); e != nil {
		return time.Time{}, e
	}
	var now time.Time
	var e error
	if tx == nil {
		if r.pool == nil {
			return time.Time{}, ErrPaymentsEnvironment
		}
		e = r.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	} else {
		e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	}
	if e != nil || now.IsZero() || now.Year() < 1 || now.Year() > 9999 {
		if ctx.Err() != nil {
			return time.Time{}, ctx.Err()
		}
		return time.Time{}, ErrPaymentsUnavailable
	}
	return now.UTC(), nil
}
func (r *paymentRuntime) verifyNotification(now time.Time, h paymentproto.SignedHeaders, raw []byte) (paymentproto.VerifiedNotification, error) {
	if !r.hasEnvironment() || r.options.keyring == nil || r.fenced.Load() {
		return paymentproto.VerifiedNotification{}, ErrPaymentsEnvironment
	}
	return r.options.keyring.VerifyNotification(r.environment(), now, h, raw)
}
func (r *paymentRuntime) verifyQuery(now time.Time, q paymentproto.QueryRequest, h paymentproto.SignedHeaders, raw []byte) (paymentproto.VerifiedQuery, error) {
	if !r.hasEnvironment() || r.options.keyring == nil || r.fenced.Load() {
		return paymentproto.VerifiedQuery{}, ErrPaymentsEnvironment
	}
	return r.options.keyring.VerifyQuery(r.environment(), q, now, h, raw)
}
