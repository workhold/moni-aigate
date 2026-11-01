package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	classRelation  uint32 = 1259
	classType      uint32 = 1247
	classFunction  uint32 = 1255
	classRole      uint32 = 1260
	classNamespace uint32 = 2615
)

func refSQL(class, expr string) string {
	return "jsonb_build_object('$ref',jsonb_build_array('" + class + "'::regclass::oid,(" + expr + ")::oid,0))"
}
func typeSQL(expr string) string { return "jsonb_build_object('$type'," + expr + ")" }

func (r *resolver) register(k refKey, v object) error {
	if k.oid == 0 {
		return errStage
	}
	if _, ok := r.refs[k]; ok {
		return errStage
	}
	b, e := canonical(v)
	if e != nil {
		return e
	}
	if r.reverse == nil {
		r.reverse = map[string]refKey{}
	}
	if _, ok := r.reverse[string(b)]; ok {
		return errStage
	}
	r.refs[k] = v
	r.reverse[string(b)] = k
	return nil
}

func (r *resolver) validateSymbols() error {
	reverse := map[string]refKey{}
	for k, v := range r.refs {
		b, e := canonical(v)
		if e != nil {
			return e
		}
		if old, ok := reverse[string(b)]; ok && old != k {
			return errStage
		}
		reverse[string(b)] = k
	}
	r.reverse = reverse
	return nil
}
func collect(ctx context.Context, tx pgx.Tx, q string, args ...any) ([]any, error) {
	var b []byte
	if tx.QueryRow(ctx, "SELECT coalesce(jsonb_agg(v),'[]'::jsonb) FROM ("+q+") AS rows(v)", args...).Scan(&b) != nil {
		return nil, fmt.Errorf("catalog_query: %w", errStage)
	}
	v, e := decode(b)
	if e != nil {
		return nil, fmt.Errorf("catalog_decode: %w", e)
	}
	a, ok := array(v)
	if !ok {
		return nil, errStage
	}
	return a, nil
}

func (r *resolver) reference(k refKey) (any, error) {
	if k.oid == 0 {
		if k.class == classRole {
			return object{"class": "role", "scope": "public", "name": "PUBLIC"}, nil
		}
		return nil, nil
	}
	if x, ok := r.refs[k]; ok {
		return x, nil
	}
	if k.sub != 0 {
		if k.class != classRelation {
			return nil, errStage
		}
		name, valid := r.attributes[k.oid][k.sub]
		if !valid {
			return nil, errStage
		}
		if x, ok := r.refs[refKey{k.class, k.oid, 0}]; ok {
			y := object{}
			for f, v := range x {
				y[f] = v
			}
			y["sub_id"], y["column"] = k.sub, name
			return y, nil
		}
		return nil, errStage
	}
	if x, ok := r.builtins[k]; ok {
		return x, nil
	}
	x, e := r.lookup(k)
	if e != nil {
		return nil, e
	}
	r.builtins[k] = x
	return x, nil
}

func (r *resolver) normal(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		if a, ok := x["$ref"]; ok {
			if len(x) != 1 {
				return nil, errStage
			}
			vs, ok := array(a)
			if !ok || len(vs) != 3 {
				return nil, errStage
			}
			var ns [3]uint32
			for i, v := range vs {
				n, ok := number(v)
				if !ok {
					return nil, errStage
				}
				ns[i] = n
			}
			return r.reference(refKey{ns[0], ns[1], ns[2]})
		}
		if n, ok := x["$type"]; ok {
			if len(x) != 1 {
				return nil, errStage
			}
			oid, ok := number(n)
			if !ok {
				return nil, errStage
			}
			if oid == 0 {
				return nil, nil
			}
			if t, ok := r.types[oid]; ok {
				return object{"object": t}, nil
			}
			b, e := r.reference(refKey{classType, oid, 0})
			if e != nil {
				return nil, e
			}
			return object{"namespace": "pg_catalog", "name": b.(object)["name"], "oid": oid}, nil
		}
		out := object{}
		for k, v := range x {
			if k == "proconfig" {
				n, e := r.config(v)
				if e != nil {
					return nil, e
				}
				out[k] = n
				continue
			}
			n, e := r.normal(v)
			if e != nil {
				return nil, e
			}
			if k == "entries" || k == "default_entries" {
				a, ok := n.([]any)
				if !ok || sortSet(a) != nil {
					return nil, errStage
				}
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			n, e := r.normal(v)
			if e != nil {
				return nil, e
			}
			out[i] = n
		}
		return out, nil
	case string:
		// Original function source is handled separately; other deparse fragments
		// must not be normalized with arbitrary replacements.
		for _, n := range r.names {
			if strings.Contains(x, n) {
				return nil, errStage
			}
		}
		return x, nil
	default:
		return v, nil
	}
}

func parsePath(s string) ([]string, error) {
	var out []string
	for {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, errStage
		}
		var word strings.Builder
		if s[0] == '"' {
			s = s[1:]
			closed := false
			for len(s) > 0 {
				c := s[0]
				s = s[1:]
				if c == '"' {
					if len(s) > 0 && s[0] == '"' {
						word.WriteByte('"')
						s = s[1:]
						continue
					}
					closed = true
					break
				}
				word.WriteByte(c)
			}
			if !closed {
				return nil, errStage
			}
		} else {
			i := 0
			for i < len(s) && ((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= '0' && s[i] <= '9') || s[i] == '_') {
				i++
			}
			if i == 0 {
				return nil, errStage
			}
			word.WriteString(s[:i])
			s = s[i:]
		}
		out = append(out, word.String())
		s = strings.TrimSpace(s)
		if s == "" {
			return out, nil
		}
		if s[0] != ',' {
			return nil, errStage
		}
		s = s[1:]
	}
}

