package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

func TestBrowserQARejectsNonTestOrNonLocalDatabase(t *testing.T) {
	for _, raw := range []string{"", "not-a-postgres-url", "postgres://user:private-marker@127.0.0.1:55439/moni_review", "postgres://user:private-marker@203.0.113.8/moni_test", "postgres://user:private-marker@/moni_test?host=/tmp", "postgres://user:private-marker@127.0.0.1:55439/moni_test?host=127.0.0.1,203.0.113.8"} {
		if _, err := testDatabase(raw); err == nil || strings.Contains(err.Error(), "private-marker") || strings.Contains(err.Error(), "postgres://") {
			t.Fatal("unsafe database accepted or reflected")
		}
	}
	cfg, err := testDatabase("postgres://synthetic@localhost:55439/moni_test?sslmode=disable&search_path=public&role=owner&options=-c%20role%3Downer")
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.RuntimeParams["role"] != "" || cfg.ConnConfig.RuntimeParams["search_path"] != "" || cfg.ConnConfig.RuntimeParams["options"] != "" {
		t.Fatal("runtime overrides leaked into isolated owner configuration", err)
	}
}

func TestBrowserQAFixtureModesMustBeSeparate(t *testing.T) {
	for _, in := range []options{
		{identityFixture: true, voucherFixture: true},
		{identityFixture: true, alertsFixture: true},
		{voucherFixture: true, alertsFixture: true},
	} {
		var output bytes.Buffer
		if err := run(context.Background(), in, &output); err == nil || err.Error() != "fixture_modes_must_be_separate" || output.Len() != 0 {
			t.Fatal("mixed fixture reached resource creation")
		}
	}
}

