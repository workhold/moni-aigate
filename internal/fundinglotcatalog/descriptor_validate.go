package fundinglotcatalog

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// Token decoding rejects duplicate keys before a map can erase them. This
// parser is bounded independently of embedded SHA authentication.
func strictJSON(raw []byte) (any, error) { return strictJSONNodes(raw, 1000000) }
func strictJSONNodes(raw []byte, nodeLimit int) (any, error) {
	if len(raw) > collectorByteLimit || !utf8.Valid(raw) {
		return nil, ErrCatalog
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		nodes++
		if depth > 64 || nodes > nodeLimit {
			return nil, ErrCatalog
		}
		t, e := d.Token()
		if e != nil {
			return nil, ErrCatalog
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				m := object{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return nil, ErrCatalog
					}
					key, ok := k.(string)
					if !ok {
						return nil, ErrCatalog
					}
					if _, dup := m[key]; dup {
						return nil, ErrCatalog
					}
					v, e := read(depth + 1)
					if e != nil {
						return nil, ErrCatalog
					}
					m[key] = v
				}
				if t, e = d.Token(); e != nil || t != json.Delim('}') {
					return nil, ErrCatalog
				}
				return m, nil
			case '[':
				a := []any{}
				for d.More() {
					v, e := read(depth + 1)
					if e != nil {
						return nil, ErrCatalog
					}
					a = append(a, v)
					if len(a) > collectorRowLimit {
						return nil, ErrCatalog
					}
				}
				if t, e = d.Token(); e != nil || t != json.Delim(']') {
					return nil, ErrCatalog
				}
				return a, nil
			}
			return nil, ErrCatalog
		}
		if s, ok := t.(string); ok && (len(s) > collectorRecordLimit || strings.ContainsRune(s, 0)) {
			return nil, ErrCatalog
		}
		if n, ok := t.(json.Number); ok && !validCanonicalNumber(string(n)) {
			return nil, ErrCatalog
		}
		return t, nil
	}
	v, e := read(0)
	if e != nil {
		return nil, ErrCatalog
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, ErrCatalog
	}
	return v, nil
}