func (r *resolver) config(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	a, ok := array(v)
	if !ok {
		return nil, errStage
	}
	out := make([]any, 0, len(a))
	for _, x := range a {
		s, ok := x.(string)
		if !ok {
			return nil, errStage
		}
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			return nil, errStage
		}
		if key != "search_path" {
			for _, n := range r.names {
				if strings.Contains(s, n) {
					return nil, errStage
				}
			}
			out = append(out, object{"key": key, "value": value})
			continue
		}
		path, e := parsePath(value)
		if e != nil || len(path) != 3 || path[0] != r.names[0] || path[1] != "pg_catalog" || path[2] != "pg_temp" {
			return nil, errStage
		}
		out = append(out, object{"key": key, "namespaces": []any{"application", "pg_catalog", "pg_temp"}})
	}
	return out, nil
}

func newResolver(ctx context.Context, tx pgx.Tx, p *profile) (*resolver, []uint32, error) {
	r := &resolver{refs: map[refKey]object{}, types: map[uint32]object{}, builtins: map[refKey]object{}, attributes: map[uint32]map[uint32]string{}, names: []string{p.schema, p.owner, p.app, p.worker, p.projection}}
	if r.register(refKey{classNamespace, p.schemaOID, 0}, object{"class": "namespace", "scope": "application", "name": "application"}) != nil {
		return nil, nil, errStage
	}
	for _, x := range []struct{ name, symbol string }{{p.owner, "application_owner"}, {p.app, "application"}, {p.worker, "worker"}, {p.projection, "projector"}} {
		if r.register(refKey{classRole, p.roleOIDs[x.name], 0}, object{"class": "role", "scope": "role", "name": x.symbol}) != nil {
			return nil, nil, errStage
		}
	}
	r.lookup = func(k refKey) (object, error) {
		var typ, identity string
		var ns, name *string
		if tx.QueryRow(ctx, `SELECT type,schema,name,identity FROM pg_catalog.pg_identify_object($1,$2,$3)`, k.class, k.oid, k.sub).Scan(&typ, &ns, &name, &identity) != nil {
			return nil, errStage
		}
		if ns != nil && *ns != "pg_catalog" {
			return nil, errStage
		}
		if ns == nil && k.class != 2612 && k.class != 2601 && k.class != 2615 && k.class != 1213 {
			return nil, errStage
		}
		if ns == nil && k.class == 2615 && (name == nil || *name != "pg_catalog" && *name != "pg_toast") {
			return nil, errStage
		}
		n := identity
		if name != nil {
			n = *name
		}
		return object{"class": typ, "scope": "pg_catalog", "name": n, "identity": identity, "oid": k.oid, "sub_id": k.sub}, nil
	}
	rows, e := tx.Query(ctx, `SELECT oid,relname,reltoastrelid FROM pg_catalog.pg_class WHERE relnamespace=$1`, p.schemaOID)
	if e != nil {
		return nil, nil, errStage
	}
	var rels []uint32
	toastOwners := map[uint32]string{}
	for rows.Next() {
		var oid, toast uint32
		var name string
		if rows.Scan(&oid, &name, &toast) != nil {
			rows.Close()
			return nil, nil, errStage
		}
		rels = append(rels, oid)
		if r.register(refKey{classRelation, oid, 0}, object{"class": "relation", "scope": "application", "name": name}) != nil {
			rows.Close()
			return nil, nil, errStage
		}
		if toast != 0 {
			if _, exists := toastOwners[toast]; exists {
				rows.Close()
				return nil, nil, errStage
			}
			toastOwners[toast] = name
		}
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, nil, errStage
	}
	rows.Close()
	for toast, owner := range toastOwners {
		if r.register(refKey{classRelation, toast, 0}, object{"class": "relation", "scope": "automatic", "name": "toast_heap", "owner": object{"class": "relation", "scope": "application", "name": owner}}) != nil {
			rows.Close()
			return nil, nil, errStage
		}
		rels = append(rels, toast)
		rows, e = tx.Query(ctx, `SELECT indexrelid FROM pg_catalog.pg_index WHERE indrelid=$1`, toast)
		if e != nil {
			return nil, nil, errStage
		}
		n := 0
		for rows.Next() {
			var oid uint32
			if rows.Scan(&oid) != nil {
				rows.Close()
				return nil, nil, errStage
			}
			n++
			rels = append(rels, oid)
			if r.register(refKey{classRelation, oid, 0}, object{"class": "relation", "scope": "automatic", "name": "toast_index", "owner": object{"class": "relation", "scope": "application", "name": owner}}) != nil {
				rows.Close()
				return nil, nil, errStage
			}
		}
		if rows.Err() != nil || n != 1 {
			rows.Close()
			return nil, nil, errStage
		}
		rows.Close()
	}
	rows, e = tx.Query(ctx, `SELECT attrelid,attnum,attname FROM pg_catalog.pg_attribute WHERE attrelid=ANY($1::oid[]) AND attnum>0 AND NOT attisdropped`, rels)
	if e != nil {
		return nil, nil, errStage
	}
	for rows.Next() {
		var rel, num uint32
		var name string
		if rows.Scan(&rel, &num, &name) != nil {
			rows.Close()
			return nil, nil, errStage
		}
		if r.attributes[rel] == nil {
			r.attributes[rel] = map[uint32]string{}
		}
		r.attributes[rel][num] = name
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, nil, errStage
	}
	rows.Close()
	rows, e = tx.Query(ctx, `SELECT oid,typname FROM pg_catalog.pg_type WHERE typnamespace=$1`, p.schemaOID)
	if e != nil {
		return nil, nil, errStage
	}
	for rows.Next() {
		var oid uint32
		var name string
		if rows.Scan(&oid, &name) != nil {
			rows.Close()
			return nil, nil, errStage
		}
		ref := object{"class": "type", "scope": "application", "name": name}
		if r.register(refKey{classType, oid, 0}, ref) != nil {
			rows.Close()
			return nil, nil, errStage
		}
		r.types[oid] = ref
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, nil, errStage
	}
	rows.Close()
	rows, e = tx.Query(ctx, `SELECT oid,proname,proargtypes::oid[] FROM pg_catalog.pg_proc WHERE pronamespace=$1`, p.schemaOID)
	if e != nil {
		return nil, nil, errStage
	}
	for rows.Next() {
		var oid uint32
		var name string
		var args []uint32
		if rows.Scan(&oid, &name, &args) != nil {
			rows.Close()
			return nil, nil, errStage
		}
		sig := make([]any, len(args))
		for i, a := range args {
			sig[i] = object{"$type": json.Number(strconv.FormatUint(uint64(a), 10))}
		}
		if r.register(refKey{classFunction, oid, 0}, object{"class": "function", "scope": "application", "name": name, "identity_arguments": sig}) != nil {
			rows.Close()
			return nil, nil, errStage
		}
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, nil, errStage
	}
	rows.Close()
	// Resolve type wrappers inside function identities after all user types exist.
	for k, v := range r.refs {
		if k.class == classFunction {
			n, e := r.normal(v["identity_arguments"])
			if e != nil {
				return nil, nil, e
			}
			v["identity_arguments"] = n
		}
	}
	queries := []struct {
		class uint32
		q     string
	}{
		{2606, `SELECT k.oid,c.relname||'/'||k.conname FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid WHERE k.connamespace=$1`},
		{2604, `SELECT d.oid,c.relname||'/'||d.adnum FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON c.oid=d.adrelid WHERE c.relnamespace=$1`},
		{2620, `SELECT t.oid,CASE WHEN t.tgisinternal THEN c.relname||'/'||k.conname||'/'||rc.relname||'/'||f.proname||'/'||t.tgtype ELSE c.relname||'/'||t.tgname END FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid JOIN pg_catalog.pg_proc f ON f.oid=t.tgfoid LEFT JOIN pg_catalog.pg_constraint k ON k.oid=t.tgconstraint LEFT JOIN pg_catalog.pg_class rc ON rc.oid=k.conrelid WHERE c.relnamespace=$1`},
	}
	for _, q := range queries {
		rows, e = tx.Query(ctx, q.q, p.schemaOID)
		if e != nil {
			return nil, nil, errStage
		}
		names := map[string]bool{}
		for rows.Next() {
			var oid uint32
			var name *string
			if rows.Scan(&oid, &name) != nil || name == nil || names[*name] {
				rows.Close()
				return nil, nil, errStage
			}
			names[*name] = true
			if r.register(refKey{q.class, oid, 0}, object{"class": map[uint32]string{2606: "constraint", 2604: "default", 2620: "trigger"}[q.class], "scope": "application", "name": *name}) != nil {
				rows.Close()
				return nil, nil, errStage
			}
		}
		if rows.Err() != nil {
			rows.Close()
			return nil, nil, errStage
		}
		rows.Close()
	}
	if r.validateSymbols() != nil {
		return nil, nil, errStage
	}
	return r, rels, nil
}
func normalizedSet(ctx context.Context, tx pgx.Tx, r *resolver, multiset bool, q string, args ...any) ([]any, error) {
	a, e := collect(ctx, tx, q, args...)
	if e != nil {
		return nil, e
	}
	return normalizeRecords(r, multiset, a)
}

