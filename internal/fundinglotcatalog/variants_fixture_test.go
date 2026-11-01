package fundinglotcatalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

type ordinaryVariantKind string

const (
	ordinaryNamed        ordinaryVariantKind = "named-isolated"
	ordinaryNamedMinimal ordinaryVariantKind = "named-minimal-login"
	ordinaryNamedImage   ordinaryVariantKind = "named-image-login"
	ordinaryPublic       ordinaryVariantKind = "public-minimal-login"
	ordinaryPublicImage  ordinaryVariantKind = "public-image-login"
	ordinaryVariantDDL                       = "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047"
	ordinaryVariantACL                       = "864a3e5b19a9e7d21d98b6557abf1d4903ca6ced438f1ec0259e786582b0d74e"
)

func ordinaryVariantKinds() []ordinaryVariantKind {
	return []ordinaryVariantKind{ordinaryNamed, ordinaryNamedMinimal, ordinaryNamedImage, ordinaryPublic, ordinaryPublicImage}
}

func (k ordinaryVariantKind) valid() bool {
	switch k {
	case ordinaryNamed, ordinaryNamedMinimal, ordinaryNamedImage, ordinaryPublic, ordinaryPublicImage:
		return true
	default:
		return false
	}
}

func (k ordinaryVariantKind) publicNamespace() bool {
	switch k {
	case ordinaryPublic, ordinaryPublicImage:
		return true
	default:
		return false
	}
}

func (k ordinaryVariantKind) ownerLogin() bool {
	switch k {
	case ordinaryNamedMinimal, ordinaryNamedImage, ordinaryPublic, ordinaryPublicImage:
		return true
	default:
		return false
	}
}

func (k ordinaryVariantKind) ownerFlags() string {
	switch k {
	case ordinaryNamed:
		return "NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"
	case ordinaryNamedMinimal, ordinaryPublic:
		return "LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"
	case ordinaryNamedImage, ordinaryPublicImage:
		return "LOGIN INHERIT SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS"
	default:
		return ""
	}
}

func (k ordinaryVariantKind) ownerFlagVector() string {
	switch k {
	case ordinaryNamed:
		return "[false false false false false false false]"
	case ordinaryNamedMinimal, ordinaryPublic:
		return "[true false false false false false false]"
	case ordinaryNamedImage, ordinaryPublicImage:
		return "[true true true true true true true]"
	default:
		return ""
	}
}

// This fixture owns only these exact names. Public variants use a new database,
// never the public namespace of the parent moni_test database.
type ordinaryVariantFixture struct {
	kind                                          ordinaryVariantKind
	pins                                          Pins
	parentAdmin, owner, app, projector            *pgxpool.Pool
	parentConfig                                  *pgxpool.Config
	ctx                                           context.Context
	cancel                                        context.CancelFunc
	root, schema, database                        string
	ownerName, appName, projectorName             string
	ownerPassword, appPassword, projectorPassword string
	createdRoles                                  []string
	schemaIntent, databaseIntent                  bool
	mu                                            sync.Mutex
	backends                                      []ordinaryVariantBackend
	beforeFunctions                               map[string]string
	cleanupDeadline                               time.Time
	closed                                        bool
}

type ordinaryVariantBackend struct {
	pid         uint32
	started     time.Time
	database    uint32
	application string
	connection  *pgx.Conn
}

func ordinaryVariantRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal("ordinary variant working directory")
	}
	for i := 0; i < 5; i++ {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.HasPrefix(string(b), "module github.com/moni-ai/moni-aigate\n") {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("ordinary variant repository root")
	return ""
}

func ordinaryVariantSource(t *testing.T, root, name, expected string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal("ordinary variant fixed source")
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != expected {
		t.Fatal("ordinary variant source pin")
	}
	return b
}

func ordinaryVariantConfig(raw string) (*pgxpool.Config, error) {
	u, err := url.Parse(raw)
	if err != nil || raw == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host != "127.0.0.1:55439" || u.Path != "/moni_test" || u.User == nil || u.User.Username() != "moni_test" || u.Fragment != "" {
		return nil, fmt.Errorf("ordinary_variant_database")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("ordinary_variant_database")
	}
	for k, values := range q {
		if k != "sslmode" || len(values) != 1 || values[0] != "disable" {
			return nil, fmt.Errorf("ordinary_variant_database")
		}
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" || len(cfg.ConnConfig.Fallbacks) != 0 || cfg.ConnConfig.TLSConfig != nil {
		return nil, fmt.Errorf("ordinary_variant_database")
	}
	cfg.ConnConfig.RuntimeParams = map[string]string{"search_path": "pg_catalog", "application_name": "c0_variant_parent"}
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.MinConns, cfg.MaxConns = 0, 1
	return cfg, nil
}

func ordinaryVariantRandom(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal("ordinary variant randomness")
	}
	return hex.EncodeToString(b[:])
}

func newOrdinaryVariantFixture(t *testing.T, kind ordinaryVariantKind) *ordinaryVariantFixture {
	t.Helper()
	return newOrdinaryVariantFixtureWithDDL(t, kind, nil)
}

