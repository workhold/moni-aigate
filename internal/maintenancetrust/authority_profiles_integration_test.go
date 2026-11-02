package maintenancetrust

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

const authorityImageOwnerFlags = "LOGIN INHERIT SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS"

// Public profiles must never install into the parent test database's public
// schema. Each fixture owns one random database and exactly three random roles.
// Cleanup closes only captured connections and never terminates another backend.
type authorityPublicProfile struct {
	parent                          *pgx.Conn
	parentConfig, ownerConfig       *pgx.ConnConfig
	pins                            fundinglotcatalog.Pins
	tag, database, owner, app, proj string
	password                        string
	sharedBefore                    string
	rolesIntent, databaseIntent     bool
	mu                              sync.Mutex
	backends                        []authorityProfileBackend
	gates                           []*Gate
}

type authorityProfileBackend struct {
	pid      uint32
	start    time.Time
	database uint32
	conn     *pgx.Conn
}

func newAuthorityPublicProfile(t *testing.T, image bool) *authorityPublicProfile {
	t.Helper()
	return newAuthorityPublicProfilePrepared(t, image, nil)
}

// prepare creates original application history before the C0 candidate adds
// its epoch-dependent tenant trigger. A nil callback preserves the original
// empty-installation public profile fixture.
func newAuthorityPublicProfilePrepared(t *testing.T, image bool, prepare func(*pgxpool.Pool)) *authorityPublicProfile {
	t.Helper()
	cfg := gateTestConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tag := "mtap_" + testRandom(t)
	f := &authorityPublicProfile{parentConfig: cfg.Copy(), tag: tag, database: tag + "_db", owner: tag + "_o", app: tag + "_a", proj: tag + "_p", password: testRandom(t) + testRandom(t)}
	f.parentConfig.RuntimeParams = map[string]string{"application_name": tag + "_parent", "search_path": "pg_catalog"}
	t.Cleanup(func() { f.cleanup(t) })
	var err error
	f.parent, err = pgx.ConnectConfig(ctx, f.parentConfig)
	if err != nil {
		t.Fatal("authority public parent connection")
	}
	var version int
	var identity bool
	if f.parent.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer,
 current_user=session_user AND current_user='moni_test' AND current_database()='moni_test'
 AND host(inet_server_addr())='127.0.0.1' AND inet_server_port()=55439
 AND r.rolsuper AND r.rolcanlogin AND d.datdba=r.oid
 FROM pg_roles r JOIN pg_database d ON d.datname=current_database() WHERE r.rolname=current_user`).Scan(&version, &identity) != nil || version/10000 != 16 || !identity {
		t.Fatal("authority public dedicated parent identity")
	}
	if f.parent.QueryRow(ctx, sharedReadSQL).Scan(&f.sharedBefore) != nil {
		t.Fatal("authority public parent baseline")
	}
	var absent bool
	if f.parent.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=ANY($1::text[]))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=$2)`, []string{f.owner, f.app, f.proj}, f.database).Scan(&absent) != nil || !absent {
		t.Fatal("authority public names must be absent")
	}
	// Mark the exact names before sending CREATE: an unknown commit reply must
	// still be followed by cleanup rather than a second creation attempt.
	f.rolesIntent = true
	tx, err := f.parent.Begin(ctx)
	if err != nil {
		t.Fatal("authority public role transaction")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	minimal := "LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"
	for _, item := range []struct{ name, flags string }{{f.owner, minimal}, {f.app, minimal}, {f.proj, minimal}} {
		flags := item.flags
		if image && item.name == f.owner {
			flags = authorityImageOwnerFlags
		}
		if _, err = tx.Exec(ctx, "CREATE ROLE "+pgx.Identifier{item.name}.Sanitize()+" "+flags+" PASSWORD '"+f.password+"'"); err != nil {
			t.Fatal("authority public role creation")
		}
	}
	if tx.Commit(ctx) != nil {
		t.Fatal("authority public role commit unknown; cleanup only")
	}
	f.databaseIntent = true
	if _, err = f.parent.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{f.database}.Sanitize()+" OWNER "+pgx.Identifier{f.owner}.Sanitize()+" TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C'"); err != nil {
		t.Fatal("authority public database create unknown; cleanup only")
	}
	f.ownerConfig = f.config(f.owner, "owner")
	pc, err := pgxpool.ParseConfig("")
	if err != nil {
		t.Fatal("authority public install pool config")
	}
	pc.ConnConfig, pc.MinConns, pc.MaxConns = f.ownerConfig.Copy(), 0, 1
	pc.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { return f.capture(ctx, c) }
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal("authority public install pool")
	}
	defer pool.Close()
	root := snapshotRepositoryRoot(t)
	snapshotCheckFixtureSources(t, root)
	ddl := snapshotFixedSource(t, root, "specs/022-self-service-payments/c0-production-schema.sql", "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047")
	acl := snapshotFixedSource(t, root, "specs/022-self-service-payments/c0-ordinary-permissions.sql", "864a3e5b19a9e7d21d98b6557abf1d4903ca6ced438f1ec0259e786582b0d74e")
	if migrations.Apply(ctx, pool) != nil || operations.ProvisionFor(ctx, pool, "public", f.app, f.proj) != nil {
		t.Fatal("authority public original migrations and provision")
	}
	if prepare != nil {
		// Original billing APIs bind JSON []byte through pgx's typed protocol.
		// Its pool is owned by this fixture and closes before fixed C0 DDL;
		// maintenance/DDL and subsequent APP verification stay simple protocol.
		businessConfig := pc.Copy()
		businessConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
		businessPool, err := pgxpool.NewWithConfig(ctx, businessConfig)
		if err != nil {
			t.Fatal("authority public original business pool")
		}
		func() {
			defer businessPool.Close()
			prepare(businessPool)
		}()
	}
	if _, err = pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatal("authority public fixed C0 schema")
	}
	text := strings.ReplaceAll(string(acl), `:"fl_app"`, pgx.Identifier{f.app}.Sanitize())
	if strings.Contains(text, `:"`) {
		t.Fatal("authority public unresolved ACL placeholder")
	}
	if _, err = pool.Exec(ctx, text); err != nil {
		t.Fatal("authority public fixed ordinary ACL")
	}
	var dbOID, schemaOID, namespaceOwner, dbOwner, ownerOID, appOID, projOID uint32
	if pool.QueryRow(ctx, `SELECT d.oid,n.oid,n.nspowner,d.datdba,
 (SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1),
 (SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$2),
 (SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$3)
 FROM pg_catalog.pg_database d CROSS JOIN pg_catalog.pg_namespace n
 WHERE d.datname=current_database() AND n.nspname='public'`, f.owner, f.app, f.proj).Scan(&dbOID, &schemaOID, &namespaceOwner, &dbOwner, &ownerOID, &appOID, &projOID) != nil || namespaceOwner != 6171 || dbOwner != ownerOID {
		t.Fatal("authority public builtin and effective owner pins")
	}
	f.pins = fundinglotcatalog.Pins{Mode: fundinglotcatalog.Ordinary,
		Database:  fundinglotcatalog.DatabasePin{Host: "127.0.0.1", Port: 55439, Name: f.database, OID: dbOID},
		AppSchema: fundinglotcatalog.SchemaPin{Name: "public", OID: schemaOID, NamespaceOwner: fundinglotcatalog.RolePin{Name: "pg_database_owner", OID: 6171}, ObjectOwner: fundinglotcatalog.RolePin{Name: f.owner, OID: ownerOID}},
		App:       fundinglotcatalog.RolePin{Name: f.app, OID: appOID}, Projector: fundinglotcatalog.RolePin{Name: f.proj, OID: projOID}}
	var current, session string
	var flags []bool
	if pool.QueryRow(ctx, `SELECT current_user::text,session_user::text,
 ARRAY[rolcanlogin,rolinherit,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls]
 FROM pg_catalog.pg_roles WHERE rolname=current_user`).Scan(&current, &session, &flags) != nil || current != f.owner || session != f.owner || !reflect.DeepEqual(flags, authorityProfileFlags(image)) {
		t.Fatal("authority public actual owner LOGIN profile")
	}
	return f
}

