package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/security"
)

// Procurement is separate from the customer reservation: customer release and
// refund never prove that the supplier did not consume a request.
type procurementHold struct {
	Attempt, Account, Binding, State string
	Upper, Held, Spent               *big.Int
	Input, Output                    int64
	Anomaly                          bool
	Revision                         int64
	periodic                         periodicbudget.SupplierLocks
}

func procurementAccountLock(ctx context.Context, tx pgx.Tx, account string) error {
	var id string
	// Account identities never change. Avoid upgrading FK KEY SHARE locks.
	return tx.QueryRow(ctx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, account).Scan(&id)
}

func procurementAdmission(ctx context.Context, tx pgx.Tx, account string, extra *big.Int) error {
	var active, available bool
	err := tx.QueryRow(ctx, `SELECT state='active' AND NOT EXISTS(SELECT 1 FROM procurement_reservations p WHERE p.account_id=a.id AND p.anomaly AND p.state IN ('unknown','disputed')),spent_amount+held_amount+$2::numeric<=limit_amount
 FROM procurement_accounts a WHERE id=$1`, account, extra.String()).Scan(&active, &available)
	if err != nil {
		return err
	}
	if !active {
		return fail("procurement_paused", "supplier procurement account is paused")
	}
	if !available {
		return fail("procurement_budget_exceeded", "shared supplier procurement budget is exhausted")
	}
	return nil
}

func recordProcurementAttempt(ctx context.Context, tx pgx.Tx, r financialRequest, in AttemptInput, attempt string, cost attemptCost) error {
	var binding, account string
	err := tx.QueryRow(ctx, `SELECT id,account_id FROM credential_procurement_bindings WHERE credential_id=$1 AND channel_id=$2`, in.CredentialID, in.ChannelID).Scan(&binding, &account)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // Explicit legacy_unmanaged, never silently infer an account.
	}
	if err != nil {
		return err
	}
	var prior bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM procurement_reservations WHERE attempt_id=$1)`, attempt).Scan(&prior); err != nil || prior {
		return err
	}
	if err = procurementAccountLock(ctx, tx, account); err != nil {
		return err
	}
	periodic, err := periodicbudget.LockSupplierTx(ctx, tx, account)
	if err != nil {
		return periodicError(err)
	}
	// The account lock may have waited across a scheduled price or deadline.
	var current attemptCost
	current.Basis = "unknown"
	err = tx.QueryRow(ctx, currentAttemptCostSQL, in.ChannelID, in.UpstreamModel).Scan(&current.ID, &current.InputRate, &current.OutputRate, &current.Basis)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail("cost_unknown", "controlled procurement requires a complete quote")
	}
	if err != nil {
		return err
	}
	if !quoteOf(current).Equal(quoteOf(cost)) || in.CostQuote == nil || !in.CostQuote.Equal(quoteOf(current)) {
		return fail("cost_quote_stale", "configured procurement quote changed before reservation")
	}
	if current.InputRate == nil || current.OutputRate == nil || !oneOf(current.Basis, "contract", "invoice") {
		return fail("cost_unknown", "controlled procurement requires both known cost dimensions")
	}
	var input, output int64
	var eligible bool
	if err = tx.QueryRow(ctx, `SELECT input_upper_bound,output_limit,clock_timestamp()<request_deadline FROM request_records WHERE id=$1`, r.ID).Scan(&input, &output, &eligible); err != nil {
		return err
	}
	if !eligible {
		return fail("request_deadline", "request deadline expired while waiting for procurement")
	}
	upper, err := Calculate(input, output, Money(*current.InputRate), Money(*current.OutputRate))
	if err != nil {
		return fail("procurement_bound_invalid", "procurement hard upper bound cannot be represented")
	}
	n := big.NewInt(int64(upper))
	if err = procurementAdmission(ctx, tx, account, n); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,spent_amount,state)
 VALUES($1,$2,$3,$4,$5,$6::numeric,$6::numeric,0,'prepared')`, attempt, account, binding, input, output, n.String()); err != nil {
		return err
	}
	admission, err := periodicbudget.AdmitSupplierTx(ctx, tx, periodic, attempt, periodicbudget.Amount{Decimal: n.String()})
	if err != nil {
		return periodicError(err)
	}
	var expires *time.Time
	if err = tx.QueryRow(ctx, "SELECT expires_at FROM api_keys WHERE tenant_id=$1 AND id=$2", r.TenantID, r.KeyID).Scan(&expires); err != nil {
		return err
	}
	if err = dispatchTimeEligible(admission.At, expires, r.Deadline, r.RequestDeadline); err != nil {
		return err
	}
	if err = checkDispatchCostQuote(ctx, tx, r.TenantID, attempt, admission.At); err != nil {
		return err
	}
	return procurementEvent(ctx, tx, periodic, account, attempt, "reserve:"+attempt, "reserved", n, big.NewInt(0), "")
}

