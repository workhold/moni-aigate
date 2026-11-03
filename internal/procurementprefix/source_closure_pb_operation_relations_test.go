package procurementprefix

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sourcePBRelTestID(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

// The initial receipt/fingerprint is the independently pinned literal from the
// JSON ABI tests. The following fixtures never invoke a writer or verifier.
func sourcePBRelFixture() Rows {
	o := sourcePBTestRow()
	id := sourcePBRelTestID(21)
	o.ObservationID = &id
	c := PeriodicBudgetControlRow{ID: o.ResourceID, Scope: "tenant", TenantID: sourcePBRelPointer(sourcePBRelTestID(1)), PeriodKind: "utc_day", Enabled: true, LimitAmount: "100", Revision: "1", TrackingStartedAt: o.CreatedAt, EffectiveFrom: Time{Valid: true, Time: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}, MaxObservedWindowStart: Time{Valid: true, Time: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}, HeldAmount: "0", SpentAmount: "0", EventSeq: "0", CreatedXID: "1", ConfigurationOperationID: o.OperationID}
	return Rows{OriginalRows: OriginalRows{
		Tenants: []TenantRow{{ID: *c.TenantID}}, Users: []UserRow{{ID: o.Actor}},
		PeriodicBudgetControls:              []PeriodicBudgetControlRow{c},
		PeriodicBudgetOperations:            []PeriodicBudgetOperationRow{o},
		PeriodicBudgetConfigurationVersions: []PeriodicBudgetConfigurationVersionRow{{ControlID: c.ID, Revision: "1", OperationID: o.OperationID, PeriodKind: c.PeriodKind, Enabled: true, LimitAmount: c.LimitAmount, Actor: o.Actor, CreatedXID: "1", CreatedAt: o.CreatedAt}},
		PeriodicBudgetObservationOrigins:    []PeriodicBudgetObservationOriginRow{{ControlID: c.ID, Version: "1", CreatedXID: "1", TrackingStartedAt: c.TrackingStartedAt}},
		PeriodicBudgetObservationHeads:      []PeriodicBudgetObservationHeadRow{{ControlID: c.ID, Ordinal: "1", LastObservationID: &id, UpdatedXID: "1"}},
		PeriodicBudgetObservationJournal:    []PeriodicBudgetObservationJournalRow{{ID: id, ControlID: c.ID, Ordinal: "1", Kind: "command", SourceID: o.OperationID, EventSeq: "0", ConfigurationRevision: "1", ConfigurationOperationID: o.OperationID, CreatedXID: "1", ObservedAt: o.CreatedAt, CommandSourceID: &o.OperationID}},
		AuditEvents:                         []AuditEventRow{{ID: sourcePBRelTestID(31), Actor: o.Actor, Action: "periodic_budget.configured", Target: o.OperationID, SanitizedDiff: `{"control_id":"` + c.ID + `","revision":"1","scope":"tenant","scope_id":"` + *c.TenantID + `","changed":true}`, CreatedAt: o.CreatedAt}},
	}}
}

func sourcePBRelRun(ctx context.Context, rows Rows) (SourceClosure, error) {
	w, _, err := newReplayWork(ctx, rows)
	if err != nil {
		return SourceClosure{}, err
	}
	s := sourceClosureState{w: w, tables: make(map[string][]sourceRawRow), bad: make(map[string]keySet)}
	if err = s.physicalRows(reflect.ValueOf(rows.OriginalRows)); err != nil {
		return SourceClosure{}, err
	}
	if err = s.pbOperationRelations(); err != nil {
		return SourceClosure{}, err
	}
	return SourceClosure{InvalidScalarRows: uint64(len(s.bad["scalar"])), DuplicateRows: uint64(len(s.bad["duplicate"])), CompanionRows: uint64(len(s.bad["companion"])), OriginalPointerRows: uint64(len(s.bad["pointer"]))}, nil
}

func sourcePBRelClean(t *testing.T, rows Rows) {
	t.Helper()
	r, err := sourcePBRelRun(context.Background(), rows)
	if err != nil || r != (SourceClosure{}) {
		t.Fatalf("legal PB operation relations rejected: %+v %v", r, err)
	}
}

func sourcePBRelReject(t *testing.T, rows Rows) SourceClosure {
	t.Helper()
	r, err := sourcePBRelRun(context.Background(), rows)
	if err != nil || r.OriginalPointerRows+r.CompanionRows == 0 {
		t.Fatalf("invalid PB operation relations accepted: %+v %v", r, err)
	}
	return r
}

func sourcePBRelLegacy(rows *Rows) {
	r := &rows.OriginalRows
	r.PeriodicBudgetObservationOrigins, r.PeriodicBudgetObservationHeads, r.PeriodicBudgetObservationJournal = nil, nil, nil
	for i := range r.PeriodicBudgetOperations {
		r.PeriodicBudgetOperations[i].ObservationID = nil
	}
	for i := range r.PeriodicBudgetWindows {
		r.PeriodicBudgetWindows[i].ObservationID, r.PeriodicBudgetWindows[i].ObservationCreatedXID = nil, nil
	}
}

func sourcePBRelAddOperation(rows *Rows, changed bool) {
	r := &rows.OriginalRows
	old := r.PeriodicBudgetOperations[len(r.PeriodicBudgetOperations)-1]
	o := old
	o.OperationID, o.ObservationID = sourcePBRelTestID(40+len(r.PeriodicBudgetOperations)), sourcePBRelPointer(sourcePBRelTestID(50+len(r.PeriodicBudgetOperations)))
	o.CreatedXID = "2"
	// Same-period wall-clock regression is legitimate; do not order by time.
	o.CreatedAt.Time = o.CreatedAt.Time.Add(-time.Minute)
	expected, limit := old.Revision, "100"
	if changed {
		o.Revision, limit = "2", "200"
	}
	o.SafeInput = `{"expected_revision":"` + expected + `","period_kind":"utc_day","enabled":true,"limit_amount":"` + limit + `"}`
	o.ResultSnapshot = strings.NewReplacer(`"revision":"1"`, `"revision":"`+o.Revision+`"`, `"limit_amount":"100"`, `"limit_amount":"`+limit+`"`, `2026-11-01T12:00:00.000000Z"}`, o.CreatedAt.Time.UTC().Format(sourcePBUTC6)+`"}`).Replace(sourcePBSnapshotJSON)
	o.InputFingerprint = sourcePBTestFingerprint(o.Kind, o.Actor, o.Scope, o.Target, expected, "utc_day", "true", limit)
	r.PeriodicBudgetOperations = append(r.PeriodicBudgetOperations, o)
	config := r.PeriodicBudgetConfigurationVersions[len(r.PeriodicBudgetConfigurationVersions)-1].OperationID
	if changed {
		config = o.OperationID
		r.PeriodicBudgetConfigurationVersions = append(r.PeriodicBudgetConfigurationVersions, PeriodicBudgetConfigurationVersionRow{ControlID: o.ResourceID, Revision: o.Revision, OperationID: o.OperationID, PeriodKind: "utc_day", Enabled: true, LimitAmount: limit, Actor: o.Actor, CreatedXID: o.CreatedXID, CreatedAt: o.CreatedAt})
		r.PeriodicBudgetControls[0].Revision, r.PeriodicBudgetControls[0].LimitAmount, r.PeriodicBudgetControls[0].ConfigurationOperationID = o.Revision, limit, o.OperationID
	}
	last := r.PeriodicBudgetObservationJournal[len(r.PeriodicBudgetObservationJournal)-1]
	j := PeriodicBudgetObservationJournalRow{ID: *o.ObservationID, ControlID: o.ResourceID, Ordinal: fmt.Sprint(len(r.PeriodicBudgetObservationJournal) + 1), PreviousID: sourcePBRelPointer(last.ID), Kind: "command", SourceID: o.OperationID, EventSeq: "0", ConfigurationRevision: o.Revision, ConfigurationOperationID: config, CreatedXID: o.CreatedXID, ObservedAt: o.CreatedAt, CommandSourceID: sourcePBRelPointer(o.OperationID)}
	r.PeriodicBudgetObservationJournal = append(r.PeriodicBudgetObservationJournal, j)
	r.PeriodicBudgetObservationHeads[0].Ordinal, r.PeriodicBudgetObservationHeads[0].LastObservationID, r.PeriodicBudgetObservationHeads[0].UpdatedXID = j.Ordinal, sourcePBRelPointer(j.ID), j.CreatedXID
	r.AuditEvents = append(r.AuditEvents, AuditEventRow{ID: sourcePBRelTestID(60 + len(r.AuditEvents)), Actor: o.Actor, Action: "periodic_budget.configured", Target: o.OperationID, SanitizedDiff: `{"control_id":"` + o.ResourceID + `","revision":"` + o.Revision + `","scope":"tenant","scope_id":"` + sourcePBRelTestID(1) + `","changed":` + fmt.Sprint(changed) + `}`, CreatedAt: o.CreatedAt})
}

func sourcePBRelWindow(rows *Rows, before bool, effect bool) {
	r := &rows.OriginalRows
	c, cmd := r.PeriodicBudgetControls[0], r.PeriodicBudgetObservationJournal[0]
	wid, bid := sourcePBRelTestID(4), sourcePBRelTestID(71)
	w := PeriodicBudgetWindowRow{ID: wid, ControlID: c.ID, StartAt: c.MaxObservedWindowStart, EndAt: c.EffectiveFrom, HeldAmount: "0", SpentAmount: "0", Revision: "0", ObservationID: &bid, ObservationCreatedXID: sourcePBRelPointer("1")}
	b := PeriodicBudgetObservationJournalRow{ID: bid, ControlID: c.ID, Kind: "window_birth", SourceID: wid, EventSeq: "0", ConfigurationRevision: "1", ConfigurationOperationID: c.ConfigurationOperationID, WindowID: &wid, WindowRevision: sourcePBRelPointer("0"), CreatedXID: "1", WindowSourceID: &wid}
	if before {
		b.Ordinal, cmd.Ordinal, cmd.PreviousID = "1", "2", &bid
		cmd.WindowID, cmd.WindowRevision = &wid, sourcePBRelPointer("0")
		r.PeriodicBudgetOperations[0].ResultSnapshot = strings.Replace(sourcePBSnapshotJSON, `"current_window":null`, `"current_window":`+sourcePBWindowJSON, 1)
		r.PeriodicBudgetObservationJournal = []PeriodicBudgetObservationJournalRow{b, cmd}
	} else {
		b.Ordinal, b.PreviousID = "2", sourcePBRelPointer(cmd.ID)
		r.PeriodicBudgetObservationJournal = []PeriodicBudgetObservationJournalRow{cmd, b}
	}
	if effect {
		if !before {
			panic("effect fixture requires birth before command")
		}
		aid, eid, request := sourcePBRelTestID(72), sourcePBRelTestID(73), sourcePBRelTestID(74)
		r.PeriodicBudgetAllocations = []PeriodicBudgetAllocationRow{{ID: aid, ControlID: c.ID, SubjectType: "customer_request", SubjectID: request, Origin: "new", OriginSourceID: request, WindowID: &wid, AdmittedAt: r.PeriodicBudgetOperations[0].CreatedAt, UpperAmount: "10", Baseline: "0", SourceHeld: "10", SourceSpent: "0", HeldAmount: "10", SpentAmount: "0", State: "held", Revision: "1", LastEventID: &eid, CreatedXID: "1", CreatedAt: r.PeriodicBudgetOperations[0].CreatedAt}}
		r.PeriodicBudgetEvents = []PeriodicBudgetEventRow{{ID: eid, ControlID: c.ID, AllocationID: aid, SourceKind: "reservation", SourceID: request, Seq: "1", AllocationBeforeRevision: "0", WindowBeforeRevision: sourcePBRelPointer("0"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "10", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: "10", SpentDelta: "0", CreatedXID: "1", CreatedAt: r.PeriodicBudgetOperations[0].CreatedAt}}
		w.Revision, w.LastEventID, w.HeldAmount = "1", &eid, "10"
		r.PeriodicBudgetControls[0].EventSeq, r.PeriodicBudgetControls[0].LastEventID, r.PeriodicBudgetControls[0].HeldAmount = "1", &eid, "10"
		j := &r.PeriodicBudgetObservationJournal[1]
		j.EventSeq, j.LastEventID, j.WindowRevision, j.WindowLastEventID = "1", &eid, sourcePBRelPointer("1"), &eid
		r.PeriodicBudgetOperations[0].ResultSnapshot = strings.Replace(r.PeriodicBudgetOperations[0].ResultSnapshot, `"held_amount":"0"`, `"held_amount":"10"`, 1)
	}
	r.PeriodicBudgetWindows = []PeriodicBudgetWindowRow{w}
	last := r.PeriodicBudgetObservationJournal[1]
	r.PeriodicBudgetObservationHeads[0].Ordinal, r.PeriodicBudgetObservationHeads[0].LastObservationID, r.PeriodicBudgetObservationHeads[0].UpdatedXID = last.Ordinal, sourcePBRelPointer(last.ID), last.CreatedXID
}

func TestSourcePBOperationRelationsLiteralChangedNoopAndLegacy(t *testing.T) {
	sourcePBRelClean(t, Rows{})
	for _, changed := range []bool{false, true} {
		v := sourcePBRelFixture()
		sourcePBRelClean(t, v)
		sourcePBRelAddOperation(&v, changed)
		sourcePBRelClean(t, v)
		sourcePBRelLegacy(&v)
		sourcePBRelClean(t, v)
	}
	for _, actor := range []string{"", "原 actor\n� 🧪"} {
		v := sourcePBRelFixture()
		r := &v.OriginalRows
		r.Users[0].ID, r.PeriodicBudgetOperations[0].Actor, r.PeriodicBudgetConfigurationVersions[0].Actor, r.AuditEvents[0].Actor = actor, actor, actor, actor
		o := &r.PeriodicBudgetOperations[0]
		o.InputFingerprint = sourcePBTestFingerprint(o.Kind, actor, o.Scope, o.Target, "0", "utc_day", "true", "100")
		sourcePBRelClean(t, v)
	}
	// The source timestamp can represent the same instant in another Location.
	v := sourcePBRelFixture()
	v.OriginalRows.PeriodicBudgetConfigurationVersions[0].CreatedAt.Time = v.OriginalRows.PeriodicBudgetOperations[0].CreatedAt.Time.In(time.FixedZone("+8", 8*3600))
	sourcePBRelClean(t, v)
}

func TestSourcePBOperationRelationsThreeScopesAndOnlyIDExistence(t *testing.T) {
	for _, scope := range []string{"key", "procurement"} {
		v := sourcePBRelFixture()
		r := &v.OriginalRows
		c, o, a := &r.PeriodicBudgetControls[0], &r.PeriodicBudgetOperations[0], &r.AuditEvents[0]
		c.Scope = scope
		id := sourcePBRelTestID(1)
		if scope == "key" {
			c.KeyID = &id
			r.APIKeys = []APIKeyRow{{ID: id, TenantID: *c.TenantID, Spent: "0", Held: "0"}}
			o.Scope, o.Target = *c.TenantID, "/api/keys/"+id+"/periodic-budget"
			a.TenantScope = c.TenantID
		} else {
			c.TenantID, c.AccountID = nil, &id
			r.ProcurementAccounts = []ProcurementAccountRow{{ID: id, Label: "a", Provider: "p", State: "active", LimitAmount: "100", HeldAmount: "0", SpentAmount: "0", Revision: "1", CreatedAt: o.CreatedAt}}
			o.Target = "/api/platform/procurement-accounts/" + id + "/periodic-budget"
		}
		o.Kind = scope + "_periodic_budget.configure"
		o.ResultSnapshot = strings.Replace(o.ResultSnapshot, `"scope":"tenant"`, `"scope":"`+scope+`"`, 1)
		o.InputFingerprint = sourcePBTestFingerprint(o.Kind, o.Actor, o.Scope, o.Target, "0", "utc_day", "true", "100")
		a.SanitizedDiff = strings.Replace(a.SanitizedDiff, `"scope":"tenant"`, `"scope":"`+scope+`"`, 1)
		sourcePBRelClean(t, v)
		if scope == "key" {
			r.APIKeys[0].TenantID = "wrong"
		} else {
			r.ProcurementAccounts = nil
		}
		sourcePBRelReject(t, v)
	}
}

func TestSourcePBOperationRelationsMonthCalendarAndDisabledNumeric100(t *testing.T) {
	v := sourcePBRelFixture()
	r := &v.OriginalRows
	at := time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC)
	c, o, version := &r.PeriodicBudgetControls[0], &r.PeriodicBudgetOperations[0], &r.PeriodicBudgetConfigurationVersions[0]
	c.PeriodKind, c.Enabled = "utc_month", false
	c.TrackingStartedAt.Time, c.EffectiveFrom.Time, c.MaxObservedWindowStart.Time = at, time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2028, 2, 1, 0, 0, 0, 0, time.UTC)
	o.CreatedAt.Time = at
	o.SafeInput = strings.NewReplacer("utc_day", "utc_month", `"enabled":true`, `"enabled":false`).Replace(o.SafeInput)
	o.ResultSnapshot = strings.NewReplacer("utc_day", "utc_month", `"enabled":true`, `"enabled":false`, "scheduled", "disabled", "2026-11-01T12:00:00.000000Z", "2028-02-29T12:00:00.000000Z", "2026-11-02T00:00:00.000000Z", "2028-03-01T00:00:00.000000Z", `"carry_held":"0"`, `"carry_held":"`+strings.Repeat("9", 100)+`"`).Replace(o.ResultSnapshot)
	o.InputFingerprint = sourcePBTestFingerprint(o.Kind, o.Actor, o.Scope, o.Target, "0", "utc_month", "false", "100")
	version.PeriodKind, version.Enabled, version.CreatedAt = "utc_month", false, o.CreatedAt
	r.PeriodicBudgetObservationOrigins[0].TrackingStartedAt = c.TrackingStartedAt
	r.PeriodicBudgetObservationJournal[0].ObservedAt = o.CreatedAt
	r.AuditEvents[0].CreatedAt = o.CreatedAt
	sourcePBRelClean(t, v)
	c.EffectiveFrom.Time = c.EffectiveFrom.Time.AddDate(0, 0, 1)
	sourcePBRelReject(t, v)
}

