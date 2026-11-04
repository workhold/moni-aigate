package procurementexecution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type observerLifecycleTx struct {
	fixture                                  *observeAccountTx
	conn                                     *observerLifecycleConn
	settings, queries, rollbacks, commits    int
	settingsErr, queryErr, rollbackErr       error
	settingsPanic, queryPanic, rollbackPanic bool
	settingsCtx, queryCtx, rollbackCtx       context.Context
	rollbackIdle                             bool
	beforeQuery                              func()
}

func (t *observerLifecycleTx) exec(ctx context.Context, sql string) error {
	t.settings++
	t.settingsCtx = ctx
	if sql != runnerSettingsSQL {
		panic("unexpected settings")
	}
	if t.settingsPanic {
		panic("private settings panic")
	}
	return t.settingsErr
}
func (t *observerLifecycleTx) query(ctx context.Context, sql string, args ...any) (Rows, error) {
	t.queries++
	t.queryCtx = ctx
	if t.beforeQuery != nil {
		t.beforeQuery()
	}
	if t.queryPanic {
		panic("private query panic")
	}
	if t.queryErr != nil {
		return nil, t.queryErr
	}
	return t.fixture.query(ctx, sql, args...)
}
func (t *observerLifecycleTx) rollback(ctx context.Context) error {
	t.rollbacks++
	t.rollbackCtx = ctx
	if t.rollbackPanic {
		panic("private rollback panic")
	}
	t.conn.idleValue = t.rollbackIdle
	return t.rollbackErr
}
func (t *observerLifecycleTx) commit(context.Context) error {
	t.commits++
	return errors.New("observer must never commit")
}

type observerLifecycleConn struct {
	tx                         *observerLifecycleTx
	idleValue                  bool
	beginErr, closeErr         error
	beginPanic, releasePanic   bool
	releases, destroys, begins int
	beginCtx, closeCtx         context.Context
	options                    pgx.TxOptions
}

func (c *observerLifecycleConn) begin(ctx context.Context, options pgx.TxOptions) (runnerTx, error) {
	c.begins++
	c.beginCtx = ctx
	c.options = options
	if c.beginPanic {
		panic("private begin panic")
	}
	if c.beginErr != nil {
		return nil, c.beginErr
	}
	c.idleValue = false
	return c.tx, nil
}
func (c *observerLifecycleConn) idle() bool { return c.idleValue }
func (c *observerLifecycleConn) release() {
	c.releases++
	if c.releasePanic {
		panic("private release panic")
	}
}
func (c *observerLifecycleConn) destroy(ctx context.Context) error {
	c.destroys++
	c.closeCtx = ctx
	return c.closeErr
}

type observerLifecyclePool struct {
	conn       *observerLifecycleConn
	calls      int
	err        error
	panicValue bool
	ctx        context.Context
}

func (p *observerLifecyclePool) acquire(ctx context.Context) (runnerConn, error) {
	p.calls++
	p.ctx = ctx
	if p.panicValue {
		panic("private acquire panic")
	}
	if p.err != nil {
		return nil, p.err
	}
	return p.conn, nil
}

func observerLifecycleFixture(t *testing.T) (*Runner, *ObservationHandle, *observerLifecyclePool) {
	t.Helper()
	h, fixture := observeAccountFixture(t, "not_found")
	conn := &observerLifecycleConn{idleValue: true}
	tx := &observerLifecycleTx{fixture: fixture, conn: conn, rollbackIdle: true}
	conn.tx = tx
	pool := &observerLifecyclePool{conn: conn}
	r := &Runner{pool: pool, stamp: &runnerStamp{issued: 1}}
	h.issuer = r
	h.stamp = r.stamp
	h.commitCause = io.EOF
	return r, h, pool
}

