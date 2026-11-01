package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

type discoveryFixture struct {
	*gatewayFixture
	channel, version, actor, session, csrf, format string
	gets                                           atomic.Int32
}

func newDiscoveryFixture(t *testing.T, format string, handler http.HandlerFunc) *discoveryFixture {
	t.Helper()
	f := &discoveryFixture{gatewayFixture: newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		t.Error("discovery sent a generation request")
		w.WriteHeader(500)
	}}, nil), format: format}
	f.session, f.csrf = probeAdmin(t, f.gatewayFixture)
	if err := f.pool.QueryRow(context.Background(), "SELECT id FROM users WHERE platform_role='admin'").Scan(&f.actor); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gets.Add(1)
		prefix := "/v1"
		if format == "gemini" {
			prefix = "/v1beta"
		}
		if r.Method != "GET" || r.URL.Path != prefix+"/models" || r.Header.Get("Cookie") != "" {
			t.Errorf("unsafe directory request %s %s", r.Method, r.URL.Path)
		}
		switch format {
		case "openai":
			if r.Header.Get("Authorization") != "Bearer directory-secret" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("X-Goog-Api-Key") != "" {
				t.Error("invalid OpenAI directory auth")
			}
		case "anthropic":
			if r.Header.Get("X-Api-Key") != "directory-secret" || r.Header.Get("Anthropic-Version") != "2023-06-01" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Goog-Api-Key") != "" {
				t.Error("invalid Anthropic directory auth")
			}
		case "gemini":
			if r.Header.Get("X-Goog-Api-Key") != "directory-secret" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" {
				t.Error("invalid Gemini directory auth")
			}
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(upstream.Close)
	prefix := "/v1"
	if format == "gemini" {
		prefix = "/v1beta"
	}
	var err error
	f.channel, err = f.server.Catalog.SaveChannel(context.Background(), f.actor, "", catalog.ChannelInput{Name: "directory-channel", BaseURL: upstream.URL + prefix, Secret: "directory-secret", AllowedCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(context.Background(), "SELECT current_version FROM channels WHERE id=$1", f.channel).Scan(&f.version); err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Catalog.Publish(context.Background(), f.actor, f.model, 1, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "provider-model", Weight: 1, MaxInflight: 4}}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *discoveryFixture) path(suffix string) string {
	return "/api/platform/channels/" + f.channel + suffix
}
func (f *discoveryFixture) body() string {
	return fmt.Sprintf(`{"format":%q,"expected_version":%q}`, f.format, f.version)
}
func (f *discoveryFixture) call(t *testing.T, method, path, body, session, csrf string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, f.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "moni_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Origin", f.server.Config.Origin)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw
}
func (f *discoveryFixture) discover(t *testing.T) (int, []byte) {
	return f.call(t, "POST", f.path("/discover-models"), f.body(), f.session, f.csrf)
}
func discoveryView(t *testing.T, raw []byte) catalog.DiscoveryView {
	t.Helper()
	var envelope struct {
		Data catalog.DiscoveryView `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data.ID == "" {
		t.Fatalf("invalid directory envelope %s %v", raw, err)
	}
	return envelope.Data
}
func (f *discoveryFixture) assertUnchanged(t *testing.T) {
	t.Helper()
	probeAssertWalletUntouched(t, f.gatewayFixture)
	if f.posts.Load() != 0 || len(f.server.probes) != 0 || len(f.server.global) != 0 {
		t.Fatal("directory leaked generation or runtime resources")
	}
	var probes, revoked, isolated, attempts int
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM platform_probe_attempts),(SELECT count(*) FROM channel_credentials WHERE revoked_at IS NOT NULL),(SELECT count(*) FROM route_binding_isolations),(SELECT count(*) FROM upstream_attempts)`).Scan(&probes, &revoked, &isolated, &attempts); err != nil || probes != 0 || revoked != 0 || isolated != 0 || attempts != 0 {
		t.Fatalf("directory affected inference state %d/%d/%d/%d %v", probes, revoked, isolated, attempts, err)
	}
}