// customDDL is a test-local failure injector, never a production input option.
func newOrdinaryVariantFixtureWithDDL(t *testing.T, kind ordinaryVariantKind, customDDL []byte) *ordinaryVariantFixture {
	t.Helper()
	raw := os.Getenv("C0_CATALOG_DATABASE_URL")
	if raw == "" {
		t.Skip("requires explicit dedicated C0_CATALOG_DATABASE_URL; no catalog database acceptance")
	}
	if !kind.valid() {
		t.Fatal("ordinary variant kind")
	}
	cfg, err := ordinaryVariantConfig(raw)
	if err != nil {
		t.Fatal("ordinary variant closed database configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	prefix := "flcv_" + ordinaryVariantRandom(t)
	f := &ordinaryVariantFixture{kind: kind, parentConfig: cfg, ctx: ctx, cancel: cancel, root: ordinaryVariantRoot(t), schema: prefix + "_s", database: "moni_test", ownerName: prefix + "_o", appName: prefix + "_a", projectorName: prefix + "_p", ownerPassword: ordinaryVariantRandom(t), appPassword: ordinaryVariantRandom(t), projectorPassword: ordinaryVariantRandom(t)}
	t.Cleanup(func() { f.close(t) })
	f.parentAdmin, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("ordinary variant parent pool")
	}
	var valid bool
	var version int
	if f.parentAdmin.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer,current_user=session_user AND current_user='moni_test' AND current_database()='moni_test' AND host(inet_server_addr())='127.0.0.1' AND inet_server_port()=55439 AND r.rolsuper AND r.rolcanlogin AND d.datdba=r.oid FROM pg_roles r JOIN pg_database d ON d.datname=current_database() WHERE r.rolname=current_user`).Scan(&version, &valid) != nil || !valid || version/10000 != 16 {
		t.Fatal("ordinary variant parent identity")
	}
	if kind.publicNamespace() {
		f.schema, f.database = "public", prefix+"_db"
	}
	ddl := ordinaryVariantSource(t, f.root, "specs/022-self-service-payments/c0-production-schema.sql", ordinaryVariantDDL)
	acl := ordinaryVariantSource(t, f.root, "specs/022-self-service-payments/c0-ordinary-permissions.sql", ordinaryVariantACL)
	ordinaryVariantCheckInputs(t, f.root)
	f.create(t)
	f.owner = f.pool(t, f.ownerName, f.ownerPassword, !kind.ownerLogin(), "owner")
	if migrations.Apply(ctx, f.owner) != nil {
		t.Fatal("ordinary variant original migrations")
	}
	if operations.ProvisionFor(ctx, f.owner, f.schema, f.appName, f.projectorName) != nil {
		t.Fatal("ordinary variant original provision")
	}
	f.beforeFunctions = f.functionIdentity(t)
	if customDDL != nil {
		ddl = customDDL
	}
	if _, err = f.owner.Exec(ctx, string(ddl)); err != nil {
		if customDDL != nil {
			f.close(t)
			return f
		}
		t.Fatal("ordinary variant fixed C0 DDL")
	}
	if customDDL != nil {
		t.Fatal("ordinary variant failure injection unexpectedly installed")
	}
	text := strings.ReplaceAll(string(acl), `:"fl_app"`, pgx.Identifier{f.appName}.Sanitize())
	if strings.Contains(text, `:"`) {
		t.Fatal("ordinary variant unresolved ACL placeholder")
	}
	if _, err = f.owner.Exec(ctx, text); err != nil {
		t.Fatal("ordinary variant fixed ACL")
	}
	f.app = f.pool(t, f.appName, f.appPassword, false, "app")
	f.projector = f.pool(t, f.projectorName, f.projectorPassword, false, "projector")
	f.loadPins(t)
	return f
}