func TestObserverFreshReadOnlyTransactionUsesOneBoundedContext(t *testing.T) {
	r, h, p := observerLifecycleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	before := time.Now()
	got, err := r.Observe(ctx, h)
	tx := p.conn.tx
	if err != nil || got != (Observation{State: ReceiptPresent}) || p.calls != 1 || p.conn.begins != 1 || tx.settings != 1 || tx.queries != 7 || tx.rollbacks != 1 || tx.commits != 0 || p.conn.releases != 1 || p.conn.destroys != 0 {
		t.Fatal("unexpected read-only lifecycle", got, err)
	}
	if p.conn.options != (pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}) || p.ctx != p.conn.beginCtx || p.ctx != tx.settingsCtx || p.ctx != tx.queryCtx {
		t.Fatal("observer used different transaction/context")
	}
	deadline, _ := p.ctx.Deadline()
	original, _ := ctx.Deadline()
	cleanup, _ := tx.rollbackCtx.Deadline()
	if deadline.After(original) || deadline.After(before.Add(5*time.Second)) || tx.rollbackCtx == p.ctx || cleanup.After(time.Now().Add(5*time.Second)) || tx.rollbackCtx.Err() == nil {
		t.Fatal("work/cleanup deadline or cancellation changed")
	}
	if h.commitCause != io.EOF || h.account.OperationID == "" {
		t.Fatal("observer modified handle")
	}
}

func TestObserverInvalidMaterialsNeverAcquire(t *testing.T) {
	for _, name := range []string{"nil_context", "nil_handle", "zero_handle", "foreign_runner", "copied_runner", "wrong_schema", "wrong_template", "wrong_args", "missing_identity"} {
		t.Run(name, func(t *testing.T) {
			r, h, p := observerLifecycleFixture(t)
			ctx := context.Background()
			want := ReasonInvalidHandle
			switch name {
			case "nil_context":
				ctx = nil
				want = ReasonInvalidContext
			case "nil_handle":
				h = nil
			case "zero_handle":
				h = &ObservationHandle{}
			case "foreign_runner":
				h.issuer = &Runner{pool: p, stamp: r.stamp}
			case "copied_runner":
				copy := *r
				r = &copy
			case "wrong_schema":
				h.schema = "other"
			case "wrong_template":
				h.invocation.query += "; SELECT 1"
			case "wrong_args":
				h.invocation.args = append([]any(nil), h.invocation.args...)
				h.invocation.args[0] = "other"
			case "missing_identity":
				h.account.RootID = ""
				want = ReasonIdentityUnavailable
			}
			got, err := r.Observe(ctx, h)
			if err == nil || got.State != Unresolved || got.Reason != want || p.calls != 0 {
				t.Fatal("invalid handle reached pool", got, err)
			}
		})
	}
	r, h, p := observerLifecycleFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := r.Observe(ctx, h)
	if got.Reason != ReasonObservationTimeout || !errors.Is(err, context.Canceled) || p.calls != 0 {
		t.Fatal("canceled context acquired")
	}
}

func TestObserverProbeClassificationPrecedesHistoricCommitCause(t *testing.T) {
	for _, tc := range []struct {
		state  string
		reason ObservationReason
	}{
		{"PP902", ReasonHistoryInvariant}, {"PP906", ReasonUnsupportedHistory}, {"57014", ReasonObservationTimeout}, {"42501", ReasonObservationUnavailable}, {"PP901", ReasonObservationUnavailable}, {"PP903", ReasonObservationUnavailable}, {"PP904", ReasonObservationUnavailable}, {"PP905", ReasonObservationUnavailable}, {"23514", ReasonObservationUnavailable},
	} {
		t.Run(tc.state, func(t *testing.T) {
			r, h, p := observerLifecycleFixture(t)
			old := &pgconn.PgError{Code: "PP906", Message: "private old commit"}
			h.commitCause = old
			cause := &pgconn.PgError{Code: tc.state, Message: "private probe"}
			p.conn.tx.queryErr = cause
			got, err := r.Observe(context.Background(), h)
			var first *pgconn.PgError
			if got.State != Unresolved || got.Reason != tc.reason || !errors.As(err, &first) || first != cause || !errors.Is(err, old) || p.calls != 1 || p.conn.tx.commits != 0 {
				t.Fatal("wrong probe classification or lost cause", got, err)
			}
		})
	}
}

