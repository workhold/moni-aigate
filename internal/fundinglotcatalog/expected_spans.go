package fundinglotcatalog

import (
	"bytes"
	"encoding/json"
)

// expectedSpan indexes only the five envelope levels of an already strictly
// decoded, closed and canonical-authenticated expected root. This is not a JSON
// validator or a parser for actual metadata. Every retained slice belongs to
// decodeExpected's new canonical encoding, never its caller's input.
type expectedSpan struct {
	raw      []byte
	fields   map[string]expectedSpan
	elements []expectedSpan
}

type expectedSpanIndex struct {
	raw   []byte
	nodes int
}

func expectedCanonicalSpans(raw []byte) (expectedSpan, error) {
	if len(raw) == 0 || len(raw) > collectorByteLimit {
		return expectedSpan{}, ErrCatalog
	}
	index := expectedSpanIndex{raw: raw}
	root, end, err := index.read(0, 0)
	if err != nil || end != len(raw) {
		return expectedSpan{}, ErrCatalog
	}
	return root, nil
}

func (x *expectedSpanIndex) quoted(start int) (int, error) {
	if start >= len(x.raw) || x.raw[start] != '"' {
		return 0, ErrCatalog
	}
	for offset := start + 1; offset < len(x.raw); {
		n := bytes.IndexByte(x.raw[offset:], '"')
		if n < 0 {
			return 0, ErrCatalog
		}
		quote := offset + n
		slashes := 0
		for p := quote - 1; p > start && x.raw[p] == '\\'; p-- {
			slashes++
		}
		if slashes%2 == 0 {
			return quote + 1, nil
		}
		offset = quote + 1
	}
	return 0, ErrCatalog
}

// skip traverses each category once without rebuilding its many records.
// Strict syntax, UTF8, duplicate keys, number forms and all resource limits
// have already been checked before this index is allowed to run.
func (x *expectedSpanIndex) skip(start, pathDepth int) (int, error) {
	if start >= len(x.raw) {
		return 0, ErrCatalog
	}
	if x.raw[start] == '"' {
		return x.quoted(start)
	}
	if x.raw[start] != '{' && x.raw[start] != '[' {
		n := bytes.IndexAny(x.raw[start:], ",}]")
		if n < 0 {
			return len(x.raw), nil
		}
		if n == 0 {
			return 0, ErrCatalog
		}
		return start + n, nil
	}
	var stack [64]byte
	stack[0] = x.raw[start]
	depth := 1
	for offset := start + 1; offset < len(x.raw); {
		n := bytes.IndexAny(x.raw[offset:], "{}[]\"")
		if n < 0 {
			return 0, ErrCatalog
		}
		offset += n
		switch x.raw[offset] {
		case '"':
			end, err := x.quoted(offset)
			if err != nil {
				return 0, ErrCatalog
			}
			offset = end
			continue
		case '{', '[':
			if depth == len(stack) || pathDepth+depth > 64 {
				return 0, ErrCatalog
			}
			stack[depth] = x.raw[offset]
			depth++
		case '}', ']':
			top := stack[depth-1]
			if x.raw[offset] == '}' && top != '{' || x.raw[offset] == ']' && top != '[' {
				return 0, ErrCatalog
			}
			depth--
			if depth == 0 {
				return offset + 1, nil
			}
		}
		offset++
	}
	return 0, ErrCatalog
}

func (x *expectedSpanIndex) read(start, depth int) (expectedSpan, int, error) {
	x.nodes++
	if start >= len(x.raw) || x.nodes > 4096 {
		return expectedSpan{}, 0, ErrCatalog
	}
	span := expectedSpan{}
	// root=0, variants=1, variant=2, family=3, scope=4, category=5.
	if depth >= 5 || x.raw[start] != '{' && x.raw[start] != '[' {
		end, err := x.skip(start, depth)
		if err != nil {
			return expectedSpan{}, 0, ErrCatalog
		}
		span.raw = x.raw[start:end:end]
		return span, end, nil
	}
	kind := x.raw[start]
	end := start + 1
	if kind == '{' {
		span.fields = map[string]expectedSpan{}
		for end < len(x.raw) && x.raw[end] != '}' {
			keyEnd, err := x.quoted(end)
			if err != nil {
				return expectedSpan{}, 0, ErrCatalog
			}
			var key string
			if json.Unmarshal(x.raw[end:keyEnd], &key) != nil {
				return expectedSpan{}, 0, ErrCatalog
			}
			if _, duplicate := span.fields[key]; duplicate {
				return expectedSpan{}, 0, ErrCatalog
			}
			end = keyEnd
			if end >= len(x.raw) || x.raw[end] != ':' {
				return expectedSpan{}, 0, ErrCatalog
			}
			value, valueEnd, err := x.read(end+1, depth+1)
			if err != nil {
				return expectedSpan{}, 0, ErrCatalog
			}
			span.fields[key], end = value, valueEnd
			if end < len(x.raw) && x.raw[end] == ',' {
				end++
				if end >= len(x.raw) || x.raw[end] == '}' {
					return expectedSpan{}, 0, ErrCatalog
				}
				continue
			}
			break
		}
		if end >= len(x.raw) || x.raw[end] != '}' {
			return expectedSpan{}, 0, ErrCatalog
		}
	} else {
		for end < len(x.raw) && x.raw[end] != ']' {
			value, valueEnd, err := x.read(end, depth+1)
			if err != nil {
				return expectedSpan{}, 0, ErrCatalog
			}
			span.elements, end = append(span.elements, value), valueEnd
			if end < len(x.raw) && x.raw[end] == ',' {
				end++
				if end >= len(x.raw) || x.raw[end] == ']' {
					return expectedSpan{}, 0, ErrCatalog
				}
				continue
			}
			break
		}
		if end >= len(x.raw) || x.raw[end] != ']' {
			return expectedSpan{}, 0, ErrCatalog
		}
	}
	end++
	span.raw = x.raw[start:end:end]
	return span, end, nil
}

