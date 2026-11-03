package procurementprefix

import (
	"context"
	"reflect"
	"testing"
)

func sourcePtr(v string) *string { return &v }

type sourceGeneratedFixture struct {
	table    string
	row      any
	expected map[string]*string
}

// These are manually stated row values, independent of the generated-rule
// registry. Nil entries are as material as active discriminator values.
func sourceGeneratedFixtures() []sourceGeneratedFixture {
	p := sourcePtr
	fixed := func(kind string) map[string]*string { return map[string]*string{"step_kind": p(kind)} }
	roots := map[string]*string{"d_request_birth": nil, "d_attempt_birth": nil, "d_account_command": nil, "d_attempt_delivery": nil, "d_incoming": p("source"), "d_meter_choice": nil, "d_request_decision": nil, "d_normal_cost_decision": nil, "d_cost_mutation": nil, "d_cost_reset_decision": nil, "d_evidence_choice": nil, "d_procurement_transition": nil, "d_proposal_birth": nil, "d_approval_consumed": nil, "d_unmanaged_correction": nil, "d_recovery": nil, "d_cost_outbox": nil, "d_reconciliation": nil}
	return []sourceGeneratedFixture{
		{"pp_root_steps", RootStepRow{Kind: "incoming", SourceID: "source", DIncoming: p("source")}, roots},
		{"pp_request_origins", RequestOriginRow{StepKind: "request_birth"}, fixed("request_birth")},
		{"pp_attempt_origins", AttemptOriginRow{StepKind: "attempt_birth"}, fixed("attempt_birth")},
		{"pp_account_origins", AccountOriginRow{StepKind: "account_command"}, fixed("account_command")},
		{"pp_account_command_sources", AccountCommandSourceRow{StepKind: "account_command"}, fixed("account_command")},
		{"pp_attempt_delivery_sources", AttemptDeliverySourceRow{StepKind: "attempt_delivery", Mode: "finalization", CauseSourceID: p("cause"), DFinalization: p("cause")}, map[string]*string{"step_kind": p("attempt_delivery"), "d_finalization": p("cause"), "d_recovery": nil}},
		{"pp_incoming_sources", IncomingSourceRow{StepKind: "incoming"}, fixed("incoming")},
		{"pp_meter_choices", MeterChoiceRow{StepKind: "meter_choice"}, fixed("meter_choice")},
		{"pp_meter_origins", MeterOriginRow{StepKind: "meter_choice"}, fixed("meter_choice")},
		{"pp_conflict_sources", ConflictSourceRow{StepKind: "meter_choice"}, fixed("meter_choice")},
		{"pp_request_decisions", RequestDecisionRow{StepKind: "request_decision", Ordinal: "2", PreviousOrdinal: "1"}, map[string]*string{"step_kind": p("request_decision"), "previous_ordinal": p("1")}},
		{"pp_normal_cost_decisions", NormalCostDecisionRow{StepKind: "normal_cost_decision"}, fixed("normal_cost_decision")},
		{"pp_cost_mutations", CostMutationRow{StepKind: "cost_mutation", CauseKind: "normal_cost_decision", CauseSourceID: "cause", DNormalCostDecision: p("cause")}, map[string]*string{"step_kind": p("cost_mutation"), "d_normal_cost_decision": p("cause"), "d_procurement_dispute_reset": nil, "d_incoming_conflict_reset": nil, "d_approval_consumed": nil, "d_unmanaged_correction": nil}},
		{"pp_cost_reset_decisions", CostResetDecisionRow{StepKind: "cost_reset_decision", CauseKind: "incoming_conflict", CauseSourceID: "cause", Policy: "applied", DIncomingConflict: p("cause"), MutationCauseKind: p("incoming_conflict_reset")}, map[string]*string{"step_kind": p("cost_reset_decision"), "d_procurement_dispute": nil, "d_incoming_conflict": p("cause"), "mutation_cause_kind": p("incoming_conflict_reset")}},
		{"pp_evidence_choices", EvidenceChoiceRow{StepKind: "evidence_choice"}, fixed("evidence_choice")},
		{"pp_evidence_members", EvidenceMemberRow{StepKind: "evidence_choice", MemberOrdinal: "2", PreviousMemberOrdinal: "1"}, map[string]*string{"step_kind": p("evidence_choice"), "previous_member_ordinal": p("1")}},
		{"pp_procurement_transitions", ProcurementTransitionRow{StepKind: "procurement_transition", CauseKind: "evidence_choice", CauseSourceID: "cause", DEvidenceChoice: p("cause")}, map[string]*string{"step_kind": p("procurement_transition"), "d_attempt_birth": nil, "d_evidence_choice": p("cause"), "d_approval_consumed": nil, "d_recovery": nil}},
		{"pp_proposal_births", ProposalBirthRow{StepKind: "proposal_birth"}, fixed("proposal_birth")},
		{"pp_approval_consumptions", ApprovalConsumptionRow{StepKind: "approval_consumed"}, fixed("approval_consumed")},
		{"pp_unmanaged_correction_sources", UnmanagedCorrectionSourceRow{StepKind: "unmanaged_correction"}, fixed("unmanaged_correction")},
		{"pp_recovery_sources", RecoverySourceRow{StepKind: "recovery"}, fixed("recovery")},
		{"pp_cost_outbox_links", CostOutboxLinkRow{StepKind: "cost_outbox", CauseKind: "approval_consumed", CauseSourceID: "cause", DApprovalConsumed: p("cause")}, map[string]*string{"step_kind": p("cost_outbox"), "d_normal_cost_decision": nil, "d_approval_consumed": p("cause"), "d_unmanaged_correction": nil}},
		{"pp_reconciliation_sources", ReconciliationSourceRow{StepKind: "reconciliation"}, fixed("reconciliation")},
		{"pp_customer_finish_outcomes", CustomerFinishOutcomeRow{StepKind: "reconciliation", ResultKind: "new_settlement", SettlementID: p("settlement"), DNewSettlement: p("settlement")}, map[string]*string{"step_kind": p("reconciliation"), "d_new_settlement": p("settlement")}},
		{"pp_customer_finish_ledger_effects", CustomerFinishLedgerEffectRow{StepKind: "reconciliation"}, fixed("reconciliation")},
		{"pp_customer_finish_pb_effects", CustomerFinishPBEffectRow{StepKind: "reconciliation"}, fixed("reconciliation")},
		{"pp_attempt_nodes", AttemptNodeRow{Ordinal: "2", PreviousOrdinal: "1"}, map[string]*string{"previous_ordinal": p("1")}},
		{"pp_account_journal", AccountJournalRow{Ordinal: "2", PreviousOrdinal: "1", SourceKind: "budget_event", SourceID: "event", ProcurementEventID: p("event")}, map[string]*string{"previous_ordinal": p("1"), "command_operation_id": nil, "procurement_event_id": p("event"), "dispatch_attempt_id": nil}},
	}
}

