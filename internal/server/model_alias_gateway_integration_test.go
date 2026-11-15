package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func TestModelAliasHTTPGatewayAuthorizationAndOriginalName(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var head struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(raw, &head) != nil || head.Model != "provider-model" {
			t.Errorf("alias did not resolve to the bound upstream model: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, gatewayJSON)
	}}, nil)
	admin, adminCSRF := probeAdmin(t, f)
	member, _ := authUser(t, f.server, f.pool, "none")
	memberSession, memberCSRF := authSession(t, f.server, f.pool, member, f.tenant, true, true)
	alias := "gpt-4o-alias"
	createPath := "/api/platform/models/" + f.model + "/aliases"
	body := map[string]any{"operation_id": uuid.NewString(), "alias": alias}
	if got := authHTTP(t, f.server, http.MethodPost, createPath, memberSession, memberCSRF, f.server.Config.Origin, body); got.Code != http.StatusForbidden {
		t.Fatalf("non-admin created an alias: HTTP %d %s", got.Code, got.Body.String())
	}
	created := authHTTP(t, f.server, http.MethodPost, createPath, admin, adminCSRF, f.server.Config.Origin, body)
	if created.Code != http.StatusOK {
		t.Fatalf("alias creation failed: HTTP %d %s", created.Code, created.Body.String())
	}
	var createdAlias catalog.ModelAlias
	if err := json.Unmarshal(authData(t, created), &createdAlias); err != nil || createdAlias.Alias != alias || createdAlias.ModelID != f.model || createdAlias.State != "active" {
		t.Fatalf("unexpected alias receipt: %+v %v", createdAlias, err)
	}
	replay := authHTTP(t, f.server, http.MethodPost, createPath, admin, adminCSRF, f.server.Config.Origin, body)
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"revision":"1"`) {
		t.Fatalf("alias operation replay was not idempotent: HTTP %d %s", replay.Code, replay.Body.String())
	}
	listed := authHTTP(t, f.server, http.MethodGet, createPath, admin, adminCSRF, f.server.Config.Origin, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"alias":"`+alias+`"`) {
		t.Fatalf("alias management list failed: HTTP %d %s", listed.Code, listed.Body.String())
	}

	request := func(model string) *http.Response {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hello"}}, "max_tokens": 20})
		r, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/chat/completions", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+f.token)
		response, err := f.http.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = response.Body.Close() })
		return response
	}
	modelsRequest, err := http.NewRequest(http.MethodGet, f.http.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	modelsRequest.Header.Set("Authorization", "Bearer "+f.token)
	modelsResponse, err := f.http.Client().Do(modelsRequest)
	if err != nil {
		t.Fatal(err)
	}
	modelsRaw, _ := io.ReadAll(modelsResponse.Body)
	_ = modelsResponse.Body.Close()
	if modelsResponse.StatusCode != http.StatusOK || !strings.Contains(string(modelsRaw), `"id":"retail-model"`) || !strings.Contains(string(modelsRaw), `"id":"`+alias+`"`) {
		t.Fatalf("authorized model directory did not list public name and alias: HTTP %d %s", modelsResponse.StatusCode, modelsRaw)
	}

	response := request(alias)
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("alias invocation failed: HTTP %d %s", response.StatusCode, raw)
	}
	var completion struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &completion) != nil || completion.Model != alias {
		t.Fatalf("response did not echo the original alias: %s", raw)
	}
	var requestedName, modelID, billingStatus string
	if err := f.pool.QueryRow(context.Background(), `SELECT requested_model_name,model_id,billing_status FROM request_records WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 1`, f.tenant).Scan(&requestedName, &modelID, &billingStatus); err != nil {
		t.Fatal(err)
	}
	if requestedName != alias || modelID != f.model || billingStatus != "settled" {
		t.Fatalf("alias request identity changed: requested=%s model=%s billing=%s", requestedName, modelID, billingStatus)
	}
	if f.posts.Load() != 1 {
		t.Fatalf("alias invocation dispatched %d upstream requests", f.posts.Load())
	}

	// Alias resolution never widens the API key's logical-model scopes.
	if _, err := f.pool.Exec(context.Background(), "UPDATE api_keys SET scopes='{}' WHERE id=$1", f.key); err != nil {
		t.Fatal(err)
	}
	denied := request(alias)
	deniedRaw, _ := io.ReadAll(denied.Body)
	if denied.StatusCode != http.StatusForbidden || !strings.Contains(string(deniedRaw), "model_forbidden") || f.posts.Load() != 1 {
		t.Fatalf("alias bypassed key authorization: HTTP %d %s posts=%d", denied.StatusCode, deniedRaw, f.posts.Load())
	}
	if _, err := f.pool.Exec(context.Background(), "UPDATE api_keys SET scopes=$2 WHERE id=$1", f.key, []string{f.model}); err != nil {
		t.Fatal(err)
	}

	retire := authHTTP(t, f.server, http.MethodPost, "/api/platform/model-aliases/"+alias+"/retire", admin, adminCSRF, f.server.Config.Origin, map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1"})
	if retire.Code != http.StatusOK {
		t.Fatalf("alias retirement failed: HTTP %d %s", retire.Code, retire.Body.String())
	}
	retired := request(alias)
	retiredRaw, _ := io.ReadAll(retired.Body)
	if retired.StatusCode != http.StatusForbidden || !strings.Contains(string(retiredRaw), "model_forbidden") || f.posts.Load() != 1 {
		t.Fatalf("retired alias remained dispatchable: HTTP %d %s posts=%d", retired.StatusCode, retiredRaw, f.posts.Load())
	}
}
