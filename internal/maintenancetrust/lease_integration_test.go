package maintenancetrust

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func realReadLease(t *testing.T, f *snapshotFixture, init bool, ctx context.Context, kind ScanKind) (*Gate, *Snapshot, *Permit, *ReadLease, *gateWire, observedBackend) {
	t.Helper()
	g, s, w, b := authorityInstalled(t, f, init)
	p, err := Attest(context.Background(), s)
	if err != nil || p == nil {
		t.Fatalf("lease genuine authority: %v", err)
	}
	l, err := p.BeginScan(ctx, kind)
	if err != nil || l == nil {
		t.Fatalf("lease genuine BeginScan: %v", err)
	}
	t.Cleanup(func() { _ = l.Abort(); _ = g.Close(nil) })
	return g, s, p, l, w, b
}

func TestReadLeasePostgresPlanInitRowsAndProof(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, init := range []bool{false, true} {
		t.Run(fmt.Sprint(init), func(t *testing.T) {
			g, s, p, l, _, b := realReadLease(t, f, init, context.Background(), ScanLedger)
			if l.Require(ScanLedger) != nil {
				t.Fatal("lease kind")
			}
			var pid uint32
			var xid, access string
			if err := l.QueryRow(`SELECT pg_catalog.pg_backend_pid(),pg_catalog.pg_current_xact_id()::text,pg_catalog.current_setting('transaction_read_only')`).Scan(&pid, &xid, &access); err != nil || pid != b.pid || xid != s.state.xid || (access == "off") != init {
				t.Fatalf("lease changed original transaction: %v", err)
			}
			rows, err := l.Query(`SELECT n::bigint,('row-'||n)::text FROM pg_catalog.generate_series(1,3) n ORDER BY n`)
			if err != nil {
				t.Fatal("lease rows query")
			}
			var got []int64
			for {
				next, e := rows.Next()
				if e != nil {
					t.Fatal("lease Next")
				}
				if !next {
					break
				}
				var n int64
				var text string
				if rows.Scan(&n, &text) != nil || text != fmt.Sprint("row-", n) {
					t.Fatal("lease scalar scan")
				}
				got = append(got, n)
			}
			if !reflect.DeepEqual(got, []int64{1, 2, 3}) {
				t.Fatal("lease lost rows")
			}
			if rows.Close() != nil || rows.Err() != nil {
				t.Fatal("close then Err")
			}
			proof, err := l.Finish()
			if err != nil || proof == nil || proof.Verify(p, ScanLedger) != nil {
				t.Fatalf("lease completion: %v", err)
			}
			copyProof := *proof
			if l.Abort() != nil || copyProof.Verify(p, ScanLedger) != nil {
				t.Fatal("deferred Abort revoked successful proof")
			}
			if another, e := p.BeginScan(context.Background(), ScanLedger); another != nil || e != ErrTransaction {
				t.Fatal("duplicate kind consumed twice")
			}
			next, e := p.BeginScan(context.Background(), ScanPeriodicBudget)
			if e != nil || next == nil || proof.Verify(p, ScanLedger) != nil {
				t.Fatal("proof cannot bind while another legitimate scan busy")
			}
			if next.QueryRow(`SELECT true`).Scan(new(bool)) != nil {
				t.Fatal("second real scan")
			}
			if _, e = next.Finish(); e != nil {
				t.Fatal("second scan completion")
			}
			if proof.Verify(p, ScanPeriodicBudget) == nil || proof.Verify(&Permit{}, ScanLedger) == nil {
				t.Fatal("proof rebound kind or permit")
			}
			if err = s.Rollback(context.Background()); err != nil {
				t.Fatal("lease rollback")
			}
			if proof.Verify(p, ScanLedger) == nil || copyProof.Verify(p, ScanLedger) == nil {
				t.Fatal("terminal proof survived")
			}
			if g.Close(nil) != nil {
				t.Fatal("lease terminal cleanup")
			}
			f.gone(t, b)
		})
	}
}

func TestReadLeasePostgresBusyOwnershipAndOmissions(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, mode := range []string{"rows", "row", "eof-no-err", "eof-no-close", "early-close", "skip-row"} {
		t.Run(mode, func(t *testing.T) {
			g, s, p, l, w, b := realReadLease(t, f, false, context.Background(), ScanLedger)
			before, _ := w.recorded()
			if p.Verify(context.Background()) != ErrGateBusy || s.Commit(context.Background()) != ErrGateBusy || s.Rollback(context.Background()) != ErrGateBusy || g.Close(nil) != ErrGateBusy {
				t.Fatal("lease released scan busy")
			}
			if other, e := p.BeginScan(context.Background(), ScanPeriodicBudget); other != nil || e != ErrGateBusy {
				t.Fatal("second scan not busy")
			}
			after, _ := w.recorded()
			if len(before) != len(after) {
				t.Fatal("busy operation sent SQL")
			}
			if mode == "row" {
				_ = l.QueryRow(`SELECT true`)
			} else {
				rows, e := l.Query(`SELECT n::bigint FROM pg_catalog.generate_series(1,2) n`)
				if e != nil {
					t.Fatal("omission query")
				}
				if other, e := l.Query(`SELECT true`); other != nil || e != ErrGateBusy {
					t.Fatal("second query not busy")
				}
				if mode == "early-close" {
					if rows.Close() == nil {
						t.Fatal("early close accepted")
					}
				} else if mode == "skip-row" {
					if ok, e := rows.Next(); !ok || e != nil {
						t.Fatal("first row")
					}
					if _, e := rows.Next(); e == nil {
						t.Fatal("unconsumed row skipped")
					}
				} else if mode != "rows" {
					for {
						ok, e := rows.Next()
						if e != nil {
							t.Fatal("read rows")
						}
						if !ok {
							break
						}
						var n int64
						if rows.Scan(&n) != nil {
							t.Fatal("scan rows")
						}
					}
					if mode == "eof-no-err" {
						_ = rows.Close()
					} else {
						_ = rows.Err()
					}
				}
			}
			if proof, e := l.Finish(); proof != nil || e == nil || e == ErrGateBusy {
				t.Fatal("omitted handle returned completion")
			}
			f.gone(t, b)
			_ = g.Close(nil)
		})
	}
}

