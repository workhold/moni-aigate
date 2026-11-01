package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func TestPostgresTenantPricingExactDomainAndImmutableSnapshot(t *testing.T) {
	f := integration(t)
	c := catalog.New(f.pool, nil)
	actor := catalog.PricingActor{UserID: f.admin}
	g, e := c.CreatePriceGroup(f.ctx, actor, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "customer group"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: 1, GroupID: &g.Result.ID}); e != nil {
		t.Fatal(e)
	}
	_, e = f.store.Reserve(f.ctx, f.input())
	assertCode(t, e, "price_unavailable")
	var count int
	if e = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM request_records`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	quote, e := c.PublishGroupPrice(f.ctx, actor, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: g.Result.ID, ModelID: f.model, InputRate: 2000000, OutputRate: 3000000, EffectiveAt: time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)})
	if e != nil {
		t.Fatal(e)
	}
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	if r.Amount != 260 || r.PriceVersionID != quote.Result.ID || r.PricingGroupID == nil || *r.PricingGroupID != g.Result.ID || r.TenantPricingRevision != 2 || r.RetailPricedAt.IsZero() {
		t.Fatal(r)
	}
	if _, e = c.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: 2}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.UpdatePriceGroup(f.ctx, actor, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: g.Result.ID, Label: "retired", State: "retired", ExpectedRevision: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e = f.pool.Exec(f.ctx, `UPDATE request_records SET pricing_group_id=NULL WHERE id=$1`, r.RequestID); e == nil {
		t.Fatal("snapshot changed")
	}
	if _, e = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); e != nil {
		t.Fatal(e)
	}
	next := f.reserve(t)
	defer f.store.EndRequest(next.RequestID)
	if next.Amount != 120 || next.PricingGroupID != nil || next.TenantPricingRevision != 3 {
		t.Fatal(next)
	}
	if _, e = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: next.RequestID, Reason: "not_sent"}); e != nil {
		t.Fatal(e)
	}
	f.check(t)
}

func TestPostgresTenantPricingLostCommitReplyAndOriginalReceipt(t *testing.T) {
	f := integration(t)
	p, armed, lost := voucherCommitLossPool(t, f)
	via := catalog.New(p, nil)
	direct := catalog.New(f.pool, nil)
	actor := catalog.PricingActor{UserID: f.admin}
	command := catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "lost commit"}
	armed.Store(true)
	out, e := via.CreatePriceGroup(f.ctx, actor, command)
	var pe *catalog.PricingError
	if !errors.As(e, &pe) || !pe.CommitUnknown || out.Result.ID != "" || lost.Load() != 1 {
		t.Fatal("unconfirmed result escaped", out, e, lost.Load())
	}
	receipt, e := direct.GetPricingOperation(f.ctx, f.admin, command.OperationID)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := via.CreatePriceGroup(f.ctx, actor, command)
	if e != nil || !replay.Replayed || replay.Result.ID != receipt.Operation.ResourceID {
		t.Fatal(replay, e)
	}
	var n int
	if e = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM price_groups WHERE label='lost commit'`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
func TestPostgresTenantPricingCommitRejectionRetryBound(t *testing.T) {
	for _, pgcode := range []string{"40P01", "40001", "23514"} {
		t.Run(pgcode, func(t *testing.T) {
			f := integration(t)
			if _, e := f.pool.Exec(f.ctx, `CREATE SEQUENCE pricing_retry_count; CREATE FUNCTION pricing_retry_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF nextval('pricing_retry_count')<3 THEN RAISE EXCEPTION 'synthetic rollback' USING ERRCODE='`+pgcode+`'; END IF; RETURN NULL; END $$; CREATE CONSTRAINT TRIGGER pricing_retry_commit AFTER INSERT ON pricing_operations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pricing_retry_commit()`); e != nil {
				t.Fatal(e)
			}
			c := catalog.New(f.pool, nil)
			out, e := c.CreatePriceGroup(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "retry"})
			var attempts, groups, receipts int
			if scan := f.pool.QueryRow(f.ctx, `SELECT (SELECT last_value FROM pricing_retry_count),(SELECT count(*) FROM price_groups),(SELECT count(*) FROM pricing_operations)`).Scan(&attempts, &groups, &receipts); scan != nil {
				t.Fatal(scan)
			}
			if pgcode == "23514" {
				if e == nil || attempts != 1 || groups != 0 || receipts != 0 {
					t.Fatal(out, e, attempts, groups, receipts)
				}
			} else if e != nil || attempts != 3 || groups != 1 || receipts != 1 {
				t.Fatal(out, e, attempts, groups, receipts)
			}
		})
	}
}
