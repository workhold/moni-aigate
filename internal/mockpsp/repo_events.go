package mockpsp

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type storedCapture struct {
	id   string
	fact paymentproto.CaptureFact
}

func readCapture(ctx context.Context, tx pgx.Tx, order string) (storedCapture, error) {
	var c storedCapture
	var occurred time.Time
	err := tx.QueryRow(ctx, `SELECT id,provider_transaction_id,amount::text,currency,occurred_at,provider_order_revision::text FROM mock_captures WHERE merchant_order_id=$1`, order).Scan(&c.id, &c.fact.ProviderTransactionID, &c.fact.Amount, &c.fact.Currency, &occurred, &c.fact.ProviderOrderRevision)
	c.fact.OccurredAt = timeText(occurred)
	return c, err
}

type controlIDs struct {
	capture, eventRow, eventRef, transaction, refund, caseRef, transition string
	command                                                               commandIDs
}

func newControlIDs() controlIDs {
	return controlIDs{capture: uuid.NewString(), eventRow: uuid.NewString(), eventRef: "evt:" + uuid.NewString(), transaction: "txn:" + uuid.NewString(), refund: uuid.NewString(), caseRef: "case:" + uuid.NewString(), transition: uuid.NewString(), command: newCommandIDs()}
}

func completeTerms(o storedOrder) bool {
	return o.amount != nil && *o.amount > 0 && o.created != nil && o.expires != nil && o.checkoutRef != nil && o.currency == "CNY"
}

func (r *Repository) eventBase(o storedOrder, cap *storedCapture, now time.Time, ids controlIDs) paymentproto.Notification {
	fact := paymentproto.CaptureFact{ProviderTransactionID: ids.transaction, Amount: strconv.FormatInt(*o.amount, 10), Currency: o.currency, OccurredAt: timeText(now), ProviderOrderRevision: "2"}
	if cap != nil {
		fact = cap.fact
	}
	return paymentproto.Notification{SchemaVersion: 1, EventID: ids.eventRef, EventType: "capture.confirmed", EnvironmentID: r.env.ID, MerchantID: r.env.MerchantID, OrderID: o.id, ProviderTransactionID: strptr(fact.ProviderTransactionID), Amount: fact.Amount, Currency: fact.Currency, OccurredAt: fact.OccurredAt, ProviderOrderRevision: fact.ProviderOrderRevision}
}

// scenarioEvent changes only the fields named by the frozen scenario table.
// A false result is a persistent QA state_conflict, not a protocol mutation.
func (r *Repository) scenarioEvent(o storedOrder, cap *storedCapture, now time.Time, ids controlIDs, scenario string) (paymentproto.Notification, bool) {
	if !completeTerms(o) {
		return paymentproto.Notification{}, false
	}
	n := r.eventBase(o, cap, now, ids)
	var at time.Time
	hasAt := true
	switch scenario {
	case "capture_at_created":
		at = *o.created
	case "capture_before_created_1us":
		at = o.created.Add(-time.Microsecond)
	case "capture_before_expiry_1us":
		at = o.expires.Add(-time.Microsecond)
	case "capture_at_expiry":
		at = *o.expires
	case "capture_future_6s":
		at = now.Add(6 * time.Second)
	case "amount_plus_one", "same_revision_conflict":
		hasAt = false
		if scenario == "same_revision_conflict" && cap == nil {
			return paymentproto.Notification{}, false
		}
		amount, e := strconv.ParseInt(n.Amount, 10, 64)
		if e != nil || amount == math.MaxInt64 {
			return paymentproto.Notification{}, false
		}
		n.Amount = strconv.FormatInt(amount+1, 10)
	case "duplicate_capture_other_transaction":
		hasAt = false
		if cap == nil {
			return paymentproto.Notification{}, false
		}
		n.ProviderTransactionID = strptr(ids.transaction)
	case "refund_unsupported":
		hasAt = false
		if cap == nil {
			return paymentproto.Notification{}, false
		}
		n.EventType = "refund.confirmed"
		n.RefundID = strptr(ids.refund)
	case "dispute_unsupported":
		hasAt = false
		if cap == nil {
			return paymentproto.Notification{}, false
		}
		n.EventType = "dispute.opened"
		n.CaseID = strptr(ids.caseRef)
	default:
		return paymentproto.Notification{}, false
	}
	if hasAt {
		if at.Year() < 1 || at.Year() > 9999 {
			return paymentproto.Notification{}, false
		}
		n.OccurredAt = timeText(at)
	}
	if _, e := paymentproto.CanonicalNotification(n); e != nil {
		return paymentproto.Notification{}, false
	}
	return n, true
}

func (r *Repository) insertEvent(ctx context.Context, tx pgx.Tx, in CanonicalControl, ids controlIDs, n paymentproto.Notification, captureID *string) error {
	raw, err := paymentproto.CanonicalNotification(n)
	if err != nil {
		return err
	}
	parsed, err := paymentproto.ParseNotification(raw)
	if err != nil || parsed.EnvironmentID != r.env.ID || parsed.MerchantID != r.env.MerchantID {
		return fail("mock_unavailable")
	}
	_, err = tx.Exec(ctx, `INSERT INTO mock_notification_events(id,event_id,merchant_order_id,event_type,canonical_body,business_digest,control_operation_id,canonical_capture_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, ids.eventRow, n.EventID, *in.request.MerchantOrderID, n.EventType, raw, paymentproto.RequestDigest(raw), in.request.OperationID, captureID)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO mock_notification_delivery(event_id) VALUES($1)`, ids.eventRow)
	}
	return err
}

func optionalCapture(ctx context.Context, tx pgx.Tx, order string) (*storedCapture, error) {
	c, e := readCapture(ctx, tx, order)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &c, nil
}
