package fundinglotverify

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func pbgID(n int) string      { return fmt.Sprintf("10000000-0000-0000-0000-%012d", n) }
func pbgPtr(s string) *string { return &s }
func pbgTime(s string) PeriodicBudgetGraphTime {
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return PeriodicBudgetGraphTime{Value: at, Valid: true}
}
func pbgJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}

// Fixture fingerprints are independently framed, outside the checked package helper.
func pbgFP(parts ...string) string {
	var frame []byte
	for _, s := range parts {
		frame = binary.BigEndian.AppendUint32(frame, uint32(len(s)))
		frame = append(frame, s...)
	}
	sum := sha256.Sum256(frame)
	return hex.EncodeToString(sum[:])
}
func pbgSnapshot(c PeriodicBudgetControlRow, at PeriodicBudgetGraphTime, carry string) []byte {
	id := ""
	if c.TenantID != nil {
		id = *c.TenantID
	}
	if c.Scope == "key" {
		id = *c.KeyID
	}
	if c.Scope == "procurement" {
		id = *c.AccountID
	}
	state := "scheduled"
	if !at.Value.Before(c.EffectiveFrom.Value) {
		state = "active"
	}
	if !c.Enabled {
		state = "disabled"
	}
	return pbgJSON(map[string]any{"scope": c.Scope, "scope_id": id, "revision": c.Revision, "period_kind": c.PeriodKind, "enabled": c.Enabled, "limit_amount": c.LimitAmount, "state": state, "effective_from": c.EffectiveFrom.Value.UTC().Format("2006-01-02T15:04:05.000000Z"), "tracking_started_at": c.TrackingStartedAt.Value.UTC().Format("2006-01-02T15:04:05.000000Z"), "current_window": nil, "carry_held": carry, "as_of": at.Value.UTC().Format("2006-01-02T15:04:05.000000Z")})
}
func pbgCommand(r *PeriodicBudgetGraphRows, expected string, changed bool, at PeriodicBudgetGraphTime) {
	c := r.Controls[0]
	id := ""
	if c.TenantID != nil {
		id = *c.TenantID
	}
	kind, scope, target := "tenant_periodic_budget.configure", "platform", "/api/platform/tenants/"+id+"/periodic-budget"
	if c.Scope == "key" {
		kind = "key_periodic_budget.configure"
		scope = *c.TenantID
		id = *c.KeyID
		target = "/api/keys/" + id + "/periodic-budget"
	}
	if c.Scope == "procurement" {
		kind = "procurement_periodic_budget.configure"
		id = *c.AccountID
		target = "/api/platform/procurement-accounts/" + id + "/periodic-budget"
	}
	opid := c.ConfigurationOperationID
	if !changed {
		opid = pbgID(90 + len(r.Operations))
	}
	op := PeriodicBudgetOperationRow{OperationID: opid, Kind: kind, Actor: "operator", Scope: scope, Target: target, ResourceID: c.ID, Revision: c.Revision, SafeInput: pbgJSON(map[string]any{"expected_revision": expected, "period_kind": c.PeriodKind, "enabled": c.Enabled, "limit_amount": c.LimitAmount}), ResultSnapshot: pbgSnapshot(c, at, "0"), InputFingerprint: pbgFP(kind, "operator", scope, target, expected, c.PeriodKind, fmt.Sprint(c.Enabled), c.LimitAmount), CreatedXID: c.CreatedXID, CreatedAt: at}
	r.Operations = append(r.Operations, op)
	if changed {
		r.Versions = append(r.Versions, PeriodicBudgetVersionRow{ControlID: c.ID, Revision: c.Revision, OperationID: opid, PeriodKind: c.PeriodKind, Enabled: c.Enabled, LimitAmount: c.LimitAmount, Actor: "operator", CreatedXID: op.CreatedXID, CreatedAt: at})
	}
	var tenant *string
	if c.Scope == "key" {
		tenant = c.TenantID
	}
	r.Audits = append(r.Audits, PeriodicBudgetAuditRow{ID: pbgID(100 + len(r.Audits)), Actor: "operator", TenantScope: tenant, Action: "periodic_budget.configured", Target: opid, SanitizedDiff: pbgJSON(map[string]any{"control_id": c.ID, "revision": c.Revision, "scope": c.Scope, "scope_id": id, "changed": changed}), CreatedAt: at})
}
func pbgBase() PeriodicBudgetGraphRows {
	at := pbgTime("2026-10-01T08:00:00Z")
	r := PeriodicBudgetGraphRows{Tenants: []ProcurementAttemptTenantRow{{ID: pbgID(2)}}, Keys: []PeriodicBudgetKeyRow{{ID: pbgID(3), TenantID: pbgID(2)}}, Users: []ProcurementApprovalUserRow{{ID: "operator"}}}
	r.Controls = []PeriodicBudgetControlRow{{ID: pbgID(1), Scope: "tenant", TenantID: pbgPtr(pbgID(2)), PeriodKind: "utc_day", Enabled: true, LimitAmount: "1000", Revision: "1", TrackingStartedAt: at, EffectiveFrom: pbgTime("2026-10-02T00:00:00Z"), MaxObservedWindowStart: pbgTime("2026-10-01T00:00:00Z"), HeldAmount: "0", SpentAmount: "0", EventSeq: "0", CreatedXID: "9", ConfigurationOperationID: pbgID(4)}}
	pbgCommand(&r, "0", true, at)
	return r
}
func pbgCustomer(terminal string, amount, charge string) PeriodicBudgetGraphRows {
	r := pbgBase()
	rid, vid, aid, eid, wid := pbgID(10), pbgID(11), pbgID(12), pbgID(13), pbgID(14)
	at := pbgTime("2026-10-01T08:10:00Z")
	r.Requests = []PeriodicBudgetRequestRow{{ID: rid, TenantID: pbgID(2), KeyID: pbgID(3), CreatedXID: pbgPtr("10"), TenantControl: pbgPtr(pbgID(1)), AdmittedAt: at}}
	r.Reservations = []CustomerKeyReservationRow{{ID: vid, TenantID: pbgID(2), RequestID: rid, Amount: amount, Status: "reserved"}}
	r.Windows = []PeriodicBudgetWindowRow{{ID: wid, ControlID: pbgID(1), StartAt: pbgTime("2026-10-01T00:00:00Z"), EndAt: pbgTime("2026-10-02T00:00:00Z"), HeldAmount: amount, SpentAmount: "0", Revision: "1", LastEventID: pbgPtr(eid)}}
	r.Allocations = []PeriodicBudgetAllocationRow{{ID: aid, ControlID: pbgID(1), SubjectType: "customer_request", SubjectID: rid, Origin: "new", OriginSourceID: vid, WindowID: pbgPtr(wid), AdmittedAt: at, UpperAmount: amount, Baseline: "0", SourceHeld: amount, SourceSpent: "0", HeldAmount: amount, SpentAmount: "0", State: "held", Revision: "1", LastEventID: pbgPtr(eid), CreatedXID: "10", CreatedAt: at}}
	r.Events = []PeriodicBudgetEventRow{{ID: eid, ControlID: pbgID(1), AllocationID: aid, SourceKind: "reservation", SourceID: vid, Seq: "1", AllocationBeforeRevision: "0", WindowBeforeRevision: pbgPtr("0"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: amount, AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: amount, SpentDelta: "0", CreatedXID: "10", CreatedAt: at}}
	c := &r.Controls[0]
	c.HeldAmount = amount
	c.EventSeq = "1"
	c.LastEventID = pbgPtr(eid)
	if terminal != "" {
		sid, last := pbgID(15), pbgID(16)
		r.Reservations[0].Status = terminal
		r.Settlements = []PeriodicBudgetSettlementRow{{ID: sid, TenantID: pbgID(2), RequestID: rid, ChargedAmount: charge, Status: terminal}}
		negative := "-" + amount
		if amount == "0" {
			negative = "0"
		}
		r.Events = append(r.Events, PeriodicBudgetEventRow{ID: last, ControlID: c.ID, AllocationID: aid, SourceKind: "settlement", SourceID: sid, Seq: "2", AllocationBeforeRevision: "1", WindowBeforeRevision: pbgPtr("1"), BeforeHeld: amount, BeforeSpent: "0", AfterHeld: "0", AfterSpent: charge, AfterSourceSpent: charge, AfterState: terminal, HeldDelta: negative, SpentDelta: charge, CreatedXID: "11", CreatedAt: at})
		a := &r.Allocations[0]
		a.HeldAmount = "0"
		a.SourceHeld = "0"
		a.SourceSpent = charge
		a.SpentAmount = charge
		a.State = terminal
		a.Revision = "2"
		a.LastEventID = pbgPtr(last)
		win := &r.Windows[0]
		win.HeldAmount = "0"
		win.SpentAmount = charge
		win.Revision = "2"
		win.LastEventID = pbgPtr(last)
		c.HeldAmount = "0"
		c.SpentAmount = charge
		c.EventSeq = "2"
		c.LastEventID = pbgPtr(last)
	}
	return r
}
func pbgSupplier() PeriodicBudgetGraphRows {
	r := pbgBase()
	c := &r.Controls[0]
	c.Scope = "procurement"
	c.TenantID = nil
	c.AccountID = pbgPtr(pbgID(30))
	r.Operations = nil
	r.Versions = nil
	r.Audits = nil
	pbgCommand(&r, "0", true, c.TrackingStartedAt)
	r.Requests = []PeriodicBudgetRequestRow{{ID: pbgID(10), TenantID: pbgID(2), KeyID: pbgID(3)}}
	r.Reservations = []CustomerKeyReservationRow{{ID: pbgID(11), TenantID: pbgID(2), RequestID: pbgID(10), Amount: "200", Status: "reserved"}}
	r.Attempts = []PeriodicBudgetAttemptRow{{ID: pbgID(20), TenantID: pbgID(2), RequestID: pbgID(10), CredentialID: "credential", ChannelID: "channel", DeliveryState: "prepared", CreatedXID: pbgPtr("10")}}
	r.Accounts = []ProcurementBudgetAccountRow{{ID: pbgID(30), HeldAmount: "200", SpentAmount: "0"}}
	r.Bindings = []PeriodicBudgetBindingRow{{ID: pbgID(31), AccountID: pbgID(30), CredentialID: "credential", ChannelID: "channel"}}
	at := pbgTime("2026-10-01T08:10:00Z")
	r.Procurement = []PeriodicBudgetSupplierRow{{AttemptID: pbgID(20), AccountID: pbgID(30), BindingID: pbgID(31), UpperAmount: "200", HeldAmount: "200", SpentAmount: "0", State: "prepared", CreatedXID: pbgPtr("10"), ControlID: pbgPtr(c.ID), AdmittedAt: at}}
	r.ProcurementEvents = []PeriodicBudgetProcurementEventRow{{ID: pbgID(32), EventKey: "reserve:" + pbgID(20), AccountID: pbgID(30), AttemptID: pbgID(20), HeldDelta: "200", SpentDelta: "0", Type: "reserved", CreatedXID: pbgPtr("10")}}
	r.Windows = []PeriodicBudgetWindowRow{{ID: pbgID(14), ControlID: c.ID, StartAt: pbgTime("2026-10-01T00:00:00Z"), EndAt: pbgTime("2026-10-02T00:00:00Z"), HeldAmount: "200", SpentAmount: "0", Revision: "1", LastEventID: pbgPtr(pbgID(13))}}
	r.Allocations = []PeriodicBudgetAllocationRow{{ID: pbgID(12), ControlID: c.ID, SubjectType: "supplier_attempt", SubjectID: pbgID(20), Origin: "new", OriginSourceID: pbgID(32), WindowID: pbgPtr(pbgID(14)), AdmittedAt: at, UpperAmount: "200", Baseline: "0", SourceHeld: "200", SourceSpent: "0", HeldAmount: "200", SpentAmount: "0", State: "held", Revision: "1", LastEventID: pbgPtr(pbgID(13)), CreatedXID: "10", CreatedAt: at}}
	r.Events = []PeriodicBudgetEventRow{{ID: pbgID(13), ControlID: c.ID, AllocationID: pbgID(12), SourceKind: "procurement_event", SourceID: pbgID(32), Seq: "1", AllocationBeforeRevision: "0", WindowBeforeRevision: pbgPtr("0"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "200", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: "200", SpentDelta: "0", CreatedXID: "10", CreatedAt: at}}
	c.HeldAmount = "200"
	c.EventSeq = "1"
	c.LastEventID = pbgPtr(pbgID(13))
	return r
}
func pbgCheck(t *testing.T, r PeriodicBudgetGraphRows) PeriodicBudgetGraphCheck {
	t.Helper()
	got, e := CheckPeriodicBudgetGraph(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	return got
}
func pbgClean(t *testing.T, r PeriodicBudgetGraphRows) PeriodicBudgetGraphCheck {
	t.Helper()
	got := pbgCheck(t, r)
	if got.Mismatches != (PeriodicBudgetGraphMismatches{}) {
		t.Fatalf("unexpected mismatches: %+v", got.Mismatches)
	}
	return got
}

func TestCheckPeriodicBudgetGraphNormal(t *testing.T) {
	for name, r := range map[string]PeriodicBudgetGraphRows{"empty": {}, "configured": pbgBase(), "reserved": pbgCustomer("", "200", "0"), "partial": pbgCustomer("settled", "200", "73"), "zero settled": pbgCustomer("settled", "0", "0"), "zero released": pbgCustomer("released", "0", "0"), "released": pbgCustomer("released", "200", "0"), "supplier": pbgSupplier()} {
		t.Run(name, func(t *testing.T) {
			got := pbgClean(t, r)
			if got.Scope != "periodic_budget_graph_v1" {
				t.Fatal(got)
			}
			if len(r.Windows) > 0 {
				if got.Status != "indeterminate" || got.Unproven.OperationSnapshotPrefixUnproven != 1 {
					t.Fatal(got)
				}
			} else if !got.Balanced || !got.CoverageComplete {
				t.Fatal(got)
			}
		})
	}
	r := PeriodicBudgetGraphRows{Tenants: []ProcurementAttemptTenantRow{{ID: ""}}, Keys: []PeriodicBudgetKeyRow{{ID: "", TenantID: ""}}, Users: []ProcurementApprovalUserRow{{ID: ""}}, Requests: []PeriodicBudgetRequestRow{{ID: "", TenantID: "", KeyID: ""}}, Reservations: []CustomerKeyReservationRow{{ID: "", TenantID: "", RequestID: "", Amount: "0", Status: "reserved"}}}
	if !pbgClean(t, r).Balanced {
		t.Fatal("raw empty identities lost")
	}
	r = pbgBase()
	pbgCommand(&r, "1", false, r.Controls[0].TrackingStartedAt)
	pbgClean(t, r)
	r = pbgBase()
	r.Controls[0].Revision = "2"
	r.Controls[0].LimitAmount = "70"
	r.Controls[0].ConfigurationOperationID = pbgID(80)
	pbgCommand(&r, "1", true, r.Controls[0].TrackingStartedAt)
	pbgCommand(&r, "2", false, r.Controls[0].TrackingStartedAt)
	pbgClean(t, r)
}

func TestCheckPeriodicBudgetGraphCorruptions(t *testing.T) {
	cases := []struct {
		name, field string
		mutate      func(*PeriodicBudgetGraphRows)
	}{
		{"source tenant", "SourceIdentityMismatches", func(r *PeriodicBudgetGraphRows) { r.Requests[0].TenantID = "foreign" }},
		{"missing allocation", "MissingAllocations", func(r *PeriodicBudgetGraphRows) { r.Allocations = nil }},
		{"orphan allocation", "OrphanAllocations", func(r *PeriodicBudgetGraphRows) { r.Allocations[0].SubjectID = pbgID(99) }},
		{"origin", "AllocationOriginMismatches", func(r *PeriodicBudgetGraphRows) { r.Allocations[0].CreatedXID = "12" }},
		{"source tail", "AllocationSourceMismatches", func(r *PeriodicBudgetGraphRows) { r.Allocations[0].SourceSpent = "1" }},
		{"allocation tail", "AllocationProjectionMismatches", func(r *PeriodicBudgetGraphRows) { r.Allocations[0].Revision = "3" }},
		{"missing finish", "MissingEffects", func(r *PeriodicBudgetGraphRows) { r.Events = r.Events[:1] }},
		{"orphan effect", "OrphanEffects", func(r *PeriodicBudgetGraphRows) { r.Events[1].SourceID = pbgID(98) }},
		{"wrong effect kind", "EffectBindingMismatches", func(r *PeriodicBudgetGraphRows) { r.Events[0].SourceKind = "settlement" }},
		{"before not oracle", "EventChainMismatches", func(r *PeriodicBudgetGraphRows) { r.Events[1].BeforeHeld = "201"; r.Events[1].HeldDelta = "-201" }},
		{"window revision", "WindowMismatches", func(r *PeriodicBudgetGraphRows) { r.Windows[0].Revision = "99" }},
		{"control sum", "ControlMismatches", func(r *PeriodicBudgetGraphRows) { r.Controls[0].SpentAmount = "74" }},
		{"config pointer", "ConfigurationMismatches", func(r *PeriodicBudgetGraphRows) { r.Controls[0].ConfigurationOperationID = pbgID(99) }},
		{"version actor", "VersionMismatches", func(r *PeriodicBudgetGraphRows) { r.Versions[0].Actor = "absent" }},
		{"command frame", "OperationMismatches", func(r *PeriodicBudgetGraphRows) { r.Operations[0].InputFingerprint = strings.Repeat("0", 64) }},
		{"audit orphan", "AuditMismatches", func(r *PeriodicBudgetGraphRows) { r.Audits[0].Target = pbgID(99) }},
		{"snapshot static", "SnapshotMismatches", func(r *PeriodicBudgetGraphRows) { r.Operations[0].ResultSnapshot = []byte(`{}`) }},
		{"bad numeric", "InvalidAmountRows", func(r *PeriodicBudgetGraphRows) { r.Reservations[0].Amount = "01" }},
		{"bad revision", "InvalidRevisionRows", func(r *PeriodicBudgetGraphRows) { r.Events[0].Seq = "9223372036854775808" }},
		{"bad xid", "InvalidXIDRows", func(r *PeriodicBudgetGraphRows) { r.Events[0].CreatedXID = "18446744073709551616" }},
		{"bad enum", "InvalidEnumRows", func(r *PeriodicBudgetGraphRows) { r.Reservations[0].Status = "other" }},
		{"bad uuid", "InvalidUUIDRows", func(r *PeriodicBudgetGraphRows) { r.Allocations[0].OriginSourceID = "bad" }},
		{"bad text", "InvalidTextRows", func(r *PeriodicBudgetGraphRows) { r.Users[0].ID = "bad\x00" }},
		{"infinite", "InvalidTimeRows", func(r *PeriodicBudgetGraphRows) {
			r.Controls[0].TrackingStartedAt = PeriodicBudgetGraphTime{Valid: true, Infinity: 1}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := pbgCustomer("settled", "200", "73")
			tc.mutate(&r)
			got := pbgCheck(t, r)
			field := reflect.ValueOf(got.Mismatches).FieldByName(tc.field)
			if !field.IsValid() || field.Uint() == 0 || got.Status != "mismatch" || got.Balanced {
				t.Fatalf("%s: %+v", tc.field, got)
			}
		})
	}
}

func TestCheckPeriodicBudgetGraphStrictReceipts(t *testing.T) {
	for _, raw := range []string{`{"expected_revision":"0","period_kind":"utc_day","enabled":true,"limit_amount":"1000","x":0}`, `{"expected_revision":"0","period_kind":"utc_day","enabled":true,"limit_amount":"1000","enabled":true}`, `{"expected_revision":"0","period_kind":"utc_day","enabled":null,"limit_amount":"1000"}`, `{"expected_revision":"00","period_kind":"utc_day","enabled":true,"limit_amount":"1000"}`, `{"expected_revision":"0","period_kind":"\ud800","enabled":true,"limit_amount":"1000"}`} {
		r := pbgBase()
		r.Operations[0].SafeInput = []byte(raw)
		if pbgCheck(t, r).Mismatches.OperationMismatches != 1 {
			t.Fatal(raw)
		}
	}
	r := pbgBase()
	r.Operations[0].ResultSnapshot = bytesReplacePB(r.Operations[0].ResultSnapshot, `"carry_held":"0"`, `"carry_held":"1"`)
	got := pbgCheck(t, r)
	if got.Mismatches.SnapshotMismatches != 1 || got.Unproven.OperationSnapshotPrefixUnproven != 0 {
		t.Fatal(got)
	}
	r = pbgBase()
	r.Audits[0].SanitizedDiff = []byte(`{"control_id":"x","revision":1,"scope":"tenant","scope_id":"x","changed":true}`)
	if pbgCheck(t, r).Mismatches.AuditMismatches != 1 {
		t.Fatal("audit string revision")
	}
}
func bytesReplacePB(raw []byte, a, b string) []byte {
	return []byte(strings.Replace(string(raw), a, b, 1))
}

func TestCheckPeriodicBudgetGraphDuplicates(t *testing.T) {
	// Every physical group has an independent primary-key namespace.
	for i := 0; i < reflect.TypeOf(PeriodicBudgetGraphRows{}).NumField(); i++ {
		t.Run("primary_"+reflect.TypeOf(PeriodicBudgetGraphRows{}).Field(i).Name, func(t *testing.T) {
			r := pbgCustomer("settled", "200", "73")
			row := reflect.ValueOf(&r).Elem().Field(i)
			if row.Len() == 0 {
				r = pbgSupplier()
				row = reflect.ValueOf(&r).Elem().Field(i)
			}
			if row.Len() == 0 {
				t.Fatal("missing fixture group")
			}
			row.Set(reflect.Append(row, row.Index(0)))
			got := pbgCheck(t, r)
			if got.Mismatches.DuplicatePrimaryRows != 1 {
				t.Fatal(got)
			}
		})
	}
	r := pbgCustomer("settled", "200", "73")
	r.Events = append(r.Events, r.Events[0])
	r.Allocations = append(r.Allocations, r.Allocations[0])
	r.Reservations = append(r.Reservations, r.Reservations[0])
	got := pbgCheck(t, r)
	if got.Mismatches.DuplicatePrimaryRows != 3 || got.Mismatches.ControlMismatches != 0 {
		t.Fatal(got)
	}
	cases := []struct {
		field  string
		mutate func(*PeriodicBudgetGraphRows)
	}{
		{"DuplicateControlScopeRows", func(r *PeriodicBudgetGraphRows) {
			c := r.Controls[0]
			c.ID = pbgID(41)
			r.Controls = append(r.Controls, c)
		}},
		{"DuplicateWindowStartRows", func(r *PeriodicBudgetGraphRows) {
			v := r.Windows[0]
			v.ID = pbgID(42)
			r.Windows = append(r.Windows, v)
		}},
		{"DuplicateAllocationSubjectRows", func(r *PeriodicBudgetGraphRows) {
			v := r.Allocations[0]
			v.ID = pbgID(43)
			r.Allocations = append(r.Allocations, v)
		}},
		{"DuplicateEventSequenceRows", func(r *PeriodicBudgetGraphRows) { v := r.Events[0]; v.ID = pbgID(44); r.Events = append(r.Events, v) }},
		{"DuplicateEffectRows", func(r *PeriodicBudgetGraphRows) {
			v := r.Events[0]
			v.ID = pbgID(45)
			v.Seq = "3"
			r.Events = append(r.Events, v)
		}},
		{"DuplicateVersionOperationRows", func(r *PeriodicBudgetGraphRows) {
			v := r.Versions[0]
			v.Revision = "2"
			r.Versions = append(r.Versions, v)
		}},
		{"DuplicateReservationRequestRows", func(r *PeriodicBudgetGraphRows) {
			v := r.Reservations[0]
			v.ID = pbgID(46)
			r.Reservations = append(r.Reservations, v)
		}},
		{"DuplicateSettlementRequestRows", func(r *PeriodicBudgetGraphRows) {
			v := r.Settlements[0]
			v.ID = pbgID(47)
			r.Settlements = append(r.Settlements, v)
		}},
		{"DuplicateAuditTargetRows", func(r *PeriodicBudgetGraphRows) { v := r.Audits[0]; v.ID = pbgID(48); r.Audits = append(r.Audits, v) }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			r := pbgCustomer("settled", "200", "73")
			tc.mutate(&r)
			got := pbgCheck(t, r)
			if reflect.ValueOf(got.Mismatches).FieldByName(tc.field).Uint() != 1 {
				t.Fatal(got)
			}
		})
	}
}

type pbgCancelContext struct {
	context.Context
	calls   atomic.Int64
	after   int64
	timeout bool
}

func (c *pbgCancelContext) Err() error {
	if c.calls.Add(1) >= c.after {
		if c.timeout {
			return context.DeadlineExceeded
		}
		return context.Canceled
	}
	return nil
}
func TestCheckPeriodicBudgetGraphResourcesAndCancellation(t *testing.T) {
	if got, e := CheckPeriodicBudgetGraph(nil, PeriodicBudgetGraphRows{}); e != ErrPeriodicBudgetGraphRowsInput || got != (PeriodicBudgetGraphCheck{}) {
		t.Fatal(got, e)
	}
	for name, r := range map[string]PeriodicBudgetGraphRows{"rows": {Users: make([]ProcurementApprovalUserRow, 100001)}, "field": {Users: []ProcurementApprovalUserRow{{ID: strings.Repeat("x", 65537)}}}, "bytes": {Users: make([]ProcurementApprovalUserRow, 257)}} {
		if name == "bytes" {
			for i := range r.Users {
				r.Users[i].ID = strings.Repeat("x", 65536)
			}
		}
		got, e := CheckPeriodicBudgetGraph(context.Background(), r)
		if e != ErrPeriodicBudgetGraphRowsResource || got != (PeriodicBudgetGraphCheck{}) {
			t.Fatal(name, got, e)
		}
	}
	r := PeriodicBudgetGraphRows{Users: make([]ProcurementApprovalUserRow, 256)}
	for i := range r.Users {
		r.Users[i].ID = strings.Repeat("x", 65536)
	}
	pbgCheck(t, r)
	// Source rows and bytes are shared across groups; native timestamps count.
	r = PeriodicBudgetGraphRows{Users: make([]ProcurementApprovalUserRow, 100000)}
	pbgCheck(t, r)
	r.Tenants = []ProcurementAttemptTenantRow{{ID: ""}}
	if got, e := CheckPeriodicBudgetGraph(context.Background(), r); e != ErrPeriodicBudgetGraphRowsResource || got != (PeriodicBudgetGraphCheck{}) {
		t.Fatal("shared row boundary", got, e)
	}
	r = PeriodicBudgetGraphRows{Users: make([]ProcurementApprovalUserRow, 255), Audits: []PeriodicBudgetAuditRow{{ID: strings.Repeat("a", 65536-32)}}}
	for i := range r.Users {
		r.Users[i].ID = strings.Repeat("x", 65536)
	}
	pbgCheck(t, r)
	r.Audits[0].RequestID = "x"
	if got, e := CheckPeriodicBudgetGraph(context.Background(), r); e != ErrPeriodicBudgetGraphRowsResource || got != (PeriodicBudgetGraphCheck{}) {
		t.Fatal("shared native byte boundary", got, e)
	}
	for _, timeout := range []bool{false, true} {
		for _, after := range []int64{1, 3, 12} {
			ctx := &pbgCancelContext{Context: context.Background(), after: after, timeout: timeout}
			r := PeriodicBudgetGraphRows{Users: make([]ProcurementApprovalUserRow, 10000)}
			got, e := CheckPeriodicBudgetGraph(ctx, r)
			want := ErrPeriodicBudgetGraphRowsCanceled
			if timeout {
				want = ErrPeriodicBudgetGraphRowsTimeout
			}
			if e != want || got != (PeriodicBudgetGraphCheck{}) {
				t.Fatal(after, got, e)
			}
		}
	}
}
