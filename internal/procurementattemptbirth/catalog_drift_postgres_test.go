package procurementattemptbirth_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/procurementattemptbirth"
)

// This is an attack inventory for the test-only catalog inspector. It does not
// install an alternative writer, commit drift, refresh expectations, provide a
// production Check, or issue a Qualified/profile value. Every mutation belongs
// to one owner transaction and must disappear before the next positive check.
type abCatalogDriftFault struct {
	name  string
	group string
	sql   string
	probe string
}

func abCatalogDriftFaults(t *testing.T, b procurementattemptbirth.Bindings) []abCatalogDriftFault {
	t.Helper()
	q := func(v string) string { return pgx.Identifier{v}.Sanitize() }
	literal := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }
	table := func(v string) string { return pgx.Identifier{b.Schema, v}.Sanitize() }
	fn := func(v string) string { return table(v) + "()" }
	app, writer := q(b.App), q(b.Writer)
	peer := q(strings.Replace(b.App, "ab_app_", "ab_peer_", 1))
	extra := q(strings.Replace(b.App, "ab_app_", "ab_extra_", 1))
	root, step, origin, head, request := table("pp_operation_roots"), table("pp_root_steps"), table("pp_request_origins"), table("pp_request_heads"), table("request_records")
	attemptOrigin := table("pp_attempt_origins")
	var out []abCatalogDriftFault
	add := func(name, group, sql string) {
		out = append(out, abCatalogDriftFault{name: name, group: group, sql: sql})
	}
	// Relation metadata is compared before ACLs. Constraints/indexes may detect
	// an affected object before its table because inspector query order is free.
	add("table_owner_app", "metadata", "ALTER TABLE "+head+" OWNER TO "+app)
	add("table_owner_writer", "metadata", "ALTER TABLE "+head+" OWNER TO "+writer)
	add("table_rls", "metadata", "ALTER TABLE "+head+" ENABLE ROW LEVEL SECURITY")
	add("parent_rls", "metadata", "ALTER TABLE "+table("api_keys")+" ENABLE ROW LEVEL SECURITY")
	add("table_force_rls", "metadata", "ALTER TABLE "+head+" FORCE ROW LEVEL SECURITY")
	add("table_replica_identity", "metadata", "ALTER TABLE "+head+" REPLICA IDENTITY FULL")
	add("column_type", "metadata", "ALTER TABLE "+origin+" ALTER COLUMN client_ip TYPE varchar(200)")
	add("column_not_null", "metadata", "ALTER TABLE "+head+" ALTER COLUMN ordinal DROP NOT NULL")
	add("column_default_original_pointer", "metadata", "ALTER TABLE "+request+" ALTER COLUMN pp_last_decision_id SET DEFAULT 'catalog-drift'")
	add("column_default_new", "metadata", "ALTER TABLE "+head+" ALTER COLUMN ordinal SET DEFAULT 0")
	add("column_generated_removed", "metadata", "ALTER TABLE "+origin+" ALTER COLUMN step_kind DROP EXPRESSION")
	add("column_generated_expression", "metadata", "ALTER TABLE "+step+" DROP COLUMN d_reconciliation; ALTER TABLE "+step+" ADD COLUMN d_reconciliation text GENERATED ALWAYS AS ('wrong'::text) STORED")
	add("column_extra", "metadata", "ALTER TABLE "+head+" ADD COLUMN catalog_drift text")
	add("candidate_table_extra", "domain", "CREATE TABLE "+table("pp_catalog_drift")+"(id text)")
	add("candidate_view_extra", "domain", "CREATE VIEW "+table("pp_catalog_drift")+" AS SELECT 1 AS id")
	add("index_extra", "metadata", "CREATE INDEX ab_catalog_drift_idx ON "+origin+"(policy_version)")
	add("index_partial", "metadata", "CREATE INDEX ab_catalog_drift_idx ON "+head+"(request_id) WHERE ordinal=0")
	add("index_include", "metadata", "CREATE INDEX ab_catalog_drift_idx ON "+head+"(request_id) INCLUDE(ordinal)")
	add("index_hash", "metadata", "CREATE INDEX ab_catalog_drift_idx ON "+head+" USING hash(request_id)")
	add("original_index_missing", "metadata", "DROP INDEX "+table("requests_tenant_time"))
	add("payload_wide_unique", "metadata", "ALTER TABLE "+origin+" ADD CONSTRAINT ab_catalog_payload_uq UNIQUE(request_id,policy_version)")
	add("key_missing", "metadata", "ALTER TABLE "+head+" DROP CONSTRAINT pp_s_request_head_pk CASCADE")
	add("check_weakened", "metadata", "ALTER TABLE "+root+" DROP CONSTRAINT pp_s_root_template_ck; ALTER TABLE "+root+" ADD CONSTRAINT pp_s_root_template_ck CHECK(true)")
	add("check_not_valid", "metadata", "ALTER TABLE "+root+" DROP CONSTRAINT pp_s_root_scalar_ck; ALTER TABLE "+root+" ADD CONSTRAINT pp_s_root_scalar_ck CHECK(step_count>0) NOT VALID")
	add("fk_missing", "metadata", "ALTER TABLE "+origin+" DROP CONSTRAINT pp_bx_request_step_locator_fk")
	add("fk_not_valid", "metadata", "ALTER TABLE "+origin+" DROP CONSTRAINT pp_bx_request_step_locator_fk; ALTER TABLE "+origin+" ADD CONSTRAINT pp_bx_request_step_locator_fk FOREIGN KEY(root_id,step_no) REFERENCES "+step+"(root_id,step_no) DEFERRABLE INITIALLY DEFERRED NOT VALID")
	add("fk_not_deferrable", "metadata", "ALTER TABLE "+origin+" ALTER CONSTRAINT pp_bx_request_step_locator_fk NOT DEFERRABLE")
	add("fk_initially_immediate", "metadata", "ALTER TABLE "+origin+" ALTER CONSTRAINT pp_bx_request_step_locator_fk DEFERRABLE INITIALLY IMMEDIATE")
	add("fk_delete_action", "metadata", "ALTER TABLE "+origin+" DROP CONSTRAINT pp_bx_request_step_locator_fk; ALTER TABLE "+origin+" ADD CONSTRAINT pp_bx_request_step_locator_fk FOREIGN KEY(root_id,step_no) REFERENCES "+step+"(root_id,step_no) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED")
	add("fk_target", "metadata", "ALTER TABLE "+root+" DROP CONSTRAINT pp_bx_root_request_origin_fk; ALTER TABLE "+root+" ADD CONSTRAINT pp_bx_root_request_origin_fk FOREIGN KEY(request_id) REFERENCES "+head+"(request_id) DEFERRABLE INITIALLY DEFERRED")
	add("fk_column_order", "metadata", "ALTER TABLE "+request+" ADD CONSTRAINT ab_catalog_order_uq UNIQUE(id,tenant_id); ALTER TABLE "+root+" DROP CONSTRAINT pp_s_root_request_fk; ALTER TABLE "+root+" ADD CONSTRAINT pp_s_root_request_fk FOREIGN KEY(request_id,tenant_id) REFERENCES "+request+"(id,tenant_id) DEFERRABLE INITIALLY DEFERRED")

	guard := fn("pp_ab_request_write_v1")
	add("function_owner", "function_source", "ALTER FUNCTION "+guard+" OWNER TO CURRENT_USER")
	add("function_private_definer", "function_metadata", "ALTER FUNCTION "+guard+" SECURITY DEFINER")
	add("function_capture_invoker", "function_metadata", "ALTER FUNCTION "+fn("pp_ab_capture_request_v1")+" SECURITY INVOKER")
	add("function_public_root_invoker", "function_metadata", "ALTER FUNCTION "+table("pp_create_account")+"(text,text,text,text,text,bigint) SECURITY INVOKER")
	add("function_search_path", "function_metadata", "ALTER FUNCTION "+guard+" SET search_path=pg_catalog")
	add("function_extra_config", "function_metadata", "ALTER FUNCTION "+guard+" SET statement_timeout='2s'")
	add("function_stability", "function_metadata", "ALTER FUNCTION "+guard+" STABLE")
	add("function_parallel", "function_metadata", "ALTER FUNCTION "+guard+" PARALLEL SAFE")
	add("function_strict", "function_metadata", "ALTER FUNCTION "+guard+" STRICT")
	add("function_leakproof", "function_metadata", "ALTER FUNCTION "+guard+" LEAKPROOF")
	add("function_cost", "function_metadata", "ALTER FUNCTION "+guard+" COST 2")
	bodies, creates, err := abCatalogFunctionSources(b)
	must(t, "render fixed exact source for body-only catalog attacks", err)
	for _, v := range []struct{ name, body string }{{"pp_ab_request_write_v1", "\nBEGIN RETURN NEW; END\n"}, {"pp_ac_uuid_v1", "\n SELECT false;\n"}, {"pp_create_account", "\nBEGIN RETURN; END\n"}} {
		original := creates[v.name]
		if original == "" || bodies[v.name] == "" || strings.Count(original, bodies[v.name]) != 1 {
			t.Fatalf("body-only mutation source missing/ambiguous %s", v.name)
		}
		altered := strings.Replace(original, bodies[v.name], v.body, 1)
		altered = strings.Replace(altered, "CREATE FUNCTION ", "CREATE OR REPLACE FUNCTION ", 1)
		add("function_body_"+v.name, "function_source", altered)
	}
	add("function_overload", "function_metadata", "CREATE FUNCTION "+table("pp_ab_request_write_v1")+"(text) RETURNS void LANGUAGE plpgsql AS $ab_drift$ BEGIN RETURN; END $ab_drift$")
	add("function_renamed", "function_metadata", "ALTER FUNCTION "+guard+" RENAME TO pp_ab_catalog_wrong_name")
	add("function_missing", "function_metadata", "DROP FUNCTION "+guard+" CASCADE")
	// These names are real A1/account-only writer families. Stubs intentionally
	// prove inventory rejection; this never runs or installs their older bodies.
	add("mixed_a1_writer", "mixed_writer", "CREATE FUNCTION "+fn("pp_rb_request_write_v1")+" RETURNS trigger LANGUAGE plpgsql AS $ab_drift$ BEGIN RETURN NEW; END $ab_drift$")
	add("mixed_account_writer", "mixed_writer", "CREATE FUNCTION "+fn("pp_ac_account_write_v1")+" RETURNS trigger LANGUAGE plpgsql AS $ab_drift$ BEGIN RETURN NEW; END $ab_drift$")
	add("dependency_definer", "dependency_function", "ALTER FUNCTION "+table("pp_at_current_quote_v1")+"(text,text) SECURITY DEFINER")
	add("dependency_search_path", "dependency_function", "ALTER FUNCTION "+table("pp_at_current_quote_v1")+"(text,text) SET search_path=public")
	// Preserve original A0 parameter names, types and safety attributes. Only
	// the quote-equality body changes; accepting this stub would erase fencing.
	add("dependency_body", "dependency_function", "CREATE OR REPLACE FUNCTION "+table("pp_at_quote_equal_v1")+`(
 has_expected pg_catalog.bool,expected_version pg_catalog.bytea,expected_input pg_catalog.int8,
 expected_output pg_catalog.int8,expected_basis pg_catalog.bytea,actual_version pg_catalog.text,
 actual_input pg_catalog.int8,actual_output pg_catalog.int8,actual_basis pg_catalog.text)
 RETURNS pg_catalog.bool LANGUAGE sql IMMUTABLE PARALLEL SAFE SECURITY INVOKER
 AS $ab_drift$ SELECT true $ab_drift$`)

	add("trigger_missing", "trigger", "DROP TRIGGER aa_pp_ab_request_origin_write ON "+origin)
	for _, v := range []struct{ name, action string }{{"disabled", "DISABLE TRIGGER"}, {"replica", "ENABLE REPLICA TRIGGER"}, {"always", "ENABLE ALWAYS TRIGGER"}} {
		add("trigger_"+v.name, "trigger", "ALTER TABLE "+origin+" "+v.action+" aa_pp_ab_request_origin_write")
	}
	add("trigger_original_disabled", "trigger", "ALTER TABLE "+request+" DISABLE TRIGGER request_retail_snapshot")
	add("trigger_guard_order", "trigger", "ALTER TRIGGER aa_pp_ab_request_origin_write ON "+origin+" RENAME TO zzz_pp_ab_request_origin_write")
	add("trigger_capture_order", "trigger", "ALTER TRIGGER zz_pp_ab_request_birth ON "+request+" RENAME TO a_pp_ab_request_birth")
	add("trigger_function", "trigger", "DROP TRIGGER aa_pp_ab_request_origin_write ON "+origin+"; CREATE TRIGGER aa_pp_ab_request_origin_write BEFORE INSERT OR UPDATE OR DELETE ON "+origin+" FOR EACH ROW EXECUTE FUNCTION "+fn("pp_ab_shared_write_v1"))
	add("trigger_event", "trigger", "DROP TRIGGER aa_pp_ab_request_origin_write ON "+origin+"; CREATE TRIGGER aa_pp_ab_request_origin_write BEFORE INSERT ON "+origin+" FOR EACH ROW EXECUTE FUNCTION "+fn("pp_ab_source_write_v1"))
	add("trigger_update_columns", "trigger", "DROP TRIGGER aa_pp_ab_request_origin_write ON "+origin+"; CREATE TRIGGER aa_pp_ab_request_origin_write BEFORE UPDATE OF request_id ON "+origin+" FOR EACH ROW EXECUTE FUNCTION "+fn("pp_ab_source_write_v1"))
	add("trigger_when", "trigger", "DROP TRIGGER aa_pp_ab_request_origin_write ON "+origin+"; CREATE TRIGGER aa_pp_ab_request_origin_write BEFORE INSERT OR UPDATE ON "+origin+" FOR EACH ROW WHEN (NEW.request_id IS NOT NULL) EXECUTE FUNCTION "+fn("pp_ab_source_write_v1"))
	add("trigger_not_deferred", "trigger", "DROP TRIGGER zz_pp_ab_request_origin_complete ON "+origin+"; CREATE CONSTRAINT TRIGGER zz_pp_ab_request_origin_complete AFTER INSERT OR UPDATE OR DELETE ON "+origin+" NOT DEFERRABLE FOR EACH ROW EXECUTE FUNCTION "+fn("pp_ab_deferred_v1"))
	add("trigger_initially_immediate", "trigger", "DROP TRIGGER zz_pp_ab_request_origin_complete ON "+origin+"; CREATE CONSTRAINT TRIGGER zz_pp_ab_request_origin_complete AFTER INSERT OR UPDATE OR DELETE ON "+origin+" DEFERRABLE INITIALLY IMMEDIATE FOR EACH ROW EXECUTE FUNCTION "+fn("pp_ab_deferred_v1"))
	add("trigger_extra", "trigger", "CREATE TRIGGER ab_catalog_drift_mutator BEFORE INSERT ON "+origin+" FOR EACH ROW EXECUTE FUNCTION "+fn("pp_ab_source_write_v1"))

	add("app_table_select", "acl", "GRANT SELECT ON TABLE "+request+" TO "+app)
	add("writer_table_update", "acl", "GRANT UPDATE ON TABLE "+root+" TO "+writer)
	add("app_insert_new_source", "acl", "GRANT INSERT(request_id) ON TABLE "+origin+" TO "+app)
	add("writer_update_immutable_payload", "acl", "GRANT UPDATE(policy_version) ON TABLE "+origin+" TO "+writer)
	add("writer_read_credential_secret", "acl", "GRANT SELECT(secret_ciphertext) ON TABLE "+table("channel_credentials")+" TO "+writer)
	add("writer_payment_write", "acl", "GRANT INSERT ON TABLE "+table("payment_orders")+" TO "+writer)
	add("writer_source_read_missing", "acl", "REVOKE SELECT(subject_id) ON TABLE "+table("periodic_budget_allocations")+" FROM "+writer)
	add("writer_lock_column_missing", "acl", "REVOKE UPDATE(created_at) ON TABLE "+table("users")+" FROM "+writer)
	add("app_ordinary_grant_option", "acl", "GRANT SELECT(attempt_id) ON TABLE "+attemptOrigin+" TO "+app+" WITH GRANT OPTION")
	add("app_references", "acl", "GRANT REFERENCES(attempt_id) ON TABLE "+attemptOrigin+" TO "+app)
	add("app_ctid", "acl", "GRANT SELECT(ctid) ON TABLE "+request+" TO "+app)
	add("writer_xmax", "acl", "GRANT SELECT(xmax) ON TABLE "+origin+" TO "+writer)
	add("writer_xmin_missing", "acl", "REVOKE SELECT(xmin) ON TABLE "+table("upstream_attempts")+" FROM "+writer)
	add("app_xmin_grant_option", "acl", "GRANT SELECT(xmin) ON TABLE "+request+" TO "+app+" WITH GRANT OPTION")
	add("public_secret_bypass", "acl", "GRANT SELECT(secret_ciphertext) ON TABLE "+table("channel_credentials")+" TO PUBLIC")
	out[len(out)-1].probe = "SELECT pg_catalog.has_column_privilege(" + literal(b.Writer) + "," + literal(table("channel_credentials")) + ",'secret_ciphertext','SELECT')"
	// APP/W already possess this ordinary SELECT. Only a full grantee check can
	// detect these extra principals; effective APP/W comparisons cannot do so.
	add("public_existing_read_column", "foreign_relation", "GRANT SELECT(attempt_id) ON TABLE "+attemptOrigin+" TO PUBLIC")
	add("peer_existing_read_column", "foreign_relation", "GRANT SELECT(attempt_id) ON TABLE "+attemptOrigin+" TO "+peer)
	add("parent_owner_peer", "metadata", "ALTER TABLE "+table("api_keys")+" OWNER TO "+peer)
	add("fresh_foreign_grantee", "foreign_relation", "CREATE ROLE "+extra+" NOLOGIN; GRANT SELECT(attempt_id) ON TABLE "+attemptOrigin+" TO "+extra)
	add("app_private_execute", "acl", "GRANT EXECUTE ON FUNCTION "+fn("pp_ab_require_writer_v1")+" TO "+app)
	add("public_private_execute", "acl", "GRANT EXECUTE ON FUNCTION "+fn("pp_ab_deferred_v1")+" TO PUBLIC")
	add("app_public_execute_missing", "acl", "REVOKE EXECUTE ON FUNCTION "+table("pp_validate_attempt")+"(text) FROM "+app)
	add("app_function_grant_option", "acl", "GRANT EXECUTE ON FUNCTION "+table("pp_validate_attempt")+"(text) TO "+app+" WITH GRANT OPTION")
	add("peer_existing_read_function", "foreign_function", "GRANT EXECUTE ON FUNCTION "+table("pp_validate_attempt")+"(text) TO "+peer)
	add("dependency_function_owner_peer", "foreign_function", "ALTER FUNCTION "+table("pp_at_current_quote_v1")+"(text,text) OWNER TO "+peer)

	add("owner_global_default_public", "default_acl", "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO PUBLIC")
	add("owner_schema_default_public", "default_acl", "ALTER DEFAULT PRIVILEGES IN SCHEMA "+q(b.Schema)+" GRANT EXECUTE ON FUNCTIONS TO PUBLIC")
	add("writer_global_default_public", "default_acl", "ALTER DEFAULT PRIVILEGES FOR ROLE "+writer+" GRANT EXECUTE ON FUNCTIONS TO PUBLIC")
	add("writer_schema_default_public", "default_acl", "ALTER DEFAULT PRIVILEGES FOR ROLE "+writer+" IN SCHEMA "+q(b.Schema)+" GRANT EXECUTE ON FUNCTIONS TO PUBLIC")
	add("writer_default_foreign_peer", "default_acl", "ALTER DEFAULT PRIVILEGES FOR ROLE "+writer+" IN SCHEMA "+q(b.Schema)+" GRANT EXECUTE ON FUNCTIONS TO "+peer)

	add("app_inherit", "role", "ALTER ROLE "+app+" INHERIT")
	add("app_no_login", "role", "ALTER ROLE "+app+" NOLOGIN")
	add("writer_login", "role", "ALTER ROLE "+writer+" LOGIN")
	add("writer_bypass_rls", "role", "ALTER ROLE "+writer+" BYPASSRLS")
	add("app_membership", "role", "GRANT "+peer+" TO "+app)
	add("writer_reverse_membership", "role", "GRANT "+writer+" TO "+peer)
	add("app_inherited_insert_bypass", "role", "GRANT INSERT(request_id) ON TABLE "+origin+" TO "+peer+"; GRANT "+peer+" TO "+app+" WITH INHERIT TRUE")
	out[len(out)-1].probe = "SELECT pg_catalog.has_column_privilege(" + literal(b.App) + "," + literal(origin) + ",'request_id','INSERT')"
	add("schema_owner_app", "schema", "ALTER SCHEMA "+q(b.Schema)+" OWNER TO "+app)
	add("schema_create_writer", "schema", "GRANT CREATE ON SCHEMA "+q(b.Schema)+" TO "+writer)
	add("schema_create_public", "schema", "GRANT CREATE ON SCHEMA "+q(b.Schema)+" TO PUBLIC")
	add("schema_owner_peer", "schema", "ALTER SCHEMA "+q(b.Schema)+" OWNER TO "+peer)
	add("schema_create_peer", "schema", "GRANT CREATE ON SCHEMA "+q(b.Schema)+" TO "+peer)
	add("schema_usage_grant_option_app", "schema", "GRANT USAGE ON SCHEMA "+q(b.Schema)+" TO "+app+" WITH GRANT OPTION")
	return out
}

