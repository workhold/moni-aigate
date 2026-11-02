package fundinglotverify

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// All before/after values are supplied explicitly from the hand-calculated case.
func pbgSupplierEffect(r *PeriodicBudgetGraphRows, kind, dh, ds, h, source, spent, budgetDelta, state string) {
	n := len(r.Events)
	peid, eid := pbgID(200+n), pbgID(300+n)
	p := &r.Procurement[0]
	a := &r.Allocations[0]
	c := &r.Controls[0]
	stamp := strconv.Itoa(20 + n)
	at := pbgTime("2026-10-01T09:00:00Z")
	r.ProcurementEvents = append(r.ProcurementEvents, PeriodicBudgetProcurementEventRow{ID: peid, EventKey: "effect:" + peid, AccountID: p.AccountID, AttemptID: p.AttemptID, HeldDelta: dh, SpentDelta: ds, Type: kind, CreatedXID: pbgPtr(stamp)})
	var windowRevision *string
	if a.WindowID != nil {
		windowRevision = pbgPtr(strconv.Itoa(n))
	}
	r.Events = append(r.Events, PeriodicBudgetEventRow{ID: eid, ControlID: c.ID, AllocationID: a.ID, SourceKind: "procurement_event", SourceID: peid, Seq: strconv.Itoa(n + 1), AllocationBeforeRevision: strconv.Itoa(n), WindowBeforeRevision: windowRevision, BeforeHeld: a.HeldAmount, BeforeSpent: a.SpentAmount, AfterHeld: h, AfterSpent: spent, AfterSourceSpent: source, AfterState: state, HeldDelta: dh, SpentDelta: budgetDelta, CreatedXID: stamp, CreatedAt: at})
	p.HeldAmount = h
	p.SpentAmount = source
	p.State = state
	if state == "held" {
		p.State = "unknown"
	}
	p.Anomaly = state == "disputed"
	r.Attempts[0].DeliveryState = "unknown"
	r.Accounts[0].HeldAmount = h
	r.Accounts[0].SpentAmount = source
	a.HeldAmount = h
	a.SourceHeld = h
	a.SourceSpent = source
	a.SpentAmount = spent
	a.State = state
	a.Revision = strconv.Itoa(n + 1)
	a.LastEventID = pbgPtr(eid)
	c.HeldAmount = h
	c.SpentAmount = spent
	c.EventSeq = strconv.Itoa(n + 1)
	c.LastEventID = pbgPtr(eid)
	if a.WindowID != nil {
		win := &r.Windows[0]
		win.HeldAmount = h
		win.SpentAmount = spent
		win.Revision = strconv.Itoa(n + 1)
		win.LastEventID = pbgPtr(eid)
	}
}
func pbgLegacySupplier() PeriodicBudgetGraphRows {
	r := pbgSupplier()
	r.Windows = nil
	r.Attempts[0].CreatedXID = nil
	p := &r.Procurement[0]
	p.CreatedXID = nil
	p.ControlID = nil
	p.AdmittedAt = PeriodicBudgetGraphTime{}
	p.SpentAmount = "50"
	p.State = "disputed"
	p.Anomaly = true
	r.Accounts[0].SpentAmount = "50"
	for i := range r.ProcurementEvents {
		r.ProcurementEvents[i].CreatedXID = nil
	}
	r.ProcurementEvents = append(r.ProcurementEvents, PeriodicBudgetProcurementEventRow{ID: pbgID(33), EventKey: "old-cost", AccountID: p.AccountID, AttemptID: p.AttemptID, HeldDelta: "-200", SpentDelta: "50", Type: "cost_confirmed"}, PeriodicBudgetProcurementEventRow{ID: pbgID(34), EventKey: "old-dispute", AccountID: p.AccountID, AttemptID: p.AttemptID, HeldDelta: "200", SpentDelta: "0", Type: "evidence_disputed"})
	a := &r.Allocations[0]
	a.Origin = "legacy_import"
	a.OriginSourceID = p.AttemptID
	a.WindowID = nil
	a.AdmittedAt = PeriodicBudgetGraphTime{}
	a.Baseline = "50"
	a.SourceSpent = "50"
	a.State = "disputed"
	a.CreatedXID = "9"
	e := &r.Events[0]
	e.SourceKind = "legacy_import"
	e.SourceID = p.AttemptID
	e.WindowBeforeRevision = nil
	e.AfterSourceSpent = "50"
	e.AfterState = "disputed"
	e.CreatedXID = "9"
	r.Operations[0].ResultSnapshot = pbgSnapshot(r.Controls[0], r.Operations[0].CreatedAt, "200")
	return r
}
func pbgCatchup() PeriodicBudgetGraphRows {
	r := pbgSupplier()
	r.Windows = nil
	r.Attempts[0].CreatedXID = nil
	p := &r.Procurement[0]
	p.CreatedXID = nil
	p.ControlID = nil
	p.AdmittedAt = PeriodicBudgetGraphTime{}
	p.HeldAmount = "0"
	p.SpentAmount = "70"
	p.State = "settled"
	r.Accounts[0].HeldAmount = "0"
	r.Accounts[0].SpentAmount = "70"
	r.ProcurementEvents[0].CreatedXID = nil
	r.ProcurementEvents = append(r.ProcurementEvents, PeriodicBudgetProcurementEventRow{ID: pbgID(33), EventKey: "old-cost", AccountID: p.AccountID, AttemptID: p.AttemptID, HeldDelta: "-200", SpentDelta: "50", Type: "cost_confirmed"}, PeriodicBudgetProcurementEventRow{ID: pbgID(34), EventKey: "new-correction", AccountID: p.AccountID, AttemptID: p.AttemptID, HeldDelta: "0", SpentDelta: "20", Type: "cost_corrected", CreatedXID: pbgPtr("10")})
	a := &r.Allocations[0]
	a.Origin = "legacy_catchup"
	a.OriginSourceID = pbgID(34)
	a.WindowID = nil
	a.AdmittedAt = PeriodicBudgetGraphTime{}
	a.Baseline = "50"
	a.HeldAmount = "0"
	a.SourceHeld = "0"
	a.SourceSpent = "70"
	a.SpentAmount = "20"
	a.State = "settled"
	e := &r.Events[0]
	e.SourceID = pbgID(34)
	e.WindowBeforeRevision = nil
	e.AfterHeld = "0"
	e.AfterSpent = "20"
	e.AfterSourceSpent = "70"
	e.AfterState = "settled"
	e.HeldDelta = "0"
	e.SpentDelta = "20"
	r.Controls[0].HeldAmount = "0"
	r.Controls[0].SpentAmount = "20"
	return r
}