func authorityProfileFlags(image bool) []bool {
	if image {
		return []bool{true, true, true, true, true, true, true}
	}
	return []bool{true, false, false, false, false, false, false}
}

func (f *authorityPublicProfile) config(user, label string) *pgx.ConnConfig {
	cfg := f.parentConfig.Copy()
	cfg.Database, cfg.User, cfg.Password = f.database, user, f.password
	cfg.RuntimeParams = map[string]string{"application_name": f.tag + "_" + label, "search_path": "public,pg_catalog,pg_temp"}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return cfg
}

func (f *authorityPublicProfile) capture(ctx context.Context, c *pgx.Conn) error {
	b := authorityProfileBackend{conn: c, pid: c.PgConn().PID()}
	if c.QueryRow(ctx, `SELECT backend_start,datid FROM pg_catalog.pg_stat_activity WHERE pid=pg_backend_pid()`).Scan(&b.start, &b.database) != nil {
		return fmt.Errorf("authority_profile_backend")
	}
	f.mu.Lock()
	f.backends = append(f.backends, b)
	f.mu.Unlock()
	return nil
}

func (f *authorityPublicProfile) acquire(t *testing.T) *Gate {
	t.Helper()
	g, err := Acquire(context.Background(), f.ownerConfig.Copy(), f.pins)
	if err != nil || g == nil {
		t.Fatal("authority public actual-owner gate")
	}
	f.mu.Lock()
	f.gates = append(f.gates, g)
	f.backends = append(f.backends, authorityProfileBackend{pid: g.state.pid, start: g.state.backendStart, database: f.pins.Database.OID})
	f.mu.Unlock()
	return g
}