func normalizeRecords(r *resolver, multiset bool, a []any) ([]any, error) {
	rawByNormalized := map[string]string{}
	for i, v := range a {
		var rawIdentity string
		if multiset {
			var e error
			rawIdentity, e = recordIdentity(v)
			if e != nil {
				return nil, e
			}
		}
		// prosrc is original SQL text, never normalized. Keep its exact bytes and
		// digest outside recursive string checks.
		var src any
		if m, ok := v.(object); ok {
			src = m["prosrc"]
			delete(m, "prosrc")
		}
		n, e := r.normal(v)
		if e != nil {
			return nil, fmt.Errorf("catalog_symbol: %w", e)
		}
		if multiset {
			k, e := recordIdentity(n)
			if e != nil {
				return nil, e
			}
			if old, ok := rawByNormalized[k]; ok && old != rawIdentity {
				return nil, errStage
			}
			rawByNormalized[k] = rawIdentity
		}
		a[i] = n
		if src != nil {
			m := n.(object)
			m["prosrc"] = src
			s, ok := src.(string)
			if !ok {
				return nil, errStage
			}
			m["source_sha256"] = digest([]byte(s))
		}
	}
	if multiset {
		return dependencyMultiset(a)
	}
	if sortRecords(a) != nil {
		return nil, fmt.Errorf("catalog_duplicate: %w", errStage)
	}
	return a, nil
}

