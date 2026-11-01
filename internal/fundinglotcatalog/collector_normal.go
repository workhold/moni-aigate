package fundinglotcatalog

import "strings"

const (
	classRelation  uint32 = 1259
	classType      uint32 = 1247
	classFunction  uint32 = 1255
	classRole      uint32 = 1260
	classNamespace uint32 = 2615
)

func refSQL(class, expr string) string {
	return "pg_catalog.jsonb_build_object('$ref',pg_catalog.jsonb_build_array('pg_catalog." + class + "'::pg_catalog.regclass::pg_catalog.oid,(" + expr + ")::pg_catalog.oid,0))"
}
func typeSQL(expr string) string { return "pg_catalog.jsonb_build_object('$type'," + expr + ")" }

func (r *resolver) register(k refKey, v object) error {
	if k.oid == 0 {
		return ErrCatalog
	}
	if _, ok := r.refs[k]; ok {
		return ErrCatalog
	}
	b, e := canonical(v)
	if e != nil {
		return e
	}
	if r.reverse == nil {
		r.reverse = map[string]refKey{}
	}
	if _, ok := r.reverse[string(b)]; ok {
		return ErrCatalog
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
			return ErrCatalog
		}
		reverse[string(b)] = k
	}
	r.reverse = reverse
	return nil
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
			return nil, ErrCatalog
		}
		name, valid := r.attributes[k.oid][k.sub]
		if !valid {
			return nil, ErrCatalog
		}
		if x, ok := r.refs[refKey{k.class, k.oid, 0}]; ok {
			y := object{}
			for f, v := range x {
				y[f] = v
			}
			y["sub_id"], y["column"] = k.sub, name
			return y, nil
		}
		return nil, ErrCatalog
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
				return nil, ErrCatalog
			}
			vs, ok := array(a)
			if !ok || len(vs) != 3 {
				return nil, ErrCatalog
			}
			var ns [3]uint32
			for i, v := range vs {
				n, ok := number(v)
				if !ok {
					return nil, ErrCatalog
				}
				ns[i] = n
			}
			return r.reference(refKey{ns[0], ns[1], ns[2]})
		}
		if n, ok := x["$type"]; ok {
			if len(x) != 1 {
				return nil, ErrCatalog
			}
			oid, ok := number(n)
			if !ok {
				return nil, ErrCatalog
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
		// Only single-use privately decoded raw catalog trees enter this branch.
		// $ref/$type returned above are borrowed immutable symbol nodes and are
		// never walked or modified here. Expected documents never enter normal.
		out := x
		for k, v := range x {
			if isDeparseField(k) {
				if v != nil {
					if _, ok := v.(string); !ok {
						return nil, ErrCatalog
					}
				}
				out[k] = v
				continue
			}
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
					return nil, ErrCatalog
				}
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := x
		for i, v := range x {
			n, e := r.normal(v)
			if e != nil {
				return nil, e
			}
			out[i] = n
		}
		return out, nil
	case string:
		// Never rewrite text. Exact comparison (or a reviewed identifier-only
		// expected template) handles deparse fields, including public schemas.
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
			return nil, ErrCatalog
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
				return nil, ErrCatalog
			}
		} else {
			i := 0
			for i < len(s) && ((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= '0' && s[i] <= '9') || s[i] == '_') {
				i++
			}
			if i == 0 {
				return nil, ErrCatalog
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
			return nil, ErrCatalog
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
		return nil, ErrCatalog
	}
	out := make([]any, 0, len(a))
	for _, x := range a {
		s, ok := x.(string)
		if !ok {
			return nil, ErrCatalog
		}
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			return nil, ErrCatalog
		}
		if key != "search_path" {
			out = append(out, object{"key": key, "value": value})
			continue
		}
		path, e := parsePath(value)
		if e != nil || len(path) != 3 || r.namespaces[path[0]] == "" || path[1] != "pg_catalog" || path[2] != "pg_temp" {
			return nil, ErrCatalog
		}
		out = append(out, object{"key": key, "namespaces": []any{r.namespaces[path[0]], "pg_catalog", "pg_temp"}})
	}
	return out, nil
}
