package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

// newAuthFixture is also available to gateway_integration_test.go. Every fixture
// gets its own schema and applies real migrations; no mock database is used.
func newAuthFixture(t *testing.T) (*Server, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required: HTTP auth tests need real PostgreSQL")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 20
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	if e = migrations.ApplyCurrent(ctx, pool); e != nil {
		t.Fatal(e)
	}
	secrets := &security.Secrets{Keys: map[string][]byte{"test": []byte(strings.Repeat("k", 32))}, Active: "test", Pepper: []byte(strings.Repeat("p", 32))}
	c := config.Config{Origin: "http://console.test", CookieSecure: false, Secrets: secrets}
	s := New(c, pool, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("test console")}}, nil)
	s.Ready(true)
	return s, pool
}

const authTestPassword = "auth-fixture-password"

func authExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, e := pool.Exec(context.Background(), sql, args...); e != nil {
		t.Fatal(e)
	}
}
func authUser(t *testing.T, s *Server, pool *pgxpool.Pool, role string) (id, email string) {
	t.Helper()
	id = uuid.NewString()
	email = fmt.Sprintf("%s@auth.test", id)
	hash, e := security.HashPassword(authTestPassword)
	if e != nil {
		t.Fatal(e)
	}
	authExec(t, pool, "INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,$3,$4)", id, email, hash, role)
	return
}
func authSession(t *testing.T, s *Server, pool *pgxpool.Pool, user, tenant string, verified, recent bool) (raw, csrf string) {
	t.Helper()
	raw = security.Random(32)
	csrf = security.Random(24)
	var tenantArg any
	if tenant != "" {
		tenantArg = tenant
	}
	family := uuid.NewString()
	authExec(t, pool, "INSERT INTO session_families(id,user_id) VALUES($1,$2)", family, user)
	authExec(t, pool, "INSERT INTO sessions(digest,user_id,tenant_id,csrf_token,mfa_verified,reauthenticated_at,expires_at,family_id) VALUES($1,$2,$3,$4,$5,CASE WHEN $6 THEN clock_timestamp() ELSE NULL END,clock_timestamp()+interval '1 hour',$7)", s.Config.Secrets.Digest(raw), user, tenantArg, csrf, verified, recent, family)
	return
}
func authHTTP(t *testing.T, s *Server, method, path, raw, csrf, origin string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var e error
		data, e = json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
	}
	req := httptest.NewRequest(method, "http://console.test"+path, bytes.NewReader(data))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if raw != "" {
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.Engine().ServeHTTP(rec, req)
	return rec
}
func requireAuthStatus(t *testing.T, res *httptest.ResponseRecorder, status int) {
	t.Helper()
	if res.Code != status {
		t.Fatalf("HTTP=%d want=%d response=%s", res.Code, status, res.Body.String())
	}
}
func authData(t *testing.T, res *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var v struct {
		Data json.RawMessage `json:"data"`
	}
	if e := json.Unmarshal(res.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v.Data
}
func authCookie(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range res.Result().Cookies() {
		if c.Name == "moni_session" {
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
				t.Fatalf("unsafe session cookie: %+v", c)
			}
			return c.Value
		}
	}
	t.Fatal("session cookie absent")
	return ""
}

