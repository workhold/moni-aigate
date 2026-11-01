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

func keyRuntimeView(t *testing.T, response *httptest.ResponseRecorder) identity.KeyLimitsView {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("key runtime response status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data identity.KeyLimitsView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Data.ID == "" || envelope.Data.LimitRevision == "" || envelope.Data.RPMLimit < 1 || envelope.Data.BurstLimit < 1 || envelope.Data.ConcurrencyLimit < 1 {
		t.Fatalf("key runtime response omitted safe limit fields: %v", err)
	}
	return envelope.Data
}

func keyRuntimePut(t *testing.T, f *gatewayFixture, revision string, rpm, burst, concurrency int) identity.KeyLimitsView {
	t.Helper()
	session, csrf := keyPolicyOwner(t, f)
	body, err := json.Marshal(map[string]any{"expected_revision": revision, "rpm_limit": rpm, "burst_limit": burst, "concurrency_limit": concurrency})
	if err != nil {
		t.Fatal(err)
	}
	return keyRuntimeView(t, keyPolicyHTTP(t, f.server.Engine(), "PUT", "/api/keys/"+f.key+"/runtime-limits", string(body), session, csrf, f.server.Config.Origin, "", nil))
}

func TestKeyRuntimeLimitsHTTPCreateCASProjectionAndStrictBoundary(t *testing.T) {
	s, pool := newAuthFixture(t)
	owner, _ := authUser(t, s, pool, "none")
	tenant := uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'runtime-policy')", tenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", tenant, owner)
	session, csrf := authSession(t, s, pool, owner, tenant, false, true)
	engine := s.Engine()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, method, path, body, session, csrf, s.Config.Origin, "", nil)
	}
	defaultView := keyRuntimeView(t, call("POST", "/api/keys", `{"name":"default app","scopes":["*"]}`))
	if defaultView.RPMLimit != 60 || defaultView.BurstLimit != 20 || defaultView.ConcurrencyLimit != 16 || defaultView.LimitRevision != "1" {
		t.Fatalf("omitted creation limits did not retain established defaults: %+v", defaultView)
	}
	created := call("POST", "/api/keys", `{"name":"custom app","scopes":["*"],"allowed_client_cidrs":["192.0.2.1"],"rpm_limit":6000,"burst_limit":200,"concurrency_limit":16}`)
	view := keyRuntimeView(t, created)
	if view.RPMLimit != 6000 || view.BurstLimit != 200 || view.ConcurrencyLimit != 16 || view.LimitRevision != "1" {
		t.Fatalf("explicit creation limits were lost: %+v", view)
	}
	var secret struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if json.Unmarshal(created.Body.Bytes(), &secret) != nil || !strings.HasPrefix(secret.Data.Key, "mg_") {
		t.Fatal("limit creation lost its one-time key")
	}
	path := "/api/keys/" + view.ID + "/runtime-limits"
	updated := keyRuntimeView(t, call("PUT", path, `{"expected_revision":"1","rpm_limit":1,"burst_limit":1,"concurrency_limit":1}`))
	if updated.RPMLimit != 1 || updated.BurstLimit != 1 || updated.ConcurrencyLimit != 1 || updated.LimitRevision != "2" {
		t.Fatalf("CAS update omitted new limits: %+v", updated)
	}
	read := call("GET", path, "")
	if got := keyRuntimeView(t, read); !reflect.DeepEqual(got, updated) {
		t.Fatal("exact limit lookup did not return committed limits")
	}
	var safeEnvelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(read.Body.Bytes(), &safeEnvelope) != nil || len(safeEnvelope.Data) != 5 || read.Header().Get("Cache-Control") != "no-store" || read.Header().Get("X-Content-Type-Options") != "nosniff" || read.Header().Get("X-Request-ID") == "" || strings.Contains(read.Body.String(), secret.Data.Key) || strings.Contains(read.Body.String(), "digest") {
		t.Fatal("exact runtime lookup exposed unsafe metadata or omitted security headers")
	}
	if got := call("PUT", path, `{"expected_revision":"1","rpm_limit":1,"burst_limit":1,"concurrency_limit":1}`); got.Code != 409 {
		t.Fatalf("stale runtime CAS status=%d", got.Code)
	}
	if got := keyRuntimeView(t, call("PUT", path, `{"expected_revision":"2","rpm_limit":1,"burst_limit":1,"concurrency_limit":1}`)); got.LimitRevision != "2" {
		t.Fatal("same-content runtime CAS changed revision")
	}
	for _, body := range []string{
		`{}`, `null`, `[]`,
		`{"rpm_limit":1,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1,"burst_limit":1}`,
		`{"expected_revision":2,"rpm_limit":1,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"02","rpm_limit":1,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":null,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1,"burst_limit":null,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1,"burst_limit":1,"concurrency_limit":null}`,
		`{"expected_revision":"2","rpm_limit":"1","burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1.5,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1e2,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":0,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":6001,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1,"burst_limit":201,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1,"burst_limit":1,"concurrency_limit":17}`,
		`{"expected_revision":"2","rpm_limit":1,"rpm_limit":1,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1,"RPM_LIMIT":1,"burst_limit":1,"concurrency_limit":1}`,
		`{"expected_revision":"2","rpm_limit":1,"burst_limit":1,"concurrency_limit":1,"secret":"private-marker"}`,
		`{"expected_revision":"2","rpm_limit":1,"burst_limit":1,"concurrency_limit":1} {}`,
		`{"expected_revision":"` + strings.Repeat("private-marker", 1400) + `","rpm_limit":1,"burst_limit":1,"concurrency_limit":1}`,
	} {
		got := call("PUT", path, body)
		if got.Code != 400 || strings.Contains(got.Body.String(), "private-marker") {
			t.Fatalf("invalid runtime PUT accepted or reflected input: status=%d", got.Code)
		}
	}
	for _, fields := range []string{
		`"rpm_limit":null`, `"burst_limit":null`, `"concurrency_limit":null`,
		`"rpm_limit":0`, `"rpm_limit":6001`, `"burst_limit":201`, `"concurrency_limit":17`,
		`"rpm_limit":"1"`, `"burst_limit":1.5`, `"concurrency_limit":1e1`,
		`"rpm_limit":1,"rpm_limit":1`, `"rpm_limit":1,"RPM_LIMIT":1`, `"limit_revision":"1"`,
	} {
		got := call("POST", "/api/keys", `{"name":"private-marker","scopes":["*"],`+fields+`}`)
		if got.Code != 400 || strings.Contains(got.Body.String(), "private-marker") {
			t.Fatalf("invalid runtime creation accepted or reflected input: status=%d", got.Code)
		}
	}
	listed := call("GET", "/api/keys", "")
	var list struct {
		Data []identity.KeyLimitsView `json:"data"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &list) != nil || len(list.Data) != 2 || strings.Contains(listed.Body.String(), secret.Data.Key) {
		t.Fatal("runtime list omitted limits, leaked a key or invalid writes persisted")
	}
	byID := map[string]identity.KeyLimitsView{}
	for _, row := range list.Data {
		byID[row.ID] = row
	}
	if !reflect.DeepEqual(byID[updated.ID], updated) || !reflect.DeepEqual(byID[defaultView.ID], defaultView) {
		t.Fatal("runtime list lost explicit or default limits")
	}
	var digest, accessRevision string
	var sourceCIDRs, scopes []string
	if err := pool.QueryRow(context.Background(), "SELECT digest,scopes,allowed_client_cidrs,access_policy_revision::text FROM api_keys WHERE id=$1", view.ID).Scan(&digest, &scopes, &sourceCIDRs, &accessRevision); err != nil || digest != s.Config.Secrets.Digest(secret.Data.Key) || !reflect.DeepEqual(scopes, []string{"*"}) || !reflect.DeepEqual(sourceCIDRs, []string{"192.0.2.1/32"}) || accessRevision != "1" {
		t.Fatalf("runtime editing changed key identity or source authorization: err=%v", err)
	}
}

func TestKeyRuntimeLimitsHTTPCurrentRolesAndSecurityGates(t *testing.T) {
	s, pool := newAuthFixture(t)
	owner, _ := authUser(t, s, pool, "none")
	member, _ := authUser(t, s, pool, "none")
	viewer, _ := authUser(t, s, pool, "none")
	foreignOwner, _ := authUser(t, s, pool, "none")
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'runtime-A'),($2,'runtime-B')", tenant, foreignTenant)
	for _, row := range []struct{ tenant, user, role string }{{tenant, owner, "owner"}, {tenant, member, "member"}, {tenant, viewer, "billing_viewer"}, {foreignTenant, foreignOwner, "owner"}} {
		authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,$3,true)", row.tenant, row.user, row.role)
	}
	ownerSession, ownerCSRF := authSession(t, s, pool, owner, tenant, false, true)
	memberSession, memberCSRF := authSession(t, s, pool, member, tenant, false, true)
	viewerSession, viewerCSRF := authSession(t, s, pool, viewer, tenant, false, true)
	foreignSession, foreignCSRF := authSession(t, s, pool, foreignOwner, foreignTenant, false, true)
	engine := s.Engine()
	create := func(session, csrf string) identity.KeyLimitsView {
		return keyRuntimeView(t, keyPolicyHTTP(t, engine, "POST", "/api/keys", `{"name":"app","scopes":["*"]}`, session, csrf, s.Config.Origin, "", nil))
	}
	ownerKey, memberKey, viewerKey := create(ownerSession, ownerCSRF), create(memberSession, memberCSRF), create(ownerSession, ownerCSRF)
	authExec(t, pool, "UPDATE api_keys SET owner_id=$2 WHERE id=$1", viewerKey.ID, viewer)
	put := func(key, session, csrf string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, "PUT", "/api/keys/"+key+"/runtime-limits", `{"expected_revision":"1","rpm_limit":1,"burst_limit":1,"concurrency_limit":1}`, session, csrf, s.Config.Origin, "", nil)
	}
	get := func(key, session string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, "GET", "/api/keys/"+key+"/runtime-limits", "", session, "", "", "", nil)
	}
	for _, tc := range []struct {
		name, key, session, csrf string
		read, write              int
	}{
		{"member_other", ownerKey.ID, memberSession, memberCSRF, 403, 403},
		{"viewer_other", ownerKey.ID, viewerSession, viewerCSRF, 403, 403},
		{"viewer_own", viewerKey.ID, viewerSession, viewerCSRF, 200, 403},
		{"foreign", ownerKey.ID, foreignSession, foreignCSRF, 403, 403},
		{"missing", uuid.NewString(), ownerSession, ownerCSRF, 403, 403},
		{"unauthenticated", ownerKey.ID, "", "", 401, 401},
	} {
		if got := get(tc.key, tc.session); got.Code != tc.read {
			t.Fatalf("%s runtime read status=%d", tc.name, got.Code)
		}
		if got := put(tc.key, tc.session, tc.csrf); got.Code != tc.write {
			t.Fatalf("%s runtime write status=%d", tc.name, got.Code)
		}
	}
	if got := put(memberKey.ID, memberSession, memberCSRF); got.Code != 200 {
		t.Fatalf("member could not update own limits: %d", got.Code)
	}
	managed := keyRuntimeView(t, keyPolicyHTTP(t, engine, "PUT", "/api/keys/"+memberKey.ID+"/runtime-limits", `{"expected_revision":"2","rpm_limit":2,"burst_limit":2,"concurrency_limit":2}`, ownerSession, ownerCSRF, s.Config.Origin, "", nil))
	if managed.LimitRevision != "3" {
		t.Fatal("owner could not manage a member's limits")
	}
	staleSession, staleCSRF := authSession(t, s, pool, owner, tenant, false, false)
	if got := get(ownerKey.ID, staleSession); got.Code != 200 {
		t.Fatalf("read-only runtime lookup required recent reauthentication: %d", got.Code)
	}
	for _, tc := range []struct{ name, session, csrf string }{{"csrf", ownerSession, "wrong"}, {"reauth", staleSession, staleCSRF}} {
		if got := put(ownerKey.ID, tc.session, tc.csrf); got.Code != 403 {
			t.Fatalf("%s runtime gate status=%d", tc.name, got.Code)
		}
	}
	authExec(t, pool, "UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2", tenant, owner)
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 403 {
		t.Fatalf("runtime mutation used a stale owner role: %d", got.Code)
	}
	if got := get(memberKey.ID, ownerSession); got.Code != 403 {
		t.Fatalf("runtime lookup used a stale owner role: %d", got.Code)
	}
	authExec(t, pool, "UPDATE memberships SET role='owner' WHERE tenant_id=$1 AND user_id=$2", tenant, owner)
	s.Config.RecoveryMode = true
	if got := get(ownerKey.ID, staleSession); got.Code != 200 {
		t.Fatalf("recovery blocked runtime inspection: %d", got.Code)
	}
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 503 {
		t.Fatalf("recovery allowed runtime mutation: %d", got.Code)
	}
	s.Config.RecoveryMode = false
	s.Ready(false)
	if got := get(ownerKey.ID, ownerSession); got.Code != 503 {
		t.Fatalf("unready instance allowed runtime lookup: %d", got.Code)
	}
	s.Ready(true)
	s.Guard = func(context.Context) error { return errors.New("fixture guard unavailable") }
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 503 {
		t.Fatalf("failed guard allowed runtime mutation: %d", got.Code)
	}
	s.Guard = nil
	s.Ready(true)
	authExec(t, pool, "UPDATE users SET mfa_enabled=true WHERE id=$1", owner)
	if got := get(ownerKey.ID, ownerSession); got.Code != 403 {
		t.Fatalf("enabled MFA was bypassed by runtime lookup: %d", got.Code)
	}
	authExec(t, pool, "UPDATE users SET mfa_enabled=false WHERE id=$1", owner)
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 200 {
		t.Fatalf("optional unbound MFA blocked runtime mutation: %d", got.Code)
	}
	authExec(t, pool, "UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1", ownerKey.ID)
	if got := get(ownerKey.ID, ownerSession); got.Code != 200 {
		t.Fatalf("revoked runtime limits could not be inspected: %d", got.Code)
	}
	if got := put(ownerKey.ID, ownerSession, ownerCSRF); got.Code != 403 {
		t.Fatalf("revoked key allowed runtime mutation: %d", got.Code)
	}
}

func TestKeyRuntimeLimitsHTTPAllProtocolsRejectWithoutFinancialFacts(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolMessages, provider.ProtocolEmbeddings, provider.ProtocolRerank, provider.ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{gatewaySuccess(t)}, nil, 8, protocol)
			keyRuntimePut(t, f, "1", 1, 2, 1)
			principal, err := f.server.Identity.APIKey(context.Background(), f.token)
			if err != nil {
				t.Fatal(err)
			}
			release, err := f.server.acquireKeyRuntime(context.Background(), principal)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			engine := f.server.Engine()
			path, body := keyPolicyProtocolInput(t, protocol)
			headers := http.Header{"Authorization": []string{"Bearer " + f.token}}
			got := keyPolicyHTTP(t, engine, "POST", path, body, "", "", "", "127.0.0.1:1234", headers)
			if got.Code != 429 || protocol != provider.ProtocolMessages && !strings.Contains(got.Body.String(), "concurrency_limited") {
				t.Fatalf("concurrency-limited inference status=%d", got.Code)
			}
			admissionAssertNoFacts(t, f)
			release()
			// A concurrency rejection must not consume the remaining token.
			second, err := f.server.acquireKeyRuntime(context.Background(), principal)
			if err != nil {
				t.Fatalf("concurrency rejection consumed a key token: %v", err)
			}
			second()
			got = keyPolicyHTTP(t, engine, "POST", path, body, "", "", "", "127.0.0.1:1234", headers)
			if got.Code != 429 || protocol != provider.ProtocolMessages && !strings.Contains(got.Body.String(), "rate_limited") {
				t.Fatalf("rate-limited inference status=%d", got.Code)
			}
			admissionAssertNoFacts(t, f)
			if models := keyPolicyHTTP(t, engine, "GET", "/v1/models", "", "", "", "", "127.0.0.1:1234", headers); models.Code != 200 {
				t.Fatalf("runtime inference limits unexpectedly consumed model-directory access: %d", models.Code)
			}
			retrievalResourcesReleased(t, f)
		})
	}
}

func TestKeyRuntimeLimitsHTTPRechecksAfterDatabaseKeyLock(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	principal, err := f.server.Identity.APIKey(ctx, f.token)
	if err != nil {
		t.Fatal(err)
	}
	release, err := f.server.acquireKeyRuntime(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var blockerPID int
	if err = blocker.QueryRow(ctx, "SELECT pg_backend_pid() FROM api_keys WHERE id=$1 FOR UPDATE", f.key).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	result := admissionStartHTTP(f.http.Client(), admissionHTTPRequest(t, f, ctx, false))
	admissionAwait(t, "runtime admission waiting for the key SHARE lock", func() bool {
		var blocked bool
		if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", blockerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		return blocked
	})
	admissionAssertNoFacts(t, f)
	if _, err = blocker.Exec(ctx, "UPDATE api_keys SET concurrency_limit=1 WHERE id=$1", f.key); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := admissionAwaitHTTP(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.response.StatusCode != 429 {
		body, _ := io.ReadAll(got.response.Body)
		t.Fatalf("post-lock runtime admission used old concurrency: status=%d body=%s", got.response.StatusCode, body)
	}
	admissionAssertNoFacts(t, f)
	release()
	retrievalResourcesReleased(t, f)
}

func TestKeyRuntimeLimitsHTTPRuntimeCapacityFailsClosedBeforeFinancialFacts(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	f.server.mu.Lock()
	for i := 0; i < 4096; i++ {
		f.server.keyRuntime[fmt.Sprintf("fixture-runtime-%d", i)] = &keyRuntimeState{}
	}
	f.server.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response := f.request(t, ctx, false, false)
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 503 || !strings.Contains(string(body), "limiter_unavailable") {
		t.Fatalf("runtime capacity exhaustion did not fail closed: status=%d body=%s err=%v", response.StatusCode, body, err)
	}
	admissionAssertNoFacts(t, f)
	retrievalResourcesReleased(t, f)
}

func TestKeyRuntimeLimitsHTTPAdmittedQueueContinuesAfterTightening(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	routes := admissionRouteResources(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	principal, err := f.server.Identity.APIKey(ctx, f.token)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.server.acquireKeyRuntime(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	owner, err := f.server.admission.acquire(ctx, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	result := admissionStartHTTP(f.http.Client(), admissionHTTPRequest(t, f, ctx, false))
	admissionAwait(t, "HTTP has a runtime permit and is waiting before Reserve", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 2 && admissionResources(f, routes) == (admissionResourceState{global: 2, tenant: 2, key: 2})
	})
	admissionAssertNoFacts(t, f)
	policy := keyRuntimePut(t, f, "1", 1, 1, 1)
	owner()
	got := admissionAwaitHTTP(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	body, err := io.ReadAll(got.response.Body)
	if err != nil || got.response.StatusCode != 200 {
		t.Fatalf("previously admitted queue was revoked by new limits: status=%d body=%s err=%v", got.response.StatusCode, body, err)
	}
	f.assertSettled(t, got.response.Header.Get("X-Request-ID"), 1)
	first()
	admissionAwait(t, "old runtime grants all released after tightening", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
	})
	var revision string
	if err := f.pool.QueryRow(ctx, "SELECT limit_revision::text FROM api_keys WHERE id=$1", f.key).Scan(&revision); err != nil || revision != policy.LimitRevision {
		t.Fatalf("old request changed the new limit revision: revision=%s err=%v", revision, err)
	}
}

func TestKeyRuntimeLimitsHTTPInFlightStreamContinuesAfterTightening(t *testing.T) {
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
		t.Fatalf("stream did not start: status=%d line=%s err=%v", first.response.StatusCode, line, err)
	}
	policy := keyRuntimePut(t, f, "1", 1, 1, 1)
	second := f.request(t, ctx, false, false)
	denied, err := io.ReadAll(second.Body)
	if err != nil || second.StatusCode != 429 || f.posts.Load() != 1 {
		t.Fatalf("tightened concurrency did not reject successor: status=%d posts=%d body=%s err=%v", second.StatusCode, f.posts.Load(), denied, err)
	}
	once.Do(func() { close(finish) })
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "[DONE]") {
		t.Fatalf("in-flight stream did not complete after tightening: %s err=%v", rest, err)
	}
	admissionAwait(t, "tightened stream returning all runtime and routing resources", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
	})
	f.assertSettled(t, first.response.Header.Get("X-Request-ID"), 1)
	var requests, settlements int
	var revision string
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM settlements),limit_revision::text FROM api_keys WHERE id=$1", f.key).Scan(&requests, &settlements, &revision); err != nil || requests != 1 || settlements != 1 || revision != policy.LimitRevision || f.posts.Load() != 1 {
		t.Fatalf("old stream modified new runtime policy or created extra facts: requests=%d settlements=%d revision=%s posts=%d err=%v", requests, settlements, revision, f.posts.Load(), err)
	}
}
