package paymentrefundproto_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	p "github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

const (
	commandReplyDomain = "moni-aigate/payment-refund-command-reply/v2"
	queryReplyDomain   = "moni-aigate/payment-refund-query-reply/v2"
	notificationDomain = "moni-aigate/payment-refund-notification/v2"
)

func keyBytes() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}
func spec(id string, key []byte) p.KeySpec {
	return p.KeySpec{ID: id, Key: key, NotBefore: now().Add(-time.Hour), NotAfter: now().Add(time.Hour)}
}
func keys(t *testing.T) *p.Keyring {
	t.Helper()
	k, e := p.NewKeyring("refund_v2", []p.KeySpec{spec("refund_v2", keyBytes())})
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func unsigned() p.SignedHeaders {
	return p.SignedHeaders{DeliveryID: deliveryID, DeliveryTime: fmt.Sprint(now().Unix())}
}

// These adversarial envelopes intentionally bypass Sign's semantic binding
// checks. Frame layout and HMAC are expressed independently of package helpers.
func independentFrame(parts ...[]byte) []byte {
	var b bytes.Buffer
	for _, part := range parts {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		b.Write(n[:])
		b.Write(part)
	}
	return b.Bytes()
}
func independentDigest(domain string, raw []byte) string {
	s := sha256.Sum256(independentFrame([]byte(domain), raw))
	return hex.EncodeToString(s[:])
}
func independentHeaders(raw []byte, domain, context string, h p.SignedHeaders) p.SignedHeaders {
	if h.KeyID == "" {
		h.KeyID = "refund_v2"
	}
	s := sha256.Sum256(raw)
	m := hmac.New(sha256.New, keyBytes())
	m.Write(independentFrame([]byte(domain), []byte(h.KeyID), []byte(envID), []byte(merchantID), []byte(context), []byte(h.DeliveryID), []byte(h.DeliveryTime), []byte(hex.EncodeToString(s[:]))))
	h.Signature = hex.EncodeToString(m.Sum(nil))
	return h
}
func commandContext(t *testing.T, r p.RefundRequest) string {
	return independentDigest("moni-aigate/payment-refund-command/v2", mustJSON(t, r))
}
func queryContext(t *testing.T, q p.QueryRequest) string {
	return independentDigest("moni-aigate/payment-refund-query/v2", mustJSON(t, q))
}

func TestThreeAuthenticatedPurposesAndRawCanonicalSeparation(t *testing.T) {
	k := keys(t)
	r := request(t)
	q := query(t)
	cb := mustJSON(t, commandReply(t))
	qb := mustJSON(t, queryReply(t))
	nb := mustJSON(t, notification(t))
	ch, e := k.SignCommandReply(env(), r, unsigned(), cb)
	if e != nil {
		t.Fatal(e)
	}
	qh, e := k.SignQueryReply(env(), q, unsigned(), qb)
	if e != nil {
		t.Fatal(e)
	}
	nh, e := k.SignNotification(env(), unsigned(), nb)
	if e != nil {
		t.Fatal(e)
	}
	if ch != independentHeaders(cb, commandReplyDomain, commandContext(t, r), unsigned()) || qh != independentHeaders(qb, queryReplyDomain, queryContext(t, q), unsigned()) || nh != independentHeaders(nb, notificationDomain, "", unsigned()) {
		t.Fatal("independent framing/HMAC differs")
	}
	cv, e := k.VerifyCommandReply(env(), r, now(), ch, cb)
	if e != nil {
		t.Fatal(e)
	}
	qv, e := k.VerifyQueryReply(env(), q, now(), qh, qb)
	if e != nil {
		t.Fatal(e)
	}
	nv, e := k.VerifyNotification(env(), now(), nh, nb)
	if e != nil {
		t.Fatal(e)
	}
	cf, _ := cv.Fact()
	qf, _ := qv.Fact()
	nf, _ := nv.Fact()
	cd, _ := cf.Digest()
	qd, _ := qf.Digest()
	nd, _ := nf.Digest()
	if cd != qd || cd != nd {
		t.Fatal("same fact differs across observations")
	}
	if _, e = k.VerifyNotification(env(), now(), ch, cb); e != p.ErrSignature {
		t.Fatal("command domain accepted as notification")
	}
	if _, e = k.VerifyCommandReply(env(), r, now(), qh, qb); e != p.ErrSignature {
		t.Fatal("query domain accepted as command")
	}
	if _, e = k.VerifyQueryReply(env(), q, now(), nh, nb); e != p.ErrSignature {
		t.Fatal("notification domain accepted as query")
	}
	// A legally reordered body is authenticated as its raw bytes, then returns
	// the same closed canonical semantic value. A byte change needs a new MAC.
	var m map[string]any
	_ = json.Unmarshal(nb, &m)
	raw, _ := json.MarshalIndent(m, "", " ")
	h, e := k.SignNotification(env(), unsigned(), raw)
	if e != nil {
		t.Fatal(e)
	}
	if h.Signature == nh.Signature {
		t.Fatal("raw body hash was canonicalized")
	}
	v, e := k.VerifyNotification(env(), now(), h, raw)
	if e != nil {
		t.Fatal(e)
	}
	canonical, _ := v.Canonical()
	if !bytes.Equal(canonical, nb) {
		t.Fatal("verified canonical differs")
	}
	if _, e = k.VerifyNotification(env(), now(), h, append(raw, ' ')); e != p.ErrSignature {
		t.Fatal("one raw byte not covered")
	}
	// All caller-visible bytes and DTO pointers remain detached from opaque data.
	for _, b := range [][]byte{raw, canonical, nb, cb, qb} {
		b[0] = '!'
	}
	next, _ := v.Canonical()
	if next[0] != '{' {
		t.Fatal("raw/canonical aliased opaque")
	}
	fc, _ := nf.Canonical()
	fc[0] = '!'
	next, _ = nf.Canonical()
	if next[0] != '{' {
		t.Fatal("fact canonical alias")
	}
	parsed, e := p.ParseRefundFact(next)
	if e != nil {
		t.Fatal(e)
	}
	*parsed.ProviderRefundReference = "changed"
	next, _ = nf.Canonical()
	if bytes.Contains(next, []byte("changed")) {
		t.Fatal("DTO alias")
	}
}

func TestTrustedRequestAndEnvironmentBindings(t *testing.T) {
	k := keys(t)
	r := request(t)
	q := query(t)
	// Every command identity field remains bound, even if the peer validly signs
	// another complete, internally consistent command using the expected context.
	changes := []func(*p.RefundRequest){
		func(x *p.RefundRequest) { x.CommandID = otherID }, func(x *p.RefundRequest) { x.LocalRefundID = otherID },
		func(x *p.RefundRequest) { x.MerchantOrderID = "mps1." + envID + "." + otherID }, func(x *p.RefundRequest) { x.ProviderTransactionID = "other:transaction" },
		func(x *p.RefundRequest) { x.OriginalCaptureAmount = "2000000" }, func(x *p.RefundRequest) { x.CaptureDigest = strings.Repeat("f", 64) },
		func(x *p.RefundRequest) { x.Amount = "1" }, func(x *p.RefundRequest) { x.MerchantID = "other:merchant" },
		func(x *p.RefundRequest) {
			x.EnvironmentID = otherID
			x.MerchantOrderID = "mps1." + otherID + "." + orderID
		},
	}
	for i, alter := range changes {
		x := r
		alter(&x)
		d := commandContext(t, x)
		reply := p.CommandReply{SchemaVersion: 2, Command: x, ParameterDigest: d, CommandState: "in_progress"}
		raw := mustJSON(t, reply)
		h := independentHeaders(raw, commandReplyDomain, commandContext(t, r), unsigned())
		v, e := k.VerifyCommandReply(env(), r, now(), h, raw)
		if e != p.ErrBinding {
			t.Fatalf("command field %d: %v", i, e)
		}
		if b, e := v.Canonical(); b != nil || e != p.ErrInvalid {
			t.Fatal("partial verified command")
		}
		if _, e = k.SignCommandReply(env(), r, unsigned(), raw); e != p.ErrBinding {
			t.Fatal("signer mismatch")
		}
	}
	for i, alter := range []func(*p.QueryReply){func(x *p.QueryReply) { x.QueryID = otherID }, func(x *p.QueryReply) { x.MutationGeneration = "8" }, func(x *p.QueryReply) { x.Command.CommandID = otherID; x.ParameterDigest = commandContext(t, x.Command) }} {
		x := queryReply(t)
		x.CommandState = "not_seen"
		x.Result = nil
		alter(&x)
		raw := mustJSON(t, x)
		h := independentHeaders(raw, queryReplyDomain, queryContext(t, q), unsigned())
		if _, e := k.VerifyQueryReply(env(), q, now(), h, raw); e != p.ErrBinding {
			t.Fatalf("query binding %d: %v", i, e)
		}
	}
	goodRaw := mustJSON(t, queryReply(t))
	h, e := k.SignQueryReply(env(), q, unsigned(), goodRaw)
	if e != nil {
		t.Fatal(e)
	}
	old := q
	old.MutationGeneration = "8"
	if _, e = k.VerifyQueryReply(env(), old, now(), h, goodRaw); e != p.ErrSignature {
		t.Fatal("generation context not signed")
	}
	otherEnv := env()
	otherEnv.ID = otherID
	if _, e = k.VerifyQueryReply(otherEnv, q, now(), h, goodRaw); e != p.ErrBinding {
		t.Fatal("trusted env command mismatch")
	}
	n := notification(t)
	x := r
	x.MerchantID = "other:merchant"
	n.Fact = factFor(t, x)
	raw := mustJSON(t, n)
	h = independentHeaders(raw, notificationDomain, "", unsigned())
	if _, e = k.VerifyNotification(env(), now(), h, raw); e != p.ErrBinding {
		t.Fatal("notification environment mismatch")
	}
	bad := []byte(strings.Replace(string(mustJSON(t, notification(t))), `"schema_version":2`, `"schema_version":2.0`, 1))
	h = independentHeaders(bad, notificationDomain, "", unsigned())
	if _, e = k.VerifyNotification(env(), now(), h, bad); e != p.ErrInvalid {
		t.Fatal("valid signature bypassed strict codec")
	}
}

func TestHeadersAndDeliveryTimeBoundaries(t *testing.T) {
	k := keys(t)
	raw := mustJSON(t, notification(t))
	h, e := k.SignNotification(env(), unsigned(), raw)
	if e != nil {
		t.Fatal(e)
	}
	names := []string{"X-Moni-Refund-V2-Key-Id", "X-Moni-Refund-V2-Delivery-Id", "X-Moni-Refund-V2-Delivery-Time", "X-Moni-Refund-V2-Signature"}
	values := []string{h.KeyID, h.DeliveryID, h.DeliveryTime, h.Signature}
	head := http.Header{}
	for i, n := range names {
		head[n] = []string{values[i]}
	}
	head["X-Moni-Key-Id"] = []string{"ignored-v1"}
	got, e := p.ReadSignedHeaders(head)
	if e != nil || got != h {
		t.Fatal("v2 header read", e)
	}
	for i, n := range names {
		x := head.Clone()
		delete(x, n)
		if _, e = p.ReadSignedHeaders(x); e != p.ErrSignature {
			t.Fatal("missing header")
		}
		x = head.Clone()
		x[strings.ToLower(n)] = []string{values[i]}
		if _, e = p.ReadSignedHeaders(x); e != p.ErrSignature {
			t.Fatal("case duplicate")
		}
		x = head.Clone()
		x[n] = []string{values[i], values[i]}
		if _, e = p.ReadSignedHeaders(x); e != p.ErrSignature {
			t.Fatal("multiple values")
		}
		x = head.Clone()
		x[n] = []string{values[i] + ",x"}
		if _, e = p.ReadSignedHeaders(x); e != p.ErrSignature {
			t.Fatal("comma value")
		}
	}
	x := head.Clone()
	x["bad\nname"] = []string{"x"}
	if _, e = p.ReadSignedHeaders(x); e != p.ErrSignature {
		t.Fatal("invalid name")
	}
	for _, clock := range []string{"-1", "01", "1.0", "1e0", "253402300800", "9999999999999", " 1", ""} {
		x := head.Clone()
		x[names[2]] = []string{clock}
		if _, e = p.ReadSignedHeaders(x); e != p.ErrSignature {
			t.Fatalf("delivery syntax %q", clock)
		}
	}
	for _, clock := range []string{"0", "253402300799"} {
		x := head.Clone()
		x[names[2]] = []string{clock}
		if _, e = p.ReadSignedHeaders(x); e != nil {
			t.Fatal("valid delivery range", e)
		}
	}
	for _, alter := range []func(*p.SignedHeaders){func(x *p.SignedHeaders) { x.KeyID = "unknown" }, func(x *p.SignedHeaders) { x.Signature = strings.Repeat("0", 64) }, func(x *p.SignedHeaders) { x.Signature = strings.ToUpper(x.Signature) }, func(x *p.SignedHeaders) { x.DeliveryID = "00000000-0000-0000-0000-000000000000" }} {
		x := h
		alter(&x)
		if _, e = k.VerifyNotification(env(), now(), x, raw); e != p.ErrSignature {
			t.Fatal("bad header verify")
		}
	}
	// Test the inclusive seconds window independently of fractional receivedAt.
	received := now().Truncate(time.Second)
	for _, seconds := range []int64{-301, -300, 300, 301} {
		x := unsigned()
		x.DeliveryTime = fmt.Sprint(received.Add(time.Duration(seconds) * time.Second).Unix())
		x, e = k.SignNotification(env(), x, raw)
		if e != nil {
			t.Fatal(e)
		}
		_, e = k.VerifyNotification(env(), received, x, raw)
		if seconds == -300 || seconds == 300 {
			if e != nil {
				t.Fatal("inclusive time window", e)
			}
		} else if e != p.ErrSignature {
			t.Fatal("wide time window")
		}
	}
	for _, badNow := range []time.Time{time.Time{}, time.Date(0, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("ahead", 3600)), time.Date(9999, 12, 31, 23, 59, 59, 0, time.FixedZone("behind", -3600))} {
		if _, e = k.VerifyNotification(env(), badNow, h, raw); e != p.ErrInvalid {
			t.Fatal("UTC receive year/zero", e)
		}
	}
	if _, e = k.VerifyNotification(env(), now().In(time.FixedZone("offset", 9*3600)).Add(time.Nanosecond), h, raw); e != nil {
		t.Fatal("valid location/nanos", e)
	}
	for _, delta := range []time.Duration{5 * time.Second, 5*time.Second + time.Microsecond} {
		n := notification(t)
		n.Fact.CompletedAt = now().Add(delta).UTC().Format("2006-01-02T15:04:05.000000Z")
		b := mustJSON(t, n)
		x, e := k.SignNotification(env(), unsigned(), b)
		if e != nil {
			t.Fatal(e)
		}
		_, e = k.VerifyNotification(env(), now(), x, b)
		if delta == 5*time.Second {
			if e != nil {
				t.Fatal("completed upper bound", e)
			}
		} else if e != p.ErrSignature {
			t.Fatal("future completed accepted", e)
		}
	}
	n := notification(t)
	n.Fact.CompletedAt = "2001-01-01T00:00:00.000000Z"
	b := mustJSON(t, n)
	xH, e := k.SignNotification(env(), unsigned(), b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = k.VerifyNotification(env(), now(), xH, b); e != nil {
		t.Fatal("invented historical age policy", e)
	}
}

func TestKeyringConstructionRotationAndNil(t *testing.T) {
	base := spec("refund_v2", keyBytes())
	for _, alter := range []func(*p.KeySpec){func(x *p.KeySpec) { x.ID = "" }, func(x *p.KeySpec) { x.ID = strings.Repeat("a", 33) }, func(x *p.KeySpec) { x.Key = x.Key[:31] }, func(x *p.KeySpec) { x.NotBefore = time.Time{} }, func(x *p.KeySpec) { x.NotBefore = x.NotBefore.In(time.FixedZone("UTC", 0)) }, func(x *p.KeySpec) { x.NotBefore = x.NotBefore.Add(time.Nanosecond) }, func(x *p.KeySpec) { x.NotAfter = x.NotBefore.Add(-time.Second) }} {
		x := base
		alter(&x)
		k, e := p.NewKeyring("refund_v2", []p.KeySpec{x})
		if k != nil || e != p.ErrKey {
			t.Fatal("invalid key spec")
		}
	}
	for _, specs := range [][]p.KeySpec{nil, {base, base}, {base, spec("other", keyBytes())}, make([]p.KeySpec, 129)} {
		if k, e := p.NewKeyring("refund_v2", specs); k != nil || e != p.ErrKey {
			t.Fatal("keyset bound/duplicates")
		}
	}
	if k, e := p.NewKeyring("missing", []p.KeySpec{base}); k != nil || e != p.ErrKey {
		t.Fatal("missing active")
	}
	material := keyBytes()
	specs := []p.KeySpec{spec("refund_v2", material)}
	k, e := p.NewKeyring("refund_v2", specs)
	if e != nil {
		t.Fatal(e)
	}
	for i := range material {
		material[i] = 0
	}
	specs[0].ID = "changed"
	specs[0].NotAfter = time.Time{}
	raw := mustJSON(t, notification(t))
	h, e := k.SignNotification(env(), unsigned(), raw)
	if e != nil || h != independentHeaders(raw, notificationDomain, "", unsigned()) {
		t.Fatal("keyring retained caller refs", e)
	}
	old := spec("refund_v2", keyBytes())
	fresh := spec("new", bytes.Repeat([]byte{254}, 32))
	rotated, e := p.NewKeyring("new", []p.KeySpec{old, fresh})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = rotated.VerifyNotification(env(), now(), h, raw); e != nil {
		t.Fatal("retained historical key", e)
	}
	oldH := unsigned()
	oldH.KeyID = "refund_v2"
	if _, e = rotated.SignNotification(env(), oldH, raw); e != p.ErrKey {
		t.Fatal("signed using old key")
	}
	if rotated.ActiveID() != "new" || !rotated.HasVersion("refund_v2") || rotated.HasVersion("absent") {
		t.Fatal("key queries")
	}
	boundary := now().Truncate(time.Second)
	equal := p.KeySpec{ID: "refund_v2", Key: keyBytes(), NotBefore: boundary, NotAfter: boundary}
	single, e := p.NewKeyring("refund_v2", []p.KeySpec{equal})
	if e != nil {
		t.Fatal(e)
	}
	u := unsigned()
	u.DeliveryTime = fmt.Sprint(boundary.Unix())
	h, e = single.SignNotification(env(), u, raw)
	if e != nil {
		t.Fatal("key inclusive point", e)
	}
	if _, e = single.VerifyNotification(env(), boundary, h, raw); e != nil {
		t.Fatal("key inclusive verify", e)
	}
	u.DeliveryTime = fmt.Sprint(boundary.Add(time.Second).Unix())
	if _, e = single.SignNotification(env(), u, raw); e != p.ErrKey {
		t.Fatal("expired sign")
	}
	var nilKeys *p.Keyring
	if nilKeys.ActiveID() != "" || nilKeys.HasVersion("x") {
		t.Fatal("nil key queries")
	}
	if _, e = nilKeys.SignNotification(env(), unsigned(), raw); e != p.ErrKey {
		t.Fatal("nil sign")
	}
	if _, e = nilKeys.VerifyNotification(env(), now(), h, raw); e != p.ErrKey {
		t.Fatal("nil verify")
	}
	if _, e = nilKeys.SignCommandReply(env(), request(t), unsigned(), nil); e != p.ErrKey {
		t.Fatal("nil command sign")
	}
	if _, e = nilKeys.SignQueryReply(env(), query(t), unsigned(), nil); e != p.ErrKey {
		t.Fatal("nil query sign")
	}
	if _, e = nilKeys.VerifyCommandReply(env(), request(t), now(), h, raw); e != p.ErrKey {
		t.Fatal("nil command verify")
	}
	if _, e = nilKeys.VerifyQueryReply(env(), query(t), now(), h, raw); e != p.ErrKey {
		t.Fatal("nil query verify")
	}
}

func TestOpaqueZeroFactAndConcurrentReadCopies(t *testing.T) {
	var c p.VerifiedCommandReply
	var q p.VerifiedQueryReply
	var n p.VerifiedNotification
	var f p.VerifiedRefundFact
	for _, canonical := range []func() ([]byte, error){c.Canonical, q.Canonical, n.Canonical, f.Canonical} {
		b, e := canonical()
		if b != nil || e != p.ErrInvalid {
			t.Fatal("zero canonical")
		}
	}
	for _, fact := range []func() (p.VerifiedRefundFact, error){c.Fact, q.Fact, n.Fact} {
		v, e := fact()
		if e != p.ErrInvalid {
			t.Fatal("zero fact")
		}
		if b, e := v.Canonical(); b != nil || e != p.ErrInvalid {
			t.Fatal("partial zero fact")
		}
	}
	if d, e := f.Digest(); d != "" || e != p.ErrInvalid {
		t.Fatal("zero fact digest")
	}
	k := keys(t)
	command := request(t)
	query := query(t)
	for _, state := range []string{"not_seen", "in_progress"} {
		r := queryReply(t)
		r.CommandState = state
		r.Result = nil
		raw := mustJSON(t, r)
		h, e := k.SignQueryReply(env(), query, unsigned(), raw)
		if e != nil {
			t.Fatal(e)
		}
		v, e := k.VerifyQueryReply(env(), query, now(), h, raw)
		if e != nil {
			t.Fatal(e)
		}
		z, e := v.Fact()
		if e != p.ErrInvalid {
			t.Fatal("nonterminal fact")
		}
		if d, e := z.Digest(); d != "" || e != p.ErrInvalid {
			t.Fatal("nonterminal zero digest")
		}
	}
	r := commandReply(t)
	r.CommandState = "in_progress"
	r.Result = nil
	raw := mustJSON(t, r)
	h, e := k.SignCommandReply(env(), command, unsigned(), raw)
	if e != nil {
		t.Fatal(e)
	}
	v, e := k.VerifyCommandReply(env(), command, now(), h, raw)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.Fact(); e != p.ErrInvalid {
		t.Fatal("inprogress command fact")
	}
	nr := mustJSON(t, notification(t))
	nh, e := k.SignNotification(env(), unsigned(), nr)
	if e != nil {
		t.Fatal(e)
	}
	nv, e := k.VerifyNotification(env(), now(), nh, nr)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	fail := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			for range 100 {
				b, e := nv.Canonical()
				if e != nil || !bytes.Equal(b, nr) {
					fail <- fmt.Errorf("canonical read: %v", e)
					return
				}
				b[0] = '!'
				fact, e := nv.Fact()
				if e != nil {
					fail <- e
					return
				}
				d, e := fact.Digest()
				if e != nil || len(d) != 64 {
					fail <- fmt.Errorf("digest read: %v", e)
					return
				}
				if _, e = k.VerifyNotification(env(), now(), nh, nr); e != nil {
					fail <- e
					return
				}
			}
		})
	}
	wg.Wait()
	close(fail)
	for e := range fail {
		t.Fatal(e)
	}
}
