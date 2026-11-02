package maintenancetrust

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

// snapshotFixture adds the original 001--022 + fixed ordinary C0 installation
// to T0's isolated named-minimal-login fixture. No public/database ACL changes,
// SET ROLE, synthetic proof, profile changes, or production SQL hooks are used.
type snapshotFixture struct {
	*gateFixture
	qa string
}

func snapshotRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal("snapshot fixture directory")
	}
	for i := 0; i < 5; i++ {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.HasPrefix(string(b), "module github.com/moni-ai/moni-aigate\n") {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("snapshot fixture repository")
	return ""
}

func snapshotFixedSource(t *testing.T, root, name, want string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	h := sha256.Sum256(b)
	if err != nil || hex.EncodeToString(h[:]) != want {
		t.Fatal("snapshot fixture fixed source pin")
	}
	return b
}

func newSnapshotFixture(t *testing.T) *snapshotFixture {
	return newSnapshotFixturePrepared(t, nil)
}

// prepare may create original business history before the fixed C0 candidate
// is installed. It cannot change production APIs, expected pins or candidate SQL.
func newSnapshotFixturePrepared(t *testing.T, prepare func(*snapshotFixture, *pgxpool.Pool)) *snapshotFixture {
	t.Helper()
	f := &snapshotFixture{gateFixture: newGateFixture(t)}
	root := snapshotRepositoryRoot(t)
	snapshotCheckFixtureSources(t, root)
	ddl := snapshotFixedSource(t, root, "specs/022-self-service-payments/c0-production-schema.sql", "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047")
	acl := snapshotFixedSource(t, root, "specs/022-self-service-payments/c0-ordinary-permissions.sql", "864a3e5b19a9e7d21d98b6557abf1d4903ca6ced438f1ec0259e786582b0d74e")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// pgxpool checks its private ParseConfig marker. Build through ParseConfig
	// without reconstructing or exposing the fixture password in error output.
	pc, err := pgxpool.ParseConfig("")
	if err != nil {
		t.Fatal("snapshot fixture pool config")
	}
	pc.ConnConfig = f.cfg.Copy()
	pc.MinConns, pc.MaxConns = 0, 1
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal("snapshot fixture owner pool")
	}
	defer pool.Close()
	if migrations.Apply(ctx, pool) != nil || operations.ProvisionFor(ctx, pool, f.schema, f.app, f.projector) != nil {
		t.Fatal("snapshot fixture original migrations/provision")
	}
	if prepare != nil {
		// Original application APIs write JSON []byte through pgx's normal
		// typed protocol. Keep that fixture traffic on its own short-lived pool;
		// the maintenance/DDL connection retains its fixed simple profile.
		businessConfig := pc.Copy()
		businessConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
		businessPool, businessErr := pgxpool.NewWithConfig(ctx, businessConfig)
		if businessErr != nil {
			t.Fatal("snapshot fixture original business pool")
		}
		func() {
			defer businessPool.Close()
			prepare(f, businessPool)
		}()
	}
	if _, err = pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatal("snapshot fixture fixed C0 DDL")
	}
	text := strings.ReplaceAll(string(acl), `:"fl_app"`, pgx.Identifier{f.app}.Sanitize())
	if strings.Contains(text, `:"`) {
		t.Fatal("snapshot fixture unresolved ACL placeholder")
	}
	if _, err = pool.Exec(ctx, text); err != nil {
		t.Fatal("snapshot fixture fixed ordinary ACL")
	}
	for _, name := range []string{f.owner, f.app, f.projector} {
		cfg := f.cfg.Copy()
		cfg.User = name
		c, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal("snapshot fixture actual role LOGIN")
		}
		var current, session string
		var flags []bool
		err = c.QueryRow(ctx, `SELECT current_user::text,session_user::text,ARRAY[rolcanlogin,rolinherit,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls] FROM pg_catalog.pg_roles WHERE rolname=current_user`).Scan(&current, &session, &flags)
		closeErr := c.Close(ctx)
		if err != nil || closeErr != nil || current != name || session != name || !reflect.DeepEqual(flags, []bool{true, false, false, false, false, false, false}) {
			t.Fatal("snapshot fixture named minimal actual LOGIN profile")
		}
	}
	var tables int
	if pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_class WHERE relnamespace=$1 AND relkind='r'`, f.pins.AppSchema.OID).Scan(&tables) != nil || tables != 125 {
		t.Fatal("snapshot fixture complete 125 table installation")
	}
	return f
}

func (f *snapshotFixture) acquire(t *testing.T, arm string) (*Gate, *gateWire, observedBackend) {
	t.Helper()
	cfg, ready := f.wireConfig(t, arm)
	if arm == "snapshot_catalog_stage" {
		dial := cfg.DialFunc
		cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
			c, err := dial(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &snapshotStageWire{gateWire: c.(*gateWire)}, nil
		}
	}
	g, err := Acquire(context.Background(), cfg, f.pins)
	if err != nil {
		t.Fatal("snapshot fixture acquire")
	}
	t.Cleanup(func() { _ = g.Close(nil) })
	return g, awaitWire(t, ready), observedBackend{g.state.pid, g.state.backendStart}
}

func (f *snapshotFixture) begin(t *testing.T, g *Gate, init bool, ctx context.Context) *Snapshot {
	t.Helper()
	var s *Snapshot
	var err error
	if init {
		s, err = g.BeginInit(ctx)
	} else {
		s, err = g.BeginPlan(ctx)
	}
	if err != nil || s == nil || s.Outcome() != OutcomeOpen {
		t.Fatal("snapshot fixture begin")
	}
	return s
}

// Expected lock targets are independently read from the frozen design, not
// derived from production's static list or a runtime catalog lookup.
func snapshotExpectedTables(t *testing.T) []string {
	t.Helper()
	root := snapshotRepositoryRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "specs/022-self-service-payments/c0-snapshot-lifecycle-contract.md"))
	if err != nil {
		t.Fatal("snapshot frozen lock appendix")
	}
	section := strings.SplitN(string(b), "## 附录 A：", 2)
	if len(section) != 2 {
		t.Fatal("snapshot lock appendix missing")
	}
	fenced := strings.SplitN(section[1], "~~~text\n", 2)
	if len(fenced) != 2 {
		t.Fatal("snapshot lock appendix fence")
	}
	list := strings.SplitN(fenced[1], "~~~", 2)[0]
	names := strings.Split(strings.TrimSuffix(list, "\n"), "\n")
	canonical, err := json.Marshal(names)
	digest := sha256.Sum256(canonical)
	if err != nil || len(canonical) != 2878 || len(names) != 125 || hex.EncodeToString(digest[:]) != "6fd978751fe24d37fa29da679be0976e12659e8a8f64d3983050280fffc00b19" || !sort.StringsAreSorted(names) {
		t.Fatal("snapshot frozen lock appendix pin")
	}
	return names
}

func snapshotAfterBegin(t *testing.T, w *gateWire) []string {
	t.Helper()
	queries, _ := w.recorded()
	for i, q := range queries {
		if strings.HasPrefix(strings.ToLower(q), "begin") {
			return queries[i:]
		}
	}
	t.Fatal("snapshot wire BEGIN missing")
	return nil
}

func (f *snapshotFixture) waitRelationLock(t *testing.T, b observedBackend, table, mode string, granted bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	for ctx.Err() == nil {
		var found bool
		err := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_locks l JOIN pg_catalog.pg_stat_activity a ON a.pid=l.pid JOIN pg_catalog.pg_class c ON c.oid=l.relation WHERE a.pid=$1 AND a.backend_start=$2 AND c.relnamespace=$3 AND c.relname=$4 AND l.mode=$5 AND l.granted=$6)`, b.pid, b.start, f.pins.AppSchema.OID, table, mode, granted).Scan(&found)
		if err != nil {
			t.Fatal("snapshot physical relation lock observation")
		}
		if found {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("snapshot physical relation lock barrier missing")
}

