package fundinglotcatalog

import (
	"context"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	collectorRowLimit       = 100000
	collectorByteLimit      = 64 << 20
	collectorRecordLimit    = 64 << 10
	collectorAttributeLimit = 1600
	collectorArgumentLimit  = 100
	collectorACLLimit       = 256
)

type collectorBudget struct{ rows, bytes int }

func (b *collectorBudget) take(rows, bytes int) error {
	if rows < 0 || bytes < 0 || rows > collectorRowLimit-b.rows || bytes > collectorByteLimit-b.bytes {
		return ErrCatalog
	}
	b.rows += rows
	b.bytes += bytes
	return nil
}

type collectorProfile struct {
	scope, schema string
	schemaOID     uint32
	roleOIDs      []uint32
}

func catalogProfiles(p Pins) ([]collectorProfile, []struct {
	pin    RolePin
	symbol string
}) {
	roles := []struct {
		pin    RolePin
		symbol string
	}{{p.AppSchema.ObjectOwner, "application_owner"}, {p.App, "application"}, {p.Projector, "projector"}}
	profiles := []collectorProfile{{scope: "application", schema: p.AppSchema.Name, schemaOID: p.AppSchema.OID}}
	if p.Mode == Sandbox && p.Sandbox != nil && p.Worker != nil {
		roles = append(roles, struct {
			pin    RolePin
			symbol string
		}{*p.Worker, "worker"}, struct {
			pin    RolePin
			symbol string
		}{p.Sandbox.Mock.ObjectOwner, "mock_owner"}, struct {
			pin    RolePin
			symbol string
		}{p.Sandbox.MockRuntime, "mock_runtime"}, struct {
			pin    RolePin
			symbol string
		}{p.Sandbox.Receiver.ObjectOwner, "receiver_owner"}, struct {
			pin    RolePin
			symbol string
		}{p.Sandbox.ReceiverRuntime, "receiver_runtime"})
		profiles = append(profiles, collectorProfile{scope: "mock", schema: p.Sandbox.Mock.Name, schemaOID: p.Sandbox.Mock.OID}, collectorProfile{scope: "receiver", schema: p.Sandbox.Receiver.Name, schemaOID: p.Sandbox.Receiver.OID})
	}
	for i := range profiles {
		for _, r := range roles {
			profiles[i].roleOIDs = append(profiles[i].roleOIDs, r.pin.OID)
		}
	}
	return profiles, roles
}

// collectRows streams the outer catalog. Per-record child inputs in the static
// SQL registry are separately bounded; neither whole catalogs nor business rows
// are aggregated. A NULL guard result means server-side record size overflow.
func collectRows(ctx context.Context, tx pgx.Tx, b *collectorBudget, q string, limit int, args ...any) ([]any, error) {
	if limit < 0 || limit > collectorRowLimit {
		return nil, ErrCatalog
	}
	query := `SELECT CASE WHEN pg_catalog.octet_length(v::pg_catalog.text) OPERATOR(pg_catalog.<=) 65536 THEN v ELSE NULL END FROM (` + q + `) AS catalog_rows(v) LIMIT ` + strconv.Itoa(limit+1)
	rows, e := tx.Query(ctx, query, args...)
	if e != nil {
		return nil, ErrCatalog
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		if len(out) >= limit {
			return nil, ErrCatalog
		}
		var raw []byte
		if rows.Scan(&raw) != nil || raw == nil || len(raw) > collectorRecordLimit || !utf8.Valid(raw) || b.take(1, len(raw)) != nil {
			return nil, ErrCatalog
		}
		v, e := decodeCatalogJSON(raw)
		if e != nil {
			return nil, ErrCatalog
		}
		out = append(out, v)
	}
	rows.Close()
	if rows.Err() != nil || ctx.Err() != nil {
		return nil, ErrCatalog
	}
	return out, nil
}

// All catalog variable-size fields are checked in this caller snapshot before
// per-record aggregation. Each bounded child SELECT also retains LIMIT+1.
func preflightProfileTx(ctx context.Context, tx pgx.Tx, b *collectorBudget, p collectorProfile) error {
	return rawCatalogPreflight(ctx, tx, b, p)
}

