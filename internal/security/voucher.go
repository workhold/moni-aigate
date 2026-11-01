package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var ErrVoucherKeyConfiguration = errors.New("voucher_key_configuration")
var ErrVoucherKeyUnavailable = errors.New("voucher_key_unavailable")

// VoucherKeyring is immutable and independent from authentication/AES keys.
type VoucherKeyring struct {
	active string
	keys   map[string][]byte
}

func voucherKeyVersion(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func ParseVoucherKeyring(rawJSON, active string) (*VoucherKeyring, error) {
	if len(rawJSON) > 65536 || !utf8.ValidString(rawJSON) || !voucherKeyVersion(active) {
		return nil, ErrVoucherKeyConfiguration
	}
	d := json.NewDecoder(strings.NewReader(rawJSON))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return nil, ErrVoucherKeyConfiguration
	}
	keys := map[string][]byte{}
	for d.More() {
		t, e := d.Token()
		id, ok := t.(string)
		if e != nil || !ok || !voucherKeyVersion(id) || keys[id] != nil || len(keys) >= 128 {
			return nil, ErrVoucherKeyConfiguration
		}
		var encoded string
		if d.Decode(&encoded) != nil {
			return nil, ErrVoucherKeyConfiguration
		}
		key, e := base64.RawURLEncoding.DecodeString(encoded)
		if e != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != encoded {
			return nil, ErrVoucherKeyConfiguration
		}
		keys[id] = key
	}
	if t, e := d.Token(); e != nil || t != json.Delim('}') {
		return nil, ErrVoucherKeyConfiguration
	}
	if _, e = d.Token(); e != io.EOF || keys[active] == nil {
		return nil, ErrVoucherKeyConfiguration
	}
	return &VoucherKeyring{active: active, keys: keys}, nil
}
func (k *VoucherKeyring) ActiveVersion() string {
	if k == nil {
		return ""
	}
	return k.active
}
func (k *VoucherKeyring) HasVersion(v string) bool { return k != nil && len(k.keys[v]) == 32 }
func (k *VoucherKeyring) digest(version, domain, id, secret string) (string, error) {
	if !k.HasVersion(version) {
		return "", ErrVoucherKeyUnavailable
	}
	u, e := uuid.Parse(id)
	raw, se := base64.RawURLEncoding.DecodeString(secret)
	if e != nil || u == uuid.Nil || u.String() != id || se != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != secret {
		return "", ErrVoucherKeyConfiguration
	}
	b, _ := json.Marshal(struct {
		ID     string `json:"voucher_id"`
		Secret string `json:"secret"`
	}{id, secret})
	h := hmac.New(sha256.New, k.keys[version])
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (k *VoucherKeyring) CodeDigest(version, id, secret string) (string, error) {
	return k.digest(version, "moni-aigate:voucher-code:v1", id, secret)
}
func (k *VoucherKeyring) CommandFingerprint(version, id, secret string) (string, error) {
	return k.digest(version, "moni-aigate:voucher-command:v1", id, secret)
}