func (f *snapshotFixture) expectAllTableLocks(t *testing.T, b observedBackend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := f.admin.Query(ctx, `SELECT c.relname::text FROM pg_catalog.pg_locks l JOIN pg_catalog.pg_stat_activity a ON a.pid=l.pid JOIN pg_catalog.pg_class c ON c.oid=l.relation WHERE a.pid=$1 AND a.backend_start=$2 AND c.relnamespace=$3 AND l.locktype='relation' AND l.mode='ShareRowExclusiveLock' AND l.granted ORDER BY c.relname COLLATE pg_catalog."C"`, b.pid, b.start, f.pins.AppSchema.OID)
	if err != nil {
		t.Fatal("snapshot all relation locks")
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			t.Fatal("snapshot relation lock decode")
		}
		actual = append(actual, name)
	}
	if rows.Err() != nil || !reflect.DeepEqual(actual, snapshotExpectedTables(t)) {
		t.Fatal("snapshot actual 125 relation OID lock set")
	}
}

// This second random QA schema is intentionally outside the authenticated 125
// tables. Its private test-only writes prove transaction outcomes, never epochs
// or funding semantics, and do not add arbitrary SQL to production APIs.
func (f *snapshotFixture) sentinel(t *testing.T) string {
	t.Helper()
	if f.qa != "" {
		return f.qa
	}
	f.qa = "mts_" + testRandom(t)
	q := pgx.Identifier{f.qa}.Sanitize()
	f.exec(t, f.admin, "CREATE SCHEMA "+q+" AUTHORIZATION "+pgx.Identifier{f.owner}.Sanitize())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := f.admin.Exec(ctx, "DROP SCHEMA "+q+" CASCADE"); err != nil {
			t.Error("snapshot QA schema cleanup")
		}
		var n int
		if f.admin.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspname=$1`, f.qa).Scan(&n) != nil || n != 0 {
			t.Error("snapshot exact QA schema survived cleanup")
		}
	})
	owner := f.connect(t)
	f.exec(t, owner, "CREATE TABLE "+q+".sentinel(id text PRIMARY KEY)")
	f.exec(t, owner, "CREATE TABLE "+q+".child(id text PRIMARY KEY,parent text REFERENCES "+q+".sentinel(id) DEFERRABLE INITIALLY DEFERRED)")
	return f.qa
}

func (f *snapshotFixture) sentinelCount(t *testing.T, id string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n int
	if f.admin.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{f.qa, "sentinel"}.Sanitize()+" WHERE id=$1", id).Scan(&n) != nil {
		t.Fatal("snapshot independently observed QA sentinel")
	}
	return n
}

func snapshotAssertNoAddedSQL(t *testing.T, w *gateWire, before int, fn func() error) {
	t.Helper()
	start := time.Now()
	if err := fn(); err != ErrGateBusy {
		t.Fatal("snapshot concurrent operation not busy")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("snapshot busy rejection waited")
	}
	after, _ := w.recorded()
	if len(after) != before {
		t.Fatal("snapshot busy rejection sent SQL")
	}
}

func snapshotArm(w *gateWire, sql string) {
	w.mu.Lock()
	w.arm = sql
	w.mu.Unlock()
}

func (f *snapshotFixture) snapshotSQL(sql string) string {
	return fmt.Sprintf(sql, pgx.Identifier{f.schema}.Sanitize())
}

// Stop the first real catalog identity reply after the server has answered.
// T0 preflight lacks pg_current_xact_id, so Acquire is never intercepted.
type snapshotStageWire struct{ *gateWire }

func (w *snapshotStageWire) Write(b []byte) (int, error) {
	if len(b) >= 6 && b[0] == 'Q' {
		q := string(bytes.TrimSuffix(b[5:], []byte{0}))
		if strings.HasPrefix(q, "SELECT pg_catalog.current_database()") && strings.Contains(q, "pg_catalog.pg_current_xact_id()") {
			w.mu.Lock()
			w.arm = q
			w.mu.Unlock()
		}
	}
	return w.gateWire.Write(b)
}

// Source/manifest checks describe fixture provenance only. They do not call or
// assert a T1 history/manifest authority stage; that production stage is T2.
func snapshotCheckFixtureSources(t *testing.T, root string) {
	t.Helper()
	for _, pin := range []struct{ name, sha string }{
		{"internal/operations/operations.go", "8ca51f8c3f5aee042fc28e8ede6fb450e4f0bff9650723eeb83b0560c00d9d79"},
		{"internal/operations/alert_roles.go", "3bdfd12de87c72d326813983d447fc5de82c97f8e3a85b00ee086801145e5073"},
		{"internal/operations/periodic_roles.go", "79ab556316fc2c366a97d897ab58c736c0e815de396f9904828b7abd65a4640d"},
		{"internal/operations/payment_roles.go", "966ad915a7ea277ed1ce13a76ca093342ad389166679cdfb8ac48b522907b8d2"},
		{"internal/operations/payment_roles_functions.go", "4307a70cc13890c4a2fe92d2306fd2efd68c75129808daac9309f6f368de9cb6"},
		{"migrations/embed.go", "915164219bb6490fc4570986a39a93696018f4d61e6465e0c1f6ec7532975b74"},
	} {
		snapshotFixedSource(t, root, pin.name, pin.sha)
	}
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil || len(names) != 22 {
		t.Fatal("snapshot fixture original migration set")
	}
	sort.Strings(names)
	type filePin struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	}
	pins := make([]filePin, 0, len(names))
	for _, name := range names {
		b, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal("snapshot fixture original embedded migration")
		}
		sum := sha256.Sum256(b)
		pins = append(pins, filePin{name, hex.EncodeToString(sum[:])})
	}
	canonical, err := json.Marshal(pins)
	sum := sha256.Sum256(canonical)
	if err != nil || len(canonical) != 2459 || hex.EncodeToString(sum[:]) != "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de" {
		t.Fatal("snapshot fixture fixed original migration bytes")
	}
}

func (f *snapshotFixture) expectBackendIdle(t *testing.T, backend observedBackend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var idle bool
	if f.admin.QueryRow(ctx, `SELECT state='idle' AND xact_start IS NULL FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND backend_start=$2`, backend.pid, backend.start).Scan(&idle) != nil || !idle {
		t.Fatal("server did not end transaction before intercepted reply")
	}
}
