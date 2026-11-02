package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	procurementApprovalProposalID  = "11111111-1111-1111-1111-111111111111"
	procurementApprovalOperationID = "22222222-2222-2222-2222-222222222222"
)

func validProcurementApprovalRows() ProcurementCostApprovalRows {
	attempt := validProcurementAttemptRows().Attempts[0]
	attempt.CostAmount, attempt.CostStatus = procurementAttemptPtr("25"), "invoice"
	approvedAt := time.Unix(400, 0)
	return ProcurementCostApprovalRows{
		Users:        []ProcurementApprovalUserRow{{ID: "requester-a"}, {ID: "approver-a"}},
		Attempts:     []ProcurementAttemptRow{attempt},
		Reservations: validProcurementAttemptRows().Reservations,
		Events: []ProcurementApprovalEventRow{{ID: "event-a", EventKey: "correction:" + procurementApprovalOperationID,
			AccountID: "account-a", AttemptID: "attempt-a", Type: "cost_corrected", EvidenceRef: "invoice:a"}},
		Corrections: []ProcurementCostCorrectionRow{{ID: "correction-a", TenantID: "tenant-a", AttemptID: "attempt-a",
			OperationID: "procurement:" + procurementApprovalOperationID, Amount: "25", Basis: "invoice", EvidenceRef: "invoice:a", CreatedAt: time.Unix(350, 0)}},
		Proposals: []ProcurementCostProposalRow{{ID: procurementApprovalProposalID, TenantID: "tenant-a", AttemptID: "attempt-a", Amount: "25", Basis: "invoice",
			EvidenceRef: "invoice:a", Reason: "supplier invoice", Requester: "requester-a", ExpectedRevision: "5", ExpectedEvidenceCount: "3", State: "approved",
			Approver: procurementAttemptPtr("approver-a"), ApprovalOperationID: procurementAttemptPtr(procurementApprovalOperationID), CreatedAt: time.Unix(300, 0), ApprovedAt: &approvedAt}},
	}
}

