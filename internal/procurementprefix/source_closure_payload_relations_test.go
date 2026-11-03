package procurementprefix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func payloadRelationAt(n int) Time {
	return Time{Time: time.Date(2026, 11, 4, 0, 0, n, 123000, time.UTC), Valid: true}
}

func payloadRelationJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func payloadRelationSnapshot(label, state, limit, held, spent, revision string) string {
	return payloadRelationJSON(map[string]string{"id": "account", "label": label, "provider": "provider", "state": state, "limit_amount": limit, "held_amount": held, "spent_amount": spent, "revision": revision, "created_at": "2026-11-04T00:00:00.000123Z"})
}

func payloadRelationAudit(id, actor, tenant, action, target, request, payload, root, step, kind, source, role string) AuditEventRow {
	a := AuditEventRow{ID: id, Actor: actor, Action: action, Target: target, RequestID: request, SanitizedDiff: payload, CreatedAt: payloadRelationAt(19)}
	if tenant != "" {
		a.TenantScope = sourcePtr(tenant)
	}
	if root != "" {
		a.PPRootID, a.PPRootStep, a.PPSourceKind, a.PPSourceID, a.PPSourceRole, a.PPCreatedXID = sourcePtr(root), sourcePtr(step), sourcePtr(kind), sourcePtr(source), sourcePtr(role), sourcePtr("7")
	}
	return a
}

func payloadRelationOutbox(id, operation, kind, attempt, amount, basis, root, link string) ProjectionOutboxRow {
	return ProjectionOutboxRow{EventID: id, TenantID: "tenant", OperationID: operation, Kind: kind, SchemaVersion: "1", SanitizedPayload: payloadRelationJSON(map[string]string{"attempt_id": attempt, "amount": amount, "basis": basis}), CreatedAt: payloadRelationAt(18), PPRootID: sourcePtr(root), PPRootStep: sourcePtr("3"), PPSourceKind: sourcePtr("cost_outbox"), PPSourceID: sourcePtr(link), PPSourceRole: sourcePtr("cost"), PPCreatedXID: sourcePtr("7")}
}

// These fixtures provide explicit historical snapshots, inputs and output
// amounts. No writer, replay helper, Candidates or new relation helper builds
// the expected values. They deliberately are not complete root/ledger proofs.
func payloadRelationCommandFixture(branch string) Rows {
	s := AccountCommandSourceRow{ID: "command-source", RootID: "command-root", StepNo: "1", StepKind: "account_command", CreatedXID: "7", CreatedAt: payloadRelationAt(8), OperationID: "command-operation", ActorID: "admin", Kind: "account_create", InputLabel: "new", InputProvider: "provider", InputState: "active", InputLimitAmount: "100", TargetID: "account", CommandStatus: "applied", CommandCreatedAt: payloadRelationAt(4), AuditID: "command-audit", JournalID: sourcePtr("command-journal")}
	j := AccountJournalRow{ID: "command-journal", AccountID: "account", Ordinal: "1", RootID: s.RootID, StepNo: "1", StepKind: "account_command", CreatedXID: "7", Kind: "create", SourceKind: "command", SourceID: s.OperationID, CommandSourceID: sourcePtr(s.ID), AfterLabel: "new", AfterProvider: "provider", AfterState: "active", AfterLimitAmount: "100", AfterHeldAmount: "0", AfterSpentAmount: "0", AfterConfigurationRevision: "1", AfterBlockingCount: "0"}
	b, a := "null", payloadRelationSnapshot("new", "active", "100", "0", "0", "1")
	preimage := `{"Kind":"account_create","Target":"","Payload":{"operation_id":"command-operation","label":"new","provider":"provider","state":"active","limit_amount":"100"}}`
	if branch != "create" {
		s.Kind, s.InputTargetID, s.InputExpectedRevision = "account_configure", "account", sourcePtr("3")
		s.InputState, s.InputLimitAmount = "paused", "80"
		j.Ordinal, j.Kind = "4", "configure"
		j.BeforeLabel, j.BeforeProvider, j.BeforeState, j.BeforeLimitAmount, j.BeforeHeldAmount, j.BeforeSpentAmount, j.BeforeConfigurationRevision, j.BeforeBlockingCount = sourcePtr("before"), sourcePtr("provider"), sourcePtr("active"), sourcePtr("100"), sourcePtr("9"), sourcePtr("11"), sourcePtr("3"), sourcePtr("0")
		j.AfterState, j.AfterLimitAmount, j.AfterHeldAmount, j.AfterSpentAmount, j.AfterConfigurationRevision = "paused", "80", "9", "11", "4"
		b, a = payloadRelationSnapshot("before", "active", "100", "9", "11", "3"), payloadRelationSnapshot("new", "paused", "80", "9", "11", "4")
		preimage = `{"Kind":"account_configure","Target":"account","Payload":{"operation_id":"command-operation","expected_revision":"3","label":"new","provider":"provider","state":"paused","limit_amount":"80"}}`
		if branch == "same" {
			s.InputLabel, s.InputState, s.InputLimitAmount = "before", "active", "100"
			j.AfterLabel, j.AfterState, j.AfterLimitAmount, j.AfterConfigurationRevision = "before", "active", "100", "3"
			a = b
			preimage = `{"Kind":"account_configure","Target":"account","Payload":{"operation_id":"command-operation","expected_revision":"3","label":"before","provider":"provider","state":"active","limit_amount":"100"}}`
		}
		if branch == "rejected" {
			s.InputExpectedRevision, s.CommandStatus, s.CommandReason = sourcePtr("2"), "rejected", "revision_conflict"
			j.Kind, j.AfterLabel, j.AfterState, j.AfterLimitAmount, j.AfterConfigurationRevision = "configure_rejected", "before", "active", "100", "3"
			a = "null"
			preimage = `{"Kind":"account_configure","Target":"account","Payload":{"operation_id":"command-operation","expected_revision":"2","label":"new","provider":"provider","state":"paused","limit_amount":"80"}}`
		}
		if branch == "not_found" {
			s.CommandStatus, s.CommandReason, s.JournalID, b, a = "rejected", "not_found", nil, "null", "null"
		}
	}
	sum := sha256.Sum256([]byte(preimage))
	s.InputDigest = hex.EncodeToString(sum[:])
	c := ProcurementCommandRow{OperationID: s.OperationID, Actor: s.ActorID, Kind: s.Kind, TargetID: "account", InputDigest: s.InputDigest, Status: s.CommandStatus, Reason: s.CommandReason, BeforeState: b, AfterState: a, CreatedAt: s.CommandCreatedAt, PPSourceID: sourcePtr(s.ID), PPRootID: sourcePtr(s.RootID), PPRootStep: sourcePtr("1"), PPCreatedXID: sourcePtr("7"), PPAccountJournalID: s.JournalID}
	rows := Rows{OriginalRows: OriginalRows{ProcurementAccounts: []ProcurementAccountRow{{ID: "account", Label: "today", State: "paused", HeldAmount: "800", SpentAmount: "900", Revision: "42", CreatedAt: payloadRelationAt(0)}}, ProcurementCommands: []ProcurementCommandRow{c}, AuditEvents: []AuditEventRow{payloadRelationAudit(s.AuditID, "admin", "", "procurement.command."+s.CommandStatus, s.OperationID, "", payloadRelationJSON(map[string]string{"kind": s.Kind, "status": s.CommandStatus}), s.RootID, "1", "account_command", s.ID, "account_command")}}, PrefixRows: PrefixRows{AccountCommandSources: []AccountCommandSourceRow{s}}}
	if s.JournalID != nil {
		rows.PrefixRows.AccountJournal = []AccountJournalRow{j}
		rows.PrefixRows.AccountOrigins = []AccountOriginRow{{AccountID: "account", CreatedAt: payloadRelationAt(0)}}
	}
	return rows
}

