package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func publicHTTPModel(t *testing.T, s *Server, actor, name string) string {
	t.Helper()
	id, err := s.Catalog.CreateModel(context.Background(), actor, catalog.ModelInput{PublicName: name, ContextLimit: 100, InputUpperBound: 80, OutputLimit: 20, DefaultOutputLimit: 10, SupportsTools: true})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func publicHTTPModelWithMetadata(t *testing.T, pool *pgxpool.Pool, name string, metadata map[string]any) string {
	t.Helper()
	id, version := uuid.NewString(), uuid.NewString()
	profile := map[string]any{"version": version, "model": name, "protocol": "openai_chat", "context_limit": 100, "input_token_upper_bound": 80, "output_limit": 20, "default_output_limit": 10, "supports_tools": true, "output_limit_field": "max_tokens"}
	for key, value := range metadata {
		profile[key] = value
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	authExec(t, pool, `SELECT meter_insert_text_model($1,$2,$3::jsonb,$4)`, id, name, body, version)
	return id
}

func publicJSONKeys(t *testing.T, raw json.RawMessage, expected ...string) map[string]json.RawMessage {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(value))
	for key := range value {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("public JSON whitelist changed", actual, expected)
	}
	return value
}

func TestPublicCatalogHTTPPublicationCASAndAnonymousWhitelist(t *testing.T) {
	s, pool := newAuthFixture(t)
	actor, _ := authUser(t, s, pool, "admin")
	session, csrf := authSession(t, s, pool, actor, "", false, true)
	id := publicHTTPModelWithMetadata(t, pool, "public-catalog-model", map[string]any{"internal_secret": "private-profile-canary", "future_internal_field": "private-new-field-canary"})
	meterquote.PublishDefaultPrice(t, context.Background(), pool, meterquote.DefaultPrice{ModelID: id, Actor: actor, InputRate: 9007199254740993, OutputRate: 0, EffectiveAt: time.Now().UTC().Add(-24 * time.Hour)})
	engine := s.Engine()
	count := 0
	call := func(method, path, raw, cookie string) *httptest.ResponseRecorder {
		count++
		return keyPolicyHTTP(t, engine, method, path, raw, cookie, csrf, s.Config.Origin, fmt.Sprintf("192.0.2.%d:1234", count), nil)
	}
	page := call("GET", "/api/public/models", "", "invalid-session")
	var envelope struct {
		Data []catalog.PublicModel `json:"data"`
		AsOf time.Time             `json:"as_of"`
	}
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &envelope) != nil || envelope.Data == nil || len(envelope.Data) != 0 || envelope.AsOf.IsZero() || page.Header().Get("Set-Cookie") != "" {
		t.Fatal("private default or empty DB timestamp changed", page.Code, page.Body.String())
	}
	for _, hidden := range []string{id, uuid.NewString()} {
		got := call("GET", "/api/public/models/"+hidden, "", "")
		if got.Code != 404 || !strings.Contains(got.Body.String(), `"code":"not_found"`) {
			t.Fatal("private and missing models differ", got.Code, got.Body.String())
		}
	}
	path := "/api/platform/models/" + id + "/publication"
	put := func(revision string, visible bool) *httptest.ResponseRecorder {
		return call("PUT", path, fmt.Sprintf(`{"expected_revision":%q,"public_visible":%t}`, revision, visible), session)
	}
	viewOf := func(response *httptest.ResponseRecorder) catalog.PublicationView {
		t.Helper()
		if response.Code != 200 {
			t.Fatal("publication failed", response.Code, response.Body.String())
		}
		publicJSONKeys(t, authData(t, response), "id", "public_name", "enabled", "public_visible", "public_revision")
		var view catalog.PublicationView
		if err := json.Unmarshal(authData(t, response), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	if view := viewOf(call("GET", path, "", session)); view.PublicVisible || view.PublicRevision != "1" {
		t.Fatal("new model was public", view)
	}
	if view := viewOf(put("1", true)); !view.Enabled || !view.PublicVisible || view.PublicRevision != "2" {
		t.Fatal("explicit publication did not advance CAS", view)
	}
	if view := viewOf(put("2", true)); view.PublicRevision != "2" {
		t.Fatal("same-value CAS advanced revision")
	}
	if stale := put("1", true); stale.Code != 409 || !strings.Contains(stale.Body.String(), "public_catalog_revision_conflict") {
		t.Fatal("same-value stale CAS accepted", stale.Code, stale.Body.String())
	}
	var audits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE target=$1 AND action='model.publication.update'`, id).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("publication audit/no-op mismatch", audits, err)
	}
	detail := call("GET", "/api/public/models/"+id, "", "invalid-session")
	if detail.Code != 200 || detail.Header().Get("Cache-Control") != "no-store" || detail.Header().Get("Set-Cookie") != "" || strings.Contains(detail.Body.String(), "canary") || strings.Contains(detail.Body.String(), "csrf") {
		t.Fatal("public detail leaked private state", detail.Code, detail.Body.String())
	}
	model := publicJSONKeys(t, authData(t, detail), "id", "public_name", "protocol", "capabilities", "limits", "pricing")
	publicJSONKeys(t, model["capabilities"], "streaming", "tools", "input_only", "text_only")
	publicJSONKeys(t, model["limits"], "context_tokens", "input_token_upper_bound", "max_output_tokens", "default_output_tokens")
	pricing := publicJSONKeys(t, model["pricing"], "status", "price_version_id", "schema_id", "schema_digest", "variant_key", "currency", "rates", "effective_at")
	var rates struct {
		Rates []struct {
			Dimension string `json:"dimension"`
			Numerator string `json:"numerator"`
		} `json:"rates"`
	}
	if json.Unmarshal(pricing["rates"], &rates) != nil || len(rates.Rates) != 2 || rates.Rates[0].Numerator != "9007199254740993" || rates.Rates[1].Numerator != "0" || string(model["protocol"]) != `"openai_chat"` {
		t.Fatal("rate precision or legacy protocol normalization changed", string(pricing["rates"]))
	}
	publicJSONKeys(t, detail.Body.Bytes(), "data", "as_of", "request_id")
	visiblePage := call("GET", "/api/public/models", "", "")
	publicJSONKeys(t, visiblePage.Body.Bytes(), "data", "as_of", "request_id", "next_cursor")
	authExec(t, pool, `UPDATE models SET enabled=false WHERE id=$1`, id)
	if got := call("GET", "/api/public/models/"+id, "", ""); got.Code != 404 {
		t.Fatal("disabled model remained public", got.Code)
	}
	if view := viewOf(call("GET", path, "", session)); view.Enabled || !view.PublicVisible || view.PublicRevision != "2" {
		t.Fatal("disable changed publication intent or revision", view)
	}
	authExec(t, pool, `UPDATE models SET enabled=true WHERE id=$1`, id)
	if got := call("GET", "/api/public/models/"+id, "", ""); got.Code != 200 {
		t.Fatal("re-enable did not restore publication", got.Code)
	}
	viewOf(put("2", false))
	if got := call("GET", "/api/public/models/"+id, "", ""); got.Code != 404 {
		t.Fatal("withdrawn model remained public")
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		if got := call(method, "/api/public/models", `{}`, session); got.Code != 404 {
			t.Fatal("unregistered public method did not preserve API rejection", method, got.Code)
		}
	}
	for _, invalid := range []string{"INVALID", uuid.Nil.String(), strings.ToUpper(id), id + "?secret=private-input-canary"} {
		if got := call("GET", "/api/public/models/"+invalid, "", ""); got.Code != 400 || strings.Contains(got.Body.String(), "canary") {
			t.Fatal("invalid detail reached DB or reflected input", got.Code, got.Body.String())
		}
	}
}

func TestPublicCatalogHTTPPrivateGatesAndRecoveryRemainIndependent(t *testing.T) {
	s, pool := newAuthFixture(t)
	admin, _ := authUser(t, s, pool, "admin")
	id := publicHTTPModel(t, s, admin, "public-gates-model")
	path := "/api/platform/models/" + id + "/publication"
	engine := s.Engine()
	for _, role := range []string{"none", "finance_operator", "finance_approver"} {
		user, _ := authUser(t, s, pool, role)
		session, csrf := authSession(t, s, pool, user, "", false, true)
		for _, method := range []string{"GET", "PUT"} {
			got := keyPolicyHTTP(t, engine, method, path, `{"expected_revision":"1","public_visible":true}`, session, csrf, s.Config.Origin, "", nil)
			if got.Code != 403 {
				t.Fatal("publication escaped admin boundary", role, method, got.Code)
			}
		}
	}
	session, csrf := authSession(t, s, pool, admin, "", false, true)
	unrecent, unrecentCSRF := authSession(t, s, pool, admin, "", false, false)
	for _, tc := range []struct{ session, csrf, origin string }{{unrecent, unrecentCSRF, s.Config.Origin}, {session, "wrong-csrf", s.Config.Origin}, {session, csrf, "https://foreign.invalid"}} {
		got := keyPolicyHTTP(t, engine, "PUT", path, `{"expected_revision":"1","public_visible":true}`, tc.session, tc.csrf, tc.origin, "", nil)
		if got.Code != 403 {
			t.Fatal("publication mutation skipped sensitive gate", got.Code)
		}
	}
	if got := keyPolicyHTTP(t, engine, "GET", path, "", unrecent, "", "", "", nil); got.Code != 200 {
		t.Fatal("read incorrectly required recent authentication", got.Code)
	}
	// Existing MFA setup and verification never gate anonymous publication data.
	authExec(t, pool, `UPDATE users SET mfa_enabled=true WHERE id=$1`, admin)
	if got := keyPolicyHTTP(t, engine, "GET", path, "", session, "", "", "", nil); got.Code != 403 {
		t.Fatal("MFA pending bypassed on private route", got.Code)
	}
	if got := keyPolicyHTTP(t, engine, "GET", "/api/public/models", "", session, "", "", "192.0.2.1:1234", nil); got.Code != 200 || got.Header().Get("Set-Cookie") != "" {
		t.Fatal("anonymous route read pending MFA session", got.Code)
	}
	authExec(t, pool, `UPDATE users SET mfa_enabled=false WHERE id=$1`, admin)
	s.Identity.EnforcePlatformMFA = true
	if got := keyPolicyHTTP(t, engine, "GET", path, "", session, "", "", "", nil); got.Code != 403 {
		t.Fatal("MFA enrollment gate disappeared", got.Code)
	}
	if got := keyPolicyHTTP(t, engine, "GET", "/api/public/models", "", session, "", "", "192.0.2.2:1234", nil); got.Code != 200 {
		t.Fatal("anonymous route gated MFA enrollment", got.Code)
	}
	s.Identity.EnforcePlatformMFA = false
	s.Config.RecoveryMode = true
	if got := keyPolicyHTTP(t, engine, "PUT", path, `{"expected_revision":"1","public_visible":true}`, session, csrf, s.Config.Origin, "", nil); got.Code != 503 || !strings.Contains(got.Body.String(), "recovery_mode") {
		t.Fatal("recovery accepted publication write", got.Code)
	}
	for _, cookie := range []string{"", session} {
		if got := keyPolicyHTTP(t, engine, "GET", "/api/public/models", "", cookie, "", "", "192.0.2.3:1234", nil); got.Code != 200 {
			t.Fatal("ready recovery blocked anonymous read", got.Code)
		}
	}
	s.Ready(false)
	if got := keyPolicyHTTP(t, engine, "GET", "/api/public/models", "", "", "", "", "192.0.2.4:1234", nil); got.Code != 503 || !strings.Contains(got.Body.String(), "instance_unavailable") || s.ready.Load() {
		t.Fatal("anonymous read reopened unready recovery", got.Code)
	}
	s.Ready(true)
	s.Guard = func(context.Context) error { return errors.New("private-guard-canary") }
	if got := keyPolicyHTTP(t, engine, "GET", "/api/public/models", "", "", "", "", "192.0.2.5:1234", nil); got.Code != 503 || !strings.Contains(got.Body.String(), "instance_unavailable") || strings.Contains(got.Body.String(), "canary") || s.ready.Load() {
		t.Fatal("anonymous read bypassed Guard", got.Code, got.Body.String())
	}
}

func TestPublicCatalogHTTPPaginationWithdrawAndInvalidProjection(t *testing.T) {
	s, pool := newAuthFixture(t)
	actor, _ := authUser(t, s, pool, "admin")
	var ids []string
	for i := range 51 {
		id := publicHTTPModel(t, s, actor, fmt.Sprintf("model-%02d", i))
		ids = append(ids, id)
		authExec(t, pool, `UPDATE models SET public_visible=true WHERE id=$1`, id)
	}
	engine := s.Engine()
	call := func(path string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, engine, "GET", path, "", "", "", "", "", nil)
	}
	var first struct {
		Data       []catalog.PublicModel `json:"data"`
		NextCursor string                `json:"next_cursor"`
		AsOf       time.Time             `json:"as_of"`
	}
	page := call("/api/public/models?q=model-")
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &first) != nil || len(first.Data) != 50 || first.NextCursor == "" || first.AsOf.IsZero() {
		t.Fatal("public first page bound/cursor failed", page.Code, page.Body.String())
	}
	// Existing cursors re-check visibility at each SQL statement snapshot.
	authExec(t, pool, `UPDATE models SET public_visible=false WHERE id=$1`, ids[50])
	second := call("/api/public/models?q=model-&cursor=" + first.NextCursor)
	var last struct {
		Data       []catalog.PublicModel `json:"data"`
		NextCursor string                `json:"next_cursor"`
		AsOf       time.Time             `json:"as_of"`
	}
	if second.Code != 200 || json.Unmarshal(second.Body.Bytes(), &last) != nil || len(last.Data) != 0 || last.Data == nil || last.NextCursor != "" || last.AsOf.Before(first.AsOf) {
		t.Fatal("cursor returned withdrawn model or omitted empty-page clock", second.Code, second.Body.String())
	}
	brokenID := publicHTTPModelWithMetadata(t, pool, "oversized-model", map[string]any{"internal_secret": strings.Repeat("private-profile-canary", 1000)})
	authExec(t, pool, `UPDATE models SET public_visible=true WHERE id=$1`, brokenID)
	broken := call("/api/public/models")
	if broken.Code != 503 || !strings.Contains(broken.Body.String(), "public_catalog_unavailable") || strings.Contains(broken.Body.String(), "canary") || s.publicCatalog.inflight != 0 {
		t.Fatal("invalid public profile was exposed or hidden as missing price", broken.Code, broken.Body.String())
	}
}

func TestPublicCatalogHTTPTotalDeadlineIncludesPoolWaitAndCancellation(t *testing.T) {
	s, pool := newAuthFixture(t)
	// A real exhausted pool proves the total anonymous deadline includes waiting
	// for a connection. No main instance, provider or paid request is involved.
	connections := make([]*pgxpool.Conn, 0, pool.Config().MaxConns)
	for range pool.Config().MaxConns {
		conn, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	defer func() {
		for _, conn := range connections {
			conn.Release()
		}
	}()
	engine := s.Engine()
	ctx, cancel := context.WithCancel(context.Background())
	var group sync.WaitGroup
	group.Add(1)
	cancelled := httptest.NewRecorder()
	go func() {
		defer group.Done()
		engine.ServeHTTP(cancelled, httptest.NewRequest("GET", "/api/public/models", nil).WithContext(ctx))
	}()
	deadline := time.Now().Add(time.Second)
	for pool.Stat().EmptyAcquireWaitTime() == 0 && time.Now().Before(deadline) {
		s.publicCatalog.mu.Lock()
		waiting := s.publicCatalog.inflight == 1
		s.publicCatalog.mu.Unlock()
		if waiting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	group.Wait()
	if cancelled.Code != 503 || s.publicCatalog.inflight != 0 {
		t.Fatal("cancelled connection wait leaked permit", cancelled.Code)
	}
	start := time.Now()
	timed := keyPolicyHTTP(t, engine, "GET", "/api/public/models", "", "", "", "", "192.0.2.2:1234", nil)
	elapsed := time.Since(start)
	if timed.Code != 503 || !strings.Contains(timed.Body.String(), "public_catalog_unavailable") || elapsed < 1800*time.Millisecond || elapsed > 2800*time.Millisecond || s.publicCatalog.inflight != 0 || !s.ready.Load() {
		t.Fatal("pool wait escaped anonymous total deadline or changed readiness", timed.Code, elapsed, timed.Body.String())
	}
}