// A rejection must identify the intended semantic boundary. SQLSTATE failures,
// missing dependencies, aborted transactions, reference-installation failures,
// timeouts and cancellations are not evidence that drift was recognized.
func abCatalogDriftMatches(err error, group string) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		return false
	}

	// Some inspector error boundaries intentionally format a cause with %v.
	// Reject those diagnostic forms too: they no longer support errors.Is/As.
	message := err.Error()
	if strings.Contains(message, context.Canceled.Error()) ||
		strings.Contains(message, context.DeadlineExceeded.Error()) ||
		strings.Contains(message, "(SQLSTATE ") {
		return false
	}
	prefixes := map[string][]string{
		"metadata":            {"A2 relations drift", "A2 columns drift", "A2 constraints drift", "A2 indexes drift"},
		"domain":              {"A2 relation inventory drift"},
		"function_metadata":   {"A2 function signature/config/safety drift"},
		"function_source":     {"A2 exact function source/authority"},
		"dependency_function": {"A2 dependency function drift"},
		"mixed_writer":        {"A2 old/foreign pp writer mixed inventory"},
		"trigger":             {"A2 trigger inventory/order/enabled/identity drift", "A2 descriptor trigger"},
		"acl":                 {"A2 table ACL ", "A2 column ACL ", "A2 function ACL ", "A2 foreign relation ACL", "A2 foreign function ACL"},
		"foreign_relation":    {"A2 foreign relation ACL"},
		"foreign_function":    {"A2 foreign function ACL"},
		"default_acl":         {"A2 default function ACL"},
		"role":                {"A2 APP/W identity/attributes", "A2 role membership bypass"},
		"schema":              {"A2 schema effective ACL"},
	}
	for _, prefix := range prefixes[group] {
		if strings.HasPrefix(err.Error(), prefix) {
			return true
		}
	}
	return false
}

