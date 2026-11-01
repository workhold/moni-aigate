package paymentruntime

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
)

// This reads the actual catalog only. Expected values come exclusively from
// reviewed static JSON embedded alongside the exact version-1 SQL.
const catalogSnapshotSQL = `SELECT jsonb_build_object(
 'tables',(SELECT jsonb_agg(jsonb_build_object('name',c.relname,'columns',(SELECT jsonb_agg(jsonb_build_object('name',a.attname,'type',format_type(a.atttypid,a.atttypmod),'not_null',a.attnotnull,'default',coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum) FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)) ORDER BY c.relname) FROM pg_class c WHERE c.relnamespace=$1::oid AND c.relkind='r'),
 'functions',(SELECT jsonb_agg(jsonb_build_object('name',p.proname,'arguments',pg_get_function_identity_arguments(p.oid),'returns',pg_get_function_result(p.oid),'language',l.lanname,'volatility',p.provolatile,'strict',p.proisstrict,'source_sha256',encode(sha256(convert_to(p.prosrc,'UTF8')),'hex')) ORDER BY p.proname,pg_get_function_identity_arguments(p.oid)) FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.pronamespace=$1::oid),
 'triggers',(SELECT jsonb_agg(jsonb_build_object('table',c.relname,'name',t.tgname,'function',p.proname,'type',t.tgtype,'deferrable',t.tgdeferrable,'deferred',t.tginitdeferred) ORDER BY c.relname,t.tgname) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_proc p ON p.oid=t.tgfoid WHERE c.relnamespace=$1::oid AND NOT t.tgisinternal),
 'indexes',(SELECT jsonb_agg(jsonb_build_object('name',ic.relname,'table',c.relname,'definition',pg_get_indexdef(i.indexrelid)) ORDER BY ic.relname) FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_class ic ON ic.oid=i.indexrelid WHERE c.relnamespace=$1::oid),
 'constraints',(SELECT jsonb_agg(jsonb_build_object('table',c.relname,'name',co.conname,'type',co.contype,'definition',pg_get_constraintdef(co.oid),'deferrable',co.condeferrable,'deferred',co.condeferred) ORDER BY c.relname,co.conname) FROM pg_constraint co JOIN pg_class c ON c.oid=co.conrelid WHERE c.relnamespace=$1::oid))`

