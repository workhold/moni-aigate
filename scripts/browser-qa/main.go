// browser-qa runs the real binary against disposable, synthetic local resources.
// Normal CLI runs never create channels, tenants, funds, keys or inference facts.
// Private opt-in lifecycle tests may create isolated synthetic business facts.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

type options struct {
	binary, listen, root, database string
	identityFixture                bool
	voucherFixture                 bool
	alertsFixture                  bool
	// Private test seams are never populated by CLI flags or environment.
	mailFixtureReady func(context.Context, *mailFixture) error
	mailLifecycle    func(context.Context, *privateLifecycle) error
	modelLifecycle   func(context.Context, *privateModelLifecycle) error
}
type account struct {
	Email    string `json:"email"`
	Password string `json:"password,omitempty"`
	Role     string `json:"role"`
}
type mockCounters struct {
	DirectoryGETs uint64 `json:"directory_gets"`
	ChatPOSTs     uint64 `json:"chat_posts"`
	ChatJSONPOSTs uint64 `json:"chat_json_posts"`
	ChatSSEPOSTs  uint64 `json:"chat_sse_posts"`
	AuthRejected  uint64 `json:"auth_rejected"`
	Unsupported   uint64 `json:"unsupported"`
}
type businessCounters struct {
	Probes             int64 `json:"platform_probe_attempts"`
	Attempts           int64 `json:"customer_upstream_attempts"`
	Requests           int64 `json:"customer_requests"`
	Settlements        int64 `json:"settlements"`
	CreditOrders       int64 `json:"credit_orders"`
	FundingReceipts    int64 `json:"funding_receipts"`
	VoucherRedemptions int64 `json:"voucher_redemptions"`
	CommerceCommands   int64 `json:"commerce_commands"`
	AlertRules         int64 `json:"alert_rules"`
	AlertEpisodes      int64 `json:"alert_episodes"`
	AlertEvents        int64 `json:"alert_events"`
	AlertCommands      int64 `json:"alert_commands"`
}
type manifest struct {
	IdentityFixture  bool               `json:"identity_fixture"`
	VoucherFixture   bool               `json:"voucher_fixture"`
	AlertsFixture    bool               `json:"alerts_fixture"`
	MailAddress      string             `json:"mail_address,omitempty"`
	MailAccepted     int                `json:"mail_accepted"`
	MailboxRemoved   bool               `json:"mailbox_removed"`
	RunID            string             `json:"run_id"`
	Status           string             `json:"status"`
	CreatedAt        time.Time          `json:"created_at"`
	AppOrigin        string             `json:"app_origin"`
	MockOrigin       string             `json:"mock_origin"`
	HarnessPID       int                `json:"harness_pid"`
	ServePID         int                `json:"serve_pid,omitempty"`
	Binary           string             `json:"binary"`
	BinarySHA256     string             `json:"binary_sha256"`
	Schema           string             `json:"schema"`
	Database         string             `json:"database"`
	AppRole          string             `json:"app_role"`
	ProjectionRole   string             `json:"projection_role"`
	Accounts         map[string]account `json:"accounts"`
	Provider         providerManifest   `json:"provider"`
	MockCounts       mockCounters       `json:"mock_counts"`
	BusinessCounts   businessCounters   `json:"business_counts"`
	CounterSource    string             `json:"counter_source"`
	CounterReadValid bool               `json:"counter_read_valid"`
	CounterReadAt    time.Time          `json:"counter_read_at,omitempty"`
	ServeShutdown    string             `json:"serve_shutdown,omitempty"`
	Remaining        []string           `json:"remaining_resources,omitempty"`
}
type providerManifest struct {
	BaseURL            string         `json:"base_url"`
	Secret             string         `json:"secret,omitempty"`
	SecondarySecret    string         `json:"secondary_secret,omitempty"`
	AllowedCIDRs       []string       `json:"allowed_cidrs"`
	Models             []string       `json:"upstream_models"`
	ResponseText       string         `json:"response_text"`
	ExpectedUsage      map[string]int `json:"expected_chat_usage"`
	ExpectedProbeUsage map[string]int `json:"expected_probe_usage"`
	SuggestedProfile   map[string]any `json:"suggested_profile"`
}

