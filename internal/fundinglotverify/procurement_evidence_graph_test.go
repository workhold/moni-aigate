package fundinglotverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

const evidenceTestUsage = `{"status":"confirmed_final","input_tokens":10,"output_tokens":2,"total_tokens":12,"cached_tokens":0,"reasoning_tokens":0}`

func evidenceTestHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
func evidenceTestRows() ProcurementEvidenceGraphRows {
	old := validProcurementAttemptRows()
	at := time.Unix(50, 0).UTC()
	binding := old.Bindings[0]
	binding.OperationID = procurementCommandOperations[1]
	account := ProcurementEvidenceAccountRow{ID: "account-a", Label: "synthetic account", Provider: "synthetic provider", State: "active", LimitAmount: "1000", HeldAmount: "220", SpentAmount: "0", Revision: "1", CreatedAt: at}
	snap := map[string]any{"id": account.ID, "label": account.Label, "provider": account.Provider, "state": account.State, "limit_amount": account.LimitAmount, "held_amount": "0", "spent_amount": "0", "revision": "1", "created_at": at.Format(time.RFC3339Nano)}
	commands := []ProcurementCommandRow{
		{OperationID: procurementCommandOperations[0], Actor: "admin-a", Kind: "account_create", TargetID: account.ID, InputDigest: strings.Repeat("a", 64), Status: "applied", Before: "null", After: procurementCommandJSON(snap)},
		{OperationID: binding.OperationID, Actor: "admin-a", Kind: "credential_bind", TargetID: binding.CredentialID, InputDigest: strings.Repeat("b", 64), Status: "applied", Before: "null", After: procurementCommandBindingSnapshot(binding, "1")},
	}
	return ProcurementEvidenceGraphRows{Channels: old.Channels, Credentials: old.Credentials, Versions: old.Versions, PoolMemberships: old.PoolMemberships, Tenants: old.Tenants,
		Requests: []ProcurementEvidenceRequestRow{{ID: "request-a", TenantID: "tenant-a", InputUpperBound: "100", OutputLimit: "20", PolicyVersion: "policy-a", RequestDeadline: time.Unix(300, 0)}}, CostVersions: old.CostVersions, Accounts: []ProcurementEvidenceAccountRow{account}, Bindings: []ProcurementAttemptBindingRow{binding}, Attempts: old.Attempts,
		Reservations: []ProcurementEvidenceReservationRow{{AttemptID: "attempt-a", AccountID: "account-a", BindingID: "binding-a", InputBound: "100", OutputBound: "20", UpperAmount: "220", HeldAmount: "220", SpentAmount: "0", State: "prepared", Revision: "1"}},
		BudgetEvents: []ProcurementEvidenceBudgetEventRow{{ID: "budget-reserve", EventKey: "reserve:attempt-a", AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "220", SpentDelta: "0", Type: "reserved"}}, Users: []ProcurementApprovalUserRow{{ID: "admin-a"}}, Commands: commands, Audits: []ProcurementAuditRow{procurementCommandAudit(commands[0], "audit-create"), procurementCommandAudit(commands[1], "audit-bind")}}
}
func evidenceTestAppendNormal(rows *ProcurementEvidenceGraphRows, usage, reason, stage, original string, originalContent bool) {
	n := len(rows.Finalizations) + 1
	event := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
	id := fmt.Sprintf("40000000-0000-0000-0000-%012d", n)
	sourceEvent := fmt.Sprintf("source-%d", n)
	input := fmt.Sprintf(`{"ID":%q,"TenantID":"tenant-a","RequestID":"request-a","AttemptID":"attempt-a","Source":"provider","SourceEventID":%q,"Usage":%s,"ProtocolOutcome":"completed","TransportEndReason":%q,"DeliveryStage":%q,"FinishReason":"","ContentWritten":%t,"ObservedAt":"0001-01-01T00:00:00Z"}`, id, sourceEvent, usage, reason, original, originalContent)
	rows.Metering = append(rows.Metering, ProcurementEvidenceMeterRow{ID: event, TenantID: "tenant-a", RequestID: "request-a", AttemptID: procurementAttemptPtr("attempt-a"), Source: "provider", SourceEventID: sourceEvent, SchemaVersion: 1, UsageJSON: usage})
	rows.Finalizations = append(rows.Finalizations, ProcurementEvidenceFinalizationRow{ID: id, TenantID: "tenant-a", RequestID: "request-a", EventID: event, InputDigest: evidenceTestHash(input), ProtocolOutcome: "completed", TransportEndReason: reason, DeliveryStage: stage, ContentWritten: stage == "content", PolicyVersion: "policy-a"})
	digest := evidenceTestHash(`{"Usage":` + usage + `,"Reason":` + fmt.Sprintf("%q", reason) + `}`)
	found := false
	for _, old := range rows.Identities {
		found = found || old.AttemptID == "attempt-a" && old.Digest == digest
	}
	if !found {
		rows.Identities = append(rows.Identities, ProcurementEvidenceIdentityRow{AttemptID: "attempt-a", EventID: event, Digest: digest})
	}
	rows.Requests[0].LatestFinalizationID = procurementAttemptPtr(id)
}
func evidenceTestRecord(rows *ProcurementEvidenceGraphRows, amount, basis string) {
	rows.Outbox = append(rows.Outbox, ProcurementOutboxRow{ID: "outbox-cost", TenantID: "tenant-a", OperationID: "attempt-a", Kind: "cost_recorded", SchemaVersion: 1, Payload: procurementCommandJSON(map[string]string{"attempt_id": "attempt-a", "amount": amount, "basis": basis})})
	rows.Attempts[0].CostStatus = basis
	rows.Attempts[0].CostAmount = procurementAttemptPtr(amount)
}
func evidenceTestTerminal(rows *ProcurementEvidenceGraphRows, kind, amount string) {
	p := &rows.Reservations[0]
	p.HeldAmount = "0"
	p.SpentAmount = amount
	p.State = "settled"
	p.Revision = "2"
	rows.Accounts[0].HeldAmount = "0"
	rows.Accounts[0].SpentAmount = amount
	rows.Attempts[0].DeliveryState = "completed"
	key := "settle:attempt-a"
	if kind != "cost_confirmed" {
		p.State = "released"
		key = "release:attempt-a"
	}
	ref := ""
	if kind != "expired_without_permit" {
		ref = rows.Metering[len(rows.Metering)-1].ID
	}
	rows.BudgetEvents = append(rows.BudgetEvents, ProcurementEvidenceBudgetEventRow{ID: "budget-terminal", EventKey: key, AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "-220", SpentDelta: amount, Type: kind, EvidenceRef: ref})
}
func evidenceTestCheck(t *testing.T, rows ProcurementEvidenceGraphRows) ProcurementEvidenceGraphCheck {
	t.Helper()
	got, err := CheckProcurementEvidenceGraph(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balanced != (got.Status == "balanced") || got.CoverageComplete == procurementEvidenceNonzero(got.Unproven) {
		t.Fatalf("invalid result shape: %+v", got)
	}
	return got
}
func evidenceTestNoMismatch(t *testing.T, got ProcurementEvidenceGraphCheck) {
	t.Helper()
	if got.Status == "mismatch" {
		t.Fatalf("unexpected mismatch: %+v", got)
	}
}

func TestCheckProcurementEvidenceGraphPreparedEmptyAndStructuralRaw(t *testing.T) {
	for _, rows := range []ProcurementEvidenceGraphRows{{}, evidenceTestRows()} {
		got := evidenceTestCheck(t, rows)
		if !got.Balanced || got.Scope != "procurement_domain_evidence_graph_v1" {
			t.Fatalf("valid source graph failed: %+v", got)
		}
	}
	rows := evidenceTestRows()
	rows.Attempts[0].ChannelVersionID = "private-missing-version"
	got := evidenceTestCheck(t, rows)
	if got.Structural.Binding.AttemptVersionIdentityRows != 1 || got.Status != "mismatch" {
		t.Fatalf("five core raw graph was not actually checked: %+v", got)
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"tenant-a", "account-a", "private-missing-version", "attempt-a"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("report disclosed source text: %s", raw)
		}
	}
}
func TestCheckProcurementEvidenceGraphNormalStates(t *testing.T) {
	for _, tc := range []struct{ name, usage, reason, kind, amount string }{
		{"confirmed", evidenceTestUsage, "done", "cost_confirmed", "22"},
		{"zero_consume", evidenceTestUsage, "not_sent", "released_no_consumption", "0"},
		{"rejected", evidenceTestUsage, "rejected_no_consumption", "released_no_consumption", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := evidenceTestRows()
			evidenceTestAppendNormal(&rows, tc.usage, tc.reason, "content", "content", false)
			evidenceTestRecord(&rows, tc.amount, "contract")
			evidenceTestTerminal(&rows, tc.kind, tc.amount)
			got := evidenceTestCheck(t, rows)
			if !got.Balanced {
				t.Fatalf("normal state failed: %+v", got)
			}
		})
	}
	rows := evidenceTestRows()
	evidenceTestTerminal(&rows, "expired_without_permit", "0")
	rows.Attempts[0].DeliveryState = "failed"
	if got := evidenceTestCheck(t, rows); !got.Balanced {
		t.Fatalf("no permit recovery manufactured evidence obligation: %+v", got)
	}
	for _, kind := range []string{"consumption_unknown", "recovered_unknown"} {
		rows := evidenceTestRows()
		ref := ""
		if kind == "consumption_unknown" {
			usage := strings.Replace(evidenceTestUsage, "confirmed_final", "provisional", 1)
			evidenceTestAppendNormal(&rows, usage, "eof", "none", "none", false)
			ref = rows.Metering[0].ID
		}
		rows.Reservations[0].State = "unknown"
		rows.Reservations[0].Revision = "2"
		rows.Attempts[0].DeliveryState = "unknown"
		rows.BudgetEvents = append(rows.BudgetEvents, ProcurementEvidenceBudgetEventRow{ID: "budget-unknown", EventKey: "unknown:attempt-a", AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "0", SpentDelta: "0", Type: kind, EvidenceRef: ref})
		if got := evidenceTestCheck(t, rows); !got.Balanced {
			t.Fatalf("unknown path failed: %+v", got)
		}
	}
}
func TestCheckProcurementEvidenceGraphDigestStickyAndReplay(t *testing.T) {
	rows := evidenceTestRows()
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "content", "content", false)
	evidenceTestRecord(&rows, "22", "contract")
	evidenceTestTerminal(&rows, "cost_confirmed", "22")
	// A new meter with the exact same semantic digest must retain the first identity.
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "content", "content", true)
	got := evidenceTestCheck(t, rows)
	if !got.Balanced || got.Scanned.Identities != 1 || got.Scanned.Metering != 2 {
		t.Fatalf("semantic replay demanded extra effects: %+v", got)
	}
	rows.Requests[0].LatestFinalizationID = procurementAttemptPtr(rows.Finalizations[0].ID)
	if got = evidenceTestCheck(t, rows); !got.Balanced {
		t.Fatalf("exact id replay latest was sorted by time/id: %+v", got)
	}
	rows.Finalizations[0].InputDigest = strings.Repeat("0", 64)
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.FinalizationInputDigestRows != 1 {
		t.Fatalf("bad digest hidden by gap: %+v", got)
	}
	rows = evidenceTestRows()
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "content", "none", false)
	evidenceTestRecord(&rows, "22", "contract")
	evidenceTestTerminal(&rows, "cost_confirmed", "22")
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.FinalizationStickyRows != 1 || got.Unproven.OriginalDeliveryPredecessorUnproven != 1 || got.Unproven.FinalizationOrderUnproven != 1 {
		t.Fatalf("unanchored sticky upgrade was accepted: %+v", got)
	}
	rows = evidenceTestRows()
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "committed", "committed", false)
	evidenceTestRecord(&rows, "22", "contract")
	evidenceTestTerminal(&rows, "cost_confirmed", "22")
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "content", "content", false)
	got = evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Status != "indeterminate" || got.Unproven.FinalizationOrderUnproven != 1 {
		t.Fatalf("mixed historical levels falsely proved order: %+v", got)
	}
	rows.Requests[0].LatestFinalizationID = procurementAttemptPtr(rows.Finalizations[0].ID)
	if got = evidenceTestCheck(t, rows); got.Mismatches.FinalizationStickyRows == 0 {
		t.Fatalf("latest lost absorbing content: %+v", got)
	}
}
func TestCheckProcurementEvidenceGraphUsageStrictAndWideMath(t *testing.T) {
	valid := []string{evidenceTestUsage, `{"reasoning_tokens":0,"cached_tokens":0,"total_tokens":12,"output_tokens":2,"input_tokens":10,"status":"confirmed_final"}`, strings.Replace(evidenceTestUsage, "confirmed_final", "missing", 1)}
	for _, raw := range valid {
		if _, ok := procurementEvidenceUsageParse(raw); !ok {
			t.Fatalf("valid Usage rejected: %s", raw)
		}
	}
	for _, raw := range []string{strings.Replace(evidenceTestUsage, `"input_tokens":10`, `"input_tokens":1e1`, 1), strings.Replace(evidenceTestUsage, `"input_tokens":10`, `"input_tokens":10.0`, 1), strings.Replace(evidenceTestUsage, `"input_tokens":10`, `"input_tokens":"10"`, 1), strings.Replace(evidenceTestUsage, `"total_tokens":12`, `"total_tokens":13`, 1), strings.Replace(evidenceTestUsage, `"cached_tokens":0`, `"cached_tokens":11`, 1), strings.Replace(evidenceTestUsage, `"status":"confirmed_final"`, `"status":null`, 1), strings.Replace(evidenceTestUsage, `"status":"confirmed_final"`, `"status":"confirmed_final","sta\u0074us":"confirmed_final"`, 1), evidenceTestUsage + `{}`, strings.Replace(evidenceTestUsage, `"status":"confirmed_final"`, `"status":"\ud800"`, 1), `[]`} {
		if _, ok := procurementEvidenceUsageParse(raw); ok {
			t.Fatalf("malformed Usage accepted: %s", raw)
		}
	}
	u := procurementEvidenceUsage{Status: "confirmed_final", InputTokens: math.MaxInt64, TotalTokens: math.MaxInt64}
	a := ProcurementAttemptRow{CostInputRate: procurementAttemptPtr("9223372036854775807"), CostOutputRate: procurementAttemptPtr("0"), CostBasis: "contract"}
	n, ok := procurementEvidenceAmount(a, u)
	if !ok || n.String() != "85070591730234615847396907784233" {
		t.Fatalf("wide ceiling lost exact precision: %v %v", n, ok)
	}
	if d := procurementEvidenceDigest(procurementEvidenceUsage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 2, TotalTokens: 12}, "done"); d != "609eee13d39c7cb5c517fd20816973d05309cb3ffe0a96bd0da564adc2c2fea9" {
		t.Fatalf("semantic digest ABI changed: %s", d)
	}
}
func TestCheckProcurementEvidenceGraphMissingCostAndDefiniteSourceContradictions(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ProcurementEvidenceGraphRows)
		check  func(ProcurementEvidenceGraphCheck) bool
	}{
		{"missing_cost", func(r *ProcurementEvidenceGraphRows) { r.Outbox = nil }, func(g ProcurementEvidenceGraphCheck) bool { return g.Mismatches.CostRecordedMissingRows > 0 }},
		{"bad_cost_json", func(r *ProcurementEvidenceGraphRows) {
			r.Outbox[0].Payload = `{"attempt_id":"attempt-a","amount":22,"basis":"contract"}`
		}, func(g ProcurementEvidenceGraphCheck) bool { return g.Mismatches.CostRecordedJSONRows == 1 }},
		{"wrong_cost_source", func(r *ProcurementEvidenceGraphRows) {
			r.Outbox[0].Payload = `{"attempt_id":"attempt-a","amount":"23","basis":"contract"}`
		}, func(g ProcurementEvidenceGraphCheck) bool { return g.Mismatches.CostRecordedSourceRows == 1 }},
		{"event_type", func(r *ProcurementEvidenceGraphRows) { r.BudgetEvents[1].Type = "expired_without_permit" }, func(g ProcurementEvidenceGraphCheck) bool { return g.Mismatches.BudgetEventShapeRows > 0 }},
		{"identity_missing", func(r *ProcurementEvidenceGraphRows) { r.Identities = nil }, func(g ProcurementEvidenceGraphCheck) bool { return g.Mismatches.NormalIdentityMissingRows == 1 }},
		{"identity_wrong_digest", func(r *ProcurementEvidenceGraphRows) { r.Identities[0].Digest = strings.Repeat("f", 64) }, func(g ProcurementEvidenceGraphCheck) bool { return g.Mismatches.IdentityDigestRows == 1 }},
		{"latest_missing", func(r *ProcurementEvidenceGraphRows) { r.Requests[0].LatestFinalizationID = nil }, func(g ProcurementEvidenceGraphCheck) bool { return g.Mismatches.LatestFinalizationRows == 1 }},
		{"meter_orphan", func(r *ProcurementEvidenceGraphRows) { r.Metering[0].RequestID = "missing" }, func(g ProcurementEvidenceGraphCheck) bool { return g.Mismatches.MeterIdentityRows == 1 }},
		{"current_wrong", func(r *ProcurementEvidenceGraphRows) { r.Attempts[0].CostAmount = procurementAttemptPtr("23") }, func(g ProcurementEvidenceGraphCheck) bool { return g.Mismatches.CurrentCostRows == 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := evidenceTestRows()
			evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "content", "content", false)
			evidenceTestRecord(&rows, "22", "contract")
			evidenceTestTerminal(&rows, "cost_confirmed", "22")
			tc.mutate(&rows)
			got := evidenceTestCheck(t, rows)
			if got.Status != "mismatch" || !tc.check(got) {
				t.Fatalf("definite contradiction hidden: %+v", got)
			}
		})
	}
}
func TestCheckProcurementEvidenceGraphErrorsZeroAndSharedBudget(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, tc := range []struct {
		ctx  context.Context
		rows ProcurementEvidenceGraphRows
		want error
	}{{nil, ProcurementEvidenceGraphRows{}, ErrProcurementEvidenceGraphRowsInput}, {cancelled, ProcurementEvidenceGraphRows{}, ErrProcurementEvidenceGraphRowsCanceled}, {expired, ProcurementEvidenceGraphRows{}, ErrProcurementEvidenceGraphRowsTimeout}, {context.Background(), ProcurementEvidenceGraphRows{Conflicts: make([]ProcurementEvidenceConflictRow, fundingSourceRowLimit+1)}, ErrProcurementEvidenceGraphRowsResource}, {context.Background(), ProcurementEvidenceGraphRows{Requests: []ProcurementEvidenceRequestRow{{ID: strings.Repeat("x", fundingSourceByteLimit/2+1)}}, Conflicts: []ProcurementEvidenceConflictRow{{UsageJSON: strings.Repeat("y", fundingSourceByteLimit/2+1)}}}, ErrProcurementEvidenceGraphRowsResource}, {context.Background(), ProcurementEvidenceGraphRows{Metering: []ProcurementEvidenceMeterRow{{SchemaVersion: 2}}}, ErrProcurementEvidenceGraphRowsExecution}} {
		got, err := CheckProcurementEvidenceGraph(tc.ctx, tc.rows)
		if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, ProcurementEvidenceGraphCheck{}) {
			t.Fatalf("failure leaked partial result: %+v %v want %v", got, err, tc.want)
		}
	}
}