func attestCatalog(ctx context.Context, p *pgxpool.Pool, c Config) error {
	tx, e := p.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return ErrEnvironment
	}
	defer tx.Rollback(ctx)
	m := c.manifest
	own := ownPin(m)
	for _, s := range []schemaPin{m.App, m.Mock, m.Receiver} {
		var oid, owner uint32
		if tx.QueryRow(ctx, "SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1", s.Name).Scan(&oid, &owner) != nil || oid != s.OID || owner != s.Owner.OID {
			return ErrEnvironment
		}
	}
	for _, r := range allRoles(m) {
		var oid uint32
		var login, inherit, super, createdb, createrole, replication, bypass, member bool
		e = tx.QueryRow(ctx, `SELECT oid,rolcanlogin,rolinherit,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls,
 EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members a WHERE a.member=pg_roles.oid OR a.roleid=pg_roles.oid)
 FROM pg_catalog.pg_roles WHERE rolname=$1`, r.Name).Scan(&oid, &login, &inherit, &super, &createdb, &createrole, &replication, &bypass, &member)
		if e != nil || oid != r.OID || login != r.Login || inherit || super || createdb || createrole || replication || bypass || member {
			return ErrRole
		}
	}
	var bad bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspowner=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_class WHERE relowner=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_proc WHERE proowner=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datdba=$1::oid)`, own.Runtime.OID).Scan(&bad)
	if e != nil || bad {
		return ErrRole
	}
	var fsync, fullPage, encoding string
	var version int
	e = tx.QueryRow(ctx, `SELECT current_setting('fsync'),current_setting('full_page_writes'),current_setting('server_encoding'),current_setting('server_version_num')::integer`).Scan(&fsync, &fullPage, &encoding, &version)
	if e != nil || fsync != "on" || fullPage != "on" || encoding != "UTF8" || version < 160000 {
		return ErrEnvironment
	}
	inventory, e := paymentsandboxschema.Inventory(paymentsandboxschema.Component(m.Component))
	if e != nil {
		return ErrConfiguration
	}
	var body []byte
	if tx.QueryRow(ctx, catalogSnapshotSQL, own.OID).Scan(&body) != nil {
		return ErrState
	}
	body = []byte(strings.ReplaceAll(string(body), own.Name, "__SCHEMA__"))
	var actual paymentsandboxschema.CatalogInventory
	if json.Unmarshal(body, &actual) != nil {
		return ErrState
	}
	actual.SQLSHA256 = inventory.SQLSHA256
	if !reflect.DeepEqual(actual, inventory) {
		return ErrState
	}
	var count int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_class WHERE relnamespace=$1::oid`, own.OID).Scan(&count)
	if e != nil || count != len(inventory.Tables)+len(inventory.Indexes) {
		return ErrState
	}
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace=$1::oid AND (relowner<>$2::oid OR relrowsecurity OR relforcerowsecurity))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_extension WHERE extnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid JOIN pg_catalog.pg_proc f ON f.oid=t.tgfoid WHERE c.relnamespace=$1::oid AND NOT t.tgisinternal AND (t.tgenabled<>'O' OR f.pronamespace<>$1::oid))`, own.OID, own.Owner.OID).Scan(&bad)
	if e != nil || bad {
		return ErrState
	}
	rows, e := tx.Query(ctx, `SELECT proowner,prosecdef,proconfig FROM pg_catalog.pg_proc WHERE pronamespace=$1::oid`, own.OID)
	if e != nil {
		return ErrState
	}
	valid := true
	for rows.Next() {
		var owner uint32
		var definer bool
		var settings []string
		if rows.Scan(&owner, &definer, &settings) != nil || owner != own.Owner.OID || definer || !fixedSearchPath(settings, own.Name) {
			valid = false
			break
		}
	}
	rows.Close()
	if rows.Err() != nil || !valid {
		return ErrState
	}
	if e = attestNamespace(ctx, tx, own, inventory); e != nil {
		return e
	}
	if e = attestOwnPermissions(ctx, tx, c, inventory); e != nil {
		return e
	}
	if e = attestForeignPermissions(ctx, tx, m); e != nil {
		return e
	}
	if tx.Commit(ctx) != nil {
		return ErrState
	}
	return nil
}

// Version 1 contains only the reviewed tables and their automatic row/array
// types. A static table name set defines the expected type names; catalog OIDs
// are checked only as the bidirectional binding of those reviewed objects.
func attestNamespace(ctx context.Context, tx pgx.Tx, own schemaPin, inventory paymentsandboxschema.CatalogInventory) error {
	type relation struct{ oid, rowType uint32 }
	tables := make(map[string]relation, len(inventory.Tables))
	for _, table := range inventory.Tables {
		tables[table.Name] = relation{}
	}
	rows, e := tx.Query(ctx, `SELECT relname,oid,reltype FROM pg_catalog.pg_class WHERE relnamespace=$1::oid AND relkind='r'`, own.OID)
	if e != nil {
		return ErrState
	}
	valid := true
	count := 0
	for rows.Next() {
		var name string
		var r relation
		if rows.Scan(&name, &r.oid, &r.rowType) != nil {
			valid = false
			break
		}
		if _, expected := tables[name]; !expected || r.oid == 0 || r.rowType == 0 {
			valid = false
			break
		}
		tables[name] = r
		count++
	}
	rows.Close()
	if rows.Err() != nil || !valid || count != len(tables) {
		return ErrState
	}
	type dataType struct {
		oid, owner, relation, element, array uint32
		kind, category                       string
		defined                              bool
	}
	types := make(map[string]dataType, len(tables)*2)
	rows, e = tx.Query(ctx, `SELECT typname,oid,typowner,typrelid,typelem,typarray,typtype::text,typcategory::text,typisdefined FROM pg_catalog.pg_type WHERE typnamespace=$1::oid`, own.OID)
	if e != nil {
		return ErrState
	}
	for rows.Next() {
		var name string
		var t dataType
		if rows.Scan(&name, &t.oid, &t.owner, &t.relation, &t.element, &t.array, &t.kind, &t.category, &t.defined) != nil {
			valid = false
			break
		}
		if _, duplicate := types[name]; duplicate {
			valid = false
			break
		}
		types[name] = t
	}
	rows.Close()
	if rows.Err() != nil || !valid || len(types) != len(tables)*2 {
		return ErrState
	}
	for name, r := range tables {
		row, haveRow := types[name]
		array, haveArray := types["_"+name]
		if !haveRow || !haveArray || row.oid != r.rowType || row.kind != "c" || row.category != "C" || row.owner != own.Owner.OID || row.relation != r.oid || row.element != 0 || row.array == 0 || !row.defined ||
			array.oid != row.array || array.kind != "b" || array.category != "A" || array.owner != own.Owner.OID || array.relation != 0 || array.element != row.oid || array.array != 0 || !array.defined {
			return ErrState
		}
	}
	var extra bool
	e = tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM pg_catalog.pg_operator WHERE oprnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite r JOIN pg_catalog.pg_class c ON c.oid=r.ev_class WHERE c.relnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_policy p JOIN pg_catalog.pg_class c ON c.oid=p.polrelid WHERE c.relnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_collation WHERE collnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_conversion WHERE connamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_opclass WHERE opcnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_opfamily WHERE opfnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_ts_config WHERE cfgnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_ts_dict WHERE dictnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_ts_parser WHERE prsnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_ts_template WHERE tmplnamespace=$1::oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_statistic_ext WHERE stxnamespace=$1::oid)`, own.OID).Scan(&extra)
	if e != nil || extra {
		return ErrState
	}
	return nil
}