func TestHTTPOriginCSRFAndTenantInjection(t *testing.T) {
	s, p := newAuthFixture(t)
	uid, email := authUser(t, s, p, "none")
	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	authExec(t, p, "INSERT INTO tenants(id,name) VALUES($1,'A'),($2,'B')", tenantA, tenantB)
	authExec(t, p, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", tenantA, uid)
	loginBody := map[string]any{"email": email, "password": authTestPassword}
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/me", "", "", "", nil), 401)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/auth/login", "", "", "https://foreign.test", loginBody), 403)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/auth/login", "", "", "", loginBody), 403)
	login := authHTTP(t, s, "POST", "/api/auth/login", "", "", s.Config.Origin, loginBody)
	requireAuthStatus(t, login, 200)
	raw := authCookie(t, login)
	me := authHTTP(t, s, "GET", "/api/me", raw, "", "", nil)
	requireAuthStatus(t, me, 200)
	var v identity.Session
	if e := json.Unmarshal(authData(t, me), &v); e != nil {
		t.Fatal(e)
	}
	if v.Tenant == nil || v.Tenant.ID != tenantA {
		t.Fatalf("wrong selected tenant: %+v", v.Tenant)
	}
	body := map[string]any{"name": "origin-csrf-test", "scopes": []string{"*"}, "tenant_id": tenantB}
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/keys", raw, "", s.Config.Origin, body), 403)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/keys", raw, v.CSRF, "https://foreign.test", body), 403)
	// Key creation rejects fields outside the public contract, including tenant
	// identity. A valid request still derives its tenant from the session.
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/keys", raw, v.CSRF, s.Config.Origin, body), 400)
	delete(body, "tenant_id")
	create := authHTTP(t, s, "POST", "/api/keys", raw, v.CSRF, s.Config.Origin, body)
	requireAuthStatus(t, create, 200)
	var countA, countB int
	if e := p.QueryRow(context.Background(), "SELECT count(*) FILTER(WHERE tenant_id=$1),count(*) FILTER(WHERE tenant_id=$2) FROM api_keys", tenantA, tenantB).Scan(&countA, &countB); e != nil {
		t.Fatal(e)
	}
	if countA != 1 || countB != 0 {
		t.Fatalf("client tenant injection A=%d B=%d", countA, countB)
	}
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/tenant/switch", raw, v.CSRF, s.Config.Origin, map[string]any{"tenant_id": tenantB}), 403)
	logout := authHTTP(t, s, "POST", "/api/auth/logout", raw, v.CSRF, s.Config.Origin, map[string]any{})
	requireAuthStatus(t, logout, 200)
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/me", raw, "", "", nil), 401)
}
func TestHTTPMFAAndPlatformRoleSeparation(t *testing.T) {
	s, p := newAuthFixture(t)
	// Exercise the opt-in enforced policy independently of the default.
	s = New(config.Config{Origin: s.Config.Origin, Secrets: s.Config.Secrets, MFAEnforcePlatform: true}, p, s.assets, nil)
	s.Ready(true)
	for _, role := range []string{"admin", "finance_operator", "finance_approver"} {
		t.Run(role, func(t *testing.T) {
			uid, _ := authUser(t, s, p, role)
			raw, csrf := authSession(t, s, p, uid, "", false, false)
			me := authHTTP(t, s, "GET", "/api/me", raw, "", "", nil)
			requireAuthStatus(t, me, 200)
			if !bytes.Contains(me.Body.Bytes(), []byte(`"enrollment_required":true`)) {
				t.Fatalf("platform role did not require enrollment: %s", me.Body.String())
			}
			requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/channels", raw, "", "", nil), 403)
			// Mark MFA-enabled for the middleware boundary; cryptographic factor/recovery
			// lifecycle is separately exercised by identity integration tests.
			encrypted, e := s.Config.Secrets.Seal(security.NewTOTPSecret())
			if e != nil {
				t.Fatal(e)
			}
			authExec(t, p, "UPDATE users SET mfa_enabled=true,mfa_secret=$2 WHERE id=$1", uid, encrypted)
			raw, csrf = authSession(t, s, p, uid, "", true, false)
			requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/users", raw, csrf, s.Config.Origin, map[string]any{}), 403)
			raw, csrf = authSession(t, s, p, uid, "", true, true)
			if role != "admin" {
				requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/channels", raw, "", "", nil), 403)
				requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/users", raw, csrf, s.Config.Origin, map[string]any{"email": "must-not-create@auth.test", "password": authTestPassword, "platform_role": "admin"}), 403)
			}
			if role != "finance_operator" {
				requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/credits", raw, csrf, s.Config.Origin, map[string]any{"tenant_id": "x", "amount": "1000000", "business_ref": "x", "proof_ref": "x"}), 403)
			}
			if role != "finance_approver" {
				requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/credits/nonexistent/approve", raw, csrf, s.Config.Origin, map[string]any{}), 403)
			}
		})
	}
}

