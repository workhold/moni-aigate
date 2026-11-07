package billing

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

func settlementRow(ctx context.Context, tx pgx.Tx, tenant, request string) (Settlement, error) {
	var v Settlement
	err := tx.QueryRow(ctx, "SELECT id,request_id,tenant_id,charged_amount,raw_retail_amount::text,waived_retail_amount::text,refunded_amount,status,reason,journal_id FROM settlements WHERE tenant_id=$1 AND request_id=$2", tenant, request).Scan(&v.ID, &v.RequestID, &v.TenantID, &v.ChargedAmount, &v.RawRetailAmount, &v.WaivedRetailAmount, &v.RefundedAmount, &v.Status, &v.Reason, &v.JournalID)
	return v, err
}

func finalizationRow(ctx context.Context, tx pgx.Tx, id string) (Finalization, error) {
	var f Finalization
	var input, output *int64
	err := tx.QueryRow(ctx, `SELECT f.id,f.event_id,f.request_id,e.retail_usage_state,meter_event_quantity(e.id,'text_input_token'),meter_event_quantity(e.id,'text_output_token'),f.protocol_outcome,f.transport_end_reason,f.delivery_stage,f.content_written,f.finish_reason FROM finalization_records f JOIN metering_events e ON e.id=f.event_id WHERE f.id=$1`, id).Scan(&f.ID, &f.EventID, &f.RequestID, &f.Usage.Status, &input, &output, &f.ProtocolOutcome, &f.TransportEndReason, &f.DeliveryStage, &f.ContentWritten, &f.FinishReason)
	if err == nil {
		if input != nil {
			f.Usage.InputTokens = *input
		}
		if output != nil {
			f.Usage.OutputTokens = *output
		}
		f.Usage.TotalTokens = f.Usage.InputTokens + f.Usage.OutputTokens
	}
	return f, err
}

func (s *Store) Settle(ctx context.Context, in SettleInput) (Settlement, error) {
	var result Settlement
	if in.Mode == "" {
		in.Mode = "live"
	}
	if in.Mode != "live" && in.Mode != "recovery" {
		return result, fail("invalid_mode", "unknown settlement mode")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		r, err := lockRequest(ctx, tx, in.TenantID, in.RequestID)
		if err != nil {
			return err
		}
		result, err = settleRequest(ctx, tx, r, in.Mode)
		return err
	})
	return result, err
}

func settleRequest(ctx context.Context, tx pgx.Tx, r financialRequest, mode string) (Settlement, error) {
	existing, err := settlementRow(ctx, tx, r.TenantID, r.ID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Settlement{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return Settlement{}, err
	}
	// The financial deadline always wins, including trustworthy evidence stored earlier.
	if !now.Before(r.Deadline) {
		return finish(ctx, tx, r, 0, big.NewInt(0), "released_deadline")
	}
	if r.LatestFinalization == nil {
		if mode == "recovery" {
			var sent bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM upstream_attempts WHERE request_id=$1 AND delivery_state<>'prepared')", r.ID).Scan(&sent); err != nil {
				return Settlement{}, err
			}
			if !sent {
				return finish(ctx, tx, r, 0, big.NewInt(0), "released_no_delivery")
			}
		}
		return pendingResult(ctx, tx, r, "delivery_unknown")
	}
	f, err := finalizationRow(ctx, tx, *r.LatestFinalization)
	if err != nil {
		return Settlement{}, err
	}
	var conflict bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM reconciliation_cases WHERE request_id=$1 AND reason='metering_conflict' AND state<>'approved' AND state<>'closed')", r.ID).Scan(&conflict); err != nil {
		return Settlement{}, err
	}
	if conflict {
		return pendingResult(ctx, tx, r, "metering_conflict")
	}
	clientCancelled := f.TransportEndReason == "client_cancelled"
	noOutput := !f.ContentWritten && f.DeliveryStage != "unknown"
	failed := f.ProtocolOutcome == "failed" || f.ProtocolOutcome == "unknown" || oneOf(f.TransportEndReason, "provider_error", "platform_error", "platform_timeout", "connection_error", "not_sent", "rejected_no_consumption")
	if !clientCancelled && noOutput && (mode == "recovery" || failed || !now.Before(r.RequestDeadline)) {
		return finish(ctx, tx, r, 0, big.NewInt(0), "released_no_delivery")
	}
	if f.DeliveryStage == "unknown" && !f.ContentWritten && !clientCancelled {
		return pendingResult(ctx, tx, r, "delivery_unknown")
	}
	if f.Usage.Status != "confirmed_final" {
		return pendingResult(ctx, tx, r, "usage_unconfirmed")
	}
	if err = validateUsage(f.Usage); err != nil {
		return pendingResult(ctx, tx, r, "usage_unconfirmed")
	}
	// A live normal response needs a legitimate protocol terminal. Partial output and explicit cancellation have their own policy rows.
	if f.ProtocolOutcome != "completed" && !f.ContentWritten && !clientCancelled {
		return pendingResult(ctx, tx, r, "delivery_unknown")
	}
	raw, err := storedActualAmount(ctx, tx, r.TenantID, r.ID, f.ID, "", metering.Retail)
	if err != nil {
		return Settlement{}, err
	}
	charge := r.Amount
	if raw.Cmp(big.NewInt(charge)) < 0 {
		charge = raw.Int64()
	}
	return finish(ctx, tx, r, charge, raw, "charged_usage")
}