func checkProcurementApproval(t *testing.T, rows ProcurementCostApprovalRows) ProcurementCostApprovalCheck {
	t.Helper()
	got, err := CheckProcurementCostApprovalGraph(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balanced != (got.Mismatches == (ProcurementCostApprovalMismatches{})) {
		t.Fatalf("balanced did not reflect every mismatch: %+v", got)
	}
	return got
}

func TestCheckProcurementCostApprovalGraphBalancedEmptyProposedApproved(t *testing.T) {
	empty := checkProcurementApproval(t, ProcurementCostApprovalRows{})
	if !empty.Balanced || empty.Scope != "procurement_cost_approval_graph_v1" || empty.Scanned != (ProcurementCostApprovalCounts{}) {
		t.Fatalf("empty projection did not produce its scoped summary: %+v", empty)
	}
	rows := validProcurementApprovalRows()
	got := checkProcurementApproval(t, rows)
	if !got.Balanced || got.Scanned != (ProcurementCostApprovalCounts{Users: 2, Attempts: 1, Reservations: 1, Events: 1, Corrections: 1, Proposals: 1}) {
		t.Fatalf("complete approval graph rejected: %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil || strings.Contains(string(raw), "requester-a") || strings.Contains(string(raw), "invoice:a") || strings.Contains(string(raw), procurementApprovalProposalID) {
		t.Fatalf("summary disclosed source text: %s %v", raw, err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if object["scope"] != "procurement_cost_approval_graph_v1" || object["scanned"].(map[string]any)["proposals"] != float64(1) {
		t.Fatalf("stable JSON shape changed: %s", raw)
	}

	rows.Proposals[0].State = "proposed"
	rows.Proposals[0].Approver, rows.Proposals[0].ApprovalOperationID, rows.Proposals[0].ApprovedAt = nil, nil, nil
	rows.Events, rows.Corrections = nil, nil
	if got := checkProcurementApproval(t, rows); !got.Balanced {
		t.Fatalf("legal proposed row required effects or current snapshot equality: %+v", got)
	}
}

func TestCheckProcurementCostApprovalGraphPreservesHistoricalCorrectionsAndLegacy(t *testing.T) {
	rows := validProcurementApprovalRows()
	second := rows.Proposals[0]
	second.ID, second.Amount, second.Basis, second.EvidenceRef = "33333333-3333-3333-3333-333333333333", "0", "contract", "contract:b"
	second.ApprovalOperationID = procurementAttemptPtr("44444444-4444-4444-4444-444444444444")
	rows.Proposals = append(rows.Proposals, second)
	rows.Corrections = append(rows.Corrections, ProcurementCostCorrectionRow{ID: "correction-b", TenantID: second.TenantID, AttemptID: second.AttemptID,
		OperationID: "procurement:" + *second.ApprovalOperationID, Amount: second.Amount, Basis: second.Basis, EvidenceRef: second.EvidenceRef})
	rows.Events = append(rows.Events, ProcurementApprovalEventRow{ID: "event-b", EventKey: "correction:" + *second.ApprovalOperationID,
		AccountID: "account-a", AttemptID: second.AttemptID, Type: "cost_corrected", EvidenceRef: second.EvidenceRef})
	// Later evidence/corrections may change the current cost and reservation;
	// these historical proposals remain valid and need not be fresh today.
	rows.Attempts[0].CostAmount, rows.Attempts[0].CostStatus = procurementAttemptPtr("1234567"), "contract"
	rows.Reservations[0].SpentAmount, rows.Reservations[0].State = "999", "disputed"
	rows.Proposals[0].ExpectedRevision, rows.Proposals[0].ExpectedEvidenceCount = "9223372036854775807", "0"
	// clock_timestamp has no monotonicity guarantee in the write contract.
	backwards := time.Unix(1, 0)
	rows.Proposals[0].ApprovedAt, rows.Proposals[0].CreatedAt, rows.Corrections[0].CreatedAt = &backwards, time.Unix(1000, 0), time.Unix(2000, 0)
	if got := checkProcurementApproval(t, rows); !got.Balanced {
		t.Fatalf("history was compared to current cost/freshness/process-clock order: %+v", got)
	}

	legacy := rows.Attempts[0]
	legacy.ID = "legacy-attempt"
	rows.Attempts = append(rows.Attempts, legacy)
	rows.Corrections = append(rows.Corrections, ProcurementCostCorrectionRow{ID: "legacy-correction", TenantID: legacy.TenantID, AttemptID: legacy.ID,
		OperationID: "procurement:legacy-operation", Amount: "9223372036854775807", Basis: "contract", EvidenceRef: strings.Repeat("x", 300)})
	rows.Events = append(rows.Events, ProcurementApprovalEventRow{ID: "historical-other-event", EventKey: "other-key", AccountID: "account-a", AttemptID: "attempt-a", Type: "future_budget_fact", EvidenceRef: ""})
	if got := checkProcurementApproval(t, rows); !got.Balanced {
		t.Fatalf("legacy prefix, long legacy evidence or uninterpreted event was misclassified: %+v", got)
	}
}

func TestCheckProcurementCostApprovalGraphReportsBothRelationDirections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProcurementCostApprovalRows)
		want   ProcurementCostApprovalMismatches
	}{
		{"missing correction", func(r *ProcurementCostApprovalRows) { r.Corrections = nil }, ProcurementCostApprovalMismatches{ApprovedCorrectionRows: 1}},
		{"missing event", func(r *ProcurementCostApprovalRows) { r.Events = nil }, ProcurementCostApprovalMismatches{ApprovedEventRows: 1}},
		{"missing proposal", func(r *ProcurementCostApprovalRows) { r.Proposals = nil }, ProcurementCostApprovalMismatches{ManagedCorrectionApprovalRows: 1, CorrectionEventApprovalRows: 1}},
		{"wrong correction evidence", func(r *ProcurementCostApprovalRows) { r.Corrections[0].EvidenceRef = "other" }, ProcurementCostApprovalMismatches{ApprovedCorrectionRows: 1, ManagedCorrectionApprovalRows: 1}},
		{"wrong correction amount", func(r *ProcurementCostApprovalRows) { r.Corrections[0].Amount = "26" }, ProcurementCostApprovalMismatches{ApprovedCorrectionRows: 1, ManagedCorrectionApprovalRows: 1}},
		{"wrong correction basis", func(r *ProcurementCostApprovalRows) { r.Corrections[0].Basis = "contract" }, ProcurementCostApprovalMismatches{ApprovedCorrectionRows: 1, ManagedCorrectionApprovalRows: 1}},
		{"arbitrary managed operation", func(r *ProcurementCostApprovalRows) { r.Corrections[0].OperationID = "ordinary-operation" }, ProcurementCostApprovalMismatches{ApprovedCorrectionRows: 1, ManagedCorrectionApprovalRows: 1}},
		{"wrong correction tenant", func(r *ProcurementCostApprovalRows) { r.Corrections[0].TenantID = "other-tenant" }, ProcurementCostApprovalMismatches{CorrectionIdentityRows: 1, ApprovedCorrectionRows: 1, ManagedCorrectionApprovalRows: 1}},
		{"orphan correction", func(r *ProcurementCostApprovalRows) { r.Corrections[0].AttemptID = "missing" }, ProcurementCostApprovalMismatches{CorrectionIdentityRows: 1, ApprovedCorrectionRows: 1}},
		{"wrong event account", func(r *ProcurementCostApprovalRows) { r.Events[0].AccountID = "other-account" }, ProcurementCostApprovalMismatches{ApprovedEventRows: 1, CorrectionEventApprovalRows: 1}},
		{"wrong event attempt", func(r *ProcurementCostApprovalRows) { r.Events[0].AttemptID = "other-attempt" }, ProcurementCostApprovalMismatches{ApprovedEventRows: 1, CorrectionEventApprovalRows: 1}},
		{"wrong event evidence", func(r *ProcurementCostApprovalRows) { r.Events[0].EvidenceRef = "" }, ProcurementCostApprovalMismatches{ApprovedEventRows: 1, CorrectionEventApprovalRows: 1}},
		{"short event key", func(r *ProcurementCostApprovalRows) { r.Events[0].EventKey = "x" }, ProcurementCostApprovalMismatches{ApprovedEventRows: 1, CorrectionEventApprovalRows: 1}},
		{"wrong event type", func(r *ProcurementCostApprovalRows) { r.Events[0].Type = "other" }, ProcurementCostApprovalMismatches{ApprovedEventRows: 1}},
		{"wrong proposal tenant", func(r *ProcurementCostApprovalRows) { r.Proposals[0].TenantID = "other-tenant" }, ProcurementCostApprovalMismatches{ProposalIdentityRows: 1, ApprovedCorrectionRows: 1, ManagedCorrectionApprovalRows: 1}},
		{"no reservation", func(r *ProcurementCostApprovalRows) { r.Reservations = nil }, ProcurementCostApprovalMismatches{ProposalIdentityRows: 1, ApprovedEventRows: 1, CorrectionEventApprovalRows: 1}},
		{"no requester", func(r *ProcurementCostApprovalRows) { r.Users = r.Users[1:] }, ProcurementCostApprovalMismatches{ProposalIdentityRows: 1}},
		{"no approver", func(r *ProcurementCostApprovalRows) { r.Users = r.Users[:1] }, ProcurementCostApprovalMismatches{ProposalIdentityRows: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := validProcurementApprovalRows()
			tc.mutate(&rows)
			got := checkProcurementApproval(t, rows)
			if got.Mismatches != tc.want {
				t.Fatalf("relation mismatch counting changed: got %+v want %+v", got.Mismatches, tc.want)
			}
		})
	}
}

func TestCheckProcurementCostApprovalGraphChecksApprovalShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProcurementCostProposalRow)
	}{
		{"self approval", func(p *ProcurementCostProposalRow) { p.Approver = procurementAttemptPtr(p.Requester) }},
		{"operation equals proposal", func(p *ProcurementCostProposalRow) { p.ApprovalOperationID = procurementAttemptPtr(p.ID) }},
		{"missing approver", func(p *ProcurementCostProposalRow) { p.Approver = nil }},
		{"missing operation", func(p *ProcurementCostProposalRow) { p.ApprovalOperationID = nil }},
		{"missing approved time", func(p *ProcurementCostProposalRow) { p.ApprovedAt = nil }},
		{"proposed with approval fields", func(p *ProcurementCostProposalRow) { p.State = "proposed" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := validProcurementApprovalRows()
			tc.mutate(&rows.Proposals[0])
			got := checkProcurementApproval(t, rows)
			if got.Mismatches.ProposalShapeRows != 1 || got.Mismatches.ManagedCorrectionApprovalRows != 1 || got.Mismatches.CorrectionEventApprovalRows != 1 ||
				got.Mismatches.ApprovedCorrectionRows != 0 || got.Mismatches.ApprovedEventRows != 0 {
				t.Fatalf("invalid approval shape entered the relation indexes: %+v", got.Mismatches)
			}
		})
	}
}