type abCatalogDriftFixture struct {
	name, group string
	names       []string
}

func abCatalogDriftPartitions() map[string][]abCatalogDriftFixture {
	// These fixed names partition the same attack inventory. Splitting the two
	// large classes bounds each independent fixture without extending its 180s
	// context. Consecutive A/B/C/D parents allow CI's ordinary sorted rotation.
	return map[string][]abCatalogDriftFixture{
		"A": {
			{"metadata_relations_columns", "metadata", []string{
				"table_owner_app", "table_owner_writer", "table_rls", "parent_rls", "table_force_rls", "table_replica_identity",
				"column_type", "column_not_null", "column_default_original_pointer", "column_default_new", "column_generated_removed", "column_generated_expression", "column_extra",
				"index_extra", "parent_owner_peer",
			}},
			{"acl_ordinary_columns", "acl", []string{
				"app_table_select", "writer_table_update", "app_insert_new_source", "writer_update_immutable_payload", "writer_read_credential_secret",
				"writer_payment_write", "writer_source_read_missing", "writer_lock_column_missing", "app_ordinary_grant_option", "app_references",
			}},
			{"function_source", "function_source", nil},
			{"foreign_relation", "foreign_relation", nil},
			{"foreign_function", "foreign_function", nil},
			{"mixed_writer", "mixed_writer", nil},
		},
		"B": {
			{"metadata_indexes_constraints", "metadata", []string{
				"index_partial", "index_include", "index_hash", "original_index_missing", "payload_wide_unique", "key_missing", "check_weakened", "check_not_valid",
				"fk_missing", "fk_not_valid", "fk_not_deferrable", "fk_initially_immediate", "fk_delete_action", "fk_target", "fk_column_order",
			}},
			{"acl_system_columns_functions", "acl", []string{
				"app_ctid", "writer_xmax", "writer_xmin_missing", "app_xmin_grant_option", "public_secret_bypass",
				"app_private_execute", "public_private_execute", "app_public_execute_missing", "app_function_grant_option",
			}},
			{"default_acl", "default_acl", nil},
			{"schema", "schema", nil},
		},
		"C": {
			{"function_metadata", "function_metadata", nil},
			{"dependency_function", "dependency_function", nil},
			{"role", "role", nil},
			{"domain", "domain", nil},
		},
		"D": {{"trigger", "trigger", nil}},
	}
}