func TestDiscoveryHTTPFormatsSnapshotsAndHistory(t *testing.T) {
	for _, format := range []string{"openai", "anthropic", "gemini"} {
		t.Run(format, func(t *testing.T) {
			f := newDiscoveryFixture(t, format, func(w http.ResponseWriter, r *http.Request) {
				switch format {
				case "openai":
					_, _ = io.WriteString(w, `{"data":[{"id":"new-model","description":"private-directory-metadata"},{"id":"provider-model"},{"id":"new-model"}]}`)
				case "anthropic":
					_, _ = io.WriteString(w, `{"data":[{"id":"provider-model"},{"id":"new-model"}],"has_more":false,"description":"private-directory-metadata"}`)
				case "gemini":
					_, _ = io.WriteString(w, `{"models":[{"name":"models/provider-model","supportedGenerationMethods":["generateContent"]},{"name":"models/new-model"},{"name":"models/embed-only","supportedGenerationMethods":["embedContent"]}],"description":"private-directory-metadata"}`)
				}
			})
			ctx := context.Background()
			for i, name := range []string{"alias-model", "paused-model"} {
				id, err := f.server.Catalog.CreateModel(ctx, f.actor, catalog.ModelInput{PublicName: name, InputUpperBound: 100, ContextLimit: 120, OutputLimit: 20, DefaultOutputLimit: 20})
				if err != nil {
					t.Fatal(err)
				}
				upstream := "provider-model"
				if i == 1 {
					upstream = "not-listed"
				}
				if _, err = f.server.Catalog.Publish(ctx, f.actor, id, 0, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: upstream, Weight: 1, MaxInflight: 2}}); err != nil {
					t.Fatal(err)
				}
				if i == 1 {
					if err = f.server.Catalog.SetModelEnabled(ctx, f.actor, id, false); err != nil {
						t.Fatal(err)
					}
				}
			}
			var before string
			if err := f.pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT count(*) FROM models),(SELECT count(*) FROM model_bindings),(SELECT count(*) FROM price_versions),(SELECT count(*) FROM tenant_model_grants),(SELECT count(*) FROM journals))::text`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			status, raw := f.discover(t)
			if status != 200 {
				t.Fatalf("discover %d %s", status, raw)
			}
			v := discoveryView(t, raw)
			if v.Status != "completed" || v.Stale || v.Pages != 1 || len(v.Models) != 2 || len(v.Unbound) != 1 || v.Unbound[0] != "new-model" || len(v.Present) != 2 || len(v.Missing) != 1 || v.Missing[0].Enabled || v.Missing[0].UpstreamModel != "not-listed" {
				t.Fatalf("incorrect directory diff %+v", v)
			}
			if bytes.Contains(raw, []byte("private-directory-metadata")) || bytes.Contains(raw, []byte("directory-secret")) {
				t.Fatal("unsafe provider metadata escaped")
			}
			status, raw = f.call(t, "GET", f.path("/model-discoveries"), "", f.session, "")
			if status != 200 {
				t.Fatalf("history %d %s", status, raw)
			}
			var history struct {
				Data []map[string]any `json:"data"`
			}
			if json.Unmarshal(raw, &history) != nil || len(history.Data) != 1 || history.Data[0]["id"] != v.ID || history.Data[0]["models"] != nil {
				t.Fatalf("unexpected directory history %s", raw)
			}
			status, raw = f.call(t, "GET", f.path("/model-discoveries/"+v.ID), "", f.session, "")
			if status != 200 || discoveryView(t, raw).ID != v.ID {
				t.Fatal("directory detail unreadable")
			}
			wrong := strings.Replace(f.path("/model-discoveries/"+v.ID), f.channel, probeChannels(t, f.gatewayFixture)[1], 1)
			// The other fixture channel cannot read this record, even as admin.
			if strings.Contains(wrong, f.channel) {
				t.Fatal("wrong-channel fixture failed")
			}
			status, _ = f.call(t, "GET", wrong, "", f.session, "")
			if status != 404 {
				t.Fatal("directory detail crossed channel", status)
			}
			var audits int
			if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE target=$1 AND action IN ('channel.discovery.start','channel.discovery.completed')`, v.ID).Scan(&audits); err != nil || audits != 2 {
				t.Fatal("directory audit missing", audits, err)
			}
			var after string
			if err := f.pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT count(*) FROM models),(SELECT count(*) FROM model_bindings),(SELECT count(*) FROM price_versions),(SELECT count(*) FROM tenant_model_grants),(SELECT count(*) FROM journals))::text`).Scan(&after); err != nil || before != after {
				t.Fatal("discovery changed business configuration", before, after, err)
			}
			if f.gets.Load() != 1 {
				t.Fatal("history caused extra network", f.gets.Load())
			}
			f.assertUnchanged(t)
		})
	}
}

func TestDiscoveryHTTPAdmissionAndCredentialVersion(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"data":[]}`) })
	for _, raw := range []string{`{}`, `{"format":"unknown","expected_version":"x"}`, f.body() + ` {}`, strings.Replace(f.body(), `"format":"openai"`, `"format":"openai","format":"openai"`, 1), strings.TrimSuffix(f.body(), "}") + `,"url":"http://169.254.169.254"}`} {
		status, body := f.call(t, "POST", f.path("/discover-models"), raw, f.session, f.csrf)
		if status != 400 {
			t.Fatalf("invalid discovery body accepted %d %s", status, body)
		}
	}
	var customer, finance string
	for role, dst := range map[string]*string{"none": &customer, "finance_operator": &finance} {
		if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role=$1 LIMIT 1`, role).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{customer, finance} {
		raw, csrf := authSession(t, f.server, f.pool, user, "", true, true)
		for _, path := range []string{"/discover-models", "/model-discoveries", "/model-discoveries/missing"} {
			method := "GET"
			body := ""
			if path == "/discover-models" {
				method = "POST"
				body = f.body()
			}
			status, _ := f.call(t, method, f.path(path), body, raw, csrf)
			if status != 403 {
				t.Fatal("non-admin directory access", status, path)
			}
		}
	}
	raw, csrf := authSession(t, f.server, f.pool, f.actor, "", true, false)
	status, _ := f.call(t, "POST", f.path("/discover-models"), f.body(), raw, csrf)
	if status != 403 {
		t.Fatal("discovery skipped reauthentication", status)
	}
	status, _ = f.call(t, "POST", f.path("/discover-models"), f.body(), f.session, "wrong")
	if status != 403 {
		t.Fatal("discovery skipped CSRF", status)
	}
	f.server.Config.RecoveryMode = true
	status, _ = f.discover(t)
	f.server.Config.RecoveryMode = false
	if status != 503 {
		t.Fatal("discovery skipped recovery gate", status)
	}
	f.server.Ready(false)
	status, _ = f.discover(t)
	f.server.Ready(true)
	if status != 503 {
		t.Fatal("discovery skipped readiness", status)
	}
	status, _ = f.call(t, "POST", f.path("/discover-models"), strings.Replace(f.body(), f.version, "old-version", 1), f.session, f.csrf)
	if status != 409 {
		t.Fatal("old channel version accepted", status)
	}
	// Reset only the test limiter to exercise the revoked admission separately.
	f.server.mu.Lock()
	delete(f.server.limits, "discovery:"+f.channel)
	f.server.mu.Unlock()
	authExec(t, f.pool, `UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=(SELECT credential_id FROM channel_versions WHERE id=$1)`, f.version)
	status, _ = f.discover(t)
	if status != 409 {
		t.Fatal("revoked credential accepted", status)
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM channel_model_discoveries`).Scan(&count); err != nil || count != 0 || f.gets.Load() != 0 {
		t.Fatal("admission rejection reached durable/network start", count, f.gets.Load(), err)
	}
	probeAssertWalletUntouched(t, f.gatewayFixture)
}

