package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var procurementCommandOperations = []string{
	"10000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000002",
	"10000000-0000-0000-0000-000000000003", "10000000-0000-0000-0000-000000000004",
}

func procurementCommandJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("invalid procurement test fixture")
	}
	return string(encoded)
}

func procurementCommandAccountSnapshot() map[string]any {
	return map[string]any{"id": "account-a", "label": "synthetic account", "provider": "synthetic provider", "state": "active", "limit_amount": "1000", "held_amount": "0", "spent_amount": "0", "revision": "1", "created_at": "2026-11-01T00:00:00.123456Z"}
}

func procurementCommandBindingSnapshot(row ProcurementAttemptBindingRow, revision string) string {
	return procurementCommandJSON(map[string]any{"id": row.ID, "credential_id": row.CredentialID, "channel_id": row.ChannelID, "account_id": row.AccountID, "operation_id": row.OperationID, "bound_at": row.BoundAt.Format(time.RFC3339Nano), "channel_revision": revision})
}

func procurementCommandObservationSnapshot(row ProcurementObservationRow) string {
	return procurementCommandJSON(map[string]any{"id": row.ID, "account_id": row.AccountID, "operation_id": row.OperationID, "actor": row.Actor, "status": row.Status,
		"amount": row.Amount, "currency": row.Currency, "source": row.Source, "semantic": row.Semantic, "observed_at": row.ObservedAt, "provider_as_of": row.ProviderAsOf, "evidence_ref": row.EvidenceRef, "created_at": row.CreatedAt})
}

func procurementCommandAudit(row ProcurementCommandRow, id string) ProcurementAuditRow {
	return ProcurementAuditRow{ID: id, Actor: row.Actor, Action: "procurement.command." + row.Status, Target: row.OperationID, Diff: procurementCommandJSON(map[string]string{"kind": row.Kind, "status": row.Status})}
}

func validProcurementCommandRows() ProcurementCommandRows {
	account := procurementCommandAccountSnapshot()
	before, after := procurementCommandAccountSnapshot(), procurementCommandAccountSnapshot()
	before["held_amount"], before["spent_amount"], before["revision"] = "33", "44", "7"
	after["held_amount"], after["spent_amount"], after["revision"], after["label"] = "33", "44", "8", "updated name"
	binding := ProcurementAttemptBindingRow{ID: "binding-a", CredentialID: "credential-a", ChannelID: "channel-a", AccountID: "account-a", OperationID: procurementCommandOperations[2], BoundAt: time.Unix(100, 123456000).UTC()}
	providerAsOf := time.Unix(150, 1000).UTC()
	observation := ProcurementObservationRow{ID: "observation-a", AccountID: "account-a", OperationID: procurementCommandOperations[3], Actor: "admin-a", Status: "known", Amount: procurementAttemptPtr("9007199254740993"), Currency: "CNY", Source: "manual", Semantic: "cash_balance", ObservedAt: time.Unix(200, 1000).UTC(), ProviderAsOf: &providerAsOf, EvidenceRef: "invoice:synthetic", CreatedAt: time.Unix(201, 2000).UTC()}
	commands := []ProcurementCommandRow{
		{OperationID: procurementCommandOperations[0], Actor: "admin-a", Kind: "account_create", TargetID: "account-a", InputDigest: strings.Repeat("a", 64), Status: "applied", Before: "null", After: procurementCommandJSON(account)},
		{OperationID: procurementCommandOperations[1], Actor: "admin-a", Kind: "account_configure", TargetID: "account-a", InputDigest: strings.Repeat("b", 64), Status: "applied", Before: procurementCommandJSON(before), After: procurementCommandJSON(after)},
		{OperationID: procurementCommandOperations[2], Actor: "admin-a", Kind: "credential_bind", TargetID: "credential-a", InputDigest: strings.Repeat("c", 64), Status: "applied", Before: "null", After: procurementCommandBindingSnapshot(binding, "3")},
		{OperationID: procurementCommandOperations[3], Actor: "admin-a", Kind: "balance_observe", TargetID: "account-a", InputDigest: strings.Repeat("d", 64), Status: "applied", Before: "null", After: procurementCommandObservationSnapshot(observation)},
	}
	rows := ProcurementCommandRows{Users: []ProcurementApprovalUserRow{{ID: "admin-a"}}, Accounts: []ProcurementBudgetAccountRow{{ID: "account-a", HeldAmount: "999", SpentAmount: "987"}}, Bindings: []ProcurementAttemptBindingRow{binding}, Commands: commands, Observations: []ProcurementObservationRow{observation}}
	for i, command := range commands {
		rows.Audits = append(rows.Audits, procurementCommandAudit(command, []string{"audit-create", "audit-configure", "audit-bind", "audit-observe"}[i]))
	}
	return rows
}