func TestBrowserQARejectsReviewPortAndCreatesPrivateFiles(t *testing.T) {
	for _, raw := range []string{"127.0.0.1:58123", "0.0.0.0:58124", "203.0.113.8:58124", "localhost:58124", "127.0.0.1:058124"} {
		if _, err := appAddress(raw); err == nil {
			t.Fatal("unsafe application address accepted")
		}
	}
	addr, err := appAddress("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(addr)
	if host != "127.0.0.1" || port == "0" || port == "58123" {
		t.Fatal("selected address was not an isolated loopback endpoint")
	}
	listener, err := loopbackListener("127.0.0.1:0", port)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if listener.Addr().String() == addr {
		t.Fatal("mock acquired the reserved application endpoint")
	}
	root := t.TempDir()
	folder, err := privateDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, "manifest.json")
	if err = writeManifest(path, manifest{RunID: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{{folder, 0700}, {path, 0600}} {
		info, err := os.Stat(entry.path)
		if err != nil || info.Mode().Perm() != entry.mode {
			t.Fatal("private artifact permissions", err)
		}
	}
	unsafeRoot := t.TempDir()
	if err = os.Symlink(t.TempDir(), filepath.Join(unsafeRoot, ".local")); err != nil {
		t.Fatal(err)
	}
	if _, err = privateDirectory(unsafeRoot); err == nil {
		t.Fatal("symlink artifact parent was accepted")
	}
}

func TestBrowserQAChildOutputIsBoundedAndCannotReflectSecrets(t *testing.T) {
	var out bytes.Buffer
	l := &childLog{w: &out}
	secret := "synthetic-private-marker"
	for _, line := range []string{
		"postgres://owner:" + secret + "@127.0.0.1/moni_test\n",
		`{"stage":"http","code":"200","password":"` + secret + `","url":"http://` + secret + `"}` + "\n",
		`{"stage":"http","code":"2xy","request_id":"` + secret + `"}` + "\n",
		`{"stage":"startup","code":"` + secret + `"}` + "\n",
		strings.Repeat(secret, 1000) + "\n",
	} {
		for _, part := range []string{line[:len(line)/2], line[len(line)/2:]} {
			l.Write([]byte(part))
		}
	}
	if strings.Contains(out.String(), secret) || strings.Contains(out.String(), "postgres://") || strings.Contains(out.String(), "http://") || strings.Contains(out.String(), "2xy") {
		t.Fatal("untrusted child output escaped sanitization")
	}
	if !strings.Contains(out.String(), `"code":"200"`) || !strings.Contains(out.String(), `"code":"started"`) {
		t.Fatal("safe operational metadata was lost")
	}
	l.written = (8 << 20) - 1
	size := out.Len()
	l.Write([]byte("arbitrary secret\n"))
	if out.Len() != size || len(l.pending) > 16384 {
		t.Fatal("child log exceeded bounded storage")
	}
}

func TestBrowserQAMockRealProviderJSONSSEAndProbeUsage(t *testing.T) {
	m := &mockProvider{keys: []string{"synthetic-primary", "synthetic-secondary"}}
	upstream := httptest.NewServer(m)
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, stream := range []bool{false, true} {
		profile := provider.Profile{Version: "qa-fixture", Model: "qa-chat", Protocol: provider.ProtocolChat, ContextLimit: 256, InputTokenUpperBound: 128, OutputLimit: 64, DefaultOutputLimit: 16, OutputLimitField: "max_tokens", UsageSemantics: "final"}
		payload, _ := json.Marshal(map[string]any{"model": "qa-chat", "stream": stream, "max_tokens": 16, "messages": []map[string]string{{"role": "user", "content": "synthetic QA"}}})
		req, err := provider.Parse(provider.ProtocolChat, payload, profile)
		if err != nil {
			t.Fatal(err)
		}
		_, port, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
		n, _ := strconv.ParseUint(port, 10, 16)
		response, err := provider.NewClient(provider.ClientOptions{}).Do(ctx, provider.Endpoint{URL: upstream.URL + "/v1/chat/completions", APIKey: "synthetic-primary", Model: "qa-chat", Network: provider.NetworkPolicy{AllowHTTP: true, AllowedCIDRs: []string{"127.0.0.1/32"}, AllowedPorts: []uint16{uint16(n)}}}, req)
		if err != nil {
			t.Fatal(err)
		}
		if stream {
			for {
				event, e := response.ReadEvent()
				if e != nil {
					t.Fatal(e)
				}
				if event.Kind == "done" {
					break
				}
			}
		} else if _, err = response.DecodeJSON(); err != nil {
			t.Fatal(err)
		}
		if response.Usage.Status != provider.UsageConfirmed || response.Usage.InputTokens != 4 || response.Usage.OutputTokens != 2 || response.Usage.TotalTokens != 6 || response.ProtocolOutcome != "completed" {
			t.Fatal("mock was not accepted by the actual provider evidence path", response.Usage)
		}
		response.Close()
	}
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"qa-chat","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`))
	request.Header.Set("Authorization", "Bearer synthetic-secondary")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, request)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"completion_tokens":1`) || !strings.Contains(w.Body.String(), `"total_tokens":5`) {
		t.Fatal("one-token platform probe exceeded its profile")
	}
	request = httptest.NewRequest("GET", "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer synthetic-primary")
	w = httptest.NewRecorder()
	m.ServeHTTP(w, request)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"qa-chat-alt"`) {
		t.Fatal("mock directory was not provided")
	}
	request.Header.Add("Authorization", "Bearer synthetic-secondary")
	w = httptest.NewRecorder()
	m.ServeHTTP(w, request)
	if w.Code != 401 {
		t.Fatal("ambiguous synthetic authorization was accepted")
	}
	request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`invalid-json`))
	request.Header.Set("Authorization", "Bearer synthetic-primary")
	w = httptest.NewRecorder()
	m.ServeHTTP(w, request)
	if m.counts().ChatPOSTs != 4 || m.counts().ChatJSONPOSTs != 2 || m.counts().ChatSSEPOSTs != 1 || m.counts().DirectoryGETs != 1 || m.counts().AuthRejected != 1 {
		t.Fatal("mock HTTP counters do not separate actual posts and accepted forms", m.counts())
	}
}

type safeBuffer struct {
	sync.Mutex
	b bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) { b.Lock(); defer b.Unlock(); return b.b.Write(p) }

