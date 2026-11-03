package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func sourceCurrentChains(t *testing.T, s *sourceClosureState, table, group, ordinal, previous string) map[string][]sourceRawRow {
	t.Helper()
	seq, err := s.sourceChain(table, "test_chain", group, ordinal, previous)
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestSourceCurrentRequestPointersAndConflictTail(t *testing.T) {
	fixture := func() Rows {
		return Rows{OriginalRows: OriginalRows{RequestRecords: []RequestRecordRow{{ID: "r", PPOriginXID: sourcePtr("1"), PPLastDecisionID: sourcePtr("conflict"), LatestFinalizationID: sourcePtr("f"), Status: "failed", BillingStatus: "released"}}}, PrefixRows: PrefixRows{
			RequestOrigins: []RequestOriginRow{{RequestID: "r"}},
			RequestDecisions: []RequestDecisionRow{
				{ID: "conflict", RequestID: "r", Ordinal: "2", PreviousDecisionID: sourcePtr("normal"), BeforeLatestID: sourcePtr("f"), AfterLatestID: sourcePtr("f"), Branch: "conflict"},
				{ID: "normal", RequestID: "r", Ordinal: "1", AfterLatestID: sourcePtr("f"), Branch: "normal"},
			},
		}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
		bad    bool
	}{
		{"conflict_is_last_decision_not_latest_finalization", func(*Rows) {}, false},
		{"stale_last_decision", func(r *Rows) { r.OriginalRows.RequestRecords[0].PPLastDecisionID = sourcePtr("normal") }, true},
		{"wrong_latest", func(r *Rows) { r.OriginalRows.RequestRecords[0].LatestFinalizationID = sourcePtr("other") }, true},
		{"missing_source", func(r *Rows) { r.PrefixRows.RequestOrigins = nil }, true},
		{"broken_chain_cannot_be_empty_history", func(r *Rows) { r.PrefixRows.RequestDecisions[0].Ordinal = "3" }, true},
		{"ordinary_status_changes_are_legal", func(r *Rows) {
			r.OriginalRows.RequestRecords[0].Status = "accepted"
			r.OriginalRows.RequestRecords[0].BillingStatus = "settled"
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := fixture()
			tc.mutate(&rows)
			s := sourceTestRows(t, rows)
			seq := sourceCurrentChains(t, s, "pp_request_decisions", "request_id", "ordinal", "previous_decision_id")
			if err := s.currentRequestPointers(seq); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["pointer"]) != 0) != tc.bad {
				t.Fatalf("pointer keys: %v", s.bad["pointer"])
			}
		})
	}
	rows := fixture()
	rows.OriginalRows.RequestRecords[0].PPLastDecisionID = sourcePtr("wrong")
	rows.OriginalRows.RequestRecords[0].LatestFinalizationID = nil
	s := sourceTestRows(t, rows)
	if err := s.currentRequestPointers(sourceCurrentChains(t, s, "pp_request_decisions", "request_id", "ordinal", "previous_decision_id")); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["pointer"]) != 1 {
		t.Fatal("multiple wrong current columns must count the physical request once")
	}
}

func sourceCurrentAttemptFixture() Rows {
	p := sourcePtr
	at := Time{Valid: true, Time: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)}
	return Rows{OriginalRows: OriginalRows{UpstreamAttempts: []UpstreamAttemptRow{{ID: "a", TenantID: "t", RequestID: "r", PPOriginXID: p("1"), PPLastCostMutationID: p("m"), PPLastDeliverySourceID: p("done"), CostStatus: "invoice", CostAmount: p("0"), DeliveryState: "completed", PPDispatchSourceID: p("permit"), PPDispatchRootID: p("send"), PPDispatchRootStep: p("1"), PPDispatchXID: p("2"), PBDispatchCheckedAt: at}}}, PrefixRows: PrefixRows{
		AttemptOrigins: []AttemptOriginRow{{AttemptID: "a", TenantID: "t", RequestID: "r", InitialCostStatus: "unknown", InitialDeliveryState: "prepared", ManagementMode: "unmanaged"}},
		AttemptNodes: []AttemptNodeRow{
			{ID: "n3", AttemptID: "a", Ordinal: "3", PreviousNodeID: p("n2"), RootID: "finish", StepNo: "1", StepKind: "attempt_delivery", SourceID: "done", CreatedXID: "3"},
			{ID: "n1", AttemptID: "a", Ordinal: "1", RootID: "send", StepNo: "1", StepKind: "attempt_delivery", SourceID: "permit", CreatedXID: "2"},
			{ID: "n2", AttemptID: "a", Ordinal: "2", PreviousNodeID: p("n1"), RootID: "cost", StepNo: "1", StepKind: "cost_mutation", SourceID: "m", CreatedXID: "8"},
		},
		AttemptDeliverySources: []AttemptDeliverySourceRow{
			{ID: "done", AttemptID: "a", TenantID: "t", RequestID: "r", RootID: "finish", StepNo: "1", StepKind: "attempt_delivery", CreatedXID: "3", Mode: "finalization", BeforeDeliveryState: "sending", AfterDeliveryState: "completed"},
			{ID: "permit", AttemptID: "a", TenantID: "t", RequestID: "r", RootID: "send", StepNo: "1", StepKind: "attempt_delivery", CreatedXID: "2", Mode: "dispatch_permit", BeforeDeliveryState: "prepared", AfterDeliveryState: "sending", DispatchXID: p("2"), PBDispatchCheckedAt: at},
		},
		CostMutations: []CostMutationRow{{ID: "m", AttemptID: "a", TenantID: "t", RequestID: "r", RootID: "cost", StepNo: "1", StepKind: "cost_mutation", CreatedXID: "8", AfterStatus: "invoice", AfterAmount: p("0")}},
	}}
}