func TestSourcePBOperationRelationsWindowBirthBeforeChangedReceipt(t *testing.T) {
	v := sourcePBRelFixture()
	sourcePBRelAddOperation(&v, true)
	r := &v.OriginalRows
	c, first, last := r.PeriodicBudgetControls[0], r.PeriodicBudgetObservationJournal[0], r.PeriodicBudgetObservationJournal[1]
	wid, bid := sourcePBRelTestID(4), sourcePBRelTestID(71)
	b := PeriodicBudgetObservationJournalRow{ID: bid, ControlID: c.ID, Ordinal: "2", PreviousID: sourcePBRelPointer(first.ID), Kind: "window_birth", SourceID: wid, EventSeq: "0", ConfigurationRevision: "2", ConfigurationOperationID: c.ConfigurationOperationID, WindowID: &wid, WindowRevision: sourcePBRelPointer("0"), CreatedXID: "2", WindowSourceID: &wid}
	last.Ordinal, last.PreviousID, last.WindowID, last.WindowRevision = "3", &bid, &wid, sourcePBRelPointer("0")
	r.PeriodicBudgetObservationJournal = []PeriodicBudgetObservationJournalRow{first, b, last}
	r.PeriodicBudgetObservationHeads[0].Ordinal = "3"
	r.PeriodicBudgetWindows = []PeriodicBudgetWindowRow{{ID: wid, ControlID: c.ID, StartAt: c.MaxObservedWindowStart, EndAt: c.EffectiveFrom, HeldAmount: "0", SpentAmount: "0", Revision: "0", ObservationID: &bid, ObservationCreatedXID: sourcePBRelPointer("2")}}
	r.PeriodicBudgetOperations[1].ResultSnapshot = strings.Replace(r.PeriodicBudgetOperations[1].ResultSnapshot, `"current_window":null`, `"current_window":`+sourcePBWindowJSON, 1)
	sourcePBRelClean(t, v)
}