func TestCheckPeriodicBudgetGraphSupplierLifecycle(t *testing.T) {
	r := pbgSupplier()
	pbgSupplierEffect(&r, "consumption_unknown", "0", "0", "200", "0", "0", "0", "held")
	pbgClean(t, r)
	pbgSupplierEffect(&r, "recovered_unknown", "0", "0", "200", "0", "0", "0", "held")
	pbgClean(t, r)
	pbgSupplierEffect(&r, "cost_confirmed", "-200", "300", "0", "300", "300", "300", "settled")
	pbgClean(t, r)
	pbgSupplierEffect(&r, "evidence_disputed", "200", "0", "200", "300", "300", "0", "disputed")
	pbgClean(t, r)
	pbgSupplierEffect(&r, "cost_corrected", "-200", "-250", "0", "50", "50", "-250", "settled")
	pbgClean(t, r)
	for _, kind := range []string{"released_no_consumption", "expired_without_permit"} {
		r := pbgSupplier()
		pbgSupplierEffect(&r, kind, "-200", "0", "0", "0", "0", "0", "released")
		pbgClean(t, r)
	}
	r = pbgSupplier()
	pbgSupplierEffect(&r, "cost_confirmed", "-200", strings.Repeat("9", 100), "0", strings.Repeat("9", 100), strings.Repeat("9", 100), strings.Repeat("9", 100), "settled")
	pbgClean(t, r)
	for name, change := range map[string]func(*PeriodicBudgetGraphRows){"source total": func(r *PeriodicBudgetGraphRows) { r.Procurement[0].HeldAmount = "199" }, "account total": func(r *PeriodicBudgetGraphRows) { r.Accounts[0].HeldAmount = "199" }, "canonical reserve": func(r *PeriodicBudgetGraphRows) { r.ProcurementEvents[0].EventKey = "not-reserve" }} {
		t.Run(name, func(t *testing.T) {
			r := pbgSupplier()
			change(&r)
			if pbgCheck(t, r).Mismatches.ProcurementSourceMismatches == 0 {
				t.Fatal(name)
			}
		})
	}
	r = pbgSupplier()
	r.ProcurementEvents = append(r.ProcurementEvents, PeriodicBudgetProcurementEventRow{ID: pbgID(61), EventKey: "zero-effect", AccountID: pbgID(30), AttemptID: pbgID(20), HeldDelta: "0", SpentDelta: "0", Type: "consumption_unknown", CreatedXID: pbgPtr("20")})
	if pbgCheck(t, r).Mismatches.MissingEffects != 1 {
		t.Fatal("new zero source omitted")
	}
}

