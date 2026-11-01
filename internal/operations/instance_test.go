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
	if _, err = f.owner.Exec(f.ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(first.Check(f.ctx), ErrInstanceLost) {
		t.Fatal("dead lock session still admitted")
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
	c, err := s.CreateCredit(ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: 1000, BusinessRef: "drain", ProofRef: "drain", IdempotencyKey: "drain", RequestHMAC: "drain"})
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
