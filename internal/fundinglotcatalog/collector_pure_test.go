package fundinglotcatalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogStrictJSONRejectsAmbiguousAndUnboundedInputs(t *testing.T) {
	bad := []string{`{"x":1,"x":2}`, `{"x":{"y":1,"y":2}}`, `{"x":1} {}`, `{"x":"\u0000"}`, `{"x":1e2}`, `{"x":-0}`, `{"x":1.0}`, `{"x":0.10}`, `{"x":01}`, strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65)}
	for _, raw := range bad {
		if _, e := strictJSON([]byte(raw)); e != ErrCatalog {
			t.Fatalf("ambiguous/unsupported JSON accepted: %.100s", raw)
		}
	}
	for _, raw := range []string{`0`, `-1`, `1.25`, `0.0001`, `{"a":[true,null,"value"]}`} {
		if _, e := strictJSON([]byte(raw)); e != nil {
			t.Fatalf("valid metadata rejected: %s", raw)
		}
	}
	if _, e := strictJSON(append([]byte(`{"x":"`), append(bytes.Repeat([]byte("x"), collectorRecordLimit+1), []byte(`"}`)...)...)); e != ErrCatalog {
		t.Fatal("oversized text accepted")
	}
	if _, e := strictJSONNodes([]byte(`[1,2,3]`), 3); e != ErrCatalog {
		t.Fatal("node limit exceeded")
	}
	if _, e := strictJSON([]byte{0xff}); e != ErrCatalog {
		t.Fatal("invalid UTF8 accepted")
	}
}

func TestCatalogClosedReferenceTypesAndCanonicalOIDs(t *testing.T) {
	valid := []object{
		{"class": "role", "scope": "public", "name": "PUBLIC"},
		{"class": "role", "scope": "role", "name": "application"},
		{"class": "role", "scope": "pg_catalog", "name": "pg_database_owner", "oid": json.Number("6171")},
		{"namespace": "pg_catalog", "name": reviewedBuiltinTypeNames[20], "oid": json.Number("20")},
		{"object": object{"class": "type", "scope": "application", "name": "fl_source_proof"}},
		{"class": "relation", "scope": "application", "name": "funding_lots", "column": "id", "sub_id": json.Number("1")},
	}
	for _, v := range valid {
		if validateReference(v) != nil {
			t.Fatalf("valid closed reference rejected: %v", v)
		}
	}
	bad := []object{
		{"class": "role", "scope": "public", "name": "other"},
		{"class": "role", "scope": "role", "name": "unexpected_runtime"},
		{"class": "role", "scope": "pg_catalog", "name": "pg_database_owner", "oid": json.Number("6172")},
		{"namespace": "pg_catalog", "name": "text", "oid": json.Number("20")},
		{"namespace": "pg_catalog", "name": "text", "oid": "25"},
		{"namespace": "pg_catalog", "name": "text", "oid": json.Number("2.5e1")},
		{"object": object{"class": "relation", "scope": "application", "name": "funding_lots"}},
		{"class": "relation", "scope": "application", "name": "funding_lots", "column": "id", "sub_id": json.Number("-0")},
		{"class": "function", "scope": "application", "name": "f", "identity_arguments": []any{object{"class": "role", "scope": "role", "name": "application"}}},
	}
	for _, v := range bad {
		if validateReference(v) != ErrCatalog {
			t.Fatalf("invalid reference accepted: %v", v)
		}
	}
	for _, v := range valid {
		clone := object{}
		for k, x := range v {
			clone[k] = x
		}
		clone["unknown"] = true
		if validateReference(clone) != ErrCatalog {
			t.Fatal("unknown nested ref key accepted")
		}
	}
}