type leaseMaliciousRewriter struct{ calls *atomic.Int32 }

func (r leaseMaliciousRewriter) RewriteQuery(context.Context, *pgx.Conn, string, []any) (string, []any, error) {
	r.calls.Add(1)
	return "SELECT true", nil, nil
}

type leaseMaliciousScanner struct{ calls *atomic.Int32 }

func (s leaseMaliciousScanner) ScanRow(pgx.Rows) error { s.calls.Add(1); return nil }

func TestReadLeasePostgresRejectsRawConnectionCallbacks(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, mode := range []string{"rewriter", "exec-mode", "row-scanner", "borrowed-bytes", "any-target"} {
		t.Run(mode, func(t *testing.T) {
			g, _, _, l, w, b := realReadLease(t, f, false, context.Background(), ScanLedger)
			var calls atomic.Int32
			if mode == "rewriter" || mode == "exec-mode" {
				before, _ := w.recorded()
				var arg any = leaseMaliciousRewriter{&calls}
				if mode == "exec-mode" {
					arg = pgx.QueryExecModeSimpleProtocol
				}
				if rows, e := l.Query(`SELECT true`, arg); rows != nil || e != ErrInput || errors.Unwrap(e) != nil {
					t.Fatal("unsafe query option accepted")
				}
				after, _ := w.recorded()
				if len(before) != len(after) {
					t.Fatal("rejected query sent SQL")
				}
			} else {
				row := l.QueryRow(`SELECT pg_catalog.decode('010203','hex')`)
				var err error
				if mode == "row-scanner" {
					err = row.Scan(leaseMaliciousScanner{&calls})
				} else if mode == "borrowed-bytes" {
					var target pgtype.DriverBytes
					err = row.Scan(&target)
				} else {
					var target any
					err = row.Scan(&target)
				}
				if err != ErrInput || errors.Unwrap(err) != nil {
					t.Fatal("unsafe scan target accepted")
				}
			}
			if calls.Load() != 0 {
				t.Fatal("raw pgx callback invoked")
			}
			f.gone(t, b)
			_ = g.Close(nil)
		})
	}
}

func TestReadLeasePostgresCancellationAndExitRuntime(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, mode := range []string{"cpu-gap", "sql-cancel", "concurrent-abort", "exit-runtime"} {
		t.Run(mode, func(t *testing.T) {
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			g, _, _, l, w, b := realReadLease(t, f, false, caller, ScanLedger)
			if mode == "exit-runtime" {
				c := authorityRuntime(t, f, f.app)
				defer c.Close(context.Background())
				if proof, e := l.Finish(); proof != nil || e != ErrIdentity {
					t.Fatalf("new runtime escaped Finish: %v", e)
				}
			} else if mode == "cpu-gap" {
				cancel()
				f.gone(t, b)
				if l.Err() == nil {
					t.Fatal("CPU gap cancellation not observed")
				}
				if proof, e := l.Finish(); proof != nil || e == nil {
					t.Fatal("cancelled lease completed")
				}
			} else {
				result := make(chan error, 1)
				go func() {
					var ignored string
					result <- l.QueryRow(`SELECT pg_catalog.pg_sleep(1)::text`).Scan(&ignored)
				}()
				ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				for {
					var waiting bool
					if f.admin.QueryRow(ctx, `SELECT wait_event='PgSleep' FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND backend_start=$2`, b.pid, b.start).Scan(&waiting) == nil && waiting {
						break
					}
					if ctx.Err() != nil {
						t.Fatal("lease SQL not live")
					}
					time.Sleep(5 * time.Millisecond)
				}
				if _, e := l.Query(`SELECT true`); e != ErrGateBusy {
					t.Fatal("concurrent lease method not busy")
				}
				if mode == "sql-cancel" {
					cancel()
				} else if l.Abort() != nil {
					t.Fatal("concurrent Abort")
				}
				// Physical client closure is immediate. PostgreSQL may discover a
				// disconnected client only at its next I/O; short bounded server
				// work allows independent natural-exit observation below without
				// claiming raw-close interrupts arbitrary long server CPU work.
				select {
				case <-w.closed:
				case <-time.After(500 * time.Millisecond):
					t.Fatal("lease did not physically close captured socket")
				}
				select {
				case e := <-result:
					if e == nil || errors.Unwrap(e) != nil {
						t.Fatal("blocked SQL did not fail safely")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("original SQL did not leave on raw close")
				}
			}
			f.gone(t, b)
			_ = g.Close(nil)
		})
	}
}