func exactKeys(m object, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
func allowedKeys(m object, keys ...string) bool {
	for k := range m {
		found := false
		for _, a := range keys {
			if k == a {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func validateReference(v any) error {
	m, ok := v.(object)
	if !ok {
		return ErrCatalog
	}
	if _, yes := m["namespace"]; yes {
		if !exactKeys(m, "namespace", "name", "oid") || m["namespace"] != "pg_catalog" {
			return ErrCatalog
		}
		if validateValueType(m["name"], "text") != nil || !oidNumber(m["oid"], false) {
			return ErrCatalog
		}
		n, _ := number(m["oid"])
		if !reviewedBuiltinTypes[n] || m["name"] != reviewedBuiltinTypeNames[n] {
			return ErrCatalog
		}
		return nil
	}
	if o, yes := m["object"]; yes {
		if !exactKeys(m, "object") {
			return ErrCatalog
		}
		ref, ok := o.(object)
		if !ok || ref["class"] != "type" || ref["scope"] != "application" && ref["scope"] != "mock" && ref["scope"] != "receiver" {
			return ErrCatalog
		}
		return validateReference(o)
	}
	if !allowedKeys(m, "class", "scope", "name", "identity_arguments", "owner", "sub_id", "column", "oid", "identity") {
		return ErrCatalog
	}
	class, cok := m["class"].(string)
	scope, sok := m["scope"].(string)
	name, nok := m["name"].(string)
	if !cok || !sok || !nok || name == "" {
		return ErrCatalog
	}
	switch scope {
	case "public":
		if class != "role" || name != "PUBLIC" || !exactKeys(m, "class", "scope", "name") {
			return ErrCatalog
		}
	case "role":
		switch name {
		case "application_owner", "application", "worker", "projector", "mock_owner", "mock_runtime", "receiver_owner", "receiver_runtime":
		default:
			return ErrCatalog
		}
		if class != "role" || !exactKeys(m, "class", "scope", "name") {
			return ErrCatalog
		}
	case "pg_catalog":
		if !oidNumber(m["oid"], false) {
			return ErrCatalog
		}
		if class == "role" {
			if name != "pg_database_owner" || !same(m["oid"], 6171) || !exactKeys(m, "class", "scope", "name", "oid") {
				return ErrCatalog
			}
		} else if !exactKeys(m, "class", "scope", "name", "identity", "oid", "sub_id") || validateValueType(m["identity"], "text") != nil || !oidNumber(m["sub_id"], true) {
			return ErrCatalog
		}
	case "automatic":
		if class != "relation" || name != "toast_heap" && name != "toast_index" || !exactKeys(m, "class", "scope", "name", "owner") && !exactKeys(m, "class", "scope", "name", "owner", "column", "sub_id") {
			return ErrCatalog
		}
		return validateReference(m["owner"])
	case "application", "mock", "receiver":
		switch class {
		case "function":
			if !exactKeys(m, "class", "scope", "name", "identity_arguments") {
				return ErrCatalog
			}
			a, ok := m["identity_arguments"].([]any)
			if !ok || len(a) > collectorArgumentLimit {
				return ErrCatalog
			}
			for _, v := range a {
				child, ok := v.(object)
				if !ok {
					return ErrCatalog
				}
				if _, ok := child["namespace"]; !ok {
					if _, ok := child["object"]; !ok {
						return ErrCatalog
					}
				}
				if validateReference(v) != nil {
					return ErrCatalog
				}
			}
		case "relation":
			if _, ok := m["sub_id"]; ok {
				if !oidNumber(m["sub_id"], false) || validateValueType(m["column"], "text") != nil {
					return ErrCatalog
				}
			}
			if !exactKeys(m, "class", "scope", "name") && !exactKeys(m, "class", "scope", "name", "sub_id", "column") {
				return ErrCatalog
			}
		case "type", "constraint", "default", "trigger", "namespace":
			if !exactKeys(m, "class", "scope", "name") {
				return ErrCatalog
			}
		default:
			return ErrCatalog
		}
	default:
		return ErrCatalog
	}
	return nil
}

func validateNested(v any, key string) error {
	if v == nil {
		return nil
	}
	if m, ok := v.(object); ok {
		if _, yes := m["class"]; yes {
			return validateReference(m)
		}
		if _, yes := m["namespace"]; yes {
			return validateReference(m)
		}
		if _, yes := m["object"]; yes {
			if exactKeys(m, "object") {
				return validateReference(m)
			}
			return ErrCatalog
		}
		known := false
		if _, yes := m["grantor"]; yes {
			known = true
			if !exactKeys(m, "grantor", "grantee", "privilege", "grant_option") {
				return ErrCatalog
			}
		}
		if _, yes := m["attnum"]; yes {
			known = true
			if !exactKeys(m, "attnum", "name", "type", "typmod", "collation", "not_null", "dropped", "identity", "generated", "storage", "compression", "statistics", "options", "default") {
				return ErrCatalog
			}
		}
		if _, yes := m["key"]; yes {
			known = true
			if !exactKeys(m, "key", "namespaces") && !exactKeys(m, "key", "value") {
				return ErrCatalog
			}
		}
		if !known {
			return ErrCatalog
		}
		for k, v := range m {
			if validateNested(v, k) != nil {
				return ErrCatalog
			}
		}
		return nil
	}
	if a, ok := v.([]any); ok {
		for _, v := range a {
			if validateNested(v, key) != nil {
				return ErrCatalog
			}
		}
	}
	return nil
}

func validateExpectedDocuments(schema, roles document) error {
	names := map[string]string{"application": "expected_application", "mock": "expected_mock", "receiver": "expected_receiver"}
	var strip func(any, string) (any, error)
	strip = func(v any, path string) (any, error) {
		switch x := v.(type) {
		case object:
			if _, ok := x["parts"]; ok {
				rendered, e := renderSQLTemplate(x, names, path)
				if e != nil {
					return nil, ErrCatalog
				}
				return rendered, nil
			}
			m := object{}
			for k, v := range x {
				n, e := strip(v, path+"."+k)
				if e != nil {
					return nil, ErrCatalog
				}
				m[k] = n
			}
			return m, nil
		case []any:
			a := make([]any, len(x))
			for i, v := range x {
				n, e := strip(v, path+".")
				if e != nil {
					return nil, ErrCatalog
				}
				a[i] = n
			}
			return a, nil
		}
		return v, nil
	}
	s, e := strip(schema, "")
	if e != nil {
		return ErrCatalog
	}
	r, e := strip(roles, "")
	if e != nil {
		return ErrCatalog
	}
	return validateCatalogDocuments(s.(object), r.(object))
}

func validateCatalogDocuments(schema, roles document) error {
	if len(schema) != 1 && len(schema) != 3 || len(schema) != len(roles) || schema["application"] == nil || len(schema) == 3 && (schema["mock"] == nil || schema["receiver"] == nil) {
		return ErrCatalog
	}
	for scope, raw := range schema {
		if scope != "application" && scope != "mock" && scope != "receiver" {
			return ErrCatalog
		}
		s, ok := raw.(object)
		if !ok || len(s) != len(schemaRecordFields)+1 || !same(s["format_version"], 1) {
			return ErrCatalog
		}
		r, ok := roles[scope].(object)
		if !ok || len(r) != len(roleRecordFields)+3 || !same(r["format_version"], 1) {
			return ErrCatalog
		}
		profile, worker := "ordinary_app_acl", "absent"
		if len(schema) == 3 {
			profile, worker = "sandbox_app_acl", "sandbox_runtime_role"
		}
		if r["profile"] != profile || r["worker_kind"] != worker {
			return ErrCatalog
		}
		for _, set := range []struct {
			doc      object
			registry map[string][]string
		}{{s, schemaRecordFields}, {r, roleRecordFields}} {
			for category, keys := range set.registry {
				a, ok := set.doc[category].([]any)
				if !ok {
					return ErrCatalog
				}
				for _, v := range a {
					m, ok := v.(object)
					if !ok || len(keys) == 0 || !exactKeys(m, keys...) {
						return ErrCatalog
					}
					if validateRecordValues(m, category) != nil {
						return ErrCatalog
					}
					for k, v := range m {
						if validateNested(v, k) != nil {
							return ErrCatalog
						}
					}
				}
			}
		}
	}
	return nil
}