func sourceNewTestState(t *testing.T, rows PrefixRows) *sourceClosureState {
	t.Helper()
	w, _, err := newReplayWork(context.Background(), Rows{PrefixRows: rows})
	if err != nil {
		t.Fatal(err)
	}
	s := &sourceClosureState{w: w, bad: make(map[string]keySet), tables: make(map[string][]sourceRawRow)}
	if err := s.physicalRows(reflect.ValueOf(rows)); err != nil {
		t.Fatal(err)
	}
	return s
}
func sourceGeneratedTestState(t *testing.T, table string, row reflect.Value) *sourceClosureState {
	t.Helper()
	s := sourceNewTestState(t, PrefixRows{})
	s.tables[table] = []sourceRawRow{{key: tupleKey("fixture"), value: row}}
	return s
}

func TestSourceGeneratedAll68IndependentValues(t *testing.T) {
	total := 0
	for _, fixture := range sourceGeneratedFixtures() {
		fixture := fixture
		t.Run(fixture.table, func(t *testing.T) {
			baseline := reflect.ValueOf(fixture.row)
			for column, expected := range fixture.expected {
				actual := sourceField(baseline, column)
				if !actual.IsValid() {
					t.Fatalf("missing %s", column)
				}
				if actual.Kind() == reflect.Pointer {
					if !sourceSameOptional(sourceOptional(baseline, column), expected) {
						t.Fatalf("fixture expected %s", column)
					}
				} else if expected == nil || actual.String() != *expected {
					t.Fatalf("fixture expected %s", column)
				}
			}
			s := sourceGeneratedTestState(t, fixture.table, baseline)
			if err := s.generated(PrefixRows{}); err != nil {
				t.Fatal(err)
			}
			if len(s.bad["generated"]) != 0 {
				t.Fatal("independently stated baseline rejected")
			}
			for column := range fixture.expected {
				t.Run(column, func(t *testing.T) {
					copy := reflect.New(baseline.Type()).Elem()
					copy.Set(baseline)
					field := sourceField(copy, column)
					if field.Kind() == reflect.Pointer {
						if field.IsNil() {
							field.Set(reflect.ValueOf(sourcePtr("wrong")))
						} else {
							field.Set(reflect.Zero(field.Type()))
						}
					} else {
						field.SetString("wrong")
					}
					s := sourceGeneratedTestState(t, fixture.table, copy)
					if err := s.generated(PrefixRows{}); err != nil {
						t.Fatal(err)
					}
					if len(s.bad["generated"]) != 1 {
						t.Fatalf("mutated generated %s not detected", column)
					}
				})
			}
		})
		total += len(fixture.expected)
	}
	if total != 68 {
		t.Fatalf("expected all 68 generated columns, got %d", total)
	}
}