func TestCheckPeriodicBudgetGraphLegacyBoundaries(t *testing.T) {
	r := pbgLegacySupplier()
	got := pbgClean(t, r)
	if got.Unproven.LegacyEffectBoundaryUnproven != 1 || got.Unproven.OperationSnapshotPrefixUnproven != 1 || got.CoverageComplete {
		t.Fatal(got)
	}
	pbgSupplierEffect(&r, "cost_corrected", "-200", "-30", "0", "20", "0", "0", "settled")
	pbgClean(t, r)
	pbgSupplierEffect(&r, "cost_corrected", "0", "50", "0", "70", "20", "20", "settled")
	pbgClean(t, r)
	r = pbgCatchup()
	got = pbgClean(t, r)
	if got.Unproven.LegacyEffectBoundaryUnproven != 1 || got.Unproven.OperationSnapshotPrefixUnproven != 0 {
		t.Fatal(got)
	}
	r.ProcurementEvents = append(r.ProcurementEvents, PeriodicBudgetProcurementEventRow{ID: pbgID(71), EventKey: "cancel-plus", AccountID: pbgID(30), AttemptID: pbgID(20), HeldDelta: "0", SpentDelta: "1", Type: "cost_corrected"}, PeriodicBudgetProcurementEventRow{ID: pbgID(72), EventKey: "cancel-minus", AccountID: pbgID(30), AttemptID: pbgID(20), HeldDelta: "0", SpentDelta: "-1", Type: "cost_corrected"}, PeriodicBudgetProcurementEventRow{ID: pbgID(73), EventKey: "zero", AccountID: pbgID(30), AttemptID: pbgID(20), HeldDelta: "0", SpentDelta: "0", Type: "consumption_unknown"})
	got = pbgClean(t, r)
	if got.Unproven.LegacyEffectBoundaryUnproven != 1 {
		t.Fatal("cancelling/zero omissions cannot be proved")
	}
	r = pbgCatchup()
	r.Allocations[0].Baseline = "49"
	if pbgCheck(t, r).Mismatches.LegacyBaselineMismatches != 1 {
		t.Fatal("wrong residual")
	}
	r = pbgCustomer("settled", "200", "73")
	r.Requests[0].CreatedXID = nil
	r.Requests[0].TenantControl = nil
	r.Requests[0].AdmittedAt = PeriodicBudgetGraphTime{}
	r.Allocations = nil
	r.Events = nil
	r.Windows = nil
	r.Controls[0].HeldAmount = "0"
	r.Controls[0].SpentAmount = "0"
	r.Controls[0].EventSeq = "0"
	r.Controls[0].LastEventID = nil
	got = pbgClean(t, r)
	if got.Unproven.LegacyMembershipUnproven != 1 || got.Unproven.OperationSnapshotPrefixUnproven != 0 {
		t.Fatal(got)
	}
	r = pbgCustomer("settled", "200", "73")
	r.Requests[0].CreatedXID = nil
	r.Requests[0].TenantControl = nil
	r.Requests[0].AdmittedAt = PeriodicBudgetGraphTime{}
	r.Windows = nil
	a := &r.Allocations[0]
	a.Origin = "legacy_import"
	a.OriginSourceID = a.SubjectID
	a.WindowID = nil
	a.AdmittedAt = PeriodicBudgetGraphTime{}
	a.CreatedXID = "9"
	r.Events[0].SourceKind = "legacy_import"
	r.Events[0].SourceID = a.SubjectID
	r.Events[0].CreatedXID = "9"
	for i := range r.Events {
		r.Events[i].WindowBeforeRevision = nil
	}
	pbgClean(t, r)
}