func payloadRelationRun(t *testing.T, rows Rows) *sourceClosureState {
	t.Helper()
	s := sourceTestRows(t, rows)
	if err := s.payloadRelationRows(); err != nil {
		t.Fatal(err)
	}
	return s
}

func payloadRelationClean(t *testing.T, rows Rows) {
	t.Helper()
	if got := payloadRelationRun(t, rows).bad["pointer"]; len(got) != 0 {
		t.Fatalf("unexpected payload relation defects: %v", got)
	}
}

func TestSourcePayloadRelationTrackedCommands(t *testing.T) {
	for _, branch := range []string{"create", "configure", "same", "rejected", "not_found"} {
		t.Run(branch, func(t *testing.T) { payloadRelationClean(t, payloadRelationCommandFixture(branch)) })
	}
	for _, field := range []string{"id", "label", "provider", "state", "limit_amount", "held_amount", "spent_amount", "revision", "created_at"} {
		t.Run("snapshot_"+field, func(t *testing.T) {
			rows := payloadRelationCommandFixture("configure")
			var v map[string]string
			json.Unmarshal([]byte(rows.OriginalRows.ProcurementCommands[0].BeforeState), &v)
			v[field] = map[string]string{"id": "other-account", "label": "changed", "provider": "other-provider", "state": "paused", "limit_amount": "101", "held_amount": "10", "spent_amount": "12", "revision": "4", "created_at": "2026-11-04T00:00:00.000124Z"}[field]
			rows.OriginalRows.ProcurementCommands[0].BeforeState = payloadRelationJSON(v)
			s := payloadRelationRun(t, rows)
			if len(s.bad["pointer"]) != 3 {
				t.Fatalf("source, command and its actual audit must each count once: %v", s.bad["pointer"])
			}
		})
	}
	for _, mutate := range []func(*Rows){
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].Actor = "other" },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].TargetID = "other" },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].InputDigest = strings.Repeat("0", 64) },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].Status = "rejected" },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].Reason = "not_found" },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].CreatedAt = payloadRelationAt(8) },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].PPSourceID = sourcePtr("other") },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].PPRootStep = sourcePtr("2") },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].PPRootID = sourcePtr("other") },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].PPCreatedXID = sourcePtr("8") },
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].PPAccountJournalID = sourcePtr("other") },
		func(r *Rows) { r.PrefixRows.AccountJournal[0].AfterLabel = "wrong" },
		func(r *Rows) { r.PrefixRows.AccountJournal[0].SourceID = "other" },
		func(r *Rows) { r.PrefixRows.AccountJournal[0].CreatedXID = "8" },
		func(r *Rows) { r.PrefixRows.AccountOrigins[0].CreatedAt = payloadRelationAt(1) },
		func(r *Rows) { r.PrefixRows.AccountCommandSources[0].InputLabel = "changed" },
	} {
		rows := payloadRelationCommandFixture("configure")
		mutate(&rows)
		if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
			t.Fatal("command content mutation accepted")
		}
	}
	rows := payloadRelationCommandFixture("create")
	rows.OriginalRows.ProcurementCommands[0].AfterState = strings.Replace(rows.OriginalRows.ProcurementCommands[0].AfterState, "2026-11-04T00:00:00.000123Z", "2026-11-04T08:00:00.000123+08:00", 1)
	payloadRelationClean(t, rows)
}

func payloadRelationLegacyCommands() Rows {
	rows := payloadRelationCommandFixture("create")
	rows.PrefixRows = PrefixRows{}
	c := &rows.OriginalRows.ProcurementCommands[0]
	c.PPSourceID, c.PPRootID, c.PPRootStep, c.PPCreatedXID, c.PPAccountJournalID = nil, nil, nil, nil, nil
	a := &rows.OriginalRows.AuditEvents[0]
	a.PPRootID, a.PPRootStep, a.PPSourceKind, a.PPSourceID, a.PPSourceRole, a.PPCreatedXID = nil, nil, nil, nil, nil, nil
	b := CredentialProcurementBindingRow{ID: "binding", CredentialID: "credential", ChannelID: "channel", AccountID: "account", OperationID: "00000000-0000-0000-0000-000000000011", BoundAt: payloadRelationAt(2)}
	bindJSON := `{"id":"binding","credential_id":"credential","channel_id":"channel","account_id":"account","operation_id":"00000000-0000-0000-0000-000000000011","bound_at":"2026-11-04T08:00:02.000123+08:00","channel_revision":"777"}`
	for _, op := range []string{"00000000-0000-0000-0000-000000000011", "00000000-0000-0000-0000-000000000013"} {
		command := ProcurementCommandRow{OperationID: op, Actor: "admin", Kind: "credential_bind", TargetID: "credential", Status: "applied", BeforeState: "null", AfterState: bindJSON}
		if op == "00000000-0000-0000-0000-000000000013" {
			command.Status, command.Reason, command.BeforeState, command.AfterState = "rejected", "already_bound", bindJSON, "null"
		}
		rows.OriginalRows.ProcurementCommands = append(rows.OriginalRows.ProcurementCommands, command)
		rows.OriginalRows.AuditEvents = append(rows.OriginalRows.AuditEvents, payloadRelationAudit(op+"-audit", "admin", "", "procurement.command."+command.Status, op, "", payloadRelationJSON(map[string]string{"kind": command.Kind, "status": command.Status}), "", "", "", "", ""))
	}
	o := ProcurementBalanceObservationRow{ID: "observation", AccountID: "account", OperationID: "00000000-0000-0000-0000-000000000012", Actor: "admin", Status: "unknown", Currency: "CNY", Source: "manual", Semantic: "reported", ObservedAt: payloadRelationAt(1), EvidenceRef: "invoice:1", CreatedAt: payloadRelationAt(3)}
	obsJSON := `{"id":"observation","account_id":"account","operation_id":"00000000-0000-0000-0000-000000000012","actor":"admin","status":"unknown","amount":null,"currency":"CNY","source":"manual","semantic":"reported","observed_at":"2026-11-04T00:00:01.000123Z","provider_as_of":null,"evidence_ref":"invoice:1","created_at":"2026-11-04T00:00:03.000123Z"}`
	rows.OriginalRows.ProcurementCommands = append(rows.OriginalRows.ProcurementCommands, ProcurementCommandRow{OperationID: "00000000-0000-0000-0000-000000000012", Actor: "admin", Kind: "balance_observe", TargetID: "account", Status: "applied", BeforeState: "null", AfterState: obsJSON})
	rows.OriginalRows.AuditEvents = append(rows.OriginalRows.AuditEvents, payloadRelationAudit("observe-audit", "admin", "", "procurement.command.applied", "00000000-0000-0000-0000-000000000012", "", `{"kind":"balance_observe","status":"applied"}`, "", "", "", "", ""))
	rows.OriginalRows.CredentialProcurementBindings = []CredentialProcurementBindingRow{b}
	rows.OriginalRows.ProcurementBalanceObservations = []ProcurementBalanceObservationRow{o}
	return rows
}