func abCatalogDriftFixtureFaults(t *testing.T, spec abCatalogDriftFixture, faults []abCatalogDriftFault) []abCatalogDriftFault {
	t.Helper()
	names := map[string]bool{}
	for _, name := range spec.names {
		if names[name] {
			t.Fatalf("duplicate attack in catalog fixture %s: %s", spec.name, name)
		}
		names[name] = true
	}
	var selected []abCatalogDriftFault
	for _, fault := range faults {
		if fault.group == spec.group && (spec.names == nil || names[fault.name]) {
			selected = append(selected, fault)
		}
	}
	if len(selected) == 0 || (spec.names != nil && len(selected) != len(spec.names)) {
		t.Fatalf("empty/incomplete fixed catalog fixture %s: got=%d specified=%d", spec.name, len(selected), len(spec.names))
	}
	return selected
}

func TestA2CatalogDriftInventory(t *testing.T) {
	b := procurementattemptbirth.Bindings{Schema: "ab_catalog_static", App: "ab_app_catalog_static", Writer: "ab_w_catalog_static", AppOID: 29001, WriterOID: 29002}
	faults := abCatalogDriftFaults(t, b)
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, f := range faults {
		if f.name == "" || f.group == "" || f.sql == "" || seen[f.name] {
			t.Fatalf("catalog drift inventory incomplete/duplicate %s", f.name)
		}
		seen[f.name] = true
		counts[f.group]++
		statements, err := abCatalogStatements(f.sql)
		must(t, "parse fixed drift statement boundaries "+f.name, err)
		if len(statements) == 0 {
			t.Fatal("empty drift")
		}

	}
	want := map[string]int{"metadata": 30, "domain": 2, "function_metadata": 13, "function_source": 4, "dependency_function": 3, "mixed_writer": 2, "trigger": 14, "acl": 19, "foreign_relation": 3, "foreign_function": 2, "default_acl": 5, "role": 7, "schema": 6}
	if len(faults) != 110 || !reflect.DeepEqual(counts, want) {
		t.Fatalf("catalog drift coverage changed total=%d groups=%v", len(faults), counts)
	}
	partitions := abCatalogDriftPartitions()
	assigned, fixtures := map[string]bool{}, map[string]bool{}
	splitCounts := map[string]int{"metadata_relations_columns": 15, "metadata_indexes_constraints": 15, "acl_ordinary_columns": 10, "acl_system_columns_functions": 9}
	if len(partitions) != 4 {
		t.Fatal("catalog drift must have four complete top-level partitions")
	}
	for _, part := range []string{"A", "B", "C", "D"} {
		if len(partitions[part]) == 0 {
			t.Fatalf("empty catalog drift partition %s", part)
		}
		for _, spec := range partitions[part] {
			if fixtures[spec.name] {
				t.Fatalf("duplicate catalog drift fixture %s", spec.name)
			}
			fixtures[spec.name] = true
			selected := abCatalogDriftFixtureFaults(t, spec, faults)
			if expected, split := splitCounts[spec.name]; split && len(selected) != expected {
				t.Fatalf("fixed catalog split changed %s: got=%d want=%d", spec.name, len(selected), expected)
			}
			for _, fault := range selected {
				if assigned[fault.name] {
					t.Fatalf("catalog attack occurs in multiple partitions: %s", fault.name)
				}
				assigned[fault.name] = true
			}
		}
	}
	if len(fixtures) != 15 || !reflect.DeepEqual(assigned, seen) {
		t.Fatalf("catalog drift partitions not mutually exclusive and exhaustive: fixtures=%d assigned=%d inventory=%d", len(fixtures), len(assigned), len(seen))
	}
	for _, sample := range []struct {
		err   error
		group string
	}{
		{nil, "metadata"}, {context.Canceled, "metadata"}, {context.DeadlineExceeded, "metadata"},
		{&pgconn.PgError{Code: "42501", Message: "not semantic drift"}, "metadata"},
		{fmt.Errorf("A2 independent reference: failed"), "metadata"},
		{fmt.Errorf("A2 APP/W identity/attributes: %v", context.Canceled), "role"},
		{fmt.Errorf("A2 APP/W identity/attributes: %v", context.DeadlineExceeded), "role"},
		{fmt.Errorf("A2 function ACL: %v", &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}), "acl"},
		{fmt.Errorf("A2 dependency function drift: %v", &pgconn.PgError{Code: "25P02", Message: "current transaction is aborted"}), "dependency_function"},
	} {
		if abCatalogDriftMatches(sample.err, sample.group) {
			t.Fatalf("unrelated failure accepted as %s catalog rejection: %v", sample.group, sample.err)
		}
	}

	t.Logf("fixed catalog drift attacks: %d in %d fixtures/four top-level partitions; each requires in-transaction rejection and full acceptance after rollback", len(faults), len(fixtures))
}

