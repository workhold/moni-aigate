package maintenancetrust

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

// These fixtures never install C0, run a server, or change public/DB ACLs. The
// exact opt-in parent is the disposable local test database; generated schemas,
// roles, sessions, and the two fixed gate keys are owned and cleaned here only.
type gateFixture struct {
	t                                  *testing.T
	admin                              *pgx.Conn
	cfg                                *pgx.ConnConfig
	pins                               fundinglotcatalog.Pins
	schema, owner, app, projector, tag string
	madeSchema                         bool
	madeRoles                          []string
	sharedBefore                       string
}

func gateTestConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	raw := os.Getenv("C0_MAINTENANCE_DATABASE_URL")
	if raw == "" {
		t.Skip("C0_MAINTENANCE_DATABASE_URL not set")
	}
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host != "127.0.0.1:55439" || u.Path != "/moni_test" || u.User == nil || u.User.Username() != "moni_test" || u.Fragment != "" {
		t.Fatal("maintenance test database rejected")
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		t.Fatal("maintenance test query")
	}
	for k, v := range q {
		if k != "sslmode" || len(v) != 1 || v[0] != "disable" {
			t.Fatal("maintenance test query rejected")
		}
	}
	c, e := pgx.ParseConfig(raw)
	if e != nil || c.Host != "127.0.0.1" || c.Port != 55439 || c.Database != "moni_test" || c.User != "moni_test" || len(c.Fallbacks) != 0 || c.TLSConfig != nil {
		t.Fatal("maintenance parsed test endpoint rejected")
	}
	c.RuntimeParams = map[string]string{"application_name": "c0_maintenance_fixture", "search_path": "pg_catalog"}
	c.ConnectTimeout = 3 * time.Second
	return c
}
func testRandom(t *testing.T) string {
	t.Helper()
	b := make([]byte, 10)
	if _, e := rand.Read(b); e != nil {
		t.Fatal("fixture entropy")
	}
	return hex.EncodeToString(b)
}
func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	cfg := gateTestConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal("maintenance fixture connect")
	}
	root := "mtg_" + testRandom(t)
	f := &gateFixture{t: t, admin: admin, tag: root, schema: root + "_s", owner: root + "_o", app: root + "_a", projector: root + "_p"}
	t.Cleanup(f.cleanup)
	if err := admin.QueryRow(ctx, sharedReadSQL).Scan(&f.sharedBefore); err != nil {
		t.Fatal("shared fixture baseline")
	}
	// A single login is enough for this Gate-only fixture; app/projector are
	// actual distinct roles without attempting to certify a C0 installation.
	password := testRandom(t) + testRandom(t)
	for _, name := range []string{f.owner, f.app, f.projector} {
		sql := "CREATE ROLE " + pgx.Identifier{name}.Sanitize() + " LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '" + password + "'"
		if _, e := admin.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); e != nil {
			t.Fatal("maintenance fixture role")
		}
		f.madeRoles = append(f.madeRoles, name)
	}
	if _, e := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" AUTHORIZATION "+pgx.Identifier{f.owner}.Sanitize(), pgx.QueryExecModeSimpleProtocol); e != nil {
		t.Fatal("maintenance fixture schema")
	}
	f.madeSchema = true
	role := func(name string) fundinglotcatalog.RolePin {
		var oid uint32
		if admin.QueryRow(ctx, `SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1`, name).Scan(&oid) != nil {
			t.Fatal("fixture role identity")
		}
		return fundinglotcatalog.RolePin{Name: name, OID: oid}
	}
	owner := role(f.owner)
	var dbOID, schemaOID uint32
	if admin.QueryRow(ctx, `SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()`).Scan(&dbOID) != nil || admin.QueryRow(ctx, `SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=$1`, f.schema).Scan(&schemaOID) != nil {
		t.Fatal("fixture object identity")
	}
	f.pins = fundinglotcatalog.Pins{Mode: fundinglotcatalog.Ordinary, Database: fundinglotcatalog.DatabasePin{Host: cfg.Host, Port: cfg.Port, Name: cfg.Database, OID: dbOID}, AppSchema: fundinglotcatalog.SchemaPin{Name: f.schema, OID: schemaOID, NamespaceOwner: owner, ObjectOwner: owner}, App: role(f.app), Projector: role(f.projector)}
	f.cfg = cfg.Copy()
	f.cfg.User = f.owner
	f.cfg.Password = password
	f.cfg.RuntimeParams = map[string]string{"application_name": f.tag, "search_path": pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp"}
	f.cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return f
}