func evidenceTestSettled() ProcurementEvidenceGraphRows {
	r := evidenceTestRows()
	evidenceTestAppendNormal(&r, evidenceTestUsage, "done", "content", "content", false)
	evidenceTestRecord(&r, "22", "contract")
	evidenceTestTerminal(&r, "cost_confirmed", "22")
	return r
}
func evidenceTestConflict(r *ProcurementEvidenceGraphRows, usage string) {
	event := fmt.Sprintf("50000000-0000-0000-0000-%012d", len(r.Conflicts)+1)
	r.Metering = append(r.Metering, ProcurementEvidenceMeterRow{ID: event, TenantID: "tenant-a", RequestID: "request-a", AttemptID: procurementAttemptPtr("attempt-a"), Source: "provider", SourceEventID: event, SchemaVersion: 1, UsageJSON: usage})
	r.Conflicts = append(r.Conflicts, ProcurementEvidenceConflictRow{ID: "conflict-" + event, EventID: event, UsageJSON: usage})
	r.Identities = append(r.Identities, ProcurementEvidenceIdentityRow{AttemptID: "attempt-a", EventID: event, Digest: evidenceTestHash(`{"Usage":` + usage + `,"Reason":"done"}`)})
}
func TestCheckProcurementEvidenceGraphConflictBirthAndPostDisputeIdentity(t *testing.T) {
	rows := evidenceTestSettled()
	usage := strings.Replace(strings.Replace(evidenceTestUsage, `"input_tokens":10`, `"input_tokens":11`, 1), `"total_tokens":12`, `"total_tokens":13`, 1)
	evidenceTestConflict(&rows, usage)
	event := rows.Metering[1].ID
	rows.BudgetEvents = append(rows.BudgetEvents, ProcurementEvidenceBudgetEventRow{ID: "budget-dispute", EventKey: "conflict:attempt-a:" + event, AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "198", SpentDelta: "0", Type: "evidence_disputed", EvidenceRef: event})
	rows.Reservations[0].State = "disputed"
	rows.Reservations[0].HeldAmount = "198"
	rows.Reservations[0].Anomaly = true
	rows.Reservations[0].Revision = "3"
	rows.Accounts[0].HeldAmount = "198"
	rows.Accounts[0].State = "paused"
	rows.Accounts[0].Revision = "2"
	rows.Attempts[0].CostStatus = "unknown"
	rows.Attempts[0].CostAmount = nil
	got := evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Status != "indeterminate" || got.Unproven.ConflictIncomingUnproven != 1 || got.Unproven.ConflictReasonUnproven != 1 || got.Unproven.AccountPauseOrderingUnproven != 1 {
		t.Fatalf("precise conflict gaps lost: %+v", got)
	}
	// A new semantic observation while disputed adds identity without another event.
	usage = strings.Replace(strings.Replace(evidenceTestUsage, `"input_tokens":10`, `"input_tokens":12`, 1), `"total_tokens":12`, `"total_tokens":14`, 1)
	evidenceTestConflict(&rows, usage)
	got = evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Scanned.BudgetEvents != 3 || got.Unproven.ConflictIncomingUnproven != 2 || got.Unproven.ConflictReasonUnproven != 2 {
		t.Fatalf("post-dispute evidence demanded repeated funding effect: %+v", got)
	}
	// Replayed conflict roots really are separate observations.
	c := rows.Conflicts[0]
	c.ID = "another-conflict-row"
	rows.Conflicts = append(rows.Conflicts, c)
	got = evidenceTestCheck(t, rows)
	if got.Unproven.ConflictIncomingUnproven != 3 {
		t.Fatalf("conflicts were incorrectly semantically deduplicated: %+v", got)
	}
	rows.Finalizations = nil
	rows.Requests[0].LatestFinalizationID = nil
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.MeterBirthRows == 0 {
		t.Fatalf("circular conflict cohort self-proved its birth: %+v", got)
	}
}
func TestCheckProcurementEvidenceGraphCrossAttemptIncomingIsUnproven(t *testing.T) {
	rows := evidenceTestSettled()
	a := rows.Attempts[0]
	a.ID = "attempt-b"
	a.RequestID = "request-b"
	a.CostStatus = "unknown"
	a.CostAmount = nil
	a.DeliveryState = "prepared"
	rows.Attempts = append(rows.Attempts, a)
	request := rows.Requests[0]
	request.ID = "request-b"
	request.LatestFinalizationID = nil
	rows.Requests = append(rows.Requests, request)
	p := rows.Reservations[0]
	p.AttemptID = "attempt-b"
	p.HeldAmount = "220"
	p.SpentAmount = "0"
	p.State = "disputed"
	p.Anomaly = true
	p.Revision = "2"
	rows.Reservations = append(rows.Reservations, p)
	event := rows.Metering[0].ID
	rows.Conflicts = append(rows.Conflicts, ProcurementEvidenceConflictRow{ID: "cross-attempt-conflict", EventID: event, UsageJSON: evidenceTestUsage})
	rows.Identities = append(rows.Identities, ProcurementEvidenceIdentityRow{AttemptID: "attempt-b", EventID: event, Digest: rows.Identities[0].Digest})
	rows.BudgetEvents = append(rows.BudgetEvents, ProcurementEvidenceBudgetEventRow{ID: "reserve-b", EventKey: "reserve:attempt-b", AccountID: "account-a", AttemptID: "attempt-b", HeldDelta: "220", SpentDelta: "0", Type: "reserved"}, ProcurementEvidenceBudgetEventRow{ID: "dispute-b", EventKey: "conflict:attempt-b:" + event, AccountID: "account-a", AttemptID: "attempt-b", HeldDelta: "0", SpentDelta: "0", Type: "evidence_disputed", EvidenceRef: event})
	rows.Accounts[0].HeldAmount = "220"
	rows.Accounts[0].State = "paused"
	rows.Accounts[0].Revision = "2"
	got := evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Unproven.IncomingBindingUnproven != 1 || got.Unproven.ConflictReasonUnproven != 1 {
		t.Fatalf("legal cross-attempt identity was equated with old meter binding: %+v", got)
	}
	rows.Attempts[1].TenantID = "different-tenant"
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.IdentitySourceRows == 0 {
		t.Fatalf("cross-tenant contradiction hidden as missing incoming: %+v", got)
	}
}
func evidenceTestCorrection(rows *ProcurementEvidenceGraphRows, amount string) {
	at := time.Unix(500, 0).UTC()
	id := "60000000-0000-0000-0000-000000000001"
	op := "60000000-0000-0000-0000-000000000002"
	approver := "checker-a"
	rows.Users = append(rows.Users, ProcurementApprovalUserRow{ID: approver})
	p := ProcurementCostProposalRow{ID: id, TenantID: "tenant-a", AttemptID: "attempt-a", Amount: amount, Basis: "invoice", EvidenceRef: "invoice:synthetic", Reason: "formal supplier invoice", Requester: "admin-a", ExpectedRevision: rows.Reservations[0].Revision, ExpectedEvidenceCount: fmt.Sprint(len(rows.Identities)), State: "approved", Approver: &approver, ApprovalOperationID: &op, CreatedAt: at, ApprovedAt: &at}
	rows.Proposals = append(rows.Proposals, p)
	rows.Corrections = append(rows.Corrections, ProcurementCostCorrectionRow{ID: "correction-a", TenantID: p.TenantID, AttemptID: p.AttemptID, OperationID: "procurement:" + op, Amount: amount, Basis: p.Basis, EvidenceRef: p.EvidenceRef, CreatedAt: at})
	rows.Audits = append(rows.Audits, ProcurementAuditRow{ID: "audit-propose", Actor: p.Requester, Action: "procurement.cost_proposed", Target: p.ID, TenantScope: procurementAttemptPtr(p.TenantID), RequestID: "request-a", Diff: procurementJournalJSONString(map[string]string{"attempt_id": p.AttemptID, "amount": p.Amount, "basis": p.Basis, "evidence_ref": p.EvidenceRef})}, ProcurementAuditRow{ID: "audit-approve", Actor: approver, Action: "procurement.cost_approved", Target: p.ID, TenantScope: procurementAttemptPtr(p.TenantID), RequestID: "request-a", Diff: procurementJournalJSONString(map[string]string{"attempt_id": p.AttemptID, "amount": p.Amount, "evidence_ref": p.EvidenceRef})})
	rows.Outbox = append(rows.Outbox, ProcurementOutboxRow{ID: "outbox-corrected", TenantID: p.TenantID, OperationID: "procurement:" + op, Kind: "cost_corrected", SchemaVersion: 1, Payload: procurementJournalJSONString(map[string]string{"attempt_id": p.AttemptID, "amount": p.Amount, "basis": p.Basis})})
	rows.BudgetEvents = append(rows.BudgetEvents, ProcurementEvidenceBudgetEventRow{ID: "budget-correction", EventKey: "correction:" + op, AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "0", SpentDelta: "-7", Type: "cost_corrected", EvidenceRef: p.EvidenceRef})
	rows.Reservations[0].SpentAmount = amount
	rows.Reservations[0].Revision = "3"
	rows.Accounts[0].SpentAmount = amount
	rows.Attempts[0].CostStatus = "invoice"
	rows.Attempts[0].CostAmount = procurementAttemptPtr(amount)
}
func TestCheckProcurementEvidenceGraphInvoiceAndProposalBarrier(t *testing.T) {
	rows := evidenceTestSettled()
	evidenceTestCorrection(&rows, "15")
	got := evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Unproven.ProposalBarrierUnproven != 1 || got.Unproven.CostRecordedEligibilityUnproven != 1 || got.Mismatches.CostRecordedSourceRows != 0 {
		t.Fatalf("invoice replaced current but invalidated old cost evidence: %+v", got)
	}
	rows.Proposals[0].ExpectedEvidenceCount = "2"
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.ProposalBarrierRows != 1 {
		t.Fatalf("impossible future evidence barrier accepted: %+v", got)
	}
	rows.Proposals[0].ExpectedEvidenceCount = "1"
	rows.BudgetEvents[2].SpentDelta = "-6"
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.EventDeltaRows == 0 {
		t.Fatalf("single correction before bucket was not replayed: %+v", got)
	}
}
func TestCheckProcurementEvidenceGraphAccountChainAndAdmission(t *testing.T) {
	rows := evidenceTestRows()
	rows.Commands = rows.Commands[1:]
	rows.Audits = rows.Audits[1:]
	if got := evidenceTestCheck(t, rows); got.Mismatches.AccountCreationRows != 1 {
		t.Fatalf("current account self-proved creation: %+v", got)
	}
	rows = evidenceTestRows()
	rows.Accounts[0].Label = "owner-mutation"
	if got := evidenceTestCheck(t, rows); got.Mismatches.AccountConfigurationRows != 1 {
		t.Fatalf("same revision config contradiction accepted: %+v", got)
	}
	rows = evidenceTestRows()
	rows.Accounts[0].State = "paused"
	rows.Accounts[0].Revision = "2"
	if got := evidenceTestCheck(t, rows); got.Mismatches.AccountConfigurationRows != 1 || got.Unproven.AccountPauseOrderingUnproven != 0 {
		t.Fatalf("pause without true anomaly was invented: %+v", got)
	}
	rows = evidenceTestRows()
	rows.Accounts[0].LimitAmount = "219"
	var snap map[string]any
	_ = json.Unmarshal([]byte(rows.Commands[0].After), &snap)
	snap["limit_amount"] = "219"
	rows.Commands[0].After = procurementCommandJSON(snap)
	if got := evidenceTestCheck(t, rows); got.Mismatches.AccountAdmissionRows != 1 {
		t.Fatalf("deterministically impossible admission became gap: %+v", got)
	}
	rows = evidenceTestRows()
	before := map[string]any{}
	_ = json.Unmarshal([]byte(rows.Commands[0].After), &before)
	before["held_amount"] = "220"
	after := map[string]any{}
	for k, v := range before {
		after[k] = v
	}
	after["revision"] = "2"
	after["label"] = "configured-name"
	command := ProcurementCommandRow{OperationID: procurementCommandOperations[2], Actor: "admin-a", Kind: "account_configure", TargetID: "account-a", InputDigest: strings.Repeat("c", 64), Status: "applied", Before: procurementCommandJSON(before), After: procurementCommandJSON(after)}
	rows.Commands = append(rows.Commands, command)
	rows.Audits = append(rows.Audits, procurementCommandAudit(command, "audit-configure"))
	rows.Accounts[0].Revision = "2"
	rows.Accounts[0].Label = "configured-name"
	got := evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Unproven.AccountAdmissionOrderUnproven != 1 {
		t.Fatalf("historical configure/admission order fabricated: %+v", got)
	}
	rows.Accounts[0].Revision = "4"
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.AccountConfigurationRows == 0 {
		t.Fatalf("multi-revision gap hidden by automatic pause: %+v", got)
	}
}