func main() {
	flags := flag.NewFlagSet("browser-qa", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	binary := flags.String("binary", "bin/moni-aigate", "already-built application binary")
	listen := flags.String("listen", "127.0.0.1:0", "loopback address; port zero selects a new port")
	identityFixture := flags.Bool("identity-fixture", false, "enable only isolated loopback SMTP and self-service identity fixture")
	voucherFixture := flags.Bool("voucher-fixture", false, "enable only isolated voucher runtime keys; DB policy remains off")
	alertsFixture := flags.Bool("alerts-fixture", false, "enable only isolated in-app alert evaluations; no rules or business facts are created")
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "browser_qa_failed: invalid_arguments")
		os.Exit(1)
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "browser_qa_failed: workspace_unavailable")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = run(ctx, options{binary: *binary, listen: *listen, root: root, database: os.Getenv("TEST_DATABASE_URL"), identityFixture: *identityFixture, voucherFixture: *voucherFixture, alertsFixture: *alertsFixture}, os.Stdout); err != nil {
		// run returns only fixed stage codes, never raw driver/child errors.
		fmt.Fprintln(os.Stderr, "browser_qa_failed:", err)
		os.Exit(1)
	}
}

func stage(code string) error { return errors.New(code) }

func testDatabase(raw string) (*pgxpool.Config, error) {
	if raw == "" {
		return nil, stage("explicit_TEST_DATABASE_URL_required")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User == "" {
		return nil, stage("only_local_moni_test_allowed")
	}
	local := func(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }
	if cfg.ConnConfig.Host == "localhost" {
		cfg.ConnConfig.Host = "127.0.0.1"
	}
	if !local(cfg.ConnConfig.Host) {
		return nil, stage("only_local_moni_test_allowed")
	}
	for _, fallback := range cfg.ConnConfig.Fallbacks {
		if fallback.Host == "localhost" {
			fallback.Host = "127.0.0.1"
		}
		if !local(fallback.Host) {
			return nil, stage("only_local_moni_test_allowed")
		}
	}
	cfg.ConnConfig.RuntimeParams = map[string]string{"application_name": "moni_browser_qa_owner", "statement_timeout": "5000", "lock_timeout": "2000"}
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.MaxConns = 3
	return cfg, nil
}

func privateDirectory(root string) (string, error) {
	for _, path := range []string{filepath.Join(root, ".local"), filepath.Join(root, ".local", "browser-qa")} {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", stage("private_directory_unavailable")
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", stage("private_directory_unavailable")
		}
	}
	path, err := os.MkdirTemp(filepath.Join(root, ".local", "browser-qa"), "run-")
	if err != nil || os.Chmod(path, 0700) != nil {
		return "", stage("private_directory_unavailable")
	}
	return path, nil
}

func writeManifest(path string, value manifest) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return stage("manifest_write_failed")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".manifest-")
	if err != nil {
		return stage("manifest_write_failed")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(body, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil || os.Rename(tmp, path) != nil {
		return stage("manifest_write_failed")
	}
	return nil
}

func randomBytes(n int) ([]byte, error) {
	value := make([]byte, n)
	if _, err := rand.Read(value); err != nil {
		return nil, stage("random_source_failed")
	}
	return value, nil
}

func randomPassword() (string, error) {
	value, err := randomBytes(24)
	return base64.RawURLEncoding.EncodeToString(value), err
}

func roleURL(cfg *pgxpool.Config, role, password, schema string) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(role, password), Host: net.JoinHostPort(cfg.ConnConfig.Host, strconv.Itoa(int(cfg.ConnConfig.Port))), Path: "/moni_test"}
	q := url.Values{"sslmode": {"disable"}, "search_path": {schema}, "application_name": {"moni_browser_qa_" + role}}
	u.RawQuery = q.Encode()
	return u.String()
}

