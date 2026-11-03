package procurementrequestbirth_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurementrequestbirth"
)

// This inventory is frozen independently of catalog grants. Explicit NULLs
// require INSERT, while generated columns participate only in SELECT *.
const rbGrantOriginInsert = "request_id,root_id,step_no,created_xid,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,request_created_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,initial_latest_id,initial_status,initial_billing_status"
const rbGrantHeadColumns = "request_id,ordinal,last_decision_id,updated_xid"
const rbGrantStepInsert = "root_id,step_no,kind,source_id,created_xid"
const rbGrantRootColumns = "id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result"
const rbGrantRequestInsert = "id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at"
const rbGrantRequestColumns = "id,tenant_id,key_id,owner_id,model_id,status,billing_status,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,latest_finalization_id,created_at,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_last_decision_id"
const rbGrantStepColumns = rbGrantStepInsert + ",d_request_birth,d_attempt_birth,d_account_command,d_attempt_delivery,d_incoming,d_meter_choice,d_request_decision,d_normal_cost_decision,d_cost_mutation,d_cost_reset_decision,d_evidence_choice,d_procurement_transition,d_proposal_birth,d_approval_consumed,d_unmanaged_correction,d_recovery,d_cost_outbox,d_reconciliation"

var rbGrantInserts = []struct{ table, columns string }{
	{"pp_request_origins", rbGrantOriginInsert},
	{"pp_request_heads", rbGrantHeadColumns},
	{"pp_root_steps", rbGrantStepInsert},
	{"pp_operation_roots", rbGrantRootColumns},
}
var rbGrantReads = []struct{ table, columns string }{
	{"request_records", rbGrantRequestColumns},
	{"pp_request_origins", rbGrantOriginInsert + ",step_kind"},
	{"pp_request_heads", rbGrantHeadColumns},
	{"pp_operation_roots", rbGrantRootColumns},
	{"pp_root_steps", rbGrantStepColumns},
}

type rbGrantItem struct{ role, table, column, privilege, function string }

func (p rbGrantItem) name() string {
	if p.function != "" {
		return p.role + "/EXECUTE/" + p.function
	}
	return p.role + "/" + p.privilege + "/" + p.table + "/" + p.column
}
func rbGrantMatrix() []rbGrantItem {
	var out []rbGrantItem
	add := func(role, table, columns, privilege string) {
		for _, c := range strings.Split(columns, ",") {
			out = append(out, rbGrantItem{role: role, table: table, column: c, privilege: privilege})
		}
	}
	for _, g := range rbGrantInserts {
		add("W", g.table, g.columns, "INSERT")
	}
	add("W", "request_records", "pp_origin_root_id,pp_origin_root_step,pp_origin_xid", "UPDATE")
	for _, g := range []struct{ table, column string }{{"pp_request_origins", "request_id"}, {"pp_request_heads", "request_id"}, {"pp_root_steps", "root_id"}} {
		add("W", g.table, g.column, "UPDATE")
	}
	add("APP", "request_records", rbGrantRequestInsert, "INSERT")
	for _, r := range []string{"W", "APP"} {
		for _, g := range rbGrantReads {
			add(r, g.table, g.columns, "SELECT")
		}
	}
	add("APP", "request_records", "xmin", "SELECT")
	for _, fn := range []string{"pp_rb_validate_request_v1(text)", "pp_rb_validate_root_v1(text)"} {
		out = append(out, rbGrantItem{role: "APP", privilege: "EXECUTE", function: fn})
	}
	return out
}

func rbGrantSortedColumns(csv string) []string {
	v := strings.Split(csv, ",")
	sort.Strings(v)
	return v
}