func TestSourcePBOperationRelationsConfigurationCounterexamples(t *testing.T) {
	cases := []struct {
		name string
		edit func(*OriginalRows)
	}{
		{"missing_tenant", func(r *OriginalRows) { r.Tenants = nil }},
		{"missing_actor", func(r *OriginalRows) { r.Users = nil }},
		{"missing_control", func(r *OriginalRows) { r.PeriodicBudgetControls = nil }},
		{"missing_version", func(r *OriginalRows) { r.PeriodicBudgetConfigurationVersions = nil }},
		{"missing_operation", func(r *OriginalRows) { r.PeriodicBudgetOperations = nil }},
		{"current_limit", func(r *OriginalRows) { r.PeriodicBudgetControls[0].LimitAmount = "101" }},
		{"current_operation", func(r *OriginalRows) { r.PeriodicBudgetControls[0].ConfigurationOperationID = sourcePBRelTestID(99) }},
		{"calendar", func(r *OriginalRows) {
			r.PeriodicBudgetControls[0].EffectiveFrom.Time = r.PeriodicBudgetControls[0].EffectiveFrom.Time.Add(time.Hour)
		}},
		{"wrong_version_resource", func(r *OriginalRows) { r.PeriodicBudgetConfigurationVersions[0].ControlID = "wrong" }},
		{"wrong_version_actor", func(r *OriginalRows) { r.PeriodicBudgetConfigurationVersions[0].Actor = "wrong" }},
		{"wrong_version_xid", func(r *OriginalRows) { r.PeriodicBudgetConfigurationVersions[0].CreatedXID = "9" }},
		{"wrong_version_time", func(r *OriginalRows) {
			r.PeriodicBudgetConfigurationVersions[0].CreatedAt.Time = r.PeriodicBudgetConfigurationVersions[0].CreatedAt.Time.Add(time.Microsecond)
		}},
		{"wrong_version_configuration", func(r *OriginalRows) { r.PeriodicBudgetConfigurationVersions[0].Enabled = false }},
		{"duplicate_scope", func(r *OriginalRows) {
			c := r.PeriodicBudgetControls[0]
			c.ID = sourcePBRelTestID(98)
			r.PeriodicBudgetControls = append(r.PeriodicBudgetControls, c)
		}},
		{"sparse_max_revision", func(r *OriginalRows) { r.PeriodicBudgetControls[0].Revision = "9223372036854775807" }},
		{"noop_claimed_changed", func(r *OriginalRows) { r.PeriodicBudgetConfigurationVersions[0].OperationID = sourcePBRelTestID(99) }},
		{"snapshot_tracking", func(r *OriginalRows) {
			r.PeriodicBudgetControls[0].TrackingStartedAt.Time = r.PeriodicBudgetControls[0].TrackingStartedAt.Time.Add(time.Hour)
			r.PeriodicBudgetObservationOrigins[0].TrackingStartedAt = r.PeriodicBudgetControls[0].TrackingStartedAt
		}},
		{"snapshot_scope_identity", func(r *OriginalRows) {
			id := sourcePBRelTestID(99)
			r.Tenants = append(r.Tenants, TenantRow{ID: id})
			r.PeriodicBudgetControls[0].TenantID = &id
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			v := sourcePBRelFixture()
			tt.edit(&v.OriginalRows)
			sourcePBRelReject(t, v)
		})
	}
	for _, name := range []string{"unchanged_version", "gap", "reused_operation", "wrong_noop_cas", "noop_config_pointer"} {
		t.Run(name, func(t *testing.T) {
			v := sourcePBRelFixture()
			sourcePBRelAddOperation(&v, name != "wrong_noop_cas" && name != "noop_config_pointer")
			r := &v.OriginalRows
			switch name {
			case "unchanged_version":
				r.PeriodicBudgetConfigurationVersions[1].LimitAmount = "100"
			case "gap":
				r.PeriodicBudgetConfigurationVersions = r.PeriodicBudgetConfigurationVersions[1:]
			case "reused_operation":
				r.PeriodicBudgetConfigurationVersions[1].OperationID = r.PeriodicBudgetConfigurationVersions[0].OperationID
			case "wrong_noop_cas":
				o := &r.PeriodicBudgetOperations[1]
				o.SafeInput = sourcePBInputJSON
				o.InputFingerprint = sourcePBTestFingerprint(o.Kind, o.Actor, o.Scope, o.Target, "0", "utc_day", "true", "100")
			case "noop_config_pointer":
				r.PeriodicBudgetObservationJournal[1].ConfigurationOperationID = r.PeriodicBudgetOperations[1].OperationID
			}
			sourcePBRelReject(t, v)
		})
	}
}

