package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func procurementJournalJSONString(values map[string]string) string {
	raw, _ := json.Marshal(values)
	return string(raw)
}

func validProcurementCostJournalRows() ProcurementCostJournalRows {
	source := validProcurementApprovalRows()
	p, c := source.Proposals[0], source.Corrections[0]
	return ProcurementCostJournalRows{
		Users: source.Users, Attempts: source.Attempts, Reservations: source.Reservations, Corrections: source.Corrections, Proposals: source.Proposals,
		Audits: []ProcurementAuditRow{
			{ID: "audit-proposed", Actor: p.Requester, Action: "procurement.cost_proposed", Target: p.ID, TenantScope: procurementAttemptPtr(p.TenantID), RequestID: source.Attempts[0].RequestID,
				Diff: procurementJournalJSONString(map[string]string{"attempt_id": p.AttemptID, "amount": p.Amount, "basis": p.Basis, "evidence_ref": p.EvidenceRef})},
			{ID: "audit-approved", Actor: *p.Approver, Action: "procurement.cost_approved", Target: p.ID, TenantScope: procurementAttemptPtr(p.TenantID), RequestID: source.Attempts[0].RequestID,
				Diff: procurementJournalJSONString(map[string]string{"attempt_id": p.AttemptID, "amount": p.Amount, "evidence_ref": p.EvidenceRef})},
		},
		Outbox: []ProcurementOutboxRow{{ID: "cost-outbox", TenantID: c.TenantID, OperationID: c.OperationID, Kind: "cost_corrected", SchemaVersion: 1,
			Payload: procurementJournalJSONString(map[string]string{"attempt_id": c.AttemptID, "amount": c.Amount, "basis": c.Basis})}},
	}
}

func addProcurementJournalLegacy(rows *ProcurementCostJournalRows, actor string) {
	a := rows.Attempts[0]
	a.ID = "legacy-attempt"
	rows.Attempts = append(rows.Attempts, a)
	c := ProcurementCostCorrectionRow{ID: "legacy-correction", TenantID: a.TenantID, AttemptID: a.ID,
		OperationID: "procurement:33333333-3333-3333-3333-333333333333", Amount: "9007199254740993", Basis: "contract", EvidenceRef: "legacy:invoice"}
	rows.Corrections = append(rows.Corrections, c)
	rows.Audits = append(rows.Audits, ProcurementAuditRow{ID: "legacy-audit", Actor: actor, Action: "cost.corrected", Target: c.ID,
		TenantScope: procurementAttemptPtr(c.TenantID), RequestID: a.RequestID, Diff: procurementJournalJSONString(map[string]string{"amount": c.Amount, "basis": c.Basis})})
	rows.Outbox = append(rows.Outbox, ProcurementOutboxRow{ID: "legacy-outbox", TenantID: c.TenantID, OperationID: c.OperationID, Kind: "cost_corrected", SchemaVersion: 1,
		Payload: procurementJournalJSONString(map[string]string{"attempt_id": c.AttemptID, "amount": c.Amount, "basis": c.Basis})})
}

func checkProcurementCostJournal(t testing.TB, rows ProcurementCostJournalRows) ProcurementCostJournalCheck {
	t.Helper()
	got, err := CheckProcurementCostJournalGraph(context.Background(), rows)
	if err != nil || got.Balanced != (got.Mismatches == (ProcurementCostJournalMismatches{})) {
		t.Fatalf("journal core failed or did not include all mismatch directions: %+v %v", got, err)
	}
	return got
}