func gatherRefs(v any, want map[refKey]bool) error {
	switch x := v.(type) {
	case object:
		if a, ok := x["$ref"]; ok {
			vs, ok := a.([]any)
			if !ok || len(vs) != 3 {
				return ErrCatalog
			}
			var ns [3]uint32
			for i, v := range vs {
				n, ok := number(v)
				if !ok {
					return ErrCatalog
				}
				ns[i] = n
			}
			if ns[1] != 0 {
				want[refKey{ns[0], ns[1], ns[2]}] = true
			}
			return nil
		}
		if a, ok := x["$type"]; ok {
			n, ok := number(a)
			if !ok {
				return ErrCatalog
			}
			if n != 0 {
				want[refKey{classType, n, 0}] = true
			}
			return nil
		}
		for _, v := range x {
			if gatherRefs(v, want) != nil {
				return ErrCatalog
			}
		}
	case []any:
		for _, v := range x {
			if gatherRefs(v, want) != nil {
				return ErrCatalog
			}
		}
	}
	return nil
}

func primeBuiltins(ctx context.Context, tx pgx.Tx, r *resolver, b *collectorBudget, expected *descriptorSet, values ...any) error {
	want := map[refKey]bool{}
	for _, v := range values {
		if gatherRefs(v, want) != nil {
			return ErrCatalog
		}
	}
	for _, v := range r.refs {
		if gatherRefs(v, want) != nil {
			return ErrCatalog
		}
	}
	var keys []refKey
	for k := range want {
		if _, ok := r.refs[k]; ok {
			continue
		}
		if k.sub != 0 {
			if k.class != classRelation {
				return ErrCatalog
			}
			if _, ok := r.attributes[k.oid][k.sub]; !ok {
				return ErrCatalog
			}
			if _, ok := r.refs[refKey{k.class, k.oid, 0}]; !ok {
				return ErrCatalog
			}
			continue
		}
		switch k.class {
		case 1247, 1255, 2612, 2601, 2615, 1213, 2617, 2616, 3456:
		default:
			return ErrCatalog
		}
		if k.class == classType {
			if !reviewedBuiltinTypes[k.oid] {
				return ErrCatalog
			}
		} else if _, ok := expectedBuiltinObject(expected, k); !ok {
			return ErrCatalog
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].class != keys[j].class {
			return keys[i].class < keys[j].class
		}
		return keys[i].oid < keys[j].oid
	})
	classes, oids, subs := make([]uint32, len(keys)), make([]uint32, len(keys)), make([]int32, len(keys))
	for i, k := range keys {
		classes[i], oids[i] = k.class, k.oid
	}
	if len(keys) > 0 {
		rows, e := tx.Query(ctx, `SELECT k.classid,k.objid,k.subid,i.type,i.schema,i.name,i.identity FROM ROWS FROM(pg_catalog.unnest($1::pg_catalog.oid[]),pg_catalog.unnest($2::pg_catalog.oid[]),pg_catalog.unnest($3::pg_catalog.int4[])) AS k(classid,objid,subid) CROSS JOIN LATERAL pg_catalog.pg_identify_object(k.classid,k.objid,k.subid) AS i`, classes, oids, subs)
		if e != nil {
			return ErrCatalog
		}
		for rows.Next() {
			var class, oid, sub uint32
			var typ, id string
			var ns, name *string
			if rows.Scan(&class, &oid, &sub, &typ, &ns, &name, &id) != nil || b.take(1, len(typ)+len(id)) != nil {
				rows.Close()
				return ErrCatalog
			}
			if ns != nil && *ns != "pg_catalog" {
				rows.Close()
				return ErrCatalog
			}
			if ns == nil && class != 2612 && class != 2601 && class != 2615 && class != 1213 {
				rows.Close()
				return ErrCatalog
			}
			if class == 2615 && ns == nil && (name == nil || *name != "pg_catalog" && *name != "pg_toast") {
				rows.Close()
				return ErrCatalog
			}
			n := id
			if name != nil {
				n = *name
			}
			k := refKey{class, oid, sub}
			if _, dup := r.builtins[k]; dup {
				rows.Close()
				return ErrCatalog
			}
			v := object{"class": typ, "scope": "pg_catalog", "name": n, "identity": id, "oid": oid, "sub_id": sub}
			if class == classType {
				if typ != "type" || n != reviewedBuiltinTypeNames[oid] {
					rows.Close()
					return ErrCatalog
				}
			} else {
				raw, e := canonical(v)
				want, ok := expectedBuiltinObject(expected, k)
				if e != nil || !ok || string(raw) != want {
					rows.Close()
					return ErrCatalog
				}
			}
			r.builtins[k] = v
		}
		rows.Close()
		if rows.Err() != nil || len(r.builtins) != len(keys) {
			return ErrCatalog
		}
	}
	r.lookup = func(refKey) (object, error) { return nil, ErrCatalog }
	for k, v := range r.refs {
		if k.class == classFunction {
			n, e := r.normal(v["identity_arguments"])
			if e != nil {
				return ErrCatalog
			}
			v["identity_arguments"] = n
		}
	}
	return r.validateSymbols()
}