func (f *ordinaryVariantFixture) create(t *testing.T) {
	t.Helper()
	c, err := f.parentAdmin.Acquire(f.ctx)
	if err != nil {
		t.Fatal("ordinary variant create connection")
	}
	var backend ordinaryVariantBackend
	backend.application = "c0_variant_parent"
	if c.QueryRow(f.ctx, `SELECT pid,backend_start,datid FROM pg_stat_activity WHERE pid=pg_backend_pid()`).Scan(&backend.pid, &backend.started, &backend.database) != nil {
		c.Release()
		t.Fatal("ordinary variant create backend")
	}
	confirmed := false
	var tx pgx.Tx
	defer func() {
		if confirmed {
			c.Release()
			return
		}
		cleanup, cancel := f.cleanupContext()
		defer cancel()
		if tx != nil {
			_ = tx.Rollback(cleanup)
		}
		raw := c.Hijack()
		backend.connection = raw
		_ = raw.Close(cleanup)
		f.mu.Lock()
		f.backends = append(f.backends, backend)
		f.mu.Unlock()
	}()
	var absent bool
	names := []string{f.ownerName, f.appName, f.projectorName}
	if c.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($1::text[])) AND NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$2) AND NOT EXISTS(SELECT 1 FROM pg_database WHERE datname=$3)`, names, map[bool]string{true: f.schema, false: ""}[!f.kind.publicNamespace()], map[bool]string{true: f.database, false: ""}[f.kind.publicNamespace()]).Scan(&absent) != nil || !absent {
		t.Fatal("ordinary variant names must be absent")
	}
	f.createdRoles = append([]string(nil), names...)
	tx, err = c.Begin(f.ctx)
	if err != nil {
		t.Fatal("ordinary variant role transaction")
	}
	ownerFlags := f.kind.ownerFlags()
	for _, r := range []struct{ name, password, flags string }{{f.ownerName, f.ownerPassword, ownerFlags}, {f.appName, f.appPassword, "LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"}, {f.projectorName, f.projectorPassword, "LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"}} {
		if _, err = tx.Exec(f.ctx, "CREATE ROLE "+pgx.Identifier{r.name}.Sanitize()+" "+r.flags+" CONNECTION LIMIT -1 PASSWORD '"+r.password+"'"); err != nil {
			t.Fatal("ordinary variant create role")
		}
	}
	if !f.kind.publicNamespace() {
		f.schemaIntent = true
		if _, err = tx.Exec(f.ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" AUTHORIZATION "+pgx.Identifier{f.ownerName}.Sanitize()); err != nil {
			t.Fatal("ordinary variant create schema")
		}
	}
	if tx.Commit(f.ctx) != nil {
		t.Fatal("ordinary variant role commit unknown; cleanup only")
	}
	if f.kind.publicNamespace() {
		f.databaseIntent = true
		if _, err = c.Exec(f.ctx, "CREATE DATABASE "+pgx.Identifier{f.database}.Sanitize()+" OWNER "+pgx.Identifier{f.ownerName}.Sanitize()+" TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C'"); err != nil {
			t.Fatal("ordinary variant database create unknown; cleanup only")
		}
	}
	confirmed = true
}

func (f *ordinaryVariantFixture) pool(t *testing.T, role, password string, setOwner bool, label string) *pgxpool.Pool {
	t.Helper()
	cfg := f.parentConfig.Copy()
	cfg.ConnConfig.Database = f.database
	// Separate caller-owned snapshots are needed by the public proof negatives.
	// Every additional physical backend is still tracked for exact cleanup.
	cfg.MaxConns = 4
	if !setOwner {
		cfg.ConnConfig.User, cfg.ConnConfig.Password = role, password
	}
	app := "c0_variant_" + label
	cfg.ConnConfig.RuntimeParams = map[string]string{"search_path": pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp", "application_name": app}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		var b ordinaryVariantBackend
		b.application, b.connection = app, c
		if c.QueryRow(ctx, `SELECT pid,backend_start,datid FROM pg_stat_activity WHERE pid=pg_backend_pid()`).Scan(&b.pid, &b.started, &b.database) != nil {
			return fmt.Errorf("ordinary_variant_backend")
		}
		f.mu.Lock()
		f.backends = append(f.backends, b)
		f.mu.Unlock()
		if setOwner {
			_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{f.ownerName}.Sanitize())
			return err
		}
		return nil
	}
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil || p.Ping(f.ctx) != nil {
		if p != nil {
			p.Close()
		}
		t.Fatal("ordinary variant role pool")
	}
	return p
}

func (f *ordinaryVariantFixture) loadPins(t *testing.T) {
	t.Helper()
	var dbOID, nsOID, nsOwner, dbOwner, objectOwner, appOID, projectorOID uint32
	if f.owner.QueryRow(f.ctx, `SELECT d.oid,n.oid,n.nspowner,d.datdba,(SELECT oid FROM pg_roles WHERE rolname=$2),(SELECT oid FROM pg_roles WHERE rolname=$3),(SELECT oid FROM pg_roles WHERE rolname=$4) FROM pg_database d CROSS JOIN pg_namespace n WHERE d.datname=current_database() AND n.nspname=$1`, f.schema, f.ownerName, f.appName, f.projectorName).Scan(&dbOID, &nsOID, &nsOwner, &dbOwner, &objectOwner, &appOID, &projectorOID) != nil {
		t.Fatal("ordinary variant pins")
	}
	nsOwnerName := f.ownerName
	if f.kind.publicNamespace() {
		if nsOwner != 6171 || dbOwner != objectOwner {
			t.Fatal("ordinary variant public owner")
		}
		nsOwnerName = "pg_database_owner"
	} else if nsOwner != objectOwner {
		t.Fatal("ordinary variant named owner")
	}
	f.pins = Pins{Mode: Ordinary, Database: DatabasePin{Host: "127.0.0.1", Port: 55439, Name: f.database, OID: dbOID}, AppSchema: SchemaPin{Name: f.schema, OID: nsOID, NamespaceOwner: RolePin{Name: nsOwnerName, OID: nsOwner}, ObjectOwner: RolePin{Name: f.ownerName, OID: objectOwner}}, App: RolePin{Name: f.appName, OID: appOID}, Projector: RolePin{Name: f.projectorName, OID: projectorOID}}
}

func (f *ordinaryVariantFixture) functionIdentity(t *testing.T) map[string]string {
	t.Helper()
	rows, err := f.owner.Query(f.ctx, `SELECT proname||'/'||proargtypes::text,oid::text||'/'||proowner::text||'/'||encode(sha256(convert_to(prosrc,'UTF8')),'hex') FROM pg_proc WHERE pronamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1)`, f.schema)
	if err != nil {
		t.Fatal("ordinary variant function identity")
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			t.Fatal("ordinary variant function identity")
		}
		out[k] = v
	}
	if rows.Err() != nil {
		t.Fatal("ordinary variant function identity")
	}
	return out
}

func (f *ordinaryVariantFixture) close(t *testing.T) {
	t.Helper()
	if f == nil || f.closed {
		return
	}
	f.closed = true
	defer f.cancel()
	ctx, cancel := f.cleanupContext()
	defer cancel()
	f.mu.Lock()
	backends := append([]ordinaryVariantBackend(nil), f.backends...)
	f.mu.Unlock()
	for _, b := range backends {
		if b.connection != nil {
			_ = b.connection.Close(ctx)
		}
	}
	for _, p := range []*pgxpool.Pool{f.projector, f.app, f.owner} {
		if p != nil {
			p.Close()
		}
	}
	if f.parentAdmin == nil {
		return
	}
	defer f.parentAdmin.Close()
	for _, b := range backends {
		for {
			var exists bool
			if f.parentAdmin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND datid=$3 AND application_name=$4)`, b.pid, b.started, b.database, b.application).Scan(&exists) != nil {
				t.Error("ordinary variant cleanup backend audit")
				return
			}
			if !exists {
				break
			}
			if _, err := f.parentAdmin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND datid=$3 AND application_name=$4 AND pid<>pg_backend_pid()`, b.pid, b.started, b.database, b.application); err != nil {
				t.Error("ordinary variant cleanup backend")
				return
			}
			select {
			case <-ctx.Done():
				t.Error("ordinary variant cleanup timeout")
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if f.schemaIntent {
		if _, err := f.parentAdmin.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error("ordinary variant cleanup schema")
			return
		}
	}
	if f.databaseIntent {
		if _, err := f.parentAdmin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{f.database}.Sanitize()); err != nil {
			t.Error("ordinary variant cleanup database")
			return
		}
	}
	for i := len(f.createdRoles) - 1; i >= 0; i-- {
		if _, err := f.parentAdmin.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{f.createdRoles[i]}.Sanitize()); err != nil {
			t.Error("ordinary variant cleanup role")
			return
		}
	}
	var gone bool
	if f.parentAdmin.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($1::text[])) AND NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$2) AND NOT EXISTS(SELECT 1 FROM pg_database WHERE datname=$3)`, f.createdRoles, map[bool]string{true: f.schema, false: ""}[f.schemaIntent], map[bool]string{true: f.database, false: ""}[f.databaseIntent]).Scan(&gone) != nil || !gone {
		t.Error("ordinary variant cleanup final audit")
	}
	f.ownerPassword, f.appPassword, f.projectorPassword = "", "", ""
}

