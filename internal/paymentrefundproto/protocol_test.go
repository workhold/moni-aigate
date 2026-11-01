package paymentrefundproto_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	v1 "github.com/moni-ai/moni-aigate/internal/paymentproto"
	p "github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

const (
	envID         = "11111111-1111-1111-1111-111111111111"
	orderID       = "22222222-2222-2222-2222-222222222222"
	commandID     = "33333333-3333-3333-3333-333333333333"
	refundID      = "44444444-4444-4444-4444-444444444444"
	queryID       = "55555555-5555-5555-5555-555555555555"
	factID        = "66666666-6666-6666-6666-666666666666"
	eventID       = "77777777-7777-7777-7777-777777777777"
	deliveryID    = "88888888-8888-8888-8888-888888888888"
	otherID       = "99999999-9999-9999-9999-999999999999"
	merchantID    = "商户:测试_α/01"
	transactionID = "交易:合成/001"
	merchantOrder = "mps1." + envID + "." + orderID
	stamp         = "2026-01-02T03:04:05.123456Z"
)

func ptr(s string) *string { return &s }
func now() time.Time       { t, _ := time.Parse(time.RFC3339Nano, stamp); return t.Add(10 * time.Second) }
func env() p.Environment   { return p.Environment{ID: envID, MerchantID: merchantID} }
func requireError(t *testing.T, err, want error) {
	t.Helper()
	if err != want || !errors.Is(err, want) || errors.Unwrap(err) != nil {
		t.Fatalf("want fixed %v, got %v", want, err)
	}
}
func captureNotification() v1.Notification {
	return v1.Notification{SchemaVersion: 1, EventID: "capture:synthetic", EventType: "capture.confirmed", EnvironmentID: envID,
		MerchantID: merchantID, OrderID: orderID, ProviderTransactionID: ptr(transactionID), Amount: "1000000", Currency: "CNY", OccurredAt: stamp, ProviderOrderRevision: "2"}
}
func v1Keys(t *testing.T) *v1.Keyring {
	t.Helper()
	k, err := v1.NewKeyring("v1", []v1.KeySpec{{ID: "v1", Key: bytes.Repeat([]byte{41}, 32), NotBefore: now().Add(-time.Hour), NotAfter: now().Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	return k
}
func v1Env(n v1.Notification) v1.Environment {
	return v1.Environment{ID: n.EnvironmentID, MerchantID: n.MerchantID, PayeeLabel: "synthetic payee"}
}
func v1Verified(t *testing.T, n v1.Notification) v1.VerifiedNotification {
	t.Helper()
	raw, err := v1.CanonicalNotification(n)
	if err != nil {
		t.Fatal(err)
	}
	k := v1Keys(t)
	h, err := k.SignNotification(v1Env(n), v1.SignedHeaders{DeliveryID: deliveryID, DeliveryTime: fmt.Sprint(now().Unix())}, raw)
	if err != nil {
		t.Fatal(err)
	}
	v, err := k.VerifyNotification(v1Env(n), now(), h, raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func capture(t *testing.T) p.CaptureBinding {
	t.Helper()
	c, err := p.CaptureFromV1Notification(v1Verified(t, captureNotification()))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func request(t *testing.T) p.RefundRequest {
	t.Helper()
	r, err := p.NewRefundRequest(capture(t), commandID, refundID, "125000")
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func query(t *testing.T) p.QueryRequest {
	t.Helper()
	q, err := p.NewQueryRequest(request(t), queryID, "7")
	if err != nil {
		t.Fatal(err)
	}
	return q
}
func factFor(t *testing.T, r p.RefundRequest) p.RefundFact {
	t.Helper()
	d, err := p.ParameterDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	return p.RefundFact{SchemaVersion: 2, FactID: factID, EnvironmentID: r.EnvironmentID, MerchantID: r.MerchantID,
		LocalRefundID: r.LocalRefundID, CommandID: r.CommandID, ParameterDigest: d, MerchantOrderID: r.MerchantOrderID,
		ProviderTransactionID: r.ProviderTransactionID, OriginalCaptureAmount: r.OriginalCaptureAmount, CaptureDigest: r.CaptureDigest,
		Amount: r.Amount, Currency: r.Currency, Outcome: "confirmed", ProviderRefundReference: ptr("refund:synthetic/001"), CaptureRefundRevision: "1", CompletedAt: stamp}
}
func fact(t *testing.T) p.RefundFact { return factFor(t, request(t)) }
func notification(t *testing.T) p.Notification {
	return p.Notification{SchemaVersion: 2, EventID: eventID, EventType: "refund.confirmed", Fact: fact(t)}
}
func commandReply(t *testing.T) p.CommandReply {
	r := request(t)
	f := factFor(t, r)
	return p.CommandReply{SchemaVersion: 2, Command: r, ParameterDigest: f.ParameterDigest, CommandState: "completed", Result: &f}
}
func queryReply(t *testing.T) p.QueryReply {
	q := query(t)
	f := factFor(t, q.Command)
	return p.QueryReply{SchemaVersion: 2, QueryID: q.QueryID, MutationGeneration: q.MutationGeneration, Command: q.Command,
		ParameterDigest: q.ParameterDigest, CommandState: "completed", Result: &f}
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func change(t *testing.T, raw []byte, field string, value any) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		t.Fatal("bad fixture")
	}
	if value == nil {
		delete(m, field)
	} else {
		m[field] = mustJSON(t, value)
	}
	return mustJSON(t, m)
}

type codecCase struct {
	name           string
	raw            []byte
	parseCanonical func([]byte) ([]byte, error)
}

func codecs(t *testing.T) []codecCase {
	t.Helper()
	return []codecCase{
		{"request", mustJSON(t, request(t)), func(b []byte) ([]byte, error) {
			v, e := p.ParseRefundRequest(b)
			if e != nil {
				return nil, e
			}
			return p.CanonicalRefundRequest(v)
		}},
		{"query", mustJSON(t, query(t)), func(b []byte) ([]byte, error) {
			v, e := p.ParseQueryRequest(b)
			if e != nil {
				return nil, e
			}
			return p.CanonicalQueryRequest(v)
		}},
		{"fact", mustJSON(t, fact(t)), func(b []byte) ([]byte, error) {
			v, e := p.ParseRefundFact(b)
			if e != nil {
				return nil, e
			}
			return p.CanonicalRefundFact(v)
		}},
		{"notification", mustJSON(t, notification(t)), func(b []byte) ([]byte, error) {
			v, e := p.ParseNotification(b)
			if e != nil {
				return nil, e
			}
			return p.CanonicalNotification(v)
		}},
		{"command_reply", mustJSON(t, commandReply(t)), func(b []byte) ([]byte, error) {
			v, e := p.ParseCommandReply(b)
			if e != nil {
				return nil, e
			}
			return p.CanonicalCommandReply(v)
		}},
		{"query_reply", mustJSON(t, queryReply(t)), func(b []byte) ([]byte, error) {
			v, e := p.ParseQueryReply(b)
			if e != nil {
				return nil, e
			}
			return p.CanonicalQueryReply(v)
		}},
	}
}

func TestAllWireCodecsStrictAndCanonical(t *testing.T) {
	for _, c := range codecs(t) {
		t.Run(c.name, func(t *testing.T) {
			b, err := c.parseCanonical(c.raw)
			if err != nil || !bytes.Equal(b, c.raw) {
				t.Fatalf("canonical changed: %v", err)
			}
			var m map[string]any
			_ = json.Unmarshal(c.raw, &m)
			shuffled, _ := json.MarshalIndent(m, "", "  ")
			b, err = c.parseCanonical(shuffled)
			if err != nil || !bytes.Equal(b, c.raw) {
				t.Fatalf("unordered did not converge: %v", err)
			}
			for field := range m {
				if b, e := c.parseCanonical(change(t, c.raw, field, nil)); b != nil || e != p.ErrInvalid {
					t.Fatalf("missing required member %s", field)
				}
			}
			bad := [][]byte{nil, []byte("null"), []byte("[]"), []byte(`"x"`), append([]byte{0xef, 0xbb, 0xbf}, c.raw...), append(append([]byte(nil), c.raw...), []byte(" {}")...), []byte(strings.Replace(string(c.raw), `"schema_version":2`, `"schema_version":2,"schema_version":2`, 1)), []byte(strings.Replace(string(c.raw), `"schema_version":2`, `"schema_version":2,"schema_\u0076ersion":2`, 1)), change(t, c.raw, "schema_version", nil), change(t, c.raw, "Schema_Version", 2), change(t, c.raw, "extra", true), change(t, c.raw, "schema_version", nil), []byte(strings.Replace(string(c.raw), `"schema_version":2`, `"schema_version":null`, 1)), []byte(strings.Replace(string(c.raw), `"schema_version":2`, `"schema_version":[]`, 1))}
			for _, version := range []string{`"2"`, `2.0`, `2e0`, `1`, `0`, `-2`, `true`} {
				bad = append(bad, []byte(strings.Replace(string(c.raw), `"schema_version":2`, `"schema_version":`+version, 1)))
			}
			for _, invalid := range []string{`\ud800`, `\udc00`, `\ud800\u0041`, `\ud800x`, `\uZZZZ`, `\q`} {
				bad = append(bad, []byte(strings.Replace(string(c.raw), `"schema_version":2`, `"schema_version":2,"bad":"`+invalid+`"`, 1)))
			}
			bad = append(bad, append([]byte{0xff}, c.raw...), change(t, c.raw, "deep", json.RawMessage(strings.Repeat(`{"x":`, 9)+`null`+strings.Repeat("}", 9))), append(append([]byte(nil), c.raw...), bytes.Repeat([]byte(" "), 64<<10)...))
			for i, badBody := range bad {
				out, err := c.parseCanonical(badBody)
				if err != p.ErrInvalid || out != nil {
					t.Fatalf("bad case %d yielded bytes=%d error=%v", i, len(out), err)
				}
			}
		})
	}
}

func TestNestedObjectsRemainClosedAndBodyLimits(t *testing.T) {
	for _, c := range codecs(t) {
		if c.name == "request" || c.name == "fact" {
			continue
		}
		// Change only a nested schema token. A typed int decode must not erase
		// the distinction between literal 2, 2.0, and 2e0 at any object level.
		at := bytes.Index(c.raw[2:], []byte(`"schema_version":2`))
		if at < 0 {
			t.Fatal("nested schema fixture")
		}
		at += 2
		for _, version := range []string{"2.0", "2e0", `"2"`, "1"} {
			raw := append([]byte(nil), c.raw[:at]...)
			raw = append(raw, []byte(strings.Replace(string(c.raw[at:]), `"schema_version":2`, `"schema_version":`+version, 1))...)
			if out, err := c.parseCanonical(raw); out != nil || err != p.ErrInvalid {
				t.Fatalf("nested %s version %s", c.name, version)
			}
		}
		raw := append([]byte(nil), c.raw[:at]...)
		raw = append(raw, []byte(strings.Replace(string(c.raw[at:]), `"schema_version":2`, `"schema_version":2,"schema_\u0076ersion":2`, 1))...)
		if out, err := c.parseCanonical(raw); out != nil || err != p.ErrInvalid {
			t.Fatal("nested escaped duplicate")
		}
	}
	for _, c := range codecs(t) {
		limit := 64 << 10
		if c.name == "request" || c.name == "query" {
			limit = 8 << 10
		}
		raw := append(append([]byte(nil), c.raw...), bytes.Repeat([]byte(" "), limit-len(c.raw))...)
		if _, err := c.parseCanonical(raw); err != nil {
			t.Fatal("inclusive body byte limit", c.name, err)
		}
		if out, err := c.parseCanonical(append(raw, ' ')); out != nil || err != p.ErrInvalid {
			t.Fatal("exceeded byte limit", c.name)
		}
	}
	r := request(t)
	// A valid escaped non-BMP letter remains a valid v1 reference and converges
	// to its UTF8 bytes; rejecting all surrogate pairs would break the bridge.
	r.MerchantID = "𐐀"
	encoded := mustJSON(t, r)
	escaped := []byte(strings.Replace(string(encoded), "𐐀", `\ud801\udc00`, 1))
	parsed, err := p.ParseRefundRequest(escaped)
	if err != nil || parsed.MerchantID != "𐐀" {
		t.Fatal("valid surrogate pair", err)
	}
	canonical, err := p.CanonicalRefundRequest(parsed)
	if err != nil || !bytes.Equal(canonical, encoded) {
		t.Fatal("surrogate canonical", err)
	}
}

func TestNullUnionsAndOriginalCommandBinding(t *testing.T) {
	f := fact(t)
	valid := []p.RefundFact{f}
	for _, reason := range []string{"refund_limit_exceeded", "refund_policy_rejected", "capture_not_refundable"} {
		x := f
		x.Outcome = "final_no_effect"
		x.ProviderRefundReference = nil
		x.FailureCode = ptr(reason)
		valid = append(valid, x)
	}
	for _, x := range valid {
		b, err := p.CanonicalRefundFact(x)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := p.ParseRefundFact(b)
		if err != nil || parsed.Outcome != x.Outcome {
			t.Fatal(err)
		}
		for _, field := range []string{"provider_refund_reference", "failure_code"} {
			if _, err = p.ParseRefundFact(change(t, b, field, nil)); err != p.ErrInvalid {
				t.Fatal("missing nullable accepted")
			}
		}
	}
	bad := []p.RefundFact{}
	for _, alter := range []func(*p.RefundFact){
		func(x *p.RefundFact) { x.ProviderRefundReference = nil }, func(x *p.RefundFact) { x.FailureCode = ptr("refund_policy_rejected") },
		func(x *p.RefundFact) { x.Outcome = "final_no_effect" }, func(x *p.RefundFact) { x.Outcome = "refund.failed" },
		func(x *p.RefundFact) { x.ParameterDigest = strings.Repeat("a", 64) }, func(x *p.RefundFact) { x.CaptureRefundRevision = "0" },
		func(x *p.RefundFact) {
			x.Outcome = "final_no_effect"
			x.ProviderRefundReference = nil
			x.FailureCode = ptr("transport_timeout")
		},
	} {
		x := f
		alter(&x)
		bad = append(bad, x)
	}
	for _, x := range bad {
		if b, e := p.CanonicalRefundFact(x); e != p.ErrInvalid || b != nil {
			t.Fatal("bad fact accepted", e)
		}
	}
	for _, state := range []string{"in_progress", "completed"} {
		r := commandReply(t)
		r.CommandState = state
		if state == "in_progress" {
			r.Result = nil
		}
		if _, e := p.CanonicalCommandReply(r); e != nil {
			t.Fatal(e)
		}
		r.Result = &f
		if state == "in_progress" {
			if _, e := p.CanonicalCommandReply(r); e != p.ErrInvalid {
				t.Fatal("in progress fact")
			}
		}
	}
	for _, state := range []string{"not_seen", "in_progress", "completed"} {
		r := queryReply(t)
		r.CommandState = state
		if state != "completed" {
			r.Result = nil
		}
		if _, e := p.CanonicalQueryReply(r); e != nil {
			t.Fatal(e)
		}
		if state != "completed" {
			r.Result = &f
		} else {
			r.Result = nil
		}
		if _, e := p.CanonicalQueryReply(r); e != p.ErrInvalid {
			t.Fatal("state/result mismatch")
		}
	}
	r := commandReply(t)
	r.CommandState = "not_seen"
	r.Result = nil
	if _, e := p.CanonicalCommandReply(r); e != p.ErrInvalid {
		t.Fatal("command not_seen")
	}
	n := notification(t)
	n.EventType = "refund.final_no_effect"
	if _, e := p.CanonicalNotification(n); e != p.ErrInvalid {
		t.Fatal("event union")
	}
	r = commandReply(t)
	other := r.Command
	other.LocalRefundID = otherID
	r.Result = ptrFact(factFor(t, other))
	if _, e := p.CanonicalCommandReply(r); e != p.ErrInvalid {
		t.Fatal("cross command fact")
	}
	q := query(t)
	q.ParameterDigest = strings.Repeat("a", 64)
	if _, e := p.CanonicalQueryRequest(q); e != p.ErrInvalid {
		t.Fatal("wrong command digest")
	}
}
func ptrFact(f p.RefundFact) *p.RefundFact { return &f }

func TestLexicalValuesAndUTCBoundaries(t *testing.T) {
	r := request(t)
	for _, amount := range []string{"1", "1000000", "9223372036854775807"} {
		x := r
		x.Amount = amount
		x.OriginalCaptureAmount = "9223372036854775807"
		if _, e := p.CanonicalRefundRequest(x); e != nil {
			t.Fatal(e)
		}
	}
	for _, amount := range []string{"", "0", "-1", "+1", "01", "1.0", "1e0", " 1", "9223372036854775808"} {
		x := r
		x.Amount = amount
		if b, e := p.CanonicalRefundRequest(x); e != p.ErrInvalid || b != nil {
			t.Fatalf("amount %q", amount)
		}
	}
	x := r
	x.Amount = "1000001"
	if _, e := p.CanonicalRefundRequest(x); e != p.ErrInvalid {
		t.Fatal("over original")
	}
	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA", "33333333333333333333333333333333", "33333333-3333-3333-3333-33333333333g"} {
		x := r
		x.CommandID = id
		if _, e := p.CanonicalRefundRequest(x); e != p.ErrInvalid {
			t.Fatal("bad UUID")
		}
	}
	x = r
	x.CommandID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if _, e := p.CanonicalRefundRequest(x); e != nil {
		t.Fatal("invented variant restriction")
	}
	for _, ref := range []string{":merchant", "._:/-", "𐐀:交易", strings.Repeat("界", 33)} {
		x := r
		x.MerchantID = ref
		if _, e := p.CanonicalRefundRequest(x); e != nil {
			t.Fatal("valid v1 reference", e)
		}
	}
	for _, ref := range []string{"", strings.Repeat("界", 34), "https://merchant", "SK-secret", "Mg_token", "bEaReRfoo", "white space", "e\u0301", "😀"} {
		x := r
		x.MerchantID = ref
		if _, e := p.CanonicalRefundRequest(x); e != p.ErrInvalid {
			t.Fatalf("bad v1 reference %q", ref)
		}
	}
	for _, ref := range []string{"1", "A:b._/-", strings.Repeat("a", 100)} {
		x := fact(t)
		x.ProviderRefundReference = ptr(ref)
		if _, e := p.CanonicalRefundFact(x); e != nil {
			t.Fatal("valid refund reference", e)
		}
	}
	for _, ref := range []string{"", "_first", ":first", "商户", "https://provider", "SK-key", "MG_key", "BEARERtoken", strings.Repeat("a", 101)} {
		x := fact(t)
		x.ProviderRefundReference = ptr(ref)
		if _, e := p.CanonicalRefundFact(x); e != p.ErrInvalid {
			t.Fatalf("bad refund reference %q", ref)
		}
	}
	for _, stamp := range []string{"0001-01-01T00:00:00.000000Z", "9999-12-31T23:59:59.999999Z"} {
		x := fact(t)
		x.CompletedAt = stamp
		if _, e := p.CanonicalRefundFact(x); e != nil {
			t.Fatal("valid UTC6", e)
		}
	}
	for _, stamp := range []string{"0000-01-01T00:00:00.000000Z", "2026-01-02T03:04:05Z", "2026-01-02T03:04:05.12345Z", "2026-01-02T03:04:05.1234567Z", "2026-01-02T03:04:05,123456Z", "2026-01-02T03:04:05.123456+00:00", "2026-01-02T03:04:60.000000Z"} {
		x := fact(t)
		x.CompletedAt = stamp
		if _, e := p.CanonicalRefundFact(x); e != p.ErrInvalid {
			t.Fatalf("bad UTC6 %q", stamp)
		}
	}
}