func TestCheckProcurementCostApprovalGraphRetainsFirstIdentityAndUniqueKeys(t *testing.T) {
	rows := validProcurementApprovalRows()
	rows.Users = append(rows.Users, rows.Users[0])
	rows.Attempts = append(rows.Attempts, rows.Attempts[0])
	rows.Reservations = append(rows.Reservations, rows.Reservations[0])
	rows.Events = append(rows.Events, rows.Events[0])
	rows.Corrections = append(rows.Corrections, rows.Corrections[0])
	rows.Proposals = append(rows.Proposals, rows.Proposals[0])
	got := checkProcurementApproval(t, rows)
	want := ProcurementCostApprovalMismatches{DuplicateUserRows: 1, DuplicateAttemptRows: 1, DuplicateReservationRows: 1,
		DuplicateEventRows: 1, DuplicateEventKeyRows: 1, DuplicateCorrectionRows: 1, DuplicateCorrectionOperationRows: 1,
		DuplicateProposalRows: 1, DuplicateApprovalOperationRows: 1}
	if got.Mismatches != want || got.Scanned.Proposals != 2 || got.Scanned.Users != 3 {
		t.Fatalf("duplicates amplified graph comparisons or lost raw counts: %+v", got)
	}

	rows = validProcurementApprovalRows()
	event := rows.Events[0]
	event.ID, event.EvidenceRef = "new-event-id", "corrupt-duplicate"
	rows.Events = append(rows.Events, event)
	correction := rows.Corrections[0]
	correction.ID, correction.Amount = "new-correction-id", "999"
	rows.Corrections = append(rows.Corrections, correction)
	proposal := rows.Proposals[0]
	proposal.ID, proposal.Amount = "33333333-3333-3333-3333-333333333333", "999"
	rows.Proposals = append(rows.Proposals, proposal)
	got = checkProcurementApproval(t, rows)
	want = ProcurementCostApprovalMismatches{DuplicateEventKeyRows: 1, DuplicateCorrectionOperationRows: 1, DuplicateApprovalOperationRows: 1}
	if got.Mismatches != want {
		t.Fatalf("later conflicting unique keys replaced first facts: %+v", got.Mismatches)
	}
}