func (f *ordinaryVariantFixture) cleanupContext() (context.Context, context.CancelFunc) {
	f.mu.Lock()
	if f.cleanupDeadline.IsZero() {
		f.cleanupDeadline = time.Now().Add(10 * time.Second)
	}
	d := f.cleanupDeadline
	f.mu.Unlock()
	return context.WithDeadline(context.Background(), d)
}

func TestOrdinaryVariantClosedDatabaseConfig(t *testing.T) {
	good := "postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable"
	if _, err := ordinaryVariantConfig(good); err != nil {
		t.Fatal("dedicated parent rejected")
	}
	for _, bad := range []string{"", strings.Replace(good, "127.0.0.1", "localhost", 1), strings.Replace(good, "55439", "5432", 1), strings.Replace(good, "/moni_test", "/moni_review", 1), good + "&host=elsewhere", good + "&sslmode=disable", good + "&service=production"} {
		if _, err := ordinaryVariantConfig(bad); err == nil {
			t.Fatal("unsafe parent accepted")
		}
	}
}

func TestOrdinaryVariantsInstallExactACL(t *testing.T) {
	for _, kind := range ordinaryVariantKinds() {
		t.Run(string(kind), func(t *testing.T) {
			f := newOrdinaryVariantFixture(t, kind)
			for _, identity := range []struct {
				pool *pgxpool.Pool
				name string
			}{{f.app, f.appName}, {f.projector, f.projectorName}} {
				var current, session string
				if identity.pool.QueryRow(f.ctx, `SELECT current_user,session_user`).Scan(&current, &session) != nil || current != identity.name || session != current {
					t.Fatal("actual ordinary runtime LOGIN")
				}
			}
			var ownerCurrent, ownerSession string
			if f.owner.QueryRow(f.ctx, `SELECT current_user,session_user`).Scan(&ownerCurrent, &ownerSession) != nil || ownerCurrent != f.ownerName || (kind.ownerLogin() && ownerSession != ownerCurrent) || (!kind.ownerLogin() && ownerSession != "moni_test") {
				t.Fatal("ordinary owner actual LOGIN or isolated offline identity")
			}
			afterFunctions := f.functionIdentity(t)
			for k, v := range f.beforeFunctions {
				if afterFunctions[k] != v {
					t.Fatal("original function identity or prosrc changed")
				}
			}
			var tables, cols, functions, types, nonempty int
			if f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM pg_class WHERE relnamespace=$1 AND relkind='r'),(SELECT count(*) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=$1 AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped),(SELECT count(*) FROM pg_proc WHERE pronamespace=$1),(SELECT count(*) FROM pg_type WHERE typnamespace=$1)`, f.pins.AppSchema.OID).Scan(&tables, &cols, &functions, &types) != nil || tables != 125 || cols != 1145 || functions != 197 || types != 252 {
				t.Fatal("ordinary full catalog shape")
			}
			for _, name := range []string{"funding_lot_epoch", "funding_lot_runtime_attestations", "funding_lot_legacy_receipts", "funding_lot_legacy_sandbox_receipts", "funding_lot_legacy_settlements", "funding_lot_legacy_approved_refunds", "funding_lot_legacy_openings", "funding_wallet_legacy_sources", "funding_wallet_operations", "funding_wallet_heads", "funding_lots", "funding_lot_events", "funding_lot_balances", "funding_reservation_lots", "funding_settlement_lots", "funding_consumption_refund_lots"} {
				var n int
				if f.owner.QueryRow(f.ctx, "SELECT count(*) FROM "+pgx.Identifier{f.schema, name}.Sanitize()).Scan(&n) != nil {
					t.Fatal("ordinary C0 empty table query")
				}
				nonempty += n
			}
			if nonempty != 0 {
				t.Fatal("ordinary C0 installation must be empty")
			}
			var insert, update []string
			var tenantOID uint32
			if f.owner.QueryRow(f.ctx, `SELECT oid FROM pg_class WHERE relnamespace=$1 AND relname='tenants'`, f.pins.AppSchema.OID).Scan(&tenantOID) != nil {
				t.Fatal("ordinary tenant identity")
			}
			if f.owner.QueryRow(f.ctx, `SELECT ARRAY(SELECT a.attname::text FROM pg_attribute a WHERE a.attrelid=$1::oid AND a.attnum>0 AND has_column_privilege($2::oid,a.attrelid,a.attnum,'INSERT') ORDER BY a.attname COLLATE "C"),ARRAY(SELECT a.attname::text FROM pg_attribute a WHERE a.attrelid=$1::oid AND a.attnum>0 AND has_column_privilege($2::oid,a.attrelid,a.attnum,'UPDATE') ORDER BY a.attname COLLATE "C")`, tenantOID, f.pins.App.OID).Scan(&insert, &update) != nil || strings.Join(insert, ",") != "allowed_regions,created_at,id,name,price_group_id,pricing_operation_id,pricing_revision,status" || strings.Join(update, ",") != "allowed_regions,name,price_group_id,pricing_operation_id,pricing_revision,status" {
				t.Fatal("ordinary tenant exact eight/six columns")
			}
			var broadInsert, broadUpdate, birthWrite, projectorData, projectorFunction, namespaceCreate bool
			if f.owner.QueryRow(f.ctx, `SELECT has_table_privilege($1::oid,$2::oid,'INSERT'),has_table_privilege($1::oid,$2::oid,'UPDATE'),has_column_privilege($1::oid,$2::oid,'fl_created_xid','INSERT') OR has_column_privilege($1::oid,$2::oid,'fl_created_xid','UPDATE'),has_table_privilege($3::oid,$4::regclass,'SELECT'),has_function_privilege($3::oid,$5::regprocedure,'EXECUTE'),has_schema_privilege($1::oid,$6::oid,'CREATE')`, f.pins.App.OID, tenantOID, f.pins.Projector.OID, pgx.Identifier{f.schema, "funding_lots"}.Sanitize(), pgx.Identifier{f.schema, "fl_require_epoch"}.Sanitize()+"()", f.pins.AppSchema.OID).Scan(&broadInsert, &broadUpdate, &birthWrite, &projectorData, &projectorFunction, &namespaceCreate) != nil || broadInsert || broadUpdate || birthWrite || projectorData || projectorFunction || namespaceCreate {
				t.Fatal("ordinary data and stamp privilege boundary")
			}
			var ownerFlags []bool
			if f.owner.QueryRow(f.ctx, `SELECT ARRAY[rolcanlogin,rolinherit,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls] FROM pg_roles WHERE oid=$1`, f.pins.AppSchema.ObjectOwner.OID).Scan(&ownerFlags) != nil {
				t.Fatal("ordinary owner profile")
			}
			want := kind.ownerFlagVector()
			if fmt.Sprint(ownerFlags) != want {
				t.Fatal("ordinary owner flags mismatch")
			}
			var publicUsage, publicCreate bool
			if f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace n,LATERAL aclexplode(n.nspacl) a WHERE n.oid=$1 AND a.grantee=0 AND a.privilege_type='USAGE'),EXISTS(SELECT 1 FROM pg_namespace n,LATERAL aclexplode(n.nspacl) a WHERE n.oid=$1 AND a.grantee=0 AND a.privilege_type='CREATE')`, f.pins.AppSchema.OID).Scan(&publicUsage, &publicCreate) != nil || publicCreate || publicUsage != kind.publicNamespace() {
				t.Fatal("ordinary exact PUBLIC namespace ACL")
			}
			f.close(t)
			t.Log("installed fixed ordinary ACL; actual runtime LOGIN; original function identity; catalog counts; C0 empty; tenant columns; owner profile; public isolation")
		})
	}
}

