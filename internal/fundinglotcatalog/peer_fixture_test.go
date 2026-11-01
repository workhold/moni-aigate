package fundinglotcatalog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

const peerFixtureDDL = "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047"
const peerFixtureACL = "9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae"

// This is an offline catalog fixture, not a payment sandbox installation or a
// runtime proof issuer. It creates no peer environment/key/history records.
type catalogPeerFixture struct {
	ctx                                       context.Context
	cancel                                    context.CancelFunc
	config                                    *pgxpool.Config
	pins                                      Pins
	admin, appOwner, mockOwner, receiverOwner *pgxpool.Pool
	app, worker, projector, mock, receiver    *pgxpool.Pool
	schemas                                   []string
	roles                                     []string
	passwords                                 map[string]string
	created                                   bool
	closed                                    bool
	mu                                        sync.Mutex
	backends                                  []ordinaryVariantBackend
}

func newCatalogPeerFixture(t *testing.T) *catalogPeerFixture {
	t.Helper()
	raw := os.Getenv("C0_CATALOG_DATABASE_URL")
	if raw == "" {
		t.Skip("requires explicit dedicated C0_CATALOG_DATABASE_URL; no catalog database acceptance")
	}
	cfg, err := ordinaryVariantConfig(raw)
	if err != nil {
		t.Fatal("peer fixture closed database configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	prefix := "flcp_" + ordinaryVariantRandom(t)
	f := &catalogPeerFixture{ctx: ctx, cancel: cancel, config: cfg, passwords: map[string]string{}, schemas: []string{prefix + "_a", prefix + "_m", prefix + "_q"}, roles: []string{prefix + "_ao", prefix + "_a", prefix + "_w", prefix + "_p", prefix + "_mo", prefix + "_m", prefix + "_qo", prefix + "_q"}}
	t.Cleanup(func() { f.close(t) })
	f.admin, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("peer fixture administrator pool")
	}
	var version int
	var valid bool
	if f.admin.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer,current_user=session_user AND current_user='moni_test' AND current_database()='moni_test' AND host(inet_server_addr())='127.0.0.1' AND inet_server_port()=55439 AND r.rolsuper AND r.rolcanlogin AND d.datdba=r.oid FROM pg_roles r JOIN pg_database d ON d.datname=current_database() WHERE r.rolname=current_user`).Scan(&version, &valid) != nil || !valid || version/10000 != 16 {
		t.Fatal("peer fixture administrator identity")
	}
	root := ordinaryVariantRoot(t)
	for _, pin := range peerSourcePins {
		ordinaryVariantSource(t, root, pin.Name, pin.SHA256)
	}
	ddl := ordinaryVariantSource(t, root, "specs/022-self-service-payments/c0-production-schema.sql", peerFixtureDDL)
	acl := ordinaryVariantSource(t, root, "specs/022-self-service-payments/c0-production-permissions.sql", peerFixtureACL)
	f.create(t)
	f.appOwner = f.pool(t, 0, 0, true, "application_owner")
	f.mockOwner = f.pool(t, 1, 4, true, "mock_owner")
	f.receiverOwner = f.pool(t, 2, 6, true, "receiver_owner")
	if migrations.Apply(ctx, f.appOwner) != nil {
		t.Fatal("peer fixture original 001-022 migrations")
	}
	// Match the frozen sandbox v1 order before adding C0. In particular the
	// original PUBLIC function default is revoked before any C0 function exists.
	if _, err = f.appOwner.Exec(ctx, "REVOKE ALL ON ALL FUNCTIONS IN SCHEMA "+pgx.Identifier{f.schemas[0]}.Sanitize()+" FROM PUBLIC; GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA "+pgx.Identifier{f.schemas[0]}.Sanitize()+" TO "+pgx.Identifier{f.roles[1]}.Sanitize()+","+pgx.Identifier{f.roles[3]}.Sanitize()); err != nil {
		t.Fatal("peer fixture original function ACL")
	}
	if operations.ProvisionFor(ctx, f.appOwner, f.schemas[0], f.roles[1], f.roles[3]) != nil {
		t.Fatal("peer fixture original application provision")
	}
	// This conspicuously synthetic marker exists solely because the frozen
	// owner-only worker provisioner requires it; it is not used as an oracle.
	if _, err = f.appOwner.Exec(ctx, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role) SELECT '00000000-0000-4000-8000-000000000022','catalog-fixture-only','catalog-fixture-only',d.oid,n.oid,$1,$2,$3,$4 FROM pg_catalog.pg_database d CROSS JOIN pg_catalog.pg_namespace n WHERE d.datname=current_database() AND n.nspname=$5`, strings.Repeat("c", 64), f.roles[1], f.roles[2], f.roles[3], f.schemas[0]); err != nil {
		t.Fatal("peer fixture synthetic worker marker")
	}
	if operations.ProvisionPaymentWorkerFor(ctx, f.appOwner, f.schemas[0], f.roles[1], f.roles[2], f.roles[3]) != nil {
		t.Fatal("peer fixture original worker provision")
	}
	if _, err = f.appOwner.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+pgx.Identifier{f.schemas[0], "funding_uuid"}.Sanitize()+"(text) TO "+pgx.Identifier{f.roles[2]}.Sanitize()); err != nil {
		t.Fatal("peer fixture original worker leaf")
	}
	if _, err = f.appOwner.Exec(ctx, string(ddl)); err != nil {
		t.Fatal("peer fixture fixed C0 DDL")
	}
	text := string(acl)
	for i, key := range []string{"fl_app", "fl_worker", "fl_projector"} {
		text = strings.ReplaceAll(text, `:"`+key+`"`, pgx.Identifier{f.roles[i+1]}.Sanitize())
	}
	if strings.Contains(text, `:"`) {
		t.Fatal("peer fixture unresolved ACL placeholder")
	}
	if _, err = f.appOwner.Exec(ctx, text); err != nil {
		t.Fatal("peer fixture fixed C0 ACL")
	}
	f.installPeer(t, paymentsandboxschema.Mock, f.mockOwner, 1, 5)
	f.installPeer(t, paymentsandboxschema.Receiver, f.receiverOwner, 2, 7)
	f.app = f.pool(t, 0, 1, false, "application")
	f.worker = f.pool(t, 0, 2, false, "worker")
	f.projector = f.pool(t, 0, 3, false, "projector")
	f.mock = f.pool(t, 1, 5, false, "mock")
	f.receiver = f.pool(t, 2, 7, false, "receiver")
	f.loadPins(t)
	return f
}

