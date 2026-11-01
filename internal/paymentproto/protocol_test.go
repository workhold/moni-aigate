package paymentproto_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

const (
	envID              = "11111111-1111-4111-8111-111111111111"
	orderID            = "22222222-2222-4222-8222-222222222222"
	commandID          = "33333333-3333-4333-8333-333333333333"
	queryID            = "44444444-4444-4444-8444-444444444444"
	deliveryID         = "55555555-5555-4555-8555-555555555555"
	otherID            = "66666666-6666-4666-8666-666666666666"
	merchantOrder      = "mps1." + envID + "." + orderID
	notificationGolden = `{"schema_version":1,"event_id":"capture:one","event_type":"capture.confirmed","environment_id":"11111111-1111-4111-8111-111111111111","merchant_id":"merchant:qa","order_id":"22222222-2222-4222-8222-222222222222","provider_transaction_id":"transaction:one","amount":"1000000","currency":"CNY","occurred_at":"2026-10-31T23:59:59.000000Z","provider_order_revision":"2"}`
	queryGolden        = `{"schema_version":1,"query_id":"44444444-4444-4444-8444-444444444444","environment_id":"11111111-1111-4111-8111-111111111111","merchant_id":"merchant:qa","merchant_order_id":"mps1.11111111-1111-4111-8111-111111111111.22222222-2222-4222-8222-222222222222","command_id":"33333333-3333-4333-8333-333333333333","command_kind":"checkout","parameter_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","command_state":"not_seen","provider_order_revision":null,"result":null}`
)

func ptr(s string) *string { return &s }
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *p.Error
	if !errors.As(err, &e) || e.Code != code || err.Error() != code {
		t.Fatalf("want fixed %s, got %v", code, err)
	}
	if errors.Unwrap(err) != nil {
		t.Fatal("protocol error exposes a cause")
	}
}
func checkout() p.CheckoutRequest {
	return p.CheckoutRequest{SchemaVersion: 1, EnvironmentID: envID, MerchantID: "merchant:qa", CommandID: commandID, MerchantOrderID: merchantOrder, Amount: "1000000", Currency: "CNY", CreatedAt: "2026-11-01T00:00:00Z", ExpiresAt: "2026-11-01T00:15:00Z"}
}
func query() p.QueryRequest {
	return p.QueryRequest{SchemaVersion: 1, EnvironmentID: envID, MerchantID: "merchant:qa", QueryID: queryID, CommandID: commandID, MerchantOrderID: merchantOrder, CommandKind: "checkout", ParameterDigest: strings.Repeat("a", 64)}
}
func notification() p.Notification {
	return p.Notification{SchemaVersion: 1, EventID: "capture:one", EventType: "capture.confirmed", EnvironmentID: envID, MerchantID: "merchant:qa", OrderID: orderID, ProviderTransactionID: ptr("transaction:one"), Amount: "1000000", Currency: "CNY", OccurredAt: "2026-10-31T23:59:59Z", ProviderOrderRevision: "2"}
}
func queryReply() p.QueryReply {
	q := query()
	return p.QueryReply{SchemaVersion: 1, QueryID: q.QueryID, EnvironmentID: q.EnvironmentID, MerchantID: q.MerchantID, MerchantOrderID: q.MerchantOrderID, CommandID: q.CommandID, CommandKind: q.CommandKind, ParameterDigest: q.ParameterDigest, CommandState: "not_seen"}
}
func changed(raw []byte, key string, value any) []byte {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m[key] = value
	b, _ := json.Marshal(m)
	return b
}

