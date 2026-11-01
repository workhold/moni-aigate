package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

type credentialHTTPFixture struct {
	*gatewayFixture
	channel, actor, session, csrf string
}

func newCredentialHTTPFixture(t *testing.T, handler http.HandlerFunc, noConsumption []int) *credentialHTTPFixture {
	t.Helper()
	f := &credentialHTTPFixture{gatewayFixture: newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		t.Error("pool request used superseded endpoint")
		w.WriteHeader(500)
	}}, noConsumption)}
	f.channel = probeChannels(t, f.gatewayFixture)[0]
	f.session, f.csrf = probeAdmin(t, f.gatewayFixture)
	if err := f.pool.QueryRow(context.Background(), "SELECT id FROM users WHERE platform_role='admin'").Scan(&f.actor); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.posts.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
			t.Error("invalid pool endpoint")
		}
		handler(w, r)
	}))
	t.Cleanup(upstream.Close)
	if _, err := f.server.Catalog.SaveChannel(context.Background(), f.actor, f.channel, catalog.ChannelInput{BaseURL: upstream.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	f.publish(t, nil)
	return f
}

func (f *credentialHTTPFixture) publish(t *testing.T, extra []catalog.BindingInput) {
	t.Helper()
	var revision int64
	if err := f.pool.QueryRow(context.Background(), "SELECT active_revision FROM models WHERE id=$1", f.model).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	bindings := append([]catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "provider-model", Priority: 10, Weight: 1, MaxInflight: 4}}, extra...)
	if _, err := f.server.Catalog.Publish(context.Background(), f.actor, f.model, revision, bindings); err != nil {
		t.Fatal(err)
	}
}

