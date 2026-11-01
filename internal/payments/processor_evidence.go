package payments

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type processorEvidence struct {
	id, environment, merchant, order, kind, fact, state string
	inbox, query, transaction, currency                 *string
	lastError                                           *string
	amount, revision                                    *int64
	happened, until                                     *time.Time
	verified                                            time.Time
	generation                                          int64
	attempts                                            int
}

func processorReadEvidence(ctx context.Context, tx pgx.Tx, id string) (processorEvidence, error) {
	var e processorEvidence
	err := tx.QueryRow(ctx, `SELECT e.id,e.environment_id,e.merchant_id,e.order_id,e.kind,e.fact_digest,e.inbox_id,e.verified_query_id,
 e.provider_transaction_id,e.currency,e.amount,e.provider_order_revision,e.happened_at,e.verified_at,
 p.state,p.generation,p.attempt_count,p.lease_until,p.last_error_code FROM payment_verified_evidence e JOIN payment_evidence_processing p ON p.evidence_id=e.id WHERE e.id=$1`, id).Scan(&e.id, &e.environment, &e.merchant, &e.order, &e.kind, &e.fact, &e.inbox, &e.query, &e.transaction, &e.currency, &e.amount, &e.revision, &e.happened, &e.verified, &e.state, &e.generation, &e.attempts, &e.until, &e.lastError)
	return e, err
}
func processorPtrEqual[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func processorTimeEqual(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}
func processorFrame(parts ...string) string {
	var b []byte
	for _, p := range parts {
		b = binary.BigEndian.AppendUint32(b, uint32(len([]byte(p))))
		b = append(b, []byte(p)...)
	}
	return processorDigest(b)
}
func processorFact(kind, env, merchant, order, original string, transaction *string, amount *int64, currency *string, happened *time.Time, revision *int64) (string, error) {
	if kind == "capture" {
		if transaction == nil || amount == nil || currency == nil || happened == nil || revision == nil {
			return "", ErrPaymentsIntegrity
		}
		return processorFrame(env, merchant, order, kind, *transaction, processorInt(*amount), *currency, processorTime(*happened), processorInt(*revision)), nil
	}
	if kind == "closed" {
		if happened == nil || revision == nil {
			return "", ErrPaymentsIntegrity
		}
		return processorFrame(env, merchant, order, kind, processorTime(*happened), processorInt(*revision)), nil
	}
	return processorFrame(env, merchant, order, kind, original), nil
}
func processorPositive(s string) (int64, error) {
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil || v <= 0 || processorInt(v) != s {
		return 0, ErrPaymentsIntegrity
	}
	return v, nil
}
func processorInstant(s string) (time.Time, error) {
	t, e := time.Parse(time.RFC3339Nano, s)
	if e != nil || t.Nanosecond()%1000 != 0 {
		return time.Time{}, ErrPaymentsIntegrity
	}
	return t.UTC(), nil
}

// Authentication happened at the P entrance. This replays strict parsing and
// bindings of the stored original; it does not resurrect an expired signature.
func (p *Processor) rebuildEvidence(ctx context.Context, tx pgx.Tx, e processorEvidence, o *processorOrder) error {
	env := p.runtime.environment()
	if e.environment != env.ID || e.merchant != env.MerchantID || (e.inbox == nil) == (e.query == nil) {
		return ErrPaymentsIntegrity
	}
	var expected processorEvidence
	var original string
	var received time.Time
	expected.environment = e.environment
	expected.merchant = e.merchant
	expected.order = e.order
	if e.inbox != nil {
		var raw []byte
		var digest, event, storedEnv, storedMerchant string
		err := tx.QueryRow(ctx, `SELECT canonical_body,business_digest,event_id,environment_id,merchant_id,received_at FROM payment_inbox WHERE id=$1`, *e.inbox).Scan(&raw, &digest, &event, &storedEnv, &storedMerchant, &received)
		if err != nil {
			return err
		}
		n, err := paymentproto.ParseNotification(raw)
		if err != nil {
			return ErrPaymentsIntegrity
		}
		canonical, err := paymentproto.CanonicalNotification(n)
		if err != nil || !processorCanonical(raw, canonical, digest) || n.EventID != event || n.EnvironmentID != storedEnv || n.MerchantID != storedMerchant || n.EnvironmentID != e.environment || n.MerchantID != e.merchant || n.OrderID != e.order {
			return ErrPaymentsIntegrity
		}
		original = n.EventID
		expected.kind = "unsupported"
		if n.EventType == "capture.confirmed" {
			expected.kind = "capture"
		} else if n.EventType == "checkout.failed" {
			expected.kind = "observation"
			if n.FailureCode != nil && *n.FailureCode == "order_closed" {
				expected.kind = "closed"
			}
		}
		amount, err := processorPositive(n.Amount)
		if err != nil {
			return err
		}
		revision, err := processorPositive(n.ProviderOrderRevision)
		if err != nil {
			return err
		}
		at, err := processorInstant(n.OccurredAt)
		if err != nil {
			return err
		}
		expected.transaction = n.ProviderTransactionID
		expected.currency = &n.Currency
		expected.amount = &amount
		expected.revision = &revision
		expected.happened = &at
	} else {
		var childID string
		if err := tx.QueryRow(ctx, `SELECT query_id,received_at FROM payment_verified_queries WHERE id=$1`, *e.query).Scan(&childID, &received); err != nil {
			return err
		}
		a, err := processorReadChild(ctx, tx, childID)
		if err != nil {
			return err
		}
		s, err := processorReadOutbox(ctx, tx, a.supervisor, "")
		if err != nil {
			return err
		}
		if s.original == nil || o == nil {
			return ErrPaymentsIntegrity
		}
		b, err := processorReadOutbox(ctx, tx, *s.original, "")
		if err != nil {
			return err
		}
		if err = p.boundMutation(ctx, tx, b, *o); err != nil {
			return err
		}
		if !processorChildBound(a, s, b, *o) {
			return ErrPaymentsIntegrity
		}
		id, _, reply, found, err := processorSnapshot(ctx, tx, a)
		if err != nil {
			return err
		}
		if !found || id != *e.query {
			return ErrPaymentsIntegrity
		}
		original = reply.QueryID
		expected.kind = "observation"
		if reply.CommandState == "completed" && reply.Result != nil {
			if reply.Result.Status == "captured" {
				f := reply.Result.Capture
				if f == nil {
					return ErrPaymentsIntegrity
				}
				amount, err := processorPositive(f.Amount)
				if err != nil {
					return err
				}
				revision, err := processorPositive(f.ProviderOrderRevision)
				if err != nil {
					return err
				}
				at, err := processorInstant(f.OccurredAt)
				if err != nil {
					return err
				}
				expected.kind = "capture"
				expected.transaction = &f.ProviderTransactionID
				expected.currency = &f.Currency
				expected.amount = &amount
				expected.revision = &revision
				expected.happened = &at
			} else if reply.Result.Status == "closed" {
				if reply.Result.ClosedAt == nil || reply.ProviderOrderRevision == nil {
					return ErrPaymentsIntegrity
				}
				at, err := processorInstant(*reply.Result.ClosedAt)
				if err != nil {
					return err
				}
				revision, err := processorPositive(*reply.ProviderOrderRevision)
				if err != nil {
					return err
				}
				expected.kind = "closed"
				expected.happened = &at
				expected.revision = &revision
			}
		}
	}
	if !received.Equal(e.verified) || e.kind != expected.kind || !processorPtrEqual(e.transaction, expected.transaction) || !processorPtrEqual(e.currency, expected.currency) || !processorPtrEqual(e.amount, expected.amount) || !processorPtrEqual(e.revision, expected.revision) || !processorTimeEqual(e.happened, expected.happened) || e.happened != nil && e.happened.After(received.Add(5*time.Second)) {
		return ErrPaymentsIntegrity
	}
	fact, err := processorFact(e.kind, e.environment, e.merchant, e.order, original, e.transaction, e.amount, e.currency, e.happened, e.revision)
	if err != nil {
		return err
	}
	if fact != e.fact {
		return ErrPaymentsIntegrity
	}
	return nil
}

type processorApplyIDs struct{ identity, capture, review string }

func (p *Processor) ProcessEvidence(ctx context.Context, lease EvidenceLease) (ProcessResult, error) {
	if !p.valid(lease.binding, "evidence") {
		return ProcessResult{}, ErrPaymentsInvalidWork
	}
	if p.runtime.recovery() {
		return ProcessResult{}, ErrPaymentsRecovery
	}
	ids := processorApplyIDs{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	var result ProcessResult
	err := p.transaction(ctx, true, func(ctx context.Context, tx pgx.Tx) (error, error) {
		result = ProcessResult{}
		e, err := processorReadEvidence(ctx, tx, lease.binding.id)
		if err != nil {
			return nil, err
		}
		if e.generation != lease.binding.generation {
			return nil, ErrPaymentsLeaseLost
		}
		if e.state == "processed" || e.state == "processing_review" {
			result, err = p.restoreEvidence(ctx, tx, e)
			return nil, err
		}
		now, err := p.runtime.databaseTime(ctx, tx)
		if err != nil {
			return nil, err
		}
		if !processorCurrent(lease.binding, e.state, e.generation, e.until, now) {
			return nil, ErrPaymentsLeaseLost
		}
		o, err := processorLockOrder(ctx, tx, e.order)
		var order *processorOrder
		if err == nil {
			order = &o
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err = p.rebuildEvidence(ctx, tx, e, order); err != nil {
			return nil, err
		}
		reason := ""
		if order == nil {
			reason = "unknown_order"
		} else if e.kind == "unsupported" {
			reason = "unsupported_effect_review"
		}
		if reason != "" {
			result, err = processorReview(ctx, tx, e, ids.review, reason, "", "")
			if err != nil {
				return nil, err
			}
			return nil, processorFinishEvidence(ctx, tx, lease.binding, "processing_review", "permanent")
		}
		if o.environment != e.environment || o.merchant != e.merchant {
			return nil, ErrPaymentsIntegrity
		}
		if e.kind == "observation" {
			result = ProcessResult{Outcome: "processed"}
			return nil, processorFinishEvidence(ctx, tx, lease.binding, "processed", "")
		}
		if e.kind == "capture" {
			if e.amount == nil || e.currency == nil || e.happened == nil || e.revision == nil || e.transaction == nil {
				return nil, ErrPaymentsIntegrity
			}
			if *e.amount != o.amount {
				reason = "amount_mismatch"
			} else if *e.currency != o.currency {
				reason = "currency_mismatch"
			} else if e.happened.Before(o.created) || !e.happened.Before(o.expires) {
				reason = "capture_outside_window"
			}
		}
		if reason == "" {
			reason, err = processorRevision(ctx, tx, e)
			if err != nil {
				return nil, err
			}
		}
		if reason != "" {
			result, err = processorReview(ctx, tx, e, ids.review, reason, "", "")
			if err != nil {
				return nil, err
			}
			return nil, processorFinishEvidence(ctx, tx, lease.binding, "processing_review", "permanent")
		}
		if e.kind == "closed" {
			result = ProcessResult{Outcome: "processed"}
			return nil, processorFinishEvidence(ctx, tx, lease.binding, "processed", "")
		}
		result, err = p.applyCapture(ctx, tx, e, o, lease.binding, ids)
		if err != nil {
			return nil, err
		}
		state, lastError := "processed", ""
		if result.Outcome == "review" {
			state, lastError = "processing_review", "permanent"
		}
		return nil, processorFinishEvidence(ctx, tx, lease.binding, state, lastError)
	})
	if err != nil {
		return ProcessResult{}, err
	}
	return result, nil
}

func processorFinishEvidence(ctx context.Context, tx pgx.Tx, b leaseBinding, state, code string) error {
	tag, err := tx.Exec(ctx, `UPDATE payment_evidence_processing SET state=$3,last_error_code=$4 WHERE evidence_id=$1 AND state='leased' AND generation=$2 AND lease_until>clock_timestamp()`, b.id, b.generation, state, processorNullable(code))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrPaymentsLeaseLost
	}
	return nil
}
func processorReview(ctx context.Context, tx pgx.Tx, e processorEvidence, id, reason, identity, selection string) (ProcessResult, error) {
	_, err := tx.Exec(ctx, `INSERT INTO payment_review_facts(id,order_id,evidence_id,identity_id,selection_order_id,reason) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(evidence_id,reason) DO NOTHING`, id, e.order, e.id, processorNullable(identity), processorNullable(selection), reason)
	if err != nil {
		return ProcessResult{}, err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT order_id=$2 AND identity_id IS NOT DISTINCT FROM $3::text AND selection_order_id IS NOT DISTINCT FROM $4::text FROM payment_review_facts WHERE evidence_id=$1 AND reason=$5`, e.id, e.order, processorNullable(identity), processorNullable(selection), reason).Scan(&valid)
	if err != nil {
		return ProcessResult{}, err
	}
	if !valid {
		return ProcessResult{}, ErrPaymentsIntegrity
	}
	return ProcessResult{Outcome: "review", ReviewReason: &reason}, nil
}

func processorRevision(ctx context.Context, tx pgx.Tx, e processorEvidence) (string, error) {
	if e.revision == nil || e.happened == nil {
		return "", ErrPaymentsIntegrity
	}
	var conflict bool
	if e.kind == "closed" {
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_transaction_identities WHERE order_id=$1 AND (captured_at>=$2 OR provider_order_revision>=$3))`, e.order, *e.happened, *e.revision).Scan(&conflict)
		if err != nil {
			return "", err
		}
	} else {
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_provider_revisions r JOIN payment_verified_evidence c ON c.id=r.evidence_id WHERE r.order_id=$1 AND r.kind='closed' AND ($2>=c.happened_at OR $3>=r.provider_order_revision))`, e.order, *e.happened, *e.revision).Scan(&conflict)
		if err != nil {
			return "", err
		}
	}
	if conflict {
		return "provider_revision_conflict", nil
	}
	var kind, fact string
	err := tx.QueryRow(ctx, `SELECT kind,fact_digest FROM payment_provider_revisions WHERE environment_id=$1 AND merchant_id=$2 AND order_id=$3 AND provider_order_revision=$4`, e.environment, e.merchant, e.order, *e.revision).Scan(&kind, &fact)
	if err == nil {
		if kind != e.kind || fact != e.fact {
			return "provider_revision_conflict", nil
		}
		return "", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_provider_revisions(environment_id,merchant_id,order_id,provider_order_revision,kind,fact_digest,evidence_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, e.environment, e.merchant, e.order, *e.revision, e.kind, e.fact, e.id)
	return "", err
}

func (p *Processor) applyCapture(ctx context.Context, tx pgx.Tx, e processorEvidence, o processorOrder, b leaseBinding, ids processorApplyIDs) (ProcessResult, error) {
	_, err := tx.Exec(ctx, `INSERT INTO payment_transaction_identities(id,environment_id,merchant_id,provider_transaction_id,order_id,tenant_id,amount,currency,captured_at,provider_order_revision,evidence_id,processing_generation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(environment_id,merchant_id,provider_transaction_id) DO NOTHING`, ids.identity, e.environment, e.merchant, *e.transaction, e.order, o.tenant, *e.amount, *e.currency, *e.happened, *e.revision, e.id, b.generation)
	if err != nil {
		return ProcessResult{}, err
	}
	var identity string
	var same bool
	err = tx.QueryRow(ctx, `SELECT id,(order_id,tenant_id,amount,currency,captured_at,provider_order_revision)=($4,$5,$6,$7,$8,$9) FROM payment_transaction_identities WHERE environment_id=$1 AND merchant_id=$2 AND provider_transaction_id=$3`, e.environment, e.merchant, *e.transaction, e.order, o.tenant, *e.amount, *e.currency, *e.happened, *e.revision).Scan(&identity, &same)
	if err != nil {
		return ProcessResult{}, err
	}
	if !same {
		return processorReview(ctx, tx, e, ids.review, "transaction_conflict", "", "")
	}
	var selected string
	err = tx.QueryRow(ctx, `SELECT identity_id FROM payment_order_capture_selections WHERE order_id=$1`, e.order).Scan(&selected)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO payment_order_capture_selections(order_id,identity_id,first_evidence_id) VALUES($1,$2,$3)`, e.order, identity, e.id)
		if err != nil {
			return ProcessResult{}, err
		}
		selected = identity
	} else if err != nil {
		return ProcessResult{}, err
	}
	if selected != identity {
		return processorReview(ctx, tx, e, ids.review, "duplicate_order_capture", identity, e.order)
	}
	var overflow bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_review_facts WHERE selection_order_id=$1 AND identity_id=$2 AND reason='funding_amount_overflow')`, e.order, identity).Scan(&overflow); err != nil {
		return ProcessResult{}, err
	}
	if overflow {
		return processorReview(ctx, tx, e, ids.review, "funding_amount_overflow", identity, e.order)
	}
	// Existing is accepted only with the complete source, journal, receipt and
	// original capture audit. It never invokes the helper to repair a half graph.
	existing, found, err := processorExisting(ctx, tx, e.order, identity)
	if err != nil {
		return ProcessResult{}, err
	}
	if found {
		return existing, nil
	}
	var tenant string
	if err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, o.tenant).Scan(&tenant); err != nil {
		return ProcessResult{}, err
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT capture_funding`); err != nil {
		return ProcessResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_captures(id,order_id,identity_id,tenant_id,environment_id,merchant_id,provider_transaction_id,amount,wallet_credit_amount,currency,captured_at,provider_order_revision,verified_evidence_id,processing_generation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, ids.capture, e.order, identity, o.tenant, e.environment, e.merchant, *e.transaction, *e.amount, o.credit, *e.currency, *e.happened, *e.revision, e.id, b.generation)
	if err != nil {
		return ProcessResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_effects(capture_id,identity_id,environment_id,merchant_id,provider_transaction_id) VALUES($1,$2,$3,$4,$5)`, ids.capture, identity, e.environment, e.merchant, *e.transaction)
	if err != nil {
		return ProcessResult{}, err
	}
	funding, err := p.funding.ApplyFundingTx(ctx, tx, billing.FundingSourceRef{Kind: billing.FundingSandboxCapture, ID: ids.capture}, billing.FundingScope{TenantID: o.tenant, ActorID: "payment-sandbox-worker"})
	if err != nil {
		var amountError *billing.Error
		if errors.As(err, &amountError) && amountError.Code == "amount_overflow" {
			if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT capture_funding`); err != nil {
				return ProcessResult{}, err
			}
			return processorReview(ctx, tx, e, ids.review, "funding_amount_overflow", identity, e.order)
		}
		return ProcessResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,actor,tenant_scope,action,target,request_id,sanitized_diff) VALUES($1,'payment-sandbox-worker',$2,'payment.capture.credited',$3,'',jsonb_build_object('amount',$4::text))`, uuid.NewString(), o.tenant, ids.capture, processorInt(o.credit))
	if err != nil {
		return ProcessResult{}, err
	}
	receipt, journal := funding.Receipt.ID, funding.Receipt.JournalID
	return ProcessResult{Outcome: "credited", CaptureID: &ids.capture, FundingReceiptID: &receipt, JournalID: &journal}, nil
}

func processorExisting(ctx context.Context, tx pgx.Tx, order, identity string) (ProcessResult, bool, error) {
	var capture string
	var receipt, journal *string
	var valid *bool
	err := tx.QueryRow(ctx, `SELECT c.id,f.id,f.journal_id,payment_capture_source_valid(c.id) AND payment_sandbox_journal_valid(f.journal_id) AND EXISTS(SELECT 1 FROM audit_events a WHERE a.action='payment.capture.credited' AND a.target=c.id AND a.actor='payment-sandbox-worker' AND a.tenant_scope=c.tenant_id AND a.request_id='' AND a.sanitized_diff=jsonb_build_object('amount',c.wallet_credit_amount::text)) FROM payment_captures c LEFT JOIN funding_receipts f ON f.source_kind='sandbox_capture' AND f.source_id=c.id WHERE c.order_id=$1 AND c.identity_id=$2`, order, identity).Scan(&capture, &receipt, &journal, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProcessResult{}, false, nil
	}
	if err != nil {
		return ProcessResult{}, false, err
	}
	if receipt == nil || journal == nil || valid == nil || !*valid {
		return ProcessResult{}, false, ErrPaymentsIntegrity
	}
	return ProcessResult{Outcome: "existing", CaptureID: &capture, FundingReceiptID: receipt, JournalID: journal}, true, nil
}
func (p *Processor) restoreEvidence(ctx context.Context, tx pgx.Tx, e processorEvidence) (ProcessResult, error) {
	// The immutable source graph remains the authority after a confirmed final
	// processing transition; no new mutable state or authorization is acquired.
	var o processorOrder
	err := tx.QueryRow(ctx, `SELECT id,tenant_id,environment_id,merchant_id,merchant_order_id,currency,amount,wallet_credit_amount,created_at,expires_at FROM payment_orders WHERE id=$1`, e.order).Scan(&o.id, &o.tenant, &o.environment, &o.merchant, &o.merchantOrder, &o.currency, &o.amount, &o.credit, &o.created, &o.expires)
	var order *processorOrder
	if err == nil {
		order = &o
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ProcessResult{}, err
	}
	if err = p.rebuildEvidence(ctx, tx, e, order); err != nil {
		return ProcessResult{}, err
	}
	if e.state == "processing_review" {
		var reason string
		var identity, selection *string
		err := tx.QueryRow(ctx, `SELECT reason,identity_id,selection_order_id FROM payment_review_facts WHERE evidence_id=$1 AND order_id=$2 ORDER BY created_at,id COLLATE "C" LIMIT 1`, e.id, e.order).Scan(&reason, &identity, &selection)
		if errors.Is(err, pgx.ErrNoRows) && e.attempts == 20 && e.lastError != nil && *e.lastError == "attempts_exhausted" {
			reason = "processing_attempts_exhausted"
			err = nil
		}
		if err != nil {
			return ProcessResult{}, err
		}
		if reason != "processing_attempts_exhausted" {
			if err = processorReviewValid(ctx, tx, e, order, reason, identity, selection); err != nil {
				return ProcessResult{}, err
			}
		}
		return ProcessResult{Outcome: "review", ReviewReason: &reason}, nil
	}
	if e.kind == "capture" {
		if e.transaction == nil {
			return ProcessResult{}, ErrPaymentsIntegrity
		}
		var identity string
		err := tx.QueryRow(ctx, `SELECT id FROM payment_transaction_identities WHERE environment_id=$1 AND merchant_id=$2 AND provider_transaction_id=$3 AND order_id=$4 AND (amount,currency,captured_at,provider_order_revision)=($5,$6,$7,$8)`, e.environment, e.merchant, *e.transaction, e.order, e.amount, e.currency, e.happened, e.revision).Scan(&identity)
		if err != nil {
			return ProcessResult{}, err
		}
		result, found, err := processorExisting(ctx, tx, e.order, identity)
		if err != nil {
			return ProcessResult{}, err
		}
		if !found {
			return ProcessResult{}, ErrPaymentsIntegrity
		}
		return result, nil
	}
	if e.kind != "observation" && e.kind != "closed" {
		return ProcessResult{}, ErrPaymentsIntegrity
	}
	if e.kind == "closed" {
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_provider_revisions WHERE environment_id=$1 AND merchant_id=$2 AND order_id=$3 AND provider_order_revision=$4 AND kind='closed' AND fact_digest=$5)`, e.environment, e.merchant, e.order, e.revision, e.fact).Scan(&valid); err != nil {
			return ProcessResult{}, err
		}
		if !valid {
			return ProcessResult{}, ErrPaymentsIntegrity
		}
	}
	return ProcessResult{Outcome: "processed"}, nil
}

