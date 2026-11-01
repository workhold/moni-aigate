package paymentrefundproto_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	p "github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

func FuzzWireRoundTrips(f *testing.F) {
	f.Add([]byte(`{"schema_version":2,"environment_id":"` + envID + `","merchant_id":"merchant","command_kind":"refund","command_id":"` + commandID + `","local_refund_id":"` + refundID + `","merchant_order_id":"` + merchantOrder + `","provider_transaction_id":"transaction","original_capture_amount":"1000000","capture_digest":"` + strings.Repeat("a", 64) + `","amount":"1","currency":"CNY"}`))
	f.Add([]byte(`{"schema_version":2,"schema_\u0076ersion":2}`))
	f.Add([]byte(`{"fact":{"schema_version":2.0},"bad":"\ud800"}`))
	f.Add([]byte("[]"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		parsers := []func([]byte) ([]byte, error){
			func(b []byte) ([]byte, error) {
				v, e := p.ParseRefundRequest(b)
				if e != nil {
					return nil, e
				}
				return p.CanonicalRefundRequest(v)
			},
			func(b []byte) ([]byte, error) {
				v, e := p.ParseQueryRequest(b)
				if e != nil {
					return nil, e
				}
				return p.CanonicalQueryRequest(v)
			},
			func(b []byte) ([]byte, error) {
				v, e := p.ParseRefundFact(b)
				if e != nil {
					return nil, e
				}
				return p.CanonicalRefundFact(v)
			},
			func(b []byte) ([]byte, error) {
				v, e := p.ParseNotification(b)
				if e != nil {
					return nil, e
				}
				return p.CanonicalNotification(v)
			},
			func(b []byte) ([]byte, error) {
				v, e := p.ParseCommandReply(b)
				if e != nil {
					return nil, e
				}
				return p.CanonicalCommandReply(v)
			},
			func(b []byte) ([]byte, error) {
				v, e := p.ParseQueryReply(b)
				if e != nil {
					return nil, e
				}
				return p.CanonicalQueryReply(v)
			},
		}
		for _, parse := range parsers {
			out, e := parse(raw)
			if e != nil {
				if e != p.ErrInvalid || out != nil {
					t.Fatal("unsafe codec error")
				}
				continue
			}
			again, e := parse(out)
			if e != nil || !bytes.Equal(out, again) {
				t.Fatal("canonical not fixed point")
			}
		}
	})
}

func FuzzAuthenticatedNotification(f *testing.F) {
	f.Add([]byte(`{"schema_version":2,"event_id":"` + eventID + `","event_type":"refund.confirmed","fact":null}`))
	f.Add([]byte(`{"schema_version":2,"bad":"\ud800"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			return
		}
		k, e := p.NewKeyring("refund_v2", []p.KeySpec{spec("refund_v2", keyBytes())})
		if e != nil {
			t.Fatal(e)
		}
		h := independentHeaders(raw, notificationDomain, "", unsigned())
		v, e := k.VerifyNotification(env(), now(), h, raw)
		if e != nil {
			if e != p.ErrInvalid && e != p.ErrSignature && e != p.ErrBinding {
				t.Fatal("unsafe verifier error", e)
			}
			if b, e := v.Canonical(); b != nil || e != p.ErrInvalid {
				t.Fatal("failure returns opaque")
			}
			return
		}
		canonical, e := v.Canonical()
		if e != nil {
			t.Fatal(e)
		}
		n, e := p.ParseNotification(canonical)
		if e != nil {
			t.Fatal(e)
		}
		if n.Fact.EnvironmentID != envID || n.Fact.MerchantID != merchantID {
			t.Fatal("unbound accepted observation")
		}
		if _, e := v.Fact(); e != nil {
			t.Fatal(e)
		}
	})
}

func FuzzSignedHeaders(f *testing.F) {
	f.Add("refund_v2", deliveryID, fmt.Sprint(now().Unix()), strings.Repeat("a", 64))
	f.Fuzz(func(t *testing.T, key, delivery, clock, signature string) {
		if len(key)+len(delivery)+len(clock)+len(signature) > 8192 {
			return
		}
		raw := http.Header{"X-Moni-Refund-V2-Key-Id": {key}, "X-Moni-Refund-V2-Delivery-Id": {delivery}, "X-Moni-Refund-V2-Delivery-Time": {clock}, "X-Moni-Refund-V2-Signature": {signature}}
		h, e := p.ReadSignedHeaders(raw)
		if e != nil {
			if e != p.ErrSignature || h != (p.SignedHeaders{}) {
				t.Fatal("unsafe header failure")
			}
			return
		}
		if h.KeyID != key || h.DeliveryID != delivery || h.DeliveryTime != clock || h.Signature != signature {
			t.Fatal("header rewritten")
		}
	})
}