func (f *credentialHTTPFixture) poolView(t *testing.T) catalog.CredentialPoolView {
	t.Helper()
	v, err := f.server.Catalog.CredentialPool(context.Background(), f.channel, "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *credentialHTTPFixture) addSecondary(t *testing.T) catalog.CredentialPoolView {
	t.Helper()
	current := f.poolView(t)
	v, err := f.server.Catalog.UpdateCredentialPool(context.Background(), f.actor, f.channel, catalog.CredentialPoolInput{ExpectedVersion: current.CurrentVersion, Members: []catalog.CredentialPoolMemberInput{
		{CredentialID: current.Members[0].CredentialID, Label: "primary", Weight: 1, MaxInflight: 2},
		{Secret: "pool-secondary-secret", Label: "secondary", Weight: 1, MaxInflight: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *credentialHTTPFixture) call(t *testing.T, method, suffix, body, session, csrf string) (int, []byte) {
	t.Helper()
	d := &discoveryFixture{gatewayFixture: f.gatewayFixture}
	return d.call(t, method, "/api/platform/channels/"+f.channel+suffix, body, session, csrf)
}

func TestCredentialPoolHTTPManagementCASHistoryAndSecretBoundary(t *testing.T) {
	f := newCredentialHTTPFixture(t, gatewaySuccess(t), nil)
	old := f.poolView(t)
	valid := fmt.Sprintf(`{"expected_version":%q,"members":[{"credential_id":%q,"label":"original","weight":1,"max_inflight":2},{"secret":"pool-secondary-secret","label":"second","weight":2,"max_inflight":3}]}`, old.CurrentVersion, old.Members[0].CredentialID)
	for _, raw := range []string{
		strings.Replace(valid, `"members":`, `"extra":true,"members":`, 1),
		strings.Replace(valid, `"weight":1`, `"weight":1,"weight":2`, 1),
		strings.Replace(valid, `"weight":1`, `"weight":1,"WEIGHT":2`, 1),
		strings.Replace(valid, `"expected_version":`, `"EXPECTED_VERSION":"alias","expected_version":`, 1),
		strings.Replace(valid, `"secret":`, `"SECRET":"alias","secret":`, 1),
		strings.Replace(valid, `"secret":`, `"ſecret":"alias","secret":`, 1),
		strings.Replace(valid, `"label":"second"`, `"label":"second","unknown":"secret-do-not-echo"`, 1),
		strings.Replace(valid, `"expected_version":`, `"expected_version":"duplicate","expected_version":`, 1),
		valid + `{}`, `null`, `[]`, `{"expected_version":"x","members":null}`,
	} {
		status, body := f.call(t, "PUT", "/credential-pool", raw, f.session, f.csrf)
		if status != 400 || strings.Contains(string(body), "pool-secondary-secret") || strings.Contains(string(body), "secret-do-not-echo") {
			t.Fatalf("ambiguous pool request accepted or reflected %d %s", status, body)
		}
	}
	status, body := f.call(t, "PUT", "/credential-pool", valid, f.session, f.csrf)
	var envelope struct {
		Data catalog.CredentialPoolView `json:"data"`
	}
	if status != 200 || json.Unmarshal(body, &envelope) != nil || len(envelope.Data.Members) != 2 || envelope.Data.CurrentVersion == old.CurrentVersion {
		t.Fatalf("pool save %d %s", status, body)
	}
	v := envelope.Data
	for _, forbidden := range []string{"pool-secondary-secret", "local-fixture-secret", "secret_ciphertext", "secret_prefix"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatal("pool exposed secret metadata", forbidden)
		}
	}
	status, _ = f.call(t, "PUT", "/credential-pool", valid, f.session, f.csrf)
	if status != 409 {
		t.Fatal("stale CAS overwrote pool", status)
	}
	status, body = f.call(t, "GET", "/credential-pool?version_id="+old.CurrentVersion, "", f.session, f.csrf)
	if status != 200 || json.Unmarshal(body, &envelope) != nil || !envelope.Data.Stale || len(envelope.Data.Members) != 1 || envelope.Data.Members[0].CredentialID != old.Members[0].CredentialID {
		t.Fatal("historical pool changed", status, string(body))
	}
	for range 2 {
		status, _ = f.call(t, "DELETE", "/credentials/"+v.Members[1].CredentialID, "", f.session, f.csrf)
		if status != 200 {
			t.Fatal("credential revocation failed", status)
		}
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_events WHERE action='channel.credential_revoke' AND target=$1", v.Members[1].CredentialID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("revocation was not idempotent", audits, err)
	}
	status, _ = f.call(t, "PUT", "/credential-pool", strings.Replace(valid, old.CurrentVersion, v.CurrentVersion, 1), f.session, f.csrf)
	if status != 400 {
		t.Fatal("revoked secret was resurrected", status)
	}
	status, _ = f.call(t, "PUT", "/credential-pool", fmt.Sprintf(`{"expected_version":%q,"members":[{"credential_id":%q,"label":"retained","weight":1,"max_inflight":2}]}`, v.CurrentVersion, v.Members[0].CredentialID), f.session, f.csrf)
	if status != 200 {
		t.Fatal("revoked member could not be removed", status)
	}
	probeAssertWalletUntouched(t, f.gatewayFixture)
}

func TestCredentialPoolHTTPAdminCSRFReauthenticationRecoveryAndOptionalMFA(t *testing.T) {
	f := newCredentialHTTPFixture(t, gatewaySuccess(t), nil)
	v := f.poolView(t)
	body := fmt.Sprintf(`{"expected_version":%q,"members":[{"credential_id":%q,"label":"retained","weight":1,"max_inflight":2}]}`, v.CurrentVersion, v.Members[0].CredentialID)
	methods := []struct{ method, path, body string }{{"GET", "/credential-pool", ""}, {"PUT", "/credential-pool", body}, {"DELETE", "/credentials/" + v.Members[0].CredentialID, ""}}
	for _, role := range []string{"none", "finance_operator", "finance_approver"} {
		var user string
		if err := f.pool.QueryRow(context.Background(), "SELECT id FROM users WHERE platform_role=$1 LIMIT 1", role).Scan(&user); err != nil {
			t.Fatal(err)
		}
		session, csrf := authSession(t, f.server, f.pool, user, "", true, true)
		for _, op := range methods {
			status, _ := f.call(t, op.method, op.path, op.body, session, csrf)
			if status != 403 {
				t.Fatal("non-admin pool access", role, op.method, status)
			}
		}
	}
	reauth, csrf := authSession(t, f.server, f.pool, f.actor, "", true, false)
	for _, op := range methods[1:] {
		status, _ := f.call(t, op.method, op.path, op.body, reauth, csrf)
		if status != 403 {
			t.Fatal("pool skipped recent password", status)
		}
		status, _ = f.call(t, op.method, op.path, op.body, f.session, "wrong")
		if status != 403 {
			t.Fatal("pool skipped CSRF", status)
		}
	}
	f.server.Config.RecoveryMode = true
	status, _ := f.call(t, "PUT", "/credential-pool", body, f.session, f.csrf)
	f.server.Config.RecoveryMode = false
	if status != 503 {
		t.Fatal("pool skipped recovery gate", status)
	}
	f.server.Ready(false)
	status, _ = f.call(t, "PUT", "/credential-pool", body, f.session, f.csrf)
	f.server.Ready(true)
	if status != 503 {
		t.Fatal("pool skipped readiness gate", status)
	}
	user := uuid.NewString()
	authExec(t, f.pool, "INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused','admin')", user, user+"@optional.invalid")
	optional, optionalCSRF := authSession(t, f.server, f.pool, user, "", false, true)
	status, _ = f.call(t, "PUT", "/credential-pool", body, optional, optionalCSRF)
	if status != 200 {
		t.Fatal("optional MFA became mandatory", status)
	}
	probeAssertWalletUntouched(t, f.gatewayFixture)
}

func TestCredentialPoolGatewayFrozenPublicationActualIdentityAndRevocation(t *testing.T) {
	observed := make(chan string, 4)
	f := newCredentialHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Header.Get("Authorization")
		gatewaySuccess(t)(w, r)
	}, nil)
	old := f.poolView(t)
	v := f.addSecondary(t)
	request := func(wantVersion, wantCredential, wantHeader string, total int64) {
		t.Helper()
		r := f.request(t, context.Background(), false, false)
		body, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 {
			t.Fatalf("pool inference %d %s", r.StatusCode, body)
		}
		var version, credential string
		var decisions int
		if err := f.pool.QueryRow(context.Background(), "SELECT channel_version_id,credential_id,jsonb_array_length(route_decisions) FROM upstream_attempts WHERE request_id=$1", r.Header.Get("X-Request-ID")).Scan(&version, &credential, &decisions); err != nil || version != wantVersion || credential != wantCredential || decisions != 1 {
			t.Fatal("actual pool identity/decision mismatch", version, credential, decisions, err)
		}
		if header := <-observed; header != wantHeader {
			t.Fatal("dispatched a different credential", header)
		}
		s := f.state(t, r.Header.Get("X-Request-ID"))
		if s.attempts != 1 || s.settlements != 1 || s.charge != 20 || s.available != 1000-total*20 || s.spent != total*20 || s.held != 0 {
			t.Fatalf("pool changed final charge %+v", s)
		}
		retrievalResourcesReleased(t, f.gatewayFixture)
		if f.server.Routing.CredentialInflight(credential) != 0 {
			t.Fatal("pool retained a lease")
		}
	}
	request(old.CurrentVersion, old.Members[0].CredentialID, "Bearer local-fixture-secret", 1)
	f.publish(t, nil)
	f.server.Routing.Cooldown(v.Members[0].CredentialID+":"+f.model, time.Minute)
	request(v.CurrentVersion, v.Members[1].CredentialID, "Bearer pool-secondary-secret", 2)
	if err := f.server.Catalog.RevokeCredential(context.Background(), f.actor, f.channel, v.Members[0].CredentialID); err != nil {
		t.Fatal(err)
	}
	request(v.CurrentVersion, v.Members[1].CredentialID, "Bearer pool-secondary-secret", 3)
	if err := f.server.Catalog.RevokeCredential(context.Background(), f.actor, f.channel, v.Members[1].CredentialID); err != nil {
		t.Fatal(err)
	}
	r := f.request(t, context.Background(), false, false)
	if r.StatusCode != 503 || f.posts.Load() != 3 {
		t.Fatal("all-revoked pool dispatched", r.StatusCode, f.posts.Load())
	}
	var records, settlements int
	if err := f.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM settlements)").Scan(&records, &settlements); err != nil || records != 3 || settlements != 3 {
		t.Fatal("all-revoked admission affected funds", records, settlements, err)
	}
}