func expectedSpanKeys(span expectedSpan, keys ...string) bool {
	if len(span.fields) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := span.fields[key]; !ok {
			return false
		}
	}
	return true
}

// Only decodeExpected calls this function, after SHA, strict parsing, complete
// validation and whole-root canonical equality. ownedCanonical is its fresh
// encoding, so retained spans cannot alias mutable caller raw or another decode.
func cacheAuthenticatedVariantDocuments(d *descriptorSet, ownedCanonical []byte) error {
	if d == nil {
		return ErrCatalog
	}
	root, err := expectedCanonicalSpans(ownedCanonical)
	if err != nil || !expectedSpanKeys(root, "format_version", "postgres_major", "mode", "variants") || !bytes.Equal(root.fields["format_version"].raw, []byte("1")) || !bytes.Equal(root.fields["postgres_major"].raw, []byte("16")) {
		return ErrCatalog
	}
	var mode string
	if json.Unmarshal(root.fields["mode"].raw, &mode) != nil || mode != d.Mode || d.FormatVersion != 1 || d.PostgresMajor != 16 {
		return ErrCatalog
	}
	variants := root.fields["variants"]
	if len(variants.raw) == 0 || variants.raw[0] != '[' || len(variants.elements) != len(d.Variants) {
		return ErrCatalog
	}
	type cachedDocument struct {
		doc   document
		bytes []byte
	}
	schemas, roles := map[string]cachedDocument{}, map[string]cachedDocument{}
	type cachedCategory struct {
		value any
		bytes []byte
	}
	categories := map[string]cachedCategory{}
	save := func(doc document, raw []byte, cache map[string]cachedDocument) (cachedDocument, error) {
		key := digest(raw)
		if old, ok := cache[key]; ok {
			if !bytes.Equal(raw, old.bytes) {
				return cachedDocument{}, ErrCatalog
			}
			return old, nil
		}
		value := cachedDocument{doc: doc, bytes: raw}
		cache[key] = value
		return value, nil
	}
	for i := range d.Variants {
		variant, rawVariant := &d.Variants[i], variants.elements[i]
		if !expectedSpanKeys(rawVariant, "name", "schema", "roles") {
			return ErrCatalog
		}
		var name string
		if json.Unmarshal(rawVariant.fields["name"].raw, &name) != nil || name != variant.Name {
			return ErrCatalog
		}
		schema, role := rawVariant.fields["schema"], rawVariant.fields["roles"]
		for _, family := range []struct {
			doc  document
			span expectedSpan
		}{{variant.Schema, schema}, {variant.Roles, role}} {
			if len(family.span.raw) == 0 || family.span.raw[0] != '{' || len(family.span.fields) != len(family.doc) {
				return ErrCatalog
			}
			for scope, value := range family.doc {
				doc, ok := value.(object)
				if !ok {
					return ErrCatalog
				}
				scopeSpan, ok := family.span.fields[scope]
				if !ok || len(scopeSpan.raw) == 0 || scopeSpan.raw[0] != '{' || len(scopeSpan.fields) != len(doc) {
					return ErrCatalog
				}
				for name, value := range doc {
					part, ok := scopeSpan.fields[name]
					if !ok {
						return ErrCatalog
					}
					if _, array := value.([]any); !array {
						encoded, err := canonical(value)
						if err != nil || !bytes.Equal(encoded, part.raw) {
							return ErrCatalog
						}
						continue
					}
					if len(part.raw) == 0 || part.raw[0] != '[' {
						return ErrCatalog
					}
					key := digest(part.raw)
					if old, ok := categories[key]; ok {
						if !bytes.Equal(part.raw, old.bytes) {
							return ErrCatalog
						}
						doc[name] = old.value
					} else {
						categories[key] = cachedCategory{value: value, bytes: part.raw}
					}
				}
			}
		}
		s, err := save(variant.Schema, schema.raw, schemas)
		if err != nil {
			return ErrCatalog
		}
		r, err := save(variant.Roles, role.raw, roles)
		if err != nil {
			return ErrCatalog
		}
		variant.Schema, variant.schemaCanonical = s.doc, s.bytes
		variant.Roles, variant.rolesCanonical = r.doc, r.bytes
	}
	return nil
}
