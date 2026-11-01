package periodicbudget

import (
	"encoding/json"
	"strings"
	"testing"
)

func ptr[T any](value T) *T { return &value }

func configuredBudget() PeriodicBudgetView {
	return PeriodicBudgetView{
		Scope: "key", ScopeID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", Revision: "1", PeriodKind: ptr("utc_day"), Enabled: true, LimitAmount: ptr("0"), State: "active",
		EffectiveFrom: ptr("2026-11-01T00:00:00.000000Z"), TrackingStartedAt: ptr("2026-10-31T12:00:00.000000Z"),
		CurrentWindow: &BudgetWindowView{"dddddddd-dddd-4ddd-8ddd-dddddddddddd", "2026-11-01T00:00:00.000000Z", "2026-11-02T00:00:00.000000Z", "70", "0"},
		CarryHeld:     "0", AsOf: "2026-11-01T00:00:00.000000Z",
	}
}

func TestBudgetViewStateCalendarAndExactNullableShape(t *testing.T) {
	v := PeriodicBudgetView{Scope: "tenant", ScopeID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Revision: "0", State: "unconfigured", CarryHeld: "0", AsOf: "2026-11-01T00:00:00.000000Z"}
	if v.Validate() != nil {
		t.Fatal("unconfigured rejected")
	}
	b, err := json.Marshal(v)
	const want = `{"scope":"tenant","scope_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","revision":"0","period_kind":null,"enabled":false,"limit_amount":null,"state":"unconfigured","effective_from":null,"tracking_started_at":null,"current_window":null,"carry_held":"0","as_of":"2026-11-01T00:00:00.000000Z"}`
	if err != nil || string(b) != want {
		t.Fatalf("DTO shape changed: %s %v", b, err)
	}
	v.CarryHeld = "1"
	if ErrorCode(v.Validate()) != "periodic_budget_invariant" {
		t.Fatal("unconfigured carry accepted")
	}
	v = configuredBudget()
	if v.Validate() != nil {
		t.Fatal("active over-cap historical view rejected")
	}
	v.Enabled, v.State = false, "disabled"
	if v.Validate() != nil {
		t.Fatal("disabled before/after boundary rejected")
	}
	v.Enabled, v.State = true, "scheduled"
	v.AsOf, v.CurrentWindow = "2026-10-31T23:59:59.999999Z", nil
	if v.Validate() != nil {
		t.Fatal("scheduled rejected")
	}
	for _, mutate := range []func(*PeriodicBudgetView){
		func(v *PeriodicBudgetView) { v.Revision = "0" },
		func(v *PeriodicBudgetView) { v.State = "scheduled" },
		func(v *PeriodicBudgetView) { v.EffectiveFrom = nil },
		func(v *PeriodicBudgetView) { v.LimitAmount = ptr("9223372036854775808") },
		func(v *PeriodicBudgetView) { v.EffectiveFrom = ptr("2026-11-02T00:00:00.000000Z") },
		func(v *PeriodicBudgetView) { v.CurrentWindow.EndAt = "2026-11-03T00:00:00.000000Z" },
		func(v *PeriodicBudgetView) { v.CurrentWindow.StartAt = "2026-10-31T00:00:00.000000Z" },
	} {
		bad := configuredBudget()
		mutate(&bad)
		if ErrorCode(bad.Validate()) != "periodic_budget_invariant" {
			t.Fatal("invalid view accepted")
		}
	}
	v = configuredBudget()
	v.PeriodKind, v.TrackingStartedAt, v.EffectiveFrom = ptr("utc_month"), ptr("2024-02-29T23:59:59.000000Z"), ptr("2024-03-01T00:00:00.000000Z")
	v.AsOf = "2024-03-01T00:00:00.000000Z"
	v.CurrentWindow.StartAt, v.CurrentWindow.EndAt = v.AsOf, "2024-04-01T00:00:00.000000Z"
	if v.Validate() != nil {
		t.Fatal("natural month/leap day rejected")
	}
}

func TestAllocationOriginAndSupplierCostAboveUpper(t *testing.T) {
	v := BudgetAllocationView{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", SubjectKind: "supplier_attempt", SubjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", WindowID: ptr("cccccccc-cccc-4ccc-8ccc-cccccccccccc"), UpperAmount: "1", HeldAmount: "0", SpentAmount: strings.Repeat("9", 100), State: "disputed", CreatedAt: "2026-11-01T00:00:00.000000Z"}
	if v.Validate() != nil {
		t.Fatal("legal supplier spent above upper was truncated/rejected")
	}
	v.SubjectKind = "customer_request"
	if v.Validate() == nil {
		t.Fatal("customer disputed accepted")
	}
	v.State, v.SubjectKind, v.WindowID = "held", "legacy_customer", nil
	if v.Validate() != nil {
		t.Fatal("legacy customer rejected")
	}
	v.WindowID = ptr(v.SubjectID)
	if v.Validate() == nil {
		t.Fatal("legacy window accepted")
	}
	v.SubjectKind, v.WindowID = "supplier_attempt", nil
	if v.Validate() == nil {
		t.Fatal("normal allocation without window accepted")
	}
}

func TestReceiptValidatesTargetTimeAndRevisionWithoutEquatingControlAndScope(t *testing.T) {
	r := CommandReceipt{Operation: PeriodicBudgetOperationView{OperationID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Kind: "key_periodic_budget.configure", Scope: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Actor: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Target: "/api/keys/cccccccc-cccc-4ccc-8ccc-cccccccccccc/periodic-budget", InputFingerprint: strings.Repeat("a", 64), ResourceID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", Revision: "1", CreatedAt: "2026-11-01T00:00:00.000000Z"}, Result: configuredBudget()}
	if r.Validate() != nil {
		t.Fatal("valid distinct control/scope rejected")
	}
	for _, mutate := range []func(*CommandReceipt){
		func(r *CommandReceipt) { r.Operation.Revision = "2" },
		func(r *CommandReceipt) { r.Operation.CreatedAt = "2026-11-01T00:00:00.000001Z" },
		func(r *CommandReceipt) { r.Operation.Target += "/" },
		func(r *CommandReceipt) { r.Operation.Scope = "platform" },
		func(r *CommandReceipt) { r.Result.ScopeID = r.Operation.ResourceID },
		func(r *CommandReceipt) { r.Result.Scope = "tenant" },
	} {
		bad := r
		mutate(&bad)
		if ErrorCode(bad.Validate()) != "periodic_budget_invariant" {
			t.Fatal("bad receipt relation accepted")
		}
	}
}

func TestEmptyPagesAlwaysEncodeArraysAndEmptyNextCursor(t *testing.T) {
	for _, value := range []any{WindowPage{}, AllocationPage{}, PageResponse[BudgetWindowView]{}} {
		b, err := json.Marshal(value)
		if err != nil || !strings.Contains(string(b), `"data":[]`) || !strings.Contains(string(b), `"next_cursor":""`) || strings.Contains(string(b), "null") {
			t.Fatalf("nil collection escaped: %s %v", b, err)
		}
	}
}
