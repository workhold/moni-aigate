package procurementattemptbirth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/internal/procurementattemptbirth"
	"github.com/moni-ai/moni-aigate/internal/procurementattemptrules"
	"github.com/moni-ai/moni-aigate/internal/procurementidentity"
	"github.com/moni-ai/moni-aigate/migrations"
)

// This inspector is test-only. It issues no Qualified value, runtime profile,
// migration or production authority. The reference is rebuilt from authenticated
// frozen input in a rolled-back child transaction; no target catalog definition
// is ever used to create an expected value. Target objects are only read.
type abCatalogDB interface {
	Begin(context.Context) (pgx.Tx, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type abCatalogColumn struct {
	Position   int    `json:"position"`
	Name       string `json:"name"`
	Definition string `json:"definition_sql"`
}
type abCatalogTable struct {
	Name    string            `json:"name"`
	Table   string            `json:"table"`
	Columns []abCatalogColumn `json:"columns"`
}
type abCatalogConstraint struct {
	Name       string `json:"name"`
	Table      string `json:"table"`
	Kind       string `json:"kind"`
	Definition string `json:"definition_sql"`
}
type abCatalogRelation struct {
	Candidate          string                `json:"candidate"`
	Counts             map[string]int        `json:"counts"`
	Tables             []abCatalogTable      `json:"tables"`
	OriginalExtensions []abCatalogTable      `json:"original_extensions"`
	Constraints        []abCatalogConstraint `json:"constraints"`
}
type abCatalogFunction struct {
	Name        string   `json:"name"`
	Types       []string `json:"input_types"`
	Material    string   `json:"source_material"`
	SourceIndex int      `json:"source_index"`
	Language    string   `json:"language"`
	Definer     bool     `json:"security_definer"`
	Volatility  string   `json:"volatility"`
	Parallel    string   `json:"parallel"`
	Owner       string   `json:"owner"`
	SearchPath  []string `json:"search_path"`
}
type abCatalogTrigger struct {
	Name       string `json:"name"`
	Table      string `json:"table"`
	Function   string `json:"function"`
	Type       int    `json:"type"`
	Deferrable bool   `json:"deferrable"`
	Initially  bool   `json:"initially_deferred"`
	Enabled    string `json:"enabled"`
	Constraint bool   `json:"constraint"`
}
type abCatalogDescriptor struct {
	Candidate string              `json:"candidate"`
	Counts    map[string]int      `json:"counts"`
	Functions []abCatalogFunction `json:"functions"`
	Triggers  []abCatalogTrigger  `json:"triggers"`
}

func abCatalogMaterials() (abCatalogRelation, abCatalogDescriptor, error) {
	var r abCatalogRelation
	var c abCatalogDescriptor
	rb, e := procurementattemptbirth.RelationDescriptor()
	if e != nil {
		return r, c, e
	}
	cb, e := procurementattemptbirth.CatalogDescriptor()
	if e != nil {
		return r, c, e
	}
	if e = json.Unmarshal(rb, &r); e != nil {
		return r, c, e
	}
	if e = json.Unmarshal(cb, &c); e != nil {
		return r, c, e
	}
	if r.Candidate != procurementattemptbirth.Candidate || c.Candidate != r.Candidate || len(r.Tables) != 12 || len(r.OriginalExtensions) != 7 || len(r.Constraints) != 137 || len(c.Functions) != 38 || len(c.Triggers) != 68 {
		return r, c, fmt.Errorf("A2 fixed inventory changed")
	}
	columns, extensions, keys, checks, fks, definers := 0, 0, 0, 0, 0, 0
	seen := map[string]bool{}
	for _, t := range r.Tables {
		if seen[t.Name] {
			return r, c, fmt.Errorf("duplicate table %s", t.Name)
		}
		seen[t.Name] = true
		columns += len(t.Columns)
		for i, v := range t.Columns {
			if v.Position != i+1 {
				return r, c, fmt.Errorf("column ordinal %s.%s", t.Name, v.Name)
			}
		}
	}
	for _, t := range r.OriginalExtensions {
		extensions += len(t.Columns)
	}
	for _, k := range r.Constraints {
		switch k.Kind {
		case "primary_key", "unique":
			keys++
		case "check":
			checks++
		case "foreign_key":
			fks++
		default:
			return r, c, fmt.Errorf("constraint kind %s", k.Kind)
		}
	}
	for _, f := range c.Functions {
		if f.Definer {
			definers++
		}
		if f.Owner != "writer" || !reflect.DeepEqual(f.SearchPath, []string{"{{MONI_SCHEMA}}", "pg_catalog", "pg_temp"}) {
			return r, c, fmt.Errorf("function authority descriptor %s", f.Name)
		}
	}
	if columns != 262 || extensions != 38 || keys != 22 || checks != 46 || fks != 69 || definers != 4 || r.Counts["new_columns"] != 262 || c.Counts["functions"] != 38 || c.Counts["triggers"] != 68 {
		return r, c, fmt.Errorf("A2 inventory count mismatch")
	}
	return r, c, nil
}

// Split only top-level statements. Semicolons, comment markers and dollar tags
// inside SQL strings/bodies are preserved byte-for-byte, including whitespace.
func abCatalogStatements(sql string) ([]string, error) {
	var out []string
	start := 0
	for i := 0; i < len(sql); {
		switch {
		case strings.HasPrefix(sql[i:], "--"):
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				i = len(sql)
			} else {
				i += j + 1
			}
		case strings.HasPrefix(sql[i:], "/*"):
			depth := 1
			i += 2
			for i < len(sql) && depth > 0 {
				if strings.HasPrefix(sql[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(sql[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth != 0 {
				return nil, fmt.Errorf("unterminated comment")
			}
		case sql[i] == '\'' || sql[i] == '"':
			quote := sql[i]
			i++
			closed := false
			for i < len(sql) {
				if sql[i] == quote {
					if i+1 < len(sql) && sql[i+1] == quote {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated SQL quote")
			}
		case sql[i] == '$':
			j := i + 1
			for j < len(sql) && ((sql[j] >= 'a' && sql[j] <= 'z') || (sql[j] >= 'A' && sql[j] <= 'Z') || (sql[j] >= '0' && sql[j] <= '9') || sql[j] == '_') {
				j++
			}
			if j < len(sql) && sql[j] == '$' {
				tag := sql[i : j+1]
				end := strings.Index(sql[j+1:], tag)
				if end < 0 {
					return nil, fmt.Errorf("unterminated dollar quote")
				}
				i = j + 1 + end + len(tag)
			} else {
				i++
			}
		case sql[i] == ';':
			out = append(out, sql[start:i+1])
			i++
			start = i
		default:
			i++
		}
	}
	if strings.TrimSpace(sql[start:]) != "" {
		out = append(out, sql[start:])
	}
	return out, nil
}

var abCatalogFunctionRE = regexp.MustCompile(`(?s)CREATE FUNCTION\s+(?:"(?:[^"]|"")+"|[a-z_0-9]+)\.(pp_[a-z_0-9]+)\(`)
var abCatalogBodyRE = regexp.MustCompile(`(?s)\bAS (\$moni_ab_[0-9]+\$)(.*?)\$moni_ab_[0-9]+\$;\s*$`)

func abCatalogFunctionSources(b procurementattemptbirth.Bindings) (map[string]string, map[string]string, error) {
	sql, e := procurementattemptbirth.SQL(b)
	if e != nil {
		return nil, nil, e
	}
	stmts, e := abCatalogStatements(sql)
	if e != nil {
		return nil, nil, e
	}
	bodies, creates := map[string]string{}, map[string]string{}
	for _, s := range stmts {
		m := abCatalogFunctionRE.FindStringSubmatchIndex(s)
		if m == nil {
			continue
		}
		name := s[m[2]:m[3]]
		body := abCatalogBodyRE.FindStringSubmatch(s)
		if len(body) != 3 || bodies[name] != "" {
			return nil, nil, fmt.Errorf("function source %s duplicate/unparseable", name)
		}
		bodies[name] = body[2]
		creates[name] = s[m[0]:]
	}
	if len(bodies) != 38 {
		return nil, nil, fmt.Errorf("want 38 rendered function sources, got %d", len(bodies))
	}
	return bodies, creates, nil
}

func abCatalogNormalize(raw []byte, schema string) (string, error) {
	var value any
	if e := json.Unmarshal(raw, &value); e != nil {
		return "", e
	}
	var visit func(any) any
	visit = func(v any) any {
		switch x := v.(type) {
		case string:
			if x == schema {
				return "<AB>"
			}
			if x == "search_path="+schema+", pg_catalog, pg_temp" || x == "search_path="+(pgx.Identifier{schema}).Sanitize()+", pg_catalog, pg_temp" {
				return "search_path=<AB>, pg_catalog, pg_temp"
			}
			x = strings.ReplaceAll(x, pgx.Identifier{schema}.Sanitize()+".", "<AB>.")
			return strings.ReplaceAll(x, schema+".", "<AB>.")
		case []any:
			for i := range x {
				x[i] = visit(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = visit(x[k])
			}
		}
		return v
	}
	normalized, e := json.Marshal(visit(value))
	return string(normalized), e
}
func abCatalogJSON(ctx context.Context, db abCatalogDB, q, schema string, tables []string) (string, error) {
	var raw []byte
	if e := db.QueryRow(ctx, q, schema, tables).Scan(&raw); e != nil {
		return "", e
	}
	return abCatalogNormalize(raw, schema)
}

var abCatalogRelationQueries = map[string]string{
	"relations": `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.name),'[]'::jsonb) FROM (
 SELECT c.relname AS name,c.relkind,c.relpersistence,c.relowner::bigint AS owner,c.relispartition,c.relrowsecurity,c.relforcerowsecurity,c.relreplident,c.reloftype::bigint AS typed,c.reloptions,c.relhasrules,a.amname,
 EXISTS(SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid=c.oid OR i.inhparent=c.oid) AS inherited
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_catalog.pg_am a ON a.oid=c.relam WHERE n.nspname=$1 AND c.relname=ANY($2::text[])) x`,
	"columns": `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.attnum),'[]'::jsonb) FROM (
 SELECT c.relname AS table_name,a.attnum,a.attname,a.atttypid::bigint AS type,a.atttypmod,a.attcollation::bigint AS collation,a.attnotnull,a.attidentity,a.attgenerated,a.attisdropped,a.atthasdef,a.atthasmissing,a.attislocal,a.attinhcount,a.attlen,a.attbyval,a.attalign,a.attstorage,a.attcompression,
 pg_catalog.pg_get_expr(d.adbin,d.adrelid,false) AS expression
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE n.nspname=$1 AND c.relname=ANY($2::text[]) AND a.attnum>0) x`,
	"constraints": `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.name),'[]'::jsonb) FROM (
 SELECT c.relname AS table_name,k.conname AS name,k.contype,k.condeferrable,k.condeferred,k.convalidated,k.connoinherit,k.conislocal,k.coninhcount,k.conkey,k.confkey,k.confmatchtype,k.confupdtype,k.confdeltype,k.conparentid::bigint AS parent,
 fn.nspname AS target_schema,f.relname AS target_table,ix.relname AS target_index,ixn.nspname AS target_index_schema,
 CASE WHEN k.contype='f' THEN ii.indrelid=k.confrelid WHEN k.contype IN ('p','u') THEN ii.indrelid=k.conrelid ELSE NULL END AS index_on_exact_relation,
 pg_catalog.pg_get_constraintdef(k.oid,false) AS definition
 FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_catalog.pg_class f ON f.oid=k.confrelid LEFT JOIN pg_catalog.pg_namespace fn ON fn.oid=f.relnamespace LEFT JOIN pg_catalog.pg_class ix ON ix.oid=k.conindid LEFT JOIN pg_catalog.pg_namespace ixn ON ixn.oid=ix.relnamespace LEFT JOIN pg_catalog.pg_index ii ON ii.indexrelid=k.conindid
 WHERE n.nspname=$1 AND c.relname=ANY($2::text[]) AND k.contype IN ('p','u','c','f','x')) x`,
	"indexes": `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.name),'[]'::jsonb) FROM (
 SELECT c.relname AS table_name,ix.relname AS name,ix.relnamespace=c.relnamespace AS same_namespace,ix.relowner::bigint AS owner,ix.relkind,ix.relpersistence,ix.reloptions,a.amname,i.indnatts,i.indnkeyatts,i.indisunique,i.indnullsnotdistinct,i.indisprimary,i.indisexclusion,i.indimmediate,i.indisclustered,i.indisvalid,i.indcheckxmin,i.indisready,i.indislive,i.indisreplident,i.indkey::text,i.indcollation::text,i.indclass::text,i.indoption::text,
 pg_catalog.pg_get_expr(i.indexprs,i.indrelid,false) AS expressions,pg_catalog.pg_get_expr(i.indpred,i.indrelid,false) AS predicate,pg_catalog.pg_get_indexdef(ix.oid,0,false) AS definition
 FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_class ix ON ix.oid=i.indexrelid JOIN pg_catalog.pg_am a ON a.oid=ix.relam WHERE n.nspname=$1 AND c.relname=ANY($2::text[])) x`,
}

const abCatalogFunctionsQuery = `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.name,x.argtypes::text),'[]'::jsonb) FROM (
 SELECT p.proname AS name,l.lanname AS language,p.prokind,p.provolatile,p.proparallel,p.prosecdef,p.proisstrict,p.proleakproof,p.proretset,p.pronargs,p.pronargdefaults,p.proargtypes::oid[]::bigint[] AS argtypes,p.proallargtypes::bigint[] AS allargtypes,p.proargmodes,p.proargnames,p.prorettype::bigint AS result,p.proconfig,p.probin,p.prosupport::oid::bigint AS support,p.protrftypes::bigint[] AS transforms,p.procost,p.prorows
 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace JOIN pg_catalog.pg_language l ON l.oid=p.prolang WHERE n.nspname=$1 AND p.proname=ANY($2::text[])) x`
const abCatalogTriggersQuery = `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.name),'[]'::jsonb) FROM (
 SELECT c.relname AS table_name,t.tgname AS name,p.proname AS function,pn.nspname AS function_schema,p.proargtypes::text AS function_types,t.tgtype,t.tgenabled::text,t.tgparentid::bigint AS parent,t.tgconstrrelid=0 AS no_constraint_relation,t.tgconstrindid=0 AS no_constraint_index,t.tgargs,t.tgnargs,t.tgoldtable,t.tgnewtable,t.tgattr::text,t.tgqual::text,coalesce(k.conname,'') AS constraint_name,t.tgdeferrable,t.tginitdeferred,coalesce(k.contype::text,'') AS constraint_type,coalesce(kc.relname,'') AS constraint_table
 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid JOIN pg_catalog.pg_namespace pn ON pn.oid=p.pronamespace LEFT JOIN pg_catalog.pg_constraint k ON k.oid=t.tgconstraint LEFT JOIN pg_catalog.pg_class kc ON kc.oid=k.conrelid WHERE n.nspname=$1 AND NOT t.tgisinternal AND ($2::text[] IS NULL OR c.relname=ANY($2::text[]))) x`

// Reference DDL is assembled from the descriptor, independently of the target's
// relation definitions. Replaying the fixed historical bodies supplies original
// key/column ordering, so altered original tables and retained native FKs are
// checked as well as all 137 added constraints.
func abCatalogReference(ctx context.Context, tx pgx.Tx, b procurementattemptbirth.Bindings, r abCatalogRelation, creates map[string]string) (string, error) {
	schema := b.Schema + "_catalog"
	q := pgx.Identifier{schema}.Sanitize()
	if _, e := tx.Exec(ctx, "CREATE SCHEMA "+q+"; SET LOCAL search_path="+q+",pg_catalog; CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())"); e != nil {
		return "", e
	}
	manifest, e := migrations.Original22Manifest()
	if e != nil {
		return "", e
	}
	var entries []struct {
		Name string `json:"name"`
	}
	if e = json.Unmarshal(manifest, &entries); e != nil {
		return "", e
	}
	for _, item := range entries {
		body, e := migrations.Files.ReadFile(item.Name)
		if e != nil {
			return "", e
		}
		if _, e = tx.Exec(ctx, string(body)); e != nil {
			return "", fmt.Errorf("reference %s: %w", item.Name, e)
		}
	}
	bp, e := periodicbudgetprefixschema.SQL()
	if e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, bp); e != nil {
		return "", e
	}
	for _, body := range []string{procurementidentity.SQLTemplate(), procurementattemptrules.SQLTemplate()} {
		if _, e = tx.Exec(ctx, strings.ReplaceAll(body, "{{MONI_SCHEMA}}", q)); e != nil {
			return "", e
		}
	}
	for _, table := range r.Tables {
		var cols []string
		for _, col := range table.Columns {
			cols = append(cols, pgx.Identifier{col.Name}.Sanitize()+" "+col.Definition)
		}
		if _, e = tx.Exec(ctx, "CREATE TABLE "+pgx.Identifier{schema, table.Name}.Sanitize()+" ("+strings.Join(cols, ",")+")"); e != nil {
			return "", e
		}
	}
	for _, table := range r.OriginalExtensions {
		for _, col := range table.Columns {
			if _, e = tx.Exec(ctx, "ALTER TABLE "+pgx.Identifier{schema, table.Table}.Sanitize()+" ADD COLUMN "+pgx.Identifier{col.Name}.Sanitize()+" "+col.Definition); e != nil {
				return "", e
			}
		}
	}
	for _, k := range r.Constraints {
		def := strings.ReplaceAll(k.Definition, "{{MONI_SCHEMA}}", q)
		if _, e = tx.Exec(ctx, "ALTER TABLE "+pgx.Identifier{schema, k.Table}.Sanitize()+" ADD CONSTRAINT "+pgx.Identifier{k.Name}.Sanitize()+" "+def); e != nil {
			return "", fmt.Errorf("reference constraint %s: %w", k.Name, e)
		}
	}
	// Render again with the reference namespace, never rewrite literal text or
	// extract definitions from the installed target.
	ref := b
	ref.Schema = schema
	_, refCreates, e := abCatalogFunctionSources(ref)
	if e != nil {
		return "", e
	}
	names := make([]string, 0, len(creates))
	for name := range creates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, e = tx.Exec(ctx, refCreates[name]); e != nil {
			return "", fmt.Errorf("reference function %s: %w", name, e)
		}
	}
	full, e := procurementattemptbirth.SQL(ref)
	if e != nil {
		return "", e
	}
	stmts, e := abCatalogStatements(full)
	if e != nil {
		return "", e
	}
	for _, s := range stmts {
		i := strings.Index(s, "CREATE TRIGGER ")
		j := strings.Index(s, "CREATE CONSTRAINT TRIGGER ")
		if j >= 0 && (i < 0 || j < i) {
			i = j
		}
		if i >= 0 {
			if _, e = tx.Exec(ctx, s[i:]); e != nil {
				return "", fmt.Errorf("reference trigger: %w", e)
			}
		}
	}
	if _, e = tx.Exec(ctx, "SET LOCAL search_path=pg_catalog"); e != nil {
		return "", e
	}
	return schema, nil
}

// The grammar is deliberately limited to this frozen material's GRANT/REVOKE
// statements. A newly introduced ACL syntax fails the inventory test rather
// than silently weakening the effective-privilege matrix.
type abCatalogACL struct {
	table  map[string]bool
	column map[string]map[string]bool
}

func abCatalogNewACL() *abCatalogACL {
	return &abCatalogACL{map[string]bool{}, map[string]map[string]bool{}}
}
func (a *abCatalogACL) set(priv, col string, v bool) {
	if col == "" {
		a.table[priv] = v
		return
	}
	if a.column[col] == nil {
		a.column[col] = map[string]bool{}
	}
	a.column[col][priv] = v
}
func (a *abCatalogACL) has(priv, col string) bool {
	return a.table[priv] || col != "" && a.column[col][priv]
}
func abCatalogComma(s string) []string {
	depth := 0
	start := 0
	var out []string
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

var abCatalogTableACLRE = regexp.MustCompile(`(?s)^(GRANT|REVOKE) (.*?) ON TABLE (.*?) (?:TO|FROM) (.*?);$`)
var abCatalogFunctionACLRE = regexp.MustCompile(`(?s)^(GRANT|REVOKE) (?:ALL|EXECUTE) ON FUNCTION (.*?) (?:TO|FROM) (.*?);$`)
var abCatalogPrivilegeRE = regexp.MustCompile(`^(ALL|SELECT|INSERT|UPDATE|DELETE|TRUNCATE|REFERENCES|TRIGGER)(?:\(([^)]*)\))?$`)
var abCatalogAllTablePrivileges = []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"}
var abCatalogColumnPrivileges = []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"}
var abCatalogSystemColumns = []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"}

func abCatalogACLStatements(sql string) []string {
	var out []string
	for _, line := range strings.Split(sql, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "GRANT ") || strings.HasPrefix(line, "REVOKE ") {
			out = append(out, line)
		}
	}
	return out
}

type abCatalogACLExpected struct {
	tables         map[string]map[string]*abCatalogACL
	functions      map[string]map[string]bool
	functionOwners map[string]bool
	covered        map[string]bool
}

func abCatalogExpectedACL(b procurementattemptbirth.Bindings, c abCatalogDescriptor, columns map[string][]string) (*abCatalogACLExpected, error) {
	sql, e := procurementattemptbirth.Permissions(b)
	if e != nil {
		return nil, e
	}
	out := &abCatalogACLExpected{tables: map[string]map[string]*abCatalogACL{}, functions: map[string]map[string]bool{}, functionOwners: map[string]bool{}, covered: map[string]bool{}}
	roles := []string{b.App, b.Writer, "PUBLIC"}
	get := func(role, table string) *abCatalogACL {
		if out.tables[role] == nil {
			out.tables[role] = map[string]*abCatalogACL{}
		}
		a := out.tables[role][table]
		if a == nil {
			a = abCatalogNewACL()
			out.tables[role][table] = a
		}
		return a
	}
	// Fixed original ProvisionFor baseline for the source/parent relations in
	// scope. Candidate tables did not exist when broad original grants ran.
	for table := range columns {
		if strings.HasPrefix(table, "pp_") {
			continue
		}
		a := get(b.App, table)
		for _, p := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			a.set(p, "", true)
		}
	}
	immutable := "audit_events channel_versions channel_version_credentials route_policy_versions model_bindings metering_events finalization_records route_binding_isolations cost_versions credential_procurement_bindings procurement_budget_events procurement_commands"
	for _, table := range strings.Fields(immutable) {
		a := get(b.App, table)
		a.set("UPDATE", "", false)
		a.set("DELETE", "", false)
	}
	for _, table := range []string{"procurement_accounts", "procurement_reservations"} {
		get(b.App, table).set("DELETE", "", false)
	}
	for _, v := range []struct{ table, cols string }{{"request_records", "status billing_status latest_finalization_id"}, {"tenants", "name status allowed_regions price_group_id pricing_revision pricing_operation_id"}, {"settlements", "refunded_amount"}} {
		a := get(b.App, v.table)
		a.set("UPDATE", "", false)
		if v.table != "request_records" {
			a.set("DELETE", "", false)
		}
		for _, col := range strings.Fields(v.cols) {
			a.set("UPDATE", col, true)
		}
	}
	a := get(b.App, "payment_sandbox_environment")
	a.table = map[string]bool{"SELECT": true}
	a.column = map[string]map[string]bool{}
	pbMutable := map[string]string{"periodic_budget_controls": "enabled limit_amount configuration_operation_id revision held_amount spent_amount event_seq last_event_id max_observed_window_start", "periodic_budget_windows": "held_amount spent_amount revision last_event_id", "periodic_budget_allocations": "held_amount spent_amount source_held source_spent state revision last_event_id"}
	for _, table := range strings.Fields("periodic_budget_controls periodic_budget_windows periodic_budget_allocations periodic_budget_events periodic_budget_operations periodic_budget_configuration_versions") {
		a := get(b.App, table)
		a.table = map[string]bool{"SELECT": true, "INSERT": true}
		a.column = map[string]map[string]bool{}
		for _, col := range strings.Fields(pbMutable[table]) {
			a.set("UPDATE", col, true)
		}
	}
	for _, f := range c.Functions {
		sig := f.Name + "(" + strings.Join(f.Types, ",") + ")"
		out.functionOwners[sig] = true
	}
	prefix := pgx.Identifier{b.Schema}.Sanitize() + "."
	roleName := func(s string) string {
		s = strings.TrimSpace(s)
		for _, role := range roles {
			if s == role || s == (pgx.Identifier{role}).Sanitize() {
				return role
			}
		}
		return ""
	}
	for _, stmt := range abCatalogACLStatements(sql) {
		if m := abCatalogTableACLRE.FindStringSubmatch(stmt); m != nil {
			table := strings.TrimPrefix(m[3], prefix)
			table = strings.Trim(table, "\"")
			if table == m[3] || strings.ContainsAny(table, " .,") {
				return nil, fmt.Errorf("unsupported ACL relation: %s", stmt)
			}
			out.covered[table] = true
			for _, roleSQL := range abCatalogComma(m[4]) {
				role := roleName(roleSQL)
				if role == "" {
					return nil, fmt.Errorf("unknown ACL grantee")
				}
				a := get(role, table)
				for _, privSQL := range abCatalogComma(m[2]) {
					p := abCatalogPrivilegeRE.FindStringSubmatch(privSQL)
					if p == nil {
						return nil, fmt.Errorf("unsupported ACL privilege %s", privSQL)
					}
					ps := []string{p[1]}
					if p[1] == "ALL" {
						ps = abCatalogAllTablePrivileges
					}
					cols := []string{""}
					if p[2] != "" {
						cols = abCatalogComma(p[2])
					}
					for _, priv := range ps {
						for _, col := range cols {
							a.set(priv, strings.Trim(col, "\""), m[1] == "GRANT")
						}
					}
				}
			}
		} else if m := abCatalogFunctionACLRE.FindStringSubmatch(stmt); m != nil {
			sig := strings.TrimPrefix(m[2], prefix)
			if sig == m[2] {
				return nil, fmt.Errorf("function ACL namespace")
			}
			if out.functions[sig] == nil {
				out.functions[sig] = map[string]bool{}
			}
			for _, roleSQL := range abCatalogComma(m[3]) {
				role := roleName(roleSQL)
				if role == "" {
					return nil, fmt.Errorf("unknown function ACL grantee")
				}
				out.functions[sig][role] = m[1] == "GRANT"
			}
		} else if !strings.Contains(stmt, " ON SCHEMA ") {
			return nil, fmt.Errorf("unsupported fixed ACL statement: %s", stmt)
		}
	}
	// Canonical signatures from the descriptor use parser type names, whereas
	// GRANT text may use integer/bigint/boolean. Ownership is resolved by name.
	for sig, allowed := range out.functions {
		name := strings.SplitN(sig, "(", 2)[0]
		for _, f := range c.Functions {
			if f.Name == name {
				allowed[b.Writer] = true
				out.functionOwners[sig] = true
			}
		}
	}
	return out, nil
}

func abCatalogCheckRoles(ctx context.Context, db abCatalogDB, b procurementattemptbirth.Bindings) error {
	var ok bool
	e := db.QueryRow(ctx, `SELECT count(*)=2 AND bool_and(NOT rolsuper AND NOT rolinherit AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls AND ((oid=$1 AND rolname=$3 AND rolcanlogin) OR (oid=$2 AND rolname=$4 AND NOT rolcanlogin))) FROM pg_catalog.pg_roles WHERE oid IN ($1,$2)`, b.AppOID, b.WriterOID, b.App, b.Writer).Scan(&ok)
	if e != nil || !ok {
		return fmt.Errorf("A2 APP/W identity/attributes: %v", e)
	}
	var n int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_auth_members WHERE roleid IN ($1,$2) OR member IN ($1,$2)`, b.AppOID, b.WriterOID).Scan(&n); e != nil || n != 0 {
		return fmt.Errorf("A2 role membership bypass: %d %v", n, e)
	}
	for _, role := range []string{b.App, b.Writer} {
		var usage, create bool
		if e = db.QueryRow(ctx, `SELECT pg_catalog.has_schema_privilege($1,$2,'USAGE'),pg_catalog.has_schema_privilege($1,$2,'CREATE')`, role, b.Schema).Scan(&usage, &create); e != nil || !usage || create {
			return fmt.Errorf("A2 schema effective ACL %s: %v", role, e)
		}
	}
	return nil
}
func abCatalogCheckACL(ctx context.Context, db abCatalogDB, b procurementattemptbirth.Bindings, c abCatalogDescriptor, columns map[string][]string) error {
	ex, e := abCatalogExpectedACL(b, c, columns)
	if e != nil {
		return e
	}
	// W is checked on every fixed historical relation, including unrelated
	// payment/funding tables. APP checks cover all explicitly touched relations.
	var tables []string
	for table := range columns {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	rows, e := db.Query(ctx, `WITH roles AS (SELECT unnest($2::text[]) AS name), privs AS (SELECT unnest($4::text[]) AS privilege)
 SELECT r.name,c.relname,p.privilege,pg_catalog.has_table_privilege(r.name,c.oid,p.privilege),pg_catalog.has_table_privilege(r.name,c.oid,p.privilege||' WITH GRANT OPTION')
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace CROSS JOIN roles r CROSS JOIN privs p WHERE n.nspname=$1 AND c.relname=ANY($3::text[]) ORDER BY r.name,c.relname,p.privilege`, b.Schema, []string{b.App, b.Writer}, tables, abCatalogAllTablePrivileges)
	if e != nil {
		return e
	}
	count := 0
	for rows.Next() {
		var role, table, priv string
		var got, option bool
		if e = rows.Scan(&role, &table, &priv, &got, &option); e != nil {
			break
		}
		if role == b.App && !ex.covered[table] {
			continue
		}
		a := ex.tables[role][table]
		want := a != nil && a.has(priv, "")
		if got != want || option {
			e = fmt.Errorf("A2 table ACL %s %s/%s actual=%t grantable=%t want=%t", role, table, priv, got, option, want)
			break
		}
		count++
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return e
	}
	if count != (len(tables)+len(ex.covered))*len(abCatalogAllTablePrivileges) {
		return fmt.Errorf("A2 table ACL inventory incomplete: %d", count)
	}
	rows, e = db.Query(ctx, `WITH roles AS (SELECT unnest($2::text[]) AS name), privs AS (SELECT unnest($4::text[]) AS privilege)
 SELECT r.name,c.relname,a.attname,p.privilege,pg_catalog.has_column_privilege(r.name,c.oid,a.attnum,p.privilege),pg_catalog.has_column_privilege(r.name,c.oid,a.attnum,p.privilege||' WITH GRANT OPTION')
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid CROSS JOIN roles r CROSS JOIN privs p WHERE n.nspname=$1 AND c.relname=ANY($3::text[]) AND NOT a.attisdropped AND (a.attnum>0 OR (a.attnum<0 AND p.privilege='SELECT')) ORDER BY r.name,c.relname,a.attnum,p.privilege`, b.Schema, []string{b.App, b.Writer}, tables, abCatalogColumnPrivileges)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for rows.Next() {
		var role, table, col, priv string
		var got, option bool
		if e = rows.Scan(&role, &table, &col, &priv, &got, &option); e != nil {
			break
		}
		if role == b.App && !ex.covered[table] {
			continue
		}
		a := ex.tables[role][table]
		want := a != nil && a.has(priv, col)
		if got != want || option {
			e = fmt.Errorf("A2 column ACL %s %s.%s/%s actual=%t grantable=%t want=%t", role, table, col, priv, got, option, want)
			break
		}
		seen[role+"/"+table+"/"+col+"/"+priv] = true
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return e
	}
	for _, role := range []string{b.App, b.Writer} {
		for table, cols := range columns {
			if role == b.App && !ex.covered[table] {
				continue
			}
			for _, col := range cols {
				for _, priv := range abCatalogColumnPrivileges {
					if !seen[role+"/"+table+"/"+col+"/"+priv] {
						return fmt.Errorf("A2 ordinary column ACL missing %s.%s", table, col)
					}
				}
			}
			for _, col := range abCatalogSystemColumns {
				if !seen[role+"/"+table+"/"+col+"/SELECT"] {
					return fmt.Errorf("A2 system column ACL missing %s.%s", table, col)
				}
			}
		}
	}
	for sig, grants := range ex.functions {
		for _, role := range []string{b.App, b.Writer} {
			var got, option, public bool
			full := pgx.Identifier{b.Schema}.Sanitize() + "." + sig
			e = db.QueryRow(ctx, `SELECT pg_catalog.has_function_privilege($1,$2::regprocedure,'EXECUTE'),pg_catalog.has_function_privilege($1,$2::regprocedure,'EXECUTE WITH GRANT OPTION'),EXISTS(SELECT 1 FROM pg_catalog.pg_proc p CROSS JOIN LATERAL pg_catalog.aclexplode(coalesce(p.proacl,pg_catalog.acldefault('f',p.proowner))) a WHERE p.oid=$2::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE')`, role, full).Scan(&got, &option, &public)
			want := grants[role] || grants["PUBLIC"]
			wantOption := role == b.Writer && ex.functionOwners[sig]
			if e != nil || got != want || option != wantOption || public != grants["PUBLIC"] {
				return fmt.Errorf("A2 function ACL %s %s actual=%t grantable=%t public=%t want=%t: %v", role, sig, got, option, public, want, e)
			}
		}
	}
	// Functions created by O or W must not acquire global/default PUBLIC execute.
	var installer uint32
	if e = db.QueryRow(ctx, `SELECT current_user::regrole::oid`).Scan(&installer); e != nil {
		return e
	}
	for _, owner := range []uint32{installer, b.WriterOID} {
		var bad bool
		e = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(coalesce((SELECT d.defaclacl FROM pg_catalog.pg_default_acl d WHERE d.defaclrole=$1 AND d.defaclnamespace=0 AND d.defaclobjtype='f'),pg_catalog.acldefault('f',$1::oid))) a WHERE a.grantee<>$1::oid AND a.privilege_type='EXECUTE') OR EXISTS(SELECT 1 FROM pg_catalog.pg_default_acl d JOIN pg_catalog.pg_namespace n ON n.oid=d.defaclnamespace CROSS JOIN LATERAL pg_catalog.aclexplode(d.defaclacl) a WHERE d.defaclrole=$1 AND n.nspname=$2 AND d.defaclobjtype='f' AND a.grantee<>$1::oid AND a.privilege_type='EXECUTE')`, owner, b.Schema).Scan(&bad)
		if e != nil || bad {
			return fmt.Errorf("A2 default function ACL owner=%d: %v", owner, e)
		}
	}
	return nil
}

func abCatalogVerify(ctx context.Context, db abCatalogDB, b procurementattemptbirth.Bindings) error {
	r, c, e := abCatalogMaterials()
	if e != nil {
		return e
	}
	bodies, creates, e := abCatalogFunctionSources(b)
	if e != nil {
		return e
	}
	if e = abCatalogCheckRoles(ctx, db, b); e != nil {
		return e
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	ref, e := abCatalogReference(ctx, tx, b, r, creates)
	if e != nil {
		return fmt.Errorf("A2 independent reference: %w", e)
	}
	var tables []string
	for _, v := range r.Tables {
		tables = append(tables, v.Name)
	}
	for _, v := range r.OriginalExtensions {
		tables = append(tables, v.Table)
	}
	sort.Strings(tables)
	for group, q := range abCatalogRelationQueries {
		want, e := abCatalogJSON(ctx, tx, q, ref, tables)
		if e != nil {
			return fmt.Errorf("reference %s: %w", group, e)
		}
		got, e := abCatalogJSON(ctx, tx, q, b.Schema, tables)
		if e != nil {
			return e
		}
		if got != want {
			return fmt.Errorf("A2 %s drift\nactual=%s\nexpected=%s", group, got, want)
		}
	}
	var names []string
	for _, f := range c.Functions {
		names = append(names, f.Name)
		if _, ok := bodies[f.Name]; !ok {
			return fmt.Errorf("descriptor function missing %s", f.Name)
		}
	}
	want, e := abCatalogJSON(ctx, tx, abCatalogFunctionsQuery, ref, names)
	if e != nil {
		return e
	}
	got, e := abCatalogJSON(ctx, tx, abCatalogFunctionsQuery, b.Schema, names)
	if e != nil {
		return e
	}
	if got != want {
		return fmt.Errorf("A2 function signature/config/safety drift\nactual=%s\nexpected=%s", got, want)
	}
	rows, e := tx.Query(ctx, `SELECT p.proname,p.prosrc,p.proowner,l.lanname,p.prosecdef,p.provolatile::text,p.proparallel::text,ARRAY(SELECT pg_catalog.format_type(v,NULL) FROM unnest(p.proargtypes::oid[]) v) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace JOIN pg_catalog.pg_language l ON l.oid=p.prolang WHERE n.nspname=$1 AND p.proname=ANY($2::text[])`, b.Schema, names)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	byName := map[string]abCatalogFunction{}
	for _, f := range c.Functions {
		byName[f.Name] = f
	}
	for rows.Next() {
		var name, body, lang, vol, parallel string
		var owner uint32
		var definer bool
		var types []string
		if e = rows.Scan(&name, &body, &owner, &lang, &definer, &vol, &parallel, &types); e != nil {
			break
		}
		f := byName[name]
		if seen[name] || body != bodies[name] || owner != b.WriterOID || lang != f.Language || definer != f.Definer || vol != f.Volatility || parallel != f.Parallel {
			e = fmt.Errorf("A2 exact function source/authority %s", name)
			break
		}
		seen[name] = true
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return e
	}
	for _, f := range c.Functions {
		var match bool
		e = tx.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(ARRAY(SELECT unnest(p.proargtypes::oid[]))=ARRAY(SELECT v::regtype::oid FROM unnest($3::text[]) v)) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname=$2`, b.Schema, f.Name, f.Types).Scan(&match)
		if e != nil || !match {
			return fmt.Errorf("A2 descriptor function signature %s: %v", f.Name, e)
		}
	}
	if len(seen) != 38 {
		return fmt.Errorf("A2 candidate function count %d", len(seen))
	}
	// Independent identity/A0 are the only additional pp_ functions. Comparing
	// the complete inventory catches A1/account-only writers and overload mixing.
	inv := `SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.name,x.args),'[]'::jsonb) FROM (SELECT p.proname AS name,p.proargtypes::text AS args FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND left(p.proname,3)='pp_' AND ($2::text[] IS NULL)) x`
	want, e = abCatalogJSON(ctx, tx, inv, ref, nil)
	if e != nil {
		return e
	}
	got, e = abCatalogJSON(ctx, tx, inv, b.Schema, nil)
	if e != nil {
		return e
	}
	if got != want {
		return fmt.Errorf("A2 old/foreign pp writer mixed inventory: actual=%s expected=%s", got, want)
	}
	relinv := `SELECT coalesce(jsonb_agg(c.relname ORDER BY c.relname),'[]'::jsonb) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND left(c.relname,3)='pp_' AND c.relkind IN ('r','p','v','m','f') AND ($2::text[] IS NULL)`
	want, e = abCatalogJSON(ctx, tx, relinv, ref, nil)
	if e != nil {
		return e
	}
	got, e = abCatalogJSON(ctx, tx, relinv, b.Schema, nil)
	if e != nil {
		return e
	}
	if got != want {
		return fmt.Errorf("A2 relation inventory drift")
	}
	want, e = abCatalogJSON(ctx, tx, abCatalogTriggersQuery, ref, nil)
	if e != nil {
		return e
	}
	got, e = abCatalogJSON(ctx, tx, abCatalogTriggersQuery, b.Schema, nil)
	if e != nil {
		return e
	}
	if got != want {
		return fmt.Errorf("A2 trigger inventory/order/enabled/identity drift\nactual=%s\nexpected=%s", got, want)
	}
	for _, trigger := range c.Triggers {
		var count int
		e = tx.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid JOIN pg_catalog.pg_namespace pn ON pn.oid=p.pronamespace WHERE n.nspname=$1 AND c.relname=$2 AND t.tgname=$3 AND p.proname=$4 AND pn.nspname=$1 AND t.tgtype=$5 AND t.tgenabled::text=$6 AND t.tgdeferrable=$7 AND t.tginitdeferred=$8 AND (t.tgconstraint<>0)=$9 AND NOT t.tgisinternal`, b.Schema, trigger.Table, trigger.Name, trigger.Function, trigger.Type, trigger.Enabled, trigger.Deferrable, trigger.Initially, trigger.Constraint).Scan(&count)
		if e != nil || count != 1 {
			return fmt.Errorf("A2 descriptor trigger %s/%s: %v", trigger.Table, trigger.Name, e)
		}
	}
	columns := map[string][]string{}
	rows, e = tx.Query(ctx, `SELECT c.relname,a.attname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid WHERE n.nspname=$1 AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum`, ref)
	if e != nil {
		return e
	}
	for rows.Next() {
		var table, col string
		if e = rows.Scan(&table, &col); e != nil {
			break
		}
		columns[table] = append(columns[table], col)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return e
	}
	if e = abCatalogCheckACL(ctx, tx, b, c, columns); e != nil {
		return e
	}
	// Rolling back removes only the independently assembled reference namespace.
	// It never commits the caller's install or grants any runtime authority.
	return tx.Rollback(ctx)
}
func abCatalogCheck(t *testing.T, db abCatalogDB, b procurementattemptbirth.Bindings) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	must(t, "A2 complete fixed catalog proof (test only)", abCatalogVerify(ctx, db, b))
}
func TestA2CatalogIndependentInventory(t *testing.T) {
	r, c, e := abCatalogMaterials()
	must(t, "authenticate A2 fixed descriptors", e)
	b := procurementattemptbirth.Bindings{Schema: "ab_catalog_static", App: "ab_catalog_app", Writer: "ab_catalog_w", AppOID: 29001, WriterOID: 29002}
	bodies, creates, e := abCatalogFunctionSources(b)
	must(t, "parse exact fixed rendered function sources", e)
	if len(bodies) != 38 || len(creates) != 38 || len(c.Triggers) != 68 || len(r.Constraints) != 137 {
		t.Fatal("A2 fixed catalog coverage narrowed")
	}
	for _, f := range c.Functions {
		if bodies[f.Name] == "" {
			t.Fatalf("missing body %s", f.Name)
		}
	}
	cols := map[string][]string{}
	for _, v := range r.Tables {
		for _, col := range v.Columns {
			cols[v.Name] = append(cols[v.Name], col.Name)
		}
	}
	ex, e := abCatalogExpectedACL(b, c, cols)
	must(t, "parse fixed ACL grammar", e)
	if len(ex.covered) != 50 || len(ex.functions) < 80 {
		t.Fatalf("ACL coverage tables=%d functions=%d", len(ex.covered), len(ex.functions))
	}
	parts, e := abCatalogStatements("-- ;\nSELECT ';'; /* outer ; /* inner */ */ SELECT \";\"; DO $tag$BEGIN PERFORM ';'; END$tag$;")
	must(t, "quote-aware top-level splitter", e)
	if len(parts) != 3 {
		t.Fatalf("split statement count %d", len(parts))
	}
}
func TestPostgresA2CatalogCommitted(t *testing.T) {
	f := newFixture(t)
	abCatalogCheck(t, f.owner, f.b)
}
