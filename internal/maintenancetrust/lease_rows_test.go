package maintenancetrust

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// This fake is only an iteration/ownership oracle. It cannot establish a
// catalog proof, issue a real Permit or make a public BeginScan succeed.
type pureLeaseRows struct {
	count, index         int
	fail                 error
	scans, closes, nexts atomic.Int32
	entered, release     chan struct{}
}

func (r *pureLeaseRows) Close()                                     { r.closes.Add(1) }
func (r *pureLeaseRows) Err() error                                 { return r.fail }
func (*pureLeaseRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*pureLeaseRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *pureLeaseRows) Next() bool {
	r.nexts.Add(1)
	if r.entered != nil {
		close(r.entered)
		<-r.release
		r.entered = nil
	}
	r.index++
	return r.index <= r.count
}
func (r *pureLeaseRows) Scan(dest ...any) error {
	r.scans.Add(1)
	for _, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = "owned"
		case *[]byte:
			*p = []byte{1, 2}
		default:
			return errors.New("private scan failure")
		}
	}
	return nil
}
func (*pureLeaseRows) Values() ([]any, error) { panic("forbidden values") }
func (*pureLeaseRows) RawValues() [][]byte    { panic("forbidden raw values") }
func (*pureLeaseRows) Conn() *pgx.Conn        { panic("forbidden connection") }

func pureReadLease(t *testing.T, driver *pureLeaseRows, one bool) (*ReadLease, *leaseQuery, *Permit) {
	t.Helper()
	s, _, _ := pureSnapshotLifecycle(t)
	st, gs := s.state, s.state.gate
	call, err := st.stageContext(context.Background())
	if err != nil {
		t.Fatal("pure lease call")
	}
	ps := &permitState{snapshot: st, nonce: &permitNonce{}}
	ls := &leaseState{snapshot: st, permit: ps, permitNonce: ps.nonce, snapshotNonce: st.nonce, gateNonce: gs.nonce, nonce: &leaseNonce{}, kind: ScanLedger, call: call}
	q := &leaseQuery{lease: ls, driver: driver, one: one}
	ls.query = q
	gs.mu.Lock()
	gs.busy = true
	st.permit = ps
	st.permitCall = call
	st.activeLease = ls
	gs.mu.Unlock()
	l := &ReadLease{state: ls}
	t.Cleanup(func() { _ = l.Abort() })
	return l, q, &Permit{state: ps}
}

func TestReadLeaseRowsRequireEOFErrCloseInBothOrders(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		t.Run(map[bool]string{true: "close-first", false: "err-first"}[closeFirst], func(t *testing.T) {
			driver := &pureLeaseRows{count: 1}
			l, q, _ := pureReadLease(t, driver, false)
			r := &Rows{state: q}
			if ok, err := r.Next(); !ok || err != nil {
				t.Fatal("first row")
			}
			var s string
			if r.Scan(&s) != nil || s != "owned" {
				t.Fatal("scan")
			}
			if ok, err := r.Next(); ok || err != nil {
				t.Fatal("real EOF")
			}
			if closeFirst {
				if r.Close() != nil {
					t.Fatal("Close")
				}
			} else {
				if r.Err() != nil {
					t.Fatal("Err")
				}
			}
			if _, err := l.Query("SELECT 1"); err != ErrGateBusy {
				t.Fatal("query released before three facts")
			}
			if closeFirst {
				if r.Err() != nil {
					t.Fatal("Err")
				}
			} else {
				if r.Close() != nil {
					t.Fatal("Close")
				}
			}
			if !q.released || q.lease.query != nil || r.Close() != nil || r.Err() != nil || driver.closes.Load() != 1 {
				t.Fatal("full EOF cleanup or idempotency")
			}
		})
	}
}

func TestReadLeaseRowsProtocolMistakesFailClosed(t *testing.T) {
	for _, mode := range []string{"scan-without-next", "next-without-scan", "double-scan", "early-close", "finish-pending"} {
		t.Run(mode, func(t *testing.T) {
			driver := &pureLeaseRows{count: 2}
			l, q, _ := pureReadLease(t, driver, false)
			r := &Rows{state: q}
			var s string
			var err error
			switch mode {
			case "scan-without-next":
				err = r.Scan(&s)
			case "next-without-scan":
				_, _ = r.Next()
				_, err = r.Next()
			case "double-scan":
				_, _ = r.Next()
				_ = r.Scan(&s)
				err = r.Scan(&s)
			case "early-close":
				err = r.Close()
			case "finish-pending":
				_, err = l.Finish()
			}
			if err != ErrTransaction || !q.lease.revoked || !q.lease.released || q.lease.snapshot.gate.busy {
				t.Fatal("protocol error retained ownership")
			}
			if r.Close() != nil || driver.closes.Load() != 0 {
				t.Fatal("failure drain touched driver")
			}
		})
	}
}