func readProcurementHold(ctx context.Context, tx pgx.Tx, attempt string) (*procurementHold, error) {
	var account string
	err := tx.QueryRow(ctx, `SELECT account_id FROM procurement_reservations WHERE attempt_id=$1`, attempt).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = procurementAccountLock(ctx, tx, account); err != nil {
		return nil, err
	}
	periodic, err := periodicbudget.LockSupplierTx(ctx, tx, account)
	if err != nil {
		return nil, periodicError(err)
	}
	h := &procurementHold{Attempt: attempt, Account: account, periodic: periodic}
	var upper, held, spent string
	err = tx.QueryRow(ctx, `SELECT binding_id,input_bound,output_bound,upper_amount::text,held_amount::text,spent_amount::text,state,anomaly,revision
 FROM procurement_reservations WHERE attempt_id=$1 FOR UPDATE`, attempt).Scan(&h.Binding, &h.Input, &h.Output, &upper, &held, &spent, &h.State, &h.Anomaly, &h.Revision)
	if err != nil {
		return nil, err
	}
	h.Upper, _ = new(big.Int).SetString(upper, 10)
	h.Held, _ = new(big.Int).SetString(held, 10)
	h.Spent, _ = new(big.Int).SetString(spent, 10)
	if h.Upper == nil || h.Held == nil || h.Spent == nil {
		return nil, fail("procurement_invariant", "invalid stored procurement amount")
	}
	return h, nil
}

