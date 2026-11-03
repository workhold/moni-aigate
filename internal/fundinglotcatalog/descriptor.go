package fundinglotcatalog

import (
	"encoding/json"
	"strconv"
)

type object = map[string]any
type document = map[string]any
type refKey struct{ class, oid, sub uint32 }
type resolver struct {
	refs       map[refKey]object
	reverse    map[string]refKey
	types      map[uint32]object
	builtins   map[refKey]object
	attributes map[uint32]map[uint32]string
	names      []string
	namespaces map[string]string
	lookup     func(refKey) (object, error)
}
type catalogSnapshot struct {
	schemaCanonical, rolesCanonical []byte
	observedSchema, observedRoles   document
}
type descriptorSet struct {
	bounds         map[categoryBoundKey]categoryBound
	builtinObjects map[refKey]string
	FormatVersion  uint32              `json:"format_version"`
	PostgresMajor  uint32              `json:"postgres_major"`
	Mode           string              `json:"mode"`
	Schema         document            `json:"schema,omitempty"`
	Roles          document            `json:"roles,omitempty"`
	Variants       []descriptorVariant `json:"variants,omitempty"`
}
type descriptorVariant struct {
	schemaCanonical, rolesCanonical []byte
	Name                            string   `json:"name"`
	Schema                          document `json:"schema"`
	Roles                           document `json:"roles"`
}
type sqlTemplate struct {
	Parts []sqlTemplatePart `json:"parts"`
}
type sqlTemplatePart struct {
	Literal *string `json:"literal,omitempty"`
	Schema  string  `json:"schema,omitempty"`
}

func array(v any) ([]any, bool) { a, ok := v.([]any); return a, ok }
func number(v any) (uint32, bool) {
	switch x := v.(type) {
	case uint32:
		return x, true
	case int:
		return uint32(x), x >= 0 && uint64(x) <= 1<<32-1
	case int32:
		return uint32(x), x >= 0
	case int64:
		return uint32(x), x >= 0 && x <= 1<<32-1
	}

	if s, ok := v.(string); ok {
		if s == "" || len(s) > 1 && s[0] == '0' {
			return 0, false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		n, e := strconv.ParseUint(s, 10, 32)
		return uint32(n), e == nil
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, e := n.Int64()
	return uint32(i), e == nil && i >= 0 && i <= 1<<32-1
}
