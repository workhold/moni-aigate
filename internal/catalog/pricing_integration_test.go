package catalog_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func pricingCode(t *testing.T, e error, want string) {
	t.Helper()
	var pe *catalog.PricingError
	if !errors.As(e, &pe) || pe.Code != want {
		t.Fatalf("want %s, got %v", want, e)
	}
}
func TestTenantPricingCommandsAndOriginalReceipts(t *testing.T) {
	f := publicDB(t)
	actor := catalog.PricingActor{UserID: f.admin}
	cmd := catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "商业客户"}
	first, e := f.store.CreatePriceGroup(f.ctx, actor, cmd)
	if e != nil {
		t.Fatal(e)
	}
	if first.Replayed || first.Result.Revision != "1" {
		t.Fatal(first)
	}
	replay, e := f.store.CreatePriceGroup(f.ctx, actor, cmd)
	if e != nil || !replay.Replayed || !reflect.DeepEqual(first.Result, replay.Result) {
		t.Fatal(replay, e)
	}
	cmd.Label = "other"
	_, e = f.store.CreatePriceGroup(f.ctx, actor, cmd)
	pricingCode(t, e, "policy_operation_conflict")
	updated, e := f.store.UpdatePriceGroup(f.ctx, actor, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: first.Result.ID, Label: "VIP", State: "active", ExpectedRevision: 1})
	if e != nil || updated.Result.Revision != "2" {
		t.Fatal(updated, e)
	}
	original, e := f.store.GetPricingOperation(f.ctx, f.finance, first.Operation.OperationID)
	if e != nil || original.Result.(*catalog.GroupView).Label != "商业客户" {
		t.Fatal(original, e)
	}
	noop, e := f.store.UpdatePriceGroup(f.ctx, actor, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: first.Result.ID, Label: "VIP", State: "active", ExpectedRevision: 2})
	if e != nil || noop.Result.Revision != "2" {
		t.Fatal(noop, e)
	}
	_, e = f.store.UpdatePriceGroup(f.ctx, actor, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: first.Result.ID, Label: "VIP", State: "active", ExpectedRevision: 1})
	pricingCode(t, e, "pricing_revision_conflict")
	tenant := uuid.NewString()
	if _, e = f.owner.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'pricing')`, tenant); e != nil {
		t.Fatal(e)
	}
	assigned, e := f.store.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: tenant, ExpectedRevision: 1, GroupID: &first.Result.ID})
	if e != nil || assigned.Result.PricingRevision != "2" {
		t.Fatal(assigned, e)
	}
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	price, e := f.store.PublishGroupPrice(f.ctx, actor, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: first.Result.ID, ModelID: f.model, InputRate: 0, OutputRate: 3, EffectiveAt: at})
	if e != nil || price.Result.InputRate != "0" {
		t.Fatal(price, e)
	}
	_, e = f.store.PublishGroupPrice(f.ctx, actor, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: first.Result.ID, ModelID: f.model, InputRate: 2, OutputRate: 3, EffectiveAt: at})
	pricingCode(t, e, "price_effective_conflict")
	if _, e = f.store.UpdatePriceGroup(f.ctx, actor, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: first.Result.ID, Label: "VIP", State: "retired", ExpectedRevision: 2}); e != nil {
		t.Fatal(e)
	}
	// Existing assignment/no-op survives retirement; a new assignment does not.
	_, e = f.store.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: tenant, ExpectedRevision: 2, GroupID: &first.Result.ID})
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.store.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: tenant, ExpectedRevision: 2})
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.store.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: tenant, ExpectedRevision: 3, GroupID: &first.Result.ID})
	pricingCode(t, e, "price_group_retired")
	if _, e = f.owner.Exec(f.ctx, `UPDATE users SET platform_role='none' WHERE id=$1`, f.admin); e != nil {
		t.Fatal(e)
	}
	_, e = f.store.CreatePriceGroup(f.ctx, actor, catalog.GroupCreateCommand{OperationID: first.Operation.OperationID, Label: "商业客户"})
	pricingCode(t, e, "forbidden")
	_, e = f.store.GetPricingOperation(f.ctx, f.admin, first.Operation.OperationID)
	pricingCode(t, e, "forbidden")
}
func TestTenantPricingConcurrentOperationAndAuditRollback(t *testing.T) {
	f := publicDB(t)
	cmd := catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "concurrent"}
	actor := catalog.PricingActor{UserID: f.admin}
	var wg sync.WaitGroup
	results := make(chan catalog.PricingOperationResult[catalog.GroupView], 10)
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); v, e := f.store.CreatePriceGroup(f.ctx, actor, cmd); results <- v; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var id string
	first := 0
	for v := range results {
		if id != "" && id != v.Result.ID {
			t.Fatal("duplicate resources")
		}
		id = v.Result.ID
		if !v.Replayed {
			first++
		}
	}
	if first != 1 {
		t.Fatal("first results", first)
	}
	if _, e := f.owner.Exec(f.ctx, `CREATE FUNCTION pricing_test_audit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='pricing.group.create' THEN RAISE EXCEPTION 'synthetic failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER pricing_test_audit_fail BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION pricing_test_audit_fail()`); e != nil {
		t.Fatal(e)
	}
	failed := uuid.NewString()
	_, e := f.store.CreatePriceGroup(f.ctx, actor, catalog.GroupCreateCommand{OperationID: failed, Label: "rollback"})
	if e == nil {
		t.Fatal("audit failure committed")
	}
	var n int
	if e = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM pricing_operations WHERE operation_id=$1)+(SELECT count(*) FROM price_groups WHERE label='rollback')`, failed).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
func TestTenantPricingReadIsolationPreviewAndPagination(t *testing.T) {
	f := publicDB(t)
	a := catalog.PricingActor{UserID: f.admin}
	tenant := uuid.NewString()
	if _, e := f.owner.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'preview')`, tenant); e != nil {
		t.Fatal(e)
	}
	g, e := f.store.CreatePriceGroup(f.ctx, a, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "selected"})
	if e != nil {
		t.Fatal(e)
	}
	empty, e := f.store.PreviewTenantPricing(f.ctx, f.finance, tenant, &g.Result.ID, "")
	if e != nil || len(empty.Data) != 0 || empty.Context.AsOf == "" || !empty.Context.Assignable {
		t.Fatal(empty, e)
	}
	for i := 0; i < 52; i++ {
		model := f.modelWith(t, fmt.Sprintf("pricing-%03d", i), "openai_chat", false)
		if _, e = f.owner.Exec(f.ctx, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, tenant, model); e != nil {
			t.Fatal(e)
		}
	}
	page, e := f.store.PreviewTenantPricing(f.ctx, f.finance, tenant, &g.Result.ID, "")
	if e != nil || len(page.Data) != 50 || page.NextCursor == "" {
		t.Fatal(len(page.Data), e)
	}
	for _, v := range page.Data {
		if v.RetailPrice.Status != "unavailable" || v.RetailPrice.AsOf != page.Context.AsOf || v.RetailPrice.Source != "group" {
			t.Fatal(v)
		}
	}
	next, e := f.store.PreviewTenantPricing(f.ctx, f.finance, tenant, &g.Result.ID, page.NextCursor)
	if e != nil || len(next.Data) != 2 || next.NextCursor != "" {
		t.Fatal(next, e)
	}
	_, e = f.store.PreviewTenantPricing(f.ctx, f.admin, tenant, &g.Result.ID, page.NextCursor)
	pricingCode(t, e, "invalid_cursor")
	_, e = f.store.PreviewTenantPricing(f.ctx, f.finance, tenant, nil, page.NextCursor)
	pricingCode(t, e, "invalid_cursor")
	_, e = f.store.PriceGroups(f.ctx, f.customer, "")
	pricingCode(t, e, "forbidden")
}
