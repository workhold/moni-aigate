package billing

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/moni-ai/moni-aigate/internal/security"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Recover only adjudicates durable evidence; no upstream client is reachable from this package.
func (s *Store) Recover(ctx context.Context, in RecoverInput) (RecoveryResult, error) {
	var result RecoveryResult
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	type claim struct {
		Tenant, Request string
		Expired         bool
	}
	var claims []claim
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		claims = nil
		// Recreate notifications lost to a worker crash. The unfinished reservation is authoritative.
		if _, err := tx.Exec(ctx, `INSERT INTO recovery_jobs(request_id,tenant_id)
   SELECT request_id,tenant_id FROM reservations WHERE status IN ('reserved','pending_reconciliation')
   ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT j.tenant_id,j.request_id,v.deadline<=clock_timestamp()
   FROM recovery_jobs j JOIN request_records r ON r.id=j.request_id JOIN reservations v ON v.request_id=r.id
   WHERE j.next_run_at<=clock_timestamp() AND (j.lease_until IS NULL OR j.lease_until<clock_timestamp())
   AND (r.request_deadline<clock_timestamp() OR v.deadline<=clock_timestamp())
   AND v.status IN ('reserved','pending_reconciliation') ORDER BY j.next_run_at,j.request_id
   LIMIT $1 FOR UPDATE OF j SKIP LOCKED`, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c claim
			if err = rows.Scan(&c.Tenant, &c.Request, &c.Expired); err != nil {
				rows.Close()
				return err
			}
			claims = append(claims, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, c := range claims {
			if _, err = tx.Exec(ctx, `UPDATE recovery_jobs SET lease_until=clock_timestamp()+interval '30 seconds',owner=$2,attempts=attempts+1 WHERE request_id=$1`, c.Request, s.instanceID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	var firstErr error
	for _, c := range claims {
		if _, live := s.active.Load(c.Request); live && !c.Expired {
			s.deferRecovery(ctx, c.Request)
			continue
		}
		settlement, err := s.Settle(ctx, SettleInput{TenantID: c.Tenant, RequestID: c.Request, Mode: "recovery"})
		if err != nil {
			s.deferRecovery(ctx, c.Request)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		result.Processed++
		switch settlement.Status {
		case "settled":
			result.Settled++
		case "released":
			result.Released++
		default:
			result.Pending++
			s.deferRecovery(ctx, c.Request)
		}
	}
	return result, firstErr
}

func (s *Store) deferRecovery(ctx context.Context, request string) {
	_, _ = s.pool.Exec(ctx, `UPDATE recovery_jobs SET lease_until=NULL,owner=NULL,next_run_at=clock_timestamp()+interval '15 seconds' WHERE request_id=$1 AND owner=$2`, request, s.instanceID)
}

func (s *Store) Reconcile(ctx context.Context, in ReconcileInput) (Reconciliation, error) {
	var result Reconciliation
	if !oneOf(in.Action, "propose", "approve", "release") {
		return result, fail("invalid_reconciliation", "unknown action")
	}
	if in.Action == "propose" {
		if in.Usage.Status != "confirmed_final" || validateUsage(in.Usage) != nil || !security.ValidReference(in.EvidenceRef, 256) || !boundedString(in.Reason, 200) {
			return result, fail("invalid_evidence", "trusted final usage and controlled evidence reference required")
		}
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		required := "finance_operator"
		if in.Action == "approve" || in.Action == "release" {
			required = "finance_approver"
		}
		if err := role(ctx, tx, in.ActorID, required); err != nil {
			return err
		}
		r, err := lockRequest(ctx, tx, in.TenantID, in.RequestID)
		if err != nil {
			return err
		}
		var state string
		var requester *string
		var payload []byte
		var evidence, proposalReason *string
		err = tx.QueryRow(ctx, `SELECT id,state,requester,proposed_usage,evidence_ref,proposal_reason FROM reconciliation_cases WHERE tenant_id=$1 AND request_id=$2 FOR UPDATE`, in.TenantID, in.RequestID).Scan(&result.ID, &state, &requester, &payload, &evidence, &proposalReason)
		if errors.Is(err, pgx.ErrNoRows) {
			return fail("reconciliation_not_found", "request is not awaiting reconciliation")
		}
		if err != nil {
			return err
		}
		if in.Action == "release" {
			if !boundedString(in.Reason, 200) {
				return fail("invalid_reason", "release reason required")
			}
			if r.ReservationStatus == "released" {
				prior, err := settlementRow(ctx, tx, r.TenantID, r.ID)
				if err != nil {
					return err
				}
				result.State = "closed"
				result.Settlement = &prior
				return nil
			}
			if r.ReservationStatus != "pending_reconciliation" {
				return fail("reconciliation_closed", "only pending holds can be manually released; settled charges require refund")
			}
			released, err := finish(ctx, tx, r, 0, big.NewInt(0), "released_reconciliation")
			if err != nil {
				return err
			}
			result.State = "closed"
			result.Settlement = &released
			return audit(ctx, tx, in.TenantID, in.ActorID, "reconciliation.released", result.ID, r.ID, map[string]any{"reason": in.Reason})
		}
		if in.Action == "propose" {
			if state == "approved" || state == "closed" {
				return fail("reconciliation_closed", "case is already decided")
			}
			if r.ReservationStatus != "pending_reconciliation" {
				return fail("reconciliation_closed", "hold is no longer pending")
			}
			if state == "proposed" {
				var prior Usage
				if err = json.Unmarshal(payload, &prior); err != nil {
					return err
				}
				if requester != nil && *requester == in.ActorID && evidence != nil && *evidence == in.EvidenceRef && proposalReason != nil && *proposalReason == in.Reason && prior == in.Usage {
					result.State = "proposed"
					return nil
				}
				return fail("idempotency_conflict", "a proposed reconciliation is immutable pending checker decision")
			}
			proposed, err := json.Marshal(in.Usage)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE reconciliation_cases SET state='proposed',requester=$2,approver=NULL,proposed_usage=$3,evidence_ref=$4,proposal_reason=$5 WHERE id=$1`, result.ID, in.ActorID, proposed, in.EvidenceRef, in.Reason); err != nil {
				return err
			}
			result.State = "proposed"
			return audit(ctx, tx, in.TenantID, in.ActorID, "reconciliation.proposed", result.ID, r.ID, map[string]any{"usage": in.Usage, "evidence_ref": in.EvidenceRef, "reason": in.Reason})
		}
		if requester != nil {
			if err = role(ctx, tx, *requester, "finance_operator"); err != nil {
				return err
			}
		}
		if requester == nil || *requester == in.ActorID {
			return fail("self_approval", "different maker and checker required")
		}
		if state == "approved" || state == "closed" {
			prior, err := settlementRow(ctx, tx, r.TenantID, r.ID)
			if errors.Is(err, pgx.ErrNoRows) && r.ReservationStatus == "pending_reconciliation" {
				prior = Settlement{RequestID: r.ID, TenantID: r.TenantID, Status: "pending_reconciliation", Reason: "approved_evidence_pending_delivery", RawRetailAmount: "0", WaivedRetailAmount: "0"}
				err = nil
			}
			if err != nil {
				return err
			}
			result.State = state
			result.Settlement = &prior
			return nil
		}
		if state != "proposed" || evidence == nil {
			return fail("reconciliation_not_proposed", "trusted evidence must first be proposed")
		}
		var usage Usage
		if err = json.Unmarshal(payload, &usage); err != nil {
			return err
		}
		if usage.Status != "confirmed_final" || validateUsage(usage) != nil {
			return fail("invalid_evidence", "invalid approved usage")
		}
		var now time.Time
		if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE reconciliation_cases SET state='approved',approver=$2 WHERE id=$1`, result.ID, in.ActorID); err != nil {
			return err
		}
		if now.Before(r.Deadline) {
			// Approval can replace usage, never manufacture delivery. Preserve the original local facts.
			f := Finalization{ProtocolOutcome: "unknown", TransportEndReason: "unknown", DeliveryStage: "unknown"}
			if r.LatestFinalization != nil {
				f, err = finalizationRow(ctx, tx, *r.LatestFinalization)
				if err != nil {
					return err
				}
			}
			approved, semantic, err := recordFinalization(ctx, tx, r, FinalizationInput{ID: uuid.NewString(), TenantID: r.TenantID, RequestID: r.ID, Source: "approved_reconciliation", SourceEventID: result.ID, Usage: usage, ProtocolOutcome: f.ProtocolOutcome, TransportEndReason: f.TransportEndReason, DeliveryStage: f.DeliveryStage, ContentWritten: f.ContentWritten, FinishReason: f.FinishReason})
			if err != nil {
				return err
			}
			if semantic != nil {
				return semantic
			}
			r.LatestFinalization = &approved.ID
		}
		settled, err := settleRequest(ctx, tx, r, "recovery")
		if err != nil {
			return err
		}
		result.State = "approved"
		result.Settlement = &settled
		return audit(ctx, tx, in.TenantID, in.ActorID, "reconciliation.approved", result.ID, r.ID, map[string]any{"outcome": settled.Status})
	})
	return result, err
}

func (s *Store) RecordCostCorrection(ctx context.Context, in CostCorrectionInput) error {
	if in.Amount < 0 || !boundedString(in.OperationID, 200) || !security.ValidReference(in.EvidenceRef, 256) || !oneOf(in.Basis, "contract", "invoice") {
		return fail("invalid_cost", "nonnegative supported cost fact required")
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		var request string
		if err := tx.QueryRow(ctx, "SELECT request_id FROM upstream_attempts WHERE tenant_id=$1 AND id=$2", in.TenantID, in.AttemptID).Scan(&request); err != nil {
			return err
		}
		if _, err := lockRequest(ctx, tx, in.TenantID, request); err != nil {
			return err
		}
		var managed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM procurement_reservations WHERE attempt_id=$1)`, in.AttemptID).Scan(&managed); err != nil {
			return err
		}
		if managed {
			return fail("procurement_approval_required", "managed supplier costs require an independent procurement checker")
		}
		id := uuid.NewString()
		tag, err := tx.Exec(ctx, `INSERT INTO provider_cost_corrections(id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(operation_id) DO NOTHING`, id, in.TenantID, in.AttemptID, in.OperationID, int64(in.Amount), in.Basis, in.EvidenceRef)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var equal bool
			err = tx.QueryRow(ctx, `SELECT tenant_id=$2 AND attempt_id=$3 AND amount=$4 AND basis=$5 AND evidence_ref=$6 FROM provider_cost_corrections WHERE operation_id=$1`, in.OperationID, in.TenantID, in.AttemptID, int64(in.Amount), in.Basis, in.EvidenceRef).Scan(&equal)
			if err != nil {
				return err
			}
			if !equal {
				return fail("idempotency_conflict", "cost operation already used")
			}
			return nil
		}
		if _, err = tx.Exec(ctx, `UPDATE upstream_attempts SET cost_amount=$2,cost_status=$3,latest_cost_finalization_id=NULL,latest_cost_correction_id=$4 WHERE id=$1`, in.AttemptID, int64(in.Amount), in.Basis, id); err != nil {
			return err
		}
		if err = audit(ctx, tx, in.TenantID, costActor(in.ActorID), "cost.corrected", id, request, map[string]any{"amount": in.Amount, "basis": in.Basis}); err != nil {
			return err
		}
		return emit(ctx, tx, in.TenantID, in.OperationID, "cost_corrected", map[string]any{"attempt_id": in.AttemptID, "amount": in.Amount, "basis": in.Basis})
	})
}

func costActor(actor string) string {
	if actor == "" {
		return "system"
	}
	return actor
}