const sharedReadSQL = `SELECT n.nspowner::text || '/' || COALESCE(n.nspacl::text,'NULL') || '/' || (SELECT count(*)::text FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid) || '/' || COALESCE(d.datacl::text,'NULL') FROM pg_catalog.pg_namespace n,pg_catalog.pg_database d WHERE n.nspname='public' AND d.datname=current_database()`

func (f *gateFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if f.admin == nil {
		return
	}
	// No broad termination or discovery cleanup. Only our exact created objects
	// are removed; a surviving session/role is reported as a fixture failure.
	if f.madeSchema {
		if _, e := f.admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE", pgx.QueryExecModeSimpleProtocol); e != nil {
			f.t.Error("maintenance fixture schema cleanup")
		}
	}
	for i := len(f.madeRoles) - 1; i >= 0; i-- {
		if _, e := f.admin.Exec(ctx, "DROP ROLE "+pgx.Identifier{f.madeRoles[i]}.Sanitize(), pgx.QueryExecModeSimpleProtocol); e != nil {
			f.t.Error("maintenance fixture role cleanup")
		}
	}
	var sharedAfter string
	if f.sharedBefore != "" && (f.admin.QueryRow(ctx, sharedReadSQL).Scan(&sharedAfter) != nil || sharedAfter != f.sharedBefore) {
		f.t.Error("shared public/database catalog changed")
	}
	for ctx.Err() == nil {
		var remaining int
		err := f.admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspname=$1)+(SELECT count(*) FROM pg_catalog.pg_roles WHERE rolname=ANY($2::text[]))+(SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE application_name=ANY($3::text[]))`, f.schema, f.madeRoles, []string{f.tag, strings.TrimSuffix(f.tag, "_gate")}).Scan(&remaining)
		if err != nil {
			f.t.Error("exact resource cleanup audit")
			break
		}
		if remaining == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ctx.Err() != nil {
		f.t.Error("exact created resources survived cleanup")
	}
	if e := f.admin.Close(ctx); e != nil {
		f.t.Error("maintenance fixture admin cleanup")
	}
}
func (f *gateFixture) connect(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	c, e := pgx.ConnectConfig(ctx, f.cfg.Copy())
	if e != nil {
		t.Fatal("fixture blocker connection")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.PgConn().Conn().Close()
		_ = c.Close(ctx)
	})
	return c
}

type observedBackend struct {
	pid   uint32
	start time.Time
}

func (f *gateFixture) backend(t *testing.T) observedBackend {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var b observedBackend
		e := f.admin.QueryRow(ctx, `SELECT pid,backend_start FROM pg_catalog.pg_stat_activity WHERE datid=$1 AND usename=$2 AND application_name=$3 ORDER BY pid LIMIT 1`, f.pins.Database.OID, f.cfg.User, f.tag).Scan(&b.pid, &b.start)
		if e == nil {
			return b
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			t.Fatal("backend observation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("backend not observed")
	return observedBackend{}
}
func (f *gateFixture) locks(t *testing.T, b observedBackend) []uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, e := f.admin.Query(ctx, `SELECT l.objid FROM pg_catalog.pg_locks l JOIN pg_catalog.pg_stat_activity a ON a.pid=l.pid WHERE a.pid=$1 AND a.backend_start=$2 AND l.locktype='advisory' AND l.database=$3 AND l.granted ORDER BY l.objid`, b.pid, b.start, f.pins.Database.OID)
	if e != nil {
		t.Fatal("lock observation")
	}
	defer rows.Close()
	var keys []uint32
	for rows.Next() {
		var key uint32
		if rows.Scan(&key) != nil {
			t.Fatal("lock decode")
		}
		keys = append(keys, key)
	}
	if rows.Err() != nil {
		t.Fatal("lock rows")
	}
	return keys
}
func (f *gateFixture) gone(t *testing.T, b observedBackend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var alive bool
		e := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND backend_start=$2) OR EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND locktype='advisory')`, b.pid, b.start).Scan(&alive)
		if e != nil {
			t.Fatal("backend cleanup observation")
		}
		if !alive {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("original backend or advisory locks survived physical closure")
}
func (f *gateFixture) exec(t *testing.T, c *pgx.Conn, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, e := c.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); e != nil {
		t.Fatal("fixture statement")
	}
}