func TestA1GrantFaultFrozenMatrix(t *testing.T) {
	matrix := rbGrantMatrix()
	seen := map[rbGrantItem]bool{}
	counts := map[string]int{}
	for _, p := range matrix {
		if seen[p] {
			t.Fatal("duplicate frozen grant fault: " + p.name())
		}
		seen[p] = true
		counts[p.role+"/"+p.privilege]++
	}
	want := map[string]int{"W/INSERT": 52, "W/UPDATE": 6, "APP/INSERT": 21, "W/SELECT": 104, "APP/SELECT": 105, "APP/EXECUTE": 2}
	if len(matrix) != 290 || !reflect.DeepEqual(counts, want) {
		t.Fatalf("frozen matrix drift: %d %v", len(matrix), counts)
	}
	b := procurementrequestbirth.Bindings{Schema: "rb_grant_fixed", App: "rb_grant_app", Writer: "rb_grant_writer", AppOID: 19001, WriterOID: 19002}
	sql, err := procurementrequestbirth.SQL(b)
	must(t, "authenticate frozen SQL", err)
	actualInsert := map[string]string{}
	for _, m := range regexp.MustCompile(`INSERT INTO "rb_grant_fixed"\.([a-z_]+)\(([^)]*)\)`).FindAllStringSubmatch(sql, -1) {
		if _, ok := actualInsert[m[1]]; ok {
			t.Fatal("duplicate writer insert requires review")
		}
		actualInsert[m[1]] = m[2]
	}
	fixedInsert := map[string]string{}
	for _, g := range rbGrantInserts {
		fixedInsert[g.table] = g.columns
	}
	if !reflect.DeepEqual(actualInsert, fixedInsert) {
		t.Fatalf("writer INSERT coverage drift: %v", actualInsert)
	}
	update := regexp.MustCompile(`UPDATE "rb_grant_fixed"\.request_records SET ([^\n]+?) WHERE `).FindAllStringSubmatch(sql, -1)
	if len(update) != 1 {
		t.Fatal("pointer SET count changed")
	}
	var set []string
	for _, a := range strings.Split(update[0][1], ",") {
		set = append(set, strings.SplitN(a, "=", 2)[0])
	}
	if strings.Join(set, ",") != "pp_origin_root_id,pp_origin_root_step,pp_origin_xid" {
		t.Fatal("pointer SET changed")
	}
	readTables := map[string]bool{}
	for _, m := range regexp.MustCompile(`SELECT \* INTO [a-z]+ FROM "rb_grant_fixed"\.([a-z_]+)`).FindAllStringSubmatch(sql, -1) {
		readTables[m[1]] = true
	}
	if len(readTables) != 5 {
		t.Fatal("actual SELECT * table set changed")
	}
	acl, err := procurementrequestbirth.Permissions(b)
	must(t, "authenticate precise grants", err)
	for _, g := range rbGrantReads {
		if !readTables[g.table] {
			t.Fatal("fixed SELECT * relation is not read: " + g.table)
		}
		pattern := `GRANT SELECT\(([^)]*)\) ON TABLE "rb_grant_fixed"\.` + g.table + ` TO "rb_grant_app", "rb_grant_writer";`
		m := regexp.MustCompile(pattern).FindStringSubmatch(acl)
		if len(m) != 2 || !reflect.DeepEqual(rbGrantSortedColumns(m[1]), rbGrantSortedColumns(g.columns)) {
			t.Fatal("ordinary read grant drift: " + g.table)
		}
	}
	original, err := os.ReadFile("../billing/reserve.go")
	must(t, "read original Reserve for fixed 21-column coverage", err)
	originalInsert := regexp.MustCompile(`INSERT INTO request_records\(([^)]*)\)`).FindStringSubmatch(string(original))
	if len(originalInsert) != 2 || originalInsert[1] != rbGrantRequestInsert {
		t.Fatal("original APP INSERT drift")
	}
	// The original 020 IF short-circuits its allocation subquery when the
	// pointer UPDATE leaves (id,tenant_id,key_id) unchanged. Real PG established
	// these as unused grant controls; never add an artificial read to force loss.
	if !strings.Contains(acl, `GRANT SELECT(subject_type,subject_id) ON TABLE "rb_grant_fixed".periodic_budget_allocations TO "rb_grant_writer";`) {
		t.Fatal("unused original allocation grant surface drift")
	}
	if strings.Contains(acl, "semantic_result),UPDATE(id)") {
		t.Fatal("unused root UPDATE returned")
	}
	t.Log("290 isolated effective grants + 1 explicit PUBLIC-isolated W EXECUTE = 291 faults; 2 unused allocation reads, PUBLIC overlap and private-trigger controls are not faults")
}