func checkProcurementCommands(t *testing.T, rows ProcurementCommandRows) ProcurementCommandCheck {
	t.Helper()
	got, err := CheckProcurementCommandGraph(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balanced != (got.Mismatches == (ProcurementCommandMismatches{})) {
		t.Fatalf("balanced did not reflect all counters: %+v", got)
	}
	return got
}

func TestCheckProcurementCommandGraphBalancedAndRedacted(t *testing.T) {
	if got := checkProcurementCommands(t, ProcurementCommandRows{}); !got.Balanced || got.Scope != "procurement_command_observation_graph_v1" {
		t.Fatalf("empty scoped result rejected: %+v", got)
	}
	rows := validProcurementCommandRows()
	got := checkProcurementCommands(t, rows)
	if !got.Balanced || got.Scanned != (ProcurementCommandCounts{Users: 1, Accounts: 1, Bindings: 1, Commands: 4, Observations: 1, Audits: 4}) {
		t.Fatalf("valid full graph did not balance: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), "admin-a") || strings.Contains(string(encoded), "invoice:synthetic") || strings.Contains(string(encoded), procurementCommandOperations[0]) {
		t.Fatalf("summary leaked source text: %s %v", encoded, err)
	}
	// The current account is intentionally unrelated to every historical
	// held/spent/configuration snapshot. No historical origin chain is inferred.
	rows.Accounts[0].HeldAmount, rows.Accounts[0].SpentAmount = "100000000000000000000000", "999999999999999999999999"
	if got := checkProcurementCommands(t, rows); !got.Balanced {
		t.Fatalf("historical receipts were tied to a mutable account: %+v", got)
	}
}

func TestCheckProcurementCommandGraphSupportsEveryRejectedShape(t *testing.T) {
	tests := []struct{ kind, reason, before string }{
		{"account_configure", "not_found", "null"},
		{"account_configure", "revision_conflict", procurementCommandJSON(procurementCommandAccountSnapshot())},
		{"credential_bind", "not_found", "null"}, {"credential_bind", "revision_conflict", "null"}, {"credential_bind", "credential_revoked", "null"},
		{"credential_bind", "already_bound", "binding"}, {"balance_observe", "not_found", "null"},
	}
	for _, tc := range tests {
		t.Run(tc.kind+"/"+tc.reason, func(t *testing.T) {
			rows := validProcurementCommandRows()
			row := ProcurementCommandRow{OperationID: "20000000-0000-0000-0000-000000000001", Actor: "admin-a", Kind: tc.kind, TargetID: "account-a", InputDigest: strings.Repeat("f", 64), Status: "rejected", Reason: tc.reason, Before: tc.before, After: "null"}
			if tc.kind == "credential_bind" {
				row.TargetID = "credential-a"
			}
			if row.Before == "binding" {
				// The rejected operation differs from the original binding's
				// immutable operation, and its observed revision can be newer.
				row.Before = procurementCommandBindingSnapshot(rows.Bindings[0], "99999")
			}
			rows.Commands = append(rows.Commands, row)
			rows.Audits = append(rows.Audits, procurementCommandAudit(row, "audit-rejected"))
			if got := checkProcurementCommands(t, rows); !got.Balanced {
				t.Fatalf("legitimate rejected receipt did not balance: %+v", got)
			}
		})
	}
	rows := validProcurementCommandRows()
	rows.Commands[1].After = rows.Commands[1].Before // A no-op configuration does not advance revision.
	if got := checkProcurementCommands(t, rows); !got.Balanced {
		t.Fatalf("no-op configuration required a new revision: %+v", got)
	}
	rows = validProcurementCommandRows()
	rows.Observations[0].Status, rows.Observations[0].Amount, rows.Observations[0].ProviderAsOf = "unknown", nil, nil
	rows.Commands[3].After = procurementCommandObservationSnapshot(rows.Observations[0])
	if got := checkProcurementCommands(t, rows); !got.Balanced {
		t.Fatalf("explicit unknown observation was rejected: %+v", got)
	}
}

func TestCheckProcurementCommandGraphRejectsInvalidJSONWithoutExecutionFailure(t *testing.T) {
	base := procurementCommandJSON(procurementCommandAccountSnapshot())
	tests := []string{
		"", "{", "[]", "true", `"value"`, base + " null",
		strings.Replace(base, `"id":"account-a"`, `"id":"account-a","id":"account-a"`, 1),
		strings.Replace(base, `"id":"account-a"`, `"id":"account-a","\u0069d":"account-a"`, 1),
		strings.Replace(base, `"id":"account-a"`, `"id":"account-a","unknown":"x"`, 1),
		strings.Replace(base, `"id":"account-a",`, "", 1),
		strings.Replace(base, `"label":"synthetic account"`, `"label":null`, 1),
		strings.Replace(base, `"label":"synthetic account"`, `"label":{}`, 1),
		strings.Replace(base, `"label":"synthetic account"`, `"label":[]`, 1),
		strings.Replace(base, `"revision":"1"`, `"revision":1`, 1),
		strings.Replace(base, `"revision":"1"`, `"revision":"01"`, 1),
		strings.Replace(base, `"limit_amount":"1000"`, `"limit_amount":"9223372036854775808"`, 1),
		strings.Replace(base, `"created_at":"2026-11-01T00:00:00.123456Z"`, `"created_at":"2026-11-01T00:00:00.123456789Z"`, 1),
		strings.Replace(base, `"label":"synthetic account"`, `"label":"   "`, 1),
		strings.Replace(base, `"label":"synthetic account"`, `"label":"\ud800"`, 1),
		strings.Replace(base, `"label":"synthetic account"`, `"label":"\udc00"`, 1),
		strings.Replace(base, `"label":"synthetic account"`, `"label":"\ud800\u1234"`, 1),
		strings.Replace(base, `"id":"account-a"`, `"id":"account\na"`, 1),
	}
	for i, raw := range tests {
		rows := validProcurementCommandRows()
		rows.Commands[0].After = raw
		got := checkProcurementCommands(t, rows)
		if got.Mismatches.SnapshotRows != 1 {
			t.Fatalf("bad JSON case %d was not one data mismatch: %+v", i, got.Mismatches)
		}
	}
	// The per-document limit counts original bytes, including whitespace.
	rows := validProcurementCommandRows()
	rows.Commands[0].After = base + strings.Repeat(" ", 16384-len(base))
	if got := checkProcurementCommands(t, rows); !got.Balanced {
		t.Fatalf("exact snapshot byte boundary rejected: %+v", got)
	}
	rows.Commands[0].After += " "
	if got := checkProcurementCommands(t, rows); got.Mismatches.SnapshotRows != 1 {
		t.Fatalf("oversized snapshot became execution error or was accepted: %+v", got)
	}
	for _, label := range []string{`\ud83d\ude00`, `literal\\ud800`} {
		rows := validProcurementCommandRows()
		rows.Commands[0].After = strings.Replace(base, `"label":"synthetic account"`, `"label":"`+label+`"`, 1)
		if got := checkProcurementCommands(t, rows); !got.Balanced {
			t.Fatalf("valid surrogate pair or escaped literal was rejected: %+v", got)
		}
	}
	rows = validProcurementCommandRows()
	rows.Commands[0].Reason, rows.Commands[0].After = "not_found", "invalid-json"
	if got := checkProcurementCommands(t, rows); got.Mismatches.SnapshotRows != 1 || got.Mismatches.CommandShapeRows != 1 {
		t.Fatalf("malformed JSON hid an independent state/reason contradiction: %+v", got.Mismatches)
	}
}

func TestCheckProcurementCommandGraphChecksStatusAndInternalConfigurationChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProcurementCommandRows)
	}{
		{"create rejected", func(r *ProcurementCommandRows) {
			r.Commands[0].Status, r.Commands[0].Reason, r.Commands[0].After = "rejected", "not_found", "null"
		}},
		{"applied reason", func(r *ProcurementCommandRows) { r.Commands[0].Reason = "not_found" }},
		{"create non-null before", func(r *ProcurementCommandRows) { r.Commands[0].Before = r.Commands[0].After }},
		{"create null after", func(r *ProcurementCommandRows) { r.Commands[0].After = "null" }},
		{"create nonzero held", func(r *ProcurementCommandRows) {
			r.Commands[0].After = strings.Replace(r.Commands[0].After, `"held_amount":"0"`, `"held_amount":"1"`, 1)
		}},
		{"configure null before", func(r *ProcurementCommandRows) { r.Commands[1].Before = "null" }},
		{"configure money changed", func(r *ProcurementCommandRows) {
			r.Commands[1].After = strings.Replace(r.Commands[1].After, `"held_amount":"33"`, `"held_amount":"34"`, 1)
		}},
		{"configure revision skipped", func(r *ProcurementCommandRows) {
			r.Commands[1].After = strings.Replace(r.Commands[1].After, `"revision":"8"`, `"revision":"9"`, 1)
		}},
		{"configure birth changed", func(r *ProcurementCommandRows) {
			r.Commands[1].After = strings.Replace(r.Commands[1].After, "2026-11-01", "2026-11-02", 1)
		}},
		{"bind rejected wrong reason", func(r *ProcurementCommandRows) {
			r.Commands[2].Status, r.Commands[2].Reason, r.Commands[2].After = "rejected", "already_bound", "null"
		}},
		{"observe rejected wrong reason", func(r *ProcurementCommandRows) {
			r.Commands[3].Status, r.Commands[3].Reason, r.Commands[3].After = "rejected", "revision_conflict", "null"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := validProcurementCommandRows()
			tc.mutate(&rows)
			if got := checkProcurementCommands(t, rows); got.Mismatches.CommandShapeRows != 1 {
				t.Fatalf("invalid command shape was not counted once: %+v", got.Mismatches)
			}
		})
	}
}