// wireConn is deliberately test-only. It forwards the real RemoteAddr and raw
// TCP socket; no endpoint proxy or synthetic peer identity is used. Fixed simple
// protocol SQL provides precise before-send / received-reply barriers.
type gateWire struct {
	net.Conn
	mu        sync.Mutex
	queries   []string
	times     []time.Time
	arm       string
	holdReply bool
	reply     chan struct{}
	release   chan struct{}
	drop      chan struct{}
	deadline  chan struct{}
	dlTimer   *time.Timer
	closed    chan struct{}
	closeOnce sync.Once
	announced bool
}

func newGateWire(c net.Conn) *gateWire {
	return &gateWire{Conn: c, reply: make(chan struct{}), release: make(chan struct{}), drop: make(chan struct{}), deadline: make(chan struct{}, 1), closed: make(chan struct{})}
}
func (w *gateWire) Write(b []byte) (int, error) {
	// pgx flushes each fixed simple query as one frontend Q message. Startup is
	// not a Q message and cannot accidentally arm a lock-response barrier.
	if len(b) >= 6 && b[0] == 'Q' {
		q := string(bytes.TrimSuffix(b[5:], []byte{0}))
		w.mu.Lock()
		w.queries = append(w.queries, q)
		w.times = append(w.times, time.Now())
		if q == w.arm {
			w.holdReply = true
		}
		w.mu.Unlock()
	}
	return w.Conn.Write(b)
}
func (w *gateWire) Read(b []byte) (int, error) {
	n, e := w.Conn.Read(b)
	w.mu.Lock()
	hold := w.holdReply && n > 0
	if hold && !w.announced {
		close(w.reply)
		w.announced = true
	}
	w.mu.Unlock()
	if hold {
		select {
		case <-w.release:
		case <-w.drop:
			return 0, io.ErrUnexpectedEOF
		case <-w.deadline:
			return 0, os.ErrDeadlineExceeded
		case <-w.closed:
			return 0, io.ErrUnexpectedEOF
		}
	}
	return n, e
}
func (w *gateWire) SetDeadline(t time.Time) error {
	w.mu.Lock()
	if w.dlTimer != nil {
		w.dlTimer.Stop()
		w.dlTimer = nil
	}
	select {
	case <-w.deadline:
	default:
	}
	if !t.IsZero() {
		w.dlTimer = time.AfterFunc(time.Until(t), func() {
			select {
			case w.deadline <- struct{}{}:
			default:
			}
		})
	}
	w.mu.Unlock()
	return w.Conn.SetDeadline(t)
}
func (w *gateWire) Close() error { w.closeOnce.Do(func() { close(w.closed) }); return w.Conn.Close() }
func (w *gateWire) recorded() ([]string, []time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.queries...), append([]time.Time(nil), w.times...)
}
func (f *gateFixture) wireConfig(t *testing.T, arm string) (*pgx.ConnConfig, <-chan *gateWire) {
	t.Helper()
	cfg := f.cfg.Copy()
	dial := cfg.DialFunc
	ready := make(chan *gateWire, 1)
	cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, e := dial(ctx, network, address)
		if e != nil {
			return nil, e
		}
		w := newGateWire(c)
		w.arm = arm
		ready <- w
		return w, nil
	}
	return cfg, ready
}
func awaitWire(t *testing.T, ready <-chan *gateWire) *gateWire {
	t.Helper()
	select {
	case w := <-ready:
		t.Cleanup(func() { _ = w.Close() })
		return w
	case <-time.After(4 * time.Second):
		t.Fatal("wire not connected")
		return nil
	}
}
func awaitReply(t *testing.T, w *gateWire) {
	t.Helper()
	select {
	case <-w.reply:
	case <-time.After(4 * time.Second):
		t.Fatal("fixed SQL reply not observed")
	}
}
func gateStatements(w *gateWire) []string {
	q, _ := w.recorded()
	var out []string
	for _, s := range q {
		if s == tryInstanceSQL || s == tryMigrationSQL || s == unlockMigrationSQL || s == unlockInstanceSQL {
			out = append(out, s)
		}
	}
	return out
}

