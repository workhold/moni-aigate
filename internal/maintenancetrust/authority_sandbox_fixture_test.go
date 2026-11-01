package maintenancetrust

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
)

// This fixture starts with the genuine immutable v1 QA installer, including
// its original marker, canonical proof, keys, migrations and funded tenants.
// Only the two fixed reviewed C0 sources are appended. No catalog expected
// value, original SQL, production hook, public ACL or database ACL is changed.
type authoritySandboxFixture struct {
	env          *paymentsandboxqa.ApplicationEnvironment
	admin        *pgx.Conn
	cfg          *pgx.ConnConfig
	pins         fundinglotcatalog.Pins
	sharedBefore string
	gates        []*Gate
	pools        []*pgxpool.Pool
	backends     []observedBackend
}

func newAuthoritySandboxFixture(t *testing.T) *authoritySandboxFixture {
	t.Helper()
	cfg := gateTestConfig(t)
	root := snapshotRepositoryRoot(t)
	snapshotCheckFixtureSources(t, root)
	for _, pin := range []struct{ name, sha string }{
		{"internal/paymentsandboxqa/install.go", "dd0ac0d67bbe9b6e400f11ccff14479a9c156779b5fc2079ee24c7c830e18888"},
		{"internal/paymentsandboxqa/application.go", "b0d28048b9fa3d856b83e494d319158ad4c3937eb745dde88674536496254f33"},
		{"internal/paymentsandboxschema/mock_v1.sql", "3942a0af2577bdad4e841f2b1fa3f0502a3a0db898d6f050fbaf2da45e7e0127"},
		{"internal/paymentsandboxschema/receiver_v1.sql", "3c1a4956b55ef058ad6a927e678bf026017a4f3d23bc60d19cb02640e98f433e"},
		{"internal/paymentsandboxschema/mock_inventory.json", "3f5e926b8ed9152d6bf9ff757400ab3119241629cb3b00cdbd40974d9f818234"},
		{"internal/paymentsandboxschema/receiver_inventory.json", "73be5ea3231cd8eb3504d1e1b8b269fab6b92f10be6b8128e7feafa569b662d8"},
	} {
		snapshotFixedSource(t, root, pin.name, pin.sha)
	}
	ddl := snapshotFixedSource(t, root, "specs/022-self-service-payments/c0-production-schema.sql", "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047")
	acl := snapshotFixedSource(t, root, "specs/022-self-service-payments/c0-production-permissions.sql", "9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f := &authoritySandboxFixture{}
	var err error
	f.admin, err = pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("sandbox authority administrator connection")
	}
	t.Cleanup(func() { f.close(t) })
	var version int
	var actual bool
	if f.admin.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer,current_user=session_user AND current_user='moni_test' AND r.rolsuper AND r.rolcanlogin AND d.datdba=r.oid FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_database d ON d.datname=current_database() WHERE r.rolname=current_user`).Scan(&version, &actual) != nil || version/10000 != 16 || !actual {
		t.Fatal("sandbox authority actual PG16 installer LOGIN")
	}
	if f.admin.QueryRow(ctx, sharedReadSQL).Scan(&f.sharedBefore) != nil {
		t.Fatal("sandbox authority shared baseline")
	}
	// Normalize only the already-validated dedicated test DSN. Never emit it.
	u := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(cfg.User, cfg.Password), RawQuery: "sslmode=disable"}
	f.env, err = paymentsandboxqa.CreateApplication(ctx, u.String(), t.TempDir())
	if err != nil || f.env == nil {
		t.Fatal("sandbox authority genuine v1 installation")
	}
	f.loadPins(t, ctx)
	owner, err := f.env.OpenPool(ctx, "app_owner")
	if err != nil {
		t.Fatal("sandbox authority offline owner setup")
	}
	f.pools = append(f.pools, owner)
	b := f.observePool(t, ctx, owner, f.env.Roles["app_owner"], "moni_test")
	if _, err = owner.Exec(ctx, string(ddl)); err != nil {
		t.Fatal("sandbox authority fixed C0 DDL")
	}
	text := string(acl)
	for _, item := range []struct{ key, role string }{{"fl_app", "app_runtime"}, {"fl_worker", "app_worker"}, {"fl_projector", "app_projector"}} {
		text = strings.ReplaceAll(text, `:"`+item.key+`"`, pgx.Identifier{f.env.Roles[item.role]}.Sanitize())
	}
	if strings.Contains(text, `:"`) {
		t.Fatal("sandbox authority unresolved fixed ACL")
	}
	if _, err = owner.Exec(ctx, text); err != nil {
		t.Fatal("sandbox authority fixed C0 permissions")
	}
	owner.Close()
	f.gone(t, b)
	f.runtimesGone(t)
	f.cfg = cfg.Copy()
	f.cfg.RuntimeParams = map[string]string{"application_name": "c0_authority_sandbox_fixture", "search_path": pgx.Identifier{f.env.Schemas["app"]}.Sanitize() + ",pg_catalog,pg_temp"}
	f.cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return f
}