func rbGrantRole(f *fixture, p rbGrantItem) string {
	switch p.role {
	case "APP":
		return f.b.App
	case "W":
		return f.b.Writer
	default:
		panic("invalid frozen grant role")
	}
}
func rbGrantFunction(f *fixture, signature string) string {
	at := strings.IndexByte(signature, '(')
	return pgx.Identifier{f.b.Schema, signature[:at]}.Sanitize() + signature[at:]
}
func rbGrantClause(f *fixture, p rbGrantItem) string {
	if p.function != "" {
		return "EXECUTE ON FUNCTION " + rbGrantFunction(f, p.function)
	}
	return p.privilege + "(" + pgx.Identifier{p.column}.Sanitize() + ") ON TABLE " + pgx.Identifier{f.b.Schema, p.table}.Sanitize()
}

// Canonical ACLs include every grantee/grantor/option on the table and every
// column, including xmin's negative attnum. They are not target-role filters.
func rbGrantTableACL(ctx context.Context, f *fixture, table string) (string, error) {
	var value string
	err := f.owner.QueryRow(ctx, `WITH object AS (
 SELECT c.oid,c.relowner,c.relacl FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2
 ), entries AS (
 SELECT 0::smallint attnum,x.grantor,x.grantee,x.privilege_type,x.is_grantable
 FROM object o CROSS JOIN LATERAL pg_catalog.aclexplode(coalesce(o.relacl,pg_catalog.acldefault('r',o.relowner))) x
 UNION ALL
 SELECT a.attnum,x.grantor,x.grantee,x.privilege_type,x.is_grantable
 FROM object o JOIN pg_catalog.pg_attribute a ON a.attrelid=o.oid AND a.attnum<>0 AND NOT a.attisdropped
 CROSS JOIN LATERAL pg_catalog.aclexplode(a.attacl) x
 ) SELECT jsonb_build_object('owner',(SELECT relowner::text FROM object),'entries',coalesce((SELECT jsonb_agg(jsonb_build_array(attnum,grantor::text,grantee::text,privilege_type,is_grantable) ORDER BY attnum,grantor,grantee,privilege_type,is_grantable) FROM entries),'[]'::jsonb))::text`, f.b.Schema, table).Scan(&value)
	return value, err
}
func rbGrantFunctionACL(ctx context.Context, f *fixture, fn string) (string, error) {
	var value string
	err := f.owner.QueryRow(ctx, `SELECT jsonb_build_object('owner',p.proowner::text,'entries',coalesce((SELECT jsonb_agg(jsonb_build_array(x.grantor::text,x.grantee::text,x.privilege_type,x.is_grantable) ORDER BY x.grantor,x.grantee,x.privilege_type,x.is_grantable)
 FROM pg_catalog.aclexplode(coalesce(p.proacl,pg_catalog.acldefault('f',p.proowner))) x),'[]'::jsonb))::text FROM pg_catalog.pg_proc p WHERE p.oid=pg_catalog.to_regprocedure($1)`, rbGrantFunction(f, fn)).Scan(&value)
	return value, err
}
func rbGrantACL(ctx context.Context, f *fixture, p rbGrantItem) (string, error) {
	if p.function != "" {
		return rbGrantFunctionACL(ctx, f, p.function)
	}
	return rbGrantTableACL(ctx, f, p.table)
}
func rbGrantEffective(ctx context.Context, f *fixture, p rbGrantItem, want bool) error {
	var actual bool
	var err error
	if p.function != "" {
		err = f.owner.QueryRow(ctx, "SELECT pg_catalog.has_function_privilege($1,$2,'EXECUTE')", rbGrantRole(f, p), rbGrantFunction(f, p.function)).Scan(&actual)
	} else {
		err = f.owner.QueryRow(ctx, "SELECT pg_catalog.has_column_privilege($1,$2,$3,$4)", rbGrantRole(f, p), pgx.Identifier{f.b.Schema, p.table}.Sanitize(), p.column, p.privilege).Scan(&actual)
	}
	if err != nil {
		return err
	}
	if actual != want {
		return fmt.Errorf("%s effective permission want %t got %t", p.name(), want, actual)
	}
	return nil
}
func rbGrantTarget(t *testing.T, f *fixture, p rbGrantItem) {
	t.Helper()
	role := rbGrantRole(f, p)
	var owned bool
	if p.function != "" {
		must(t, "observe non-owner EXECUTE target", f.owner.QueryRow(f.ctx, "SELECT p.proowner=r.oid FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_roles r ON r.rolname=$2 WHERE p.oid=pg_catalog.to_regprocedure($1)", rbGrantFunction(f, p.function), role).Scan(&owned))
	} else {
		var generated string
		must(t, "observe fixed column target", f.owner.QueryRow(f.ctx, `SELECT c.relowner=r.oid,a.attgenerated::text FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid JOIN pg_catalog.pg_roles r ON r.rolname=$3 WHERE n.nspname=$1 AND c.relname=$2 AND a.attname=$4 AND a.attnum<>0 AND NOT a.attisdropped`, f.b.Schema, p.table, role, p.column).Scan(&owned, &generated))
		if p.privilege != "SELECT" && generated != "" {
			t.Fatal("generated column is not a necessary DML target")
		}
	}
	if owned {
		t.Fatal("owner authorization cannot be represented as an ordinary grant fault: " + p.name())
	}
}
func rbGrantRevoke(t *testing.T, f *fixture, p rbGrantItem) func() error {
	t.Helper()
	rbGrantTarget(t, f, p)
	must(t, "effective grant before exact REVOKE", rbGrantEffective(f.ctx, f, p, true))
	baseline, err := rbGrantACL(f.ctx, f, p)
	must(t, "snapshot full object ACL", err)
	restored := false
	restore := func() error {
		if restored {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := f.owner.Exec(ctx, "GRANT "+rbGrantClause(f, p)+" TO "+pgx.Identifier{rbGrantRole(f, p)}.Sanitize()); e != nil {
			return e
		}
		if e := rbGrantEffective(ctx, f, p, true); e != nil {
			return e
		}
		after, e := rbGrantACL(ctx, f, p)
		if e != nil {
			return e
		}
		if after != baseline {
			return fmt.Errorf("complete canonical ACL not restored for %s; baseline=%s actual=%s", p.name(), baseline, after)
		}
		restored = true
		return nil
	}
	t.Cleanup(func() {
		if e := restore(); e != nil {
			t.Errorf("independent exact grant cleanup: %v", e)
		}
	})
	_, err = f.owner.Exec(f.ctx, "REVOKE "+rbGrantClause(f, p)+" FROM "+pgx.Identifier{rbGrantRole(f, p)}.Sanitize())
	must(t, "revoke only fixed ordinary grant", err)
	must(t, "effective grant really false", rbGrantEffective(f.ctx, f, p, false))
	return restore
}

func rbGrantRoleBoundary(t *testing.T, f *fixture) {
	t.Helper()
	var safe bool
	must(t, "observe W actual isolation", f.owner.QueryRow(f.ctx, `SELECT NOT r.rolcanlogin AND NOT r.rolinherit AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members m WHERE m.member=r.oid OR m.roleid=r.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname=$2 AND n.nspowner=r.oid)
 AND NOT pg_catalog.has_schema_privilege(r.oid,$2,'CREATE')
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$2 AND c.relowner=r.oid)
 FROM pg_catalog.pg_roles r WHERE r.oid=$1::oid`, f.b.WriterOID, f.b.Schema).Scan(&safe))
	if !safe {
		t.Fatal("W isolation drift")
	}
	var session, current string
	must(t, "observe actual APP", f.app.QueryRow(f.ctx, "SELECT session_user,current_user").Scan(&session, &current))
	if session != f.b.App || current != f.b.App {
		t.Fatal("not actual APP LOGIN")
	}
	names := []string{"pp_rb_request_write_v1", "pp_rb_capture_request_v1", "pp_rb_source_write_v1", "pp_rb_shared_write_v1", "pp_rb_validate_request_v1", "pp_rb_validate_root_v1", "pp_rb_deferred_v1"}
	var count int
	must(t, "observe unchanged function owners/security/search path", f.owner.QueryRow(f.ctx, `SELECT count(*) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=$1 AND p.proname=ANY($2::text[]) AND p.proowner=$3::oid
 AND p.prosecdef=(p.proname='pp_rb_capture_request_v1') AND p.provolatile='v' AND p.proparallel='u'
 AND NOT p.proisstrict AND cardinality(p.proconfig)=1 AND replace(replace(p.proconfig[1],' ',''),'"','')='search_path='||$1||',pg_catalog,pg_temp'`, f.b.Schema, names, f.b.WriterOID).Scan(&count))
	if count != 7 {
		t.Fatal("fixed seven-function catalog boundary drift")
	}
	for _, fn := range []string{"pp_rb_request_write_v1()", "pp_rb_capture_request_v1()", "pp_rb_source_write_v1()", "pp_rb_shared_write_v1()", "pp_rb_deferred_v1()"} {
		must(t, "APP private direct EXECUTE remains false", rbGrantEffective(f.ctx, f, rbGrantItem{role: "APP", privilege: "EXECUTE", function: fn}, false))
	}
	// Compare columns with the fixed inventory, rather than deriving test cases
	// from whatever the installer currently happens to grant.
	for _, g := range rbGrantReads {
		rows, e := f.owner.Query(f.ctx, `SELECT a.attname::text FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attname`, f.b.Schema, g.table)
		must(t, "enumerate installed exact columns", e)
		columns, e := pgx.CollectRows(rows, pgx.RowTo[string])
		must(t, "collect exact columns", e)
		if !reflect.DeepEqual(columns, rbGrantSortedColumns(g.columns)) {
			t.Fatal("installed column inventory differs: " + g.table)
		}
	}
}
func rbGrantFunds(t *testing.T, f *fixture) [3]int64 {
	t.Helper()
	var v [3]int64
	must(t, "observe original customer funds and Key held", f.owner.QueryRow(f.ctx, `SELECT (SELECT balance FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE'),(SELECT balance FROM ledger_accounts WHERE tenant_id=$1 AND kind='HELD'),(SELECT held FROM api_keys WHERE tenant_id=$1 AND id=$2)`, f.tenant, f.key).Scan(&v[0], &v[1], &v[2]))
	return v
}
func rbGrantSuccess(t *testing.T, f *fixture) billing.Reservation {
	t.Helper()
	before := rbGrantFunds(t, f)
	r := f.reserve(t)
	if r.Amount != 120 {
		t.Fatalf("restored original reservation amount=%v", r.Amount)
	}
	f.checkBirth(t, r.RequestID)
	after := rbGrantFunds(t, f)
	if after != [3]int64{before[0] - 120, before[1] + 120, before[2] + 120} {
		t.Fatalf("normal committed Reserve finances differ: before=%v after=%v", before, after)
	}
	return r
}
func rbGrantBaseline(t *testing.T, f *fixture) {
	t.Helper()
	rbGrantRoleBoundary(t, f)
	// Every restoration control commits a new real request. Fund once through
	// original verified credit/approval before snapshots, never during a fault.
	store := billing.New(f.owner)
	key := uuid.NewString()
	credit, e := store.CreateCredit(f.ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, BusinessRef: key, ProofRef: "fixture:grant-baseline-verified", Amount: 100000, IdempotencyKey: key, RequestHMAC: key})
	must(t, "prepare sufficient real baseline credit", e)
	_, e = store.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: f.approver})
	must(t, "approve sufficient original customer funds", e)
	rbGrantSuccess(t, f)
}
func rbGrantUnchanged(t *testing.T, f *fixture, before, request, idempotency string) {
	t.Helper()
	if f.snapshot(t) != before {
		t.Fatal("effective loss changed one of 18 complete business/source tables")
	}
	var n int
	must(t, "observe no failed request claims", f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM request_records WHERE id=$1)+(SELECT count(*) FROM reservations WHERE request_id=$1)
 +(SELECT count(*) FROM request_idempotency WHERE request_id=$1 OR (tenant_id=$2 AND key=$3))
 +(SELECT count(*) FROM pp_request_origins WHERE request_id=$1)+(SELECT count(*) FROM pp_request_heads WHERE request_id=$1)
 +(SELECT count(*) FROM pp_operation_roots WHERE request_id=$1)+(SELECT count(*) FROM pp_root_steps WHERE kind='request_birth' AND source_id=$1)`, request, f.tenant, idempotency).Scan(&n))
	if n != 0 {
		t.Fatal("failed request left a claimed natural identity")
	}
}

func TestPostgresA1NecessaryGrantFaultMatrix(t *testing.T) {
	f := newFixture(t)
	rbGrantBaseline(t, f)
	matrix := rbGrantMatrix()
	if len(matrix) != 290 {
		t.Fatal("frozen fault count drift")
	}
	for _, p := range matrix {
		t.Run(p.name(), func(t *testing.T) {
			before := f.snapshot(t)
			restore := rbGrantRevoke(t, f, p)
			in := f.input()
			_, err := billing.New(f.app).Reserve(f.ctx, in)
			code(t, err, "42501")
			must(t, "restore original grant and complete ACL", restore())
			rbGrantUnchanged(t, f, before, in.RequestID, in.IdempotencyKey)
			rbGrantSuccess(t, f)
		})
	}
}

// The first actual PG run failed the earlier necessary-loss assertion for
// precisely these two grants: effective permission was false but Reserve
// committed. Preserve the original short-circuit behavior and grant material.
func TestPostgresA1UnusedAllocationReadGrantControls(t *testing.T) {
	f := newFixture(t)
	rbGrantBaseline(t, f)
	for _, column := range []string{"subject_type", "subject_id"} {
		t.Run(column, func(t *testing.T) {
			p := rbGrantItem{role: "W", table: "periodic_budget_allocations", column: column, privilege: "SELECT"}
			restore := rbGrantRevoke(t, f, p)
			rbGrantSuccess(t, f) // Actual APP COMMIT with this W SELECT effectively false.
			must(t, "restore unused allocation grant and exact complete ACL", restore())
			rbGrantSuccess(t, f) // Original path remains executable after restoration.
		})
	}
}

func TestPostgresA1PBExecuteGrantControls(t *testing.T) {
	f := newFixture(t)
	rbGrantBaseline(t, f)
	p := rbGrantItem{role: "W", privilege: "EXECUTE", function: "pb_require_read_committed()"}
	fn := rbGrantFunction(f, p.function)
	qw := pgx.Identifier{f.b.Writer}.Sanitize()
	qa := pgx.Identifier{f.b.App}.Sanitize()
	t.Run("direct_W_grant_PUBLIC_overlap_positive", func(t *testing.T) {
		rbGrantTarget(t, f, p)
		before, e := rbGrantACL(f.ctx, f, p)
		must(t, "snapshot full original PB function ACL", e)
		must(t, "W original effective EXECUTE", rbGrantEffective(f.ctx, f, p, true))
		restored := false
		restore := func() error {
			if restored {
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, e := f.owner.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+fn+" TO "+qw); e != nil {
				return e
			}
			if e := rbGrantEffective(ctx, f, p, true); e != nil {
				return e
			}
			after, e := rbGrantACL(ctx, f, p)
			if e != nil {
				return e
			}
			if after != before {
				return fmt.Errorf("PB overlapping direct grant ACL not restored")
			}
			restored = true
			return nil
		}
		t.Cleanup(func() {
			if e := restore(); e != nil {
				t.Error(e)
			}
		})
		_, e = f.owner.Exec(f.ctx, "REVOKE EXECUTE ON FUNCTION "+fn+" FROM "+qw)
		must(t, "remove only W direct EXECUTE", e)
		must(t, "PUBLIC still authorizes W; this is not a fault", rbGrantEffective(f.ctx, f, p, true))
		rbGrantSuccess(t, f)
		must(t, "restore original PB direct grant", restore())
	})
	t.Run("PUBLIC_isolated_W_effective_loss", func(t *testing.T) {
		rbGrantTarget(t, f, p)
		original, e := rbGrantACL(f.ctx, f, p)
		must(t, "snapshot PB ACL before explicit isolation", e)
		var expected bool
		must(t, "observe exact PUBLIC and explicit W starting grants", f.owner.QueryRow(f.ctx, `SELECT p.proowner<>$2::oid
 AND EXISTS(SELECT 1 FROM pg_catalog.aclexplode(coalesce(p.proacl,pg_catalog.acldefault('f',p.proowner))) x WHERE x.grantee=0 AND x.grantor=p.proowner AND x.privilege_type='EXECUTE' AND NOT x.is_grantable)
 AND EXISTS(SELECT 1 FROM pg_catalog.aclexplode(p.proacl) x WHERE x.grantee=$2::oid AND x.grantor=p.proowner AND x.privilege_type='EXECUTE' AND NOT x.is_grantable)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(p.proacl) x WHERE x.grantee=$3::oid)
 FROM pg_catalog.pg_proc p WHERE p.oid=pg_catalog.to_regprocedure($1)`, fn, f.b.WriterOID, f.b.AppOID).Scan(&expected))
		if !expected {
			t.Fatal("PB isolation would overwrite an unexpected original grantee/options")
		}
		restored := false
		restore := func() error {
			if restored {
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, sql := range []string{"GRANT EXECUTE ON FUNCTION " + fn + " TO " + qw, "GRANT EXECUTE ON FUNCTION " + fn + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + fn + " FROM " + qa} {
				if _, e := f.owner.Exec(ctx, sql); e != nil {
					return e
				}
			}
			if e := rbGrantEffective(ctx, f, p, true); e != nil {
				return e
			}
			if e := rbGrantEffective(ctx, f, rbGrantItem{role: "APP", privilege: "EXECUTE", function: p.function}, true); e != nil {
				return e
			}
			after, e := rbGrantACL(ctx, f, p)
			if e != nil {
				return e
			}
			if after != original {
				return fmt.Errorf("full original PB function ACL not restored; original=%s actual=%s", original, after)
			}
			restored = true
			return nil
		}
		t.Cleanup(func() {
			if e := restore(); e != nil {
				t.Errorf("independent PUBLIC isolation cleanup: %v", e)
			}
		})
		_, e = f.owner.Exec(f.ctx, "GRANT EXECUTE ON FUNCTION "+fn+" TO "+qa)
		must(t, "temporarily preserve only original APP PB execution", e)
		_, e = f.owner.Exec(f.ctx, "REVOKE EXECUTE ON FUNCTION "+fn+" FROM PUBLIC")
		must(t, "remove PUBLIC alternate EXECUTE for this explicit scenario", e)
		must(t, "W still true via original explicit grant", rbGrantEffective(f.ctx, f, p, true))
		must(t, "APP remains true before fault", rbGrantEffective(f.ctx, f, rbGrantItem{role: "APP", privilege: "EXECUTE", function: p.function}, true))
		before := f.snapshot(t)
		_, e = f.owner.Exec(f.ctx, "REVOKE EXECUTE ON FUNCTION "+fn+" FROM "+qw)
		must(t, "remove W now-isolated EXECUTE", e)
		must(t, "W really false", rbGrantEffective(f.ctx, f, p, false))
		must(t, "APP remains true after W loss", rbGrantEffective(f.ctx, f, rbGrantItem{role: "APP", privilege: "EXECUTE", function: p.function}, true))
		in := f.input()
		_, e = billing.New(f.app).Reserve(f.ctx, in)
		code(t, e, "42501")
		must(t, "restore exact original PB ACL and effective permissions", restore())
		rbGrantUnchanged(t, f, before, in.RequestID, in.IdempotencyKey)
		rbGrantSuccess(t, f)
	})
}

// No helper suppresses pgx cancellation/rollback errors, rewrites a privilege
// failure, creates a root, or refreshes permissions. API failures must preserve
// the original PostgreSQL cause through any ordinary billing wrappers.