func TestGatePostgresAcquireAndReverseClose(t *testing.T) {
	f := newGateFixture(t)
	cfg, ready := f.wireConfig(t, "")
	original := cfg.Copy()
	pins := copyPins(f.pins)
	g, e := Acquire(context.Background(), cfg, pins)
	if e != nil {
		t.Fatalf("acquire: %v", e)
	}
	t.Cleanup(func() { _ = g.Close(nil) })
	w := awaitWire(t, ready)
	b := observedBackend{g.state.pid, g.state.backendStart}
	if g.state.conn.PgConn().PID() == f.admin.PgConn().PID() || g.state.conn.PgConn().PID() == 0 {
		t.Fatal("not a dedicated actual LOGIN")
	}
	if keys := f.locks(t, b); len(keys) != 2 || keys[0] != migrationKey || keys[1] != instanceKey {
		t.Fatal("actual fixed locks")
	}
	if cfg.User != original.User || cfg.RuntimeParams["search_path"] != original.RuntimeParams["search_path"] || pins != f.pins {
		t.Fatal("caller inputs mutated")
	}
	cfg.RuntimeParams["search_path"] = "pg_catalog"
	pins.AppSchema.Name = "changed"
	if g.state.conn.Config().RuntimeParams["search_path"] != original.RuntimeParams["search_path"] || g.state.pins.AppSchema.Name != f.schema {
		t.Fatal("caller alias")
	}
	copyGate := *g
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if g.Close(ctx) != nil || copyGate.Close(nil) != nil || g.Close(context.Background()) != nil {
		t.Fatal("independent cancelled/idempotent close")
	}
	f.gone(t, b)
	want := []string{tryInstanceSQL, tryMigrationSQL, unlockMigrationSQL, unlockInstanceSQL}
	got := gateStatements(w)
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatal("lock order or recursive request")
	}
}

