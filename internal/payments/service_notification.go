package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func paymentProtocolError(err error) error {
	var pe *p.Error
	if errors.As(err, &pe) {
		switch pe.Code {
		case "payment_key_unavailable", "payment_environment_invalid", "payment_signature_invalid":
			return paymentFail(pe.Code)
		}
	}
	return paymentFail("payment_signature_invalid")
}
func (s *Service) ReceiveNotification(ctx context.Context, in NotificationInput) (NotificationACK, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !s.runtime.hasEnvironment() {
		return NotificationACK{}, false, ErrPaymentsEnvironment
	}
	if e := s.runtime.check(ctx); e != nil {
		return NotificationACK{}, false, e
	}
	now, e := s.runtime.databaseTime(ctx, nil)
	if e != nil {
		return NotificationACK{}, false, paymentSafeError(e)
	}
	verified, e := s.runtime.verifyNotification(now, in.Headers, in.Body)
	if e != nil {
		return NotificationACK{}, false, paymentProtocolError(e)
	}
	canonical, e := verified.Canonical()
	if e != nil {
		return NotificationACK{}, false, ErrPaymentsIntegrity
	}
	n, e := p.ParseNotification(canonical)
	if e != nil {
		return NotificationACK{}, false, ErrPaymentsIntegrity
	}
	var ack NotificationACK
	var first, conflict bool
	e = s.transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ack = NotificationACK{}
		first = false
		conflict = false
		tag, e := tx.Exec(ctx, `INSERT INTO payment_inbox(id,environment_id,merchant_id,event_id,canonical_body,verified_key_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(environment_id,merchant_id,event_id) DO NOTHING`, uuid.NewString(), n.EnvironmentID, n.MerchantID, n.EventID, canonical, in.Headers.KeyID)
		if e != nil {
			return e
		}
		first = tag.RowsAffected() == 1
		var stored []byte
		var digest string
		if e = tx.QueryRow(ctx, `SELECT id,event_id,received_at,canonical_body,business_digest FROM payment_inbox WHERE environment_id=$1 AND merchant_id=$2 AND event_id=$3`, n.EnvironmentID, n.MerchantID, n.EventID).Scan(&ack.ReceiptID, &ack.EventID, &ack.ReceivedAt, &stored, &digest); e != nil {
			return e
		}
		if p.RequestDigest(stored) != digest {
			return ErrPaymentsIntegrity
		}
		if !bytes.Equal(stored, canonical) {
			if _, e = tx.Exec(ctx, `INSERT INTO payment_event_conflicts(id,inbox_id,conflicting_digest) VALUES($1,$2,$3) ON CONFLICT(inbox_id,conflicting_digest) DO NOTHING`, uuid.NewString(), ack.ReceiptID, p.RequestDigest(canonical)); e != nil {
				return e
			}
			conflict = true
			first = false
			return nil
		}
		if _, e = normalizeNotificationTx(ctx, tx, ack.ReceiptID); e != nil {
			return e
		}
		ack.Status = "received"
		ack.Environment = "sandbox"
		return nil
	})
	if e != nil {
		return NotificationACK{}, false, e
	}
	if conflict {
		return NotificationACK{}, false, paymentFail("payment_event_conflict")
	}
	return ack, first, nil
}

type expectedPaymentEvidence struct {
	environment, merchant, order, kind, digest string
	transaction                                *string
	amount, revision                           *int64
	happened                                   *time.Time
	verified                                   time.Time
}

func paymentFactDigest(parts ...string) string {
	var b bytes.Buffer
	for _, part := range parts {
		_ = binary.Write(&b, binary.BigEndian, int32(len([]byte(part))))
		b.WriteString(part)
	}
	h := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(h[:])
}
func paymentEvidenceFact(e *expectedPaymentEvidence, ref string) {
	switch e.kind {
	case "capture":
		e.digest = paymentFactDigest(e.environment, e.merchant, e.order, "capture", *e.transaction, strconv.FormatInt(*e.amount, 10), "CNY", e.happened.UTC().Format(paymentTimeLayout), strconv.FormatInt(*e.revision, 10))
	case "closed":
		e.digest = paymentFactDigest(e.environment, e.merchant, e.order, "closed", e.happened.UTC().Format(paymentTimeLayout), strconv.FormatInt(*e.revision, 10))
	default:
		e.digest = paymentFactDigest(e.environment, e.merchant, e.order, e.kind, ref)
	}
}
func parseEvidenceNumber(raw string) (*int64, error) {
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n < 1 || strconv.FormatInt(n, 10) != raw {
		return nil, ErrPaymentsIntegrity
	}
	return &n, nil
}
func parseEvidenceTime(raw string) (*time.Time, error) {
	t, e := time.Parse(time.RFC3339Nano, raw)
	if e != nil || t.Year() < 1 || t.Year() > 9999 || t.Nanosecond()%1000 != 0 {
		return nil, ErrPaymentsIntegrity
	}
	t = t.UTC()
	return &t, nil
}