func TestCheckProcurementCostApprovalGraphChecksCanonicalNewAmounts(t *testing.T) {
	for _, amount := range []string{"", "01", "+1", "-0", "-1", "1e2", "1.0", "9223372036854775808"} {
		t.Run("amount "+amount, func(t *testing.T) {
			rows := validProcurementApprovalRows()
			rows.Proposals[0].Amount, rows.Corrections[0].Amount = amount, amount
			got := checkProcurementApproval(t, rows)
			if got.Mismatches.InvalidAmountRows != 2 || got.Mismatches.ApprovedCorrectionRows != 1 || got.Mismatches.ManagedCorrectionApprovalRows != 1 {
				t.Fatalf("noncanonical amount compared equal: %+v", got.Mismatches)
			}
		})
	}
	for _, value := range []string{"0", "-1", "+1", "01", "9223372036854775808"} {
		rows := validProcurementApprovalRows()
		rows.Proposals[0].ExpectedRevision = value
		if got := checkProcurementApproval(t, rows); got.Mismatches.InvalidAmountRows != 1 {
			t.Fatalf("invalid expected revision %q accepted: %+v", value, got.Mismatches)
		}
	}
	rows := validProcurementApprovalRows()
	rows.Proposals[0].ExpectedRevision, rows.Proposals[0].ExpectedEvidenceCount = "0", "-1"
	rows.Proposals[0].Amount = "-1"
	if got := checkProcurementApproval(t, rows); got.Mismatches.InvalidAmountRows != 1 {
		t.Fatalf("multiple bad amounts in one proposal were counted more than once: %+v", got.Mismatches)
	}
	rows = validProcurementApprovalRows()
	rows.Corrections[0].Amount, rows.Proposals[0].Amount = "9223372036854775807", "9223372036854775807"
	if got := checkProcurementApproval(t, rows); !got.Balanced {
		t.Fatalf("exact BIGINT boundary rejected: %+v", got.Mismatches)
	}
}

