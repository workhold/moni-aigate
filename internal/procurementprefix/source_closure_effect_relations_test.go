package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sourceEffectRun(t *testing.T, rows Rows) *sourceClosureState {
	t.Helper()
	s := sourceTestRows(t, rows)
	if err := s.effectRelations(); err != nil {
		t.Fatal(err)
	}
	return s
}

func sourceEffectClean(t *testing.T, rows Rows) {
	t.Helper()
	s := sourceEffectRun(t, rows)
	if len(s.bad["pointer"]) != 0 || len(s.bad["companion"]) != 0 {
		t.Fatalf("valid declared effects rejected: %+v", s.bad)
	}
}

func TestSourceEffectCustomerLiteralSetsAndHistoricalValues(t *testing.T) {
	for _, action := range []string{"release", "approve"} {
		t.Run(action, func(t *testing.T) {
			v := ffFixture(action) // Hand-written rows; no production writer is used.
			sourceEffectClean(t, v)
			for i := range v.OriginalRows.LedgerAccounts {
				v.OriginalRows.LedgerAccounts[i].Balance = "123456789"
			}
			v.OriginalRows.Settlements[0].RefundedAmount = v.OriginalRows.Settlements[0].ChargedAmount
			if action == "approve" {
				v.PrefixRows.CustomerFinishLedgerEffects[2].BeforeBalance = "-8"
				v.PrefixRows.CustomerFinishLedgerEffects[2].AfterBalance = "-2"
			}
			sourceEffectClean(t, v)
		})
	}
	v := ffFixture("release")
	ffRewrite(&v, "released_reconciliation", "released", "0", "0", "0", "7", "0", "100", "0")
	v.PrefixRows.CustomerFinishOutcomes[0].ReservationAmount = "0"
	v.PrefixRows.CustomerFinishLedgerEffects[0].SignedDelta = "0"
	v.PrefixRows.CustomerFinishLedgerEffects[0].AfterBalance = "100"
	v.OriginalRows.JournalLines[0].SignedDelta = "0"
	sourceEffectClean(t, v)
}

func TestSourceEffectCustomerExactOriginalAndReverseSets(t *testing.T) {
	mutations := []struct {
		name string
		fn   func(*Rows)
	}{
		{"wrong_settlement", func(v *Rows) { v.OriginalRows.Settlements[0].ChargedAmount = "5" }},
		{"wrong_finalization", func(v *Rows) { v.OriginalRows.Settlements[0].FinalizationID = ffPtr("other") }},
		{"wrong_journal_operation", func(v *Rows) { v.OriginalRows.Journals[0].OperationID = "other" }},
		{"wrong_journal_xid", func(v *Rows) { v.OriginalRows.Journals[0].CreatedTxid = "124" }},
		{"extra_settlement_same_request", func(v *Rows) {
			a := v.OriginalRows.Settlements[0]
			a.ID = "extra"
			a.PPSourceID = nil
			v.OriginalRows.Settlements = append(v.OriginalRows.Settlements, a)
		}},
		{"extra_journal_same_operation", func(v *Rows) {
			a := v.OriginalRows.Journals[0]
			a.ID = "extra"
			a.PPSourceID = nil
			v.OriginalRows.Journals = append(v.OriginalRows.Journals, a)
		}},
		{"wrong_line_amount", func(v *Rows) { v.OriginalRows.JournalLines[0].SignedDelta = "-9" }},
		{"wrong_line_journal", func(v *Rows) { v.OriginalRows.JournalLines[0].JournalID = "other" }},
		{"cross_tenant", func(v *Rows) { v.OriginalRows.LedgerAccounts[0].TenantID = ffPtr("other") }},
		{"missing_effect", func(v *Rows) { v.PrefixRows.CustomerFinishLedgerEffects = v.PrefixRows.CustomerFinishLedgerEffects[:2] }},
		{"missing_original", func(v *Rows) { v.OriginalRows.JournalLines = v.OriginalRows.JournalLines[:2] }},
		{"extra_line", func(v *Rows) {
			v.OriginalRows.JournalLines = append(v.OriginalRows.JournalLines, JournalLineRow{ID: "extra", JournalID: "journal", AccountID: "available", SignedDelta: "0"})
		}},
		{"reused_line", func(v *Rows) {
			v.PrefixRows.CustomerFinishLedgerEffects[1].JournalLineID = v.PrefixRows.CustomerFinishLedgerEffects[0].JournalLineID
		}},
		{"duplicate_business_account", func(v *Rows) {
			a := v.OriginalRows.LedgerAccounts[0]
			a.ID = "other"
			v.OriginalRows.LedgerAccounts = append(v.OriginalRows.LedgerAccounts, a)
		}},
		{"duplicate_physical_line", func(v *Rows) {
			v.OriginalRows.JournalLines = append(v.OriginalRows.JournalLines, v.OriginalRows.JournalLines[0])
		}},
		{"duplicate_kind", func(v *Rows) {
			a := v.PrefixRows.CustomerFinishLedgerEffects[0]
			a.AccountID = "another"
			v.PrefixRows.CustomerFinishLedgerEffects = append(v.PrefixRows.CustomerFinishLedgerEffects, a)
		}},
		{"pending_has_effects", func(v *Rows) { v.PrefixRows.CustomerFinishOutcomes[0].ResultKind = "pending" }},
		{"missing_request", func(v *Rows) { v.OriginalRows.RequestRecords = nil }},
		{"missing_every_original", func(v *Rows) { v.OriginalRows = OriginalRows{} }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			v := ffFixture("approve")
			tc.fn(&v)
			s := sourceEffectRun(t, v)
			if len(s.bad["pointer"])+len(s.bad["companion"]) == 0 {
				t.Fatal("forged effect relation accepted")
			}
		})
	}
	// Identical IDs in different physical tables must remain separate objects.
	v := ffFixture("approve")
	v.OriginalRows.Journals[0].ID = "outcome"
	v.OriginalRows.Journals[0].CreatedTxid = "124"
	v.PrefixRows.CustomerFinishOutcomes[0].CustomerJournalID = ffPtr("outcome")
	s := sourceEffectRun(t, v)
	if _, ok := s.bad["pointer"][tupleKey("journals", tupleKey("outcome"))]; !ok {
		t.Fatal("bad journal object missing")
	}
	if _, ok := s.bad["pointer"][tupleKey("pp_customer_finish_outcomes", tupleKey("outcome"))]; !ok {
		t.Fatal("same-ID source incorrectly merged with journal")
	}
}

