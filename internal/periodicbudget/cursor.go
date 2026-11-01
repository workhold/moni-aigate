package periodicbudget

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
)

const (
	cursorKeyDomain       = "moni-aigate:periodic-budget:cursor-key:v1"
	cursorSignatureDomain = "moni-aigate:periodic-budget:cursor-signature:v1"
	cursorVersion         = "pbc1"
	maxCursorBytes        = 1024
	maxCursorPayloadBytes = 512
)

type CursorBinding struct {
	Route         string
	Scope         string
	ScopeID       string
	IdentityScope string
	Order         string
}

type CursorPosition struct {
	At string
	ID string
}

type CursorCodec struct {
	key         [32]byte
	initialized bool
}

func (*CursorCodec) String() string     { return "periodicbudget.CursorCodec(redacted)" }
func (c *CursorCodec) GoString() string { return c.String() }

func NewCursorCodec(pepper []byte) (*CursorCodec, error) {
	if len(pepper) != 32 {
		return nil, ErrUnavailable
	}
	h := hmac.New(sha256.New, pepper)
	_, _ = h.Write([]byte(cursorKeyDomain))
	c := &CursorCodec{initialized: true}
	copy(c.key[:], h.Sum(nil))
	return c, nil
}

type cursorPayload struct {
	V             int    `json:"v"`
	Route         string `json:"route"`
	Scope         string `json:"scope"`
	ScopeID       string `json:"scope_id"`
	IdentityScope string `json:"identity_scope"`
	Order         string `json:"order"`
	LastAt        string `json:"last_at"`
	LastID        string `json:"last_id"`
}

func validCursorBinding(b CursorBinding) bool {
	if !ValidUUID(b.ScopeID) {
		return false
	}
	var scope, identity, order string
	switch b.Route {
	case "platform_tenant_windows", "platform_tenant_allocations":
		scope, identity = "tenant", "platform"
	case "wallet_windows", "wallet_allocations":
		scope, identity = "tenant", "tenant:"+b.ScopeID
	case "key_windows", "key_allocations":
		scope = "key"
		if !strings.HasPrefix(b.IdentityScope, "tenant:") || !ValidUUID(strings.TrimPrefix(b.IdentityScope, "tenant:")) {
			return false
		}
		identity = b.IdentityScope
	case "procurement_windows", "procurement_allocations":
		scope, identity = "procurement", "platform"
	default:
		return false
	}
	if strings.HasSuffix(b.Route, "_windows") {
		order = "start_at_desc_id_c_desc"
	} else {
		order = "created_at_desc_id_c_desc"
	}
	return b.Scope == scope && b.IdentityScope == identity && b.Order == order
}

func encodeCursorPayload(p cursorPayload) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(p) != nil {
		return nil, ErrInvalidCursor
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}

func (c *CursorCodec) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, c.key[:])
	_, _ = h.Write([]byte(cursorSignatureDomain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(cursorVersion))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	return h.Sum(nil)
}

func (c *CursorCodec) Encode(binding CursorBinding, position CursorPosition) (string, error) {
	if c == nil || !c.initialized || !validCursorBinding(binding) || !ValidUTC6(position.At) || !ValidUUID(position.ID) {
		return "", ErrInvalidCursor
	}
	payload, err := encodeCursorPayload(cursorPayload{1, binding.Route, binding.Scope, binding.ScopeID, binding.IdentityScope, binding.Order, position.At, position.ID})
	if err != nil || len(payload) > maxCursorPayloadBytes {
		return "", ErrInvalidCursor
	}
	return cursorVersion + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(c.mac(payload)), nil
}

func rawURLSegment(segment string, maxDecoded int) ([]byte, bool) {
	if len(segment) == 0 || len(segment) > base64.RawURLEncoding.EncodedLen(maxDecoded) {
		return nil, false
	}
	for i := range segment {
		b := segment[i]
		if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return nil, false
		}
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(segment)
	return decoded, err == nil && len(decoded) <= maxDecoded && base64.RawURLEncoding.EncodeToString(decoded) == segment
}

func (c *CursorCodec) Decode(binding CursorBinding, token string) (CursorPosition, error) {
	var zero CursorPosition
	if c == nil || !c.initialized || len(token) == 0 || len(token) > maxCursorBytes {
		return zero, ErrInvalidCursor
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != cursorVersion || len(parts[2]) != 43 {
		return zero, ErrInvalidCursor
	}
	payload, ok := rawURLSegment(parts[1], maxCursorPayloadBytes)
	if !ok {
		return zero, ErrInvalidCursor
	}
	mac, ok := rawURLSegment(parts[2], sha256.Size)
	if !ok || len(mac) != sha256.Size || !hmac.Equal(c.mac(payload), mac) {
		return zero, ErrInvalidCursor
	}
	if !validJSONUnicode(payload) {
		return zero, ErrInvalidCursor
	}
	var p cursorPayload
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || p.V != 1 || !ValidUTC6(p.LastAt) || !ValidUUID(p.LastID) {
		return zero, ErrInvalidCursor
	}
	canonical, err := encodeCursorPayload(p)
	if err != nil || !bytes.Equal(payload, canonical) {
		return zero, ErrInvalidCursor
	}
	actual := CursorBinding{p.Route, p.Scope, p.ScopeID, p.IdentityScope, p.Order}
	if !validCursorBinding(binding) || actual != binding {
		return zero, ErrInvalidCursor
	}
	return CursorPosition{At: p.LastAt, ID: p.LastID}, nil
}