func TestGatePostgresWaitsForSecondLock(t *testing.T) {
	f := newGateFixture(t)
	blocker := f.connect(t)
	f.exec(t, blocker, `SELECT pg_catalog.pg_advisory_lock(782193401::bigint)`)
	// Use a distinct app name for the gate, so the observer cannot select blocker.
	f.tag += "_gate"
	f.cfg.RuntimeParams["application_name"] = f.tag
	cfg, ready := f.wireConfig(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	type result struct {
		g *Gate
		e error
	}
	done := make(chan result, 1)
	go func() { g, e := Acquire(ctx, cfg, f.pins); done <- result{g, e} }()
	w := awaitWire(t, ready)
	b := f.backend(t)
	deadline := time.Now().Add(2 * time.Second)
	for {
		keys := f.locks(t, b)
		if len(keys) == 1 && keys[0] == instanceKey {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first gate not physically held")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(130 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("returned a partial gate")
	default:
	}
	f.exec(t, blocker, `SELECT pg_catalog.pg_advisory_unlock(782193401::bigint)`)
	r := <-done
	if r.e != nil || r.g == nil {
		t.Fatal("did not acquire after explicit false")
	}
	if r.g.Close(nil) != nil {
		t.Fatal("close")
	}
	f.gone(t, b)
	q, times := w.recorded()
	instances, migrations := 0, 0
	var last time.Time
	for i, s := range q {
		if s == tryInstanceSQL {
			instances++
		}
		if s == tryMigrationSQL {
			migrations++
			if !last.IsZero() && times[i].Sub(last) < 45*time.Millisecond {
				t.Fatal("false polling exceeded 50ms cadence")
			}
			last = times[i]
		}
	}
	if instances != 1 || migrations < 3 {
		t.Fatal("did not observe one instance acquisition and bounded false polling")
	}
}

func TestGatePostgresPartialCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent_deadline", true: "explicit_cancel"}[cancelled], func(t *testing.T) {
			f := newGateFixture(t)
			blocker := f.connect(t)
			f.exec(t, blocker, `SELECT pg_catalog.pg_advisory_lock(782193401::bigint)`)
			f.tag += "_gate"
			f.cfg.RuntimeParams["application_name"] = f.tag
			ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
			defer cancel()
			cfg, ready := f.wireConfig(t, "")
			done := make(chan error, 1)
			go func() {
				g, e := Acquire(ctx, cfg, f.pins)
				if g != nil {
					_ = g.Close(nil)
					done <- ErrIdentity
					return
				}
				done <- e
			}()
			w := awaitWire(t, ready)
			b := f.backend(t)
			for end := time.Now().Add(2 * time.Second); ; {
				keys := f.locks(t, b)
				if len(keys) == 1 && keys[0] == instanceKey {
					break
				}
				if time.Now().After(end) {
					t.Fatal("partial acquisition barrier")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if cancelled {
				cancel()
			}
			if e := <-done; e != ErrTimeout {
				t.Fatal("cancel/deadline error")
			}
			f.gone(t, b)
			for _, q := range gateStatements(w) {
				if q == unlockInstanceSQL || q == unlockMigrationSQL {
					t.Fatal("unknown path sent compensation")
				}
			}
			// The other session's migration lock must still exist and be untouched.
			var held bool
			if f.admin.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND locktype='advisory' AND objid=782193401 AND granted)`, blocker.PgConn().PID()).Scan(&held) != nil || !held {
				t.Fatal("foreign blocker changed")
			}
		})
	}
}

func TestGatePostgresThirtySecondEntranceBudget(t *testing.T) {
	f := newGateFixture(t)
	blocker := f.connect(t)
	f.exec(t, blocker, `SELECT pg_catalog.pg_advisory_lock(782193402::bigint)`)
	f.tag += "_gate"
	f.cfg.RuntimeParams["application_name"] = f.tag
	cfg, ready := f.wireConfig(t, "")
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		g, e := Acquire(context.Background(), cfg, f.pins)
		if g != nil {
			_ = g.Close(nil)
			done <- ErrIdentity
			return
		}
		done <- e
	}()
	_ = awaitWire(t, ready)
	b := f.backend(t)
	if e := <-done; e != ErrTimeout {
		t.Fatal("default entrance timeout")
	}
	elapsed := time.Since(started)
	if elapsed < 29*time.Second || elapsed > 33*time.Second {
		t.Fatal("entrance budget reset or exceeded")
	}
	f.gone(t, b)
}

func TestGatePostgresUnknownLockReplyPhysicallyCloses(t *testing.T) {
	f := newGateFixture(t)
	cfg, ready := f.wireConfig(t, tryMigrationSQL)
	done := make(chan error, 1)
	go func() {
		g, e := Acquire(context.Background(), cfg, f.pins)
		if g != nil {
			_ = g.Close(nil)
			done <- ErrIdentity
			return
		}
		done <- e
	}()
	w := awaitWire(t, ready)
	awaitReply(t, w)
	b := f.backend(t)
	// This observation proves PG already granted 401, while its true reply has
	// not reached pgx. Describe/pre-execution failures cannot satisfy the test.
	keys := f.locks(t, b)
	if len(keys) != 2 || keys[0] != migrationKey || keys[1] != instanceKey {
		t.Fatal("401 effect was not physically observed before lost reply")
	}
	close(w.drop) // lose only the reply; production must close the still-live socket
	if e := <-done; e != ErrExecution {
		t.Fatal("unknown lock reply result")
	}
	select {
	case <-w.closed:
	default:
		t.Fatal("unknown lock did not force raw socket closure")
	}
	f.gone(t, b)
	q := gateStatements(w)
	if len(q) != 2 || q[0] != tryInstanceSQL || q[1] != tryMigrationSQL {
		t.Fatal("unknown request was retried or compensated")
	}
}

func TestGatePostgresUnknownUnlockReplyPhysicallyCloses(t *testing.T) {
	f := newGateFixture(t)
	cfg, ready := f.wireConfig(t, unlockMigrationSQL)
	g, e := Acquire(context.Background(), cfg, f.pins)
	if e != nil {
		t.Fatal("acquire")
	}
	t.Cleanup(func() { _ = g.Close(nil) })
	w := awaitWire(t, ready)
	b := observedBackend{g.state.pid, g.state.backendStart}
	done := make(chan error, 1)
	go func() { done <- g.Close(nil) }()
	awaitReply(t, w)
	keys := f.locks(t, b)
	if len(keys) != 1 || keys[0] != instanceKey {
		t.Fatal("unlock401 effect not observed")
	}
	close(w.drop) // lose only the reply; production must close the still-live socket
	if e := <-done; e != ErrCleanup || g.Close(nil) != ErrCleanup {
		t.Fatal("unknown cleanup outcome")
	}
	f.gone(t, b)
	q := gateStatements(w)
	if len(q) != 3 || q[2] != unlockMigrationSQL {
		t.Fatal("unknown unlock continued or retried")
	}
}

func TestGatePostgresRejectsLockAndTransactionAnomalies(t *testing.T) {
	for _, tt := range []struct{ name, sql string }{
		{"extra", `SELECT pg_catalog.pg_advisory_lock(782193499::bigint)`},
		{"lost", `SELECT pg_catalog.pg_advisory_unlock(782193401::bigint)`},
		{"shared", `SELECT pg_catalog.pg_advisory_lock_shared(782193401::bigint)`},
		{"two_int", `SELECT pg_catalog.pg_advisory_lock(0,782193401)`},
		{"nonidle", `BEGIN`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newGateFixture(t)
			cfg, ready := f.wireConfig(t, "")
			g, e := Acquire(context.Background(), cfg, f.pins)
			if e != nil {
				t.Fatal("acquire")
			}
			t.Cleanup(func() { _ = g.Close(nil) })
			w := awaitWire(t, ready)
			b := observedBackend{g.state.pid, g.state.backendStart}
			f.exec(t, g.state.conn, tt.sql)
			if e := g.Close(nil); e != ErrCleanup {
				t.Fatal("anomalous gate accepted")
			}
			f.gone(t, b)
			for _, q := range gateStatements(w) {
				if q == unlockMigrationSQL || q == unlockInstanceSQL {
					t.Fatal("anomaly attempted SQL compensation")
				}
			}
		})
	}
}

func TestGatePostgresPreflightRejectsActualIdentityDrift(t *testing.T) {
	for _, name := range []string{"database_oid", "schema_oid", "owner_oid", "owner_name", "current_schema", "null_schema", "system_schema", "config_login_mapping", "session_auth_mapping", "existing_lock", "actual_ip", "actual_port"} {
		t.Run(name, func(t *testing.T) {
			f := newGateFixture(t)
			cfg := f.cfg.Copy()
			p := f.pins
			switch name {
			case "database_oid":
				p.Database.OID++
			case "schema_oid":
				p.AppSchema.OID++
			case "owner_oid":
				p.AppSchema.NamespaceOwner.OID += 1000000000
				p.AppSchema.ObjectOwner.OID += 1000000000
			case "owner_name":
				p.AppSchema.NamespaceOwner.Name += "_wrong"
				p.AppSchema.ObjectOwner.Name = p.AppSchema.NamespaceOwner.Name
			case "current_schema":
				cfg.RuntimeParams["search_path"] = "public,pg_catalog"
			case "null_schema":
				cfg.RuntimeParams["search_path"] = "does_not_exist"
			case "system_schema":
				cfg.RuntimeParams["search_path"] = "pg_catalog"
			case "config_login_mapping", "session_auth_mapping":
				adminCfg := gateTestConfig(t)
				cfg.User = adminCfg.User
				cfg.Password = adminCfg.Password
				sql := "SET ROLE " + pgx.Identifier{f.app}.Sanitize()
				if name == "session_auth_mapping" {
					sql = "SET SESSION AUTHORIZATION " + pgx.Identifier{f.app}.Sanitize()
				}
				cfg.AfterConnect = func(ctx context.Context, c *pgconn.PgConn) error { return c.Exec(ctx, sql).Close() }
			case "existing_lock":
				cfg.AfterConnect = func(ctx context.Context, c *pgconn.PgConn) error {
					return c.Exec(ctx, `SELECT pg_catalog.pg_advisory_lock(782193499::bigint)`).Close()
				}
			case "actual_ip", "actual_port":
				dial := cfg.DialFunc
				cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
					return dial(ctx, "tcp", "127.0.0.1:55439")
				}
				if name == "actual_ip" {
					cfg.Host = "127.0.0.2"
					p.Database.Host = cfg.Host
				} else {
					cfg.Port = 55440
					p.Database.Port = cfg.Port
				}
			}
			// Record the exact successful physical socket, so this test can additionally
			// prove server-side cleanup even when preflight returns no Gate.
			dial := cfg.DialFunc
			var w *gateWire
			cfg.DialFunc = func(ctx context.Context, n, a string) (net.Conn, error) {
				c, e := dial(ctx, n, a)
				if e != nil {
					return nil, e
				}
				w = newGateWire(c)
				return w, nil
			}
			g, e := Acquire(context.Background(), cfg, p)
			if g != nil || e == nil {
				if g != nil {
					_ = g.Close(nil)
				}
				t.Fatal("actual identity drift accepted")
			}
			if w == nil {
				t.Fatal("did not reach a real dedicated connection")
			}
			t.Cleanup(func() { _ = w.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			for {
				var n int
				if f.admin.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE application_name=$1`, f.tag).Scan(&n) != nil {
					t.Fatal("identity cleanup query")
				}
				if n == 0 {
					break
				}
				if ctx.Err() != nil {
					t.Fatal("identity failure session survived")
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

func TestGatePostgresConcurrentCloseRejectsWithoutSQL(t *testing.T) {
	f := newGateFixture(t)
	cfg, ready := f.wireConfig(t, unlockMigrationSQL)
	g, e := Acquire(context.Background(), cfg, f.pins)
	if e != nil {
		t.Fatal("acquire")
	}
	w := awaitWire(t, ready)
	b := observedBackend{g.state.pid, g.state.backendStart}
	done := make(chan error, 1)
	go func() { done <- g.Close(nil) }()
	awaitReply(t, w)
	copyGate := *g
	if e := copyGate.Close(context.Background()); e != ErrGateBusy {
		t.Fatal("concurrent close was not rejected")
	}
	close(w.release)
	if e := <-done; e != nil {
		t.Fatal("first close")
	}
	if g.Close(nil) != nil {
		t.Fatal("idempotence")
	}
	f.gone(t, b)
}

func TestGatePostgresCleanupUsesIndependentFiveSecondBudget(t *testing.T) {
	f := newGateFixture(t)
	cfg, ready := f.wireConfig(t, unlockMigrationSQL)
	g, e := Acquire(context.Background(), cfg, f.pins)
	if e != nil {
		t.Fatal("acquire")
	}
	w := awaitWire(t, ready)
	b := observedBackend{g.state.pid, g.state.backendStart}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- g.Close(cancelled) }()
	awaitReply(t, w)
	if keys := f.locks(t, b); len(keys) != 1 || keys[0] != instanceKey {
		t.Fatal("cleanup barrier did not observe first unlock")
	}
	if e := <-done; e != ErrCleanup {
		t.Fatal("stalled reply cleanup result")
	}
	elapsed := time.Since(started)
	if elapsed < 4500*time.Millisecond || elapsed > 6500*time.Millisecond {
		t.Fatal("independent five-second total cleanup budget")
	}
	// The fixture never closes the socket to release the stalled reply. Expiry
	// must force physical close even after pgx has begun asynchronous cleanup.
	select {
	case <-w.closed:
	default:
		t.Fatal("cleanup timeout did not force physical closure")
	}
	f.gone(t, b)
	q := gateStatements(w)
	if len(q) != 3 || q[2] != unlockMigrationSQL {
		t.Fatal("unknown cleanup continued SQL")
	}
}

func TestGatePostgresOrdinaryDNSAndNonOwnerAreOnlyPreflight(t *testing.T) {
	f := newGateFixture(t)
	cfg := f.cfg.Copy()
	p := f.pins
	cfg.Host = "localhost"
	p.Database.Host = "localhost"
	// Pin the normal resolver to the actual IPv4 endpoint. DNS spelling is
	// allowed in ordinary mode; actual TCP and SQL server still have to match.
	cfg.LookupFunc = func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }
	g, e := Acquire(context.Background(), cfg, p)
	if e != nil {
		t.Fatal("ordinary DNS gate")
	}
	b := observedBackend{g.state.pid, g.state.backendStart}
	if g.Close(nil) != nil {
		t.Fatal("close")
	}
	f.gone(t, b)
	adminCfg := gateTestConfig(t)
	adminCfg.RuntimeParams = map[string]string{"application_name": f.tag, "search_path": pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp"}
	g, e = Acquire(context.Background(), adminCfg, f.pins)
	if e != nil {
		t.Fatal("Gate incorrectly imposed future maintenance owner authority")
	}
	b = observedBackend{g.state.pid, g.state.backendStart}
	if g.Close(nil) != nil {
		t.Fatal("close")
	}
	f.gone(t, b)
}

func TestGatePostgresPublicEffectiveOwnerTargetBinding(t *testing.T) {
	f := newGateFixture(t)
	cfg := gateTestConfig(t)
	cfg.RuntimeParams = map[string]string{"application_name": f.tag, "search_path": "public,pg_catalog,pg_temp"}
	p := f.pins
	p.AppSchema.Name = "public"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if f.admin.QueryRow(ctx, `SELECT n.oid,n.nspowner,r.rolname::text,d.datdba,o.rolname::text FROM pg_catalog.pg_namespace n JOIN pg_catalog.pg_roles r ON r.oid=n.nspowner CROSS JOIN pg_catalog.pg_database d JOIN pg_catalog.pg_roles o ON o.oid=d.datdba WHERE n.nspname='public' AND d.datname=current_database()`).Scan(&p.AppSchema.OID, &p.AppSchema.NamespaceOwner.OID, &p.AppSchema.NamespaceOwner.Name, &p.AppSchema.ObjectOwner.OID, &p.AppSchema.ObjectOwner.Name) != nil {
		t.Fatal("public owner target")
	}
	if p.AppSchema.NamespaceOwner.Name != "pg_database_owner" || p.AppSchema.NamespaceOwner.OID != 6171 {
		t.Fatal("test cluster public profile")
	}
	g, e := Acquire(context.Background(), cfg, p)
	if e != nil {
		t.Fatal("public effective target binding")
	}
	b := observedBackend{g.state.pid, g.state.backendStart}
	if g.Close(nil) != nil {
		t.Fatal("public gate close")
	}
	f.gone(t, b)
	for _, name := range []bool{false, true} {
		bad := p
		if name {
			bad.AppSchema.ObjectOwner.Name += "_wrong"
		} else {
			bad.AppSchema.ObjectOwner.OID += 1000000000
		}
		if g, e := Acquire(context.Background(), cfg, bad); g != nil || e != ErrIdentity {
			if g != nil {
				_ = g.Close(nil)
			}
			t.Fatal("public effective owner mismatch accepted")
		}
	}
}