func TestObserverCleanupFailuresOverridePresenceAndShareBudget(t *testing.T) {
	for _, name := range []string{"rollback_error", "rollback_dirty", "rollback_panic", "close_error", "release_panic"} {
		t.Run(name, func(t *testing.T) {
			r, h, p := observerLifecycleFixture(t)
			tx := p.conn.tx
			cause := errors.New("private cleanup")
			switch name {
			case "rollback_error":
				tx.rollbackErr = cause
			case "rollback_dirty":
				tx.rollbackIdle = false
			case "rollback_panic":
				tx.rollbackPanic = true
			case "close_error":
				tx.rollbackErr = cause
				p.conn.closeErr = io.ErrUnexpectedEOF
			case "release_panic":
				p.conn.releasePanic = true
			}
			got, err := r.Observe(context.Background(), h)
			if got.State != Unresolved || got.Reason != ReasonObservationCleanupFailed || err == nil || !errors.Is(err, io.EOF) || tx.commits != 0 || p.calls != 1 {
				t.Fatal("cleanup published presence", got, err)
			}
			if name == "release_panic" {
				if p.conn.releases != 1 || p.conn.destroys != 0 {
					t.Fatal("touched transferred connection")
				}
			} else {
				if p.conn.releases != 0 || p.conn.destroys != 1 || p.conn.closeCtx != tx.rollbackCtx {
					t.Fatal("cleanup budget restarted or dirty connection released")
				}
			}
		})
	}
}

func TestObserverAcquireBeginAndPanicPathsNeverRetry(t *testing.T) {
	for _, name := range []string{"acquire", "acquire_panic", "begin_idle", "begin_dirty", "begin_panic", "settings", "settings_panic", "query_panic"} {
		t.Run(name, func(t *testing.T) {
			r, h, p := observerLifecycleFixture(t)
			cause := errors.New("private failure")
			reason := ReasonObservationUnavailable
			switch name {
			case "acquire":
				p.err = cause
			case "acquire_panic":
				p.panicValue = true
				reason = ReasonObservationPanicked
			case "begin_idle":
				p.conn.beginErr = cause
			case "begin_dirty":
				p.conn.beginErr = cause
				p.conn.idleValue = false
			case "begin_panic":
				p.conn.beginPanic = true
				reason = ReasonObservationPanicked
			case "settings":
				p.conn.tx.settingsErr = cause
			case "settings_panic":
				p.conn.tx.settingsPanic = true
				reason = ReasonObservationPanicked
			case "query_panic":
				p.conn.tx.queryPanic = true
				reason = ReasonObservationPanicked
			}
			got, err := r.Observe(context.Background(), h)
			if got.Reason != reason || err == nil || p.calls != 1 || p.conn.tx.commits != 0 || strings.Contains(err.Error(), "private") {
				t.Fatal("failure retried or leaked", got, err)
			}
			if name == "begin_dirty" && p.conn.destroys != 1 {
				t.Fatal("dirty begin connection survived")
			}
		})
	}
}

func TestObserverSafeErrorSerializersAndOnlyTwoResultFields(t *testing.T) {
	_, err := observationUnresolved(ReasonReceiptMismatch, errors.New("private-input-and-sql"), &pgconn.PgError{Code: "PP902", Message: "private-db"})
	var safe *ObservationError
	if !errors.As(err, &safe) {
		t.Fatal("typed observation error missing")
	}
	for _, v := range []any{safe, *safe, map[string]any{"nested": safe}} {
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		out := fmt.Sprintf("%v|%+v|%#v|%s", v, v, v, raw)
		if strings.Contains(out, "private") || strings.Contains(out, "PP902") {
			t.Fatal("serializer exposed cause", out)
		}
	}
	text, _ := safe.MarshalText()
	if string(text) != "procurement_observation_unresolved" || safe.PublicCode() != safe.Error() {
		t.Fatal("unsafe public code")
	}
}
