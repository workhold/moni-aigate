package paymentruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
)

type runtimeFixture struct {
	ctx                    context.Context
	admin                  *pgxpool.Pool
	mockPath, receiverPath string
	m                      commonManifest
}

func runtimeDB(t *testing.T) *runtimeFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for dedicated payment runtime PostgreSQL tests")
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("dedicated local database required")
	}
	ctx := context.Background()
	admin, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	path, _ := configFixture(t, Mock)
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var mock mockManifest
	if e = strictDecode(raw, &mock); e != nil {
		t.Fatal(e)
	}
	f := &runtimeFixture{ctx: ctx, admin: admin, mockPath: path, m: mock.commonManifest}
	roles := allRoles(f.m)
	t.Cleanup(func() {
		for _, s := range []schemaPin{f.m.Receiver, f.m.Mock, f.m.App} {
			if _, e := admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{s.Name}.Sanitize()+" CASCADE"); e != nil {
				t.Error("cleanup schema failed")
			}
		}
		for i := len(roles) - 1; i >= 0; i-- {
			if _, e := admin.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{roles[i].Name}.Sanitize()); e != nil {
				t.Error("cleanup role failed")
			}
		}
		admin.Close()
	})
	u, _ := url.Parse(string(mustReadFile(t, filepath.Join(filepath.Dir(path), "dsn"))))
	mockPassword, _ := u.User.Password()
	passwords := map[string]string{}
	for _, r := range roles {
		var material [32]byte
		if _, e = rand.Read(material[:]); e != nil {
			t.Fatal(e)
		}
		password := base64.RawURLEncoding.EncodeToString(material[:])
		if r.Name == mock.Mock.Runtime.Name {
			password = mockPassword
		}
		passwords[r.Name] = password
		mode := "NOLOGIN"
		if r.Login {
			mode = "LOGIN PASSWORD '" + password + "'"
		}
		if _, e = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{r.Name}.Sanitize()+" "+mode+" NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); e != nil {
			t.Fatal("create isolated role failed")
		}
	}
	for _, s := range []schemaPin{f.m.App, f.m.Mock, f.m.Receiver} {
		if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{s.Name}.Sanitize()+" AUTHORIZATION "+pgx.Identifier{s.Owner.Name}.Sanitize()); e != nil {
			t.Fatal("create isolated schema failed")
		}
	}
	if e = admin.QueryRow(ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&mock.Database.OID); e != nil {
		t.Fatal(e)
	}
	schemas := []*schemaPin{&mock.App, &mock.Mock, &mock.Receiver}
	for _, s := range schemas {
		if e = admin.QueryRow(ctx, "SELECT oid FROM pg_namespace WHERE nspname=$1", s.Name).Scan(&s.OID); e != nil {
			t.Fatal(e)
		}
		for _, r := range []*rolePin{&s.Owner, &s.Runtime} {
			if e = admin.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", r.Name).Scan(&r.OID); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = admin.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", mock.AppProjector.Name).Scan(&mock.AppProjector.OID); e != nil {
		t.Fatal(e)
	}
	f.m = mock.commonManifest
	writeJSONFile(t, path, mock)
	receiverDir := t.TempDir()
	if e = os.Chmod(receiverDir, 0700); e != nil {
		t.Fatal(e)
	}
	writeSecret := func(name string, b []byte) secretFile {
		t.Helper()
		if e := os.WriteFile(filepath.Join(receiverDir, name), b, 0600); e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(b)
		return secretFile{name, hex.EncodeToString(h[:])}
	}
	receiver := receiverManifest{commonManifest: mock.commonManifest, ListenOrigin: "http://127.0.0.1:19083"}
	receiver.Component = Receiver
	receiver.OwnSchemaSQL.SHA256, _ = paymentsandboxschema.Digest(paymentsandboxschema.Receiver)
	var nonce [32]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		t.Fatal(e)
	}
	receiver.NonceFile = writeSecret("nonce", []byte(base64.RawURLEncoding.EncodeToString(nonce[:])))
	receiver.RuntimeDSNFile = writeSecret("dsn", []byte("postgres://"+receiver.Receiver.Runtime.Name+":"+passwords[receiver.Receiver.Runtime.Name]+"@127.0.0.1:55439/moni_test?sslmode=disable"))
	f.receiverPath = filepath.Join(receiverDir, "receiver.json")
	writeJSONFile(t, f.receiverPath, receiver)
	for _, component := range []Component{Mock, Receiver} {
		pin := f.m.Mock
		if component == Receiver {
			pin = f.m.Receiver
		}
		ownerConfig := admin.Config()
		ownerConfig.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{pin.Name}.Sanitize() + ",pg_catalog,pg_temp"
		ownerConfig.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, e := c.Exec(ctx, "SET ROLE "+pgx.Identifier{pin.Owner.Name}.Sanitize())
			return e
		}
		owner, e := pgxpool.NewWithConfig(ctx, ownerConfig)
		if e != nil {
			t.Fatal(e)
		}
		sql, _ := paymentsandboxschema.SQL(paymentsandboxschema.Component(component))
		if _, e = owner.Exec(ctx, sql); e != nil {
			owner.Close()
			t.Fatal("install reviewed schema failed")
		}
		cpath := path
		if component == Receiver {
			cpath = f.receiverPath
		}
		loaded, e := Load(ctx, cpath, component)
		if e != nil {
			owner.Close()
			t.Fatal(e)
		}
		installRuntimeMarker(t, owner, loaded)
		owner.Close()
		inventory, e := paymentsandboxschema.Inventory(paymentsandboxschema.Component(component))
		if e != nil {
			t.Fatal(e)
		}
		statements := []string{"REVOKE ALL ON SCHEMA " + pgx.Identifier{pin.Name}.Sanitize() + " FROM PUBLIC", "REVOKE ALL ON ALL TABLES IN SCHEMA " + pgx.Identifier{pin.Name}.Sanitize() + " FROM PUBLIC", "REVOKE ALL ON ALL FUNCTIONS IN SCHEMA " + pgx.Identifier{pin.Name}.Sanitize() + " FROM PUBLIC", "GRANT USAGE ON SCHEMA " + pgx.Identifier{pin.Name}.Sanitize() + " TO " + pgx.Identifier{pin.Runtime.Name}.Sanitize(), "GRANT SELECT ON ALL TABLES IN SCHEMA " + pgx.Identifier{pin.Name}.Sanitize() + " TO " + pgx.Identifier{pin.Runtime.Name}.Sanitize(), "GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA " + pgx.Identifier{pin.Name}.Sanitize() + " TO " + pgx.Identifier{pin.Runtime.Name}.Sanitize()}
		for _, table := range inventory.Tables {
			insert, cols := paymentsandboxschema.Permissions(paymentsandboxschema.Component(component), table.Name)
			target := pgx.Identifier{pin.Name, table.Name}.Sanitize()
			role := pgx.Identifier{pin.Runtime.Name}.Sanitize()
			if insert {
				statements = append(statements, "GRANT INSERT ON "+target+" TO "+role)
			}
			if len(cols) > 0 {
				quoted := []string{}
				for _, col := range cols {
					quoted = append(quoted, pgx.Identifier{col}.Sanitize())
				}
				statements = append(statements, "GRANT UPDATE("+strings.Join(quoted, ",")+") ON "+target+" TO "+role)
			}
		}
		for _, statement := range statements {
			if _, e = admin.Exec(ctx, statement); e != nil {
				t.Fatal("grant reviewed privileges failed")
			}
		}
	}
	if _, e = admin.Exec(ctx, "REVOKE ALL ON SCHEMA "+pgx.Identifier{f.m.App.Name}.Sanitize()+" FROM PUBLIC"); e != nil {
		t.Fatal(e)
	}
	return f
}
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func installRuntimeMarker(t *testing.T, p *pgxpool.Pool, c Config) {
	t.Helper()
	ctx := context.Background()
	m := c.manifest
	prefix := string(m.Component)
	if m.Component == Mock {
		_, e := p.Exec(ctx, `INSERT INTO mock_environment(environment_id,state_directory_id,database_oid,app_schema_name,app_schema_oid,app_owner,app_runtime,app_projector,mock_schema_name,mock_schema_oid,mock_owner,mock_runtime,receiver_schema_name,receiver_schema_oid,receiver_owner,receiver_runtime,merchant_id,payee_label,nonce_digest,api_token_digest,control_token_digest,api_origin,control_origin,callback_url,protocol_version)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,1)`, m.EnvironmentID, m.StateDirectoryID, m.Database.OID, m.App.Name, m.App.OID, m.App.Owner.Name, m.App.Runtime.Name, m.AppProjector.Name, m.Mock.Name, m.Mock.OID, m.Mock.Owner.Name, m.Mock.Runtime.Name, m.Receiver.Name, m.Receiver.OID, m.Receiver.Owner.Name, m.Receiver.Runtime.Name, m.MerchantID, m.PayeeLabel, c.nonceDigest, c.apiDigest, c.controlDigest, c.origins[API], c.origins[Control], m.CallbackURL)
		if e != nil {
			t.Fatal("mock marker failed")
		}
	} else {
		_, e := p.Exec(ctx, `INSERT INTO receiver_environment(environment_id,merchant_id,database_oid,receiver_schema_name,receiver_schema_oid,receiver_owner,receiver_runtime,state_directory_id,nonce_digest,callback_url,protocol_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1)`, m.EnvironmentID, m.MerchantID, m.Database.OID, m.Receiver.Name, m.Receiver.OID, m.Receiver.Owner.Name, m.Receiver.Runtime.Name, m.StateDirectoryID, c.nonceDigest, m.CallbackURL)
		if e != nil {
			t.Fatal("receiver marker failed")
		}
	}
	if _, e := p.Exec(ctx, "INSERT INTO "+pgx.Identifier{prefix + "_schema_migrations"}.Sanitize()+"(version,sha256) VALUES(1,$1)", m.OwnSchemaSQL.SHA256); e != nil {
		t.Fatal(e)
	}
	for _, k := range m.Keys {
		w := c.windows[k.ID]
		if _, e := p.Exec(ctx, "INSERT INTO "+pgx.Identifier{prefix + "_signing_keys"}.Sanitize()+"(key_id,key_digest,not_before,not_after) VALUES($1,$2,$3,$4)", k.ID, k.Digest, w.before, w.after); e != nil {
			t.Fatal(e)
		}
	}
}