func TestSourcePayloadRelationLegacyBindingObservation(t *testing.T) {
	rows := payloadRelationLegacyCommands()
	payloadRelationClean(t, rows)
	// Current mutable account values and an unprojected current channel revision
	// cannot disprove either old command's historical snapshot.
	rows.OriginalRows.ProcurementAccounts[0].Label, rows.OriginalRows.ProcurementAccounts[0].HeldAmount = "today-again", "999999"
	payloadRelationClean(t, rows)
	for _, i := range []int{1, 3} {
		var snapshot map[string]json.RawMessage
		json.Unmarshal([]byte(rows.OriginalRows.ProcurementCommands[i].AfterState), &snapshot)
		for key := range snapshot {
			bad := payloadRelationLegacyCommands()
			var fields map[string]json.RawMessage
			json.Unmarshal([]byte(bad.OriginalRows.ProcurementCommands[i].AfterState), &fields)
			if key == "channel_revision" {
				fields[key] = json.RawMessage(`"888"`)
				bad.OriginalRows.ProcurementCommands[i].AfterState = payloadRelationJSON(fields)
				payloadRelationClean(t, bad)
				continue
			}
			fields[key] = json.RawMessage(`"other"`)
			bad.OriginalRows.ProcurementCommands[i].AfterState = payloadRelationJSON(fields)
			if len(payloadRelationRun(t, bad).bad["pointer"]) == 0 {
				t.Fatalf("immutable snapshot %d/%s mutation accepted", i, key)
			}
		}
	}
	for _, mutate := range []func(*Rows){
		func(r *Rows) { r.OriginalRows.ProcurementCommands = r.OriginalRows.ProcurementCommands[:1] },
		func(r *Rows) { r.OriginalRows.CredentialProcurementBindings[0].OperationID = "missing" },
		func(r *Rows) { r.OriginalRows.ProcurementBalanceObservations[0].OperationID = "missing" },
		func(r *Rows) { r.OriginalRows.AuditEvents[0].TenantScope = sourcePtr("") },
		func(r *Rows) {
			r.OriginalRows.AuditEvents = append(r.OriginalRows.AuditEvents, r.OriginalRows.AuditEvents[0])
		},
		func(r *Rows) { r.OriginalRows.ProcurementCommands[0].PPSourceID = sourcePtr("missing") },
	} {
		bad := payloadRelationLegacyCommands()
		mutate(&bad)
		if len(payloadRelationRun(t, bad).bad["pointer"]) == 0 {
			t.Fatal("reverse legacy relation mutation accepted")
		}
	}
	for _, kind := range []string{"account_configure", "credential_bind", "balance_observe"} {
		for _, reason := range []string{"not_found", "revision_conflict", "credential_revoked"} {
			if kind != "credential_bind" && reason != "not_found" {
				continue
			}
			c := ProcurementCommandRow{OperationID: "missing", Kind: kind, Actor: "admin", TargetID: "account", Status: "rejected", Reason: reason, BeforeState: "null", AfterState: "null"}
			a := payloadRelationAudit("audit", "admin", "", "procurement.command.rejected", "missing", "", payloadRelationJSON(map[string]string{"kind": kind, "status": "rejected"}), "", "", "", "", "")
			payloadRelationClean(t, Rows{OriginalRows: OriginalRows{ProcurementCommands: []ProcurementCommandRow{c}, AuditEvents: []AuditEventRow{a}}})
		}
	}
}

