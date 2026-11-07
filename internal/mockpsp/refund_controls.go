package mockpsp

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

func refundControlAction(action string) bool {
	switch action {
	case "refund_confirmed", "refund_final_no_effect", "refund_conflict", "refund_not_seen":
		return true
	default:
		return false
	}
}

func saveRefundControl(ctx context.Context, tx pgx.Tx, in CanonicalControl, reason string, resource *string) (ControlReply, error) {
	status := "rejected"
	if reason == "accepted" {
		status = "accepted"
	}
	reply := ControlReply{OperationID: in.request.OperationID, Status: status, Reason: reason, ResourceID: resource}
	raw, err := canonicalControlReply(reply)
	if err != nil {
		return ControlReply{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO mock_refund_controls(operation_id,action,scenario,command_id,parameter_digest,canonical_parameters,result_body) VALUES($1,$2,$3,$4,$5,$6,$7)`, in.request.OperationID, in.request.Action, in.request.Scenario, *in.request.CommandID, in.digest, in.raw, raw)
	return reply, err
}

func refundControlFactMatches(action, scenario string, fact paymentrefundproto.RefundFact) bool {
	switch action {
	case "refund_confirmed":
		return scenario == "normal" && fact.Outcome == "confirmed"
	case "refund_final_no_effect":
		return fact.Outcome == "final_no_effect" && fact.FailureCode != nil && *fact.FailureCode == scenario
	default:
		return false
	}
}

func (r *Repository) controlRefundTx(ctx context.Context, tx pgx.Tx, in CanonicalControl, ids controlIDs) (ControlReply, error) {
	commandID := *in.request.CommandID
	var rootOrder string
	if err := tx.QueryRow(ctx, `SELECT merchant_order_id FROM mock_refund_commands WHERE environment_id=$1 AND merchant_id=$2 AND command_id=$3`, r.env.ID, r.env.MerchantID, commandID).Scan(&rootOrder); err != nil {
		if errors.Is(err, pgx.ErrNoRows) && in.request.Action == "refund_not_seen" {
			return saveRefundControl(ctx, tx, in, "accepted", strptr(in.request.OperationID))
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return saveRefundControl(ctx, tx, in, "target_not_found", nil)
		}
		return ControlReply{}, err
	}
	// The control operation lock is already held. Resolve the real capture
	// root before taking its xact lock; never fabricate an order lock from the
	// command ID.
	if err := r.merchantGate(ctx, tx, rootOrder); err != nil {
		return ControlReply{}, err
	}

	var raw []byte
	var state, rowOrder, transaction, digest string
	var local string
	var amount string
	err := tx.QueryRow(ctx, `SELECT canonical_parameters,state,merchant_order_id,provider_transaction_id,parameter_digest,local_refund_id,amount::text FROM mock_refund_commands WHERE environment_id=$1 AND merchant_id=$2 AND command_id=$3 FOR UPDATE`, r.env.ID, r.env.MerchantID, commandID).Scan(&raw, &state, &rowOrder, &transaction, &digest, &local, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		if in.request.Action == "refund_not_seen" {
			return saveRefundControl(ctx, tx, in, "accepted", strptr(in.request.OperationID))
		}
		return saveRefundControl(ctx, tx, in, "target_not_found", nil)
	}
	if err != nil {
		return ControlReply{}, err
	}
	command, err := paymentrefundproto.ParseRefundRequest(raw)
	commandDigest, digestErr := paymentrefundproto.ParameterDigest(command)
	if err != nil || digestErr != nil || command.CommandID != commandID || command.LocalRefundID != local || commandDigest != digest {
		return ControlReply{}, fail("mock_unavailable")
	}
	if in.request.Action == "refund_not_seen" {
		return saveRefundControl(ctx, tx, in, "state_conflict", nil)
	}

	observedFact, err := readRefundFact(ctx, tx, r.env.ID, r.env.MerchantID, commandID)
	if err != nil {
		return ControlReply{}, err
	}
	if observedFact != nil {
		if !refundFactMatchesCommand(*observedFact, command) {
			return ControlReply{}, fail("mock_unavailable")
		}
		if refundControlAction(in.request.Action) && refundControlFactMatches(in.request.Action, in.request.Scenario, *observedFact) {
			return saveRefundControl(ctx, tx, in, "accepted", strptr(observedFact.FactID))
		}
		_, e := insertRefundConflict(ctx, tx, r, command.CommandID, command.LocalRefundID, "fact_conflict", in.digest)
		if e != nil {
			return ControlReply{}, e
		}
		return saveRefundControl(ctx, tx, in, "state_conflict", nil)
	}
	if state != "in_progress" {
		return ControlReply{}, fail("mock_unavailable")
	}

	if in.request.Action == "refund_conflict" {
		conflictID, e := insertRefundConflict(ctx, tx, r, command.CommandID, command.LocalRefundID, in.request.Scenario, in.digest)
		if e != nil {
			return ControlReply{}, e
		}
		return saveRefundControl(ctx, tx, in, "accepted", &conflictID)
	}

	var captureTransaction, captureAmount, captureCurrency, captureRevision string
	var occurred time.Time
	if err = tx.QueryRow(ctx, `SELECT c.provider_transaction_id,c.amount::text,c.currency,c.occurred_at,c.provider_order_revision::text FROM mock_captures c JOIN mock_orders o USING(merchant_order_id) WHERE c.merchant_order_id=$1 AND o.environment_id=$2 AND o.merchant_id=$3`, rowOrder, r.env.ID, r.env.MerchantID).Scan(&captureTransaction, &captureAmount, &captureCurrency, &occurred, &captureRevision); err != nil {
		return ControlReply{}, fail("mock_unavailable")
	}
	now, err := dbNow(ctx, tx)
	if err != nil {
		return ControlReply{}, err
	}
	captureDigest, err := r.storedCaptureDigest(now, rowOrder, captureTransaction, captureAmount, captureCurrency, occurred, captureRevision)
	if err != nil {
		return ControlReply{}, err
	}
	if captureTransaction != command.ProviderTransactionID || captureAmount != command.OriginalCaptureAmount || captureCurrency != command.Currency || captureDigest != command.CaptureDigest {
		_, e := insertRefundConflict(ctx, tx, r, command.CommandID, command.LocalRefundID, "identity_conflict", in.digest)
		if e != nil {
			return ControlReply{}, e
		}
		return saveRefundControl(ctx, tx, in, "state_conflict", nil)
	}

	var storedDigest string
	var original, confirmed, noEffect, revision int64
	if err = tx.QueryRow(ctx, `SELECT capture_digest,original_amount,confirmed_amount,final_no_effect_amount,refund_revision FROM mock_refund_captures WHERE environment_id=$1 AND merchant_id=$2 AND merchant_order_id=$3 FOR UPDATE`, r.env.ID, r.env.MerchantID, rowOrder).Scan(&storedDigest, &original, &confirmed, &noEffect, &revision); err != nil {
		return ControlReply{}, fail("mock_unavailable")
	}
	if storedDigest != command.CaptureDigest || strconv.FormatInt(original, 10) != command.OriginalCaptureAmount || original < 1 {
		return ControlReply{}, fail("mock_unavailable")
	}
	requested, err := strconv.ParseInt(amount, 10, 64)
	if err != nil || requested <= 0 || confirmed > original || noEffect > original || requested > original-confirmed-noEffect {
		_, e := insertRefundConflict(ctx, tx, r, command.CommandID, command.LocalRefundID, "capture_limit_conflict", in.digest)
		if e != nil {
			return ControlReply{}, e
		}
		return saveRefundControl(ctx, tx, in, "state_conflict", nil)
	}
	if revision == 9223372036854775807 {
		return ControlReply{}, fail("mock_unavailable")
	}

	factID := uuid.NewString()
	completedAt := timeText(now)
	newFact := paymentrefundproto.RefundFact{SchemaVersion: 2, FactID: factID, EnvironmentID: command.EnvironmentID, MerchantID: command.MerchantID, LocalRefundID: command.LocalRefundID, CommandID: command.CommandID, ParameterDigest: digest, MerchantOrderID: command.MerchantOrderID, ProviderTransactionID: command.ProviderTransactionID, OriginalCaptureAmount: command.OriginalCaptureAmount, CaptureDigest: command.CaptureDigest, Amount: command.Amount, Currency: command.Currency, CaptureRefundRevision: strconv.FormatInt(revision+1, 10), CompletedAt: completedAt}
	if in.request.Action == "refund_confirmed" {
		providerReference := "refund:" + uuid.NewString()
		newFact.Outcome = "confirmed"
		newFact.ProviderRefundReference = &providerReference
	} else if in.request.Action == "refund_final_no_effect" {
		failure := in.request.Scenario
		newFact.Outcome = "final_no_effect"
		newFact.FailureCode = &failure
	} else {
		return ControlReply{}, fail("invalid_request")
	}
	factRaw, err := paymentrefundproto.CanonicalRefundFact(newFact)
	if err != nil {
		return ControlReply{}, fail("mock_unavailable")
	}
	factDigest, err := paymentrefundproto.FactDigest(newFact)
	if err != nil {
		return ControlReply{}, fail("mock_unavailable")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mock_refund_facts(environment_id,merchant_id,fact_id,command_id,local_refund_id,provider_transaction_id,fact_digest,outcome,provider_refund_reference,failure_code,amount,capture_refund_revision,canonical_fact,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, r.env.ID, r.env.MerchantID, factID, command.CommandID, command.LocalRefundID, command.ProviderTransactionID, factDigest, newFact.Outcome, newFact.ProviderRefundReference, newFact.FailureCode, requested, revision+1, factRaw, now); err != nil {
		return ControlReply{}, err
	}
	if in.request.Action == "refund_confirmed" {
		confirmed += requested
	} else {
		noEffect += requested
	}
	if _, err = tx.Exec(ctx, `UPDATE mock_refund_captures SET confirmed_amount=$1,final_no_effect_amount=$2,refund_revision=$3 WHERE environment_id=$4 AND merchant_id=$5 AND merchant_order_id=$6`, confirmed, noEffect, revision+1, r.env.ID, r.env.MerchantID, rowOrder); err != nil {
		return ControlReply{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE mock_refund_commands SET state='completed',completed_at=$1 WHERE environment_id=$2 AND merchant_id=$3 AND command_id=$4 AND state='in_progress'`, now, r.env.ID, r.env.MerchantID, command.CommandID); err != nil {
		return ControlReply{}, err
	}
	return saveRefundControl(ctx, tx, in, "accepted", &factID)
}

func insertRefundConflict(ctx context.Context, tx pgx.Tx, r *Repository, command, local, reason, observedDigest string) (string, error) {
	id := uuid.NewString()
	if r == nil || !mockInputUUID(command) || !mockInputUUID(local) || !refundLowerHex(observedDigest) {
		return "", fail("mock_unavailable")
	}
	_, err := tx.Exec(ctx, `INSERT INTO mock_refund_conflicts(environment_id,merchant_id,conflict_id,command_id,local_refund_id,reason,observed_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, r.env.ID, r.env.MerchantID, id, command, local, reason, observedDigest)
	return id, err
}
