// Package periodicprefix supports the isolated B-P2 PostgreSQL candidate tests.
// Its checks do not issue catalog, runtime, maintenance, or funding authority.
package periodicprefix

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Pins names one isolated installation and its two existing runtime LOGINS.
type Pins struct {
	Schema         string
	AppRole        string
	ProjectionRole string
}

var ErrRolePrivileges = errors.New("periodic_prefix_role_privileges_invalid")

type prefixColumn struct {
	name      string
	typeOID   uint32
	insert    bool
	update    bool
	generated string
}

type prefixTable struct {
	name    string
	columns []prefixColumn
}

// These lists are reviewed SQL expectations, never discovered permission plans.
var prefixTables = []prefixTable{
	{"periodic_budget_observation_origins", []prefixColumn{
		{"control_id", 25, true, false, ""}, {"version", 23, true, false, ""},
		{"created_xid", 5069, false, false, ""}, {"tracking_started_at", 1184, true, false, ""},
	}},
	{"periodic_budget_observation_heads", []prefixColumn{
		{"control_id", 25, true, false, ""}, {"ordinal", 20, true, true, ""},
		{"last_observation_id", 25, true, true, ""}, {"updated_xid", 5069, false, false, ""},
	}},
	{"periodic_budget_observation_journal", []prefixColumn{
		{"id", 25, true, false, ""}, {"control_id", 25, true, false, ""}, {"ordinal", 20, true, false, ""},
		{"previous_id", 25, true, false, ""}, {"kind", 25, true, false, ""}, {"source_id", 25, true, false, ""},
		{"event_seq", 20, true, false, ""}, {"last_event_id", 25, true, false, ""},
		{"configuration_revision", 20, true, false, ""}, {"configuration_operation_id", 25, true, false, ""},
		{"window_id", 25, true, false, ""}, {"window_revision", 20, true, false, ""},
		{"window_last_event_id", 25, true, false, ""}, {"created_xid", 5069, false, false, ""},
		{"observed_at", 1184, true, false, ""}, {"command_source_id", 25, false, false, "s"},
		{"window_source_id", 25, false, false, "s"},
	}},
}

type prefixFunction struct {
	name, arguments string
	result          uint32
}

var prefixFunctions = []prefixFunction{
	{"pb_prefix_immutable", "", 2279}, {"pb_prefix_origin_guard", "", 2279},
	{"pb_prefix_head_guard", "", 2279}, {"pb_prefix_journal_guard", "", 2279},
	{"pb_prefix_append", "25 25 25 20 25 20 25 25 20 25 1184", 25},
	{"pb_prefix_control_origin", "", 2279}, {"pb_prefix_operation_lock", "", 2279},
	{"pb_prefix_operation_capture", "", 2279}, {"pb_prefix_window_capture", "", 2279},
	{"pb_prefix_check_origin", "", 2279}, {"pb_prefix_check_journal", "", 2279},
	{"pb_prefix_check_source", "", 2279},
}

type tableObject struct {
	expected prefixTable
	oid      uint32
	typeOID  uint32
}

type functionObject struct{ oid uint32 }

type roleContext struct {
	oid uint32
}

func validRoleIdentifier(v string) bool {
	return v != "" && len(v) <= 63 && !strings.ContainsRune(v, '\x00')
}

func validRolePins(p Pins) bool {
	return validRoleIdentifier(p.Schema) && validRoleIdentifier(p.AppRole) && validRoleIdentifier(p.ProjectionRole) &&
		p.AppRole != p.ProjectionRole && p.Schema != "public" && p.Schema != "information_schema" && !strings.HasPrefix(p.Schema, "pg_")
}