func TestCheckProcurementCostApprovalGraphChecksTextAndEnumsPerSourceRow(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProcurementCostApprovalRows)
	}{
		{"uppercase uuid", func(r *ProcurementCostApprovalRows) { r.Proposals[0].ID = "aaaaaaaa-aaaa-aaaa-aaaa-AAAAAAAAAAAA" }},
		{"invalid operation uuid", func(r *ProcurementCostApprovalRows) {
			r.Proposals[0].ApprovalOperationID = procurementAttemptPtr("not-uuid")
		}},
		{"empty nullable approver", func(r *ProcurementCostApprovalRows) { r.Proposals[0].Approver = procurementAttemptPtr("") }},
		{"long proposal evidence", func(r *ProcurementCostApprovalRows) { r.Proposals[0].EvidenceRef = strings.Repeat("x", 257) }},
		{"control proposal evidence", func(r *ProcurementCostApprovalRows) { r.Proposals[0].EvidenceRef = "line\nnext" }},
		{"long proposal reason", func(r *ProcurementCostApprovalRows) { r.Proposals[0].Reason = strings.Repeat("x", 401) }},
		{"control proposal reason", func(r *ProcurementCostApprovalRows) { r.Proposals[0].Reason = "line\tvalue" }},
		{"empty correction evidence", func(r *ProcurementCostApprovalRows) { r.Corrections[0].EvidenceRef = "" }},
		{"event invalid utf8", func(r *ProcurementCostApprovalRows) { r.Events[0].EvidenceRef = string([]byte{0xff}) }},
		{"event nul", func(r *ProcurementCostApprovalRows) { r.Events[0].Type = "cost\x00corrected" }},
		{"attempt optional empty", func(r *ProcurementCostApprovalRows) { r.Attempts[0].CostAmount = procurementAttemptPtr("") }},
		{"reservation multiple invalid", func(r *ProcurementCostApprovalRows) { r.Reservations[0].BindingID, r.Reservations[0].State = "", "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := validProcurementApprovalRows()
			tc.mutate(&rows)
			if got := checkProcurementApproval(t, rows); got.Mismatches.InvalidTextRows != 1 {
				t.Fatalf("invalid text did not count exactly one source row: %+v", got.Mismatches)
			}
		})
	}
	rows := validProcurementApprovalRows()
	rows.Proposals[0].State, rows.Proposals[0].Basis, rows.Corrections[0].Basis = "other", "unknown", "unknown"
	if got := checkProcurementApproval(t, rows); got.Mismatches.InvalidEnumRows != 2 || got.Mismatches.ProposalShapeRows != 1 {
		t.Fatalf("enum errors were not row counts: %+v", got.Mismatches)
	}
	rows = validProcurementApprovalRows()
	rows.Proposals[0].Reason, rows.Proposals[0].EvidenceRef = strings.Repeat("r", 400), strings.Repeat("e", 256)
	rows.Corrections[0].EvidenceRef, rows.Events[0].EvidenceRef = rows.Proposals[0].EvidenceRef, rows.Proposals[0].EvidenceRef
	if got := checkProcurementApproval(t, rows); !got.Balanced {
		t.Fatalf("exact text boundaries rejected: %+v", got)
	}
}

