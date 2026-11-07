package mockpsp

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

func canonicalRefundReply(r paymentrefundproto.CommandReply) (CanonicalRefundReply, error) {
	b, err := paymentrefundproto.CanonicalCommandReply(r)
	if err != nil {
		return CanonicalRefundReply{}, fail("mock_unavailable")
	}
	parsed, err := paymentrefundproto.ParseCommandReply(b)
	if err != nil {
		return CanonicalRefundReply{}, fail("mock_unavailable")
	}
	d, err := paymentrefundproto.ParameterDigest(parsed.Command)
	if err != nil {
		return CanonicalRefundReply{}, fail("mock_unavailable")
	}
	return CanonicalRefundReply{raw: b, digest: d, env: paymentrefundproto.Environment{ID: parsed.Command.EnvironmentID, MerchantID: parsed.Command.MerchantID}, request: parsed}, nil
}

func canonicalRefundQueryReply(r paymentrefundproto.QueryReply) (CanonicalRefundQueryReply, error) {
	b, err := paymentrefundproto.CanonicalQueryReply(r)
	if err != nil {
		return CanonicalRefundQueryReply{}, fail("mock_unavailable")
	}
	parsed, err := paymentrefundproto.ParseQueryReply(b)
	if err != nil {
		return CanonicalRefundQueryReply{}, fail("mock_unavailable")
	}
	d, err := paymentrefundproto.QueryDigest(paymentrefundproto.QueryRequest{SchemaVersion: parsed.SchemaVersion, QueryID: parsed.QueryID, MutationGeneration: parsed.MutationGeneration, Command: parsed.Command, ParameterDigest: parsed.ParameterDigest})
	if err != nil {
		return CanonicalRefundQueryReply{}, fail("mock_unavailable")
	}
	return CanonicalRefundQueryReply{raw: b, digest: d, env: paymentrefundproto.Environment{ID: parsed.Command.EnvironmentID, MerchantID: parsed.Command.MerchantID}, request: parsed}, nil
}

func refundReplyFor(req paymentrefundproto.RefundRequest, state string, fact *paymentrefundproto.RefundFact) (CanonicalRefundReply, error) {
	digest, err := paymentrefundproto.ParameterDigest(req)
	if err != nil {
		return CanonicalRefundReply{}, fail("mock_unavailable")
	}
	return canonicalRefundReply(paymentrefundproto.CommandReply{SchemaVersion: 2, Command: req, ParameterDigest: digest, CommandState: state, Result: fact})
}

func refundQueryReplyFor(req paymentrefundproto.QueryRequest, state string, fact *paymentrefundproto.RefundFact) (CanonicalRefundQueryReply, error) {
	if _, err := paymentrefundproto.QueryDigest(req); err != nil {
		return CanonicalRefundQueryReply{}, fail("mock_unavailable")
	}
	return canonicalRefundQueryReply(paymentrefundproto.QueryReply{SchemaVersion: 2, QueryID: req.QueryID, MutationGeneration: req.MutationGeneration, Command: req.Command, ParameterDigest: req.ParameterDigest, CommandState: state, Result: fact})
}

