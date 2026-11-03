package procurementrequestbirth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/procurementbirthschema"
	"github.com/moni-ai/moni-aigate/internal/procurementrequestbirth"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Test-only catalog attestation. Neither installing the writer nor calling its
// readonly row validators automatically invokes this inspector.
type rbCatalogDB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type rbCatalogFailure struct{ group, object string }

func (e *rbCatalogFailure) Error() string { return "A1 catalog " + e.group + ": " + e.object }

func rbCatalogFail(group, object string) error { return &rbCatalogFailure{group, object} }

var rbCatalogTables = []string{"pp_operation_roots", "pp_request_heads", "pp_request_origins", "pp_root_steps", "request_records"}

// Physical order is independent of the writer's INSERT order and its ACL SQL.
var rbCatalogColumns = map[string]string{
	"request_records":    "id,tenant_id,key_id,owner_id,model_id,status,billing_status,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,latest_finalization_id,created_at,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_last_decision_id",
	"pp_operation_roots": "id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result",
	"pp_request_heads":   "request_id,ordinal,last_decision_id,updated_xid",
	"pp_request_origins": "request_id,root_id,step_no,step_kind,created_xid,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,request_created_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,initial_latest_id,initial_status,initial_billing_status",
	"pp_root_steps":      "root_id,step_no,kind,source_id,created_xid,d_request_birth,d_attempt_birth,d_account_command,d_attempt_delivery,d_incoming,d_meter_choice,d_request_decision,d_normal_cost_decision,d_cost_mutation,d_cost_reset_decision,d_evidence_choice,d_procurement_transition,d_proposal_birth,d_approval_consumed,d_unmanaged_correction,d_recovery,d_cost_outbox,d_reconciliation",
}

var rbCatalogConstraintNames = map[string]string{
	"request_records":    "request_records_pkey,request_records_tenant_id_id_key,request_records_input_rate_check,request_records_output_rate_check,request_records_input_upper_bound_check,request_records_output_limit_check,request_records_route_revision_check,request_retail_snapshot_shape,request_records_tenant_id_fkey,request_records_owner_id_fkey,request_records_model_id_fkey,request_records_price_version_id_fkey,request_records_tenant_id_key_id_fkey,request_finalization_fk,request_records_pricing_group_id_fkey,pp_o_request_birth_ck,pp_bx_original_request_step_locator_fk",
	"pp_operation_roots": "pp_s_root_pk,pp_s_root_xid_uq,pp_s_root_scalar_ck,pp_s_root_template_ck,pp_s_root_request_fk,pp_bx_root_request_origin_fk",
	"pp_root_steps":      "pp_s_step_pk,pp_s_step_shape_ck,pp_s_step_root_fk,pp_bx_step_request_origin_fk",
	"pp_request_origins": "pp_s_request_birth_pk,pp_s_request_birth_step_ck,pp_s_request_initial_ck,pp_s_request_snapshot_ck,pp_s_request_birth_root_fk,pp_s_request_original_fk,pp_s_request_origin_head_fk,pp_bx_request_step_locator_fk",
	"pp_request_heads":   "pp_s_request_head_pk,pp_s_request_head_shape_ck,pp_s_request_head_origin_fk",
}

var rbCatalogFunctions = map[string]string{
	"pp_rb_validate_request_v1": "p_request",
	"pp_rb_validate_root_v1":    "p_root",
	"pp_rb_request_write_v1":    "",
	"pp_rb_source_write_v1":     "",
	"pp_rb_shared_write_v1":     "",
	"pp_rb_capture_request_v1":  "",
	"pp_rb_deferred_v1":         "",
}

func rbCatalogDigest(raw []byte) string {
	s := sha256.Sum256(raw)
	return hex.EncodeToString(s[:])
}

func rbCatalogMaterials(t *testing.T) {
	t.Helper()
	manifest, err := migrations.Original22Manifest()
	must(t, "authenticate independent historical reference DDL", err)
	if rbCatalogDigest(manifest) != "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de" {
		t.Fatal("independent original22 reference manifest pin changed")
	}
	pins, err := procurementbirthschema.A1Digests()
	must(t, "authenticate independent A1 reference DDL", err)
	want := []procurementbirthschema.MaterialDigest{
		{Name: "a1-tables-v1.sql", SHA256: "483e429714616143337db75318a23a01b755a7585088879e2ec465d4aa6b0d74"},
		{Name: "a1-original-columns-v1.sql", SHA256: "2a0e056155a7947047bca5d1473ece29b575df34f2e6bf536d79704a8cfe08d7"},
		{Name: "a1-constraints-v1.sql", SHA256: "471931a538b4a304c5b0999fcacb5e8bb24ddcce48d2cd785cd88e96b2f087aa"},
	}
	if !reflect.DeepEqual(pins, want) {
		t.Fatal("independent A1 reference DDL pins changed")
	}
	d, err := procurementrequestbirth.Digests()
	must(t, "authenticate independent writer body pins", err)
	if d.SQLSHA256 != "325910df461aac3d8d068eb078bac08c18e75741765ee79aa316d5107373db4b" || d.PermissionsSHA256 != "32464fd4a1e01ebcff8c2c2793fb334e8be3d20be11e5e6497a0d785d6cfe0ef" {
		t.Fatal("independent catalog writer/ACL pins changed")
	}
}

func TestA1CatalogIndependentInventory(t *testing.T) {
	rbCatalogMaterials(t)
	n := 0
	for table, csv := range rbCatalogColumns {
		seen := map[string]bool{}
		for _, c := range strings.Split(csv, ",") {
			if seen[c] || c == "" {
				t.Fatalf("duplicate physical column %s.%s", table, c)
			}
			seen[c] = true
			n++
		}
	}
	constraints := 0
	for _, csv := range rbCatalogConstraintNames {
		constraints += len(strings.Split(csv, ","))
	}
	if n != 104 || constraints != 38 || len(rbCatalogFunctions) != 7 || len(rbCatalogNewTriggers()) != 16 {
		t.Fatalf("independent inventory columns=%d constraints=%d", n, constraints)
	}
	b := procurementrequestbirth.Bindings{Schema: "rb_catalog_static", App: "rb_catalog_app", Writer: "rb_catalog_w", AppOID: 19001, WriterOID: 19002}
	bodies, err := rbCatalogBodies(b)
	must(t, "parse separately pinned writer bodies", err)
	if len(bodies) != 7 {
		t.Fatal("body inventory must be exactly seven")
	}
	faultNames := map[string]bool{}
	for _, fault := range rbCatalogFaults(t, &fixture{b: b}) {
		if faultNames[fault.name] || fault.group == "" || fault.sql == "" {
			t.Fatal("catalog fault inventory duplicate/incomplete: " + fault.name)
		}
		faultNames[fault.name] = true
	}
	if len(faultNames) < 75 {
		t.Fatalf("catalog drift coverage unexpectedly narrowed: %d", len(faultNames))
	}
	t.Logf("independent catalog drift inventory: %d transaction faults", len(faultNames))
}

func rbCatalogNormalize(body, schema string) string {
	// Only qualifiers of our two explicit trusted schemas are normalized. String
	// values, object names, OIDs and arbitrary user text are never stripped.
	body = strings.ReplaceAll(body, pgx.Identifier{schema}.Sanitize()+".", "<A1>.")
	return strings.ReplaceAll(body, schema+".", "<A1>.")
}