func TestCheckPeriodicBudgetGraphTwoScopesAndDispatch(t *testing.T) {
	r := pbgCustomer("settled", "200", "73")
	c := r.Controls[0]
	c.ID = pbgID(50)
	c.Scope = "key"
	c.KeyID = pbgPtr(pbgID(3))
	c.ConfigurationOperationID = pbgID(51)
	c.LastEventID = pbgPtr(pbgID(55))
	controls := append(r.Controls, c)
	r.Controls = []PeriodicBudgetControlRow{c}
	pbgCommand(&r, "0", true, c.TrackingStartedAt)
	r.Controls = controls
	r.Requests[0].KeyControl = pbgPtr(c.ID)
	win := r.Windows[0]
	win.ID = pbgID(52)
	win.ControlID = c.ID
	win.LastEventID = pbgPtr(pbgID(55))
	r.Windows = append(r.Windows, win)
	a := r.Allocations[0]
	a.ID = pbgID(53)
	a.ControlID = c.ID
	a.WindowID = pbgPtr(win.ID)
	a.LastEventID = pbgPtr(pbgID(55))
	r.Allocations = append(r.Allocations, a)
	for i := 0; i < 2; i++ {
		e := r.Events[i]
		e.ID = pbgID(54 + i)
		e.ControlID = c.ID
		e.AllocationID = a.ID
		r.Events = append(r.Events, e)
	}
	got := pbgClean(t, r)
	if got.Unproven.OperationSnapshotPrefixUnproven != 2 {
		t.Fatal(got)
	}
	for _, state := range []string{"completed", "failed", "unknown"} {
		r := pbgSupplier()
		r.Attempts[0].DeliveryState = state
		pbgClean(t, r)
	}
	r = pbgSupplier()
	r.Attempts[0].DeliveryState = "sending"
	if pbgCheck(t, r).Mismatches.DispatchMetadataMismatches != 1 {
		t.Fatal("new sending requires actual check")
	}
	r.Attempts[0].DispatchCheckedAt = pbgTime("2026-10-01T09:00:00Z")
	pbgClean(t, r)
	r = pbgLegacySupplier()
	r.Attempts[0].DeliveryState = "sending"
	pbgClean(t, r)
	r.Attempts[0].DeliveryState = "prepared"
	r.Attempts[0].DispatchCheckedAt = pbgTime("2026-10-01T09:00:00Z")
	if pbgCheck(t, r).Mismatches.DispatchMetadataMismatches != 1 {
		t.Fatal("prepared with dispatch stamp")
	}
}

