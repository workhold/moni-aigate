package paymentproto_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func clockTime() time.Time { t, _ := time.Parse(time.RFC3339, "2026-11-01T00:00:00Z"); return t }
func environment() p.Environment {
	return p.Environment{ID: envID, MerchantID: "merchant:qa", PayeeLabel: "QA payee"}
}
func keyBytes() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}
func keySpec(id string, raw []byte) p.KeySpec {
	return p.KeySpec{ID: id, Key: raw, NotBefore: clockTime().Add(-time.Hour), NotAfter: clockTime().Add(time.Hour)}
}
func keys(t *testing.T) *p.Keyring {
	t.Helper()
	k, e := p.NewKeyring("v1", []p.KeySpec{keySpec("v1", keyBytes())})
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func unsigned() p.SignedHeaders {
	return p.SignedHeaders{DeliveryID: deliveryID, DeliveryTime: strconv.FormatInt(clockTime().Unix(), 10)}
}

// Independent signer used for validly signed malicious/mismatched bodies that
// the production Sign methods correctly refuse to construct.
func independentlySigned(raw []byte, query *p.QueryRequest) p.SignedHeaders {
	h := unsigned()
	h.KeyID = "v1"
	domain := "moni-payment-sandbox-v1"
	parts := []string{domain, envID, "merchant:qa"}
	if query != nil {
		parts = []string{"moni-payment-sandbox-query-v1", envID, "merchant:qa", query.QueryID, query.CommandID}
	}
	hash := sha256.Sum256(raw)
	parts = append(parts, h.DeliveryID, h.DeliveryTime, hex.EncodeToString(hash[:]))
	mac := hmac.New(sha256.New, keyBytes())
	mac.Write([]byte(strings.Join(parts, "\n")))
	h.Signature = hex.EncodeToString(mac.Sum(nil))
	return h
}

func TestIndependentGoldenNotificationAndQuerySignatures(t *testing.T) {
	k := keys(t)
	env := environment()
	h, e := k.SignNotification(env, unsigned(), []byte(notificationGolden))
	if e != nil {
		t.Fatal(e)
	}
	// Vectors computed independently with Python hashlib/hmac and explicit LF bytes.
	if h.Signature != "4692550407a00637c199a392008325609a7c6341cfa8e1436886d4cd33f4f900" || h.KeyID != "v1" {
		t.Fatal("notification HMAC vector differs")
	}
	v, e := k.VerifyNotification(env, clockTime(), h, []byte(notificationGolden))
	if e != nil {
		t.Fatal(e)
	}
	canonical, e := v.Canonical()
	if e != nil || string(canonical) != notificationGolden {
		t.Fatal("verified canonical body differs", e)
	}
	qh, e := k.SignQuery(env, query(), unsigned(), []byte(queryGolden))
	if e != nil {
		t.Fatal(e)
	}
	if qh.Signature != "fc25ed03b1334c0ef542b0f1bd90d7386778a344de7af0c0084aaee0f95d4686" {
		t.Fatal("query HMAC vector differs")
	}
	qv, e := k.VerifyQuery(env, query(), clockTime(), qh, []byte(queryGolden))
	if e != nil {
		t.Fatal(e)
	}
	canonical, e = qv.Canonical()
	if e != nil || string(canonical) != queryGolden {
		t.Fatal("query verified vector", e)
	}
	if _, e = k.VerifyNotification(env, clockTime(), qh, []byte(queryGolden)); e == nil {
		t.Fatal("query signature accepted in notification domain")
	}
	if _, e = k.VerifyQuery(env, query(), clockTime(), h, []byte(notificationGolden)); e == nil {
		t.Fatal("notification signature accepted in query domain")
	}
}

func TestVerifyUsesRawBodyBeforeStrictParsingAndPreservesImmutableEvidence(t *testing.T) {
	k := keys(t)
	env := environment()
	raw := []byte(notificationGolden)
	pretty := bytes.ReplaceAll(raw, []byte(","), []byte(",\n "))
	h, e := k.SignNotification(env, unsigned(), pretty)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = k.VerifyNotification(env, clockTime(), h, raw); e == nil {
		t.Fatal("signature normalized body before verifying")
	}
	v, e := k.VerifyNotification(env, clockTime(), h, pretty)
	if e != nil {
		t.Fatal(e)
	}
	canonical, e := v.Canonical()
	if e != nil || !bytes.Equal(canonical, raw) {
		t.Fatal("equivalent raw JSON did not yield canonical evidence", e)
	}
	for i := range pretty {
		pretty[i] = 'x'
	}
	canonical[0] = 'x'
	again, e := v.Canonical()
	if e != nil || string(again) != notificationGolden {
		t.Fatal("verified evidence retained caller-owned bytes", e)
	}
	var zeroN p.VerifiedNotification
	_, e = zeroN.Canonical()
	assertCode(t, e, "invalid_request")
	var zeroQ p.VerifiedQuery
	_, e = zeroQ.Canonical()
	assertCode(t, e, "invalid_request")
	badBody := []byte(`{"unknown":true}`)
	wrong := independentlySigned(badBody, nil)
	wrong.Signature = strings.Repeat("0", 64)
	_, e = k.VerifyNotification(env, clockTime(), wrong, badBody)
	assertCode(t, e, "payment_signature_invalid")
	_, e = k.VerifyNotification(env, clockTime(), independentlySigned(badBody, nil), badBody)
	assertCode(t, e, "invalid_request")
	badUnion := changed(raw, "refund_id", otherID)
	_, e = k.VerifyNotification(env, clockTime(), independentlySigned(badUnion, nil), badUnion)
	assertCode(t, e, "invalid_request")
	otherMerchant := changed(raw, "merchant_id", "merchant:other")
	_, e = k.VerifyNotification(env, clockTime(), independentlySigned(otherMerchant, nil), otherMerchant)
	assertCode(t, e, "payment_environment_invalid")
}

func TestQueryVerificationBindsAllFrozenRequestIdentities(t *testing.T) {
	k := keys(t)
	env := environment()
	q := query()
	raw := []byte(queryGolden)
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"query_id", otherID}, {"command_id", otherID}, {"merchant_order_id", "mps1." + envID + "." + otherID}, {"command_kind", "cancel"}, {"parameter_digest", strings.Repeat("b", 64)},
	} {
		body := changed(raw, tc.key, tc.value)
		h := independentlySigned(body, &q)
		_, e := k.VerifyQuery(env, q, clockTime(), h, body)
		assertCode(t, e, "invalid_request")
		_, e = k.SignQuery(env, q, unsigned(), body)
		assertCode(t, e, "invalid_request")
	}
	for _, tc := range []struct {
		key   string
		value any
	}{{"merchant_id", "merchant:other"}} {
		body := changed(raw, tc.key, tc.value)
		_, e := k.VerifyQuery(env, q, clockTime(), independentlySigned(body, &q), body)
		assertCode(t, e, "payment_environment_invalid")
	}
	h, e := k.SignQuery(env, q, unsigned(), raw)
	if e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*p.QueryRequest){func(q *p.QueryRequest) { q.QueryID = otherID }, func(q *p.QueryRequest) { q.CommandID = otherID }} {
		changedQ := q
		mutate(&changedQ)
		_, e := k.VerifyQuery(env, changedQ, clockTime(), h, raw)
		assertCode(t, e, "payment_signature_invalid")
	}
	v, e := k.VerifyQuery(env, q, clockTime(), h, raw)
	if e != nil {
		t.Fatal(e)
	}
	canonical, _ := v.Canonical()
	raw[0] = 'x'
	canonical[0] = 'x'
	again, e := v.Canonical()
	if e != nil || string(again) != queryGolden {
		t.Fatal("query retained mutable bytes", e)
	}
}