func TestSourceEffectCustomerDualPBAndClosedLegacy(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		v := ffFixture("approve")
		ffPB(&v, legacy)
		sourceEffectClean(t, v)
		v.OriginalRows.PeriodicBudgetControls[0].HeldAmount = "999"
		v.OriginalRows.PeriodicBudgetAllocations[0].SpentAmount = "999"
		sourceEffectClean(t, v) // These current values are not historical snapshots.
	}
	for _, mutate := range []func(*Rows){
		func(v *Rows) { v.PrefixRows.CustomerFinishPBEffects = v.PrefixRows.CustomerFinishPBEffects[:1] },
		func(v *Rows) { v.PrefixRows.CustomerFinishPBEffects[0].EventID = "key-finish" },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetEvents[1].AfterSpent = "5" },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetEvents[0].CreatedXID = "123" },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetAllocations[0].Baseline = "1" },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetAllocations[0].Origin = "legacy_catchup" },
		func(v *Rows) { v.OriginalRows.RequestRecords[0].PBTenantControl = ffPtr("other") },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetWindows[0].EndAt = ffTime(5) },
		func(v *Rows) {
			a := v.OriginalRows.PeriodicBudgetEvents[1]
			a.ID = "extra"
			v.OriginalRows.PeriodicBudgetEvents = append(v.OriginalRows.PeriodicBudgetEvents, a)
		},
		func(v *Rows) {
			v.OriginalRows.PeriodicBudgetAllocations = append(v.OriginalRows.PeriodicBudgetAllocations, v.OriginalRows.PeriodicBudgetAllocations[0])
		},
	} {
		v := ffFixture("approve")
		ffPB(&v, false)
		mutate(&v)
		s := sourceEffectRun(t, v)
		if len(s.bad["pointer"])+len(s.bad["companion"]) == 0 {
			t.Fatal("forged customer PB relation accepted")
		}
	}
	v := ffFixture("approve")
	v.PrefixRows.CustomerFinishOutcomes[0].CustomerTenantControlID = ffPtr("later-control")
	v.OriginalRows.PeriodicBudgetControls = []PeriodicBudgetControlRow{{ID: "later-control", Scope: "tenant", TenantID: ffPtr("tenant")}}
	sourceEffectClean(t, v) // Real old closed source, no marker/allocation/effect.
	v.OriginalRows.PeriodicBudgetAllocations = []PeriodicBudgetAllocationRow{{ID: "allocation", ControlID: "later-control", SubjectType: "customer_request", SubjectID: "request"}}
	if len(sourceEffectRun(t, v).bad["companion"]) != 1 {
		t.Fatal("actual allocation without finish effect accepted")
	}
}