func TestRequestCanonicalBytesAndLexicalRules(t *testing.T) {
	r := checkout()
	b, e := p.CanonicalCheckout(r)
	if e != nil {
		t.Fatal(e)
	}
	want := `{"schema_version":1,"environment_id":"` + envID + `","merchant_id":"merchant:qa","command_id":"` + commandID + `","merchant_order_id":"` + merchantOrder + `","amount":"1000000","currency":"CNY","created_at":"2026-11-01T00:00:00.000000Z","expires_at":"2026-11-01T00:15:00.000000Z"}`
	if string(b) != want || r.CreatedAt != "2026-11-01T00:00:00Z" {
		t.Fatal("canonical byte order/time differs or input was modified")
	}
	var unordered map[string]any
	_ = json.Unmarshal(b, &unordered)
	reorder, _ := json.MarshalIndent(unordered, "", " ")
	parsed, e := p.ParseCheckout(reorder)
	if e != nil {
		t.Fatal(e)
	}
	again, e := p.CanonicalCheckout(parsed)
	if e != nil || !bytes.Equal(b, again) {
		t.Fatal("equivalent request JSON did not converge", e)
	}
	if id, e := p.MerchantOrderID(envID, orderID); e != nil || id != merchantOrder || len(id) != 78 {
		t.Fatal("merchant order encoding", e)
	}
	if env, order, e := p.ParseMerchantOrderID(merchantOrder); e != nil || env != envID || order != orderID {
		t.Fatal("merchant order round trip", e)
	}
	for _, bad := range []string{strings.ToUpper(merchantOrder), merchantOrder + "x", "mps1." + envID + ".00000000-0000-0000-0000-000000000000", ""} {
		_, _, e := p.ParseMerchantOrderID(bad)
		assertCode(t, e, "invalid_request")
	}
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"schema_version", nil}, {"schema_version", 1.0 + 0.1}, {"schema_version", "1"}, {"amount", 0}, {"amount", nil}, {"amount", "0"}, {"amount", "01"}, {"amount", "+1"}, {"amount", "1e3"}, {"amount", "9223372036854775808"}, {"amount", "١"},
		{"command_id", "AAAAAAAA-3333-4333-8333-333333333333"}, {"command_id", "00000000-0000-0000-0000-000000000000"}, {"environment_id", otherID}, {"merchant_id", "https://private.invalid"}, {"merchant_id", "sk-private"}, {"merchant_id", "merchant\nqa"}, {"currency", "USD"},
		{"created_at", "2026-11-01T00:00:00+00:00"}, {"created_at", "2026-11-01T00:00:00,1Z"}, {"created_at", "2026-11-01T00:00:00.0000001Z"}, {"created_at", "2026-11-01T00:00:60Z"}, {"created_at", "0000-11-01T00:00:00Z"}, {"expires_at", "2026-11-01T00:15:00.000001Z"}, {"extra", true},
	} {
		_, e := p.ParseCheckout(changed(b, tc.key, tc.value))
		assertCode(t, e, "invalid_request")
	}
	// Noninteger lexical schema encodings are rejected even when numerically 1.
	for _, n := range []string{"1.0", "1e0"} {
		_, e := p.ParseCheckout([]byte(strings.Replace(string(b), `"schema_version":1`, `"schema_version":`+n, 1)))
		assertCode(t, e, "invalid_request")
	}
	for _, amount := range []string{"1", "9223372036854775807"} {
		good := r
		good.Amount = amount
		if _, e := p.CanonicalCheckout(good); e != nil {
			t.Fatal("valid int64 amount rejected", e)
		}
	}
	for _, fraction := range []string{".1", ".12", ".123", ".1234", ".12345", ".123456"} {
		good := r
		good.CreatedAt = "2026-11-01T00:00:00" + fraction + "Z"
		good.ExpiresAt = "2026-11-01T00:15:00" + fraction + "Z"
		if _, e := p.CanonicalCheckout(good); e != nil {
			t.Fatal("valid microsecond precision rejected", e)
		}
	}
}