func TestSourceGeneratedDiscriminatorsEveryActiveBranch(t *testing.T) {
	groups := []struct {
		table, field string
		row          any
		kinds        []string
	}{
		{"pp_root_steps", "kind", RootStepRow{SourceID: "cause"}, []string{"request_birth", "attempt_birth", "account_command", "attempt_delivery", "incoming", "meter_choice", "request_decision", "normal_cost_decision", "cost_mutation", "cost_reset_decision", "evidence_choice", "procurement_transition", "proposal_birth", "approval_consumed", "unmanaged_correction", "recovery", "cost_outbox", "reconciliation"}},
		{"pp_cost_mutations", "cause_kind", CostMutationRow{StepKind: "cost_mutation", CauseSourceID: "cause"}, []string{"normal_cost_decision", "procurement_dispute_reset", "incoming_conflict_reset", "approval_consumed", "unmanaged_correction"}},
		{"pp_procurement_transitions", "cause_kind", ProcurementTransitionRow{StepKind: "procurement_transition", CauseSourceID: "cause"}, []string{"attempt_birth", "evidence_choice", "approval_consumed", "recovery"}},
		{"pp_cost_outbox_links", "cause_kind", CostOutboxLinkRow{StepKind: "cost_outbox", CauseSourceID: "cause"}, []string{"normal_cost_decision", "approval_consumed", "unmanaged_correction"}},
	}
	for _, group := range groups {
		for _, kind := range group.kinds {
			t.Run(group.table+"/"+kind, func(t *testing.T) {
				r := reflect.New(reflect.TypeOf(group.row)).Elem()
				r.Set(reflect.ValueOf(group.row))
				sourceField(r, group.field).SetString(kind)
				sourceField(r, "d_"+kind).Set(reflect.ValueOf(sourcePtr("cause")))
				s := sourceGeneratedTestState(t, group.table, r)
				if err := s.generated(PrefixRows{}); err != nil {
					t.Fatal(err)
				}
				if len(s.bad["generated"]) != 0 {
					t.Fatal("active discriminator rejected")
				}
				sourceField(r, "d_"+kind).Set(reflect.ValueOf(sourcePtr("")))
				s = sourceGeneratedTestState(t, group.table, r)
				if err := s.generated(PrefixRows{}); err != nil {
					t.Fatal(err)
				}
				if len(s.bad["generated"]) != 1 {
					t.Fatal("empty and cause were conflated")
				}
			})
		}
	}
}