// The real-main test is explicit: main's single-instance advisory lock is
// database-wide, so it must not run beside another browser QA/main process.
func TestBrowserQARealMainLifecycle(t *testing.T) {
	if os.Getenv("BROWSER_QA_REAL_MAIN_TEST") != "1" {
		t.Skip("explicit isolated main lifecycle opt-in required")
	}
	cfg, err := testDatabase(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	binary, err := filepath.Abs(filepath.Join("..", "..", "bin", "moni-aigate"))
	if err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 4; repeat++ {
		root := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var out safeBuffer
		done := make(chan error, 1)
		identityFixture := repeat == 1
		voucherFixture := repeat == 2
		alertsFixture := repeat == 3
		go func() {
			done <- run(ctx, options{binary: binary, listen: "127.0.0.1:0", root: root, database: os.Getenv("TEST_DATABASE_URL"), identityFixture: identityFixture, voucherFixture: voucherFixture, alertsFixture: alertsFixture}, &out)
		}()
		var m manifest
		var path string
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			paths, _ := filepath.Glob(filepath.Join(root, ".local", "browser-qa", "run-*", "manifest.json"))
			if len(paths) == 1 {
				path = paths[0]
				raw, _ := os.ReadFile(path)
				_ = json.Unmarshal(raw, &m)
				if m.Status == "ready" {
					break
				}
			}
			select {
			case e := <-done:
				t.Fatal("real main ended before ready", e)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
		if m.Status != "ready" {
			t.Fatal("real main readiness deadline")
		}
		var tenants, keys, requests, credits, users int
		conn, err := admin.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Exec(context.Background(), "SET search_path TO "+m.Schema); err != nil {
			t.Fatal(err)
		}
		err = conn.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM tenants),(SELECT count(*) FROM api_keys),(SELECT count(*) FROM request_records),(SELECT count(*) FROM credit_orders),(SELECT count(*) FROM users)`).Scan(&tenants, &keys, &requests, &credits, &users)
		conn.Release()
		if err != nil || tenants != 0 || keys != 0 || requests != 0 || credits != 0 || users != 3 {
			t.Fatal("harness fabricated browser business facts", err)
		}
		if identityFixture {
			request, err := http.NewRequest(http.MethodPost, m.AppOrigin+"/api/auth/registration/request", strings.NewReader(`{"email":"lifecycle@moni.invalid"}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", m.AppOrigin)
			request.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("identity fixture request failed")
			}
			response.Body.Close()
			if response.StatusCode != http.StatusAccepted {
				t.Fatal("identity fixture request rejected")
			}
			mailbox := filepath.Join(filepath.Dir(path), "mailbox-private.json")
			captured := false
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				body, readErr := os.ReadFile(mailbox)
				var messages []fixtureMail
				if readErr == nil && json.Unmarshal(body, &messages) == nil && len(messages) == 1 {
					captured = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if info, err := os.Stat(mailbox); !captured || err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("identity fixture did not privately capture mail")
			}
		}
		if voucherFixture || alertsFixture {
			loginBody, _ := json.Marshal(map[string]string{"email": m.Accounts["admin"].Email, "password": m.Accounts["admin"].Password})
			request, err := http.NewRequest(http.MethodPost, m.AppOrigin+"/api/auth/login", bytes.NewReader(loginBody))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", m.AppOrigin)
			request.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("voucher fixture login failed")
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("voucher fixture login rejected")
			}
			policyPath := "/api/platform/voucher-policy"
			if alertsFixture {
				policyPath = "/api/platform/alerts/capabilities"
			}
			request, err = http.NewRequest(http.MethodGet, m.AppOrigin+policyPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, cookie := range response.Cookies() {
				request.AddCookie(cookie)
			}
			response, err = client.Do(request)
			if err != nil {
				t.Fatal("voucher fixture policy unavailable")
			}
			var policy struct {
				Data struct {
					RuntimeAllowed      bool `json:"runtime_allowed"`
					Enabled             bool `json:"enabled"`
					EffectiveEnabled    bool `json:"effective_enabled"`
					DeploymentEnabled   bool `json:"deployment_enabled"`
					EvaluationAvailable bool `json:"evaluation_available"`
				} `json:"data"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&policy)
			response.Body.Close()
			if voucherFixture && (response.StatusCode != 200 || decodeErr != nil || !m.VoucherFixture || !policy.Data.RuntimeAllowed || policy.Data.Enabled || policy.Data.EffectiveEnabled) {
				t.Fatal("voucher fixture did not retain runtime ceiling on and DB policy off")
			}
			if alertsFixture {
				if response.StatusCode != 200 || decodeErr != nil || !m.AlertsFixture || !policy.Data.DeploymentEnabled || !policy.Data.EvaluationAvailable {
					t.Fatal("alert fixture did not enable isolated evaluations")
				}
				conn, err := admin.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				_, err = conn.Exec(context.Background(), "SET search_path TO "+m.Schema)
				var rules, episodes int
				if err == nil {
					err = conn.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM alert_rules),(SELECT count(*) FROM alert_episodes)`).Scan(&rules, &episodes)
				}
				conn.Release()
				if err != nil || rules != 0 || episodes != 0 {
					t.Fatal("alert fixture fabricated alert facts", err)
				}
			}
		}
		cancel()
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("real main did not stop and clean")
		}
		raw, err := os.ReadFile(path)
		m = manifest{}
		if err != nil || json.Unmarshal(raw, &m) != nil || m.Status != "stopped" || m.ServeShutdown != "graceful" || len(m.Remaining) != 0 || !m.CounterReadValid {
			t.Fatal("unclean lifecycle manifest", err)
		}
		if strings.Contains(string(raw), `"password"`) || m.Provider.Secret != "" || m.Provider.SecondarySecret != "" {
			t.Fatal("stopped manifest retained one-time login credentials")
		}
		if m.BusinessCounts.CreditOrders != 0 || m.BusinessCounts.FundingReceipts != 0 || m.BusinessCounts.VoucherRedemptions != 0 || m.BusinessCounts.CommerceCommands != 0 {
			t.Fatal("lifecycle fabricated commerce facts")
		}
		if identityFixture {
			if !m.IdentityFixture || !m.MailboxRemoved || m.MailAccepted != 1 {
				t.Fatal("identity fixture did not stop and erase its private mailbox")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), "mailbox-private.json")); !os.IsNotExist(err) {
				t.Fatal("identity fixture mailbox remained after shutdown")
			}
			connection, err := net.DialTimeout("tcp", m.MailAddress, time.Second)
			if err == nil {
				connection.Close()
				t.Fatal("identity fixture SMTP listener remained after shutdown")
			}
		}
		var remaining bool
		if err = admin.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1) OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($2::text[]))`, m.Schema, []string{m.AppRole, m.ProjectionRole}).Scan(&remaining); err != nil || remaining {
			t.Fatal("lifecycle left isolated database resources", err)
		}
		response, e := http.Get(m.AppOrigin + "/health/ready")
		if e == nil {
			response.Body.Close()
			t.Fatal("stopped real main still has a listener")
		}
	}
}

func TestBrowserQAFailedBootstrapCleansOwnedResources(t *testing.T) {
	if os.Getenv("BROWSER_QA_ERROR_TEST") != "1" {
		t.Skip("explicit PostgreSQL lifecycle opt-in required")
	}
	cfg, err := testDatabase(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "failed-bootstrap")
	if err = os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = run(context.Background(), options{binary: binary, listen: "127.0.0.1:0", root: root, database: os.Getenv("TEST_DATABASE_URL")}, io.Discard); err == nil || err.Error() != "bootstrap_failed" {
		t.Fatal("failure injection was not reached", err)
	}
	paths, _ := filepath.Glob(filepath.Join(root, ".local", "browser-qa", "run-*", "manifest.json"))
	if len(paths) != 1 {
		t.Fatal("failed setup did not retain a safe cleanup record")
	}
	var m manifest
	raw, _ := os.ReadFile(paths[0])
	if json.Unmarshal(raw, &m) != nil || m.Status != "stopped" || len(m.Remaining) != 0 {
		t.Fatal("failed bootstrap left an incomplete cleanup")
	}
	admin, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var remaining bool
	if err = admin.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1) OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($2::text[]))`, m.Schema, []string{m.AppRole, m.ProjectionRole}).Scan(&remaining); err != nil || remaining {
		t.Fatal("startup failure leaked database resources", err)
	}
}
