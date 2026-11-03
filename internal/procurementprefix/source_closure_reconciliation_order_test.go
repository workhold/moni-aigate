package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sourceManualUsageJSON = `{"status":"confirmed_final","input_tokens":3,"output_tokens":5,"total_tokens":8,"cached_tokens":1,"reasoning_tokens":2}`

// Explicit snapshots and expected transfer values come from the Go Reconcile
// branches. This fixture never calls a writer or an order/replay helper.
func sourceManualFixture(actions, approvalKind string) Rows {
	p := sourcePtr
	base := ReconciliationSourceRow{StepNo: "1", StepKind: "reconciliation", TenantID: "tenant", RequestID: "request", CaseID: "case", ActorStatus: "active", BeforeCaseReason: "delivery_unknown", CaseCreatedAt: ffTime(1), BeforeReservationStatus: "pending_reconciliation", ReservationDeadline: ffTime(100)}
	proposal := base
	proposal.ID, proposal.RootID, proposal.CreatedXID = "manual-p", "root-p", "90"
	proposal.Action, proposal.Actor, proposal.ActorRole = "propose", "maker", "finance_operator"
	proposal.BeforeCaseState, proposal.AfterCaseState = "open", "proposed"
	proposal.AfterRequester = p("maker")
	proposal.InputUsageStatus, proposal.InputInputTokens, proposal.InputOutputTokens = p("confirmed_final"), p("3"), p("5")
	proposal.InputTotalTokens, proposal.InputCachedTokens, proposal.InputReasoningTokens = p("8"), p("1"), p("2")
	proposal.InputEvidenceRef, proposal.InputReason = p("invoice:1"), p("checked")
	approval := base
	approval.ID, approval.RootID, approval.CreatedXID = "manual-a", "root-a", "2"
	approval.Action, approval.Actor, approval.ActorRole = "approve", "checker", "finance_approver"
	approval.BeforeCaseState, approval.AfterCaseState = "proposed", "approved"
	approval.BeforeRequester, approval.AfterRequester, approval.AfterApprover = p("maker"), p("maker"), p("checker")
	approval.RequesterRole, approval.RequesterStatus = p("finance_operator"), p("active")
	approval.CaseUsageStatus, approval.CaseInputTokens, approval.CaseOutputTokens = p("confirmed_final"), p("3"), p("5")
	approval.CaseTotalTokens, approval.CaseCachedTokens, approval.CaseReasoningTokens = p("8"), p("1"), p("2")
	approval.CaseEvidenceRef, approval.CaseProposalReason = p("invoice:1"), p("checked")
	approval.CustomerFinishOutcomeID = p("approval-outcome")
	release := base
	release.ID, release.RootID, release.CreatedXID = "manual-r", "root-r", "1"
	release.Action, release.Actor, release.ActorRole = "release", "maker", "finance_approver"
	release.BeforeCaseState, release.AfterCaseState = "open", "closed"
	release.InputReason = p("release")
	var rows Rows
	c := ReconciliationCaseRow{ID: "case", TenantID: "tenant", RequestID: "request", Reason: "arbitrary_automatic_reason", CreatedAt: ffTime(1)}
	if strings.Contains(actions, "P") {
		rows.PrefixRows.ReconciliationSources = append(rows.PrefixRows.ReconciliationSources, proposal)
	}
	if strings.Contains(actions, "A") {
		rows.PrefixRows.ReconciliationSources = append(rows.PrefixRows.ReconciliationSources, approval)
		status := "pending_reconciliation"
		if approvalKind != "pending" {
			status = "settled"
		}
		rows.PrefixRows.CustomerFinishOutcomes = []CustomerFinishOutcomeRow{{ID: "approval-outcome", RootID: "root-a", StepNo: "1", StepKind: "reconciliation", SourceID: "manual-a", ReconciliationSourceID: "manual-a", CreatedXID: "2", TenantID: "tenant", RequestID: "request", CaseID: "case", CustomerReservationID: "reservation", ReservationAmount: "10", ReservationDeadline: ffTime(100), BeforeKeyHeld: "10", BeforeKeySpent: "0", AfterKeyHeld: "10", AfterKeySpent: "0", RawAmount: "0", ChargedAmount: "0", WaivedAmount: "0", ResultKind: approvalKind, ResultStatus: status, AfterCaseState: "approved", AfterReservationStatus: status}}
	}
	if strings.Contains(actions, "P") || strings.Contains(actions, "A") {
		release.BeforeCaseState = "proposed"
		release.BeforeRequester, release.AfterRequester = p("maker"), p("maker")
		release.CaseUsageStatus, release.CaseInputTokens, release.CaseOutputTokens = p("confirmed_final"), p("3"), p("5")
		release.CaseTotalTokens, release.CaseCachedTokens, release.CaseReasoningTokens = p("8"), p("1"), p("2")
		release.CaseEvidenceRef, release.CaseProposalReason = p("invoice:1"), p("checked")
		c.ProposedUsage, c.EvidenceRef, c.ProposalReason, c.Requester = p(sourceManualUsageJSON), p("invoice:1"), p("checked"), p("maker")
	}
	last := proposal
	c.State = "proposed"
	if strings.Contains(actions, "A") {
		last = approval
		c.State, c.Approver = "approved", p("checker")
		release.BeforeCaseState, release.AfterCaseState = "approved", "approved"
		release.BeforeApprover, release.AfterApprover = p("checker"), p("checker")
	}
	if strings.Contains(actions, "R") {
		rows.PrefixRows.ReconciliationSources = append(rows.PrefixRows.ReconciliationSources, release)
		last = release
		c.State = release.AfterCaseState
	}
	c.PPLastRootID, c.PPLastRootStep = p(last.RootID), p("1")
	c.PPLastSourceKind, c.PPLastSourceID, c.PPLastSourceRole, c.PPLastCreatedXID = p("reconciliation"), p(last.ID), p("reconciliation"), p(last.CreatedXID)
	rows.OriginalRows.ReconciliationCases = []ReconciliationCaseRow{c}
	return rows
}