func (f *authoritySandboxFixture) loadPins(t *testing.T, ctx context.Context) {
	t.Helper()
	var db uint32
	if f.admin.QueryRow(ctx, `SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()`).Scan(&db) != nil {
		t.Fatal("sandbox authority database OID")
	}
	role := func(key string, login bool) fundinglotcatalog.RolePin {
		r := fundinglotcatalog.RolePin{Name: f.env.Roles[key]}
		var actualLogin bool
		if r.Name == "" || f.admin.QueryRow(ctx, `SELECT oid,rolcanlogin FROM pg_catalog.pg_roles WHERE rolname=$1`, r.Name).Scan(&r.OID, &actualLogin) != nil || actualLogin != login || r.OID == 0 {
			t.Fatal("sandbox authority actual role pin")
		}
		return r
	}
	schema := func(key string) fundinglotcatalog.SchemaPin {
		owner := role(key+"_owner", false)
		s := fundinglotcatalog.SchemaPin{Name: f.env.Schemas[key], NamespaceOwner: owner, ObjectOwner: owner}
		var actualOwner uint32
		if f.admin.QueryRow(ctx, `SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1`, s.Name).Scan(&s.OID, &actualOwner) != nil || s.OID == 0 || actualOwner != owner.OID {
			t.Fatal("sandbox authority actual schema pin")
		}
		return s
	}
	w := role("app_worker", true)
	f.pins = fundinglotcatalog.Pins{Mode: fundinglotcatalog.Sandbox, Database: fundinglotcatalog.DatabasePin{Host: "127.0.0.1", Port: 55439, Name: "moni_test", OID: db}, AppSchema: schema("app"), App: role("app_runtime", true), Projector: role("app_projector", true), Worker: &w, Sandbox: &fundinglotcatalog.SandboxPins{Mock: schema("mock"), MockRuntime: role("mock_runtime", true), Receiver: schema("receiver"), ReceiverRuntime: role("receiver_runtime", true)}}
}

func (f *authoritySandboxFixture) observePool(t *testing.T, ctx context.Context, p *pgxpool.Pool, current, session string) observedBackend {
	t.Helper()
	conn, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal("sandbox authority pool observation acquire")
	}
	defer conn.Release()
	var b observedBackend
	var c, s string
	var currentOID, sessionOID uint32
	var currentLogin, sessionLogin bool
	// The offline owner connection is a real moni_test LOGIN followed by
	// the original QA installer's SET ROLE to its NOLOGIN object owner.
	// pg_stat_activity masks backend_start from that current owner because
	// the backend belongs to the session LOGIN. Read the local PID and role
	// scalars here, then independently observe its start as the administrator.
	err = conn.QueryRow(ctx, `SELECT current_user::text,session_user::text,c.oid,c.rolcanlogin,s.oid,s.rolcanlogin,pg_catalog.pg_backend_pid() FROM pg_catalog.pg_roles c,pg_catalog.pg_roles s WHERE c.rolname=current_user AND s.rolname=session_user`).Scan(&c, &s, &currentOID, &currentLogin, &sessionOID, &sessionLogin, &b.pid)
	ownerSetup := current != session
	if err != nil || c != current || s != session || currentOID == 0 || sessionOID == 0 || currentLogin == ownerSetup || !sessionLogin || b.pid == 0 || p.Config().ConnConfig.User != session {
		t.Fatalf("sandbox authority pool role identity: query_ok=%t current_matches=%t session_matches=%t owner_setup=%t current_login=%t session_login=%t nonzero_oids=%t nonzero_pid=%t config_session_matches=%t", err == nil, c == current, s == session, ownerSetup, currentLogin, sessionLogin, currentOID != 0 && sessionOID != 0, b.pid != 0, p.Config().ConnConfig.User == session)
	}
	var observed bool
	err = f.admin.QueryRow(ctx, `SELECT backend_start,usesysid=$3::pg_catalog.oid AND usename=$4::pg_catalog.name AND datid=$2::pg_catalog.oid FROM pg_catalog.pg_stat_activity WHERE pid=$1`, b.pid, f.pins.Database.OID, sessionOID, session).Scan(&b.start, &observed)
	if err != nil || !observed || b.start.IsZero() {
		t.Fatalf("sandbox authority independent backend identity: query_ok=%t session_database_match=%t nonzero_start=%t", err == nil, observed, !b.start.IsZero())
	}
	f.backends = append(f.backends, b)
	return b
}