func payloadRelationCostFixture(kind string) Rows {
	rows := Rows{}
	amount, basis, cause, operation := "7", "invoice", "cause", "operation"
	switch kind {
	case "normal_cost_decision":
		amount = strings.Repeat("9", 100)
		operation = "attempt"
		rows.PrefixRows.NormalCostDecisions = []NormalCostDecisionRow{{ID: cause, RootID: "cost-root", StepNo: "1", StepKind: kind, CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", Decision: "write_final_usage", SelectedAmount: sourcePtr(amount), SelectedBasis: sourcePtr(basis), CostMutationID: sourcePtr("mutation"), CostRecordedOutboxID: sourcePtr("cost-output")}}
	case "approval_consumed":
		operation = "procurement:approval-operation"
		rows.OriginalRows.UpstreamAttempts = []UpstreamAttemptRow{{ID: "attempt", TenantID: "tenant", RequestID: "request"}}
		rows.PrefixRows.ProposalBirths = []ProposalBirthRow{{ID: "birth", RootID: "birth-root", StepNo: "1", StepKind: "proposal_birth", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", ProposalID: "proposal", Requester: "maker", ReservationRevision: "3", MemberCount: "1", AuditID: "proposed-audit"}}
		rows.PrefixRows.ApprovalConsumptions = []ApprovalConsumptionRow{{ID: cause, RootID: "cost-root", StepNo: "1", StepKind: kind, CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", ProposalID: "proposal", ProposalBirthID: "birth", ApprovalOperationID: "approval-operation", Requester: "maker", Approver: "checker", CostMutationID: "mutation", OutboxID: "cost-output", AuditID: "approved-audit"}}
		rows.OriginalRows.ProcurementCostProposals = []ProcurementCostProposalRow{{ID: "proposal", TenantID: "tenant", AttemptID: "attempt", Amount: amount, Basis: basis, EvidenceRef: "invoice:1", Requester: "maker", ExpectedReservationRevision: "3", ExpectedEvidenceCount: "1", State: "approved", Approver: sourcePtr("checker"), ApprovalOperationID: sourcePtr("approval-operation")}}
		rows.OriginalRows.AuditEvents = []AuditEventRow{payloadRelationAudit("proposed-audit", "maker", "tenant", "procurement.cost_proposed", "proposal", "request", `{"attempt_id":"attempt","amount":"7","basis":"invoice","evidence_ref":"invoice:1"}`, "birth-root", "1", "proposal_birth", "birth", "proposal"), payloadRelationAudit("approved-audit", "checker", "tenant", "procurement.cost_approved", "proposal", "request", `{"attempt_id":"attempt","amount":"7","evidence_ref":"invoice:1"}`, "cost-root", "1", "approval_consumed", cause, "approval")}
	case "unmanaged_correction":
		operation = "procurement:looks-managed"
		rows.PrefixRows.UnmanagedCorrectionSources = []UnmanagedCorrectionSourceRow{{ID: cause, RootID: "cost-root", StepNo: "1", StepKind: kind, CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", OperationID: operation, ProviderCorrectionID: "correction", EffectiveAuditActor: "system", Amount: amount, Basis: basis, CostMutationID: "mutation", OutboxID: "cost-output", AuditID: "corrected-audit"}}
		rows.OriginalRows.AuditEvents = []AuditEventRow{payloadRelationAudit("corrected-audit", "system", "tenant", "cost.corrected", "correction", "request", `{"amount":"7","basis":"invoice"}`, "cost-root", "1", kind, cause, kind)}
	}
	rows.PrefixRows.CostMutations = []CostMutationRow{{ID: "mutation", RootID: "cost-root", StepNo: "2", StepKind: "cost_mutation", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", CauseKind: kind, CauseSourceID: cause, AfterStatus: basis, AfterAmount: sourcePtr(amount)}}
	rows.PrefixRows.CostOutboxLinks = []CostOutboxLinkRow{{ID: "link", RootID: "cost-root", StepNo: "3", StepKind: "cost_outbox", CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", CauseKind: kind, CauseSourceID: cause, OutboxID: "cost-output"}}
	outKind := "cost_corrected"
	if kind == "normal_cost_decision" {
		outKind = "cost_recorded"
	}
	rows.OriginalRows.ProjectionOutbox = []ProjectionOutboxRow{payloadRelationOutbox("cost-output", operation, outKind, "attempt", amount, basis, "cost-root", "link")}
	return rows
}

func TestSourcePayloadRelationThreeCostCauses(t *testing.T) {
	for _, kind := range []string{"normal_cost_decision", "approval_consumed", "unmanaged_correction"} {
		t.Run(kind, func(t *testing.T) {
			payloadRelationClean(t, payloadRelationCostFixture(kind))
			for _, mutate := range []func(*Rows){
				func(r *Rows) { r.PrefixRows.CostMutations = nil },
				func(r *Rows) { r.PrefixRows.CostMutations[0].CauseSourceID = "same-amount-other" },
				func(r *Rows) { r.PrefixRows.CostMutations[0].StepNo = "3" },
				func(r *Rows) { r.PrefixRows.CostMutations[0].RootID = "other" },
				func(r *Rows) { r.PrefixRows.CostMutations[0].AfterAmount = sourcePtr("0") },
				func(r *Rows) { r.PrefixRows.CostOutboxLinks[0].CauseKind = "other" },
				func(r *Rows) { r.PrefixRows.CostOutboxLinks[0].AttemptID = "other" },
				func(r *Rows) {
					r.PrefixRows.CostOutboxLinks = append(r.PrefixRows.CostOutboxLinks, r.PrefixRows.CostOutboxLinks[0])
				},
				func(r *Rows) { r.OriginalRows.ProjectionOutbox[0].OperationID = "other" },
				func(r *Rows) { r.OriginalRows.ProjectionOutbox[0].PPSourceID = sourcePtr("cause") },
				func(r *Rows) {
					r.OriginalRows.ProjectionOutbox[0].SanitizedPayload = `{"attempt_id":"other","amount":"7","basis":"invoice"}`
				},
			} {
				bad := payloadRelationCostFixture(kind)
				mutate(&bad)
				if len(payloadRelationRun(t, bad).bad["pointer"]) == 0 {
					t.Fatal("cost output ownership mutation accepted")
				}
			}
		})
	}
	rows := payloadRelationCostFixture("normal_cost_decision")
	prior := rows.PrefixRows.NormalCostDecisions[0]
	prior.ID, prior.Decision, prior.SelectedAmount, prior.SelectedBasis, prior.CostMutationID, prior.CostRecordedOutboxID = "later-skip", "skip_already_known", nil, nil, nil, nil
	rows.PrefixRows.NormalCostDecisions = append(rows.PrefixRows.NormalCostDecisions, prior)
	payloadRelationClean(t, rows)
	rows.PrefixRows.CostOutboxLinks[0].CauseSourceID = "later-skip"
	if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
		t.Fatal("skip claimed old output")
	}
}

func payloadRelationFinishFixture(action, result string, profile bool) Rows {
	s := ReconciliationSourceRow{ID: "reconcile-source", RootID: "reconcile-root", StepNo: "1", StepKind: "reconciliation", CreatedXID: "7", TenantID: "tenant", RequestID: "request", CaseID: "case", Action: action, Actor: "checker", AuditID: "reconcile-audit", CustomerFinishOutcomeID: sourcePtr("outcome"), InputReason: sourcePtr("manual\nreason")}
	var payload, status string
	o := CustomerFinishOutcomeRow{ID: "outcome", RootID: s.RootID, StepNo: s.StepNo, StepKind: s.StepKind, SourceID: s.ID, CreatedXID: "7", TenantID: "tenant", RequestID: "request", ReconciliationSourceID: s.ID, CaseID: "case", ResultKind: result, ResultStatus: "settled", ReservationAmount: "10", ChargedAmount: "7", WaivedAmount: "2", SelectedReason: "charged_usage", SettlementID: sourcePtr("settlement")}
	if action == "propose" {
		s.Actor, s.CustomerFinishOutcomeID = "maker", nil
		s.InputUsageStatus, s.InputInputTokens, s.InputOutputTokens, s.InputTotalTokens, s.InputCachedTokens, s.InputReasoningTokens, s.InputEvidenceRef = sourcePtr("confirmed_final"), sourcePtr("2"), sourcePtr("3"), sourcePtr("5"), sourcePtr("1"), sourcePtr("2"), sourcePtr("invoice:1")
		payload = `{"usage":{"status":"confirmed_final","input_tokens":2,"output_tokens":3,"total_tokens":5,"cached_tokens":1,"reasoning_tokens":2},"evidence_ref":"invoice:1","reason":"manual\nreason"}`
		status = "proposed"
	} else if action == "approve" {
		if result == "pending" {
			o.ResultStatus = "pending_reconciliation"
		}
		payload, status = `{"outcome":"`+o.ResultStatus+`"}`, "approved"
	} else {
		payload, status = `{"reason":"manual\nreason"}`, "released"
	}
	rows := Rows{PrefixRows: PrefixRows{ReconciliationSources: []ReconciliationSourceRow{s}}, OriginalRows: OriginalRows{AuditEvents: []AuditEventRow{payloadRelationAudit(s.AuditID, s.Actor, "tenant", "reconciliation."+status, "case", "request", payload, s.RootID, "1", "reconciliation", s.ID, "reconciliation")}}}
	if action == "propose" {
		return rows
	}
	if result == "new_settlement" {
		o.BillingAuditID, o.BillingOutboxID = sourcePtr("billing-audit"), sourcePtr("billing-output")
		rows.OriginalRows.AuditEvents = append(rows.OriginalRows.AuditEvents, payloadRelationAudit("billing-audit", "system", "tenant", "billing.settled", "settlement", "request", `{"amount":"7","reason":"charged_usage"}`, s.RootID, "1", "reconciliation", s.ID, "settlement"))
		rows.OriginalRows.ProjectionOutbox = []ProjectionOutboxRow{{EventID: "billing-output", TenantID: "tenant", OperationID: "settlement", Kind: "settled", SchemaVersion: "1", CreatedAt: payloadRelationAt(18), SanitizedPayload: `{"amount":"7","released":"3","request_id":"request"}`, PPRootID: sourcePtr(s.RootID), PPRootStep: sourcePtr("1"), PPSourceKind: sourcePtr("reconciliation"), PPSourceID: sourcePtr(s.ID), PPSourceRole: sourcePtr("settlement"), PPCreatedXID: sourcePtr("7")}}
		if profile {
			o.ProfileBlockAuditID, o.ProfileBlockModelID, o.ProfileBlockProfileVersion, o.ProfileBlockDisposition = sourcePtr("profile-audit"), sourcePtr("model"), sourcePtr("profile"), sourcePtr("reused")
			rows.OriginalRows.RequestRecords = []RequestRecordRow{{ID: "request", TenantID: "tenant", ModelID: "model", ProfileVersion: "profile"}}
			rows.OriginalRows.BillingProfileBlocks = []BillingProfileBlockRow{{ModelID: "model", ProfileVersion: "profile", Reason: "usage_exceeded_reservation", RequestID: "old-request"}}
			rows.OriginalRows.AuditEvents = append(rows.OriginalRows.AuditEvents, payloadRelationAudit("profile-audit", "system", "tenant", "billing.profile_blocked", "model", "request", `{"waived":"2"}`, s.RootID, "1", "reconciliation", s.ID, "profile_block"))
		}
	}
	rows.PrefixRows.CustomerFinishOutcomes = []CustomerFinishOutcomeRow{o}
	return rows
}

func TestSourcePayloadRelationReconcileAndFinish(t *testing.T) {
	for _, tc := range []struct{ action, result string }{{"propose", ""}, {"approve", "pending"}, {"approve", "existing_settlement"}, {"release", "existing_settlement"}, {"release", "new_settlement"}, {"approve", "new_settlement"}} {
		payloadRelationClean(t, payloadRelationFinishFixture(tc.action, tc.result, false))
	}
	payloadRelationClean(t, payloadRelationFinishFixture("approve", "new_settlement", true))
	for _, mutate := range []func(*Rows){
		func(r *Rows) { r.PrefixRows.CustomerFinishOutcomes[0].SourceID = "other" },
		func(r *Rows) { r.PrefixRows.CustomerFinishOutcomes[0].CaseID = "other" },
		func(r *Rows) { r.PrefixRows.CustomerFinishOutcomes[0].BillingAuditID = nil },
		func(r *Rows) { r.OriginalRows.AuditEvents[0].SanitizedDiff = `{"outcome":"released"}` },
		func(r *Rows) { r.OriginalRows.AuditEvents[1].Actor = "checker" },
		func(r *Rows) { r.OriginalRows.AuditEvents[1].SanitizedDiff = `{"amount":"8","reason":"charged_usage"}` },
		func(r *Rows) {
			r.OriginalRows.ProjectionOutbox[0].SanitizedPayload = `{"amount":"7","released":"4","request_id":"request"}`
		},
		func(r *Rows) { r.OriginalRows.ProjectionOutbox[0].OperationID = "other" },
		func(r *Rows) { r.OriginalRows.AuditEvents[2].SanitizedDiff = `{"waived":"3"}` },
		func(r *Rows) { r.PrefixRows.CustomerFinishOutcomes[0].ProfileBlockDisposition = sourcePtr("created") },
	} {
		rows := payloadRelationFinishFixture("approve", "new_settlement", true)
		mutate(&rows)
		if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
			t.Fatal("finish output mutation accepted")
		}
	}
	// Older manual P and ordinary billing outputs remain outside a new A's
	// scopes when no new P or settlement is claimed by that A.
	rows := payloadRelationFinishFixture("approve", "existing_settlement", false)
	rows.OriginalRows.AuditEvents = append(rows.OriginalRows.AuditEvents, AuditEventRow{ID: "old-proposed", Action: "reconciliation.proposed", Target: "case"}, AuditEventRow{ID: "ordinary-billing", Action: "billing.settled", Target: "settlement"})
	rows.OriginalRows.ProjectionOutbox = []ProjectionOutboxRow{{EventID: "old-output", OperationID: "settlement", Kind: "settled"}}
	payloadRelationClean(t, rows)
}

func TestSourcePayloadRelationAuditSlotsAndReverse(t *testing.T) {
	for _, fixture := range []func() Rows{func() Rows { return payloadRelationCommandFixture("create") }, func() Rows { return payloadRelationCostFixture("approval_consumed") }, func() Rows { return payloadRelationCostFixture("unmanaged_correction") }, func() Rows { return payloadRelationFinishFixture("propose", "", false) }, func() Rows { return payloadRelationFinishFixture("approve", "new_settlement", true) }} {
		rows := fixture()
		for i := range rows.OriginalRows.AuditEvents {
			for _, field := range []string{"Actor", "TenantScope", "Action", "Target", "RequestID", "PPRootID", "PPRootStep", "PPSourceKind", "PPSourceID", "PPSourceRole", "PPCreatedXID"} {
				bad := fixture()
				v := reflect.ValueOf(&bad.OriginalRows.AuditEvents[i]).Elem().FieldByName(field)
				if v.Kind() == reflect.Pointer {
					v.Set(reflect.ValueOf(sourcePtr("other")))
				} else {
					v.SetString("other")
				}
				if len(payloadRelationRun(t, bad).bad["pointer"]) == 0 {
					t.Fatalf("audit %d/%s mutation accepted", i, field)
				}
			}
			bad := fixture()
			extra := bad.OriginalRows.AuditEvents[i]
			extra.ID += "-extra"
			extra.PPRootID, extra.PPRootStep, extra.PPSourceKind, extra.PPSourceID, extra.PPSourceRole, extra.PPCreatedXID = nil, nil, nil, nil, nil, nil
			bad.OriginalRows.AuditEvents = append(bad.OriginalRows.AuditEvents, extra)
			s := payloadRelationRun(t, bad)
			if _, bad := s.bad["pointer"][tupleKey("audit_events", tupleKey(extra.ID))]; !bad {
				t.Fatalf("additional NULL-pointer audit escaped: %v", s.bad["pointer"])
			}
		}
	}
	rows := payloadRelationCostFixture("unmanaged_correction")
	rows.PrefixRows.UnmanagedCorrectionSources[0].InputActorID = sourcePtr("")
	if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
		t.Fatal("NULL actor conflated with empty TEXT")
	}
	for _, fixture := range []func() Rows{func() Rows { return payloadRelationCostFixture("normal_cost_decision") }, func() Rows { return payloadRelationFinishFixture("approve", "new_settlement", false) }} {
		rows := fixture()
		extra := rows.OriginalRows.ProjectionOutbox[0]
		extra.EventID += "-extra"
		extra.PPRootID, extra.PPRootStep, extra.PPSourceKind, extra.PPSourceID, extra.PPSourceRole, extra.PPCreatedXID = nil, nil, nil, nil, nil, nil
		rows.OriginalRows.ProjectionOutbox = append(rows.OriginalRows.ProjectionOutbox, extra)
		if _, found := payloadRelationRun(t, rows).bad["pointer"][tupleKey("projection_outbox", tupleKey(extra.EventID))]; !found {
			t.Fatal("additional NULL-pointer outbox escaped")
		}
	}
}

func TestSourcePayloadRelationNaturalKeysAndBudget(t *testing.T) {
	rows := payloadRelationCommandFixture("create")
	rows.OriginalRows.AuditEvents[0].Actor, rows.OriginalRows.AuditEvents[0].Target = "bad", "bad"
	s := payloadRelationRun(t, rows)
	before := len(s.bad["pointer"])
	if before != 2 {
		t.Fatalf("one audit plus one source must deduplicate multiple reasons: %v", s.bad["pointer"])
	}
	if err := s.payloadRelationRows(); err != nil || len(s.bad["pointer"]) != before {
		t.Fatalf("repeat double-counted: %v %v", s.bad["pointer"], err)
	}
	rows = payloadRelationLegacyCommands()
	for i, j := 0, len(rows.OriginalRows.ProcurementCommands)-1; i < j; i, j = i+1, j-1 {
		rows.OriginalRows.ProcurementCommands[i], rows.OriginalRows.ProcurementCommands[j] = rows.OriginalRows.ProcurementCommands[j], rows.OriginalRows.ProcurementCommands[i]
	}
	payloadRelationClean(t, rows)
	s = sourceTestRows(t, rows)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.w.b.ctx = ctx
	if err := s.payloadRelationRows(); !errors.Is(err, ErrCanceled) {
		t.Fatalf("cancel lost: %v", err)
	}
	s = sourceTestRows(t, rows)
	s.w.b.deadline = time.Now().Add(-time.Second)
	if err := s.payloadRelationRows(); !errors.Is(err, ErrTimeout) {
		t.Fatalf("deadline lost: %v", err)
	}
	for _, ctx := range []context.Context{ctx, context.Background()} {
		w, _, err := newReplayWork(context.Background(), Rows{})
		if err != nil {
			t.Fatal(err)
		}
		w.b.ctx = ctx
		if ctx.Err() == nil {
			w.b.deadline = time.Now().Add(-time.Second)
		}
		got, err := checkSourceClosure(w, Rows{})
		if err == nil || !reflect.DeepEqual(got, sourceClosureResult{}) {
			t.Fatalf("execution error returned a partial result: %#v %v", got, err)
		}
	}
}

func payloadRelationChangeJSON(t *testing.T, raw, field, replacement string) string {
	t.Helper()
	var v map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	if _, found := v[field]; !found {
		t.Fatalf("fixture has no %s", field)
	}
	v[field] = json.RawMessage(replacement)
	return payloadRelationJSON(v)
}

func TestSourcePayloadRelationTrackedDependencyAndMetadataMutations(t *testing.T) {
	for _, field := range []string{"OperationID", "ActorID", "Kind", "InputDigest", "TargetID", "CommandStatus", "CommandReason", "CommandCreatedAt", "RootID", "StepNo", "StepKind", "CreatedXID", "JournalID"} {
		t.Run("source_"+field, func(t *testing.T) {
			rows := payloadRelationCommandFixture("configure")
			v := reflect.ValueOf(&rows.PrefixRows.AccountCommandSources[0]).Elem().FieldByName(field)
			switch v.Kind() {
			case reflect.Pointer:
				v.Set(reflect.ValueOf(sourcePtr("other")))
			case reflect.Struct:
				v.Set(reflect.ValueOf(payloadRelationAt(5)))
			default:
				v.SetString("other")
			}
			if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
				t.Fatal("source metadata mutation accepted")
			}
		})
	}
	for _, field := range []string{"AccountID", "SourceKind", "SourceID", "CommandSourceID", "RootID", "StepNo", "StepKind", "CreatedXID", "Kind"} {
		t.Run("journal_"+field, func(t *testing.T) {
			rows := payloadRelationCommandFixture("configure")
			v := reflect.ValueOf(&rows.PrefixRows.AccountJournal[0]).Elem().FieldByName(field)
			if v.Kind() == reflect.Pointer {
				v.Set(reflect.ValueOf(sourcePtr("other")))
			} else {
				v.SetString("other")
			}
			if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
				t.Fatal("journal metadata mutation accepted")
			}
		})
	}
	for _, mutate := range []func(*Rows){
		func(r *Rows) { r.PrefixRows.AccountJournal = nil },
		func(r *Rows) {
			r.PrefixRows.AccountJournal = append(r.PrefixRows.AccountJournal, r.PrefixRows.AccountJournal[0])
		},
		func(r *Rows) { r.PrefixRows.AccountOrigins = nil },
		func(r *Rows) {
			r.PrefixRows.AccountOrigins = append(r.PrefixRows.AccountOrigins, r.PrefixRows.AccountOrigins[0])
		},
		func(r *Rows) { r.OriginalRows.ProcurementAccounts = nil },
		func(r *Rows) {
			r.OriginalRows.ProcurementAccounts = append(r.OriginalRows.ProcurementAccounts, r.OriginalRows.ProcurementAccounts[0])
		},
		func(r *Rows) { r.OriginalRows.ProcurementCommands = nil },
		func(r *Rows) {
			r.OriginalRows.ProcurementCommands = append(r.OriginalRows.ProcurementCommands, r.OriginalRows.ProcurementCommands[0])
		},
		func(r *Rows) {
			r.PrefixRows.AccountCommandSources = append(r.PrefixRows.AccountCommandSources, r.PrefixRows.AccountCommandSources[0])
		},
	} {
		rows := payloadRelationCommandFixture("configure")
		mutate(&rows)
		if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
			t.Fatal("missing/ambiguous command dependency selected")
		}
	}
	rows := payloadRelationCommandFixture("not_found")
	rows.PrefixRows.AccountJournal = []AccountJournalRow{{ID: "unexpected", SourceKind: "command", RootID: "command-root"}}
	if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
		t.Fatal("historical not_found accepted a journal claiming its root")
	}
}

func TestSourcePayloadRelationLegacyBranchesAndObservationNulls(t *testing.T) {
	for _, branch := range []string{"create", "configure", "same", "rejected", "not_found"} {
		rows := payloadRelationCommandFixture(branch)
		rows.PrefixRows = PrefixRows{}
		c, a := &rows.OriginalRows.ProcurementCommands[0], &rows.OriginalRows.AuditEvents[0]
		c.PPSourceID, c.PPRootID, c.PPRootStep, c.PPCreatedXID, c.PPAccountJournalID = nil, nil, nil, nil, nil
		a.PPRootID, a.PPRootStep, a.PPSourceKind, a.PPSourceID, a.PPSourceRole, a.PPCreatedXID = nil, nil, nil, nil, nil, nil
		payloadRelationClean(t, rows)
	}
	rows := payloadRelationLegacyCommands()
	for i, reason := range []string{"not_found", "revision_conflict", "credential_revoked"} {
		op := []string{"00000000-0000-0000-0000-000000000020", "00000000-0000-0000-0000-000000000021", "00000000-0000-0000-0000-000000000022"}[i]
		rows.OriginalRows.ProcurementCommands = append(rows.OriginalRows.ProcurementCommands, ProcurementCommandRow{OperationID: op, Actor: "admin", Kind: "credential_bind", TargetID: "credential", Status: "rejected", Reason: reason, BeforeState: "null", AfterState: "null"})
		rows.OriginalRows.AuditEvents = append(rows.OriginalRows.AuditEvents, payloadRelationAudit(op+"-audit", "admin", "", "procurement.command.rejected", op, "", `{"kind":"credential_bind","status":"rejected"}`, "", "", "", "", ""))
	}
	op := "00000000-0000-0000-0000-000000000023"
	rows.OriginalRows.ProcurementCommands = append(rows.OriginalRows.ProcurementCommands, ProcurementCommandRow{OperationID: op, Actor: "admin", Kind: "balance_observe", TargetID: "account", Status: "rejected", Reason: "not_found", BeforeState: "null", AfterState: "null"})
	rows.OriginalRows.AuditEvents = append(rows.OriginalRows.AuditEvents, payloadRelationAudit(op+"-audit", "admin", "", "procurement.command.rejected", op, "", `{"kind":"balance_observe","status":"rejected"}`, "", "", "", "", ""))
	payloadRelationClean(t, rows)
	for _, status := range []string{"unknown", "unsupported", "error", "known"} {
		for _, amount := range []string{"0", "9223372036854775807"} {
			rows := payloadRelationLegacyCommands()
			o := &rows.OriginalRows.ProcurementBalanceObservations[0]
			o.Status, o.ProviderAsOf = status, payloadRelationAt(4)
			c := &rows.OriginalRows.ProcurementCommands[3]
			c.AfterState = payloadRelationChangeJSON(t, c.AfterState, "status", payloadRelationJSON(status))
			c.AfterState = payloadRelationChangeJSON(t, c.AfterState, "provider_as_of", `"2026-11-04T08:00:04.000123+08:00"`)
			if status == "known" {
				o.Amount = sourcePtr(amount)
				c.AfterState = payloadRelationChangeJSON(t, c.AfterState, "amount", payloadRelationJSON(amount))
			}
			payloadRelationClean(t, rows)
			c.AfterState = payloadRelationChangeJSON(t, c.AfterState, "provider_as_of", `"2026-11-04T00:00:04.000124Z"`)
			if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
				t.Fatal("observation's provider microsecond change accepted")
			}
		}
	}
	for _, field := range []string{"amount", "provider_as_of"} {
		for _, raw := range []string{`""`, "0", `"0"`} {
			rows := payloadRelationLegacyCommands()
			c := &rows.OriginalRows.ProcurementCommands[3]
			c.AfterState = payloadRelationChangeJSON(t, c.AfterState, field, raw)
			if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
				t.Fatalf("observation NULL conflated with %s", raw)
			}
		}
	}
}

func TestSourcePayloadRelationAuditAndOutboxContentMutations(t *testing.T) {
	fixtures := []func() Rows{func() Rows { return payloadRelationCommandFixture("create") }, func() Rows { return payloadRelationCostFixture("approval_consumed") }, func() Rows { return payloadRelationCostFixture("unmanaged_correction") }, func() Rows { return payloadRelationFinishFixture("propose", "", false) }, func() Rows { return payloadRelationFinishFixture("approve", "new_settlement", true) }, func() Rows { return payloadRelationFinishFixture("release", "new_settlement", false) }}
	for _, fixture := range fixtures {
		rows := fixture()
		for i, a := range rows.OriginalRows.AuditEvents {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(a.SanitizedDiff), &fields); err != nil {
				t.Fatal(err)
			}
			for key := range fields {
				bad := fixture()
				replacement := `"changed"`
				switch key {
				case "amount", "waived":
					replacement = `"8"`
				case "kind":
					replacement = `"account_configure"`
				case "status":
					replacement = `"rejected"`
				case "basis":
					replacement = `"contract"`
				case "usage":
					replacement = `{"status":"confirmed_final","input_tokens":2,"output_tokens":3,"total_tokens":5,"cached_tokens":0,"reasoning_tokens":2}`
				}
				bad.OriginalRows.AuditEvents[i].SanitizedDiff = payloadRelationChangeJSON(t, a.SanitizedDiff, key, replacement)
				if len(payloadRelationRun(t, bad).bad["pointer"]) == 0 {
					t.Fatalf("well-shaped audit %s content field %s changed undetected", a.Action, key)
				}
			}
		}
	}
	for _, fixture := range []func() Rows{func() Rows { return payloadRelationCostFixture("normal_cost_decision") }, func() Rows { return payloadRelationFinishFixture("approve", "new_settlement", false) }} {
		for _, field := range []string{"EventID", "TenantID", "OperationID", "Kind", "SchemaVersion", "PPRootID", "PPRootStep", "PPSourceKind", "PPSourceID", "PPSourceRole", "PPCreatedXID"} {
			rows := fixture()
			v := reflect.ValueOf(&rows.OriginalRows.ProjectionOutbox[0]).Elem().FieldByName(field)
			if v.Kind() == reflect.Pointer {
				v.Set(reflect.ValueOf(sourcePtr("other")))
			} else {
				v.SetString("other")
			}
			if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
				t.Fatalf("outbox %s mutation accepted", field)
			}
		}
	}
}

func TestSourcePayloadRelationDependencyScopeAndDuplicateCounts(t *testing.T) {
	for _, mutate := range []func(*Rows){
		func(r *Rows) { r.OriginalRows.UpstreamAttempts = nil },
		func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].RequestID = "other" },
		func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].TenantID = "other" },
		func(r *Rows) {
			r.OriginalRows.UpstreamAttempts = append(r.OriginalRows.UpstreamAttempts, r.OriginalRows.UpstreamAttempts[0])
		},
		func(r *Rows) { r.OriginalRows.ProcurementCostProposals = nil },
		func(r *Rows) {
			r.OriginalRows.ProcurementCostProposals = append(r.OriginalRows.ProcurementCostProposals, r.OriginalRows.ProcurementCostProposals[0])
		},
		func(r *Rows) { r.PrefixRows.ProposalBirths[0].RequestID = "other" },
		func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].ProposalID = "other" },
		func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].Approver = "other" },
		func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].ApprovalOperationID = "other" },
	} {
		rows := payloadRelationCostFixture("approval_consumed")
		mutate(&rows)
		if len(payloadRelationRun(t, rows).bad["pointer"]) == 0 {
			t.Fatal("proposal's missing/ambiguous/wrong-scope dependency accepted")
		}
	}
	rows := payloadRelationCostFixture("normal_cost_decision")
	rows.PrefixRows.NormalCostDecisions = append(rows.PrefixRows.NormalCostDecisions, rows.PrefixRows.NormalCostDecisions[0])
	s := payloadRelationRun(t, rows)
	if len(s.bad["pointer"]) != 3 {
		t.Fatalf("duplicate cause must count its source, link and actual outbox once each: %v", s.bad["pointer"])
	}
	rows = payloadRelationCommandFixture("create")
	rows.OriginalRows.AuditEvents = append(rows.OriginalRows.AuditEvents, rows.OriginalRows.AuditEvents[0])
	s = payloadRelationRun(t, rows)
	if len(s.bad["pointer"]) != 2 {
		t.Fatalf("duplicate physical audit must not count twice: %v", s.bad["pointer"])
	}
	// Text identity is scoped to physical table; an audit and outbox can share
	// the same text ID without satisfying each other's absent output slot.
	rows = payloadRelationFinishFixture("approve", "new_settlement", false)
	rows.OriginalRows.ProjectionOutbox[0].EventID = "billing-audit"
	rows.PrefixRows.CustomerFinishOutcomes[0].BillingOutboxID = sourcePtr("billing-audit")
	payloadRelationClean(t, rows)
	rows.OriginalRows.AuditEvents = rows.OriginalRows.AuditEvents[:1]
	s = payloadRelationRun(t, rows)
	if _, found := s.bad["pointer"][tupleKey("pp_customer_finish_outcomes", tupleKey("outcome"))]; !found {
		t.Fatal("same ID in another table satisfied missing billing audit")
	}
}

