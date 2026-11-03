package procurementprefix

import (
	"reflect"
	"testing"
)

type sourceTemplateFixture struct {
	root     OperationRootRow
	steps    []RootStepRow
	children map[string][]sourceCanonicalRow
}

func (f *sourceTemplateFixture) add(kind, id string, row any) {
	if f.children == nil {
		f.children = make(map[string][]sourceCanonicalRow)
	}
	raw := sourceRawRow{key: tupleKey(id), value: reflect.ValueOf(row)}
	f.children[tupleKey(kind, id)] = []sourceCanonicalRow{{kind: kind, id: id, raw: raw}}
	f.steps = append(f.steps, RootStepRow{Kind: kind, SourceID: id})
}
func sourceNormalTemplateFixture() sourceTemplateFixture {
	p := sourcePtr
	f := sourceTemplateFixture{root: OperationRootRow{EntryKind: "record_finalization", SemanticResult: "normal", AttemptID: p("attempt")}}
	f.add("incoming", "i", IncomingSourceRow{ID: "i"})
	f.add("meter_choice", "c", MeterChoiceRow{ID: "c", IncomingSourceID: "i"})
	f.add("request_decision", "d", RequestDecisionRow{ID: "d", Branch: "normal", IncomingSourceID: "i", MeterChoiceID: "c", FinalizationID: p("final")})
	f.add("attempt_delivery", "delivery", AttemptDeliverySourceRow{ID: "delivery", Mode: "finalization", CauseSourceID: p("d")})
	f.add("normal_cost_decision", "n", NormalCostDecisionRow{ID: "n", FinalizationID: "final", CostMutationID: p("m"), CostRecordedOutboxID: p("original-outbox")})
	f.add("cost_mutation", "m", CostMutationRow{ID: "m", CauseKind: "normal_cost_decision", CauseSourceID: "n"})
	f.add("cost_outbox", "outbox-link", CostOutboxLinkRow{ID: "outbox-link", CauseKind: "normal_cost_decision", CauseSourceID: "n", OutboxID: "original-outbox"})
	f.add("evidence_choice", "e", EvidenceChoiceRow{ID: "e", ProcurementTransitionID: p("t"), ProcurementEventID: p("event"), AccountJournalID: p("journal")})
	f.add("procurement_transition", "t", ProcurementTransitionRow{ID: "t", CauseKind: "evidence_choice", CauseSourceID: "e", EventType: "evidence_disputed"})
	f.add("cost_reset_decision", "reset", CostResetDecisionRow{ID: "reset", CauseKind: "procurement_dispute", CauseSourceID: "t", Policy: "applied", CostMutationID: p("reset-mutation")})
	f.add("cost_mutation", "reset-mutation", CostMutationRow{ID: "reset-mutation", CauseKind: "procurement_dispute_reset", CauseSourceID: "reset"})
	raw := sourceRawRow{key: tupleKey("attempt"), value: reflect.ValueOf(AttemptOriginRow{AttemptID: "attempt", ManagementMode: "managed"})}
	f.children[tupleKey("attempt_birth", "attempt")] = []sourceCanonicalRow{{kind: "attempt_birth", id: "attempt", raw: raw}}
	return f
}
func TestSourceCanonicalNestedTemplateLanguage(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*sourceTemplateFixture)
		valid  bool
	}{
		{"normal write then managed dispute reset", func(*sourceTemplateFixture) {}, true},
		{"outbox before mutation", func(f *sourceTemplateFixture) { f.steps[5], f.steps[6] = f.steps[6], f.steps[5] }, false},
		{"missing dispute reset", func(f *sourceTemplateFixture) { f.steps = f.steps[:9] }, false},
		{"extra canonical step", func(f *sourceTemplateFixture) { f.add("incoming", "extra", IncomingSourceRow{ID: "extra"}) }, false},
		{"borrowed mutation", func(f *sourceTemplateFixture) {
			entry := f.children[tupleKey("cost_mutation", "m")][0]
			r := entry.raw.value.Interface().(CostMutationRow)
			r.CauseSourceID = "other"
			entry.raw.value = reflect.ValueOf(r)
			f.children[tupleKey("cost_mutation", "m")][0] = entry
		}, false},
		{"wrong original outbox", func(f *sourceTemplateFixture) {
			entry := f.children[tupleKey("cost_outbox", "outbox-link")][0]
			r := entry.raw.value.Interface().(CostOutboxLinkRow)
			r.OutboxID = "other"
			entry.raw.value = reflect.ValueOf(r)
			f.children[tupleKey("cost_outbox", "outbox-link")][0] = entry
		}, false},
		{"unmanaged cannot gain managed effects", func(f *sourceTemplateFixture) {
			f.children[tupleKey("attempt_birth", "attempt")][0].raw.value = reflect.ValueOf(AttemptOriginRow{AttemptID: "attempt", ManagementMode: "unmanaged"})
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := sourceNormalTemplateFixture()
			tc.mutate(&f)
			if got := sourceBasicTemplate(f.root, f.steps, f.children); got != tc.valid {
				t.Fatalf("valid=%v got=%v", tc.valid, got)
			}
		})
	}
	// The conflict branch preserves two distinct actual resets, including the
	// second unknown->unknown update. They cannot be collapsed into one step.
	conflict := sourceNormalTemplateFixture()
	conflict.root.SemanticResult = "conflict"
	decision := RequestDecisionRow{ID: "d", Branch: "conflict", IncomingSourceID: "i", MeterChoiceID: "c", ConflictSourceID: sourcePtr("conflict")}
	conflict.children[tupleKey("request_decision", "d")][0].raw.value = reflect.ValueOf(decision)
	conflict.steps = append(conflict.steps[:3:3], conflict.steps[7:]...)
	conflict.add("cost_reset_decision", "outer", CostResetDecisionRow{ID: "outer", CauseKind: "incoming_conflict", CauseSourceID: "conflict", Policy: "applied", CostMutationID: sourcePtr("outer-mutation")})
	conflict.add("cost_mutation", "outer-mutation", CostMutationRow{ID: "outer-mutation", CauseKind: "incoming_conflict_reset", CauseSourceID: "outer"})
	if !sourceBasicTemplate(conflict.root, conflict.steps, conflict.children) {
		t.Fatal("two real nested resets rejected")
	}
	conflict.steps = conflict.steps[:len(conflict.steps)-2]
	if sourceBasicTemplate(conflict.root, conflict.steps, conflict.children) {
		t.Fatal("missing outer reset accepted")
	}
}

