package identity

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

const identityTestPassword = "identity-fixture-password"

type identityFixture struct {
	ctx                                   context.Context
	pool                                  *pgxpool.Pool
	store                                 *Store
	tenant, owner, member, model1, model2 string
}

func newIdentityFixture(t *testing.T) *identityFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required: identity tests need real PostgreSQL")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "identity_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 12
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	if e = migrations.Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	sec := &security.Secrets{Keys: map[string][]byte{"test": []byte(strings.Repeat("e", 32))}, Active: "test", Pepper: []byte(strings.Repeat("a", 32))}
	f := &identityFixture{ctx: ctx, pool: pool, store: New(pool, sec), tenant: uuid.NewString(), owner: uuid.NewString(), member: uuid.NewString(), model1: uuid.NewString(), model2: uuid.NewString()}
	hash, e := security.HashPassword(identityTestPassword)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct{ id, email string }{{f.owner, "owner@identity.test"}, {f.member, "member@identity.test"}} {
		if _, e = pool.Exec(ctx, "INSERT INTO users(id,email,password_hash) VALUES($1,$2,$3)", v.id, v.email, hash); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = pool.Exec(ctx, "INSERT INTO tenants(id,name) VALUES($1,'Identity test tenant')", f.tenant); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true),($1,$3,'member',false)", f.tenant, f.owner, f.member); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{f.model1, f.model2} {
		if _, e = pool.Exec(ctx, "INSERT INTO models(id,public_name,profile) VALUES($1,$1,'{}'::jsonb)", id); e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, "INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, id); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = pool.Exec(ctx, "INSERT INTO member_model_grants(tenant_id,user_id,model_id) VALUES($1,$2,$3)", f.tenant, f.member, f.model1); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *identityFixture) login(t *testing.T, email string) (string, *Session) {
	t.Helper()
	raw, _, _, e := f.store.Login(f.ctx, email, identityTestPassword, "")
	if e != nil {
		t.Fatal(e)
	}
	v, e := f.store.Session(f.ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	return raw, v
}
func (f *identityFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, e := f.pool.Exec(f.ctx, query, args...); e != nil {
		t.Fatal(e)
	}
}