func finish(ctx context.Context, tx pgx.Tx, r financialRequest, charge int64, raw *big.Int, reason string) (Settlement, error) {
	var result Settlement
	if charge < 0 || charge > r.Amount {
		return result, fail("invalid_charge", "charge exceeds reservation")
	}
	// Lock every affected account before deciding the hard financial deadline. A blocked account lock must never extend billing eligibility.
	// Tightening the profile shares the model parent synchronization point with
	// Reserve/PrepareDispatch. Normal requests only retain SHARE on that parent.
	if raw.Cmp(big.NewInt(r.Amount)) > 0 {
		var model string
		if err := tx.QueryRow(ctx, "SELECT id FROM models WHERE id=$1 FOR UPDATE", r.ModelID).Scan(&model); err != nil {
			return result, err
		}
	}
	controls, err := periodicbudget.LockCustomerTx(ctx, tx, r.TenantID, r.KeyID)
	if err != nil {
		return result, periodicError(err)
	}
	kinds := []string{"HELD", "AVAILABLE"}
	if charge > 0 {
		kinds = append(kinds, "REVENUE")
	}
	accounts, err := lockAccounts(ctx, tx, r.TenantID, kinds...)
	if err != nil {
		return result, err
	}
	lotPlan, err := prepareFundingFinishTx(ctx, tx, r)
	if err != nil {
		return result, err
	}
	// Callers hold the same reservation lock. Recheck financial time immediately before money writes.
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return result, err
	}
	if !now.Before(r.Deadline) {
		charge = 0
		raw = big.NewInt(0)
		reason = "released_deadline"
	}
	id := uuid.NewString()
	status := "settled"
	if reason != "charged_usage" {
		status = "released"
	}
	deltas := map[string]int64{"HELD": -r.Amount, "AVAILABLE": r.Amount - charge}
	if charge > 0 {
		deltas["REVENUE"] = charge
	}
	journal, err := postLocked(ctx, tx, r.TenantID, id, status, r.ID, "system", deltas, accounts)
	if err != nil {
		return result, err
	}
	waived := new(big.Int).Sub(new(big.Int).Set(raw), big.NewInt(charge))
	if waived.Sign() < 0 {
		return result, fail("invalid_charge", "invalid raw charge")
	}
	var finalID any
	if r.LatestFinalization != nil {
		finalID = *r.LatestFinalization
	}
	_, err = tx.Exec(ctx, "INSERT INTO settlements(id,tenant_id,request_id,finalization_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id) VALUES($1,$2,$3,$4,$5,$6::text::numeric,$7::text::numeric,$8,$9,$10)", id, r.TenantID, r.ID, finalID, charge, raw.String(), waived.String(), status, reason, journal)
	if err != nil {
		return result, err
	}
	if err = applyFundingFinishTx(ctx, tx, r, id, journal, Money(charge), lotPlan); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, "UPDATE api_keys SET held=held-$3,spent=spent+$4 WHERE tenant_id=$1 AND id=$2", r.TenantID, r.KeyID, r.Amount, charge); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, "UPDATE reservations SET status=$2 WHERE id=$1", r.ReservationID, status); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, "UPDATE request_records SET billing_status=$2,status=CASE WHEN $2='released' AND status IN ('accepted','dispatching') THEN 'failed' ELSE status END WHERE id=$1", r.ID, status); err != nil {
		return result, err
	}
	if err = periodicbudget.FinishCustomerTx(ctx, tx, controls, r.ID, id); err != nil {
		return result, periodicError(err)
	}
	if _, err = tx.Exec(ctx, "UPDATE reconciliation_cases SET state='closed' WHERE request_id=$1 AND state<>'approved'", r.ID); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM recovery_jobs WHERE request_id=$1", r.ID); err != nil {
		return result, err
	}
	if err = audit(ctx, tx, r.TenantID, "system", "billing."+status, id, r.ID, map[string]any{"amount": Money(charge), "reason": reason}); err != nil {
		return result, err
	}
	if err = emit(ctx, tx, r.TenantID, id, status, map[string]any{"amount": Money(charge), "released": Money(r.Amount - charge), "request_id": r.ID}); err != nil {
		return result, err
	}
	if raw.Cmp(big.NewInt(r.Amount)) > 0 {
		if _, err = tx.Exec(ctx, "INSERT INTO billing_profile_blocks(model_id,profile_version,reason,request_id) VALUES($1,$2,'usage_exceeded_reservation',$3) ON CONFLICT DO NOTHING", r.ModelID, r.ProfileVersion, r.ID); err != nil {
			return result, err
		}
		if err = audit(ctx, tx, r.TenantID, "system", "billing.profile_blocked", r.ModelID, r.ID, map[string]any{"waived": waived.String()}); err != nil {
			return result, err
		}
	}
	result = Settlement{ID: id, RequestID: r.ID, TenantID: r.TenantID, ChargedAmount: Money(charge), RawRetailAmount: raw.String(), WaivedRetailAmount: waived.String(), Status: status, Reason: reason, JournalID: journal}
	return result, nil
}