func (f *authorityPublicProfile) cleanup(t *testing.T) {
	t.Helper()
	if f == nil || f.parent == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() { _ = f.parent.Close(ctx) }()
	f.mu.Lock()
	backends, gates := append([]authorityProfileBackend(nil), f.backends...), append([]*Gate(nil), f.gates...)
	f.mu.Unlock()
	for _, g := range gates {
		_ = g.Close(nil)
	}
	for _, b := range backends {
		if b.conn != nil {
			_ = b.conn.PgConn().Conn().Close()
			_ = b.conn.Close(ctx)
		}
	}
	// No FORCE or pg_terminate_backend: an unclosed session is a test failure,
	// never a reason to discover or terminate someone else's connection.
	for _, b := range backends {
		for {
			var alive bool
			if f.parent.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity
 WHERE pid=$1 AND backend_start=$2 AND datid=$3)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND database=$3 AND locktype='advisory')`, b.pid, b.start, b.database).Scan(&alive) != nil {
				t.Error("authority public exact backend cleanup audit")
				return
			}
			if !alive {
				break
			}
			select {
			case <-ctx.Done():
				t.Error("authority public own backend survived cleanup")
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	if f.databaseIntent {
		if _, err := f.parent.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{f.database}.Sanitize()); err != nil {
			t.Error("authority public exact database cleanup")
			return
		}
	}
	if f.rolesIntent {
		for _, role := range []string{f.proj, f.app, f.owner} {
			if _, err := f.parent.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("authority public exact role cleanup")
				return
			}
		}
	}
	var gone bool
	if f.parent.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=ANY($1::text[]))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=$2)`, []string{f.owner, f.app, f.proj}, f.database).Scan(&gone) != nil || !gone {
		t.Error("authority public exact resource cleanup")
	}
	var sharedAfter string
	if f.sharedBefore != "" && (f.parent.QueryRow(ctx, sharedReadSQL).Scan(&sharedAfter) != nil || sharedAfter != f.sharedBefore) {
		t.Error("authority public changed parent public or database ACL")
	}
	f.password = ""
}

func TestAuthorityPostgresOrdinaryImageProfile(t *testing.T) {
	f := newSnapshotFixture(t)
	f.exec(t, f.admin, "ALTER ROLE "+pgx.Identifier{f.owner}.Sanitize()+" "+authorityImageOwnerFlags)
	owner := f.connect(t)
	var current, session string
	var flags []bool
	if owner.QueryRow(context.Background(), `SELECT current_user::text,session_user::text,
 ARRAY[rolcanlogin,rolinherit,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls]
 FROM pg_catalog.pg_roles WHERE rolname=current_user`).Scan(&current, &session, &flags) != nil || current != f.owner || session != f.owner || !reflect.DeepEqual(flags, authorityProfileFlags(true)) {
		t.Fatal("authority named image actual owner LOGIN")
	}
	_ = owner.Close(context.Background())
	for _, init := range []bool{false, true} {
		t.Run(fmt.Sprint(init), func(t *testing.T) {
			g, s, _, b := authorityInstalled(t, f, init)
			p, err := Attest(context.Background(), s)
			if p == nil || err != nil || p.Verify(context.Background()) != nil || p.state.authority.active {
				t.Fatal("authority named image permit")
			}
			id := p.state.authority.identity
			if id.loginOID != f.pins.AppSchema.ObjectOwner.OID || id.loginName != f.owner || id.sessionName != f.owner || id.rawOwnerOID != id.loginOID || id.effectiveOwnerOID != id.loginOID {
				t.Fatal("authority named image actual owner binding")
			}
			if s.Rollback(context.Background()) != nil || p.Verify(context.Background()) != ErrTransaction || g.Close(nil) != nil {
				t.Fatal("authority named image revocation or cleanup")
			}
			f.gone(t, b)
		})
	}
}

