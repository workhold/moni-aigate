package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Secrets struct {
	Keys   map[string][]byte
	Active string
	Pepper []byte
}

func (s *Secrets) Validate() error {
	if len(s.Pepper) != 32 || len(s.Keys[s.Active]) != 32 {
		return errors.New("32-byte authentication and encryption keys required")
	}
	for id, k := range s.Keys {
		if id == "" || strings.Contains(id, ".") || len(k) != 32 {
			return errors.New("invalid encryption keyring")
		}
	}
	return nil
}
func Random(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Secrets) Digest(value string) string {
	h := hmac.New(sha256.New, s.Pepper)
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Secrets) Seal(value string) (string, error) {
	block, e := aes.NewCipher(s.Keys[s.Active])
	if e != nil {
		return "", e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return "", e
	}
	ad := []byte("moni-aigate:v1:" + s.Active)
	encrypted := a.Seal(nonce, nonce, []byte(value), ad)
	return "v1." + s.Active + "." + base64.RawURLEncoding.EncodeToString(encrypted), nil
}
func (s *Secrets) Open(value string) (string, error) {
	p := strings.Split(value, ".")
	if len(p) != 3 || p[0] != "v1" {
		return "", errors.New("invalid ciphertext")
	}
	key, ok := s.Keys[p[1]]
	if !ok {
		return "", errors.New("encryption key unavailable")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	b, e := base64.RawURLEncoding.DecodeString(p[2])
	if e != nil || len(b) < a.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	plain, e := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte("moni-aigate:v1:"+p[1]))
	if e != nil {
		return "", errors.New("ciphertext authentication failed")
	}
	return string(plain), nil
}
func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 72 {
		return "", errors.New("密码须为12至72字节")
	}
	v, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(v), e
}
func CheckPassword(hash, password string) bool {
	return len(password) <= 72 && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}
func TOTP(secret string, step int64) (string, error) {
	key, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if e != nil {
		return "", e
	}
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(step))
	h := hmac.New(sha1.New, key)
	h.Write(b)
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 15
	v := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", v), nil
}
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	if _, e := strconv.Atoi(code); e != nil {
		return 0, false
	}
	step := now.Unix() / 30
	for _, s := range []int64{step, step - 1, step + 1} {
		expected, e := TOTP(secret, s)
		if e == nil && s > lastStep && subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}
func OTPURL(email, secret string) string {
	return "otpauth://totp/" + url.PathEscape("moni-aigate:"+email) + "?secret=" + url.QueryEscape(secret) + "&issuer=moni-aigate&algorithm=SHA1&digits=6&period=30"
}

// SealFor binds ciphertext to its purpose and owning record. Swapping rows fails authentication.
func (s *Secrets) SealFor(value, purpose, id string) (string, error) {
	if purpose == "" || id == "" {
		return "", errors.New("ciphertext identity required")
	}
	block, e := aes.NewCipher(s.Keys[s.Active])
	if e != nil {
		return "", e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return "", e
	}
	ad := []byte("moni-aigate:v2:" + s.Active + ":" + purpose + ":" + id)
	encrypted := a.Seal(nonce, nonce, []byte(value), ad)
	return "v2." + s.Active + "." + base64.RawURLEncoding.EncodeToString(encrypted), nil
}
func (s *Secrets) OpenFor(value, purpose, id string) (string, error) {
	p := strings.Split(value, ".")
	if len(p) != 3 || p[0] != "v2" || purpose == "" || id == "" {
		return "", errors.New("invalid ciphertext identity")
	}
	key, ok := s.Keys[p[1]]
	if !ok {
		return "", errors.New("encryption key unavailable")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	b, e := base64.RawURLEncoding.DecodeString(p[2])
	if e != nil || len(b) < a.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	plain, e := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte("moni-aigate:v2:"+p[1]+":"+purpose+":"+id))
	if e != nil {
		return "", errors.New("ciphertext authentication failed")
	}
	return string(plain), nil
}