func TestCatalogScalarContractsRejectReferenceSubstitution(t *testing.T) {
	ref := object{"class": "role", "scope": "role", "name": "application"}
	for _, typ := range []string{"text", "bool", "int", "decimal", "chars", "ints", "bools"} {
		if validateValueType(ref, typ) != ErrCatalog {
			t.Fatalf("reference substituted for %s", typ)
		}
	}
	acl := object{"grantor": ref, "grantee": object{"class": "role", "scope": "public", "name": "PUBLIC"}, "privilege": "EXECUTE", "grant_option": false}
	if validateACL(acl) != nil {
		t.Fatal("valid ACL rejected")
	}
	acl["unexpected"] = true
	if validateACL(acl) != ErrCatalog {
		t.Fatal("unknown ACL field accepted")
	}
	delete(acl, "unexpected")
	acl["privilege"] = "DROP"
	if validateACL(acl) != ErrCatalog {
		t.Fatal("unregistered ACL privilege accepted")
	}
	cfg := object{"key": "search_path", "namespaces": []any{"application", "pg_catalog", "pg_temp"}}
	if validateConfig(cfg) != nil {
		t.Fatal("valid symbolic path rejected")
	}
	cfg["value"] = "application"
	if validateConfig(cfg) != ErrCatalog {
		t.Fatal("mixed config variants accepted")
	}
	badRecord := object{"role": ref, "kind": "function", "object": ref, "privileges": []any{true}, "grant_options": []any{object{"unexpected": true}}}
	if validateRecordValues(badRecord, "effective_privileges") != ErrCatalog {
		t.Fatal("unknown privilege vector node accepted")
	}
}

func TestCatalogTemplatesUseOnlyRegisteredExpectedDeparseFields(t *testing.T) {
	template := object{"parts": []any{object{"literal": "CHECK ("}, object{"schema": "application"}, object{"literal": ".helper(value))"}}}
	names := map[string]string{"application": "simple_name", "mock": "\"Mixed Name\""}
	got, e := renderSQLTemplate(template, names, ".application.constraints..expression")
	if e != nil || got != "CHECK (simple_name.helper(value))" {
		t.Fatal("minimal quote_ident output was not retained")
	}
	for _, path := range []string{".application.functions..prosrc", ".application.functions..prosqlbody", ".application.relations..options", ".application.types..default"} {
		if _, e := renderSQLTemplate(template, names, path); e != ErrCatalog {
			t.Fatalf("template accepted outside deparse registry: %s", path)
		}
	}
	bad := []object{
		{"parts": []any{object{"literal": "x"}}},
		{"parts": []any{object{"literal": "x", "schema": "application"}}},
		{"parts": []any{object{"schema": "unknown"}}},
		{"parts": []any{object{"schema": "application", "unknown": true}}},
	}
	for _, v := range bad {
		if _, e := renderSQLTemplate(v, names, ".application.constraints..expression"); e != ErrCatalog {
			t.Fatal("invalid template accepted")
		}
	}
	if compareDescriptorValue("CHECK (simple_name.helper(value))", template, names, ".application.constraints..expression") != nil {
		t.Fatal("exact rendered expected rejected")
	}
	if compareDescriptorValue("CHECK (other.helper(value))", template, names, ".application.constraints..expression") != ErrCatalog {
		t.Fatal("actual SQL was rewritten or accepted loosely")
	}
}

func TestCatalogBudgetsRejectOverflowWithoutMutation(t *testing.T) {
	b := collectorBudget{}
	if b.take(collectorRowLimit, collectorByteLimit) != nil {
		t.Fatal("exact budget rejected")
	}
	before := b
	for _, pair := range [][2]int{{1, 0}, {0, 1}, {-1, 0}, {0, -1}} {
		if b.take(pair[0], pair[1]) != ErrCatalog || b != before {
			t.Fatal("budget overflow changed counters")
		}
	}
}

