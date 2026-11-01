package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/routing"
)

func TestRouteDecisionSnapshotsAreSafeImmutableAndPlatformOnly(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t), gatewaySuccess(t), gatewaySuccess(t)}, nil)
	ctx := context.Background()
	channels := probeChannels(t, f)
	authExec(t, f.pool, "UPDATE channels SET enabled=false WHERE id=$1", channels[0])
	var credential string
	if err := f.pool.QueryRow(ctx, "SELECT credential_id FROM channel_versions WHERE channel_id=$1", channels[1]).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	f.server.Routing.Cooldown(credential+":"+f.model, time.Minute)
	response := f.request(t, ctx, false, false)
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		t.Fatalf("status=%d %s", response.StatusCode, body)
	}
	var raw []byte
	if err := f.pool.QueryRow(ctx, "SELECT route_decisions FROM upstream_attempts WHERE request_id=$1", response.Header.Get("X-Request-ID")).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var decisions []routing.Decision
	if json.Unmarshal(raw, &decisions) != nil || len(decisions) != 3 {
		t.Fatalf("decisions=%s", raw)
	}
	reasons := map[string]string{}
	for _, d := range decisions {
		reasons[d.ChannelID] = d.Reason
	}
	if reasons[channels[0]] != "disabled" || reasons[channels[1]] != "circuit_open" || reasons[channels[2]] != "selected" {
		t.Fatal(reasons)
	}
	for _, forbidden := range []string{"secret", "provider-model", "127.0.0.1", "credential", "Endpoint"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("unsafe routing metadata", forbidden)
		}
	}
	if _, err := f.pool.Exec(ctx, "UPDATE upstream_attempts SET route_decisions='[]' WHERE request_id=$1", response.Header.Get("X-Request-ID")); err == nil {
		t.Fatal("route explanation was mutable")
	}
	admin, csrf := probeAdmin(t, f)
	view := authHTTP(t, f.server, "GET", "/api/platform/requests/"+response.Header.Get("X-Request-ID")+"/attempts", admin, csrf, "", nil)
	if view.Code != 200 || !strings.Contains(view.Body.String(), "route_decisions") {
		t.Fatal(view.Code, view.Body.String())
	}
	var owner string
	if err := f.pool.QueryRow(ctx, "SELECT owner_id FROM api_keys WHERE id=$1", f.key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	customer, cc := authSession(t, f.server, f.pool, owner, f.tenant, true, false)
	view = authHTTP(t, f.server, "GET", "/api/platform/requests/"+response.Header.Get("X-Request-ID")+"/attempts", customer, cc, "", nil)
	if view.Code != 403 {
		t.Fatal("customer accessed platform routing metadata", view.Code)
	}
}