func TestHTTPMFAOptionalPlatformAccess(t *testing.T) {
	s, p := newAuthFixture(t)
	for _, role := range []string{"admin", "finance_operator", "finance_approver"} {
		t.Run(role, func(t *testing.T) {
			uid, email := authUser(t, s, p, role)
			// A pre-upgrade, unverified enrollment session must also become usable
			// when enrollment is optional, without mutating the user's factors.
			legacy, _ := authSession(t, s, p, uid, "", false, true)
			requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/tenants", legacy, "", "", nil), 200)
			login := authHTTP(t, s, "POST", "/api/auth/login", "", "", s.Config.Origin, map[string]any{"email": email, "password": authTestPassword})
			requireAuthStatus(t, login, 200)
			var flags struct {
				MFARequired        bool `json:"mfa_required"`
				EnrollmentRequired bool `json:"enrollment_required"`
			}
			if err := json.Unmarshal(authData(t, login), &flags); err != nil {
				t.Fatal(err)
			}
			if flags.MFARequired || flags.EnrollmentRequired {
				t.Fatal("default login still requires MFA enrollment")
			}
			raw := authCookie(t, login)
			var session identity.Session
			me := authHTTP(t, s, "GET", "/api/me", raw, "", "", nil)
			requireAuthStatus(t, me, 200)
			if err := json.Unmarshal(authData(t, me), &session); err != nil {
				t.Fatal(err)
			}
			if session.MFARequired || session.EnrollmentRequired || session.User.MFAEnabled {
				t.Fatal("optional policy and session disagree")
			}
			requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/tenants", raw, "", "", nil), 200)
			// Optional MFA leaves password freshness and platform duties intact.
			authExec(t, p, "UPDATE sessions SET reauthenticated_at=NULL WHERE digest=$1", s.Config.Secrets.Digest(raw))
			body := map[string]any{"email": uuid.NewString() + "@auth.test", "password": authTestPassword, "platform_role": "finance_operator"}
			denied := authHTTP(t, s, "POST", "/api/platform/users", raw, session.CSRF, s.Config.Origin, body)
			requireAuthStatus(t, denied, 403)
			if !bytes.Contains(denied.Body.Bytes(), []byte("reauth_required")) {
				t.Fatal("password freshness was not enforced")
			}
			requireAuthStatus(t, authHTTP(t, s, "POST", "/api/auth/reauth", raw, session.CSRF, s.Config.Origin, map[string]any{"password": authTestPassword}), 200)
			want := 403
			if role == "admin" {
				want = 200
			}
			requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/users", raw, session.CSRF, s.Config.Origin, body), want)
		})
	}
}

