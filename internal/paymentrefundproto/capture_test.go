package paymentrefundproto_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	v1 "github.com/moni-ai/moni-aigate/internal/paymentproto"
	p "github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

func capturedQuery(t *testing.T, state string) v1.VerifiedQuery {
	t.Helper()
	q := v1.QueryRequest{SchemaVersion: 1, EnvironmentID: envID, MerchantID: merchantID, QueryID: queryID,
		CommandID: commandID, MerchantOrderID: merchantOrder, CommandKind: "cancel", ParameterDigest: strings.Repeat("a", 64)}
	r := v1.QueryReply{SchemaVersion: 1, QueryID: q.QueryID, EnvironmentID: q.EnvironmentID, MerchantID: q.MerchantID,
		MerchantOrderID: q.MerchantOrderID, CommandID: q.CommandID, CommandKind: q.CommandKind, ParameterDigest: q.ParameterDigest, CommandState: state}
	if state == "completed" {
		r.ProviderOrderRevision = ptr("2")
		r.Result = &v1.CommandResult{Status: "captured", Capture: &v1.CaptureFact{ProviderTransactionID: transactionID, Amount: "1000000", Currency: "CNY", OccurredAt: stamp, ProviderOrderRevision: "2"}}
	}
	raw, err := v1.CanonicalQueryReply(r)
	if err != nil {
		t.Fatal(err)
	}
	k := v1Keys(t)
	e := v1Env(captureNotification())
	h, err := k.SignQuery(e, q, v1.SignedHeaders{DeliveryID: deliveryID, DeliveryTime: fmt.Sprint(now().Unix())}, raw)
	if err != nil {
		t.Fatal(err)
	}
	v, err := k.VerifyQuery(e, q, now(), h, raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestCaptureBridgesHaveSameImmutableIdentity(t *testing.T) {
	a := capture(t)
	b, err := p.CaptureFromV1Query(capturedQuery(t, "completed"))
	if err != nil {
		t.Fatal(err)
	}
	ac, _ := a.Canonical()
	bc, _ := b.Canonical()
	ad, _ := a.Digest()
	bd, _ := b.Digest()
	if !bytes.Equal(ac, bc) || ad != bd {
		t.Fatal("same capture has divergent identity")
	}
	// Editing either bridge's caller-visible canonical cannot alter the value.
	ac[0] = '!'
	bc[0] = '!'
	again, _ := a.Canonical()
	if again[0] != '{' {
		t.Fatal("bridge retains caller bytes")
	}
	var zeroN v1.VerifiedNotification
	var zeroQ v1.VerifiedQuery
	if _, err = p.CaptureFromV1Notification(zeroN); err != p.ErrInvalid {
		t.Fatal("zero v1 notification")
	}
	if _, err = p.CaptureFromV1Query(zeroQ); err != p.ErrInvalid {
		t.Fatal("zero v1 query")
	}
	if _, err = p.CaptureFromV1Query(capturedQuery(t, "not_seen")); err != p.ErrInvalid {
		t.Fatal("not_seen capture")
	}
	if _, err = p.CaptureFromV1Query(capturedQuery(t, "in_progress")); err != p.ErrInvalid {
		t.Fatal("in progress capture")
	}
	for _, eventType := range []string{"refund.confirmed", "refund.failed", "dispute.opened", "chargeback.debited", "chargeback.reversed", "checkout.failed"} {
		n := captureNotification()
		n.EventType = eventType
		switch eventType {
		case "refund.confirmed", "refund.failed":
			n.RefundID = ptr(refundID)
		case "checkout.failed":
			n.ProviderTransactionID = nil
			n.FailureCode = ptr("checkout_rejected")
		default:
			n.CaseID = ptr("case:synthetic")
		}
		if _, err = p.CaptureFromV1Notification(v1Verified(t, n)); err != p.ErrInvalid {
			t.Fatalf("event %s became capture", eventType)
		}
	}
	// The bridge preserves a different authenticated environment; authorization
	// against this service's trusted environment occurs later, never in a global.
	n := captureNotification()
	n.EnvironmentID = otherID
	other, err := p.CaptureFromV1Notification(v1Verified(t, n))
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.NewRefundRequest(other, commandID, refundID, "1")
	if err != nil || r.EnvironmentID != otherID || r.MerchantOrderID != "mps1."+otherID+"."+orderID {
		t.Fatal("foreign bridge lost identity", err)
	}
	if _, err = p.NewRefundRequest(p.CaptureBinding{}, commandID, refundID, "1"); err != p.ErrInvalid {
		t.Fatal("zero bridge request")
	}
	var zero p.CaptureBinding
	if b, err := zero.Canonical(); b != nil || err != p.ErrInvalid {
		t.Fatal("zero bridge canonical")
	}
	if d, err := zero.Digest(); d != "" || err != p.ErrInvalid {
		t.Fatal("zero bridge digest")
	}
}