func sourceManualRun(t *testing.T, rows Rows) (*sourceClosureState, sourceReconciliationOrderResult) {
	t.Helper()
	s := sourceTestRows(t, rows)
	result, err := s.reconciliationOrder()
	if err != nil {
		t.Fatal(err)
	}
	return s, result
}
func sourceManualClean(t *testing.T, rows Rows, gaps int) {
	t.Helper()
	s, result := sourceManualRun(t, rows)
	if len(s.bad["pointer"])+len(s.bad["scalar"]) != 0 {
		t.Fatalf("manual order rejected: pointer=%v scalar=%v", s.bad["pointer"], s.bad["scalar"])
	}
	if len(result.LegacyCases) != gaps {
		t.Fatalf("legacy cases=%v, want %d", result.LegacyCases, gaps)
	}
}

func TestSourceReconciliationManualOrderLanguage(t *testing.T) {
	for _, actions := range []string{"P", "PA", "PR", "PAR", "R"} {
		t.Run(actions, func(t *testing.T) { sourceManualClean(t, sourceManualFixture(actions, "pending"), 0) })
	}
	t.Run("automatic_pending_reason_and_finish_close_after_P", func(t *testing.T) {
		rows := sourceManualFixture("P", "")
		rows.OriginalRows.ReconciliationCases[0].State = "closed"
		rows.OriginalRows.ReconciliationCases[0].Reason = "metering_conflict"
		sourceManualClean(t, rows, 0)
	})
	t.Run("approved_survives_automatic_finish", func(t *testing.T) {
		rows := sourceManualFixture("PA", "new_settlement")
		rows.OriginalRows.ReconciliationCases[0].Reason = "usage_unconfirmed"
		sourceManualClean(t, rows, 0)
		rows.OriginalRows.ReconciliationCases[0].State = "closed"
		s, _ := sourceManualRun(t, rows)
		if len(s.bad["pointer"]) != 1 {
			t.Fatal("ordinary finish incorrectly permitted closing an approved case")
		}
	})
	t.Run("physical_order_UUID_and_XID_do_not_order_actions", func(t *testing.T) {
		rows := sourceManualFixture("PAR", "pending")
		rows.PrefixRows.ReconciliationSources[0], rows.PrefixRows.ReconciliationSources[2] = rows.PrefixRows.ReconciliationSources[2], rows.PrefixRows.ReconciliationSources[0]
		sourceManualClean(t, rows, 0) // P xid90, A xid2, R xid1, raw array reversed.
	})
	t.Run("no_release_double_approval_requirement", func(t *testing.T) {
		rows := sourceManualFixture("PR", "")
		// Release actor is the existing maker, which the original release path permits.
		sourceManualClean(t, rows, 0)
	})
}

