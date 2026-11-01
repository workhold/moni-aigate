package fundinglotcatalog

import (
	"encoding/json"
	"strconv"
	"strings"
)

// These field contracts supplement the closed key registry. A reference-shaped
// object cannot stand in for a scalar, nor can arbitrary arrays hide new nodes.
var recordValueTypes = map[string]string{
	"namespace":            "identity:ref owner:ref",
	"relations":            "identity:ref owner:ref kind:char persistence:char am:?ref partition:bool partition_bound:?text partition_key:?text row_security:bool force_row_security:bool replica_identity:char options:?texts tablespace:?ref toast:?ref row_type:?ref attributes:attributes inherits:refs",
	"types":                "identity:ref owner:ref type:char category:char defined:bool length:int by_value:bool alignment:char storage:char preferred:bool delimiter:char relation:?ref element:?ref array:?ref base:?ref typmod:int not_null:bool dimensions:int collation:?ref default:?text default_expression:?text input:ref output:ref receive:?ref send:?ref typmod_in:?ref typmod_out:?ref analyze:?ref subscript:?ref enum_labels:empty range:null",
	"functions":            "identity:ref owner:ref language:ref kind:char aggregate:null volatility:char strict:bool definer:bool leakproof:bool retset:bool parallel:char cost:decimal rows:decimal variadic:?ref support:?ref result:ref argument_types:args all_argument_types:?args argument_names:?texts argument_modes:?chars argument_defaults:?text default_count:int prosrc:text source_sha256:sha probin:?text prosqlbody:?text proconfig:?configs transform_types:?args",
	"constraints":          "identity:ref kind:char relation:?ref domain:?ref columns:?ints foreign_relation:?ref foreign_columns:?ints index:?ref parent:?ref deferrable:bool deferred:bool validated:bool local:bool inherit_count:int no_inherit:bool update_action:char delete_action:char match:char delete_set_columns:?ints expression:?text equality_operators:refs pk_equality_operators:refs fk_equality_operators:refs",
	"indexes":              "identity:ref relation:ref attribute_count:int key_count:int unique:bool nulls_not_distinct:bool primary:bool exclusion:bool immediate:bool clustered:bool valid:bool check_xmin:bool ready:bool live:bool replica_identity:bool keys:ints options:ints collations:nullable_refs opclasses:refs expressions:?text predicate:?text",
	"triggers":             "identity:ref relation:ref function:ref type:int enabled:char internal:bool constraint:?ref constraint_relation:?ref constraint_index:?ref deferrable:bool deferred:bool argument_count:int arguments:text columns:ints when:?text old_table:?text new_table:?text",
	"dependencies":         "catalog:text object:ref referenced:ref kind:char multiplicity:int",
	"acl_dependencies":     "catalog:text object:ref role:ref kind:char multiplicity:int",
	"effective_privileges": "role:ref kind:text object:ref privileges:bools grant_options:bools",
	"roles":                "identity:ref login:bool inherit:bool superuser:bool create_db:bool create_role:bool replication:bool bypass_rls:bool connection_limit:int valid_until:?text config:?texts",
	"memberships":          "role:ref member:ref grantor:ref admin:bool inherit:bool set:bool",
	"schema_acl":           "object:ref acl_is_null:bool entries:acls default_entries:acls",
	"relation_acl":         "object:ref acl_is_null:bool entries:acls default_entries:acls",
	"column_acl":           "object:ref acl_is_null:bool entries:acls default_entries:acls",
	"function_acl":         "object:ref acl_is_null:bool entries:acls default_entries:acls",
	"type_acl":             "object:ref acl_is_null:bool entries:acls default_entries:acls",
	"default_acl":          "owner:ref namespace:?ref object_type:char entries:acls",
}

func canonicalNumberText(v any) (string, bool) {
	switch x := v.(type) {
	case json.Number:
		return string(x), true
	case int:
		return strconv.Itoa(x), true
	case uint32:
		return strconv.FormatUint(uint64(x), 10), true
	case int32:
		return strconv.FormatInt(int64(x), 10), true
	case int64:
		return strconv.FormatInt(x, 10), true
	}
	return "", false
}

