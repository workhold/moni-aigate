package maintenancetrust

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
)

func TestFundingLotsObservationInputConsumesOnlyOriginalGate(t *testing.T) {
	for _, kind := range []string{"nil_context", "cancelled", "oversized", "zero_uuid", "uppercase_digest"} {
		t.Run(kind, func(t *testing.T) {
			g := &Gate{state: &gateState{nonce: &gateNonce{}}}
			ctx := context.Background()
			expected := fundingLotsObservationExpected()
			want := ErrInput
			switch kind {
			case "nil_context":
				ctx = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = ErrTimeout
			case "oversized":
				expected.RuntimeProofDigest = strings.Repeat("a", 1<<20)
			case "zero_uuid":
				expected.EpochID = "00000000-0000-0000-0000-000000000000"
			case "uppercase_digest":
				expected.ManifestDigest = strings.Repeat("A", 64)
			}
			result, err := g.ObserveFundingLots(ctx, expected)
			if err != want || result != (FundingLotsObservation{}) || !g.state.beginAttempted || !g.state.closed || g.state.busy || g.state.snapshot != nil {
				t.Fatal("bad observation input did not consume and close its unique gate")
			}
			alias := *g
			if next, err := alias.BeginPlan(context.Background()); next != nil || err != ErrTransaction {
				t.Fatal("failed observation gate became an installation gate")
			}
		})
	}
	for _, g := range []*Gate{nil, {}, {state: &gateState{busy: true}}} {
		result, err := g.ObserveFundingLots(context.Background(), fundingLotsObservationExpected())
		if result != (FundingLotsObservation{}) || err == nil {
			t.Fatal("invalid or busy gate returned an observation")
		}
		if g != nil && g.state != nil && g.state.beginAttempted {
			t.Fatal("busy rejection consumed another owner")
		}
	}
}

func pureObservationRows(t *testing.T, driver *pureLeaseRows) (*fundingLotsObservationRows, *Snapshot) {
	t.Helper()
	s, _, _ := pureSnapshotLifecycle(t)
	s.state.mode = snapshotObserve
	s.state.gate.beginAttempted, s.state.gate.busy = true, true
	run := &fundingLotsObservationRun{snapshot: s.state}
	rows := &fundingLotsObservationRows{run: run, driver: driver}
	run.pending = rows
	return rows, s
}

func TestFundingLotsObservationRowsFaultsNeverComplete(t *testing.T) {
	for _, kind := range []string{"driver_error", "skip_row", "double_scan", "early_close", "cancel_cpu_gap"} {
		t.Run(kind, func(t *testing.T) {
			driver := &pureLeaseRows{count: 1}
			rows, s := pureObservationRows(t, driver)
			var err error
			if kind == "driver_error" {
				driver.count, driver.fail = 0, errors.New("private driver detail")
				_, err = rows.Next()
			} else if kind == "early_close" {
				err = rows.Close()
			} else {
				if more, err := rows.Next(); !more || err != nil {
					t.Fatal("unit row setup")
				}
				var value string
				if kind == "skip_row" {
					_, err = rows.Next()
				} else {
					if rows.Scan(&value) != nil || value != "owned" {
						t.Fatal("unit scan setup")
					}
					if kind == "double_scan" {
						err = rows.Scan(&value)
					} else {
						s.state.cancel()
						_, err = rows.Next()
					}
				}
			}
			if err == nil || strings.Contains(err.Error(), "private") || rows.released || rows.run.pending != rows {
				t.Fatal("row fault became completed evidence or exposed driver details")
			}
			if kind == "early_close" && driver.closes.Load() != 0 {
				t.Fatal("early close tried to drain the original pgx result")
			}
			if err := s.Commit(context.Background()); err != ErrGateBusy {
				t.Fatal("public terminal entered busy observation")
			}
		})
	}
}

func TestFundingLotsObservationRowsRequirePhysicalCompletion(t *testing.T) {
	driver := &pureLeaseRows{count: 1}
	rows, _ := pureObservationRows(t, driver)
	var value []byte
	if more, err := rows.Next(); !more || err != nil || rows.Scan(&value) != nil || len(value) != 2 {
		t.Fatal("owned original bytes")
	}
	if more, err := rows.Next(); more || err != nil || rows.Err() != nil || rows.released {
		t.Fatal("EOF/Err alone released pending evidence")
	}
	if rows.Close() != nil || !rows.released || rows.run.pending != nil || rows.Err() != nil || driver.closes.Load() != 1 {
		t.Fatal("completed observation rows did not close exactly once")
	}
}

// The synthetic startup is only an idle pgx handle for terminal bookkeeping.
// No real PostgreSQL, SQL execution, identity or catalog evidence is involved.
func observationIdleWire(t *testing.T) *pgx.Conn {
	t.Helper()
	client, peer := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer peer.Close()
		backend := pgproto3.NewBackend(peer, peer)
		if _, err := backend.ReceiveStartupMessage(); err != nil {
			return
		}
		backend.Send(&pgproto3.AuthenticationOk{})
		backend.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "16.0"})
		backend.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: 2})
		backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if backend.Flush() != nil {
			return
		}
		// The synthetic transaction below performs no wire SQL. Only closing
		// this handle is permitted after its startup handshake.
		_, _ = backend.Receive()
	}()
	cfg, err := pgx.ParseConfig("host=127.0.0.1 port=1 user=synthetic database=synthetic sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DialFunc = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		_ = client.Close()
		_ = peer.Close()
		<-done
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = peer.Close()
		<-done
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = conn.Close(closeCtx)
	})
	return conn
}

type observationRollbackTx struct {
	pgx.Tx
	rollback func(context.Context) error
}

func (tx *observationRollbackTx) Rollback(ctx context.Context) error { return tx.rollback(ctx) }

type observationHealthyContext struct {
	context.Context
	onErr func()
}

func (ctx observationHealthyContext) Err() error {
	ctx.onErr()
	return nil
}

func TestFundingLotsObservationFinishChecksAbsoluteDeadlineBeforeAndAfterRollback(t *testing.T) {
	for _, at := range []string{"before_rollback", "after_rollback"} {
		t.Run(at, func(t *testing.T) {
			s, _, _ := pureSnapshotLifecycle(t)
			st, gs := s.state, s.state.gate
			st.mode = snapshotObserve
			gs.beginAttempted, gs.busy = true, true
			gs.conn = observationIdleWire(t)
			calls, checks := 0, 0
			st.rawTx = &observationRollbackTx{rollback: func(ctx context.Context) error {
				calls++
				if ctx.Err() != nil || st.ctx.Err() != nil || !gs.idle() {
					t.Fatal("terminal fixture was not healthy and idle")
				}
				if at == "after_rollback" {
					st.deadline = time.Now().Add(-time.Second)
				}
				return nil
			}}
			ctx := observationHealthyContext{Context: context.Background(), onErr: func() {
				checks++
				if at == "before_rollback" && checks == 2 {
					// Model an elapsed absolute budget after the first check
					// while the context's timer callback has not run yet.
					st.deadline = time.Now().Add(-time.Second)
				}
			}}
			run := &fundingLotsObservationRun{snapshot: st}
			if err := run.finish(ctx); err != ErrTimeout || st.terminalDone || st.outcome != OutcomeOpen {
				t.Fatal("elapsed budget published a successful read-only terminal")
			}
			wantCalls := 0
			if at == "after_rollback" {
				wantCalls = 1
			}
			if calls != wantCalls || ctx.Err() != nil || st.ctx.Err() != nil {
				t.Fatal("deadline rejection depended on context cancellation or repeated rollback")
			}
		})
	}
}
