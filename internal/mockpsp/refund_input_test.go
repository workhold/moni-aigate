package mockpsp

import (
	"testing"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

func TestRefundInputCanonicalizesAndBindsEnvironment(t *testing.T) {
	env := paymentproto.Environment{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MerchantID: "merchant:test", PayeeLabel: "Test"}
	raw := []byte(`{"schema_version":2,"environment_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","merchant_id":"merchant:test","command_kind":"refund","command_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","local_refund_id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","merchant_order_id":"mps1.bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.cccccccc-cccc-4ccc-8ccc-cccccccccccc","provider_transaction_id":"txn:original","original_capture_amount":"100","capture_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","amount":"25","currency":"CNY"}`)
	command, err := paymentrefundproto.ParseRefundRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = paymentrefundproto.CanonicalRefundRequest(command)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRefundCommandInput(env, raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parsed.Bytes()
	if err != nil || string(got) != string(raw) {
		t.Fatalf("canonical bytes changed: %v", err)
	}
	if _, err = ParseRefundCommandInput(paymentproto.Environment{ID: env.ID, MerchantID: "other", PayeeLabel: env.PayeeLabel}, raw); ErrorCode(err) != "invalid_request" {
		t.Fatalf("wrong environment accepted: %v", err)
	}
	query, err := paymentrefundproto.NewQueryRequest(command, "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "1")
	if err != nil {
		t.Fatal(err)
	}
	queryRaw, err := paymentrefundproto.CanonicalQueryRequest(query)
	if err != nil {
		t.Fatal(err)
	}
	q, err := ParseRefundQueryInput(env, queryRaw)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := q.Bytes(); err != nil || string(got) != string(queryRaw) {
		t.Fatalf("query canonical bytes changed: %v", err)
	}
}
