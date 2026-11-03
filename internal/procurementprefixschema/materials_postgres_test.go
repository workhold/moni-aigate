package procurementprefixschema_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/internal/procurementprefixschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

func must(t *testing.T, step string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
}

type column struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Nullable   bool    `json:"nullable"`
	Expression *string `json:"generated_expression"`
}

type table struct {
	Name    string   `json:"name"`
	Columns []column `json:"columns"`
}

func layout(t *testing.T, filename string) []table {
	t.Helper()
	raw, err := os.ReadFile("../../specs/023-periodic-products/" + filename)
	must(t, "read independent layout", err)
	var manifest struct {
		Tables []table `json:"tables"`
	}
	must(t, "decode independent layout", json.Unmarshal(raw, &manifest))
	return manifest.Tables
}

func catalogColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) map[string][]column {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT c.relname,a.attname,pg_catalog.format_type(a.atttypid,a.atttypmod),
 NOT a.attnotnull,pg_catalog.pg_get_expr(d.adbin,d.adrelid)
 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid
 JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE n.nspname=$1 AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped
 ORDER BY c.relname,a.attnum`, schema)
	must(t, "read exact column catalog", err)
	defer rows.Close()
	result := make(map[string][]column)
	for rows.Next() {
		var name string
		var col column
		must(t, "scan exact column", rows.Scan(&name, &col.Name, &col.Type, &col.Nullable, &col.Expression))
		result[name] = append(result[name], col)
	}
	must(t, "finish exact column catalog", rows.Err())
	return result
}

func routinesAndACL(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) string {
	t.Helper()
	var result string
	must(t, "snapshot routines, user triggers and explicit ACL", pool.QueryRow(ctx, `SELECT pg_catalog.jsonb_build_object(
 'functions',(SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(p.oid,p.prosrc,p.proacl) ORDER BY p.oid)
  FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1),
 'triggers',(SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(t.oid,t.tgname,t.tgfoid) ORDER BY t.oid)
  FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND NOT t.tgisinternal),
 'acl',(SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(c.oid,c.relacl) ORDER BY c.oid)
  FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relacl IS NOT NULL),
 'column_acl',(SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(a.attrelid,a.attnum,a.attacl) ORDER BY a.attrelid,a.attnum)
  FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND a.attacl IS NOT NULL)
 )::pg_catalog.text`, schema).Scan(&result))
	return result
}

func verifyHistory(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	must(t, "begin original22 verification", err)
	defer tx.Rollback(ctx)
	must(t, "authenticate original22 history", migrations.VerifyOriginal22SnapshotTx(ctx, tx, schema))
	must(t, "commit original22 verification", tx.Commit(ctx))
}

// This acceptance installs declaration materials as the fixture owner. It is
// deliberately not a business writer, root seal, guard or actual APP test.
func TestPostgresProcurementRelationMaterials(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated relation PostgreSQL acceptance")
	}
	config, err := pgxpool.ParseConfig(raw)
	must(t, "parse dedicated database", err)
	cc := config.ConnConfig
	if cc.Host != "127.0.0.1" || cc.Port != 55439 || cc.Database != "moni_test" || cc.User != "moni_test" || len(cc.Fallbacks) != 0 {
		t.Fatal("requires isolated 127.0.0.1:55439/moni_test and moni_test LOGIN without fallbacks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	must(t, "connect dedicated owner", err)
	schema := "pp_relation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		defer admin.Close()
		if pool != nil {
			pool.Close()
		}
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, err := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		if err != nil {
			t.Errorf("exact schema cleanup: %v", err)
			return
		}
		var remains bool
		if err := admin.QueryRow(cleanup, "SELECT EXISTS(SELECT FROM pg_catalog.pg_namespace WHERE nspname=$1)", schema).Scan(&remains); err != nil || remains {
			t.Error("fixture schema remains after cleanup")
		}
	})
	var version int
	must(t, "read PG version", admin.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version))
	if version < 160000 || version >= 170000 {
		t.Fatal("requires PostgreSQL16")
	}
	mustExec := func(sql string) {
		t.Helper()
		_, err := admin.Exec(ctx, sql)
		must(t, "exact schema DDL", err)
	}
	mustExec("CREATE SCHEMA " + quoted)
	// Cleanup owns admin. A deferred Close would run before t.Cleanup.
	adminConfig := config.Copy()
	adminConfig.MaxConns = 2
	adminConfig.ConnConfig.RuntimeParams["search_path"] = schema
	adminConfig.ConnConfig.RuntimeParams["statement_timeout"] = "8s"
	adminConfig.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	pool, err = pgxpool.NewWithConfig(ctx, adminConfig)
	must(t, "connect isolated schema", err)
	must(t, "apply exact original22", migrations.ApplyThrough(ctx, pool, "022_payment_runtime_attestation.sql"))
	verifyHistory(t, ctx, pool, schema)
	bp1, err := periodicbudgetprefixschema.SQL()
	must(t, "authenticate B-P1 material", err)
	_, err = pool.Exec(ctx, bp1)
	must(t, "apply isolated B-P1 candidate", err)
	_, err = pool.Exec(ctx, `INSERT INTO audit_events(id,actor,action,target)
 VALUES ('10000000-0000-0000-0000-000000000009','fixture','fixture.reference','local')`)
	must(t, "seed representative legacy original", err)
	legacySnapshot := func() string {
		t.Helper()
		var row string
		must(t, "read representative legacy original", pool.QueryRow(ctx, `SELECT pg_catalog.jsonb_strip_nulls(pg_catalog.to_jsonb(a))::pg_catalog.text
 FROM audit_events a WHERE id='10000000-0000-0000-0000-000000000009'`).Scan(&row))
		return row
	}
	legacyBefore := legacySnapshot()
	beforeColumns := catalogColumns(t, ctx, pool, schema)
	beforeRoutines := routinesAndACL(t, ctx, pool, schema)
	sql, err := procurementprefixschema.SQL(schema)
	must(t, "authenticate relation materials", err)
	_, err = pool.Exec(ctx, sql)
	must(t, "install full phased relation materials", err)
	if legacyBefore != legacySnapshot() {
		t.Fatal("representative legacy row changed or new fields acquired non-NULL values")
	}
	if after := routinesAndACL(t, ctx, pool, schema); beforeRoutines != after {
		t.Fatal("relation materials changed routines, user triggers or explicit grants")
	}
	verifyHistory(t, ctx, pool, schema)
	afterColumns := catalogColumns(t, ctx, pool, schema)
	newTables := layout(t, "b-procurement-column-layout-v1.json")
	newColumns, generated := 0, 0
	for _, expected := range newTables {
		got := afterColumns[expected.Name]
		if len(got) != len(expected.Columns) {
			t.Fatalf("%s column count %d != %d", expected.Name, len(got), len(expected.Columns))
		}
		for i, col := range expected.Columns {
			want := col
			want.Type = strings.ToLower(want.Type)
			if want.Type == "timestamptz" {
				want.Type = "timestamp with time zone"
			}
			if got[i].Name != want.Name || got[i].Type != want.Type || got[i].Nullable != want.Nullable || !sameExpression(got[i].Expression, want.Expression) {
				t.Fatalf("%s column %d differs: got %+v want %+v", expected.Name, i, got[i], want)
			}
			newColumns++
			if col.Expression != nil {
				generated++
			}
		}
	}
	if len(newTables) != 32 || newColumns != 715 || generated != 68 || len(afterColumns) != len(beforeColumns)+32 {
		t.Fatal("independent new table/column/generation inventory differs")
	}
	additions := layout(t, "b-procurement-original-column-layout-v1.json")
	expectedAdditions := make(map[string][]column)
	added := 0
	for _, table := range additions {
		for _, c := range table.Columns {
			if c.Type == "int8" {
				c.Type = "bigint"
			}
			if c.Type == "int2" {
				c.Type = "smallint"
			}
			if c.Type == "timestamptz" {
				c.Type = "timestamp with time zone"
			}
			expectedAdditions[table.Name] = append(expectedAdditions[table.Name], c)
			added++
		}
	}
	if len(additions) != 18 || added != 110 {
		t.Fatal("independent original extension inventory differs")
	}
	for name, original := range beforeColumns {
		want := append(append([]column(nil), original...), expectedAdditions[name]...)
		if !reflect.DeepEqual(want, afterColumns[name]) {
			t.Fatalf("%s original columns/defaults changed or extensions differ", name)
		}
	}
	var bad, fk, keys, checks int
	must(t, "verify all procurement constraints", pool.QueryRow(ctx, `SELECT
 count(*) FILTER (WHERE NOT convalidated OR (contype='f' AND (NOT condeferrable OR NOT condeferred)) OR (contype IN ('p','u') AND condeferrable)),
	 count(*) FILTER (WHERE contype='f'),count(*) FILTER (WHERE contype IN ('p','u')),count(*) FILTER (WHERE contype='c')
	 FROM pg_catalog.pg_constraint WHERE connamespace=$1::pg_catalog.regnamespace AND conname LIKE 'pp\_%' ESCAPE '\'`, schema).Scan(&bad, &fk, &keys, &checks))
	if bad != 0 || fk != 291 || keys != 268 || checks != 126 {
		t.Fatalf("constraint validation/defer/inventory differs: bad=%d FK=%d keys=%d checks=%d", bad, fk, keys, checks)
	}
	var nonStored, stored int
	must(t, "verify generated always stored", pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE a.attgenerated<>'s'),count(*) FILTER (WHERE a.attgenerated='s') FROM pg_catalog.pg_attribute a
 JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=$1::pg_catalog.regnamespace
 AND c.relname LIKE 'pp\_%' ESCAPE '\' AND a.attgenerated<>''`, schema).Scan(&nonStored, &stored))
	if nonStored != 0 || stored != 68 {
		t.Fatal("generated column is not stored")
	}
	t.Logf("installed relation-only materials: 32 tables, 715 columns (68 generated), 18 original extensions/110 columns; %d keys/%d checks/%d deferred FKs", keys, checks, fk)
	t.Run("declared_constraints", func(t *testing.T) { declaredConstraints(t, ctx, pool) })
}