// VerifyRolesTx checks only the frozen candidate's local objects and ACL delta
// in a caller-owned RR transaction. It never begins, ends, installs, or repairs.
// The caller must exclusively own the transaction and avoid concurrent role DDL.
func VerifyRolesTx(ctx context.Context, tx pgx.Tx, pins Pins) error {
	if ctx == nil || tx == nil || ctx.Err() != nil || !validRolePins(pins) {
		return ErrRolePrivileges
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var isolation, access string
	if tx.QueryRow(ctx, `SELECT pg_catalog.current_setting('transaction_isolation'),pg_catalog.current_setting('transaction_read_only')`).Scan(&isolation, &access) != nil || isolation != "repeatable read" || access != "on" && access != "off" {
		return ErrRolePrivileges
	}
	var schemaOID, ownerOID uint32
	if tx.QueryRow(ctx, `SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1`, pins.Schema).Scan(&schemaOID, &ownerOID) != nil {
		return ErrRolePrivileges
	}
	app, err := prefixLogin(ctx, tx, pins.AppRole, ownerOID)
	if err != nil {
		return ErrRolePrivileges
	}
	projection, err := prefixLogin(ctx, tx, pins.ProjectionRole, ownerOID)
	if err != nil || projection == app {
		return ErrRolePrivileges
	}
	tables, err := loadPrefixTables(ctx, tx, schemaOID, ownerOID, app)
	if err != nil {
		return ErrRolePrivileges
	}
	functions, err := loadPrefixFunctions(ctx, tx, pins.Schema, schemaOID, ownerOID, app)
	if err != nil {
		return ErrRolePrivileges
	}
	if verifyPrefixSourceShape(ctx, tx, schemaOID, ownerOID) != nil {
		return ErrRolePrivileges
	}
	for _, runtime := range []struct {
		oid uint32
		app bool
	}{{app, true}, {projection, false}} {
		contexts, err := prefixRoleContexts(ctx, tx, runtime.oid)
		if err != nil {
			return ErrRolePrivileges
		}
		for _, c := range contexts {
			required := c.oid == runtime.oid
			if verifyPrefixSchemaPrivileges(ctx, tx, c.oid, schemaOID, ownerOID, required) != nil ||
				verifyPrefixObjectPrivileges(ctx, tx, c.oid, tables, functions, runtime.app, required) != nil ||
				verifyPrefixSourcePrivileges(ctx, tx, c.oid, schemaOID, runtime.app, required) != nil {
				return ErrRolePrivileges
			}
		}
	}
	if ctx.Err() != nil || !time.Now().Before(deadline(ctx)) {
		return ErrRolePrivileges
	}
	return nil
}

func deadline(ctx context.Context) time.Time {
	v, _ := ctx.Deadline()
	return v
}

func prefixLogin(ctx context.Context, tx pgx.Tx, name string, owner uint32) (uint32, error) {
	var oid uint32
	var login bool
	if tx.QueryRow(ctx, `SELECT oid,rolcanlogin FROM pg_catalog.pg_roles WHERE rolname=$1`, name).Scan(&oid, &login) != nil || !login || oid == owner {
		return 0, ErrRolePrivileges
	}
	return oid, nil
}

func prefixRoleContexts(ctx context.Context, tx pgx.Tx, initial uint32) ([]roleContext, error) {
	rows, err := tx.Query(ctx, `WITH RECURSIVE contexts(oid) AS (
 SELECT $1::oid UNION
 SELECT m.roleid FROM contexts c JOIN pg_catalog.pg_auth_members m
 ON (m.member=c.oid AND m.set_option)
 OR (m.admin_option AND pg_catalog.pg_has_role(c.oid,m.member,'USAGE'))
 ) SELECT r.oid,r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication OR r.rolbypassrls
 FROM pg_catalog.pg_roles r JOIN contexts c ON c.oid=r.oid ORDER BY r.oid LIMIT 257`, initial)
	if err != nil {
		return nil, ErrRolePrivileges
	}
	defer rows.Close()
	var out []roleContext
	for rows.Next() {
		var c roleContext
		var high bool
		if rows.Scan(&c.oid, &high) != nil || high || len(out) >= 256 {
			return nil, ErrRolePrivileges
		}
		out = append(out, c)
	}
	if rows.Err() != nil || len(out) == 0 {
		return nil, ErrRolePrivileges
	}
	return out, nil
}

func verifyPrefixSchemaPrivileges(ctx context.Context, tx pgx.Tx, role, schema, owner uint32, required bool) error {
	var usage, create, grantable, owns bool
	if tx.QueryRow(ctx, `SELECT pg_catalog.has_schema_privilege($1::oid,$2::oid,'USAGE'),
 pg_catalog.has_schema_privilege($1::oid,$2::oid,'CREATE'),
 pg_catalog.has_schema_privilege($1::oid,$2::oid,'USAGE WITH GRANT OPTION') OR pg_catalog.has_schema_privilege($1::oid,$2::oid,'CREATE WITH GRANT OPTION'),
 pg_catalog.pg_has_role($1::oid,$3::oid,'USAGE')`, role, schema, owner).Scan(&usage, &create, &grantable, &owns) != nil || create || grantable || owns || required && !usage {
		return ErrRolePrivileges
	}
	return nil
}

func loadPrefixTables(ctx context.Context, tx pgx.Tx, schema, owner, app uint32) ([]tableObject, error) {
	var out []tableObject
	for _, expected := range prefixTables {
		var object tableObject
		object.expected = expected
		var valid bool
		if tx.QueryRow(ctx, `SELECT c.oid,c.reltype,
 c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND c.relowner=$3::oid
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a
 WHERE a.grantor<>c.relowner OR a.is_grantable OR a.grantee NOT IN(c.relowner,$4::oid)
 OR (a.grantee=$4::oid AND a.privilege_type<>'SELECT'))
 FROM pg_catalog.pg_class c WHERE c.relnamespace=$1::oid AND c.relname=$2`, schema, expected.name, owner, app).Scan(&object.oid, &object.typeOID, &valid) != nil || !valid {
			return nil, ErrRolePrivileges
		}
		if verifyPrefixColumns(ctx, tx, object, owner, app) != nil || verifyPrefixType(ctx, tx, object, schema, owner, app) != nil {
			return nil, ErrRolePrivileges
		}
		out = append(out, object)
	}
	return out, nil
}

func verifyPrefixColumns(ctx context.Context, tx pgx.Tx, table tableObject, owner, app uint32) error {
	var inserts, updates []string
	for _, c := range table.expected.columns {
		if c.insert {
			inserts = append(inserts, c.name)
		}
		if c.update {
			updates = append(updates, c.name)
		}
	}
	// Empty non-nil arrays avoid SQL NULL turning a forbidden ACL into UNKNOWN.
	if inserts == nil {
		inserts = []string{}
	}
	if updates == nil {
		updates = []string{}
	}
	rows, err := tx.Query(ctx, `SELECT a.attname,a.attnum,a.atttypid,a.attgenerated::text,
 NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(a.attacl) x
 WHERE x.grantor<>$2::oid OR x.is_grantable OR x.grantee NOT IN($2::oid,$3::oid)
 OR (x.grantee=$3::oid AND NOT((x.privilege_type='INSERT' AND a.attname=ANY($4::text[]))
 OR (x.privilege_type='UPDATE' AND a.attname=ANY($5::text[])))))
 FROM pg_catalog.pg_attribute a WHERE a.attrelid=$1::oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`, table.oid, owner, app, inserts, updates)
	if err != nil {
		return ErrRolePrivileges
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name, generated string
		var ordinal int16
		var oid uint32
		var valid bool
		if rows.Scan(&name, &ordinal, &oid, &generated, &valid) != nil || count >= len(table.expected.columns) || !valid {
			return ErrRolePrivileges
		}
		expected := table.expected.columns[count]
		if ordinal != int16(count+1) || name != expected.name || oid != expected.typeOID || generated != expected.generated {
			return ErrRolePrivileges
		}
		count++
	}
	if rows.Err() != nil || count != len(table.expected.columns) {
		return ErrRolePrivileges
	}
	return nil
}

func verifyPrefixType(ctx context.Context, tx pgx.Tx, table tableObject, schema, owner, app uint32) error {
	var valid bool
	if tx.QueryRow(ctx, `SELECT t.typtype='c' AND t.typisdefined AND t.typrelid=$2::oid AND t.typnamespace=$3::oid AND t.typowner=$4::oid AND t.typname=$6
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(t.typacl,pg_catalog.acldefault('T',t.typowner))) a
 WHERE a.grantor<>t.typowner OR a.is_grantable OR a.grantee NOT IN(t.typowner,$5::oid) OR a.privilege_type<>'USAGE')
 FROM pg_catalog.pg_type t WHERE t.oid=$1::oid`, table.typeOID, table.oid, schema, owner, app, table.expected.name).Scan(&valid) != nil || !valid {
		return ErrRolePrivileges
	}
	return nil
}

func loadPrefixFunctions(ctx context.Context, tx pgx.Tx, schemaName string, schema, owner, app uint32) ([]functionObject, error) {
	rows, err := tx.Query(ctx, `SELECT p.oid,p.proname,p.proargtypes::text,p.prorettype,
 p.proowner=$3::oid AND p.prokind='f' AND NOT p.prosecdef AND NOT p.proretset AND NOT p.proleakproof
 AND p.provolatile='v' AND p.provariadic=0 AND p.pronargdefaults=0 AND l.lanname='plpgsql'
 AND p.proconfig=ARRAY['search_path='||pg_catalog.quote_ident($2)||', pg_catalog, pg_temp']::text[]
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
 WHERE a.grantor<>p.proowner OR a.is_grantable OR a.grantee NOT IN(p.proowner,$4::oid) OR a.privilege_type<>'EXECUTE')
 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_language l ON l.oid=p.prolang
 WHERE p.pronamespace=$1::oid AND pg_catalog.left(p.proname,10)='pb_prefix_' ORDER BY p.proname,p.oid`, schema, schemaName, owner, app)
	if err != nil {
		return nil, ErrRolePrivileges
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []functionObject
	for rows.Next() {
		var object functionObject
		var name, args string
		var result uint32
		var valid bool
		if rows.Scan(&object.oid, &name, &args, &result, &valid) != nil || !valid || seen[name] || len(out) >= len(prefixFunctions) {
			return nil, ErrRolePrivileges
		}
		i := slices.IndexFunc(prefixFunctions, func(f prefixFunction) bool { return f.name == name })
		if i < 0 || prefixFunctions[i].arguments != args || prefixFunctions[i].result != result {
			return nil, ErrRolePrivileges
		}
		seen[name] = true
		out = append(out, object)
	}
	if rows.Err() != nil || len(out) != len(prefixFunctions) {
		return nil, ErrRolePrivileges
	}
	return out, nil
}

func privilegeSubset(actual, allowed []bool, required bool) bool {
	if len(actual) != len(allowed) {
		return false
	}
	for i, v := range actual {
		if v && !allowed[i] || required && v != allowed[i] {
			return false
		}
	}
	return true
}

func verifyPrefixObjectPrivileges(ctx context.Context, tx pgx.Tx, role uint32, tables []tableObject, functions []functionObject, app, required bool) error {
	for _, table := range tables {
		var privileges []bool
		var owns, grantable, usage, typeGrantable bool
		if tx.QueryRow(ctx, `SELECT
 ARRAY(SELECT pg_catalog.has_table_privilege($1::oid,c.oid,p) FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) WITH ORDINALITY x(p,n) ORDER BY n),
 pg_catalog.pg_has_role($1::oid,c.relowner,'USAGE'),
 EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) x(p) WHERE pg_catalog.has_table_privilege($1::oid,c.oid,p||' WITH GRANT OPTION')),
 pg_catalog.has_type_privilege($1::oid,c.reltype,'USAGE'),pg_catalog.has_type_privilege($1::oid,c.reltype,'USAGE WITH GRANT OPTION')
 FROM pg_catalog.pg_class c WHERE c.oid=$2::oid`, role, table.oid).Scan(&privileges, &owns, &grantable, &usage, &typeGrantable) != nil || owns || grantable || typeGrantable || !privilegeSubset(privileges, []bool{app, false, false, false, false, false, false}, required) || usage && !app || required && usage != app {
			return ErrRolePrivileges
		}
		rows, err := tx.Query(ctx, `SELECT a.attname,
 ARRAY(SELECT pg_catalog.has_column_privilege($1::oid,a.attrelid,a.attnum,p) FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) WITH ORDINALITY x(p,n) ORDER BY n),
 EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) x(p) WHERE pg_catalog.has_column_privilege($1::oid,a.attrelid,a.attnum,p||' WITH GRANT OPTION'))
 FROM pg_catalog.pg_attribute a WHERE a.attrelid=$2::oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`, role, table.oid)
		if err != nil {
			return ErrRolePrivileges
		}
		count := 0
		for rows.Next() {
			var name string
			var columnPrivileges []bool
			var columnGrantable bool
			if rows.Scan(&name, &columnPrivileges, &columnGrantable) != nil || columnGrantable || count >= len(table.expected.columns) {
				rows.Close()
				return ErrRolePrivileges
			}
			c := table.expected.columns[count]
			if name != c.name || !privilegeSubset(columnPrivileges, []bool{app, app && c.insert, app && c.update, false}, required) {
				rows.Close()
				return ErrRolePrivileges
			}
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || count != len(table.expected.columns) {
			return ErrRolePrivileges
		}
	}
	for _, function := range functions {
		var execute, grantable, owns bool
		if tx.QueryRow(ctx, `SELECT pg_catalog.has_function_privilege($1::oid,p.oid,'EXECUTE'),pg_catalog.has_function_privilege($1::oid,p.oid,'EXECUTE WITH GRANT OPTION'),pg_catalog.pg_has_role($1::oid,p.proowner,'USAGE') FROM pg_catalog.pg_proc p WHERE p.oid=$2::oid`, role, function.oid).Scan(&execute, &grantable, &owns) != nil || grantable || owns || execute && !app || required && execute != app {
			return ErrRolePrivileges
		}
	}
	return nil
}

const prefixSourcePredicate = `(c.relname='periodic_budget_operations' AND a.attname='observation_id') OR (c.relname='periodic_budget_windows' AND a.attname IN('observation_id','observation_created_xid'))`

func verifyPrefixSourceShape(ctx context.Context, tx pgx.Tx, schema, owner uint32) error {
	rows, err := tx.Query(ctx, `SELECT c.relname,a.attname,a.atttypid,
 c.relkind='r' AND c.relpersistence='p' AND c.relowner=$2::oid AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(a.attacl) x WHERE x.grantee<>c.relowner OR x.grantor<>c.relowner OR x.is_grantable)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) x WHERE x.grantee=0)
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid
 WHERE c.relnamespace=$1::oid AND a.attnum>0 AND NOT a.attisdropped AND (`+prefixSourcePredicate+`) ORDER BY c.relname,a.attname`, schema, owner)
	if err != nil {
		return ErrRolePrivileges
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var table, column string
		var oid uint32
		var valid bool
		if rows.Scan(&table, &column, &oid, &valid) != nil || !valid || count >= 3 || column == "observation_created_xid" && oid != 5069 || column == "observation_id" && oid != 25 {
			return ErrRolePrivileges
		}
		count++
	}
	if rows.Err() != nil || count != 3 {
		return ErrRolePrivileges
	}
	return nil
}

func verifyPrefixSourcePrivileges(ctx context.Context, tx pgx.Tx, role, schema uint32, app, required bool) error {
	rows, err := tx.Query(ctx, `SELECT
 ARRAY(SELECT pg_catalog.has_column_privilege($1::oid,a.attrelid,a.attnum,p) FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) WITH ORDINALITY x(p,n) ORDER BY n),
 EXISTS(SELECT 1 FROM pg_catalog.unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) x(p) WHERE pg_catalog.has_column_privilege($1::oid,a.attrelid,a.attnum,p||' WITH GRANT OPTION')),
 pg_catalog.pg_has_role($1::oid,c.relowner,'USAGE')
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid
 WHERE c.relnamespace=$2::oid AND a.attnum>0 AND NOT a.attisdropped AND (`+prefixSourcePredicate+`) ORDER BY c.relname,a.attname`, role, schema)
	if err != nil {
		return ErrRolePrivileges
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var privileges []bool
		var grantable, owns bool
		if rows.Scan(&privileges, &grantable, &owns) != nil || grantable || owns || !privilegeSubset(privileges, []bool{app, app, false, false}, required) || count >= 3 {
			return ErrRolePrivileges
		}
		count++
	}
	if rows.Err() != nil || count != 3 {
		return ErrRolePrivileges
	}
	return nil
}
