package procurementprefix

import (
	"reflect"
	"testing"
	"time"
)

func TestSourceAllOriginalGClosedGroups(t *testing.T) {
	cases := []struct {
		table, prefix, kind, role string
		row                       any
	}{
		{"metering_events", "pp_", "meter_choice", "meter_origin", MeteringEventRow{}},
		{"metering_event_conflicts", "pp_", "meter_choice", "conflict", MeteringEventConflictRow{}},
		{"finalization_records", "pp_", "request_decision", "finalization", FinalizationRecordRow{}},
		{"procurement_evidence_identities", "pp_", "evidence_choice", "evidence_identity", ProcurementEvidenceIdentityRow{}},
		{"procurement_cost_proposals", "pp_", "proposal_birth", "proposal_birth", ProcurementCostProposalRow{State: "proposed"}},
		{"provider_cost_corrections", "pp_", "unmanaged_correction", "correction", ProviderCostCorrectionRow{}},
		{"reconciliation_cases", "pp_last_", "reconciliation", "reconciliation", ReconciliationCaseRow{}},
		{"settlements", "pp_", "reconciliation", "settlement", SettlementRow{}},
		{"journals", "pp_", "reconciliation", "customer_journal", JournalRow{}},
		{"journal_lines", "pp_", "reconciliation", "ledger_line", JournalLineRow{}},
		{"audit_events", "pp_", "reconciliation", "settlement", AuditEventRow{Action: "billing.released"}},
		{"projection_outbox", "pp_", "cost_outbox", "cost", ProjectionOutboxRow{Kind: "cost_recorded"}},
	}
	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			legacy := reflect.ValueOf(tc.row)
			if !sourceOriginalUnion(tc.table, legacy) {
				t.Fatal("all-NULL legacy boundary narrowed")
			}
			row := reflect.New(legacy.Type()).Elem()
			row.Set(legacy)
			fields := map[string]string{"root_id": "root", "root_step": "1", "source_kind": tc.kind, "source_id": "source", "source_role": tc.role, "created_xid": "12"}
			for field, text := range fields {
				sourceField(row, tc.prefix+field).Set(reflect.ValueOf(sourcePtr(text)))
			}
			if !sourceOriginalUnion(tc.table, row) {
				t.Fatal("complete fixed kind/role group rejected")
			}
			for field := range fields {
				copy := reflect.New(row.Type()).Elem()
				copy.Set(row)
				sourceField(copy, tc.prefix+field).Set(reflect.Zero(sourceField(copy, tc.prefix+field).Type()))
				if sourceOriginalUnion(tc.table, copy) {
					t.Fatalf("partial NULL %s accepted", field)
				}
			}
			for _, field := range []string{"root_step", "created_xid"} {
				copy := reflect.New(row.Type()).Elem()
				copy.Set(row)
				sourceField(copy, tc.prefix+field).Set(reflect.ValueOf(sourcePtr("0")))
				if sourceOriginalUnion(tc.table, copy) {
					t.Fatalf("zero %s accepted", field)
				}
			}
			sourceField(row, tc.prefix+"source_role").Set(reflect.ValueOf(sourcePtr("other")))
			if sourceOriginalUnion(tc.table, row) {
				t.Fatal("wrong original role accepted")
			}
		})
	}
}
func TestSourceOriginalBirthDispatchAndApprovalUnions(t *testing.T) {
	p := sourcePtr
	stamp := Time{Valid: true, Time: time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)}
	trackedAttempt := UpstreamAttemptRow{PPOriginRootID: p("root"), PPOriginRootStep: p("1"), PPOriginXID: p("12"), PPQuoteSelectedAt: stamp, DeliveryState: "prepared"}
	dispatch := trackedAttempt
	dispatch.DeliveryState = "sending"
	dispatch.PPDispatchSourceID = p("source")
	dispatch.PPDispatchRootID = p("dispatch-root")
	dispatch.PPDispatchRootStep = p("1")
	dispatch.PPDispatchXID = p("13")
	dispatch.PBDispatchCheckedAt = stamp
	for _, tc := range []struct {
		name, table string
		row         any
		valid       bool
	}{
		{"request legacy", "request_records", RequestRecordRow{}, true},
		{"legacy cannot have last", "request_records", RequestRecordRow{PPLastDecisionID: p("decision")}, false},
		{"request partial birth", "request_records", RequestRecordRow{PPOriginRootID: p("root")}, false},
		{"attempt tracked", "upstream_attempts", trackedAttempt, true},
		{"legacy cannot quote", "upstream_attempts", UpstreamAttemptRow{PPQuoteSelectedAt: stamp}, false},
		{"attempt unmanaged dispatch", "upstream_attempts", dispatch, true},
		{"legacy sending remains legacy", "upstream_attempts", UpstreamAttemptRow{DeliveryState: "sending"}, true},
		{"account legacy", "procurement_accounts", ProcurementAccountRow{}, true},
		{"account tracked", "procurement_accounts", ProcurementAccountRow{PPVersion: p("1"), PPCreatedXID: p("1"), PPOriginJournalID: p("journal")}, true},
		{"account partial", "procurement_accounts", ProcurementAccountRow{PPVersion: p("1")}, false},
		{"p partial", "procurement_reservations", ProcurementReservationRow{PPOriginJournalID: p("journal")}, false},
		{"p tracked", "procurement_reservations", ProcurementReservationRow{PPCreatedXID: p("1"), PPOriginJournalID: p("journal"), PPLastJournalID: p("journal")}, true},
		{"PE partial", "procurement_budget_events", ProcurementBudgetEventRow{PPRootID: p("root")}, false},
		{"command not found", "procurement_commands", ProcurementCommandRow{Kind: "account_configure", Status: "rejected", Reason: "not_found", PPSourceID: p("source"), PPRootID: p("root"), PPRootStep: p("1"), PPCreatedXID: p("12")}, true},
		{"create must have journal", "procurement_commands", ProcurementCommandRow{Kind: "account_create", Status: "applied", PPSourceID: p("source"), PPRootID: p("root"), PPRootStep: p("1"), PPCreatedXID: p("12")}, false},
		{"other command cannot borrow source", "procurement_commands", ProcurementCommandRow{Kind: "credential_bind", PPSourceID: p("source"), PPRootID: p("root"), PPRootStep: p("1"), PPCreatedXID: p("12"), PPAccountJournalID: p("journal")}, false},
		{"legacy approved proposal", "procurement_cost_proposals", ProcurementCostProposalRow{State: "approved"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceOriginalUnion(tc.table, reflect.ValueOf(tc.row)); got != tc.valid {
				t.Fatalf("valid=%v got=%v", tc.valid, got)
			}
		})
	}
	missingPermit := trackedAttempt
	missingPermit.DeliveryState = "sending"
	if sourceOriginalUnion("upstream_attempts", reflect.ValueOf(missingPermit)) {
		t.Fatal("tracked sending lacks immutable dispatch source")
	}
	partial := dispatch
	partial.PPDispatchAccountID = p("account")
	if sourceOriginalUnion("upstream_attempts", reflect.ValueOf(partial)) {
		t.Fatal("dispatch account/journal partial pair accepted")
	}
	proposal := ProcurementCostProposalRow{State: "approved", PPRootID: p("root"), PPRootStep: p("1"), PPSourceKind: p("proposal_birth"), PPSourceID: p("birth"), PPSourceRole: p("proposal_birth"), PPCreatedXID: p("12")}
	if sourceOriginalUnion("procurement_cost_proposals", reflect.ValueOf(proposal)) {
		t.Fatal("tracked approved proposal missing approval group")
	}
	proposal.PPApprovalRootID = p("approval-root")
	proposal.PPApprovalRootStep = p("1")
	proposal.PPApprovalSourceKind = p("approval_consumed")
	proposal.PPApprovalSourceID = p("approval")
	proposal.PPApprovalSourceRole = p("approval")
	proposal.PPApprovalCreatedXID = p("13")
	if !sourceOriginalUnion("procurement_cost_proposals", reflect.ValueOf(proposal)) {
		t.Fatal("complete proposal birth/approval rejected")
	}
	proposal.State = "proposed"
	if sourceOriginalUnion("procurement_cost_proposals", reflect.ValueOf(proposal)) {
		t.Fatal("proposed row with approval source accepted")
	}
}