func TestCheckProcurementCommandGraphChecksBothBindingAndObservationDirections(t *testing.T) {
	rows := validProcurementCommandRows()
	rows.Bindings = nil
	if got := checkProcurementCommands(t, rows); got.Mismatches.AppliedBindingRows != 1 || got.Mismatches.BindingCommandRows != 0 {
		t.Fatalf("missing physical binding not found: %+v", got.Mismatches)
	}
	rows = validProcurementCommandRows()
	rows.Commands = append(rows.Commands[:2], rows.Commands[3:]...)
	if got := checkProcurementCommands(t, rows); got.Mismatches.BindingCommandRows != 1 || got.Mismatches.AuditCommandRows != 1 {
		t.Fatalf("orphan binding/command audit not found: %+v", got.Mismatches)
	}
	rows = validProcurementCommandRows()
	rows.Bindings[0].AccountID = "other-account"
	if got := checkProcurementCommands(t, rows); got.Mismatches.AppliedBindingRows != 1 || got.Mismatches.BindingCommandRows != 1 {
		t.Fatalf("binding snapshot mismatch did not cover both directions: %+v", got.Mismatches)
	}
	rows = validProcurementCommandRows()
	rows.Observations = nil
	if got := checkProcurementCommands(t, rows); got.Mismatches.AppliedObservationRows != 1 {
		t.Fatalf("missing observation was not found: %+v", got.Mismatches)
	}
	rows = validProcurementCommandRows()
	rows.Observations[0].EvidenceRef = "other:evidence"
	if got := checkProcurementCommands(t, rows); got.Mismatches.AppliedObservationRows != 1 || got.Mismatches.ObservationCommandRows != 1 {
		t.Fatalf("observation mismatch did not cover both directions: %+v", got.Mismatches)
	}
	rows = validProcurementCommandRows()
	rows.Accounts, rows.Users = nil, nil
	if got := checkProcurementCommands(t, rows); got.Mismatches.CommandActorRows != 4 || got.Mismatches.CommandTargetRows != 2 || got.Mismatches.ObservationCommandRows != 1 {
		t.Fatalf("actor/account references were not checked: %+v", got.Mismatches)
	}
	// Equal instants with a different offset remain the same immutable fact.
	rows = validProcurementCommandRows()
	rows.Commands[2].After = strings.Replace(rows.Commands[2].After, rows.Bindings[0].BoundAt.Format(time.RFC3339Nano), rows.Bindings[0].BoundAt.In(time.FixedZone("test", 8*3600)).Format(time.RFC3339Nano), 1)
	if got := checkProcurementCommands(t, rows); !got.Balanced {
		t.Fatalf("offset formatting changed the immutable binding instant: %+v", got)
	}
}