func readRefundFact(ctx context.Context, tx pgx.Tx, env, merchant, command string) (*paymentrefundproto.RefundFact, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT canonical_fact FROM mock_refund_facts WHERE environment_id=$1 AND merchant_id=$2 AND command_id=$3`, env, merchant, command).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	f, err := paymentrefundproto.ParseRefundFact(raw)
	if err != nil {
		return nil, fail("mock_unavailable")
	}
	return &f, nil
}

func refundFactMatchesCommand(f paymentrefundproto.RefundFact, command paymentrefundproto.RefundRequest) bool {
	digest, err := paymentrefundproto.ParameterDigest(command)
	return err == nil && f.SchemaVersion == command.SchemaVersion &&
		f.EnvironmentID == command.EnvironmentID && f.MerchantID == command.MerchantID &&
		f.LocalRefundID == command.LocalRefundID && f.CommandID == command.CommandID &&
		f.ParameterDigest == digest && f.MerchantOrderID == command.MerchantOrderID &&
		f.ProviderTransactionID == command.ProviderTransactionID &&
		f.OriginalCaptureAmount == command.OriginalCaptureAmount &&
		f.CaptureDigest == command.CaptureDigest && f.Amount == command.Amount &&
		f.Currency == command.Currency
}

// storedCaptureDigest rebuilds the v1 authenticated capture bridge from the
// immutable mock capture row. The request's self-reported digest is never used
// as authorization for a refund.
func (r *Repository) storedCaptureDigest(now time.Time, order, transaction, amount, currency string, occurred time.Time, revision string) (string, error) {
	_, orderID, err := paymentproto.ParseMerchantOrderID(order)
	if err != nil || r == nil || r.keyring == nil {
		return "", fail("mock_unavailable")
	}
	transactionCopy := transaction
	n := paymentproto.Notification{SchemaVersion: 1, EventID: uuid.NewString(), EventType: "capture.confirmed",
		EnvironmentID: r.env.ID, MerchantID: r.env.MerchantID, OrderID: orderID,
		ProviderTransactionID: &transactionCopy, Amount: amount, Currency: currency,
		OccurredAt: timeText(occurred), ProviderOrderRevision: revision}
	raw, err := paymentproto.CanonicalNotification(n)
	if err != nil {
		return "", fail("mock_unavailable")
	}
	delivery := paymentproto.SignedHeaders{DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}
	signed, err := r.keyring.SignNotification(r.env, delivery, raw)
	if err != nil {
		return "", fail("mock_unavailable")
	}
	verified, err := r.keyring.VerifyNotification(r.env, now, signed, raw)
	if err != nil {
		return "", fail("mock_unavailable")
	}
	binding, err := paymentrefundproto.CaptureFromV1Notification(verified)
	if err != nil {
		return "", fail("mock_unavailable")
	}
	digest, err := binding.Digest()
	if err != nil {
		return "", fail("mock_unavailable")
	}
	return digest, nil
}

func recordRefundConflict(ctx context.Context, tx pgx.Tx, r *Repository, command, local, reason, observedDigest string) error {
	if r == nil || !mockInputUUID(command) || !mockInputUUID(local) || !refundLowerHex(observedDigest) {
		return fail("mock_unavailable")
	}
	_, err := tx.Exec(ctx, `INSERT INTO mock_refund_conflicts(environment_id,merchant_id,conflict_id,command_id,local_refund_id,reason,observed_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, r.env.ID, r.env.MerchantID, uuid.NewString(), command, local, reason, observedDigest)
	return err
}

func refundLowerHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range s {
		if !((s[i] >= '0' && s[i] <= '9') || (s[i] >= 'a' && s[i] <= 'f')) {
			return false
		}
	}
	return true
}

