package mockpsp

import (
	"bytes"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func scenarioFixture() (*Repository, storedOrder, *storedCapture, time.Time, controlIDs) {
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	expires := created.Add(15 * time.Minute)
	amount := int64(100)
	r := &Repository{env: paymentproto.Environment{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MerchantID: "merchant:test", PayeeLabel: "Test"}}
	o := storedOrder{id: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", amount: &amount, currency: "CNY", created: &created, expires: &expires, checkoutRef: strptr("checkout:dddddddd-dddd-4ddd-8ddd-dddddddddddd")}
	cap := &storedCapture{fact: paymentproto.CaptureFact{ProviderTransactionID: "txn:original", Amount: "99", Currency: "CNY", OccurredAt: timeText(created.Add(time.Minute)), ProviderOrderRevision: "2"}}
	ids := controlIDs{eventRef: "evt:test", transaction: "txn:new", refund: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", caseRef: "case:new"}
	return r, o, cap, created.Add(2 * time.Minute), ids
}

func TestScenarioExactFieldChanges(t *testing.T) {
	r, o, cap, now, ids := scenarioFixture()
	base := paymentproto.Notification{SchemaVersion: 1, EventID: ids.eventRef, EventType: "capture.confirmed", EnvironmentID: r.env.ID, MerchantID: r.env.MerchantID, OrderID: o.id, ProviderTransactionID: strptr("txn:original"), Amount: "99", Currency: "CNY", OccurredAt: cap.fact.OccurredAt, ProviderOrderRevision: "2"}
	tests := []struct {
		name   string
		change func(*paymentproto.Notification)
	}{
		{"capture_at_created", func(n *paymentproto.Notification) { n.OccurredAt = timeText(*o.created) }},
		{"capture_before_created_1us", func(n *paymentproto.Notification) { n.OccurredAt = "2026-10-31T23:59:59.999999Z" }},
		{"capture_before_expiry_1us", func(n *paymentproto.Notification) { n.OccurredAt = "2026-11-01T00:14:59.999999Z" }},
		{"capture_at_expiry", func(n *paymentproto.Notification) { n.OccurredAt = "2026-11-01T00:15:00.000000Z" }},
		{"capture_future_6s", func(n *paymentproto.Notification) { n.OccurredAt = "2026-11-01T00:02:06.000000Z" }},
		{"amount_plus_one", func(n *paymentproto.Notification) { n.Amount = "100" }},
		{"duplicate_capture_other_transaction", func(n *paymentproto.Notification) { n.ProviderTransactionID = strptr("txn:new") }},
		{"same_revision_conflict", func(n *paymentproto.Notification) { n.Amount = "100" }},
		{"refund_unsupported", func(n *paymentproto.Notification) { n.EventType = "refund.confirmed"; n.RefundID = strptr(ids.refund) }},
		{"dispute_unsupported", func(n *paymentproto.Notification) { n.EventType = "dispute.opened"; n.CaseID = strptr(ids.caseRef) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := base
			tt.change(&want)
			got, ok := r.scenarioEvent(o, cap, now, ids, tt.name)
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("scenario modified fields outside its contract: got=%+v want=%+v ok=%v", got, want, ok)
			}
			raw, e := paymentproto.CanonicalNotification(got)
			if e != nil {
				t.Fatal(e)
			}
			parsed, e := paymentproto.ParseNotification(raw)
			if e != nil {
				t.Fatal(e)
			}
			again, e := paymentproto.CanonicalNotification(parsed)
			if e != nil || !bytes.Equal(raw, again) {
				t.Fatal("scenario is not canonical")
			}
		})
	}
	if cap.fact.Amount != "99" || cap.fact.ProviderTransactionID != "txn:original" {
		t.Fatal("canonical source capture was changed")
	}
}

func TestScenarioRequiresCaptureAndDoesNotOverflow(t *testing.T) {
	r, o, cap, now, ids := scenarioFixture()
	for _, scenario := range []string{"duplicate_capture_other_transaction", "same_revision_conflict", "refund_unsupported", "dispute_unsupported"} {
		if _, ok := r.scenarioEvent(o, nil, now, ids, scenario); ok {
			t.Fatal("missing canonical capture accepted", scenario)
		}
	}
	n, ok := r.scenarioEvent(o, nil, now, ids, "amount_plus_one")
	if !ok || n.Amount != "101" || n.OccurredAt != timeText(now) || *n.ProviderTransactionID != ids.transaction {
		t.Fatal("uncaptured base did not use frozen order and current time")
	}
	max := int64(math.MaxInt64)
	o.amount = &max
	cap.fact.Amount = "9223372036854775807"
	for _, scenario := range []string{"amount_plus_one", "same_revision_conflict"} {
		if _, ok := r.scenarioEvent(o, cap, now, ids, scenario); ok {
			t.Fatal("amount overflow accepted", scenario)
		}
	}
	yearOne := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	o.created = &yearOne
	if _, ok := r.scenarioEvent(o, cap, now, ids, "capture_before_created_1us"); ok {
		t.Fatal("year zero accepted")
	}
	n, ok = r.scenarioEvent(o, cap, now, ids, "capture_at_created")
	if !ok || n.OccurredAt != "0001-01-01T00:00:00.000000Z" {
		t.Fatal("valid year-one instant was treated as missing")
	}
	late := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if _, ok := r.scenarioEvent(o, cap, late, ids, "capture_future_6s"); ok {
		t.Fatal("year 10000 accepted")
	}
	o.checkoutRef = nil
	if _, ok := r.scenarioEvent(o, cap, now, ids, "capture_at_expiry"); ok {
		t.Fatal("tombstone accepted")
	}
}

func TestFutureScenarioSignsButImmediateVerificationRejects(t *testing.T) {
	r, o, cap, now, ids := scenarioFixture()
	n, ok := r.scenarioEvent(o, cap, now, ids, "capture_future_6s")
	if !ok {
		t.Fatal("scenario rejected")
	}
	raw, e := paymentproto.CanonicalNotification(n)
	if e != nil {
		t.Fatal(e)
	}
	keys, e := paymentproto.NewKeyring("v1", []paymentproto.KeySpec{{ID: "v1", Key: bytes.Repeat([]byte{7}, 32), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}})
	if e != nil {
		t.Fatal(e)
	}
	h, e := keys.SignNotification(r.env, paymentproto.SignedHeaders{DeliveryID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", DeliveryTime: "1793491320"}, raw)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = keys.VerifyNotification(r.env, now, h, raw); e == nil {
		t.Fatal("immediate future+6s verification accepted")
	}
	if _, e = keys.VerifyNotification(r.env, now.Add(time.Second), h, raw); e != nil {
		t.Fatal("future scenario must become time-eligible after one second", e)
	}
}