func TestCheckProcurementCommandGraphChecksCommandAuditIdentityAndExactDiff(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProcurementAuditRow)
	}{
		{"actor", func(a *ProcurementAuditRow) { a.Actor = "other-actor" }},
		{"target", func(a *ProcurementAuditRow) { a.Target = "other-operation" }},
		{"status action", func(a *ProcurementAuditRow) { a.Action = "procurement.command.rejected" }},
		{"empty tenant scope is not null", func(a *ProcurementAuditRow) { a.TenantScope = procurementAttemptPtr("") }},
		{"request", func(a *ProcurementAuditRow) { a.RequestID = "not-empty" }},
		{"missing kind", func(a *ProcurementAuditRow) { a.Diff = `{"status":"applied"}` }},
		{"wrong kind", func(a *ProcurementAuditRow) { a.Diff = `{"kind":"balance_observe","status":"applied"}` }},
		{"unknown field", func(a *ProcurementAuditRow) { a.Diff = `{"kind":"account_create","status":"applied","extra":"x"}` }},
		{"duplicate status", func(a *ProcurementAuditRow) {
			a.Diff = `{"kind":"account_create","status":"applied","status":"applied"}`
		}},
		{"trailing document", func(a *ProcurementAuditRow) { a.Diff += ` {}` }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := validProcurementCommandRows()
			tc.mutate(&rows.Audits[0])
			if got := checkProcurementCommands(t, rows); got.Mismatches.CommandAuditRows != 1 || got.Mismatches.AuditCommandRows != 1 {
				t.Fatalf("audit mismatch did not cover both directions: %+v", got.Mismatches)
			}
		})
	}
	rows := validProcurementCommandRows()
	rows.Audits = rows.Audits[1:]
	if got := checkProcurementCommands(t, rows); got.Mismatches.CommandAuditRows != 1 {
		t.Fatalf("mandatory audit missing was not found: %+v", got)
	}
	rows = validProcurementCommandRows()
	rows.Audits = append(rows.Audits, ProcurementAuditRow{ID: "unrelated-audit", Action: "other-domain", Diff: `[1,true,null,{"different":"shape"}]`})
	if got := checkProcurementCommands(t, rows); !got.Balanced || got.Scanned.Audits != 5 {
		t.Fatalf("unrelated audit was not scanned or was interpreted as command audit: %+v", got)
	}
}

