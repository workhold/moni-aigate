package fundinglotcatalog

import (
	"bytes"
	"strings"
	"testing"
)

// This small closed DTO exercises decodeExpected without repeating the 58MiB
// embedded roots for each rejection case. It grants no public proof or cache.
func smallExpectedRoot(t *testing.T) []byte {
	t.Helper()
	variants := []any{}
	for _, name := range []string{"named-isolated", "named-minimal-login", "named-image-login", "public-minimal-login", "public-image-login"} {
		schema := object{"format_version": 1}
		for category := range schemaRecordFields {
			schema[category] = []any{}
		}
		roles := object{"format_version": 1, "profile": "ordinary_app_acl", "worker_kind": "absent"}
		for category := range roleRecordFields {
			roles[category] = []any{}
		}
		roles["roles"] = []any{object{
			"identity": object{"class": "role", "scope": "role", "name": "application_owner"},
			"login":    false, "inherit": false, "superuser": false, "create_db": false,
			"create_role": false, "replication": false, "bypass_rls": false,
			"connection_limit": -1, "valid_until": nil,
			"config": []any{`quotes: " \" \\"; braces: {}[]; slash: \\`},
		}}
		variants = append(variants, object{"name": name, "schema": object{"application": schema}, "roles": object{"application": roles}})
	}
	raw, err := canonical(object{"format_version": 1, "postgres_major": 16, "mode": "ordinary", "variants": variants})
	if err != nil {
		t.Fatal("encode small expected fixture failed")
	}
	return raw
}

func decodeSmallExpected(t *testing.T, raw []byte) *descriptorSet {
	t.Helper()
	d, err := decodeExpected(raw, digest(raw), Ordinary)
	if err != nil {
		t.Fatal("closed small expected fixture rejected")
	}
	return d
}

func TestCatalogExpectedSpanCacheEqualsCompleteCanonicalDTO(t *testing.T) {
	for _, mode := range []Mode{Ordinary, Sandbox} {
		d, _, err := loadExpected(Pins{Mode: mode})
		if err != nil {
			t.Fatal("reviewed expected root unavailable")
		}
		for _, variant := range d.Variants {
			for _, family := range []struct {
				doc document
				raw []byte
			}{{variant.Schema, variant.schemaCanonical}, {variant.Roles, variant.rolesCanonical}} {
				encoded, err := canonical(family.doc)
				if err != nil || !bytes.Equal(encoded, family.raw) || len(family.raw) != cap(family.raw) {
					t.Fatal("cached family differs from complete canonical DTO or permits adjacent append")
				}
			}
		}
	}
	if digest(ordinaryExpectedJSON) != ordinaryExpectedSHA || digest(sandboxExpectedJSON) != sandboxExpectedSHA {
		t.Fatal("frozen embedded root bytes changed")
	}
}

func TestCatalogExpectedSpanOwnershipAndEscapedBoundaries(t *testing.T) {
	raw := smallExpectedRoot(t)
	first := decodeSmallExpected(t, raw)
	second := decodeSmallExpected(t, append([]byte(nil), raw...))
	index, err := expectedCanonicalSpans(raw)
	if err != nil {
		t.Fatal("canonical span indexing failed")
	}
	for i, variant := range first.Variants {
		category := index.fields["variants"].elements[i].fields["roles"].fields["application"].fields["roles"]
		encoded, err := canonical(variant.Roles["application"].(object)["roles"])
		if err != nil || !bytes.Equal(encoded, category.raw) || len(category.raw) != cap(category.raw) {
			t.Fatal("escaped quotes, backslashes or brackets crossed category boundary")
		}
	}
	before := append([]byte(nil), first.Variants[0].rolesCanonical...)
	for i := range raw {
		raw[i] = 'x'
	}
	if !bytes.Equal(before, first.Variants[0].rolesCanonical) {
		t.Fatal("caller input mutation reached cached canonical bytes")
	}
	a, b := first.Variants[0].rolesCanonical, second.Variants[0].rolesCanonical
	if &a[0] == &b[0] {
		t.Fatal("independent decodes share canonical buffers")
	}
	a[0] = '!'
	if !bytes.Equal(before, b) {
		t.Fatal("one private buffer mutation reached another decode")
	}
	firstRecord := first.Variants[0].Roles["application"].(object)["roles"].([]any)[0].(object)
	secondRecord := second.Variants[0].Roles["application"].(object)["roles"].([]any)[0].(object)
	firstRecord["config"].([]any)[0] = "changed in first decode"
	if secondRecord["config"].([]any)[0] == "changed in first decode" {
		t.Fatal("independent decodes share owned DTO values")
	}
}