func sameExpression(got, want *string) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	normalize := func(s string) string {
		var result strings.Builder
		for len(s) != 0 {
			if s[0] == '\'' {
				// SQL single-quoted literals are case sensitive. Keep their exact
				// bytes, including doubled apostrophes, casts and whitespace.
				end := 1
				for end < len(s) {
					if s[end] == '\'' {
						end++
						if end >= len(s) || s[end] != '\'' {
							break
						}
					}
					end++
				}
				result.WriteString(s[:end])
				s = s[end:]
				continue
			}
			end := strings.IndexByte(s, '\'')
			if end < 0 {
				end = len(s)
			}
			part := strings.ToLower(s[:end])
			part = strings.ReplaceAll(part, "pg_catalog.", "")
			part = strings.ReplaceAll(part, "::text", "")
			part = strings.ReplaceAll(part, "::bigint", "")
			result.WriteString(strings.Map(func(r rune) rune {
				if strings.ContainsRune(" \t\n\r()", r) {
					return -1
				}
				return r
			}, part))
			s = s[end:]
		}
		return result.String()
	}
	return normalize(*got) == normalize(*want)
}

func TestGeneratedExpressionOraclePreservesLiterals(t *testing.T) {
	ptr := func(s string) *string { return &s }
	want := ptr("CASE WHEN mode = 'finalization' THEN cause_source_id ELSE NULL END")
	got := ptr("CASE WHEN (mode = 'finalization'::text) THEN cause_source_id ELSE NULL::text END")
	if !sameExpression(got, want) {
		t.Fatal("PG presentation casts/parentheses changed equivalent expression")
	}
	for _, expression := range []string{
		"CASE WHEN mode = 'FINALIZATION' THEN cause_source_id ELSE NULL END",
		"CASE WHEN mode = 'finalization ' THEN cause_source_id ELSE NULL END",
		"CASE WHEN mode = 'finalization::text' THEN cause_source_id ELSE NULL END",
		"CASE WHEN mode = 'finali(zation)' THEN cause_source_id ELSE NULL END",
	} {
		if sameExpression(ptr(expression), want) {
			t.Fatal("oracle normalized a different SQL literal")
		}
	}
	if sameExpression(ptr("'a''B'"), ptr("'a''b'")) || sameExpression(nil, ptr("NULL")) {
		t.Fatal("oracle erased escaped literal or absent expression distinction")
	}
}