func TestHTTPMFAPolicyChangeAndVoluntaryEnrollment(t *testing.T) {
	s, p := newAuthFixture(t)
	uid, email := authUser(t, s, p, "admin")
	login := authHTTP(t, s, "POST", "/api/auth/login", "", "", s.Config.Origin, map[string]any{"email": email, "password": authTestPassword})
	requireAuthStatus(t, login, 200)
	raw := authCookie(t, login)
	session, err := s.Identity.Session(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	// Turning enforcement on must block an old password-only session even if
	// its mfa_verified bit was true under the optional policy.
	enforced := New(config.Config{Origin: s.Config.Origin, Secrets: s.Config.Secrets, MFAEnforcePlatform: true}, p, s.assets, nil)
	enforced.Ready(true)
	denied := authHTTP(t, enforced, "GET", "/api/platform/tenants", raw, "", "", nil)
	requireAuthStatus(t, denied, 403)
	if !bytes.Contains(denied.Body.Bytes(), []byte("mfa_required")) {
		t.Fatal("enforcement did not close business access")
	}
	me := authHTTP(t, enforced, "GET", "/api/me", raw, "", "", nil)
	if !bytes.Contains(me.Body.Bytes(), []byte(`"enrollment_required":true`)) {
		t.Fatal("old session did not receive enrollment requirement")
	}
	// A tenant user is never forced to enroll by the platform-only policy.
	user, _ := authUser(t, s, p, "none")
	userRaw, _ := authSession(t, s, p, user, "", false, true)
	userSession, err := enforced.Identity.Session(context.Background(), userRaw)
	if err != nil || userSession.EnrollmentRequired || userSession.MFARequired {
		t.Fatal("platform policy enforced on a tenant user", err)
	}
	// Disabling enforcement reopens an unbound account; binding remains an
	// explicit authenticated action and does not happen on login.
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/tenants", raw, "", "", nil), 200)
	setup := authHTTP(t, s, "POST", "/api/auth/mfa/setup", raw, session.CSRF, s.Config.Origin, map[string]any{})
	requireAuthStatus(t, setup, 200)
	var material struct {
		Secret   string   `json:"secret"`
		URI      string   `json:"otpauth_url"`
		Recovery []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(authData(t, setup), &material); err != nil {
		t.Fatal(err)
	}
	if material.URI != security.OTPURL(email, material.Secret) || len(material.Recovery) == 0 {
		t.Fatal("enrollment did not return local QR-compatible material")
	}
	code, err := security.TOTP(material.Secret, time.Now().Unix()/30)
	if err != nil {
		t.Fatal(err)
	}
	enabled := authHTTP(t, s, "POST", "/api/auth/mfa/enable", raw, session.CSRF, s.Config.Origin, map[string]any{"code": code})
	requireAuthStatus(t, enabled, 200)
	verified := authCookie(t, enabled)
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/tenants", verified, "", "", nil), 200)
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/me", raw, "", "", nil), 401)
	pending := authHTTP(t, s, "POST", "/api/auth/login", "", "", s.Config.Origin, map[string]any{"email": email, "password": authTestPassword})
	requireAuthStatus(t, pending, 200)
	if !bytes.Contains(pending.Body.Bytes(), []byte(`"mfa_required":true`)) || !bytes.Contains(pending.Body.Bytes(), []byte(`"enrollment_required":false`)) {
		t.Fatal("optional policy bypassed a voluntarily enabled factor")
	}
	pendingRaw := authCookie(t, pending)
	pendingSession, err := s.Identity.Session(context.Background(), pendingRaw)
	if err != nil {
		t.Fatal(err)
	}
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/tenants", pendingRaw, "", "", nil), 403)
	recovery := authHTTP(t, s, "POST", "/api/auth/mfa/verify", pendingRaw, pendingSession.CSRF, s.Config.Origin, map[string]any{"code": material.Recovery[0]})
	requireAuthStatus(t, recovery, 200)
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/tenants", authCookie(t, recovery), "", "", nil), 200)
	var stillEnabled bool
	if err := p.QueryRow(context.Background(), "SELECT mfa_enabled FROM users WHERE id=$1", uid).Scan(&stillEnabled); err != nil || !stillEnabled {
		t.Fatal("policy change erased user MFA", err)
	}
}
func TestHTTPTenantMemberReadsAreScoped(t *testing.T) {
	s, p := newAuthFixture(t)
	uid, _ := authUser(t, s, p, "none")
	other, _ := authUser(t, s, p, "none")
	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	authExec(t, p, "INSERT INTO tenants(id,name) VALUES($1,'A'),($2,'B')", tenantA, tenantB)
	authExec(t, p, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner'),($3,$4,'owner')", tenantA, uid, tenantB, other)
	raw, csrf := authSession(t, s, p, uid, tenantA, true, true)
	own := authHTTP(t, s, "GET", "/api/members", raw, "", "", nil)
	requireAuthStatus(t, own, 200)
	if bytes.Contains(own.Body.Bytes(), []byte(other)) {
		t.Fatal("other tenant's member leaked")
	}
	if !bytes.Contains(own.Body.Bytes(), []byte(uid)) {
		t.Fatal("own member absent")
	}
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/tenants", raw, "", "", nil), 403)
	requireAuthStatus(t, authHTTP(t, s, "PATCH", "/api/members/"+other, raw, csrf, s.Config.Origin, map[string]any{"role": "member"}), 404)
	authExec(t, p, "UPDATE memberships SET role='member' WHERE tenant_id=$1 AND user_id=$2", tenantA, uid)
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/members", raw, "", "", nil), 403)
}
func TestHTTPMFAEnrollmentNeedsRecentPassword(t *testing.T) {
	s, p := newAuthFixture(t)
	uid, _ := authUser(t, s, p, "none")
	raw, csrf := authSession(t, s, p, uid, "", true, false)
	denied := authHTTP(t, s, "POST", "/api/auth/mfa/setup", raw, csrf, s.Config.Origin, map[string]any{})
	requireAuthStatus(t, denied, 403)
	if !bytes.Contains(denied.Body.Bytes(), []byte("reauth_required")) {
		t.Fatalf("wrong failure reason: %s", denied.Body.String())
	}
	valid := authHTTP(t, s, "POST", "/api/auth/reauth", raw, csrf, s.Config.Origin, map[string]any{"password": authTestPassword})
	requireAuthStatus(t, valid, 200)
	setup := authHTTP(t, s, "POST", "/api/auth/mfa/setup", raw, csrf, s.Config.Origin, map[string]any{})
	requireAuthStatus(t, setup, 200)
	if !bytes.Contains(setup.Body.Bytes(), []byte("otpauth_url")) {
		t.Fatal("setup did not return enrollment material")
	}
}
func TestHTTPSensitiveCustomerWriteNeedsRecentPassword(t *testing.T) {
	s, p := newAuthFixture(t)
	uid, _ := authUser(t, s, p, "none")
	tenant := uuid.NewString()
	authExec(t, p, "INSERT INTO tenants(id,name) VALUES($1,'Owner')", tenant)
	authExec(t, p, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')", tenant, uid)
	raw, csrf := authSession(t, s, p, uid, tenant, true, false)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/keys", raw, csrf, s.Config.Origin, map[string]any{"name": "requires-password", "scopes": []string{"*"}}), 403)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/members", raw, csrf, s.Config.Origin, map[string]any{"email": "new@auth.test", "password": authTestPassword, "role": "member", "allow_all_models": false, "model_ids": []string{}}), 403)
}
func TestHTTPFinanceTenantLookupDoesNotExposeOwnerOrGrants(t *testing.T) {
	s, p := newAuthFixture(t)
	uid, _ := authUser(t, s, p, "finance_operator")
	encrypted, e := s.Config.Secrets.Seal(security.NewTOTPSecret())
	if e != nil {
		t.Fatal(e)
	}
	authExec(t, p, "UPDATE users SET mfa_enabled=true,mfa_secret=$2 WHERE id=$1", uid, encrypted)
	authExec(t, p, "INSERT INTO tenants(name) VALUES('Finance lookup tenant')")
	raw, _ := authSession(t, s, p, uid, "", true, true)
	res := authHTTP(t, s, "GET", "/api/platform/tenants", raw, "", "", nil)
	requireAuthStatus(t, res, 200)
	for _, field := range []string{`"owner_email"`, `"model_ids"`, `"allowed_regions"`} {
		if bytes.Contains(res.Body.Bytes(), []byte(field)) {
			t.Fatalf("finance lookup unnecessarily exposed %s", field)
		}
	}
}
func TestHTTPExpiredSessionsFailClosed(t *testing.T) {
	s, p := newAuthFixture(t)
	uid, _ := authUser(t, s, p, "none")
	raw, _ := authSession(t, s, p, uid, "", true, true)
	authExec(t, p, "UPDATE sessions SET expires_at=$2 WHERE digest=$1", s.Config.Secrets.Digest(raw), time.Now().Add(-time.Minute))
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/me", raw, "", "", nil), 401)
}