func (f *catalogPeerFixture) create(t *testing.T) {
	t.Helper()
	var absent bool
	if f.admin.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($1::text[])) AND NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=ANY($2::text[]))`, f.roles, f.schemas).Scan(&absent) != nil || !absent {
		t.Fatal("peer fixture names must be absent")
	}
	tx, err := f.admin.Begin(f.ctx)
	if err != nil {
		t.Fatal("peer fixture creation transaction")
	}
	defer tx.Rollback(context.Background())
	f.created = true
	for i, role := range f.roles {
		login := "LOGIN"
		if i == 0 || i == 4 || i == 6 {
			login = "NOLOGIN"
		}
		password := ordinaryVariantRandom(t)
		f.passwords[role] = password
		if _, err = tx.Exec(f.ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" "+login+" NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1 PASSWORD '"+password+"'"); err != nil {
			t.Fatal("peer fixture create role")
		}
	}
	for i, schema := range f.schemas {
		owner := []int{0, 4, 6}[i]
		if _, err = tx.Exec(f.ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()+" AUTHORIZATION "+pgx.Identifier{f.roles[owner]}.Sanitize()); err != nil {
			t.Fatal("peer fixture create schema")
		}
		if _, err = tx.Exec(f.ctx, "REVOKE ALL ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" FROM PUBLIC"); err != nil {
			t.Fatal("peer fixture schema PUBLIC closure")
		}
	}
	if tx.Commit(f.ctx) != nil {
		t.Fatal("peer fixture creation outcome unknown; cleanup only")
	}
}

func (f *catalogPeerFixture) pool(t *testing.T, schemaIndex, roleIndex int, owner bool, label string) *pgxpool.Pool {
	t.Helper()
	cfg := f.config.Copy()
	role := f.roles[roleIndex]
	if !owner {
		cfg.ConnConfig.User, cfg.ConnConfig.Password = role, f.passwords[role]
	}
	app := "c0_peer_" + label
	cfg.ConnConfig.RuntimeParams = map[string]string{"search_path": pgx.Identifier{f.schemas[schemaIndex]}.Sanitize() + ",pg_catalog,pg_temp", "application_name": app}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		b := ordinaryVariantBackend{connection: c, application: app}
		if c.QueryRow(ctx, `SELECT pid,backend_start,datid FROM pg_stat_activity WHERE pid=pg_backend_pid()`).Scan(&b.pid, &b.started, &b.database) != nil {
			return fmt.Errorf("peer_fixture_backend")
		}
		f.mu.Lock()
		f.backends = append(f.backends, b)
		f.mu.Unlock()
		if owner {
			_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
			return err
		}
		return nil
	}
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil || p.Ping(f.ctx) != nil {
		if p != nil {
			p.Close()
		}
		t.Fatal("peer fixture role pool")
	}
	return p
}

func (f *catalogPeerFixture) installPeer(t *testing.T, c paymentsandboxschema.Component, owner *pgxpool.Pool, schemaIndex, roleIndex int) {
	t.Helper()
	sql, err := paymentsandboxschema.SQL(c)
	if err != nil {
		t.Fatal("peer fixture fixed SQL")
	}
	if _, err = owner.Exec(f.ctx, sql); err != nil {
		t.Fatal("peer fixture peer SQL install")
	}
	schema, role := pgx.Identifier{f.schemas[schemaIndex]}.Sanitize(), pgx.Identifier{f.roles[roleIndex]}.Sanitize()
	for _, sql := range []string{"REVOKE ALL ON ALL TABLES IN SCHEMA " + schema + " FROM PUBLIC", "REVOKE ALL ON ALL FUNCTIONS IN SCHEMA " + schema + " FROM PUBLIC", "GRANT USAGE ON SCHEMA " + schema + " TO " + role, "GRANT SELECT ON ALL TABLES IN SCHEMA " + schema + " TO " + role, "GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA " + schema + " TO " + role} {
		if _, err = owner.Exec(f.ctx, sql); err != nil {
			t.Fatal("peer fixture exact own ACL")
		}
	}
	inv, err := paymentsandboxschema.Inventory(c)
	if err != nil {
		t.Fatal("peer fixture frozen inventory")
	}
	for _, table := range inv.Tables {
		insert, update := paymentsandboxschema.Permissions(c, table.Name)
		qualified := pgx.Identifier{f.schemas[schemaIndex], table.Name}.Sanitize()
		if insert {
			if _, err = owner.Exec(f.ctx, "GRANT INSERT ON "+qualified+" TO "+role); err != nil {
				t.Fatal("peer fixture INSERT ACL")
			}
		}
		if len(update) > 0 {
			columns := make([]string, len(update))
			for i, c := range update {
				columns[i] = pgx.Identifier{c}.Sanitize()
			}
			if _, err = owner.Exec(f.ctx, "GRANT UPDATE("+strings.Join(columns, ",")+") ON "+qualified+" TO "+role); err != nil {
				t.Fatal("peer fixture UPDATE ACL")
			}
		}
	}
}

func (f *catalogPeerFixture) loadPins(t *testing.T) {
	t.Helper()
	var db uint32
	if f.admin.QueryRow(f.ctx, `SELECT oid FROM pg_database WHERE datname=current_database()`).Scan(&db) != nil {
		t.Fatal("peer fixture database OID")
	}
	roles := make([]RolePin, len(f.roles))
	for i, name := range f.roles {
		roles[i].Name = name
		if f.admin.QueryRow(f.ctx, `SELECT oid FROM pg_roles WHERE rolname=$1`, name).Scan(&roles[i].OID) != nil {
			t.Fatal("peer fixture role pin")
		}
	}
	schemas := make([]SchemaPin, len(f.schemas))
	for i, name := range f.schemas {
		schemas[i] = SchemaPin{Name: name, NamespaceOwner: roles[[]int{0, 4, 6}[i]], ObjectOwner: roles[[]int{0, 4, 6}[i]]}
		if f.admin.QueryRow(f.ctx, `SELECT oid FROM pg_namespace WHERE nspname=$1`, name).Scan(&schemas[i].OID) != nil {
			t.Fatal("peer fixture schema pin")
		}
	}
	f.pins = Pins{Mode: Sandbox, Database: DatabasePin{Host: "127.0.0.1", Port: 55439, Name: "moni_test", OID: db}, AppSchema: schemas[0], App: roles[1], Worker: &roles[2], Projector: roles[3], Sandbox: &SandboxPins{Mock: schemas[1], MockRuntime: roles[5], Receiver: schemas[2], ReceiverRuntime: roles[7]}}
}

func (f *catalogPeerFixture) close(t *testing.T) {
	t.Helper()
	if f == nil || f.closed {
		return
	}
	f.closed = true
	defer f.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.mu.Lock()
	backends := append([]ordinaryVariantBackend(nil), f.backends...)
	f.mu.Unlock()
	for _, b := range backends {
		if b.connection != nil {
			_ = b.connection.Close(ctx)
		}
	}
	for _, p := range []*pgxpool.Pool{f.receiver, f.mock, f.projector, f.worker, f.app, f.receiverOwner, f.mockOwner, f.appOwner} {
		if p != nil {
			p.Close()
		}
	}
	if f.admin == nil {
		return
	}
	defer f.admin.Close()
	for _, b := range backends {
		for {
			var exists bool
			if f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND datid=$3 AND application_name=$4)`, b.pid, b.started, b.database, b.application).Scan(&exists) != nil {
				t.Error("peer fixture cleanup backend audit")
				return
			}
			if !exists {
				break
			}
			if _, err := f.admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND datid=$3 AND application_name=$4 AND pid<>pg_backend_pid()`, b.pid, b.started, b.database, b.application); err != nil {
				t.Error("peer fixture cleanup backend")
				return
			}
			select {
			case <-ctx.Done():
				t.Error("peer fixture cleanup timeout")
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if f.created {
		for i := len(f.schemas) - 1; i >= 0; i-- {
			if _, err := f.admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{f.schemas[i]}.Sanitize()+" CASCADE"); err != nil {
				t.Error("peer fixture cleanup schema")
				return
			}
		}
		for i := len(f.roles) - 1; i >= 0; i-- {
			if _, err := f.admin.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{f.roles[i]}.Sanitize()); err != nil {
				t.Error("peer fixture cleanup role")
				return
			}
		}
	}
	var gone bool
	if f.admin.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($1::text[])) AND NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=ANY($2::text[]))`, f.roles, f.schemas).Scan(&gone) != nil || !gone {
		t.Error("peer fixture cleanup final audit")
	}
	f.passwords = nil
}