func TestOrdinaryVariantsInjectedDDLFailureCleanup(t *testing.T) {
	for _, kind := range ordinaryVariantKinds() {
		t.Run(string(kind), func(t *testing.T) {
			f := newOrdinaryVariantFixtureWithDDL(t, kind, []byte("SELECT no_such_c0_ordinary_variant_fixture_function()"))
			if !f.closed {
				t.Fatal("failed ordinary install did not complete cleanup")
			}
		})
	}
}

// Keep JSON imports local to this file's metadata export helper when collector
// is connected; no runtime method accepts these external candidate bytes.
func ordinaryVariantCanonicalAudit(t *testing.T, raw []byte) {
	t.Helper()
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		t.Fatal("ordinary variant canonical metadata")
	}
}

type ordinaryVariantSourcePin struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

func ordinaryVariantCheckInputs(t *testing.T, root string) {
	t.Helper()
	for _, p := range []ordinaryVariantSourcePin{
		{"internal/operations/operations.go", "8ca51f8c3f5aee042fc28e8ede6fb450e4f0bff9650723eeb83b0560c00d9d79"},
		{"internal/operations/alert_roles.go", "3bdfd12de87c72d326813983d447fc5de82c97f8e3a85b00ee086801145e5073"},
		{"internal/operations/periodic_roles.go", "79ab556316fc2c366a97d897ab58c736c0e815de396f9904828b7abd65a4640d"},
		{"internal/operations/payment_roles.go", "966ad915a7ea277ed1ce13a76ca093342ad389166679cdfb8ac48b522907b8d2"},
		{"internal/operations/payment_roles_functions.go", "4307a70cc13890c4a2fe92d2306fd2efd68c75129808daac9309f6f368de9cb6"},
		{"migrations/embed.go", "915164219bb6490fc4570986a39a93696018f4d61e6465e0c1f6ec7532975b74"},
	} {
		ordinaryVariantSource(t, root, p.Name, p.SHA256)
	}
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil || len(names) != 22 {
		t.Fatal("ordinary variant fixed manifest files")
	}
	sort.Strings(names)
	live, err := filepath.Glob(filepath.Join(root, "migrations", "*.sql"))
	if err != nil || len(live) != len(names) {
		t.Fatal("ordinary variant migration disk set")
	}
	var pins []ordinaryVariantSourcePin
	for i, n := range names {
		b, err := migrations.Files.ReadFile(n)
		if err != nil || filepath.Base(live[i]) != n {
			t.Fatal("ordinary variant embedded migration")
		}
		disk, err := os.ReadFile(live[i])
		if err != nil || !bytes.Equal(b, disk) {
			t.Fatal("ordinary variant migration disk bytes")
		}
		pins = append(pins, ordinaryVariantSourcePin{n, digest(b)})
	}
	b, err := canonical(pins)
	if err != nil || len(b) != 2459 || digest(b) != "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de" {
		t.Fatal("ordinary variant fixed complete manifest")
	}
}