func TestPostgresRuntimeIsolationOwnershipAndRestart(t *testing.T) {
	f := runtimeDB(t)
	for _, component := range []Component{Mock, Receiver} {
		t.Run(string(component), func(t *testing.T) {
			path := f.mockPath
			if component == Receiver {
				path = f.receiverPath
			}
			cfg, e := Load(f.ctx, path, component)
			if e != nil {
				t.Fatal(e)
			}
			r, e := Open(f.ctx, cfg)
			if e != nil {
				t.Fatal("valid runtime startup", e)
			}
			t.Cleanup(func() { _ = r.Close(context.Background()) })
			var current, session string
			if e = r.Pool().QueryRow(f.ctx, "SELECT current_user,session_user").Scan(&current, &session); e != nil || current != ownPin(cfg.manifest).Runtime.Name || session != current {
				t.Fatal("runtime is not actual LOGIN")
			}
			if _, e = Open(f.ctx, cfg); !errors.Is(e, ErrOwnershipLost) {
				t.Fatal("second instance accepted", e)
			}
			cancelled, cancel := context.WithCancel(f.ctx)
			cancel()
			if e = r.Check(cancelled); !errors.Is(e, context.Canceled) {
				t.Fatal("normal cancellation misclassified")
			}
			if e = r.Check(f.ctx); e != nil {
				t.Fatal("normal cancellation fenced runtime")
			}
			var wg sync.WaitGroup
			errs := make(chan error, 20)
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); errs <- r.Check(f.ctx) }()
			}
			wg.Wait()
			close(errs)
			for e := range errs {
				if e != nil {
					t.Fatal("concurrent fresh guard", e)
				}
			}
			if e = r.Close(f.ctx); e != nil {
				t.Fatal(e)
			}
			restarted, e := Open(f.ctx, cfg)
			if e != nil {
				t.Fatal("same identity restart", e)
			}
			var pid int
			if e = restarted.ownership.conn.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); e != nil {
				t.Fatal(e)
			}
			if _, e = f.admin.Exec(f.ctx, "SELECT pg_terminate_backend($1)", pid); e != nil {
				t.Fatal(e)
			}
			if e = restarted.Check(f.ctx); !errors.Is(e, ErrOwnershipLost) {
				t.Fatal("lost ownership not fenced")
			}
			select {
			case <-restarted.Done():
			case <-time.After(time.Second):
				t.Fatal("lost runtime did not cancel lifecycle")
			}
			if e = restarted.Check(f.ctx); !errors.Is(e, ErrOwnershipLost) {
				t.Fatal("lost ownership reconnected")
			}
			_ = restarted.Close(f.ctx)
		})
	}
}

