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

func keyConfigurationView(t *testing.T, response *httptest.ResponseRecorder) identity.KeyConfigurationView {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("key configuration response status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data identity.KeyConfigurationView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Data.ID == "" || envelope.Data.ConfigurationRevision == "" || envelope.Data.Spent == "" || envelope.Data.Held == "" {
		t.Fatalf("key configuration response omitted safe fields: %v", err)
	}
	return envelope.Data
}

func keyConfigurationBody(v identity.KeyConfigurationView) map[string]any {
	return map[string]any{"expected_revision": v.ConfigurationRevision, "name": v.Name, "scopes": v.Scopes, "allowed_regions": v.AllowedRegions, "lifetime_limit": v.LifetimeLimit, "expires_at": v.ExpiresAt}
}

func keyConfigurationJSON(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func keyConfigurationGet(t *testing.T, f *gatewayFixture) identity.KeyConfigurationView {
	t.Helper()
	session, _ := keyPolicyOwner(t, f)
	return keyConfigurationView(t, keyPolicyHTTP(t, f.server.Engine(), "GET", "/api/keys/"+f.key+"/configuration", "", session, "", "", "", nil))
}

func keyConfigurationPut(t *testing.T, f *gatewayFixture, body map[string]any) identity.KeyConfigurationView {
	t.Helper()
	session, csrf := keyPolicyOwner(t, f)
	return keyConfigurationView(t, keyPolicyHTTP(t, f.server.Engine(), "PUT", "/api/keys/"+f.key+"/configuration", keyConfigurationJSON(t, body), session, csrf, f.server.Config.Origin, "", nil))
}

func TestKeyConfigurationHTTPCreateCASProjectionAndBudgetErrors(t *testing.T) {
	s, pool := newAuthFixture(t)
	owner, _ := authUser(t, s, pool, "none")
	tenant := uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'configuration')", tenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", tenant, owner)
	session, csrf := authSession(t, s, pool, owner, tenant, false, true)
	engine := s.Engine()
	call := func(method, path, raw string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, method, path, raw, session, csrf, s.Config.Origin, "", nil)
	}
	created := call("POST", "/api/keys", `{"name":"initial app","scopes":["*"],"allowed_regions":["global"]}`)
	view := keyConfigurationView(t, created)
	if view.ConfigurationRevision != "1" || !reflect.DeepEqual(view.AllowedRegions, []string{"global"}) || view.LifetimeLimit != nil || view.ExpiresAt != nil || view.Spent != "0" || view.Held != "0" {
		t.Fatalf("creation lost configuration defaults: %+v", view)
	}
	var secret struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if json.Unmarshal(created.Body.Bytes(), &secret) != nil || !strings.HasPrefix(secret.Data.Key, "mg_") {
		t.Fatal("creation lost its one-time key")
	}
	path := "/api/keys/" + view.ID + "/configuration"
	body := keyConfigurationBody(view)
	first, second := "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second).Add(123456789 * time.Nanosecond)
	body["name"], body["scopes"], body["allowed_regions"] = "  updated app  ", []string{second, first, second}, []string{"global", " eu-west ", "global"}
	body["lifetime_limit"], body["expires_at"] = "9223372036854775807", expiry.In(time.FixedZone("test", 8*3600)).Format(time.RFC3339Nano)
	updated := keyConfigurationView(t, call("PUT", path, keyConfigurationJSON(t, body)))
	if updated.Name != "updated app" || updated.ConfigurationRevision != "2" || !reflect.DeepEqual(updated.Scopes, []string{first, second}) || !reflect.DeepEqual(updated.AllowedRegions, []string{"eu-west", "global"}) || updated.LifetimeLimit == nil || *updated.LifetimeLimit != "9223372036854775807" || updated.ExpiresAt == nil || !updated.ExpiresAt.Equal(expiry.Truncate(time.Microsecond)) || updated.ExpiresAt.Location() != time.UTC {
		t.Fatalf("configuration update lost canonical values or precision: %+v", updated)
	}
	read := call("GET", path, "")
	if got := keyConfigurationView(t, read); !reflect.DeepEqual(got, updated) {
		t.Fatal("configuration GET did not return committed values")
	}
	var safe struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(read.Body.Bytes(), &safe) != nil || len(safe.Data) != 9 || read.Header().Get("Cache-Control") != "no-store" || read.Header().Get("X-Content-Type-Options") != "nosniff" || read.Header().Get("X-Request-ID") == "" || strings.Contains(read.Body.String(), secret.Data.Key) || strings.Contains(read.Body.String(), "digest") || strings.Contains(read.Body.String(), "prefix") {
		t.Fatal("configuration GET exposed extra metadata or omitted security headers")
	}
	if got := call("PUT", path, keyConfigurationJSON(t, body)); got.Code != 409 || !strings.Contains(got.Body.String(), `"code":"conflict"`) {
		t.Fatal("old configuration CAS did not return conflict", got.Code, got.Body.String())
	}
	if got := keyConfigurationView(t, call("PUT", path, keyConfigurationJSON(t, keyConfigurationBody(updated)))); got.ConfigurationRevision != "2" {
		t.Fatal("configuration no-op advanced revision")
	}
	if got := call("PUT", "/api/keys/"+view.ID+"/access-policy", `{"expected_revision":"1","allowed_client_cidrs":["192.0.2.1"]}`); got.Code != 200 {
		t.Fatal("source update failed", got.Code)
	}
	if got := call("PUT", "/api/keys/"+view.ID+"/runtime-limits", `{"expected_revision":"1","rpm_limit":5,"burst_limit":6,"concurrency_limit":2}`); got.Code != 200 {
		t.Fatal("runtime update failed", got.Code)
	}
	authExec(t, pool, "UPDATE api_keys SET spent=4,held=6 WHERE id=$1", view.ID)
	current := keyConfigurationView(t, call("GET", path, ""))
	if current.ConfigurationRevision != "2" || current.Spent != "4" || current.Held != "6" {
		t.Fatal("consumption or unrelated policy changed the configuration version", current)
	}
	body = keyConfigurationBody(current)
	body["lifetime_limit"] = "9"
	if got := call("PUT", path, keyConfigurationJSON(t, body)); got.Code != 409 || !strings.Contains(got.Body.String(), `"code":"key_budget_below_usage"`) {
		t.Fatal("below-usage budget was not distinct from CAS conflict", got.Code, got.Body.String())
	}
	body["expected_revision"] = "1"
	if got := call("PUT", path, keyConfigurationJSON(t, body)); got.Code != 409 || !strings.Contains(got.Body.String(), `"code":"conflict"`) {
		t.Fatal("stale CAS lost precedence over below-usage budget", got.Code, got.Body.String())
	}
	body["expected_revision"], body["lifetime_limit"], body["expires_at"] = "2", "10", nil
	updated = keyConfigurationView(t, call("PUT", path, keyConfigurationJSON(t, body)))
	if updated.ConfigurationRevision != "3" || updated.ExpiresAt != nil || updated.LifetimeLimit == nil || *updated.LifetimeLimit != "10" || updated.Spent != "4" || updated.Held != "6" {
		t.Fatal("exact usage floor or explicit expiry removal failed", updated)
	}
	body = keyConfigurationBody(updated)
	body["lifetime_limit"] = nil
	updated = keyConfigurationView(t, call("PUT", path, keyConfigurationJSON(t, body)))
	if updated.ConfigurationRevision != "4" || updated.LifetimeLimit != nil {
		t.Fatal("explicit null did not remove cumulative limit", updated)
	}
	listed := call("GET", "/api/keys", "")
	var list struct {
		Data []identity.KeyConfigurationView `json:"data"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &list) != nil || len(list.Data) != 1 || !reflect.DeepEqual(list.Data[0], updated) || strings.Contains(listed.Body.String(), secret.Data.Key) {
		t.Fatal("key list lost current configuration or leaked a secret")
	}
	var digest, sourceRevision, runtimeRevision string
	var cidrs []string
	var rpm, burst, concurrency, audits int
	if err := pool.QueryRow(context.Background(), `SELECT digest,allowed_client_cidrs,access_policy_revision::text,limit_revision::text,rpm_limit,burst_limit,concurrency_limit,(SELECT count(*) FROM audit_events WHERE action='key.configuration.update') FROM api_keys WHERE id=$1`, view.ID).Scan(&digest, &cidrs, &sourceRevision, &runtimeRevision, &rpm, &burst, &concurrency, &audits); err != nil || digest != s.Config.Secrets.Digest(secret.Data.Key) || !reflect.DeepEqual(cidrs, []string{"192.0.2.1/32"}) || sourceRevision != "2" || runtimeRevision != "2" || rpm != 5 || burst != 6 || concurrency != 2 || audits != 3 {
		t.Fatalf("configuration write changed secrets/other policies or audited failures/no-op: audits=%d err=%v", audits, err)
	}
}

func TestKeyConfigurationHTTPStrictInputAndLegacyRoundTrip(t *testing.T) {
	s, pool := newAuthFixture(t)
	owner, _ := authUser(t, s, pool, "none")
	tenant := uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'strict-configuration')", tenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", tenant, owner)
	session, csrf := authSession(t, s, pool, owner, tenant, false, true)
	engine := s.Engine()
	call := func(method, path, raw string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, method, path, raw, session, csrf, s.Config.Origin, "", nil)
	}
	view := keyConfigurationView(t, call("POST", "/api/keys", `{"name":"strict","scopes":["*"]}`))
	path := "/api/keys/" + view.ID + "/configuration"
	valid := keyConfigurationBody(view)
	invalid := []string{`{}`, `null`, `[]`}
	for field := range valid {
		body := keyConfigurationBody(view)
		delete(body, field)
		invalid = append(invalid, keyConfigurationJSON(t, body))
	}
	for _, tc := range []struct {
		field string
		value any
	}{
		{"expected_revision", nil}, {"expected_revision", 1}, {"expected_revision", "01"}, {"expected_revision", "0"}, {"expected_revision", "9223372036854775808"},
		{"name", nil}, {"name", "  "}, {"name", "private-marker\nname"}, {"name", strings.Repeat("x", 101)},
		{"scopes", nil}, {"scopes", []string{}}, {"scopes", []string{"private-marker"}}, {"scopes", []string{"*", uuid.NewString()}}, {"scopes", []string{strings.ToUpper(uuid.NewString())}},
		{"allowed_regions", nil}, {"allowed_regions", []string{}}, {"allowed_regions", []string{"private-marker\nregion"}}, {"allowed_regions", []string{strings.Repeat("x", 81)}}, {"allowed_regions", []string{"*", "global"}},
		{"lifetime_limit", 1}, {"lifetime_limit", "01"}, {"lifetime_limit", "-1"}, {"lifetime_limit", "9223372036854775808"}, {"lifetime_limit", ""},
		{"expires_at", ""}, {"expires_at", "private-marker"}, {"expires_at", "2000-01-01T00:00:00Z"}, {"expires_at", 1},
		{"expires_at", "9999-12-31T23:59:59-23:59"}, {"expires_at", "0000-01-01T00:00:00+23:59"},
		{"expires_at", "2030-01-01T00:00:00+24:00"}, {"expires_at", "2030-01-01T00:00:00,123Z"},
		{"expires_at", "2030-01-01T0:00:00Z"}, {"expires_at", "2030-01-01T00:00:00+00:60"},
	} {
		body := keyConfigurationBody(view)
		body[tc.field] = tc.value
		invalid = append(invalid, keyConfigurationJSON(t, body))
	}
	canonical := keyConfigurationJSON(t, valid)
	invalid = append(invalid, canonical+` {}`, `{"expected_revision":"1",`+strings.TrimPrefix(canonical, "{"), `{"NAME":"private-marker",`+strings.TrimPrefix(canonical, "{"), `{"secret":"private-marker",`+strings.TrimPrefix(canonical, "{"))
	longBody := keyConfigurationBody(view)
	longBody["name"] = strings.Repeat("private-marker", 1400)
	invalid = append(invalid, keyConfigurationJSON(t, longBody))
	for _, raw := range invalid {
		got := call("PUT", path, raw)
		if got.Code != 400 || strings.Contains(got.Body.String(), "private-marker") {
			t.Fatalf("invalid configuration accepted or reflected input: status=%d body=%s", got.Code, got.Body.String())
		}
	}
	for _, raw := range []string{
		`{"name":"","scopes":["*"]}`, `{"name":"private-marker","scopes":["*"],"lifetime_limit":"-1"}`,
		`{"name":"private-marker","scopes":["*"],"lifetime_limit":"01"}`, `{"name":"private-marker","scopes":["*"],"expires_at":"2000-01-01T00:00:00Z"}`,
		`{"name":"private-marker","scopes":["*"],"expires_at":"9999-12-31T23:59:59-23:59"}`,
		`{"name":"private-marker","scopes":["*"],"expires_at":"0000-01-01T00:00:00+23:59"}`,
		`{"name":"private-marker","scopes":["*"],"expires_at":"2030-01-01T00:00:00+24:00"}`,
		`{"name":"private-marker","scopes":["*"],"expires_at":"2030-01-01T00:00:00,123Z"}`,
		`{"name":"private-marker","scopes":["*"],"expires_at":"2030-01-01T0:00:00Z"}`,
		`{"name":"private-marker","scopes":["*"],"expires_at":"2030-01-01T00:00:00+00:60"}`,
	} {
		got := call("POST", "/api/keys", raw)
		if got.Code != 400 || strings.Contains(got.Body.String(), "private-marker") {
			t.Fatal("invalid creation basic field was not a safe client error", got.Code, got.Body.String())
		}
	}
	if got := keyConfigurationView(t, call("GET", path, "")); !reflect.DeepEqual(got, view) {
		t.Fatal("invalid configuration changed the committed tuple or revision")
	}
	var keyCount, auditCount int
	if err := pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM api_keys),(SELECT count(*) FROM audit_events WHERE action='key.configuration.update')").Scan(&keyCount, &auditCount); err != nil || keyCount != 1 || auditCount != 0 {
		t.Fatal("invalid configuration or creation persisted a Key or audit", keyCount, auditCount, err)
	}
	for _, id := range []string{"private-marker", strings.ToUpper(view.ID), "urn:uuid:" + view.ID} {
		if got := call("GET", "/api/keys/"+id+"/configuration", ""); got.Code != 400 || strings.Contains(got.Body.String(), "private-marker") {
			t.Fatal("noncanonical configuration path was accepted or reflected", got.Code)
		}
	}
	// encoding/json otherwise decodes a null string-array element as "".
	// Even when a historical array contains "", [null] is not the same
	// input and must not bypass validation through legacy preservation.
	authExec(t, pool, "UPDATE api_keys SET scopes=$2,allowed_regions=$2 WHERE id=$1", view.ID, []string{""})
	emptyLegacy := keyConfigurationView(t, call("GET", path, ""))
	for _, field := range []string{"scopes", "allowed_regions"} {
		body := keyConfigurationBody(emptyLegacy)
		body[field] = []any{nil}
		if got := call("PUT", path, keyConfigurationJSON(t, body)); got.Code != 400 {
			t.Fatalf("legacy %s accepted a null array element: status=%d", field, got.Code)
		}
	}
	if got := keyConfigurationView(t, call("PUT", path, keyConfigurationJSON(t, keyConfigurationBody(emptyLegacy)))); !reflect.DeepEqual(got, emptyLegacy) {
		t.Fatal("literal legacy empty-string arrays did not round-trip unchanged")
	}
	// The transport must preserve legacy arrays and expired microseconds when
	// another property changes; opening/saving is not an automatic migration.
	legacyExpiry := time.Date(2020, 1, 2, 3, 4, 5, 123456000, time.UTC)
	authExec(t, pool, "UPDATE api_keys SET name=$2,scopes=$3,allowed_regions=$4,expires_at=$5 WHERE id=$1", view.ID, "old\nname", []string{"legacy-model", "legacy-model"}, []string{}, legacyExpiry)
	legacy := keyConfigurationView(t, call("GET", path, ""))
	if legacy.Name != "old\nname" || !reflect.DeepEqual(legacy.Scopes, []string{"legacy-model", "legacy-model"}) || len(legacy.AllowedRegions) != 0 || legacy.ExpiresAt == nil || !legacy.ExpiresAt.Equal(legacyExpiry) {
		t.Fatal("GET cleaned historical configuration")
	}
	body := keyConfigurationBody(legacy)
	body["name"] = "updated legacy"
	updated := keyConfigurationView(t, call("PUT", path, keyConfigurationJSON(t, body)))
	if updated.Name != "updated legacy" || !reflect.DeepEqual(updated.Scopes, legacy.Scopes) || !reflect.DeepEqual(updated.AllowedRegions, legacy.AllowedRegions) || updated.ExpiresAt == nil || !updated.ExpiresAt.Equal(legacyExpiry) {
		t.Fatal("editing a name changed legacy arrays or expired precision")
	}
	body = keyConfigurationBody(updated)
	body["expires_at"] = nil
	if got := keyConfigurationView(t, call("PUT", path, keyConfigurationJSON(t, body))); got.ExpiresAt != nil {
		t.Fatal("expired key could not remove expiry explicitly")
	}
}

func TestKeyConfigurationHTTPCurrentRolesAndSecurityGates(t *testing.T) {
	s, pool := newAuthFixture(t)
	owner, _ := authUser(t, s, pool, "none")
	member, _ := authUser(t, s, pool, "none")
	viewer, _ := authUser(t, s, pool, "none")
	foreignOwner, _ := authUser(t, s, pool, "none")
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'configuration-A'),($2,'configuration-B')", tenant, foreignTenant)
	for _, row := range []struct{ tenant, user, role string }{{tenant, owner, "owner"}, {tenant, member, "member"}, {tenant, viewer, "billing_viewer"}, {foreignTenant, foreignOwner, "owner"}} {
		authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,$3,true)", row.tenant, row.user, row.role)
	}
	ownerSession, ownerCSRF := authSession(t, s, pool, owner, tenant, false, true)
	memberSession, memberCSRF := authSession(t, s, pool, member, tenant, false, true)
	viewerSession, viewerCSRF := authSession(t, s, pool, viewer, tenant, false, true)
	foreignSession, foreignCSRF := authSession(t, s, pool, foreignOwner, foreignTenant, false, true)
	engine := s.Engine()
	create := func(session, csrf string) identity.KeyConfigurationView {
		return keyConfigurationView(t, keyPolicyHTTP(t, engine, "POST", "/api/keys", `{"name":"app","scopes":["*"]}`, session, csrf, s.Config.Origin, "", nil))
	}
	ownerKey, memberKey, viewerKey := create(ownerSession, ownerCSRF), create(memberSession, memberCSRF), create(ownerSession, ownerCSRF)
	authExec(t, pool, "UPDATE api_keys SET owner_id=$2 WHERE id=$1", viewerKey.ID, viewer)
	put := func(key identity.KeyConfigurationView, session, csrf string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, "PUT", "/api/keys/"+key.ID+"/configuration", keyConfigurationJSON(t, keyConfigurationBody(key)), session, csrf, s.Config.Origin, "", nil)
	}
	get := func(id, session string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, "GET", "/api/keys/"+id+"/configuration", "", session, "", "", "", nil)
	}
	missing := ownerKey
	missing.ID = uuid.NewString()
	for _, tc := range []struct {
		name, session, csrf string
		key                 identity.KeyConfigurationView
		read, write         int
	}{
		{"member_other", memberSession, memberCSRF, ownerKey, 403, 403},
		{"viewer_other", viewerSession, viewerCSRF, ownerKey, 403, 403},
		{"viewer_own", viewerSession, viewerCSRF, viewerKey, 200, 403},
		{"foreign", foreignSession, foreignCSRF, ownerKey, 403, 403},
		{"missing", ownerSession, ownerCSRF, missing, 403, 403},
		{"unauthenticated", "", "", ownerKey, 401, 401},
	} {
		if got := get(tc.key.ID, tc.session); got.Code != tc.read {
			t.Fatalf("%s configuration GET status=%d", tc.name, got.Code)
		}
		if got := put(tc.key, tc.session, tc.csrf); got.Code != tc.write {
			t.Fatalf("%s configuration PUT status=%d", tc.name, got.Code)
		}
	}
	memberBody := keyConfigurationBody(memberKey)
	memberBody["name"] = "member app"
	memberKey = keyConfigurationView(t, keyPolicyHTTP(t, engine, "PUT", "/api/keys/"+memberKey.ID+"/configuration", keyConfigurationJSON(t, memberBody), memberSession, memberCSRF, s.Config.Origin, "", nil))
	if got := put(memberKey, ownerSession, ownerCSRF); got.Code != 200 {
		t.Fatal("owner could not manage a member's configuration", got.Code)
	}
	staleSession, staleCSRF := authSession(t, s, pool, owner, tenant, false, false)
	if got := get(ownerKey.ID, staleSession); got.Code != 200 {
		t.Fatal("read-only configuration required recent reauthentication", got.Code)
	}
	for _, tc := range []struct{ name, session, csrf string }{{"csrf", ownerSession, "wrong"}, {"reauth", staleSession, staleCSRF}} {
		if got := put(ownerKey, tc.session, tc.csrf); got.Code != 403 {
			t.Fatalf("%s configuration gate status=%d", tc.name, got.Code)
		}
	}
	if got := keyPolicyHTTP(t, engine, "PUT", "/api/keys/"+ownerKey.ID+"/configuration", keyConfigurationJSON(t, keyConfigurationBody(ownerKey)), ownerSession, ownerCSRF, "https://foreign.test", "", nil); got.Code != 403 {
		t.Fatal("foreign Origin bypassed the configuration gate", got.Code)
	}
	authExec(t, pool, "UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2", tenant, owner)
	if got := put(ownerKey, ownerSession, ownerCSRF); got.Code != 403 {
		t.Fatal("configuration edit used stale owner role", got.Code)
	}
	if got := get(memberKey.ID, ownerSession); got.Code != 403 {
		t.Fatal("configuration GET used stale owner visibility", got.Code)
	}
	authExec(t, pool, "UPDATE memberships SET role='owner' WHERE tenant_id=$1 AND user_id=$2", tenant, owner)
	s.Config.RecoveryMode = true
	if got := get(ownerKey.ID, staleSession); got.Code != 200 {
		t.Fatal("recovery mode blocked configuration inspection", got.Code)
	}
	if got := put(ownerKey, ownerSession, ownerCSRF); got.Code != 503 {
		t.Fatal("recovery mode allowed configuration edit", got.Code)
	}
	s.Config.RecoveryMode = false
	s.Ready(false)
	if got := get(ownerKey.ID, ownerSession); got.Code != 503 {
		t.Fatal("unready instance allowed configuration GET", got.Code)
	}
	s.Ready(true)
	s.Guard = func(context.Context) error { return errors.New("fixture guard unavailable") }
	if got := put(ownerKey, ownerSession, ownerCSRF); got.Code != 503 {
		t.Fatal("failed guard allowed configuration edit", got.Code)
	}
	s.Guard = nil
	s.Ready(true)
	authExec(t, pool, "UPDATE users SET mfa_enabled=true WHERE id=$1", owner)
	if got := get(ownerKey.ID, ownerSession); got.Code != 403 {
		t.Fatal("enabled MFA was bypassed on configuration GET", got.Code)
	}
	authExec(t, pool, "UPDATE users SET mfa_enabled=false WHERE id=$1", owner)
	if got := put(ownerKey, ownerSession, ownerCSRF); got.Code != 200 {
		t.Fatal("optional unbound MFA blocked configuration edit", got.Code)
	}
	authExec(t, pool, "UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1", ownerKey.ID)
	if got := get(ownerKey.ID, ownerSession); got.Code != 200 {
		t.Fatal("revoked configuration could not be inspected", got.Code)
	}
	if got := put(ownerKey, ownerSession, ownerCSRF); got.Code != 403 {
		t.Fatal("revoked key allowed configuration edit", got.Code)
	}
}

func TestKeyConfigurationHTTPFullScopeBoundAndBodyLimit(t *testing.T) {
	s, pool := newAuthFixture(t)
	owner, _ := authUser(t, s, pool, "none")
	tenant := uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'large-key-configuration')", tenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", tenant, owner)
	session, csrf := authSession(t, s, pool, owner, tenant, false, true)
	engine := s.Engine()
	call := func(method, path, raw string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, method, path, raw, session, csrf, s.Config.Origin, "", nil)
	}
	scopes := make([]string, 1000)
	for i := range scopes {
		scopes[i] = fmt.Sprintf("00000000-0000-4000-8000-%012x", i+1)
	}
	createBody := keyConfigurationJSON(t, map[string]any{"name": "large model scope", "scopes": scopes})
	if len(createBody) <= 16384 || len(createBody) > 65536 {
		t.Fatal("scope boundary fixture did not exercise the larger request cap")
	}
	created := call("POST", "/api/keys", createBody)
	if created.Code != 200 {
		t.Fatal("valid 1000-scope creation was rejected", created.Code)
	}
	view := keyConfigurationView(t, created)
	if !reflect.DeepEqual(view.Scopes, scopes) {
		t.Fatal("large creation lost model scopes")
	}
	body := keyConfigurationBody(view)
	reversed := append([]string(nil), scopes...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	body["name"], body["scopes"] = "large model scope updated", reversed
	updateBody := keyConfigurationJSON(t, body)
	path := "/api/keys/" + view.ID + "/configuration"
	response := call("PUT", path, updateBody)
	if response.Code != 200 {
		t.Fatal("valid 1000-scope configuration was rejected", response.Code)
	}
	updated := keyConfigurationView(t, response)
	if updated.ConfigurationRevision != "2" || !reflect.DeepEqual(updated.Scopes, scopes) {
		t.Fatal("large configuration did not retain all canonical scopes")
	}
	for _, tc := range []struct{ method, path, raw string }{{"POST", "/api/keys", createBody}, {"PUT", path, updateBody}} {
		if got := call(tc.method, tc.path, strings.Repeat(" ", 65537)+tc.raw); got.Code != 400 || strings.Contains(got.Body.String(), "large model scope") {
			t.Fatal("request above 64 KiB was accepted or reflected", tc.method, got.Code)
		}
	}
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM api_keys").Scan(&count); err != nil || count != 1 {
		t.Fatal("oversized creation persisted a Key", count, err)
	}
}

func TestKeyConfigurationHTTPAllProtocolsRejectWithoutFinancialFacts(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolMessages, provider.ProtocolEmbeddings, provider.ProtocolRerank, provider.ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{gatewaySuccess(t)}, nil, 8, protocol)
			path, raw := keyPolicyProtocolInput(t, protocol)
			view := keyConfigurationGet(t, f)
			body := keyConfigurationBody(view)
			body["scopes"] = []string{uuid.NewString()}
			view = keyConfigurationPut(t, f, body)
			call := func() *httptest.ResponseRecorder {
				return keyPolicyHTTP(t, f.server.Engine(), "POST", path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}, "Idempotency-Key": []string{"configuration-denied"}})
			}
			if got := call(); got.Code != 403 {
				t.Fatal("scope tightening did not deny protocol", protocol, got.Code, got.Body.String())
			}
			admissionAssertNoFacts(t, f)
			body = keyConfigurationBody(view)
			body["scopes"], body["lifetime_limit"] = []string{f.model}, "0"
			view = keyConfigurationPut(t, f, body)
			if got := call(); got.Code != 402 || protocol != provider.ProtocolMessages && !strings.Contains(got.Body.String(), "key_budget_exceeded") {
				t.Fatal("zero budget did not deny protocol", protocol, got.Code, got.Body.String())
			}
			admissionAssertNoFacts(t, f)
			// Existing expired credentials are inspectable, but their API token
			// cannot create financial facts. This updates only the test schema.
			authExec(t, f.pool, "UPDATE api_keys SET lifetime_limit=NULL,expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.key)
			if got := call(); got.Code != 401 {
				t.Fatal("expired key did not deny protocol", protocol, got.Code, got.Body.String())
			}
			admissionAssertNoFacts(t, f)
			var idempotency, finalizations int
			if err := f.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM request_idempotency),(SELECT count(*) FROM finalization_records)").Scan(&idempotency, &finalizations); err != nil || idempotency != 0 || finalizations != 0 {
				t.Fatal("configuration rejection left idempotency or finalization facts", idempotency, finalizations, err)
			}
			retrievalResourcesReleased(t, f)
		})
	}
}

func TestKeyConfigurationHTTPQueuedRequestRechecksBeforeReserve(t *testing.T) {
	for _, field := range []string{"scopes", "lifetime_limit"} {
		t.Run(field, func(t *testing.T) {
			f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
			routes := admissionRouteResources(t, f)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			owner, err := f.server.admission.acquire(ctx, f.key)
			if err != nil {
				t.Fatal(err)
			}
			defer owner()
			result := admissionStartHTTP(f.http.Client(), admissionHTTPRequest(t, f, ctx, false))
			admissionAwait(t, "HTTP authenticated and queued before configuration update", func() bool {
				return admissionQueueDepth(&f.server.admission, f.key) == 2
			})
			admissionAssertNoFacts(t, f)
			body := keyConfigurationBody(keyConfigurationGet(t, f))
			status := 403
			if field == "scopes" {
				body[field] = []string{uuid.NewString()}
			} else {
				body[field], status = "0", 402
			}
			keyConfigurationPut(t, f, body)
			owner()
			got := admissionAwaitHTTP(t, result)
			if got.err != nil {
				t.Fatal(got.err)
			}
			raw, err := io.ReadAll(got.response.Body)
			if err != nil || got.response.StatusCode != status {
				t.Fatalf("queued request used stale %s: status=%d body=%s err=%v", field, got.response.StatusCode, raw, err)
			}
			admissionAwait(t, "configuration rejection returning all resources", func() bool {
				return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
			})
			admissionAssertNoFacts(t, f)
		})
	}
}

func TestKeyConfigurationHTTPAlreadySentStreamSettlesAfterTightening(t *testing.T) {
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
		t.Fatalf("stream did not cross sending boundary: status=%d line=%s err=%v", first.response.StatusCode, line, err)
	}
	view := keyConfigurationGet(t, f)
	if view.Held != "140" || view.Spent != "0" {
		t.Fatal("stream did not expose its frozen amount", view)
	}
	body := keyConfigurationBody(view)
	body["scopes"], body["allowed_regions"], body["lifetime_limit"] = []string{uuid.NewString()}, []string{"blocked-region"}, "140"
	updated := keyConfigurationPut(t, f, body)
	second := f.request(t, ctx, false, false)
	denied, err := io.ReadAll(second.Body)
	if err != nil || second.StatusCode != 403 || f.posts.Load() != 1 {
		t.Fatalf("configuration tightening did not reject successor: status=%d posts=%d body=%s err=%v", second.StatusCode, f.posts.Load(), denied, err)
	}
	once.Do(func() { close(finish) })
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "[DONE]") {
		t.Fatalf("in-flight stream did not finish after configuration tightening: %s err=%v", rest, err)
	}
	admissionAwait(t, "old stream returning all configuration-independent resources", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
	})
	f.assertSettled(t, first.response.Header.Get("X-Request-ID"), 1)
	current := keyConfigurationGet(t, f)
	if current.ConfigurationRevision != updated.ConfigurationRevision || current.Spent != "20" || current.Held != "0" || !reflect.DeepEqual(current.Scopes, updated.Scopes) || !reflect.DeepEqual(current.AllowedRegions, updated.AllowedRegions) {
		t.Fatal("in-flight completion overwrote current configuration or retained funds", current)
	}
	var requests, attempts, finalizations, settlements int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM finalization_records),(SELECT count(*) FROM settlements)").Scan(&requests, &attempts, &finalizations, &settlements); err != nil || requests != 1 || attempts != 1 || finalizations != 1 || settlements != 1 || f.posts.Load() != 1 {
		t.Fatal("configuration tightening created duplicate delivery or financial facts", requests, attempts, finalizations, settlements, err)
	}
}