func TestAllParsersRejectDuplicateAliasesTrailingAndInvalidUnicode(t *testing.T) {
	cancel := p.CancelRequest{SchemaVersion: 1, EnvironmentID: envID, MerchantID: "merchant:qa", CommandID: commandID, MerchantOrderID: merchantOrder}
	cancelBody, _ := p.CanonicalCancel(cancel)
	checkoutBody, _ := p.CanonicalCheckout(checkout())
	queryBody, _ := p.CanonicalQuery(query())
	commandBody, _ := p.CanonicalCommandReply(p.CommandReply{SchemaVersion: 1, EnvironmentID: envID, MerchantID: "merchant:qa", MerchantOrderID: merchantOrder, CommandID: commandID, CommandKind: "checkout", ParameterDigest: strings.Repeat("a", 64), CommandState: "in_progress"})
	for _, tc := range []struct {
		name  string
		raw   []byte
		parse func([]byte) error
		limit int
	}{
		{"checkout", checkoutBody, func(b []byte) error { _, e := p.ParseCheckout(b); return e }, 8192},
		{"cancel", cancelBody, func(b []byte) error { _, e := p.ParseCancel(b); return e }, 8192},
		{"query", queryBody, func(b []byte) error { _, e := p.ParseQuery(b); return e }, 8192},
		{"notification", []byte(notificationGolden), func(b []byte) error { _, e := p.ParseNotification(b); return e }, 65536},
		{"command_reply", commandBody, func(b []byte) error { _, e := p.ParseCommandReply(b); return e }, 65536},
		{"query_reply", []byte(queryGolden), func(b []byte) error { _, e := p.ParseQueryReply(b); return e }, 65536},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, raw := range [][]byte{
				[]byte("null"), []byte("[]"), []byte("{}"), append(append([]byte{}, tc.raw...), []byte(` {}`)...),
				[]byte(strings.Replace(string(tc.raw), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1)),
				[]byte(strings.Replace(string(tc.raw), `"schema_version":1`, `"schema_version":1,"\u0073chema_version":1`, 1)),
				[]byte(strings.Replace(string(tc.raw), `"schema_version"`, `"Schema_version"`, 1)),
				[]byte(strings.Replace(string(tc.raw), `"merchant:qa"`, `"\ud800"`, 1)),
				[]byte(strings.Replace(string(tc.raw), `"merchant:qa"`, `"\udc00"`, 1)),
				[]byte(strings.Replace(string(tc.raw), `"merchant:qa"`, `"\ud800\u0041"`, 1)),
				bytes.Replace(tc.raw, []byte("merchant:qa"), []byte{0xff}, 1), append(append([]byte{}, tc.raw...), bytes.Repeat([]byte(" "), tc.limit)...),
			} {
				assertCode(t, tc.parse(raw), "invalid_request")
			}
			// A paired non-BMP Unicode letter is valid, rather than all escapes being rejected.
			good := []byte(strings.Replace(string(tc.raw), `"merchant:qa"`, `"\ud801\udc00"`, 1))
			if e := tc.parse(good); e != nil {
				t.Fatal("valid surrogate pair rejected", e)
			}
		})
	}
	if _, e := p.CanonicalQuery(p.QueryRequest{}); e == nil {
		t.Fatal("zero request accepted")
	}
	if _, e := p.CanonicalCancel(p.CancelRequest{}); e == nil {
		t.Fatal("zero cancel accepted")
	}
}

func TestNotificationUnionAndCanonicalDigests(t *testing.T) {
	b, e := p.CanonicalNotification(notification())
	if e != nil || string(b) != notificationGolden {
		t.Fatal("notification fixed field order/time differs", e)
	}
	if p.RequestDigest(b) != "a740fe8908ffb7d40b5cf58ca2fb399fd5bf3392c9d634040a9ad5700c50f377" {
		t.Fatal("independent notification SHA-256 vector differs")
	}
	for _, typ := range []string{"capture.confirmed", "checkout.failed", "refund.confirmed", "refund.failed", "dispute.opened", "chargeback.debited", "chargeback.reversed"} {
		n := notification()
		n.EventType = typ
		switch typ {
		case "checkout.failed":
			n.ProviderTransactionID = nil
			n.FailureCode = ptr("order_expired")
		case "refund.confirmed", "refund.failed":
			n.RefundID = ptr(otherID)
		case "dispute.opened", "chargeback.debited", "chargeback.reversed":
			n.CaseID = ptr("case:one")
		}
		raw, e := p.CanonicalNotification(n)
		if e != nil {
			t.Fatal("valid union", typ, e)
		}
		parsed, e := p.ParseNotification(raw)
		if e != nil {
			t.Fatal(e)
		}
		again, e := p.CanonicalNotification(parsed)
		if e != nil || !bytes.Equal(raw, again) {
			t.Fatal("union round trip", e)
		}
		for _, field := range []string{"failure_code", "refund_id", "case_id"} {
			var object map[string]any
			_ = json.Unmarshal(raw, &object)
			if _, exists := object[field]; !exists {
				object[field] = nil
				bad, _ := json.Marshal(object)
				_, e := p.ParseNotification(bad)
				assertCode(t, e, "invalid_request")
			}
		}
	}
	for _, mutate := range []func(*p.Notification){
		func(n *p.Notification) { n.ProviderTransactionID = nil }, func(n *p.Notification) { n.FailureCode = ptr("order_closed") }, func(n *p.Notification) { n.CaseID = ptr("case:one") }, func(n *p.Notification) { n.RefundID = ptr(otherID) }, func(n *p.Notification) { n.EventType = "capture.failed" }, func(n *p.Notification) { n.ProviderOrderRevision = "0" }, func(n *p.Notification) { n.ProviderOrderRevision = "9223372036854775808" }, func(n *p.Notification) { n.EventID = "https://private.invalid" },
	} {
		n := notification()
		mutate(&n)
		_, e := p.CanonicalNotification(n)
		assertCode(t, e, "invalid_request")
	}
}