type mockProvider struct {
	keys                                                             []string
	directory, posts, jsonPosts, ssePosts, authRejected, unsupported atomic.Uint64
}

func (m *mockProvider) counts() mockCounters {
	return mockCounters{m.directory.Load(), m.posts.Load(), m.jsonPosts.Load(), m.ssePosts.Load(), m.authRejected.Load(), m.unsupported.Load()}
}

func (m *mockProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	authorized := false
	if len(r.Header.Values("Authorization")) == 1 {
		for _, key := range m.keys {
			authorized = subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) == 1 || authorized
		}
	}
	if !authorized {
		m.authRejected.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"code":"invalid_api_key","message":"Synthetic credential required"}}`)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" && r.URL.RawQuery == "" {
		m.directory.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"qa-chat","object":"model","owned_by":"local-qa"},{"id":"qa-chat-alt","object":"model","owned_by":"local-qa"}]}`)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.URL.RawQuery != "" {
		m.unsupported.Add(1)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var in struct {
		Model               string          `json:"model"`
		Stream              bool            `json:"stream"`
		MaxTokens           int64           `json:"max_tokens"`
		MaxCompletionTokens int64           `json:"max_completion_tokens"`
		Messages            json.RawMessage `json:"messages"`
	}
	m.posts.Add(1)
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || (in.Model != "qa-chat" && in.Model != "qa-chat-alt") || len(in.Messages) == 0 || string(in.Messages) == "null" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	output, text := 2, "QA OK"
	if in.MaxTokens == 1 || in.MaxCompletionTokens == 1 {
		output, text = 1, "QA"
	}
	usage := map[string]int{"prompt_tokens": 4, "completion_tokens": output, "total_tokens": 4 + output}
	if in.Stream {
		m.ssePosts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		write(map[string]any{"id": "chatcmpl_local_qa", "object": "chat.completion.chunk", "model": in.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}, "finish_reason": nil}}})
		write(map[string]any{"id": "chatcmpl_local_qa", "object": "chat.completion.chunk", "model": in.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
		write(map[string]any{"id": "chatcmpl_local_qa", "object": "chat.completion.chunk", "model": in.Model, "choices": []any{}, "usage": usage})
		io.WriteString(w, "data: [DONE]\n\n")
		return
	}
	m.jsonPosts.Add(1)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl_local_qa", "object": "chat.completion", "model": in.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": usage})
}

// childLog deliberately drops arbitrary child output and unknown JSON fields.
// It cannot persist a URL, SQL error, credential, request body or Cookie.
type childLog struct {
	mu      sync.Mutex
	w       io.Writer
	pending []byte
	written int
}

func (l *childLog) Write(body []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range body {
		if b != '\n' {
			if len(l.pending) < 16384 {
				l.pending = append(l.pending, b)
			}
			continue
		}
		var record struct {
			Stage     string `json:"stage"`
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
			Duration  int64  `json:"duration_ms"`
		}
		code, stageName := "output_redacted", "child"
		if json.Unmarshal(l.pending, &record) == nil {
			status, statusErr := strconv.Atoi(record.Code)
			if record.Stage == "http" && statusErr == nil && status >= 100 && status <= 599 && strconv.Itoa(status) == record.Code {
				code, stageName = record.Code, "http"
			} else if record.Stage == "startup" {
				code, stageName = "started", "startup"
			} else if (record.Stage == "recovery" || record.Stage == "projection") && record.Code == "worker_failed" {
				code, stageName = "worker_failed", record.Stage
			}
		}
		l.pending = l.pending[:0]
		out := map[string]any{"stage": stageName, "code": code}
		if id, err := uuid.Parse(record.RequestID); err == nil && id.String() == record.RequestID {
			out["request_id"] = record.RequestID
		}
		if record.Duration > 0 && record.Duration <= 600000 {
			out["duration_ms"] = record.Duration
		}
		data, _ := json.Marshal(out)
		if l.written+len(data)+1 <= 8<<20 {
			n, _ := l.w.Write(append(data, '\n'))
			l.written += n
		}
	}
	return len(body), nil
}