func TestPostgresRuntimeRejectsCatalogIdentityAndPermissionDrift(t *testing.T) {
	f := runtimeDB(t)
	cfg, e := Load(f.ctx, f.mockPath, Mock)
	if e != nil {
		t.Fatal(e)
	}
	changed := cfg
	changed.manifest = cfg.manifest
	changed.manifest.Mock.OID++
	if _, e = Open(f.ctx, changed); !errors.Is(e, ErrEnvironment) {
		t.Fatal("wrong schema OID accepted", e)
	}
	schema := pgx.Identifier{f.m.Mock.Name}.Sanitize()
	role := pgx.Identifier{f.m.Mock.Runtime.Name}.Sanitize()
	if _, e = f.admin.Exec(f.ctx, "GRANT UPDATE ON "+schema+".mock_commands TO "+role); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(f.ctx, cfg); !errors.Is(e, ErrRole) {
		t.Fatal("extra history write accepted", e)
	}
	if _, e = f.admin.Exec(f.ctx, "REVOKE UPDATE ON "+schema+".mock_commands FROM "+role); e != nil {
		t.Fatal(e)
	}
	if _, e = f.admin.Exec(f.ctx, "ALTER TABLE "+schema+".mock_commands DISABLE TRIGGER immutable_history"); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(f.ctx, cfg); !errors.Is(e, ErrState) {
		t.Fatal("disabled history trigger accepted", e)
	}
	if _, e = f.admin.Exec(f.ctx, "ALTER TABLE "+schema+".mock_commands ENABLE TRIGGER immutable_history"); e != nil {
		t.Fatal(e)
	}
	changed = cfg
	changed.nonceDigest = strings.Repeat("0", 64)
	if _, e = Open(f.ctx, changed); !errors.Is(e, ErrState) {
		t.Fatal("wrong state nonce accepted", e)
	}
	if _, e = f.admin.Exec(f.ctx, "CREATE TABLE "+schema+".unexpected(id integer)"); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(f.ctx, cfg); !errors.Is(e, ErrState) {
		t.Fatal("unexpected catalog object accepted", e)
	}
}

