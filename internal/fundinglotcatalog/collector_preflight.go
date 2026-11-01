package fundinglotcatalog

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// rawCatalogPreflight runs before any expression deparser. It reads only system
// metadata; pg_node_tree::text exposes the stored node text without evaluating
// constants. Empty categories are rejected before their deparse queries run.
func rawCatalogPreflight(ctx context.Context, tx pgx.Tx, b *collectorBudget, p collectorProfile) error {
	if rawCatalogBudgetTx(ctx, tx, b, p) != nil {
		return ErrCatalog
	}
	const q = `WITH rels AS (
 SELECT c.oid,c.reltoastrelid FROM pg_catalog.pg_class c WHERE c.relnamespace OPERATOR(pg_catalog.=) $1::pg_catalog.oid
 ), allrels AS (
 SELECT r.oid FROM rels r UNION ALL SELECT r.reltoastrelid FROM rels r WHERE r.reltoastrelid OPERATOR(pg_catalog.<>) 0
 UNION ALL SELECT i.indexrelid FROM pg_catalog.pg_index i JOIN rels r ON r.reltoastrelid OPERATOR(pg_catalog.=) i.indrelid
 ) SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_operator WHERE oprnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_cast c WHERE c.castsource OPERATOR(pg_catalog.=) ANY(SELECT oid FROM pg_catalog.pg_type WHERE typnamespace OPERATOR(pg_catalog.=) $1) OR c.casttarget OPERATOR(pg_catalog.=) ANY(SELECT oid FROM pg_catalog.pg_type WHERE typnamespace OPERATOR(pg_catalog.=) $1) OR c.castfunc OPERATOR(pg_catalog.=) ANY(SELECT oid FROM pg_catalog.pg_proc WHERE pronamespace OPERATOR(pg_catalog.=) $1))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_collation WHERE collnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_conversion WHERE connamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_opclass WHERE opcnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_opfamily WHERE opfnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_policy WHERE polrelid OPERATOR(pg_catalog.=) ANY(SELECT oid FROM allrels))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite WHERE ev_class OPERATOR(pg_catalog.=) ANY(SELECT oid FROM allrels))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_extension WHERE extnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_statistic_ext WHERE stxnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_ts_config WHERE cfgnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_ts_dict WHERE dictnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_ts_parser WHERE prsnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_ts_template WHERE tmplnamespace OPERATOR(pg_catalog.=) $1)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_class c WHERE c.oid OPERATOR(pg_catalog.=) ANY(SELECT oid FROM allrels) AND
 (pg_catalog.octet_length(c.relpartbound::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.octet_length(c.reloptions::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.cardinality(c.relacl) OPERATOR(pg_catalog.>) 256))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a WHERE a.attrelid OPERATOR(pg_catalog.=) ANY(SELECT oid FROM allrels) AND
 (a.attnum OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(a.attacl) OPERATOR(pg_catalog.>) 256 OR pg_catalog.octet_length(a.attoptions::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.octet_length(a.attfdwoptions::pg_catalog.text) OPERATOR(pg_catalog.>) 65536))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_attrdef d WHERE d.adrelid OPERATOR(pg_catalog.=) ANY(SELECT oid FROM allrels) AND pg_catalog.octet_length(d.adbin::pg_catalog.text) OPERATOR(pg_catalog.>) 65536)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_constraint k WHERE k.connamespace OPERATOR(pg_catalog.=) $1 AND
 (pg_catalog.octet_length(k.conbin::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.cardinality(k.conkey) OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(k.confkey) OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(k.confdelsetcols) OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(k.conpfeqop) OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(k.conppeqop) OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(k.conffeqop) OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(k.conexclop) OPERATOR(pg_catalog.>) 1600))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_index i WHERE i.indrelid OPERATOR(pg_catalog.=) ANY(SELECT oid FROM allrels) AND
 (pg_catalog.octet_length(i.indexprs::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.octet_length(i.indpred::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.cardinality(i.indkey::pg_catalog.int2[]) OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(i.indcollation::pg_catalog.oid[]) OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(i.indclass::pg_catalog.oid[]) OPERATOR(pg_catalog.>) 1600 OR pg_catalog.cardinality(i.indoption::pg_catalog.int2[]) OPERATOR(pg_catalog.>) 1600))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_trigger t WHERE t.tgrelid OPERATOR(pg_catalog.=) ANY(SELECT oid FROM allrels) AND
 (pg_catalog.octet_length(t.tgqual::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.octet_length(t.tgargs) OPERATOR(pg_catalog.>) 32768 OR t.tgnargs OPERATOR(pg_catalog.>) 100 OR pg_catalog.cardinality(t.tgattr::pg_catalog.int2[]) OPERATOR(pg_catalog.>) 1600))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_proc p WHERE p.pronamespace OPERATOR(pg_catalog.=) $1 AND
 (pg_catalog.octet_length(p.proargdefaults::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.octet_length(p.prosrc) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.octet_length(p.probin) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.octet_length(p.prosqlbody::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.octet_length(p.proconfig::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.cardinality(p.proargtypes::pg_catalog.oid[]) OPERATOR(pg_catalog.>) 100 OR pg_catalog.cardinality(p.proallargtypes) OPERATOR(pg_catalog.>) 100 OR pg_catalog.cardinality(p.proargnames) OPERATOR(pg_catalog.>) 100 OR pg_catalog.cardinality(p.proargmodes) OPERATOR(pg_catalog.>) 100 OR pg_catalog.cardinality(p.protrftypes) OPERATOR(pg_catalog.>) 100 OR pg_catalog.cardinality(p.proacl) OPERATOR(pg_catalog.>) 256 OR p.prokind OPERATOR(pg_catalog.<>) 'f'::pg_catalog."char"))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_type t WHERE t.typnamespace OPERATOR(pg_catalog.=) $1 AND
 (pg_catalog.octet_length(t.typdefaultbin::pg_catalog.text) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.octet_length(t.typdefault) OPERATOR(pg_catalog.>) 65536 OR pg_catalog.cardinality(t.typacl) OPERATOR(pg_catalog.>) 256))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid OPERATOR(pg_catalog.=) ANY(SELECT oid FROM allrels) AND i.inhseqno OPERATOR(pg_catalog.>) 1600)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_default_acl d WHERE (d.defaclnamespace OPERATOR(pg_catalog.=) $1 OR d.defaclrole OPERATOR(pg_catalog.=) ANY($2::pg_catalog.oid[])) AND pg_catalog.cardinality(d.defaclacl) OPERATOR(pg_catalog.>) 256)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.oid OPERATOR(pg_catalog.=) $1 AND pg_catalog.cardinality(n.nspacl) OPERATOR(pg_catalog.>) 256)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_roles r WHERE r.oid OPERATOR(pg_catalog.=) ANY($2::pg_catalog.oid[]) AND pg_catalog.octet_length(r.rolconfig::pg_catalog.text) OPERATOR(pg_catalog.>) 65536)`
	var bad bool
	if tx.QueryRow(ctx, q, p.schemaOID, p.roleOIDs).Scan(&bad) != nil || bad {
		return ErrCatalog
	}
	// Only PostgreSQL's automatic composite/array machinery is accepted before
	// deparsing any expression; arbitrary type I/O functions can execute code.
	const types = `SELECT t.oid,t.typtype::pg_catalog.text,t.typrelid,t.typelem,t.typinput::pg_catalog.oid,t.typoutput::pg_catalog.oid,t.typreceive::pg_catalog.oid,t.typsend::pg_catalog.oid,t.typmodin::pg_catalog.oid,t.typmodout::pg_catalog.oid,t.typanalyze::pg_catalog.oid,t.typsubscript::pg_catalog.oid,
 c.relnamespace,c.reltype,e.typnamespace
 FROM pg_catalog.pg_type t LEFT JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) t.typrelid LEFT JOIN pg_catalog.pg_type e ON e.oid OPERATOR(pg_catalog.=) t.typelem
 WHERE t.typnamespace OPERATOR(pg_catalog.=) $1::pg_catalog.oid LIMIT 100001`
	rows, e := tx.Query(ctx, types, p.schemaOID)
	if e != nil {
		return ErrCatalog
	}
	for rows.Next() {
		var oid, rel, elem, in, out, receive, send, modin, modout, analyze, subscript uint32
		var kind string
		var relns, reltype, elemns *uint32
		if rows.Scan(&oid, &kind, &rel, &elem, &in, &out, &receive, &send, &modin, &modout, &analyze, &subscript, &relns, &reltype, &elemns) != nil || b.take(1, 52) != nil {
			rows.Close()
			return ErrCatalog
		}
		if kind == "c" {
			if rel == 0 || relns == nil || *relns != p.schemaOID || reltype == nil || *reltype != oid || elem != 0 || in != 2290 || out != 2291 || receive != 2402 || send != 2403 || modin != 0 || modout != 0 || analyze != 0 || subscript != 0 {
				rows.Close()
				return ErrCatalog
			}
		} else if kind == "b" {
			if rel != 0 || elem == 0 || elemns == nil || (*elemns != 11 && *elemns != p.schemaOID) || (*elemns == 11 && !reviewedBuiltinTypes[elem]) || in != 750 || out != 751 || receive != 2400 || send != 2401 || modin != 0 || modout != 0 || analyze != 3816 || subscript != 6179 {
				rows.Close()
				return ErrCatalog
			}
		} else {
			rows.Close()
			return ErrCatalog
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return ErrCatalog
	}
	// Every deparsed object's type dependency must terminate at a reviewed PG16
	// builtin or one of the safe automatic types above. This precedes pg_get_expr.
	const deps = `WITH rels AS(SELECT oid FROM pg_catalog.pg_class WHERE relnamespace OPERATOR(pg_catalog.=) $1), owned(classid,objid) AS (
 SELECT 'pg_catalog.pg_class'::pg_catalog.regclass::pg_catalog.oid,oid FROM rels
 UNION ALL SELECT 'pg_catalog.pg_type'::pg_catalog.regclass::pg_catalog.oid,oid FROM pg_catalog.pg_type WHERE typnamespace OPERATOR(pg_catalog.=) $1
 UNION ALL SELECT 'pg_catalog.pg_proc'::pg_catalog.regclass::pg_catalog.oid,oid FROM pg_catalog.pg_proc WHERE pronamespace OPERATOR(pg_catalog.=) $1
 UNION ALL SELECT 'pg_catalog.pg_constraint'::pg_catalog.regclass::pg_catalog.oid,oid FROM pg_catalog.pg_constraint WHERE connamespace OPERATOR(pg_catalog.=) $1
 UNION ALL SELECT 'pg_catalog.pg_attrdef'::pg_catalog.regclass::pg_catalog.oid,oid FROM pg_catalog.pg_attrdef WHERE adrelid OPERATOR(pg_catalog.=) ANY(SELECT oid FROM rels)
 UNION ALL SELECT 'pg_catalog.pg_trigger'::pg_catalog.regclass::pg_catalog.oid,oid FROM pg_catalog.pg_trigger WHERE tgrelid OPERATOR(pg_catalog.=) ANY(SELECT oid FROM rels)
 ) SELECT DISTINCT t.oid,t.typnamespace FROM pg_catalog.pg_depend d JOIN owned o ON o.classid OPERATOR(pg_catalog.=) d.classid AND o.objid OPERATOR(pg_catalog.=) d.objid JOIN pg_catalog.pg_type t ON t.oid OPERATOR(pg_catalog.=) d.refobjid WHERE d.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_type'::pg_catalog.regclass::pg_catalog.oid LIMIT 100001`
	rows, e = tx.Query(ctx, deps, p.schemaOID)
	if e != nil {
		return ErrCatalog
	}
	for rows.Next() {
		var oid, ns uint32
		if rows.Scan(&oid, &ns) != nil || b.take(1, 8) != nil || ns != p.schemaOID && (ns != 11 || !reviewedBuiltinTypes[oid]) {
			rows.Close()
			return ErrCatalog
		}
	}
	rows.Close()
	if rows.Err() != nil || ctx.Err() != nil {
		return ErrCatalog
	}
	return nil
}

// Count raw catalog rows and stored variable bytes before any CTE can materialize
// a complete profile. LIMIT+1 exposes overflow rather than silently truncating.
func rawCatalogBudgetTx(ctx context.Context, tx pgx.Tx, b *collectorBudget, p collectorProfile) error {
	queries := []string{
		`SELECT COALESCE(pg_catalog.octet_length(c.relpartbound::pg_catalog.text),0)::pg_catalog.int8 OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(c.reloptions::pg_catalog.text),0) OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(c.relacl::pg_catalog.text),0) FROM pg_catalog.pg_class c WHERE c.relnamespace OPERATOR(pg_catalog.=) $1`,
		`SELECT COALESCE(pg_catalog.octet_length(a.attoptions::pg_catalog.text),0)::pg_catalog.int8 OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(a.attfdwoptions::pg_catalog.text),0) OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(a.attacl::pg_catalog.text),0) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) a.attrelid WHERE c.relnamespace OPERATOR(pg_catalog.=) $1 AND a.attnum OPERATOR(pg_catalog.>) 0`,
		`SELECT COALESCE(pg_catalog.octet_length(p.prosrc),0)::pg_catalog.int8 OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(p.probin),0) OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(p.prosqlbody::pg_catalog.text),0) OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(p.proargdefaults::pg_catalog.text),0) OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(p.proconfig::pg_catalog.text),0) OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(p.proargnames::pg_catalog.text),0) FROM pg_catalog.pg_proc p WHERE p.pronamespace OPERATOR(pg_catalog.=) $1`,
		`SELECT COALESCE(pg_catalog.octet_length(t.typdefaultbin::pg_catalog.text),0)::pg_catalog.int8 OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(t.typdefault),0) OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(t.typacl::pg_catalog.text),0) FROM pg_catalog.pg_type t WHERE t.typnamespace OPERATOR(pg_catalog.=) $1`,
		`SELECT COALESCE(pg_catalog.octet_length(k.conbin::pg_catalog.text),0)::pg_catalog.int8 FROM pg_catalog.pg_constraint k WHERE k.connamespace OPERATOR(pg_catalog.=) $1`,
		`SELECT COALESCE(pg_catalog.octet_length(t.tgqual::pg_catalog.text),0)::pg_catalog.int8 OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(t.tgargs),0) FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) t.tgrelid WHERE c.relnamespace OPERATOR(pg_catalog.=) $1`,
		`SELECT COALESCE(pg_catalog.octet_length(d.adbin::pg_catalog.text),0)::pg_catalog.int8 FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) d.adrelid WHERE c.relnamespace OPERATOR(pg_catalog.=) $1`,
		`SELECT COALESCE(pg_catalog.octet_length(i.indexprs::pg_catalog.text),0)::pg_catalog.int8 OPERATOR(pg_catalog.+) COALESCE(pg_catalog.octet_length(i.indpred::pg_catalog.text),0) FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) i.indrelid WHERE c.relnamespace OPERATOR(pg_catalog.=) $1`,
		`SELECT 0::pg_catalog.int8 FROM pg_catalog.pg_inherits i JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) i.inhrelid WHERE c.relnamespace OPERATOR(pg_catalog.=) $1`,
	}
	for _, q := range queries {
		rows, e := tx.Query(ctx, q+` LIMIT 100001`, p.schemaOID)
		if e != nil {
			return ErrCatalog
		}
		for rows.Next() {
			var n int64
			if rows.Scan(&n) != nil || n < 0 || n > collectorByteLimit || b.take(1, int(n)) != nil {
				rows.Close()
				return ErrCatalog
			}
		}
		rows.Close()
		if rows.Err() != nil || ctx.Err() != nil {
			return ErrCatalog
		}
	}
	return nil
}
