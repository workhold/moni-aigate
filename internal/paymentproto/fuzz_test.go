package paymentproto_test

import (
	"bytes"
	"testing"

	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func FuzzStrictParsersCanonicalRoundTrip(f *testing.F) {
	checkoutRaw, _ := p.CanonicalCheckout(checkout())
	queryRaw, _ := p.CanonicalQuery(query())
	for _, seed := range [][]byte{checkoutRaw, queryRaw, []byte(notificationGolden), []byte(queryGolden), []byte(`{"schema_version":1,"schema_version":1}`), []byte(`{"merchant_id":"\ud800"}`), []byte(`null`), {0xff}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if v, e := p.ParseCheckout(raw); e == nil {
			b, e := p.CanonicalCheckout(v)
			if e != nil {
				t.Fatal(e)
			}
			v2, e := p.ParseCheckout(b)
			if e != nil {
				t.Fatal(e)
			}
			again, _ := p.CanonicalCheckout(v2)
			if !bytes.Equal(b, again) {
				t.Fatal("checkout not canonical")
			}
		}
		if v, e := p.ParseCancel(raw); e == nil {
			b, e := p.CanonicalCancel(v)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = p.ParseCancel(b); e != nil {
				t.Fatal(e)
			}
		}
		if v, e := p.ParseQuery(raw); e == nil {
			b, e := p.CanonicalQuery(v)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = p.ParseQuery(b); e != nil {
				t.Fatal(e)
			}
		}
		if v, e := p.ParseNotification(raw); e == nil {
			b, e := p.CanonicalNotification(v)
			if e != nil {
				t.Fatal(e)
			}
			v2, e := p.ParseNotification(b)
			if e != nil {
				t.Fatal(e)
			}
			again, _ := p.CanonicalNotification(v2)
			if !bytes.Equal(b, again) {
				t.Fatal("notification not canonical")
			}
		}
		if v, e := p.ParseCommandReply(raw); e == nil {
			b, e := p.CanonicalCommandReply(v)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = p.ParseCommandReply(b); e != nil {
				t.Fatal(e)
			}
		}
		if v, e := p.ParseQueryReply(raw); e == nil {
			b, e := p.CanonicalQueryReply(v)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = p.ParseQueryReply(b); e != nil {
				t.Fatal(e)
			}
		}
	})
}