func TestCheckProcurementEvidenceGraphLegacyCorrectionPreventsLaterCostRecord(t *testing.T) {
	rows := evidenceTestRows()
	rows.Accounts = nil
	rows.Bindings = nil
	rows.Reservations = nil
	rows.BudgetEvents = nil
	rows.Commands = nil
	rows.Audits = nil
	rows.Users = nil
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "content", "content", false)
	rows.Identities = nil
	rows.Attempts[0].DeliveryState = "completed"
	rows.Attempts[0].CostStatus = "invoice"
	rows.Attempts[0].CostAmount = procurementAttemptPtr("15")
	c := ProcurementCostCorrectionRow{ID: "legacy-correction", TenantID: "tenant-a", AttemptID: "attempt-a", OperationID: "procurement:legacy-operation", Amount: "15", Basis: "invoice", EvidenceRef: "invoice:legacy"}
	rows.Corrections = append(rows.Corrections, c)
	rows.Audits = append(rows.Audits, ProcurementAuditRow{ID: "legacy-audit", Actor: "system", Action: "cost.corrected", Target: c.ID, TenantScope: procurementAttemptPtr(c.TenantID), RequestID: "request-a", Diff: procurementJournalJSONString(map[string]string{"amount": c.Amount, "basis": c.Basis})})
	rows.Outbox = append(rows.Outbox, ProcurementOutboxRow{ID: "legacy-outbox", TenantID: c.TenantID, OperationID: c.OperationID, Kind: "cost_corrected", SchemaVersion: 1, Payload: procurementJournalJSONString(map[string]string{"attempt_id": c.AttemptID, "amount": c.Amount, "basis": c.Basis})})
	got := evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Unproven.CostRecordedEligibilityUnproven != 1 || got.Mismatches.CostRecordedMissingRows != 0 {
		t.Fatalf("legal legacy correction prefix incorrectly required cost_recorded: %+v", got)
	}
	// With no possible reset, the current value cannot revert to an older
	// cost_recorded merely because its amount still exists in historical rows.
	evidenceTestRecord(&rows, "22", "contract")
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.CurrentCostRows != 1 {
		t.Fatalf("legacy current cost reverted past a true correction: %+v", got)
	}
	rows.Attempts[0].CostStatus = "invoice"
	rows.Attempts[0].CostAmount = procurementAttemptPtr("15")
	evidenceTestNoMismatch(t, evidenceTestCheck(t, rows))
	// A real collision can reset legacy cost. A later distinct normal row can
	// then emit the only cost_recorded, so the competing sources need a gap.
	conflictUsage := strings.Replace(evidenceTestUsage, "confirmed_final", "provisional", 1)
	rows.Conflicts = append(rows.Conflicts, ProcurementEvidenceConflictRow{ID: "legacy-reset-collision", EventID: rows.Metering[0].ID, UsageJSON: conflictUsage})
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "content", "content", false)
	rows.Identities = nil
	rows.Attempts[0].CostStatus = "contract"
	rows.Attempts[0].CostAmount = procurementAttemptPtr("22")
	got = evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Unproven.CurrentCostOrderUnproven != 1 {
		t.Fatalf("possible legacy reset erased source ordering gap: %+v", got)
	}
	rows.Conflicts = nil
	rows.Corrections = nil
	rows.Audits = nil
	rows.Outbox = nil
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.CostRecordedMissingRows != 1 || got.Mismatches.CurrentCostRows != 1 {
		t.Fatalf("current status alone exempted missing source: %+v", got)
	}
}
func TestCheckProcurementEvidenceGraphBudgetEffectUsesOriginalSemanticIdentity(t *testing.T) {
	rows := evidenceTestSettled()
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "content", "content", false)
	// Moving an effect to a later same-digest observation is impossible: the writer
	// returned at identity conflict before it could create that budget event.
	rows.BudgetEvents[1].EvidenceRef = rows.Metering[1].ID
	got := evidenceTestCheck(t, rows)
	if got.Mismatches.BudgetEventSourceRows != 1 {
		t.Fatalf("budget effect accepted a later semantic replay as original: %+v", got)
	}
}
func TestCheckProcurementEvidenceGraphZeroPriceAndHardTokenAnomaly(t *testing.T) {
	rows := evidenceTestRows()
	rows.CostVersions[0].InputRate = procurementAttemptPtr("0")
	rows.CostVersions[0].OutputRate = procurementAttemptPtr("0")
	rows.Attempts[0].CostInputRate = procurementAttemptPtr("0")
	rows.Attempts[0].CostOutputRate = procurementAttemptPtr("0")
	rows.Reservations[0].UpperAmount = "0"
	rows.Reservations[0].HeldAmount = "0"
	rows.Accounts[0].HeldAmount = "0"
	rows.BudgetEvents[0].HeldDelta = "0"
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "content", "content", false)
	evidenceTestRecord(&rows, "0", "contract")
	evidenceTestTerminal(&rows, "cost_confirmed", "0")
	rows.BudgetEvents[1].HeldDelta = "0"
	if got := evidenceTestCheck(t, rows); !got.Balanced {
		t.Fatalf("zero-price confirmation rejected: %+v", got)
	}
	rows = evidenceTestRows()
	usage := strings.Replace(strings.Replace(evidenceTestUsage, `"input_tokens":10`, `"input_tokens":101`, 1), `"total_tokens":12`, `"total_tokens":103`, 1)
	evidenceTestAppendNormal(&rows, usage, "done", "content", "content", false)
	evidenceTestRecord(&rows, "204", "contract")
	evidenceTestTerminal(&rows, "cost_confirmed", "204")
	rows.Reservations[0].Anomaly = true
	rows.Accounts[0].State = "paused"
	rows.Accounts[0].Revision = "2"
	got := evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Unproven.AccountPauseOrderingUnproven != 1 {
		t.Fatalf("token-only anomaly failed to witness actual pause: %+v", got)
	}
	rows.Reservations[0].Anomaly = false
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.StateEventRows != 1 {
		t.Fatalf("token-only anomaly not checked against frozen upper bounds: %+v", got)
	}
}
func TestCheckProcurementEvidenceGraphNullAttemptAndAllDigestInputVariants(t *testing.T) {
	for _, stage := range []string{"none", "committed", "content", "unknown"} {
		for _, content := range []bool{false, true} {
			rows := evidenceTestRows()
			stored := stage
			if content || stage == "content" {
				stored = "content"
			}
			evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", stored, stage, content)
			evidenceTestRecord(&rows, "22", "contract")
			evidenceTestTerminal(&rows, "cost_confirmed", "22")
			if got := evidenceTestCheck(t, rows); !got.Balanced {
				t.Fatalf("self-normalizing input %s/%t failed: %+v", stage, content, got)
			}
		}
	}
	rows := evidenceTestRows()
	rows.Accounts = nil
	rows.Bindings = nil
	rows.Reservations = nil
	rows.BudgetEvents = nil
	rows.Commands = nil
	rows.Audits = nil
	rows.Users = nil
	rows.Attempts = nil
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "done", "none", "none", false)
	rows.Identities = nil
	rows.Metering[0].AttemptID = nil
	input := `{"ID":"40000000-0000-0000-0000-000000000001","TenantID":"tenant-a","RequestID":"request-a","AttemptID":"","Source":"provider","SourceEventID":"source-1","Usage":` + evidenceTestUsage + `,"ProtocolOutcome":"completed","TransportEndReason":"done","DeliveryStage":"none","FinishReason":"","ContentWritten":false,"ObservedAt":"0001-01-01T00:00:00Z"}`
	rows.Finalizations[0].InputDigest = evidenceTestHash(input)
	if got := evidenceTestCheck(t, rows); !got.Balanced {
		t.Fatalf("customer-only/approved reconciliation evidence required a guessed attempt: %+v", got)
	}
}
func TestCheckProcurementEvidenceGraphDuplicateNewRootsAndBothDirections(t *testing.T) {
	rows := evidenceTestSettled()
	rows.Metering = append(rows.Metering, rows.Metering[0])
	rows.Finalizations = append(rows.Finalizations, rows.Finalizations[0])
	rows.Identities = append(rows.Identities, rows.Identities[0])
	got := evidenceTestCheck(t, rows)
	if got.Mismatches.DuplicateMeterRows != 1 || got.Mismatches.DuplicateMeterSourceRows != 0 || got.Mismatches.DuplicateFinalizationRows != 1 || got.Mismatches.DuplicateIdentityRows != 1 || got.Scanned.Metering != 2 {
		t.Fatalf("duplicates amplified or escaped raw counts: %+v", got)
	}
	rows = evidenceTestSettled()
	rows.Conflicts = []ProcurementEvidenceConflictRow{{ID: "orphan-conflict", EventID: "no-meter", UsageJSON: evidenceTestUsage}}
	rows.Metering = append(rows.Metering, ProcurementEvidenceMeterRow{ID: "orphan-meter", TenantID: "tenant-a", RequestID: "request-a", Source: "source", SourceEventID: "orphan", SchemaVersion: 1, UsageJSON: evidenceTestUsage})
	got = evidenceTestCheck(t, rows)
	if got.Mismatches.ConflictIdentityRows != 1 || got.Mismatches.MeterBirthRows != 1 || got.Unproven.ConflictIncomingUnproven != 1 || got.Status != "mismatch" || got.CoverageComplete {
		t.Fatalf("orphan source suppressed independent conflict gap: %+v", got)
	}
}