func TestDiscoveryHTTPFailuresKeepHistoryWithoutPartialDiffOrHealthChanges(t *testing.T) {
	for _, scenario := range []string{"http", "malformed", "late_page"} {
		t.Run(scenario, func(t *testing.T) {
			format := "openai"
			if scenario == "late_page" {
				format = "anthropic"
			}
			f := newDiscoveryFixture(t, format, func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "http":
					w.WriteHeader(401)
					_, _ = io.WriteString(w, `{"error":{"message":"directory-secret-canary"}}`)
				case "malformed":
					_, _ = io.WriteString(w, `{"data":[{"id":"ok"}],"data":[]}`)
				case "late_page":
					if r.URL.Query().Get("after_id") == "" {
						_, _ = io.WriteString(w, `{"data":[{"id":"first-page"}],"has_more":true,"last_id":"cursor"}`)
					} else {
						w.WriteHeader(500)
						_, _ = io.WriteString(w, "directory-secret-canary")
					}
				}
			})
			status, raw := f.discover(t)
			if status != 503 || bytes.Contains(raw, []byte("directory-secret-canary")) {
				t.Fatalf("unsafe directory failure %d %s", status, raw)
			}
			var failure struct {
				ID string `json:"discovery_id"`
			}
			if json.Unmarshal(raw, &failure) != nil || failure.ID == "" {
				t.Fatal("directory failure lost durable identity")
			}
			v, err := f.server.Catalog.ModelDiscovery(context.Background(), f.channel, failure.ID)
			if err != nil || v.Status != "failed" || v.ErrorCode == "" || len(v.Models)+len(v.Present)+len(v.Missing)+len(v.Unbound) != 0 || v.Stale {
				t.Fatalf("partial failure masquerades as diff %+v %v", v, err)
			}
			if status, _ = f.discover(t); status != 429 {
				t.Fatal("directory rate limit not retained", status)
			}
			f.assertUnchanged(t)
		})
	}
}

