package fundinglotbasis

import (
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func jsonObject(fields ...string) string {
	var b strings.Builder
	b.WriteString("pg_catalog.jsonb_build_object(")
	for i := 0; i < len(fields); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('\'')
		b.WriteString(fields[i])
		b.WriteString("',")
		b.WriteString(fields[i+1])
	}
	b.WriteString(")::pg_catalog.text")
	return b.String()
}
func boundedTexts(values ...string) string {
	var terms []string
	for _, v := range values {
		terms = append(terms, "("+v+" IS NULL OR pg_catalog.octet_length("+v+") OPERATOR(pg_catalog.<=) 65536)")
	}
	if len(terms) == 0 {
		return "true"
	}
	return strings.Join(terms, " AND ")
}

// Source fields are qualified before transmission. Prefix windows also enforce
// the remaining shared metadata allowance; rejected rows are never filtered.
func metadataSQL(marker, base string) string {
	// pgx simple protocol rejects unused arguments, including the schema binding
	// in pg_catalog-only queries. This predicate is not a row filter.
	return "/* funding_basis." + marker + ".v1 */ WITH base AS MATERIALIZED (" + base + "), ranked AS MATERIALIZED (SELECT *,pg_catalog.row_number() OVER(ORDER BY __position) AS __rn,pg_catalog.sum(pg_catalog.octet_length(__payload)::pg_catalog.int8) OVER(ORDER BY __position ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS __bytes FROM base), qualified AS MATERIALIZED (SELECT *,(__size AND __rn OPERATOR(pg_catalog.<=) $2::pg_catalog.int8 AND __bytes OPERATOR(pg_catalog.<=) $3::pg_catalog.int8) AS __qualified FROM ranked) SELECT (__sane AND $1::pg_catalog.text IS NOT NULL),__qualified,CASE WHEN __sane AND __qualified THEN __payload ELSE NULL END FROM qualified ORDER BY __position"
}
func targetNames(m *materials) string {
	names := make([]string, len(m.tables))
	for i, t := range m.tables {
		names[i] = "'" + t.name + "'"
	}
	return "ARRAY[" + strings.Join(names, ",") + "]::pg_catalog.text[]"
}
func oidList(ids []uint32) string {
	out := make([]string, len(ids))
	for i, v := range ids {
		out[i] = strconv.FormatUint(uint64(v), 10)
	}
	return strings.Join(out, ",")
}