func TestOrdinaryVariantIndependentACLMatchesAppSurface(t *testing.T) {
	root := ordinaryVariantRoot(t)
	old := ordinaryVariantSource(t, root, "specs/022-self-service-payments/c0-production-permissions.sql", "9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae")
	newSQL := ordinaryVariantSource(t, root, "specs/022-self-service-payments/c0-ordinary-permissions.sql", ordinaryVariantACL)
	statements := func(raw []byte) []string {
		var code []string
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				code = append(code, line)
			}
		}
		var out []string
		for _, s := range strings.Split(strings.Join(code, "\n"), ";") {
			s = strings.Join(strings.Fields(s), " ")
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	var want []string
	for _, s := range statements(old) {
		if strings.HasSuffix(s, `TO :"fl_worker"`) {
			continue
		}
		s = strings.TrimSuffix(s, `,:"fl_worker"`)
		if strings.Contains(s, `:"fl_worker"`) {
			t.Fatal("unknown worker syntax in original fixed ACL")
		}
		want = append(want, s)
	}
	got := statements(newSQL)
	if !same(want, got) {
		t.Fatal("independent ordinary ACL differs from complete fixed APP surface")
	}
}

func ordinaryVariantCodePins(t *testing.T, root string) []ordinaryVariantSourcePin {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(root, "internal/fundinglotcatalog", "*.go"))
	if err != nil || len(names) == 0 {
		t.Fatal("ordinary variant collector source set")
	}
	sort.Strings(names)
	var out []ordinaryVariantSourcePin
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal("ordinary variant collector source")
		}
		out = append(out, ordinaryVariantSourcePin{"internal/fundinglotcatalog/" + filepath.Base(n), digest(b)})
	}
	return out
}

func ordinaryVariantObserve(t *testing.T, f *ordinaryVariantFixture) *catalogSnapshot {
	t.Helper()
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("ordinary variant caller-owned RR")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()
	snapshot, err := observeCatalogTx(f.ctx, tx, f.pins)
	if err != nil || snapshot == nil {
		t.Fatal("ordinary variant same-RR metadata observation")
	}
	ordinaryVariantCanonicalAudit(t, snapshot.schemaCanonical)
	ordinaryVariantCanonicalAudit(t, snapshot.rolesCanonical)
	if tx.Commit(f.ctx) != nil {
		t.Fatal("ordinary variant observation transaction")
	}
	return snapshot
}