// PG16 JSON metadata uses canonical finite base-10 numbers. Exponent aliases,
// negative zero, leading zeros and fractional trailing zeros are never accepted.
func validCanonicalNumber(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	p := s
	if p[0] == '-' {
		p = p[1:]
		if p == "" {
			return false
		}
	}
	pieces := strings.Split(p, ".")
	if len(pieces) > 2 || pieces[0] == "" || len(pieces[0]) > 1 && pieces[0][0] == '0' {
		return false
	}
	for _, part := range pieces {
		if part == "" {
			return false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	if len(pieces) == 2 && pieces[1][len(pieces[1])-1] == '0' {
		return false
	}
	return s != "-0"
}
func oidNumber(v any, zero bool) bool {
	s, ok := canonicalNumberText(v)
	if !ok || !validCanonicalNumber(s) || strings.ContainsAny(s, "-.") {
		return false
	}
	n, e := strconv.ParseUint(s, 10, 32)
	return e == nil && (zero || n != 0)
}
func validateRecordValues(m object, category string) error {
	fields, ok := recordValueTypes[category]
	if !ok {
		return ErrCatalog
	}
	for _, field := range strings.Fields(fields) {
		p := strings.SplitN(field, ":", 2)
		if validateValueType(m[p[0]], p[1]) != nil {
			return ErrCatalog
		}
	}
	return validateRecordEnums(m, category)
}
func validateRecordEnums(m object, c string) error {
	check := func(k, values string) bool {
		s, ok := m[k].(string)
		return ok && len(s) == 1 && strings.Contains(values, s)
	}
	switch c {
	case "relations":
		if !check("kind", "ripvmfSctI") || !check("persistence", "put") || !check("replica_identity", "dnfi") {
			return ErrCatalog
		}
	case "types":
		if !check("type", "bc") || !check("category", "AC") || !check("alignment", "csid") || !check("storage", "pexm") {
			return ErrCatalog
		}
	case "functions":
		if !check("kind", "f") || !check("volatility", "isv") || !check("parallel", "sru") {
			return ErrCatalog
		}
		if modes, ok := m["argument_modes"].([]any); ok {
			for _, v := range modes {
				mode, ok := v.(string)
				if !ok || len(mode) != 1 || !strings.Contains("iobvt", mode) {
					return ErrCatalog
				}
			}
		}
	case "constraints":
		if !check("kind", "cfputx") || !check("update_action", " ar cnd") || !check("delete_action", " ar cnd") || !check("match", " sfp") {
			return ErrCatalog
		}
	case "triggers":
		if !check("enabled", "ODRA") {
			return ErrCatalog
		}
	case "dependencies":
		if !check("kind", "naixepPSo") {
			return ErrCatalog
		}
		if m["catalog"] != "pg_depend" && m["catalog"] != "pg_shdepend" {
			return ErrCatalog
		}
	case "acl_dependencies":
		if m["kind"] != "a" || m["catalog"] != "pg_shdepend" {
			return ErrCatalog
		}
	case "effective_privileges":
		kind, ok := m["kind"].(string)
		if !ok {
			return ErrCatalog
		}
		n := map[string]int{"table": 7, "column": 4, "function": 1, "type": 1, "schema": 2}[kind]
		a, aok := m["privileges"].([]any)
		b, bok := m["grant_options"].([]any)
		if n == 0 || !aok || !bok || len(a) != n || len(b) != n {
			return ErrCatalog
		}
	case "default_acl":
		if !check("object_type", "rSfTLns") {
			return ErrCatalog
		}
	}
	return nil
}
func validateValueType(v any, t string) error {
	if strings.HasPrefix(t, "?") {
		if v == nil {
			return nil
		}
		return validateValueType(v, t[1:])
	}
	switch t {
	case "null":
		if v == nil {
			return nil
		}
	case "bool":
		if _, ok := v.(bool); ok {
			return nil
		}
	case "text", "char", "sha":
		if s, ok := v.(string); ok && len(s) <= collectorRecordLimit && !strings.ContainsRune(s, 0) {
			if t == "char" && len(s) != 1 {
				return ErrCatalog
			}
			if t == "sha" {
				if len(s) != 64 {
					return ErrCatalog
				}
				for _, c := range s {
					if c < '0' || c > '9' && c < 'a' || c > 'f' {
						return ErrCatalog
					}
				}
			}
			return nil
		}
	case "int", "decimal":
		if s, ok := canonicalNumberText(v); ok && validCanonicalNumber(s) {
			if t == "int" && strings.ContainsRune(s, '.') {
				return ErrCatalog
			}
			return nil
		}
	case "ref":
		return validateReference(v)
	case "empty":
		if a, ok := v.([]any); ok && len(a) == 0 {
			return nil
		}
	default:
		a, ok := v.([]any)
		if !ok {
			return ErrCatalog
		}
		limit := collectorAttributeLimit
		child := ""
		switch t {
		case "refs":
			child = "ref"
		case "nullable_refs":
			child = "?ref"
		case "args":
			child = "ref"
			limit = collectorArgumentLimit
		case "ints":
			child = "int"
		case "texts":
			child = "text"
		case "chars":
			child = "char"
		case "bools":
			child = "bool"
		case "attributes":
			child = "attribute"
		case "acls":
			child = "acl"
			limit = collectorACLLimit
		case "configs":
			child = "config"
			limit = collectorArgumentLimit
		case "attribute":
			return validateAttribute(v)
		case "acl":
			return validateACL(v)
		case "config":
			return validateConfig(v)
		default:
			return ErrCatalog
		}
		if len(a) > limit {
			return ErrCatalog
		}
		for _, x := range a {
			var e error
			switch child {
			case "attribute":
				e = validateAttribute(x)
			case "acl":
				e = validateACL(x)
			case "config":
				e = validateConfig(x)
			default:
				e = validateValueType(x, child)
			}
			if e != nil {
				return ErrCatalog
			}
		}
		return nil
	}
	return ErrCatalog
}
func validateAttribute(v any) error {
	m, ok := v.(object)
	if !ok || !exactKeys(m, "attnum", "name", "type", "typmod", "collation", "not_null", "dropped", "identity", "generated", "storage", "compression", "statistics", "options", "default") {
		return ErrCatalog
	}
	fields := "attnum:int name:text type:?ref typmod:int collation:?ref not_null:bool dropped:bool identity:char0 generated:char0 storage:char compression:char0 statistics:int options:?texts default:?text"
	for _, f := range strings.Fields(fields) {
		p := strings.SplitN(f, ":", 2)
		if p[1] == "char0" {
			s, ok := m[p[0]].(string)
			if !ok || len(s) > 1 {
				return ErrCatalog
			}
		} else if validateValueType(m[p[0]], p[1]) != nil {
			return ErrCatalog
		}
	}
	return nil
}
func validateACL(v any) error {
	m, ok := v.(object)
	if !ok || !exactKeys(m, "grantor", "grantee", "privilege", "grant_option") || validateReference(m["grantor"]) != nil || validateReference(m["grantee"]) != nil || validateValueType(m["grant_option"], "bool") != nil {
		return ErrCatalog
	}
	s, ok := m["privilege"].(string)
	if !ok {
		return ErrCatalog
	}
	switch s {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "EXECUTE", "USAGE", "CREATE", "CONNECT", "TEMPORARY", "SET", "ALTER SYSTEM":
		return nil
	}
	return ErrCatalog
}
func validateConfig(v any) error {
	m, ok := v.(object)
	if !ok {
		return ErrCatalog
	}
	k, ok := m["key"].(string)
	if !ok || k == "" {
		return ErrCatalog
	}
	if k == "search_path" {
		if !exactKeys(m, "key", "namespaces") {
			return ErrCatalog
		}
		a, ok := m["namespaces"].([]any)
		if !ok || len(a) != 3 {
			return ErrCatalog
		}
		for _, v := range a {
			s, ok := v.(string)
			if !ok {
				return ErrCatalog
			}
			switch s {
			case "application", "mock", "receiver", "pg_catalog", "pg_temp":
			default:
				return ErrCatalog
			}
		}
		return nil
	}
	if !exactKeys(m, "key", "value") || validateValueType(m["value"], "text") != nil {
		return ErrCatalog
	}
	return nil
}