func TestAuthorityPostgresPublicOwnerProfiles(t *testing.T) {
	for _, image := range []bool{false, true} {
		t.Run(map[bool]string{false: "public-minimal-login", true: "public-image-login"}[image], func(t *testing.T) {
			f := newAuthorityPublicProfile(t, image)
			for _, init := range []bool{false, true} {
				t.Run(fmt.Sprint(init), func(t *testing.T) {
					g := f.acquire(t)
					var s *Snapshot
					var err error
					if init {
						s, err = g.BeginInit(context.Background())
					} else {
						s, err = g.BeginPlan(context.Background())
					}
					if s == nil || err != nil {
						t.Fatal("authority public snapshot")
					}
					proof, err := s.VerifyInstallation(context.Background())
					if proof == nil || err != nil {
						t.Fatal("authority public original installation")
					}
					*proof = fundinglotcatalog.InstallationProof{}
					p, err := Attest(context.Background(), s)
					if p == nil || err != nil || p.Verify(context.Background()) != nil || p.state.authority.active {
						t.Fatal("authority public same-snapshot permit")
					}
					id := p.state.authority.identity
					if id.rawOwnerName != "pg_database_owner" || id.rawOwnerOID != 6171 || id.effectiveOwnerName != f.owner || id.effectiveOwnerOID != f.pins.AppSchema.ObjectOwner.OID || id.databaseOwner != id.effectiveOwnerOID || id.loginOID != id.effectiveOwnerOID || id.loginName != f.owner || id.sessionName != f.owner {
						t.Fatal("authority public resolved to anything except actual datdba")
					}
					copyP := *p
					if copyP.Verify(context.Background()) != nil || s.Rollback(context.Background()) != nil || p.Verify(context.Background()) != ErrTransaction || copyP.Verify(context.Background()) != ErrTransaction || g.Close(nil) != nil {
						t.Fatal("authority public shared revocation or cleanup")
					}
				})
			}
		})
	}
}

func TestAuthorityPostgresNOLOGINOwnerAndSetRoleRejected(t *testing.T) {
	f := newSnapshotFixture(t)
	f.exec(t, f.admin, "ALTER ROLE "+pgx.Identifier{f.owner}.Sanitize()+" NOLOGIN")
	// The original named-isolated catalog profile is legitimate static input;
	// that does not make its NOLOGIN owner an actual maintenance LOGIN.
	tx, err := f.admin.BeginTx(context.Background(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("authority NOLOGIN static fixture snapshot")
	}
	if _, err = tx.Exec(context.Background(), "SET LOCAL search_path TO "+pgx.Identifier{f.schema}.Sanitize()+",pg_catalog,pg_temp"); err != nil {
		t.Fatal("authority NOLOGIN static fixture path")
	}
	installation, err := fundinglotcatalog.VerifyInstallationTx(context.Background(), tx, f.pins)
	if installation == nil || err != nil || tx.Rollback(context.Background()) != nil {
		t.Fatal("authority NOLOGIN original static profile")
	}
	if g, err := Acquire(context.Background(), f.cfg.Copy(), f.pins); g != nil || err == nil {
		if g != nil {
			_ = g.Close(nil)
		}
		t.Fatal("authority NOLOGIN accepted as actual maintenance LOGIN")
	}
	adminCfg := gateTestConfig(t)
	cfg := f.cfg.Copy()
	cfg.User, cfg.Password = adminCfg.User, adminCfg.Password
	var backend observedBackend
	hookPhase := "not_entered"
	cfg.AfterConnect = func(ctx context.Context, c *pgconn.PgConn) error {
		hookPhase = "backend_observation"
		backend.pid = c.PID()
		if f.admin.QueryRow(ctx, `SELECT backend_start FROM pg_catalog.pg_stat_activity WHERE pid=$1`, backend.pid).Scan(&backend.start) != nil {
			return fmt.Errorf("authority_set_role_backend")
		}
		hookPhase = "set_role"
		if err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{f.owner}.Sanitize()).Close(); err != nil {
			return fmt.Errorf("authority_set_role_statement")
		}
		hookPhase = "complete"
		return nil
	}
	// With SET ROLE, PostgreSQL may hide the session user's activity details
	// from current_user, making backend_start NULL. Preflight then rejects at
	// Scan (ErrExecution) before its explicit identity comparison (ErrIdentity).
	// Both fixed outcomes reject this completed substitution attempt; other
	// errors, an incomplete hook, or a surviving physical backend are failures.
	if g, err := Acquire(context.Background(), cfg, f.pins); g != nil || err != ErrIdentity && err != ErrExecution || hookPhase != "complete" {
		if g != nil {
			_ = g.Close(nil)
		}
		// Acquire returns only fixed safe package errors. No pgx/config/DSN or
		// raw SQL error is included in this fixture diagnostic.
		t.Fatalf("authority SET ROLE rejection: gate=%t safe_error=%v hook_phase=%s", g != nil, err, hookPhase)
	}
	if backend.pid == 0 || backend.start.IsZero() {
		t.Fatal("authority SET ROLE did not reach real configured backend")
	}
	f.gone(t, backend)
}