func (f *catalogPeerFixture) check(t *testing.T, pool *pgxpool.Pool, want error) {
	t.Helper()
	tx, err := pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("peer check caller RR")
	}
	defer tx.Rollback(context.Background())
	var before, after string
	if tx.QueryRow(f.ctx, `SELECT txid_current_snapshot()::text`).Scan(&before) != nil {
		t.Fatal("peer check caller snapshot")
	}
	if got := verifyPeerRoleClosureTx(f.ctx, tx, f.pins); got != want {
		t.Fatalf("peer closure result: got %v, want %v", got, want)
	}
	if tx.QueryRow(f.ctx, `SELECT txid_current_snapshot()::text`).Scan(&after) != nil || before != after {
		t.Fatal("peer check changed caller transaction/snapshot")
	}
	if tx.Commit(f.ctx) != nil {
		t.Fatal("peer check caller commit")
	}
}

func TestCatalogPeerBaselineAndActualLogins(t *testing.T) {
	f := newCatalogPeerFixture(t)
	for _, r := range []struct {
		pool *pgxpool.Pool
		name string
	}{{f.app, f.roles[1]}, {f.worker, f.roles[2]}, {f.projector, f.roles[3]}, {f.mock, f.roles[5]}, {f.receiver, f.roles[7]}} {
		var current, session string
		if r.pool.QueryRow(f.ctx, `SELECT current_user,session_user`).Scan(&current, &session) != nil || current != r.name || session != r.name {
			t.Fatal("peer actual runtime LOGIN identity")
		}
	}
	f.check(t, f.app, nil)
	f.check(t, f.worker, nil)
	f.check(t, f.projector, nil)
	var automaticTypes int
	var automaticUsage, sourceProofHidden bool
	if f.admin.QueryRow(f.ctx, `SELECT count(*)::integer,bool_and(has_type_privilege($2::oid,t.oid,'USAGE') AND NOT has_type_privilege($2::oid,t.oid,'USAGE WITH GRANT OPTION')) FROM pg_type t WHERE t.typnamespace=$1 AND (t.typrelid IN(SELECT oid FROM pg_class WHERE relnamespace=$1 AND relname=ANY($3::text[])) OR t.typelem IN(SELECT reltype FROM pg_class WHERE relnamespace=$1 AND relname=ANY($3::text[])))`, f.pins.AppSchema.OID, f.pins.Projector.OID, installationTables[:]).Scan(&automaticTypes, &automaticUsage) != nil || automaticTypes != 32 || !automaticUsage {
		t.Fatal("automatic row/array PUBLIC USAGE must not be mistaken for data privilege")
	}
	if f.admin.QueryRow(f.ctx, `SELECT bool_and(NOT has_type_privilege($2::oid,t.oid,'USAGE') AND NOT has_type_privilege($3::oid,t.oid,'USAGE') AND NOT has_type_privilege($4::oid,t.oid,'USAGE')) FROM pg_type t WHERE t.typnamespace=$1 AND t.typname=ANY(ARRAY['fl_source_proof','_fl_source_proof'])`, f.pins.AppSchema.OID, f.pins.Projector.OID, f.pins.Sandbox.MockRuntime.OID, f.pins.Sandbox.ReceiverRuntime.OID).Scan(&sourceProofHidden) != nil || !sourceProofHidden {
		t.Fatal("named source proof and dependent array must remain hidden from projector/peers")
	}
	for _, table := range []string{pgx.Identifier{f.schemas[1], "mock_orders"}.Sanitize(), pgx.Identifier{f.schemas[2], "receiver_inbox"}.Sanitize()} {
		_, err := f.app.Exec(f.ctx, "SELECT * FROM "+table+" LIMIT 0")
		pgerr, ok := err.(*pgconn.PgError)
		if !ok || pgerr.Code != "42501" {
			t.Fatal("APP cross-peer SELECT did not return 42501")
		}
	}
	t.Log("fixed 001-022+C0 sandbox baseline; eight independent roles, five actual runtime LOGINs; ten negative privilege groups; caller same RR; real peer SELECT 42501")
}