func TestCatalogCanonicalIdentityPreservesDependencyMultiplicity(t *testing.T) {
	dep := object{"catalog": "pg_depend", "kind": "n", "object": object{"class": "relation", "scope": "application", "name": "a"}, "referenced": object{"class": "relation", "scope": "application", "name": "b"}}
	got, e := dependencyMultiset([]any{dep, dep})
	if e != nil || len(got) != 1 || !same(got[0].(object)["multiplicity"], 2) {
		t.Fatal("dependency multiplicity lost")
	}
	collision := object{}
	for k, v := range dep {
		collision[k] = v
	}
	collision["extra"] = true
	if _, e := dependencyMultiset([]any{dep, collision}); e != ErrCatalog {
		t.Fatal("same identity with inconsistent body accepted")
	}
	r := &resolver{refs: map[refKey]object{}}
	if r.register(refKey{1259, 1, 0}, dep["object"].(object)) != nil {
		t.Fatal("register baseline")
	}
	if r.register(refKey{1259, 2, 0}, dep["object"].(object)) != ErrCatalog {
		t.Fatal("non-bijective reference accepted")
	}
}

func TestCatalogStaticQueriesBoundChildInputsAndQualifyOperators(t *testing.T) {
	for _, queries := range [][]catalogQuery{schemaQueries(), roleQueries()} {
		for _, q := range queries {
			if strings.Contains(q.sql, "jsonb_agg(") || strings.Contains(q.sql, " IN (") || strings.Contains(q.sql, "::pg_catalog.smallint") || strings.Contains(q.sql, "::pg_catalog.integer") {
				t.Fatalf("unbounded/implicit SQL form in %s", q.name)
			}
			if strings.Contains(q.sql, "pg_catalog.array_agg(") && !strings.Contains(q.sql, " LIMIT ") {
				t.Fatalf("child aggregate has no bounded input: %s", q.name)
			}
		}
	}
}

func TestCatalogExpectedEnvelopeRejectsUnknownAndMixedProfiles(t *testing.T) {
	for _, root := range []object{
		{"format_version": 1, "postgres_major": 16, "mode": "ordinary", "variants": []any{}, "schema": object{}},
		{"format_version": 1, "postgres_major": 16, "mode": "ordinary", "variants": []any{object{"name": "arbitrary", "schema": object{}, "roles": object{}}}},
		{"format_version": 1, "postgres_major": 16, "mode": "sandbox", "variants": []any{}, "unknown": true},
	} {
		raw, _ := canonical(root)
		if _, e := decodeExpected(raw, digest(raw), Ordinary); e != ErrCatalog {
			t.Fatal("mixed/unregistered envelope accepted")
		}
	}
	raw := []byte(`{"format_version":1,"postgres_major":16,"mode":"ordinary","variants":[]}`)
	if _, e := decodeExpected(raw, strings.Repeat("0", 64), Ordinary); e != ErrCatalog {
		t.Fatal("wrong SHA accepted")
	}
}

func TestCatalogOwnershipDependencyAndPrivilegeVectorContracts(t *testing.T) {
	ref := object{"class": "relation", "scope": "application", "name": "tenants"}
	owner := object{"class": "role", "scope": "role", "name": "application_owner"}
	dep := object{"catalog": "pg_shdepend", "kind": "o", "multiplicity": 1, "object": ref, "referenced": owner}
	if validateRecordValues(dep, "dependencies") != nil {
		t.Fatal("PG16 ownership dependency rejected")
	}
	dep["kind"] = "z"
	if validateRecordValues(dep, "dependencies") != ErrCatalog {
		t.Fatal("unknown dependency kind accepted")
	}
	for kind, n := range map[string]int{"table": 7, "column": 4, "function": 1, "type": 1, "schema": 2} {
		values := make([]any, n)
		for i := range values {
			values[i] = false
		}
		record := object{"role": owner, "kind": kind, "object": ref, "privileges": values, "grant_options": values}
		if validateRecordValues(record, "effective_privileges") != nil {
			t.Fatalf("exact %s vector rejected", kind)
		}
		record["grant_options"] = append(append([]any{}, values...), false)
		if validateRecordValues(record, "effective_privileges") != ErrCatalog {
			t.Fatalf("oversized %s vector accepted", kind)
		}
	}
	if !sameCatalogScalar(uint32(20), json.Number("20")) || !sameCatalogScalar(1, json.Number("1")) || sameCatalogScalar("20", json.Number("20")) || sameCatalogScalar(json.Number("2e1"), json.Number("20")) {
		t.Fatal("canonical scalar comparison is ambiguous")
	}
}