func TestDeliveryClockAndProviderFutureBoundaries(t *testing.T) {
	k := keys(t)
	env := environment()
	raw := []byte(notificationGolden)
	for _, offset := range []time.Duration{-300 * time.Second, 300 * time.Second} {
		h := unsigned()
		h.DeliveryTime = strconv.FormatInt(clockTime().Add(offset).Unix(), 10)
		signed, e := k.SignNotification(env, h, raw)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = k.VerifyNotification(env, clockTime(), signed, raw); e != nil {
			t.Fatal("inclusive delivery window rejected", e)
		}
	}
	for _, offset := range []time.Duration{-301 * time.Second, 301 * time.Second} {
		h := unsigned()
		h.DeliveryTime = strconv.FormatInt(clockTime().Add(offset).Unix(), 10)
		signed, e := k.SignNotification(env, h, raw)
		if e != nil {
			t.Fatal(e)
		}
		_, e = k.VerifyNotification(env, clockTime(), signed, raw)
		assertCode(t, e, "payment_signature_invalid")
	}
	h, e := k.SignNotification(env, unsigned(), raw)
	if e != nil {
		t.Fatal(e)
	}
	_, e = k.VerifyNotification(env, clockTime().Add(300*time.Second+time.Microsecond), h, raw)
	assertCode(t, e, "payment_signature_invalid")
	for _, offset := range []time.Duration{5 * time.Second, 5*time.Second + time.Microsecond} {
		n := notification()
		n.OccurredAt = clockTime().Add(offset).Format("2006-01-02T15:04:05.000000Z")
		body, e := p.CanonicalNotification(n)
		if e != nil {
			t.Fatal(e)
		}
		signed, e := k.SignNotification(env, unsigned(), body)
		if e != nil {
			t.Fatal(e)
		}
		_, e = k.VerifyNotification(env, clockTime(), signed, body)
		if offset == 5*time.Second {
			if e != nil {
				t.Fatal("inclusive future boundary rejected", e)
			}
		} else {
			assertCode(t, e, "invalid_request")
		}
		qr := queryReply()
		qr.CommandState = "completed"
		qr.ProviderOrderRevision = ptr("2")
		qr.Result = &p.CommandResult{Status: "captured", Capture: &p.CaptureFact{ProviderTransactionID: "transaction:one", Amount: "1", Currency: "CNY", OccurredAt: n.OccurredAt, ProviderOrderRevision: "2"}}
		body, e = p.CanonicalQueryReply(qr)
		if e != nil {
			t.Fatal(e)
		}
		qh, e := k.SignQuery(env, query(), unsigned(), body)
		if e != nil {
			t.Fatal(e)
		}
		_, e = k.VerifyQuery(env, query(), clockTime(), qh, body)
		if offset == 5*time.Second {
			if e != nil {
				t.Fatal(e)
			}
		} else {
			assertCode(t, e, "invalid_request")
		}
	}
	_, e = k.VerifyNotification(env, time.Time{}, h, raw)
	assertCode(t, e, "invalid_request")
}

