package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/identity"
)

type fakeAccounts struct {
	calls []string
	err   error
}

func (f *fakeAccounts) OperatorListUsers(context.Context) ([]identity.OperatorUser, error) {
	return []identity.OperatorUser{{ID: "u1", Email: "a@x.test", PlatformRole: "admin", Status: "active", MFAEnabled: true}}, f.err
}
func (f *fakeAccounts) OperatorSetPassword(_ context.Context, e, p string) error {
	f.calls = append(f.calls, "set-password:"+e+":"+p)
	return f.err
}
func (f *fakeAccounts) OperatorResetMFA(_ context.Context, e string) error {
	f.calls = append(f.calls, "reset-mfa:"+e)
	return f.err
}
func (f *fakeAccounts) OperatorSetStatus(_ context.Context, e string, s bool) error {
	f.calls = append(f.calls, "status:"+e+":"+map[bool]string{true: "suspend", false: "activate"}[s])
	return f.err
}

func TestUserCommandDispatch(t *testing.T) {
	cases := []struct {
		args  []string
		stdin string
		want  string
	}{
		{[]string{"set-password", "--email", "a@x.test", "--password-stdin"}, "correct horse battery\n", "set-password:a@x.test:correct horse battery"},
		{[]string{"reset-mfa", "--email", "a@x.test"}, "", "reset-mfa:a@x.test"},
		{[]string{"suspend", "--email", "a@x.test"}, "", "status:a@x.test:suspend"},
		{[]string{"activate", "--email", "a@x.test"}, "", "status:a@x.test:activate"},
	}
	for _, tc := range cases {
		f := &fakeAccounts{}
		var out bytes.Buffer
		if err := runUserCommand(context.Background(), f, tc.args, strings.NewReader(tc.stdin), &out); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if len(f.calls) != 1 || f.calls[0] != tc.want {
			t.Fatalf("%v called %v want %s", tc.args, f.calls, tc.want)
		}
		if strings.Contains(out.String(), "battery") {
			t.Fatal("password echoed")
		}
	}
}

func TestUserCommandRejectsBadUsageWithoutTouchingAccounts(t *testing.T) {
	for _, args := range [][]string{
		nil, {"unknown"}, {"suspend"}, {"set-password", "--email", "a@x.test"}, {"set-password", "--password-stdin"},
		{"list", "--email", "a@x.test"}, {"reset-mfa", "--email", "a@x.test", "extra"}, {"suspend", "--password", "x"},
	} {
		f := &fakeAccounts{}
		if err := runUserCommand(context.Background(), f, args, strings.NewReader("x\n"), &bytes.Buffer{}); err == nil || len(f.calls) != 0 {
			t.Fatalf("%v: err=%v calls=%v", args, err, f.calls)
		}
	}
}

func TestUserCommandMapsStoreErrorsWithoutLeakingDetail(t *testing.T) {
	for err, want := range map[error]string{identity.ErrNotFound: "not found", identity.ErrLastAdmin: "last active administrator", identity.ErrInvalid: "invalid input", errors.New("pq: secret-host-detail"): "failed"} {
		f := &fakeAccounts{err: err}
		got := runUserCommand(context.Background(), f, []string{"reset-mfa", "--email", "a@x.test"}, strings.NewReader(""), &bytes.Buffer{})
		if got == nil || !strings.Contains(got.Error(), want) || strings.Contains(got.Error(), "secret-host-detail") {
			t.Fatalf("%v -> %v", err, got)
		}
	}
}

func TestUserCommandListNeverPrintsHashes(t *testing.T) {
	var out bytes.Buffer
	if err := runUserCommand(context.Background(), &fakeAccounts{}, []string{"list"}, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "a@x.test") || strings.Contains(out.String(), "password") {
		t.Fatalf("%q %v", out.String(), err)
	}
}