func TestReadLeaseRowsConcurrentAbortRetainsOriginalMethodOwner(t *testing.T) {
	driver := &pureLeaseRows{count: 1, entered: make(chan struct{}), release: make(chan struct{})}
	l, q, _ := pureReadLease(t, driver, false)
	r := &Rows{state: q}
	done := make(chan error, 1)
	go func() { _, err := r.Next(); done <- err }()
	<-driver.entered
	if _, err := r.Next(); err != ErrGateBusy {
		t.Fatal("concurrent Next did not reject busy")
	}
	if err := r.Close(); err != ErrGateBusy {
		t.Fatal("concurrent Close did not reject busy")
	}
	if _, err := l.Finish(); err != ErrGateBusy {
		t.Fatal("concurrent Finish did not reject busy")
	}
	if l.Abort() != nil {
		t.Fatal("Abort")
	}
	gs := q.lease.snapshot.gate
	gs.mu.Lock()
	held := gs.busy && q.lease.opBusy && !q.lease.released
	gs.mu.Unlock()
	if !held || driver.closes.Load() != 0 {
		t.Fatal("Abort stole in-flight pgx ownership")
	}
	close(driver.release)
	if err := <-done; err != ErrTransaction {
		t.Fatal("original owner returned data after Abort")
	}
	gs.mu.Lock()
	released := !gs.busy && !q.lease.opBusy && q.lease.released
	gs.mu.Unlock()
	if !released {
		t.Fatal("original method did not release after unwind")
	}
}

func TestReadLeaseRowExactlyOneAndSafeFailures(t *testing.T) {
	for _, n := range []int{0, 1, 2} {
		t.Run(string(rune('0'+n)), func(t *testing.T) {
			driver := &pureLeaseRows{count: n}
			_, q, _ := pureReadLease(t, driver, true)
			r := &Row{state: q}
			s := "unchanged"
			err := r.Scan(&s)
			if n == 1 {
				if err != nil || s != "owned" || !q.released || q.lease.query != nil || driver.closes.Load() != 1 {
					t.Fatal("one row not fully consumed")
				}
			} else {
				if err != ErrExecution || s != "unchanged" || !q.lease.revoked || errors.Unwrap(err) != nil {
					t.Fatal("non-singleton leaked partial result")
				}
			}
		})
	}
	driver := &pureLeaseRows{fail: errors.New("row data or DSN must not escape")}
	_, q, _ := pureReadLease(t, driver, false)
	if _, err := (&Rows{state: q}).Next(); err != ErrExecution {
		t.Fatal("unsafe Rows.Err")
	}
}

func TestReadLeaseCancellationViewAndProofBindingArePure(t *testing.T) {
	driver := &pureLeaseRows{}
	l, _, p := pureReadLease(t, driver, false)
	ls := l.state
	if _, ok := l.Deadline(); !ok || l.Done() == nil || l.Err() != nil {
		t.Fatal("cancellation view")
	}
	// Simulate only the private immutable completion state, not a public proof
	// issuer. Real Finish requires authority.verifyEvidence and actual PG.
	gs := ls.snapshot.gate
	gs.mu.Lock()
	ls.query = nil
	ls.finished = true
	ls.proof = &scanProofState{lease: ls, nonce: &scanProofNonce{}}
	clean := ls.claimCleanupLocked()
	gs.mu.Unlock()
	if !clean {
		t.Fatal("pure completion cleanup")
	}
	ls.closeCall()
	proof := &ScanProof{state: ls.proof}
	copyP := *proof
	if l.Err() != ErrTransaction || l.Abort() != nil || proof.Verify(p, ScanLedger) != nil || copyP.Verify(p, ScanLedger) != nil {
		t.Fatal("normal call cancellation revoked completed proof")
	}
	gs.mu.Lock()
	gs.busy = true
	gs.mu.Unlock()
	if proof.Verify(p, ScanLedger) != nil {
		t.Fatal("pure proof check tried SQL while busy")
	}
	gs.mu.Lock()
	gs.busy = false
	gs.mu.Unlock()
	if proof.Verify(p, ScanFunding) != ErrTransaction || proof.Verify(&Permit{state: &permitState{}}, ScanLedger) != ErrTransaction {
		t.Fatal("proof rebound")
	}
	gs.mu.Lock()
	ls.snapshot.invalidateLocked(ErrSchema)
	gs.mu.Unlock()
	if proof.Verify(p, ScanLedger) != ErrTransaction || copyP.Verify(p, ScanLedger) != ErrTransaction {
		t.Fatal("shared revocation missing")
	}
	if reflect.TypeOf(l).NumMethod() == 0 || driver.nexts.Load() != 0 {
		t.Fatal("binding check touched driver")
	}
	select {
	case <-l.Done():
	case <-time.After(time.Second):
		t.Fatal("finished view never cancelled")
	}
}

