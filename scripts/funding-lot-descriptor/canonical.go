package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
)

var errStage = errors.New("descriptor_generation_failed")

// encoding/json sorts object keys. Array order is retained unless explicitly
// declared a set by the DTO. HTML escaping is disabled for original prosrc.
func canonical(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(v) != nil {
		return nil, errStage
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
		return nil, errStage
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
			return errStage
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
			return errStage
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
			return nil, errStage
		}
		if old, exists := byID[k]; exists && !same(old, m) {
			return nil, errStage
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
		return nil, errStage
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

func difference(before, after document) (document, error) {
	d := document{"added": document{}, "changed": document{}, "removed": document{}}
	for k, bv := range before {
		av, ok := after[k]
		if !ok {
			return nil, errStage
		}
		ba, bok := array(bv)
		aa, aok := array(av)
		if !bok || !aok {
			if !same(bv, av) {
				d["changed"].(document)[k] = object{"before": bv, "after": av}
			}
			continue
		}
		bm, am := map[string]any{}, map[string]any{}
		for _, v := range ba {
			x, e := recordIdentity(v)
			if e != nil {
				return nil, e
			}
			if _, ok := bm[x]; ok {
				return nil, errStage
			}
			bm[x] = v
		}
		for _, v := range aa {
			x, e := recordIdentity(v)
			if e != nil {
				return nil, e
			}
			if _, ok := am[x]; ok {
				return nil, errStage
			}
			am[x] = v
		}
		add, rem, changed := []any{}, []any{}, []any{}
		for x, v := range am {
			if b, ok := bm[x]; !ok {
				add = append(add, v)
			} else if !same(b, v) {
				changed = append(changed, object{"before": b, "after": v})
			}
		}
		for x, v := range bm {
			if _, ok := am[x]; !ok {
				rem = append(rem, v)
			}
		}
		if sortRecords(add) != nil || sortRecords(rem) != nil || sortRecords(changed) != nil {
			return nil, errStage
		}
		d["added"].(document)[k] = add
		d["removed"].(document)[k] = rem
		d["changed"].(document)[k] = changed
	}
	return d, nil
}

func roleDifference(before, after document) (document, error) {
	d, e := difference(before, after)
	if e != nil {
		return nil, e
	}
	b, e := canonical(before)
	if e != nil {
		return nil, e
	}
	a, e := canonical(after)
	if e != nil {
		return nil, e
	}
	d["before_sha256"], d["after_sha256"] = digest(b), digest(a)
	return d, nil
}

func recordIdentity(v any) (string, error) {
	m, ok := v.(object)
	if !ok {
		return "", errStage
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
		return "", errStage
	}
	b, e := canonical(k)
	return string(b), e
}