func TestSourcePayloadRelationFailedClaimStillOwnsActualOutputs(t *testing.T) {
	// A real write source fixes operation+kind even when its link is absent.
	// Every extra original occupying that identity remains attributable.
	rows := payloadRelationCostFixture("normal_cost_decision")
	rows.PrefixRows.CostOutboxLinks = nil
	extra := rows.OriginalRows.ProjectionOutbox[0]
	extra.EventID = "extra-null-output"
	extra.PPRootID, extra.PPRootStep, extra.PPSourceKind, extra.PPSourceID, extra.PPSourceRole, extra.PPCreatedXID = nil, nil, nil, nil, nil, nil
	rows.OriginalRows.ProjectionOutbox = append(rows.OriginalRows.ProjectionOutbox, extra)
	s := payloadRelationRun(t, rows)
	if len(s.bad["pointer"]) != 3 {
		t.Fatalf("missing link must retain its cause, physical output and extra business peer: %v", s.bad["pointer"])
	}
	if _, found := s.bad["pointer"][tupleKey("projection_outbox", tupleKey(extra.EventID))]; !found {
		t.Fatal("known write business slot escaped into legacy")
	}
	// Non-new results cannot claim any new financial or profile output. A
	// wrong declaration still names the real original even with NULL pointers.
	for _, kind := range []string{"existing_settlement", "pending"} {
		rows = payloadRelationFinishFixture("approve", "new_settlement", true)
		rows.PrefixRows.CustomerFinishOutcomes[0].ResultKind = kind
		for i := 1; i < len(rows.OriginalRows.AuditEvents); i++ {
			a := &rows.OriginalRows.AuditEvents[i]
			a.PPRootID, a.PPRootStep, a.PPSourceKind, a.PPSourceID, a.PPSourceRole, a.PPCreatedXID = nil, nil, nil, nil, nil, nil
		}
		o := &rows.OriginalRows.ProjectionOutbox[0]
		o.PPRootID, o.PPRootStep, o.PPSourceKind, o.PPSourceID, o.PPSourceRole, o.PPCreatedXID = nil, nil, nil, nil, nil, nil
		s = payloadRelationRun(t, rows)
		if len(s.bad["pointer"]) != 4 {
			t.Fatalf("%s forbidden outputs must record outcome and all three actual originals: %v", kind, s.bad["pointer"])
		}
	}
	rows = payloadRelationCostFixture("normal_cost_decision")
	extra = rows.OriginalRows.ProjectionOutbox[0]
	extra.EventID, extra.OperationID = "wrong-cause-output", "old-operation"
	extra.PPRootID, extra.PPRootStep, extra.PPSourceKind, extra.PPSourceID, extra.PPSourceRole, extra.PPCreatedXID = nil, nil, nil, nil, nil, nil
	rows.OriginalRows.ProjectionOutbox = append(rows.OriginalRows.ProjectionOutbox, extra)
	rows.PrefixRows.NormalCostDecisions[0].CostRecordedOutboxID = &extra.EventID
	s = payloadRelationRun(t, rows)
	if len(s.bad["pointer"]) != 4 {
		t.Fatalf("cause/link ID disagreement must record both originals as well as cause and link: %v", s.bad["pointer"])
	}
	// Partial tracking never enters the legacy audit path. Conversely a bad
	// historical snapshot cannot invalidate an otherwise exact legacy audit
	// whose expected metadata is independently available from its command.
	rows = payloadRelationLegacyCommands()
	rows.OriginalRows.ProcurementCommands[0].PPSourceID = sourcePtr("missing-new-source")
	s = payloadRelationRun(t, rows)
	if len(s.bad["pointer"]) != 2 {
		t.Fatalf("partly tracked command must record its actual audit too: %v", s.bad["pointer"])
	}
	rows = payloadRelationLegacyCommands()
	rows.OriginalRows.ProcurementCommands[0].AfterState = payloadRelationChangeJSON(t, rows.OriginalRows.ProcurementCommands[0].AfterState, "revision", `"2"`)
	s = payloadRelationRun(t, rows)
	if len(s.bad["pointer"]) != 1 {
		t.Fatalf("bad legacy snapshot must not poison exact independent audit metadata: %v", s.bad["pointer"])
	}
}