func TestDiscoveryHTTPRotationDuringNetworkMarksOldSnapshot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, `{"data":[{"id":"provider-model"}]}`)
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	result := make(chan probeHTTPResult, 1)
	go func() {
		req, _ := http.NewRequest("POST", f.http.URL+f.path("/discover-models"), strings.NewReader(f.body()))
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: f.session})
		req.Header.Set("Origin", f.server.Config.Origin)
		req.Header.Set("X-CSRF-Token", f.csrf)
		req.Header.Set("Content-Type", "application/json")
		res, err := f.http.Client().Do(req)
		if err != nil {
			result <- probeHTTPResult{err: err}
			return
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		result <- probeHTTPResult{res.StatusCode, raw, err}
	}()
	probeWaitSignal(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := f.server.Catalog.SaveChannel(ctx, f.actor, f.channel, catalog.ChannelInput{Name: "rotated-directory", Secret: "next-secret"}); err != nil {
		t.Fatalf("directory held transaction during HTTP %v", err)
	}
	close(release)
	select {
	case res := <-result:
		if res.err != nil || res.status != 200 {
			t.Fatalf("inflight directory %d %s %v", res.status, res.body, res.err)
		}
		v := discoveryView(t, res.body)
		if !v.Stale || v.ChannelVersionID != f.version || len(v.Present) != 1 {
			t.Fatalf("old directory passed as current %+v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("directory did not exit")
	}
	f.assertUnchanged(t)
}

func TestDiscoverySnapshotStalenessAndInterruptedHistory(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) { t.Error("history caused network") })
	ctx := context.Background()
	begin := func() string {
		target, err := f.server.Catalog.BeginModelDiscovery(ctx, f.actor, f.channel, f.version, "openai")
		if err != nil {
			t.Fatal(err)
		}
		return target.ID
	}
	id := begin()
	if err := f.server.Catalog.FinishModelDiscovery(ctx, id, provider.DiscoveryResult{Models: []string{"provider-model"}, Pages: 1}, ""); err != nil {
		t.Fatal(err)
	}
	v, err := f.server.Catalog.ModelDiscovery(ctx, f.channel, id)
	if err != nil || v.Stale {
		t.Fatal(v, err)
	}
	if err = f.server.Catalog.SetModelEnabled(ctx, f.actor, f.model, false); err != nil {
		t.Fatal(err)
	}
	v, err = f.server.Catalog.ModelDiscovery(ctx, f.channel, id)
	if err != nil || !v.Stale || !v.Present[0].Enabled {
		t.Fatal("model state change lost original snapshot", v, err)
	}
	if err = f.server.Catalog.SetModelEnabled(ctx, f.actor, f.model, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Catalog.Publish(ctx, f.actor, f.model, 2, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "new-mapping", Weight: 1, MaxInflight: 4}}); err != nil {
		t.Fatal(err)
	}
	v, err = f.server.Catalog.ModelDiscovery(ctx, f.channel, id)
	if err != nil || !v.Stale || v.Present[0].UpstreamModel != "provider-model" {
		t.Fatal("route revision rewrote directory snapshot", v, err)
	}
	running := begin()
	v, err = f.server.Catalog.ModelDiscovery(ctx, f.channel, running)
	if err != nil || v.Status != "running" || len(v.Missing) != 0 {
		t.Fatal("running presented as missing directory", v, err)
	}
	// Explicit time-compressed interrupted fixture; immutable rows are inserted
	// at their original time rather than updating previously stored history.
	interrupted := "interrupted-fixture"
	authExec(t, f.pool, `INSERT INTO channel_model_discoveries(id,actor,channel_id,channel_version_id,credential_id,format,bindings,created_at) SELECT $2,actor,channel_id,channel_version_id,credential_id,format,bindings,clock_timestamp()-interval '31 seconds' FROM channel_model_discoveries WHERE id=$1`, running, interrupted)
	status, raw := f.call(t, "GET", f.path("/model-discoveries/"+interrupted), "", f.session, "")
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	v = discoveryView(t, raw)
	if v.Status != "interrupted" || len(v.Models)+len(v.Missing) != 0 {
		t.Fatal("interrupted presented as complete empty directory", v)
	}
	f.assertUnchanged(t)
}