func snapshot(ctx context.Context, pool *pgxpool.Pool, p *profile) (document, document, map[string]string, error) {
	tx, e := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, nil, nil, errStage
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		shared, sharedCancel := p.cleanupContext()
		defer sharedCancel()
		cleanup, cancel := context.WithTimeout(shared, 5*time.Second)
		defer cancel()
		if e := tx.Rollback(cleanup); e != nil && e != pgx.ErrTxClosed {
			// The transaction wrapper cannot return a non-idle backend to the
			// pool. Close the concrete connection under the same bounded cleanup.
			_ = tx.Conn().Close(cleanup)
		}
	}()
	r, rels, e := newResolver(ctx, tx, p)
	if e != nil {
		return nil, nil, nil, e
	}
	s := document{"format_version": 1}
	roles := document{"format_version": 1, "profile": "ordinary_app_acl", "worker_kind": "c0_placeholder"}
	if p.sandbox {
		roles["profile"] = "sandbox_app_acl"
		roles["worker_kind"] = "sandbox_runtime_role"
	}
	queries := schemaQueries()
	for _, q := range queries {
		a, e := normalizedSet(ctx, tx, r, q.name == "dependencies", q.sql, p.schemaOID, rels)
		if e != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", q.name, e)
		}
		s[q.name] = a
	}
	if len(s["namespace"].([]any)) != 1 {
		return nil, nil, nil, errStage
	}
	ids := map[string]string{}
	rows, e := tx.Query(ctx, `SELECT 'function/'||proname||'/'||proargtypes::text,oid::text||'/'||proowner::text||'/'||encode(sha256(convert_to(prosrc,'UTF8')),'hex') FROM pg_catalog.pg_proc WHERE pronamespace=$1 UNION ALL SELECT 'relation/'||relname,oid::text||'/'||relowner::text FROM pg_catalog.pg_class WHERE relnamespace=$1 UNION ALL SELECT 'type/'||typname,oid::text||'/'||typowner::text FROM pg_catalog.pg_type WHERE typnamespace=$1`, p.schemaOID)
	if e != nil {
		return nil, nil, nil, errStage
	}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			rows.Close()
			return nil, nil, nil, errStage
		}
		ids[k] = v
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, nil, nil, errStage
	}
	rows.Close()
	for _, q := range roleQueries(p) {
		a, e := normalizedSet(ctx, tx, r, q.name == "acl_dependencies", q.sql, p.schemaOID, rels, []uint32{p.roleOIDs[p.owner], p.roleOIDs[p.app], p.roleOIDs[p.worker], p.roleOIDs[p.projection]})
		if e != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", q.name, errStage)
		}
		roles[q.name] = a
	}
	if tx.Commit(ctx) != nil {
		return nil, nil, nil, errStage
	}
	committed = true
	return s, roles, ids, nil
}

type catalogQuery struct{ name, sql string }

