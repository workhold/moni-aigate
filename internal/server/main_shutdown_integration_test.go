package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

type shutdownMainProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
	logPath string
	address string
}

func (p *shutdownMainProcess) diagnostic() string {
	body, _ := os.ReadFile(p.logPath)
	if len(body) > 4096 {
		body = body[len(body)-4096:]
	}
	return string(body)
}

func (p *shutdownMainProcess) wait(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		if p.waitErr != nil {
			t.Fatalf("actual main did not exit cleanly: %v\n%s", p.waitErr, p.diagnostic())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("actual main did not drain within the test bound\n%s", p.diagnostic())
	}
}

func shutdownLoopbackListener(t *testing.T) net.Listener {
	t.Helper()
	for attempt := 0; attempt < 16; attempt++ {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		selected := listener.Addr().(*net.TCPAddr)
		// Port zero is chosen by the OS and may otherwise select the review
		// preview's conventional port when that preview is temporarily stopped.
		if selected.Port != 58123 {
			return listener
		}
		_ = listener.Close()
	}
	t.Fatal("could not allocate a loopback test port distinct from the review preview")
	return nil
}

func shutdownMainStart(t *testing.T, binary, work, label string, ownerURL url.URL, appRole, projectionRole, password string, secrets *security.Secrets, overrides ...map[string]string) *shutdownMainProcess {
	t.Helper()
	listener := shutdownLoopbackListener(t)
	address := listener.Addr().String()
	_ = listener.Close()
	appURL, projectionURL := ownerURL, ownerURL
	appURL.User, projectionURL.User = url.UserPassword(appRole, password), url.UserPassword(projectionRole, password)
	p := &shutdownMainProcess{cmd: exec.Command(binary, "serve"), address: address, done: make(chan struct{}), logPath: filepath.Join(work, label+".log")}
	log, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	// The child receives only generated fixture configuration, without any
	// inherited database URLs, keyrings, authentication secrets or PG options.
	p.cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TZ=UTC",
		"DATABASE_URL=" + appURL.String(), "PROJECTION_DATABASE_URL=" + projectionURL.String(),
		"MASTER_KEY_ID=" + secrets.Active, "MASTER_KEYS_JSON=", "MASTER_KEY=" + base64.StdEncoding.EncodeToString(secrets.Keys[secrets.Active]),
		"AUTH_SECRET=" + base64.StdEncoding.EncodeToString(secrets.Pepper), "LISTEN_ADDR=" + address,
		"PUBLIC_ORIGIN=http://shutdown.test", "COOKIE_SECURE=false", "MFA_ENFORCE_PLATFORM=false", "RECOVERY_MODE=false", "TRUSTED_PROXY_CIDRS=", "GIN_MODE=release"}
	// Optional fixture-only overrides preserve all existing callers and replace
	// rather than append duplicate environment keys. Values never enter logs.
	for _, override := range overrides {
		for key, value := range override {
			retained := p.cmd.Env[:0]
			for _, entry := range p.cmd.Env {
				if !strings.HasPrefix(entry, key+"=") {
					retained = append(retained, entry)
				}
			}
			p.cmd.Env = append(retained, key+"="+value)
		}
	}
	p.cmd.Stdout, p.cmd.Stderr = log, log
	if err = p.cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	go func() { p.waitErr = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				t.Error("could not reap the dedicated main subprocess")
			}
		}
		_ = log.Close()
	})
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			t.Fatalf("actual main exited before readiness: %v\n%s", p.waitErr, p.diagnostic())
		default:
		}
		res, err := client.Get("http://" + address + "/health/ready")
		if err == nil {
			var status struct {
				Status       string `json:"status"`
				RecoveryMode bool   `json:"recovery_mode"`
			}
			decodeErr := json.NewDecoder(res.Body).Decode(&status)
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if decodeErr == nil && res.StatusCode == 200 && status.Status == "ready" && !status.RecoveryMode {
				return p
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("actual main did not become ready in normal mode\n%s", p.diagnostic())
	return nil
}

func TestMVPPostgresActualMainSIGTERMDrainsSSEAndRestartDoesNotReplay(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required: actual main shutdown uses a dedicated temporary PostgreSQL database")
	}
	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "postgres" && base.Scheme != "postgresql") {
		t.Fatal("actual main shutdown test requires a PostgreSQL URL in TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	prefix := "moni_shutdown_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	// PostgreSQL's constant advisory service lock is database-scoped. A fresh
	// database prevents this real main from competing with recovery/main tests.
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{prefix}.Sanitize()); err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "42501" {
			t.Skip("TEST_DATABASE_URL role lacks CREATEDB; actual main shutdown was not executed")
		}
		t.Fatal(err)
	}
	ownerURL := *base
	ownerURL.Path = "/" + prefix
	owner, err := pgxpool.New(ctx, ownerURL.String())
	if err != nil {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{prefix}.Sanitize()+" WITH (FORCE)")
		t.Fatal(err)
	}
	// Match deploy/init-roles.sh for a fresh PostgreSQL 16 database. The
	// current installer intentionally rejects an implicit public owner.
	if _, err = owner.Exec(ctx, "ALTER SCHEMA public OWNER TO CURRENT_USER"); err != nil {
		owner.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{prefix}.Sanitize()+" WITH (FORCE)")
		t.Fatal(err)
	}
	appRole, projectionRole := prefix+"_app", prefix+"_projection"
	createdRoles := []string{}
	t.Cleanup(func() {
		owner.Close()
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		// Use a new administrative connection: the test's deferred pool.Close
		// runs before Cleanup, and never terminate unrelated test sessions.
		control, connectErr := pgxpool.New(cleanup, raw)
		if connectErr != nil {
			t.Errorf("temporary shutdown database cleanup connection: %v", connectErr)
			return
		}
		defer control.Close()
		if _, dropErr := control.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{prefix}.Sanitize()+" WITH (FORCE)"); dropErr != nil {
			t.Errorf("temporary shutdown database cleanup: %v", dropErr)
		}
		for _, role := range createdRoles {
			if _, dropErr := control.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); dropErr != nil {
				t.Errorf("temporary shutdown role cleanup: %v", dropErr)
			}
		}
	})
	password := security.Random(24)
	for _, role := range []string{appRole, projectionRole} {
		var create string
		if err = admin.QueryRow(ctx, `SELECT format('CREATE ROLE %I LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L',$1::text,$2::text)`, role, password).Scan(&create); err != nil {
			t.Fatal(err)
		}
		if _, err = admin.Exec(ctx, create); err != nil {
			t.Fatal(err)
		}
		createdRoles = append(createdRoles, role)
	}
	if err = migrations.ApplyCurrent(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err = operations.ProvisionFor(ctx, owner, "public", appRole, projectionRole); err != nil {
		t.Fatal(err)
	}
	secrets := &security.Secrets{Active: "shutdown", Keys: map[string][]byte{"shutdown": bytes.Repeat([]byte{0x41}, 32)}, Pepper: bytes.Repeat([]byte{0x62}, 32)}
	tenant, user, operator, approver, key := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	execSQL := func(sql string, args ...any) {
		t.Helper()
		if _, execErr := owner.Exec(ctx, sql, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	execSQL(`INSERT INTO tenants(id,name) VALUES($1,'actual-main-shutdown')`, tenant)
	for _, actor := range []struct{ id, role string }{{user, "none"}, {operator, "finance_operator"}, {approver, "finance_approver"}} {
		execSQL(`INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture',$3)`, actor.id, actor.id+"@shutdown.invalid", actor.role)
	}
	execSQL(`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, tenant, user)
	c := catalog.New(owner, secrets)
	model, err := c.CreateModel(ctx, operator, catalog.ModelInput{PublicName: "retail-model", ContextLimit: 120, InputUpperBound: 100, OutputLimit: 20, DefaultOutputLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	execSQL(`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, tenant, model)
	token := "mg_" + security.Random(24)
	execSQL(`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'actual-main','mg_fixture',$4,$5,1000)`, key, tenant, user, secrets.Digest(token), []string{model})
	meterquote.PublishDefaultPrice(t, ctx, owner, meterquote.DefaultPrice{ModelID: model, Actor: operator, InputRate: 1000000, OutputRate: 2000000, EffectiveAt: time.Now().Add(-time.Hour)})
	var posts atomic.Int32
	finish := make(chan struct{})
	var finishOnce sync.Once
	release := func() { finishOnce.Do(func() { close(finish) }) }
	cancelled := make(chan struct{}, 1)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer main-local-secret" {
			t.Error("actual main did not use the frozen local provider endpoint and credential")
			w.WriteHeader(400)
			return
		}
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", gatewayContent)
		w.(http.Flusher).Flush()
		select {
		case <-finish:
			for _, event := range []string{gatewayFinish, gatewayUsage, "[DONE]"} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
			}
		case <-r.Context().Done():
			select {
			case cancelled <- struct{}{}:
			default:
			}
		}
	}))
	_ = upstream.Listener.Close()
	upstream.Listener = shutdownLoopbackListener(t)
	upstream.Start()
	t.Cleanup(func() { release(); upstream.Close() })
	channel, err := c.SaveChannel(ctx, operator, "", catalog.ChannelInput{Name: "main-loopback-only", BaseURL: upstream.URL + "/v1", Secret: "main-local-secret", Region: "global", AllowedCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Publish(ctx, operator, model, 0, []catalog.BindingInput{{ChannelID: channel, UpstreamModel: "provider-model", Weight: 1, MaxInflight: 4}}); err != nil {
		t.Fatal(err)
	}
	store := billing.New(owner)
	credit, err := store.CreateCredit(ctx, billing.CreditInput{TenantID: tenant, ActorID: operator, Amount: 1000, BusinessRef: uuid.NewString(), ProofRef: "receipt:actual-main-fixture", IdempotencyKey: uuid.NewString(), RequestHMAC: "shutdown-credit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: tenant, ActorID: approver, OrderID: credit.ID}); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	binary := filepath.Join(work, "moni-aigate")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binary, "./cmd/moni-aigate")
	build.Dir = root
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build actual main with race instrumentation: %v\n%s", buildErr, out)
	}
	first := shutdownMainStart(t, binary, work, "first", ownerURL, appRole, projectionRole, password, secrets)
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	const body = `{"model":"retail-model","messages":[{"role":"user","content":"hello"}],"max_tokens":20,"stream":true,"stream_options":{"include_usage":true}}`
	const idempotencyKey = "actual-main-shutdown-once"
	request := func(address, identity string) (*http.Response, error) {
		req, requestErr := http.NewRequestWithContext(ctx, "POST", "http://"+address+"/v1/chat/completions", strings.NewReader(body))
		if requestErr != nil {
			return nil, requestErr
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", identity)
		return client.Do(req)
	}
	response, err := request(first.address, idempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("actual main did not begin SSE: HTTP %d %s", response.StatusCode, body)
	}
	requestID := response.Header.Get("X-Request-ID")
	if _, err = uuid.Parse(requestID); err != nil {
		t.Fatal("actual main omitted request identity")
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("actual main did not deliver initial content: %v", readErr)
		}
		if strings.Contains(line, `"content":"hello"`) {
			break
		}
	}
	wallet, err := store.Wallet(ctx, billing.Query{TenantID: tenant})
	if err != nil || wallet.Available != 860 || wallet.Held != 140 || posts.Load() != 1 {
		t.Fatalf("stream did not reach the funded in-flight barrier: wallet=%+v posts=%d err=%v", wallet, posts.Load(), err)
	}
	if err = first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	closed := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		conn, dialErr := net.DialTimeout("tcp", first.address, 100*time.Millisecond)
		if dialErr != nil {
			closed = true
			break
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	if !closed {
		t.Fatal("SIGTERM did not close the new-connection admission path")
	}
	select {
	case <-first.done:
		t.Fatalf("actual main exited before its active stream could finish: %v\n%s", first.waitErr, first.diagnostic())
	case <-cancelled:
		t.Fatal("SIGTERM cancelled the already dispatched upstream SSE instead of draining it")
	default:
	}
	if res, newErr := request(first.address, "must-not-admit-during-drain"); newErr == nil {
		_ = res.Body.Close()
		t.Fatal("a fresh generation was admitted after SIGTERM closed the listener")
	}
	var records int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM request_records`).Scan(&records); err != nil || records != 1 || posts.Load() != 1 {
		t.Fatalf("closed admission created extra business facts: requests=%d posts=%d err=%v", records, posts.Load(), err)
	}
	release()
	tail, err := io.ReadAll(reader)
	_ = response.Body.Close()
	if err != nil || strings.Count(string(tail), "data: [DONE]") != 1 || !bytes.Contains(tail, []byte(`"total_tokens":15`)) {
		t.Fatalf("in-flight SSE did not finish during graceful drain: %v %s", err, tail)
	}
	first.wait(t)
	assertSettled := func() {
		t.Helper()
		var status, usage string
		var settlements, attempts, events, charge, spent, held int64
		if err := owner.QueryRow(ctx, `SELECT r.billing_status,(SELECT count(*) FROM settlements WHERE request_id=r.id),(SELECT count(*) FROM upstream_attempts WHERE request_id=r.id),(SELECT count(*) FROM finalization_records WHERE request_id=r.id),coalesce((SELECT charged_amount FROM settlements WHERE request_id=r.id),0),coalesce(z.retail_usage_state,'') FROM request_records r LEFT JOIN finalization_records z ON z.id=r.latest_finalization_id WHERE r.id=$1`, requestID).Scan(&status, &settlements, &attempts, &events, &charge, &usage); err != nil {
			t.Fatal(err)
		}
		wallet, err := store.Wallet(ctx, billing.Query{TenantID: tenant})
		if err != nil {
			t.Fatal(err)
		}
		if err = owner.QueryRow(ctx, `SELECT spent,held FROM api_keys WHERE id=$1`, key).Scan(&spent, &held); err != nil {
			t.Fatal(err)
		}
		if status != "settled" || usage != "confirmed_final" || settlements != 1 || attempts != 1 || events != 1 || charge != 20 || wallet.Available != 980 || wallet.Held != 0 || spent != 20 || held != 0 || posts.Load() != 1 {
			t.Fatalf("main drain/restart duplicated or lost financial facts: status=%s usage=%s settlements=%d attempts=%d finals=%d charge=%d wallet=%+v spent=%d held=%d posts=%d", status, usage, settlements, attempts, events, charge, wallet, spent, held, posts.Load())
		}
		ledger, err := store.VerifyLedger(ctx, billing.Query{})
		if err != nil || !ledger.Balanced {
			t.Fatalf("main drain/restart unbalanced the ledger: %+v %v", ledger, err)
		}
	}
	assertSettled()
	second := shutdownMainStart(t, binary, work, "restart", ownerURL, appRole, projectionRole, password, secrets)
	replayed, err := request(second.address, idempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	replayBody, err := io.ReadAll(replayed.Body)
	_ = replayed.Body.Close()
	if err != nil || replayed.StatusCode != 409 || !bytes.Contains(replayBody, []byte("request_exists")) || replayed.Header.Get("X-Original-Request-ID") != requestID {
		t.Fatalf("restarted main did not retain the original request identity: HTTP=%d original=%s err=%v body=%s", replayed.StatusCode, replayed.Header.Get("X-Original-Request-ID"), err, replayBody)
	}
	// Observe the normal main's real projection worker after restart, rather
	// than calling an in-process recovery/projection helper on its behalf.
	projected := false
	for deadline := time.Now().Add(13 * time.Second); time.Now().Before(deadline); {
		report, reportErr := store.Report(ctx, billing.Query{TenantID: tenant})
		if reportErr == nil && report.Charged == "20" && report.Credited == "1000" {
			projected = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !projected {
		t.Fatalf("actual main's separate projection role did not catch up after restart\n%s", second.diagnostic())
	}
	assertSettled()
	if err = second.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	second.wait(t)
}