func TestSourceCurrentAttemptPermanentPermitAndLastValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
		bad    bool
	}{
		{"out_of_order_arrays_and_nonmonotone_xid", func(*Rows) {}, false},
		{"latest_delivery_cannot_replace_permanent_permit", func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].PPDispatchSourceID = sourcePtr("done") }, true},
		{"old_permit_cannot_replace_last_delivery", func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].PPLastDeliverySourceID = sourcePtr("permit") }, true},
		{"missing_last_mutation", func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].PPLastCostMutationID = nil }, true},
		{"zero_cost_is_not_null", func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].CostAmount = nil }, true},
		{"current_cost_must_equal_last", func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].CostStatus = "contract" }, true},
		{"dispatch_xid_is_exact", func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].PPDispatchXID = sourcePtr("8") }, true},
		{"permit_own_xid_is_exact", func(r *Rows) { r.PrefixRows.AttemptDeliverySources[1].DispatchXID = sourcePtr("8") }, true},
		{"dispatch_pb_time_is_exact", func(r *Rows) {
			r.OriginalRows.UpstreamAttempts[0].PBDispatchCheckedAt.Time = r.OriginalRows.UpstreamAttempts[0].PBDispatchCheckedAt.Time.Add(time.Microsecond)
		}, true},
		{"unmanaged_cannot_borrow_account", func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].PPDispatchAccountID = sourcePtr("account") }, true},
		{"source_scope_must_match", func(r *Rows) { r.PrefixRows.CostMutations[0].RequestID = "other" }, true},
		{"source_node_must_match", func(r *Rows) { r.PrefixRows.AttemptDeliverySources[0].CreatedXID = "8" }, true},
		{"two_permits_rejected", func(r *Rows) { r.PrefixRows.AttemptDeliverySources[0].Mode = "dispatch_permit" }, true},
		{"invalid_chain_not_zero_chain", func(r *Rows) { r.PrefixRows.AttemptNodes[0].Ordinal = "4" }, true},
		{"source_missing_original", func(r *Rows) { r.OriginalRows.UpstreamAttempts = nil }, true},
		{"extra_mutation_without_node", func(r *Rows) {
			m := r.PrefixRows.CostMutations[0]
			m.ID = "orphan"
			r.PrefixRows.CostMutations = append(r.PrefixRows.CostMutations, m)
		}, true},
		{"extra_delivery_without_node", func(r *Rows) {
			d := r.PrefixRows.AttemptDeliverySources[0]
			d.ID = "orphan"
			r.PrefixRows.AttemptDeliverySources = append(r.PrefixRows.AttemptDeliverySources, d)
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourceCurrentAttemptFixture()
			tc.mutate(&rows)
			s := sourceTestRows(t, rows)
			if err := s.currentAttemptPointers(sourceCurrentChains(t, s, "pp_attempt_nodes", "attempt_id", "ordinal", "previous_node_id")); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["pointer"]) > 0) != tc.bad {
				t.Fatalf("pointer keys: %v", s.bad["pointer"])
			}
		})
	}
	// A tracked empty chain has exactly the immutable birth values and NULL tails.
	rows := sourceCurrentAttemptFixture()
	rows.PrefixRows.AttemptNodes = nil
	rows.PrefixRows.AttemptDeliverySources = nil
	rows.PrefixRows.CostMutations = nil
	rows.OriginalRows.UpstreamAttempts = []UpstreamAttemptRow{{ID: "a", PPOriginXID: sourcePtr("1"), CostStatus: "unknown", DeliveryState: "prepared"}}
	s := sourceTestRows(t, rows)
	if err := s.currentAttemptPointers(nil); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["pointer"]) != 0 {
		t.Fatal("valid birth state rejected")
	}
	rows.OriginalRows.UpstreamAttempts[0].CostAmount = sourcePtr("0")
	s = sourceTestRows(t, rows)
	if err := s.currentAttemptPointers(nil); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["pointer"]) != 1 {
		t.Fatal("fabricated zero accepted at unknown birth")
	}
}