func TestCheckProcurementCostApprovalGraphInvalidIdentityCannotSatisfyReferences(t *testing.T) {
	rows := validProcurementApprovalRows()
	rows.Users[0].ID, rows.Proposals[0].Requester = "", ""
	rows.Proposals[0].ID = "not-a-uuid"
	got := checkProcurementApproval(t, rows)
	if got.Mismatches.InvalidTextRows != 2 || got.Mismatches.ProposalIdentityRows != 1 || got.Mismatches.ManagedCorrectionApprovalRows != 1 || got.Mismatches.CorrectionEventApprovalRows != 1 {
		t.Fatalf("illegal identities entered the graph indexes: %+v", got.Mismatches)
	}
}

func TestCheckProcurementCostApprovalGraphResourceAndContextErrorsAreAtomic(t *testing.T) {
	assertError := func(ctx context.Context, rows ProcurementCostApprovalRows, expected error) {
		t.Helper()
		got, err := CheckProcurementCostApprovalGraph(ctx, rows)
		if !errors.Is(err, expected) || got != (ProcurementCostApprovalCheck{}) {
			t.Fatalf("execution failure exposed a partial summary: %+v %v; want %v", got, err, expected)
		}
	}
	assertError(nil, validProcurementApprovalRows(), ErrProcurementCostApprovalRowsInput)
	assertError(context.Background(), ProcurementCostApprovalRows{Users: make([]ProcurementApprovalUserRow, fundingSourceRowLimit+1)}, ErrProcurementCostApprovalRowsResource)
	exactRows := ProcurementCostApprovalRows{Users: make([]ProcurementApprovalUserRow, fundingSourceRowLimit)}
	if got := checkProcurementApproval(t, exactRows); got.Scanned.Users != fundingSourceRowLimit || got.Mismatches.InvalidTextRows != fundingSourceRowLimit {
		t.Fatalf("exact row budget or invalid identity counting changed: %+v", got)
	}
	rows := ProcurementCostApprovalRows{Users: []ProcurementApprovalUserRow{{ID: strings.Repeat("x", fundingSourceByteLimit)}}}
	if got := checkProcurementApproval(t, rows); !got.Balanced {
		t.Fatalf("exact text resource limit rejected: %+v", got)
	}
	rows.Users[0].ID += "x"
	assertError(context.Background(), rows, ErrProcurementCostApprovalRowsResource)
	// Nullable and reused text belongs to the shared budget too.
	rows = validProcurementApprovalRows()
	rows.Attempts[0].CostVersionID = procurementAttemptPtr(strings.Repeat("x", fundingSourceByteLimit))
	assertError(context.Background(), rows, ErrProcurementCostApprovalRowsResource)
	rows = validProcurementApprovalRows()
	rows.Reservations[0].BindingID = strings.Repeat("x", fundingSourceByteLimit)
	assertError(context.Background(), rows, ErrProcurementCostApprovalRowsResource)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	assertError(canceled, validProcurementApprovalRows(), ErrProcurementCostApprovalRowsCanceled)
	deadline, stop := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer stop()
	assertError(deadline, validProcurementApprovalRows(), ErrProcurementCostApprovalRowsTimeout)
	for _, tc := range []struct{ cause, want error }{{context.Canceled, ErrProcurementCostApprovalRowsCanceled}, {context.DeadlineExceeded, ErrProcurementCostApprovalRowsTimeout}} {
		// Four 256-row checks cover text validation; cancellation at check 6
		// occurs during identity indexing rather than only at API entry.
		ctx := &fundingSourceSwitchingContext{switchAfter: 6, err: tc.cause}
		assertError(ctx, ProcurementCostApprovalRows{Users: make([]ProcurementApprovalUserRow, 1024)}, tc.want)
	}
}