func TestReadLeaseCleanupKeepsBusyThroughCallbackAcknowledgement(t *testing.T) {
	driver := &pureLeaseRows{}
	l, q, p := pureReadLease(t, driver, false)
	ls, gs := l.state, l.state.snapshot.gate
	// Replace only this pure fixture's cancellation call with an already
	// started callback acknowledgement barrier. No driver or authority issuer
	// is involved; production closeCall must retain ownership until it opens.
	ls.call.close()
	ctx, cancel := context.WithCancel(context.Background())
	ack := make(chan struct{})
	call := &snapshotCall{state: ls.snapshot, ctx: ctx, caller: context.Background(), cancel: cancel, stopCaller: func() bool { return false }, done: ack}
	gs.mu.Lock()
	ls.call = call
	ls.snapshot.permitCall = call
	ls.query = nil
	ls.revoked = true
	claimed := ls.claimCleanupLocked()
	gs.mu.Unlock()
	if !claimed {
		t.Fatal("cleanup owner not claimed")
	}
	completed := make(chan struct{})
	go func() { ls.closeCall(); close(completed) }()
	gs.mu.Lock()
	held := gs.busy && ls.opBusy && ls.cleaning && !ls.released && ls.snapshot.activeLease == ls
	gs.mu.Unlock()
	if !held {
		t.Fatal("busy released before callback acknowledgement")
	}
	if _, err := p.BeginScan(context.Background(), ScanFunding); err != ErrGateBusy {
		t.Fatal("next scan entered before old callback acknowledgement")
	}
	if l.Abort() != nil || driver.closes.Load() != 0 || q.driver != driver {
		t.Fatal("concurrent cleanup touched driver")
	}
	close(ack)
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("cleanup acknowledgement hung")
	}
	gs.mu.Lock()
	released := !gs.busy && !ls.opBusy && ls.released && ls.snapshot.activeLease == nil && ls.snapshot.permitCall == nil
	gs.mu.Unlock()
	if !released {
		t.Fatal("cleanup did not release after acknowledgement")
	}
}

func TestReadLeaseCompletedProofSeesSuccessorCancellationBeforeCallback(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, source := range []string{"caller", "private-context"} {
		t.Run(source, func(t *testing.T) {
			l, _, permit := pureReadLease(t, &pureLeaseRows{}, false)
			ls, st := l.state, l.state.snapshot
			gs := st.gate
			// This is only the pure completion predicate, never a substitute for
			// a real BeginScan/Finish authority issuer.
			gs.mu.Lock()
			ls.query = nil
			ls.finished = true
			ls.proof = &scanProofState{lease: ls, nonce: &scanProofNonce{}}
			claimed := ls.claimCleanupLocked()
			gs.mu.Unlock()
			if !claimed {
				t.Fatal("pure completion cleanup")
			}
			ls.closeCall()
			proof := &ScanProof{state: ls.proof}
			copyProof := *proof
			if proof.Verify(permit, ScanLedger) != nil {
				t.Fatal("old normally cancelled call invalidated completion")
			}
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			next, err := st.stageContext(caller)
			if err != nil {
				t.Fatal("pure successor call")
			}
			defer next.close()
			gs.mu.Lock()
			gs.busy, st.permitCall = true, next
			gs.mu.Unlock()
			if proof.Verify(permit, ScanLedger) != nil || copyProof.Verify(permit, ScanLedger) != nil {
				t.Fatal("healthy successor busy invalidated prior completion")
			}
			// On one P this goroutine continues directly from cancellation into
			// Verify before the queued AfterFunc can update shared revocation.
			if source == "caller" {
				cancel()
			} else {
				next.cancel()
			}
			if err := proof.Verify(permit, ScanLedger); err != ErrTimeout {
				t.Fatalf("cancelled successor escaped pure completion binding: %v", err)
			}
			if err := copyProof.Verify(permit, ScanLedger); err != ErrTimeout {
				t.Fatalf("copied completion missed successor cancellation: %v", err)
			}
			gs.mu.Lock()
			next.stopLocked()
			st.permitCall = nil
			gs.busy = false
			gs.mu.Unlock()
		})
	}
}