func sourceCurrentReservationFixture() Rows {
	p := sourcePtr
	return Rows{OriginalRows: OriginalRows{ProcurementReservations: []ProcurementReservationRow{{AttemptID: "a", AccountID: "account", BindingID: "binding", InputBound: "10", OutputBound: "20", UpperAmount: "3", HeldAmount: "3", SpentAmount: "0", State: "sending", Revision: "2", PPCreatedXID: p("7"), PPOriginJournalID: p("reserve"), PPLastJournalID: p("dispatch")}}}, PrefixRows: PrefixRows{
		AttemptOrigins: []AttemptOriginRow{{AttemptID: "a", RootID: "birth", CreatedXID: "7", ManagementMode: "managed", AccountID: p("account"), BindingID: p("binding"), AccountJournalID: p("reserve"), ProcurementTransitionID: p("birth-transition"), ReservedEventID: p("reserved-event")}},
		AccountJournal: []AccountJournalRow{
			{ID: "reserve", AccountID: "account", Ordinal: "1", RootID: "birth", CreatedXID: "7", Kind: "reserve", StepKind: "procurement_transition", SourceKind: "budget_event", SourceID: "reserved-event", TransitionID: p("birth-transition"), ProcurementEventID: p("reserved-event"), AttemptID: p("a")},
			{ID: "config", AccountID: "account", Ordinal: "2", PreviousID: p("reserve"), Kind: "configure"},
			{ID: "other", AccountID: "account", Ordinal: "3", PreviousID: p("config"), Kind: "configure"},
			{ID: "dispatch", AccountID: "account", Ordinal: "4", PreviousID: p("other"), Kind: "dispatch", AttemptID: p("a"), BindingID: p("binding"), AfterReservationRevision: p("2"), AfterReservationState: p("sending"), AfterReservationHeldAmount: p("3"), AfterReservationSpentAmount: p("0"), AfterReservationAnomaly: func() *bool { v := false; return &v }(), InputBound: p("10"), OutputBound: p("20"), UpperAmount: p("3")},
			{ID: "last-config", AccountID: "account", Ordinal: "5", PreviousID: p("dispatch"), Kind: "configure"},
		},
	}}
}

func TestSourceCurrentManagedDispatchJournal(t *testing.T) {
	fixture := func() Rows {
		r := sourceCurrentAttemptFixture()
		r.PrefixRows.AttemptOrigins[0].ManagementMode = "managed"
		r.PrefixRows.AttemptOrigins[0].AccountID = sourcePtr("account")
		d := &r.PrefixRows.AttemptDeliverySources[1]
		d.DispatchAccountID, d.DispatchJournalID = sourcePtr("account"), sourcePtr("dispatch-journal")
		a := &r.OriginalRows.UpstreamAttempts[0]
		a.PPDispatchAccountID, a.PPDispatchJournalID = sourcePtr("account"), sourcePtr("dispatch-journal")
		r.PrefixRows.AccountJournal = []AccountJournalRow{{ID: "dispatch-journal", AccountID: "account", Kind: "dispatch", SourceKind: "dispatch", SourceID: "a", AttemptID: sourcePtr("a"), DeliverySourceID: sourcePtr("permit"), RootID: "send", StepNo: "1", StepKind: "attempt_delivery", CreatedXID: "2"}}
		return r
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
		bad    bool
	}{
		{"managed_exact_journal", func(*Rows) {}, false},
		{"missing_journal", func(r *Rows) { r.PrefixRows.AccountJournal = nil }, true},
		{"another_accounts_journal", func(r *Rows) { r.PrefixRows.AccountJournal[0].AccountID = "other" }, true},
		{"same_source_id_in_other_root", func(r *Rows) { r.PrefixRows.AccountJournal[0].RootID = "other" }, true},
		{"same_journal_wrong_attempt", func(r *Rows) { r.PrefixRows.AccountJournal[0].AttemptID = sourcePtr("other") }, true},
		{"same_attempt_wrong_source", func(r *Rows) { r.PrefixRows.AccountJournal[0].DeliverySourceID = sourcePtr("done") }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture()
			tc.mutate(&r)
			s := sourceTestRows(t, r)
			if err := s.currentAttemptPointers(sourceCurrentChains(t, s, "pp_attempt_nodes", "attempt_id", "ordinal", "previous_node_id")); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["pointer"]) > 0) != tc.bad {
				t.Fatalf("pointer keys: %v", s.bad["pointer"])
			}
		})
	}
}

