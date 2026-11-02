package fundinglotscan

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// These descriptors are created exclusively by fixed package declarations.
// @column denotes raw bytea; other []byte targets receive JSONB text bytes.
// A trailing # on a key denotes its physical BIGINT comparison semantics.
type binding struct {
	table   string
	keys    []key
	columns []column
	target  reflect.Type
	valid   bool
}

var goTimeType = reflect.TypeFor[time.Time]()
var budgetTimeType = reflect.TypeFor[fundinglotverify.PeriodicBudgetGraphTime]()

func bind[T any](table, keys, fields string) binding {
	b := binding{table: table, target: reflect.TypeFor[T](), valid: true}
	if b.target.Kind() != reflect.Struct {
		b.valid = false
		return b
	}
	for _, name := range strings.Fields(keys) {
		b.keys = append(b.keys, key{name: strings.TrimSuffix(name, "#"), numeric: strings.HasSuffix(name, "#")})
	}
	names := strings.Fields(fields)
	if len(names) != b.target.NumField() || len(b.keys) == 0 {
		b.valid = false
		return b
	}
	seen := map[string]bool{}
	for i, name := range names {
		raw := strings.HasPrefix(name, "@")
		name = strings.TrimPrefix(name, "@")
		if name == "" || seen[name] {
			b.valid = false
			return b
		}
		seen[name] = true
		t := b.target.Field(i).Type
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		kind := textKind
		switch {
		case t == goTimeType || t == budgetTimeType:
			kind = timeKind
		case t.Kind() == reflect.Bool:
			kind = boolKind
		case t.Kind() == reflect.String || t.Kind() == reflect.Int32:
		case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8:
			if raw {
				kind = bytesKind
			}
		default:
			b.valid = false
			return b
		}
		if raw && kind != bytesKind {
			b.valid = false
			return b
		}
		b.columns = append(b.columns, column{name: name, kind: kind})
	}
	for _, k := range b.keys {
		if !seen[k.name] {
			b.valid = false
		}
	}
	return b
}

func registry(groups ...[]binding) ([]tableData, map[string]int, error) {
	var tables []tableData
	index := map[string]int{}
	for _, group := range groups {
		for _, b := range group {
			if !b.valid {
				return nil, nil, ErrExecution
			}
			i, ok := index[b.table]
			if !ok {
				i = len(tables)
				index[b.table] = i
				tables = append(tables, tableData{spec: tableSpec{name: b.table, keys: append([]key(nil), b.keys...), index: map[string]int{}}})
			}
			spec := &tables[i].spec
			if !reflect.DeepEqual(spec.keys, b.keys) {
				return nil, nil, ErrExecution
			}
			for _, c := range b.columns {
				if j, ok := spec.index[c.name]; ok {
					if spec.columns[j].kind != c.kind {
						return nil, nil, ErrExecution
					}
				} else {
					spec.index[c.name] = len(spec.columns)
					spec.columns = append(spec.columns, c)
				}
			}
		}
	}
	return tables, index, nil
}

// A single non-representable native field disqualifies this complete core
// input. It never produces a zero/sentinel timestamp for an invoked core.
func projectInto[T any](s *session, b binding, out *[]T, qualified *bool) error {
	if !b.valid || b.target != reflect.TypeFor[T]() {
		return ErrExecution
	}
	i, ok := s.index[b.table]
	if !ok || !s.tables[i].eof {
		return ErrExecution
	}
	table := &s.tables[i]
	result := make([]T, 0, len(table.rows))
	for ri, row := range table.rows {
		if s.ctx.Err() != nil {
			return contextFailure(s.ctx)
		}
		var value T
		dest := reflect.ValueOf(&value).Elem()
		for fi, col := range b.columns {
			ci, ok := table.spec.index[col.name]
			if !ok || ci >= len(row) {
				return ErrExecution
			}
			if !assignCell(dest.Field(fi), row[ci]) {
				*qualified = false
				if s.rejected == nil {
					s.rejected = map[cellRef]bool{}
				}
				s.rejected[cellRef{b.table, ri, ci}] = true
			}
		}
		result = append(result, value)
	}
	*out = result
	return nil
}

func assignCell(dest reflect.Value, c cell) bool {
	if dest.Type() == budgetTimeType {
		if c.kind != timeKind {
			return false
		}
		dest.Set(reflect.ValueOf(fundinglotverify.PeriodicBudgetGraphTime{Value: c.instant.Time, Valid: c.instant.Valid, Infinity: int8(c.instant.InfinityModifier)}))
		return true
	}
	if dest.Kind() == reflect.Pointer {
		if !c.valid {
			return true
		}
		value := reflect.New(dest.Type().Elem())
		if !assignCell(value.Elem(), c) {
			return false
		}
		dest.Set(value)
		return true
	}
	if dest.Type() == goTimeType {
		if c.kind != timeKind || !c.instant.Valid || c.instant.InfinityModifier != 0 {
			return false
		}
		dest.Set(reflect.ValueOf(c.instant.Time))
		return true
	}
	if dest.Kind() == reflect.Slice && dest.Type().Elem().Kind() == reflect.Uint8 {
		if !c.valid {
			return true
		}
		if c.kind == bytesKind {
			dest.SetBytes(bytes.Clone(c.data))
			return true
		}
		if c.kind == textKind {
			dest.SetBytes([]byte(c.text))
			return true
		}
		return false
	}
	if !c.valid {
		return false
	}
	switch dest.Kind() {
	case reflect.String:
		if c.kind != textKind {
			return false
		}
		dest.SetString(c.text)
		return true
	case reflect.Bool:
		if c.kind != boolKind {
			return false
		}
		dest.SetBool(c.boolean)
		return true
	case reflect.Int32:
		if c.kind != textKind {
			return false
		}
		n, e := strconv.ParseInt(c.text, 10, 32)
		if e != nil || strconv.FormatInt(n, 10) != c.text {
			return false
		}
		dest.SetInt(n)
		return true
	}
	return false
}
