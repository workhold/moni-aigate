package fundinglotcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"sort"
)

// encoding/json sorts object keys. Array order is retained unless explicitly
// declared a set by the DTO. HTML escaping is disabled for original prosrc.
func canonical(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(v) != nil {
		return nil, ErrCatalog
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}

func digest(b []byte) string { return fmtHex(sha256.Sum256(b)) }
func fmtHex(sum [32]byte) string {
	const digits = "0123456789abcdef"
	var b [64]byte
	for i, x := range sum {
		b[2*i], b[2*i+1] = digits[x>>4], digits[x&15]
	}
	return string(b[:])
}

func decode(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil, ErrCatalog
	}
	return v, nil
}

func sortSet(a []any) error {
	type item struct {
		value any
		bytes []byte
	}
	items := make([]item, len(a))
	for i, v := range a {
		b, e := canonical(v)
		if e != nil {
			return e
		}
		items[i] = item{v, b}
	}
	sort.Slice(items, func(i, j int) bool { return bytes.Compare(items[i].bytes, items[j].bytes) < 0 })
	for i := range items {
		if i > 0 && bytes.Equal(items[i-1].bytes, items[i].bytes) {
			return ErrCatalog
		}
		a[i] = items[i].value
	}
	return nil
}

func sortRecords(a []any) error {
	type item struct {
		value any
		key   string
	}
	items := make([]item, len(a))
	for i, v := range a {
		k, e := recordIdentity(v)
		if e != nil {
			return e
		}
		items[i] = item{v, k}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	for i, x := range items {
		if i > 0 && items[i-1].key == x.key {
			return ErrCatalog
		}
		a[i] = x.value
	}
	return nil
}

func dependencyMultiset(a []any) ([]any, error) {
	byID := map[string]object{}
	counts := map[string]int{}
	for _, v := range a {
		k, e := recordIdentity(v)
		if e != nil {
			return nil, e
		}
		m, ok := v.(object)
		if !ok {
			return nil, ErrCatalog
		}
		if old, exists := byID[k]; exists && !same(old, m) {
			return nil, ErrCatalog
		}
		byID[k] = m
		counts[k]++
	}
	out := make([]any, 0, len(byID))
	for k, v := range byID {
		copy := object{}
		for n, x := range v {
			copy[n] = x
		}
		copy["multiplicity"] = counts[k]
		out = append(out, copy)
	}
	if sortRecords(out) != nil {
		return nil, ErrCatalog
	}
	return out, nil
}

func same(a, b any) bool {
	x, e := canonical(a)
	if e != nil {
		return false
	}
	y, e := canonical(b)
	return e == nil && bytes.Equal(x, y)
}

func recordIdentity(v any) (string, error) {
	m, ok := v.(object)
	if !ok {
		return "", ErrCatalog
	}
	if id, ok := m["identity"]; ok {
		b, e := canonical(id)
		return string(b), e
	}
	if after, ok := m["after"]; ok {
		return recordIdentity(after)
	}
	k := object{}
	for _, name := range []string{"object", "referenced", "role", "member", "grantor", "grantee", "privilege", "kind", "catalog", "owner", "namespace", "object_type", "name"} {
		if v, ok := m[name]; ok {
			k[name] = v
		}
	}
	if len(k) == 0 {
		return "", ErrCatalog
	}
	b, e := canonical(k)
	return string(b), e
}