// Each case checks a named declaration and always rolls back all owner rows.
// Other deferred edges are intentionally pending: these are not sealed roots.
func declaredConstraints(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	const root = "10000000-0000-0000-0000-000000000001"
	const request = "10000000-0000-0000-0000-000000000002"
	const tenant = "10000000-0000-0000-0000-000000000003"
	rootSQL := `INSERT INTO pp_operation_roots(id,entry_kind,scope_kind,tenant_id,request_id,created_xid,session_role_oid,step_count,semantic_result)
 VALUES ('` + root + `','request_birth','request','` + tenant + `','` + request + `','7',1,1,'born')`
	stepSQL := `INSERT INTO pp_root_steps(root_id,step_no,kind,source_id,created_xid) VALUES ('` + root + `',1,'request_birth','` + request + `','7')`
	originSQL := `INSERT INTO pp_request_origins(request_id,root_id,step_no,created_xid,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,tenant_pricing_revision,retail_priced_at,request_created_at,pb_created_xid,pb_admitted_at,initial_status,initial_billing_status)
 VALUES ('` + request + `','` + root + `',1,'7','` + tenant + `','key','owner','model','price',1,1,10,10,'profile','policy','hmac',1,'2026-11-03T01:00:00Z','instance',false,1,'2026-11-03T00:00:00Z','2026-11-03T00:00:00Z','7','2026-11-03T00:00:00Z','accepted','reserved')`
	auditSQL := `INSERT INTO audit_events(actor,action,target,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid)
 VALUES ('fixture','procurement.command.applied','local','` + root + `',1,'account_command','` + request + `','account_command','7')`
	commandStepSQL := strings.Replace(stepSQL, "'request_birth'", "'account_command'", 1)
	cases := []struct {
		name, constraint, code string
		setup                  []string
	}{
		{"matching root xid", "pp_s_step_root_fk", "", []string{rootSQL, stepSQL}},
		{"wrong root xid", "pp_s_step_root_fk", "23503", []string{rootSQL, strings.Replace(stepSQL, "'7'", "'8'", 1)}},
		{"missing typed source", "pp_s_step_request_birth_fk", "23503", []string{rootSQL, stepSQL}},
		{"matching typed source", "pp_s_step_request_birth_fk", "", []string{rootSQL, stepSQL, originSQL}},
		{"matching tenant identity", "pp_s_request_birth_root_r_fk", "", []string{rootSQL, originSQL}},
		{"wrong tenant identity", "pp_s_request_birth_root_r_fk", "23503", []string{rootSQL, strings.Replace(originSQL, tenant, "10000000-0000-0000-0000-000000000004", 1)}},
		{"duplicate canonical step", "pp_s_step_pk", "23505", []string{rootSQL, stepSQL, stepSQL}},
		{"zero head with decision", "pp_s_request_head_shape_ck", "23514", []string{`INSERT INTO pp_request_heads VALUES ('` + request + `',0,'decision','7')`}},
		{"matching original source group", "pp_o_audit_step_fk", "", []string{commandStepSQL, auditSQL}},
		{"original source wrong xid", "pp_o_audit_step_fk", "23503", []string{commandStepSQL, strings.Replace(auditSQL, "'7'", "'8'", 1)}},
		{"partial original source group", "pp_o_audit_group_ck", "23514", []string{strings.Replace(auditSQL, "1,'account_command'", "1,NULL", 1)}},
		{"original source wrong role", "pp_o_audit_group_ck", "23514", []string{strings.Replace(auditSQL, "'account_command','7'", "'correction','7'", 1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			must(t, "begin declaration case", err)
			defer tx.Rollback(ctx)
			for _, sql := range tc.setup {
				_, err = tx.Exec(ctx, sql)
				if err != nil {
					break
				}
			}
			if err == nil && tc.constraint != "" {
				_, err = tx.Exec(ctx, "SET CONSTRAINTS "+pgx.Identifier{tc.constraint}.Sanitize()+" IMMEDIATE")
			}
			if tc.code == "" {
				must(t, "matching declaration", err)
				return
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
				t.Fatalf("want SQLSTATE %s; got %v", tc.code, err)
			}
			if tc.constraint != "" && pgErr.ConstraintName != tc.constraint {
				t.Fatalf("wrong rejecting declaration %s", pgErr.ConstraintName)
			}
		})
	}
}