func schemaQueries() []catalogQuery {
	R := refSQL
	T := typeSQL
	qs := []catalogQuery{
		{"namespace", `SELECT jsonb_build_object('identity',` + R("pg_namespace", "n.oid") + `,'owner',` + R("pg_authid", "n.nspowner") + `) FROM pg_catalog.pg_namespace n WHERE n.oid=$1 AND $2::oid[] IS NOT NULL`},
		{"relations", `SELECT jsonb_build_object('identity',` + R("pg_class", "c.oid") + `,'owner',` + R("pg_authid", "c.relowner") + `,'kind',c.relkind,'persistence',c.relpersistence,'am',` + R("pg_am", "c.relam") + `,'partition',c.relispartition,'partition_bound',pg_get_expr(c.relpartbound,c.oid),'partition_key',pg_get_partkeydef(c.oid),'row_security',c.relrowsecurity,'force_row_security',c.relforcerowsecurity,'replica_identity',c.relreplident,'options',c.reloptions,'tablespace',` + R("pg_tablespace", "c.reltablespace") + `,'toast',` + R("pg_class", "c.reltoastrelid") + `,'row_type',` + T("c.reltype") + `,'attributes',coalesce((SELECT jsonb_agg(jsonb_build_object('attnum',a.attnum,'name',a.attname,'type',` + T("a.atttypid") + `,'typmod',a.atttypmod,'collation',` + R("pg_collation", "a.attcollation") + `,'not_null',a.attnotnull,'dropped',a.attisdropped,'identity',a.attidentity,'generated',a.attgenerated,'storage',a.attstorage,'compression',a.attcompression,'statistics',a.attstattarget,'options',a.attoptions,'default',pg_get_expr(d.adbin,d.adrelid)) ORDER BY a.attnum) FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=c.oid AND a.attnum>0),'[]'::jsonb),'inherits',coalesce((SELECT jsonb_agg(` + R("pg_class", "i.inhparent") + ` ORDER BY i.inhseqno) FROM pg_inherits i WHERE i.inhrelid=c.oid),'[]'::jsonb)) FROM pg_class c WHERE c.oid=ANY($2::oid[]) AND $1::oid<>0`},
		{"types", `SELECT jsonb_build_object('identity',` + R("pg_type", "t.oid") + `,'owner',` + R("pg_authid", "t.typowner") + `,'type',t.typtype,'category',t.typcategory,'defined',t.typisdefined,'length',t.typlen,'by_value',t.typbyval,'alignment',t.typalign,'storage',t.typstorage,'preferred',t.typispreferred,'delimiter',t.typdelim,'relation',` + R("pg_class", "t.typrelid") + `,'element',` + T("t.typelem") + `,'array',` + T("t.typarray") + `,'base',` + T("t.typbasetype") + `,'typmod',t.typtypmod,'not_null',t.typnotnull,'dimensions',t.typndims,'collation',` + R("pg_collation", "t.typcollation") + `,'default',t.typdefault,'default_expression',pg_get_expr(t.typdefaultbin,0),'input',` + R("pg_proc", "t.typinput") + `,'output',` + R("pg_proc", "t.typoutput") + `,'receive',` + R("pg_proc", "t.typreceive") + `,'send',` + R("pg_proc", "t.typsend") + `,'typmod_in',` + R("pg_proc", "t.typmodin") + `,'typmod_out',` + R("pg_proc", "t.typmodout") + `,'analyze',` + R("pg_proc", "t.typanalyze") + `,'subscript',` + R("pg_proc", "t.typsubscript") + `,'enum_labels',coalesce((SELECT jsonb_agg(jsonb_build_object('sort_order',e.enumsortorder,'label',e.enumlabel) ORDER BY e.enumsortorder) FROM pg_enum e WHERE e.enumtypid=t.oid),'[]'::jsonb),'range',(SELECT jsonb_build_object('subtype',` + T("rg.rngsubtype") + `,'multirange',` + T("rg.rngmultitypid") + `,'collation',` + R("pg_collation", "rg.rngcollation") + `,'opclass',` + R("pg_opclass", "rg.rngsubopc") + `,'canonical',` + R("pg_proc", "rg.rngcanonical") + `,'subdiff',` + R("pg_proc", "rg.rngsubdiff") + `) FROM pg_range rg WHERE rg.rngtypid=t.oid)) FROM pg_type t WHERE t.typnamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"functions", `SELECT jsonb_build_object('identity',` + R("pg_proc", "p.oid") + `,'owner',` + R("pg_authid", "p.proowner") + `,'language',` + R("pg_language", "p.prolang") + `,'kind',p.prokind,'aggregate',(SELECT jsonb_build_object('kind',a.aggkind,'direct_args',a.aggnumdirectargs,'transition',` + R("pg_proc", "a.aggtransfn") + `,'final',` + R("pg_proc", "a.aggfinalfn") + `,'combine',` + R("pg_proc", "a.aggcombinefn") + `,'serial',` + R("pg_proc", "a.aggserialfn") + `,'deserialize',` + R("pg_proc", "a.aggdeserialfn") + `,'transition_type',` + T("a.aggtranstype") + `,'init',a.agginitval) FROM pg_aggregate a WHERE a.aggfnoid=p.oid),'volatility',p.provolatile,'strict',p.proisstrict,'definer',p.prosecdef,'leakproof',p.proleakproof,'retset',p.proretset,'parallel',p.proparallel,'cost',p.procost,'rows',p.prorows,'variadic',` + T("p.provariadic") + `,'support',` + R("pg_proc", "p.prosupport") + `,'result',` + T("p.prorettype") + `,'argument_types',coalesce((SELECT jsonb_agg(` + T("a.v") + ` ORDER BY a.n) FROM unnest(p.proargtypes::oid[]) WITH ORDINALITY a(v,n)),'[]'::jsonb),'all_argument_types',CASE WHEN p.proallargtypes IS NULL THEN NULL ELSE (SELECT jsonb_agg(` + T("a.v") + ` ORDER BY a.n) FROM unnest(p.proallargtypes) WITH ORDINALITY a(v,n)) END,'argument_names',p.proargnames,'argument_modes',p.proargmodes,'argument_defaults',pg_get_expr(p.proargdefaults,0),'default_count',p.pronargdefaults,'prosrc',p.prosrc,'probin',p.probin,'prosqlbody',p.prosqlbody::text,'proconfig',p.proconfig,'transform_types',CASE WHEN p.protrftypes IS NULL THEN NULL ELSE (SELECT jsonb_agg(` + T("a.v") + ` ORDER BY a.n) FROM unnest(p.protrftypes) WITH ORDINALITY a(v,n)) END) FROM pg_proc p WHERE p.pronamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"constraints", `SELECT jsonb_build_object('identity',` + R("pg_constraint", "k.oid") + `,'kind',k.contype,'relation',` + R("pg_class", "k.conrelid") + `,'domain',` + T("k.contypid") + `,'columns',k.conkey,'foreign_relation',` + R("pg_class", "k.confrelid") + `,'foreign_columns',k.confkey,'index',` + R("pg_class", "k.conindid") + `,'parent',` + R("pg_constraint", "k.conparentid") + `,'deferrable',k.condeferrable,'deferred',k.condeferred,'validated',k.convalidated,'local',k.conislocal,'inherit_count',k.coninhcount,'no_inherit',k.connoinherit,'update_action',k.confupdtype,'delete_action',k.confdeltype,'match',k.confmatchtype,'delete_set_columns',k.confdelsetcols,'expression',pg_get_expr(k.conbin,k.conrelid),'equality_operators',coalesce((SELECT jsonb_agg(` + R("pg_operator", "a.v") + ` ORDER BY a.n) FROM unnest(k.conpfeqop) WITH ORDINALITY a(v,n)),'[]'::jsonb),'pk_equality_operators',coalesce((SELECT jsonb_agg(` + R("pg_operator", "a.v") + ` ORDER BY a.n) FROM unnest(k.conppeqop) WITH ORDINALITY a(v,n)),'[]'::jsonb),'fk_equality_operators',coalesce((SELECT jsonb_agg(` + R("pg_operator", "a.v") + ` ORDER BY a.n) FROM unnest(k.conffeqop) WITH ORDINALITY a(v,n)),'[]'::jsonb)) FROM pg_constraint k WHERE k.connamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"indexes", `SELECT jsonb_build_object('identity',` + R("pg_class", "i.indexrelid") + `,'relation',` + R("pg_class", "i.indrelid") + `,'attribute_count',i.indnatts,'key_count',i.indnkeyatts,'unique',i.indisunique,'nulls_not_distinct',i.indnullsnotdistinct,'primary',i.indisprimary,'exclusion',i.indisexclusion,'immediate',i.indimmediate,'clustered',i.indisclustered,'valid',i.indisvalid,'check_xmin',i.indcheckxmin,'ready',i.indisready,'live',i.indislive,'replica_identity',i.indisreplident,'keys',i.indkey::smallint[],'options',i.indoption::smallint[],'collations',coalesce((SELECT jsonb_agg(` + R("pg_collation", "a.v") + ` ORDER BY a.n) FROM unnest(i.indcollation::oid[]) WITH ORDINALITY a(v,n)),'[]'::jsonb),'opclasses',coalesce((SELECT jsonb_agg(` + R("pg_opclass", "a.v") + ` ORDER BY a.n) FROM unnest(i.indclass::oid[]) WITH ORDINALITY a(v,n)),'[]'::jsonb),'expressions',pg_get_expr(i.indexprs,i.indrelid),'predicate',pg_get_expr(i.indpred,i.indrelid)) FROM pg_index i WHERE i.indrelid=ANY($2::oid[]) AND $1::oid<>0`},
		{"triggers", `SELECT jsonb_build_object('identity',` + R("pg_trigger", "t.oid") + `,'relation',` + R("pg_class", "t.tgrelid") + `,'function',` + R("pg_proc", "t.tgfoid") + `,'type',t.tgtype,'enabled',t.tgenabled,'internal',t.tgisinternal,'constraint',` + R("pg_constraint", "t.tgconstraint") + `,'constraint_relation',` + R("pg_class", "t.tgconstrrelid") + `,'constraint_index',` + R("pg_class", "t.tgconstrindid") + `,'deferrable',t.tgdeferrable,'deferred',t.tginitdeferred,'argument_count',t.tgnargs,'arguments',encode(t.tgargs,'hex'),'columns',t.tgattr::smallint[],'when',pg_get_expr(t.tgqual,t.tgrelid),'old_table',t.tgoldtable,'new_table',t.tgnewtable) FROM pg_trigger t WHERE t.tgrelid=ANY($2::oid[]) AND $1::oid<>0`},
	}
	owned := `WITH owned(classid,objid) AS (SELECT 'pg_namespace'::regclass::oid,$1::oid UNION ALL SELECT 'pg_class'::regclass::oid,unnest($2::oid[]) UNION ALL SELECT 'pg_type'::regclass::oid,oid FROM pg_type WHERE typnamespace=$1 UNION ALL SELECT 'pg_proc'::regclass::oid,oid FROM pg_proc WHERE pronamespace=$1 UNION ALL SELECT 'pg_constraint'::regclass::oid,oid FROM pg_constraint WHERE connamespace=$1 UNION ALL SELECT 'pg_trigger'::regclass::oid,oid FROM pg_trigger WHERE tgrelid=ANY($2::oid[]) UNION ALL SELECT 'pg_attrdef'::regclass::oid,oid FROM pg_attrdef WHERE adrelid=ANY($2::oid[])) `
	qs = append(qs, catalogQuery{"dependencies", owned + `SELECT jsonb_build_object('catalog','pg_depend','object',jsonb_build_object('$ref',jsonb_build_array(d.classid,d.objid,d.objsubid)),'referenced',jsonb_build_object('$ref',jsonb_build_array(d.refclassid,d.refobjid,d.refobjsubid)),'kind',d.deptype) FROM pg_depend d WHERE EXISTS(SELECT 1 FROM owned x WHERE (x.classid=d.classid AND x.objid=d.objid) OR (x.classid=d.refclassid AND x.objid=d.refobjid)) UNION ALL SELECT jsonb_build_object('catalog','pg_shdepend','object',jsonb_build_object('$ref',jsonb_build_array(d.classid,d.objid,d.objsubid)),'referenced',jsonb_build_object('$ref',jsonb_build_array(d.refclassid,d.refobjid,0)),'kind',d.deptype) FROM pg_shdepend d WHERE d.deptype<>'a' AND d.dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND EXISTS(SELECT 1 FROM owned x WHERE x.classid=d.classid AND x.objid=d.objid)`})
	// These categories must be empty for the fixed inputs, but are queried
	// explicitly. Do not silently filter unsupported objects from an artifact.
	empty := []catalogQuery{
		{"operators", `SELECT jsonb_build_object('name',oprname,'kind',oprkind,'owner',oprowner,'left',oprleft,'right',oprright,'result',oprresult,'function',oprcode) FROM pg_operator WHERE oprnamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"casts", `SELECT jsonb_build_object('source',castsource,'target',casttarget,'function',castfunc,'context',castcontext,'method',castmethod) FROM pg_cast WHERE (castsource IN (SELECT oid FROM pg_type WHERE typnamespace=$1) OR casttarget IN (SELECT oid FROM pg_type WHERE typnamespace=$1) OR castfunc IN (SELECT oid FROM pg_proc WHERE pronamespace=$1)) AND $2::oid[] IS NOT NULL`},
		{"collations", `SELECT jsonb_build_object('name',collname,'owner',collowner,'provider',collprovider,'encoding',collencoding,'deterministic',collisdeterministic,'collate',collcollate,'ctype',collctype,'locale',colliculocale,'version',collversion) FROM pg_collation WHERE collnamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"conversions", `SELECT jsonb_build_object('name',conname,'owner',conowner,'from',conforencoding,'to',contoencoding,'function',conproc,'default',condefault) FROM pg_conversion WHERE connamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"opclasses", `SELECT jsonb_build_object('name',opcname,'owner',opcowner,'method',opcmethod,'family',opcfamily,'input',opcintype,'key',opckeytype) FROM pg_opclass WHERE opcnamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"opfamilies", `SELECT jsonb_build_object('name',opfname,'owner',opfowner,'method',opfmethod) FROM pg_opfamily WHERE opfnamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"policies", `SELECT jsonb_build_object('name',polname,'relation',polrelid,'command',polcmd,'permissive',polpermissive,'roles',polroles,'using',pg_get_expr(polqual,polrelid),'check',pg_get_expr(polwithcheck,polrelid)) FROM pg_policy WHERE polrelid=ANY($2::oid[]) AND $1::oid<>0`},
		{"rules", `SELECT jsonb_build_object('name',rulename,'relation',ev_class,'event',ev_type,'enabled',ev_enabled,'instead',is_instead,'qual',ev_qual::text,'action',ev_action::text) FROM pg_rewrite WHERE ev_class=ANY($2::oid[]) AND $1::oid<>0`},
		{"extensions", `SELECT jsonb_build_object('name',extname,'owner',extowner,'version',extversion,'relocatable',extrelocatable,'config',extconfig,'condition',extcondition) FROM pg_extension WHERE extnamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"statistics", `SELECT jsonb_build_object('name',stxname,'owner',stxowner,'relation',stxrelid,'keys',stxkeys::smallint[],'kind',stxkind,'expressions',pg_get_expr(stxexprs,stxrelid)) FROM pg_statistic_ext WHERE stxnamespace=$1 AND $2::oid[] IS NOT NULL`},
		{"text_search", `SELECT jsonb_build_object('kind','config','name',cfgname,'owner',cfgowner,'parser',cfgparser) FROM pg_ts_config WHERE cfgnamespace=$1 AND $2::oid[] IS NOT NULL UNION ALL SELECT jsonb_build_object('kind','dictionary','name',dictname,'owner',dictowner,'template',dicttemplate,'options',dictinitoption) FROM pg_ts_dict WHERE dictnamespace=$1 UNION ALL SELECT jsonb_build_object('kind','parser','name',prsname,'start',prsstart,'token',prstoken,'end',prsend,'headline',prsheadline,'types',prslextype) FROM pg_ts_parser WHERE prsnamespace=$1 UNION ALL SELECT jsonb_build_object('kind','template','name',tmplname,'init',tmplinit,'lexize',tmpllexize) FROM pg_ts_template WHERE tmplnamespace=$1`},
	}
	qs = append(qs, empty...)
	return qs
}

func roleQueries(p *profile) []catalogQuery {
	R := refSQL
	base := []catalogQuery{
		{"roles", `SELECT jsonb_build_object('identity',` + R("pg_authid", "r.oid") + `,'login',r.rolcanlogin,'inherit',r.rolinherit,'superuser',r.rolsuper,'create_db',r.rolcreatedb,'create_role',r.rolcreaterole,'replication',r.rolreplication,'bypass_rls',r.rolbypassrls,'connection_limit',r.rolconnlimit,'valid_until',r.rolvaliduntil::text,'config',r.rolconfig) FROM pg_roles r WHERE r.oid=ANY($3::oid[]) AND $1::oid<>0 AND $2::oid[] IS NOT NULL`},
		{"memberships", `SELECT jsonb_build_object('role',` + R("pg_authid", "m.roleid") + `,'member',` + R("pg_authid", "m.member") + `,'grantor',` + R("pg_authid", "m.grantor") + `,'admin',m.admin_option,'inherit',m.inherit_option,'set',m.set_option) FROM pg_auth_members m WHERE (m.roleid=ANY($3::oid[]) OR m.member=ANY($3::oid[])) AND $1::oid<>0 AND $2::oid[] IS NOT NULL`},
	}
	for _, q := range []struct{ name, from, identity, acl, defaults, filter string }{
		{"schema_acl", "pg_namespace c", R("pg_namespace", "c.oid"), "c.nspacl", "acldefault('n',c.nspowner)", "c.oid=$1"},
		{"relation_acl", "pg_class c", R("pg_class", "c.oid"), "c.relacl", "CASE WHEN c.relkind='S' THEN acldefault('S',c.relowner) ELSE acldefault('r',c.relowner) END", "c.oid=ANY($2::oid[]) AND c.relkind IN ('r','p','v','m','f','S')"},
		{"column_acl", "pg_class c JOIN pg_attribute col ON col.attrelid=c.oid", `jsonb_build_object('$ref',jsonb_build_array('pg_class'::regclass::oid,c.oid,col.attnum))`, "col.attacl", "NULL::aclitem[]", "c.oid=ANY($2::oid[]) AND col.attnum>0 AND NOT col.attisdropped"},
		{"function_acl", "pg_proc c", R("pg_proc", "c.oid"), "c.proacl", "acldefault('f',c.proowner)", "c.pronamespace=$1"},
		{"type_acl", "pg_type c", R("pg_type", "c.oid"), "c.typacl", "acldefault('T',c.typowner)", "c.typnamespace=$1"},
	} {
		entries := func(expr string) string {
			return `coalesce((SELECT jsonb_agg(jsonb_build_object('grantor',` + R("pg_authid", "acl.grantor") + `,'grantee',` + R("pg_authid", "acl.grantee") + `,'privilege',acl.privilege_type,'grant_option',acl.is_grantable)) FROM aclexplode(` + expr + `) acl),'[]'::jsonb)`
		}
		base = append(base, catalogQuery{q.name, `SELECT jsonb_build_object('object',` + q.identity + `,'acl_is_null',(` + q.acl + `) IS NULL,'entries',` + entries(q.acl) + `,'default_entries',` + entries(q.defaults) + `) FROM ` + q.from + ` WHERE ` + q.filter + ` AND $1::oid<>0 AND $2::oid[] IS NOT NULL AND $3::oid[] IS NOT NULL`})
	}
	base = append(base, catalogQuery{"default_acl", `SELECT jsonb_build_object('owner',` + R("pg_authid", "d.defaclrole") + `,'namespace',` + R("pg_namespace", "d.defaclnamespace") + `,'object_type',d.defaclobjtype,'entries',coalesce((SELECT jsonb_agg(jsonb_build_object('grantor',` + R("pg_authid", "acl.grantor") + `,'grantee',` + R("pg_authid", "acl.grantee") + `,'privilege',acl.privilege_type,'grant_option',acl.is_grantable)) FROM aclexplode(d.defaclacl) acl),'[]'::jsonb)) FROM pg_default_acl d WHERE (d.defaclnamespace=$1 OR d.defaclrole=ANY($3::oid[])) AND $2::oid[] IS NOT NULL`})
	base = append(base, catalogQuery{"acl_dependencies", `SELECT jsonb_build_object('catalog','pg_shdepend','object',jsonb_build_object('$ref',jsonb_build_array(d.classid,d.objid,d.objsubid)),'role',` + R("pg_authid", "d.refobjid") + `,'kind',d.deptype) FROM pg_shdepend d WHERE d.deptype='a' AND d.dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND (d.refobjid=ANY($3::oid[]) OR d.objid=ANY($2::oid[]) OR (d.classid='pg_namespace'::regclass::oid AND d.objid=$1) OR (d.classid='pg_proc'::regclass::oid AND d.objid IN (SELECT oid FROM pg_proc WHERE pronamespace=$1)) OR (d.classid='pg_type'::regclass::oid AND d.objid IN (SELECT oid FROM pg_type WHERE typnamespace=$1)))`})
	// Registry order is explicit; no dynamically accepted privilege names.
	eff := `SELECT jsonb_build_object('role',` + R("pg_authid", "r.oid") + `,'kind','table','object',` + R("pg_class", "c.oid") + `,'privileges',to_jsonb(ARRAY(SELECT has_table_privilege(r.oid,c.oid,p) FROM unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) p)),'grant_options',to_jsonb(ARRAY(SELECT has_table_privilege(r.oid,c.oid,p||' WITH GRANT OPTION') FROM unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) p))) FROM pg_roles r CROSS JOIN pg_class c WHERE r.oid=ANY($3::oid[]) AND c.relnamespace=$1 AND c.relkind IN ('r','p','v','m','f','S') AND $2::oid[] IS NOT NULL
 UNION ALL SELECT jsonb_build_object('role',` + R("pg_authid", "r.oid") + `,'kind','column','object',jsonb_build_object('$ref',jsonb_build_array('pg_class'::regclass::oid,c.oid,a.attnum)),'privileges',to_jsonb(ARRAY(SELECT has_column_privilege(r.oid,c.oid,a.attnum,p) FROM unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) p)),'grant_options',to_jsonb(ARRAY(SELECT has_column_privilege(r.oid,c.oid,a.attnum,p||' WITH GRANT OPTION') FROM unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) p))) FROM pg_roles r CROSS JOIN pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE r.oid=ANY($3::oid[]) AND c.relnamespace=$1 AND c.relkind IN ('r','p','v','m','f') AND a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT jsonb_build_object('role',` + R("pg_authid", "r.oid") + `,'kind','function','object',` + R("pg_proc", "c.oid") + `,'privileges',jsonb_build_array(has_function_privilege(r.oid,c.oid,'EXECUTE')),'grant_options',jsonb_build_array(has_function_privilege(r.oid,c.oid,'EXECUTE WITH GRANT OPTION'))) FROM pg_roles r CROSS JOIN pg_proc c WHERE r.oid=ANY($3::oid[]) AND c.pronamespace=$1
 UNION ALL SELECT jsonb_build_object('role',` + R("pg_authid", "r.oid") + `,'kind','type','object',` + R("pg_type", "c.oid") + `,'privileges',jsonb_build_array(has_type_privilege(r.oid,c.oid,'USAGE')),'grant_options',jsonb_build_array(has_type_privilege(r.oid,c.oid,'USAGE WITH GRANT OPTION'))) FROM pg_roles r CROSS JOIN pg_type c WHERE r.oid=ANY($3::oid[]) AND c.typnamespace=$1
 UNION ALL SELECT jsonb_build_object('role',` + R("pg_authid", "r.oid") + `,'kind','schema','object',` + R("pg_namespace", "c.oid") + `,'privileges',jsonb_build_array(has_schema_privilege(r.oid,c.oid,'USAGE'),has_schema_privilege(r.oid,c.oid,'CREATE')),'grant_options',jsonb_build_array(has_schema_privilege(r.oid,c.oid,'USAGE WITH GRANT OPTION'),has_schema_privilege(r.oid,c.oid,'CREATE WITH GRANT OPTION'))) FROM pg_roles r CROSS JOIN pg_namespace c WHERE r.oid=ANY($3::oid[]) AND c.oid=$1`
	base = append(base, catalogQuery{"effective_privileges", eff})
	return base
}