func ordinaryVariantCandidatePath(t *testing.T, root, name string) string {
	t.Helper()
	if name != "" && !filepath.IsAbs(name) {
		name = filepath.Join(root, name)
	}
	abs, err := filepath.Abs(name)
	base := filepath.Join(root, ".local", "c0-catalog-candidates")
	if err != nil || name == "" || filepath.Dir(abs) != base {
		t.Fatal("ordinary variant candidate output must be one new child of .local/c0-catalog-candidates")
	}
	for _, c := range filepath.Base(abs) {
		if c != '-' && c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			t.Fatal("ordinary variant output basename")
		}
	}
	for _, p := range []string{filepath.Join(root, ".local"), base} {
		fi, err := os.Lstat(p)
		if err == nil && (!fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
			t.Fatal("ordinary variant output symlink")
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal("ordinary variant output ancestor")
		}
	}
	if _, err := os.Lstat(abs); !os.IsNotExist(err) {
		t.Fatal("ordinary variant output already exists")
	}
	return abs
}

func TestOrdinaryVariantCandidateReproducibility(t *testing.T) {
	output := os.Getenv("C0_CATALOG_EXPORT_DIRECTORY")
	if output == "" {
		t.Skip("explicit metadata candidate export only; formal expected is never auto-accepted")
	}
	root := ordinaryVariantRoot(t)
	output = ordinaryVariantCandidatePath(t, root, output)
	sources := ordinaryVariantCodePins(t, root)
	files := map[string][]byte{}
	for _, kind := range ordinaryVariantKinds() {
		var previous *catalogSnapshot
		for round := 0; round < 2; round++ {
			f := newOrdinaryVariantFixture(t, kind)
			snapshot := ordinaryVariantObserve(t, f)
			for _, raw := range [][]byte{snapshot.schemaCanonical, snapshot.rolesCanonical} {
				for _, name := range []string{f.ownerName, f.appName, f.projectorName} {
					if bytes.Contains(raw, []byte(name)) {
						t.Fatal("random role name in candidate")
					}
				}
				if !f.kind.publicNamespace() && bytes.Contains(raw, []byte(f.schema)) {
					t.Fatal("random schema name in candidate")
				}
			}
			f.close(t)
			if round != 0 && (!bytes.Equal(previous.schemaCanonical, snapshot.schemaCanonical) || !bytes.Equal(previous.rolesCanonical, snapshot.rolesCanonical)) {
				t.Fatal("independent ordinary metadata rounds differ")
			}
			previous = snapshot
		}
		files["ordinary-"+string(kind)+"-schema.json"] = previous.schemaCanonical
		files["ordinary-"+string(kind)+"-roles.json"] = previous.rolesCanonical
	}
	if !same(sources, ordinaryVariantCodePins(t, root)) {
		t.Fatal("collector sources changed during candidate export")
	}
	if t.Failed() {
		return
	}
	type filePin struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Bytes  int    `json:"bytes"`
	}
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var sums []filePin
	for _, name := range names {
		sums = append(sums, filePin{name, digest(files[name]), len(files[name])})
	}
	summary, err := canonical(document{"format_version": 1, "artifact_kind": "unreviewed_descriptor_candidate", "metadata_only": true, "formal_expected": false, "epoch_issued": false, "runtime_proof_issued": false, "variants": ordinaryVariantKinds(), "independent_rounds_per_variant": 2, "files": sums, "source_pins": sources, "ddl_sha256": ordinaryVariantDDL, "ordinary_acl_sha256": ordinaryVariantACL, "profiles_cleaned_before_publish": 10})
	if err != nil {
		t.Fatal("ordinary candidate summary")
	}
	files["summary.json"] = summary
	names = append(names, "summary.json")
	output = ordinaryVariantCandidatePath(t, root, output)
	if os.MkdirAll(filepath.Dir(output), 0700) != nil || os.Mkdir(output, 0700) != nil {
		t.Fatal("ordinary candidate create directory")
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(output)
		}
	}()
	for _, name := range names {
		file, err := os.OpenFile(filepath.Join(output, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal("ordinary candidate create file")
		}
		_, we := file.Write(files[name])
		se := file.Sync()
		ce := file.Close()
		if we != nil || se != nil || ce != nil {
			t.Fatal("ordinary candidate publish")
		}
	}
	published = true
	t.Log("ten isolated ordinary installations; two identical rounds per variant; all exact resources cleaned; unreviewed metadata candidate", filepath.Base(output))
}