func normalizeRecords(r *resolver, multiset bool, a []any) ([]any, error) {
	rawByNormalized := map[string]string{}
	byID := map[string]object{}
	counts := map[string]int{}
	var identities []string
	for i, v := range a {
		var rawID string
		var e error
		if multiset {
			rawID, e = recordIdentity(v)
			if e != nil {
				return nil, ErrCatalog
			}
		}
		var src any
		if m, ok := v.(object); ok {
			src = m["prosrc"]
			delete(m, "prosrc")
		}
		n, e := r.normal(v)
		if e != nil {
			return nil, ErrCatalog
		}
		if multiset {
			k, e := recordIdentity(n)
			if e != nil {
				return nil, ErrCatalog
			}
			if old, ok := rawByNormalized[k]; ok && old != rawID {
				return nil, ErrCatalog
			}
			rawByNormalized[k] = rawID
			m, ok := n.(object)
			if !ok {
				return nil, ErrCatalog
			}
			if old, exists := byID[k]; exists {
				if !same(old, m) {
					return nil, ErrCatalog
				}
			} else {
				byID[k] = m
				identities = append(identities, k)
			}
			counts[k]++
		}
		a[i] = n
		if src != nil {
			m := n.(object)
			text, ok := src.(string)
			if !ok {
				return nil, ErrCatalog
			}
			m["prosrc"], m["source_sha256"] = text, digest([]byte(text))
		}
	}
	if multiset {
		sort.Strings(identities)
		out := make([]any, 0, len(identities))
		for _, key := range identities {
			m := object{}
			for k, v := range byID[key] {
				m[k] = v
			}
			m["multiplicity"] = counts[key]
			out = append(out, m)
		}
		return out, nil
	}
	if sortRecords(a) != nil {
		return nil, ErrCatalog
	}
	return a, nil
}

// observeCatalogTx is intentionally package-private metadata capture, used by
// fixed offline test fixtures. It compares no expected and can issue no proof.
func observeCatalogTx(ctx context.Context, tx pgx.Tx, pins Pins) (*catalogSnapshot, error) {
	s, e := observeCatalogDocumentsTx(ctx, tx, pins)
	if e != nil {
		return nil, ErrCatalog
	}
	if validateCatalogDocuments(s.observedSchema, s.observedRoles) != nil {
		return nil, ErrCatalog
	}
	s.schemaCanonical, e = canonical(s.observedSchema)
	if e != nil {
		return nil, ErrCatalog
	}
	s.rolesCanonical, e = canonical(s.observedRoles)
	if e != nil || ctx.Err() != nil {
		return nil, ErrCatalog
	}
	return s, nil
}

// Public issuance compares normalized documents to the closed expected without serializing
// observed metadata. Canonical bytes come only from the matched immutable root.
func observeCatalogDocumentsTx(ctx context.Context, tx pgx.Tx, pins Pins) (*catalogSnapshot, error) {
	reviewed, _, e := loadExpected(pins)
	if e != nil {
		return nil, ErrCatalog
	}
	return observeCatalogProfileDocumentsTx(ctx, tx, pins, reviewed)
}

