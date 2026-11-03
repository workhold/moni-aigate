package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func sourceContinuityTestNodes(items ...[4]string) []AttemptNodeRow {
	var nodes []AttemptNodeRow
	for i, item := range items {
		n := AttemptNodeRow{ID: "node-" + strconv.Itoa(i+1), AttemptID: "attempt", Ordinal: strconv.Itoa(i + 1), PreviousOrdinal: strconv.Itoa(i), RootID: item[2], StepNo: item[3], StepKind: item[0], SourceID: item[1], CreatedXID: "7"}
		if i > 0 {
			n.PreviousNodeID = sourcePtr(nodes[i-1].ID)
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// This fixture supplies literal p/cost/member snapshots and two explicit orders.
// Account configuration may occupy ordinals between this attempt's actions.
// No writer, price/state helper, business replay or Candidates creates expected
// values. The fixture deliberately does not claim complete root/ledger proof.
func sourceContinuityFixture() Rows {
	p := sourcePtr
	f := false
	rows := Rows{PrefixRows: PrefixRows{
		AttemptOrigins: []AttemptOriginRow{{AttemptID: "attempt", RootID: "birth", StepNo: "1", StepKind: "attempt_birth", CreatedXID: "7", TenantID: "tenant", RequestID: "request", InitialDeliveryState: "prepared", InitialCostStatus: "unknown", ManagementMode: "managed", AccountID: p("account"), BindingID: p("binding"), ProcurementTransitionID: p("reserved")}},
		AttemptNodes: sourceContinuityTestNodes(
			[4]string{"procurement_transition", "reserved", "birth", "2"},
			[4]string{"attempt_delivery", "dispatch", "send", "1"},
			[4]string{"incoming", "input", "final", "1"},
			[4]string{"meter_choice", "meter", "final", "2"},
			[4]string{"request_decision", "decision", "final", "3"},
			[4]string{"attempt_delivery", "delivered", "final", "4"},
			[4]string{"normal_cost_decision", "normal", "final", "5"},
			[4]string{"cost_mutation", "cost", "final", "6"},
			[4]string{"cost_outbox", "outbox", "final", "7"},
			[4]string{"evidence_choice", "evidence", "final", "8"},
			[4]string{"procurement_transition", "settled", "final", "9"},
			[4]string{"proposal_birth", "proposal", "propose", "1"},
			[4]string{"approval_consumed", "approval", "approve", "1"},
			[4]string{"procurement_transition", "corrected", "approve", "2"},
			[4]string{"cost_mutation", "corrected-cost", "approve", "3"}),
		IncomingSources:  []IncomingSourceRow{{ID: "input", RootID: "final", StepNo: "1", StepKind: "incoming", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: p("attempt")}},
		MeterChoices:     []MeterChoiceRow{{ID: "meter", RootID: "final", StepNo: "2", StepKind: "meter_choice", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: p("attempt")}},
		RequestDecisions: []RequestDecisionRow{{ID: "decision", RootID: "final", StepNo: "3", StepKind: "request_decision", CreatedXID: "7", TenantID: "tenant", RequestID: "request"}},
		AttemptDeliverySources: []AttemptDeliverySourceRow{
			{ID: "dispatch", RootID: "send", StepNo: "1", StepKind: "attempt_delivery", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", Mode: "dispatch_permit", BeforeDeliveryState: "prepared", AfterDeliveryState: "sending", DispatchAccountID: p("account"), DispatchJournalID: p("dispatch-journal")},
			{ID: "delivered", RootID: "final", StepNo: "4", StepKind: "attempt_delivery", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", Mode: "finalization", BeforeDeliveryState: "sending", AfterDeliveryState: "completed"},
		},
		NormalCostDecisions: []NormalCostDecisionRow{{ID: "normal", RootID: "final", StepNo: "5", StepKind: "normal_cost_decision", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", ManagementMode: "managed", BeforeCostStatus: "unknown", BeforeProcurementRevision: p("2"), BeforeProcurementState: p("sending"), BeforeProcurementHeld: p("10"), BeforeProcurementSpent: p("0"), BeforeProcurementAnomaly: &f, MemberCountBefore: p("0"), AccountID: p("account"), AccountOrdinalCut: p("5"), AccountTailCut: p("configure-2")}},
		CostMutations: []CostMutationRow{
			{ID: "cost", RootID: "final", StepNo: "6", StepKind: "cost_mutation", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", BeforeStatus: "unknown", AfterStatus: "contract", AfterAmount: p("7")},
			{ID: "corrected-cost", RootID: "approve", StepNo: "3", StepKind: "cost_mutation", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", BeforeStatus: "contract", BeforeAmount: p("7"), AfterStatus: "invoice", AfterAmount: p("8")},
		},
		CostOutboxLinks:      []CostOutboxLinkRow{{ID: "outbox", RootID: "final", StepNo: "7", StepKind: "cost_outbox", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt"}},
		EvidenceChoices:      []EvidenceChoiceRow{{ID: "evidence", RootID: "final", StepNo: "8", StepKind: "evidence_choice", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", ProcurementBeforeRevision: "2", ProcurementBeforeState: "sending", ProcurementBeforeHeld: "10", ProcurementBeforeSpent: "0", AccountID: "account", AccountOrdinalCut: "5", AccountTailCut: "configure-2", EvidenceDigest: "identity", IdentityAttemptID: "attempt", IdentityDigest: "identity", IdentityEventID: "meter-event", Disposition: "new_member", MemberCountBefore: "0", MemberCountAfter: "1", MemberTailAfter: p("member")}},
		EvidenceMembers:      []EvidenceMemberRow{{ID: "member", AttemptID: "attempt", MemberOrdinal: "1", PreviousMemberOrdinal: "0", IdentityDigest: "identity", IdentityEventID: "meter-event", EvidenceChoiceID: "evidence", RootID: "final", StepNo: "8", StepKind: "evidence_choice", SourceID: "evidence", CreatedXID: "7"}},
		ProposalBirths:       []ProposalBirthRow{{ID: "proposal", RootID: "propose", StepNo: "1", StepKind: "proposal_birth", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", ReservationRevision: "3", ReservationState: "settled", ReservationHeld: "0", ReservationSpent: "7", MemberCount: "1", MemberTail: p("member"), AccountID: "account", AccountOrdinalCut: "6", AccountTailCut: "settled-journal"}},
		ApprovalConsumptions: []ApprovalConsumptionRow{{ID: "approval", RootID: "approve", StepNo: "1", StepKind: "approval_consumed", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", ConsumedRevision: "3", ConsumedState: "settled", ConsumedHeld: "0", ConsumedSpent: "7", ConsumedMemberCount: "1", ConsumedMemberTail: p("member"), BeforeCostStatus: "contract", BeforeCostAmount: p("7"), AccountID: "account", AccountOrdinalCut: "6", AccountTailCut: "settled-journal"}},
	}}
	rows.PrefixRows.ProcurementTransitions = []ProcurementTransitionRow{
		{ID: "reserved", RootID: "birth", StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", AccountID: "account", BindingID: "binding", CauseKind: "attempt_birth", JournalID: "reserve-journal", EventID: "reserve-event", EventType: "reserved", AfterRevision: "1", AfterState: "prepared", AfterHeld: "10", AfterSpent: "0"},
		{ID: "settled", RootID: "final", StepNo: "9", StepKind: "procurement_transition", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", AccountID: "account", BindingID: "binding", CauseKind: "evidence_choice", JournalID: "settled-journal", EventID: "settled-event", BeforeRevision: p("2"), BeforeState: p("sending"), BeforeHeld: p("10"), BeforeSpent: p("0"), BeforeAnomaly: &f, AfterRevision: "3", AfterState: "settled", AfterHeld: "0", AfterSpent: "7"},
		{ID: "corrected", RootID: "approve", StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", AccountID: "account", BindingID: "binding", CauseKind: "approval_consumed", JournalID: "corrected-journal", EventID: "corrected-event", BeforeRevision: p("3"), BeforeState: p("settled"), BeforeHeld: p("0"), BeforeSpent: p("7"), BeforeAnomaly: &f, AfterRevision: "4", AfterState: "settled", AfterHeld: "0", AfterSpent: "8"},
	}
	rows.PrefixRows.AccountJournal = []AccountJournalRow{
		{ID: "create", AccountID: "account", Ordinal: "1", Kind: "create"},
		{ID: "reserve-journal", AccountID: "account", Ordinal: "2", PreviousID: p("create"), RootID: "birth", StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", Kind: "reserve", SourceKind: "budget_event", SourceID: "reserve-event", TransitionID: p("reserved"), AttemptID: p("attempt"), BindingID: p("binding"), AfterReservationRevision: p("1"), AfterReservationState: p("prepared"), AfterReservationHeldAmount: p("10"), AfterReservationSpentAmount: p("0"), AfterReservationAnomaly: &f},
		{ID: "configure-1", AccountID: "account", Ordinal: "3", PreviousID: p("reserve-journal"), Kind: "configure"},
		{ID: "dispatch-journal", AccountID: "account", Ordinal: "4", PreviousID: p("configure-1"), RootID: "send", StepNo: "1", StepKind: "attempt_delivery", CreatedXID: "7", Kind: "dispatch", SourceKind: "dispatch", SourceID: "attempt", DeliverySourceID: p("dispatch"), AttemptID: p("attempt"), BindingID: p("binding"), BeforeReservationRevision: p("1"), BeforeReservationState: p("prepared"), BeforeReservationHeldAmount: p("10"), BeforeReservationSpentAmount: p("0"), BeforeReservationAnomaly: &f, AfterReservationRevision: p("2"), AfterReservationState: p("sending"), AfterReservationHeldAmount: p("10"), AfterReservationSpentAmount: p("0"), AfterReservationAnomaly: &f},
		{ID: "configure-2", AccountID: "account", Ordinal: "5", PreviousID: p("dispatch-journal"), Kind: "configure_rejected"},
		{ID: "settled-journal", AccountID: "account", Ordinal: "6", PreviousID: p("configure-2"), RootID: "final", StepNo: "9", StepKind: "procurement_transition", CreatedXID: "7", Kind: "cost_confirmed", SourceKind: "budget_event", SourceID: "settled-event", TransitionID: p("settled"), AttemptID: p("attempt"), BindingID: p("binding"), BeforeReservationRevision: p("2"), BeforeReservationState: p("sending"), BeforeReservationHeldAmount: p("10"), BeforeReservationSpentAmount: p("0"), BeforeReservationAnomaly: &f, AfterReservationRevision: p("3"), AfterReservationState: p("settled"), AfterReservationHeldAmount: p("0"), AfterReservationSpentAmount: p("7"), AfterReservationAnomaly: &f},
		{ID: "corrected-journal", AccountID: "account", Ordinal: "7", PreviousID: p("settled-journal"), RootID: "approve", StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", Kind: "cost_corrected", SourceKind: "budget_event", SourceID: "corrected-event", TransitionID: p("corrected"), AttemptID: p("attempt"), BindingID: p("binding"), BeforeReservationRevision: p("3"), BeforeReservationState: p("settled"), BeforeReservationHeldAmount: p("0"), BeforeReservationSpentAmount: p("7"), BeforeReservationAnomaly: &f, AfterReservationRevision: p("4"), AfterReservationState: p("settled"), AfterReservationHeldAmount: p("0"), AfterReservationSpentAmount: p("8"), AfterReservationAnomaly: &f},
	}
	return rows
}

func sourceContinuityRun(t *testing.T, rows Rows) *sourceClosureState {
	t.Helper()
	s := sourceTestRows(t, rows)
	if err := s.continuityRows(); err != nil {
		t.Fatal(err)
	}
	return s
}

func sourceContinuityClean(t *testing.T, rows Rows) {
	t.Helper()
	s := sourceContinuityRun(t, rows)
	for _, bucket := range []string{"attempt_chain", "account_chain", "evidence_chain"} {
		if len(s.bad[bucket]) != 0 {
			t.Fatalf("%s defects: %v", bucket, s.bad[bucket])
		}
	}
}

func TestSourceContinuityLiteralHistoryAndDistinctOrders(t *testing.T) {
	rows := sourceContinuityFixture()
	sourceContinuityClean(t, rows)
	// Array order is neither business sequence. All roots may share one XID.
	for i, j := 0, len(rows.PrefixRows.AttemptNodes)-1; i < j; i, j = i+1, j-1 {
		rows.PrefixRows.AttemptNodes[i], rows.PrefixRows.AttemptNodes[j] = rows.PrefixRows.AttemptNodes[j], rows.PrefixRows.AttemptNodes[i]
	}
	for i, j := 0, len(rows.PrefixRows.AccountJournal)-1; i < j; i, j = i+1, j-1 {
		rows.PrefixRows.AccountJournal[i], rows.PrefixRows.AccountJournal[j] = rows.PrefixRows.AccountJournal[j], rows.PrefixRows.AccountJournal[i]
	}
	sourceContinuityClean(t, rows)
	// No-attempt Reconcile descendants are outside the attempt language.
	rows = Rows{PrefixRows: PrefixRows{IncomingSources: []IncomingSourceRow{{ID: "no-attempt", StepKind: "incoming"}}, RequestDecisions: []RequestDecisionRow{{ID: "no-attempt-decision", StepKind: "request_decision"}}}}
	sourceContinuityClean(t, rows)
}

func TestSourceContinuityReadonlySnapshotsAndNaturalKeys(t *testing.T) {
	for _, tc := range []struct {
		name, bucket, table, id string
		mutate                  func(*Rows)
	}{
		{"delivery_before", "attempt_chain", "pp_attempt_delivery_sources", "delivered", func(r *Rows) { r.PrefixRows.AttemptDeliverySources[1].BeforeDeliveryState = "prepared" }},
		{"cost_before", "attempt_chain", "pp_cost_mutations", "corrected-cost", func(r *Rows) { r.PrefixRows.CostMutations[1].BeforeAmount = sourcePtr("8") }},
		{"normal_before_cost", "attempt_chain", "pp_normal_cost_decisions", "normal", func(r *Rows) { r.PrefixRows.NormalCostDecisions[0].BeforeCostStatus = "contract" }},
		{"normal_before_p", "attempt_chain", "pp_normal_cost_decisions", "normal", func(r *Rows) { r.PrefixRows.NormalCostDecisions[0].BeforeProcurementRevision = sourcePtr("1") }},
		{"proposal_before_p", "attempt_chain", "pp_proposal_births", "proposal", func(r *Rows) { r.PrefixRows.ProposalBirths[0].ReservationHeld = "1" }},
		{"approval_before_p", "attempt_chain", "pp_approval_consumptions", "approval", func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].ConsumedState = "disputed" }},
		{"approval_before_cost", "attempt_chain", "pp_approval_consumptions", "approval", func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].BeforeCostAmount = sourcePtr("8") }},
		{"normal_member_future", "evidence_chain", "pp_normal_cost_decisions", "normal", func(r *Rows) {
			r.PrefixRows.NormalCostDecisions[0].MemberCountBefore = sourcePtr("1")
			r.PrefixRows.NormalCostDecisions[0].MemberTailBefore = sourcePtr("member")
		}},
		{"proposal_member_lost", "evidence_chain", "pp_proposal_births", "proposal", func(r *Rows) {
			r.PrefixRows.ProposalBirths[0].MemberCount = "0"
			r.PrefixRows.ProposalBirths[0].MemberTail = nil
		}},
		{"approval_member_tail", "evidence_chain", "pp_approval_consumptions", "approval", func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].ConsumedMemberTail = sourcePtr("future") }},
		{"account_cut_future_action", "attempt_chain", "pp_normal_cost_decisions", "normal", func(r *Rows) {
			r.PrefixRows.NormalCostDecisions[0].AccountOrdinalCut = sourcePtr("6")
			r.PrefixRows.NormalCostDecisions[0].AccountTailCut = sourcePtr("settled-journal")
		}},
		{"account_cut_before_dispatch", "attempt_chain", "pp_normal_cost_decisions", "normal", func(r *Rows) {
			r.PrefixRows.NormalCostDecisions[0].AccountOrdinalCut = sourcePtr("3")
			r.PrefixRows.NormalCostDecisions[0].AccountTailCut = sourcePtr("configure-1")
		}},
		{"account_cut_wrong_tail", "attempt_chain", "pp_proposal_births", "proposal", func(r *Rows) { r.PrefixRows.ProposalBirths[0].AccountTailCut = "other" }},
		{"account_cut_regresses_with_same_p", "attempt_chain", "pp_evidence_choices", "evidence", func(r *Rows) {
			r.PrefixRows.EvidenceChoices[0].AccountOrdinalCut = "4"
			r.PrefixRows.EvidenceChoices[0].AccountTailCut = "dispatch-journal"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourceContinuityFixture()
			tc.mutate(&rows)
			s := sourceContinuityRun(t, rows)
			key := tupleKey(tc.table, tupleKey(tc.id))
			if _, found := s.bad[tc.bucket][key]; !found || len(s.bad[tc.bucket]) != 1 {
				t.Fatalf("want one physical %s/%s, got %v", tc.table, tc.id, s.bad[tc.bucket])
			}
			if err := s.continuityRows(); err != nil || len(s.bad[tc.bucket]) != 1 {
				t.Fatalf("repeat counted error twice: %v %v", s.bad[tc.bucket], err)
			}
		})
	}
	rows := sourceContinuityFixture()
	d := &rows.PrefixRows.NormalCostDecisions[0]
	d.BeforeCostStatus, d.BeforeProcurementState, d.AccountTailCut = "invoice", sourcePtr("disputed"), sourcePtr("bad")
	if got := sourceContinuityRun(t, rows).bad["attempt_chain"]; len(got) != 1 {
		t.Fatalf("one source with three bad observations counted %v", got)
	}
	rows = sourceContinuityFixture()
	rows.PrefixRows.NormalCostDecisions[0].ID = "cost"
	rows.PrefixRows.AttemptNodes[6].SourceID = "cost"
	rows.PrefixRows.NormalCostDecisions[0].BeforeCostStatus = "invoice"
	rows.PrefixRows.CostMutations[0].BeforeStatus = "invoice"
	if got := sourceContinuityRun(t, rows).bad["attempt_chain"]; len(got) != 2 {
		t.Fatalf("same text ID in two physical source tables collapsed: %v", got)
	}
}

func TestSourceContinuityOtherAttemptAndSameAmountCorrection(t *testing.T) {
	rows := sourceContinuityFixture()
	f := false
	rows.PrefixRows.AttemptOrigins = append(rows.PrefixRows.AttemptOrigins, AttemptOriginRow{AttemptID: "other-attempt", RootID: "other-birth", StepNo: "1", StepKind: "attempt_birth", CreatedXID: "1", TenantID: "other-tenant", RequestID: "other-request", InitialDeliveryState: "prepared", InitialCostStatus: "unknown", ManagementMode: "managed", AccountID: sourcePtr("account"), BindingID: sourcePtr("other-binding"), ProcurementTransitionID: sourcePtr("other-reserved")})
	rows.PrefixRows.AttemptNodes = append(rows.PrefixRows.AttemptNodes, AttemptNodeRow{ID: "other-node", AttemptID: "other-attempt", Ordinal: "1", RootID: "other-birth", StepNo: "2", StepKind: "procurement_transition", SourceID: "other-reserved", CreatedXID: "1"})
	rows.PrefixRows.ProcurementTransitions = append(rows.PrefixRows.ProcurementTransitions, ProcurementTransitionRow{ID: "other-reserved", RootID: "other-birth", StepNo: "2", StepKind: "procurement_transition", CreatedXID: "1", TenantID: "other-tenant", RequestID: "other-request", AttemptID: "other-attempt", AccountID: "account", BindingID: "other-binding", CauseKind: "attempt_birth", JournalID: "configure-1", EventID: "other-reserve-event", AfterRevision: "1", AfterState: "prepared", AfterHeld: "4", AfterSpent: "0"})
	rows.PrefixRows.AccountJournal[2] = AccountJournalRow{ID: "configure-1", AccountID: "account", Ordinal: "3", PreviousID: sourcePtr("reserve-journal"), RootID: "other-birth", StepNo: "2", StepKind: "procurement_transition", CreatedXID: "1", Kind: "reserve", SourceKind: "budget_event", SourceID: "other-reserve-event", TransitionID: sourcePtr("other-reserved"), AttemptID: sourcePtr("other-attempt"), BindingID: sourcePtr("other-binding"), AfterReservationRevision: sourcePtr("1"), AfterReservationState: sourcePtr("prepared"), AfterReservationHeldAmount: sourcePtr("4"), AfterReservationSpentAmount: sourcePtr("0"), AfterReservationAnomaly: &f}
	sourceContinuityClean(t, rows)
	// A new correction action can leave the p revision and amount unchanged.
	// Its journal remains a distinct account ordinal and cross-order boundary.
	rows = sourceContinuityFixture()
	rows.PrefixRows.ProcurementTransitions[2].AfterRevision = "3"
	rows.PrefixRows.ProcurementTransitions[2].AfterSpent = "7"
	rows.PrefixRows.AccountJournal[6].AfterReservationRevision = sourcePtr("3")
	rows.PrefixRows.AccountJournal[6].AfterReservationSpentAmount = sourcePtr("7")
	rows.PrefixRows.CostMutations[1].AfterAmount = sourcePtr("7")
	sourceContinuityClean(t, rows)
}

func sourceContinuityAppendNode(rows *Rows, kind, id, root, step string) {
	nodes := rows.PrefixRows.AttemptNodes
	i := len(nodes)
	n := AttemptNodeRow{ID: "node-" + strconv.Itoa(i+1), AttemptID: "attempt", Ordinal: strconv.Itoa(i + 1), PreviousOrdinal: strconv.Itoa(i), RootID: root, StepNo: step, StepKind: kind, SourceID: id, CreatedXID: "7"}
	if i > 0 {
		n.PreviousNodeID = sourcePtr(nodes[i-1].ID)
	}
	rows.PrefixRows.AttemptNodes = append(nodes, n)
}

func sourceContinuityRecoveryFixture(permit bool) Rows {
	rows := sourceContinuityFixture()
	p := &rows.PrefixRows
	p.AttemptNodes = p.AttemptNodes[:1]
	p.ProcurementTransitions = p.ProcurementTransitions[:1]
	p.AccountJournal = p.AccountJournal[:3]
	p.IncomingSources, p.MeterChoices, p.RequestDecisions, p.NormalCostDecisions = nil, nil, nil, nil
	p.CostMutations, p.CostOutboxLinks, p.EvidenceChoices, p.EvidenceMembers = nil, nil, nil, nil
	p.ProposalBirths, p.ApprovalConsumptions = nil, nil
	p.AttemptDeliverySources = nil
	revision, state, delivery, afterRevision, afterState, afterHeld, afterDelivery := "1", "prepared", "prepared", "2", "released", "0", "failed"
	var permitID *string
	if permit {
		full := sourceContinuityFixture()
		p.AttemptNodes = full.PrefixRows.AttemptNodes[:2]
		p.AttemptDeliverySources = full.PrefixRows.AttemptDeliverySources[:1]
		p.AccountJournal = full.PrefixRows.AccountJournal[:5]
		revision, state, delivery, afterRevision, afterState, afterHeld, afterDelivery = "2", "sending", "sending", "3", "unknown", "10", "unknown"
		permitID = sourcePtr("dispatch")
	}
	f := false
	p.RecoverySources = []RecoverySourceRow{{ID: "recovery", RootID: "recover", StepNo: "1", StepKind: "recovery", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", ProcurementBeforeRevision: revision, ProcurementBeforeState: state, ProcurementBeforeHeld: "10", ProcurementBeforeSpent: "0", DeliveryBeforeState: delivery, PermitSourceID: permitID, AccountJournalID: "recovery-journal", ProcurementTransitionID: "recovery-transition", ProcurementEventID: "recovery-event"}}
	sourceContinuityAppendNode(&rows, "recovery", "recovery", "recover", "1")
	p.AttemptDeliverySources = append(p.AttemptDeliverySources, AttemptDeliverySourceRow{ID: "recovery-delivery", RootID: "recover", StepNo: "2", StepKind: "attempt_delivery", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", Mode: "recovery", BeforeDeliveryState: delivery, AfterDeliveryState: afterDelivery})
	sourceContinuityAppendNode(&rows, "attempt_delivery", "recovery-delivery", "recover", "2")
	p.ProcurementTransitions = append(p.ProcurementTransitions, ProcurementTransitionRow{ID: "recovery-transition", RootID: "recover", StepNo: "3", StepKind: "procurement_transition", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", AccountID: "account", BindingID: "binding", CauseKind: "recovery", CauseSourceID: "recovery", JournalID: "recovery-journal", EventID: "recovery-event", BeforeRevision: sourcePtr(revision), BeforeState: sourcePtr(state), BeforeHeld: sourcePtr("10"), BeforeSpent: sourcePtr("0"), BeforeAnomaly: &f, AfterRevision: afterRevision, AfterState: afterState, AfterHeld: afterHeld, AfterSpent: "0"})
	sourceContinuityAppendNode(&rows, "procurement_transition", "recovery-transition", "recover", "3")
	previous := p.AccountJournal[len(p.AccountJournal)-1].ID
	p.AccountJournal = append(p.AccountJournal, AccountJournalRow{ID: "recovery-journal", AccountID: "account", Ordinal: strconv.Itoa(len(p.AccountJournal) + 1), PreviousID: sourcePtr(previous), RootID: "recover", StepNo: "3", StepKind: "procurement_transition", CreatedXID: "7", Kind: "recovered_unknown", SourceKind: "budget_event", SourceID: "recovery-event", TransitionID: sourcePtr("recovery-transition"), AttemptID: sourcePtr("attempt"), BindingID: sourcePtr("binding"), BeforeReservationRevision: sourcePtr(revision), BeforeReservationState: sourcePtr(state), BeforeReservationHeldAmount: sourcePtr("10"), BeforeReservationSpentAmount: sourcePtr("0"), BeforeReservationAnomaly: &f, AfterReservationRevision: sourcePtr(afterRevision), AfterReservationState: sourcePtr(afterState), AfterReservationHeldAmount: sourcePtr(afterHeld), AfterReservationSpentAmount: sourcePtr("0"), AfterReservationAnomaly: &f})
	return rows
}

func TestSourceContinuityRecoveryDeliveryAndImplicitAccountCut(t *testing.T) {
	for _, permit := range []bool{false, true} {
		sourceContinuityClean(t, sourceContinuityRecoveryFixture(permit))
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
	}{
		{"wrong_delivery_before", func(r *Rows) { r.PrefixRows.RecoverySources[0].DeliveryBeforeState = "completed" }},
		{"lost_permanent_permit", func(r *Rows) { r.PrefixRows.RecoverySources[0].PermitSourceID = nil }},
		{"wrong_p_before", func(r *Rows) { r.PrefixRows.RecoverySources[0].ProcurementBeforeRevision = "1" }},
		{"missing_effect_journal", func(r *Rows) { r.PrefixRows.RecoverySources[0].AccountJournalID = "missing" }},
		{"configuration_cannot_be_borrowed_as_recovery_cut", func(r *Rows) { r.PrefixRows.RecoverySources[0].AccountJournalID = "configure-2" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourceContinuityRecoveryFixture(true)
			tc.mutate(&rows)
			got := sourceContinuityRun(t, rows).bad["attempt_chain"]
			if _, found := got[tupleKey("pp_recovery_sources", tupleKey("recovery"))]; !found || len(got) != 1 {
				t.Fatalf("expected one recovery source: %v", got)
			}
		})
	}
}

func TestSourceContinuityDisputeResetUsesTransitionBefore(t *testing.T) {
	rows := sourceContinuityFixture()
	f, yes := false, true
	rows.PrefixRows.ProcurementTransitions = append(rows.PrefixRows.ProcurementTransitions, ProcurementTransitionRow{ID: "disputed", RootID: "dispute", StepNo: "1", StepKind: "procurement_transition", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", AccountID: "account", BindingID: "binding", CauseKind: "evidence_choice", JournalID: "dispute-journal", EventID: "dispute-event", BeforeRevision: sourcePtr("4"), BeforeState: sourcePtr("settled"), BeforeHeld: sourcePtr("0"), BeforeSpent: sourcePtr("8"), BeforeAnomaly: &f, AfterRevision: "5", AfterState: "disputed", AfterHeld: "2", AfterSpent: "8", AfterAnomaly: true})
	sourceContinuityAppendNode(&rows, "procurement_transition", "disputed", "dispute", "1")
	rows.PrefixRows.AccountJournal = append(rows.PrefixRows.AccountJournal, AccountJournalRow{ID: "dispute-journal", AccountID: "account", Ordinal: "8", PreviousID: sourcePtr("corrected-journal"), RootID: "dispute", StepNo: "1", StepKind: "procurement_transition", CreatedXID: "7", Kind: "evidence_disputed", SourceKind: "budget_event", SourceID: "dispute-event", TransitionID: sourcePtr("disputed"), AttemptID: sourcePtr("attempt"), BindingID: sourcePtr("binding"), BeforeReservationRevision: sourcePtr("4"), BeforeReservationState: sourcePtr("settled"), BeforeReservationHeldAmount: sourcePtr("0"), BeforeReservationSpentAmount: sourcePtr("8"), BeforeReservationAnomaly: &f, AfterReservationRevision: sourcePtr("5"), AfterReservationState: sourcePtr("disputed"), AfterReservationHeldAmount: sourcePtr("2"), AfterReservationSpentAmount: sourcePtr("8"), AfterReservationAnomaly: &yes})
	rows.PrefixRows.CostResetDecisions = []CostResetDecisionRow{
		{ID: "dispute-reset", RootID: "dispute", StepNo: "2", StepKind: "cost_reset_decision", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", CauseKind: "procurement_dispute", CauseSourceID: "disputed", ProcurementStateBefore: sourcePtr("settled")},
		{ID: "incoming-reset", RootID: "dispute", StepNo: "3", StepKind: "cost_reset_decision", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", CauseKind: "incoming_conflict", ProcurementStateBefore: sourcePtr("disputed")},
	}
	sourceContinuityAppendNode(&rows, "cost_reset_decision", "dispute-reset", "dispute", "2")
	sourceContinuityAppendNode(&rows, "cost_reset_decision", "incoming-reset", "dispute", "3")
	sourceContinuityClean(t, rows)
	rows.PrefixRows.CostResetDecisions[0].ProcurementStateBefore = sourcePtr("disputed")
	if got := sourceContinuityRun(t, rows).bad["attempt_chain"]; len(got) != 1 {
		t.Fatalf("dispute reset substituted current state for actual cause-before: %v", got)
	}
	rows.PrefixRows.CostResetDecisions[0].ProcurementStateBefore = sourcePtr("settled")
	rows.PrefixRows.CostResetDecisions[0].CauseSourceID = "corrected"
	if got := sourceContinuityRun(t, rows).bad["attempt_chain"]; len(got) != 1 {
		t.Fatalf("other-root transition used as reset predecessor: %v", got)
	}
}

func sourceContinuityAppendExisting(rows *Rows, disposition string) {
	e := rows.PrefixRows.EvidenceChoices[0]
	e.ID, e.RootID, e.StepNo = "again", "again-root", "1"
	e.ProcurementBeforeRevision, e.ProcurementBeforeState, e.ProcurementBeforeHeld, e.ProcurementBeforeSpent = "4", "settled", "0", "8"
	e.AccountOrdinalCut, e.AccountTailCut = "7", "corrected-journal"
	e.MemberCountBefore, e.MemberCountAfter, e.MemberTailBefore, e.MemberTailAfter = "1", "1", sourcePtr("member"), sourcePtr("member")
	e.Disposition = disposition
	rows.PrefixRows.EvidenceChoices = append(rows.PrefixRows.EvidenceChoices, e)
	nodes := rows.PrefixRows.AttemptNodes
	rows.PrefixRows.AttemptNodes = append(nodes, AttemptNodeRow{ID: "node-16", AttemptID: "attempt", Ordinal: "16", PreviousOrdinal: "15", PreviousNodeID: sourcePtr("node-15"), RootID: "again-root", StepNo: "1", StepKind: "evidence_choice", SourceID: "again", CreatedXID: "7"})
}

func TestSourceContinuityMemberBirthAndReuse(t *testing.T) {
	rows := sourceContinuityFixture()
	sourceContinuityAppendExisting(&rows, "existing_member")
	sourceContinuityClean(t, rows)
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
	}{
		{"existing_before_birth", func(r *Rows) { r.PrefixRows.EvidenceChoices[0].Disposition = "existing_member" }},
		{"repeat_identity_cannot_be_new", func(r *Rows) { r.PrefixRows.EvidenceChoices[1].Disposition = "new_member" }},
		{"existing_wrong_original_event", func(r *Rows) { r.PrefixRows.EvidenceChoices[1].IdentityEventID = "new-meter-event" }},
		{"existing_unknown_identity", func(r *Rows) { r.PrefixRows.EvidenceChoices[1].IdentityDigest = "unseen" }},
		{"existing_changed_count", func(r *Rows) { r.PrefixRows.EvidenceChoices[1].MemberCountAfter = "2" }},
		{"existing_cannot_add_companion", func(r *Rows) {
			m := r.PrefixRows.EvidenceMembers[0]
			m.ID, m.MemberOrdinal, m.PreviousMemberOrdinal, m.PreviousMemberID, m.EvidenceChoiceID, m.SourceID, m.RootID, m.StepNo = "extra", "2", "1", sourcePtr("member"), "again", "again", "again-root", "1"
			r.PrefixRows.EvidenceMembers = append(r.PrefixRows.EvidenceMembers, m)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := sourceContinuityFixture()
			sourceContinuityAppendExisting(&r, "existing_member")
			tc.mutate(&r)
			if got := sourceContinuityRun(t, r).bad["evidence_chain"]; len(got) == 0 {
				t.Fatal("bad complete member prefix accepted")
			}
		})
	}
	rows = sourceContinuityFixture()
	m := &rows.PrefixRows.EvidenceMembers[0]
	m.IdentityEventID = "wrong-event"
	s := sourceContinuityRun(t, rows)
	if len(s.bad["evidence_chain"]) != 2 {
		t.Fatalf("choice and companion are distinct physical objects: %v", s.bad["evidence_chain"])
	}
}

func TestSourceContinuityJournalBridgeAndBrokenOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
	}{
		{"journal_before_p", func(r *Rows) { r.PrefixRows.AccountJournal[5].BeforeReservationHeldAmount = sourcePtr("9") }},
		{"journal_after_differs_transition", func(r *Rows) { r.PrefixRows.AccountJournal[6].AfterReservationSpentAmount = sourcePtr("9") }},
		{"dispatch_journal_cannot_be_omitted", func(r *Rows) { r.PrefixRows.AttemptDeliverySources[0].DispatchJournalID = nil }},
		{"transition_cannot_borrow_other_journal", func(r *Rows) { r.PrefixRows.ProcurementTransitions[1].JournalID = "corrected-journal" }},
		{"journal_node_order_disagrees", func(r *Rows) {
			r.PrefixRows.AttemptNodes[10].Ordinal, r.PrefixRows.AttemptNodes[13].Ordinal = "14", "11"
			r.PrefixRows.AttemptNodes[10].PreviousNodeID, r.PrefixRows.AttemptNodes[13].PreviousNodeID = sourcePtr("node-13"), sourcePtr("node-10")
			r.PrefixRows.AttemptNodes[11].PreviousNodeID, r.PrefixRows.AttemptNodes[14].PreviousNodeID = sourcePtr("node-14"), sourcePtr("node-11")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourceContinuityFixture()
			tc.mutate(&rows)
			s := sourceContinuityRun(t, rows)
			if len(s.bad["attempt_chain"])+len(s.bad["account_chain"]) == 0 {
				t.Fatal("bad p/node/account bridge accepted")
			}
		})
	}
	rows := sourceContinuityFixture()
	rows.PrefixRows.AttemptNodes[0].Ordinal = "99"
	s := sourceContinuityRun(t, rows)
	if len(s.bad["attempt_chain"]) == 0 {
		t.Fatal("invalid nonempty attempt order became an empty prefix")
	}
	rows = sourceContinuityFixture()
	rows.PrefixRows.EvidenceMembers[0].MemberOrdinal = "2"
	s = sourceContinuityRun(t, rows)
	if len(s.bad["evidence_chain"]) == 0 {
		t.Fatal("invalid nonempty member order became no members")
	}
	rows = sourceContinuityFixture()
	rows.PrefixRows.AccountJournal[0].Ordinal = "9"
	s = sourceContinuityRun(t, rows)
	if len(s.bad["account_chain"]) == 0 {
		t.Fatal("invalid nonempty account order became no p actions")
	}
}

func TestSourceContinuityWrongAccountDoesNotSeedActionOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		rows := sourceContinuityFixture()
		p := &rows.PrefixRows
		transition := p.ProcurementTransitions[0]
		transition.ID, transition.AccountID, transition.JournalID = "wrong-reserved", "wrong-account", "wrong-journal"
		transition.RootID, transition.StepNo, transition.EventID = "wrong-root", "1", "wrong-event"
		p.ProcurementTransitions = append(p.ProcurementTransitions, transition)
		sourceContinuityAppendNode(&rows, "procurement_transition", "wrong-reserved", "wrong-root", "1")
		journal := p.AccountJournal[1]
		journal.ID, journal.AccountID, journal.RootID, journal.StepNo = "wrong-journal", "wrong-account", "wrong-root", "1"
		journal.Ordinal, journal.PreviousID, journal.SourceID, journal.TransitionID = "1", nil, "wrong-event", sourcePtr("wrong-reserved")
		p.AccountJournal = append(p.AccountJournal, journal)
		if reverse {
			for i, j := 0, len(p.AccountJournal)-1; i < j; i, j = i+1, j-1 {
				p.AccountJournal[i], p.AccountJournal[j] = p.AccountJournal[j], p.AccountJournal[i]
			}
		}
		// Both array orders must produce the same one bad physical journal,
		// regardless of which account Go's map iteration happens to visit first.
		for trial := 0; trial < 64; trial++ {
			got := sourceContinuityRun(t, rows).bad["account_chain"]
			_, found := got[tupleKey("pp_account_journal", tupleKey("wrong-journal"))]
			if !found || len(got) != 1 {
				t.Fatalf("reverse=%t trial=%d: wrong account contaminated valid action order: %v", reverse, trial, got)
			}
		}
	}
}

func TestSourceContinuityUnmanagedCorrectionAndNoInventedP(t *testing.T) {
	p := sourcePtr
	rows := Rows{PrefixRows: PrefixRows{
		AttemptOrigins:             []AttemptOriginRow{{AttemptID: "attempt", TenantID: "tenant", RequestID: "request", InitialDeliveryState: "prepared", InitialCostStatus: "unknown", ManagementMode: "unmanaged"}},
		AttemptNodes:               sourceContinuityTestNodes([4]string{"unmanaged_correction", "correction", "correct", "1"}, [4]string{"cost_mutation", "mutation", "correct", "2"}, [4]string{"cost_reset_decision", "reset", "conflict", "1"}),
		UnmanagedCorrectionSources: []UnmanagedCorrectionSourceRow{{ID: "correction", RootID: "correct", StepNo: "1", StepKind: "unmanaged_correction", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", BeforeCostStatus: "unknown"}},
		CostMutations:              []CostMutationRow{{ID: "mutation", RootID: "correct", StepNo: "2", StepKind: "cost_mutation", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", BeforeStatus: "unknown", AfterStatus: "invoice", AfterAmount: p("0")}},
		CostResetDecisions:         []CostResetDecisionRow{{ID: "reset", RootID: "conflict", StepNo: "1", StepKind: "cost_reset_decision", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", CauseKind: "incoming_conflict"}},
	}}
	sourceContinuityClean(t, rows)
	rows.PrefixRows.UnmanagedCorrectionSources[0].BeforeCostAmount = p("0")
	rows.PrefixRows.CostResetDecisions[0].ProcurementStateBefore = p("released")
	if got := sourceContinuityRun(t, rows).bad["attempt_chain"]; len(got) != 2 {
		t.Fatalf("unknown NULL cost and nonexistent p conflated: %v", got)
	}
}

func TestSourceContinuityMissingAndDuplicateSources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
	}{
		{"missing_mutation", func(r *Rows) { r.PrefixRows.CostMutations = r.PrefixRows.CostMutations[:1] }},
		{"duplicate_mutation", func(r *Rows) {
			r.PrefixRows.CostMutations = append(r.PrefixRows.CostMutations, r.PrefixRows.CostMutations[1])
		}},
		{"source_without_node", func(r *Rows) { r.PrefixRows.AttemptNodes = r.PrefixRows.AttemptNodes[:14] }},
		{"wrong_nullable_attempt", func(r *Rows) { r.PrefixRows.IncomingSources[0].AttemptID = sourcePtr("other") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourceContinuityFixture()
			tc.mutate(&rows)
			if got := sourceContinuityRun(t, rows).bad["attempt_chain"]; len(got) == 0 {
				t.Fatal("source/node binding was arbitrarily selected or ignored")
			}
		})
	}
}

func TestSourceContinuityCancellationAndTimeout(t *testing.T) {
	if err := (*sourceClosureState)(nil).continuityRows(); !errors.Is(err, ErrInput) {
		t.Fatal("nil state accepted")
	}
	rows := sourceContinuityFixture()
	for i := 0; i < 500; i++ {
		rows.PrefixRows.EvidenceChoices = append(rows.PrefixRows.EvidenceChoices, rows.PrefixRows.EvidenceChoices[0])
	}
	for _, checks := range []int{1, 2, 4, 8} {
		s := sourceTestRows(t, rows)
		s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: checks}
		s.w.b.steps = 255
		if err := s.continuityRows(); !errors.Is(err, ErrCanceled) {
			t.Fatalf("check %d did not cancel: %v", checks, err)
		}
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	s := &sourceClosureState{w: &replayWork{b: budget{ctx: ctx}}, bad: map[string]keySet{}, tables: map[string][]sourceRawRow{}}
	if err := s.continuityRows(); !errors.Is(err, ErrTimeout) {
		t.Fatalf("deadline ignored: %v", err)
	}
	// The source entry remains private and returns no partial proof on abort.
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 1}
	got, err := checkSourceClosure(w, rows)
	if !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(got, sourceClosureResult{}) {
		t.Fatalf("abort exposed result: %+v %v", got, err)
	}
}