func (r *Repository) Refund(parent context.Context, in CanonicalRefundCommand) (RefundOutcome, error) {
	if r == nil || in.env.ID != r.env.ID || in.env.MerchantID != r.env.MerchantID {
		return RefundOutcome{}, fail("invalid_request")
	}
	checked, err := ParseRefundCommandInput(r.env, in.raw)
	if err != nil || checked.digest != in.digest || !bytes.Equal(checked.raw, in.raw) {
		return RefundOutcome{}, fail("invalid_request")
	}
	ctx, cancel := r.bounded(parent)
	defer cancel()
	c, err := r.acquire(ctx, lockCommand, checked.request.CommandID)
	if err != nil {
		return RefundOutcome{}, err
	}
	defer c.close()
	// A local refund identity is independent from command_id. Serialize it
	// explicitly so two different commands cannot race a cross-order UNIQUE
	// conflict into an unsafe database error.
	if err = c.gate(ctx, lockCommand, checked.request.LocalRefundID); err != nil {
		return RefundOutcome{}, normalizeError(err)
	}
	var out RefundOutcome
	var conflictErr error
	err = c.transact(ctx, func(tx pgx.Tx) error {
		if err := r.merchantGate(ctx, tx, checked.request.MerchantOrderID); err != nil {
			return err
		}
		var raw []byte
		var digest, order, merchant string
		var state string
		err := tx.QueryRow(ctx, `SELECT canonical_parameters,parameter_digest,merchant_order_id,merchant_id,state FROM mock_refund_commands WHERE environment_id=$1 AND merchant_id=$2 AND command_id=$3 FOR UPDATE`, r.env.ID, r.env.MerchantID, checked.request.CommandID).Scan(&raw, &digest, &order, &merchant, &state)
		if err == nil {
			if digest != checked.digest || order != checked.request.MerchantOrderID || merchant != r.env.MerchantID || !bytes.Equal(raw, checked.raw) {
				stored, parseErr := paymentrefundproto.ParseRefundRequest(raw)
				if parseErr != nil {
					return fail("mock_unavailable")
				}
				conflictErr = fail("command_parameter_conflict")
				return recordRefundConflict(ctx, tx, r, checked.request.CommandID, stored.LocalRefundID, "identity_conflict", checked.digest)
			}
			stored, e := paymentrefundproto.ParseRefundRequest(raw)
			if e != nil {
				return fail("mock_unavailable")
			}
			fact, e := readRefundFact(ctx, tx, r.env.ID, r.env.MerchantID, checked.request.CommandID)
			if e != nil {
				return e
			}
			if (state == "completed") != (fact != nil) || (fact != nil && !refundFactMatchesCommand(*fact, stored)) {
				return fail("mock_unavailable")
			}
			out.Body, e = refundReplyFor(stored, state, fact)
			out.Replayed = true
			return e
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var localExists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mock_refund_commands WHERE environment_id=$1 AND merchant_id=$2 AND local_refund_id=$3)`, r.env.ID, r.env.MerchantID, checked.request.LocalRefundID).Scan(&localExists)
		if err == nil {
			if localExists {
				conflictErr = fail("command_parameter_conflict")
				return recordRefundConflict(ctx, tx, r, checked.request.CommandID, checked.request.LocalRefundID, "identity_conflict", checked.digest)
			}
		} else {
			return err
		}
		var transaction, amount, currency, revision string
		var occurred time.Time
		if err = tx.QueryRow(ctx, `SELECT c.provider_transaction_id,c.amount::text,c.currency,c.occurred_at,c.provider_order_revision::text
FROM mock_captures c JOIN mock_orders o USING(merchant_order_id)
WHERE c.merchant_order_id=$1 AND o.environment_id=$2 AND o.merchant_id=$3
`, checked.request.MerchantOrderID, r.env.ID, r.env.MerchantID).Scan(&transaction, &amount, &currency, &occurred, &revision); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fail("not_found")
			}
			return err
		}
		now, e := dbNow(ctx, tx)
		if e != nil {
			return e
		}
		captureDigest, e := r.storedCaptureDigest(now, checked.request.MerchantOrderID, transaction, amount, currency, occurred, revision)
		if e != nil {
			return e
		}
		if transaction != checked.request.ProviderTransactionID || amount != checked.request.OriginalCaptureAmount || currency != checked.request.Currency || revision == "" || captureDigest != checked.request.CaptureDigest {
			conflictErr = fail("command_parameter_conflict")
			return recordRefundConflict(ctx, tx, r, checked.request.CommandID, checked.request.LocalRefundID, "identity_conflict", checked.digest)
		}
		_, err = tx.Exec(ctx, `INSERT INTO mock_refund_captures(environment_id,merchant_id,merchant_order_id,provider_transaction_id,capture_digest,original_amount) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT (environment_id,merchant_id,merchant_order_id) DO NOTHING`, r.env.ID, r.env.MerchantID, checked.request.MerchantOrderID, transaction, captureDigest, amount)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO mock_refund_commands(environment_id,merchant_id,command_id,local_refund_id,merchant_order_id,provider_transaction_id,parameter_digest,canonical_parameters,amount,mutation_generation,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,1,'in_progress')`, r.env.ID, r.env.MerchantID, checked.request.CommandID, checked.request.LocalRefundID, checked.request.MerchantOrderID, checked.request.ProviderTransactionID, checked.digest, checked.raw, checked.request.Amount)
		if err != nil {
			return err
		}
		out.Body, err = refundReplyFor(checked.request, "in_progress", nil)
		return err
	})
	if err != nil {
		return RefundOutcome{}, err
	}
	if conflictErr != nil {
		return RefundOutcome{}, conflictErr
	}
	return out, nil
}

