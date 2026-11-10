package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
)

// This is an opt-in equivalent browser smoke. It starts the built binary,
// creates two tenants through the isolated owner fixture, and uses only public
// HTTP endpoints for the request and session assertions. It deliberately does
// not use SQL to read the request under test.
func TestBrowserQAHTTPSecuritySmoke(t *testing.T) {
	if os.Getenv("BROWSER_QA_HTTP_SMOKE") != "1" {
		t.Skip("explicit isolated HTTP smoke opt-in required")
	}
	if runtime.GOOS == "windows" {
		t.Skip("loopback process smoke uses POSIX-only lifecycle assumptions")
	}
	if _, err := modelLifecycleDatabase(os.Getenv("TEST_DATABASE_URL")); err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("BROWSER_QA_BINARY")
	if binary == "" {
		binary = filepath.Join("..", "..", "bin", "moni-aigate")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal("HTTP smoke binary path unavailable")
	}
	if info, statErr := os.Stat(binary); statErr != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		t.Skip("prebuilt real main binary is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err = run(ctx, options{
		binary:   binary,
		listen:   "127.0.0.1:0",
		root:     t.TempDir(),
		database: os.Getenv("TEST_DATABASE_URL"),
		modelLifecycle: func(ctx context.Context, live *privateModelLifecycle) error {
			return exerciseHTTPSecuritySmoke(ctx, live)
		},
	}, io.Discard)
	if err != nil {
		t.Fatal("HTTP security smoke failed", err)
	}
}