func TestCheckProcurementCostJournalGraphBalancedProposedManagedLegacyAndUnrelated(t *testing.T) {
	empty := checkProcurementCostJournal(t, ProcurementCostJournalRows{})
	if !empty.Balanced || empty.Scope != "procurement_cost_journal_graph_v1" || empty.Scanned != (ProcurementCostJournalCounts{}) {
		t.Fatalf("empty scoped result invalid: %+v", empty)
	}
	rows := validProcurementCostJournalRows()
	if got := checkProcurementCostJournal(t, rows); !got.Balanced || got.Scanned != (ProcurementCostJournalCounts{Users: 2, Attempts: 1, Reservations: 1, Corrections: 1, Proposals: 1, Audits: 2, Outbox: 1}) {
		t.Fatalf("valid managed graph rejected: %+v", got)
	}
	for _, actor := range []string{"system", "legacy-actor-no-longer-in-users"} {
		rows = validProcurementCostJournalRows()
		addProcurementJournalLegacy(&rows, actor)
		rows.Audits = append(rows.Audits,
			ProcurementAuditRow{ID: "other-audit", Action: "user.create", Diff: `{"not-a-cost":123}`, TenantScope: procurementAttemptPtr("")},
			ProcurementAuditRow{ID: "other-null-tenant-audit", Action: "projection.switched", Diff: `not JSON`})
		rows.Outbox = append(rows.Outbox, ProcurementOutboxRow{ID: "other-outbox", Kind: "cost_recorded", SchemaVersion: -3, Payload: `not a cost payload`})
		got := checkProcurementCostJournal(t, rows)
		if !got.Balanced || got.Scanned.Corrections != 2 || got.Scanned.Audits != 5 || got.Scanned.Outbox != 3 {
			t.Fatalf("legacy prefix/actor or unrelated fact misclassified: %+v", got)
		}
		raw, err := json.Marshal(got)
		if err != nil || strings.Contains(string(raw), actor) || strings.Contains(string(raw), "9007199254740993") || strings.Contains(string(raw), "legacy:") {
			t.Fatal("summary disclosed source identities or amounts", err)
		}
	}
	rows = validProcurementCostJournalRows()
	rows.Proposals[0].State = "proposed"
	rows.Proposals[0].Approver, rows.Proposals[0].ApprovalOperationID, rows.Proposals[0].ApprovedAt = nil, nil, nil
	rows.Audits, rows.Corrections, rows.Outbox = rows.Audits[:1], nil, nil
	if got := checkProcurementCostJournal(t, rows); !got.Balanced {
		t.Fatalf("unapproved proposal required future effects: %+v", got)
	}
}