func TestSourcePayloadRelationDuplicateOwnerDifferentBusinessIdentities(t *testing.T) {
	rows := payloadRelationCostFixture("normal_cost_decision")
	cause := rows.PrefixRows.NormalCostDecisions[0]
	cause.ID, cause.RootID, cause.AttemptID, cause.CostMutationID, cause.CostRecordedOutboxID = "cause-two", "cost-root-two", "attempt-two", sourcePtr("mutation-two"), sourcePtr("output-two")
	mutation := rows.PrefixRows.CostMutations[0]
	mutation.ID, mutation.RootID, mutation.AttemptID, mutation.CauseSourceID = "mutation-two", "cost-root-two", "attempt-two", "cause-two"
	link := rows.PrefixRows.CostOutboxLinks[0]
	link.ID, link.RootID, link.AttemptID, link.CauseSourceID, link.OutboxID = "link-two", "cost-root-two", "attempt-two", "cause-two", "output-two"
	rows.PrefixRows.NormalCostDecisions = append(rows.PrefixRows.NormalCostDecisions, cause)
	rows.PrefixRows.CostMutations = append(rows.PrefixRows.CostMutations, mutation)
	rows.PrefixRows.CostOutboxLinks = append(rows.PrefixRows.CostOutboxLinks, link)
	rows.OriginalRows.ProjectionOutbox = append(rows.OriginalRows.ProjectionOutbox, payloadRelationOutbox("output-two", "attempt-two", "cost_recorded", "attempt-two", strings.Repeat("9", 100), "invoice", "cost-root-two", "link-two"))
	payloadRelationClean(t, rows)
	rows.PrefixRows.CostOutboxLinks[1].ID = "link"
	rows.OriginalRows.ProjectionOutbox[1].PPSourceID = sourcePtr("link")
	s := payloadRelationRun(t, rows)
	if len(s.bad["pointer"]) != 5 {
		t.Fatalf("same link PK with different causes must count both causes/outputs and one link: %v", s.bad["pointer"])
	}
	rows = payloadRelationCommandFixture("create")
	source := rows.PrefixRows.AccountCommandSources[0]
	source.ID, source.RootID, source.OperationID, source.TargetID, source.AuditID, source.JournalID = "command-source-two", "command-root-two", "command-operation-two", "account-two", "command-audit-two", sourcePtr("journal-two")
	preimage := `{"Kind":"account_create","Target":"","Payload":{"operation_id":"command-operation-two","label":"new","provider":"provider","state":"active","limit_amount":"100"}}`
	sum := sha256.Sum256([]byte(preimage))
	source.InputDigest = hex.EncodeToString(sum[:])
	command := rows.OriginalRows.ProcurementCommands[0]
	command.OperationID, command.TargetID, command.InputDigest, command.PPRootID, command.PPAccountJournalID = source.OperationID, source.TargetID, source.InputDigest, &source.RootID, source.JournalID
	command.PPSourceID = sourcePtr("command-source-two")
	command.AfterState = payloadRelationChangeJSON(t, command.AfterState, "id", `"account-two"`)
	journal := rows.PrefixRows.AccountJournal[0]
	journal.ID, journal.RootID, journal.AccountID, journal.SourceID = "journal-two", source.RootID, source.TargetID, source.OperationID
	journal.CommandSourceID = sourcePtr("command-source-two")
	origin := rows.PrefixRows.AccountOrigins[0]
	origin.AccountID = "account-two"
	account := rows.OriginalRows.ProcurementAccounts[0]
	account.ID = "account-two"
	rows.PrefixRows.AccountCommandSources = append(rows.PrefixRows.AccountCommandSources, source)
	rows.PrefixRows.AccountJournal = append(rows.PrefixRows.AccountJournal, journal)
	rows.PrefixRows.AccountOrigins = append(rows.PrefixRows.AccountOrigins, origin)
	rows.OriginalRows.ProcurementCommands = append(rows.OriginalRows.ProcurementCommands, command)
	rows.OriginalRows.ProcurementAccounts = append(rows.OriginalRows.ProcurementAccounts, account)
	rows.OriginalRows.AuditEvents = append(rows.OriginalRows.AuditEvents, payloadRelationAudit("command-audit-two", "admin", "", "procurement.command.applied", "command-operation-two", "", `{"kind":"account_create","status":"applied"}`, "command-root-two", "1", "account_command", "command-source-two", "account_command"))
	payloadRelationClean(t, rows)
	rows.PrefixRows.AccountCommandSources[1].ID = "command-source"
	rows.PrefixRows.AccountJournal[1].CommandSourceID = sourcePtr("command-source")
	rows.OriginalRows.ProcurementCommands[1].PPSourceID = sourcePtr("command-source")
	rows.OriginalRows.AuditEvents[1].PPSourceID = sourcePtr("command-source")
	s = payloadRelationRun(t, rows)
	if len(s.bad["pointer"]) != 5 {
		t.Fatalf("same source PK with different operations must count both commands/audits and one source: %v", s.bad["pointer"])
	}
}

