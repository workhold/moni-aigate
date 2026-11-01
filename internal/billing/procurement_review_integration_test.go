package billing

import (
	"testing"

	"github.com/google/uuid"
)

// A balance/state revision alone cannot distinguish a new supplier claim while
// an attempt is already disputed. Duplicate semantic observations must remain
// harmless, while new evidence invalidates a proposal prepared beforehand.
func TestPostgresProcurementDisputedEvidenceApprovalBarrier(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "duplicate_semantics"
		if changed {
			name = "new_semantics"
		}
		t.Run(name, func(t *testing.T) {
			f := integration(t)
			account := procurementFixture(t, f, 1000)
			r := f.reserve(t)
			a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
				t.Fatal(err)
			}
			initial := procurementFinal(t, f, r, a, Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 2, TotalTokens: 12}, "done")
			if _, err = f.store.RecordFinalization(f.ctx, initial); err != nil {
				t.Fatal(err)
			}
			conflict := procurementFinal(t, f, r, a, Usage{Status: "confirmed_final", InputTokens: 11, OutputTokens: 2, TotalTokens: 13}, "done")
			_, err = f.store.RecordFinalization(f.ctx, conflict)
			assertCode(t, err, "metering_conflict")
			p, err := f.store.ProposeProcurementCost(f.ctx, ProcurementCostProposalInput{OperationID: uuid.NewString(), TenantID: f.tenant, AttemptID: a.ID, ActorID: f.operator, Amount: 24, Basis: "invoice", EvidenceRef: "invoice:review", Reason: "review conflicting supplier claims"})
			if err != nil {
				t.Fatal(err)
			}
			// A new transport/event identity with identical normalized evidence
			// is still a duplicate; its event ID must not stale the proposal.
			later := conflict
			later.ID, later.SourceEventID = uuid.NewString(), uuid.NewString()
			if changed {
				later.Usage.InputTokens++
				later.Usage.TotalTokens++
			}
			_, err = f.store.RecordFinalization(f.ctx, later)
			assertCode(t, err, "metering_conflict")
			_, err = f.store.ApproveProcurementCost(f.ctx, ProcurementCostApprovalInput{ProposalID: p.ID, OperationID: uuid.NewString(), ActorID: f.approver})
			if changed {
				assertCode(t, err, "procurement_evidence_stale")
				procurementBalance(t, f, account, "216", "24")
			} else {
				if err != nil {
					t.Fatal("duplicate normalized supplier evidence invalidated review", err)
				}
				procurementBalance(t, f, account, "0", "24")
			}
		})
	}
}

func TestPostgresProcurementReopenRequiresResolvedAnomaly(t *testing.T) {
	for _, mode := range []string{"new_attempt_disputed", "prepared_dispatch_disputed", "settled_breach"} {
		t.Run(mode, func(t *testing.T) {
			f := integration(t)
			account := procurementFixture(t, f, 1000)
			r := f.reserve(t)
			a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
				t.Fatal(err)
			}
			usage := Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 2, TotalTokens: 12}
			if mode == "settled_breach" {
				usage = Usage{Status: "confirmed_final", InputTokens: 140, OutputTokens: 10, TotalTokens: 150}
			}
			if _, err = f.store.RecordFinalization(f.ctx, procurementFinal(t, f, r, a, usage, "done")); err != nil {
				t.Fatal(err)
			}
			second := f.reserve(t)
			var prepared Attempt
			if mode == "prepared_dispatch_disputed" {
				prepared, err = f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, second))
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode != "settled_breach" {
				usage.InputTokens++
				usage.TotalTokens++
				_, err = f.store.RecordFinalization(f.ctx, procurementFinal(t, f, r, a, usage, "done"))
				assertCode(t, err, "metering_conflict")
			}
			// Even a directly restored active flag cannot authorize new spend
			// while an unresolved disputed supplier fact remains anomalous.
			if _, err = f.pool.Exec(f.ctx, `UPDATE procurement_accounts SET state='active' WHERE id=$1`, account); err != nil {
				t.Fatal(err)
			}
			if mode == "prepared_dispatch_disputed" {
				err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: prepared.ID})
			} else {
				_, err = f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, second))
			}
			if mode == "settled_breach" {
				if err != nil {
					t.Fatal("an explicitly reopened, fully known supplier fact blocked new budget", err)
				}
				procurementBalance(t, f, account, "240", "300")
			} else {
				assertCode(t, err, "procurement_paused")
				wantHeld := "216"
				if mode == "prepared_dispatch_disputed" {
					wantHeld = "456"
				}
				procurementBalance(t, f, account, wantHeld, "24")
			}
		})
	}
}

func TestPostgresProcurementAdjudicatedOriginalEvidenceDoesNotReopen(t *testing.T) {
	f := integration(t)
	account := procurementFixture(t, f, 1000)
	r := f.reserve(t)
	a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	initial := procurementFinal(t, f, r, a, Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 2, TotalTokens: 12}, "done")
	if _, err = f.store.RecordFinalization(f.ctx, initial); err != nil {
		t.Fatal(err)
	}
	conflict := procurementFinal(t, f, r, a, Usage{Status: "confirmed_final", InputTokens: 11, OutputTokens: 2, TotalTokens: 13}, "done")
	_, err = f.store.RecordFinalization(f.ctx, conflict)
	assertCode(t, err, "metering_conflict")
	p, err := f.store.ProposeProcurementCost(f.ctx, ProcurementCostProposalInput{OperationID: uuid.NewString(), TenantID: f.tenant, AttemptID: a.ID, ActorID: f.operator, Amount: 25, Basis: "invoice", EvidenceRef: "invoice:review", Reason: "adjudicate both known claims"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveProcurementCost(f.ctx, ProcurementCostApprovalInput{ProposalID: p.ID, OperationID: uuid.NewString(), ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	initial.ID, initial.SourceEventID = uuid.NewString(), uuid.NewString()
	_, err = f.store.RecordFinalization(f.ctx, initial)
	assertCode(t, err, "metering_conflict")
	procurementBalance(t, f, account, "0", "25")
	var state, amount string
	if err = f.pool.QueryRow(f.ctx, `SELECT p.state,a.cost_amount::text FROM procurement_reservations p JOIN upstream_attempts a ON a.id=p.attempt_id WHERE p.attempt_id=$1`, a.ID).Scan(&state, &amount); err != nil || state != "settled" || amount != "25" {
		t.Fatal("reobserved original usage erased an already adjudicated supplier fact", state, amount, err)
	}
}