func TestSourceReconciliationManualTransfersAndLastPointer(t *testing.T) {
	mutations := []struct {
		name              string
		change            func(*Rows)
		pointers, scalars int
	}{
		{"consume_wrong_maker", func(r *Rows) {
			r.PrefixRows.ReconciliationSources[1].BeforeRequester = sourcePtr("other")
			r.PrefixRows.ReconciliationSources[1].AfterRequester = sourcePtr("other")
		}, 2, 0},
		{"consume_wrong_usage", func(r *Rows) { r.PrefixRows.ReconciliationSources[1].CaseInputTokens = sourcePtr("4") }, 2, 0},
		{"consume_wrong_evidence", func(r *Rows) { r.PrefixRows.ReconciliationSources[1].CaseEvidenceRef = sourcePtr("invoice:2") }, 2, 0},
		{"consume_wrong_proposal_reason", func(r *Rows) { r.PrefixRows.ReconciliationSources[1].CaseProposalReason = sourcePtr("different") }, 2, 0},
		{"release_wrong_maker", func(r *Rows) {
			r.PrefixRows.ReconciliationSources[2].BeforeRequester = sourcePtr("other")
			r.PrefixRows.ReconciliationSources[2].AfterRequester = sourcePtr("other")
		}, 2, 0},
		{"release_wrong_approver", func(r *Rows) {
			r.PrefixRows.ReconciliationSources[2].BeforeApprover = sourcePtr("other")
			r.PrefixRows.ReconciliationSources[2].AfterApprover = sourcePtr("other")
		}, 2, 0},
		{"release_wrong_proposal", func(r *Rows) { r.PrefixRows.ReconciliationSources[2].CaseOutputTokens = sourcePtr("4") }, 1, 0},
		{"release_before_approval_state", func(r *Rows) {
			r.PrefixRows.ReconciliationSources[2].BeforeCaseState = "proposed"
			r.PrefixRows.ReconciliationSources[2].AfterCaseState = "closed"
		}, 2, 0},
		{"current_points_to_P_not_R", func(r *Rows) {
			c := &r.OriginalRows.ReconciliationCases[0]
			c.PPLastRootID = sourcePtr("root-p")
			c.PPLastSourceID = sourcePtr("manual-p")
			c.PPLastCreatedXID = sourcePtr("90")
		}, 1, 0},
		{"current_partial_pointer", func(r *Rows) { r.OriginalRows.ReconciliationCases[0].PPLastSourceRole = nil }, 1, 0},
		{"current_proposal_changed", func(r *Rows) {
			r.OriginalRows.ReconciliationCases[0].ProposedUsage = sourcePtr(`{"status":"confirmed_final","input_tokens":4,"output_tokens":5,"total_tokens":9,"cached_tokens":1,"reasoning_tokens":2}`)
		}, 1, 0},
		{"current_maker_changed", func(r *Rows) { r.OriginalRows.ReconciliationCases[0].Requester = sourcePtr("other") }, 1, 0},
		{"source_wrong_original_identity", func(r *Rows) { r.PrefixRows.ReconciliationSources[1].TenantID = "wrong" }, 1, 0},
		{"self_approval", func(r *Rows) { r.PrefixRows.ReconciliationSources[1].Actor = "maker" }, 0, 1},
		{"inactive_role", func(r *Rows) { r.PrefixRows.ReconciliationSources[1].RequesterStatus = sourcePtr("disabled") }, 0, 1},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			rows := sourceManualFixture("PAR", "pending")
			mutation.change(&rows)
			s, _ := sourceManualRun(t, rows)
			if len(s.bad["pointer"]) != mutation.pointers || len(s.bad["scalar"]) != mutation.scalars {
				t.Fatalf("pointer=%v scalar=%v want %d/%d", s.bad["pointer"], s.bad["scalar"], mutation.pointers, mutation.scalars)
			}
		})
	}
	for _, kind := range []string{"new_settlement", "existing_settlement"} {
		rows := sourceManualFixture("PAR", kind)
		s, _ := sourceManualRun(t, rows)
		if len(s.bad["pointer"]) != 1 {
			t.Fatalf("new release after approval %s accepted", kind)
		}
	}
}

func TestSourceReconciliationManualLegacyCases(t *testing.T) {
	rows := sourceManualFixture("A", "pending")
	sourceManualClean(t, rows, 1)
	for _, state := range []string{"proposed", "approved", "closed"} {
		t.Run(state, func(t *testing.T) {
			rows := sourceManualFixture("R", "")
			r := &rows.PrefixRows.ReconciliationSources[0]
			r.BeforeCaseState = state
			c := &rows.OriginalRows.ReconciliationCases[0]
			if state == "approved" {
				r.AfterCaseState = "approved"
				c.State = "approved"
			}
			sourceManualClean(t, rows, 1)
		})
	}
	rows = Rows{OriginalRows: OriginalRows{ReconciliationCases: []ReconciliationCaseRow{{ID: "old", State: "closed", CreatedAt: ffTime(1)}}}}
	_, result := sourceManualRun(t, rows)
	if _, found := result.LegacyCases[tupleKey("old")]; !found || len(result.LegacyCases) != 1 {
		t.Fatal("all-NULL old case lost natural-key gap")
	}
	rows = sourceManualFixture("PR", "")
	r := &rows.PrefixRows.ReconciliationSources[1]
	r.BeforeCaseState, r.AfterCaseState = "approved", "approved"
	rows.OriginalRows.ReconciliationCases[0].State = "approved"
	s, result := sourceManualRun(t, rows)
	if len(s.bad["pointer"]) != 1 || len(result.LegacyCases) != 0 {
		t.Fatal("missing new A between tracked P and R was reclassified legacy")
	}
	for _, action := range []string{"R", "A"} {
		rows = sourceManualFixture(action, "pending")
		rows.OriginalRows.ReconciliationCases = nil
		s, result = sourceManualRun(t, rows)
		if len(s.bad["pointer"]) != 1 || len(result.LegacyCases) != 0 {
			t.Fatal("missing original case became a legacy gap")
		}
	}
	rows = sourceManualFixture("R", "")
	rows.PrefixRows.ReconciliationSources[0].BeforeRequester = sourcePtr("old-maker")
	rows.PrefixRows.ReconciliationSources[0].AfterRequester = sourcePtr("old-maker")
	rows.OriginalRows.ReconciliationCases[0].Requester = sourcePtr("old-maker")
	sourceManualClean(t, rows, 1) // Legacy actor-bearing open case remains accepted, not complete.
}