func TestCatalogExpectedSpanOptimizationPreservesStrictRejection(t *testing.T) {
	raw := smallExpectedRoot(t)
	cases := map[string][]byte{
		"leading whitespace":  append([]byte(" "), raw...),
		"trailing whitespace": append(append([]byte(nil), raw...), '\n'),
		"trailing document":   append(append([]byte(nil), raw...), []byte(`{}`)...),
		"duplicate key":       append([]byte(`{"format_version":1,`), raw[1:]...),
		"escaped key alias":   bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"\u0066ormat_version":1`), 1),
		"exponent alias":      bytes.Replace(raw, []byte(`"postgres_major":16`), []byte(`"postgres_major":1.6e1`), 1),
		"fraction alias":      bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"format_version":1.0`), 1),
		"negative zero":       bytes.Replace(raw, []byte(`"connection_limit":-1`), []byte(`"connection_limit":-0`), 1),
		"invalid UTF8":        append(append([]byte(nil), raw...), byte(0xff)),
	}
	for name, invalid := range cases {
		t.Run(name, func(t *testing.T) {
			if d, err := decodeExpected(invalid, digest(invalid), Ordinary); err != ErrCatalog || d != nil {
				t.Fatal("strict expected gate accepted altered input with recalculated SHA")
			}
		})
	}
}

func TestCatalogExpectedSpanIndexRejectsDTOPathMismatch(t *testing.T) {
	raw := smallExpectedRoot(t)
	for _, name := range []string{"mode", "version", "variant order", "family", "scope", "category"} {
		t.Run(name, func(t *testing.T) {
			d := decodeSmallExpected(t, raw)
			v, err := strictJSON(raw)
			if err != nil {
				t.Fatal("parse canonical fixture failed")
			}
			root := v.(object)
			variant := root["variants"].([]any)[0].(object)
			switch name {
			case "mode":
				root["mode"] = "sandbox"
			case "version":
				root["format_version"] = 2
			case "variant order":
				variants := root["variants"].([]any)
				variants[0], variants[1] = variants[1], variants[0]
			case "family":
				variant["role_alias"] = variant["roles"]
				delete(variant, "roles")
			case "scope":
				family := variant["roles"].(object)
				family["scope_alias"] = family["application"]
				delete(family, "application")
			case "category":
				scope := variant["roles"].(object)["application"].(object)
				scope["roles_alias"] = scope["roles"]
				delete(scope, "roles")
			}
			altered, err := canonical(root)
			if err != nil || cacheAuthenticatedVariantDocuments(d, altered) != ErrCatalog {
				t.Fatal("span index accepted mismatched validated DTO path")
			}
		})
	}
}

func TestCatalogExpectedSpanIndexOwnResourceBounds(t *testing.T) {
	tooMany := []byte("[" + strings.Repeat("0,", 4096) + "0]")
	tooDeep := []byte(strings.Repeat("[", 70) + "0" + strings.Repeat("]", 70))
	for _, raw := range [][]byte{nil, tooMany, tooDeep, []byte(`{"x":[}`), []byte(`{"x":1,}`), []byte(`[1,]`)} {
		if _, err := expectedCanonicalSpans(raw); err != ErrCatalog {
			t.Fatal("private index exceeded closed resource or shape limits")
		}
	}
}

func TestCatalogCompiledValueFieldsPreserveClosedOrderedContracts(t *testing.T) {
	check := func(schema string, fields []valueTypeField) {
		t.Helper()
		original := strings.Fields(schema)
		if len(original) != len(fields) {
			t.Fatal("compiled schema changed field count")
		}
		for i, original := range original {
			parts := strings.SplitN(original, ":", 2)
			if fields[i].name != parts[0] || fields[i].kind != parts[1] {
				t.Fatal("compiled schema changed field order, name or type")
			}
		}
	}
	if len(compiledRecordValueTypes) != len(recordValueTypes) {
		t.Fatal("compiled schema changed category set")
	}
	for name, schema := range recordValueTypes {
		check(schema, compiledRecordValueTypes[name])
	}
	check(attributeValueTypes, compiledAttributeValueTypes)
	if validateRecordValues(object{}, "unregistered") != ErrCatalog {
		t.Fatal("unregistered compiled schema accepted")
	}
}