func TestCheckProcurementCommandGraphRetainsFirstRowsForEveryUniqueKey(t *testing.T) {
	rows := validProcurementCommandRows()
	rows.Users = append(rows.Users, rows.Users[0])
	rows.Accounts = append(rows.Accounts, rows.Accounts[0])
	rows.Bindings = append(rows.Bindings, rows.Bindings[0])
	rows.Commands = append(rows.Commands, rows.Commands[0])
	rows.Observations = append(rows.Observations, rows.Observations[0])
	rows.Audits = append(rows.Audits, rows.Audits[0])
	want := ProcurementCommandMismatches{DuplicateUserRows: 1, DuplicateAccountRows: 1, DuplicateBindingRows: 1, DuplicateBindingOperationRows: 1,
		DuplicateCommandRows: 1, DuplicateObservationRows: 1, DuplicateObservationOperationRows: 1, DuplicateAuditRows: 1, DuplicateCommandAuditTargetRows: 1}
	if got := checkProcurementCommands(t, rows); got.Mismatches != want {
		t.Fatalf("duplicate source rows amplified the graph: got %+v want %+v", got.Mismatches, want)
	}
	rows = validProcurementCommandRows()
	secondAudit := rows.Audits[0]
	secondAudit.ID, secondAudit.Action = "conflicting-audit", "procurement.command.rejected"
	rows.Audits = append(rows.Audits, secondAudit)
	if got := checkProcurementCommands(t, rows); got.Mismatches != (ProcurementCommandMismatches{DuplicateCommandAuditTargetRows: 1}) {
		t.Fatalf("audit target uniqueness was incorrectly split by action: %+v", got.Mismatches)
	}
}