func TestSourceCurrentReservationUsesOwnLastJournal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
		bad    bool
	}{
		{"other_configuration_interleaving", func(*Rows) {}, false},
		{"global_tail_not_p_tail", func(r *Rows) { r.OriginalRows.ProcurementReservations[0].PPLastJournalID = sourcePtr("last-config") }, true},
		{"wrong_birth_journal", func(r *Rows) { r.OriginalRows.ProcurementReservations[0].PPOriginJournalID = sourcePtr("dispatch") }, true},
		{"wrong_birth_xid", func(r *Rows) { r.OriginalRows.ProcurementReservations[0].PPCreatedXID = sourcePtr("8") }, true},
		{"wrong_current_p", func(r *Rows) { r.OriginalRows.ProcurementReservations[0].HeldAmount = "2" }, true},
		{"wrong_binding", func(r *Rows) { r.OriginalRows.ProcurementReservations[0].BindingID = "wrong" }, true},
		{"wrong_upper", func(r *Rows) { r.OriginalRows.ProcurementReservations[0].UpperAmount = "4" }, true},
		{"managed_p_cannot_claim_legacy", func(r *Rows) {
			r.OriginalRows.ProcurementReservations[0].PPCreatedXID = nil
			r.OriginalRows.ProcurementReservations[0].PPOriginJournalID = nil
			r.OriginalRows.ProcurementReservations[0].PPLastJournalID = nil
		}, true},
		{"unmanaged_cannot_have_p", func(r *Rows) { r.PrefixRows.AttemptOrigins[0].ManagementMode = "unmanaged" }, true},
		{"managed_source_missing_p", func(r *Rows) { r.OriginalRows.ProcurementReservations = nil }, true},
		{"legacy_p_cannot_hide_new_journals", func(r *Rows) {
			r.PrefixRows.AttemptOrigins = nil
			r.OriginalRows.ProcurementReservations[0].PPCreatedXID = nil
			r.OriginalRows.ProcurementReservations[0].PPOriginJournalID = nil
			r.OriginalRows.ProcurementReservations[0].PPLastJournalID = nil
		}, true},
		{"other_account_cannot_reuse_same_attempt", func(r *Rows) {
			r.PrefixRows.AccountJournal = append(r.PrefixRows.AccountJournal, AccountJournalRow{ID: "wrong-account", AccountID: "other-account", Ordinal: "1", AttemptID: sourcePtr("a")})
		}, true},
		{"birth_event_is_exact", func(r *Rows) { r.PrefixRows.AccountJournal[0].SourceID = "wrong-event" }, true},
		{"birth_before_p_is_null", func(r *Rows) { r.PrefixRows.AccountJournal[0].BeforeReservationRevision = sourcePtr("1") }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourceCurrentReservationFixture()
			tc.mutate(&rows)
			s := sourceTestRows(t, rows)
			if err := s.currentReservationPointers(sourceCurrentChains(t, s, "pp_account_journal", "account_id", "ordinal", "previous_id")); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["pointer"]) > 0) != tc.bad {
				t.Fatalf("pointer keys: %v", s.bad["pointer"])
			}
		})
	}
}

func TestSourceCurrentPointersCancellationAndLegacy(t *testing.T) {
	rows := Rows{OriginalRows: OriginalRows{RequestRecords: []RequestRecordRow{{ID: "old", LatestFinalizationID: sourcePtr("old-f")}}, UpstreamAttempts: []UpstreamAttemptRow{{ID: "old", DeliveryState: "sending", CostStatus: "invoice", CostAmount: sourcePtr("5")}}, ProcurementReservations: []ProcurementReservationRow{{AttemptID: "old"}}}}
	s := sourceTestRows(t, rows)
	if err := s.currentPointers(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["pointer"]) != 0 {
		t.Fatal("legacy boundary was retroactively interpreted")
	}
	rows = sourceCurrentAttemptFixture()
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 2}
	w.b.steps = 255
	s = sourceTestRows(t, rows)
	s.w = w
	if err := s.currentAttemptPointers(nil); !errors.Is(err, ErrCanceled) {
		t.Fatalf("index loop cancellation: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w, _, err = newReplayWork(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	got, err := checkSourceClosure(w, rows)
	if !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(got, sourceClosureResult{}) {
		t.Fatalf("execution failure must return zero result: %+v %v", got, err)
	}
}
