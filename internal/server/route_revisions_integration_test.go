package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

type routeRevisionHTTPView struct {
	Data []struct {
		ID             string `json:"id"`
		Revision       int64  `json:"revision"`
		CreatedAt      string `json:"created_at"`
		ReleaseIntent  string `json:"release_intent"`
		Active         bool   `json:"active"`
		CurrentProfile bool   `json:"current_profile"`
		Bindings       []struct {
			ChannelID       string `json:"channel_id"`
			ChannelKind     string `json:"channel_kind"`
			IntegrationMode string `json:"integration_mode"`
			UpstreamModel   string `json:"upstream_model"`
		} `json:"bindings"`
	} `json:"data"`
	Next string `json:"next_cursor"`
}

func readRouteRevisionHTTP(t *testing.T, f *discoveryFixture) routeRevisionHTTPView {
	t.Helper()
	status, raw := f.call(t, http.MethodGet, "/api/platform/models/"+f.model+"/route-revisions", "", f.session, f.csrf)
	if status != http.StatusOK {
		t.Fatalf("route history returned HTTP %d: %s", status, raw)
	}
	for _, canary := range []string{"directory-secret", "local-fixture-secret", "base_url", "allowed_cidrs", "secret_ciphertext", "release_evidence_id", "cost_amount"} {
		if strings.Contains(string(raw), canary) {
			t.Fatalf("route history leaked %s: %s", canary, raw)
		}
	}
	var view routeRevisionHTTPView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestRouteRevisionHTTPHistoryAndNonMonotonicRollback(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) })
	path := "/api/platform/models/" + f.model + "/route-revisions"
	view := readRouteRevisionHTTP(t, f)
	if len(view.Data) != 2 || view.Data[0].Revision != 2 || !view.Data[0].Active || view.Data[1].Revision != 1 || view.Data[1].Active || view.Next != "" {
		t.Fatalf("unexpected initial history: %+v", view)
	}
	for _, row := range view.Data {
		if !row.CurrentProfile || row.ReleaseIntent != "test_only" || len(row.Bindings) != 1 || row.Bindings[0].ChannelKind != "direct" || row.Bindings[0].IntegrationMode != "embedded_adapter" || row.Bindings[0].UpstreamModel != "provider-model" || row.CreatedAt == "" {
			t.Fatalf("unsafe or incomplete route projection: %+v", row)
		}
	}
	if f.gets.Load() != 0 || f.posts.Load() != 0 {
		t.Fatal("route history performed provider I/O")
	}
	status, raw := f.call(t, http.MethodGet, path+"?cursor=not-a-cursor", "", f.session, f.csrf)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "invalid_cursor") {
		t.Fatalf("invalid cursor: %d %s", status, raw)
	}
	status, raw = f.call(t, http.MethodGet, "/api/platform/models/"+uuid.NewString()+"/route-revisions", "", f.session, f.csrf)
	if status != http.StatusOK || !strings.Contains(string(raw), `"data":[]`) {
		t.Fatalf("unknown model history: %d %s", status, raw)
	}
	status, raw = f.call(t, http.MethodPost, path+"/1/activate", `{"expected_revision":2}`, f.session, "")
	if status != http.StatusForbidden || !strings.Contains(string(raw), "csrf_invalid") {
		t.Fatalf("activation skipped CSRF: %d %s", status, raw)
	}
	f.server.Config.RecoveryMode = true
	status, raw = f.call(t, http.MethodPost, path+"/1/activate", `{"expected_revision":2}`, f.session, f.csrf)
	if status != http.StatusServiceUnavailable || !strings.Contains(string(raw), "recovery_mode") {
		t.Fatalf("activation skipped recovery gate: %d %s", status, raw)
	}
	f.server.Config.RecoveryMode = false
	if _, err := f.pool.Exec(context.Background(), `UPDATE sessions SET reauthenticated_at=NULL WHERE digest=$1`, f.server.Config.Secrets.Digest(f.session)); err != nil {
		t.Fatal(err)
	}
	status, raw = f.call(t, http.MethodPost, path+"/1/activate", `{"expected_revision":2}`, f.session, f.csrf)
	if status != http.StatusForbidden || !strings.Contains(string(raw), "reauth_required") {
		t.Fatalf("activation skipped reauthentication: %d %s", status, raw)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE sessions SET reauthenticated_at=clock_timestamp() WHERE digest=$1`, f.server.Config.Secrets.Digest(f.session)); err != nil {
		t.Fatal(err)
	}
	status, raw = f.call(t, http.MethodPost, path+"/1/activate", `{"expected_revision":2}`, f.session, f.csrf)
	if status != http.StatusOK || !strings.Contains(string(raw), `"revision":1`) {
		t.Fatalf("non-monotonic rollback failed: %d %s", status, raw)
	}
	view = readRouteRevisionHTTP(t, f)
	if len(view.Data) != 2 || !view.Data[1].Active || view.Data[0].Active {
		t.Fatalf("rollback did not move only the active pointer: %+v", view)
	}
	status, raw = f.call(t, http.MethodPost, path+"/2/activate", `{"expected_revision":2}`, f.session, f.csrf)
	if status != http.StatusConflict {
		t.Fatalf("stale activation returned %d: %s", status, raw)
	}
	var active int64
	if err := f.pool.QueryRow(context.Background(), `SELECT active_revision FROM models WHERE id=$1`, f.model).Scan(&active); err != nil || active != 1 {
		t.Fatalf("stale activation changed route: %d %v", active, err)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE users SET platform_role='finance_operator' WHERE id=$1`, f.actor); err != nil {
		t.Fatal(err)
	}
	status, raw = f.call(t, http.MethodGet, path, "", f.session, f.csrf)
	if status != http.StatusForbidden {
		t.Fatalf("non-admin read returned %d: %s", status, raw)
	}
	if f.gets.Load() != 0 || f.posts.Load() != 0 {
		t.Fatal(fmt.Sprintf("route activation unexpectedly performed provider I/O: gets=%d posts=%d", f.gets.Load(), f.posts.Load()))
	}
}