func sourceEffectSupplierFixture() Rows {
	p := ffPtr
	t := ProcurementTransitionRow{ID: "transition", RootID: "root", StepNo: "1", StepKind: "procurement_transition", CreatedXID: "7", EventID: "pe", EventKey: "reserve:attempt", EventType: "reserved", AttemptID: "attempt", AccountID: "account", BindingID: "binding", UpperAmount: "10", AfterState: "prepared", AfterHeld: "10", AfterSpent: "0", HeldDelta: "10", SpentDelta: "0"}
	return Rows{PrefixRows: PrefixRows{ProcurementTransitions: []ProcurementTransitionRow{t}}, OriginalRows: OriginalRows{
		ProcurementBudgetEvents:   []ProcurementBudgetEventRow{{ID: "pe", EventKey: "reserve:attempt", Type: "reserved", AttemptID: "attempt", AccountID: "account", HeldDelta: "10", SpentDelta: "0", PBCreatedXID: p("7")}},
		ProcurementReservations:   []ProcurementReservationRow{{AttemptID: "attempt", AccountID: "account", BindingID: "binding", PBCreatedXID: p("7"), PBControl: p("control"), PBAdmittedAt: ffTime(10)}},
		PeriodicBudgetControls:    []PeriodicBudgetControlRow{{ID: "control", Scope: "procurement", AccountID: p("account"), CreatedXID: "7"}},
		PeriodicBudgetAllocations: []PeriodicBudgetAllocationRow{{ID: "allocation", ControlID: "control", SubjectType: "supplier_attempt", SubjectID: "attempt", Origin: "new", OriginSourceID: "pe", WindowID: p("window"), AdmittedAt: ffTime(10), UpperAmount: "10", Baseline: "0", CreatedXID: "7"}},
		PeriodicBudgetWindows:     []PeriodicBudgetWindowRow{{ID: "window", ControlID: "control", StartAt: ffTime(0), EndAt: ffTime(20)}},
		PeriodicBudgetEvents:      []PeriodicBudgetEventRow{{ID: "periodic", ControlID: "control", AllocationID: "allocation", SourceKind: "procurement_event", SourceID: "pe", WindowBeforeRevision: p("0"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "10", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: "10", SpentDelta: "0", CreatedXID: "7"}},
	}}
}

func TestSourceEffectSupplierNewImportCatchupAndAbsence(t *testing.T) {
	sourceEffectClean(t, sourceEffectSupplierFixture())
	for _, origin := range []string{"legacy_import", "legacy_catchup"} {
		v := sourceEffectSupplierFixture()
		v.OriginalRows.ProcurementReservations[0].PBControl = nil
		a := &v.OriginalRows.PeriodicBudgetAllocations[0]
		a.Origin, a.WindowID, a.AdmittedAt, a.Baseline = origin, nil, Time{}, "5"
		if origin == "legacy_import" {
			a.OriginSourceID = "attempt"
		}
		target := &v.PrefixRows.ProcurementTransitions[0]
		target.BeforeRevision, target.BeforeHeld, target.BeforeSpent = ffPtr("2"), ffPtr("0"), ffPtr("5")
		target.AfterState, target.AfterSpent, target.AfterHeld = "settled", "8", "0"
		target.HeldDelta, target.SpentDelta, target.EventKey, target.EventType = "0", "3", "correction:attempt:invoice", "cost_corrected"
		pe := &v.OriginalRows.ProcurementBudgetEvents[0]
		pe.Type, pe.EventKey, pe.HeldDelta, pe.SpentDelta = target.EventType, target.EventKey, "0", "3"
		event := &v.OriginalRows.PeriodicBudgetEvents[0]
		event.WindowBeforeRevision = nil
		event.BeforeSpent, event.AfterHeld, event.AfterSpent, event.AfterSourceSpent, event.HeldDelta, event.SpentDelta, event.AfterState = "0", "0", "3", "8", "0", "3", "settled"
		sourceEffectClean(t, v)
		event.AfterSpent = "8"
		if len(sourceEffectRun(t, v).bad["pointer"]) == 0 {
			t.Fatal("legacy baseline omitted")
		}
	}
	v := sourceEffectSupplierFixture()
	v.OriginalRows.ProcurementReservations[0].PBControl = nil
	v.OriginalRows.PeriodicBudgetEvents = nil
	// Today's allocation may have been configured after this old transition.
	sourceEffectClean(t, v)
	v.OriginalRows.ProcurementReservations[0].PBControl = ffPtr("control")
	if len(sourceEffectRun(t, v).bad["companion"]) != 1 {
		t.Fatal("admission marker did not require actual PB event")
	}
}

func TestSourceEffectSupplierRejectsWrongOriginalOrPB(t *testing.T) {
	for _, mutate := range []func(*Rows){
		func(v *Rows) {
			extra := v.OriginalRows.ProcurementBudgetEvents[0]
			extra.ID, extra.PBCreatedXID = "extra-pe", nil
			v.OriginalRows.ProcurementBudgetEvents = append(v.OriginalRows.ProcurementBudgetEvents, extra)
		},
		func(v *Rows) { v.OriginalRows.ProcurementBudgetEvents[0].EvidenceRef = "other" },
		func(v *Rows) { v.OriginalRows.ProcurementBudgetEvents[0].PBCreatedXID = nil },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetEvents[0].HeldDelta = "9" },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetEvents[0].AfterSourceSpent = "1" },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetEvents[0].CreatedXID = "8" },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetControls[0].AccountID = ffPtr("other") },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetAllocations[0].SubjectID = "other" },
		func(v *Rows) { v.OriginalRows.PeriodicBudgetWindows[0].StartAt = ffTime(11) },
		func(v *Rows) {
			a := v.OriginalRows.PeriodicBudgetEvents[0]
			a.ID = "extra"
			v.OriginalRows.PeriodicBudgetEvents = append(v.OriginalRows.PeriodicBudgetEvents, a)
		},
		func(v *Rows) { v.OriginalRows.PeriodicBudgetEvents[0].AllocationID = "absent" },
	} {
		v := sourceEffectSupplierFixture()
		mutate(&v)
		s := sourceEffectRun(t, v)
		if len(s.bad["pointer"])+len(s.bad["companion"]) == 0 {
			t.Fatal("forged supplier effect accepted")
		}
	}
	v := sourceEffectSupplierFixture()
	v.PrefixRows.ProcurementTransitions[0].UpperAmount = "0"
	v.PrefixRows.ProcurementTransitions[0].AfterHeld, v.PrefixRows.ProcurementTransitions[0].HeldDelta = "0", "0"
	v.OriginalRows.ProcurementBudgetEvents[0].HeldDelta = "0"
	v.OriginalRows.PeriodicBudgetAllocations[0].UpperAmount = "0"
	v.OriginalRows.PeriodicBudgetEvents[0].AfterHeld, v.OriginalRows.PeriodicBudgetEvents[0].HeldDelta = "0", "0"
	sourceEffectClean(t, v) // Zero-valued reserve still has its actual event.
}