func loopbackListener(raw, excludedPort string) (net.Listener, error) {
	for retry := 0; retry < 8; retry++ {
		listener, err := net.Listen("tcp4", raw)
		if err != nil {
			return nil, stage("loopback_port_unavailable")
		}
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		if port != "58123" && port != excludedPort {
			return listener, nil
		}
		listener.Close()
		if !strings.HasSuffix(raw, ":0") {
			break
		}
	}
	return nil, stage("distinct_loopback_port_unavailable")
}

func appAddress(raw string) (string, error) {
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host != "127.0.0.1" || port == "58123" {
		return "", stage("only_distinct_loopback_app_address_allowed")
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || strconv.FormatUint(n, 10) != port {
		return "", stage("invalid_app_port")
	}
	listener, err := loopbackListener(net.JoinHostPort(host, port), "")
	if err != nil {
		return "", stage("app_port_unavailable")
	}
	addr := listener.Addr().String()
	listener.Close()
	return addr, nil
}

func run(ctx context.Context, in options, stdout io.Writer) (result error) {
	fixtureModes := 0
	for _, enabled := range []bool{in.identityFixture, in.voucherFixture, in.alertsFixture} {
		if enabled {
			fixtureModes++
		}
	}
	if fixtureModes > 1 {
		return stage("fixture_modes_must_be_separate")
	}
	if (in.mailFixtureReady != nil || in.mailLifecycle != nil) && !in.identityFixture {
		return stage("mail_lifecycle_requires_identity_fixture")
	}
	if in.modelLifecycle != nil && fixtureModes != 0 {
		return stage("model_lifecycle_requires_separate_fixture")
	}
	baseConfig, err := testDatabase(in.database)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(in.binary) {
		in.binary = filepath.Join(in.root, in.binary)
	}
	info, err := os.Stat(in.binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return stage("built_application_binary_required")
	}
	binaryFile, err := os.Open(in.binary)
	if err != nil {
		return stage("binary_hash_failed")
	}
	hash := sha256.New()
	_, err = io.Copy(hash, binaryFile)
	binaryFile.Close()
	if err != nil {
		return stage("binary_hash_failed")
	}
	addr, err := appAddress(in.listen)
	if err != nil {
		return err
	}
	folder, err := privateDirectory(in.root)
	if err != nil {
		return err
	}
	path := filepath.Join(folder, "manifest.json")
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	m := manifest{RunID: uuid.NewString(), Status: "starting", CreatedAt: time.Now().UTC(), HarnessPID: os.Getpid(), Binary: in.binary, BinarySHA256: hex.EncodeToString(hash.Sum(nil)), AppOrigin: "http://" + addr, Schema: "browser_qa_" + suffix, Database: "moni_test", AppRole: "bqa_app_" + suffix, ProjectionRole: "bqa_projection_" + suffix, Accounts: map[string]account{}, CounterSource: "mock counts are actual authenticated HTTP requests; platform/customer attempt counts come from separate authoritative PostgreSQL tables"}
	var admin, owner *pgxpool.Pool
	var mock *http.Server
	var mockListener net.Listener
	var mockState *mockProvider
	var smtpFixture *mailFixture
	var modelFixture *lifecycleModelFixture
	var child *exec.Cmd
	var childDone chan error
	var childExited bool
	var logFile *os.File
	var schemaCreated bool
	createdRoles := []string{}
	defer func() {
		remaining := []string{}
		if modelFixture != nil {
			modelFixture.close()
		}
		m.ServeShutdown = "not_started"
		if child != nil && child.Process != nil && !childExited {
			child.Process.Signal(syscall.SIGTERM)
			select {
			case childErr := <-childDone:
				childExited = true
				m.ServeShutdown = "graceful"
				if childErr != nil {
					m.ServeShutdown = "exited_with_error"
					if result == nil {
						result = stage("serve_shutdown_failed")
					}
				}
			case <-time.After(12 * time.Second):
				m.ServeShutdown = "forced"
				if result == nil {
					result = stage("serve_forced_stop")
				}
				child.Process.Kill()
				select {
				case <-childDone:
					childExited = true
				case <-time.After(3 * time.Second):
					remaining = append(remaining, fmt.Sprintf("serve_pid:%d", child.Process.Pid))
				}
			}
		} else if child != nil && child.Process != nil {
			m.ServeShutdown = "already_exited"
		}
		if mock != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if mock.Shutdown(shutdown) != nil {
				mock.Close()
			}
			cancel()
		} else if mockListener != nil {
			mockListener.Close()
		}
		if mockState != nil {
			m.MockCounts = mockState.counts()
		}
		if smtpFixture != nil {
			var closeErr error
			m.MailAccepted, closeErr = smtpFixture.close()
			m.MailboxRemoved = closeErr == nil
			if closeErr != nil {
				remaining = append(remaining, "private_mailbox")
			}
		}
		if owner != nil {
			read, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			counterErr := owner.QueryRow(read, `SELECT (SELECT count(*) FROM platform_probe_attempts),(SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM request_records),(SELECT count(*) FROM settlements),(SELECT count(*) FROM credit_orders),(SELECT count(*) FROM funding_receipts),(SELECT count(*) FROM voucher_redemptions),(SELECT count(*) FROM commerce_commands),(SELECT count(*) FROM alert_rules),(SELECT count(*) FROM alert_episodes),(SELECT count(*) FROM alert_episode_events),(SELECT count(*) FROM alert_commands)`).Scan(&m.BusinessCounts.Probes, &m.BusinessCounts.Attempts, &m.BusinessCounts.Requests, &m.BusinessCounts.Settlements, &m.BusinessCounts.CreditOrders, &m.BusinessCounts.FundingReceipts, &m.BusinessCounts.VoucherRedemptions, &m.BusinessCounts.CommerceCommands, &m.BusinessCounts.AlertRules, &m.BusinessCounts.AlertEpisodes, &m.BusinessCounts.AlertEvents, &m.BusinessCounts.AlertCommands)
			m.CounterReadValid = counterErr == nil
			if m.CounterReadValid {
				m.CounterReadAt = time.Now().UTC()
			}
			cancel()
			owner.Close()
		}
		if admin != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			if child == nil || child.Process == nil || childExited {
				if schemaCreated {
					if _, e := admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{m.Schema}.Sanitize()+" CASCADE"); e != nil {
						remaining = append(remaining, "schema:"+m.Schema)
					}
				}
				for _, role := range createdRoles {
					if _, e := admin.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); e != nil {
						remaining = append(remaining, "role:"+role)
						continue
					}
					if _, e := admin.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); e != nil {
						remaining = append(remaining, "role:"+role)
					}
				}
			} else {
				remaining = append(remaining, "schema:"+m.Schema)
				for _, role := range createdRoles {
					remaining = append(remaining, "role:"+role)
				}
			}
			cancel()
			admin.Close()
		}
		if logFile != nil {
			logFile.Close()
		}
		for key, a := range m.Accounts {
			a.Password = ""
			m.Accounts[key] = a
		}
		m.Provider.Secret, m.Provider.SecondarySecret = "", ""
		m.Status, m.Remaining = "stopped", remaining
		if len(remaining) > 0 {
			m.Status = "cleanup_failed"
			result = stage("cleanup_failed")
		}
		if writeManifest(path, m) != nil {
			result = stage("final_manifest_write_failed")
		}
		fmt.Fprintf(stdout, "status=%s manifest=%s", m.Status, path)
		if len(remaining) > 0 {
			fmt.Fprintf(stdout, " remaining=%s", strings.Join(remaining, ","))
		}
		fmt.Fprintln(stdout)
	}()
	if err = writeManifest(path, m); err != nil {
		return err
	}
	setup, cancelSetup := context.WithTimeout(ctx, 35*time.Second)
	defer cancelSetup()
	admin, err = pgxpool.NewWithConfig(setup, baseConfig)
	if err != nil || admin.Ping(setup) != nil {
		return stage("test_database_unavailable")
	}
	if _, err = admin.Exec(setup, "CREATE SCHEMA "+pgx.Identifier{m.Schema}.Sanitize()); err != nil {
		return stage("schema_creation_failed")
	}
	schemaCreated = true
	appPassword, err := randomPassword()
	if err != nil {
		return err
	}
	projectionPassword, err := randomPassword()
	if err != nil {
		return err
	}
	for _, role := range []struct{ name, password string }{{m.AppRole, appPassword}, {m.ProjectionRole, projectionPassword}} {
		// Passwords are generated base64url; quote defensively nevertheless.
		statement := "CREATE ROLE " + pgx.Identifier{role.name}.Sanitize() + " LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD '" + strings.ReplaceAll(role.password, "'", "''") + "'"
		if _, err = admin.Exec(setup, statement); err != nil {
			return stage("role_creation_failed")
		}
		createdRoles = append(createdRoles, role.name)
	}
	ownerConfig := baseConfig.Copy()
	ownerConfig.ConnConfig.RuntimeParams["search_path"] = m.Schema
	owner, err = pgxpool.NewWithConfig(setup, ownerConfig)
	if err != nil {
		return stage("schema_connection_failed")
	}
	if migrations.Apply(setup, owner) != nil {
		return stage("schema_migration_failed")
	}
	if operations.ProvisionFor(setup, owner, m.Schema, m.AppRole, m.ProjectionRole) != nil {
		return stage("role_provisioning_failed")
	}
	appURL, projectionURL := roleURL(baseConfig, m.AppRole, appPassword, m.Schema), roleURL(baseConfig, m.ProjectionRole, projectionPassword, m.Schema)
	for _, role := range []struct{ url, name string }{{appURL, m.AppRole}, {projectionURL, m.ProjectionRole}} {
		p, e := pgxpool.New(setup, role.url)
		if e != nil {
			return stage("role_connection_failed")
		}
		var current, session string
		var privileged bool
		e = p.QueryRow(setup, `SELECT current_user,session_user,rolsuper OR rolcreaterole OR rolcreatedb FROM pg_roles WHERE rolname=current_user`).Scan(&current, &session, &privileged)
		p.Close()
		if e != nil || current != role.name || session != role.name || privileged {
			return stage("least_privilege_role_check_failed")
		}
	}
	master, err := randomBytes(32)
	if err != nil {
		return err
	}
	pepper, err := randomBytes(32)
	if err != nil {
		return err
	}
	secrets := &security.Secrets{Keys: map[string][]byte{"browserqa": master}, Active: "browserqa", Pepper: pepper}
	for key, role := range map[string]string{"admin": "admin", "finance_operator": "finance_operator", "finance_approver": "finance_approver"} {
		password, e := randomPassword()
		if e != nil {
			return e
		}
		m.Accounts[key] = account{Email: key + "-" + suffix + "@browser-qa.invalid", Password: password, Role: role}
	}
	key, err := randomPassword()
	if err != nil {
		return err
	}
	secondary, err := randomPassword()
	if err != nil {
		return err
	}
	mockState = &mockProvider{keys: []string{key, secondary}}
	_, appPort, _ := net.SplitHostPort(addr)
	mockListener, err = loopbackListener("127.0.0.1:0", appPort)
	if err != nil {
		return stage("mock_listen_failed")
	}
	m.MockOrigin = "http://" + mockListener.Addr().String()
	m.Provider = providerManifest{BaseURL: m.MockOrigin + "/v1", Secret: key, SecondarySecret: secondary, AllowedCIDRs: []string{"127.0.0.1/32"}, Models: []string{"qa-chat", "qa-chat-alt"}, ResponseText: "QA OK", ExpectedUsage: map[string]int{"input_tokens": 4, "output_tokens": 2, "total_tokens": 6}, ExpectedProbeUsage: map[string]int{"input_tokens": 4, "output_tokens": 1, "total_tokens": 5}, SuggestedProfile: map[string]any{"protocol": "openai_chat", "context_limit": 256, "input_token_upper_bound": 128, "output_limit": 64, "default_output_limit": 16, "output_limit_field": "max_tokens", "usage_semantics": "final"}}
	mock = &http.Server{Handler: mockState, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	if in.modelLifecycle != nil {
		modelFixture = newLifecycleModelFixture(ctx, mockState)
		mock.Handler = modelFixture
		mock.WriteTimeout = 320 * time.Second
	}
	go mock.Serve(mockListener)
	childEnv := []string{"PATH=" + os.Getenv("PATH"), "TZ=UTC", "DATABASE_URL=" + appURL, "PROJECTION_DATABASE_URL=" + projectionURL, "LISTEN_ADDR=" + addr, "PUBLIC_ORIGIN=" + m.AppOrigin, "COOKIE_SECURE=false", "RECOVERY_MODE=false", "MFA_ENFORCE_PLATFORM=false", "MASTER_KEY_ID=browserqa", "MASTER_KEY=" + base64.StdEncoding.EncodeToString(master), "AUTH_SECRET=" + base64.StdEncoding.EncodeToString(pepper)}
	if in.alertsFixture {
		m.AlertsFixture = true
		childEnv = append(childEnv, "OPERATIONS_ALERTS_ENABLED=true")
	}
	if in.voucherFixture {
		voucherKey, keyErr := randomBytes(32)
		if keyErr != nil {
			return keyErr
		}
		m.VoucherFixture = true
		childEnv = append(childEnv, "VOUCHERS_ENABLED=true", "VOUCHER_HMAC_ACTIVE_KEY=browserqa", `VOUCHER_HMAC_KEYS={"browserqa":"`+base64.RawURLEncoding.EncodeToString(voucherKey)+`"}`)
	}
	if in.identityFixture {
		smtpFixture, err = newMailFixtureContext(ctx, folder)
		if err != nil {
			return err
		}
		m.IdentityFixture = true
		m.MailAddress = smtpFixture.listener.Addr().String()
		mailHost, mailPort, splitErr := net.SplitHostPort(m.MailAddress)
		if splitErr != nil {
			return stage("mail_fixture_address_failed")
		}
		childEnv = append(childEnv, "AUTH_REGISTRATION_ENABLED=true", "AUTH_PASSWORD_RESET_ENABLED=true", "MAIL_TRANSPORT=smtp", "SMTP_HOST="+mailHost, "SMTP_PORT="+mailPort, "SMTP_FROM=fixture@moni.invalid", "SMTP_TLS_MODE=plain_loopback")
		if in.mailFixtureReady != nil {
			if err = in.mailFixtureReady(ctx, smtpFixture); err != nil {
				return stage("mail_fixture_hook_failed")
			}
		}
	}
	bootstrap := exec.CommandContext(setup, in.binary, "bootstrap", "--email", m.Accounts["admin"].Email, "--password-stdin")
	bootstrap.Dir, bootstrap.Env, bootstrap.Stdin = in.root, childEnv, strings.NewReader(m.Accounts["admin"].Password+"\n")
	bootstrap.Stdout, bootstrap.Stderr = io.Discard, io.Discard
	if bootstrap.Run() != nil {
		return stage("bootstrap_failed")
	}
	var adminID string
	if owner.QueryRow(setup, `SELECT id FROM users WHERE platform_role='admin'`).Scan(&adminID) != nil {
		return stage("bootstrap_verification_failed")
	}
	identities := identity.New(owner, secrets)
	for _, name := range []string{"finance_operator", "finance_approver"} {
		a := m.Accounts[name]
		if _, err = identities.CreateUser(setup, adminID, a.Email, a.Password, a.Role); err != nil {
			return stage("synthetic_identity_creation_failed")
		}
	}
	logFile, err = os.OpenFile(filepath.Join(folder, "serve.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return stage("private_log_creation_failed")
	}
	output := &childLog{w: logFile}
	startChild := func(waitCtx context.Context) error {
		currentHash, hashErr := privateBinaryDigest(in.binary)
		if hashErr != nil || currentHash != m.BinarySHA256 {
			return stage("mail_lifecycle_binary_changed")
		}
		next := exec.Command(in.binary, "serve")
		next.Dir, next.Env, next.Stdout, next.Stderr = in.root, childEnv, output, output
		if next.Start() != nil {
			return stage("serve_start_failed")
		}
		child, childExited = next, false
		m.ServePID = next.Process.Pid
		done := make(chan error, 1)
		childDone = done
		go func() { done <- next.Wait() }()
		exited, readyErr := privateWaitReady(waitCtx, m.AppOrigin, done)
		childExited = exited
		return readyErr
	}
	if err = startChild(setup); err != nil {
		return err
	}
	m.Status = "ready"
	if writeManifest(path, m) != nil {
		return stage("manifest_write_failed")
	}
	fmt.Fprintf(stdout, "app_origin=%s\nmock_origin=%s\nmanifest=%s\n", m.AppOrigin, m.MockOrigin, path)
	if in.mailLifecycle != nil || in.modelLifecycle != nil {
		live := &privateLifecycle{owner: owner, fixture: smtpFixture, origin: m.AppOrigin, schema: m.Schema, appRole: m.AppRole, projectionRole: m.ProjectionRole, binaryHash: m.BinarySHA256}
		live.killAndWait = func(killCtx context.Context) error {
			if child == nil || child.Process == nil || childExited {
				return stage("mail_lifecycle_no_live_owned_child")
			}
			if child.Process.Kill() != nil {
				return stage("mail_lifecycle_sigkill_failed")
			}
			select {
			case <-killCtx.Done():
				return stage("mail_lifecycle_sigkill_wait_timeout")
			case <-childDone:
				childExited = true
			}
			status, ok := child.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				return stage("mail_lifecycle_sigkill_unconfirmed")
			}
			return privateWaitRoleExit(killCtx, admin, m.AppRole, m.ProjectionRole)
		}
		live.restart = func(restartCtx context.Context) error {
			if !childExited {
				return stage("mail_lifecycle_restart_requires_exit")
			}
			if err := startChild(restartCtx); err != nil {
				return err
			}
			if err := writeManifest(path, m); err != nil {
				return err
			}
			return nil
		}
		live.proofMatches = func(link, expected string) bool {
			prefix := m.AppOrigin + "/register/complete#token="
			return strings.HasPrefix(link, prefix) && secrets.Digest("identity-challenge:v1:registration:"+strings.TrimPrefix(link, prefix)) == expected
		}
		if in.modelLifecycle != nil {
			return in.modelLifecycle(ctx, &privateModelLifecycle{privateLifecycle: live, fixture: modelFixture, secrets: secrets, provider: m.Provider})
		}
		return in.mailLifecycle(ctx, live)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-childDone:
			childExited = true
			return stage("serve_exited")
		case <-ticker.C:
			m.MockCounts = mockState.counts()
			read, done := context.WithTimeout(ctx, time.Second)
			err = owner.QueryRow(read, `SELECT (SELECT count(*) FROM platform_probe_attempts),(SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM request_records),(SELECT count(*) FROM settlements)`).Scan(&m.BusinessCounts.Probes, &m.BusinessCounts.Attempts, &m.BusinessCounts.Requests, &m.BusinessCounts.Settlements)
			done()
			m.CounterReadValid = err == nil
			if m.CounterReadValid {
				m.CounterReadAt = time.Now().UTC()
			}
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return stage("counter_read_failed")
			}
			if writeManifest(path, m) != nil {
				return stage("manifest_write_failed")
			}
		}
	}
}