func TestSourcePBOperationRelationsAuditExactReverseAndStrictJSON(t *testing.T) {
	for _, edit := range []func(*OriginalRows){
		func(r *OriginalRows) { r.AuditEvents = nil },
		func(r *OriginalRows) {
			a := r.AuditEvents[0]
			a.ID = sourcePBRelTestID(99)
			r.AuditEvents = append(r.AuditEvents, a)
		},
		func(r *OriginalRows) { r.AuditEvents[0].Target = sourcePBRelTestID(99) },
		func(r *OriginalRows) { r.AuditEvents[0].Actor = "wrong" },
		func(r *OriginalRows) { r.AuditEvents[0].TenantScope = sourcePBRelPointer(sourcePBRelTestID(1)) },
		func(r *OriginalRows) { r.AuditEvents[0].RequestID = "wrong" },
		func(r *OriginalRows) {
			r.AuditEvents[0].CreatedAt.Time = r.AuditEvents[0].CreatedAt.Time.Add(time.Microsecond)
		},
		func(r *OriginalRows) { r.AuditEvents[0].PPRootID = sourcePBRelPointer("fake") },
	} {
		v := sourcePBRelFixture()
		edit(&v.OriginalRows)
		sourcePBRelReject(t, v)
	}
	for _, diff := range []string{
		`null`, `[]`, `{}`, `{"control_id":"x","revision":"1","scope":"tenant","scope_id":"x","changed":"true"}`,
		`{"control_id":"x","revision":"1","scope":"tenant","scope_id":"x","changed":true,"\u0063hanged":true}`,
		`{"control_id":"x","revision":"1","scope":"tenant","scope_id":"x","changed":true,"unknown":true}`,
		`{"control_id":"x","revision":"1","scope":"tenant","scope_id":"x","changed":true} {}`,
	} {
		v := sourcePBRelFixture()
		v.OriginalRows.AuditEvents[0].SanitizedDiff = diff
		if got := sourcePBRelReject(t, v); got.InvalidScalarRows != 1 {
			t.Fatalf("bad audit JSON did not keep scalar ownership: %+v", got)
		}
	}
	v := sourcePBRelFixture()
	v.OriginalRows.AuditEvents[0].SanitizedDiff = strings.Replace(v.OriginalRows.AuditEvents[0].SanitizedDiff, `"changed":true`, `"changed":false`, 1)
	if got := sourcePBRelReject(t, v); got.InvalidScalarRows != 0 || got.OriginalPointerRows < 1 {
		t.Fatal("valid shape with wrong changed was not a relation mismatch", got)
	}
	v = sourcePBRelFixture()
	v.OriginalRows.AuditEvents[0].SanitizedDiff = " \n" + strings.Replace(v.OriginalRows.AuditEvents[0].SanitizedDiff, `"scope"`, `"\u0073cope"`, 1) + " \t"
	sourcePBRelClean(t, v)
}

