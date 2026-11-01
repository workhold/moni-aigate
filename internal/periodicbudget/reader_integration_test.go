package periodicbudget_test

import (
	"context"
	"github.com/google/uuid"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"testing"
	"time"
)

func pbReader(t *testing.T, f *pbTxFixture) *Reader {
	t.Helper()
	r, e := NewReader(f.app, make([]byte, 32))
	pbTxMust(t, "construct read-only repository", e)
	return r
}
func readerWantCode(t *testing.T, e error, want string) {
	t.Helper()
	if ErrorCode(e) != want {
		t.Fatalf("reader safe error %q, want %q", ErrorCode(e), want)
	}
}
func TestReaderPGCurrentAuthorizationNoWrites(t *testing.T) {
	f := newPBTxFixture(t)
	r := pbReader(t, f)
	before := f.snapshot(t)
	calls := []func() (PeriodicBudgetView, error){
		func() (PeriodicBudgetView, error) { return r.PlatformTenantBudget(f.ctx, f.operator, f.tenant) },
		func() (PeriodicBudgetView, error) { return r.WalletBudget(f.ctx, f.user, f.tenant) },
		func() (PeriodicBudgetView, error) { return r.KeyBudget(f.ctx, f.user, f.tenant, f.key) },
		func() (PeriodicBudgetView, error) { return r.ProcurementBudget(f.ctx, f.approver, f.account) },
	}
	for _, call := range calls {
		v, e := call()
		pbTxMust(t, "read exact unconfigured view", e)
		if v.State != "unconfigured" || v.Revision != "0" || v.CurrentWindow != nil || v.AsOf != "2026-11-10T12:00:00.123456Z" {
			t.Fatal("unconfigured shape or DB time drift")
		}
	}
	page, e := r.KeyWindows(f.ctx, f.user, f.tenant, f.key, "")
	pbTxMust(t, "empty historical page", e)
	if page.Data == nil || len(page.Data) != 0 || page.NextCursor != "" {
		t.Fatal("empty page contract")
	}
	_, e = r.KeyWindows(f.ctx, f.user, f.tenant, f.key, "pbc1.invalid.invalid")
	readerWantCode(t, e, "invalid_cursor")
	if f.snapshot(t) != before {
		t.Fatal("read-only unconfigured requests wrote business facts")
	}
	_, e = r.WalletBudget(f.ctx, f.operator, f.tenant)
	readerWantCode(t, e, "forbidden")
	_, e = r.PlatformTenantBudget(f.ctx, f.user, uuid.NewString())
	readerWantCode(t, e, "not_found")
	_, e = r.KeyBudget(f.ctx, f.user, uuid.NewString(), f.key)
	readerWantCode(t, e, "forbidden")
	// This finance user is also a current tenant member, and can read only its
	// own Key. Current role/ownership comes from this RR snapshot, never a claim.
	_, e = f.app.Exec(f.ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'member')`, f.tenant, f.operator)
	pbTxMust(t, "add fixture member", e)
	_, e = r.KeyBudget(f.ctx, f.operator, f.tenant, f.key)
	readerWantCode(t, e, "not_found")
	own := uuid.NewString()
	_, e = f.app.Exec(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes) VALUES($1,$2,$3,'reader-own','fixture',$1,$4)`, own, f.tenant, f.operator, []string{f.model})
	pbTxMust(t, "create member own Key", e)
	_, e = r.KeyBudget(f.ctx, f.operator, f.tenant, own)
	pbTxMust(t, "member own Key read", e)
	_, e = f.app.Exec(f.ctx, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, own)
	pbTxMust(t, "revoke fixture Key", e)
	_, e = r.KeyBudget(f.ctx, f.operator, f.tenant, own)
	pbTxMust(t, "revoked Key history remains readable", e)
	_, e = f.app.Exec(f.ctx, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.operator)
	pbTxMust(t, "make current billing viewer", e)
	_, e = r.WalletBudget(f.ctx, f.operator, f.tenant)
	pbTxMust(t, "billing viewer wallet read", e)
	_, e = f.app.Exec(f.ctx, `UPDATE memberships SET status='suspended' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.operator)
	pbTxMust(t, "suspend current membership", e)
	_, e = r.KeyBudget(f.ctx, f.operator, f.tenant, own)
	readerWantCode(t, e, "forbidden")
	_, e = f.app.Exec(f.ctx, `UPDATE tenants SET status='suspended' WHERE id=$1`, f.tenant)
	pbTxMust(t, "suspend target tenant", e)
	_, e = r.PlatformTenantBudget(f.ctx, f.user, f.tenant)
	pbTxMust(t, "platform can observe suspended target", e)
	_, e = r.WalletBudget(f.ctx, f.user, f.tenant)
	readerWantCode(t, e, "forbidden")
	f.verifyFinance(t)
}
func TestReaderPGOriginalOperationAndClockRegression(t *testing.T) {
	f := newPBTxFixture(t)
	r := pbReader(t, f)
	tenantControl := f.configure(t, "tenant", true, 500)
	keyControl := f.configure(t, "key", false, 500)
	var platformOperation, keyOperation string
	pbTxMust(t, "read original operation identities", f.app.QueryRow(f.ctx, `SELECT (SELECT configuration_operation_id FROM periodic_budget_controls WHERE id=$1),(SELECT configuration_operation_id FROM periodic_budget_controls WHERE id=$2)`, tenantControl, keyControl).Scan(&platformOperation, &keyOperation))
	original, e := r.PlatformOperation(f.ctx, f.approver, platformOperation)
	pbTxMust(t, "read original platform receipt", e)
	if original.Operation.ResourceID == original.Result.ScopeID {
		t.Fatal("fixture must distinguish control identity from target")
	}
	f.configure(t, "tenant", true, 200)
	again, e := r.PlatformOperation(f.ctx, f.operator, platformOperation)
	pbTxMust(t, "read immutable historical result", e)
	if string(pbTxJSON(t, original)) != string(pbTxJSON(t, again)) {
		t.Fatal("old receipt changed after configuration")
	}
	_, e = r.TenantOperation(f.ctx, f.user, f.tenant, keyOperation)
	pbTxMust(t, "current Key reader reads original tenant receipt", e)
	_, e = r.PlatformOperation(f.ctx, f.user, keyOperation)
	readerWantCode(t, e, "not_found")
	_, e = r.TenantOperation(f.ctx, f.user, f.tenant, platformOperation)
	readerWantCode(t, e, "not_found")
	// Only the isolated owner moves the test clock; APP advances the observed
	// window through the real function. Historical reads must not call it.
	f.setClock(t, time.Date(2026, 12, 10, 12, 0, 0, 123456000, time.UTC))
	tx := f.begin(t)
	f.exec(t, tx, `SELECT pb_observe($1,pb_now())`, tenantControl)
	pbTxMust(t, "observe fixture next month", tx.Commit(f.ctx))
	f.setClock(t, time.Date(2026, 11, 10, 12, 0, 0, 123456000, time.UTC))
	_, e = r.PlatformTenantBudget(f.ctx, f.user, f.tenant)
	readerWantCode(t, e, "periodic_clock_regression")
	before := f.snapshot(t)
	p, e := r.PlatformTenantWindows(f.ctx, f.user, f.tenant, "")
	pbTxMust(t, "history survives current clock regression", e)
	if len(p.Data) != 1 {
		t.Fatal("history lost")
	}
	_, e = r.PlatformOperation(f.ctx, f.user, platformOperation)
	pbTxMust(t, "original receipt survives current clock regression", e)
	if before != f.snapshot(t) {
		t.Fatal("historical reads changed clocks or budget facts")
	}
	_, e = f.app.Exec(f.ctx, `UPDATE users SET status='suspended' WHERE id=$1`, f.user)
	pbTxMust(t, "suspend current actor", e)
	_, e = r.PlatformOperation(f.ctx, f.user, platformOperation)
	readerWantCode(t, e, "forbidden")
}
func TestReaderPGFixedPagesCOrderAndCursorBinding(t *testing.T) {
	f := newPBTxFixture(t)
	r := pbReader(t, f)
	c := f.configure(t, "tenant", false, 100000)
	f.setClock(t, time.Date(2036, 11, 10, 0, 0, 0, 0, time.UTC))
	tx := f.begin(t)
	for i := 0; i < 101; i++ {
		start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0)
		f.exec(t, tx, `INSERT INTO periodic_budget_windows(id,control_id,start_at,end_at) VALUES($1,$2,$3,$4)`, uuid.NewString(), c, start, start.AddDate(0, 1, 0))
	}
	pbTxMust(t, "commit 101 legitimate empty windows", tx.Commit(f.ctx))
	before := f.snapshot(t)
	cursor := ""
	seen := map[string]bool{}
	last := ""
	for n, want := range []int{50, 50, 1} {
		p, e := r.PlatformTenantWindows(f.ctx, f.user, f.tenant, cursor)
		pbTxMust(t, "read fixed window page", e)
		if len(p.Data) != want {
			t.Fatal("wrong fixed window page size")
		}
		for _, w := range p.Data {
			if seen[w.ID] || last != "" && w.StartAt >= last {
				t.Fatal("window keyset duplicate or order drift")
			}
			seen[w.ID] = true
			last = w.StartAt
		}
		if n < 2 && p.NextCursor == "" || n == 2 && p.NextCursor != "" {
			t.Fatal("incorrect final cursor")
		}
		cursor = p.NextCursor
	}
	if len(seen) != 101 || f.snapshot(t) != before {
		t.Fatal("window pagination lost rows or wrote facts")
	}
	first, e := r.PlatformTenantWindows(f.ctx, f.user, f.tenant, "")
	pbTxMust(t, "first window page", e)
	codec, e := NewCursorCodec(make([]byte, 32))
	pbTxMust(t, "construct public cursor verifier", e)
	decoded, e := codec.Decode(CursorBinding{Route: "platform_tenant_windows", Scope: "tenant", ScopeID: f.tenant, IdentityScope: "platform", Order: "start_at_desc_id_c_desc"}, first.NextCursor)
	pbTxMust(t, "decode page boundary", e)
	if decoded.ID != first.Data[49].ID {
		t.Fatal("cursor did not come from row 50")
	}
	_, e = r.WalletWindows(f.ctx, f.user, f.tenant, first.NextCursor)
	readerWantCode(t, e, "invalid_cursor")
	// Same DB-created_at for all allocations exercises the C-ordered UUID tie.
	tx = f.begin(t)
	locks := pbTxCustomerLocks(t, f, tx, "")
	for i := 0; i < 51; i++ {
		f.newCustomer(t, tx, locks)
	}
	pbTxMust(t, "commit 51 complete customer reserves", tx.Commit(f.ctx))
	f.verifyFinance(t)
	before = f.snapshot(t)
	a, e := r.PlatformTenantAllocations(f.ctx, f.user, f.tenant, "")
	pbTxMust(t, "allocation first 50", e)
	b, e := r.PlatformTenantAllocations(f.ctx, f.user, f.tenant, a.NextCursor)
	pbTxMust(t, "allocation last one", e)
	if len(a.Data) != 50 || len(b.Data) != 1 || b.NextCursor != "" {
		t.Fatal("allocation page boundary")
	}
	all := append(a.Data, b.Data...)
	for i := 1; i < len(all); i++ {
		if all[i].CreatedAt != all[0].CreatedAt || all[i].ID >= all[i-1].ID {
			t.Fatal("allocation C tie ordering")
		}
	}
	if before != f.snapshot(t) {
		t.Fatal("allocation reader changed financial facts")
	}
	// All methods must be bounded by the earlier parent cancellation.
	cancelled, stop := context.WithCancel(f.ctx)
	stop()
	_, e = r.PlatformTenantBudget(cancelled, f.user, f.tenant)
	readerWantCode(t, e, "periodic_budget_unavailable")
}
