package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func pbEventAt(text string) Time {
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		panic(err)
	}
	return Time{Time: at, Valid: true}
}

func pbEventControl(id, scope, tracking, high, held, spent, seq, tail string) PeriodicBudgetControlRow {
	c := PeriodicBudgetControlRow{ID: id, Scope: scope, PeriodKind: "utc_day", Enabled: true, LimitAmount: "100", Revision: "1", CreatedXID: "1", TrackingStartedAt: pbEventAt(tracking), EffectiveFrom: pbEventAt("2026-11-02T00:00:00Z"), MaxObservedWindowStart: pbEventAt(high), HeldAmount: held, SpentAmount: spent, EventSeq: seq}
	if tail != "" {
		c.LastEventID = sourcePtr(tail)
	}
	switch scope {
	case "tenant":
		c.TenantID = sourcePtr("tenant")
	case "key":
		c.TenantID, c.KeyID = sourcePtr("tenant"), sourcePtr("key")
	case "procurement":
		c.AccountID = sourcePtr("account")
	}
	return c
}

// The literal values are hand derived from 10 reserved and 7 charged. These
// are local relation fixtures, not full PP roots or a commercial acceptance.
func pbEventCustomerFixture(origin, terminal string) Rows {
	c := pbEventControl("control", "tenant", "2026-11-01T12:00:00Z", "2026-11-04T00:00:00Z", "10", "0", "1", "birth")
	r := RequestRecordRow{ID: "request", TenantID: "tenant", KeyID: "key", PBCreatedXID: sourcePtr("2"), PBAdmittedAt: pbEventAt("2026-11-04T01:00:00Z"), PBTenantControl: sourcePtr("control")}
	v := ReservationRow{ID: "reserve", TenantID: "tenant", RequestID: "request", Amount: "10", Status: "reserved"}
	a := PeriodicBudgetAllocationRow{ID: "allocation", ControlID: "control", SubjectType: "customer_request", SubjectID: "request", Origin: origin, OriginSourceID: "reserve", WindowID: sourcePtr("window"), AdmittedAt: r.PBAdmittedAt, UpperAmount: "10", Baseline: "0", SourceHeld: "10", SourceSpent: "0", HeldAmount: "10", SpentAmount: "0", State: "held", Revision: "1", LastEventID: sourcePtr("birth"), CreatedXID: "2"}
	e := PeriodicBudgetEventRow{ID: "birth", ControlID: "control", AllocationID: "allocation", SourceKind: "reservation", SourceID: "reserve", Seq: "1", AllocationBeforeRevision: "0", WindowBeforeRevision: sourcePtr("0"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "10", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: "10", SpentDelta: "0", CreatedXID: "2"}
	w := PeriodicBudgetWindowRow{ID: "window", ControlID: "control", StartAt: pbEventAt("2026-11-04T00:00:00Z"), EndAt: pbEventAt("2026-11-05T00:00:00Z"), HeldAmount: "10", SpentAmount: "0", Revision: "1", LastEventID: sourcePtr("birth")}
	rows := Rows{OriginalRows: OriginalRows{Tenants: []TenantRow{{ID: "tenant"}}, APIKeys: []APIKeyRow{{ID: "key", TenantID: "tenant"}}}}
	if origin == "legacy_import" {
		r.PBCreatedXID, r.PBTenantControl, r.PBAdmittedAt = nil, nil, Time{}
		a.OriginSourceID, a.WindowID, a.AdmittedAt, a.CreatedXID = "request", nil, Time{}, "1"
		e.SourceKind, e.SourceID, e.WindowBeforeRevision, e.CreatedXID = "legacy_import", "request", nil, "1"
		c.MaxObservedWindowStart = pbEventAt("2026-11-01T00:00:00Z")
	} else {
		rows.OriginalRows.PeriodicBudgetWindows = []PeriodicBudgetWindowRow{w}
	}
	rows.OriginalRows.PeriodicBudgetEvents = []PeriodicBudgetEventRow{e}
	if terminal != "" {
		charge := "7"
		if terminal == "released" {
			charge = "0"
		}
		v.Status = terminal
		rows.OriginalRows.Settlements = []SettlementRow{{ID: "settlement", RequestID: "request", TenantID: "tenant", Status: terminal, ChargedAmount: charge, JournalID: "journal"}}
		rows.OriginalRows.Journals = []JournalRow{{ID: "journal", TenantID: "tenant", OperationID: "settlement", CreatedTxid: "3"}}
		finish := PeriodicBudgetEventRow{ID: "finish", ControlID: "control", AllocationID: "allocation", SourceKind: "settlement", SourceID: "settlement", Seq: "2", AllocationBeforeRevision: "1", WindowBeforeRevision: sourcePtr("1"), BeforeHeld: "10", BeforeSpent: "0", AfterHeld: "0", AfterSpent: charge, AfterSourceSpent: charge, AfterState: terminal, HeldDelta: "-10", SpentDelta: charge, CreatedXID: "3"}
		if origin == "legacy_import" {
			finish.WindowBeforeRevision = nil
		}
		rows.OriginalRows.PeriodicBudgetEvents = append(rows.OriginalRows.PeriodicBudgetEvents, finish)
		a.SourceHeld, a.HeldAmount, a.SourceSpent, a.SpentAmount, a.State, a.Revision, a.LastEventID = "0", "0", charge, charge, terminal, "2", sourcePtr("finish")
		c.HeldAmount, c.SpentAmount, c.EventSeq, c.LastEventID = "0", charge, "2", sourcePtr("finish")
		if origin == "new" {
			w.HeldAmount, w.SpentAmount, w.Revision, w.LastEventID = "0", charge, "2", sourcePtr("finish")
			rows.OriginalRows.PeriodicBudgetWindows[0] = w
		}
	}
	rows.OriginalRows.RequestRecords = []RequestRecordRow{r}
	rows.OriginalRows.Reservations = []ReservationRow{v}
	rows.OriginalRows.PeriodicBudgetControls = []PeriodicBudgetControlRow{c}
	rows.OriginalRows.PeriodicBudgetAllocations = []PeriodicBudgetAllocationRow{a}
	return rows
}

func pbEventPE(id, kind, dh, ds, xid string) ProcurementBudgetEventRow {
	e := ProcurementBudgetEventRow{ID: id, EventKey: id, AttemptID: "attempt", AccountID: "account", Type: kind, HeldDelta: dh, SpentDelta: ds}
	if xid != "" {
		e.PBCreatedXID = sourcePtr(xid)
	}
	if kind == "reserved" {
		e.EventKey = "reserve:attempt"
	}
	return e
}

func pbEventSupplierFixture(origin string) Rows {
	rows := Rows{OriginalRows: OriginalRows{ProcurementAccounts: []ProcurementAccountRow{{ID: "account"}}, RequestRecords: []RequestRecordRow{{ID: "request", TenantID: "tenant"}}, UpstreamAttempts: []UpstreamAttemptRow{{ID: "attempt", TenantID: "tenant", RequestID: "request", CredentialID: "credential", ChannelID: "channel", PBCreatedXID: sourcePtr("2")}}, CredentialProcurementBindings: []CredentialProcurementBindingRow{{ID: "binding", CredentialID: "credential", ChannelID: "channel", AccountID: "account"}}}}
	p := ProcurementReservationRow{AttemptID: "attempt", AccountID: "account", BindingID: "binding", UpperAmount: "10", HeldAmount: "0", SpentAmount: "5", State: "settled", PBCreatedXID: sourcePtr("2"), PBAdmittedAt: pbEventAt("2026-11-04T01:00:00Z"), PBControl: sourcePtr("control")}
	c := pbEventControl("control", "procurement", "2026-11-01T12:00:00Z", "2026-11-04T00:00:00Z", "0", "5", "4", "correct")
	a := PeriodicBudgetAllocationRow{ID: "allocation", ControlID: "control", SubjectType: "supplier_attempt", SubjectID: "attempt", Origin: origin, OriginSourceID: "reserve", WindowID: sourcePtr("window"), AdmittedAt: p.PBAdmittedAt, UpperAmount: "10", Baseline: "0", SourceHeld: "0", SourceSpent: "5", HeldAmount: "0", SpentAmount: "5", State: "settled", Revision: "4", LastEventID: sourcePtr("correct"), CreatedXID: "2"}
	rows.OriginalRows.PeriodicBudgetWindows = []PeriodicBudgetWindowRow{{ID: "window", ControlID: "control", StartAt: pbEventAt("2026-11-04T00:00:00Z"), EndAt: pbEventAt("2026-11-05T00:00:00Z"), HeldAmount: "0", SpentAmount: "5", Revision: "4", LastEventID: sourcePtr("correct")}}
	rows.OriginalRows.ProcurementBudgetEvents = []ProcurementBudgetEventRow{pbEventPE("reserve", "reserved", "10", "0", "2"), pbEventPE("unknown", "consumption_unknown", "0", "0", "3"), pbEventPE("confirmed", "cost_confirmed", "-10", "7", "4"), pbEventPE("correction", "cost_corrected", "0", "-2", "5")}
	rows.OriginalRows.PeriodicBudgetEvents = []PeriodicBudgetEventRow{
		{ID: "birth", ControlID: "control", AllocationID: "allocation", SourceKind: "procurement_event", SourceID: "reserve", Seq: "1", AllocationBeforeRevision: "0", WindowBeforeRevision: sourcePtr("0"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "10", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: "10", SpentDelta: "0", CreatedXID: "2"},
		{ID: "zero", ControlID: "control", AllocationID: "allocation", SourceKind: "procurement_event", SourceID: "unknown", Seq: "2", AllocationBeforeRevision: "1", WindowBeforeRevision: sourcePtr("1"), BeforeHeld: "10", BeforeSpent: "0", AfterHeld: "10", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: "0", SpentDelta: "0", CreatedXID: "3"},
		{ID: "charge", ControlID: "control", AllocationID: "allocation", SourceKind: "procurement_event", SourceID: "confirmed", Seq: "3", AllocationBeforeRevision: "2", WindowBeforeRevision: sourcePtr("2"), BeforeHeld: "10", BeforeSpent: "0", AfterHeld: "0", AfterSpent: "7", AfterSourceSpent: "7", AfterState: "settled", HeldDelta: "-10", SpentDelta: "7", CreatedXID: "4"},
		{ID: "correct", ControlID: "control", AllocationID: "allocation", SourceKind: "procurement_event", SourceID: "correction", Seq: "4", AllocationBeforeRevision: "3", WindowBeforeRevision: sourcePtr("3"), BeforeHeld: "0", BeforeSpent: "7", AfterHeld: "0", AfterSpent: "5", AfterSourceSpent: "5", AfterState: "settled", HeldDelta: "0", SpentDelta: "-2", CreatedXID: "5"},
	}
	if origin != "new" {
		p.PBControl = nil
		rows.OriginalRows.PeriodicBudgetWindows = nil
		c.MaxObservedWindowStart = pbEventAt("2026-11-01T00:00:00Z")
		a.WindowID, a.AdmittedAt, a.Baseline, a.Revision, a.SpentAmount = nil, Time{}, "7", "2", "0"
		if origin == "legacy_import" {
			a.OriginSourceID, a.CreatedXID = "attempt", "1"
			rows.OriginalRows.ProcurementBudgetEvents = []ProcurementBudgetEventRow{pbEventPE("reserve", "reserved", "10", "0", ""), pbEventPE("confirmed", "cost_confirmed", "-10", "7", ""), pbEventPE("dispute", "evidence_disputed", "10", "0", ""), pbEventPE("correction", "cost_corrected", "-10", "-2", "5")}
			rows.OriginalRows.PeriodicBudgetEvents = []PeriodicBudgetEventRow{
				{ID: "import", ControlID: "control", AllocationID: "allocation", SourceKind: "legacy_import", SourceID: "attempt", Seq: "1", AllocationBeforeRevision: "0", BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "10", AfterSpent: "0", AfterSourceSpent: "7", AfterState: "disputed", HeldDelta: "10", SpentDelta: "0", CreatedXID: "1"},
				{ID: "correct", ControlID: "control", AllocationID: "allocation", SourceKind: "procurement_event", SourceID: "correction", Seq: "2", AllocationBeforeRevision: "1", BeforeHeld: "10", BeforeSpent: "0", AfterHeld: "0", AfterSpent: "0", AfterSourceSpent: "5", AfterState: "settled", HeldDelta: "-10", SpentDelta: "0", CreatedXID: "5"},
			}
			c.SpentAmount, c.EventSeq = "0", "2"
		} else {
			a.OriginSourceID, a.CreatedXID, a.Revision, a.SpentAmount, a.SourceSpent = "correction", "5", "1", "5", "12"
			p.SpentAmount = "12"
			rows.OriginalRows.ProcurementBudgetEvents = []ProcurementBudgetEventRow{pbEventPE("reserve", "reserved", "10", "0", ""), pbEventPE("confirmed", "cost_confirmed", "-10", "7", ""), pbEventPE("correction", "cost_corrected", "0", "5", "5")}
			rows.OriginalRows.PeriodicBudgetEvents = []PeriodicBudgetEventRow{{ID: "correct", ControlID: "control", AllocationID: "allocation", SourceKind: "procurement_event", SourceID: "correction", Seq: "1", AllocationBeforeRevision: "0", BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "0", AfterSpent: "5", AfterSourceSpent: "12", AfterState: "settled", HeldDelta: "0", SpentDelta: "5", CreatedXID: "5"}}
			c.EventSeq = "1"
		}
	}
	rows.OriginalRows.ProcurementReservations = []ProcurementReservationRow{p}
	rows.OriginalRows.PeriodicBudgetControls = []PeriodicBudgetControlRow{c}
	rows.OriginalRows.PeriodicBudgetAllocations = []PeriodicBudgetAllocationRow{a}
	return rows
}

func pbEventRun(t *testing.T, rows Rows) *sourceClosureState {
	t.Helper()
	s := sourceTestRows(t, rows)
	if err := s.pbEventRelationRows(); err != nil {
		t.Fatal(err)
	}
	return s
}

func pbEventClean(t *testing.T, rows Rows) {
	t.Helper()
	if got := pbEventRun(t, rows).bad["pointer"]; len(got) != 0 {
		t.Fatalf("unexpected PB relation errors: %v", got)
	}
}

func TestSourcePBEventRelationsLiteralLifecycles(t *testing.T) {
	for _, origin := range []string{"new", "legacy_import"} {
		for _, terminal := range []string{"", "settled", "released"} {
			t.Run("customer_"+origin+"_"+terminal, func(t *testing.T) { pbEventClean(t, pbEventCustomerFixture(origin, terminal)) })
		}
	}
	for _, origin := range []string{"new", "legacy_import", "legacy_catchup"} {
		t.Run("supplier_"+origin, func(t *testing.T) { pbEventClean(t, pbEventSupplierFixture(origin)) })
	}
}

func TestSourcePBEventRelationsEventAndProjectionMutations(t *testing.T) {
	for _, field := range []string{"ControlID", "AllocationID", "SourceKind", "SourceID", "Seq", "AllocationBeforeRevision", "WindowBeforeRevision", "BeforeHeld", "BeforeSpent", "AfterHeld", "AfterSpent", "AfterSourceSpent", "AfterState", "HeldDelta", "SpentDelta", "CreatedXID"} {
		t.Run(field, func(t *testing.T) {
			rows := pbEventSupplierFixture("new")
			v := reflect.ValueOf(&rows.OriginalRows.PeriodicBudgetEvents[2]).Elem().FieldByName(field)
			if v.Kind() == reflect.Pointer {
				v.Set(reflect.ValueOf(sourcePtr("99")))
			} else {
				v.SetString("99")
			}
			if len(pbEventRun(t, rows).bad["pointer"]) == 0 {
				t.Fatal("PB event mutation accepted")
			}
		})
	}
	for _, table := range []string{"allocation", "window", "control"} {
		for _, field := range []string{"HeldAmount", "SpentAmount", "Revision", "LastEventID"} {
			rows := pbEventSupplierFixture("new")
			var row reflect.Value
			switch table {
			case "allocation":
				row = reflect.ValueOf(&rows.OriginalRows.PeriodicBudgetAllocations[0]).Elem()
			case "window":
				row = reflect.ValueOf(&rows.OriginalRows.PeriodicBudgetWindows[0]).Elem()
			case "control":
				if field == "Revision" {
					field = "EventSeq" // Configuration revision is another leaf's contract.
				}
				row = reflect.ValueOf(&rows.OriginalRows.PeriodicBudgetControls[0]).Elem()
			}
			v := row.FieldByName(field)
			if v.Kind() == reflect.Pointer {
				v.Set(reflect.ValueOf(sourcePtr("wrong")))
			} else {
				v.SetString("99")
			}
			if len(pbEventRun(t, rows).bad["pointer"]) == 0 {
				t.Fatalf("%s.%s mutation accepted", table, field)
			}
		}
	}
}

func TestSourcePBEventRelationsLegacyAndReverseCollections(t *testing.T) {
	for _, mutate := range []func(*Rows){
		func(r *Rows) { r.OriginalRows.PeriodicBudgetEvents = r.OriginalRows.PeriodicBudgetEvents[1:] },
		func(r *Rows) {
			r.OriginalRows.PeriodicBudgetEvents = append(r.OriginalRows.PeriodicBudgetEvents, r.OriginalRows.PeriodicBudgetEvents[0])
		},
		func(r *Rows) {
			r.OriginalRows.PeriodicBudgetAllocations = append(r.OriginalRows.PeriodicBudgetAllocations, r.OriginalRows.PeriodicBudgetAllocations[0])
		},
		func(r *Rows) { r.OriginalRows.ProcurementBudgetEvents[0].AccountID = "other" },
		func(r *Rows) { r.OriginalRows.PeriodicBudgetEvents[0].CreatedXID = "8" },
		func(r *Rows) { r.OriginalRows.PeriodicBudgetAllocations[0].Baseline = "8" },
		func(r *Rows) { r.OriginalRows.PeriodicBudgetAllocations[0].WindowID = sourcePtr("window") },
		func(r *Rows) { r.OriginalRows.PeriodicBudgetAllocations[0].OriginSourceID = "other" },
	} {
		for _, origin := range []string{"legacy_import", "legacy_catchup"} {
			rows := pbEventSupplierFixture(origin)
			mutate(&rows)
			if len(pbEventRun(t, rows).bad["pointer"]) == 0 {
				t.Fatal("legacy source/baseline/collection mutation accepted")
			}
		}
	}
	rows := pbEventSupplierFixture("new")
	rows.OriginalRows.PeriodicBudgetEvents = append(rows.OriginalRows.PeriodicBudgetEvents[:1], rows.OriginalRows.PeriodicBudgetEvents[2:]...)
	if len(pbEventRun(t, rows).bad["pointer"]) == 0 {
		t.Fatal("missing zero delta PE mirror accepted")
	}
	rows = pbEventCustomerFixture("legacy_import", "settled")
	rows.OriginalRows.PeriodicBudgetEvents, rows.OriginalRows.PeriodicBudgetAllocations = nil, nil
	c := &rows.OriginalRows.PeriodicBudgetControls[0]
	c.HeldAmount, c.SpentAmount, c.EventSeq, c.LastEventID = "0", "0", "0", nil
	pbEventClean(t, rows) // Closed before tracking: missing membership remains historical, not fabricated.
	rows.OriginalRows.Reservations[0].Status = "reserved"
	rows.OriginalRows.Settlements = nil
	if len(pbEventRun(t, rows).bad["pointer"]) == 0 {
		t.Fatal("unfinished real source missed forward allocation duty")
	}
}

func TestSourcePBEventRelationsCalendarAndEmptyWindows(t *testing.T) {
	rows := pbEventCustomerFixture("new", "settled")
	rows.OriginalRows.PeriodicBudgetWindows[0].StartAt = pbEventAt("2026-11-04T08:00:00+08:00")
	pbEventClean(t, rows)
	rows.OriginalRows.PeriodicBudgetControls[0].Enabled, rows.OriginalRows.PeriodicBudgetControls[0].LimitAmount = false, "0"
	pbEventClean(t, rows) // Current configuration cannot reject a saved historical finish.
	empty := PeriodicBudgetWindowRow{ID: "empty", ControlID: "control", StartAt: pbEventAt("2026-11-05T00:00:00Z"), EndAt: pbEventAt("2026-11-06T00:00:00Z"), HeldAmount: "0", SpentAmount: "0", Revision: "0"}
	rows.OriginalRows.PeriodicBudgetWindows = append(rows.OriginalRows.PeriodicBudgetWindows, empty)
	rows.OriginalRows.PeriodicBudgetControls[0].MaxObservedWindowStart = empty.StartAt
	pbEventClean(t, rows)
	for _, mutate := range []func(*Rows){
		func(r *Rows) { r.OriginalRows.PeriodicBudgetWindows[0].EndAt = pbEventAt("2026-11-06T00:00:00Z") },
		func(r *Rows) {
			r.OriginalRows.PeriodicBudgetWindows[0].StartAt = pbEventAt("2026-11-04T00:00:00.000001Z")
		},
		func(r *Rows) {
			r.OriginalRows.PeriodicBudgetControls[0].EffectiveFrom = pbEventAt("2026-11-03T00:00:00Z")
		},
		func(r *Rows) {
			r.OriginalRows.PeriodicBudgetControls[0].MaxObservedWindowStart = pbEventAt("2026-11-03T00:00:00Z")
		},
	} {
		bad := pbEventCustomerFixture("new", "settled")
		mutate(&bad)
		if len(pbEventRun(t, bad).bad["pointer"]) == 0 {
			t.Fatal("calendar mutation accepted")
		}
	}
}

func pbEventDualScopeFixture() Rows {
	rows := pbEventCustomerFixture("new", "settled")
	key := pbEventCustomerFixture("new", "settled")
	c := key.OriginalRows.PeriodicBudgetControls[0]
	c.ID, c.Scope, c.KeyID, c.LastEventID = "key-control", "key", sourcePtr("key"), sourcePtr("key-finish")
	w := key.OriginalRows.PeriodicBudgetWindows[0]
	w.ID, w.ControlID, w.LastEventID = "key-window", "key-control", sourcePtr("key-finish")
	a := key.OriginalRows.PeriodicBudgetAllocations[0]
	a.ID, a.ControlID, a.WindowID, a.LastEventID = "key-allocation", "key-control", sourcePtr("key-window"), sourcePtr("key-finish")
	rows.OriginalRows.PeriodicBudgetControls = append(rows.OriginalRows.PeriodicBudgetControls, c)
	rows.OriginalRows.PeriodicBudgetWindows = append(rows.OriginalRows.PeriodicBudgetWindows, w)
	rows.OriginalRows.PeriodicBudgetAllocations = append(rows.OriginalRows.PeriodicBudgetAllocations, a)
	for i, e := range key.OriginalRows.PeriodicBudgetEvents {
		e.ID, e.ControlID, e.AllocationID = []string{"key-birth", "key-finish"}[i], "key-control", "key-allocation"
		rows.OriginalRows.PeriodicBudgetEvents = append(rows.OriginalRows.PeriodicBudgetEvents, e)
	}
	rows.OriginalRows.RequestRecords[0].PBKeyControl = sourcePtr("key-control")
	return rows
}

func pbEventInterleavedFixture() Rows {
	rows := pbEventCustomerFixture("new", "settled")
	r := rows.OriginalRows.RequestRecords[0]
	r.ID, r.PBCreatedXID, r.PBAdmittedAt = "request-two", sourcePtr("4"), pbEventAt("2026-11-05T01:00:00Z")
	v := ReservationRow{ID: "reserve-two", TenantID: "tenant", RequestID: "request-two", Amount: "4", Status: "pending_reconciliation"}
	a := PeriodicBudgetAllocationRow{ID: "allocation-two", ControlID: "control", SubjectType: "customer_request", SubjectID: "request-two", Origin: "new", OriginSourceID: "reserve-two", WindowID: sourcePtr("window-two"), AdmittedAt: r.PBAdmittedAt, UpperAmount: "4", Baseline: "0", SourceHeld: "4", SourceSpent: "0", HeldAmount: "4", SpentAmount: "0", State: "held", Revision: "1", LastEventID: sourcePtr("birth-two"), CreatedXID: "4"}
	w := PeriodicBudgetWindowRow{ID: "window-two", ControlID: "control", StartAt: pbEventAt("2026-11-05T00:00:00Z"), EndAt: pbEventAt("2026-11-06T00:00:00Z"), HeldAmount: "4", SpentAmount: "0", Revision: "1", LastEventID: sourcePtr("birth-two")}
	e := PeriodicBudgetEventRow{ID: "birth-two", ControlID: "control", AllocationID: "allocation-two", SourceKind: "reservation", SourceID: "reserve-two", Seq: "2", AllocationBeforeRevision: "0", WindowBeforeRevision: sourcePtr("0"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "4", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: "4", SpentDelta: "0", CreatedXID: "4"}
	rows.OriginalRows.PeriodicBudgetEvents[1].Seq = "3"
	rows.OriginalRows.PeriodicBudgetEvents = append(rows.OriginalRows.PeriodicBudgetEvents, e)
	rows.OriginalRows.RequestRecords = append(rows.OriginalRows.RequestRecords, r)
	rows.OriginalRows.Reservations = append(rows.OriginalRows.Reservations, v)
	rows.OriginalRows.PeriodicBudgetAllocations = append(rows.OriginalRows.PeriodicBudgetAllocations, a)
	rows.OriginalRows.PeriodicBudgetWindows = append(rows.OriginalRows.PeriodicBudgetWindows, w)
	c := &rows.OriginalRows.PeriodicBudgetControls[0]
	c.HeldAmount, c.EventSeq, c.MaxObservedWindowStart = "4", "3", w.StartAt
	return rows
}

func TestSourcePBEventRelationsScopesInterleavingAndCurrentHistory(t *testing.T) {
	pbEventClean(t, pbEventDualScopeFixture())
	rows := pbEventInterleavedFixture()
	pbEventClean(t, rows)
	// An arbitrary input permutation cannot replace the explicit control seq.
	rows.OriginalRows.PeriodicBudgetEvents[0], rows.OriginalRows.PeriodicBudgetEvents[2] = rows.OriginalRows.PeriodicBudgetEvents[2], rows.OriginalRows.PeriodicBudgetEvents[0]
	pbEventClean(t, rows)
	for _, mutate := range []func(*Rows){
		func(r *Rows) { r.OriginalRows.PeriodicBudgetControls[0].HeldAmount = "0" },
		func(r *Rows) { r.OriginalRows.PeriodicBudgetWindows[1].LastEventID = sourcePtr("finish") },
		func(r *Rows) { r.OriginalRows.PeriodicBudgetWindows[1].Revision = "3" },
		func(r *Rows) { r.OriginalRows.PeriodicBudgetEvents[2].WindowBeforeRevision = sourcePtr("1") },
	} {
		bad := pbEventInterleavedFixture()
		mutate(&bad)
		if len(pbEventRun(t, bad).bad["pointer"]) == 0 {
			t.Fatal("cross-window projection/revision mutation accepted")
		}
	}
	rows = pbEventDualScopeFixture()
	rows.OriginalRows.Tenants[0].ID = ""
	rows.OriginalRows.APIKeys[0].TenantID, rows.OriginalRows.RequestRecords[0].TenantID, rows.OriginalRows.Reservations[0].TenantID, rows.OriginalRows.Settlements[0].TenantID, rows.OriginalRows.Journals[0].TenantID = "", "", "", "", ""
	for i := range rows.OriginalRows.PeriodicBudgetControls {
		rows.OriginalRows.PeriodicBudgetControls[i].TenantID = sourcePtr("")
	}
	pbEventClean(t, rows)
	rows.OriginalRows.PeriodicBudgetControls[0].TenantID = nil
	if len(pbEventRun(t, rows).bad["pointer"]) == 0 {
		t.Fatal("legacy TEXT tenant was conflated with SQL NULL")
	}
}

func TestSourcePBEventRelationsNumeric100AndZeroEffects(t *testing.T) {
	rows := pbEventSupplierFixture("new")
	large, after := strings.Repeat("9", 100), strings.Repeat("9", 99)+"7"
	rows.OriginalRows.ProcurementBudgetEvents[2].SpentDelta = large
	rows.OriginalRows.PeriodicBudgetEvents[2].AfterSpent, rows.OriginalRows.PeriodicBudgetEvents[2].AfterSourceSpent, rows.OriginalRows.PeriodicBudgetEvents[2].SpentDelta = large, large, large
	rows.OriginalRows.PeriodicBudgetEvents[3].BeforeSpent, rows.OriginalRows.PeriodicBudgetEvents[3].AfterSpent, rows.OriginalRows.PeriodicBudgetEvents[3].AfterSourceSpent = large, after, after
	rows.OriginalRows.ProcurementReservations[0].SpentAmount = after
	rows.OriginalRows.PeriodicBudgetAllocations[0].SpentAmount, rows.OriginalRows.PeriodicBudgetAllocations[0].SourceSpent = after, after
	rows.OriginalRows.PeriodicBudgetWindows[0].SpentAmount, rows.OriginalRows.PeriodicBudgetControls[0].SpentAmount = after, after
	pbEventClean(t, rows)
	rows.OriginalRows.PeriodicBudgetEvents[2].SpentDelta = strings.Repeat("9", 101)
	if len(pbEventRun(t, rows).bad["pointer"]) == 0 {
		t.Fatal("NUMERIC100 delta boundary was relaxed")
	}
	rows = pbEventSupplierFixture("new")
	rows.OriginalRows.ProcurementReservations[0].UpperAmount, rows.OriginalRows.ProcurementReservations[0].SpentAmount = "0", "0"
	rows.OriginalRows.PeriodicBudgetAllocations[0].UpperAmount, rows.OriginalRows.PeriodicBudgetAllocations[0].SpentAmount, rows.OriginalRows.PeriodicBudgetAllocations[0].SourceSpent = "0", "0", "0"
	rows.OriginalRows.PeriodicBudgetControls[0].SpentAmount, rows.OriginalRows.PeriodicBudgetWindows[0].SpentAmount = "0", "0"
	for i := range rows.OriginalRows.ProcurementBudgetEvents {
		rows.OriginalRows.ProcurementBudgetEvents[i].HeldDelta, rows.OriginalRows.ProcurementBudgetEvents[i].SpentDelta = "0", "0"
	}
	for i := range rows.OriginalRows.PeriodicBudgetEvents {
		e := &rows.OriginalRows.PeriodicBudgetEvents[i]
		e.BeforeHeld, e.BeforeSpent, e.AfterHeld, e.AfterSpent, e.AfterSourceSpent, e.HeldDelta, e.SpentDelta = "0", "0", "0", "0", "0", "0", "0"
	}
	pbEventClean(t, rows)
}

func TestSourcePBEventRelationsAmbiguousPhysicalAndBusinessKeys(t *testing.T) {
	for _, mutate := range []func(*Rows){
		func(r *Rows) {
			c := r.OriginalRows.PeriodicBudgetControls[0]
			c.ID = "second-control"
			r.OriginalRows.PeriodicBudgetControls = append(r.OriginalRows.PeriodicBudgetControls, c)
		},
		func(r *Rows) {
			w := r.OriginalRows.PeriodicBudgetWindows[0]
			w.ID = "second-window"
			r.OriginalRows.PeriodicBudgetWindows = append(r.OriginalRows.PeriodicBudgetWindows, w)
		},
		func(r *Rows) {
			a := r.OriginalRows.PeriodicBudgetAllocations[0]
			a.ID = "second-allocation"
			r.OriginalRows.PeriodicBudgetAllocations = append(r.OriginalRows.PeriodicBudgetAllocations, a)
		},
		func(r *Rows) {
			e := r.OriginalRows.PeriodicBudgetEvents[1]
			e.ID = "second-event"
			r.OriginalRows.PeriodicBudgetEvents = append(r.OriginalRows.PeriodicBudgetEvents, e)
		},
		func(r *Rows) {
			e := r.OriginalRows.PeriodicBudgetEvents[1]
			e.ID, e.Seq = "second-event", "5"
			r.OriginalRows.PeriodicBudgetEvents = append(r.OriginalRows.PeriodicBudgetEvents, e)
		},
		func(r *Rows) {
			pe := r.OriginalRows.ProcurementBudgetEvents[1]
			pe.ID = "second-pe"
			r.OriginalRows.ProcurementBudgetEvents = append(r.OriginalRows.ProcurementBudgetEvents, pe)
		},
		func(r *Rows) { r.OriginalRows.PeriodicBudgetEvents[1].Seq = "1" },
		func(r *Rows) { r.OriginalRows.PeriodicBudgetEvents[1].Seq = "01" },
	} {
		rows := pbEventSupplierFixture("new")
		mutate(&rows)
		s := pbEventRun(t, rows)
		if len(s.bad["pointer"]) == 0 {
			t.Fatal("ambiguous physical/business index consumed a candidate")
		}
		count := len(s.bad["pointer"])
		if err := s.pbEventRelationRows(); err != nil || len(s.bad["pointer"]) != count {
			t.Fatal("repeated reasons changed physical-key count", err)
		}
	}
	rows := pbEventSupplierFixture("new")
	rows.OriginalRows.PeriodicBudgetEvents[1].Seq = "1"
	s := sourceTestRows(t, rows)
	p := &sourcePBEventRelations{s: s, index: map[string]map[string][]sourceRawRow{}, scopes: map[string][]sourceRawRow{}, windowStarts: map[string][]sourceRawRow{}, subjects: map[string][]sourceRawRow{}, seqs: map[string][]sourceRawRow{}, effects: map[string][]sourceRawRow{}, reservations: map[string][]sourceRawRow{}, settlements: map[string][]sourceRawRow{}, peKeys: map[string][]sourceRawRow{}, pe: map[string][]sourceRawRow{}, controlEvents: map[string][]sourceRawRow{}, allocEvents: map[string][]sourceRawRow{}, allocations: map[string]*sourcePBEventAllocation{}, controlGood: map[string]bool{}, windowGood: map[string]bool{}, controlRun: map[string]*sourcePBEventBucket{}, windowRun: map[string]*sourcePBEventBucket{}, eventAnchored: map[string]bool{}, controlBroken: map[string]bool{}}
	p.prepare()
	p.controlsAndWindows()
	p.anchorEvents()
	a := &sourcePBEventAllocation{row: rows.OriginalRows.PeriodicBudgetAllocations[0], control: rows.OriginalRows.PeriodicBudgetControls[0]}
	for _, raw := range p.allocEvents["allocation"][:2] {
		if _, bound := p.boundPE(a, raw); bound {
			t.Fatal("same-seq different PE incorrectly deducted from residual")
		}
	}
}

func TestSourcePBEventRelationsGregorianMonthsAndFarYears(t *testing.T) {
	for _, tc := range []struct{ tracking, start, end string }{{"2024-02-29T12:00:00Z", "2024-02-01T00:00:00Z", "2024-03-01T00:00:00Z"}, {"2026-12-31T23:59:59Z", "2026-12-01T00:00:00Z", "2027-01-01T00:00:00Z"}} {
		c := pbEventControl("control", "tenant", tc.tracking, tc.start, "0", "0", "0", "")
		c.PeriodKind, c.EffectiveFrom = "utc_month", pbEventAt(tc.end)
		w := PeriodicBudgetWindowRow{ID: "window", ControlID: "control", StartAt: pbEventAt(tc.start), EndAt: pbEventAt(tc.end), HeldAmount: "0", SpentAmount: "0", Revision: "0"}
		rows := Rows{OriginalRows: OriginalRows{Tenants: []TenantRow{{ID: "tenant"}}, PeriodicBudgetControls: []PeriodicBudgetControlRow{c}, PeriodicBudgetWindows: []PeriodicBudgetWindowRow{w}}}
		pbEventClean(t, rows)
		rows.OriginalRows.PeriodicBudgetWindows[0].StartAt = pbEventAt("2023-01-01T00:00:00Z")
		if len(pbEventRun(t, rows).bad["pointer"]) == 0 {
			t.Fatal("window predating control tracking period accepted")
		}
	}
	tracking := Time{Time: time.Date(12000, 2, 29, 12, 0, 0, 0, time.UTC), Valid: true}
	start := Time{Time: time.Date(12000, 2, 1, 0, 0, 0, 0, time.UTC), Valid: true}
	end := Time{Time: time.Date(12000, 3, 1, 0, 0, 0, 0, time.UTC), Valid: true}
	c := pbEventControl("control", "tenant", "2026-11-01T12:00:00Z", "2026-11-01T00:00:00Z", "0", "0", "0", "")
	c.PeriodKind, c.TrackingStartedAt, c.EffectiveFrom, c.MaxObservedWindowStart = "utc_month", tracking, end, start
	pbEventClean(t, Rows{OriginalRows: OriginalRows{Tenants: []TenantRow{{ID: "tenant"}}, PeriodicBudgetControls: []PeriodicBudgetControlRow{c}, PeriodicBudgetWindows: []PeriodicBudgetWindowRow{{ID: "window", ControlID: "control", StartAt: start, EndAt: end, HeldAmount: "0", SpentAmount: "0", Revision: "0"}}}})
}

func TestSourcePBEventRelationsWrongAnchorsCannotPolluteOtherScope(t *testing.T) {
	for i := 0; i < 64; i++ {
		rows := pbEventDualScopeFixture()
		rows.OriginalRows.PeriodicBudgetAllocations[0].WindowID = sourcePtr("key-window")
		s := pbEventRun(t, rows)
		for _, item := range []struct{ table, id string }{{"periodic_budget_allocations", "key-allocation"}, {"periodic_budget_windows", "key-window"}, {"periodic_budget_events", "key-birth"}, {"periodic_budget_events", "key-finish"}, {"periodic_budget_controls", "key-control"}} {
			if _, bad := s.bad["pointer"][tupleKey(item.table, tupleKey(item.id))]; bad {
				t.Fatalf("wrong-window claimant poisoned unrelated valid scope on round %d: %v", i, s.bad["pointer"])
			}
		}
	}
	rows := pbEventDualScopeFixture()
	wrong := rows.OriginalRows.PeriodicBudgetEvents[0]
	wrong.ID, wrong.ControlID = "wrong-anchor", "key-control"
	rows.OriginalRows.PeriodicBudgetEvents = append(rows.OriginalRows.PeriodicBudgetEvents, wrong)
	s := pbEventRun(t, rows)
	for _, id := range []string{"key-birth", "key-finish"} {
		if _, bad := s.bad["pointer"][tupleKey("periodic_budget_events", tupleKey(id))]; bad {
			t.Fatal("wrong-control child entered valid scope's sequence index")
		}
	}
	// A missing zero-valued origin still invalidates its real control/window;
	// absence must not quietly become a valid zero-event projection.
	rows = pbEventCustomerFixture("new", "")
	rows.OriginalRows.Reservations[0].Amount = "0"
	a := &rows.OriginalRows.PeriodicBudgetAllocations[0]
	a.UpperAmount, a.HeldAmount, a.SourceHeld = "0", "0", "0"
	c := &rows.OriginalRows.PeriodicBudgetControls[0]
	c.HeldAmount, c.EventSeq, c.LastEventID = "0", "0", nil
	w := &rows.OriginalRows.PeriodicBudgetWindows[0]
	w.HeldAmount, w.Revision, w.LastEventID = "0", "0", nil
	rows.OriginalRows.PeriodicBudgetEvents = nil
	s = pbEventRun(t, rows)
	for _, item := range []struct{ table, id string }{{"periodic_budget_allocations", "allocation"}, {"periodic_budget_windows", "window"}, {"periodic_budget_controls", "control"}} {
		if _, bad := s.bad["pointer"][tupleKey(item.table, tupleKey(item.id))]; !bad {
			t.Fatalf("missing zero origin laundered projection %s: %v", item.table, s.bad["pointer"])
		}
	}
}

func TestSourcePBEventRelationsActualSourceCounterparts(t *testing.T) {
	rows := pbEventSupplierFixture("legacy_import")
	rows.OriginalRows.PeriodicBudgetEvents[1].CreatedXID = "99"
	s := pbEventRun(t, rows)
	if _, bad := s.bad["pointer"][tupleKey("procurement_budget_events", tupleKey("correction"))]; !bad {
		t.Fatal("existing actual PE escaped a wrong same-tx claim")
	}
	rows = pbEventCustomerFixture("new", "settled")
	rows.OriginalRows.Journals[0].CreatedTxid = "99"
	s = pbEventRun(t, rows)
	for _, item := range []struct{ table, id string }{{"journals", "journal"}, {"settlements", "settlement"}, {"periodic_budget_events", "finish"}} {
		if _, bad := s.bad["pointer"][tupleKey(item.table, tupleKey(item.id))]; !bad {
			t.Fatalf("actual source counterpart %s escaped: %v", item.table, s.bad["pointer"])
		}
	}
}

func TestSourcePBEventRelationsBudgetAndZeroResults(t *testing.T) {
	if err := (*sourceClosureState)(nil).pbEventRelationRows(); !errors.Is(err, ErrInput) {
		t.Fatal("nil state accepted")
	}
	rows := pbEventSupplierFixture("new")
	for i := 0; i < 1000; i++ {
		rows.OriginalRows.PeriodicBudgetEvents = append(rows.OriginalRows.PeriodicBudgetEvents, rows.OriginalRows.PeriodicBudgetEvents[0])
	}
	for _, checks := range []int{1, 4, 8} {
		s := sourceTestRows(t, rows)
		s.w.b.steps = 255
		s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: checks}
		if err := s.pbEventRelationRows(); !errors.Is(err, ErrCanceled) {
			t.Fatalf("mid-loop cancel %d lost: %v", checks, err)
		}
	}
	s := sourceTestRows(t, rows)
	s.w.b.deadline = time.Now().Add(-time.Second)
	if err := s.pbEventRelationRows(); !errors.Is(err, ErrTimeout) {
		t.Fatal("absolute deadline renewed", err)
	}
	rows.OriginalRows.PeriodicBudgetAllocations[0].Baseline = strings.Repeat("9", int(fieldLimit)+1)
	got, err := checkSourceClosure(&replayWork{b: budget{ctx: context.Background()}}, rows)
	if !errors.Is(err, ErrResource) || !reflect.DeepEqual(got, sourceClosureResult{}) {
		t.Fatal("resource execution error exposed partial result", err)
	}
}