func TestSourcePBOperationRelationsOriginsBindingsAndCompleteChain(t *testing.T) {
	cases := []struct {
		name string
		edit func(*OriginalRows)
	}{
		{"missing_origin", func(r *OriginalRows) { r.PeriodicBudgetObservationOrigins = nil }},
		{"bad_origin", func(r *OriginalRows) { r.PeriodicBudgetObservationOrigins[0].CreatedXID = "2" }},
		{"missing_head", func(r *OriginalRows) { r.PeriodicBudgetObservationHeads = nil }},
		{"orphan_head", func(r *OriginalRows) { r.PeriodicBudgetObservationHeads[0].ControlID = sourcePBRelTestID(99) }},
		{"missing_journal", func(r *OriginalRows) { r.PeriodicBudgetObservationJournal = nil }},
		{"missing_command_pointer", func(r *OriginalRows) { r.PeriodicBudgetOperations[0].ObservationID = nil }},
		{"wrong_command_pointer", func(r *OriginalRows) {
			r.PeriodicBudgetOperations[0].ObservationID = sourcePBRelPointer(sourcePBRelTestID(99))
		}},
		{"wrong_source", func(r *OriginalRows) { r.PeriodicBudgetObservationJournal[0].SourceID = sourcePBRelTestID(99) }},
		{"wrong_discriminator", func(r *OriginalRows) { r.PeriodicBudgetObservationJournal[0].CommandSourceID = nil }},
		{"wrong_xid", func(r *OriginalRows) { r.PeriodicBudgetObservationJournal[0].CreatedXID = "2" }},
		{"wrong_time", func(r *OriginalRows) {
			r.PeriodicBudgetObservationJournal[0].ObservedAt.Time = r.PeriodicBudgetObservationJournal[0].ObservedAt.Time.Add(time.Microsecond)
		}},
		{"wrong_configuration_operation", func(r *OriginalRows) {
			r.PeriodicBudgetObservationJournal[0].ConfigurationOperationID = sourcePBRelTestID(99)
		}},
		{"wrong_configuration_revision", func(r *OriginalRows) { r.PeriodicBudgetObservationJournal[0].ConfigurationRevision = "2" }},
		{"head_tail", func(r *OriginalRows) { r.PeriodicBudgetObservationHeads[0].LastObservationID = nil }},
		{"head_xid", func(r *OriginalRows) { r.PeriodicBudgetObservationHeads[0].UpdatedXID = "2" }},
		{"head_ordinal", func(r *OriginalRows) { r.PeriodicBudgetObservationHeads[0].Ordinal = "2" }},
		{"skip_ordinal", func(r *OriginalRows) { r.PeriodicBudgetObservationJournal[0].Ordinal = "2" }},
		{"wrong_previous", func(r *OriginalRows) {
			r.PeriodicBudgetObservationJournal[0].PreviousID = sourcePBRelPointer(sourcePBRelTestID(99))
		}},
		{"orphan_journal", func(r *OriginalRows) {
			j := r.PeriodicBudgetObservationJournal[0]
			j.ID = sourcePBRelTestID(99)
			j.Ordinal = "2"
			j.PreviousID = sourcePBRelPointer(r.PeriodicBudgetObservationJournal[0].ID)
			r.PeriodicBudgetObservationJournal = append(r.PeriodicBudgetObservationJournal, j)
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			v := sourcePBRelFixture()
			tt.edit(&v.OriginalRows)
			sourcePBRelReject(t, v)
		})
	}
	// A missing origin with any surviving pointer is contradictory coverage,
	// whereas the fully NULL legacy cluster remains intentionally uncovered.
	v := sourcePBRelFixture()
	sourcePBRelLegacy(&v)
	sourcePBRelClean(t, v)
	v.OriginalRows.PeriodicBudgetOperations[0].ObservationID = sourcePBRelPointer(sourcePBRelTestID(21))
	sourcePBRelReject(t, v)
}

