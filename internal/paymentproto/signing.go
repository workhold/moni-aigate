package paymentproto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type signingKey struct {
	bytes         [32]byte
	before, after time.Time
}
type Keyring struct {
	active string
	keys   map[string]signingKey
}

func keyTime(t time.Time) bool {
	return !t.IsZero() && t.Location() == time.UTC && t.Nanosecond()%1000 == 0 && t.Year() >= 1 && t.Year() <= 9999
}
func NewKeyring(active string, specs []KeySpec) (*Keyring, error) {
	if !version(active) || len(specs) < 1 || len(specs) > 128 {
		return nil, fail("payment_key_unavailable")
	}
	k := &Keyring{active: active, keys: make(map[string]signingKey, len(specs))}
	material := map[[32]byte]bool{}
	for _, s := range specs {
		if !version(s.ID) || len(s.Key) != 32 || !keyTime(s.NotBefore) || !keyTime(s.NotAfter) || s.NotAfter.Before(s.NotBefore) {
			return nil, fail("payment_key_unavailable")
		}
		if _, ok := k.keys[s.ID]; ok {
			return nil, fail("payment_key_unavailable")
		}
		var b [32]byte
		copy(b[:], s.Key)
		if material[b] {
			return nil, fail("payment_key_unavailable")
		}
		material[b] = true
		k.keys[s.ID] = signingKey{bytes: b, before: s.NotBefore, after: s.NotAfter}
	}
	if !k.HasVersion(active) {
		return nil, fail("payment_key_unavailable")
	}
	return k, nil
}
func (k *Keyring) ActiveID() string {
	if k == nil {
		return ""
	}
	return k.active
}
func (k *Keyring) HasVersion(id string) bool {
	if k == nil {
		return false
	}
	_, ok := k.keys[id]
	return ok
}
func (k *Keyring) configured() bool { return k != nil && k.HasVersion(k.active) }
func headerName(name string) bool {
	if name == "" {
		return false
	}
	for i := range name {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}
func unixSeconds(s string) (time.Time, bool) {
	if len(s) == 0 || len(s) > 12 || (len(s) > 1 && s[0] == '0') {
		return time.Time{}, false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return time.Time{}, false
		}
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n > 253402300799 {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}
func headersValid(h SignedHeaders, signed bool) bool {
	_, ok := unixSeconds(h.DeliveryTime)
	return version(h.KeyID) && uuidValid(h.DeliveryID) && ok && ((signed && lowerHex(h.Signature)) || (!signed && h.Signature == ""))
}
func ReadSignedHeaders(raw http.Header) (SignedHeaders, error) {
	names := []string{"X-Moni-Key-Id", "X-Moni-Delivery-Id", "X-Moni-Delivery-Time", "X-Moni-Signature"}
	values := make([]string, 4)
	seen := make([]bool, 4)
	for name, v := range raw {
		if !headerName(name) {
			return SignedHeaders{}, fail("payment_signature_invalid")
		}
		for i, want := range names {
			if strings.EqualFold(name, want) {
				if seen[i] || len(v) != 1 || strings.Contains(v[0], ",") {
					return SignedHeaders{}, fail("payment_signature_invalid")
				}
				seen[i] = true
				values[i] = v[0]
			}
		}
	}
	for _, ok := range seen {
		if !ok {
			return SignedHeaders{}, fail("payment_signature_invalid")
		}
	}
	h := SignedHeaders{values[0], values[1], values[2], values[3]}
	if !headersValid(h, true) {
		return SignedHeaders{}, fail("payment_signature_invalid")
	}
	return h, nil
}
func signature(key signingKey, domain string, env Environment, q *QueryRequest, h SignedHeaders, raw []byte) string {
	parts := []string{domain, env.ID, env.MerchantID}
	if q != nil {
		parts = append(parts, q.QueryID, q.CommandID)
	}
	parts = append(parts, h.DeliveryID, h.DeliveryTime, RequestDigest(raw))
	mac := hmac.New(sha256.New, key.bytes[:])
	mac.Write([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}
func (k *Keyring) signingHeaders(env Environment, h SignedHeaders) (SignedHeaders, signingKey, error) {
	if !k.configured() {
		return SignedHeaders{}, signingKey{}, fail("payment_key_unavailable")
	}
	if !environmentValid(env) {
		return SignedHeaders{}, signingKey{}, fail("payment_environment_invalid")
	}
	if h.KeyID != "" && h.KeyID != k.active {
		return SignedHeaders{}, signingKey{}, fail("invalid_request")
	}
	h.KeyID = k.active
	if !headersValid(h, false) {
		return SignedHeaders{}, signingKey{}, fail("invalid_request")
	}
	t, _ := unixSeconds(h.DeliveryTime)
	key := k.keys[k.active]
	if t.Before(key.before) || t.After(key.after) {
		return SignedHeaders{}, signingKey{}, fail("payment_key_unavailable")
	}
	return h, key, nil
}
func queryBound(env Environment, q QueryRequest, r QueryReply) error {
	if q.EnvironmentID != env.ID || q.MerchantID != env.MerchantID || r.EnvironmentID != env.ID || r.MerchantID != env.MerchantID {
		return fail("payment_environment_invalid")
	}
	if r.QueryID != q.QueryID || r.CommandID != q.CommandID || r.MerchantOrderID != q.MerchantOrderID || r.CommandKind != q.CommandKind || r.ParameterDigest != q.ParameterDigest {
		return fail("invalid_request")
	}
	return nil
}
func (k *Keyring) SignNotification(env Environment, h SignedHeaders, raw []byte) (SignedHeaders, error) {
	h, key, e := k.signingHeaders(env, h)
	if e != nil {
		return SignedHeaders{}, e
	}
	n, e := ParseNotification(raw)
	if e != nil {
		return SignedHeaders{}, e
	}
	if n.EnvironmentID != env.ID || n.MerchantID != env.MerchantID {
		return SignedHeaders{}, fail("payment_environment_invalid")
	}
	h.Signature = signature(key, "moni-payment-sandbox-v1", env, nil, h, raw)
	return h, nil
}
func (k *Keyring) SignQuery(env Environment, q QueryRequest, h SignedHeaders, raw []byte) (SignedHeaders, error) {
	if !queryValid(q) {
		return SignedHeaders{}, fail("invalid_request")
	}
	h, key, e := k.signingHeaders(env, h)
	if e != nil {
		return SignedHeaders{}, e
	}
	r, e := ParseQueryReply(raw)
	if e != nil {
		return SignedHeaders{}, e
	}
	if e = queryBound(env, q, r); e != nil {
		return SignedHeaders{}, e
	}
	h.Signature = signature(key, "moni-payment-sandbox-query-v1", env, &q, h, raw)
	return h, nil
}
func (k *Keyring) verifyHeaders(env Environment, now time.Time, h SignedHeaders, raw []byte, query *QueryRequest) error {
	if !k.configured() {
		return fail("payment_key_unavailable")
	}
	if !environmentValid(env) {
		return fail("payment_environment_invalid")
	}
	if now.IsZero() || now.UTC().Year() < 1 || now.UTC().Year() > 9999 || len(raw) == 0 || len(raw) > bodyLimit {
		return fail("invalid_request")
	}
	key, exists := k.keys[h.KeyID]
	t, validTime := unixSeconds(h.DeliveryTime)
	if !exists || !headersValid(h, true) || !validTime || t.Before(now.Add(-300*time.Second)) || t.After(now.Add(300*time.Second)) || t.Before(key.before) || t.After(key.after) {
		return fail("payment_signature_invalid")
	}
	domain := "moni-payment-sandbox-v1"
	if query != nil {
		domain = "moni-payment-sandbox-query-v1"
	}
	expected := signature(key, domain, env, query, h, raw)
	if !hmac.Equal([]byte(expected), []byte(h.Signature)) {
		return fail("payment_signature_invalid")
	}
	return nil
}
func (k *Keyring) VerifyNotification(env Environment, now time.Time, h SignedHeaders, raw []byte) (VerifiedNotification, error) {
	if e := k.verifyHeaders(env, now, h, raw, nil); e != nil {
		return VerifiedNotification{}, e
	}
	n, e := ParseNotification(raw)
	if e != nil {
		return VerifiedNotification{}, e
	}
	if n.EnvironmentID != env.ID || n.MerchantID != env.MerchantID {
		return VerifiedNotification{}, fail("payment_environment_invalid")
	}
	t, _ := instant(n.OccurredAt)
	if t.After(now.Add(5 * time.Second)) {
		return VerifiedNotification{}, fail("invalid_request")
	}
	canonical, e := CanonicalNotification(n)
	if e != nil {
		return VerifiedNotification{}, e
	}
	return VerifiedNotification{canonical: canonical, keyID: h.KeyID}, nil
}
func (k *Keyring) VerifyQuery(env Environment, q QueryRequest, now time.Time, h SignedHeaders, raw []byte) (VerifiedQuery, error) {
	if !queryValid(q) {
		return VerifiedQuery{}, fail("invalid_request")
	}
	if q.EnvironmentID != env.ID || q.MerchantID != env.MerchantID {
		return VerifiedQuery{}, fail("payment_environment_invalid")
	}
	if e := k.verifyHeaders(env, now, h, raw, &q); e != nil {
		return VerifiedQuery{}, e
	}
	r, e := ParseQueryReply(raw)
	if e != nil {
		return VerifiedQuery{}, e
	}
	if e = queryBound(env, q, r); e != nil {
		return VerifiedQuery{}, e
	}
	if r.Result != nil {
		var value string
		if r.Result.Capture != nil {
			value = r.Result.Capture.OccurredAt
		}
		if r.Result.ClosedAt != nil {
			value = *r.Result.ClosedAt
		}
		if value != "" {
			t, _ := instant(value)
			if t.After(now.Add(5 * time.Second)) {
				return VerifiedQuery{}, fail("invalid_request")
			}
		}
	}
	canonical, e := CanonicalQueryReply(r)
	if e != nil {
		return VerifiedQuery{}, e
	}
	return VerifiedQuery{canonical: canonical, keyID: h.KeyID}, nil
}
