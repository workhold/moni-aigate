package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func TestHTTPCoinDisplayPublicAdminReplayAndStrictInput(t *testing.T) {
	s, pool := newAuthFixture(t)
	admin, _ := authUser(t, s, pool, "admin")
	operator, _ := authUser(t, s, pool, "finance_operator")
	adminRaw, adminCSRF := authSession(t, s, pool, admin, "", false, true)
	staleRaw, staleCSRF := authSession(t, s, pool, admin, "", false, false)
	operatorRaw, _ := authSession(t, s, pool, operator, "", false, true)
	call := func(method, path, raw, csrf string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		var err error
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, s.Config.Origin+path, bytes.NewReader(data))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Origin", s.Config.Origin)
		req.Header.Set("X-CSRF-Token", csrf)
		if raw != "" {
			req.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
		}
		res := httptest.NewRecorder()
		s.Engine().ServeHTTP(res, req)
		return res
	}
	public := call(http.MethodGet, "/api/coin-display", "", "", nil)
	requireAuthStatus(t, public, http.StatusOK)
	publicJSONKeys(t, authData(t, public), "version", "display_name", "fiat_currency", "rate_num", "rate_den")
	if bytes.Contains(authData(t, public), []byte("actor")) || bytes.Contains(authData(t, public), []byte("reason")) || bytes.Contains(authData(t, public), []byte("operation_id")) {
		t.Fatal("public display leaked administration fields")
	}
	requireAuthStatus(t, call(http.MethodGet, "/api/platform/coin-display-settings", operatorRaw, "", nil), http.StatusForbidden)
	command := map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1", "display_name": "点数", "fiat_currency": "CNY", "rate_num": "7", "rate_den": "10", "reason": "review display labels", "confirmation": billing.CoinDisplayConfirmation}
	requireAuthStatus(t, call(http.MethodPut, "/api/platform/coin-display-settings", staleRaw, staleCSRF, command), http.StatusForbidden)
	for _, patch := range []map[string]any{
		{"rate_num": nil},
		{"rate_num": 7},
		{"fiat_currency": "EUR"},
		{"confirmation": "no"},
		{"private": true},
	} {
		invalid := map[string]any{}
		for key, value := range command {
			invalid[key] = value
		}
		for key, value := range patch {
			invalid[key] = value
		}
		requireAuthStatus(t, call(http.MethodPut, "/api/platform/coin-display-settings", adminRaw, adminCSRF, invalid), http.StatusBadRequest)
	}
	updated := call(http.MethodPut, "/api/platform/coin-display-settings", adminRaw, adminCSRF, command)
	requireAuthStatus(t, updated, http.StatusOK)
	replayed := call(http.MethodPut, "/api/platform/coin-display-settings", adminRaw, adminCSRF, command)
	requireAuthStatus(t, replayed, http.StatusOK)
	if !bytes.Equal(authData(t, updated), authData(t, replayed)) {
		t.Fatal("display operation replay changed the response")
	}
	public = call(http.MethodGet, "/api/coin-display", "", "", nil)
	requireAuthStatus(t, public, http.StatusOK)
	var current billing.CoinDisplayPublic
	if err := json.Unmarshal(authData(t, public), &current); err != nil || current.Version != 2 || current.DisplayName != "点数" || current.FiatCurrency == nil || *current.FiatCurrency != "CNY" || current.RateNum == nil || *current.RateNum != "7" {
		t.Fatalf("updated public display: %+v %v", current, err)
	}
	operationPath := "/api/platform/coin-display-settings/" + command["operation_id"].(string)
	requireAuthStatus(t, call(http.MethodGet, operationPath, adminRaw, "", nil), http.StatusOK)
	requireAuthStatus(t, call(http.MethodGet, operationPath, operatorRaw, "", nil), http.StatusForbidden)
	conflict := map[string]any{}
	for key, value := range command {
		conflict[key] = value
	}
	conflict["reason"] = "different intent"
	requireAuthStatus(t, call(http.MethodPut, "/api/platform/coin-display-settings", adminRaw, adminCSRF, conflict), http.StatusConflict)
	stale := map[string]any{}
	for key, value := range command {
		stale[key] = value
	}
	stale["operation_id"] = uuid.NewString()
	requireAuthStatus(t, call(http.MethodPut, "/api/platform/coin-display-settings", adminRaw, adminCSRF, stale), http.StatusConflict)
	requireAuthStatus(t, call(http.MethodPut, "/api/platform/coin-display-settings", adminRaw, "", command), http.StatusForbidden)
	var audits int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='coin_display.updated'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("display audit count=%d %v", audits, err)
	}
}

func TestPostgresCoinDisplayRenameDoesNotAffectGateway(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `ALTER TABLE coin_display_versions RENAME TO coin_display_versions_offline`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.pool.Exec(ctx, `ALTER TABLE coin_display_versions_offline RENAME TO coin_display_versions`); err != nil {
			t.Error("restore display table name", err)
		}
	}()
	response := f.request(t, ctx, false, false)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("display table entered the financial request path: %d", response.StatusCode)
	}
}
