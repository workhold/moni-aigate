package fundinglotbasis_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/migrations"
)

// This external suite uses original migrations and real record_send output.
// Controlled orphan rows exercise physical fingerprints, not payment, wallet,
// installation, maintenance, or runtime proof acceptance.
type independentBasisPG struct {
	ctx          context.Context
	admin, owner *pgxpool.Pool
	schema       string
}

func independentBasisPGMust(t testing.TB, stage string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		t.Fatalf("%s failed (SQLSTATE %s)", stage, pg.Code)
	}
	t.Fatalf("%s failed", stage)
}

func newIndependentBasisPG(t *testing.T) *independentBasisPG {
	t.Helper()
	raw := os.Getenv("MONI_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("MONI_TEST_DATABASE_URL required for dedicated basis PostgreSQL acceptance")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" || cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 {
		t.Fatal("basis tests require dedicated loopback moni_test:55439 without TLS or fallbacks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	cfg.MaxConns, cfg.MinConns = 4, 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams = map[string]string{
		"application_name": "moni-independent-basis-test", "timezone": "UTC",
		"search_path": "pg_catalog,pg_temp", "statement_timeout": "10000", "lock_timeout": "3000",
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		cancel()
		independentBasisPGMust(t, "open dedicated database", err)
	}
	var version int
	var actual bool
	err = admin.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer,
 current_database()='moni_test' AND current_user=session_user AND current_user='moni_test'
 AND pg_catalog.host(pg_catalog.inet_server_addr())='127.0.0.1' AND pg_catalog.inet_server_port()=55439
 AND d.datdba=r.oid AND r.rolsuper AND r.rolcanlogin
 FROM pg_catalog.pg_roles AS r JOIN pg_catalog.pg_database AS d ON d.datname=current_database()
 WHERE r.rolname=current_user`).Scan(&version, &actual)
	if err != nil || version/10000 != 16 || !actual {
		admin.Close()
		cancel()
		t.Fatal("basis fixture requires actual PG16 dedicated database owner and local superuser session")
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		admin.Close()
		cancel()
		t.Fatal("isolated schema identity unavailable")
	}
	f := &independentBasisPG{ctx: ctx, admin: admin, schema: "flbasis_" + hex.EncodeToString(nonce[:])}
	var exists bool
	err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)`, f.schema).Scan(&exists)
	if err != nil || exists {
		admin.Close()
		cancel()
		t.Fatal("isolated basis schema collision check failed")
	}
	// The exact absent name is registered before CREATE. Cleanup never searches
	// by prefix and never targets an existing database or preview schema.
	t.Cleanup(func() {
		if f.owner != nil {
			f.owner.Close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		defer cancel()
		defer admin.Close()
		if _, e := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("isolated basis schema cleanup failed")
			return
		}
		var remains bool
		if e := admin.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)`, f.schema).Scan(&remains); e != nil || remains {
			t.Error("isolated basis schema cleanup was not confirmed")
		}
	})
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize())
	independentBasisPGMust(t, "create isolated basis schema", err)
	ownerCfg := cfg.Copy()
	ownerCfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp"
	f.owner, err = pgxpool.NewWithConfig(ctx, ownerCfg)
	independentBasisPGMust(t, "open isolated schema pool", err)
	independentBasisPGMust(t, "install original exact 001 through 022 migrations", migrations.ApplyThrough(ctx, f.owner, "022_payment_runtime_attestation.sql"))
	return f
}

func (f *independentBasisPG) begin(t *testing.T, access pgx.TxAccessMode) pgx.Tx {
	t.Helper()
	tx, err := f.owner.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: access})
	independentBasisPGMust(t, "begin isolated repeatable-read transaction", err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := tx.Rollback(ctx); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			t.Error("basis fixture transaction rollback failed")
		}
	})
	return tx
}

func (f *independentBasisPG) exec(t testing.TB, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	_, err := tx.Exec(f.ctx, sql, args...)
	independentBasisPGMust(t, "prepare isolated physical basis vector", err)
}

type independentBasisQuery struct {
	ctx       context.Context
	tx        pgx.Tx
	schema    string
	pages     map[string]int
	counts    map[string]int
	rows      []*independentBasisRows
	business  int
	lastError error
}

func (q *independentBasisQuery) Query(sql string, args ...any) (fundinglotbasis.Rows, error) {
	if strings.Contains(sql, "/* funding_basis.page.v1 */") || strings.Contains(sql, "/* funding_basis.count.v1 */") {
		q.business++
		for _, marker := range []struct {
			text string
			dest map[string]int
		}{
			{"/* funding_basis.page.v1 */", q.pages}, {"/* funding_basis.count.v1 */", q.counts},
		} {
			if !strings.Contains(sql, marker.text) {
				continue
			}
			prefix := "FROM ONLY " + pgx.Identifier{q.schema}.Sanitize() + ".\""
			if start := strings.Index(sql, prefix); start >= 0 {
				name := sql[start+len(prefix):]
				if end := strings.IndexByte(name, '"'); end >= 0 {
					marker.dest[name[:end]]++
				}
			}
		}
	}
	// Match the real ReadLease protocol and expose SQL parameter errors. The
	// adapter forwards all arguments unchanged; it never repairs reader SQL.
	args = append([]any{pgx.QueryExecModeSimpleProtocol}, args...)
	rows, err := q.tx.Query(q.ctx, sql, args...)
	if err != nil {
		q.lastError = err
		return nil, err
	}
	r := &independentBasisRows{rows: rows}
	q.rows = append(q.rows, r)
	return r, nil
}

type independentBasisRows struct {
	rows                 pgx.Rows
	eof, checked, closed bool
}

func (r *independentBasisRows) Next() (bool, error) {
	more := r.rows.Next()
	if !more {
		r.eof = true
	}
	return more, nil
}
func (r *independentBasisRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *independentBasisRows) Err() error             { r.checked = true; return r.rows.Err() }
func (r *independentBasisRows) Close() error           { r.closed = true; r.rows.Close(); return nil }

func (f *independentBasisPG) query(tx pgx.Tx) *independentBasisQuery {
	return &independentBasisQuery{ctx: f.ctx, tx: tx, schema: f.schema, pages: map[string]int{}, counts: map[string]int{}}
}

func (f *independentBasisPG) read(t *testing.T, tx pgx.Tx) (fundinglotbasis.Report, *independentBasisQuery) {
	t.Helper()
	q := f.query(tx)
	facts, err := fundinglotbasis.Read(f.ctx, q, f.schema)
	if err != nil {
		if q.lastError != nil {
			independentBasisPGMust(t, "execute actual basis SQL", q.lastError)
		}
		independentBasisPGMust(t, "read actual basis", err)
	}
	report := facts.Summary()
	if report.Scope != "fixed_109_table_basis_v1" || !report.PhysicalEOFComplete || report.ScannedTables != 109 || report.ScannedColumns != 976 || len(q.pages) != 109 || len(q.counts) != 109 {
		t.Fatal("real basis did not fully enumerate 109 tables and 976 columns")
	}
	for _, rows := range q.rows {
		if !rows.eof || !rows.checked || !rows.closed {
			t.Fatal("real successful query omitted physical EOF, Err, or Close")
		}
	}
	return report, q
}

func (f *independentBasisPG) reject(t *testing.T, tx pgx.Tx, want error) {
	t.Helper()
	q := f.query(tx)
	facts, err := fundinglotbasis.Read(f.ctx, q, f.schema)
	if err != want || !reflect.DeepEqual(facts.Summary(), fundinglotbasis.Report{}) || q.business != 0 {
		t.Fatal("metadata counterexample published partial facts or reached business record_send")
	}
}

type independentBasisTable struct {
	Table      string              `json:"table"`
	PrimaryKey []string            `json:"primary_key"`
	Columns    [][]json.RawMessage `json:"columns"`
}

func independentBasisMaterial(t testing.TB, name, want string) []byte {
	t.Helper()
	raw, err := os.ReadFile("materials/" + name)
	if err != nil || independentBasisSHA(raw) != want {
		t.Fatal("reviewed independent basis material changed")
	}
	return raw
}

func independentBasisSHA(raw []byte) string {
	d := sha256.Sum256(raw)
	return hex.EncodeToString(d[:])
}

type independentBasisFrame struct{ h hash.Hash }

func (f independentBasisFrame) u(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	_, _ = f.h.Write(b[:])
}
func (f independentBasisFrame) b(raw []byte) { f.u(uint64(len(raw))); _, _ = f.h.Write(raw) }
func (f independentBasisFrame) n(v *string) {
	if v == nil {
		f.u(0)
		return
	}
	f.u(1)
	f.b([]byte(*v))
}

// This reference independently materializes the small test dataset, sorts raw
// keys in Go, and builds the frozen byte frame. It does not use production SQL
// builders, codecs, cursors, materials parsers, or framing helpers.
func (f *independentBasisPG) reference(t *testing.T, tx pgx.Tx) (string, uint64, uint64) {
	t.Helper()
	descriptor := independentBasisMaterial(t, "descriptor-v1.json", "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5")
	manifest := independentBasisMaterial(t, "manifest-v1.json", "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de")
	send := independentBasisMaterial(t, "send-v1.json", "1bd800f0014a159d1528e716275263c7b6c97e6fb91a037d034e1fbbe028684b")
	var tables []independentBasisTable
	if json.Unmarshal(descriptor, &tables) != nil || len(tables) != 109 {
		t.Fatal("independent descriptor shape")
	}
	var dbOID, schemaOID uint32
	var version int64
	var serverEncoding, clientEncoding, databaseEncoding, provider, collate, ctype string
	var icuLocale, icuRules, collationVersion, actualVersion *string
	err := tx.QueryRow(f.ctx, `SELECT d.oid,n.oid,current_setting('server_version_num')::bigint,
 current_setting('server_encoding'),current_setting('client_encoding'),pg_catalog.pg_encoding_to_char(d.encoding),
 d.datlocprovider::text,d.datcollate,d.datctype,d.daticulocale,d.daticurules,d.datcollversion,
 pg_catalog.pg_database_collation_actual_version(d.oid)
 FROM pg_catalog.pg_database d JOIN pg_catalog.pg_namespace n ON n.nspname=$1 WHERE d.datname=current_database()`, f.schema).
		Scan(&dbOID, &schemaOID, &version, &serverEncoding, &clientEncoding, &databaseEncoding, &provider, &collate, &ctype, &icuLocale, &icuRules, &collationVersion, &actualVersion)
	independentBasisPGMust(t, "read independent actual frame header", err)
	frame := independentBasisFrame{sha256.New()}
	_, _ = frame.h.Write([]byte("moni-aigate/funding-lots-basis/v1\x00"))
	for _, v := range []uint64{uint64(dbOID), uint64(schemaOID), 16, uint64(version), 6} {
		frame.u(v)
	}
	for _, v := range []string{serverEncoding, clientEncoding, databaseEncoding, provider, collate, ctype} {
		frame.b([]byte(v))
	}
	for _, v := range []*string{icuLocale, icuRules, collationVersion, actualVersion} {
		frame.n(v)
	}
	for _, v := range [][]byte{send, manifest, descriptor} {
		frame.b(v)
	}
	var sourceRows, sourceBytes uint64
	for _, table := range tables {
		var columns []string
		for _, column := range table.Columns {
			var name string
			if len(column) != 6 || json.Unmarshal(column[1], &name) != nil {
				t.Fatal("independent column shape")
			}
			columns = append(columns, name)
		}
		rowSQL := func(names []string) string {
			parts := make([]string, len(names))
			for i, name := range names {
				parts[i] = pgx.Identifier{name}.Sanitize()
			}
			return "pg_catalog.record_send(ROW(" + strings.Join(parts, ",") + "))"
		}
		rows, e := tx.Query(f.ctx, "SELECT "+rowSQL(table.PrimaryKey)+","+rowSQL(columns)+" FROM ONLY "+pgx.Identifier{f.schema, table.Table}.Sanitize(), pgx.QueryExecModeSimpleProtocol)
		independentBasisPGMust(t, "read independent native records", e)
		type record struct{ key, row []byte }
		var records []record
		for rows.Next() {
			var item record
			independentBasisPGMust(t, "decode independent native record", rows.Scan(&item.key, &item.row))
			records = append(records, record{bytes.Clone(item.key), bytes.Clone(item.row)})
		}
		rows.Close()
		independentBasisPGMust(t, "finish independent native record query", rows.Err())
		sort.Slice(records, func(i, j int) bool { return bytes.Compare(records[i].key, records[j].key) < 0 })
		frame.b([]byte(table.Table))
		frame.u(uint64(len(columns)))
		pkJSON, e := json.Marshal(table.PrimaryKey)
		independentBasisPGMust(t, "encode independent PK frame", e)
		frame.b(pkJSON)
		frame.u(uint64(len(records)))
		for _, item := range records {
			frame.b(item.row)
			sourceRows++
			sourceBytes += uint64(len(item.key) + len(item.row))
		}
	}
	return hex.EncodeToString(frame.h.Sum(nil)), sourceRows, sourceBytes
}

func (f *independentBasisPG) compareReference(t *testing.T, tx pgx.Tx) (fundinglotbasis.Report, *independentBasisQuery) {
	t.Helper()
	report, q := f.read(t, tx)
	want, rows, size := f.reference(t, tx)
	if report.BasisSHA256 != want || report.SourceRows != rows || report.SourceBytes != size || report.DescriptorSHA256 != "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5" || report.ManifestSHA256 != "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de" || report.SendMetadataSHA256 != "1bd800f0014a159d1528e716275263c7b6c97e6fb91a037d034e1fbbe028684b" {
		t.Fatal("actual basis differs from independent full binary frame or counters")
	}
	return report, q
}

func TestIndependentPostgresBasis(t *testing.T) {
	f := newIndependentBasisPG(t)
	t.Run("original_migrations_all_empty_table_eof", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadOnly)
		_, q := f.compareReference(t, tx)
		for table, pages := range q.pages {
			if pages != 1 || q.counts[table] != 1 {
				t.Fatal("original table skipped its sole complete page or count")
			}
		}
	})
	t.Run("native_numeric_json_bytea_array_unicode_and_times", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadWrite)
		f.exec(t, tx, "SET LOCAL session_replication_role='replica'")
		f.exec(t, tx, `INSERT INTO tenants(id,name,allowed_regions,created_at) VALUES
 ('basis-native-tenant','模型 😀 é',ARRAY['global','中国','',NULL]::text[],'0001-01-01 00:00:00+00')`)
		f.exec(t, tx, `INSERT INTO models(id,public_name,profile,profile_version,created_at) VALUES
 ('basis-native-model','模型/测试','{"unicode":"中文😀","decimal":12345678901234567890.123400,"nested":[null,true,"<A&B>"]}'::jsonb,'native','10000-01-01 00:00:00+00')`)
		f.exec(t, tx, `INSERT INTO report_totals(consumer,tenant_id,credited,charged,refunded,released,requests) VALUES
 ('消费者','basis-native-tenant',9999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999,123,0,42,9223372036854775807)`)
		f.exec(t, tx, `INSERT INTO audit_events(id,actor,action,target,sanitized_diff,created_at) VALUES
 ('native-minus-infinity','fixture','physical.basis','fixture','{"v":null}','-infinity'),
 ('native-plus-infinity','fixture','physical.basis','fixture','{"v":""}','infinity'),
 ('native-bc','fixture','physical.basis','fixture','{}','0001-01-01 00:00:00+00 BC')`)
		var binaryValue [256]byte
		for i := range binaryValue {
			binaryValue[i] = byte(i)
		}
		f.exec(t, tx, `INSERT INTO payment_runtime_attestations(singleton,environment_id,database_oid,schema_oid,schema_owner_oid,app_role_oid,worker_role_oid,projector_role_oid,app_nonce_digest,worker_nonce_digest,proof_version,proof_canonical,proof_sha256,created_at)
 VALUES(true,'physical-only-not-runtime-authority',1,2,3,4,5,6,repeat('1',64),repeat('2',64),1,$1,encode(sha256($1::bytea),'hex'),'2001-02-03 04:05:06.123456+00')`, binaryValue[:])
		report, _ := f.compareReference(t, tx)
		if report.SourceRows < 8 || report.SourceBytes < 256 {
			t.Fatal("native fixture records missing from actual basis")
		}
	})
	for _, count := range []int{128, 129} {
		t.Run(map[int]string{128: "exact_128_requires_empty_page", 129: "129_rows_cross_composite_pages"}[count], func(t *testing.T) {
			tx := f.begin(t, pgx.ReadWrite)
			f.exec(t, tx, "SET LOCAL session_replication_role='replica'")
			f.exec(t, tx, `INSERT INTO payment_provider_revisions(environment_id,merchant_id,order_id,provider_order_revision,kind,fact_digest,evidence_id)
 SELECT 'environment','merchant','order',n,'closed',repeat('a',64),'evidence:'||n FROM generate_series(1,$1::integer) AS n`, count)
			f.exec(t, tx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight)
 SELECT CASE WHEN n=0 THEN '' ELSE 'v:'||(n/32)::text END,'channel',CASE WHEN n=0 THEN '' ELSE 'c:'||(n%32)::text END,(n%32)+1,'fixture',1,1 FROM generate_series(0,$1::integer-1) AS n`, count)
			f.exec(t, tx, `INSERT INTO periodic_budget_configuration_versions(control_id,revision,operation_id,period_kind,enabled,limit_amount,actor)
 SELECT 'control',n,'operation:'||n,'utc_day',false,0,'actor' FROM generate_series(1,$1::integer) AS n`, count)
			_, q := f.compareReference(t, tx)
			for _, table := range []string{"payment_provider_revisions", "channel_version_credentials", "periodic_budget_configuration_versions"} {
				if q.pages[table] != 2 {
					t.Fatal("binary composite cursor missed a full-page boundary or empty final page")
				}
			}
		})
	}
	t.Run("same_rr_stable_new_rr_one_byte_changes_hash", func(t *testing.T) {
		_, err := f.owner.Exec(f.ctx, `INSERT INTO tenants(id,name,created_at) VALUES('basis-snapshot-tenant','A','2001-01-01 00:00:00+00')`)
		independentBasisPGMust(t, "commit isolated snapshot seed", err)
		tx := f.begin(t, pgx.ReadOnly)
		before, _ := f.compareReference(t, tx)
		_, err = f.owner.Exec(f.ctx, `UPDATE tenants SET name='B' WHERE id='basis-snapshot-tenant'`)
		independentBasisPGMust(t, "commit independent one-byte source change", err)
		after, _ := f.compareReference(t, tx)
		if before != after {
			t.Fatal("same actual RR observed a concurrent committed source change")
		}
		newTx := f.begin(t, pgx.ReadOnly)
		newer, _ := f.compareReference(t, newTx)
		if newer.BasisSHA256 == before.BasisSHA256 || newer.SourceRows != before.SourceRows || newer.SourceBytes != before.SourceBytes {
			t.Fatal("new actual RR failed to bind the one-byte source change")
		}
	})
	t.Run("client_encoding_rejected_before_business_send", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadWrite)
		f.exec(t, tx, "SET LOCAL client_encoding='LATIN1'")
		// pgx SimpleProtocol rejects parameter interpolation before sending the
		// environment SQL when the connection's encoding is not UTF8. This is
		// an execution failure with no partial facts, not evidence that the
		// leaf received a non-UTF8 environment row. Core fixtures separately
		// exercise that metadata rejection.
		q := f.query(tx)
		facts, err := fundinglotbasis.Read(f.ctx, q, f.schema)
		if err != fundinglotbasis.ErrExecution || !reflect.DeepEqual(facts.Summary(), fundinglotbasis.Report{}) || q.business != 0 || len(q.rows) != 0 || q.lastError == nil || q.lastError.Error() != "simple protocol queries must be run with client_encoding=UTF8" {
			t.Fatal("non-UTF8 simple protocol was not rejected before environment SQL with zero facts")
		}
	})
	for _, tc := range []struct{ name, sql string }{
		{"extra_column", `ALTER TABLE tenants ADD COLUMN unexpected_basis_column text`},
		{"dropped_original_column", `ALTER TABLE tenants DROP COLUMN pricing_operation_id CASCADE`},
		{"changed_original_type", `ALTER TABLE audit_events ALTER COLUMN actor TYPE varchar(255)`},
		{"changed_original_nullability", `ALTER TABLE tenants ALTER COLUMN name DROP NOT NULL`},
		{"row_security", `ALTER TABLE tenants ENABLE ROW LEVEL SECURITY`},
		{"inheritance", `CREATE TABLE basis_inherited_child() INHERITS(tenants)`},
		{"birth_wrong_type", `ALTER TABLE tenants ADD COLUMN fl_created_xid xid`},
		{"birth_not_nullable", `ALTER TABLE tenants ADD COLUMN fl_created_xid xid8 NOT NULL DEFAULT pg_current_xact_id()`},
		{"birth_plus_extra", `ALTER TABLE tenants ADD COLUMN fl_created_xid xid8,ADD COLUMN unexpected_tenth text`},
	} {
		t.Run("metadata_drift/"+tc.name, func(t *testing.T) {
			tx := f.begin(t, pgx.ReadWrite)
			f.exec(t, tx, tc.sql)
			f.reject(t, tx, fundinglotbasis.ErrMetadata)
		})
	}
	t.Run("primary_key_order_drift", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadWrite)
		f.exec(t, tx, `ALTER TABLE channel_version_credentials DROP CONSTRAINT channel_version_credentials_pkey CASCADE`)
		f.exec(t, tx, `ALTER TABLE channel_version_credentials ADD PRIMARY KEY(credential_id,channel_version_id)`)
		f.reject(t, tx, fundinglotbasis.ErrMetadata)
	})
	t.Run("exact_nullable_birth_delta_excluded_from_frame", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadWrite)
		before, _ := f.compareReference(t, tx)
		f.exec(t, tx, `ALTER TABLE tenants ADD COLUMN fl_created_xid xid8`)
		nullBirth, _ := f.compareReference(t, tx)
		f.exec(t, tx, `UPDATE tenants SET fl_created_xid=pg_current_xact_id()`)
		populatedBirth, _ := f.compareReference(t, tx)
		if before != nullBirth || before != populatedBirth {
			t.Fatal("exact ninth birth column leaked into fixed old projection")
		}
	})
	t.Run("transaction_local_send_metadata_drift_rolled_back", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadWrite)
		var before string
		independentBasisPGMust(t, "read original fixed send cost", tx.QueryRow(f.ctx, `SELECT procost::text FROM pg_catalog.pg_proc WHERE oid=2437`).Scan(&before))
		if before != "1" {
			t.Fatal("fixed send profile was not the original baseline")
		}
		// This one catalog field is visible only inside this uncommitted test
		// transaction. Never COMMIT; rollback is explicit and verified below.
		command, err := tx.Exec(f.ctx, `UPDATE pg_catalog.pg_proc SET procost=2 WHERE oid=2437`)
		independentBasisPGMust(t, "change one uncommitted send metadata field", err)
		if command.RowsAffected() != 1 {
			t.Fatal("single fixed send function was not selected")
		}
		f.reject(t, tx, fundinglotbasis.ErrMetadata)
		independentBasisPGMust(t, "rollback send metadata counterexample", tx.Rollback(f.ctx))
		var restored string
		independentBasisPGMust(t, "verify original send metadata restored", f.admin.QueryRow(f.ctx, `SELECT procost::text FROM pg_catalog.pg_proc WHERE oid=2437`).Scan(&restored))
		if restored != before {
			t.Fatal("uncommitted send metadata vector was not fully rolled back")
		}
	})
}