func TestSourceCanonicalSimpleAndReconcileTemplates(t *testing.T) {
	p := sourcePtr
	fixtures := []sourceTemplateFixture{}
	single := func(entry, result, kind, id string, row any) {
		f := sourceTemplateFixture{root: OperationRootRow{EntryKind: entry, SemanticResult: result}}
		f.add(kind, id, row)
		fixtures = append(fixtures, f)
	}
	single("request_birth", "born", "request_birth", "request", RequestOriginRow{RequestID: "request"})
	single("attempt_birth", "unmanaged", "attempt_birth", "attempt", AttemptOriginRow{AttemptID: "attempt", ManagementMode: "unmanaged"})
	single("account_create", "applied", "account_command", "command", AccountCommandSourceRow{ID: "command", Kind: "account_create", CommandStatus: "applied"})
	single("account_configure", "rejected", "account_command", "command", AccountCommandSourceRow{ID: "command", Kind: "account_configure", CommandStatus: "rejected"})
	single("dispatch", "permitted", "attempt_delivery", "dispatch", AttemptDeliverySourceRow{ID: "dispatch", Mode: "dispatch_permit"})
	single("propose_cost", "proposed", "proposal_birth", "proposal", ProposalBirthRow{ID: "proposal"})
	managed := sourceTemplateFixture{root: OperationRootRow{EntryKind: "attempt_birth", SemanticResult: "managed"}}
	managed.add("attempt_birth", "attempt", AttemptOriginRow{AttemptID: "attempt", ManagementMode: "managed", ProcurementTransitionID: p("t")})
	managed.add("procurement_transition", "t", ProcurementTransitionRow{ID: "t", CauseKind: "attempt_birth", CauseSourceID: "attempt", EventType: "reserved"})
	fixtures = append(fixtures, managed)
	approval := sourceTemplateFixture{root: OperationRootRow{EntryKind: "approve_cost"}}
	approval.add("approval_consumed", "approval", ApprovalConsumptionRow{ID: "approval", ProcurementTransitionID: "t", CostMutationID: "m", OutboxID: "out"})
	approval.add("procurement_transition", "t", ProcurementTransitionRow{ID: "t", CauseKind: "approval_consumed", CauseSourceID: "approval"})
	approval.add("cost_mutation", "m", CostMutationRow{ID: "m", CauseKind: "approval_consumed", CauseSourceID: "approval"})
	approval.add("cost_outbox", "link", CostOutboxLinkRow{ID: "link", CauseKind: "approval_consumed", CauseSourceID: "approval", OutboxID: "out"})
	fixtures = append(fixtures, approval)
	correction := sourceTemplateFixture{root: OperationRootRow{EntryKind: "unmanaged_correction"}}
	correction.add("unmanaged_correction", "correction", UnmanagedCorrectionSourceRow{ID: "correction", CostMutationID: "m", OutboxID: "out"})
	correction.add("cost_mutation", "m", CostMutationRow{ID: "m", CauseKind: "unmanaged_correction", CauseSourceID: "correction"})
	correction.add("cost_outbox", "link", CostOutboxLinkRow{ID: "link", CauseKind: "unmanaged_correction", CauseSourceID: "correction", OutboxID: "out"})
	fixtures = append(fixtures, correction)
	recovery := sourceTemplateFixture{root: OperationRootRow{EntryKind: "recover_procurement"}}
	recovery.add("recovery", "recovery", RecoverySourceRow{ID: "recovery", DeliveryMutationID: "delivery", ProcurementTransitionID: "t"})
	recovery.add("attempt_delivery", "delivery", AttemptDeliverySourceRow{ID: "delivery", Mode: "recovery", CauseSourceID: p("recovery")})
	recovery.add("procurement_transition", "t", ProcurementTransitionRow{ID: "t", CauseKind: "recovery", CauseSourceID: "recovery"})
	fixtures = append(fixtures, recovery)
	for _, action := range []string{"propose", "approve", "release"} {
		f := sourceTemplateFixture{root: OperationRootRow{EntryKind: "reconcile", SemanticResult: map[string]string{"propose": "proposed", "approve": "approved", "release": "released"}[action]}}
		f.add("reconciliation", "reconcile", ReconciliationSourceRow{ID: "reconcile", Action: action})
		fixtures = append(fixtures, f)
	}
	reconcile := sourceTemplateFixture{root: OperationRootRow{EntryKind: "reconcile", SemanticResult: "approved"}}
	reconcile.add("reconciliation", "reconcile", ReconciliationSourceRow{ID: "reconcile", Action: "approve", DerivedIncomingSourceID: p("incoming"), FinalizationID: p("final")})
	reconcile.add("incoming", "incoming", IncomingSourceRow{ID: "incoming", ReconcileSourceID: p("reconcile")})
	reconcile.add("meter_choice", "choice", MeterChoiceRow{ID: "choice", IncomingSourceID: "incoming"})
	reconcile.add("request_decision", "decision", RequestDecisionRow{ID: "decision", Branch: "normal", IncomingSourceID: "incoming", MeterChoiceID: "choice", FinalizationID: p("final")})
	fixtures = append(fixtures, reconcile)
	for _, f := range fixtures {
		t.Run(f.root.EntryKind+"/"+f.root.SemanticResult, func(t *testing.T) {
			if !sourceBasicTemplate(f.root, f.steps, f.children) {
				t.Fatal("manual valid template rejected")
			}
			f.steps = append(f.steps, RootStepRow{Kind: "incoming", SourceID: "extra"})
			if sourceBasicTemplate(f.root, f.steps, f.children) {
				t.Fatal("extra canonical child accepted")
			}
		})
	}
}