func (f *authoritySandboxFixture) runtime(t *testing.T, component, role string) (*pgxpool.Pool, observedBackend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	p, err := f.env.OpenPool(ctx, component)
	if err != nil {
		t.Fatal("sandbox authority actual runtime pool")
	}
	f.pools = append(f.pools, p)
	return p, f.observePool(t, ctx, p, f.env.Roles[role], f.env.Roles[role])
}

func (f *authoritySandboxFixture) installed(t *testing.T, init bool) (*Gate, *Snapshot, *gateWire, observedBackend) {
	t.Helper()
	cfg := f.cfg.Copy()
	dial := cfg.DialFunc
	ready := make(chan *gateWire, 1)
	cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		w := newGateWire(c)
		ready <- w
		return w, nil
	}
	g, err := Acquire(context.Background(), cfg, f.pins)
	if err != nil || g == nil {
		t.Fatalf("sandbox authority real maintenance Acquire: %v", err)
	}
	f.gates = append(f.gates, g)
	b := observedBackend{pid: g.state.pid, start: g.state.backendStart}
	f.backends = append(f.backends, b)
	var observed bool
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND datid=$3 AND usename='moni_test')`, b.pid, b.start, f.pins.Database.OID).Scan(&observed) != nil || !observed || b.pid == f.admin.PgConn().PID() {
		t.Fatal("sandbox authority dedicated actual installer backend")
	}
	var s *Snapshot
	if init {
		s, err = g.BeginInit(context.Background())
	} else {
		s, err = g.BeginPlan(context.Background())
	}
	if err != nil || s == nil || s.Outcome() != OutcomeOpen {
		t.Fatalf("sandbox authority original Snapshot: %v", err)
	}
	p, err := s.VerifyInstallation(context.Background())
	if err != nil || p == nil {
		t.Fatalf("sandbox authority genuine T1 installation: %v", err)
	}
	*p = fundinglotcatalog.InstallationProof{}
	return g, s, awaitWire(t, ready), b
}

func (f *authoritySandboxFixture) gone(t *testing.T, b observedBackend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := f.awaitGone(ctx, b); err != nil {
		t.Fatal("sandbox authority original backend or advisory locks survived closure")
	}
}

func (f *authoritySandboxFixture) awaitGone(ctx context.Context, b observedBackend) error {
	for ctx.Err() == nil {
		var alive bool
		err := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND backend_start=$2) OR EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND locktype='advisory')`, b.pid, b.start).Scan(&alive)
		if err != nil {
			return err
		}
		if !alive {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ctx.Err()
}

func (f *authoritySandboxFixture) runtimesGone(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if f.awaitRuntimesGone(ctx) != nil {
		t.Fatal("sandbox authority exact runtime roles still connected")
	}
}

func (f *authoritySandboxFixture) awaitRuntimesGone(ctx context.Context) error {
	roles := []string{f.env.Roles["app_runtime"], f.env.Roles["app_worker"], f.env.Roles["app_projector"], f.env.Roles["mock_runtime"], f.env.Roles["receiver_runtime"]}
	for ctx.Err() == nil {
		var count int
		if err := f.admin.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE datid=$1 AND usename=ANY($2::text[])`, f.pins.Database.OID, roles).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ctx.Err()
}

func (f *authoritySandboxFixture) close(t *testing.T) {
	t.Helper()
	if f.admin == nil {
		return
	}
	for _, g := range f.gates {
		_ = g.Close(nil)
	}
	for _, p := range f.pools {
		p.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	allGone := true
	for _, b := range f.backends {
		if f.awaitGone(ctx, b) != nil {
			t.Error("sandbox authority precise backend cleanup")
			allGone = false
		}
	}
	if f.env != nil {
		if f.awaitRuntimesGone(ctx) != nil {
			t.Error("sandbox authority precise runtime cleanup")
			allGone = false
		}
		// Never terminate a session or drop its schema before observing all
		// owned sessions gone. The error leaves only this fixture for diagnosis.
		if allGone && f.env.Close(ctx) != nil {
			t.Error("sandbox authority exact QA resource cleanup")
		}
	}
	var sharedAfter string
	if f.sharedBefore != "" && (f.admin.QueryRow(ctx, sharedReadSQL).Scan(&sharedAfter) != nil || sharedAfter != f.sharedBefore) {
		t.Error("sandbox authority changed shared public/database catalog")
	}
	if f.admin.Close(ctx) != nil {
		t.Error("sandbox authority administrator cleanup")
	}
}