func TestSourcePayloadRelationMidCancellationLargePayloadAndResources(t *testing.T) {
	if err := (*sourceClosureState)(nil).payloadRelationRows(); !errors.Is(err, ErrInput) {
		t.Fatal("nil state accepted")
	}
	rows := payloadRelationCommandFixture("create")
	a := &rows.OriginalRows.AuditEvents[0]
	a.SanitizedDiff = strings.Repeat(" ", int(fieldLimit)-len(a.SanitizedDiff)) + a.SanitizedDiff
	payloadRelationClean(t, rows)
	for _, checks := range []int{1, 3, 8, 12} {
		s := sourceTestRows(t, rows)
		s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: checks}
		s.w.b.steps = 255
		if err := s.payloadRelationRows(); !errors.Is(err, ErrCanceled) {
			t.Fatalf("mid-payload cancellation %d lost: %v", checks, err)
		}
	}
	// The same shared absolute deadline also governs reverse-set scans. A
	// first error must end bad() rather than iterate the rest of a huge set.
	s := sourceTestRows(t, rows)
	p := &sourcePayloadRelations{s: s}
	s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 1}
	s.w.b.steps = 255
	p.bad(sourcePayloadRef{"audit_events", s.tables["audit_events"][0]}, "audit_events", make([]sourceRawRow, 100000))
	if !errors.Is(p.err, ErrCanceled) || s.w.b.steps != 256 {
		t.Fatalf("failed budget did not stop large peer loop: %v steps=%d", p.err, s.w.b.steps)
	}
	rows.OriginalRows.AuditEvents[0].SanitizedDiff = " " + rows.OriginalRows.AuditEvents[0].SanitizedDiff
	w := &replayWork{b: budget{ctx: context.Background()}}
	got, err := checkSourceClosure(w, rows)
	if !errors.Is(err, ErrResource) || !reflect.DeepEqual(got, sourceClosureResult{}) {
		t.Fatalf("oversized relation input exposed result: %#v %v", got, err)
	}
}