func TestCheckPeriodicBudgetGraphTimesAndNumbers(t *testing.T) {
	for _, invalid := range []PeriodicBudgetGraphTime{{}, {Valid: true, Infinity: 1}, {Valid: true, Infinity: -1}, {Valid: false, Infinity: 1}, {Valid: true, Infinity: 2}, {Value: time.Now()}, {Valid: true, Infinity: 1, Value: time.Now()}, {Valid: true, Value: time.Unix(0, 1)}} {
		r := pbgBase()
		r.Controls[0].TrackingStartedAt = invalid
		if pbgCheck(t, r).Mismatches.InvalidTimeRows != 1 {
			t.Fatalf("time %+v", invalid)
		}
	}
	r := pbgBase()
	r.Controls[0].TrackingStartedAt.Value = r.Controls[0].TrackingStartedAt.Value.In(time.FixedZone("offset", 8*3600))
	pbgClean(t, r)
	r = pbgBase()
	r.Controls[0].TrackingStartedAt = pbgTime("0001-01-01T00:00:00Z")
	r.Controls[0].EffectiveFrom = pbgTime("0001-01-02T00:00:00Z")
	r.Controls[0].MaxObservedWindowStart = pbgTime("0001-01-01T00:00:00Z")
	r.Operations = nil
	r.Versions = nil
	r.Audits = nil
	pbgCommand(&r, "0", true, r.Controls[0].TrackingStartedAt)
	pbgClean(t, r)
	r = pbgBase()
	r.Controls[0].PeriodKind = "utc_month"
	r.Controls[0].TrackingStartedAt = pbgTime("2024-02-29T23:59:59.999999Z")
	r.Controls[0].EffectiveFrom = pbgTime("2024-03-01T00:00:00Z")
	r.Controls[0].MaxObservedWindowStart = pbgTime("2024-02-01T00:00:00Z")
	r.Operations = nil
	r.Versions = nil
	r.Audits = nil
	pbgCommand(&r, "0", true, r.Controls[0].TrackingStartedAt)
	pbgClean(t, r)
	r = pbgBase()
	r.Controls[0].CreatedXID = "18446744073709551615"
	r.Operations[0].CreatedXID = r.Controls[0].CreatedXID
	r.Versions[0].CreatedXID = r.Controls[0].CreatedXID
	pbgClean(t, r)
	for _, bad := range []string{"-0", "+0", "00", "1e2", "1.0", "NaN", "Infinity", "-Infinity", " 1", "9223372036854775808"} {
		r := pbgCustomer("", "200", "0")
		r.Reservations[0].Amount = bad
		if pbgCheck(t, r).Mismatches.InvalidAmountRows != 1 {
			t.Fatal(bad)
		}
	}
	r = pbgSupplier()
	b := r.Bindings[0]
	b.ID = pbgID(94)
	r.Bindings = append(r.Bindings, b)
	if pbgCheck(t, r).Mismatches.DuplicateBindingCredentialRows != 1 {
		t.Fatal("credential duplicate")
	}
	r = pbgSupplier()
	e := r.ProcurementEvents[0]
	e.ID = pbgID(95)
	r.ProcurementEvents = append(r.ProcurementEvents, e)
	if pbgCheck(t, r).Mismatches.DuplicateProcurementEventKeyRows != 1 {
		t.Fatal("event key duplicate")
	}
	// A late cancellation cannot escape as a finished descriptive summary.
	probe := &pbgCancelContext{Context: context.Background(), after: 1 << 60}
	if _, e := CheckPeriodicBudgetGraph(probe, pbgBase()); e != nil {
		t.Fatal(e)
	}
	ctx := &pbgCancelContext{Context: context.Background(), after: probe.calls.Load()}
	got, e2 := CheckPeriodicBudgetGraph(ctx, pbgBase())
	if e2 != ErrPeriodicBudgetGraphRowsCanceled || !reflect.DeepEqual(got, PeriodicBudgetGraphCheck{}) {
		t.Fatal(got, e2)
	}
	// An unknown request must not accidentally borrow another source's empty ID.
	r = PeriodicBudgetGraphRows{Tenants: []ProcurementAttemptTenantRow{{ID: "t"}}, Keys: []PeriodicBudgetKeyRow{{ID: "k", TenantID: "t"}}, Requests: []PeriodicBudgetRequestRow{{ID: "a", TenantID: "t", KeyID: "k", CreatedXID: pbgPtr("10"), AdmittedAt: pbgTime("2026-10-01T00:00:00Z")}, {ID: "b", TenantID: "t", KeyID: "k"}}, Reservations: []CustomerKeyReservationRow{{ID: "", TenantID: "t", RequestID: "b", Amount: "0", Status: "reserved"}}}
	if pbgCheck(t, r).Mismatches.SourceIdentityMismatches != 1 {
		t.Fatal("empty ID aliased missing relationship")
	}
}