func TestSourcePBOperationRelationsWindowBirthOrderAndNoDynamicAmountClaim(t *testing.T) {
	for _, before := range []bool{true, false} {
		v := sourcePBRelFixture()
		sourcePBRelWindow(&v, before, false)
		sourcePBRelClean(t, v)
		// Later current amounts/revision are not this leaf's historical oracle.
		v.OriginalRows.PeriodicBudgetWindows[0].HeldAmount = "999999999999999999999999999"
		v.OriginalRows.PeriodicBudgetWindows[0].Revision = "9"
		sourcePBRelClean(t, v)
		sourcePBRelLegacy(&v)
		sourcePBRelClean(t, v)
	}
	v := sourcePBRelFixture()
	sourcePBRelWindow(&v, true, true)
	sourcePBRelClean(t, v)
	// Valid shape but deliberately wrong dynamic amount is outside this slice.
	v.OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot = strings.Replace(v.OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot, `"held_amount":"10"`, `"held_amount":"7"`, 1)
	sourcePBRelClean(t, v)
	for _, edit := range []func(*OriginalRows){
		func(r *OriginalRows) { r.PeriodicBudgetWindows[0].ObservationID = nil },
		func(r *OriginalRows) { r.PeriodicBudgetWindows[0].ObservationCreatedXID = nil },
		func(r *OriginalRows) { r.PeriodicBudgetWindows[0].ObservationCreatedXID = sourcePBRelPointer("9") },
		func(r *OriginalRows) { r.PeriodicBudgetObservationJournal[0].WindowSourceID = nil },
		func(r *OriginalRows) { r.PeriodicBudgetObservationJournal[0].WindowRevision = sourcePBRelPointer("1") },
		func(r *OriginalRows) {
			r.PeriodicBudgetObservationJournal[0].ObservedAt = r.PeriodicBudgetOperations[0].CreatedAt
		},
		func(r *OriginalRows) {
			r.PeriodicBudgetWindows[0].EndAt.Time = r.PeriodicBudgetWindows[0].EndAt.Time.Add(time.Hour)
		},
		func(r *OriginalRows) { r.PeriodicBudgetOperations[0].ResultSnapshot = sourcePBSnapshotJSON },
		func(r *OriginalRows) {
			r.PeriodicBudgetObservationJournal[1].WindowID = nil
			r.PeriodicBudgetObservationJournal[1].WindowRevision = nil
		},
		func(r *OriginalRows) {
			w := r.PeriodicBudgetWindows[0]
			w.ID = sourcePBRelTestID(99)
			r.PeriodicBudgetWindows = append(r.PeriodicBudgetWindows, w)
		},
		func(r *OriginalRows) { r.PeriodicBudgetWindows = nil },
	} {
		v := sourcePBRelFixture()
		sourcePBRelWindow(&v, true, false)
		edit(&v.OriginalRows)
		sourcePBRelReject(t, v)
	}
	v = sourcePBRelFixture()
	sourcePBRelWindow(&v, false, false)
	v.OriginalRows.PeriodicBudgetObservationJournal[0].WindowID, v.OriginalRows.PeriodicBudgetObservationJournal[0].WindowRevision = sourcePBRelPointer(sourcePBRelTestID(4)), sourcePBRelPointer("0")
	v.OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot = strings.Replace(sourcePBSnapshotJSON, `"current_window":null`, `"current_window":`+sourcePBWindowJSON, 1)
	sourcePBRelReject(t, v)
}