func TestCatalogEmbeddedExpectedRootsAreClosedAndCached(t *testing.T) {
	for _, mode := range []Mode{Ordinary, Sandbox} {
		first, root, e := loadExpected(Pins{Mode: mode})
		if e != nil || first == nil {
			t.Fatalf("reviewed embedded mode %d rejected", mode)
		}
		second, again, e := loadExpected(Pins{Mode: mode})
		if e != nil || first != second || root != again || first.FormatVersion != 1 || first.PostgresMajor != 16 {
			t.Fatal("immutable static cache did not reuse authenticated root")
		}
		if _, sha, e := expectedMetadata(Pins{Mode: mode}); e != nil || sha != root {
			t.Fatal("metadata disagrees with cached root")
		}
		if mode == Ordinary && len(first.Variants) != 5 || mode == Sandbox && (len(first.Variants) != 1 || first.Variants[0].Name != "full-sandbox") {
			t.Fatal("unregistered frozen variants")
		}
	}
}

func TestCatalogNormalizedMultisetReusesIdentityWithoutHidingAliases(t *testing.T) {
	r := &resolver{refs: map[refKey]object{
		{classRelation, 1, 0}: {"class": "relation", "scope": "application", "name": "a"},
		{classRelation, 2, 0}: {"class": "relation", "scope": "application", "name": "b"},
	}, attributes: map[uint32]map[uint32]string{}, builtins: map[refKey]object{}}
	raw := func(oid int, extra bool) object {
		m := object{"catalog": "pg_depend", "kind": "n", "object": object{"$ref": []any{1259, oid, 0}}, "referenced": object{"$ref": []any{1259, 2, 0}}}
		if extra {
			m["unexpected"] = "different body"
		}
		return m
	}
	normalized, e := normalizeRecords(r, true, []any{raw(1, false), raw(1, false)})
	if e != nil || len(normalized) != 1 || !same(normalized[0].(object)["multiplicity"], 2) {
		t.Fatal("optimized multiset lost exact multiplicity")
	}
	if _, e := normalizeRecords(r, true, []any{raw(1, false), raw(1, true)}); e != ErrCatalog {
		t.Fatal("same identity with different normalized body accepted")
	}
	// Deliberately bypass register in this internal negative fixture. Even a
	// corrupt symbol map must not collapse two distinct raw object identities.
	r.refs[refKey{classRelation, 3, 0}] = r.refs[refKey{classRelation, 1, 0}]
	if _, e := normalizeRecords(r, true, []any{raw(1, false), raw(3, false)}); e != ErrCatalog {
		t.Fatal("two raw identities collapsed into a trusted dependency")
	}
}

func TestCatalogOwnedNormalizationPreservesBorrowedSymbolsAndLiteralBytes(t *testing.T) {
	symbol := object{"class": "relation", "scope": "application", "name": "tenants"}
	r := &resolver{refs: map[refKey]object{{classRelation, 1, 0}: symbol}, attributes: map[uint32]map[uint32]string{1: {1: "id"}}, builtins: map[refKey]object{}}
	before, _ := canonical(symbol)
	ownedArray := []any{object{"$ref": []any{1259, 1, 0}}}
	literal := "raw_schema_name.literal('raw_schema_name')"
	raw := object{"identity": object{"$ref": []any{1259, 1, 0}}, "column_ref": object{"$ref": []any{1259, 1, 1}}, "attributes": ownedArray, "literal": literal}
	normalized, e := r.normal(raw)
	if e != nil {
		t.Fatal("owned raw normalization failed")
	}
	m := normalized.(object)
	raw["owned_marker"] = true
	if m["owned_marker"] != true {
		t.Fatal("owned map was copied")
	}
	ownedArray[0] = "owned array marker"
	if m["attributes"].([]any)[0] != "owned array marker" {
		t.Fatal("owned array was copied")
	}
	if m["literal"] != literal {
		t.Fatal("literal bytes were rewritten")
	}
	m["column_ref"].(object)["column"] = "private change"
	after, _ := canonical(symbol)
	if !bytes.Equal(before, after) {
		t.Fatal("borrowed relation symbol was changed through column extension")
	}
	d, _, e := loadExpected(Pins{Mode: Ordinary})
	if e != nil {
		t.Fatal("static expected unavailable")
	}
	for _, variant := range d.Variants {
		if len(variant.schemaCanonical) == 0 || len(variant.rolesCanonical) == 0 {
			t.Fatal("canonical bytes were not cached outside issuing budget")
		}
	}
}