func TestRouteRevisionHTTPPagination(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) })
	for expected := int64(2); expected < 52; expected++ {
		if _, err := f.server.Catalog.Publish(context.Background(), f.actor, f.model, expected, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "provider-model", Weight: 1, MaxInflight: 4}}); err != nil {
			t.Fatalf("publish revision %d: %v", expected+1, err)
		}
	}
	path := "/api/platform/models/" + f.model + "/route-revisions"
	first := readRouteRevisionHTTP(t, f)
	if len(first.Data) != 50 || first.Next == "" {
		t.Fatalf("first route history page: rows=%d next=%q", len(first.Data), first.Next)
	}
	status, raw := f.call(t, http.MethodGet, path+"?cursor="+first.Next, "", f.session, f.csrf)
	if status != http.StatusOK {
		t.Fatalf("second route history page: %d %s", status, raw)
	}
	var second routeRevisionHTTPView
	if err := json.Unmarshal(raw, &second); err != nil || len(second.Data) != 2 || second.Next != "" {
		t.Fatalf("second route history payload: rows=%d next=%q err=%v", len(second.Data), second.Next, err)
	}
	seen, active := map[string]bool{}, 0
	for _, row := range append(first.Data, second.Data...) {
		if seen[row.ID] {
			t.Fatalf("duplicate route history row %s", row.ID)
		}
		seen[row.ID] = true
		if row.Active {
			active++
		}
	}
	if len(seen) != 52 || active != 1 {
		t.Fatalf("pagination lost or duplicated route history: rows=%d active=%d", len(seen), active)
	}
	if f.gets.Load() != 0 || f.posts.Load() != 0 {
		t.Fatal("route history pagination performed provider I/O")
	}
}