func TestDiscoveryGuardFailurePersistsFailedWithoutNetwork(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) { t.Error("guard failure reached provider") })
	var checks atomic.Int32
	f.server.Guard = func(context.Context) error {
		if checks.Add(1) > 1 {
			return errors.New("fixture fenced")
		}
		return nil
	}
	status, raw := f.discover(t)
	if status != 503 {
		t.Fatal(status, string(raw))
	}
	var failure struct {
		ID string `json:"discovery_id"`
	}
	_ = json.Unmarshal(raw, &failure)
	v, err := f.server.Catalog.ModelDiscovery(context.Background(), f.channel, failure.ID)
	if err != nil || v.Status != "failed" || f.gets.Load() != 0 {
		t.Fatal("guard failure lost terminal", v, err)
	}
	f.assertUnchanged(t)
}

func TestDiscoveryCancelledHTTPClosesSocketAndStoresFailure(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(exited) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", f.http.URL+f.path("/discover-models"), strings.NewReader(f.body()))
	req.AddCookie(&http.Cookie{Name: "moni_session", Value: f.session})
	req.Header.Set("Origin", f.server.Config.Origin)
	req.Header.Set("X-CSRF-Token", f.csrf)
	req.Header.Set("Content-Type", "application/json")
	done := make(chan error, 1)
	go func() {
		res, err := f.http.Client().Do(req)
		if res != nil {
			res.Body.Close()
		}
		done <- err
	}()
	probeWaitSignal(t, entered)
	cancel()
	probeWaitSignal(t, exited)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled directory reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled client did not exit")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM channel_model_discovery_results WHERE status='failed'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 && len(f.server.probes) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled discovery did not persist/clean up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f.gets.Load() != 1 {
		t.Fatal("cancelled discovery replayed")
	}
	f.assertUnchanged(t)
}