func TestKeyringCopiesConfigurationRotationAndWindowValidation(t *testing.T) {
	raw := keyBytes()
	spec := keySpec("v1", raw)
	specs := []p.KeySpec{spec}
	k, e := p.NewKeyring("v1", specs)
	if e != nil {
		t.Fatal(e)
	}
	for i := range raw {
		raw[i] = 0xff
	}
	specs[0].ID = "changed"
	specs[0].NotBefore = clockTime().Add(time.Hour)
	h, e := k.SignNotification(environment(), unsigned(), []byte(notificationGolden))
	if e != nil || h.Signature != "4692550407a00637c199a392008325609a7c6341cfa8e1436886d4cd33f4f900" {
		t.Fatal("keyring retained mutable configuration", e)
	}
	rotated, e := p.NewKeyring("v2", []p.KeySpec{keySpec("v1", keyBytes()), keySpec("v2", bytes.Repeat([]byte{3}, 32))})
	if e != nil {
		t.Fatal(e)
	}
	if rotated.ActiveID() != "v2" || !rotated.HasVersion("v1") || rotated.HasVersion("missing") {
		t.Fatal("version lookup invalid")
	}
	if _, e = rotated.VerifyNotification(environment(), clockTime(), h, []byte(notificationGolden)); e != nil {
		t.Fatal("retained version could not verify old delivery", e)
	}
	oldHeader := unsigned()
	oldHeader.KeyID = "v1"
	_, e = rotated.SignNotification(environment(), oldHeader, []byte(notificationGolden))
	assertCode(t, e, "invalid_request")
	h.KeyID = "unknown"
	_, e = rotated.VerifyNotification(environment(), clockTime(), h, []byte(notificationGolden))
	assertCode(t, e, "payment_signature_invalid")
	var absent *p.Keyring
	if absent.ActiveID() != "" || absent.HasVersion("v1") {
		t.Fatal("nil keyring is configured")
	}
	_, e = absent.VerifyNotification(environment(), clockTime(), h, []byte(notificationGolden))
	assertCode(t, e, "payment_key_unavailable")
	badSpecs := [][]p.KeySpec{nil, {keySpec("bad.id", keyBytes())}, {keySpec("v1", nil)}, {keySpec("v1", keyBytes()[:31])}, {keySpec("v1", keyBytes()), keySpec("v1", bytes.Repeat([]byte{3}, 32))}, {keySpec("v1", keyBytes()), keySpec("v2", keyBytes())}}
	invalid := spec
	invalid.NotAfter = invalid.NotBefore.Add(-time.Second)
	badSpecs = append(badSpecs, []p.KeySpec{invalid})
	invalid = spec
	invalid.NotBefore = invalid.NotBefore.Add(time.Nanosecond)
	badSpecs = append(badSpecs, []p.KeySpec{invalid})
	invalid = spec
	invalid.NotBefore = invalid.NotBefore.In(time.FixedZone("UTC", 0))
	badSpecs = append(badSpecs, []p.KeySpec{invalid})
	invalid = spec
	invalid.NotBefore = time.Time{}
	badSpecs = append(badSpecs, []p.KeySpec{invalid})
	for _, bad := range badSpecs {
		_, e := p.NewKeyring("v1", bad)
		assertCode(t, e, "payment_key_unavailable")
	}
	_, e = p.NewKeyring("missing", []p.KeySpec{spec})
	assertCode(t, e, "payment_key_unavailable")
	boundary := keySpec("v1", keyBytes())
	boundary.NotBefore = clockTime()
	boundary.NotAfter = clockTime()
	single, e := p.NewKeyring("v1", []p.KeySpec{boundary})
	if e != nil {
		t.Fatal(e)
	}
	good, e := single.SignNotification(environment(), unsigned(), []byte(notificationGolden))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = single.VerifyNotification(environment(), clockTime(), good, []byte(notificationGolden)); e != nil {
		t.Fatal("inclusive key window rejected", e)
	}
	before := unsigned()
	before.DeliveryTime = strconv.FormatInt(clockTime().Add(-time.Second).Unix(), 10)
	_, e = single.SignNotification(environment(), before, []byte(notificationGolden))
	assertCode(t, e, "payment_key_unavailable")
}