func prepareProcurementDispatch(ctx context.Context, tx pgx.Tx, attempt string) (periodicbudget.SupplierLocks, error) {
	h, err := readProcurementHold(ctx, tx, attempt)
	if err != nil {
		return periodicbudget.SupplierLocks{}, err
	}
	var current *string
	if err = tx.QueryRow(ctx, `SELECT b.id FROM upstream_attempts a LEFT JOIN credential_procurement_bindings b ON b.credential_id=a.credential_id AND b.channel_id=a.channel_id WHERE a.id=$1`, attempt).Scan(&current); err != nil {
		return periodicbudget.SupplierLocks{}, err
	}
	if h == nil {
		if current != nil {
			return periodicbudget.SupplierLocks{}, fail("procurement_binding_changed", "credential was bound after this attempt was prepared")
		}
		locks, err := periodicbudget.LockSupplierTx(ctx, tx, "")
		return locks, periodicError(err)
	}
	if current == nil || *current != h.Binding || h.State != "prepared" {
		return periodicbudget.SupplierLocks{}, fail("procurement_binding_changed", "frozen procurement identity is no longer eligible")
	}
	if err = procurementAdmission(ctx, tx, h.Account, big.NewInt(0)); err != nil {
		return periodicbudget.SupplierLocks{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE procurement_reservations SET state='sending',updated_at=clock_timestamp() WHERE attempt_id=$1`, attempt)
	return h.periodic, err
}

func procurementEvent(ctx context.Context, tx pgx.Tx, periodic periodicbudget.SupplierLocks, account, attempt, key, kind string, held, spent *big.Int, evidence string) error {
	eventID := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO procurement_budget_events(id,event_key,type,account_id,attempt_id,held_delta,spent_delta,evidence_ref)
 VALUES($1,$2,$3,$4,$5,$6::numeric,$7::numeric,$8)`, eventID, key, kind, account, attempt, held.String(), spent.String(), evidence); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE procurement_accounts SET held_amount=held_amount+$2::numeric,spent_amount=spent_amount+$3::numeric WHERE id=$1`, account, held.String(), spent.String()); err != nil {
		return err
	}
	return periodicError(periodicbudget.ApplySupplierEffectTx(ctx, tx, periodic, attempt, periodicbudget.Effect{SourceKind: "procurement_event", SourceID: eventID}))
}

func updateProcurementHold(ctx context.Context, tx pgx.Tx, h *procurementHold, held, spent *big.Int, state string, anomaly bool, key, kind, evidence string) error {
	dh := new(big.Int).Sub(held, h.Held)
	ds := new(big.Int).Sub(spent, h.Spent)
	if _, err := tx.Exec(ctx, `UPDATE procurement_reservations SET held_amount=$2::numeric,spent_amount=$3::numeric,state=$4,anomaly=$5,updated_at=clock_timestamp() WHERE attempt_id=$1`, h.Attempt, held.String(), spent.String(), state, anomaly); err != nil {
		return err
	}
	if anomaly {
		if _, err := tx.Exec(ctx, `UPDATE procurement_accounts SET state='paused',revision=revision+CASE WHEN state='paused' THEN 0 ELSE 1 END WHERE id=$1`, h.Account); err != nil {
			return err
		}
	}
	if state == "disputed" {
		if _, err := tx.Exec(ctx, `UPDATE upstream_attempts SET cost_status='unknown',cost_amount=NULL WHERE id=$1`, h.Attempt); err != nil {
			return err
		}
	}
	return procurementEvent(ctx, tx, h.periodic, h.Account, h.Attempt, key, kind, dh, ds, evidence)
}

// Called only after the customer reservation and attempt have serialized the
// evidence. It never checks current account eligibility or touches customer funds.
func finalizeProcurement(ctx context.Context, tx pgx.Tx, attempt, event string, usage Usage, reason string, conflict bool) error {
	h, err := readProcurementHold(ctx, tx, attempt)
	if err != nil || h == nil {
		return err
	}
	identity, err := json.Marshal(struct {
		Usage  Usage
		Reason string
	}{usage, reason})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(identity)
	tag, err := tx.Exec(ctx, `INSERT INTO procurement_evidence_identities(attempt_id,evidence_digest,event_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, attempt, hex.EncodeToString(digest[:]), event)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	if h.State == "released" && usage.Status == "confirmed_final" && (usage.InputTokens > 0 || usage.OutputTokens > 0) && !oneOf(reason, "not_sent", "rejected_no_consumption") {
		conflict = true
	}
	if h.State == "settled" && h.Spent.Sign() > 0 && oneOf(reason, "not_sent", "rejected_no_consumption") {
		conflict = true
	}
	if conflict {
		if h.State == "disputed" {
			return nil
		}
		var seen bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM procurement_budget_events WHERE event_key=$1)`, "conflict:"+attempt+":"+event).Scan(&seen); err != nil || seen {
			return err
		}
		held := new(big.Int).Sub(h.Upper, h.Spent)
		if held.Sign() < 0 {
			held.SetInt64(0)
		}
		return updateProcurementHold(ctx, tx, h, held, h.Spent, "disputed", true, "conflict:"+attempt+":"+event, "evidence_disputed", event)
	}
	if oneOf(h.State, "settled", "released", "disputed") {
		return nil // A subsequent normal event cannot adjudicate a dispute.
	}
	if oneOf(reason, "not_sent", "rejected_no_consumption") {
		return updateProcurementHold(ctx, tx, h, big.NewInt(0), h.Spent, "released", false, "release:"+attempt, "released_no_consumption", event)
	}
	if usage.Status != "confirmed_final" {
		if h.State == "unknown" {
			return nil
		}
		return updateProcurementHold(ctx, tx, h, h.Held, h.Spent, "unknown", false, "unknown:"+attempt, "consumption_unknown", event)
	}
	var inputRate, outputRate int64
	if err = tx.QueryRow(ctx, `SELECT cost_input_rate,cost_output_rate FROM upstream_attempts WHERE id=$1`, attempt).Scan(&inputRate, &outputRate); err != nil {
		return err
	}
	n, err := rawAmount(usage.InputTokens, usage.OutputTokens, Money(inputRate), Money(outputRate))
	if err != nil {
		return err
	}
	anomaly := n.Cmp(h.Upper) > 0 || usage.InputTokens > h.Input || usage.OutputTokens > h.Output
	return updateProcurementHold(ctx, tx, h, big.NewInt(0), n, "settled", anomaly, "settle:"+attempt, "cost_confirmed", event)
}

// RecoverProcurement has no provider client. An expired prepared attempt has no
// committed sending permit; a sending attempt can only become unknown.
func (s *Store) RecoverProcurement(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT a.tenant_id,a.request_id,a.id FROM procurement_reservations p JOIN upstream_attempts a ON a.id=p.attempt_id JOIN request_records r ON r.id=a.request_id
 WHERE p.state IN ('prepared','sending') AND r.request_deadline<clock_timestamp() ORDER BY r.request_deadline,a.id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type candidate struct{ tenant, request, attempt string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.tenant, &c.request, &c.attempt); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range candidates {
		err = s.transaction(ctx, func(tx pgx.Tx) error {
			if _, err := lockRequest(ctx, tx, c.tenant, c.request); err != nil {
				return err
			}
			var state string
			if err := tx.QueryRow(ctx, `SELECT delivery_state FROM upstream_attempts WHERE id=$1 FOR UPDATE`, c.attempt).Scan(&state); err != nil {
				return err
			}
			h, err := readProcurementHold(ctx, tx, c.attempt)
			if err != nil || h == nil || !oneOf(h.State, "prepared", "sending") {
				return err
			}
			if h.State == "prepared" && state == "prepared" {
				if _, err := tx.Exec(ctx, `UPDATE upstream_attempts SET delivery_state='failed' WHERE id=$1`, c.attempt); err != nil {
					return err
				}
				return updateProcurementHold(ctx, tx, h, big.NewInt(0), h.Spent, "released", false, "release:"+c.attempt, "expired_without_permit", "")
			}
			if _, err := tx.Exec(ctx, `UPDATE upstream_attempts SET delivery_state='unknown' WHERE id=$1`, c.attempt); err != nil {
				return err
			}
			return updateProcurementHold(ctx, tx, h, h.Held, h.Spent, "unknown", false, "unknown:"+c.attempt, "recovered_unknown", "")
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

type ProcurementCostProposalInput struct {
	OperationID, TenantID, AttemptID, ActorID, Basis, EvidenceRef, Reason string
	Amount                                                                Money
}
type ProcurementCostApprovalInput struct{ ProposalID, OperationID, ActorID string }
type ProcurementCostProposal struct {
	ID                    string     `json:"id"`
	TenantID              string     `json:"tenant_id"`
	AttemptID             string     `json:"attempt_id"`
	Amount                Money      `json:"amount"`
	Basis                 string     `json:"basis"`
	EvidenceRef           string     `json:"evidence_ref"`
	Reason                string     `json:"reason"`
	Requester             string     `json:"requester"`
	State                 string     `json:"state"`
	Approver              *string    `json:"approver"`
	ApprovalOperationID   *string    `json:"approval_operation_id"`
	CreatedAt             time.Time  `json:"created_at"`
	ApprovedAt            *time.Time `json:"approved_at"`
	ExpectedRevision      string     `json:"expected_revision"`
	ExpectedEvidenceCount string     `json:"expected_evidence_count"`
}

const procurementProposalSQL = `SELECT id,tenant_id,attempt_id,amount,basis,evidence_ref,reason,requester,state,approver,approval_operation_id,created_at,approved_at,expected_reservation_revision::text,expected_evidence_count::text FROM procurement_cost_proposals WHERE id=$1`

func scanProcurementProposal(row pgx.Row) (p ProcurementCostProposal, err error) {
	err = row.Scan(&p.ID, &p.TenantID, &p.AttemptID, &p.Amount, &p.Basis, &p.EvidenceRef, &p.Reason, &p.Requester, &p.State, &p.Approver, &p.ApprovalOperationID, &p.CreatedAt, &p.ApprovedAt, &p.ExpectedRevision, &p.ExpectedEvidenceCount)
	return
}

func (s *Store) GetProcurementCostProposal(ctx context.Context, id string) (ProcurementCostProposal, error) {
	return scanProcurementProposal(s.pool.QueryRow(ctx, procurementProposalSQL, id))
}

func (s *Store) ProposeProcurementCost(ctx context.Context, in ProcurementCostProposalInput) (ProcurementCostProposal, error) {
	var out ProcurementCostProposal
	if !validID(in.OperationID) || in.Amount < 0 || !oneOf(in.Basis, "contract", "invoice") || !security.ValidReference(in.EvidenceRef, 256) || !boundedString(in.Reason, 200) {
		return out, fail("invalid_cost", "canonical operation, evidence and reason required")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,9021))`, in.OperationID); err != nil {
			return err
		}
		if err := role(ctx, tx, in.ActorID, "finance_operator"); err != nil {
			return err
		}
		prior, err := scanProcurementProposal(tx.QueryRow(ctx, procurementProposalSQL, in.OperationID))
		if err == nil {
			if prior.TenantID != in.TenantID || prior.AttemptID != in.AttemptID || prior.Amount != in.Amount || prior.Basis != in.Basis || prior.EvidenceRef != in.EvidenceRef || prior.Reason != in.Reason || prior.Requester != in.ActorID {
				return fail("idempotency_conflict", "proposal identity already used")
			}
			out = prior
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var request string
		if err = tx.QueryRow(ctx, `SELECT request_id FROM upstream_attempts WHERE tenant_id=$1 AND id=$2`, in.TenantID, in.AttemptID).Scan(&request); err != nil {
			return err
		}
		if _, err = lockRequest(ctx, tx, in.TenantID, request); err != nil {
			return err
		}
		var attemptID string
		if err = tx.QueryRow(ctx, `SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE`, in.AttemptID).Scan(&attemptID); err != nil {
			return err
		}
		h, err := readProcurementHold(ctx, tx, in.AttemptID)
		if err != nil {
			return err
		}
		if h == nil {
			return fail("procurement_not_found", "attempt has no procurement hold")
		}
		if oneOf(h.State, "prepared", "sending") {
			return fail("procurement_inflight", "inflight procurement cannot be adjudicated")
		}
		if big.NewInt(int64(in.Amount)).Cmp(h.Spent) < 0 && in.Basis != "invoice" {
			return fail("supplier_refund_evidence_required", "cost reduction requires invoice evidence")
		}
		var evidenceCount int64
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM procurement_evidence_identities WHERE attempt_id=$1`, in.AttemptID).Scan(&evidenceCount); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO procurement_cost_proposals(id,tenant_id,attempt_id,amount,basis,evidence_ref,reason,requester,state,expected_reservation_revision,expected_evidence_count) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'proposed',$9,$10)`, in.OperationID, in.TenantID, in.AttemptID, int64(in.Amount), in.Basis, in.EvidenceRef, in.Reason, in.ActorID, h.Revision, evidenceCount); err != nil {
			return err
		}
		if err = audit(ctx, tx, in.TenantID, in.ActorID, "procurement.cost_proposed", in.OperationID, request, map[string]any{"attempt_id": in.AttemptID, "amount": in.Amount, "basis": in.Basis, "evidence_ref": in.EvidenceRef}); err != nil {
			return err
		}
		out, err = scanProcurementProposal(tx.QueryRow(ctx, procurementProposalSQL, in.OperationID))
		return err
	})
	return out, err
}