func TestCheckProcurementEvidenceGraphComplexEventOrderAndMismatchPriority(t *testing.T) {
	// Formal path: confirm 22 -> approve invoice reduction to 15 -> a new
	// no-consumption normal observation disputes that authoritative positive cost.
	rows := evidenceTestSettled()
	evidenceTestCorrection(&rows, "15")
	evidenceTestAppendNormal(&rows, evidenceTestUsage, "not_sent", "content", "content", true)
	event := rows.Metering[1].ID
	rows.BudgetEvents = append(rows.BudgetEvents, ProcurementEvidenceBudgetEventRow{ID: "budget-dispute-after-invoice", EventKey: "conflict:attempt-a:" + event, AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "205", SpentDelta: "0", Type: "evidence_disputed", EvidenceRef: event})
	rows.Reservations[0].State = "disputed"
	rows.Reservations[0].HeldAmount = "205"
	rows.Reservations[0].Anomaly = true
	rows.Reservations[0].Revision = "4"
	rows.Accounts[0].HeldAmount = "205"
	rows.Accounts[0].State = "paused"
	rows.Accounts[0].Revision = "2"
	rows.Attempts[0].CostStatus = "unknown"
	rows.Attempts[0].CostAmount = nil
	got := evidenceTestCheck(t, rows)
	evidenceTestNoMismatch(t, got)
	if got.Status != "indeterminate" || got.Unproven.ProcurementEventOrderUnproven != 1 || got.Unproven.ConflictIncomingUnproven != 0 {
		t.Fatalf("complex persisted history fabricated an ordering or conflict root: %+v", got)
	}
	// Even without a persisted total ordering, a dispute cannot spend a cent.
	rows.BudgetEvents[3].SpentDelta = "1"
	got = evidenceTestCheck(t, rows)
	if got.Status != "mismatch" || got.Mismatches.EventDeltaRows == 0 || got.Unproven.ProcurementEventOrderUnproven != 1 || got.CoverageComplete {
		t.Fatalf("historical gap concealed an independently certain contradiction: %+v", got)
	}
}

func TestCheckProcurementEvidenceGraphMeterPrimarySurvivesSecondaryExclusion(t *testing.T) {
	rows := evidenceTestSettled()
	// The second row owns this physical PK even though its source key collides
	// with the first row. A third row cannot revive that PK under another source.
	second := rows.Metering[0]
	second.ID = "70000000-0000-0000-0000-000000000001"
	third := second
	third.SourceEventID = "different-source-event"
	rows.Metering = append(rows.Metering, second, third)
	got := evidenceTestCheck(t, rows)
	if got.Scanned.Metering != 3 || got.Mismatches.DuplicateMeterRows != 1 || got.Mismatches.DuplicateMeterSourceRows != 1 || got.Mismatches.MeterBirthRows != 0 {
		t.Fatalf("secondary exclusion forgot primary identity or revived a later row: %+v", got)
	}
}