func TestCheckProcurementCostJournalGraphBothDirectionsAndHistoricalSource(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProcurementCostJournalRows)
		want   ProcurementCostJournalMismatches
	}{
		{"missing proposed audit", func(r *ProcurementCostJournalRows) { r.Audits = r.Audits[1:] }, ProcurementCostJournalMismatches{ProposedAuditRows: 1}},
		{"missing approved audit", func(r *ProcurementCostJournalRows) { r.Audits = r.Audits[:1] }, ProcurementCostJournalMismatches{ApprovedAuditRows: 1}},
		{"missing outbox", func(r *ProcurementCostJournalRows) { r.Outbox = nil }, ProcurementCostJournalMismatches{CorrectionOutboxRows: 1}},
		{"wrong maker", func(r *ProcurementCostJournalRows) { r.Audits[0].Actor = "other" }, ProcurementCostJournalMismatches{ProposedAuditRows: 1, AuditSourceRows: 1}},
		{"wrong checker", func(r *ProcurementCostJournalRows) { r.Audits[1].Actor = "other" }, ProcurementCostJournalMismatches{ApprovedAuditRows: 1, AuditSourceRows: 1}},
		{"wrong audit tenant", func(r *ProcurementCostJournalRows) { r.Audits[0].TenantScope = procurementAttemptPtr("other") }, ProcurementCostJournalMismatches{ProposedAuditRows: 1, AuditSourceRows: 1}},
		{"nil audit tenant", func(r *ProcurementCostJournalRows) { r.Audits[0].TenantScope = nil }, ProcurementCostJournalMismatches{ProposedAuditRows: 1, AuditSourceRows: 1}},
		{"wrong audit request", func(r *ProcurementCostJournalRows) { r.Audits[1].RequestID = "other" }, ProcurementCostJournalMismatches{ApprovedAuditRows: 1, AuditSourceRows: 1}},
		{"wrong audit target", func(r *ProcurementCostJournalRows) { r.Audits[1].Target = "other" }, ProcurementCostJournalMismatches{ApprovedAuditRows: 1, AuditSourceRows: 1}},
		{"wrong proposed amount", func(r *ProcurementCostJournalRows) {
			r.Audits[0].Diff = strings.Replace(r.Audits[0].Diff, `"25"`, `"26"`, 1)
		}, ProcurementCostJournalMismatches{ProposedAuditRows: 1, AuditSourceRows: 1}},
		{"wrong outbox tenant", func(r *ProcurementCostJournalRows) { r.Outbox[0].TenantID = "other" }, ProcurementCostJournalMismatches{CorrectionOutboxRows: 1, OutboxSourceRows: 1}},
		{"wrong outbox version", func(r *ProcurementCostJournalRows) { r.Outbox[0].SchemaVersion = 2 }, ProcurementCostJournalMismatches{CorrectionOutboxRows: 1, OutboxSourceRows: 1}},
		{"wrong outbox operation", func(r *ProcurementCostJournalRows) { r.Outbox[0].OperationID = "other" }, ProcurementCostJournalMismatches{CorrectionOutboxRows: 1, OutboxSourceRows: 1}},
		{"wrong outbox basis", func(r *ProcurementCostJournalRows) {
			r.Outbox[0].Payload = strings.Replace(r.Outbox[0].Payload, "invoice", "contract", 1)
		}, ProcurementCostJournalMismatches{CorrectionOutboxRows: 1, OutboxSourceRows: 1}},
		{"orphan outbox", func(r *ProcurementCostJournalRows) { r.Corrections = nil }, ProcurementCostJournalMismatches{OutboxSourceRows: 1}},
		{"missing proposal", func(r *ProcurementCostJournalRows) { r.Proposals = nil }, ProcurementCostJournalMismatches{ManagedCorrectionProposalRows: 1, AuditSourceRows: 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := validProcurementCostJournalRows()
			tc.mutate(&rows)
			got := checkProcurementCostJournal(t, rows)
			if got.Mismatches != tc.want {
				t.Fatalf("mismatch directions changed: got %+v want %+v", got.Mismatches, tc.want)
			}
		})
	}
	rows := validProcurementCostJournalRows()
	addProcurementJournalLegacy(&rows, "system")
	rows.Audits = rows.Audits[:2]
	if got := checkProcurementCostJournal(t, rows); got.Mismatches != (ProcurementCostJournalMismatches{LegacyAuditRows: 1}) {
		t.Fatalf("missing legacy audit not found: %+v", got)
	}
	rows = validProcurementCostJournalRows()
	addProcurementJournalLegacy(&rows, "system")
	rows.Audits[2].Actor = ""
	if got := checkProcurementCostJournal(t, rows); got.Mismatches != (ProcurementCostJournalMismatches{InvalidTextRows: 1, LegacyAuditRows: 1, AuditSourceRows: 1}) {
		t.Fatalf("empty legacy actor accepted: %+v", got)
	}
	rows = validProcurementCostJournalRows()
	// Later invoices and process clock corrections do not change historical audit
	// identity or turn an old proposal snapshot into a current revision predicate.
	rows.Attempts[0].CostAmount = procurementAttemptPtr("9223372036854775807")
	rows.Reservations[0].SpentAmount, rows.Reservations[0].State = "999", "disputed"
	rows.Proposals[0].ExpectedRevision, rows.Proposals[0].ExpectedEvidenceCount = "9223372036854775807", "0"
	rows.Audits[0].CreatedAt, rows.Outbox[0].CreatedAt = time.Unix(999, 0), time.Unix(1, 0)
	if got := checkProcurementCostJournal(t, rows); !got.Balanced {
		t.Fatalf("current state or wall-clock order was used as history: %+v", got)
	}
	// A later approved correction creates its own immutable audit/outbox effects;
	// the first correction remains valid even when its amount is no longer current.
	p, c := rows.Proposals[0], rows.Corrections[0]
	p.ID, p.ApprovalOperationID, p.Amount, p.Basis, p.EvidenceRef = "33333333-3333-3333-3333-333333333333", procurementAttemptPtr("44444444-4444-4444-4444-444444444444"), "0", "contract", "contract:later"
	c.ID, c.OperationID, c.Amount, c.Basis, c.EvidenceRef = "later-correction", "procurement:"+*p.ApprovalOperationID, p.Amount, p.Basis, p.EvidenceRef
	rows.Proposals, rows.Corrections = append(rows.Proposals, p), append(rows.Corrections, c)
	rows.Audits = append(rows.Audits,
		ProcurementAuditRow{ID: "later-proposed", Actor: p.Requester, Action: "procurement.cost_proposed", Target: p.ID, TenantScope: procurementAttemptPtr(p.TenantID), RequestID: rows.Attempts[0].RequestID,
			Diff: procurementJournalJSONString(map[string]string{"attempt_id": p.AttemptID, "amount": p.Amount, "basis": p.Basis, "evidence_ref": p.EvidenceRef})},
		ProcurementAuditRow{ID: "later-approved", Actor: *p.Approver, Action: "procurement.cost_approved", Target: p.ID, TenantScope: procurementAttemptPtr(p.TenantID), RequestID: rows.Attempts[0].RequestID,
			Diff: procurementJournalJSONString(map[string]string{"attempt_id": p.AttemptID, "amount": p.Amount, "evidence_ref": p.EvidenceRef})})
	rows.Outbox = append(rows.Outbox, ProcurementOutboxRow{ID: "later-outbox", TenantID: c.TenantID, OperationID: c.OperationID, Kind: "cost_corrected", SchemaVersion: 1,
		Payload: procurementJournalJSONString(map[string]string{"attempt_id": c.AttemptID, "amount": c.Amount, "basis": c.Basis})})
	if got := checkProcurementCostJournal(t, rows); !got.Balanced || got.Scanned.Corrections != 2 || got.Scanned.Audits != 4 {
		t.Fatalf("later correction invalidated earlier immutable history: %+v", got)
	}
}

