package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

func keyPolicyHTTP(t *testing.T, engine http.Handler, method, path, body, session, csrf, origin, peer string, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if headers != nil {
		r.Header = headers.Clone()
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		r.AddCookie(&http.Cookie{Name: "moni_session", Value: session})
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if peer != "" {
		r.RemoteAddr = peer
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	return w
}

func keyPolicyView(t *testing.T, response *httptest.ResponseRecorder) identity.KeyAccessPolicyView {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("key policy response status=%d", response.Code)
	}
	var envelope struct {
		Data identity.KeyAccessPolicyView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Data.ID == "" || envelope.Data.AccessPolicyRevision == "" || envelope.Data.AllowedClientCIDRs == nil {
		t.Fatalf("key policy response omitted safe policy fields: %v", err)
	}
	return envelope.Data
}

func keyPolicyOwner(t *testing.T, f *gatewayFixture) (string, string) {
	t.Helper()
	var owner string
	if err := f.pool.QueryRow(context.Background(), "SELECT owner_id FROM api_keys WHERE id=$1", f.key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return authSession(t, f.server, f.pool, owner, f.tenant, false, true)
}

func keyPolicyPut(t *testing.T, f *gatewayFixture, revision string, cidrs []string) identity.KeyAccessPolicyView {
	t.Helper()
	session, csrf := keyPolicyOwner(t, f)
	body, err := json.Marshal(map[string]any{"expected_revision": revision, "allowed_client_cidrs": cidrs})
	if err != nil {
		t.Fatal(err)
	}
	return keyPolicyView(t, keyPolicyHTTP(t, f.server.Engine(), "PUT", "/api/keys/"+f.key+"/access-policy", string(body), session, csrf, f.server.Config.Origin, "", nil))
}

func TestKeyAccessPolicyHTTPCreateCASProjectionAndStrictBoundary(t *testing.T) {
	s, pool := newAuthFixture(t)
	owner, _ := authUser(t, s, pool, "none")
	tenant := uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'key-policy')", tenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", tenant, owner)
	session, csrf := authSession(t, s, pool, owner, tenant, false, true)
	engine := s.Engine()
	call := func(method, path, raw string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, method, path, raw, session, csrf, s.Config.Origin, "", nil)
	}
	created := call("POST", "/api/keys", `{"name":"restricted app","scopes":["*"],"lifetime_limit":null,"expires_at":null,"allowed_regions":["global"],"allowed_client_cidrs":["192.0.2.129/24","2001:db8::1","192.0.2.0/24"]}`)
	view := keyPolicyView(t, created)
	want := []string{"192.0.2.0/24", "2001:db8::1/128"}
	if view.AccessPolicyRevision != "1" || !reflect.DeepEqual(view.AllowedClientCIDRs, want) {
		t.Fatalf("creation did not return canonical policy: %+v", view)
	}
	var secret struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &secret); err != nil || !strings.HasPrefix(secret.Data.Key, "mg_") {
		t.Fatal("creation lost its one-time key response")
	}
	path := "/api/keys/" + view.ID + "/access-policy"
	response := call("PUT", path, `{"expected_revision":"1","allowed_client_cidrs":["198.51.100.7"]}`)
	updated := keyPolicyView(t, response)
	if updated.AccessPolicyRevision != "2" || !reflect.DeepEqual(updated.AllowedClientCIDRs, []string{"198.51.100.7/32"}) {
		t.Fatalf("CAS update did not return new canonical policy: %+v", updated)
	}
	if strings.Contains(response.Body.String(), secret.Data.Key) || strings.Contains(response.Body.String(), `"key":`) || strings.Contains(response.Body.String(), "digest") {
		t.Fatal("policy update exposed key material")
	}
	read := call("GET", path, "")
	if got := keyPolicyView(t, read); !reflect.DeepEqual(got, updated) {
		t.Fatal("exact policy lookup omitted the committed policy")
	}
	var safeEnvelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(read.Body.Bytes(), &safeEnvelope) != nil || len(safeEnvelope.Data) != 3 || read.Header().Get("Cache-Control") != "no-store" || read.Header().Get("X-Content-Type-Options") != "nosniff" || read.Header().Get("X-Request-ID") == "" || strings.Contains(read.Body.String(), secret.Data.Key) || strings.Contains(read.Body.String(), "digest") {
		t.Fatal("exact lookup exposed unsafe metadata or omitted security headers")
	}
	if got := call("PUT", path, `{"expected_revision":"1","allowed_client_cidrs":[]}`); got.Code != http.StatusConflict {
		t.Fatalf("stale CAS status=%d", got.Code)
	}
	for _, raw := range []string{
		`{}`, `null`, `[]`, `{"expected_revision":"2"}`, `{"allowed_client_cidrs":[]}`,
		`{"expected_revision":"2","allowed_client_cidrs":null}`,
		`{"expected_revision":2,"allowed_client_cidrs":[]}`,
		`{"expected_revision":"02","allowed_client_cidrs":[]}`,
		`{"expected_revision":"2","expected_revision":"2","allowed_client_cidrs":[]}`,
		`{"expected_revision":"2","EXPECTED_REVISION":"2","allowed_client_cidrs":[]}`,
		`{"expected_revision":"2","allowed_client_cidrs":[],"ALLOWED_CLIENT_CIDRS":[]}`,
		`{"expected_revision":"2","allowed_client_cidrs":["private-marker.example"]}`,
		`{"expected_revision":"2","allowed_client_cidrs":[],"secret":"private-marker"}`,
		`{"expected_revision":"2","allowed_client_cidrs":[]} {}`,
		`{"expected_revision":"2","allowed_client_cidrs":["` + strings.Repeat("private-marker", 1400) + `"]}`,
	} {
		got := call("PUT", path, raw)
		if got.Code != http.StatusBadRequest || strings.Contains(got.Body.String(), "private-marker") {
			t.Fatalf("invalid policy accepted or reflected input: status=%d", got.Code)
		}
	}
	for _, raw := range []string{
		`{"name":"private-marker","scopes":["*"],"unknown":true}`,
		`{"name":"private-marker","name":"other","scopes":["*"]}`,
		`{"name":"private-marker","NAME":"other","scopes":["*"]}`,
		`{"name":"private-marker","scopes":["*"],"allowed_client_cidrs":[],"ALLOWED_CLIENT_CIDRS":[]}`,
		`{"name":"private-marker","scopes":["*"],"allowed_client_cidrs":["invalid.example"]}`,
		`{"name":"private-marker","scopes":["*"]} {}`,
	} {
		got := call("POST", "/api/keys", raw)
		if got.Code != http.StatusBadRequest || strings.Contains(got.Body.String(), "private-marker") {
			t.Fatalf("invalid create accepted or reflected input: status=%d", got.Code)
		}
	}
	listed := call("GET", "/api/keys", "")
	var list struct {
		Data []identity.KeyAccessPolicyView `json:"data"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &list) != nil || len(list.Data) != 1 || !reflect.DeepEqual(list.Data[0], updated) || strings.Contains(listed.Body.String(), secret.Data.Key) {
		t.Fatal("key list omitted current policy, leaked a key or invalid writes persisted")
	}
	cleared := keyPolicyView(t, call("PUT", path, `{"expected_revision":"2","allowed_client_cidrs":[]}`))
	if cleared.AccessPolicyRevision != "3" || len(cleared.AllowedClientCIDRs) != 0 {
		t.Fatal("an explicit empty array did not clear the policy")
	}
}

func TestKeyAccessPolicyHTTPCurrentRolesAndSecurityGates(t *testing.T) {
	s, pool := newAuthFixture(t)
	owner, _ := authUser(t, s, pool, "none")
	member, _ := authUser(t, s, pool, "none")
	viewer, _ := authUser(t, s, pool, "none")
	foreignOwner, _ := authUser(t, s, pool, "none")
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'policy-A'),($2,'policy-B')", tenant, foreignTenant)
	for _, row := range []struct{ tenant, user, role string }{{tenant, owner, "owner"}, {tenant, member, "member"}, {tenant, viewer, "billing_viewer"}, {foreignTenant, foreignOwner, "owner"}} {
		authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,$3,true)", row.tenant, row.user, row.role)
	}
	ownerSession, ownerCSRF := authSession(t, s, pool, owner, tenant, false, true)
	memberSession, memberCSRF := authSession(t, s, pool, member, tenant, false, true)
	viewerSession, viewerCSRF := authSession(t, s, pool, viewer, tenant, false, true)
	foreignSession, foreignCSRF := authSession(t, s, pool, foreignOwner, foreignTenant, false, true)
	engine := s.Engine()
	create := func(session, csrf string) identity.KeyAccessPolicyView {
		return keyPolicyView(t, keyPolicyHTTP(t, engine, "POST", "/api/keys", `{"name":"app","scopes":["*"]}`, session, csrf, s.Config.Origin, "", nil))
	}
	ownerKey, memberKey := create(ownerSession, ownerCSRF), create(memberSession, memberCSRF)
	viewerKey := create(ownerSession, ownerCSRF)
	authExec(t, pool, "UPDATE api_keys SET owner_id=$2 WHERE id=$1", viewerKey.ID, viewer)
	put := func(key, session, csrf string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, "PUT", "/api/keys/"+key+"/access-policy", `{"expected_revision":"1","allowed_client_cidrs":["192.0.2.1"]}`, session, csrf, s.Config.Origin, "", nil)
	}
	if got := put(ownerKey.ID, memberSession, memberCSRF); got.Code != 403 {
		t.Fatalf("member edited another owner's key: %d", got.Code)
	}
	if got := put(ownerKey.ID, viewerSession, viewerCSRF); got.Code != 403 {
		t.Fatalf("billing viewer edited key: %d", got.Code)
	}
	if got := put(ownerKey.ID, foreignSession, foreignCSRF); got.Code != 403 {
		t.Fatalf("foreign key identity was exposed: %d", got.Code)
	}
	if got := put(memberKey.ID, memberSession, memberCSRF); got.Code != 200 {
		t.Fatalf("member could not edit own key: %d", got.Code)
	}
	get := func(key, session string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, "GET", "/api/keys/"+key+"/access-policy", "", session, "", "", "", nil)
	}
	for _, tc := range []struct {
		name, key, session string
		want               int
	}{
		{"owner_reads_member", memberKey.ID, ownerSession, 200},
		{"member_reads_self", memberKey.ID, memberSession, 200},
		{"member_cannot_read_owner", ownerKey.ID, memberSession, 403},
		{"viewer_reads_self", viewerKey.ID, viewerSession, 200},
		{"viewer_cannot_read_owner", ownerKey.ID, viewerSession, 403},
		{"foreign", ownerKey.ID, foreignSession, 403},
		{"missing", uuid.NewString(), ownerSession, 403},
		{"no_session", ownerKey.ID, "", 401},
	} {
		if got := get(tc.key, tc.session); got.Code != tc.want {
			t.Fatalf("%s lookup status=%d want=%d", tc.name, got.Code, tc.want)
		}
	}
	managed := keyPolicyView(t, keyPolicyHTTP(t, engine, "PUT", "/api/keys/"+memberKey.ID+"/access-policy", `{"expected_revision":"2","allowed_client_cidrs":["198.51.100.1"]}`, ownerSession, ownerCSRF, s.Config.Origin, "", nil))
	if managed.AccessPolicyRevision != "3" {
		t.Fatal("owner could not manage a member's policy")
	}
	// Lock-time role checks must override a still-live owner session.
	authExec(t, pool, "UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2", tenant, owner)
	if got := get(memberKey.ID, ownerSession); got.Code != 403 {
		t.Fatalf("lookup used a stale owner role: %d", got.Code)
	}
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 403 {
		t.Fatalf("stale owner session bypassed current role: %d", got.Code)
	}
	authExec(t, pool, "UPDATE memberships SET role='owner' WHERE tenant_id=$1 AND user_id=$2", tenant, owner)
	staleSession, staleCSRF := authSession(t, s, pool, owner, tenant, false, false)
	if got := get(ownerKey.ID, staleSession); got.Code != 200 {
		t.Fatalf("read-only lookup unexpectedly required recent reauthentication: %d", got.Code)
	}
	for _, tc := range []struct {
		name, session, csrf string
		want                int
	}{
		{"unauthenticated", "", "", 401}, {"csrf", ownerSession, "wrong", 403}, {"reauth", staleSession, staleCSRF, 403},
	} {
		if got := put(ownerKey.ID, tc.session, tc.csrf); got.Code != tc.want {
			t.Fatalf("%s gate status=%d want=%d", tc.name, got.Code, tc.want)
		}
	}
	s.Config.RecoveryMode = true
	if got := get(ownerKey.ID, staleSession); got.Code != 200 {
		t.Fatalf("recovery blocked read-only policy inspection: %d", got.Code)
	}
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 503 {
		t.Fatalf("recovery allowed source policy mutation: %d", got.Code)
	}
	s.Config.RecoveryMode = false
	s.Ready(false)
	if got := get(ownerKey.ID, ownerSession); got.Code != 503 {
		t.Fatalf("unready instance allowed policy lookup: %d", got.Code)
	}
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 503 {
		t.Fatalf("unready instance allowed source policy mutation: %d", got.Code)
	}
	s.Ready(true)
	s.Guard = func(context.Context) error { return errors.New("fixture guard unavailable") }
	if got := get(ownerKey.ID, ownerSession); got.Code != 503 {
		t.Fatalf("failed instance guard allowed policy lookup: %d", got.Code)
	}
	s.Ready(true)
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 503 {
		t.Fatalf("failed instance guard allowed source policy mutation: %d", got.Code)
	}
	s.Guard = nil
	s.Ready(true)
	authExec(t, pool, "UPDATE users SET mfa_enabled=true WHERE id=$1", owner)
	if got := get(ownerKey.ID, ownerSession); got.Code != 403 {
		t.Fatalf("enabled MFA was bypassed by policy lookup: %d", got.Code)
	}
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 403 {
		t.Fatalf("enabled MFA was bypassed: %d", got.Code)
	}
	authExec(t, pool, "UPDATE users SET mfa_enabled=false WHERE id=$1", owner)
	for _, tc := range []struct {
		name, suspend, restore string
		args                   []any
		want                   int
	}{
		{"tenant", "UPDATE tenants SET status='suspended' WHERE id=$1", "UPDATE tenants SET status='active' WHERE id=$1", []any{tenant}, 403},
		{"membership", "UPDATE memberships SET status='suspended' WHERE tenant_id=$1 AND user_id=$2", "UPDATE memberships SET status='active' WHERE tenant_id=$1 AND user_id=$2", []any{tenant, owner}, 403},
		{"user", "UPDATE users SET status='suspended' WHERE id=$1", "UPDATE users SET status='active' WHERE id=$1", []any{owner}, 401},
	} {
		authExec(t, pool, tc.suspend, tc.args...)
		if got := get(ownerKey.ID, ownerSession); got.Code != tc.want {
			t.Fatalf("lookup bypassed current %s status: %d", tc.name, got.Code)
		}
		authExec(t, pool, tc.restore, tc.args...)
	}
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 200 {
		t.Fatalf("optional unbound MFA blocked policy mutation: %d", got.Code)
	}
	authExec(t, pool, "UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1", ownerKey.ID)
	if got := get(ownerKey.ID, ownerSession); got.Code != 200 {
		t.Fatalf("revoked policy could not be safely inspected: %d", got.Code)
	}
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 403 {
		t.Fatalf("revoked key allowed policy mutation: %d", got.Code)
	}
}

func keyPolicyProtocolInput(t *testing.T, protocol string) (string, string) {
	t.Helper()
	path := "/v1/chat/completions"
	input := map[string]any{"model": "retail-model", "messages": []map[string]string{{"role": "user", "content": "hello"}}, "max_tokens": 20, "stream": false}
	switch protocol {
	case provider.ProtocolResponses, provider.ProtocolMessages:
		path, input = nativePath(protocol), nativeBody(protocol, false)
	case provider.ProtocolEmbeddings, provider.ProtocolRerank:
		path, input = retrievalPath(protocol), retrievalBody(protocol)
	case provider.ProtocolGemini:
		path, input = geminiPath(false), geminiBody()
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return path, string(raw)
}

func TestKeyAccessPolicyHTTPAllProtocolsResolveTrustedSourceAndRejectSpoofing(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolMessages, provider.ProtocolEmbeddings, provider.ProtocolRerank, provider.ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			result := gatewayJSON
			switch protocol {
			case provider.ProtocolResponses, provider.ProtocolMessages:
				result = nativeJSON(protocol)
			case provider.ProtocolEmbeddings, provider.ProtocolRerank:
				result = retrievalJSON(protocol)
			case provider.ProtocolGemini:
				result = geminiResponseJSON
			}
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, result)
			}}, nil, 8, protocol)
			policy := keyPolicyPut(t, f, "1", []string{"198.51.100.0/24", "2001:db8::/64"})
			path, raw := keyPolicyProtocolInput(t, protocol)
			for _, tc := range []struct {
				name, peer string
				trusted    []string
				realIPs    []string
			}{
				{"default_untrusted", "127.0.0.1:1234", nil, []string{"198.51.100.7"}},
				{"outside_proxy_allowlist", "203.0.113.7:1234", []string{"127.0.0.1/32"}, []string{"198.51.100.7"}},
				{"xff_cannot_override_denied_xri", "127.0.0.1:1234", []string{"127.0.0.1/32"}, []string{"203.0.113.9"}},
				{"duplicate_xri", "127.0.0.1:1234", []string{"127.0.0.1/32"}, []string{"198.51.100.7", "198.51.100.8"}},
				{"chain_xri", "127.0.0.1:1234", []string{"127.0.0.1/32"}, []string{"198.51.100.7, 198.51.100.8"}},
				{"invalid_xri", "127.0.0.1:1234", []string{"127.0.0.1/32"}, []string{"private-invalid-source"}},
				{"zone_xri", "127.0.0.1:1234", []string{"127.0.0.1/32"}, []string{"2001:db8::7%private-zone"}},
				{"missing_xri", "127.0.0.1:1234", []string{"127.0.0.1/32"}, nil},
			} {
				f.server.Config.TrustedProxyCIDRs = tc.trusted
				engine := f.server.Engine()
				headers := http.Header{"Authorization": []string{"Bearer " + f.token}, "X-Real-Ip": tc.realIPs, "X-Forwarded-For": []string{"198.51.100.7"}}
				for _, route := range []struct{ method, path, body string }{{"POST", path, raw}, {"GET", "/v1/models", ""}} {
					got := keyPolicyHTTP(t, engine, route.method, route.path, route.body, "", "", "", tc.peer, headers)
					if got.Code != http.StatusForbidden || strings.Contains(got.Body.String(), "private-") || strings.Contains(got.Body.String(), "198.51.100") || strings.Contains(got.Body.String(), "203.0.113") {
						t.Fatalf("%s %s did not reject source safely: status=%d", tc.name, route.path, got.Code)
					}
				}
				admissionAssertNoFacts(t, f)
			}
			f.server.Config.TrustedProxyCIDRs = []string{"127.0.0.1/32"}
			peer, source, normalized := "127.0.0.1:1234", "198.51.100.7", "198.51.100.7"
			switch protocol {
			case provider.ProtocolResponses:
				source = "::ffff:198.51.100.7"
			case provider.ProtocolMessages:
				source, normalized = "2001:db8::7", "2001:db8::7"
			case provider.ProtocolEmbeddings:
				peer, source, normalized = "[2001:db8::7]:1234", "203.0.113.9", "2001:db8::7"
			case provider.ProtocolRerank:
				peer, source = "[::ffff:198.51.100.7]:1234", "203.0.113.9"
			}
			engine := f.server.Engine()
			headers := http.Header{"Authorization": []string{"Bearer " + f.token}, "X-Real-Ip": []string{source}, "X-Forwarded-For": []string{"203.0.113.9"}}
			if got := keyPolicyHTTP(t, engine, "GET", "/v1/models", "", "", "", "", peer, headers); got.Code != 200 {
				t.Fatalf("allowed source could not list models: %d", got.Code)
			}
			admissionAssertNoFacts(t, f)
			got := keyPolicyHTTP(t, engine, "POST", path, raw, "", "", "", peer, headers)
			if got.Code != 200 {
				t.Fatalf("allowed source could not infer: status=%d body=%s", got.Code, got.Body.String())
			}
			retrievalResourcesReleased(t, f)
			if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
				retrievalAssertSettled(t, f, got.Header().Get("X-Request-ID"))
			} else {
				f.assertSettled(t, got.Header().Get("X-Request-ID"), 1)
			}
			var persisted, revision string
			if err := f.pool.QueryRow(context.Background(), "SELECT host(r.client_ip),k.access_policy_revision::text FROM request_records r JOIN api_keys k ON k.id=r.key_id WHERE r.id=$1", got.Header().Get("X-Request-ID")).Scan(&persisted, &revision); err != nil || persisted != normalized || revision != policy.AccessPolicyRevision {
				t.Fatalf("trusted source was not fixed and persisted, or money changed policy revision: source=%q revision=%q err=%v", persisted, revision, err)
			}
		})
	}
}

func TestKeyAccessPolicyHTTPRechecksAfterAdmissionWait(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	routes := admissionRouteResources(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner, err := f.server.admission.acquire(ctx, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	request := admissionHTTPRequest(t, f, ctx, false)
	result := admissionStartHTTP(f.http.Client(), request)
	admissionAwait(t, "HTTP source authenticated and waiting for admission", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 2
	})
	admissionAssertNoFacts(t, f)
	keyPolicyPut(t, f, "1", []string{"192.0.2.1"})
	owner()
	got := admissionAwaitHTTP(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	body, err := io.ReadAll(got.response.Body)
	if err != nil || got.response.StatusCode != 403 || !strings.Contains(string(body), "key_ip_forbidden") || strings.Contains(string(body), "127.0.0.1") {
		t.Fatalf("policy tightening was not rechecked before Reserve: status=%d body=%s err=%v", got.response.StatusCode, body, err)
	}
	admissionAwait(t, "source rejection returning all queue and routing resources", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
	})
	admissionAssertNoFacts(t, f)
}

func TestKeyAccessPolicyHTTPAlreadySentStreamSettlesAfterTightening(t *testing.T) {
	finish := make(chan struct{})
	var once sync.Once
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", gatewayContent)
		w.(http.Flusher).Flush()
		select {
		case <-finish:
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", gatewayFinish, gatewayUsage)
		case <-r.Context().Done():
		}
	}}, nil)
	t.Cleanup(func() { once.Do(func() { close(finish) }) })
	routes := admissionRouteResources(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first := admissionAwaitHTTP(t, admissionStartHTTP(f.http.Client(), admissionHTTPRequest(t, f, ctx, true)))
	if first.err != nil {
		t.Fatal(first.err)
	}
	reader := bufio.NewReader(first.response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || first.response.StatusCode != 200 || !strings.Contains(line, "hello") {
		t.Fatalf("stream did not cross its sent boundary: status=%d line=%s err=%v", first.response.StatusCode, line, err)
	}
	policy := keyPolicyPut(t, f, "1", []string{"192.0.2.1"})
	second := f.request(t, ctx, false, false)
	denied, err := io.ReadAll(second.Body)
	if err != nil || second.StatusCode != 403 || !strings.Contains(string(denied), "key_ip_forbidden") || f.posts.Load() != 1 {
		t.Fatalf("new source policy failed to reject successor: status=%d posts=%d err=%v", second.StatusCode, f.posts.Load(), err)
	}
	once.Do(func() { close(finish) })
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "[DONE]") {
		t.Fatalf("admitted stream did not finish after tightening: %s err=%v", rest, err)
	}
	admissionAwait(t, "old stream returning all queue and routing resources", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
	})
	f.assertSettled(t, first.response.Header.Get("X-Request-ID"), 1)
	var requests, settlements int
	var revision string
	if err := f.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM settlements),access_policy_revision::text FROM api_keys WHERE id=$1", f.key).Scan(&requests, &settlements, &revision); err != nil || requests != 1 || settlements != 1 || revision != policy.AccessPolicyRevision || f.posts.Load() != 1 {
		t.Fatalf("tightening changed existing settlement or created new facts: requests=%d settlements=%d revision=%q posts=%d err=%v", requests, settlements, revision, f.posts.Load(), err)
	}
}