func environmentSQL() string {
	payload := jsonObject("database_oid", "d.oid::pg_catalog.int8", "schema_oid", "n.oid::pg_catalog.int8", "server_version_num", "pg_catalog.current_setting('server_version_num')::pg_catalog.int8", "database_encoding", "d.encoding", "server_encoding", "pg_catalog.current_setting('server_encoding')", "client_encoding", "pg_catalog.current_setting('client_encoding')", "database_encoding_name", "pg_catalog.pg_encoding_to_char(d.encoding)", "provider", "d.datlocprovider::pg_catalog.text", "collate", "d.datcollate", "ctype", "d.datctype", "icu_locale", "d.daticulocale", "icu_rules", "d.daticurules", "collation_version", "d.datcollversion", "actual_collation_version", "pg_catalog.pg_database_collation_actual_version(d.oid)")
	size := boundedTexts("d.datcollate", "d.datctype", "d.daticulocale", "d.daticurules", "d.datcollversion", "pg_catalog.pg_database_collation_actual_version(d.oid)", "pg_catalog.current_setting('server_encoding')", "pg_catalog.current_setting('client_encoding')", "pg_catalog.current_setting('server_version_num')")
	return metadataSQL("environment", "SELECT 1::pg_catalog.int8 AS __position,true AS __sane,("+size+") AS __size,"+payload+" AS __payload FROM pg_catalog.pg_database AS d JOIN pg_catalog.pg_namespace AS n ON n.nspname OPERATOR(pg_catalog.=) $1::pg_catalog.text WHERE d.datname OPERATOR(pg_catalog.=) pg_catalog.current_database() LIMIT 2")
}
func relationsSQL(m *materials) string {
	payload := jsonObject("name", "c.relname::pg_catalog.text", "kind", "c.relkind::pg_catalog.text", "persistence", "c.relpersistence::pg_catalog.text", "access_method_oid", "c.relam::pg_catalog.int8", "row_security", "c.relrowsecurity", "force_row_security", "c.relforcerowsecurity", "partition", "c.relispartition", "inherits", "EXISTS(SELECT 1 FROM pg_catalog.pg_inherits AS h WHERE h.inhrelid OPERATOR(pg_catalog.=) c.oid OR h.inhparent OPERATOR(pg_catalog.=) c.oid)")
	return metadataSQL("relations", "SELECT pg_catalog.row_number() OVER(ORDER BY c.relname::pg_catalog.text COLLATE pg_catalog.\"C\") AS __position,true AS __sane,true AS __size,"+payload+" AS __payload FROM pg_catalog.pg_class AS c JOIN pg_catalog.pg_namespace AS n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace WHERE n.nspname OPERATOR(pg_catalog.=) $1::pg_catalog.text AND c.relname::pg_catalog.text OPERATOR(pg_catalog.=) ANY("+targetNames(m)+") ORDER BY c.relname::pg_catalog.text COLLATE pg_catalog.\"C\" LIMIT "+strconv.Itoa(m.profile.tables+1))
}
func attributesSQL(m *materials) string {
	payload := jsonObject("table", "c.relname::pg_catalog.text", "ordinal", "a.attnum", "name", "a.attname::pg_catalog.text", "oid", "a.atttypid::pg_catalog.int8", "typmod", "a.atttypmod", "not_null", "a.attnotnull", "collation_oid", "a.attcollation::pg_catalog.int8", "dropped", "a.attisdropped")
	return metadataSQL("attributes", "SELECT pg_catalog.row_number() OVER(ORDER BY c.relname::pg_catalog.text COLLATE pg_catalog.\"C\",a.attnum) AS __position,true AS __sane,true AS __size,"+payload+" AS __payload FROM pg_catalog.pg_class AS c JOIN pg_catalog.pg_namespace AS n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace JOIN pg_catalog.pg_attribute AS a ON a.attrelid OPERATOR(pg_catalog.=) c.oid WHERE n.nspname OPERATOR(pg_catalog.=) $1::pg_catalog.text AND c.relname::pg_catalog.text OPERATOR(pg_catalog.=) ANY("+targetNames(m)+") AND a.attnum OPERATOR(pg_catalog.>) 0 ORDER BY c.relname::pg_catalog.text COLLATE pg_catalog.\"C\",a.attnum LIMIT "+strconv.Itoa(m.profile.columns+2))
}
func primaryKeysSQL(m *materials) string {
	keys := "COALESCE((SELECT pg_catalog.jsonb_agg(a.attname::pg_catalog.text ORDER BY k.position) FROM pg_catalog.unnest(i.indkey::pg_catalog.int2[]) WITH ORDINALITY AS k(number,position) LEFT JOIN pg_catalog.pg_attribute AS a ON a.attrelid OPERATOR(pg_catalog.=) c.oid AND a.attnum OPERATOR(pg_catalog.=) k.number WHERE k.position OPERATOR(pg_catalog.<=) i.indnkeyatts),'[]'::pg_catalog.jsonb)"
	payload := jsonObject("table", "c.relname::pg_catalog.text", "key_count", "i.indnkeyatts", "unique", "i.indisunique", "valid", "i.indisvalid", "ready", "i.indisready", "live", "i.indislive", "immediate", "i.indimmediate", "plain", "i.indexprs IS NULL AND i.indpred IS NULL", "keys", keys)
	return metadataSQL("primary_keys", "SELECT pg_catalog.row_number() OVER(ORDER BY c.relname::pg_catalog.text COLLATE pg_catalog.\"C\",i.indexrelid) AS __position,true AS __sane,(i.indnkeyatts OPERATOR(pg_catalog.>) 0 AND i.indnkeyatts OPERATOR(pg_catalog.<=) 4 AND pg_catalog.cardinality(i.indkey::pg_catalog.int2[]) OPERATOR(pg_catalog.<=) 32) AS __size,"+payload+" AS __payload FROM pg_catalog.pg_class AS c JOIN pg_catalog.pg_namespace AS n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace JOIN pg_catalog.pg_index AS i ON i.indrelid OPERATOR(pg_catalog.=) c.oid WHERE n.nspname OPERATOR(pg_catalog.=) $1::pg_catalog.text AND c.relname::pg_catalog.text OPERATOR(pg_catalog.=) ANY("+targetNames(m)+") AND i.indisprimary ORDER BY c.relname::pg_catalog.text COLLATE pg_catalog.\"C\",i.indexrelid LIMIT "+strconv.Itoa(m.profile.tables+1))
}
func typesSQL(m *materials) string {
	ids := make([]uint32, len(m.send.Types))
	for i, t := range m.send.Types {
		ids[i] = t.OID
	}
	payload := jsonObject("oid", "t.oid::pg_catalog.int8", "name", "t.typname::pg_catalog.text", "namespace_oid", "t.typnamespace::pg_catalog.int8", "owner_oid", "t.typowner::pg_catalog.int8", "kind", "t.typtype::pg_catalog.text", "defined", "t.typisdefined", "length", "t.typlen", "by_value", "t.typbyval", "alignment", "t.typalign::pg_catalog.text", "storage", "t.typstorage::pg_catalog.text", "element_oid", "t.typelem::pg_catalog.int8", "base_oid", "t.typbasetype::pg_catalog.int8", "relation_oid", "t.typrelid::pg_catalog.int8", "collation_oid", "t.typcollation::pg_catalog.int8", "send_oid", "t.typsend::pg_catalog.oid::pg_catalog.int8")
	return metadataSQL("types", "SELECT t.oid::pg_catalog.int8 AS __position,true AS __sane,true AS __size,"+payload+" AS __payload FROM pg_catalog.pg_type AS t WHERE t.oid OPERATOR(pg_catalog.=) ANY(ARRAY["+oidList(ids)+"]::pg_catalog.oid[]) ORDER BY t.oid LIMIT 16")
}
func functionsSQL(m *materials) string {
	ids := make([]uint32, len(m.send.Functions))
	for i, f := range m.send.Functions {
		ids[i] = f.OID
	}
	nulls := []string{"p.proallargtypes", "p.proargmodes", "p.proargnames", "p.proargdefaults", "p.protrftypes", "p.probin", "p.prosqlbody", "p.proconfig", "p.proacl"}
	sane := make([]string, len(nulls))
	for i, v := range nulls {
		sane[i] = v + " IS NULL"
	}
	args := "(SELECT pg_catalog.jsonb_agg(k.value::pg_catalog.int8 ORDER BY k.position) FROM pg_catalog.unnest(p.proargtypes::pg_catalog.oid[]) WITH ORDINALITY AS k(value,position))"
	payload := jsonObject("oid", "p.oid::pg_catalog.int8", "name", "p.proname::pg_catalog.text", "namespace_oid", "p.pronamespace::pg_catalog.int8", "owner_oid", "p.proowner::pg_catalog.int8", "language_oid", "p.prolang::pg_catalog.int8", "kind", "p.prokind::pg_catalog.text", "security_definer", "p.prosecdef", "leakproof", "p.proleakproof", "strict", "p.proisstrict", "returns_set", "p.proretset", "volatility", "p.provolatile::pg_catalog.text", "parallel", "p.proparallel::pg_catalog.text", "cost_text", "p.procost::pg_catalog.text", "rows_text", "p.prorows::pg_catalog.text", "variadic_oid", "p.provariadic::pg_catalog.oid::pg_catalog.int8", "support_oid", "p.prosupport::pg_catalog.oid::pg_catalog.int8", "argument_count", "p.pronargs", "default_count", "p.pronargdefaults", "result_oid", "p.prorettype::pg_catalog.int8", "argument_type_oids", args, "all_argument_type_oids", "NULL", "argument_modes", "NULL", "argument_names", "NULL", "argument_defaults", "NULL", "transform_type_oids", "NULL", "source_symbol", "p.prosrc", "binary", "NULL", "sql_body", "NULL", "config", "NULL", "acl", "NULL")
	size := boundedTexts("p.prosrc", "p.procost::pg_catalog.text", "p.prorows::pg_catalog.text") + " AND pg_catalog.cardinality(p.proargtypes::pg_catalog.oid[]) OPERATOR(pg_catalog.<=) 4"
	return metadataSQL("functions", "SELECT p.oid::pg_catalog.int8 AS __position,("+strings.Join(sane, " AND ")+") AS __sane,("+size+") AS __size,"+payload+" AS __payload FROM pg_catalog.pg_proc AS p WHERE p.oid OPERATOR(pg_catalog.=) ANY(ARRAY["+oidList(ids)+"]::pg_catalog.oid[]) ORDER BY p.oid LIMIT 17")
}
func manifestSQL(schema string) string {
	payload := jsonObject("name", "s.name", "sha256", "s.checksum")
	return metadataSQL("manifest", "SELECT pg_catalog.row_number() OVER(ORDER BY s.name COLLATE pg_catalog.\"C\") AS __position,(s.name IS NOT NULL AND s.checksum IS NOT NULL) AS __sane,("+boundedTexts("s.name", "s.checksum")+") AS __size,"+payload+" AS __payload FROM ONLY "+pgx.Identifier{schema, "schema_migrations"}.Sanitize()+" AS s ORDER BY s.name COLLATE pg_catalog.\"C\" LIMIT 23")
}