func TestSourcePBOperationRelationsRealEventCutsAndBirthEdges(t *testing.T) {
	for _, edit := range []func(*OriginalRows){
		func(r *OriginalRows) {
			r.PeriodicBudgetObservationJournal[0].EventSeq = "1"
			r.PeriodicBudgetObservationJournal[0].LastEventID = sourcePBRelPointer(r.PeriodicBudgetEvents[0].ID)
		},
		func(r *OriginalRows) {
			r.PeriodicBudgetObservationJournal[1].LastEventID = sourcePBRelPointer(sourcePBRelTestID(99))
		},
		func(r *OriginalRows) { r.PeriodicBudgetObservationJournal[1].EventSeq = "2" },
		func(r *OriginalRows) {
			r.PeriodicBudgetObservationJournal[1].WindowLastEventID = sourcePBRelPointer(sourcePBRelTestID(99))
		},
		func(r *OriginalRows) {
			r.PeriodicBudgetAllocations[0].WindowID = sourcePBRelPointer(sourcePBRelTestID(99))
		},
		func(r *OriginalRows) { r.PeriodicBudgetAllocations = nil },
		func(r *OriginalRows) { r.PeriodicBudgetEvents[0].ControlID = "wrong" },
		func(r *OriginalRows) {
			e := r.PeriodicBudgetEvents[0]
			e.ID = sourcePBRelTestID(99)
			r.PeriodicBudgetEvents = append(r.PeriodicBudgetEvents, e)
		},
	} {
		v := sourcePBRelFixture()
		sourcePBRelWindow(&v, true, true)
		edit(&v.OriginalRows)
		sourcePBRelReject(t, v)
	}
}

func TestSourcePBOperationRelationsPhysicalDedupAndPermutation(t *testing.T) {
	v := sourcePBRelFixture()
	sourcePBRelAddOperation(&v, true)
	r := &v.OriginalRows
	duplicate := r.PeriodicBudgetConfigurationVersions[0]
	duplicate.Enabled = false
	r.PeriodicBudgetConfigurationVersions = append(r.PeriodicBudgetConfigurationVersions, duplicate)
	j := r.PeriodicBudgetObservationJournal[0]
	j.CreatedXID = "99"
	r.PeriodicBudgetObservationJournal = append(r.PeriodicBudgetObservationJournal, j)
	a := sourcePBRelReject(t, v)
	for _, slice := range []reflect.Value{reflect.ValueOf(r.PeriodicBudgetOperations), reflect.ValueOf(r.PeriodicBudgetConfigurationVersions), reflect.ValueOf(r.PeriodicBudgetObservationJournal), reflect.ValueOf(r.AuditEvents)} {
		swap := reflect.Swapper(slice.Interface())
		for i := 0; i < slice.Len()/2; i++ {
			swap(i, slice.Len()-1-i)
		}
	}
	b := sourcePBRelReject(t, v)
	if a != b || a.DuplicateRows != 2 {
		t.Fatalf("permutation or physical duplicate unstable: %+v %+v", a, b)
	}
}