func TestIdentitySessionRotationAndTenantBoundary(t *testing.T) {
	f := newIdentityFixture(t)
	raw, v := f.login(t, "owner@identity.test")
	if _, e := f.store.Switch(f.ctx, v, uuid.NewString()); !errors.Is(e, ErrForbidden) {
		t.Fatalf("foreign tenant switch: %v", e)
	}
	second := uuid.NewString()
	f.exec(t, "INSERT INTO tenants(id,name) VALUES($1,'second')", second)
	f.exec(t, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')", second, f.owner)
	next, e := f.store.Switch(f.ctx, v, second)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.Session(f.ctx, raw); !errors.Is(e, ErrInvalid) {
		t.Fatalf("old session survives switch: %v", e)
	}
	switched, e := f.store.Session(f.ctx, next)
	if e != nil || switched.Tenant == nil || switched.Tenant.ID != second {
		t.Fatalf("switch result=%+v err=%v", switched, e)
	}
	if e = f.store.Logout(f.ctx, switched.Digest); e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.Session(f.ctx, next); !errors.Is(e, ErrInvalid) {
		t.Fatalf("logout did not invalidate session: %v", e)
	}
}
func TestIdentityLogoutCannotResurrectThroughStaleSwitch(t *testing.T) {
	f := newIdentityFixture(t)
	_, v := f.login(t, "owner@identity.test")
	if e := f.store.Logout(f.ctx, v.Digest); e != nil {
		t.Fatal(e)
	}
	if _, e := f.store.Switch(f.ctx, v, f.tenant); e == nil {
		t.Fatal("logged-out session was reissued by Switch")
	}
	var n int
	if e := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM sessions s JOIN session_families f ON f.id=s.family_id WHERE s.user_id=$1 AND f.revoked_at IS NULL AND s.expires_at>clock_timestamp()", f.owner).Scan(&n); e != nil {
		t.Fatal(e)
	}
	if n != 0 {
		t.Fatalf("resurrected sessions=%d", n)
	}
}
func TestIdentityKeyRechecksCurrentRole(t *testing.T) {
	f := newIdentityFixture(t)
	_, v := f.login(t, "member@identity.test")
	f.exec(t, "UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2", f.tenant, f.member)
	if _, e := f.store.CreateKey(f.ctx, v, KeyInput{Name: "must-not-exist", Scopes: []string{f.model1}}); e == nil {
		t.Fatal("stale member session created key after role was reduced to billing_viewer")
	}
	var n int
	if e := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM api_keys WHERE owner_id=$1", f.member).Scan(&n); e != nil {
		t.Fatal(e)
	}
	if n != 0 {
		t.Fatal("key persisted after current role denied creation")
	}
}
func TestIdentityOwnerCannotExpandDelegatedModelRights(t *testing.T) {
	f := newIdentityFixture(t)
	f.exec(t, "UPDATE memberships SET allow_all_models=false WHERE tenant_id=$1 AND user_id=$2", f.tenant, f.owner)
	f.exec(t, "INSERT INTO member_model_grants(tenant_id,user_id,model_id) VALUES($1,$2,$3)", f.tenant, f.owner, f.model1)
	if _, e := f.store.AddMember(f.ctx, f.owner, f.tenant, "over@identity.test", identityTestPassword, "member", false, []string{f.model2}, true); e == nil {
		t.Fatal("restricted owner delegated an unauthorized model")
	}
	if _, e := f.store.AddMember(f.ctx, f.owner, f.tenant, "owner2@identity.test", identityTestPassword, "owner", false, []string{f.model1}, true); e == nil {
		t.Fatal("customer owner created another owner")
	}
	uid, e := f.store.AddMember(f.ctx, f.owner, f.tenant, "allowed@identity.test", identityTestPassword, "member", false, []string{f.model1}, true)
	if e != nil || uid == "" {
		t.Fatalf("valid subset delegation: %v", e)
	}
	suspended := "suspended"
	if e = f.store.UpdateMember(f.ctx, f.owner, f.tenant, f.owner, nil, &suspended, nil, nil, true); e == nil {
		t.Fatal("owner disabled itself")
	}
	// Platform previously granted all models to a billing viewer. A partial role
	// patch must validate the retained rights, not replace missing fields with zero values.
	f.exec(t, "UPDATE memberships SET role='billing_viewer',allow_all_models=true WHERE tenant_id=$1 AND user_id=$2", f.tenant, f.member)
	member := "member"
	if e = f.store.UpdateMember(f.ctx, f.owner, f.tenant, f.member, &member, nil, nil, nil, true); e == nil {
		t.Fatal("partial PATCH converted broad billing-viewer grants into call rights beyond the acting owner")
	}
	var role string
	if e = f.pool.QueryRow(f.ctx, "SELECT role FROM memberships WHERE tenant_id=$1 AND user_id=$2", f.tenant, f.member).Scan(&role); e != nil {
		t.Fatal(e)
	}
	if role != "billing_viewer" {
		t.Fatalf("forbidden mutation committed role=%s", role)
	}
}
func TestIdentityMFAEnrollmentInvalidatesLegacySessions(t *testing.T) {
	f := newIdentityFixture(t)
	rawA, a := f.login(t, "owner@identity.test")
	rawB, _ := f.login(t, "owner@identity.test")
	secret, codes, e := f.store.SetupMFA(f.ctx, a)
	if e != nil || len(codes) != 8 {
		t.Fatalf("setup: %v codes=%d", e, len(codes))
	}
	code, e := security.TOTP(secret, time.Now().Unix()/30)
	if e != nil {
		t.Fatal(e)
	}
	next, e := f.store.VerifyMFA(f.ctx, a, code, true)
	if e != nil {
		t.Fatal(e)
	}
	enabled, e := f.store.Session(f.ctx, next)
	if e != nil || !enabled.User.MFAEnabled || enabled.MFARequired {
		t.Fatalf("enabled=%+v err=%v", enabled, e)
	}
	for _, old := range []string{rawA, rawB} {
		if _, e = f.store.Session(f.ctx, old); !errors.Is(e, ErrInvalid) {
			t.Fatalf("pre-enrollment session remains usable after MFA was enabled: %v", e)
		}
	}
	// A new password-only session must still pass MFA, and a recovery code can only
	// be redeemed once even across separately issued sessions.
	_, pending := f.login(t, "owner@identity.test")
	if !pending.MFARequired {
		t.Fatal("MFA-enabled login was already verified")
	}
	if _, e = f.store.VerifyMFA(f.ctx, pending, codes[0], false); e != nil {
		t.Fatal(e)
	}
	_, again := f.login(t, "owner@identity.test")
	if _, e = f.store.VerifyMFA(f.ctx, again, codes[0], false); e == nil {
		t.Fatal("recovery code was reused")
	}
	if _, e = f.store.VerifyMFA(f.ctx, again, code, false); e == nil {
		t.Fatal("TOTP step was reused after enrollment")
	}
}
func TestIdentityMFARecoveryCodesAreBoundToAccount(t *testing.T) {
	f := newIdentityFixture(t)
	_, a := f.login(t, "owner@identity.test")
	secret, codes, e := f.store.SetupMFA(f.ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	code, _ := security.TOTP(secret, time.Now().Unix()/30)
	if _, e = f.store.VerifyMFA(f.ctx, a, code, true); e != nil {
		t.Fatal(e)
	}
	_, b := f.login(t, "member@identity.test")
	secretB, _, e := f.store.SetupMFA(f.ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	codeB, _ := security.TOTP(secretB, time.Now().Unix()/30)
	if _, e = f.store.VerifyMFA(f.ctx, b, codeB, true); e != nil {
		t.Fatal(e)
	}
	_, pendingB := f.login(t, "member@identity.test")
	if _, e = f.store.VerifyMFA(f.ctx, pendingB, codes[0], false); e == nil {
		t.Fatal("another account's recovery code was accepted")
	}
	_, pendingA := f.login(t, "owner@identity.test")
	if _, e = f.store.VerifyMFA(f.ctx, pendingA, codes[0], false); e != nil {
		t.Fatalf("cross-account rejection consumed original recovery code: %v", e)
	}
}
