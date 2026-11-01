package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
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
	"github.com/moni-ai/moni-aigate/internal/alerts"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

// A real serving binary, dedicated database and actual restricted LOGIN roles
// exercise process lifecycle. No in-process EvaluateDue/worker substitutes.
type alertMainFixture struct {
	ctx                                     context.Context
	owner, app                              *pgxpool.Pool
	ownerURL                                url.URL
	appRole, projectorRole, password, actor string
	secrets                                 *security.Secrets
	store                                   *alerts.Store
}

func newAlertMainFixture(t *testing.T, ctx context.Context) *alertMainFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required: actual alert main uses a dedicated PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("actual alert main requires dedicated 127.0.0.1:55439/moni_test as moni_test")
	}
	base, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := base.Query()
	query.Set("sslmode", "disable")
	base.RawQuery = query.Encode()
	admin, err := pgxpool.New(ctx, base.String())
	if err != nil {
		t.Fatal(err)
	}
	prefix := "moni_alert_main_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{prefix}.Sanitize()); err != nil {
		admin.Close()
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "42501" {
			t.Skip("actual alert main not executed: dedicated role lacks CREATEDB")
		}
		t.Fatal(err)
	}
	ownerURL := *base
	ownerURL.Path = "/" + prefix
	f := &alertMainFixture{ctx: ctx, ownerURL: ownerURL, appRole: prefix + "_app", projectorRole: prefix + "_projector", password: security.Random(24), actor: uuid.NewString(), secrets: &security.Secrets{Active: "alert-main", Keys: map[string][]byte{"alert-main": bytes.Repeat([]byte{0x47}, 32)}, Pepper: bytes.Repeat([]byte{0x71}, 32)}}
	created := []string{}
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		if f.owner != nil {
			f.owner.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, e := admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{prefix}.Sanitize()+" WITH (FORCE)"); e != nil {
			t.Errorf("alert main database cleanup: %v", e)
		}
		for _, role := range created {
			if _, e := admin.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Errorf("alert main role cleanup: %v", e)
			}
		}
		admin.Close()
	})
	f.owner, err = pgxpool.New(ctx, ownerURL.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{f.appRole, f.projectorRole} {
		var ddl string
		if err = admin.QueryRow(ctx, `SELECT format('CREATE ROLE %I LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L',$1::text,$2::text)`, role, f.password).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		if _, err = admin.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
		created = append(created, role)
	}
	if err = migrations.Apply(ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	if err = operations.ProvisionFor(ctx, f.owner, "public", f.appRole, f.projectorRole); err != nil {
		t.Fatal(err)
	}
	appURL := ownerURL
	appURL.User = url.UserPassword(f.appRole, f.password)
	f.app, err = pgxpool.New(ctx, appURL.String())
	if err != nil {
		t.Fatal(err)
	}
	var current string
	var super, createRole, bypass bool
	if err = f.app.QueryRow(ctx, `SELECT current_user,rolsuper,rolcreaterole,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&current, &super, &createRole, &bypass); err != nil || current != f.appRole || super || createRole || bypass {
		t.Fatal("actual alert main app identity is not restricted", err)
	}
	if _, err = f.owner.Exec(ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture','admin')`, f.actor, f.actor+"@alert-main.invalid"); err != nil {
		t.Fatal(err)
	}
	f.store, err = alerts.NewStore(f.app, alerts.RuntimeOptions{DeploymentEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *alertMainFixture) rule(t *testing.T) string {
	t.Helper()
	account := uuid.NewString()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,'actual-main-fixture','fixture','active',100)`, account); err != nil {
		t.Fatal(err)
	}
	r, err := f.store.Create(f.ctx, alerts.Reader{ActorID: f.actor}, alerts.CreateRule{OperationID: uuid.NewString(), Kind: alerts.ProcurementBudgetLow, ScopeType: "procurement_account", ScopeID: account, Label: "actual process budget observation", State: "enabled", Parameters: alerts.BudgetParameters{ThresholdAmount: "100", RecoveryAmount: "200"}})
	if err != nil || r.Status != "applied" || r.ResourceID == nil {
		t.Fatal("create actual-main rule", r, err)
	}
	return *r.ResourceID
}

type alertMainFacts struct {
	Episodes, Events, Generation, Write int64
	Sample, Success, Pointer            bool
	Episode, Opened, Seen               string
}

func (f *alertMainFixture) facts(t *testing.T, rule string) alertMainFacts {
	t.Helper()
	var v alertMainFacts
	err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM alert_episodes WHERE rule_id=t.rule_id),(SELECT count(*) FROM alert_episode_events e JOIN alert_episodes p ON p.id=e.episode_id WHERE p.rule_id=t.rule_id),t.episode_generation,t.write_revision,t.last_sample IS NOT NULL,t.last_success_at IS NOT NULL,t.current_episode_id IS NOT NULL,coalesce(t.current_episode_id,''),coalesce((SELECT opened_at::text FROM alert_episodes WHERE id=t.current_episode_id),''),coalesce((SELECT last_seen_at::text FROM alert_episodes WHERE id=t.current_episode_id),'') FROM alert_rule_runtime t WHERE t.rule_id=$1`, rule).Scan(&v.Episodes, &v.Events, &v.Generation, &v.Write, &v.Sample, &v.Success, &v.Pointer, &v.Episode, &v.Opened, &v.Seen)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func (f *alertMainFixture) empty(t *testing.T, rule string) {
	t.Helper()
	v := f.facts(t, rule)
	if v.Episodes != 0 || v.Events != 0 || v.Generation != 0 || v.Write != 1 || v.Sample || v.Success || v.Pointer {
		t.Fatalf("uncommitted/crashed evaluation left partial facts: %+v", v)
	}
}
func (f *alertMainFixture) awaitEpisode(t *testing.T, rule string) alertMainFacts {
	t.Helper()
	until := time.Now().Add(6 * time.Second)
	for time.Now().Before(until) {
		v := f.facts(t, rule)
		if v.Episodes == 1 && v.Events == 1 && v.Generation == 1 && v.Write == 2 && v.Sample && v.Success && v.Pointer {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("actual main did not persist one complete episode: %+v", f.facts(t, rule))
	return alertMainFacts{}
}
func alertMainKill(t *testing.T, p *shutdownMainProcess) {
	t.Helper()
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		var exit *exec.ExitError
		if !errors.As(p.waitErr, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("main did not reach actual SIGKILL: %v", p.waitErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("could not reap SIGKILLed alert main")
	}
}
func alertMainTerm(t *testing.T, p *shutdownMainProcess) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	p.wait(t)
}
func alertMainWait(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label, sql string, args ...any) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		var ok bool
		if err := pool.QueryRow(ctx, sql, args...).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(label)
}

// The barrier is in the last UPDATE statement, before the client can issue
// COMMIT. A deferred COMMIT trigger would already be in an uncertain COMMIT
// window and could still commit after client death; it cannot prove rollback.
func (f *alertMainFixture) barrier(t *testing.T, rule string) (*pgxpool.Conn, int) {
	t.Helper()
	blocker, err := f.owner.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	const key int32 = 782193481
	if _, err = blocker.Exec(f.ctx, `SELECT pg_advisory_lock($1::int,$2::int)`, key, 1); err != nil {
		blocker.Release()
		t.Fatal(err)
	}
	var pid int
	if err = blocker.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	// Fixture-only function: the driver lifecycle cancellation still bounds
	// the wait. No production trigger, guard or invariant is disabled.
	ddl := fmt.Sprintf(`CREATE FUNCTION alert_main_precommit_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.rule_id=%s AND NEW.last_success_at IS NOT NULL THEN PERFORM set_config('lock_timeout','0',true); PERFORM set_config('statement_timeout','0',true); PERFORM pg_advisory_xact_lock(%d,1); END IF; RETURN NEW; END $$;CREATE TRIGGER zz_alert_main_precommit_barrier AFTER UPDATE ON alert_rule_runtime FOR EACH ROW EXECUTE FUNCTION alert_main_precommit_barrier()`, quoteAlertMain(rule), key)
	if _, err = f.owner.Exec(f.ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = blocker.Exec(context.Background(), `SELECT pg_advisory_unlock($1::int,$2::int)`, key, 1)
		blocker.Release()
	})
	return blocker, pid
}
func quoteAlertMain(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

func alertMainRejectRole(t *testing.T, f *alertMainFixture, binary string) {
	t.Helper()
	listener := shutdownLoopbackListener(t)
	address := listener.Addr().String()
	_ = listener.Close()
	appURL, projectionURL := f.ownerURL, f.ownerURL
	appURL.User = url.UserPassword(f.appRole, f.password)
	projectionURL.User = url.UserPassword(f.projectorRole, f.password)
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "serve")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "TZ=UTC", "GIN_MODE=release",
		"DATABASE_URL=" + appURL.String(), "PROJECTION_DATABASE_URL=" + projectionURL.String(),
		"MASTER_KEY_ID=" + f.secrets.Active, "MASTER_KEY=" + base64.StdEncoding.EncodeToString(f.secrets.Keys[f.secrets.Active]),
		"AUTH_SECRET=" + base64.StdEncoding.EncodeToString(f.secrets.Pepper), "LISTEN_ADDR=" + address,
		"PUBLIC_ORIGIN=http://alert-startup.test", "COOKIE_SECURE=false"}
	body, err := command.CombinedOutput()
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 || strings.TrimSpace(string(body)) != "operations alert database role verification failed" {
		t.Fatal("actual main must reject an excessive effective role before opening its listener")
	}
	connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatal("rejected role left a serving listener")
	}
}
func (f *alertMainFixture) blockedPID(t *testing.T, blocker int) int {
	t.Helper()
	var pid int
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		err := f.owner.QueryRow(f.ctx, `SELECT pid FROM pg_stat_activity WHERE application_name='moni-aigate-alerts' AND wait_event_type='Lock' AND wait_event='advisory' AND $1::int=ANY(pg_blocking_pids(pid)) AND lower(query) LIKE 'update alert_rule_runtime%' LIMIT 1`, blocker).Scan(&pid)
		if err == nil {
			return pid
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("actual alert worker did not reach the UPDATE-before-COMMIT barrier")
	return 0
}

// A transparent, local PG wire relay. It arms only the observer application's
// transaction that actually executed UPDATE for the target rule. A successful
// server CommandComplete(COMMIT) is held, never synthesized or forwarded.
type alertMainCommitProxy struct {
	address     string
	captured    chan int
	armed       atomic.Bool
	held        atomic.Bool
	suppressed  atomic.Int32
	stop        chan struct{}
	once        sync.Once
	listener    net.Listener
	accepted    chan struct{}
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	jobs        sync.WaitGroup
	target      string
	upstream    string
}

func newAlertMainCommitProxy(t *testing.T, f *alertMainFixture, rule string) *alertMainCommitProxy {
	t.Helper()
	listener := shutdownLoopbackListener(t)
	p := &alertMainCommitProxy{address: listener.Addr().String(), captured: make(chan int, 1), stop: make(chan struct{}), listener: listener, accepted: make(chan struct{}), connections: map[net.Conn]struct{}{}, target: rule, upstream: f.ownerURL.Host}
	p.armed.Store(true)
	go func() {
		defer close(p.accepted)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			p.track(client)
			p.jobs.Add(1)
			go func() { defer p.jobs.Done(); p.connection(client) }()
		}
	}()
	t.Cleanup(p.close)
	return p
}
func (p *alertMainCommitProxy) track(c net.Conn) {
	p.mu.Lock()
	p.connections[c] = struct{}{}
	p.mu.Unlock()
}
func (p *alertMainCommitProxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.connections, c)
	p.mu.Unlock()
}
func (p *alertMainCommitProxy) close() {
	p.once.Do(func() {
		close(p.stop)
		_ = p.listener.Close()
		<-p.accepted
		p.mu.Lock()
		for c := range p.connections {
			_ = c.Close()
		}
		p.mu.Unlock()
		p.jobs.Wait()
	})
}
func alertMainCString(raw []byte, at int) (string, int, bool) {
	if at < 0 || at >= len(raw) {
		return "", at, false
	}
	end := bytes.IndexByte(raw[at:], 0)
	if end < 0 {
		return "", at, false
	}
	return string(raw[at : at+end]), at + end + 1, true
}
func alertMainMessage(c net.Conn) (byte, []byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		return 0, nil, nil, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length < 4 || length > 16<<20 {
		return 0, nil, nil, fmt.Errorf("invalid fixture wire frame")
	}
	body := make([]byte, length-4)
	if _, err := io.ReadFull(c, body); err != nil {
		return 0, nil, nil, err
	}
	frame := append(header[:], body...)
	return header[0], body, frame, nil
}
func alertMainWrite(c net.Conn, body []byte) error {
	for len(body) > 0 {
		n, err := c.Write(body)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		body = body[n:]
	}
	return nil
}
func alertMainBindRule(raw []byte, at int) string {
	if at+2 > len(raw) {
		return ""
	}
	formats := int(binary.BigEndian.Uint16(raw[at:]))
	at += 2 + 2*formats
	if at+2 > len(raw) {
		return ""
	}
	count := int(binary.BigEndian.Uint16(raw[at:]))
	at += 2
	if count < 1 || at+4 > len(raw) {
		return ""
	}
	size := int(int32(binary.BigEndian.Uint32(raw[at:])))
	at += 4
	if size < 0 || at+size > len(raw) {
		return ""
	}
	return string(raw[at : at+size])
}
func (p *alertMainCommitProxy) connection(client net.Conn) {
	defer func() { _ = client.Close(); p.untrack(client) }()
	upstream, err := net.DialTimeout("tcp", p.upstream, time.Second)
	if err != nil {
		return
	}
	p.track(upstream)
	defer func() { _ = upstream.Close(); p.untrack(upstream) }()
	// sslmode=disable is pinned by the fixture. Startup is untagged, unlike
	// subsequent messages. CancelRequest uses a separate short-lived socket.
	var size [4]byte
	if _, err = io.ReadFull(client, size[:]); err != nil {
		return
	}
	length := binary.BigEndian.Uint32(size[:])
	if length < 8 || length > 65536 {
		return
	}
	startup := make([]byte, length-4)
	if _, err = io.ReadFull(client, startup); err != nil {
		return
	}
	protocol := binary.BigEndian.Uint32(startup[:4])
	if protocol != 196608 && protocol != 80877102 {
		return
	}
	if err = alertMainWrite(upstream, append(size[:], startup...)); err != nil {
		return
	}
	if protocol == 80877102 {
		return
	}
	observer := false
	for at := 4; at < len(startup); {
		key, next, ok := alertMainCString(startup, at)
		if !ok || key == "" {
			break
		}
		value, after, ok := alertMainCString(startup, next)
		if !ok {
			return
		}
		if key == "application_name" && value == "moni-aigate-alerts" {
			observer = true
		}
		at = after
	}
	var dirty atomic.Bool
	var backendPID atomic.Int32
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		defer upstream.Close()
		defer client.Close()
		statements := map[string]string{}
		for {
			kind, body, frame, e := alertMainMessage(client)
			if e != nil {
				return
			}
			sql := ""
			boundRule := ""
			switch kind {
			case 'Q':
				sql = strings.TrimSuffix(string(body), "\x00")
			case 'P':
				name, at, ok := alertMainCString(body, 0)
				if ok {
					query, _, valid := alertMainCString(body, at)
					if valid {
						statements[name] = query
					}
				}
			case 'B':
				_, at, ok := alertMainCString(body, 0)
				if ok {
					name, after, valid := alertMainCString(body, at)
					if valid {
						sql = statements[name]
						boundRule = alertMainBindRule(body, after)
					}
				}
			}
			lower := strings.ToLower(strings.TrimSpace(sql))
			if strings.HasPrefix(lower, "begin") {
				dirty.Store(false)
			}
			if observer && strings.HasPrefix(lower, "update alert_rule_runtime set") && (boundRule == p.target || strings.Contains(sql, p.target)) {
				dirty.Store(true)
			}
			if e = alertMainWrite(upstream, frame); e != nil {
				return
			}
		}
	}()
	defer func() { _ = upstream.Close(); _ = client.Close(); <-clientDone }()
	for {
		kind, body, frame, e := alertMainMessage(upstream)
		if e != nil {
			return
		}
		if kind == 'K' && len(body) == 8 {
			backendPID.Store(int32(binary.BigEndian.Uint32(body[:4])))
		}
		if kind == 'C' && string(body) == "COMMIT\x00" {
			if observer && dirty.Load() && p.armed.CompareAndSwap(true, false) {
				p.held.Store(true)
				p.suppressed.Add(1)
				p.captured <- int(backendPID.Load())
				select {
				case <-p.stop:
				case <-clientDone:
				}
				return
			}
			dirty.Store(false)
		} else if kind == 'C' && string(body) == "ROLLBACK\x00" {
			dirty.Store(false)
		}
		if e = alertMainWrite(client, frame); e != nil {
			return
		}
	}
}