// These helpers accept only durable source IDs in the caller's transaction.
// They have no transaction ownership, network, HMAC capability or money input.
func normalizeNotificationTx(ctx context.Context, tx pgx.Tx, inboxID string) (string, error) {
	if !paymentID(inboxID) {
		return "", ErrPaymentsIntegrity
	}
	var raw []byte
	var env, merchant, event, digest string
	var received time.Time
	var insertedHere bool
	e := tx.QueryRow(ctx, `SELECT canonical_body,environment_id,merchant_id,event_id,business_digest,received_at,xmin::text=(pg_current_xact_id()::text::numeric % 4294967296)::text FROM payment_inbox WHERE id=$1`, inboxID).Scan(&raw, &env, &merchant, &event, &digest, &received, &insertedHere)
	if e != nil {
		return "", e
	}
	n, e := p.ParseNotification(raw)
	if e != nil {
		return "", ErrPaymentsIntegrity
	}
	canonical, e := p.CanonicalNotification(n)
	if e != nil || !bytes.Equal(raw, canonical) || p.RequestDigest(raw) != digest || n.EnvironmentID != env || n.MerchantID != merchant || n.EventID != event {
		return "", ErrPaymentsIntegrity
	}
	expected := expectedPaymentEvidence{environment: env, merchant: merchant, order: n.OrderID, transaction: n.ProviderTransactionID, verified: received}
	expected.amount, e = parseEvidenceNumber(n.Amount)
	if e != nil {
		return "", e
	}
	expected.revision, e = parseEvidenceNumber(n.ProviderOrderRevision)
	if e != nil {
		return "", e
	}
	expected.happened, e = parseEvidenceTime(n.OccurredAt)
	if e != nil || expected.happened.After(received.Add(5*time.Second)) {
		return "", ErrPaymentsIntegrity
	}
	switch n.EventType {
	case "capture.confirmed":
		expected.kind = "capture"
	case "checkout.failed":
		expected.kind = "observation"
		if n.FailureCode != nil && *n.FailureCode == "order_closed" {
			expected.kind = "closed"
		}
	default:
		expected.kind = "unsupported"
	}
	paymentEvidenceFact(&expected, event)
	return normalizePaymentEvidenceTx(ctx, tx, &inboxID, nil, insertedHere, expected)
}
func normalizeQueryTx(ctx context.Context, tx pgx.Tx, verifiedQueryID string) (string, error) {
	if !paymentID(verifiedQueryID) {
		return "", ErrPaymentsIntegrity
	}
	var raw, request []byte
	var digest string
	var received time.Time
	var insertedHere bool
	e := tx.QueryRow(ctx, `SELECT v.canonical_body,a.canonical_request,v.snapshot_digest,v.received_at,v.xmin::text=(pg_current_xact_id()::text::numeric % 4294967296)::text FROM payment_verified_queries v JOIN payment_query_attempts a ON a.query_id=v.query_id WHERE v.id=$1`, verifiedQueryID).Scan(&raw, &request, &digest, &received, &insertedHere)
	if e != nil {
		return "", e
	}
	q, e := p.ParseQuery(request)
	if e != nil {
		return "", ErrPaymentsIntegrity
	}
	cq, e := p.CanonicalQuery(q)
	if e != nil || !bytes.Equal(request, cq) {
		return "", ErrPaymentsIntegrity
	}
	r, e := p.ParseQueryReply(raw)
	if e != nil {
		return "", ErrPaymentsIntegrity
	}
	cr, e := p.CanonicalQueryReply(r)
	if e != nil || !bytes.Equal(raw, cr) || p.RequestDigest(raw) != digest || q.QueryID != r.QueryID || q.EnvironmentID != r.EnvironmentID || q.MerchantID != r.MerchantID || q.MerchantOrderID != r.MerchantOrderID || q.CommandID != r.CommandID || q.CommandKind != r.CommandKind || q.ParameterDigest != r.ParameterDigest {
		return "", ErrPaymentsIntegrity
	}
	env, order, e := p.ParseMerchantOrderID(r.MerchantOrderID)
	if e != nil || env != r.EnvironmentID {
		return "", ErrPaymentsIntegrity
	}
	expected := expectedPaymentEvidence{environment: r.EnvironmentID, merchant: r.MerchantID, order: order, kind: "observation", verified: received}
	if r.CommandState == "completed" && r.Result != nil {
		switch r.Result.Status {
		case "captured":
			fact := r.Result.Capture
			if fact == nil || r.ProviderOrderRevision == nil || fact.ProviderOrderRevision != *r.ProviderOrderRevision {
				return "", ErrPaymentsIntegrity
			}
			expected.kind = "capture"
			expected.transaction = &fact.ProviderTransactionID
			expected.amount, e = parseEvidenceNumber(fact.Amount)
			if e != nil {
				return "", e
			}
			expected.revision, e = parseEvidenceNumber(fact.ProviderOrderRevision)
			if e != nil {
				return "", e
			}
			expected.happened, e = parseEvidenceTime(fact.OccurredAt)
			if e != nil {
				return "", e
			}
		case "closed":
			if r.Result.ClosedAt == nil || r.ProviderOrderRevision == nil {
				return "", ErrPaymentsIntegrity
			}
			expected.kind = "closed"
			expected.revision, e = parseEvidenceNumber(*r.ProviderOrderRevision)
			if e != nil {
				return "", e
			}
			expected.happened, e = parseEvidenceTime(*r.Result.ClosedAt)
			if e != nil {
				return "", e
			}
		}
	}
	if expected.happened != nil && expected.happened.After(received.Add(5*time.Second)) {
		return "", ErrPaymentsIntegrity
	}
	paymentEvidenceFact(&expected, r.QueryID)
	return normalizePaymentEvidenceTx(ctx, tx, nil, &verifiedQueryID, insertedHere, expected)
}
func normalizePaymentEvidenceTx(ctx context.Context, tx pgx.Tx, inbox, query *string, insertedHere bool, expected expectedPaymentEvidence) (string, error) {
	var id string
	e := tx.QueryRow(ctx, `SELECT id FROM payment_verified_evidence WHERE inbox_id=$1 OR verified_query_id=$2`, inbox, query).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		// A previously committed source without its graph is corruption. Never
		// silently turn a broken historical original into a new trusted graph.
		if !insertedHere {
			return "", ErrPaymentsIntegrity
		}
		id = uuid.NewString()
		if e = tx.QueryRow(ctx, `INSERT INTO payment_verified_evidence(id,inbox_id,verified_query_id) VALUES($1,$2,$3) RETURNING id`, id, inbox, query).Scan(&id); e != nil {
			return "", e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO payment_evidence_processing(evidence_id) VALUES($1)`, id); e != nil {
			return "", e
		}
	} else if e != nil {
		return "", e
	}
	var currency *string
	if expected.amount != nil {
		c := "CNY"
		currency = &c
	}
	var valid bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_verified_evidence e JOIN payment_evidence_processing p ON p.evidence_id=e.id WHERE e.id=$1 AND e.inbox_id IS NOT DISTINCT FROM $2::text AND e.verified_query_id IS NOT DISTINCT FROM $3::text AND (e.environment_id,e.merchant_id,e.order_id,e.kind,e.fact_digest,e.verified_at)=($4,$5,$6,$7,$8,$9) AND e.provider_transaction_id IS NOT DISTINCT FROM $10::text AND e.amount IS NOT DISTINCT FROM $11::bigint AND e.currency IS NOT DISTINCT FROM $12::text AND e.happened_at IS NOT DISTINCT FROM $13::timestamptz AND e.provider_order_revision IS NOT DISTINCT FROM $14::bigint AND p.generation=p.attempt_count AND p.attempt_count BETWEEN 0 AND 20 AND ((p.state='leased')=(p.lease_until IS NOT NULL)) AND ((p.state='pending' AND p.generation=0 AND p.last_error_code IS NULL) OR (p.state<>'pending' AND p.generation>0)))`, id, inbox, query, expected.environment, expected.merchant, expected.order, expected.kind, expected.digest, expected.verified, expected.transaction, expected.amount, currency, expected.happened, expected.revision).Scan(&valid)
	if e != nil {
		return "", e
	}
	if !valid {
		return "", ErrPaymentsIntegrity
	}
	return id, nil
}