func (s *Store) ApproveProcurementCost(ctx context.Context, in ProcurementCostApprovalInput) (ProcurementCostProposal, error) {
	var out ProcurementCostProposal
	if !validID(in.OperationID) || !validID(in.ProposalID) {
		return out, fail("invalid_cost", "canonical operation and proposal required")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,9021))`, in.OperationID); err != nil {
			return err
		}
		if err := role(ctx, tx, in.ActorID, "finance_approver"); err != nil {
			return err
		}
		p, err := scanProcurementProposal(tx.QueryRow(ctx, procurementProposalSQL, in.ProposalID))
		if err != nil {
			return err
		}
		if p.Requester == in.ActorID {
			return fail("self_approval", "different maker and checker required")
		}
		if err = role(ctx, tx, p.Requester, "finance_operator"); err != nil {
			return err
		}
		var request string
		if err = tx.QueryRow(ctx, `SELECT request_id FROM upstream_attempts WHERE tenant_id=$1 AND id=$2`, p.TenantID, p.AttemptID).Scan(&request); err != nil {
			return err
		}
		if _, err = lockRequest(ctx, tx, p.TenantID, request); err != nil {
			return err
		}
		var attemptID string
		if err = tx.QueryRow(ctx, `SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE`, p.AttemptID).Scan(&attemptID); err != nil {
			return err
		}
		h, err := readProcurementHold(ctx, tx, p.AttemptID)
		if err != nil {
			return err
		}
		if h == nil {
			return fail("procurement_not_found", "procurement hold missing")
		}
		p, err = scanProcurementProposal(tx.QueryRow(ctx, procurementProposalSQL+" FOR UPDATE", in.ProposalID))
		if err != nil {
			return err
		}
		if p.State == "approved" {
			if p.ApprovalOperationID == nil || *p.ApprovalOperationID != in.OperationID || p.Approver == nil || *p.Approver != in.ActorID {
				return fail("idempotency_conflict", "proposal already approved by another operation")
			}
			out = p
			return nil
		}
		if oneOf(h.State, "prepared", "sending") {
			return fail("procurement_inflight", "inflight procurement cannot be adjudicated")
		}
		var evidenceCount int64
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM procurement_evidence_identities WHERE attempt_id=$1`, p.AttemptID).Scan(&evidenceCount); err != nil {
			return err
		}
		if p.ExpectedRevision != big.NewInt(h.Revision).String() || p.ExpectedEvidenceCount != big.NewInt(evidenceCount).String() {
			return fail("procurement_evidence_stale", "procurement facts changed after proposal; fresh evidence required")
		}
		amount := big.NewInt(int64(p.Amount))
		if amount.Cmp(h.Spent) < 0 && p.Basis != "invoice" {
			return fail("supplier_refund_evidence_required", "cost reduction requires invoice evidence")
		}
		if err = updateProcurementHold(ctx, tx, h, big.NewInt(0), amount, "settled", amount.Cmp(h.Upper) > 0, "correction:"+in.OperationID, "cost_corrected", p.EvidenceRef); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO provider_cost_corrections(id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), p.TenantID, p.AttemptID, "procurement:"+in.OperationID, int64(p.Amount), p.Basis, p.EvidenceRef); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE upstream_attempts SET cost_amount=$2,cost_status=$3 WHERE id=$1`, p.AttemptID, int64(p.Amount), p.Basis); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE procurement_cost_proposals SET state='approved',approver=$2,approval_operation_id=$3,approved_at=clock_timestamp() WHERE id=$1`, p.ID, in.ActorID, in.OperationID); err != nil {
			return err
		}
		if err = audit(ctx, tx, p.TenantID, in.ActorID, "procurement.cost_approved", p.ID, request, map[string]any{"attempt_id": p.AttemptID, "amount": p.Amount, "evidence_ref": p.EvidenceRef}); err != nil {
			return err
		}
		if err = emit(ctx, tx, p.TenantID, "procurement:"+in.OperationID, "cost_corrected", map[string]any{"attempt_id": p.AttemptID, "amount": p.Amount, "basis": p.Basis}); err != nil {
			return err
		}
		out, err = scanProcurementProposal(tx.QueryRow(ctx, procurementProposalSQL, p.ID))
		return err
	})
	return out, err
}
