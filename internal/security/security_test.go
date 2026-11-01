package security

import (
	"bytes"
	"testing"
	"time"
)

func TestCipherRotationAndTamper(t *testing.T) {
	s := Secrets{Keys: map[string][]byte{"old": bytes.Repeat([]byte{1}, 32), "new": bytes.Repeat([]byte{2}, 32)}, Active: "old", Pepper: bytes.Repeat([]byte{3}, 32)}
	v, e := s.Seal("provider-secret")
	if e != nil {
		t.Fatal(e)
	}
	s.Active = "new"
	if got, e := s.Open(v); e != nil || got != "provider-secret" {
		t.Fatal("rotation lost old secret")
	}
	p := []byte(v)
	p[len(p)-4] ^= 1
	if _, e = s.Open(string(p)); e == nil {
		t.Fatal("accepted tampered ciphertext")
	}
	if s.Digest("customer-key") == s.Digest("another-key") {
		t.Fatal("digest collision")
	}
}
func TestTOTPReplayAndWindow(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	now := time.Unix(59, 0)
	code, e := TOTP(secret, now.Unix()/30)
	if e != nil || code != "287082" {
		t.Fatalf("RFC6238 vector: %s %v", code, e)
	}
	step, ok := VerifyTOTP(secret, code, now, -1)
	if !ok {
		t.Fatal("rejected code")
	}
	if _, ok = VerifyTOTP(secret, code, now, step); ok {
		t.Fatal("replayed code")
	}
	if _, ok = VerifyTOTP(secret, code, now.Add(2*time.Minute), -1); ok {
		t.Fatal("expired code")
	}
}

func TestCiphertextBoundToPurposeAndRecord(t *testing.T) {
	s := Secrets{Keys: map[string][]byte{"active": bytes.Repeat([]byte{4}, 32)}, Active: "active"}
	v, e := s.SealFor("same-secret", "provider:platform", "credential-a")
	if e != nil {
		t.Fatal(e)
	}
	if x, e := s.OpenFor(v, "provider:platform", "credential-a"); e != nil || x != "same-secret" {
		t.Fatal("identity decrypt failed")
	}
	for _, pair := range [][2]string{{"provider:platform", "credential-b"}, {"mfa:user", "credential-a"}} {
		if _, e := s.OpenFor(v, pair[0], pair[1]); e == nil {
			t.Fatal("accepted ciphertext moved to another record/purpose")
		}
	}
}
