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
)

func isolationRequest(t *testing.T, f *gatewayFixture, model string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hello"}}, "max_tokens": 20})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	r, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if _, err = io.Copy(io.Discard, r.Body); err != nil {
		t.Fatal(err)
	}
	return r.StatusCode, r.Header.Get("X-Request-ID")
}

func TestPostgresModelIsolationSurvivesRestartAndRecoversOnlyNewPublishedVersion(t *testing.T) {
	var reject atomic.Bool
	reject.Store(true)
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if request.Model == "provider-model" && reject.Load() {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"code":"model_not_found","message":"secret-canary"}}`)
			return
		}
		_, _ = io.WriteString(w, gatewayJSON)
	}}, nil)
	ctx := context.Background()
	channel := probeChannels(t, f)[0]
	var version, credential string
	if err := f.pool.QueryRow(ctx, "SELECT id,credential_id FROM channel_versions WHERE channel_id=$1", channel).Scan(&version, &credential); err != nil {
		t.Fatal(err)
	}
	other, err := f.server.Catalog.CreateModel(ctx, "test", catalog.ModelInput{PublicName: "other-model", InputUpperBound: 100, ContextLimit: 120, OutputLimit: 20, DefaultOutputLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	authExec(t, f.pool, "INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, other)
	authExec(t, f.pool, "UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE id=$1", f.key, other)
	if _, err = f.server.Catalog.Publish(ctx, "test", other, 0, []catalog.BindingInput{{ChannelID: channel, UpstreamModel: "other-upstream", Weight: 1, MaxInflight: 4}}); err != nil {
		t.Fatal(err)
	}
	var actor string
	if err = f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='finance_operator' LIMIT 1").Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Catalog.PublishPrice(ctx, actor, other, "1000000", "2000000", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	status, requestID := isolationRequest(t, f, "retail-model")
	if status != 502 || f.posts.Load() != 1 {
		t.Fatalf("model failure status=%d posts=%d", status, f.posts.Load())
	}
	var isolated, revoked, audits int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM route_binding_isolations WHERE channel_version_id=$1 AND upstream_model='provider-model'),(SELECT count(*) FROM channel_credentials WHERE id=$2 AND revoked_at IS NOT NULL),(SELECT count(*) FROM audit_events WHERE action='binding.quarantined')`, version, credential).Scan(&isolated, &revoked, &audits); err != nil || isolated != 1 || revoked != 0 || audits != 1 {
		t.Fatalf("model failure isolation scope: isolated=%d revoked=%d audits=%d err=%v", isolated, revoked, audits, err)
	}
	// Recreate all application stores and routing state, retaining only durable DB facts.
	f.http.Close()
	f.server = New(f.server.Config, f.pool, f.server.assets, nil)
	f.server.Ready(true)
	f.http = httptest.NewServer(f.server.Engine())
	t.Cleanup(f.http.Close)
	if status, _ = isolationRequest(t, f, "retail-model"); status != 503 || f.posts.Load() != 1 {
		t.Fatal("restart forgot the binding isolation")
	}
	if status, _ = isolationRequest(t, f, "other-model"); status != 200 || f.posts.Load() != 2 {
		t.Fatal("binding isolation disabled another model using the same credential")
	}
	oldBinding := []catalog.BindingInput{{ChannelID: channel, UpstreamModel: "provider-model", Weight: 1, MaxInflight: 4}}
	if _, err = f.server.Catalog.Publish(ctx, "test", f.model, 1, oldBinding); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("republishing an isolated version unexpectedly cleared it: %v", err)
	}
	if _, err = f.server.Catalog.SaveChannel(ctx, "test", channel, catalog.ChannelInput{Name: "explicit new version"}); err != nil {
		t.Fatal(err)
	}
	if status, _ = isolationRequest(t, f, "retail-model"); status != 503 || f.posts.Load() != 2 {
		t.Fatal("unpublished new version bypassed the frozen route")
	}
	reject.Store(false)
	if _, err = f.server.Catalog.Publish(ctx, "test", f.model, 1, oldBinding); err != nil {
		t.Fatal(err)
	}
	if status, _ = isolationRequest(t, f, "retail-model"); status != 200 || f.posts.Load() != 3 {
		t.Fatal("explicit new published version did not recover")
	}
	if old, e := f.server.Catalog.Candidates(ctx, f.model, 1); e != nil || len(old) != 0 {
		t.Fatalf("old version isolation was mutated: %+v %v", old, e)
	}
	wallet, err := f.server.Billing.Wallet(ctx, billing.Query{TenantID: f.tenant})
	if err != nil || wallet.Available != 960 || wallet.Held != 0 {
		t.Fatalf("failed generation charged or successful calls misbilled: %+v %v", wallet, err)
	}
	// The existing platform attempt API exposes only the normalized isolation fact.
	raw, _ := probeAdmin(t, f)
	req, _ := http.NewRequest(http.MethodGet, f.http.URL+"/api/platform/requests/"+requestID+"/attempts", nil)
	req.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
	response, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || !strings.Contains(string(body), `"binding_isolated":true`) || !strings.Contains(string(body), "model_configuration_error") || strings.Contains(string(body), "secret-canary") {
		t.Fatalf("platform isolation visibility: HTTP%d %s", response.StatusCode, body)
	}
}

func TestPostgresBare404DoesNotPersistModelIsolation(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":{"message":"model_not_found secret-canary"}}`)
	}}, nil)
	for range 2 {
		if status, _ := isolationRequest(t, f, "retail-model"); status != 502 {
			t.Fatalf("unexpected status %d", status)
		}
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM route_binding_isolations").Scan(&count); err != nil || count != 0 || f.posts.Load() != 2 {
		t.Fatalf("bare status or human text triggered isolation: count=%d posts=%d err=%v", count, f.posts.Load(), err)
	}
}
