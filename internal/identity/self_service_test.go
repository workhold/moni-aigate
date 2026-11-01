package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/security"
)

func TestIdentityEmailAndTokenBoundaries(t *testing.T) {
	for raw, want := range map[string]string{"  A.User+tag@Example.COM  ": "a.user+tag@example.com", "simple@example.test": "simple@example.test"} {
		got, e := NormalizeIdentityEmail(raw)
		if e != nil || got != want {
			t.Fatalf("normalization got %q: %v", got, e)
		}
	}
	for _, raw := range []string{"", "User <user@example.test>", "a@example.test,b@example.test", "a\r\nb@example.test", "a b@example.test", "ü@example.test", strings.Repeat("a", 245) + "@example.test"} {
		if _, e := NormalizeIdentityEmail(raw); !errors.Is(e, ErrIdentityRequestInvalid) {
			t.Fatalf("email accepted: %v", e)
		}
	}
	token := "ic1_" + security.Random(32)
	if !ValidIdentityToken(token) {
		t.Fatal("canonical random token rejected")
	}
	// 32 zero bytes encode with a final A. B has nonzero padding bits and can
	// decode to the same bytes with a permissive decoder, but is noncanonical.
	for _, raw := range []string{"", token + "=", token[1:], "ic1_" + strings.Repeat("A", 42) + "B", "ic1_" + strings.Repeat("A", 42) + "+", "https://example.test/#token=" + token} {
		if ValidIdentityToken(raw) {
			t.Fatal("invalid token accepted")
		}
		s := &Store{}
		if e := s.CompleteRegistration(context.Background(), raw, "valid-test-password", "Tenant"); !errors.Is(e, ErrIdentityProofInvalid) {
			t.Fatalf("registration token error: %v", e)
		}
		if e := s.CompletePasswordReset(context.Background(), raw, "valid-test-password", ""); !errors.Is(e, ErrIdentityProofInvalid) {
			t.Fatalf("reset token error: %v", e)
		}
	}
}

func TestIdentityCompletionFieldValidationBeforeDatabase(t *testing.T) {
	s := &Store{}
	token := "ic1_" + security.Random(32)
	ctx := context.Background()
	for _, v := range []struct{ password, name string }{{"short", "Tenant"}, {strings.Repeat("x", 73), "Tenant"}, {"valid-test-password", " "}, {"valid-test-password", strings.Repeat("x", 201)}, {"valid-test-password", "Tenant\nName"}} {
		if e := s.CompleteRegistration(ctx, token, v.password, v.name); !errors.Is(e, ErrIdentityRequestInvalid) {
			t.Fatalf("registration fields: %v", e)
		}
	}
	for _, v := range []struct{ password, code string }{{"short", ""}, {strings.Repeat("x", 73), ""}, {"valid-test-password", strings.Repeat("x", 129)}, {"valid-test-password", "123\n456"}} {
		if e := s.CompletePasswordReset(ctx, token, v.password, v.code); !errors.Is(e, ErrIdentityRequestInvalid) {
			t.Fatalf("reset fields: %v", e)
		}
	}
}

func TestIdentityRequestRejectsNonOriginBeforeDatabase(t *testing.T) {
	s := &Store{}
	for _, origin := range []string{"", "https://portal.test#", "https://portal.test?", "https://portal.test/", "https://user@portal.test", "https://portal.test/path", "http://portal.test", "https://portal.test#token=value"} {
		if e := s.RequestIdentityMail(context.Background(), IdentityPurposeRegistration, "user@example.test", origin); !errors.Is(e, ErrIdentityRequestInvalid) {
			t.Fatalf("non-origin accepted: %v", e)
		}
	}
}
