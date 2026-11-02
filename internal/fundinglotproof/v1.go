package fundinglotproof

// The private v1 codec preserves the immutable application wire protocol.
// It deliberately has no dependency on online payment/runtime packages.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

var v1HashText = regexp.MustCompile(`^[0-9a-f]{64}$`)

type v1RolePin struct {
	Name  string `json:"name"`
	OID   uint32 `json:"oid"`
	Login bool   `json:"login"`
}
type v1SchemaPin struct {
	Name    string    `json:"name"`
	OID     uint32    `json:"oid"`
	Owner   v1RolePin `json:"owner"`
	Runtime v1RolePin `json:"runtime"`
}
type v1DatabasePin struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	Name string `json:"name"`
	OID  uint32 `json:"oid"`
}
type v1KeyPin struct {
	ID        string `json:"id"`
	Digest    string `json:"digest"`
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`
}
type v1Proof struct {
	ProofVersion      int           `json:"proof_version"`
	ProtocolVersion   int           `json:"protocol_version"`
	EnvironmentID     string        `json:"environment_id"`
	MerchantID        string        `json:"merchant_id"`
	PayeeLabel        string        `json:"payee_label"`
	Database          v1DatabasePin `json:"database"`
	App               v1SchemaPin   `json:"app"`
	Mock              v1SchemaPin   `json:"mock"`
	Receiver          v1SchemaPin   `json:"receiver"`
	Worker            v1RolePin     `json:"worker"`
	Projector         v1RolePin     `json:"projector"`
	AppNonceDigest    string        `json:"app_nonce_digest"`
	WorkerNonceDigest string        `json:"worker_nonce_digest"`
	APITokenDigest    string        `json:"api_token_digest"`
	APIOrigin         string        `json:"api_origin"`
	ControlOrigin     string        `json:"control_origin"`
	AppListenOrigin   string        `json:"app_listen_origin"`
	CallbackURL       string        `json:"callback_url"`
	Keys              []v1KeyPin    `json:"keys"`
}

// Independently reject duplicate keys and invalid UTF-16 rather than allowing
// encoding/json to merge names or replace malformed escaped values.
func v1ValidEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for ; i < len(raw) && raw[i] != '"'; i++ {
			if raw[i] < 0x20 {
				return false
			}
			if raw[i] != '\\' {
				continue
			}
			i++
			if i >= len(raw) {
				return false
			}
			if raw[i] != 'u' {
				if !strings.ContainsRune(`"\/bfnrt`, rune(raw[i])) {
					return false
				}
				continue
			}
			if i+4 >= len(raw) {
				return false
			}
			n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if e != nil {
				return false
			}
			i += 4
			if n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			if utf16.IsSurrogate(rune(n)) {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return false
				}
				low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
				if e != nil || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		}
		if i >= len(raw) {
			return false
		}
	}
	return true
}
func v1ReadJSONValue(d *json.Decoder, depth int) (any, bool) {
	if depth > 12 {
		return nil, false
	}
	v, e := d.Token()
	if e != nil || v == nil {
		return nil, false
	}
	switch t := v.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, e := d.Token()
				s, ok := key.(string)
				if e != nil || !ok {
					return nil, false
				}
				if _, exists := m[s]; exists {
					return nil, false
				}
				val, ok := v1ReadJSONValue(d, depth+1)
				if !ok {
					return nil, false
				}
				m[s] = val
			}
			end, e := d.Token()
			return m, e == nil && end == json.Delim('}')
		case '[':
			a := []any{}
			for d.More() {
				v, ok := v1ReadJSONValue(d, depth+1)
				if !ok {
					return nil, false
				}
				a = append(a, v)
			}
			end, e := d.Token()
			return a, e == nil && end == json.Delim(']')
		}
		return nil, false
	default:
		return t, true
	}
}
func v1StructFields(t reflect.Type) map[string]reflect.Type {
	result := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			for k, v := range v1StructFields(f.Type) {
				result[k] = v
			}
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			result[name] = f.Type
		}
	}
	return result
}
func v1ExactShape(v any, t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		fields := v1StructFields(t)
		if len(m) != len(fields) {
			return false
		}
		for k, kind := range fields {
			value, ok := m[k]
			if !ok || !v1ExactShape(value, kind) {
				return false
			}
		}
		return true
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return false
		}
		for _, v := range a {
			if !v1ExactShape(v, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.String:
		_, ok := v.(string)
		return ok
	case reflect.Bool:
		_, ok := v.(bool)
		return ok
	case reflect.Int, reflect.Uint16, reflect.Uint32:
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		s := string(n)
		if len(s) == 0 || (len(s) > 1 && s[0] == '0') {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		_, e := strconv.ParseUint(s, 10, t.Bits())
		return e == nil
	}
	return false
}
func v1StrictDecode(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || !v1ValidEscapes(raw) {
		return ErrCanonical
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, ok := v1ReadJSONValue(d, 0)
	if !ok {
		return ErrCanonical
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrCanonical
	}
	if !v1ExactShape(v, reflect.TypeOf(out).Elem()) || json.Unmarshal(raw, out) != nil {
		return ErrCanonical
	}
	return nil
}

var v1RoleName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
var v1KeyID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func v1UUID(s string) bool {
	return validUUID(s)
}
func v1Label(s string) bool {
	if len(s) == 0 || len(s) > 100 || !utf8.ValidString(s) || strings.Contains(s, "://") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func v1Ref(s string) bool {
	if !v1Label(s) {
		return false
	}
	v := strings.ToLower(s)
	if strings.HasPrefix(v, "sk-") || strings.HasPrefix(v, "mg_") || strings.HasPrefix(v, "bearer") {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}
func v1Instant(s string) (time.Time, error) {
	t, e := time.Parse("2006-01-02T15:04:05.000000Z", s)
	if e != nil || t.IsZero() || t.Year() < 1 || t.Year() > 9999 || t.UTC().Format("2006-01-02T15:04:05.000000Z") != s {
		return time.Time{}, ErrCanonical
	}
	return t.UTC(), nil
}
func v1Origin(raw string, callback bool) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || strings.Contains(raw, "#") || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.RawPath != "" {
		return "", ErrCanonical
	}
	host, port, e := net.SplitHostPort(u.Host)
	n, pe := strconv.ParseUint(port, 10, 16)
	if e != nil || pe != nil || n == 0 || host != "127.0.0.1" || port != strconv.FormatUint(n, 10) || u.Host != net.JoinHostPort(host, port) {
		return "", ErrCanonical
	}
	if callback {
		if u.Path != "/api/payments/sandbox/notifications" {
			return "", ErrCanonical
		}
	} else if u.Path != "" {
		return "", ErrCanonical
	}
	return "http://" + u.Host, nil
}
func v1SHA(raw []byte) string               { return fmtPredecessorV1SHA(sha256.Sum256(raw)) }
func fmtPredecessorV1SHA(v [32]byte) string { return hex.EncodeToString(v[:]) }
func v1CanonicalProof(p v1Proof) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(p) != nil {
		return nil, ErrCanonical
	}
	raw := b.Bytes()
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return nil, ErrCanonical
	}
	raw = raw[:len(raw)-1]
	if len(raw) > 65536 {
		return nil, ErrCanonical
	}
	return append([]byte(nil), raw...), nil
}
func v1Roles(p v1Proof) []v1RolePin {
	return []v1RolePin{p.App.Owner, p.App.Runtime, p.Mock.Owner, p.Mock.Runtime, p.Receiver.Owner, p.Receiver.Runtime, p.Worker, p.Projector}
}
func v1ValidateProof(p v1Proof) error {
	if p.ProofVersion != 1 || p.ProtocolVersion != 1 || !v1UUID(p.EnvironmentID) || !v1Ref(p.MerchantID) || !v1Label(p.PayeeLabel) || p.Database.Host != "127.0.0.1" || p.Database.Port != 55439 || p.Database.Name != "moni_test" || p.Database.OID == 0 {
		return ErrCanonical
	}
	for _, d := range []string{p.AppNonceDigest, p.WorkerNonceDigest, p.APITokenDigest} {
		if !v1HashText.MatchString(d) {
			return ErrCanonical
		}
	}
	schemas := map[uint32]bool{}
	suffix := ""
	for i, s := range []v1SchemaPin{p.App, p.Mock, p.Receiver} {
		prefix := []string{"payment_sandbox_", "payment_mock_", "payment_receiver_"}[i]
		if !regexp.MustCompile(`^`+prefix+`[0-9a-f]{32}$`).MatchString(s.Name) || s.OID == 0 || schemas[s.OID] || s.Owner.Login || !s.Runtime.Login {
			return ErrCanonical
		}
		part := strings.TrimPrefix(s.Name, prefix)
		if suffix != "" && part != suffix {
			return ErrCanonical
		}
		suffix = part
		schemas[s.OID] = true
	}
	ids := map[uint32]bool{}
	names := map[string]bool{}
	for _, r := range v1Roles(p) {
		if !v1RoleName.MatchString(r.Name) || r.OID == 0 || ids[r.OID] || names[r.Name] {
			return ErrCanonical
		}
		ids[r.OID] = true
		names[r.Name] = true
	}
	if !p.Worker.Login || !p.Projector.Login {
		return ErrCanonical
	}
	origins := map[string]bool{}
	for _, raw := range []string{p.APIOrigin, p.ControlOrigin, p.AppListenOrigin} {
		v, e := v1Origin(raw, false)
		if e != nil || origins[v] {
			return ErrCanonical
		}
		origins[v] = true
	}
	callback, e := v1Origin(p.CallbackURL, true)
	if e != nil || callback != p.AppListenOrigin {
		return ErrCanonical
	}
	if len(p.Keys) < 1 || len(p.Keys) > 128 {
		return ErrCanonical
	}
	prev := ""
	for _, k := range p.Keys {
		if !v1KeyID.MatchString(k.ID) || k.ID <= prev || !v1HashText.MatchString(k.Digest) {
			return ErrCanonical
		}
		before, e := v1Instant(k.NotBefore)
		if e != nil {
			return e
		}
		after, e := v1Instant(k.NotAfter)
		if e != nil || after.Before(before) {
			return ErrCanonical
		}
		prev = k.ID
	}
	return nil
}