func recordSQL(alias string, columns []column) string {
	items := make([]string, len(columns))
	for i, c := range columns {
		items[i] = alias + "." + pgx.Identifier{c.name}.Sanitize()
	}
	return "pg_catalog.record_send(ROW(" + strings.Join(items, ",") + "))"
}
func pageSQL(schema string, t table) string {
	k, r := recordSQL("s", t.pkColumns), recordSQL("s", t.columns)
	order := "pg_catalog.encode(k,'hex') COLLATE pg_catalog.\"C\""
	return "/* funding_basis.page.v1 */ WITH page AS MATERIALIZED (SELECT " + k + " AS k," + r + " AS r FROM ONLY " + pgx.Identifier{schema, t.name}.Sanitize() + " AS s WHERE (NOT $1::pg_catalog.bool OR pg_catalog.encode(" + k + ",'hex') COLLATE pg_catalog.\"C\" OPERATOR(pg_catalog.>) pg_catalog.encode($2::pg_catalog.bytea,'hex') COLLATE pg_catalog.\"C\") ORDER BY pg_catalog.encode(" + k + ",'hex') COLLATE pg_catalog.\"C\" LIMIT 128), sizes AS MATERIALIZED (SELECT *,pg_catalog.octet_length(k)::pg_catalog.int8 AS kl,pg_catalog.octet_length(r)::pg_catalog.int8 AS rl FROM page), ranked AS MATERIALIZED (SELECT *,pg_catalog.row_number() OVER(ORDER BY " + order + ") AS rn,pg_catalog.sum(kl OPERATOR(pg_catalog.+) rl) OVER(ORDER BY " + order + " ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS cumulative FROM sizes), qualified AS MATERIALIZED (SELECT *, (kl OPERATOR(pg_catalog.<=) 262144 AND rl OPERATOR(pg_catalog.<=) 1048576 AND rn OPERATOR(pg_catalog.<=) $3::pg_catalog.int8 AND cumulative OPERATOR(pg_catalog.<=) $4::pg_catalog.int8) AS q FROM ranked) SELECT q,CASE WHEN q THEN k ELSE NULL END,CASE WHEN q THEN r ELSE NULL END FROM qualified ORDER BY " + order
}