func TestPostgresRuntimeRejectsAdditionalNamespaceObjects(t *testing.T) {
	f := runtimeDB(t)
	for _, component := range []Component{Mock, Receiver} {
		t.Run(string(component), func(t *testing.T) {
			path, pin, table := f.mockPath, f.m.Mock, "mock_commands"
			if component == Receiver {
				path, pin, table = f.receiverPath, f.m.Receiver, "receiver_inbox"
			}
			cfg, e := Load(f.ctx, path, component)
			if e != nil {
				t.Fatal("component fixture configuration rejected")
			}
			// The service side is an actual restricted LOGIN. Only the offline
			// installer below is an administrator SET ROLE owner session.
			proveNormal := func() {
				t.Helper()
				r, e := Open(f.ctx, cfg)
				if e != nil {
					t.Fatal("reviewed namespace rejected", e)
				}
				var current, session string
				e = r.Pool().QueryRow(f.ctx, "SELECT current_user,session_user").Scan(&current, &session)
				closeErr := r.Close(f.ctx)
				if e != nil || closeErr != nil || current != pin.Runtime.Name || session != current {
					t.Fatal("component is not the expected actual runtime LOGIN")
				}
			}
			proveNormal()
			ownerConfig := f.admin.Config()
			ownerConfig.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{pin.Name}.Sanitize() + ",pg_catalog,pg_temp"
			ownerConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
				_, e := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{pin.Owner.Name}.Sanitize())
				return e
			}
			owner, e := pgxpool.NewWithConfig(f.ctx, ownerConfig)
			if e != nil {
				t.Fatal("offline owner session unavailable")
			}
			defer owner.Close()
			for _, tc := range []struct{ name, create, drop string }{
				{"enum", "CREATE TYPE extra_enum AS ENUM ('present')", "DROP TYPE extra_enum"},
				{"domain", "CREATE DOMAIN extra_domain AS bigint CHECK(VALUE>=0)", "DROP DOMAIN extra_domain"},
				{"nonconflicting_operator", "CREATE OPERATOR === (LEFTARG=pg_catalog.int8,RIGHTARG=pg_catalog.int8,FUNCTION=pg_catalog.int8eq)", "DROP OPERATOR === (pg_catalog.int8,pg_catalog.int8)"},
				{"ordinary_table_rule", "CREATE RULE extra_rule AS ON DELETE TO " + table + " DO INSTEAD NOTHING", "DROP RULE extra_rule ON " + table},
				{"inactive_policy", "CREATE POLICY extra_policy ON " + table + " USING (true)", "DROP POLICY extra_policy ON " + table},
				{"collation", `CREATE COLLATION extra_collation FROM pg_catalog."C"`, "DROP COLLATION extra_collation"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if _, e := owner.Exec(f.ctx, tc.create); e != nil {
						t.Fatal("offline owner could not create the extra object")
					}
					unexpected, rejected := Open(f.ctx, cfg)
					if unexpected != nil {
						_ = unexpected.Close(f.ctx)
					}
					if _, e := owner.Exec(f.ctx, tc.drop); e != nil {
						t.Fatal("offline owner could not remove its exact extra object")
					}
					if !errors.Is(rejected, ErrState) {
						t.Fatal("additional namespace object was not refused", rejected)
					}
					proveNormal()
				})
			}
		})
	}
}