func TestSourceEffectDuplicateOwnerCannotConsumeDifferentOriginals(t *testing.T) {
	v := sourceEffectSupplierFixture()
	v.OriginalRows.ProcurementReservations[0].PBControl = nil
	v.OriginalRows.PeriodicBudgetEvents = nil
	other := v.PrefixRows.ProcurementTransitions[0]
	other.ID, other.RootID = "other-transition", "other-root"
	other.EventID, other.EventKey, other.AttemptID = "other-pe", "reserve:other-attempt", "other-attempt"
	v.PrefixRows.ProcurementTransitions = append(v.PrefixRows.ProcurementTransitions, other)
	pe := v.OriginalRows.ProcurementBudgetEvents[0]
	pe.ID, pe.EventKey, pe.AttemptID = other.EventID, other.EventKey, other.AttemptID
	v.OriginalRows.ProcurementBudgetEvents = append(v.OriginalRows.ProcurementBudgetEvents, pe)
	p := v.OriginalRows.ProcurementReservations[0]
	p.AttemptID = other.AttemptID
	v.OriginalRows.ProcurementReservations = append(v.OriginalRows.ProcurementReservations, p)
	sourceEffectClean(t, v)
	v.PrefixRows.ProcurementTransitions[1].ID = v.PrefixRows.ProcurementTransitions[0].ID
	s := sourceEffectRun(t, v)
	if _, marked := s.bad["pointer"][tupleKey("pp_procurement_transitions", tupleKey("transition"))]; !marked || len(s.bad["pointer"]) != 1 {
		t.Fatalf("duplicate source consumed as two unique physical owners: %v", s.bad)
	}
}