func abCatalogRunDriftPartition(t *testing.T, part string) {
	t.Helper()
	groups := abCatalogDriftPartitions()[part]
	if len(groups) == 0 {
		t.Fatalf("empty catalog drift partition %s", part)
	}
	for _, spec := range groups {
		t.Run(spec.name, func(t *testing.T) {
			f := newFixture(t)
			must(t, "catalog positive control before transactional drift", abCatalogVerify(f.ctx, f.owner, f.b))
			baseline := f.snapshot(t)
			count := 0
			for _, fault := range abCatalogDriftFixtureFaults(t, spec, abCatalogDriftFaults(t, f.b)) {
				count++
				t.Run(fault.name, func(t *testing.T) {
					tx, err := f.owner.Begin(f.ctx)
					must(t, "begin uncommitted owner catalog attack", err)
					defer func() {
						cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						_ = tx.Rollback(cleanup)
					}()
					_, err = tx.Exec(f.ctx, fault.sql)
					must(t, "apply catalog attack "+fault.name, err)
					if fault.probe != "" {
						var effective bool
						must(t, "prove effective privilege bypass before checker "+fault.name, tx.QueryRow(f.ctx, fault.probe).Scan(&effective))
						if !effective {
							t.Fatal("catalog bypass mutation did not become effective")
						}
					}
					// The same transaction owns uncommitted DDL and supplies the inspection
					// handle. A pool would miss precisely the drift under test.
					err = abCatalogVerify(f.ctx, tx, f.b)
					matches := abCatalogDriftMatches(err, fault.group)
					if fault.name == "dependency_function_owner_peer" {
						// Full dependency metadata can reject the fixed-owner breach before
						// the later foreign-grantee ACL pass observes the same bad owner.
						matches = matches || abCatalogDriftMatches(err, "dependency_function")
					}
					if !matches {
						t.Fatalf("%s must reject semantic %s drift; got %v", fault.name, fault.group, err)
					}
					must(t, "rollback owner catalog attack", tx.Rollback(f.ctx))
					must(t, "accept complete restored catalog without refresh", abCatalogVerify(f.ctx, f.owner, f.b))
				})
			}
			if count == 0 {
				t.Fatalf("empty catalog drift group %s", spec.name)
			}
			if f.snapshot(t) != baseline {
				t.Fatal("catalog/reference attack group changed business rows")
			}
			// Every group's restored catalog is usable by the real APP roots.
			created := f.create(t, 1000)
			f.bind(t, created.TargetID)
			request := f.reserve(t)
			attempt, err := f.attempt(request.RequestID)
			must(t, "actual APP managed birth after all catalog rollback controls", err)
			if created.Status != "applied" || attempt.Outcome != "applied" || attempt.Root == nil || attempt.Source == nil {
				t.Fatal("post-drift actual APP birth receipt incomplete")
			}
			_, err = f.app.Exec(f.ctx, "SELECT pp_validate_request($1),pp_validate_attempt($2),pp_validate_account($3),pp_validate_root($4)", request.RequestID, attempt.Attempt, created.TargetID, *attempt.Root)
			must(t, "APP complete prefix after all catalog drift restored", err)
			must(t, "final catalog remains accepted after actual APP business", abCatalogVerify(f.ctx, f.owner, f.b))
		})
	}
}

func TestPostgresA2CatalogRejectsTransactionalDriftA(t *testing.T) {
	abCatalogRunDriftPartition(t, "A")
}
func TestPostgresA2CatalogRejectsTransactionalDriftB(t *testing.T) {
	abCatalogRunDriftPartition(t, "B")
}
func TestPostgresA2CatalogRejectsTransactionalDriftC(t *testing.T) {
	abCatalogRunDriftPartition(t, "C")
}
func TestPostgresA2CatalogRejectsTransactionalDriftD(t *testing.T) {
	abCatalogRunDriftPartition(t, "D")
}