func TestCheckProcurementCostJournalGraphStrictMoneyJSONAndEscapes(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"amount":25,"basis":"invoice","attempt_id":"attempt-a"}`,
		`{"amount":null,"basis":"invoice","attempt_id":"attempt-a"}`,
		`{"amount":true,"basis":"invoice","attempt_id":"attempt-a"}`,
		`{"amount":{},"basis":"invoice","attempt_id":"attempt-a"}`,
		`{"amount":"025","basis":"invoice","attempt_id":"attempt-a"}`,
		`{"amount":"+25","basis":"invoice","attempt_id":"attempt-a"}`,
		`{"amount":"9223372036854775808","basis":"invoice","attempt_id":"attempt-a"}`,
		`{"amount":"25","basis":"invoice","attempt_id":"attempt-a","unknown":"x"}`,
		`{"amount":"25","amount":"25","basis":"invoice","attempt_id":"attempt-a"}`,
		`{"amount":"25","\u0061mount":"25","basis":"invoice","attempt_id":"attempt-a"}`,
		`{"amount":"25","basis":"invoice","attempt_id":"attempt-a"} {}`,
		`{"amount":"25","basis":"invoice","attempt_id":"\ud800"}`,
		`{"amount":"25","basis":"invoice","attempt_id":"\udc00"}`,
		`{"amount":"25","basis":"invoice","attempt_id":"\ud800\u0041"}`,
	} {
		rows := validProcurementCostJournalRows()
		rows.Outbox[0].Payload = raw
		got := checkProcurementCostJournal(t, rows)
		if got.Mismatches != (ProcurementCostJournalMismatches{InvalidJSONRows: 1, CorrectionOutboxRows: 1, OutboxSourceRows: 1}) {
			t.Fatalf("non-strict outbox accepted (%s): %+v", raw, got.Mismatches)
		}
	}
	rows := validProcurementCostJournalRows()
	rows.Audits[1].Diff = `{"attempt_id":"attempt-a","amount":"25","basis":"invoice","evidence_ref":"invoice:a"}`
	if got := checkProcurementCostJournal(t, rows); got.Mismatches != (ProcurementCostJournalMismatches{InvalidJSONRows: 1, ApprovedAuditRows: 1, AuditSourceRows: 1}) {
		t.Fatalf("approval incorrectly accepted proposed-audit shape: %+v", got)
	}
	rows = validProcurementCostJournalRows()
	rows.Audits[0].Diff = "  { \"evidence_ref\":\"invoice:a\",\"basis\":\"invoice\",\"amount\":\"25\",\"\\u0061ttempt_id\":\"attempt-a\" }\n"
	if got := checkProcurementCostJournal(t, rows); !got.Balanced {
		t.Fatalf("JSONB ordering/spacing or legal key escape was rejected: %+v", got)
	}
	rows = validProcurementCostJournalRows()
	rows.Proposals[0].EvidenceRef, rows.Corrections[0].EvidenceRef = "invoice:😀", "invoice:😀"
	rows.Audits[0].Diff = `{"amount":"25","attempt_id":"attempt-a","basis":"invoice","evidence_ref":"invoice:\ud83d\ude00"}`
	rows.Audits[1].Diff = `{"amount":"25","attempt_id":"attempt-a","evidence_ref":"invoice:\uD83D\uDE00"}`
	if got := checkProcurementCostJournal(t, rows); !got.Balanced {
		t.Fatalf("legal paired surrogate escape was rejected: %+v", got)
	}
	for _, amount := range []string{"0", "9007199254740993", "9223372036854775807"} {
		rows = validProcurementCostJournalRows()
		rows.Proposals[0].Amount, rows.Corrections[0].Amount = amount, amount
		for i := range rows.Audits {
			rows.Audits[i].Diff = strings.Replace(rows.Audits[i].Diff, `"25"`, `"`+amount+`"`, 1)
		}
		rows.Outbox[0].Payload = strings.Replace(rows.Outbox[0].Payload, `"25"`, `"`+amount+`"`, 1)
		if got := checkProcurementCostJournal(t, rows); !got.Balanced {
			t.Fatalf("exact Money string boundary rejected: %+v", got)
		}
	}
}

func TestCheckProcurementCostJournalGraphDuplicatesRetainFirstAndSourceValidation(t *testing.T) {
	// An unrelated kind may legally have an empty operation ID. The full typed
	// (operation,kind) uniqueness contract still includes that empty value.
	unrelated := ProcurementCostJournalRows{Outbox: []ProcurementOutboxRow{
		{ID: "unrelated-first", Kind: "other", OperationID: "", Payload: "unparsed"},
		{ID: "unrelated-second", Kind: "other", OperationID: "", Payload: "also unparsed"},
	}}
	if got := checkProcurementCostJournal(t, unrelated); got.Mismatches != (ProcurementCostJournalMismatches{DuplicateOutboxOperationKindRows: 1}) || got.Balanced {
		t.Fatalf("empty-operation duplicate pair was omitted: %+v", got)
	}
	rows := validProcurementCostJournalRows()
	rows.Users = append(rows.Users, rows.Users[0])
	rows.Attempts = append(rows.Attempts, rows.Attempts[0])
	rows.Reservations = append(rows.Reservations, rows.Reservations[0])
	rows.Proposals = append(rows.Proposals, rows.Proposals[0])
	rows.Corrections = append(rows.Corrections, rows.Corrections[0])
	rows.Audits = append(rows.Audits, rows.Audits[0])
	rows.Outbox = append(rows.Outbox, rows.Outbox[0])
	want := ProcurementCostJournalMismatches{DuplicateUserRows: 1, DuplicateAttemptRows: 1, DuplicateReservationRows: 1, DuplicateProposalRows: 1,
		DuplicateApprovalOperationRows: 1, DuplicateCorrectionRows: 1, DuplicateCorrectionOperationRows: 1, DuplicateAuditRows: 1,
		DuplicateAuditEffectRows: 1, DuplicateOutboxRows: 1, DuplicateOutboxOperationKindRows: 1}
	if got := checkProcurementCostJournal(t, rows); got.Mismatches != want {
		t.Fatalf("exact duplicate rows amplified relationships: got %+v want %+v", got.Mismatches, want)
	}
	rows = validProcurementCostJournalRows()
	audit, box := rows.Audits[0], rows.Outbox[0]
	audit.ID, audit.Actor = "later-audit", "wrong-maker"
	box.ID, box.TenantID = "later-box", "wrong-tenant"
	rows.Audits, rows.Outbox = append(rows.Audits, audit), append(rows.Outbox, box)
	if got := checkProcurementCostJournal(t, rows); got.Mismatches != (ProcurementCostJournalMismatches{DuplicateAuditEffectRows: 1, DuplicateOutboxOperationKindRows: 1}) {
		t.Fatalf("later duplicate replaced original facts: %+v", got)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ProcurementCostJournalRows)
		field  func(ProcurementCostJournalMismatches) uint64
	}{
		{"source amount", func(r *ProcurementCostJournalRows) { r.Proposals[0].Amount = "01" }, func(m ProcurementCostJournalMismatches) uint64 { return m.InvalidAmountRows }},
		{"source revision", func(r *ProcurementCostJournalRows) { r.Proposals[0].ExpectedRevision = "0" }, func(m ProcurementCostJournalMismatches) uint64 { return m.InvalidAmountRows }},
		{"source basis", func(r *ProcurementCostJournalRows) { r.Corrections[0].Basis = "unknown" }, func(m ProcurementCostJournalMismatches) uint64 { return m.InvalidEnumRows }},
		{"source state", func(r *ProcurementCostJournalRows) { r.Proposals[0].State = "other" }, func(m ProcurementCostJournalMismatches) uint64 { return m.InvalidEnumRows }},
		{"source shape", func(r *ProcurementCostJournalRows) { r.Proposals[0].Approver = nil }, func(m ProcurementCostJournalMismatches) uint64 { return m.ProposalShapeRows }},
		{"source requester", func(r *ProcurementCostJournalRows) { r.Users = r.Users[1:] }, func(m ProcurementCostJournalMismatches) uint64 { return m.ProposalIdentityRows }},
		{"source tenant", func(r *ProcurementCostJournalRows) { r.Corrections[0].TenantID = "other" }, func(m ProcurementCostJournalMismatches) uint64 { return m.CorrectionIdentityRows }},
		{"source proposal", func(r *ProcurementCostJournalRows) { r.Corrections[0].EvidenceRef = "other" }, func(m ProcurementCostJournalMismatches) uint64 { return m.ManagedCorrectionProposalRows }},
		{"raw encoding", func(r *ProcurementCostJournalRows) { r.Audits[0].Diff = "\xff" }, func(m ProcurementCostJournalMismatches) uint64 { return m.InvalidTextRows }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := validProcurementCostJournalRows()
			tc.mutate(&rows)
			if got := checkProcurementCostJournal(t, rows); tc.field(got.Mismatches) != 1 || got.Balanced {
				t.Fatalf("invalid source was ignored: %+v", got)
			}
		})
	}
}

func TestCheckProcurementCostJournalGraphSharedResourcesAndCancellationAreAtomic(t *testing.T) {
	assertError := func(ctx context.Context, rows ProcurementCostJournalRows, want error) {
		t.Helper()
		got, err := CheckProcurementCostJournalGraph(ctx, rows)
		if !errors.Is(err, want) || err.Error() != want.Error() || got != (ProcurementCostJournalCheck{}) {
			t.Fatalf("failed check leaked result or private error: %+v %v, want %v", got, err, want)
		}
	}
	assertError(nil, validProcurementCostJournalRows(), ErrProcurementCostJournalRowsInput)
	assertError(context.Background(), ProcurementCostJournalRows{Users: make([]ProcurementApprovalUserRow, fundingSourceRowLimit), Audits: []ProcurementAuditRow{{ID: "one-too-many"}}}, ErrProcurementCostJournalRowsResource)
	rows := ProcurementCostJournalRows{Audits: []ProcurementAuditRow{{ID: "a", Action: "unrelated", Diff: strings.Repeat("x", fundingSourceByteLimit-len("a")-len("unrelated"))}}}
	if got := checkProcurementCostJournal(t, rows); !got.Balanced {
		t.Fatalf("exact raw text budget or unrelated JSON was rejected: %+v", got)
	}
	rows.Outbox = []ProcurementOutboxRow{{ID: "b", Kind: "k"}}
	assertError(context.Background(), rows, ErrProcurementCostJournalRowsResource)
	rows = validProcurementCostJournalRows()
	rows.Attempts[0].CostVersionID = procurementAttemptPtr(strings.Repeat("x", fundingSourceByteLimit))
	assertError(context.Background(), rows, ErrProcurementCostJournalRowsResource)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	assertError(canceled, validProcurementCostJournalRows(), ErrProcurementCostJournalRowsCanceled)
	deadline, stop := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer stop()
	assertError(deadline, validProcurementCostJournalRows(), ErrProcurementCostJournalRowsTimeout)
	for _, tc := range []struct{ cause, want error }{{context.Canceled, ErrProcurementCostJournalRowsCanceled}, {context.DeadlineExceeded, ErrProcurementCostJournalRowsTimeout}} {
		ctx := &fundingSourceSwitchingContext{switchAfter: 6, err: tc.cause}
		assertError(ctx, ProcurementCostJournalRows{Users: make([]ProcurementApprovalUserRow, 1024)}, tc.want)
	}
}
