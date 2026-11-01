package paymentrefundproto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	commandReplyDomain = "moni-aigate/payment-refund-command-reply/v2"
	queryReplyDomain   = "moni-aigate/payment-refund-query-reply/v2"
	notificationDomain = "moni-aigate/payment-refund-notification/v2"
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
		return nil, ErrKey
	}
	k := &Keyring{active: active, keys: make(map[string]signingKey, len(specs))}
	material := make(map[[32]byte]bool, len(specs))
	for _, s := range specs {
		if !version(s.ID) || len(s.Key) != 32 || !keyTime(s.NotBefore) || !keyTime(s.NotAfter) || s.NotAfter.Before(s.NotBefore) {
			return nil, ErrKey
		}
		if _, exists := k.keys[s.ID]; exists {
			return nil, ErrKey
		}
		var key [32]byte
		copy(key[:], s.Key)
		if material[key] {
			return nil, ErrKey
		}
		material[key] = true
		k.keys[s.ID] = signingKey{key, s.NotBefore, s.NotAfter}
	}
	if !k.HasVersion(active) {
		return nil, ErrKey
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
	_, exists := k.keys[id]
	return exists
}
func (k *Keyring) configured() bool { return k != nil && k.HasVersion(k.active) }
func headerName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := range s {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}
func unixSeconds(s string) (time.Time, bool) {
	if len(s) == 0 || len(s) > 12 || len(s) > 1 && s[0] == '0' {
		return time.Time{}, false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return time.Time{}, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > 253402300799 {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}
func headersValid(h SignedHeaders, signed bool) bool {
	_, timeOK := unixSeconds(h.DeliveryTime)
	return version(h.KeyID) && uuidValid(h.DeliveryID) && timeOK &&
		(signed && lowerHex(h.Signature) || !signed && h.Signature == "")
}
func ReadSignedHeaders(raw http.Header) (SignedHeaders, error) {
	names := [4]string{"X-Moni-Refund-V2-Key-Id", "X-Moni-Refund-V2-Delivery-Id", "X-Moni-Refund-V2-Delivery-Time", "X-Moni-Refund-V2-Signature"}
	var values [4]string
	var seen [4]bool
	for name, v := range raw {
		if !headerName(name) {
			return SignedHeaders{}, ErrSignature
		}
		for i, want := range names {
			if strings.EqualFold(name, want) {
				if seen[i] || len(v) != 1 || strings.Contains(v[0], ",") {
					return SignedHeaders{}, ErrSignature
				}
				seen[i], values[i] = true, v[0]
			}
		}
	}
	for _, s := range seen {
		if !s {
			return SignedHeaders{}, ErrSignature
		}
	}
	h := SignedHeaders{values[0], values[1], values[2], values[3]}
	if !headersValid(h, true) {
		return SignedHeaders{}, ErrSignature
	}
	return h, nil
}
func signature(key signingKey, domain string, env Environment, context string, h SignedHeaders, raw []byte) string {
	mac := hmac.New(sha256.New, key.bytes[:])
	bodySum := sha256.Sum256(raw)
	for _, part := range []string{domain, h.KeyID, env.ID, env.MerchantID, context, h.DeliveryID, h.DeliveryTime, hex.EncodeToString(bodySum[:])} {
		writeFrame(mac, []byte(part))
	}
	return hex.EncodeToString(mac.Sum(nil))
}
func (k *Keyring) signingHeaders(env Environment, h SignedHeaders) (SignedHeaders, signingKey, error) {
	if !k.configured() {
		return SignedHeaders{}, signingKey{}, ErrKey
	}
	if !environmentValid(env) {
		return SignedHeaders{}, signingKey{}, ErrInvalid
	}
	if h.KeyID != "" && h.KeyID != k.active {
		return SignedHeaders{}, signingKey{}, ErrKey
	}
	h.KeyID = k.active
	if !headersValid(h, false) {
		return SignedHeaders{}, signingKey{}, ErrInvalid
	}
	t, _ := unixSeconds(h.DeliveryTime)
	key := k.keys[k.active]
	if t.Before(key.before) || t.After(key.after) {
		return SignedHeaders{}, signingKey{}, ErrKey
	}
	return h, key, nil
}
func (k *Keyring) verifyHeaders(env Environment, receivedAt time.Time, h SignedHeaders, raw []byte, domain, context string) error {
	if !k.configured() {
		return ErrKey
	}
	if !environmentValid(env) || receivedAt.IsZero() || receivedAt.UTC().Year() < 1 || receivedAt.UTC().Year() > 9999 || len(raw) == 0 || len(raw) > bodyLimit {
		return ErrInvalid
	}
	key, exists := k.keys[h.KeyID]
	t, timeOK := unixSeconds(h.DeliveryTime)
	if !exists || !headersValid(h, true) || !timeOK ||
		t.Before(receivedAt.Add(-300*time.Second)) || t.After(receivedAt.Add(300*time.Second)) ||
		t.Before(key.before) || t.After(key.after) {
		return ErrSignature
	}
	expected := signature(key, domain, env, context, h, raw)
	if !hmac.Equal([]byte(expected), []byte(h.Signature)) {
		return ErrSignature
	}
	return nil
}
func commandEnvironment(env Environment, command RefundRequest) error {
	if !environmentValid(env) || !requestValid(command) {
		return ErrInvalid
	}
	if command.EnvironmentID != env.ID || command.MerchantID != env.MerchantID {
		return ErrBinding
	}
	return nil
}
func commandBound(command RefundRequest, r CommandReply) error {
	d, _ := ParameterDigest(command)
	if r.Command != command || r.ParameterDigest != d {
		return ErrBinding
	}
	return nil
}
func queryEnvironment(env Environment, q QueryRequest) error {
	if !queryValid(q) {
		return ErrInvalid
	}
	return commandEnvironment(env, q.Command)
}
func queryBound(q QueryRequest, r QueryReply) error {
	if (QueryRequest{r.SchemaVersion, r.QueryID, r.MutationGeneration, r.Command, r.ParameterDigest}) != q {
		return ErrBinding
	}
	return nil
}
func notificationBound(env Environment, n Notification) error {
	if n.Fact.EnvironmentID != env.ID || n.Fact.MerchantID != env.MerchantID {
		return ErrBinding
	}
	return nil
}
func completedTime(result *RefundFact, receivedAt time.Time) error {
	if result != nil {
		t, ok := instant(result.CompletedAt)
		if !ok {
			return ErrInvalid
		}
		if t.After(receivedAt.Add(5 * time.Second)) {
			return ErrSignature
		}
	}
	return nil
}
func (k *Keyring) SignCommandReply(env Environment, command RefundRequest, h SignedHeaders, raw []byte) (SignedHeaders, error) {
	h, key, err := k.signingHeaders(env, h)
	if err != nil {
		return SignedHeaders{}, err
	}
	if err := commandEnvironment(env, command); err != nil {
		return SignedHeaders{}, err
	}
	r, err := ParseCommandReply(raw)
	if err != nil {
		return SignedHeaders{}, err
	}
	if err := commandBound(command, r); err != nil {
		return SignedHeaders{}, err
	}
	d, _ := ParameterDigest(command)
	h.Signature = signature(key, commandReplyDomain, env, d, h, raw)
	return h, nil
}
func (k *Keyring) SignQueryReply(env Environment, q QueryRequest, h SignedHeaders, raw []byte) (SignedHeaders, error) {
	h, key, err := k.signingHeaders(env, h)
	if err != nil {
		return SignedHeaders{}, err
	}
	if err := queryEnvironment(env, q); err != nil {
		return SignedHeaders{}, err
	}
	r, err := ParseQueryReply(raw)
	if err != nil {
		return SignedHeaders{}, err
	}
	if err := queryBound(q, r); err != nil {
		return SignedHeaders{}, err
	}
	d, _ := QueryDigest(q)
	h.Signature = signature(key, queryReplyDomain, env, d, h, raw)
	return h, nil
}
func (k *Keyring) SignNotification(env Environment, h SignedHeaders, raw []byte) (SignedHeaders, error) {
	h, key, err := k.signingHeaders(env, h)
	if err != nil {
		return SignedHeaders{}, err
	}
	n, err := ParseNotification(raw)
	if err != nil {
		return SignedHeaders{}, err
	}
	if err := notificationBound(env, n); err != nil {
		return SignedHeaders{}, err
	}
	h.Signature = signature(key, notificationDomain, env, "", h, raw)
	return h, nil
}
func (k *Keyring) VerifyCommandReply(env Environment, command RefundRequest, receivedAt time.Time, h SignedHeaders, raw []byte) (VerifiedCommandReply, error) {
	if !k.configured() {
		return VerifiedCommandReply{}, ErrKey
	}
	if err := commandEnvironment(env, command); err != nil {
		return VerifiedCommandReply{}, err
	}
	d, _ := ParameterDigest(command)
	if err := k.verifyHeaders(env, receivedAt, h, raw, commandReplyDomain, d); err != nil {
		return VerifiedCommandReply{}, err
	}
	r, err := ParseCommandReply(raw)
	if err != nil {
		return VerifiedCommandReply{}, err
	}
	if err := commandBound(command, r); err != nil {
		return VerifiedCommandReply{}, err
	}
	if err := completedTime(r.Result, receivedAt); err != nil {
		return VerifiedCommandReply{}, err
	}
	canonical, err := CanonicalCommandReply(r)
	if err != nil {
		return VerifiedCommandReply{}, err
	}
	var fact []byte
	if r.Result != nil {
		fact, err = CanonicalRefundFact(*r.Result)
		if err != nil {
			return VerifiedCommandReply{}, err
		}
	}
	return VerifiedCommandReply{canonical, fact}, nil
}
func (k *Keyring) VerifyQueryReply(env Environment, q QueryRequest, receivedAt time.Time, h SignedHeaders, raw []byte) (VerifiedQueryReply, error) {
	if !k.configured() {
		return VerifiedQueryReply{}, ErrKey
	}
	if err := queryEnvironment(env, q); err != nil {
		return VerifiedQueryReply{}, err
	}
	d, _ := QueryDigest(q)
	if err := k.verifyHeaders(env, receivedAt, h, raw, queryReplyDomain, d); err != nil {
		return VerifiedQueryReply{}, err
	}
	r, err := ParseQueryReply(raw)
	if err != nil {
		return VerifiedQueryReply{}, err
	}
	if err := queryBound(q, r); err != nil {
		return VerifiedQueryReply{}, err
	}
	if err := completedTime(r.Result, receivedAt); err != nil {
		return VerifiedQueryReply{}, err
	}
	canonical, err := CanonicalQueryReply(r)
	if err != nil {
		return VerifiedQueryReply{}, err
	}
	var fact []byte
	if r.Result != nil {
		fact, err = CanonicalRefundFact(*r.Result)
		if err != nil {
			return VerifiedQueryReply{}, err
		}
	}
	return VerifiedQueryReply{canonical, fact}, nil
}
func (k *Keyring) VerifyNotification(env Environment, receivedAt time.Time, h SignedHeaders, raw []byte) (VerifiedNotification, error) {
	if err := k.verifyHeaders(env, receivedAt, h, raw, notificationDomain, ""); err != nil {
		return VerifiedNotification{}, err
	}
	n, err := ParseNotification(raw)
	if err != nil {
		return VerifiedNotification{}, err
	}
	if err := notificationBound(env, n); err != nil {
		return VerifiedNotification{}, err
	}
	if err := completedTime(&n.Fact, receivedAt); err != nil {
		return VerifiedNotification{}, err
	}
	canonical, err := CanonicalNotification(n)
	if err != nil {
		return VerifiedNotification{}, err
	}
	fact, err := CanonicalRefundFact(n.Fact)
	if err != nil {
		return VerifiedNotification{}, err
	}
	return VerifiedNotification{canonical, fact}, nil
}