func TestRepliesRequireCompleteNullableTypedResults(t *testing.T) {
	r := queryReply()
	b, e := p.CanonicalQueryReply(r)
	if e != nil || string(b) != queryGolden {
		t.Fatal("query byte vector", e)
	}
	if p.RequestDigest(b) != "345fe6ab19f86d2ba3f9035f734fcfd87430587fc6470196e3247358952107d6" {
		t.Fatal("independent snapshot digest differs")
	}
	for _, state := range []string{"not_seen", "in_progress"} {
		good := r
		good.CommandState = state
		if _, e := p.CanonicalQueryReply(good); e != nil {
			t.Fatal(e)
		}
		good.ProviderOrderRevision = ptr("1")
		_, e := p.CanonicalQueryReply(good)
		assertCode(t, e, "invalid_request")
	}
	for _, result := range []*p.CommandResult{
		{Status: "ready", CheckoutRef: ptr("checkout:" + otherID)},
		{Status: "rejected", FailureCode: ptr("order_expired")},
		{Status: "closed", ClosedAt: ptr("2026-11-01T00:00:00Z")},
		{Status: "captured", Capture: &p.CaptureFact{ProviderTransactionID: "transaction:one", Amount: "1", Currency: "CNY", OccurredAt: "2026-11-01T00:00:00Z", ProviderOrderRevision: "2"}},
	} {
		good := r
		good.CommandState = "completed"
		good.ProviderOrderRevision = ptr("2")
		good.Result = result
		body, e := p.CanonicalQueryReply(good)
		if e != nil {
			t.Fatal("valid typed result", e)
		}
		if _, e = p.ParseQueryReply(body); e != nil {
			t.Fatal(e)
		}
		bad := good
		copyResult := *result
		copyResult.FailureCode = ptr("not_allowed")
		bad.Result = &copyResult
		_, e = p.CanonicalQueryReply(bad)
		assertCode(t, e, "invalid_request")
		var obj map[string]any
		_ = json.Unmarshal(body, &obj)
		resultObject := obj["result"].(map[string]any)
		delete(resultObject, "closed_at")
		missing, _ := json.Marshal(obj)
		_, e = p.ParseQueryReply(missing)
		assertCode(t, e, "invalid_request")
		if result.Status == "closed" || result.Status == "captured" {
			_, e = p.ParseQueryReply([]byte(strings.Replace(string(body), ".000000Z", "Z", 1)))
			assertCode(t, e, "invalid_request")
		}
		if result.Status == "captured" {
			_ = json.Unmarshal(body, &obj)
			obj["result"].(map[string]any)["capture"].(map[string]any)["extra"] = nil
			badBody, _ := json.Marshal(obj)
			_, e = p.ParseQueryReply(badBody)
			assertCode(t, e, "invalid_request")
		}
	}
	for _, raw := range [][]byte{changed(b, "result", map[string]any{}), changed(b, "provider_order_revision", "1"), changed(b, "result", false), changed(b, "command_state", "paid")} {
		_, e := p.ParseQueryReply(raw)
		assertCode(t, e, "invalid_request")
	}
	command := p.CommandReply{SchemaVersion: 1, EnvironmentID: envID, MerchantID: "merchant:qa", MerchantOrderID: merchantOrder, CommandID: commandID, CommandKind: "cancel", ParameterDigest: strings.Repeat("a", 64), CommandState: "completed", ProviderOrderRevision: ptr("2"), Result: &p.CommandResult{Status: "ready", CheckoutRef: ptr("checkout:" + otherID)}}
	_, e = p.CanonicalCommandReply(command)
	assertCode(t, e, "invalid_request")
	command.CommandState = "not_seen"
	command.Result = nil
	command.ProviderOrderRevision = nil
	_, e = p.CanonicalCommandReply(command)
	assertCode(t, e, "invalid_request")
}
