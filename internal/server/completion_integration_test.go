package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func TestPasswordChangeRevokesFamiliesAndRequiresFreshProof(t *testing.T) {
	s, p := newAuthFixture(t)
	uid, email := authUser(t, s, p, "none")
	raw, csrf := authSession(t, s, p, uid, "", true, true)
	other, _ := authSession(t, s, p, uid, "", true, true)
	secret := security.NewTOTPSecret()
	sealed, err := s.Config.Secrets.SealFor(secret, "mfa:user", uid)
	if err != nil {
		t.Fatal(err)
	}
	authExec(t, p, "UPDATE users SET mfa_enabled=true,mfa_secret=$2 WHERE id=$1", uid, sealed)
	// Stored state wins even when the preexisting login had no MFA.
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/auth/password", raw, csrf, s.Config.Origin, map[string]any{"current_password": authTestPassword, "new_password": "new-account-password-2026"}), 401)
	code, _ := security.TOTP(secret, time.Now().Unix()/30)
	res := authHTTP(t, s, "POST", "/api/auth/password", raw, csrf, s.Config.Origin, map[string]any{"current_password": authTestPassword, "new_password": "new-account-password-2026", "code": code})
	requireAuthStatus(t, res, 200)
	for _, token := range []string{raw, other} {
		if _, err = s.Identity.Session(context.Background(), token); err == nil {
			t.Fatal("old family remained valid")
		}
	}
	if _, _, _, err = s.Identity.Login(context.Background(), email, authTestPassword, ""); err == nil {
		t.Fatal("old password accepted")
	}
	if _, _, _, err = s.Identity.Login(context.Background(), email, "new-account-password-2026", ""); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err = p.QueryRow(context.Background(), "SELECT count(*) FROM audit_events WHERE actor=$1 AND action='password.change'", uid).Scan(&audits); err != nil || audits != 1 {
		t.Fatal(audits, err)
	}
}
func TestInstanceGuardImmediatelyBeforeDispatch(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	var checks atomic.Int32
	f.server.Guard = func(context.Context) error {
		if checks.Add(1) > 1 {
			return errors.New("lock gone")
		}
		return nil
	}
	res := f.request(t, context.Background(), false, false)
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != 503 || f.posts.Load() != 0 {
		t.Fatalf("status=%d posts=%d", res.StatusCode, f.posts.Load())
	}
	w, err := f.server.Billing.Wallet(context.Background(), billing.Query{TenantID: f.tenant})
	if err != nil || w.Held != 0 || w.Available != 1000 {
		t.Fatal(w, err)
	}
	if f.server.ready.Load() {
		t.Fatal("lost instance still ready")
	}
}
func TestReportsAndCostCorrectionsKeepCustomerPrice(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	response := f.request(t, context.Background(), false, false)
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	ctx := context.Background()
	var owner, operator, approver, attempt string
	if err := f.pool.QueryRow(ctx, "SELECT owner_id FROM api_keys WHERE id=$1", f.key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='finance_operator'").Scan(&operator); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='finance_approver'").Scan(&approver); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT id FROM upstream_attempts").Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	authExec(t, f.pool, "UPDATE users SET mfa_enabled=true WHERE id=ANY($1)", []string{operator, approver})
	raw, csrf := authSession(t, f.server, f.pool, operator, "", true, true)
	body := map[string]any{"tenant_id": f.tenant, "attempt_id": attempt, "amount": "7", "basis": "invoice", "evidence_ref": "invoice:fixture"}
	post := func(amount string) *httptest.ResponseRecorder {
		body["amount"] = amount
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "http://console.test/api/platform/cost-corrections", bytes.NewReader(b))
		r.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
		r.Header.Set("Origin", f.server.Config.Origin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Idempotency-Key", "same-cost")
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		f.server.Engine().ServeHTTP(rec, r)
		return rec
	}
	requireAuthStatus(t, post("7"), 200)
	requireAuthStatus(t, post("7"), 200)
	requireAuthStatus(t, post("8"), 409)
	if _, err := f.server.Billing.Project(ctx, billing.ProjectInput{}); err != nil {
		t.Fatal(err)
	}
	view, vc := authSession(t, f.server, f.pool, owner, f.tenant, true, false)
	customer := authHTTP(t, f.server, "GET", "/api/reports/summary", view, vc, "", nil)
	requireAuthStatus(t, customer, 200)
	if strings.Contains(customer.Body.String(), "provider") || !strings.Contains(customer.Body.String(), `"charged":"20"`) {
		t.Fatal(customer.Body.String())
	}
	finance := authHTTP(t, f.server, "GET", "/api/platform/reports/summary", raw, csrf, "", nil)
	requireAuthStatus(t, finance, 200)
	if !strings.Contains(finance.Body.String(), `"known_provider_cost":"7"`) {
		t.Fatal(finance.Body.String())
	}
	var actor string
	if err := f.pool.QueryRow(ctx, "SELECT actor FROM audit_events WHERE action='cost.corrected'").Scan(&actor); err != nil || actor != operator {
		t.Fatal(actor, err)
	}
	other, oc := authSession(t, f.server, f.pool, approver, "", true, true)
	requireAuthStatus(t, authHTTP(t, f.server, "POST", "/api/platform/cost-corrections", other, oc, f.server.Config.Origin, body), 403)
}

// A published policy must satisfy the runtime's one-attempt-per-channel rule.
// Reject duplicate channels even when their upstream model strings differ.
func TestRoutePublishRejectsRepeatedChannelWithoutReplacingLiveRevision(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	ctx := context.Background()
	var actor, channel string
	if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='finance_operator'").Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT channel_id FROM model_bindings LIMIT 1").Scan(&channel); err != nil {
		t.Fatal(err)
	}
	_, err := f.server.Catalog.Publish(ctx, actor, f.model, 1, []catalog.BindingInput{{ChannelID: channel, UpstreamModel: "a", Weight: 1, MaxInflight: 4}, {ChannelID: channel, UpstreamModel: "b", Weight: 1, MaxInflight: 4}})
	if !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("duplicate-channel route published: %v", err)
	}
	var revision int64
	var policies int
	if err = f.pool.QueryRow(ctx, "SELECT active_revision FROM models WHERE id=$1", f.model).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM route_policy_versions WHERE model_id=$1", f.model).Scan(&policies); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || policies != 1 {
		t.Fatal("failed publish left partial policy", revision, policies)
	}
	res := f.request(t, ctx, false, false)
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("old policy stopped serving", res.StatusCode)
	}
}