func TestCatalogExpectedCategoryInterningUsesCompleteContent(t *testing.T) {
	d, root, e := loadExpected(Pins{Mode: Ordinary})
	if e != nil {
		t.Fatal("reviewed expected unavailable")
	}
	first := d.Variants[0].Roles["application"].(object)
	next := d.Variants[1].Roles["application"].(object)
	a := first["effective_privileges"].([]any)
	b := next["effective_privileges"].([]any)
	if len(a) == 0 || len(b) == 0 || !same(a, b) || &a[0] != &b[0] {
		t.Fatal("complete equal category content was not shared")
	}
	ownersA := first["roles"].([]any)
	ownersB := next["roles"].([]any)
	if same(ownersA, ownersB) || &ownersA[0] == &ownersB[0] {
		t.Fatal("different owner flag category was merged by name")
	}
	if root != ordinaryExpectedSHA || digest(ordinaryExpectedJSON) != ordinaryExpectedSHA {
		t.Fatal("static root bytes changed while interning decoded metadata")
	}
}

func TestCatalogCompleteComparisonRejectsMalformedActualAgainstClosedExpected(t *testing.T) {
	d, _, e := loadExpected(Pins{Mode: Ordinary})
	if e != nil {
		t.Fatal("closed expected unavailable")
	}
	variant := d.Variants[0]
	schema := variant.Schema["application"].(object)
	roles := variant.Roles["application"].(object)
	clone := func(v any) object {
		raw, e := canonical(v)
		if e != nil {
			t.Fatal("actual negative fixture encode")
		}
		x, e := strictJSON(raw)
		if e != nil {
			t.Fatal("actual negative fixture parse")
		}
		return x.(object)
	}
	namespace := schema["namespace"].([]any)[0].(object)
	role := roles["roles"].([]any)[0].(object)
	function := schema["functions"].([]any)[0].(object)
	cases := []struct {
		expected object
		mutate   func(object)
	}{
		{namespace, func(m object) { m["unknown"] = true }},
		{namespace, func(m object) { m["identity"].(object)["scope"] = "mock" }},
		{role, func(m object) { m["connection_limit"] = "-1" }},
		{role, func(m object) { m["login"] = "false" }},
		{function, func(m object) { m["kind"] = "a" }},
		{function, func(m object) { m["prosrc"] = object{"parts": []any{object{"schema": "application"}}} }},
	}
	for _, tc := range cases {
		actual := clone(tc.expected)
		tc.mutate(actual)
		if compareDescriptorValue(actual, tc.expected, map[string]string{"application": "a"}, "") != ErrCatalog {
			t.Fatal("malformed actual escaped complete closed expected comparison")
		}
	}
	for _, value := range schema["functions"].([]any) {
		fn := value.(object)
		configs, ok := fn["proconfig"].([]any)
		if !ok || len(configs) == 0 {
			continue
		}
		actual := clone(fn)
		actual["proconfig"].([]any)[0].(object)["unknown"] = true
		if compareDescriptorValue(actual, fn, nil, "") != ErrCatalog {
			t.Fatal("unknown config key accepted")
		}
		break
	}
	for _, value := range schema["functions"].([]any) {
		fn := value.(object)
		args := fn["argument_types"].([]any)
		if len(args) == 0 {
			continue
		}
		actual := clone(fn)
		actual["argument_types"].([]any)[0].(object)["unknown"] = true
		if compareDescriptorValue(actual, fn, nil, "") != ErrCatalog {
			t.Fatal("unknown nested type reference accepted")
		}
		break
	}
}