func (r *Repository) RefundQuery(parent context.Context, in CanonicalRefundQuery) (RefundQueryOutcome, error) {
	if r == nil || in.env.ID != r.env.ID || in.env.MerchantID != r.env.MerchantID {
		return RefundQueryOutcome{}, fail("invalid_request")
	}
	checked, err := ParseRefundQueryInput(r.env, in.raw)
	if err != nil || checked.digest != in.digest || !bytes.Equal(checked.raw, in.raw) {
		return RefundQueryOutcome{}, fail("invalid_request")
	}
	ctx, cancel := r.bounded(parent)
	defer cancel()
	c, err := r.acquire(ctx, lockQuery, checked.request.QueryID)
	if err != nil {
		return RefundQueryOutcome{}, err
	}
	defer c.close()
	var out RefundQueryOutcome
	err = c.transact(ctx, func(tx pgx.Tx) error {
		if err := r.merchantGate(ctx, tx, checked.request.Command.MerchantOrderID); err != nil {
			return err
		}
		var storedRaw, snapshotRaw []byte
		var storedDigest, snapshotDigest, snapshotState string
		err := tx.QueryRow(ctx, `SELECT canonical_request,request_digest,snapshot_state,snapshot_body,snapshot_digest FROM mock_refund_queries WHERE environment_id=$1 AND merchant_id=$2 AND query_id=$3 FOR UPDATE`, r.env.ID, r.env.MerchantID, checked.request.QueryID).Scan(&storedRaw, &storedDigest, &snapshotState, &snapshotRaw, &snapshotDigest)
		if err == nil {
			if storedDigest != checked.digest || !bytes.Equal(storedRaw, checked.raw) {
				return fail("query_parameter_conflict")
			}
			parsed, e := paymentrefundproto.ParseQueryReply(snapshotRaw)
			if e != nil {
				return fail("mock_unavailable")
			}
			if _, e = paymentrefundproto.QueryDigest(paymentrefundproto.QueryRequest{SchemaVersion: parsed.SchemaVersion, QueryID: parsed.QueryID, MutationGeneration: parsed.MutationGeneration, Command: parsed.Command, ParameterDigest: parsed.ParameterDigest}); e != nil {
				return fail("mock_unavailable")
			}
			if paymentproto.RequestDigest(snapshotRaw) != snapshotDigest || parsed.CommandState != snapshotState {
				return fail("mock_unavailable")
			}
			out.Body, e = canonicalRefundQueryReply(parsed)
			out.Replayed = true
			return e
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		state := "not_seen"
		var commandRaw []byte
		var commandStateText string
		var generation string
		if err = tx.QueryRow(ctx, `SELECT canonical_parameters,state,mutation_generation::text FROM mock_refund_commands WHERE environment_id=$1 AND merchant_id=$2 AND command_id=$3 FOR UPDATE`, r.env.ID, r.env.MerchantID, checked.request.Command.CommandID).Scan(&commandRaw, &commandStateText, &generation); err == nil {
			if !bytes.Equal(commandRaw, mustRefundCommandCanonical(checked.request.Command)) {
				return fail("query_parameter_conflict")
			}
			if generation != checked.request.MutationGeneration {
				return fail("query_parameter_conflict")
			}
			state = commandStateText
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		fact, err := readRefundFact(ctx, tx, r.env.ID, r.env.MerchantID, checked.request.Command.CommandID)
		if err != nil {
			return err
		}
		if fact != nil {
			if state != "completed" || !refundFactMatchesCommand(*fact, checked.request.Command) {
				return fail("mock_unavailable")
			}
			state = "completed"
		}
		if state == "completed" && fact == nil {
			return fail("mock_unavailable")
		}
		out.Body, err = refundQueryReplyFor(checked.request, state, fact)
		if err != nil {
			return err
		}
		snapshot, err := out.Body.Bytes()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO mock_refund_queries(environment_id,merchant_id,query_id,command_id,mutation_generation,request_digest,canonical_request,snapshot_state,snapshot_body,snapshot_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, r.env.ID, r.env.MerchantID, checked.request.QueryID, checked.request.Command.CommandID, checked.request.MutationGeneration, checked.digest, checked.raw, state, snapshot, paymentproto.RequestDigest(snapshot))
		return err
	})
	if err != nil {
		return RefundQueryOutcome{}, err
	}
	return out, nil
}