func TestDiscoveryCapacityAndTerminalAuditFailure(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"new-model"}]}`)
	})
	for _, budget := range []chan struct{}{f.server.probes, f.server.global} {
		for i := 0; i < cap(budget); i++ {
			budget <- struct{}{}
		}
		status, raw := f.discover(t)
		for i := 0; i < cap(budget); i++ {
			<-budget
		}
		if status != 429 || !bytes.Contains(raw, []byte("discovery_capacity_limited")) {
			t.Fatalf("discovery capacity skipped %d %s", status, raw)
		}
		f.server.mu.Lock()
		delete(f.server.limits, "discovery:"+f.channel)
		f.server.mu.Unlock()
	}
	var starts int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM channel_model_discoveries`).Scan(&starts); err != nil || starts != 0 || f.gets.Load() != 0 {
		t.Fatal("capacity rejection persisted/reached upstream", starts, f.gets.Load(), err)
	}
	authExec(t, f.pool, `CREATE FUNCTION reject_discovery_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='channel.discovery.completed' THEN RAISE EXCEPTION 'injected terminal audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_discovery_completion BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_discovery_completion()`)
	status, raw := f.discover(t)
	if status != 503 || !bytes.Contains(raw, []byte("discovery_pending")) || bytes.Contains(raw, []byte("new-model")) {
		t.Fatalf("audit failure delivered uncommitted directory %d %s", status, raw)
	}
	var failure struct {
		ID string `json:"discovery_id"`
	}
	_ = json.Unmarshal(raw, &failure)
	v, err := f.server.Catalog.ModelDiscovery(context.Background(), f.channel, failure.ID)
	if err != nil || v.Status != "running" || len(v.Models)+len(v.Missing) != 0 {
		t.Fatal("rolled back terminal appeared complete", v, err)
	}
	var results, audits int
	if err = f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM channel_model_discovery_results),(SELECT count(*) FROM audit_events WHERE action='channel.discovery.completed')`).Scan(&results, &audits); err != nil || results != 0 || audits != 0 || f.gets.Load() != 1 {
		t.Fatal("terminal audit was not atomic", results, audits, err)
	}
	f.assertUnchanged(t)
}

func TestDiscoveryUnpublishedChannelAndHistoryPagination(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"a"},{"id":"b"}]}`)
	})
	ctx := context.Background()
	var base string
	if err := f.pool.QueryRow(ctx, `SELECT base_url FROM channel_versions WHERE id=$1`, f.version).Scan(&base); err != nil {
		t.Fatal(err)
	}
	id, err := f.server.Catalog.SaveChannel(ctx, f.actor, "", catalog.ChannelInput{Name: "unpublished-directory", BaseURL: base, Secret: "directory-secret", AllowedCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	f.channel = id
	if err = f.pool.QueryRow(ctx, `SELECT current_version FROM channels WHERE id=$1`, id).Scan(&f.version); err != nil {
		t.Fatal(err)
	}
	status, raw := f.discover(t)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	v := discoveryView(t, raw)
	if v.Stale || len(v.Unbound) != 2 || len(v.Present)+len(v.Missing) != 0 {
		t.Fatal("discovery required a published model", v)
	}
	for i := 0; i < 51; i++ {
		target, err := f.server.Catalog.BeginModelDiscovery(ctx, f.actor, id, f.version, "openai")
		if err != nil {
			t.Fatal(err)
		}
		if err = f.server.Catalog.FinishModelDiscovery(ctx, target.ID, provider.DiscoveryResult{Models: []string{}, Pages: 1}, ""); err != nil {
			t.Fatal(err)
		}
	}
	type envelope struct {
		Data []map[string]any `json:"data"`
		Next string           `json:"next_cursor"`
	}
	status, raw = f.call(t, "GET", f.path("/model-discoveries"), "", f.session, "")
	var first envelope
	if status != 200 || json.Unmarshal(raw, &first) != nil || len(first.Data) != 50 || first.Next == "" {
		t.Fatal("first discovery page", status, string(raw))
	}
	status, raw = f.call(t, "GET", f.path("/model-discoveries?cursor="+first.Next), "", f.session, "")
	var second envelope
	if status != 200 || json.Unmarshal(raw, &second) != nil || len(second.Data) != 2 || second.Next != "" {
		t.Fatal("second discovery page", status, string(raw))
	}
	seen := map[string]bool{}
	for _, row := range append(first.Data, second.Data...) {
		value := row["id"].(string)
		if seen[value] {
			t.Fatal("discovery page repeated identity")
		}
		seen[value] = true
	}
	if f.gets.Load() != 1 {
		t.Fatal("history pagination invoked provider")
	}
	f.assertUnchanged(t)
}