func TestSourcePBOperationRelationsAmbiguousPhysicalObjectsNeverSupplySlots(t *testing.T) {
	// A single physical audit ID cannot be consumed once per business target.
	v := sourcePBRelFixture()
	sourcePBRelAddOperation(&v, false)
	v.OriginalRows.AuditEvents[1].ID = v.OriginalRows.AuditEvents[0].ID
	a := sourcePBRelReject(t, v)
	if a.DuplicateRows != 1 || a.OriginalPointerRows < 3 {
		t.Fatal("ambiguous audit supplied either target", a)
	}
	v.OriginalRows.AuditEvents[0], v.OriginalRows.AuditEvents[1] = v.OriginalRows.AuditEvents[1], v.OriginalRows.AuditEvents[0]
	if b := sourcePBRelReject(t, v); b != a {
		t.Fatal("same-ID different-target audits depend on order", a, b)
	}
	for _, kind := range []string{"operation", "window", "event", "journal"} {
		t.Run(kind, func(t *testing.T) {
			v := sourcePBRelFixture()
			sourcePBRelWindow(&v, true, true)
			r := &v.OriginalRows
			switch kind {
			case "operation":
				o := r.PeriodicBudgetOperations[0]
				o.ResourceID = sourcePBRelTestID(99)
				r.PeriodicBudgetOperations = append(r.PeriodicBudgetOperations, o)
			case "window":
				w := r.PeriodicBudgetWindows[0]
				w.ControlID = sourcePBRelTestID(99)
				r.PeriodicBudgetWindows = append(r.PeriodicBudgetWindows, w)
			case "event":
				e := r.PeriodicBudgetEvents[0]
				e.Seq = "2"
				r.PeriodicBudgetEvents = append(r.PeriodicBudgetEvents, e)
			case "journal":
				j := r.PeriodicBudgetObservationJournal[0]
				j.ControlID = sourcePBRelTestID(99)
				r.PeriodicBudgetObservationJournal = append(r.PeriodicBudgetObservationJournal, j)
			}
			if got := sourcePBRelReject(t, v); got.DuplicateRows != 1 || got.OriginalPointerRows == 0 {
				t.Fatal("ambiguous physical object was consumed", got)
			}
		})
	}
	// A shared textual ID across different physical tables is not duplication.
	v = sourcePBRelFixture()
	v.OriginalRows.AuditEvents[0].ID = v.OriginalRows.PeriodicBudgetOperations[0].OperationID
	sourcePBRelClean(t, v)
}

func TestSourcePBOperationRelationsBoundedLegacyOperationBatch(t *testing.T) {
	v := sourcePBRelFixture()
	sourcePBRelLegacy(&v)
	r := &v.OriginalRows
	first, audit := r.PeriodicBudgetOperations[0], r.AuditEvents[0]
	for i := 0; i < 1000; i++ {
		o := first
		o.OperationID = sourcePBRelTestID(1000 + i)
		o.SafeInput = strings.Replace(sourcePBInputJSON, `"expected_revision":"0"`, `"expected_revision":"1"`, 1)
		o.InputFingerprint = sourcePBTestFingerprint(o.Kind, o.Actor, o.Scope, o.Target, "1", "utc_day", "true", "100")
		a := audit
		a.ID, a.Target = sourcePBRelTestID(3000+i), o.OperationID
		a.SanitizedDiff = strings.Replace(a.SanitizedDiff, `"changed":true`, `"changed":false`, 1)
		r.PeriodicBudgetOperations = append(r.PeriodicBudgetOperations, o)
		r.AuditEvents = append(r.AuditEvents, a)
	}
	sourcePBRelClean(t, v)
}

func TestSourcePBOperationRelationsBoundsCancelAndZeroErrors(t *testing.T) {
	if err := (*sourceClosureState)(nil).pbOperationRelations(); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tt := range []struct {
		ctx context.Context
		err error
	}{{nil, ErrInput}, {ctx, ErrCanceled}} {
		r, err := sourcePBRelRun(tt.ctx, sourcePBRelFixture())
		if !errors.Is(err, tt.err) || r != (SourceClosure{}) {
			t.Fatal("execution failure leaked partial result", r, err)
		}
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if r, err := sourcePBRelRun(expired, sourcePBRelFixture()); !errors.Is(err, ErrTimeout) || r != (SourceClosure{}) {
		t.Fatal(r, err)
	}
	v := sourcePBRelFixture()
	v.OriginalRows.AuditEvents[0].SanitizedDiff = strings.Repeat(" ", 65537)
	if r, err := sourcePBRelRun(context.Background(), v); !errors.Is(err, ErrResource) || r != (SourceClosure{}) {
		t.Fatal(r, err)
	}
	w, _, err := newReplayWork(context.Background(), Rows{})
	if err != nil {
		t.Fatal(err)
	}
	w.b.deadline = time.Now().Add(-time.Second)
	s := sourceClosureState{w: w, tables: make(map[string][]sourceRawRow), bad: make(map[string]keySet)}
	if err := s.pbOperationRelations(); !errors.Is(err, ErrTimeout) {
		t.Fatal("lost original absolute deadline", err)
	}
}
