package operations

import (
	"context"
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func TestInstanceFencesAfterBackendTermination(t *testing.T) {
	f := operationDB(t)
	key := int64(rand.Uint64() >> 1)
	first, err := AcquireInstance(f.ctx, f.app, key)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if other, e := AcquireInstance(f.ctx, f.app, key); e == nil {
		other.Close()
		t.Fatal("second process acquired live lock")
	}
	pid := first.conn.Conn().PgConn().PID()
	// Observe from a separate physical session, capturing the backend identity
	// before termination so PID reuse cannot satisfy the disappearance check.
	observer, err := f.owner.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Release()
	var backendStart time.Time
	var databaseOID uint32
	if err = observer.QueryRow(f.ctx, `SELECT backend_start,datid FROM pg_catalog.pg_stat_activity WHERE pid=$1`, pid).Scan(&backendStart, &databaseOID); err != nil || backendStart.IsZero() || databaseOID == 0 {
		t.Fatal("capture instance backend before termination", err)
	}
	var terminated bool
	if err = observer.QueryRow(f.ctx, "SELECT pg_terminate_backend($1)", pid).Scan(&terminated); err != nil || !terminated {
		t.Fatal(err)
	}
	if !errors.Is(first.Check(f.ctx), ErrInstanceLost) {
		t.Fatal("dead lock session still admitted")
	}
	// A true pg_terminate_backend result confirms signal delivery, not remote
	// exit. Wait within the fixture lifetime (at most two seconds) for both the
	// exact backend and this random 64-bit advisory key to disappear. This is
	// observation only: the actual next AcquireInstance is still called once.
	observeCtx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	for {
		var backendPresent, keyPresent bool
		err = observer.QueryRow(observeCtx, `SELECT
 EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND backend_start=$2),
 EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE locktype='advisory' AND database=$3::pg_catalog.oid
 AND classid=$4::pg_catalog.oid AND objid=$5::pg_catalog.oid AND objsubid=1)`,
			pid, backendStart, databaseOID, uint32(uint64(key)>>32), uint32(key)).Scan(&backendPresent, &keyPresent)
		if err != nil {
			t.Fatal("observe terminated instance and advisory key", err)
		}
		if !backendPresent && !keyPresent {
			break
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-observeCtx.Done():
			timer.Stop()
			t.Fatal("terminated instance or advisory key remained within the observation budget")
		case <-timer.C:
		}
	}
	next, err := AcquireInstance(f.ctx, f.app, key)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err = next.Check(f.ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(first.Check(f.ctx), ErrInstanceLost) {
		t.Fatal("old instance reconnected")
	}
}
func TestTakeoverDrainsDurablePriorWindow(t *testing.T) {
	f := operationDB(t)
	ctx := f.ctx
	s := billing.New(f.app)
	c, err := s.CreateCredit(ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: 1000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 1000, BusinessRef: "drain", ProofRef: "drain", IdempotencyKey: "drain", RequestHMAC: "drain"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: c.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Reserve(ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, RouteRevision: 1, RequestHMAC: "drain", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().Add(30 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.EndRequest(r.RequestID)
	i, err := AcquireInstance(ctx, f.app, int64(rand.Uint64()>>1))
	if err != nil {
		t.Fatal(err)
	}
	defer i.Close()
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	err = i.DrainPredecessor(short, f.app)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished window admitted: %v", err)
	}
	if _, err = s.Release(ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "authorization_revoked"}); err != nil {
		t.Fatal(err)
	}
	if err = i.DrainPredecessor(ctx, f.app); err != nil {
		t.Fatal(err)
	}
}