func processorReviewValid(ctx context.Context, tx pgx.Tx, e processorEvidence, o *processorOrder, reason string, identity, selection *string) error {
	valid := false
	switch reason {
	case "unknown_order":
		// The insertion guard proved absence then. A legitimately created order
		// with the same UUID later cannot erase that immutable historical review
		// or grant this terminal evidence a new opportunity to move money.
		valid = true
	case "unsupported_effect_review":
		valid = e.kind == "unsupported"
	case "amount_mismatch":
		valid = o != nil && e.amount != nil && *e.amount != o.amount
	case "currency_mismatch":
		valid = o != nil && e.currency != nil && *e.currency != o.currency
	case "capture_outside_window":
		valid = o != nil && e.kind == "capture" && e.happened != nil && (e.happened.Before(o.created) || !e.happened.Before(o.expires))
	case "provider_revision_conflict":
		if e.revision == nil || e.happened == nil {
			return ErrPaymentsIntegrity
		}
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_provider_revisions r WHERE r.environment_id=$1 AND r.merchant_id=$2 AND r.order_id=$3 AND r.provider_order_revision=$4 AND (r.kind<>$5 OR r.fact_digest<>$6)) OR
 ($5='closed' AND EXISTS(SELECT 1 FROM payment_transaction_identities i WHERE i.order_id=$3 AND (i.captured_at>=$7 OR i.provider_order_revision>=$4))) OR
 ($5='capture' AND EXISTS(SELECT 1 FROM payment_provider_revisions r JOIN payment_verified_evidence c ON c.id=r.evidence_id WHERE r.order_id=$3 AND r.kind='closed' AND ($7>=c.happened_at OR $4>=r.provider_order_revision)))`, e.environment, e.merchant, e.order, *e.revision, e.kind, e.fact, *e.happened).Scan(&valid)
		if err != nil {
			return err
		}
	case "transaction_conflict":
		if o == nil || e.transaction == nil {
			return ErrPaymentsIntegrity
		}
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_transaction_identities WHERE environment_id=$1 AND merchant_id=$2 AND provider_transaction_id=$3 AND (order_id,tenant_id,amount,currency,captured_at,provider_order_revision) IS DISTINCT FROM ($4,$5,$6,$7,$8,$9))`, e.environment, e.merchant, *e.transaction, e.order, o.tenant, e.amount, e.currency, e.happened, e.revision).Scan(&valid)
		if err != nil {
			return err
		}
	case "duplicate_order_capture":
		if identity == nil || selection == nil || *selection != e.order {
			return ErrPaymentsIntegrity
		}
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_order_capture_selections s JOIN payment_transaction_identities i ON i.order_id=s.order_id WHERE s.order_id=$1 AND i.id=$2 AND s.identity_id<>i.id AND payment_identity_valid(i.id))`, e.order, *identity).Scan(&valid)
		if err != nil {
			return err
		}
	case "funding_amount_overflow":
		if identity == nil || selection == nil || *selection != e.order {
			return ErrPaymentsIntegrity
		}
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_order_capture_selections s JOIN payment_transaction_identities i ON i.id=s.identity_id WHERE s.order_id=$1 AND s.identity_id=$2 AND payment_identity_valid(i.id)) AND NOT EXISTS(SELECT 1 FROM payment_captures WHERE order_id=$1)`, e.order, *identity).Scan(&valid)
		if err != nil {
			return err
		}
	default:
		return ErrPaymentsIntegrity
	}
	if !valid {
		return ErrPaymentsIntegrity
	}
	if reason != "duplicate_order_capture" && reason != "funding_amount_overflow" && (identity != nil || selection != nil) {
		return ErrPaymentsIntegrity
	}
	return nil
}
