package periodicbudgetprefix

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	f "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func id(n int) string      { return fmt.Sprintf("10000000-0000-0000-0000-%012d", n) }
func ptr(s string) *string { return &s }
func at(s string) Time {
	v, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		panic(e)
	}
	return Time{Value: v, Valid: true}
}
func js(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func fingerprint(parts ...string) string {
	var b []byte
	for _, p := range parts {
		b = binary.BigEndian.AppendUint32(b, uint32(len(p)))
		b = append(b, p...)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func receipt(c f.PeriodicBudgetControlRow, when Time, window *f.PeriodicBudgetWindowRow, held, spent, carry string) []byte {
	target := value(c.TenantID)
	if c.Scope == "key" {
		target = value(c.KeyID)
	} else if c.Scope == "procurement" {
		target = value(c.AccountID)
	}
	state := "active"
	if !c.Enabled {
		state = "disabled"
	} else if when.Value.Before(c.EffectiveFrom.Value) {
		state = "scheduled"
	}
	var win any
	if window != nil {
		win = map[string]any{"id": window.ID, "start_at": window.StartAt.Value.UTC().Format(utc6), "end_at": window.EndAt.Value.UTC().Format(utc6), "held_amount": held, "spent_amount": spent}
	}
	return js(map[string]any{"scope": c.Scope, "scope_id": target, "revision": c.Revision, "period_kind": c.PeriodKind, "enabled": c.Enabled, "limit_amount": c.LimitAmount, "state": state, "effective_from": c.EffectiveFrom.Value.UTC().Format(utc6), "tracking_started_at": c.TrackingStartedAt.Value.UTC().Format(utc6), "as_of": when.Value.UTC().Format(utc6), "current_window": win, "carry_held": carry})
}

func addCommand(r *Rows, opid, expected, xid string, when Time, changed bool, window *f.PeriodicBudgetWindowRow, held, spent, carry string) {
	c := r.Base.Controls[0]
	kind, scope, target := "tenant_periodic_budget.configure", "platform", "/api/platform/tenants/"+value(c.TenantID)+"/periodic-budget"
	scopeID := value(c.TenantID)
	var tenant *string
	if c.Scope == "key" {
		kind = "key_periodic_budget.configure"
		scope = value(c.TenantID)
		scopeID = value(c.KeyID)
		target = "/api/keys/" + scopeID + "/periodic-budget"
		tenant = c.TenantID
	} else if c.Scope == "procurement" {
		kind = "procurement_periodic_budget.configure"
		scopeID = value(c.AccountID)
		target = "/api/platform/procurement-accounts/" + scopeID + "/periodic-budget"
	}
	op := f.PeriodicBudgetOperationRow{OperationID: opid, Kind: kind, Actor: id(9), Scope: scope, Target: target, ResourceID: c.ID, Revision: c.Revision, SafeInput: js(map[string]any{"expected_revision": expected, "period_kind": c.PeriodKind, "enabled": c.Enabled, "limit_amount": c.LimitAmount}), InputFingerprint: fingerprint(kind, id(9), scope, target, expected, c.PeriodKind, strconv.FormatBool(c.Enabled), c.LimitAmount), ResultSnapshot: receipt(c, when, window, held, spent, carry), CreatedXID: xid, CreatedAt: when}
	r.Base.Operations = append(r.Base.Operations, op)
	if changed {
		r.Base.Versions = append(r.Base.Versions, f.PeriodicBudgetVersionRow{ControlID: c.ID, Revision: c.Revision, OperationID: opid, PeriodKind: c.PeriodKind, Enabled: c.Enabled, LimitAmount: c.LimitAmount, Actor: id(9), CreatedXID: xid, CreatedAt: when})
	}
	r.Base.Audits = append(r.Base.Audits, f.PeriodicBudgetAuditRow{ID: id(200 + len(r.Base.Audits)), Actor: id(9), TenantScope: tenant, Action: "periodic_budget.configured", Target: opid, SanitizedDiff: js(map[string]any{"control_id": c.ID, "revision": c.Revision, "scope": c.Scope, "scope_id": scopeID, "changed": changed}), CreatedAt: when})
}

func emptyControl(scope string) Rows {
	first := at("2026-10-01T08:00:00Z")
	c := f.PeriodicBudgetControlRow{ID: id(1), Scope: scope, TenantID: ptr(id(2)), PeriodKind: "utc_day", Enabled: true, LimitAmount: "1000", Revision: "1", TrackingStartedAt: first, EffectiveFrom: at("2026-10-02T00:00:00Z"), MaxObservedWindowStart: at("2026-10-01T00:00:00Z"), HeldAmount: "0", SpentAmount: "0", EventSeq: "0", CreatedXID: "10", ConfigurationOperationID: id(4)}
	r := Rows{Base: f.PeriodicBudgetGraphRows{Tenants: []f.ProcurementAttemptTenantRow{{ID: id(2)}}, Keys: []f.PeriodicBudgetKeyRow{{ID: id(3), TenantID: id(2)}}, Users: []f.ProcurementApprovalUserRow{{ID: id(9)}}}}
	if scope == "key" {
		c.KeyID = ptr(id(3))
	} else if scope == "procurement" {
		c.TenantID = nil
		c.AccountID = ptr(id(30))
		r.Base.Accounts = []f.ProcurementBudgetAccountRow{{ID: id(30), HeldAmount: "0", SpentAmount: "0"}}
	}
	r.Base.Controls = []f.PeriodicBudgetControlRow{c}
	addCommand(&r, id(4), "0", "10", first, true, nil, "0", "0", "0")
	r.Origins = []Origin{{ControlID: c.ID, Version: "1", CreatedXID: "10", TrackingStartedAt: first}}
	r.Observations = []Observation{{ID: id(101), ControlID: c.ID, Ordinal: "1", Kind: "command", SourceID: id(4), EventSeq: "0", ConfigurationRevision: "1", ConfigurationOperationID: id(4), CreatedXID: "10", ObservedAt: first}}
	r.OperationBindings = []OperationBinding{{OperationID: id(4), ObservationID: ptr(id(101))}}
	r.Heads = []Head{{ControlID: c.ID, Ordinal: "1", LastObservationID: ptr(id(101)), UpdatedXID: "10"}}
	return r
}

// Hand-calculated chronology: first command sees no window; birth; reserve
// 200; second command sees 200/0; settle 73; third command sees 0/73. The
// final source graph alone cannot authenticate the three old snapshots.
func customerRows(amount, charge string) Rows {
	r := emptyControl("tenant")
	c := &r.Base.Controls[0]
	admit := at("2026-10-01T08:10:00Z")
	done := at("2026-10-01T10:00:00Z")
	r.Base.Requests = []f.PeriodicBudgetRequestRow{{ID: id(10), TenantID: id(2), KeyID: id(3), CreatedXID: ptr("20"), TenantControl: ptr(c.ID), AdmittedAt: admit}}
	r.Base.Reservations = []f.CustomerKeyReservationRow{{ID: id(11), TenantID: id(2), RequestID: id(10), Amount: amount, Status: "settled"}}
	r.Base.Settlements = []f.PeriodicBudgetSettlementRow{{ID: id(17), TenantID: id(2), RequestID: id(10), ChargedAmount: charge, Status: "settled"}}
	r.Base.Windows = []f.PeriodicBudgetWindowRow{{ID: id(14), ControlID: c.ID, StartAt: at("2026-10-01T00:00:00Z"), EndAt: at("2026-10-02T00:00:00Z"), HeldAmount: "0", SpentAmount: charge, Revision: "2", LastEventID: ptr(id(18))}}
	r.Base.Allocations = []f.PeriodicBudgetAllocationRow{{ID: id(12), ControlID: c.ID, SubjectType: "customer_request", SubjectID: id(10), Origin: "new", OriginSourceID: id(11), WindowID: ptr(id(14)), AdmittedAt: admit, UpperAmount: amount, Baseline: "0", SourceHeld: "0", SourceSpent: charge, HeldAmount: "0", SpentAmount: charge, State: "settled", Revision: "2", LastEventID: ptr(id(18)), CreatedXID: "20", CreatedAt: admit}}
	negative := "-" + amount
	if amount == "0" {
		negative = "0"
	}
	r.Base.Events = []f.PeriodicBudgetEventRow{{ID: id(13), ControlID: c.ID, AllocationID: id(12), SourceKind: "reservation", SourceID: id(11), Seq: "1", AllocationBeforeRevision: "0", WindowBeforeRevision: ptr("0"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: amount, AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: amount, SpentDelta: "0", CreatedXID: "20", CreatedAt: admit}, {ID: id(18), ControlID: c.ID, AllocationID: id(12), SourceKind: "settlement", SourceID: id(17), Seq: "2", AllocationBeforeRevision: "1", WindowBeforeRevision: ptr("1"), BeforeHeld: amount, BeforeSpent: "0", AfterHeld: "0", AfterSpent: charge, AfterSourceSpent: charge, AfterState: "settled", HeldDelta: negative, SpentDelta: charge, CreatedXID: "22", CreatedAt: done}}
	c.SpentAmount = charge
	c.EventSeq = "2"
	c.LastEventID = ptr(id(18))
	second, third := at("2026-10-01T08:20:00Z"), at("2026-10-01T11:00:00Z")
	addCommand(&r, id(15), "1", "30", second, false, &r.Base.Windows[0], amount, "0", "0")
	addCommand(&r, id(16), "1", "40", third, false, &r.Base.Windows[0], "0", charge, "0")
	r.Observations = append(r.Observations, Observation{ID: id(102), ControlID: c.ID, Ordinal: "2", PreviousID: ptr(id(101)), Kind: "window_birth", SourceID: id(14), EventSeq: "0", ConfigurationRevision: "1", ConfigurationOperationID: id(4), WindowID: ptr(id(14)), WindowRevision: ptr("0"), CreatedXID: "20"}, Observation{ID: id(103), ControlID: c.ID, Ordinal: "3", PreviousID: ptr(id(102)), Kind: "command", SourceID: id(15), EventSeq: "1", LastEventID: ptr(id(13)), ConfigurationRevision: "1", ConfigurationOperationID: id(4), WindowID: ptr(id(14)), WindowRevision: ptr("1"), WindowLastEventID: ptr(id(13)), CreatedXID: "30", ObservedAt: second}, Observation{ID: id(104), ControlID: c.ID, Ordinal: "4", PreviousID: ptr(id(103)), Kind: "command", SourceID: id(16), EventSeq: "2", LastEventID: ptr(id(18)), ConfigurationRevision: "1", ConfigurationOperationID: id(4), WindowID: ptr(id(14)), WindowRevision: ptr("2"), WindowLastEventID: ptr(id(18)), CreatedXID: "40", ObservedAt: third})
	r.OperationBindings = append(r.OperationBindings, OperationBinding{OperationID: id(15), ObservationID: ptr(id(103))}, OperationBinding{OperationID: id(16), ObservationID: ptr(id(104))})
	r.WindowBindings = []WindowBinding{{WindowID: id(14), ObservationID: ptr(id(102)), ObservationCreatedXID: ptr("20")}}
	r.Heads[0] = Head{ControlID: c.ID, Ordinal: "4", LastObservationID: ptr(id(104)), UpdatedXID: "40"}
	return r
}

func checked(t *testing.T, r Rows) Report {
	t.Helper()
	p, e := Check(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	base, e := f.CheckPeriodicBudgetGraph(context.Background(), r.Base)
	if e != nil || !reflect.DeepEqual(base, p.Base) {
		t.Fatal("original report changed", e, base, p.Base)
	}
	return p
}
func balanced(t *testing.T, r Rows, want uint64) Report {
	t.Helper()
	p := checked(t, r)
	if !p.Balanced || !p.CoverageComplete || p.Status != "balanced" || p.Scope != "periodic_budget_snapshot_prefix_v1" || p.CertifiedCommands != want || p.Mismatches != (Mismatches{}) || p.Unproven != (Gaps{}) {
		t.Fatalf("unexpected report: %+v", p)
	}
	return p
}

func TestHandCalculatedPrefixesAndOriginalBoundary(t *testing.T) {
	r := customerRows("200", "73")
	p := balanced(t, r, 3)
	if p.Base.Status != "indeterminate" || p.Base.Unproven.OperationSnapshotPrefixUnproven != 3 || p.Base.CoverageComplete {
		t.Fatal("original gap was rewritten", p.Base)
	}
	if p.Scanned.Origins != 1 || p.Scanned.Heads != 1 || p.Scanned.Observations != 4 || p.Scanned.OperationBindings != 3 || p.Scanned.WindowBindings != 1 {
		t.Fatal(p.Scanned)
	}
	before := js(r)
	checked(t, r)
	if string(before) != string(js(r)) {
		t.Fatal("checker mutated input")
	}
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run(scope, func(t *testing.T) { balanced(t, emptyControl(scope), 1) })
	}
	balanced(t, Rows{}, 0)
}

func TestSameAmountCutsStillRequireRevisionAndTail(t *testing.T) {
	r := customerRows("0", "0")
	balanced(t, r, 3)
	r.Observations[3].EventSeq = "1"
	r.Observations[3].LastEventID = ptr(id(13))
	p := checked(t, r)
	if p.Status != "mismatch" || p.Mismatches.SnapshotMismatches == 0 || p.Balanced {
		t.Fatal("equal amounts adopted wrong prefix", p)
	}
}

func TestEmptyWindowBeforeOrAfterCommandAtSameFinancialCut(t *testing.T) {
	r := customerRows("0", "0")
	balanced(t, r, 3)
	// Both cuts remain zero, but the first command can no longer claim null.
	r.Observations[0].Ordinal = "2"
	r.Observations[0].PreviousID = ptr(id(102))
	r.Observations[1].Ordinal = "1"
	r.Observations[1].PreviousID = nil
	r.Observations[2].PreviousID = ptr(id(101))
	p := checked(t, r)
	if p.Mismatches.SnapshotMismatches == 0 || p.Balanced {
		t.Fatal("birth ordering ignored", p)
	}
	// Changing only the receipt to the actual empty window is a valid history.
	r.Base.Operations[0].ResultSnapshot = receipt(r.Base.Controls[0], r.Base.Operations[0].CreatedAt, &r.Base.Windows[0], "0", "0", "0")
	r.Observations[0].WindowID = ptr(id(14))
	r.Observations[0].WindowRevision = ptr("0")
	balanced(t, r, 3)
}

func TestLaterWindowDoesNotInvalidateEarlierNull(t *testing.T) {
	balanced(t, customerRows("200", "73"), 3)
}

func TestCrossWindowCarryAndLaterSettlement(t *testing.T) {
	r := customerRows("200", "73")
	c := &r.Base.Controls[0]
	c.MaxObservedWindowStart = at("2026-10-02T00:00:00Z")
	newWindow := f.PeriodicBudgetWindowRow{ID: id(19), ControlID: c.ID, StartAt: at("2026-10-02T00:00:00Z"), EndAt: at("2026-10-03T00:00:00Z"), HeldAmount: "0", SpentAmount: "0", Revision: "0"}
	r.Base.Windows = append(r.Base.Windows, newWindow)
	for i, s := range []string{"2026-10-02T08:20:00Z", "2026-10-02T11:00:00Z"} {
		r.Base.Operations[i+1].CreatedAt = at(s)
		r.Base.Audits[i+1].CreatedAt = at(s)
		r.Observations[i+2].ObservedAt = at(s)
	}
	r.Base.Operations[1].ResultSnapshot = receipt(*c, r.Base.Operations[1].CreatedAt, &newWindow, "0", "0", "200")
	r.Base.Operations[2].ResultSnapshot = receipt(*c, r.Base.Operations[2].CreatedAt, &newWindow, "0", "0", "0")
	r.Base.Events[1].CreatedAt = at("2026-10-02T10:00:00Z")
	birth := Observation{ID: id(105), ControlID: c.ID, Ordinal: "3", PreviousID: ptr(id(102)), Kind: "window_birth", SourceID: newWindow.ID, EventSeq: "1", LastEventID: ptr(id(13)), ConfigurationRevision: "1", ConfigurationOperationID: id(4), WindowID: ptr(newWindow.ID), WindowRevision: ptr("0"), CreatedXID: "30"}
	r.Observations[2].Ordinal = "4"
	r.Observations[2].PreviousID = ptr(id(105))
	r.Observations[3].Ordinal = "5"
	for i := 2; i < 4; i++ {
		r.Observations[i].WindowID = ptr(newWindow.ID)
		r.Observations[i].WindowRevision = ptr("0")
		r.Observations[i].WindowLastEventID = nil
	}
	r.Observations = append(r.Observations, birth)
	r.WindowBindings = append(r.WindowBindings, WindowBinding{WindowID: newWindow.ID, ObservationID: ptr(id(105)), ObservationCreatedXID: ptr("30")})
	r.Heads[0].Ordinal = "5"
	balanced(t, r, 3)
	r.Base.Operations[1].ResultSnapshot = receipt(*c, r.Base.Operations[1].CreatedAt, &newWindow, "0", "0", "0")
	if checked(t, r).Mismatches.SnapshotMismatches == 0 {
		t.Fatal("lost cross-window carry")
	}
}

func TestChangedConfigurationAndNoOps(t *testing.T) {
	r := customerRows("200", "73")
	c := &r.Base.Controls[0]
	c.Revision = "2"
	c.Enabled = false
	c.ConfigurationOperationID = id(50)
	when := at("2026-10-01T12:00:00Z")
	addCommand(&r, id(50), "1", "50", when, true, &r.Base.Windows[0], "0", "73", "0")
	r.Observations = append(r.Observations, Observation{ID: id(106), ControlID: c.ID, Ordinal: "5", PreviousID: ptr(id(104)), Kind: "command", SourceID: id(50), EventSeq: "2", LastEventID: ptr(id(18)), ConfigurationRevision: "2", ConfigurationOperationID: id(50), WindowID: ptr(id(14)), WindowRevision: ptr("2"), WindowLastEventID: ptr(id(18)), CreatedXID: "50", ObservedAt: when})
	r.OperationBindings = append(r.OperationBindings, OperationBinding{OperationID: id(50), ObservationID: ptr(id(106))})
	r.Heads[0] = Head{ControlID: c.ID, Ordinal: "5", LastObservationID: ptr(id(106)), UpdatedXID: "50"}
	balanced(t, r, 4)
}

func TestLegacyDomainRemainsUncovered(t *testing.T) {
	r := customerRows("200", "73")
	r.Origins = nil
	r.Heads = nil
	r.Observations = nil
	for i := range r.OperationBindings {
		r.OperationBindings[i].ObservationID = nil
	}
	r.WindowBindings[0].ObservationID = nil
	r.WindowBindings[0].ObservationCreatedXID = nil
	p := checked(t, r)
	if p.Status != "indeterminate" || p.Unproven.UncoveredControls != 1 || p.Unproven.OperationSnapshotPrefixUnproven != 3 || p.CertifiedCommands != 0 || p.Mismatches != (Mismatches{}) {
		t.Fatal(p)
	}
	r = emptyControl("tenant")
	r.Origins = nil
	r.Heads = nil
	r.Observations = nil
	r.OperationBindings[0].ObservationID = nil
	p = checked(t, r)
	if p.Base.Status != "balanced" || p.Status != "indeterminate" || p.Unproven.UncoveredControls != 1 {
		t.Fatal("legacy exemption became enrollment", p)
	}
}

func TestCertifiedCommandPreservesLegacyMembershipGap(t *testing.T) {
	r := emptyControl("tenant")
	// A completed customer request predates PB admission markers. A covered
	// control and a fully proved command cannot prove its past membership.
	r.Base.Requests = []f.PeriodicBudgetRequestRow{{ID: id(10), TenantID: id(2), KeyID: id(3)}}
	r.Base.Reservations = []f.CustomerKeyReservationRow{{ID: id(11), TenantID: id(2), RequestID: id(10), Amount: "200", Status: "settled"}}
	r.Base.Settlements = []f.PeriodicBudgetSettlementRow{{ID: id(17), TenantID: id(2), RequestID: id(10), ChargedAmount: "73", Status: "settled"}}
	p := checked(t, r)
	if p.Base.Mismatches != (f.PeriodicBudgetGraphMismatches{}) || p.Mismatches != (Mismatches{}) || p.CertifiedCommands != 1 || p.Base.Unproven.LegacyMembershipUnproven != 1 || p.Unproven.LegacyMembershipUnproven != 1 || p.Unproven.OperationSnapshotPrefixUnproven != 0 || p.Status != "indeterminate" || p.Balanced || p.CoverageComplete {
		t.Fatal("certification erased independent membership gap", p)
	}
}

func TestCertifiedCommandPreservesLegacyEffectBoundaryGap(t *testing.T) {
	r := emptyControl("procurement")
	c := &r.Base.Controls[0]
	r.Base.Requests = []f.PeriodicBudgetRequestRow{{ID: id(10), TenantID: id(2), KeyID: id(3)}}
	r.Base.Reservations = []f.CustomerKeyReservationRow{{ID: id(11), TenantID: id(2), RequestID: id(10), Amount: "200", Status: "reserved"}}
	r.Base.Attempts = []f.PeriodicBudgetAttemptRow{{ID: id(20), TenantID: id(2), RequestID: id(10), CredentialID: "credential", ChannelID: "channel", DeliveryState: "prepared"}}
	r.Base.Accounts = []f.ProcurementBudgetAccountRow{{ID: id(30), HeldAmount: "200", SpentAmount: "50"}}
	r.Base.Bindings = []f.PeriodicBudgetBindingRow{{ID: id(31), AccountID: id(30), CredentialID: "credential", ChannelID: "channel"}}
	r.Base.Procurement = []f.PeriodicBudgetSupplierRow{{AttemptID: id(20), AccountID: id(30), BindingID: id(31), UpperAmount: "200", HeldAmount: "200", SpentAmount: "50", State: "disputed", Anomaly: true}}
	// The unmarked reserve/cost/dispute sum to held 200 and source spent 50.
	// Import can prove these totals, but cannot prove an old effect boundary.
	r.Base.ProcurementEvents = []f.PeriodicBudgetProcurementEventRow{
		{ID: id(32), EventKey: "reserve:" + id(20), AccountID: id(30), AttemptID: id(20), HeldDelta: "200", SpentDelta: "0", Type: "reserved"},
		{ID: id(33), EventKey: "old-cost", AccountID: id(30), AttemptID: id(20), HeldDelta: "-200", SpentDelta: "50", Type: "cost_confirmed"},
		{ID: id(34), EventKey: "old-dispute", AccountID: id(30), AttemptID: id(20), HeldDelta: "200", SpentDelta: "0", Type: "evidence_disputed"},
	}
	r.Base.Allocations = []f.PeriodicBudgetAllocationRow{{ID: id(12), ControlID: c.ID, SubjectType: "supplier_attempt", SubjectID: id(20), Origin: "legacy_import", OriginSourceID: id(20), UpperAmount: "200", Baseline: "50", SourceHeld: "200", SourceSpent: "50", HeldAmount: "200", SpentAmount: "0", State: "disputed", Revision: "1", LastEventID: ptr(id(13)), CreatedXID: "10", CreatedAt: c.TrackingStartedAt}}
	r.Base.Events = []f.PeriodicBudgetEventRow{{ID: id(13), ControlID: c.ID, AllocationID: id(12), SourceKind: "legacy_import", SourceID: id(20), Seq: "1", AllocationBeforeRevision: "0", BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "200", AfterSpent: "0", AfterSourceSpent: "50", AfterState: "disputed", HeldDelta: "200", SpentDelta: "0", CreatedXID: "10", CreatedAt: c.TrackingStartedAt}}
	c.HeldAmount, c.EventSeq, c.LastEventID = "200", "1", ptr(id(13))
	r.Base.Operations[0].ResultSnapshot = receipt(*c, r.Base.Operations[0].CreatedAt, nil, "0", "0", "200")
	r.Observations[0].EventSeq, r.Observations[0].LastEventID = "1", ptr(id(13))
	p := checked(t, r)
	if p.Base.Mismatches != (f.PeriodicBudgetGraphMismatches{}) || p.Mismatches != (Mismatches{}) || p.CertifiedCommands != 1 || p.Base.Unproven.LegacyEffectBoundaryUnproven != 1 || p.Unproven.LegacyEffectBoundaryUnproven != 1 || p.Base.Unproven.OperationSnapshotPrefixUnproven != 1 || p.Unproven.OperationSnapshotPrefixUnproven != 0 || p.Status != "indeterminate" || p.Balanced || p.CoverageComplete {
		t.Fatal("certification erased independent effect boundary gap", p)
	}
}

func TestDeclaredCoverageNeverFallsBackOnMissingSources(t *testing.T) {
	for name, mutate := range map[string]func(*Rows){
		"missing command binding":     func(r *Rows) { r.OperationBindings = r.OperationBindings[1:] },
		"missing command pointer":     func(r *Rows) { r.OperationBindings[0].ObservationID = nil },
		"missing command observation": func(r *Rows) { r.Observations = r.Observations[1:] },
		"missing window binding":      func(r *Rows) { r.WindowBindings = nil },
		"missing window stamp":        func(r *Rows) { r.WindowBindings[0].ObservationCreatedXID = nil },
		"missing head":                func(r *Rows) { r.Heads = nil },
		"orphan origin":               func(r *Rows) { r.Origins[0].ControlID = id(999) },
		"orphan head":                 func(r *Rows) { r.Heads[0].ControlID = id(999) },
		"orphan operation binding": func(r *Rows) {
			r.OperationBindings = append(r.OperationBindings, OperationBinding{OperationID: id(999)})
		},
		"orphan window binding":       func(r *Rows) { r.WindowBindings = append(r.WindowBindings, WindowBinding{WindowID: id(999)}) },
		"duplicate origin":            func(r *Rows) { r.Origins = append(r.Origins, r.Origins[0]) },
		"duplicate head":              func(r *Rows) { r.Heads = append(r.Heads, r.Heads[0]) },
		"duplicate journal":           func(r *Rows) { r.Observations = append(r.Observations, r.Observations[0]) },
		"duplicate operation binding": func(r *Rows) { r.OperationBindings = append(r.OperationBindings, r.OperationBindings[0]) },
		"duplicate window binding":    func(r *Rows) { r.WindowBindings = append(r.WindowBindings, r.WindowBindings[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			r := customerRows("200", "73")
			mutate(&r)
			p := checked(t, r)
			if p.Status != "mismatch" || p.Balanced || p.CoverageComplete {
				t.Fatal(p)
			}
		})
	}
}

func TestObservationFieldsAndLineageMutations(t *testing.T) {
	for name, mutate := range map[string]func(*Rows){
		"zero id":                       func(r *Rows) { r.Observations[2].ID = "00000000-0000-0000-0000-000000000000" },
		"unknown control":               func(r *Rows) { r.Observations[2].ControlID = id(999) },
		"wrong source":                  func(r *Rows) { r.Observations[2].SourceID = id(999) },
		"wrong kind":                    func(r *Rows) { r.Observations[2].Kind = "window_birth" },
		"unknown kind":                  func(r *Rows) { r.Observations[2].Kind = "anything" },
		"wrong xid":                     func(r *Rows) { r.Observations[2].CreatedXID = "999" },
		"wrong observed at":             func(r *Rows) { r.Observations[2].ObservedAt = at("2026-10-01T08:20:00.000001Z") },
		"null command at":               func(r *Rows) { r.Observations[2].ObservedAt = Time{} },
		"finite birth at":               func(r *Rows) { r.Observations[1].ObservedAt = at("2026-10-01T08:20:00Z") },
		"birth xid":                     func(r *Rows) { r.Observations[1].CreatedXID = "999" },
		"wrong revision":                func(r *Rows) { r.Observations[2].ConfigurationRevision = "2" },
		"wrong configuration operation": func(r *Rows) { r.Observations[2].ConfigurationOperationID = id(15) },
		"wrong window":                  func(r *Rows) { r.Observations[2].WindowID = ptr(id(999)) },
		"window revision":               func(r *Rows) { r.Observations[2].WindowRevision = ptr("2") },
		"window tail":                   func(r *Rows) { r.Observations[2].WindowLastEventID = ptr(id(18)) },
		"cut ahead":                     func(r *Rows) { r.Observations[2].EventSeq = "3" },
		"cut tail":                      func(r *Rows) { r.Observations[2].LastEventID = ptr(id(18)) },
		"zero cut tail":                 func(r *Rows) { r.Observations[0].LastEventID = ptr(id(13)) },
		"decreasing cut":                func(r *Rows) { r.Observations[3].EventSeq = "0"; r.Observations[3].LastEventID = nil },
		"birth includes own event":      func(r *Rows) { r.Observations[1].EventSeq = "1"; r.Observations[1].LastEventID = ptr(id(13)) },
		"birth revision": func(r *Rows) {
			r.Observations[1].WindowRevision = ptr("1")
			r.Observations[1].WindowLastEventID = ptr(id(13))
		},
		"birth source":      func(r *Rows) { r.Observations[1].WindowID = ptr(id(999)) },
		"ordinal gap":       func(r *Rows) { r.Observations[2].Ordinal = "8" },
		"ordinal duplicate": func(r *Rows) { r.Observations[2].Ordinal = "2" },
		"first previous":    func(r *Rows) { r.Observations[0].PreviousID = ptr(id(104)) },
		"wrong previous":    func(r *Rows) { r.Observations[2].PreviousID = ptr(id(101)) },
		"head ordinal":      func(r *Rows) { r.Heads[0].Ordinal = "3" },
		"head tail":         func(r *Rows) { r.Heads[0].LastObservationID = ptr(id(103)) },
		"head stamp":        func(r *Rows) { r.Heads[0].UpdatedXID = "30" },
		"origin stamp":      func(r *Rows) { r.Origins[0].CreatedXID = "20" },
		"origin version":    func(r *Rows) { r.Origins[0].Version = "2" },
		"origin time":       func(r *Rows) { r.Origins[0].TrackingStartedAt = at("2026-10-01T08:00:00.000001Z") },
	} {
		t.Run(name, func(t *testing.T) {
			r := customerRows("200", "73")
			mutate(&r)
			if p := checked(t, r); p.Status != "mismatch" || p.Balanced {
				t.Fatal(p)
			}
		})
	}
	for _, s := range []string{"", "-1", "01", "+1", "1.0", "1e0", "9223372036854775808", strings.Repeat("9", 100), "\x00", "\xff"} {
		t.Run(fmt.Sprintf("decimal_%q", s), func(t *testing.T) {
			r := customerRows("200", "73")
			r.Observations[2].EventSeq = s
			if p := checked(t, r); p.Status != "mismatch" {
				t.Fatal(p)
			}
		})
	}
}

func TestEveryMetadataTextFieldRejectsInvalidEncoding(t *testing.T) {
	for _, group := range []string{"origin", "head", "command", "birth", "operation_binding", "window_binding"} {
		row := func(r *Rows) reflect.Value {
			switch group {
			case "origin":
				return reflect.ValueOf(&r.Origins[0]).Elem()
			case "head":
				return reflect.ValueOf(&r.Heads[0]).Elem()
			case "command":
				return reflect.ValueOf(&r.Observations[2]).Elem()
			case "birth":
				return reflect.ValueOf(&r.Observations[1]).Elem()
			case "operation_binding":
				return reflect.ValueOf(&r.OperationBindings[0]).Elem()
			default:
				return reflect.ValueOf(&r.WindowBindings[0]).Elem()
			}
		}
		baseline := customerRows("200", "73")
		typ := row(&baseline).Type()
		for i := 0; i < typ.NumField(); i++ {
			if kind := typ.Field(i).Type.Kind(); kind != reflect.String && kind != reflect.Pointer {
				continue
			}
			for _, invalid := range []string{"\x00", "\xff"} {
				t.Run(fmt.Sprintf("%s_%s_%q", group, typ.Field(i).Name, invalid), func(t *testing.T) {
					r := customerRows("200", "73")
					field := row(&r).Field(i)
					if field.Kind() == reflect.Pointer {
						field.Set(reflect.ValueOf(ptr(invalid)))
					} else {
						field.SetString(invalid)
					}
					if p := checked(t, r); p.Status != "mismatch" || p.Mismatches.StructuralMismatches == 0 {
						t.Fatal("invalid text accepted", p)
					}
				})
			}
		}
	}
}

func TestObservationTimeMetadataBoundaries(t *testing.T) {
	for _, invalid := range []Time{{}, {Valid: true, Infinity: 1}, {Valid: false, Infinity: -1}, {Value: time.Now()}, {Valid: true, Value: time.Unix(0, 1)}, {Valid: true, Value: time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)}} {
		for _, field := range []string{"origin", "command"} {
			t.Run(fmt.Sprintf("%s_%+v", field, invalid), func(t *testing.T) {
				r := customerRows("200", "73")
				if field == "origin" {
					r.Origins[0].TrackingStartedAt = invalid
				} else {
					r.Observations[2].ObservedAt = invalid
				}
				if p := checked(t, r); p.Status != "mismatch" || p.Mismatches.StructuralMismatches == 0 {
					t.Fatal("invalid native timestamp accepted", p)
				}
			})
		}
	}
	r := customerRows("200", "73")
	r.Origins[0].TrackingStartedAt.Value = r.Origins[0].TrackingStartedAt.Value.In(time.FixedZone("offset", 8*3600))
	r.Observations[2].ObservedAt.Value = r.Observations[2].ObservedAt.Value.In(time.FixedZone("offset", 8*3600))
	balanced(t, r, 3)
}

func TestBaseCorruptionCannotBeQualifiedByWitnesses(t *testing.T) {
	for name, mutate := range map[string]func(*Rows){"wrong delta": func(r *Rows) { r.Base.Events[0].HeldDelta = "199" }, "bad sequence": func(r *Rows) { r.Base.Events[0].Seq = "2" }, "duplicate event": func(r *Rows) { r.Base.Events = append(r.Base.Events, r.Base.Events[0]) }, "bad amount": func(r *Rows) { r.Base.Events[0].HeldDelta = "bad" }, "orphan event": func(r *Rows) { r.Base.Events[0].AllocationID = id(999) }, "bad snapshot": func(r *Rows) { r.Base.Operations[1].ResultSnapshot = []byte(`{"carry_held":"0"}`) }} {
		t.Run(name, func(t *testing.T) {
			r := customerRows("200", "73")
			mutate(&r)
			p := checked(t, r)
			if p.Status != "mismatch" || p.CertifiedCommands != 0 || p.Balanced || p.Base.Mismatches == (f.PeriodicBudgetGraphMismatches{}) {
				t.Fatal(p)
			}
		})
	}
}

func TestSnapshotWireStrictnessAndEscapedKeys(t *testing.T) {
	r := customerRows("200", "73")
	original := string(r.Base.Operations[1].ResultSnapshot)
	r.Base.Operations[1].ResultSnapshot = []byte(strings.Replace(original, `"carry_held"`, `"\u0063arry_held"`, 1))
	balanced(t, r, 3)
	for name, raw := range map[string]string{
		"duplicate escaped key": strings.Replace(original, `"carry_held":"0"`, `"carry_held":"0","\u0063arry_held":"0"`, 1),
		"duplicate window key":  strings.Replace(original, `"held_amount":"200"`, `"held_amount":"200","\u0068eld_amount":"200"`, 1),
		"unknown field":         strings.Replace(original, `"carry_held":"0"`, `"carry_held":"0","ignored":0`, 1),
		"null carry":            strings.Replace(original, `"carry_held":"0"`, `"carry_held":null`, 1),
		"null held":             strings.Replace(original, `"held_amount":"200"`, `"held_amount":null`, 1),
		"number held":           strings.Replace(original, `"held_amount":"200"`, `"held_amount":200`, 1),
		"negative held":         strings.Replace(original, `"held_amount":"200"`, `"held_amount":"-200"`, 1),
		"noncanonical held":     strings.Replace(original, `"held_amount":"200"`, `"held_amount":"0200"`, 1),
		"oversized amount":      strings.Replace(original, `"held_amount":"200"`, `"held_amount":"`+strings.Repeat("9", 101)+`"`, 1),
		"lone high surrogate":   strings.Replace(original, `"held_amount":"200"`, `"held_amount":"\ud800"`, 1),
		"lone low surrogate":    strings.Replace(original, `"held_amount":"200"`, `"held_amount":"\udc00"`, 1),
		"invalid utf8":          strings.Replace(original, `"held_amount":"200"`, "\"held_amount\":\"\xff\"", 1),
		"trailing object":       original + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := customerRows("200", "73")
			r.Base.Operations[1].ResultSnapshot = []byte(raw)
			if p := checked(t, r); p.Status != "mismatch" || p.CertifiedCommands != 0 {
				t.Fatal("invalid snapshot qualified", p)
			}
		})
	}
}

func TestSharedBudgetsAndSafeErrors(t *testing.T) {
	if p, e := Check(nil, Rows{}); !errors.Is(e, ErrInput) || p != (Report{}) {
		t.Fatal(p, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, e := Check(ctx, Rows{}); !errors.Is(e, ErrCanceled) || p != (Report{}) {
		t.Fatal(p, e)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if p, e := Check(ctx, Rows{}); !errors.Is(e, ErrTimeout) || p != (Report{}) {
		t.Fatal(p, e)
	}
	for name, r := range map[string]Rows{"base plus evidence rows": {Base: f.PeriodicBudgetGraphRows{Tenants: make([]f.ProcurementAttemptTenantRow, 100000)}, Origins: []Origin{{}}}, "witness rows": {Observations: make([]Observation, 100001)}, "field": {Origins: []Origin{{ControlID: strings.Repeat("x", 65537)}}}, "shared bytes": {Base: f.PeriodicBudgetGraphRows{Tenants: make([]f.ProcurementAttemptTenantRow, 256)}, Origins: []Origin{{ControlID: "x"}}}} {
		t.Run(name, func(t *testing.T) {
			if name == "shared bytes" {
				for i := range r.Base.Tenants {
					r.Base.Tenants[i].ID = strings.Repeat("x", 65536)
				}
			}
			p, e := Check(context.Background(), r)
			if !errors.Is(e, ErrResource) || p != (Report{}) {
				t.Fatal(p, e)
			}
		})
	}
	ctx = deadlineWithoutErr{Context: context.Background()}
	if p, e := Check(ctx, Rows{}); !errors.Is(e, ErrTimeout) || p != (Report{}) {
		t.Fatal("absolute deadline ignored", p, e)
	}
}

type deadlineWithoutErr struct{ context.Context }

func (deadlineWithoutErr) Deadline() (time.Time, bool) { return time.Now().Add(-time.Second), true }

type cancelAfterChecks struct {
	context.Context
	after, calls int
	timeout      bool
}

func (c *cancelAfterChecks) Err() error {
	c.calls++
	if c.calls < c.after {
		return nil
	}
	if c.timeout {
		return context.DeadlineExceeded
	}
	return context.Canceled
}

func TestCancellationNeverReturnsPartialEvidence(t *testing.T) {
	baseline := &cancelAfterChecks{Context: context.Background(), after: 1000000}
	if p, e := Check(baseline, customerRows("200", "73")); e != nil || !p.Balanced {
		t.Fatal(p, e)
	}
	for after := 1; after <= baseline.calls; after++ {
		for _, timeout := range []bool{false, true} {
			t.Run(fmt.Sprintf("after_%d_timeout_%t", after, timeout), func(t *testing.T) {
				ctx := &cancelAfterChecks{Context: context.Background(), after: after, timeout: timeout}
				p, e := Check(ctx, customerRows("200", "73"))
				want := ErrCanceled
				if timeout {
					want = ErrTimeout
				}
				if !errors.Is(e, want) || p != (Report{}) {
					t.Fatal("partial result after cancellation", p, e)
				}
			})
		}
	}
}

func TestOnlySafeCountsAreReturned(t *testing.T) {
	p := balanced(t, customerRows("200", "73"), 3)
	b := string(js(p))
	for _, secret := range []string{id(1), id(14), id(15), "result_snapshot", "input_fingerprint", `"source_id"`} {
		if strings.Contains(b, secret) {
			t.Fatal("identifier or body leaked", secret)
		}
	}
}

func FuzzCheckSnapshot(fz *testing.F) {
	seed := customerRows("200", "73").Base.Operations[1].ResultSnapshot
	fz.Add(seed)
	fz.Add([]byte(`null`))
	fz.Add([]byte(`{"carry_held":"0","carry_held":"1"}`))
	fz.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 65537 {
			raw = raw[:65537]
		}
		r := customerRows("200", "73")
		r.Base.Operations[1].ResultSnapshot = raw
		p, e := Check(context.Background(), r)
		if e != nil {
			if !errors.Is(e, ErrResource) {
				t.Fatal(e)
			}
			if p != (Report{}) {
				t.Fatal("partial result")
			}
			return
		}
		if p.Balanced && (p.Base.Mismatches != (f.PeriodicBudgetGraphMismatches{}) || p.Mismatches != (Mismatches{}) || p.CertifiedCommands != 3) {
			t.Fatal(p)
		}
	})
}