func exerciseHTTPSecuritySmoke(ctx context.Context, live *privateModelLifecycle) error {
	setupCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	first, err := setupModelBusiness(setupCtx, live)
	if err != nil {
		return err
	}
	var adminID, modelID string
	if err = live.owner.QueryRow(setupCtx, `SELECT id FROM users WHERE platform_role='admin' LIMIT 1`).Scan(&adminID); err != nil {
		return stage("http_smoke_admin_lookup_failed")
	}
	if err = live.owner.QueryRow(setupCtx, `SELECT model_id FROM tenant_model_grants WHERE tenant_id=$1 LIMIT 1`, first.tenant).Scan(&modelID); err != nil {
		return stage("http_smoke_model_lookup_failed")
	}
	otherEmail := uuid.NewString() + "@http-smoke.invalid"
	otherPassword, err := randomPassword()
	if err != nil {
		return err
	}
	if _, err = identity.New(live.owner, live.secrets).CreateTenant(setupCtx, adminID, "HTTP smoke foreign tenant", otherEmail, otherPassword, []string{modelID}); err != nil {
		return stage("http_smoke_second_tenant_failed")
	}

	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	if err = live.fixture.arm(modelBeforeResponse); err != nil {
		return stage("http_smoke_model_barrier_failed")
	}
	defer live.fixture.resume()
	responseCh := make(chan struct {
		response *http.Response
		err      error
	}, 1)
	go func() {
		response, requestErr := modelRequest(ctx, client, live.origin, first.token, uuid.NewString(), false)
		responseCh <- struct {
			response *http.Response
			err      error
		}{response, requestErr}
	}()
	select {
	case <-live.fixture.hit:
	case <-ctx.Done():
		return stage("http_smoke_provider_barrier_timeout")
	}
	live.fixture.resume()
	result := <-responseCh
	if result.err != nil || result.response == nil || result.response.StatusCode != http.StatusOK {
		if result.response != nil {
			result.response.Body.Close()
		}
		return stage("http_smoke_customer_request_failed")
	}
	requestID := result.response.Header.Get("X-Request-ID")
	_, _ = io.Copy(io.Discard, result.response.Body)
	result.response.Body.Close()
	if requestID == "" {
		return stage("http_smoke_request_id_missing")
	}

	loginBody, _ := json.Marshal(map[string]string{"email": otherEmail, "password": otherPassword})
	login, err := http.NewRequestWithContext(ctx, http.MethodPost, live.origin+"/api/auth/login", bytes.NewReader(loginBody))
	if err != nil {
		return stage("http_smoke_login_request_failed")
	}
	login.Header.Set("Origin", live.origin)
	login.Header.Set("Content-Type", "application/json")
	loginResponse, err := client.Do(login)
	if err != nil || loginResponse.StatusCode != http.StatusOK {
		if loginResponse != nil {
			loginResponse.Body.Close()
		}
		return stage("http_smoke_second_tenant_login_failed")
	}
	loginResponse.Body.Close()
	cookies := loginResponse.Cookies()
	if len(cookies) != 1 {
		return stage("http_smoke_session_cookie_missing")
	}

	me, err := http.NewRequestWithContext(ctx, http.MethodGet, live.origin+"/api/me", nil)
	if err != nil {
		return stage("http_smoke_me_request_failed")
	}
	me.Header.Set("Origin", live.origin)
	me.AddCookie(cookies[0])
	meResponse, err := client.Do(me)
	if err != nil || meResponse.StatusCode != http.StatusOK {
		if meResponse != nil {
			meResponse.Body.Close()
		}
		return stage("http_smoke_session_lookup_failed")
	}
	var envelope struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(meResponse.Body, 16*1024)).Decode(&envelope)
	meResponse.Body.Close()
	if decodeErr != nil || envelope.Data.CSRF == "" {
		return stage("http_smoke_csrf_missing")
	}

	foreignRead, err := http.NewRequestWithContext(ctx, http.MethodGet, live.origin+"/api/requests/"+requestID, nil)
	if err != nil {
		return stage("http_smoke_foreign_read_request_failed")
	}
	foreignRead.AddCookie(cookies[0])
	foreignReadResponse, err := client.Do(foreignRead)
	if err != nil || foreignReadResponse.StatusCode != http.StatusNotFound {
		if foreignReadResponse != nil {
			foreignReadResponse.Body.Close()
		}
		return stage("http_smoke_cross_tenant_request_visible")
	}
	foreignReadResponse.Body.Close()

	switchBody := bytes.NewBufferString(`{"tenant_id":"` + uuid.NewString() + `"}`)
	csrfMissing, err := http.NewRequestWithContext(ctx, http.MethodPost, live.origin+"/api/tenant/switch", switchBody)
	if err != nil {
		return stage("http_smoke_csrf_request_failed")
	}
	csrfMissing.Header.Set("Origin", live.origin)
	csrfMissing.Header.Set("Content-Type", "application/json")
	csrfMissing.AddCookie(cookies[0])
	csrfMissingResponse, err := client.Do(csrfMissing)
	if err != nil || csrfMissingResponse.StatusCode != http.StatusForbidden {
		if csrfMissingResponse != nil {
			csrfMissingResponse.Body.Close()
		}
		return stage("http_smoke_missing_csrf_accepted")
	}
	csrfMissingResponse.Body.Close()

	wrongOriginBody := bytes.NewBufferString(`{"tenant_id":"` + uuid.NewString() + `"}`)
	wrongOrigin, err := http.NewRequestWithContext(ctx, http.MethodPost, live.origin+"/api/tenant/switch", wrongOriginBody)
	if err != nil {
		return stage("http_smoke_origin_request_failed")
	}
	wrongOrigin.Header.Set("Origin", "http://wrong-origin.invalid")
	wrongOrigin.Header.Set("X-CSRF-Token", envelope.Data.CSRF)
	wrongOrigin.Header.Set("Content-Type", "application/json")
	wrongOrigin.AddCookie(cookies[0])
	wrongOriginResponse, err := client.Do(wrongOrigin)
	if err != nil || wrongOriginResponse.StatusCode != http.StatusForbidden {
		if wrongOriginResponse != nil {
			wrongOriginResponse.Body.Close()
		}
		return stage("http_smoke_wrong_origin_accepted")
	}
	wrongOriginResponse.Body.Close()
	return nil
}
