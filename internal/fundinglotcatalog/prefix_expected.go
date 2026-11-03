package fundinglotcatalog

import (
	_ "embed"
	"maps"
)

// Static PG16 record deltas derive from the authenticated WalletReadV2 bytes
// and the reviewed SQL. They are never captured or loaded from a live server.
const periodicPrefixDeltaSHA = "2db8c8fc92e3cf56b2a5b335dd643818ed43d8b3c3e80a8bdb021a047be5bcb2"
const periodicPrefixOrdinaryExpectedSHA = "404c3b9d699a1a9ff7afcb62e2a21c7836a5e7217f20bf428ef1834c50903beb"
const periodicPrefixSandboxExpectedSHA = "486b753d8dbbd831fad4f78a6a8a969e6aeed77bc038f53eb2bdb88776f32c26"

//go:embed expected/periodic-prefix-v3-delta.json
var periodicPrefixDeltaJSON []byte

var periodicPrefixExpectedCache [3]expectedCacheEntry

var periodicPrefixBuiltinObjects = map[refKey]string{
	{2616, 10053, 0}: `{"class":"operator class","identity":"pg_catalog.xid8_ops USING btree","name":"xid8_ops","oid":10053,"scope":"pg_catalog","sub_id":0}`,
	{2617, 5068, 0}:  `{"class":"operator","identity":"pg_catalog.=(pg_catalog.xid8,pg_catalog.xid8)","name":"pg_catalog.=(pg_catalog.xid8,pg_catalog.xid8)","oid":5068,"scope":"pg_catalog","sub_id":0}`,
	{2617, 1320, 0}:  `{"class":"operator","identity":"pg_catalog.=(timestamp with time zone,timestamp with time zone)","name":"pg_catalog.=(timestamp with time zone,timestamp with time zone)","oid":1320,"scope":"pg_catalog","sub_id":0}`,
}

func expectedBuiltinObject(expected *descriptorSet, key refKey) (string, bool) {
	if raw, ok := reviewedBuiltinObjects[key]; ok {
		return raw, true
	}
	if expected == nil {
		return "", false
	}
	raw, ok := expected.builtinObjects[key]
	return raw, ok
}

func loadPeriodicPrefixExpected(p Pins) (*descriptorSet, string, error) {
	if p.Mode != Ordinary && p.Mode != Sandbox {
		return nil, "", ErrCatalog
	}
	c := &periodicPrefixExpectedCache[p.Mode]
	c.once.Do(func() {
		base, root, err := loadExpectedProfile(p, WalletReadV2)
		if err != nil {
			c.err = ErrCatalog
			return
		}
		c.root = periodicPrefixOrdinaryExpectedSHA
		if p.Mode == Sandbox {
			c.root = periodicPrefixSandboxExpectedSHA
		}
		c.value, c.err = derivePeriodicPrefixExpected(base, root, c.root, periodicPrefixDeltaJSON)
	})
	if c.err != nil || c.value == nil {
		return nil, "", ErrCatalog
	}
	return c.value, c.root, nil
}