func TestSignedHeaderExactMultiplicityAndLexicalFormat(t *testing.T) {
	h := independentlySigned([]byte(notificationGolden), nil)
	base := http.Header{"X-Moni-Key-Id": {h.KeyID}, "X-Moni-Delivery-Id": {h.DeliveryID}, "X-Moni-Delivery-Time": {h.DeliveryTime}, "X-Moni-Signature": {h.Signature}}
	read, e := p.ReadSignedHeaders(base)
	if e != nil || read != h {
		t.Fatal("valid header", e)
	}
	for _, mutate := range []func(http.Header){
		func(h http.Header) { delete(h, "X-Moni-Key-Id") }, func(h http.Header) { h["x-moni-key-id"] = []string{"v1"} }, func(h http.Header) { h["X-Moni-Key-Id"] = []string{"v1", "v1"} }, func(h http.Header) { h["X-Moni-Key-Id"] = []string{"v1,v1"} }, func(h http.Header) { h["X-Moni-Key-Id"] = []string{"v1 "} }, func(h http.Header) { h["X-Moni-Delivery-Id"] = []string{"00000000-0000-0000-0000-000000000000"} }, func(h http.Header) { h["X-Moni-Delivery-Time"] = []string{"01793491200"} }, func(h http.Header) { h["X-Moni-Delivery-Time"] = []string{"1e9"} }, func(h http.Header) { h["X-Moni-Delivery-Time"] = []string{"253402300800"} }, func(h http.Header) { h["X-Moni-Delivery-Time"] = []string{"-1"} }, func(h http.Header) { h["X-Moni-Signature"] = []string{strings.ToUpper(h.Get("X-Moni-Signature"))} }, func(h http.Header) { h["X-Moni-Signature"] = []string{strings.Repeat("0", 63)} }, func(h http.Header) { h["X-Moni-Key-Id"] = []string{"v1"} },
	} {
		bad := base.Clone()
		mutate(bad)
		_, e := p.ReadSignedHeaders(bad)
		assertCode(t, e, "payment_signature_invalid")
	}
	for _, mutate := range []func(*p.SignedHeaders){func(h *p.SignedHeaders) { h.Signature = "already-signed" }, func(h *p.SignedHeaders) { h.KeyID = "missing" }, func(h *p.SignedHeaders) { h.DeliveryTime = "+1793491200" }, func(h *p.SignedHeaders) { h.DeliveryID = "bad" }} {
		bad := unsigned()
		mutate(&bad)
		_, e := keys(t).SignNotification(environment(), bad, []byte(notificationGolden))
		assertCode(t, e, "invalid_request")
	}
}

func TestImmutableKeyringConcurrentSigningAndVerification(t *testing.T) {
	k := keys(t)
	env := environment()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				h, e := k.SignNotification(env, unsigned(), []byte(notificationGolden))
				if e != nil {
					errs <- e
					return
				}
				if _, e = k.VerifyNotification(env, clockTime(), h, []byte(notificationGolden)); e != nil {
					errs <- e
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