func TestOrdinaryVariantLegacyProjection(t *testing.T) {
	reference := os.Getenv("C0_CATALOG_LEGACY_REFERENCE")
	if reference == "" {
		t.Skip("requires explicit previously verified four-role descriptor directory; no live runtime filtering")
	}
	root := ordinaryVariantRoot(t)
	if !filepath.IsAbs(reference) {
		reference = filepath.Join(root, reference)
	}
	abs, err := filepath.Abs(reference)
	base := filepath.Join(root, ".local", "c0-descriptor-candidates")
	resolved, resolveErr := filepath.EvalSymlinks(abs)
	if err != nil || resolveErr != nil || resolved != abs || filepath.Dir(abs) != base {
		t.Fatal("ordinary legacy reference path")
	}
	var evidence struct {
		Files []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
			Bytes  int    `json:"bytes"`
		} `json:"files"`
		SourceSHA256 map[string]string `json:"source_sha256"`
	}
	b, err := os.ReadFile(filepath.Join(root, "docs/verification/funding-lot-descriptor.json"))
	if err != nil || json.Unmarshal(b, &evidence) != nil || len(evidence.Files) != 10 || len(evidence.SourceSHA256) != 7 {
		t.Fatal("ordinary legacy fixed verification evidence")
	}
	files := map[string][]byte{}
	for _, file := range evidence.Files {
		if filepath.Base(file.Name) != file.Name {
			t.Fatal("ordinary legacy file identity")
		}
		b, err := os.ReadFile(filepath.Join(abs, file.Name))
		if err != nil || len(b) != file.Bytes || digest(b) != file.SHA256 {
			t.Fatal("ordinary legacy artifact SHA or bytes")
		}
		files[file.Name] = b
	}
	for name, sha := range evidence.SourceSHA256 {
		ordinaryVariantSource(t, root, "scripts/funding-lot-descriptor/"+name, sha)
	}
	value, err := decode(files["c0-roles-ordinary.json"])
	if err != nil {
		t.Fatal("ordinary legacy role JSON")
	}
	roles, ok := value.(object)
	if !ok {
		t.Fatal("ordinary legacy role DTO")
	}
	isWorker := func(v any) bool {
		x, ok := v.(object)
		return ok && x["class"] == "role" && x["scope"] == "role" && x["name"] == "worker"
	}
	removeRecords := func(category, field string, want int) {
		a, ok := roles[category].([]any)
		if !ok {
			t.Fatal("ordinary legacy category")
		}
		kept := make([]any, 0, len(a))
		removed := 0
		for _, v := range a {
			m, ok := v.(object)
			if !ok {
				t.Fatal("ordinary legacy record")
			}
			if isWorker(m[field]) {
				removed++
				continue
			}
			kept = append(kept, v)
		}
		if removed != want {
			t.Fatal("ordinary legacy projection record count")
		}
		roles[category] = kept
	}
	removeRecords("roles", "identity", 1)
	removeRecords("effective_privileges", "role", 1720)
	removeRecords("acl_dependencies", "role", 82)
	for category, want := range map[string]int{"schema_acl": 0, "relation_acl": 8, "column_acl": 53, "function_acl": 20, "type_acl": 1} {
		a, ok := roles[category].([]any)
		if !ok {
			t.Fatal("ordinary legacy ACL category")
		}
		removed := 0
		for _, v := range a {
			m := v.(object)
			entries, ok := m["entries"].([]any)
			if !ok {
				t.Fatal("ordinary legacy ACL entries")
			}
			kept := make([]any, 0, len(entries))
			for _, entry := range entries {
				e := entry.(object)
				if isWorker(e["grantor"]) {
					t.Fatal("ordinary legacy unexpected worker grantor")
				}
				if isWorker(e["grantee"]) {
					removed++
					continue
				}
				kept = append(kept, entry)
			}
			m["entries"] = kept
		}
		if removed != want {
			t.Fatal("ordinary legacy projection explicit ACL count")
		}
	}
	for _, category := range []string{"memberships", "default_acl"} {
		a, ok := roles[category].([]any)
		if !ok || len(a) != 0 {
			t.Fatal("ordinary legacy unexpected membership/default ACL")
		}
	}
	if roles["profile"] != "ordinary_app_acl" || roles["worker_kind"] != "c0_placeholder" {
		t.Fatal("ordinary legacy profile")
	}
	roles["worker_kind"] = "absent"
	wantRoles, err := canonical(roles)
	if err != nil || bytes.Contains(wantRoles, []byte(`"name":"worker"`)) {
		t.Fatal("ordinary legacy unknown worker reference")
	}
	f := newOrdinaryVariantFixture(t, ordinaryNamed)
	snapshot := ordinaryVariantObserve(t, f)
	f.close(t)
	actualSchema, err := decode(snapshot.schemaCanonical)
	if err != nil {
		t.Fatal("ordinary observed schema")
	}
	actualRoles, err := decode(snapshot.rolesCanonical)
	if err != nil {
		t.Fatal("ordinary observed roles")
	}
	schemaBytes, err := canonical(actualSchema.(object)["application"])
	if err != nil || !bytes.Equal(schemaBytes, files["c0-schema.json"]) {
		t.Fatal("ordinary actual schema differs from previously reviewed unchanged structure")
	}
	roleBytes, err := canonical(actualRoles.(object)["application"])
	if err != nil || !bytes.Equal(roleBytes, wantRoles) {
		t.Fatal("ordinary actual no-worker ACL differs from exact reviewed placeholder projection")
	}
	t.Log("actual three-role ordinary schema and complete ACL exactly match reviewed four-role projection; no production filtering")
}