func fixedSearchPath(settings []string, schema string) bool {
	if len(settings) != 1 || !strings.HasPrefix(settings[0], "search_path=") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(settings[0], "search_path="), ",")
	if len(parts) != 3 {
		return false
	}
	for i, want := range []string{schema, "pg_catalog", "pg_temp"} {
		if strings.Trim(strings.TrimSpace(parts[i]), `"`) != want {
			return false
		}
	}
	return true
}
func attestOwnPermissions(ctx context.Context, tx pgx.Tx, c Config, inventory paymentsandboxschema.CatalogInventory) error {
	own := ownPin(c.manifest)
	component := paymentsandboxschema.Component(c.manifest.Component)
	var usage, create, grant bool
	e := tx.QueryRow(ctx, `SELECT has_schema_privilege($1::oid,$2::oid,'USAGE'),has_schema_privilege($1::oid,$2::oid,'CREATE'),has_schema_privilege($1::oid,$2::oid,'USAGE WITH GRANT OPTION') OR has_schema_privilege($1::oid,$2::oid,'CREATE WITH GRANT OPTION')`, own.Runtime.OID, own.OID).Scan(&usage, &create, &grant)
	if e != nil || !usage || create || grant {
		return ErrRole
	}
	for _, table := range inventory.Tables {
		insert, update := paymentsandboxschema.Permissions(component, table.Name)
		updates := map[string]bool{}
		for _, s := range update {
			updates[s] = true
		}
		var oid uint32
		if tx.QueryRow(ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relnamespace=$1::oid AND relname=$2", own.OID, table.Name).Scan(&oid) != nil {
			return ErrRole
		}
		for _, pr := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
			var have, grantable bool
			if tx.QueryRow(ctx, "SELECT has_table_privilege($1::oid,$2::oid,$3),has_table_privilege($1::oid,$2::oid,$4)", own.Runtime.OID, oid, pr, pr+" WITH GRANT OPTION").Scan(&have, &grantable) != nil {
				return ErrRole
			}
			want := pr == "SELECT" || (pr == "INSERT" && insert)
			if have != want || grantable {
				return ErrRole
			}
		}
		for _, col := range table.Columns {
			for _, pr := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
				var have, grantable bool
				if tx.QueryRow(ctx, "SELECT has_column_privilege($1::oid,$2::oid,$3,$4),has_column_privilege($1::oid,$2::oid,$3,$5)", own.Runtime.OID, oid, col.Name, pr, pr+" WITH GRANT OPTION").Scan(&have, &grantable) != nil {
					return ErrRole
				}
				want := pr == "SELECT" || (pr == "INSERT" && insert) || (pr == "UPDATE" && updates[col.Name])
				if have != want || grantable {
					return ErrRole
				}
			}
		}
	}
	var bad bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p WHERE p.pronamespace=$1::oid AND
 (NOT has_function_privilege($2::oid,p.oid,'EXECUTE') OR has_function_privilege($2::oid,p.oid,'EXECUTE WITH GRANT OPTION') OR
 EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_class c CROSS JOIN LATERAL aclexplode(coalesce(c.relacl,acldefault('r',c.relowner))) a WHERE c.relnamespace=$1::oid AND a.grantee=0)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_namespace n CROSS JOIN LATERAL aclexplode(coalesce(n.nspacl,acldefault('n',n.nspowner))) a WHERE n.oid=$1::oid AND a.grantee=0)`, own.OID, own.Runtime.OID).Scan(&bad)
	if e != nil || bad {
		return ErrRole
	}
	return nil
}
func attestForeignPermissions(ctx context.Context, tx pgx.Tx, m commonManifest) error {
	pairs := []struct {
		role   rolePin
		schema schemaPin
	}{
		{m.Mock.Runtime, m.App}, {m.Mock.Runtime, m.Receiver}, {m.Receiver.Runtime, m.App}, {m.Receiver.Runtime, m.Mock},
		{m.App.Runtime, m.Mock}, {m.App.Runtime, m.Receiver}, {m.AppProjector, m.Mock}, {m.AppProjector, m.Receiver},
	}
	for _, p := range pairs {
		var bad bool
		e := tx.QueryRow(ctx, `SELECT has_schema_privilege($1::oid,$2::oid,'USAGE') OR has_schema_privilege($1::oid,$2::oid,'CREATE')
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_class c CROSS JOIN unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) a WHERE c.relnamespace=$2::oid AND c.relkind IN ('r','p','v','m','f','S') AND has_table_privilege($1::oid,c.oid,a))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_class c CROSS JOIN unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) a WHERE c.relnamespace=$2::oid AND c.relkind IN ('r','p','v','m','f') AND has_any_column_privilege($1::oid,c.oid,a))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_proc f WHERE f.pronamespace=$2::oid AND has_function_privilege($1::oid,f.oid,'EXECUTE'))`, p.role.OID, p.schema.OID).Scan(&bad)
		if e != nil || bad {
			return ErrRole
		}
	}
	return nil
}