func markPending(ctx context.Context, tx pgx.Tx, r financialRequest, reason string) error {
	if r.ReservationStatus == "settled" || r.ReservationStatus == "released" {
		return nil
	}
	if _, err := tx.Exec(ctx, "UPDATE reservations SET status='pending_reconciliation' WHERE id=$1", r.ReservationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE request_records SET billing_status='pending_reconciliation' WHERE id=$1", r.ID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "INSERT INTO reconciliation_cases(tenant_id,request_id,reason) VALUES($1,$2,$3) ON CONFLICT(request_id) DO UPDATE SET reason=CASE WHEN reconciliation_cases.reason='metering_conflict' THEN reconciliation_cases.reason ELSE excluded.reason END", r.TenantID, r.ID, reason)
	return err
}

func pendingResult(ctx context.Context, tx pgx.Tx, r financialRequest, reason string) (Settlement, error) {
	err := markPending(ctx, tx, r, reason)
	return Settlement{RequestID: r.ID, TenantID: r.TenantID, Status: "pending_reconciliation", Reason: reason, RawRetailAmount: "0", WaivedRetailAmount: "0"}, err
}

func (s *Store) Release(ctx context.Context, in ReleaseInput) (Settlement, error) {
	var result Settlement
	if !oneOf(in.Reason, "not_sent", "rejected_no_consumption", "no_capacity", "authorization_revoked", "platform_failure", "provider_failure", "no_delivery") {
		return result, fail("invalid_release", "invalid release reason")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		r, err := lockRequest(ctx, tx, in.TenantID, in.RequestID)
		if err != nil {
			return err
		}
		existing, err := settlementRow(ctx, tx, in.TenantID, in.RequestID)
		if err == nil {
			result = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		result, err = finish(ctx, tx, r, 0, big.NewInt(0), "released_"+in.Reason)
		return err
	})
	return result, err
}

func (s *Store) MarkPending(ctx context.Context, in PendingInput) error {
	if !boundedString(in.Reason, 80) {
		return fail("invalid_reason", "pending reason required")
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		r, err := lockRequest(ctx, tx, in.TenantID, in.RequestID)
		if err != nil {
			return err
		}
		var now time.Time
		if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return err
		}
		if !now.Before(r.Deadline) {
			_, err = settleRequest(ctx, tx, r, "recovery")
			return err
		}
		return markPending(ctx, tx, r, in.Reason)
	})
}