func derivePeriodicPrefixExpected(base *descriptorSet, baseRoot, root string, deltaRaw []byte) (*descriptorSet, error) {
	if base == nil || len(base.Variants) == 0 || len(root) != 64 || digest(deltaRaw) != periodicPrefixDeltaSHA {
		return nil, ErrCatalog
	}
	value, err := strictExpectedRawJSONNodes(deltaRaw, 1000000)
	if err != nil {
		return nil, ErrCatalog
	}
	delta, ok := value.(object)
	if !ok || !exactKeys(delta, "format_version", "profile", "postgres_major", "sql_sha256", "permissions_sha256", "modes") ||
		!same(delta["format_version"], 1) || !same(delta["profile"], 3) || !same(delta["postgres_major"], 16) ||
		delta["sql_sha256"] != "b225c3ff61b735d6830aed1d25edc27bc4784f8e251cb2c1d656196d8d9c630e" ||
		delta["permissions_sha256"] != "aaea2994d5a79726df8b5c1767b5adb054d14ee8cb81fa12b43b87d24a2f28bf" {
		return nil, ErrCatalog
	}
	canonicalDelta, err := canonical(delta)
	if err != nil || string(canonicalDelta) != string(deltaRaw) {
		return nil, ErrCatalog
	}
	modes, ok := delta["modes"].(object)
	if !ok || !exactKeys(modes, "ordinary", "sandbox") {
		return nil, ErrCatalog
	}
	mode, ok := modes[base.Mode].(object)
	if !ok || !exactKeys(mode, "base_sha256", "variants") || mode["base_sha256"] != baseRoot {
		return nil, ErrCatalog
	}
	variants, ok := mode["variants"].([]any)
	if !ok || len(variants) != len(base.Variants) {
		return nil, ErrCatalog
	}
	out := &descriptorSet{FormatVersion: base.FormatVersion, PostgresMajor: base.PostgresMajor, Mode: base.Mode, builtinObjects: maps.Clone(periodicPrefixBuiltinObjects)}
	for i, value := range variants {
		variant, ok := value.(object)
		if !ok || !exactKeys(variant, "name", "changes") || variant["name"] != base.Variants[i].Name {
			return nil, ErrCatalog
		}
		changes, ok := variant["changes"].([]any)
		if !ok || len(changes) == 0 {
			return nil, ErrCatalog
		}
		current := descriptorVariant{Name: base.Variants[i].Name, Schema: clonePrefixDocument(base.Variants[i].Schema), Roles: clonePrefixDocument(base.Variants[i].Roles)}
		seen := map[string]bool{}
		for _, change := range changes {
			if applyPeriodicPrefixChange(&current, change, seen) != nil {
				return nil, ErrCatalog
			}
		}
		if validateExpectedDocuments(current.Schema, current.Roles) != nil {
			return nil, ErrCatalog
		}
		current.schemaCanonical, err = canonical(current.Schema)
		if err != nil {
			return nil, ErrCatalog
		}
		current.rolesCanonical, err = canonical(current.Roles)
		if err != nil {
			return nil, ErrCatalog
		}
		current.schemaCanonical = current.schemaCanonical[:len(current.schemaCanonical):len(current.schemaCanonical)]
		current.rolesCanonical = current.rolesCanonical[:len(current.rolesCanonical):len(current.rolesCanonical)]
		out.Variants = append(out.Variants, current)
	}
	variantValues := make([]any, 0, len(out.Variants))
	for _, variant := range out.Variants {
		variantValues = append(variantValues, object{"name": variant.Name, "schema": variant.Schema, "roles": variant.Roles})
	}
	raw, err := canonical(object{"format_version": out.FormatVersion, "postgres_major": out.PostgresMajor, "mode": out.Mode, "variants": variantValues})
	if err != nil || digest(raw) != root {
		return nil, ErrCatalog
	}
	out.bounds, err = deriveCategoryBounds(out)
	if err != nil {
		return nil, ErrCatalog
	}
	return out, nil
}

// Copy all mutable category containers; unchanged records stay immutable and
// shared with the authenticated base. Delta records have no public accessor.
func clonePrefixDocument(base document) document {
	out := maps.Clone(base)
	for scope, value := range base {
		if doc, ok := value.(object); ok {
			out[scope] = maps.Clone(doc)
		}
	}
	return out
}

func applyPeriodicPrefixChange(variant *descriptorVariant, value any, seen map[string]bool) error {
	change, ok := value.(object)
	if !ok || !exactKeys(change, "family", "scope", "category", "remove", "add") {
		return ErrCatalog
	}
	family, a := change["family"].(string)
	scope, b := change["scope"].(string)
	category, c := change["category"].(string)
	if !a || !b || !c || seen[family+"/"+scope+"/"+category] {
		return ErrCatalog
	}
	seen[family+"/"+scope+"/"+category] = true
	doc := variant.Schema
	fields := schemaRecordFields
	if family == "roles" {
		doc, fields = variant.Roles, roleRecordFields
	} else if family != "schema" {
		return ErrCatalog
	}
	if _, ok := fields[category]; !ok {
		return ErrCatalog
	}
	scoped, ok := doc[scope].(object)
	if !ok {
		return ErrCatalog
	}
	base, ok := scoped[category].([]any)
	remove, r := change["remove"].([]any)
	add, d := change["add"].([]any)
	if !ok || !r || !d || len(remove)+len(add) == 0 {
		return ErrCatalog
	}
	records := make(map[string]any, len(base)+len(add))
	for _, value := range base {
		id, err := recordIdentity(value)
		if err != nil {
			return ErrCatalog
		}
		if _, exists := records[id]; exists {
			return ErrCatalog
		}
		records[id] = value
	}
	for _, value := range remove {
		id, err := recordIdentity(value)
		old, exists := records[id]
		if err != nil || !exists || !same(old, value) {
			return ErrCatalog
		}
		delete(records, id)
	}
	for _, value := range add {
		id, err := recordIdentity(value)
		if err != nil {
			return ErrCatalog
		}
		if _, exists := records[id]; exists {
			return ErrCatalog
		}
		records[id] = value
	}
	out := make([]any, 0, len(records))
	for _, record := range records {
		out = append(out, record)
	}
	if sortRecords(out) != nil {
		return ErrCatalog
	}
	scoped[category] = out
	return nil
}

func init() {
	// Parse and authenticate before the shared five-second issuing budget.
	for _, mode := range []Mode{Ordinary, Sandbox} {
		_, _, _ = loadPeriodicPrefixExpected(Pins{Mode: mode})
	}
}
