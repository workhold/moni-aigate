package identity

import (
	"errors"
	"testing"
)

func (f *identityFixture) operatorFixtureUsers(t *testing.T) {
	t.Helper()
	for _, q := range []string{
		"UPDATE users SET platform_role='admin' WHERE email='owner@identity.test'",
		"INSERT INTO users(id,email,password_hash,platform_role) SELECT gen_random_uuid()::text,'admin2@identity.test',password_hash,'admin' FROM users WHERE email='owner@identity.test'",
	} {
		if _, err := f.pool.Exec(f.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOperatorSetPasswordReplacesCredentialAndRevokesSessions(t *testing.T) {
	f := newIdentityFixture(t)
	old, _, _, err := f.store.Login(f.ctx, "member@identity.test", identityTestPassword, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Session(f.ctx, old); err != nil {
		t.Fatal(err)
	}
	if err = f.store.OperatorSetPassword(f.ctx, " Member@Identity.test ", "short"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short password accepted: %v", err)
	}
	if err = f.store.OperatorSetPassword(f.ctx, "nobody@identity.test", "a-sufficiently-long-password"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	if err = f.store.OperatorSetPassword(f.ctx, " Member@Identity.test ", "a-sufficiently-long-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Session(f.ctx, old); err == nil {
		t.Fatal("session survived an operator password reset")
	}
	if _, _, _, err = f.store.Login(f.ctx, "member@identity.test", identityTestPassword, ""); err == nil {
		t.Fatal("old password still works")
	}
	if _, _, _, err = f.store.Login(f.ctx, "member@identity.test", "a-sufficiently-long-password", ""); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	var audits int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM audit_events WHERE actor=$1 AND action='user.password_set_by_operator'", OperatorActor).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit=%d err=%v", audits, err)
	}
}

func TestOperatorResetMFAClearsFactorAndSessions(t *testing.T) {
	f := newIdentityFixture(t)
	if _, err := f.pool.Exec(f.ctx, "UPDATE users SET mfa_enabled=true,mfa_secret='sealed',mfa_last_step=9 WHERE email='owner@identity.test'"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO mfa_recovery_codes(user_id,digest) SELECT id,'d1' FROM users WHERE email='owner@identity.test'"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.OperatorResetMFA(f.ctx, "owner@identity.test"); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	var secret string
	var codes int
	if err := f.pool.QueryRow(f.ctx, "SELECT mfa_enabled,mfa_secret,(SELECT count(*) FROM mfa_recovery_codes WHERE user_id=users.id) FROM users WHERE email='owner@identity.test'").Scan(&enabled, &secret, &codes); err != nil {
		t.Fatal(err)
	}
	if enabled || secret != "" || codes != 0 {
		t.Fatalf("factor not cleared: %v %q %d", enabled, secret, codes)
	}
	if _, _, mfa, err := f.store.Login(f.ctx, "owner@identity.test", identityTestPassword, ""); err != nil || mfa {
		t.Fatalf("login after reset: mfa=%v err=%v", mfa, err)
	}
}

func TestOperatorSuspendProtectsLastAdminAndRevokesSessions(t *testing.T) {
	f := newIdentityFixture(t)
	f.operatorFixtureUsers(t)
	token, _, _, err := f.store.Login(f.ctx, "member@identity.test", identityTestPassword, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.OperatorSetStatus(f.ctx, "member@identity.test", true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Session(f.ctx, token); err == nil {
		t.Fatal("suspended user kept a live session")
	}
	if _, _, _, err = f.store.Login(f.ctx, "member@identity.test", identityTestPassword, ""); err == nil {
		t.Fatal("suspended user can log in")
	}
	if err = f.store.OperatorSetStatus(f.ctx, "member@identity.test", true); err != nil {
		t.Fatalf("suspend must be idempotent: %v", err)
	}
	if err = f.store.OperatorSetStatus(f.ctx, "member@identity.test", false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = f.store.Login(f.ctx, "member@identity.test", identityTestPassword, ""); err != nil {
		t.Fatalf("reactivated user cannot log in: %v", err)
	}
	// Two admins: one may be suspended, the survivor may not.
	if err = f.store.OperatorSetStatus(f.ctx, "admin2@identity.test", true); err != nil {
		t.Fatal(err)
	}
	if err = f.store.OperatorSetStatus(f.ctx, "owner@identity.test", true); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin suspended: %v", err)
	}
	users, err := f.store.OperatorListUsers(f.ctx)
	if err != nil || len(users) != 3 {
		t.Fatalf("list: %d %v", len(users), err)
	}
}

func TestOperatorSuspendConcurrentAdminsKeepsOne(t *testing.T) {
	f := newIdentityFixture(t)
	f.operatorFixtureUsers(t)
	errs := make(chan error, 2)
	for _, email := range []string{"owner@identity.test", "admin2@identity.test"} {
		go func(e string) { errs <- f.store.OperatorSetStatus(f.ctx, e, true) }(email)
	}
	var ok, last int
	for i := 0; i < 2; i++ {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrLastAdmin):
			last++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || last != 1 {
		t.Fatalf("concurrent suspensions: ok=%d last=%d", ok, last)
	}
}