func TestCheckProcurementCommandGraphChecksObservationDomainsAndCommandTokens(t *testing.T) {
	for _, amount := range []string{"01", "+1", "-1", "-0", "1e2", "9223372036854775808"} {
		rows := validProcurementCommandRows()
		rows.Observations[0].Amount = procurementAttemptPtr(amount)
		rows.Commands[3].After = procurementCommandObservationSnapshot(rows.Observations[0])
		if got := checkProcurementCommands(t, rows); got.Mismatches.InvalidAmountRows != 1 || got.Mismatches.SnapshotRows != 1 {
			t.Fatalf("invalid exact observation amount was accepted: %+v", got.Mismatches)
		}
	}
	rows := validProcurementCommandRows()
	rows.Observations[0].Status, rows.Observations[0].Currency = "unsupported", "USD"
	rows.Observations[0].Source, rows.Observations[0].Semantic = "bad\nsource", strings.Repeat("s", 101)
	rows.Commands[0].InputDigest, rows.Commands[1].OperationID = strings.Repeat("G", 64), "not-uuid"
	rows.Commands[2].Kind = "not-kind"
	got := checkProcurementCommands(t, rows)
	if got.Mismatches.InvalidTextRows != 3 || got.Mismatches.InvalidAmountRows != 1 || got.Mismatches.InvalidEnumRows != 2 {
		t.Fatalf("source-domain errors were not counted once per row: %+v", got.Mismatches)
	}
	for _, mutate := range []func(*ProcurementObservationRow){
		func(r *ProcurementObservationRow) { r.OperationID = "not-uuid" },
		func(r *ProcurementObservationRow) { r.ObservedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		func(r *ProcurementObservationRow) { r.CreatedAt = time.Unix(1, 1).UTC() },
		func(r *ProcurementObservationRow) {
			r.ProviderAsOf = new(time.Time)
			*r.ProviderAsOf = time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
		},
	} {
		rows := validProcurementCommandRows()
		mutate(&rows.Observations[0])
		if got := checkProcurementCommands(t, rows); got.Mismatches.InvalidTextRows != 1 {
			t.Fatalf("physical observation identity/time shape was not one row mismatch: %+v", got.Mismatches)
		}
	}
}

func TestCheckProcurementCommandGraphResourceAndContextErrorsAreAtomic(t *testing.T) {
	assertError := func(ctx context.Context, rows ProcurementCommandRows, expected error) {
		t.Helper()
		got, err := CheckProcurementCommandGraph(ctx, rows)
		if !errors.Is(err, expected) || got != (ProcurementCommandCheck{}) {
			t.Fatalf("execution error returned a partial summary: %+v %v; want %v", got, err, expected)
		}
	}
	assertError(nil, validProcurementCommandRows(), ErrProcurementCommandRowsInput)
	assertError(context.Background(), ProcurementCommandRows{Users: make([]ProcurementApprovalUserRow, fundingSourceRowLimit+1)}, ErrProcurementCommandRowsResource)
	if got := checkProcurementCommands(t, ProcurementCommandRows{Users: make([]ProcurementApprovalUserRow, fundingSourceRowLimit)}); got.Scanned.Users != fundingSourceRowLimit {
		t.Fatalf("exact row boundary rejected: %+v", got)
	}
	rows := ProcurementCommandRows{Audits: []ProcurementAuditRow{{ID: "audit", Diff: strings.Repeat("x", fundingSourceByteLimit-len("audit"))}}}
	if got := checkProcurementCommands(t, rows); !got.Balanced {
		t.Fatalf("exact full audit text boundary rejected: %+v", got)
	}
	rows.Audits[0].Diff += "x"
	assertError(context.Background(), rows, ErrProcurementCommandRowsResource)
	rows = validProcurementCommandRows()
	rows.Commands[0].After = strings.Repeat("x", fundingSourceByteLimit)
	assertError(context.Background(), rows, ErrProcurementCommandRowsResource)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	assertError(canceled, validProcurementCommandRows(), ErrProcurementCommandRowsCanceled)
	deadline, done := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer done()
	assertError(deadline, validProcurementCommandRows(), ErrProcurementCommandRowsTimeout)
	for _, tc := range []struct{ cause, want error }{{context.Canceled, ErrProcurementCommandRowsCanceled}, {context.DeadlineExceeded, ErrProcurementCommandRowsTimeout}} {
		assertError(&fundingSourceSwitchingContext{switchAfter: 6, err: tc.cause}, ProcurementCommandRows{Users: make([]ProcurementApprovalUserRow, 1024)}, tc.want)
	}
}