func TestCredentialPoolGatewayRetryBudgetIsPerChannel(t *testing.T) {
	for _, mode := range []string{"unknown", "safe_only_pool", "safe_other_channel"} {
		t.Run(mode, func(t *testing.T) {
			status := 500
			if mode != "unknown" {
				status = 429
			}
			f := newCredentialHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				if mode == "unknown" {
					_, _ = io.WriteString(w, strings.Replace(gatewayJSON, `,"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}`, "", 1))
				} else {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
				}
			}, []int{429})
			v := f.addSecondary(t)
			var fallback atomic.Int32
			var extra []catalog.BindingInput
			if mode == "safe_other_channel" {
				u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fallback.Add(1); gatewaySuccess(t)(w, r) }))
				t.Cleanup(u.Close)
				id, err := f.server.Catalog.SaveChannel(context.Background(), f.actor, "", catalog.ChannelInput{Name: "fallback", BaseURL: u.URL + "/v1", Secret: "fallback-secret", AllowedCIDRs: []string{"127.0.0.0/8"}})
				if err != nil {
					t.Fatal(err)
				}
				extra = []catalog.BindingInput{{ChannelID: id, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: 2}}
			}
			f.publish(t, extra)
			r := f.request(t, context.Background(), false, false)
			_, _ = io.Copy(io.Discard, r.Body)
			s := f.state(t, r.Header.Get("X-Request-ID"))
			if f.posts.Load() != 1 {
				t.Fatal("retried another key within one channel", f.posts.Load())
			}
			if mode == "safe_other_channel" {
				if r.StatusCode != 200 || fallback.Load() != 1 {
					t.Fatal("safe failure did not reach next channel", r.StatusCode, fallback.Load())
				}
				f.assertSettled(t, r.Header.Get("X-Request-ID"), 2)
			} else if mode == "unknown" {
				if s.attempts != 1 || s.settlements != 0 || s.reservation != "pending_reconciliation" || s.held != 140 || s.spent != 0 {
					t.Fatalf("unknown pool consumption was retried/released %+v", s)
				}
			} else if s.attempts != 1 || s.settlements != 1 || s.charge != 0 || s.reservation != "released" || s.held != 0 || s.spent != 0 {
				t.Fatalf("safe exhausted pool affected charge %+v", s)
			}
			retrievalResourcesReleased(t, f.gatewayFixture)
			for _, m := range v.Members {
				if f.server.Routing.CredentialInflight(m.CredentialID) != 0 {
					t.Fatal("pool retry retained member lease")
				}
			}
		})
	}
}