// Reference installation contains original DDL and the exact relation slice;
// no writer, A1 grants, business rows, LIKE, or exported target definitions.
func rbCatalogReference(t *testing.T, f *fixture) (*pgxpool.Pool, string) {
	t.Helper()
	rbCatalogMaterials(t)
	schema := f.b.Schema + "_ref"
	_, err := f.owner.Exec(f.ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	must(t, "create independent reference namespace", err)
	cfg := f.owner.Config().Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 2
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	must(t, "connect independent reference namespace", err)
	t.Cleanup(func() {
		p.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, e := f.owner.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		if e != nil {
			t.Errorf("reference cleanup: %v", e)
		}
		var count int
		if e = f.owner.QueryRow(ctx, "SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspname=$1", schema).Scan(&count); e != nil || count != 0 {
			t.Errorf("reference namespace residual: %d %v", count, e)
		}
	})
	must(t, "replay frozen original001022 reference only", migrations.ApplyThrough(f.ctx, p, "022_payment_runtime_attestation.sql"))
	ddl, err := procurementbirthschema.A1SQL(schema)
	must(t, "render frozen relation reference only", err)
	_, err = p.Exec(f.ctx, ddl)
	must(t, "install frozen relation reference only", err)
	return p, schema
}

type rbCatalogExpected struct {
	b          procurementrequestbirth.Bindings
	owner      uint32
	ownerName  string
	roles      map[string]uint32
	groups     map[string]string
	identities map[string]uint32
}

// Each query returns one canonical, ordered JSON array. References between
// schema-local objects are resolved by their exact namespace and names; their
// actual numerical OIDs are checked independently by the identity inventory.
var rbCatalogQueries = map[string]string{
	"relations": `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.name),'[]'::jsonb) FROM (
 SELECT c.relname AS name,c.relkind,c.relpersistence,c.relowner::bigint AS relowner,c.relispartition,c.relrowsecurity,c.relforcerowsecurity,c.relreplident,c.reloftype::bigint AS reloftype,c.reloptions,c.relhasrules,a.amname,
 EXISTS(SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid=c.oid OR i.inhparent=c.oid) AS inherited
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_catalog.pg_am a ON a.oid=c.relam WHERE n.nspname=$1 AND c.relname=ANY($2::text[])) x`,
	"columns": `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.attnum),'[]'::jsonb) FROM (
 SELECT c.relname AS table_name,a.attnum,a.attname,a.atttypid::bigint AS atttypid,a.atttypmod,a.attcollation::bigint AS attcollation,a.attnotnull,a.attidentity,a.attgenerated,a.attisdropped,a.atthasdef,a.atthasmissing,a.attislocal,a.attinhcount,a.attlen,a.attbyval,a.attalign,a.attstorage,a.attcompression,
 pg_catalog.pg_get_expr(d.adbin,d.adrelid,false) AS expression
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE n.nspname=$1 AND c.relname=ANY($2::text[]) AND a.attnum>0) x`,
	"constraints": `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.name),'[]'::jsonb) FROM (
 SELECT c.relname AS table_name,k.conname AS name,k.contype,k.condeferrable,k.condeferred,k.convalidated,k.connoinherit,k.conislocal,k.coninhcount,k.conkey,k.confkey,k.confmatchtype,k.confupdtype,k.confdeltype,k.conparentid::bigint AS conparentid,
 CASE WHEN f.oid IS NOT NULL THEN fn.nspname END AS target_schema,f.relname AS target_table,ix.relname AS target_index,ixn.nspname AS target_index_schema,
 CASE WHEN k.contype='f' THEN ii.indrelid=k.confrelid WHEN k.contype IN ('p','u') THEN ii.indrelid=k.conrelid ELSE NULL END AS index_on_exact_relation,
 pg_catalog.pg_get_constraintdef(k.oid,false) AS definition
 FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_catalog.pg_class f ON f.oid=k.confrelid LEFT JOIN pg_catalog.pg_namespace fn ON fn.oid=f.relnamespace LEFT JOIN pg_catalog.pg_class ix ON ix.oid=k.conindid LEFT JOIN pg_catalog.pg_namespace ixn ON ixn.oid=ix.relnamespace LEFT JOIN pg_catalog.pg_index ii ON ii.indexrelid=k.conindid
 WHERE n.nspname=$1 AND c.relname=ANY($2::text[]) AND k.contype IN ('p','u','c','f')) x`,
	"indexes": `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.name),'[]'::jsonb) FROM (
 SELECT c.relname AS table_name,ix.relname AS name,ix.relnamespace=c.relnamespace AS same_namespace,ix.relowner::bigint AS relowner,ix.relkind,ix.relpersistence,ix.reloptions,a.amname,i.indnatts,i.indnkeyatts,i.indisunique,i.indnullsnotdistinct,i.indisprimary,i.indisexclusion,i.indimmediate,i.indisclustered,i.indisvalid,i.indcheckxmin,i.indisready,i.indislive,i.indisreplident,i.indkey::text,i.indcollation::text,i.indclass::text,i.indoption::text,
 pg_catalog.pg_get_expr(i.indexprs,i.indrelid,false) AS expressions,pg_catalog.pg_get_expr(i.indpred,i.indrelid,false) AS predicate,pg_catalog.pg_get_indexdef(ix.oid,0,false) AS definition
 FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_class ix ON ix.oid=i.indexrelid JOIN pg_catalog.pg_am a ON a.oid=ix.relam WHERE n.nspname=$1 AND c.relname=ANY($2::text[])) x`,
}

func rbCatalogJSON(ctx context.Context, db rbCatalogDB, query, schema string) (string, error) {
	var raw []byte
	if err := db.QueryRow(ctx, query, schema, rbCatalogTables).Scan(&raw); err != nil {
		return "", err
	}
	// Normalize qualifiers in parsed string values, never replace substrings in
	// the JSON syntax itself. A target schema name is also an explicit field.
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	var norm func(any) any
	norm = func(v any) any {
		switch x := v.(type) {
		case string:
			if x == schema {
				return "<A1>"
			}
			return rbCatalogNormalize(x, schema)
		case []any:
			for i := range x {
				x[i] = norm(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = norm(x[k])
			}
		}
		return v
	}
	raw, err := json.Marshal(norm(value))
	return string(raw), err
}

func rbCatalogExpectedNew(t *testing.T, f *fixture) *rbCatalogExpected {
	t.Helper()
	p, schema := rbCatalogReference(t, f)
	e := &rbCatalogExpected{b: f.b, roles: map[string]uint32{}, groups: map[string]string{}}
	var current, session string
	must(t, "authenticate installer identity", f.owner.QueryRow(f.ctx, `SELECT current_user,session_user,current_user::regrole::oid`).Scan(&current, &session, &e.owner))
	if current != session {
		t.Fatal("installer session/current identity differ")
	}
	e.ownerName = current
	for _, name := range []string{f.b.App, f.b.Writer, strings.Replace(f.b.App, "rb_app_", "rb_peer_", 1), strings.Replace(f.b.App, "rb_app_", "rb_proj_", 1)} {
		var oid uint32
		must(t, "resolve independent fixture role identity", f.owner.QueryRow(f.ctx, "SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1", name).Scan(&oid))
		e.roles[name] = oid
	}
	if e.roles[f.b.App] != f.b.AppOID || e.roles[f.b.Writer] != f.b.WriterOID {
		t.Fatal("binding OID differs from actual fixture role")
	}
	for group, query := range rbCatalogQueries {
		value, err := rbCatalogJSON(f.ctx, p, query, schema)
		must(t, "collect fixed reference "+group, err)
		e.groups[group] = value
	}
	// The reference is checked against independent cardinality/name/order lists
	// before it is allowed to supply PG16-normalized expressions and metadata.
	var columns []struct {
		Table     string `json:"table_name"`
		Name      string `json:"attname"`
		Num       int    `json:"attnum"`
		Generated string `json:"attgenerated"`
	}
	must(t, "decode independent column reference", json.Unmarshal([]byte(e.groups["columns"]), &columns))
	counts := map[string][]string{}
	generated := 0
	for _, c := range columns {
		counts[c.Table] = append(counts[c.Table], c.Name)
		if c.Num != len(counts[c.Table]) {
			t.Fatal("reference has physical gaps")
		}
		if c.Generated == "s" {
			generated++
		}
	}
	if len(columns) != 104 || generated != 19 {
		t.Fatalf("fixed reference columns=%d generated=%d", len(columns), generated)
	}
	for table, csv := range rbCatalogColumns {
		if strings.Join(counts[table], ",") != csv {
			t.Fatalf("fixed reference physical order differs: %s", table)
		}
	}
	var constraints []struct {
		Table string `json:"table_name"`
		Name  string `json:"name"`
	}
	must(t, "decode independent constraint reference", json.Unmarshal([]byte(e.groups["constraints"]), &constraints))
	actual := map[string][]string{}
	for _, c := range constraints {
		actual[c.Table] = append(actual[c.Table], c.Name)
	}
	if len(constraints) != 38 {
		t.Fatalf("fixed reference constraint count=%d; inspect historical source, never accept live count", len(constraints))
	}
	for table, csv := range rbCatalogConstraintNames {
		want := strings.Split(csv, ",")
		sort.Strings(want)
		sort.Strings(actual[table])
		if !reflect.DeepEqual(want, actual[table]) {
			t.Fatalf("fixed reference constraint names differ: %s %v", table, actual[table])
		}
	}
	var indexes []struct {
		Name string `json:"name"`
	}
	must(t, "decode independent indexes", json.Unmarshal([]byte(e.groups["indexes"]), &indexes))
	var names []string
	for _, i := range indexes {
		names = append(names, i.Name)
	}
	sort.Strings(names)
	wantIndexes := []string{"pp_s_request_birth_pk", "pp_s_request_head_pk", "pp_s_root_pk", "pp_s_root_xid_uq", "pp_s_step_pk", "request_records_pkey", "request_records_tenant_id_id_key", "requests_tenant_time"}
	sort.Strings(wantIndexes)
	if !reflect.DeepEqual(names, wantIndexes) {
		t.Fatalf("fixed reference index inventory differs: %v", names)
	}
	ids, err := rbCatalogIdentities(f.ctx, f.owner, f.b.Schema)
	must(t, "capture installed OID identities", err)
	e.identities = ids
	return e
}

func rbCatalogIdentities(ctx context.Context, db rbCatalogDB, schema string) (map[string]uint32, error) {
	rows, err := db.Query(ctx, `SELECT 'schema/'||nspname,oid FROM pg_catalog.pg_namespace WHERE nspname=$1
 UNION ALL SELECT 'relation/'||c.relname,c.oid FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=ANY($2::text[])
 UNION ALL SELECT 'function/'||p.proname||'/'||p.proargtypes::text,p.oid FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND (left(p.proname,3)='pp_' OR p.proname IN ('pb_require_read_committed','preserve_request_client_ip','pb_stamp_request','guard_request_retail_snapshot','pb_check_source_trigger'))`, schema, rbCatalogTables)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint32{}
	for rows.Next() {
		var name string
		var oid uint32
		if err = rows.Scan(&name, &oid); err != nil {
			return nil, err
		}
		if _, ok := out[name]; ok {
			return nil, rbCatalogFail("identity", name)
		}
		out[name] = oid
	}
	return out, rows.Err()
}

func rbCatalogBodies(b procurementrequestbirth.Bindings) (map[string]string, error) {
	sql, err := procurementrequestbirth.SQL(b)
	if err != nil {
		return nil, err
	}
	pattern := `(?s)CREATE FUNCTION ` + regexp.QuoteMeta(pgx.Identifier{b.Schema}.Sanitize()) + `\.(pp_[a-z_0-9]+)\([^)]*\) RETURNS (?:void|trigger)\s+LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY (?:INVOKER|DEFINER)\s+SET search_path=[^\n]+ AS \$moni_rb_[0-9]+\$(.*?)\$moni_rb_[0-9]+\$;`
	out := map[string]string{}
	for _, m := range regexp.MustCompile(pattern).FindAllStringSubmatch(sql, -1) {
		if _, ok := out[m[1]]; ok {
			return nil, rbCatalogFail("function", "duplicate source body")
		}
		out[m[1]] = m[2]
	}
	if len(out) != 7 {
		return nil, rbCatalogFail("function", "seven source bodies required")
	}
	return out, nil
}

type rbCatalogTrigger struct {
	Table, Name, Function, FunctionSchema  string
	FunctionOID                            uint32
	Type                                   int16
	Enabled                                string
	Parent, ConstraintRel, ConstraintIndex uint32
	Args                                   []byte
	NArgs                                  int16
	OldTable, NewTable                     *string
	Columns                                string
	When                                   *string
	Constraint                             string
	Deferred, Initially                    bool
	ConstraintType                         string
	ConstraintTable                        string
}

func rbCatalogNewTriggers() []rbCatalogTrigger {
	var out []rbCatalogTrigger
	add := func(table, name, fn string, bits int16, deferred bool) {
		v := rbCatalogTrigger{Table: table, Name: name, Function: "pp_rb_" + fn + "_v1", Type: bits, Enabled: "O", Args: []byte{}}
		if deferred {
			v.Constraint = name
			v.Deferred = true
			v.Initially = true
			v.ConstraintType = "t"
			v.ConstraintTable = table
		}
		out = append(out, v)
	}
	// pg_trigger bits: ROW=1 BEFORE=2 INSERT=4 DELETE=8 UPDATE=16 TRUNCATE=32.
	add("request_records", "aa_pp_rb_request_write", "request_write", 31, false)
	add("request_records", "aa_pp_rb_request_truncate", "request_write", 34, false)
	add("request_records", "zz_pp_rb_request_birth", "capture_request", 5, false)
	add("request_records", "zz_pp_rb_request_complete", "deferred", 21, true)
	for _, v := range []struct {
		table, short, fn string
		events           int16
	}{
		{"pp_request_origins", "origin", "source_write", 5},
		{"pp_request_heads", "head", "source_write", 21},
		{"pp_operation_roots", "root", "shared_write", 5},
		{"pp_root_steps", "step", "shared_write", 5},
	} {
		add(v.table, "aa_pp_rb_"+v.short+"_write", v.fn, 31, false)
		add(v.table, "aa_pp_rb_"+v.short+"_truncate", v.fn, 34, false)
		add(v.table, "zz_pp_rb_"+v.short+"_complete", "deferred", v.events, true)
	}
	return out
}

func rbCatalogTriggers(ctx context.Context, db rbCatalogDB, schema string) ([]rbCatalogTrigger, error) {
	rows, err := db.Query(ctx, `SELECT c.relname,t.tgname,p.proname,pn.nspname,p.oid,t.tgtype,t.tgenabled::text,t.tgparentid,t.tgconstrrelid,t.tgconstrindid,t.tgargs,t.tgnargs,t.tgoldtable,t.tgnewtable,t.tgattr::text,t.tgqual::text,coalesce(k.conname,''),t.tgdeferrable,t.tginitdeferred,coalesce(k.contype::text,''),coalesce(kc.relname,'')
 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid JOIN pg_catalog.pg_namespace pn ON pn.oid=p.pronamespace LEFT JOIN pg_catalog.pg_constraint k ON k.oid=t.tgconstraint LEFT JOIN pg_catalog.pg_class kc ON kc.oid=k.conrelid
 WHERE n.nspname=$1 AND c.relname=ANY($2::text[]) AND NOT t.tgisinternal ORDER BY c.relname,t.tgname`, schema, rbCatalogTables)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rbCatalogTrigger
	for rows.Next() {
		var v rbCatalogTrigger
		if err = rows.Scan(&v.Table, &v.Name, &v.Function, &v.FunctionSchema, &v.FunctionOID, &v.Type, &v.Enabled, &v.Parent, &v.ConstraintRel, &v.ConstraintIndex, &v.Args, &v.NArgs, &v.OldTable, &v.NewTable, &v.Columns, &v.When, &v.Constraint, &v.Deferred, &v.Initially, &v.ConstraintType, &v.ConstraintTable); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func rbCatalogCheckTriggers(ctx context.Context, db rbCatalogDB, e *rbCatalogExpected) error {
	want := rbCatalogNewTriggers()
	for _, v := range []rbCatalogTrigger{
		{Table: "request_records", Name: "immutable_request_client_ip", Function: "preserve_request_client_ip", Type: 19, Columns: "22"},
		{Table: "request_records", Name: "pb_request_stamp", Function: "pb_stamp_request", Type: 31},
		{Table: "request_records", Name: "request_retail_snapshot", Function: "guard_request_retail_snapshot", Type: 23},
		{Table: "request_records", Name: "pb_request_source_check", Function: "pb_check_source_trigger", Type: 21, Constraint: "pb_request_source_check", Deferred: true, Initially: true, ConstraintType: "t", ConstraintTable: "request_records"},
	} {
		v.Enabled = "O"
		v.Args = []byte{}
		want = append(want, v)
	}
	actual, err := rbCatalogTriggers(ctx, db, e.b.Schema)
	if err != nil {
		return rbCatalogFail("trigger", "read: "+err.Error())
	}
	if len(actual) != 20 {
		return rbCatalogFail("trigger", fmt.Sprintf("user trigger count %d", len(actual)))
	}
	byName := map[string]rbCatalogTrigger{}
	for _, v := range want {
		byName[v.Table+"/"+v.Name] = v
	}
	for _, got := range actual {
		key := got.Table + "/" + got.Name
		w, ok := byName[key]
		if !ok {
			return rbCatalogFail("trigger", key+" unexpected")
		}
		w.FunctionSchema = e.b.Schema
		var oid uint32
		if err = db.QueryRow(ctx, `SELECT p.oid FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname=$2 AND p.pronargs=0`, e.b.Schema, w.Function).Scan(&oid); err != nil {
			return rbCatalogFail("trigger", key+" function identity")
		}
		w.FunctionOID = oid
		if !reflect.DeepEqual(got, w) {
			return rbCatalogFail("trigger", fmt.Sprintf("%s actual=%+v expected=%+v", key, got, w))
		}
		delete(byName, key)
	}
	var ct int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=ANY($2::text[]) AND k.contype='t'`, e.b.Schema, rbCatalogTables).Scan(&ct); err != nil || ct != 6 {
		return rbCatalogFail("trigger", fmt.Sprintf("constraint trigger records %d %v", ct, err))
	}
	return nil
}

func rbCatalogCheckRoles(ctx context.Context, db rbCatalogDB, e *rbCatalogExpected) error {
	var owner uint32
	var current, session string
	if err := db.QueryRow(ctx, "SELECT current_user,session_user,current_user::regrole::oid").Scan(&current, &session, &owner); err != nil {
		return rbCatalogFail("identity", "installer read")
	}
	if current != e.ownerName || session != current || owner != e.owner {
		return rbCatalogFail("identity", "installer current/session/OID")
	}
	if e.roles[e.b.App] != e.b.AppOID || e.roles[e.b.Writer] != e.b.WriterOID {
		return rbCatalogFail("identity", "bound role OID/name")
	}
	for name, wantOID := range e.roles {
		var oid uint32
		var login, inherit, super, dbcreate, rolecreate, repl, bypass bool
		err := db.QueryRow(ctx, `SELECT oid,rolcanlogin,rolinherit,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls FROM pg_catalog.pg_roles WHERE rolname=$1`, name).Scan(&oid, &login, &inherit, &super, &dbcreate, &rolecreate, &repl, &bypass)
		wantLogin := name == e.b.App || strings.HasPrefix(name, "rb_peer_")
		if err != nil || oid != wantOID || login != wantLogin || inherit || super || dbcreate || rolecreate || repl || bypass {
			return rbCatalogFail("role", name+" OID/flags")
		}
		var members int
		if err = db.QueryRow(ctx, "SELECT count(*) FROM pg_catalog.pg_auth_members WHERE roleid=$1 OR member=$1", wantOID).Scan(&members); err != nil || members != 0 {
			return rbCatalogFail("role", name+" membership")
		}
	}
	var schemaOwner uint32
	if err := db.QueryRow(ctx, "SELECT nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1", e.b.Schema).Scan(&schemaOwner); err != nil || schemaOwner != e.owner {
		return rbCatalogFail("schema", "owner")
	}
	return nil
}

func rbCatalogCheckFunctions(ctx context.Context, db rbCatalogDB, e *rbCatalogExpected) error {
	bodies, err := rbCatalogBodies(e.b)
	if err != nil {
		return err
	}
	// PostgreSQL's JSON categorizer renders OID scalars/array elements as JSON
	// strings. Explicit bigint casts keep the fixed numeric OID contract; they
	// neither resolve types by a live name nor derive expected values from it.
	rows, err := db.Query(ctx, `SELECT p.proname,jsonb_build_object('owner',p.proowner::bigint,'language',l.lanname,'kind',p.prokind,'volatility',p.provolatile,'parallel',p.proparallel,'definer',p.prosecdef,'strict',p.proisstrict,'leakproof',p.proleakproof,'set',p.proretset,'nargs',p.pronargs,'ndefaults',p.pronargdefaults,'argtypes',p.proargtypes::oid[]::bigint[],'allargtypes',p.proallargtypes::bigint[],'argmodes',p.proargmodes,'argnames',p.proargnames,'result',p.prorettype::bigint,'config',p.proconfig,'body',p.prosrc,'binary',p.probin,'support',p.prosupport::oid::bigint,'transforms',p.protrftypes::bigint[],'cost',p.procost,'rows',p.prorows)
 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace JOIN pg_catalog.pg_language l ON l.oid=p.prolang WHERE n.nspname=$1 AND left(p.proname,3)='pp_' ORDER BY p.proname`, e.b.Schema)
	if err != nil {
		return rbCatalogFail("function", "query: "+err.Error())
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name string
		var raw []byte
		if err = rows.Scan(&name, &raw); err != nil {
			return err
		}
		arg, ok := rbCatalogFunctions[name]
		if !ok || seen[name] {
			return rbCatalogFail("function", name+" extra/overload")
		}
		seen[name] = true
		args := []uint32{}
		var argnames any
		result := uint32(2279)
		if arg != "" {
			args = []uint32{25}
			argnames = []string{arg}
			result = 2278
		}
		want := map[string]any{"owner": e.b.WriterOID, "language": "plpgsql", "kind": "f", "volatility": "v", "parallel": "u", "definer": name == "pp_rb_capture_request_v1", "strict": false, "leakproof": false, "set": false, "nargs": len(args), "ndefaults": 0, "argtypes": args, "allargtypes": nil, "argmodes": nil, "argnames": argnames, "result": result, "config": []string{"search_path=" + e.b.Schema + ", pg_catalog, pg_temp"}, "body": bodies[name], "binary": nil, "support": 0, "transforms": nil, "cost": 100, "rows": 0}
		var got map[string]any
		var expected map[string]any
		if err = json.Unmarshal(raw, &got); err != nil {
			return err
		}
		encoded, _ := json.Marshal(want)
		if err = json.Unmarshal(encoded, &expected); err != nil {
			return err
		}
		if !reflect.DeepEqual(got, expected) {
			for k, v := range expected {
				if !reflect.DeepEqual(got[k], v) {
					return rbCatalogFail("function", name+" attribute "+k)
				}
			}
			return rbCatalogFail("function", name+" metadata")
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(seen) != 7 {
		return rbCatalogFail("function", "missing reviewed function")
	}
	return nil
}

type rbCatalogACL struct {
	Kind, Object, Column string
	Grantor, Grantee     uint32
	Privilege            string
	Option               bool
}

func rbCatalogACLKey(v rbCatalogACL) string {
	return fmt.Sprintf("%s/%s/%s/%d/%d/%s/%t", v.Kind, v.Object, v.Column, v.Grantor, v.Grantee, v.Privilege, v.Option)
}

func rbCatalogACLExpected(e *rbCatalogExpected) map[string]rbCatalogACL {
	out := map[string]rbCatalogACL{}
	add := func(kind, object, column string, grantor, grantee uint32, priv string) {
		v := rbCatalogACL{Kind: kind, Object: object, Column: column, Grantor: grantor, Grantee: grantee, Privilege: priv}
		out[rbCatalogACLKey(v)] = v
	}
	for _, table := range rbCatalogTables {
		for _, priv := range []string{"INSERT", "SELECT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
			add("table", table, "", e.owner, e.owner, priv)
		}
		for _, column := range strings.Split(rbCatalogColumns[table], ",") {
			for _, r := range []uint32{e.b.AppOID, e.b.WriterOID} {
				add("column", table, column, e.owner, r, "SELECT")
			}
		}
	}
	add("column", "request_records", "xmin", e.owner, e.b.AppOID, "SELECT")
	// This independent list is not generated from live ACLs or the fault matrix.
	for _, c := range strings.Split("id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at", ",") {
		add("column", "request_records", c, e.owner, e.b.AppOID, "INSERT")
	}
	for _, c := range []string{"status", "billing_status"} {
		add("column", "request_records", c, e.owner, e.b.AppOID, "UPDATE")
	}
	for _, table := range []string{"pp_operation_roots", "pp_request_heads", "pp_root_steps", "pp_request_origins"} {
		for _, c := range strings.Split(rbCatalogColumns[table], ",") {
			if strings.HasPrefix(c, "d_") || c == "step_kind" {
				continue
			}
			add("column", table, c, e.owner, e.b.WriterOID, "INSERT")
		}
	}
	for _, c := range []string{"pp_origin_root_id", "pp_origin_root_step", "pp_origin_xid"} {
		add("column", "request_records", c, e.owner, e.b.WriterOID, "UPDATE")
	}
	for _, v := range []struct{ table, col string }{{"pp_request_origins", "request_id"}, {"pp_request_heads", "request_id"}, {"pp_root_steps", "root_id"}} {
		add("column", v.table, v.col, e.owner, e.b.WriterOID, "UPDATE")
	}
	for name, arg := range rbCatalogFunctions {
		add("function", name, "", e.b.WriterOID, e.b.WriterOID, "EXECUTE")
		if arg != "" {
			add("function", name, "", e.b.WriterOID, e.b.AppOID, "EXECUTE")
		}
	}
	add("schema", e.b.Schema, "", e.owner, e.owner, "CREATE")
	add("schema", e.b.Schema, "", e.owner, e.owner, "USAGE")
	for _, oid := range e.roles {
		add("schema", e.b.Schema, "", e.owner, oid, "USAGE")
	}
	for _, oid := range []uint32{e.owner, 0, e.b.WriterOID} {
		add("external_function", "pb_require_read_committed", "", e.owner, oid, "EXECUTE")
	}
	for _, c := range []string{"subject_type", "subject_id"} {
		add("external_column", "periodic_budget_allocations", c, e.owner, e.b.WriterOID, "SELECT")
	}
	return out
}

const rbCatalogACLQuery = `WITH tables AS (SELECT c.* FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=ANY($2::text[])),
 functions AS (SELECT p.* FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND left(p.proname,3)='pp_')
 SELECT 'table',c.relname,''::text,a.grantor,a.grantee,a.privilege_type,a.is_grantable FROM tables c CROSS JOIN LATERAL pg_catalog.aclexplode(coalesce(c.relacl,pg_catalog.acldefault('r',c.relowner))) a
 UNION ALL SELECT 'column',c.relname,b.attname,a.grantor,a.grantee,a.privilege_type,a.is_grantable FROM tables c JOIN pg_catalog.pg_attribute b ON b.attrelid=c.oid CROSS JOIN LATERAL pg_catalog.aclexplode(b.attacl) a
 UNION ALL SELECT 'function',p.proname,'',a.grantor,a.grantee,a.privilege_type,a.is_grantable FROM functions p CROSS JOIN LATERAL pg_catalog.aclexplode(coalesce(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
 UNION ALL SELECT 'schema',n.nspname,'',a.grantor,a.grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_namespace n CROSS JOIN LATERAL pg_catalog.aclexplode(coalesce(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) a WHERE n.nspname=$1
 UNION ALL SELECT 'external_function',p.proname,'',a.grantor,a.grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace CROSS JOIN LATERAL pg_catalog.aclexplode(coalesce(p.proacl,pg_catalog.acldefault('f',p.proowner))) a WHERE n.nspname=$1 AND p.proname='pb_require_read_committed' AND p.pronargs=0
 UNION ALL SELECT 'external_column',c.relname,b.attname,a.grantor,a.grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute b ON b.attrelid=c.oid CROSS JOIN LATERAL pg_catalog.aclexplode(b.attacl) a WHERE n.nspname=$1 AND c.relname='periodic_budget_allocations' AND a.grantee=$3
 UNION ALL SELECT 'external_table',c.relname,'',a.grantor,a.grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace CROSS JOIN LATERAL pg_catalog.aclexplode(c.relacl) a WHERE n.nspname=$1 AND c.relname='periodic_budget_allocations' AND a.grantee=$3`

func rbCatalogCheckACL(ctx context.Context, db rbCatalogDB, e *rbCatalogExpected) error {
	want := rbCatalogACLExpected(e)
	rows, err := db.Query(ctx, rbCatalogACLQuery, e.b.Schema, rbCatalogTables, e.b.WriterOID)
	if err != nil {
		return rbCatalogFail("acl", "read: "+err.Error())
	}
	defer rows.Close()
	for rows.Next() {
		var v rbCatalogACL
		if err = rows.Scan(&v.Kind, &v.Object, &v.Column, &v.Grantor, &v.Grantee, &v.Privilege, &v.Option); err != nil {
			return err
		}
		key := rbCatalogACLKey(v)
		if _, ok := want[key]; !ok {
			return rbCatalogFail("acl", key+" unexpected grant")
		}
		delete(want, key)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(want) > 0 {
		var keys []string
		for k := range want {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return rbCatalogFail("acl", keys[0]+" missing grant")
	}
	return nil
}

func rbCatalogCheckEffective(ctx context.Context, db rbCatalogDB, e *rbCatalogExpected) error {
	var names []string
	for name := range e.roles {
		names = append(names, name)
	}
	sort.Strings(names)
	const query = `WITH roles AS (SELECT oid,rolname FROM pg_catalog.pg_roles WHERE rolname=ANY($3::text[])),
 tables AS (SELECT c.* FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=ANY($2::text[])),
 funcs AS (SELECT p.* FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND left(p.proname,3)='pp_')
 SELECT r.rolname,'column',c.relname,a.attname,v.privilege,pg_catalog.has_column_privilege(r.oid,c.oid,a.attnum,v.privilege),pg_catalog.has_column_privilege(r.oid,c.oid,a.attnum,v.privilege||' WITH GRANT OPTION')
 FROM roles r CROSS JOIN tables c JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid CROSS JOIN (VALUES ('SELECT'),('INSERT'),('UPDATE'),('REFERENCES')) v(privilege)
 UNION ALL SELECT r.rolname,'table',c.relname,'',v.privilege,pg_catalog.has_table_privilege(r.oid,c.oid,v.privilege),pg_catalog.has_table_privilege(r.oid,c.oid,v.privilege||' WITH GRANT OPTION')
 FROM roles r CROSS JOIN tables c CROSS JOIN (VALUES ('SELECT'),('INSERT'),('UPDATE'),('DELETE'),('TRUNCATE'),('REFERENCES'),('TRIGGER')) v(privilege)
 UNION ALL SELECT r.rolname,'function',p.proname,'','EXECUTE',pg_catalog.has_function_privilege(r.oid,p.oid,'EXECUTE'),pg_catalog.has_function_privilege(r.oid,p.oid,'EXECUTE WITH GRANT OPTION') FROM roles r CROSS JOIN funcs p
 UNION ALL SELECT r.rolname,'schema',n.nspname,'',v.privilege,pg_catalog.has_schema_privilege(r.oid,n.oid,v.privilege),pg_catalog.has_schema_privilege(r.oid,n.oid,v.privilege||' WITH GRANT OPTION') FROM roles r CROSS JOIN pg_catalog.pg_namespace n CROSS JOIN (VALUES ('USAGE'),('CREATE')) v(privilege) WHERE n.nspname=$1`
	rows, err := db.Query(ctx, query, e.b.Schema, rbCatalogTables, names)
	if err != nil {
		return rbCatalogFail("effective", "query: "+err.Error())
	}
	defer rows.Close()
	expected := rbCatalogACLExpected(e)
	count := 0
	for rows.Next() {
		var role, kind, object, column, priv string
		var granted, grantOption bool
		if err = rows.Scan(&role, &kind, &object, &column, &priv, &granted, &grantOption); err != nil {
			return err
		}
		count++
		grantor := e.owner
		if kind == "function" {
			grantor = e.b.WriterOID
		}
		key := rbCatalogACLKey(rbCatalogACL{Kind: kind, Object: object, Column: column, Grantor: grantor, Grantee: e.roles[role], Privilege: priv})
		_, want := expected[key]
		wantOption := kind == "function" && role == e.b.Writer
		if granted != want || grantOption != wantOption {
			return rbCatalogFail("effective", fmt.Sprintf("%s/%s/%s/%s/%s actual=%t/%t expected=%t/%t", role, kind, object, column, priv, granted, grantOption, want, wantOption))
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	// 4 roles: (104 ordinary + 30 system) * 4 column rights,
	// 5 * 7 table rights, 7 function rights and 2 schema rights.
	if count != 4*((104+30)*4+5*7+7+2) {
		return rbCatalogFail("effective", fmt.Sprintf("privilege rows=%d", count))
	}
	// Verify the six actual PG16 negative attnums independently of the ACL rows.
	rows.Close()
	systems, err := db.Query(ctx, `SELECT c.relname,a.attnum,a.attname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid WHERE n.nspname=$1 AND c.relname=ANY($2::text[]) AND a.attnum<0 ORDER BY c.relname,a.attnum`, e.b.Schema, rbCatalogTables)
	if err != nil {
		return err
	}
	defer systems.Close()
	seen := 0
	systemNames := map[int]string{-1: "ctid", -2: "xmin", -3: "cmin", -4: "xmax", -5: "cmax", -6: "tableoid"}
	for systems.Next() {
		var table, name string
		var num int
		if err = systems.Scan(&table, &num, &name); err != nil {
			return err
		}
		if systemNames[num] != name {
			return rbCatalogFail("columns", table+" system attribute "+name)
		}
		seen++
	}
	if err = systems.Err(); err != nil {
		return err
	}
	if seen != 30 {
		return rbCatalogFail("columns", fmt.Sprintf("system attribute count %d", seen))
	}
	// External permissions are deliberately narrow, conservative controls.
	var pb, option bool
	if err = db.QueryRow(ctx, `SELECT pg_catalog.has_function_privilege($1::oid,($2||'.pb_require_read_committed()')::regprocedure,'EXECUTE'),pg_catalog.has_function_privilege($1::oid,($2||'.pb_require_read_committed()')::regprocedure,'EXECUTE WITH GRANT OPTION')`, e.b.WriterOID, e.b.Schema).Scan(&pb, &option); err != nil || !pb || option {
		return rbCatalogFail("effective", "W original pb_require_read_committed")
	}
	var bad int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace CROSS JOIN (VALUES ('SELECT'),('INSERT'),('UPDATE'),('REFERENCES')) v(privilege)
 WHERE n.nspname=$1 AND c.relname='periodic_budget_allocations' AND (pg_catalog.has_column_privilege($2::oid,c.oid,a.attnum,v.privilege) IS DISTINCT FROM (v.privilege='SELECT' AND a.attname IN ('subject_type','subject_id')) OR pg_catalog.has_column_privilege($2::oid,c.oid,a.attnum,v.privilege||' WITH GRANT OPTION'))`, e.b.Schema, e.b.WriterOID).Scan(&bad); err != nil || bad != 0 {
		return rbCatalogFail("effective", "W allocation conservative column surface")
	}
	var tableBypass bool
	if err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace CROSS JOIN (VALUES ('SELECT'),('INSERT'),('UPDATE'),('DELETE'),('TRUNCATE'),('REFERENCES'),('TRIGGER')) v(privilege) WHERE n.nspname=$1 AND c.relname='periodic_budget_allocations' AND (pg_catalog.has_table_privilege($2::oid,c.oid,v.privilege) OR pg_catalog.has_table_privilege($2::oid,c.oid,v.privilege||' WITH GRANT OPTION')))`, e.b.Schema, e.b.WriterOID).Scan(&tableBypass); err != nil || tableBypass {
		return rbCatalogFail("effective", "W allocation table/public privilege bypass")
	}
	return nil
}

func rbCatalogDifference(group, actual, expected string) error {
	var got, want []map[string]any
	if json.Unmarshal([]byte(actual), &got) != nil || json.Unmarshal([]byte(expected), &want) != nil {
		return rbCatalogFail(group, "canonical comparison decode")
	}
	id := func(v map[string]any) string {
		var parts []string
		for _, key := range []string{"table_name", "name", "attname"} {
			if value, ok := v[key].(string); ok {
				parts = append(parts, value)
			}
		}
		return strings.Join(parts, "/")
	}
	for i := 0; i < len(got) || i < len(want); i++ {
		if i >= len(got) {
			return rbCatalogFail(group, "missing "+id(want[i]))
		}
		if i >= len(want) {
			return rbCatalogFail(group, "extra "+id(got[i]))
		}
		if !reflect.DeepEqual(got[i], want[i]) {
			var keys []string
			for k := range want[i] {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if !reflect.DeepEqual(got[i][k], want[i][k]) {
					return rbCatalogFail(group, id(got[i])+" expected "+id(want[i])+" attribute "+k)
				}
			}
			return rbCatalogFail(group, id(got[i])+" metadata")
		}
	}
	return rbCatalogFail(group, "canonical encoding differs")
}

func rbCatalogInspect(ctx context.Context, db rbCatalogDB, e *rbCatalogExpected) error {
	if err := rbCatalogCheckRoles(ctx, db, e); err != nil {
		return err
	}
	if err := rbCatalogCheckFunctions(ctx, db, e); err != nil {
		return err
	}
	var domain []string
	rows, err := db.Query(ctx, `SELECT c.relname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND left(c.relname,3)='pp_' ORDER BY c.relname`, e.b.Schema)
	if err != nil {
		return rbCatalogFail("domain", "read: "+err.Error())
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		domain = append(domain, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	wantDomain := []string{"pp_operation_roots", "pp_request_heads", "pp_request_origins", "pp_root_steps", "pp_s_request_birth_pk", "pp_s_request_head_pk", "pp_s_root_pk", "pp_s_root_xid_uq", "pp_s_step_pk"}
	sort.Strings(wantDomain)
	if !reflect.DeepEqual(domain, wantDomain) {
		return rbCatalogFail("domain", fmt.Sprintf("extra/missing pp relation or index %v", domain))
	}
	for _, group := range []string{"relations", "columns", "constraints", "indexes"} {
		value, err := rbCatalogJSON(ctx, db, rbCatalogQueries[group], e.b.Schema)
		if err != nil {
			return rbCatalogFail(group, "query: "+err.Error())
		}
		if value != e.groups[group] {
			return rbCatalogDifference(group, value, e.groups[group])
		}
	}
	if err = rbCatalogCheckTriggers(ctx, db, e); err != nil {
		return err
	}
	if err = rbCatalogCheckACL(ctx, db, e); err != nil {
		return err
	}
	if err = rbCatalogCheckEffective(ctx, db, e); err != nil {
		return err
	}
	ids, err := rbCatalogIdentities(ctx, db, e.b.Schema)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(ids, e.identities) {
		return rbCatalogFail("identity", "object OID/name binding changed")
	}
	return nil
}

func TestPostgresA1CatalogExactObjectsAndAllGrantees(t *testing.T) {
	f := newFixture(t)
	e := rbCatalogExpectedNew(t, f)
	must(t, "read-only full installed catalog attestation", rbCatalogInspect(f.ctx, f.owner, e))
	r := f.reserve(t)
	f.checkBirth(t, r.RequestID)
	must(t, "actual APP Reserve retained exact installed objects", rbCatalogInspect(f.ctx, f.owner, e))
	// OID/name binding failures are Go inspection input faults, not SQL edits.
	for _, name := range []string{"swapped_oid", "wrong_name"} {
		t.Run(name, func(t *testing.T) {
			altered := *e
			if name == "swapped_oid" {
				altered.b.AppOID, altered.b.WriterOID = altered.b.WriterOID, altered.b.AppOID
			} else {
				altered.b.App = "rb_unknown_app"
			}
			err := rbCatalogInspect(f.ctx, f.owner, &altered)
			failure, ok := err.(*rbCatalogFailure)
			if !ok || failure.group != "identity" {
				t.Fatalf("binding fault must reject identity: %v", err)
			}
		})
	}
}

type rbCatalogFault struct{ name, group, sql string }

func rbCatalogFaults(t *testing.T, f *fixture) []rbCatalogFault {
	t.Helper()
	q := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	table := func(s string) string { return pgx.Identifier{f.b.Schema, s}.Sanitize() }
	fn := func(s string) string { return table(s) + "()" }
	app, w := q(f.b.App), q(f.b.Writer)
	peer := q(strings.Replace(f.b.App, "rb_app_", "rb_peer_", 1))
	proj := q(strings.Replace(f.b.App, "rb_app_", "rb_proj_", 1))
	extra := q(strings.Replace(f.b.App, "rb_app_", "rb_extra_", 1))
	origin, head, root, step, request := table("pp_request_origins"), table("pp_request_heads"), table("pp_operation_roots"), table("pp_root_steps"), table("request_records")
	var out []rbCatalogFault
	add := func(name, group, sql string) { out = append(out, rbCatalogFault{name, group, sql}) }
	add("function_owner", "function", "ALTER FUNCTION "+fn("pp_rb_request_write_v1")+" OWNER TO CURRENT_USER")
	add("function_definer", "function", "ALTER FUNCTION "+fn("pp_rb_request_write_v1")+" SECURITY DEFINER")
	add("capture_invoker", "function", "ALTER FUNCTION "+fn("pp_rb_capture_request_v1")+" SECURITY INVOKER")
	add("function_search_path", "function", "ALTER FUNCTION "+fn("pp_rb_request_write_v1")+" SET search_path=pg_catalog")
	add("function_extra_config", "function", "ALTER FUNCTION "+fn("pp_rb_request_write_v1")+" SET statement_timeout='2s'")
	add("function_stability", "function", "ALTER FUNCTION "+fn("pp_rb_request_write_v1")+" STABLE")
	add("function_parallel", "function", "ALTER FUNCTION "+fn("pp_rb_request_write_v1")+" PARALLEL SAFE")
	add("function_strict", "function", "ALTER FUNCTION "+fn("pp_rb_request_write_v1")+" STRICT")
	add("function_body_stub", "function", "CREATE OR REPLACE FUNCTION "+fn("pp_rb_request_write_v1")+" RETURNS trigger LANGUAGE plpgsql AS $fault$ BEGIN RETURN NEW; END $fault$")
	add("function_overload", "function", "CREATE FUNCTION "+table("pp_rb_request_write_v1")+"(text) RETURNS void LANGUAGE plpgsql AS $fault$ BEGIN RETURN; END $fault$")
	add("extra_pp_function", "function", "CREATE FUNCTION "+table("pp_unexpected")+"() RETURNS void LANGUAGE plpgsql AS $fault$ BEGIN RETURN; END $fault$")
	add("function_name", "function", "ALTER FUNCTION "+fn("pp_rb_request_write_v1")+" RENAME TO pp_rb_wrong_name")
	add("function_missing", "function", "DROP FUNCTION "+fn("pp_rb_request_write_v1")+" CASCADE")
	bodies, err := rbCatalogBodies(f.b)
	must(t, "render independently pinned body for same-name OID fault", err)
	if strings.Contains(bodies["pp_rb_validate_root_v1"], "$rb_catalog_oid$") {
		t.Fatal("OID fault delimiter occurs in frozen body")
	}
	add("function_same_name_new_oid", "identity", "DROP FUNCTION "+table("pp_rb_validate_root_v1")+"(text); CREATE FUNCTION "+table("pp_rb_validate_root_v1")+"(p_root text) RETURNS void LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE SECURITY INVOKER SET search_path="+q(f.b.Schema)+",pg_catalog,pg_temp AS $rb_catalog_oid$"+bodies["pp_rb_validate_root_v1"]+"$rb_catalog_oid$; GRANT CREATE ON SCHEMA "+q(f.b.Schema)+" TO "+w+"; ALTER FUNCTION "+table("pp_rb_validate_root_v1")+"(text) OWNER TO "+w+"; REVOKE CREATE ON SCHEMA "+q(f.b.Schema)+" FROM "+w+"; REVOKE ALL ON FUNCTION "+table("pp_rb_validate_root_v1")+"(text) FROM PUBLIC; GRANT EXECUTE ON FUNCTION "+table("pp_rb_validate_root_v1")+"(text) TO "+app)
	add("role_inherit", "role", "ALTER ROLE "+app+" INHERIT")
	add("role_login", "role", "ALTER ROLE "+w+" LOGIN")
	add("peer_login", "role", "ALTER ROLE "+peer+" NOLOGIN")
	add("role_membership", "role", "GRANT "+peer+" TO "+app)
	add("role_reverse_membership", "role", "GRANT "+w+" TO "+peer)
	add("schema_owner", "schema", "ALTER SCHEMA "+q(f.b.Schema)+" OWNER TO "+app)
	add("schema_create", "acl", "GRANT CREATE ON SCHEMA "+q(f.b.Schema)+" TO "+w)
	add("schema_public", "acl", "GRANT USAGE ON SCHEMA "+q(f.b.Schema)+" TO PUBLIC")
	add("table_owner", "relations", "ALTER TABLE "+head+" OWNER TO "+app)
	add("table_rls", "relations", "ALTER TABLE "+head+" ENABLE ROW LEVEL SECURITY")
	add("column_type", "columns", "ALTER TABLE "+origin+" ALTER COLUMN client_ip TYPE varchar(200)")
	add("column_notnull", "columns", "ALTER TABLE "+head+" ALTER COLUMN ordinal DROP NOT NULL")
	add("column_default", "columns", "ALTER TABLE "+request+" ALTER COLUMN pp_last_decision_id SET DEFAULT 'fault'")
	add("column_generated", "columns", "ALTER TABLE "+origin+" ALTER COLUMN step_kind DROP EXPRESSION")
	add("column_expression", "columns", "ALTER TABLE "+step+" DROP COLUMN d_reconciliation; ALTER TABLE "+step+" ADD COLUMN d_reconciliation text GENERATED ALWAYS AS ('wrong'::text) STORED")
	add("column_extra", "columns", "ALTER TABLE "+head+" ADD COLUMN drift text")
	add("pp_table_extra", "domain", "CREATE TABLE "+table("pp_unexpected")+"(id text)")
	add("pp_view_extra", "domain", "CREATE VIEW "+table("pp_unexpected")+" AS SELECT 1 AS id")
	add("index_extra", "indexes", "CREATE INDEX rb_unexpected_idx ON "+origin+"(policy_version)")
	add("index_pp_extra", "domain", "CREATE INDEX pp_unexpected_idx ON "+origin+"(policy_version)")
	add("index_partial", "indexes", "CREATE INDEX rb_partial_idx ON "+head+"(request_id) WHERE ordinal=0")
	add("index_include", "indexes", "CREATE INDEX rb_include_idx ON "+head+"(request_id) INCLUDE(ordinal)")
	add("index_hash", "indexes", "CREATE INDEX rb_hash_idx ON "+head+" USING hash(request_id)")
	add("index_original_missing", "indexes", "DROP INDEX "+table("requests_tenant_time"))
	add("wide_payload_unique", "constraints", "ALTER TABLE "+origin+" ADD CONSTRAINT rb_payload_uq UNIQUE(request_id,policy_version)")
	// Dropping a key also removes its pp_ index, so domain detects the missing
	// exact index before the relation-constraint comparison.
	add("key_missing", "domain", "ALTER TABLE "+head+" DROP CONSTRAINT pp_s_request_head_pk CASCADE")
	add("check_weakened", "constraints", "ALTER TABLE "+root+" DROP CONSTRAINT pp_s_root_template_ck; ALTER TABLE "+root+" ADD CONSTRAINT pp_s_root_template_ck CHECK(true)")
	add("check_not_valid", "constraints", "ALTER TABLE "+root+" DROP CONSTRAINT pp_s_root_scalar_ck; ALTER TABLE "+root+" ADD CONSTRAINT pp_s_root_scalar_ck CHECK(step_count>0) NOT VALID")
	add("fk_missing", "constraints", "ALTER TABLE "+origin+" DROP CONSTRAINT pp_bx_request_step_locator_fk")
	add("fk_not_valid", "constraints", "ALTER TABLE "+origin+" DROP CONSTRAINT pp_bx_request_step_locator_fk; ALTER TABLE "+origin+" ADD CONSTRAINT pp_bx_request_step_locator_fk FOREIGN KEY(root_id,step_no) REFERENCES "+step+"(root_id,step_no) DEFERRABLE INITIALLY DEFERRED NOT VALID")
	add("fk_immediate", "constraints", "ALTER TABLE "+origin+" ALTER CONSTRAINT pp_bx_request_step_locator_fk NOT DEFERRABLE")
	add("fk_initially_immediate", "constraints", "ALTER TABLE "+origin+" ALTER CONSTRAINT pp_bx_request_step_locator_fk DEFERRABLE INITIALLY IMMEDIATE")
	add("fk_action", "constraints", "ALTER TABLE "+origin+" DROP CONSTRAINT pp_bx_request_step_locator_fk; ALTER TABLE "+origin+" ADD CONSTRAINT pp_bx_request_step_locator_fk FOREIGN KEY(root_id,step_no) REFERENCES "+step+"(root_id,step_no) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED")
	add("fk_target", "constraints", "ALTER TABLE "+root+" DROP CONSTRAINT pp_bx_root_request_origin_fk; ALTER TABLE "+root+" ADD CONSTRAINT pp_bx_root_request_origin_fk FOREIGN KEY(request_id) REFERENCES "+head+"(request_id) DEFERRABLE INITIALLY DEFERRED")
	add("fk_column_order", "constraints", "ALTER TABLE "+request+" ADD CONSTRAINT rb_fk_order_uq UNIQUE(id,tenant_id); ALTER TABLE "+root+" DROP CONSTRAINT pp_s_root_request_fk; ALTER TABLE "+root+" ADD CONSTRAINT pp_s_root_request_fk FOREIGN KEY(request_id,tenant_id) REFERENCES "+request+"(id,tenant_id) DEFERRABLE INITIALLY DEFERRED")
	add("trigger_missing", "trigger", "DROP TRIGGER aa_pp_rb_origin_write ON "+origin)
	for _, enabled := range []struct{ name, clause string }{{"disabled", "DISABLE TRIGGER"}, {"replica", "ENABLE REPLICA TRIGGER"}, {"always", "ENABLE ALWAYS TRIGGER"}} {
		add("trigger_"+enabled.name, "trigger", "ALTER TABLE "+origin+" "+enabled.clause+" aa_pp_rb_origin_write")
	}
	add("trigger_function", "trigger", "DROP TRIGGER aa_pp_rb_origin_write ON "+origin+"; CREATE TRIGGER aa_pp_rb_origin_write BEFORE INSERT OR UPDATE OR DELETE ON "+origin+" FOR EACH ROW EXECUTE FUNCTION "+fn("pp_rb_shared_write_v1"))
	add("trigger_event", "trigger", "DROP TRIGGER aa_pp_rb_origin_write ON "+origin+"; CREATE TRIGGER aa_pp_rb_origin_write BEFORE INSERT ON "+origin+" FOR EACH ROW EXECUTE FUNCTION "+fn("pp_rb_source_write_v1"))
	add("trigger_column", "trigger", "DROP TRIGGER aa_pp_rb_origin_write ON "+origin+"; CREATE TRIGGER aa_pp_rb_origin_write BEFORE UPDATE OF request_id ON "+origin+" FOR EACH ROW EXECUTE FUNCTION "+fn("pp_rb_source_write_v1"))
	add("trigger_when", "trigger", "DROP TRIGGER aa_pp_rb_origin_write ON "+origin+"; CREATE TRIGGER aa_pp_rb_origin_write BEFORE INSERT OR UPDATE ON "+origin+" FOR EACH ROW WHEN (NEW.request_id IS NOT NULL) EXECUTE FUNCTION "+fn("pp_rb_source_write_v1"))
	add("trigger_deferred", "trigger", "DROP TRIGGER zz_pp_rb_origin_complete ON "+origin+"; CREATE CONSTRAINT TRIGGER zz_pp_rb_origin_complete AFTER INSERT ON "+origin+" NOT DEFERRABLE FOR EACH ROW EXECUTE FUNCTION "+fn("pp_rb_deferred_v1"))
	add("trigger_extra", "trigger", "CREATE TRIGGER ab_extra_new_mutator BEFORE INSERT ON "+origin+" FOR EACH ROW EXECUTE FUNCTION "+fn("pp_rb_source_write_v1"))
	add("app_table_select", "acl", "GRANT SELECT ON "+request+" TO "+app)
	add("writer_table_update", "acl", "GRANT UPDATE ON "+root+" TO "+w)
	add("app_extra_column", "acl", "GRANT UPDATE(policy_version) ON "+request+" TO "+app)
	add("writer_root_update", "acl", "GRANT UPDATE(id) ON "+root+" TO "+w)
	add("system_ctid", "acl", "GRANT SELECT(ctid) ON "+request+" TO "+app)
	add("system_xmax", "acl", "GRANT SELECT(xmax) ON "+origin+" TO "+w)
	add("system_xmin_option", "acl", "GRANT SELECT(xmin) ON "+request+" TO "+app+" WITH GRANT OPTION")
	add("public_column", "acl", "GRANT SELECT(id) ON "+request+" TO PUBLIC")
	add("peer_column", "acl", "GRANT SELECT(id) ON "+request+" TO "+peer)
	add("projector_column", "acl", "GRANT SELECT(request_id) ON "+head+" TO "+proj)
	add("unknown_grantee", "acl", "CREATE ROLE "+extra+" NOLOGIN; GRANT SELECT(request_id) ON "+head+" TO "+extra)
	add("ordinary_grant_option", "acl", "GRANT SELECT(request_id) ON "+head+" TO "+app+" WITH GRANT OPTION")
	add("references_grant", "acl", "GRANT REFERENCES(request_id) ON "+head+" TO "+app)
	add("private_execute", "acl", "GRANT EXECUTE ON FUNCTION "+fn("pp_rb_request_write_v1")+" TO "+app)
	add("public_execute", "acl", "GRANT EXECUTE ON FUNCTION "+fn("pp_rb_deferred_v1")+" TO PUBLIC")
	add("peer_execute", "acl", "GRANT EXECUTE ON FUNCTION "+table("pp_rb_validate_request_v1")+"(text) TO "+peer)
	add("function_grant_option", "acl", "GRANT EXECUTE ON FUNCTION "+table("pp_rb_validate_request_v1")+"(text) TO "+app+" WITH GRANT OPTION")
	add("allocation_extra_column", "acl", "GRANT SELECT(id) ON "+table("periodic_budget_allocations")+" TO "+w)
	add("allocation_conservative_missing", "acl", "REVOKE SELECT(subject_id) ON "+table("periodic_budget_allocations")+" FROM "+w)
	return out
}

func TestPostgresA1CatalogRejectsTransactionalDrift(t *testing.T) {
	f := newFixture(t)
	e := rbCatalogExpectedNew(t, f)
	must(t, "catalog positive control before drift", rbCatalogInspect(f.ctx, f.owner, e))
	r := f.reserve(t)
	f.checkBirth(t, r.RequestID)
	for _, fault := range rbCatalogFaults(t, f) {
		t.Run(fault.name, func(t *testing.T) {
			tx, err := f.owner.Begin(f.ctx)
			must(t, "begin owner catalog fault", err)
			defer tx.Rollback(f.ctx)
			_, err = tx.Exec(f.ctx, fault.sql)
			must(t, "apply owner catalog fault "+fault.name, err)
			// The inspector reads the very transaction that owns uncommitted DDL.
			// A pool or second connection here would silently miss these faults.
			err = rbCatalogInspect(f.ctx, tx, e)
			failure, ok := err.(*rbCatalogFailure)
			if !ok || failure.group != fault.group || failure.object == "" {
				t.Fatalf("%s must reject %s with object context: %v", fault.name, fault.group, err)
			}
			must(t, "rollback catalog drift", tx.Rollback(f.ctx))
			must(t, "fully recertify rollback, no automatic refresh", rbCatalogInspect(f.ctx, f.owner, e))
		})
	}
	r = f.reserve(t)
	f.checkBirth(t, r.RequestID)
	must(t, "APP Reserve after all restored metadata", rbCatalogInspect(f.ctx, f.owner, e))
}