func TestSourceEffectDuplicatePBEventIdentityCannotBeConsumed(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		v := ffFixture("approve")
		ffPB(&v, legacy)
		sourceEffectClean(t, v)
		id := v.OriginalRows.PeriodicBudgetEvents[0].ID
		v.OriginalRows.PeriodicBudgetEvents[2].ID = id
		s := sourceEffectRun(t, v)
		if _, marked := s.bad["pointer"][tupleKey("periodic_budget_events", tupleKey(id))]; !marked {
			t.Fatalf("duplicate admission event served tenant/key scopes: legacy=%v bad=%v", legacy, s.bad)
		}
	}
	v := sourceEffectSupplierFixture()
	other := v.PrefixRows.ProcurementTransitions[0]
	other.ID, other.RootID = "other-transition", "other-root"
	other.EventID, other.EventKey, other.AttemptID = "other-pe", "reserve:other-attempt", "other-attempt"
	v.PrefixRows.ProcurementTransitions = append(v.PrefixRows.ProcurementTransitions, other)
	pe := v.OriginalRows.ProcurementBudgetEvents[0]
	pe.ID, pe.EventKey, pe.AttemptID = other.EventID, other.EventKey, other.AttemptID
	v.OriginalRows.ProcurementBudgetEvents = append(v.OriginalRows.ProcurementBudgetEvents, pe)
	p := v.OriginalRows.ProcurementReservations[0]
	p.AttemptID = other.AttemptID
	v.OriginalRows.ProcurementReservations = append(v.OriginalRows.ProcurementReservations, p)
	a := v.OriginalRows.PeriodicBudgetAllocations[0]
	a.ID, a.SubjectID, a.OriginSourceID = "other-allocation", other.AttemptID, other.EventID
	v.OriginalRows.PeriodicBudgetAllocations = append(v.OriginalRows.PeriodicBudgetAllocations, a)
	e := v.OriginalRows.PeriodicBudgetEvents[0]
	e.ID, e.AllocationID, e.SourceID = "other-event", a.ID, other.EventID
	v.OriginalRows.PeriodicBudgetEvents = append(v.OriginalRows.PeriodicBudgetEvents, e)
	sourceEffectClean(t, v)
	v.OriginalRows.PeriodicBudgetEvents[1].ID = v.OriginalRows.PeriodicBudgetEvents[0].ID
	s := sourceEffectRun(t, v)
	if _, marked := s.bad["pointer"][tupleKey("periodic_budget_events", tupleKey("periodic"))]; !marked {
		t.Fatalf("duplicate supplier event original not recorded: %v", s.bad)
	}
}

func TestSourceEffectCancellationAndEntryZeroResult(t *testing.T) {
	if !errors.Is((*sourceClosureState)(nil).effectRelations(), ErrInput) {
		t.Fatal("nil entry")
	}
	s := sourceTestRows(t, ffFixture("approve"))
	s.w.b.deadline = time.Now().Add(-time.Second)
	if !errors.Is(s.effectRelations(), ErrTimeout) {
		t.Fatal("deadline restarted")
	}
	v := ffFixture("approve")
	for len(v.OriginalRows.JournalLines) < 700 {
		v.OriginalRows.JournalLines = append(v.OriginalRows.JournalLines, v.OriginalRows.JournalLines[0])
	}
	s = sourceTestRows(t, v)
	s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 3}
	if !errors.Is(s.effectRelations(), ErrCanceled) {
		t.Fatal("index loop ignored cancellation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := sourceUsageWork(t)
	w.b.ctx = ctx
	got, err := checkSourceClosure(w, v)
	if !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(got, sourceClosureResult{}) {
		t.Fatal("partial result exposed")
	}
	v.OriginalRows.JournalLines[0].SignedDelta = strings.Repeat("1", int(fieldLimit)+1)
	w = sourceUsageWork(t)
	got, err = checkSourceClosure(w, v)
	if !errors.Is(err, ErrResource) || !reflect.DeepEqual(got, sourceClosureResult{}) {
		t.Fatal("resource error exposed partial result")
	}
}