func TestSourceReconciliationManualPhysicalCountersAndCancellation(t *testing.T) {
	for rank, action := range []string{"P", "A", "R"} {
		rows := sourceManualFixture("PAR", "pending")
		duplicate := rows.PrefixRows.ReconciliationSources[rank]
		duplicate.ID, duplicate.RootID = "second-"+action, "second-root"
		rows.PrefixRows.ReconciliationSources = append(rows.PrefixRows.ReconciliationSources, duplicate)
		s, _ := sourceManualRun(t, rows)
		if len(s.bad["pointer"]) != 2 {
			t.Fatalf("duplicate %s physical rows=%v", action, s.bad["pointer"])
		}
	}
	rows := sourceManualFixture("PAR", "pending")
	rows.PrefixRows.ReconciliationSources[2].BeforeRequester = sourcePtr("bad")
	rows.PrefixRows.ReconciliationSources[2].CaseEvidenceRef = sourcePtr("bad")
	rows.PrefixRows.ReconciliationSources[2].BeforeApprover = sourcePtr("bad")
	s, _ := sourceManualRun(t, rows)
	if len(s.bad["pointer"]) != 1 {
		t.Fatal("one source with several transfer defects counted more than once")
	}
	rows = sourceManualFixture("P", "")
	rows.PrefixRows.ReconciliationSources[0].ID = "same-id"
	rows.OriginalRows.ReconciliationCases[0].ID = "same-id"
	rows.PrefixRows.ReconciliationSources[0].CaseID = "same-id"
	rows.OriginalRows.ReconciliationCases[0].TenantID = "bad"
	rows.OriginalRows.ReconciliationCases[0].PPLastSourceID = sourcePtr("wrong")
	s, _ = sourceManualRun(t, rows)
	if len(s.bad["pointer"]) != 2 {
		t.Fatal("same ID in source and original case collapsed physical types")
	}
	ctx, cancel := context.WithCancel(context.Background())
	w, _, err := newReplayWork(ctx, Rows{})
	if err != nil {
		t.Fatal(err)
	}
	s = &sourceClosureState{w: w, bad: map[string]keySet{}, tables: map[string][]sourceRawRow{}}
	cancel()
	result, err := s.reconciliationOrder()
	if !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(result, sourceReconciliationOrderResult{}) {
		t.Fatal("cancelled order exposed partial result")
	}
	s = sourceTestRows(t, sourceManualFixture("P", ""))
	s.w.b.deadline = time.Now().Add(-time.Second)
	if result, err := s.reconciliationOrder(); !errors.Is(err, ErrTimeout) || !reflect.DeepEqual(result, sourceReconciliationOrderResult{}) {
		t.Fatal("expired order renewed deadline or exposed partial result")
	}
	rows = sourceManualFixture("P", "")
	rows.OriginalRows.ReconciliationCases[0].ProposedUsage = sourcePtr(strings.Repeat(" ", 60000) + sourceManualUsageJSON)
	s = sourceTestRows(t, rows)
	s.w.b.steps = 0
	s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 12}
	if result, err := s.reconciliationOrder(); !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(result, sourceReconciliationOrderResult{}) {
		t.Fatal("mid-proposal cancellation exposed partial order")
	}
}

func TestSourceReconciliationManualEntryKeepsLegacyPrivate(t *testing.T) {
	rows := sourceManualFixture("A", "pending")
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	result, err := checkSourceClosure(w, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ReconciliationLegacyCases) != 1 {
		t.Fatal("manual legacy result was dropped at the private source entry")
	}
	if _, found := result.ReconciliationLegacyCases[tupleKey("case")]; !found {
		t.Fatal("legacy case lost its typed natural identity")
	}
	if result.Complete || len(result.Qualified) != 0 {
		t.Fatal("local legacy information granted complete source qualification")
	}
}