func observeCatalogProfileDocumentsTx(ctx context.Context, tx pgx.Tx, pins Pins, reviewed *descriptorSet) (*catalogSnapshot, error) {
	if ctx == nil || ctx.Err() != nil || !validPins(pins) || tx == nil {
		return nil, ErrCatalog
	}
	if reviewed == nil {
		return nil, ErrCatalog
	}
	profiles, roles := catalogProfiles(pins)
	b := &collectorBudget{}
	r := &resolver{refs: map[refKey]object{}, types: map[uint32]object{}, builtins: map[refKey]object{}, attributes: map[uint32]map[uint32]string{}, namespaces: map[string]string{}}
	for _, role := range roles {
		if r.register(refKey{classRole, role.pin.OID, 0}, object{"class": "role", "scope": "role", "name": role.symbol}) != nil {
			return nil, ErrCatalog
		}
	}
	if pins.AppSchema.NamespaceOwner.OID != pins.AppSchema.ObjectOwner.OID {
		if pins.AppSchema.Name != "public" || pins.AppSchema.NamespaceOwner.OID != 6171 {
			return nil, ErrCatalog
		}
		if r.register(refKey{classRole, 6171, 0}, object{"class": "role", "scope": "pg_catalog", "name": "pg_database_owner", "oid": uint32(6171)}) != nil {
			return nil, ErrCatalog
		}
	}
	allSchema, allRoles := document{}, document{}
	for _, p := range profiles {
		r.namespaces[p.schema] = p.scope
	}
	relations := map[string][]uint32{}
	for _, p := range profiles {
		if preflightProfileTx(ctx, tx, b, p) != nil {
			return nil, ErrCatalog
		}
		rels, e := registerProfileTx(ctx, tx, r, p, b)
		if e != nil {
			return nil, ErrCatalog
		}
		relations[p.scope] = rels
	}
	for _, p := range profiles {
		s := document{"format_version": 1}
		role := document{"format_version": 1, "profile": "ordinary_app_acl", "worker_kind": "absent"}
		if pins.Mode == Sandbox {
			role["profile"], role["worker_kind"] = "sandbox_app_acl", "sandbox_runtime_role"
		}
		for _, q := range schemaQueries() {
			bound, e := reviewed.categoryBound(p.scope, "schema", q.name)
			if e != nil {
				return nil, ErrCatalog
			}
			a, e := collectRows(ctx, tx, b, q.sql, bound.raw, p.schemaOID, relations[p.scope])
			if e != nil {
				return nil, ErrCatalog
			}
			s[q.name] = a
		}
		for _, q := range roleQueries() {
			bound, e := reviewed.categoryBound(p.scope, "roles", q.name)
			if e != nil {
				return nil, ErrCatalog
			}
			a, e := collectRows(ctx, tx, b, q.sql, bound.raw, p.schemaOID, relations[p.scope], p.roleOIDs)
			if e != nil {
				return nil, ErrCatalog
			}
			role[q.name] = a
		}
		allSchema[p.scope], allRoles[p.scope] = s, role
	}
	if primeBuiltins(ctx, tx, r, b, reviewed, allSchema, allRoles) != nil {
		return nil, ErrCatalog
	}
	for _, p := range profiles {
		for family, d := range map[string]document{"schema": allSchema[p.scope].(document), "roles": allRoles[p.scope].(document)} {
			for name, v := range d {
				a, ok := v.([]any)
				if !ok {
					continue
				}
				n, e := normalizeRecords(r, name == "dependencies" || name == "acl_dependencies", a)
				if e != nil {
					return nil, ErrCatalog
				}
				bound, e := reviewed.categoryBound(p.scope, family, name)
				if e != nil || len(n) > bound.normalized {
					return nil, ErrCatalog
				}
				d[name] = n
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ErrCatalog
	}
	return &catalogSnapshot{observedSchema: allSchema, observedRoles: allRoles}, nil
}

func collectTx(ctx context.Context, tx pgx.Tx, pins Pins, expected *descriptorSet) (*catalogSnapshot, error) {
	if expected == nil {
		return nil, ErrCatalog
	}
	s, e := observeCatalogProfileDocumentsTx(ctx, tx, pins, expected)
	if e != nil {
		return nil, ErrCatalog
	}
	schema, roles := any(s.observedSchema), any(s.observedRoles)
	if s.observedSchema == nil || s.observedRoles == nil {
		return nil, ErrCatalog
	}
	names, e := schemaIdentifiersTx(ctx, tx, pins)
	if e != nil {
		return nil, ErrCatalog
	}
	variants := expected.Variants
	if len(variants) == 0 {
		variants = []descriptorVariant{{Schema: expected.Schema, Roles: expected.Roles}}
	}
	var matched *descriptorVariant
	for i := range variants {
		v := &variants[i]
		if compareDescriptorValue(schema, v.Schema, names, "") == nil && compareDescriptorValue(roles, v.Roles, names, "") == nil {
			matched = v
			break
		}
	}
	if matched == nil {
		return nil, ErrCatalog
	}
	if verifyPeerRoleClosureTx(ctx, tx, pins) != nil {
		return nil, ErrCatalog
	}
	if len(matched.schemaCanonical) == 0 || len(matched.rolesCanonical) == 0 || ctx.Err() != nil {
		return nil, ErrCatalog
	}
	s.schemaCanonical, s.rolesCanonical = matched.schemaCanonical, matched.rolesCanonical
	return s, nil
}

func isDeparseField(k string) bool {
	switch k {
	case "default", "default_expression", "partition_bound", "partition_key", "argument_defaults", "expression", "expressions", "predicate", "when", "qual", "action":
		return true
	}
	return false
}
