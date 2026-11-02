package fundinglotruntime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Embedding intentionally makes any unapproved lifecycle operation panic.
// These fakes exercise the private adapter only, never stand in for APP proof.
type adapterTx struct {
	pgx.Tx
	rows  pgx.Rows
	err   error
	calls int
	args  []any
	query func(context.Context)
}

func (x *adapterTx) Query(ctx context.Context, _ string, args ...any) (pgx.Rows, error) {
	x.calls++
	x.args = append([]any(nil), args...)
	if x.query != nil {
		x.query(ctx)
	}
	return x.rows, x.err
}

type adapterRows struct {
	pgx.Rows
	remaining, nexts, scans, closes, errs int
	next                                  func()
	scan                                  func(...any) error
	onErr                                 func()
	close                                 func()
	err                                   error
}

func (x *adapterRows) Next() bool {
	x.nexts++
	if x.next != nil {
		x.next()
	}
	if x.remaining > 0 {
		x.remaining--
		return true
	}
	return false
}
func (x *adapterRows) Scan(d ...any) error {
	x.scans++
	if x.scan != nil {
		return x.scan(d...)
	}
	return nil
}
func (x *adapterRows) Err() error {
	x.errs++
	if x.onErr != nil {
		x.onErr()
	}
	return x.err
}
func (x *adapterRows) Close() {
	x.closes++
	if x.close != nil {
		x.close()
	}
}
func adapterFixture(t *testing.T, d *adapterRows) (*walletQuery, *adapterTx) {
	t.Helper()
	w, e := newVerificationWindow(context.Background(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(w.cancel)
	x := &adapterTx{rows: d}
	return &walletQuery{tx: x, window: w}, x
}
func adapterOpen(t *testing.T, q *walletQuery) *walletRows {
	t.Helper()
	r, e := q.Query("SELECT fixed", int64(0), "", false)
	if e != nil {
		t.Fatal(e)
	}
	return r.(*walletRows)
}
func adapterExpire(q *walletQuery) { q.window.deadline = time.Now().Add(-time.Nanosecond) }
func assertAdapterPoisoned(t *testing.T, q *walletQuery, d *adapterRows, nexts, closes int) {
	t.Helper()
	if !q.failed || q.window.ctx.Err() == nil {
		t.Fatal("failure did not latch and cancel")
	}
	if q.pending == nil {
		t.Fatal("failed pending rows cleared")
	}
	if _, e := q.Query("SELECT fixed"); e != ErrRuntime {
		t.Fatal("poisoned query admitted")
	}
	if d.nexts != nexts || d.closes != closes {
		t.Fatal("adapter additionally drained incomplete rows")
	}
}
func TestWalletAdapterCompleteOwnershipAndEOF(t *testing.T) {
	raw := []byte("original")
	text := "text"
	yes := true
	d := &adapterRows{remaining: 1, scan: func(v ...any) error {
		*v[0].(*bool) = true
		*v[1].(*pgtype.Text) = pgtype.Text{String: text, Valid: true}
		*v[2].(**bool) = &yes
		*v[3].(*[]byte) = raw
		return nil
	}}
	q, x := adapterFixture(t, d)
	r := adapterOpen(t, q)
	if x.calls != 1 || !reflect.DeepEqual(x.args, []any{pgx.QueryExecModeSimpleProtocol, int64(0), "", false}) {
		t.Fatal("fixed owned argument types")
	}
	if more, e := r.Next(); !more || e != nil {
		t.Fatal("row")
	}
	var b bool
	var txt pgtype.Text
	var ptr *bool
	var buf []byte
	if e := r.Scan(&b, &txt, &ptr, &buf); e != nil || !b || !txt.Valid || txt.String != "text" || ptr == nil || !*ptr || string(buf) != "original" {
		t.Fatal("owned projection")
	}
	raw[0] = 'X'
	yes = false
	if string(buf) != "original" || !*ptr {
		t.Fatal("driver buffers escaped")
	}
	if more, e := r.Next(); more || e != nil {
		t.Fatal("EOF")
	}
	if e := r.Err(); e != nil {
		t.Fatal(e)
	}
	if e := r.Close(); e != nil {
		t.Fatal(e)
	}
	if q.pending != nil || q.failed || d.closes != 1 {
		t.Fatal("successful completion did not release once")
	}
	if e := r.Close(); e != nil || d.closes != 1 {
		t.Fatal("idempotent close")
	}
	if _, e := q.Query("SELECT fixed"); e != nil {
		t.Fatal("complete rows retained pending")
	}
}
func TestWalletAdapterFailureNeverAddsDrain(t *testing.T) {
	for _, which := range []string{"early_close", "next_before_scan", "double_scan", "scan_error", "next_error", "err_error", "deadline_query", "deadline_next", "deadline_scan", "deadline_next_err", "deadline_err", "deadline_close", "deadline_close_err", "unsupported_dest", "nil_dest"} {
		t.Run(which, func(t *testing.T) {
			d := &adapterRows{remaining: 2}
			q, x := adapterFixture(t, d)
			if which == "deadline_query" {
				x.query = func(context.Context) { adapterExpire(q) }
				if r, e := q.Query("SELECT fixed"); r != nil || e != ErrRuntime {
					t.Fatal("query deadline admitted")
				}
				assertAdapterPoisoned(t, q, d, 0, 0)
				return
			}
			r := adapterOpen(t, q)
			if which == "early_close" {
				if r.Close() != ErrRuntime {
					t.Fatal("early close accepted")
				}
				assertAdapterPoisoned(t, q, d, 0, 0)
				return
			}
			if which == "deadline_next" {
				d.next = func() { adapterExpire(q) }
			}
			if which == "next_error" {
				d.remaining = 0
				d.err = errors.New("driver-secret")
			}
			if which == "deadline_next_err" {
				d.remaining = 0
				d.onErr = func() { adapterExpire(q) }
			}
			more, e := r.Next()
			if which == "deadline_next" || which == "next_error" || which == "deadline_next_err" {
				if more || e != ErrRuntime {
					t.Fatal("next fault admitted")
				}
				assertAdapterPoisoned(t, q, d, 1, 0)
				return
			}
			if !more || e != nil {
				t.Fatal("row missing")
			}
			switch which {
			case "next_before_scan":
				_, e = r.Next()
			case "scan_error":
				d.scan = func(...any) error { return errors.New("secret") }
				e = r.Scan(new(bool))
			case "deadline_scan":
				d.scan = func(...any) error { adapterExpire(q); return nil }
				e = r.Scan(new(bool))
			case "unsupported_dest":
				e = r.Scan(new(string))
			case "nil_dest":
				var v *bool
				e = r.Scan(v)
			default:
				if r.Scan(new(bool)) != nil {
					t.Fatal("first scan")
				}
				if which == "double_scan" {
					e = r.Scan(new(bool))
					break
				}
				d.remaining = 0
				if more, e = r.Next(); more || e != nil {
					t.Fatal("full EOF")
				}
				if which == "err_error" {
					d.err = errors.New("secret")
				}
				if which == "deadline_err" {
					d.onErr = func() { adapterExpire(q) }
				}
				e = r.Err()
				if which == "err_error" || which == "deadline_err" {
					break
				}
				if e != nil {
					t.Fatal("EOF err")
				}
				if which == "deadline_close" {
					d.close = func() { adapterExpire(q) }
				}
				if which == "deadline_close_err" {
					d.onErr = func() { adapterExpire(q) }
				}
				e = r.Close()
			}
			if e != ErrRuntime {
				t.Fatalf("fault %s admitted", which)
			}
			closes := 0
			if strings.HasPrefix(which, "deadline_close") {
				closes = 1
			}
			assertAdapterPoisoned(t, q, d, d.nexts, closes)
		})
	}
}
func TestWalletAdapterScanPublishesAtomically(t *testing.T) {
	for _, late := range []bool{false, true} {
		d := &adapterRows{remaining: 1}
		q, _ := adapterFixture(t, d)
		r := adapterOpen(t, q)
		d.scan = func(v ...any) error {
			*v[0].(*bool) = true
			*v[1].(*pgtype.Text) = pgtype.Text{String: "driver-secret", Valid: true}
			if late {
				adapterExpire(q)
				return nil
			}
			return errors.New("driver-secret")
		}
		_, _ = r.Next()
		b := false
		s := pgtype.Text{String: "unchanged", Valid: true}
		if r.Scan(&b, &s) != ErrRuntime || b || s.String != "unchanged" {
			t.Fatal("partial or late scan published")
		}
		assertAdapterPoisoned(t, q, d, 1, 0)
	}
}
func TestWalletAdapterArgumentAndZeroBoundaries(t *testing.T) {
	for _, arg := range []any{nil, int(1), uint64(1), []byte("secret"), strings.Repeat("x", (16<<20)+1)} {
		d := &adapterRows{}
		q, x := adapterFixture(t, d)
		if r, e := q.Query("SELECT fixed", arg); r != nil || e != ErrRuntime || x.calls != 0 {
			t.Fatal("unsupported or overbudget argument forwarded")
		}
	}
	d := &adapterRows{}
	q, x := adapterFixture(t, d)
	if _, e := q.Query("SELECT fixed", make([]any, 1025)...); e != ErrRuntime || x.calls != 0 {
		t.Fatal("unbounded args")
	}
	var r *walletRows
	if _, e := r.Next(); e != ErrRuntime {
		t.Fatal("nil Next")
	}
	if r.Scan() != ErrRuntime || r.Err() != ErrRuntime || r.Close() != ErrRuntime {
		t.Fatal("nil rows")
	}
	var nilQ *walletQuery
	if _, e := nilQ.Query("SELECT fixed"); e != ErrRuntime {
		t.Fatal("nil query")
	}
}
func TestRuntimeWindowDeadlineAndFreshChecks(t *testing.T) {
	for _, ctx := range []context.Context{nil, func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c }()} {
		if w, e := newVerificationWindow(ctx, time.Second); w != nil || e != ErrRuntime {
			t.Fatal("invalid context")
		}
	}
	early, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w, e := newVerificationWindow(early, runtimeBudget)
	if e != nil {
		t.Fatal(e)
	}
	defer w.cancel()
	deadline, _ := early.Deadline()
	if !w.deadline.Equal(deadline) {
		t.Fatal("earlier caller deadline extended")
	}
	w.deadline = time.Now().Add(-time.Nanosecond)
	if w.ctx.Err() != nil || w.check() != ErrRuntime {
		t.Fatal("absolute boundary relied solely on timer")
	}
	fresh, e := newVerificationWindow(context.Background(), identityBudget)
	if e != nil {
		t.Fatal("new proof checks must use fresh context")
	}
	fresh.cancel()
}