func TestMVPPostgresActualMainAlertsDefaultOffAndCrashRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	f := newAlertMainFixture(t, ctx)
	work := t.TempDir()
	binaryPath := filepath.Join(work, "moni-aigate-alert-main")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binaryPath, "./cmd/moni-aigate")
	build.Dir = root
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build actual alert main with race instrumentation: %v\n%s", e, out)
	}
	start := func(label string, overrides ...map[string]string) *shutdownMainProcess {
		return shutdownMainStart(t, binaryPath, work, label, f.ownerURL, f.appRole, f.projectorRole, f.password, f.secrets, overrides...)
	}
	assertNoFunds := func() {
		t.Helper()
		var count int64
		if e := f.owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM journals)+(SELECT count(*) FROM journal_lines)+(SELECT count(*) FROM funding_receipts)+(SELECT count(*) FROM projection_outbox)+(SELECT count(*) FROM request_records)+(SELECT count(*) FROM upstream_attempts)`).Scan(&count); e != nil || count != 0 {
			t.Fatalf("observer changed application financial/generation facts: %d %v", count, e)
		}
	}
	// Provisioning cannot prevent a database operator adding a grant later.
	// Actual startup must recheck both identities even with evaluation off.
	for _, item := range []struct{ role, privilege string }{{f.appRole, "UPDATE"}, {f.projectorRole, "SELECT"}} {
		quotedRole := pgx.Identifier{item.role}.Sanitize()
		if _, err = f.owner.Exec(ctx, `GRANT `+item.privilege+` ON alert_episode_events TO `+quotedRole); err != nil {
			t.Fatal(err)
		}
		alertMainRejectRole(t, f, binaryPath)
		if _, err = f.owner.Exec(ctx, `REVOKE `+item.privilege+` ON alert_episode_events FROM `+quotedRole); err != nil {
			t.Fatal(err)
		}
	}
	assertNoFunds()
	rule := f.rule(t)
	defaultOff := start("default-off")
	// This is a real running binary with the flag absent, not an options-false
	// call to EvaluateDue. The immediate pass and a full scheduler tick elapse.
	time.Sleep(1200 * time.Millisecond)
	f.empty(t, rule)
	var observers int
	if err = f.owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name='moni-aigate-alerts'`).Scan(&observers); err != nil || observers != 0 {
		t.Fatal("default-off main created observer database sessions", observers, err)
	}
	alertMainTerm(t, defaultOff)

	blocker, blockerPID := f.barrier(t, rule)
	graceful := start("enabled-cancel", map[string]string{"OPERATIONS_ALERTS_ENABLED": "true"})
	f.blockedPID(t, blockerPID)
	f.empty(t, rule)
	// Keep the external lock held while SIGTERM must cancel and join the real
	// observer slot. Releasing it first would not prove lifecycle cancellation.
	alertMainTerm(t, graceful)
	alertMainWait(t, ctx, f.owner, "SIGTERM did not join/close actual observer sessions", `SELECT NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name='moni-aigate-alerts')`)
	f.empty(t, rule)

	beforeCommit := start("before-commit-kill", map[string]string{"OPERATIONS_ALERTS_ENABLED": "true"})
	waitingPID := f.blockedPID(t, blockerPID)
	f.empty(t, rule)
	alertMainKill(t, beforeCommit)
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_unlock(782193481,1)`); err != nil {
		t.Fatal(err)
	}
	alertMainWait(t, ctx, f.owner, "killed UPDATE-before-COMMIT backend did not roll back", `SELECT NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)`, waitingPID)
	f.empty(t, rule)
	if _, err = f.owner.Exec(ctx, `DROP TRIGGER zz_alert_main_precommit_barrier ON alert_rule_runtime;DROP FUNCTION alert_main_precommit_barrier()`); err != nil {
		t.Fatal(err)
	}
	afterRollback := start("restart-after-precommit", map[string]string{"OPERATIONS_ALERTS_ENABLED": "true"})
	firstFacts := f.awaitEpisode(t, rule)
	alertMainTerm(t, afterRollback)
	if current := f.facts(t, rule); current != firstFacts {
		t.Fatalf("graceful shutdown duplicated/changed episode facts: before=%+v after=%+v", firstFacts, current)
	}

	// A separate fresh source makes the intercepted COMMIT unambiguous. The
	// proxy also checks this rule ID's actual bound UPDATE and startup app name.
	secondRule := f.rule(t)
	proxy := newAlertMainCommitProxy(t, f, secondRule)
	appURL := f.ownerURL
	appURL.Host = proxy.address
	appURL.User = url.UserPassword(f.appRole, f.password)
	unknown := start("committed-reply-held", map[string]string{"OPERATIONS_ALERTS_ENABLED": "true", "DATABASE_URL": appURL.String()})
	var committedPID int
	select {
	case committedPID = <-proxy.captured:
	case <-time.After(5 * time.Second):
		t.Fatalf("real PostgreSQL successful target COMMIT was never intercepted\n%s", unknown.diagnostic())
	}
	if committedPID <= 0 || !proxy.held.Load() || proxy.suppressed.Load() != 1 {
		t.Fatal("proxy did not establish the successful-COMMIT-response barrier")
	}
	select {
	case <-unknown.done:
		t.Fatalf("main exited before SIGKILL in held-reply window: %v", unknown.waitErr)
	default:
	}
	committedFacts := f.facts(t, secondRule)
	if committedFacts.Episodes != 1 || committedFacts.Events != 1 || committedFacts.Generation != 1 || committedFacts.Write != 2 || !committedFacts.Sample || !committedFacts.Success || !committedFacts.Pointer {
		t.Fatalf("intercepted COMMIT was not a complete persisted evaluation: %+v", committedFacts)
	}
	// PostgreSQL already emitted COMMIT, but no byte of that response has been
	// forwarded. The main is killed in this actual unknown-result window.
	alertMainKill(t, unknown)
	proxy.close()
	alertMainWait(t, ctx, f.owner, "committed observer backend did not close after SIGKILL", `SELECT NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)`, committedPID)
	if current := f.facts(t, secondRule); current != committedFacts {
		t.Fatalf("lost reply changed committed facts: before=%+v after=%+v", committedFacts, current)
	}

	restarted := start("restart-after-lost-commit", map[string]string{"OPERATIONS_ALERTS_ENABLED": "true"})
	// Startup performs a real observer pass. A no-queue worker must respect
	// persisted due time, preserve original episode/event and not reopen it.
	time.Sleep(1200 * time.Millisecond)
	if current := f.facts(t, secondRule); current != committedFacts {
		t.Fatalf("restart after unknown COMMIT repeated observation history: before=%+v after=%+v", committedFacts, current)
	}
	if current := f.facts(t, rule); current != firstFacts {
		t.Fatalf("other persisted episode changed during crash recovery: before=%+v after=%+v", firstFacts, current)
	}
	alertMainTerm(t, restarted)
	assertNoFunds()
	var openedAudits, episodes, events int
	if err = f.owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE action='alerts.episode.opened'),(SELECT count(*) FROM alert_episodes),(SELECT count(*) FROM alert_episode_events)`).Scan(&openedAudits, &episodes, &events); err != nil || openedAudits != 2 || episodes != 2 || events != 2 {
		t.Fatal("crash/restart left duplicate or orphan observation history", openedAudits, episodes, events, err)
	}
	t.Log("actual -race main: default off; SIGTERM cancels/joins locked evaluation; UPDATE-before-COMMIT SIGKILL rolls back; PostgreSQL successful COMMIT response held before SIGKILL; restart preserves one episode/event/runtime and no financial/model effects")
}