func TestCatalogPeerRejectsForeignPrivilegesMembershipAndOwnership(t *testing.T) {
	f := newCatalogPeerFixture(t)
	q := func(s ...string) string { return pgx.Identifier(s).Sanitize() }
	// Every directed pair is exercised independently. No schema USAGE is
	// required for the table/column/function tests to detect their effective ACL.
	pairs := []struct{ role, schema string }{
		{f.roles[1], f.schemas[1]}, {f.roles[1], f.schemas[2]},
		{f.roles[2], f.schemas[1]}, {f.roles[2], f.schemas[2]},
		{f.roles[3], f.schemas[1]}, {f.roles[3], f.schemas[2]},
		{f.roles[5], f.schemas[0]}, {f.roles[5], f.schemas[2]},
		{f.roles[7], f.schemas[0]}, {f.roles[7], f.schemas[1]},
	}
	cases := []struct{ name, sql string }{}
	for i, pair := range pairs {
		cases = append(cases, struct{ name, sql string }{fmt.Sprintf("cross_schema_usage_%02d", i), "GRANT USAGE ON SCHEMA " + q(pair.schema) + " TO " + q(pair.role)})
	}
	cases = append(cases, []struct{ name, sql string }{
		{"cross_schema_create", "GRANT CREATE ON SCHEMA " + q(f.schemas[1]) + " TO " + q(f.roles[1])},
		{"cross_schema_grant_option", "GRANT USAGE ON SCHEMA " + q(f.schemas[1]) + " TO " + q(f.roles[1]) + " WITH GRANT OPTION"},
		{"cross_table_select_without_schema_usage", "GRANT SELECT ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1])},
		{"cross_table_insert", "GRANT INSERT ON " + q(f.schemas[2], "receiver_inbox") + " TO " + q(f.roles[2])},
		{"cross_table_update", "GRANT UPDATE ON " + q(f.schemas[0], "tenants") + " TO " + q(f.roles[5])},
		{"cross_table_delete", "GRANT DELETE ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[7])},
		{"cross_table_truncate", "GRANT TRUNCATE ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[3])},
		{"cross_table_references", "GRANT REFERENCES ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1])},
		{"cross_table_trigger", "GRANT TRIGGER ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1])},
		{"cross_table_grant_option", "GRANT SELECT ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1]) + " WITH GRANT OPTION"},
		{"cross_column_select_without_schema_usage", "GRANT SELECT(merchant_order_id) ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1])},
		{"cross_column_insert", "GRANT INSERT(merchant_order_id) ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1])},
		{"cross_column_update", "GRANT UPDATE(state) ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1])},
		{"cross_column_references", "GRANT REFERENCES(merchant_order_id) ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1])},
		{"cross_column_grant_option", "GRANT UPDATE(state) ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1]) + " WITH GRANT OPTION"},
		{"cross_function_execute_without_schema_usage", "GRANT EXECUTE ON FUNCTION " + q(f.schemas[1], "mock_uuid") + "(text) TO " + q(f.roles[1])},
		{"cross_function_grant_option", "GRANT EXECUTE ON FUNCTION " + q(f.schemas[1], "mock_uuid") + "(text) TO " + q(f.roles[1]) + " WITH GRANT OPTION"},
		{"membership_inherit_set_admin_false", "GRANT " + q(f.roles[5]) + " TO " + q(f.roles[1]) + " WITH ADMIN FALSE, INHERIT FALSE, SET FALSE"},
		{"membership_admin", "GRANT " + q(f.roles[5]) + " TO " + q(f.roles[1]) + " WITH ADMIN TRUE, INHERIT FALSE, SET FALSE"},
		{"membership_owner_to_runtime", "GRANT " + q(f.roles[0]) + " TO " + q(f.roles[1]) + " WITH INHERIT FALSE, SET TRUE"},
		{"membership_runtime_to_owner", "GRANT " + q(f.roles[1]) + " TO " + q(f.roles[0]) + " WITH INHERIT FALSE, SET FALSE"},
		{"runtime_relation_owner", "CREATE TABLE " + q(f.schemas[1], "catalog_runtime_owned") + "(id integer); ALTER TABLE " + q(f.schemas[1], "catalog_runtime_owned") + " OWNER TO " + q(f.roles[1])},
		{"runtime_function_owner", "CREATE FUNCTION " + q(f.schemas[1], "catalog_runtime_owned") + "() RETURNS integer LANGUAGE SQL AS 'SELECT 1'; ALTER FUNCTION " + q(f.schemas[1], "catalog_runtime_owned") + "() OWNER TO " + q(f.roles[1])},
		{"runtime_namespace_owner", "ALTER SCHEMA " + q(f.schemas[1]) + " OWNER TO " + q(f.roles[1])},
		{"runtime_type_owner", "CREATE TYPE " + q(f.schemas[1], "catalog_runtime_owned") + " AS ENUM ('synthetic'); ALTER TYPE " + q(f.schemas[1], "catalog_runtime_owned") + " OWNER TO " + q(f.roles[1])},
		{"peer_source_proof_type_usage", "GRANT USAGE ON TYPE " + q(f.schemas[0], "fl_source_proof") + " TO " + q(f.roles[5])},
		{"runtime_inherit_flag", "ALTER ROLE " + q(f.roles[1]) + " INHERIT"},
		{"owner_login_flag", "ALTER ROLE " + q(f.roles[0]) + " LOGIN"},
		{"runtime_connection_limit", "ALTER ROLE " + q(f.roles[1]) + " CONNECTION LIMIT 1"},
		{"runtime_role_config", "ALTER ROLE " + q(f.roles[1]) + " SET statement_timeout='1s'"},
		{"runtime_expiry", "ALTER ROLE " + q(f.roles[1]) + " VALID UNTIL '2035-01-01'"},
	}...)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx, err := f.admin.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			if err != nil {
				t.Fatal("peer mutation caller transaction")
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(f.ctx, c.sql); err != nil {
				t.Fatal("peer mutation fixture setup")
			}
			if verifyPeerRoleClosureTx(f.ctx, tx, f.pins) != ErrCatalog {
				t.Fatal("peer mutation accepted")
			}
			// A safe verification failure must leave the caller transaction usable.
			var one int
			if tx.QueryRow(f.ctx, `SELECT 1`).Scan(&one) != nil || one != 1 {
				t.Fatal("peer negative aborted caller transaction")
			}
			if tx.Rollback(f.ctx) != nil {
				t.Fatal("peer mutation rollback")
			}
			f.check(t, f.app, nil)
		})
	}
}

func TestCatalogPeerRejectsInvalidPinRegistry(t *testing.T) {
	p := Pins{Mode: Ordinary}
	if _, err := peerRoleRegistry(p); err != ErrCatalog {
		t.Fatal("ordinary peer registry accepted")
	}
	base := Pins{Mode: Sandbox, AppSchema: SchemaPin{Name: "a", OID: 10, NamespaceOwner: RolePin{"ao", 1}, ObjectOwner: RolePin{"ao", 1}}, App: RolePin{"a", 2}, Worker: &RolePin{"w", 3}, Projector: RolePin{"p", 4}, Sandbox: &SandboxPins{Mock: SchemaPin{Name: "m", OID: 11, NamespaceOwner: RolePin{"mo", 5}, ObjectOwner: RolePin{"mo", 5}}, MockRuntime: RolePin{"m", 6}, Receiver: SchemaPin{Name: "q", OID: 12, NamespaceOwner: RolePin{"qo", 7}, ObjectOwner: RolePin{"qo", 7}}, ReceiverRuntime: RolePin{"q", 8}}}
	if _, err := peerRoleRegistry(base); err != nil {
		t.Fatal("valid peer registry rejected")
	}
	cases := []func(*Pins){
		func(p *Pins) { p.Worker = nil }, func(p *Pins) { p.Sandbox = nil }, func(p *Pins) { p.App.OID = 1 }, func(p *Pins) { p.App.Name = "ao" },
		func(p *Pins) { p.Sandbox.Mock.Name = "a" }, func(p *Pins) { p.Sandbox.Mock.OID = 10 }, func(p *Pins) { p.Sandbox.Mock.ObjectOwner = p.App },
	}
	for _, mutate := range cases {
		p := base
		s := *base.Sandbox
		p.Sandbox = &s
		mutate(&p)
		if _, err := peerRoleRegistry(p); err != ErrCatalog {
			t.Fatal("invalid peer registry accepted")
		}
	}
}

func TestCatalogPeerPublicAPIs(t *testing.T) {
	f := newCatalogPeerFixture(t)
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("sandbox public caller RR")
	}
	defer tx.Rollback(context.Background())
	snapshot, err := VerifySnapshotTx(f.ctx, tx, f.pins)
	if err != nil || snapshot == nil || snapshot.VerifyTx(f.ctx, tx) != nil || !snapshot.MatchesPins(f.pins) {
		t.Fatal("sandbox snapshot API")
	}
	installation, err := VerifyInstallationTx(f.ctx, tx, f.pins)
	if err != nil || installation == nil || installation.VerifyTx(f.ctx, tx) != nil {
		t.Fatal("sandbox empty installation API")
	}
	if tx.Commit(f.ctx) != nil || snapshot.VerifyTx(f.ctx, tx) != ErrCatalog || installation.VerifyTx(f.ctx, tx) != ErrCatalog {
		t.Fatal("sandbox public proofs survived transaction commit")
	}
	for _, r := range []struct {
		pool *pgxpool.Pool
		role RuntimeRole
	}{{f.app, Application}, {f.worker, Worker}, {f.projector, Projector}} {
		tx, err := r.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			t.Fatal("sandbox actual runtime caller RR RO")
		}
		proof, err := VerifyRuntimeTx(f.ctx, tx, f.pins, r.role)
		if err != nil || proof == nil || proof.VerifyTx(f.ctx, tx) != nil {
			tx.Rollback(context.Background())
			t.Fatal("sandbox actual runtime API")
		}
		if tx.Commit(f.ctx) != nil {
			t.Fatal("sandbox actual runtime caller commit")
		}
	}
	t.Log("full sandbox expected: Snapshot + sixteen-empty Installation + actual APP/worker/projector Runtime API; proofs bound to caller RR lifetime")
}

func TestCatalogPeerPublicAPIRejectsSameRRPeerDrift(t *testing.T) {
	f := newCatalogPeerFixture(t)
	baseline := func() {
		tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			t.Fatal("sandbox drift baseline caller RR")
		}
		defer tx.Rollback(context.Background())
		if proof, err := VerifySnapshotTx(f.ctx, tx, f.pins); err != nil || proof == nil || proof.VerifyTx(f.ctx, tx) != nil {
			t.Fatal("sandbox peer drift baseline/restoration")
		}
		if tx.Rollback(f.ctx) != nil {
			t.Fatal("sandbox drift baseline caller rollback")
		}
	}
	baseline()
	q := func(s ...string) string { return pgx.Identifier(s).Sanitize() }
	cases := []struct{ name, sql string }{
		{"mock_column", "ALTER TABLE " + q(f.schemas[1], "mock_orders") + " ALTER COLUMN state DROP NOT NULL"},
		{"receiver_constraint", "ALTER TABLE " + q(f.schemas[2], "receiver_inbox") + " ADD CONSTRAINT catalog_synthetic CHECK (true)"},
		{"mock_prosrc", "CREATE OR REPLACE FUNCTION " + q(f.schemas[1], "mock_uuid") + "(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT SET search_path FROM CURRENT AS 'SELECT true'"},
		{"receiver_new_function", "CREATE FUNCTION " + q(f.schemas[2], "catalog_synthetic") + "() RETURNS integer LANGUAGE sql AS 'SELECT 1'"},
		{"mock_column_acl", "GRANT UPDATE(state) ON " + q(f.schemas[1], "mock_orders") + " TO " + q(f.roles[1])},
		{"receiver_function_acl", "GRANT EXECUTE ON FUNCTION " + q(f.schemas[2], "receiver_uuid") + "(text) TO " + q(f.roles[3])},
		{"peer_membership", "GRANT " + q(f.roles[5]) + " TO " + q(f.roles[7]) + " WITH INHERIT FALSE, SET FALSE"},
		{"mock_runtime_owner", "ALTER TABLE " + q(f.schemas[1], "mock_orders") + " OWNER TO " + q(f.roles[5])},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx, err := f.admin.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			if err != nil {
				t.Fatal("sandbox drift caller RR")
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(f.ctx, "SET LOCAL search_path="+q(f.schemas[0])+",pg_catalog,pg_temp"); err != nil {
				t.Fatal("sandbox drift test caller path")
			}
			if _, err = tx.Exec(f.ctx, c.sql); err != nil {
				t.Fatal("sandbox peer drift fixture")
			}
			if proof, err := VerifySnapshotTx(f.ctx, tx, f.pins); proof != nil || err != ErrCatalog {
				t.Fatal("sandbox peer drift accepted by public API")
			}
			var one int
			if tx.QueryRow(f.ctx, `SELECT 1`).Scan(&one) != nil || one != 1 {
				t.Fatal("sandbox peer drift aborted caller")
			}
			if tx.Rollback(f.ctx) != nil {
				t.Fatal("sandbox peer drift rollback")
			}
		})
	}
	baseline()
}

func TestCatalogOrdinaryRuntimeOwnerOutsideProtectedSchemaRejected(t *testing.T) {
	f := newOrdinaryVariantFixture(t, ordinaryNamed)
	for _, role := range []string{f.appName, f.projectorName} {
		t.Run(role[len(role)-1:], func(t *testing.T) {
			tx, err := f.parentAdmin.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			if err != nil {
				t.Fatal("ordinary external owner caller RR")
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(f.ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema + "_outside"}.Sanitize()+" AUTHORIZATION "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Fatal("ordinary external ownership fixture")
			}
			if verifyPeerRoleClosureTx(f.ctx, tx, f.pins) != ErrCatalog {
				t.Fatal("ordinary runtime ownership outside protected schema accepted")
			}
			if tx.Conn().PgConn().TxStatus() != 'T' {
				t.Fatal("ordinary external owner check aborted caller")
			}
			if tx.Rollback(f.ctx) != nil {
				t.Fatal("ordinary external ownership rollback")
			}
		})
	}
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("ordinary restored owner caller RR")
	}
	defer tx.Rollback(context.Background())
	if verifyPeerRoleClosureTx(f.ctx, tx, f.pins) != nil {
		t.Fatal("ordinary ownership baseline not restored")
	}
}

type catalogDatabaseRuntime struct {
	pin   RolePin
	pool  *pgxpool.Pool
	label string
}

func checkCatalogDatabasePrivilegeClosure(t *testing.T, ctx context.Context, admin *pgxpool.Pool, pins Pins, runtimes []catalogDatabaseRuntime) {
	t.Helper()
	baseline := func(runtime catalogDatabaseRuntime) {
		t.Helper()
		tx, err := runtime.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			t.Fatal("database closure actual LOGIN caller RR")
		}
		defer tx.Rollback(context.Background())
		var current, session string
		var connect, temporary bool
		if tx.QueryRow(ctx, `SELECT current_user::pg_catalog.text,session_user::pg_catalog.text,pg_catalog.has_database_privilege($1::pg_catalog.oid,$2::pg_catalog.oid,'CONNECT'),pg_catalog.has_database_privilege($1::pg_catalog.oid,$2::pg_catalog.oid,'TEMPORARY')`, runtime.pin.OID, pins.Database.OID).Scan(&current, &session, &connect, &temporary) != nil || current != runtime.pin.Name || session != runtime.pin.Name || !connect || !temporary {
			t.Fatal("database closure actual LOGIN/default PUBLIC connection boundary")
		}
		if verifyPeerRoleClosureTx(ctx, tx, pins) != nil {
			t.Fatal("database closure actual LOGIN baseline/restoration")
		}
		if tx.Commit(ctx) != nil {
			t.Fatal("database closure baseline caller commit")
		}
	}
	for _, runtime := range runtimes {
		baseline(runtime)
	}
	database := pgx.Identifier{pins.Database.Name}.Sanitize()
	for _, runtime := range runtimes {
		for _, grant := range []struct{ name, privilege, suffix string }{{"create", "CREATE", ""}, {"create_grant_option", "CREATE", " WITH GRANT OPTION"}, {"connect_grant_option", "CONNECT", " WITH GRANT OPTION"}, {"temporary_grant_option", "TEMPORARY", " WITH GRANT OPTION"}} {
			t.Run(runtime.label+"/"+grant.name, func(t *testing.T) {
				tx, err := admin.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
				if err != nil {
					t.Fatal("database closure mutation caller RR")
				}
				defer tx.Rollback(context.Background())
				if _, err = tx.Exec(ctx, "GRANT "+grant.privilege+" ON DATABASE "+database+" TO "+pgx.Identifier{runtime.pin.Name}.Sanitize()+grant.suffix); err != nil {
					t.Fatal("database closure rollback-only ACL fixture")
				}
				if verifyPeerRoleClosureTx(ctx, tx, pins) != ErrCatalog {
					t.Fatal("database CREATE/grant-option elevation accepted")
				}
				var one int
				if tx.QueryRow(ctx, `SELECT 1`).Scan(&one) != nil || one != 1 {
					t.Fatal("database closure check aborted caller")
				}
				if tx.Rollback(ctx) != nil {
					t.Fatal("database closure ACL rollback")
				}
				baseline(runtime)
			})
		}
	}
	t.Run("public_create", func(t *testing.T) {
		tx, err := admin.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatal("database PUBLIC CREATE caller RR")
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(ctx, "GRANT CREATE ON DATABASE "+database+" TO PUBLIC"); err != nil {
			t.Fatal("database PUBLIC CREATE rollback-only fixture")
		}
		if verifyPeerRoleClosureTx(ctx, tx, pins) != ErrCatalog {
			t.Fatal("database PUBLIC CREATE elevation accepted")
		}
		if tx.Conn().PgConn().TxStatus() != 'T' || tx.Rollback(ctx) != nil {
			t.Fatal("database PUBLIC CREATE caller rollback")
		}
		for _, runtime := range runtimes {
			baseline(runtime)
		}
	})
}

func TestCatalogOrdinaryDatabasePrivilegeElevationRejected(t *testing.T) {
	f := newOrdinaryVariantFixture(t, ordinaryNamed)
	checkCatalogDatabasePrivilegeClosure(t, f.ctx, f.parentAdmin, f.pins, []catalogDatabaseRuntime{{f.pins.App, f.app, "application"}, {f.pins.Projector, f.projector, "projector"}})
}

func TestCatalogSandboxDatabasePrivilegeElevationRejected(t *testing.T) {
	f := newCatalogPeerFixture(t)
	checkCatalogDatabasePrivilegeClosure(t, f.ctx, f.admin, f.pins, []catalogDatabaseRuntime{{f.pins.App, f.app, "application"}, {*f.pins.Worker, f.worker, "worker"}, {f.pins.Projector, f.projector, "projector"}, {f.pins.Sandbox.MockRuntime, f.mock, "mock"}, {f.pins.Sandbox.ReceiverRuntime, f.receiver, "receiver"}})
}