func TestCredentialPoolDiscoveryUsesFirstLiveMemberAndTracksItsRevocation(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) { t.Error("discovery used old endpoint") })
	ctx := context.Background()
	old, err := f.server.Catalog.CredentialPool(ctx, f.channel, "")
	if err != nil {
		t.Fatal(err)
	}
	var gets atomic.Int32
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer pool-secondary-secret" {
			t.Error("directory selected wrong member")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"provider-model"}]}`)
	}))
	t.Cleanup(u.Close)
	if _, err = f.server.Catalog.SaveChannel(ctx, f.actor, f.channel, catalog.ChannelInput{BaseURL: u.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	current, err := f.server.Catalog.CredentialPool(ctx, f.channel, "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.server.Catalog.UpdateCredentialPool(ctx, f.actor, f.channel, catalog.CredentialPoolInput{ExpectedVersion: current.CurrentVersion, Members: []catalog.CredentialPoolMemberInput{
		{CredentialID: old.Members[0].CredentialID, Label: "primary", Weight: 1, MaxInflight: 2},
		{Secret: "pool-secondary-secret", Label: "secondary", Weight: 1, MaxInflight: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.server.Catalog.RevokeCredential(ctx, f.actor, f.channel, v.Members[0].CredentialID); err != nil {
		t.Fatal(err)
	}
	f.version = v.CurrentVersion
	status, raw := f.discover(t)
	if status != 200 {
		t.Fatalf("secondary directory %d %s", status, raw)
	}
	view := discoveryView(t, raw)
	if view.CredentialID != v.Members[1].CredentialID || view.Stale || gets.Load() != 1 || f.gets.Load() != 0 {
		t.Fatalf("wrong directory identity %+v", view)
	}
	if err = f.server.Catalog.RecheckModelDiscovery(ctx, view.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.server.Catalog.RevokeCredential(ctx, f.actor, f.channel, v.Members[1].CredentialID); err != nil {
		t.Fatal(err)
	}
	if err = f.server.Catalog.RecheckModelDiscovery(ctx, view.ID); !errors.Is(err, catalog.ErrConflict) {
		t.Fatal("revoked directory identity accepted", err)
	}
	view, err = f.server.Catalog.ModelDiscovery(ctx, f.channel, view.ID)
	if err != nil || !view.Stale {
		t.Fatal("revoked directory result not stale", err)
	}
	probeAssertWalletUntouched(t, f.gatewayFixture)
	if _, err = f.server.Catalog.BeginModelDiscovery(ctx, f.actor, f.channel, v.CurrentVersion, provider.DiscoveryFormatOpenAI); !errors.Is(err, catalog.ErrConflict) {
		t.Fatal("all revoked directory admitted", err)
	}
}
