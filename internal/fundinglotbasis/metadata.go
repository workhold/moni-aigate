package fundinglotbasis

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

type environment struct {
	DatabaseOID            uint32  `json:"database_oid"`
	SchemaOID              uint32  `json:"schema_oid"`
	ServerVersionNum       uint32  `json:"server_version_num"`
	DatabaseEncoding       int32   `json:"database_encoding"`
	ServerEncoding         string  `json:"server_encoding"`
	ClientEncoding         string  `json:"client_encoding"`
	DatabaseEncodingName   string  `json:"database_encoding_name"`
	Provider               string  `json:"provider"`
	Collate                string  `json:"collate"`
	Ctype                  string  `json:"ctype"`
	ICULocale              *string `json:"icu_locale"`
	ICURules               *string `json:"icu_rules"`
	CollationVersion       *string `json:"collation_version"`
	ActualCollationVersion *string `json:"actual_collation_version"`
}
type relationMetadata struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	Persistence      string `json:"persistence"`
	AccessMethodOID  uint32 `json:"access_method_oid"`
	RowSecurity      bool   `json:"row_security"`
	ForceRowSecurity bool   `json:"force_row_security"`
	Partition        bool   `json:"partition"`
	Inherits         bool   `json:"inherits"`
}
type attributeMetadata struct {
	Table        string `json:"table"`
	Ordinal      int32  `json:"ordinal"`
	Name         string `json:"name"`
	OID          uint32 `json:"oid"`
	Typmod       int32  `json:"typmod"`
	NotNull      bool   `json:"not_null"`
	CollationOID uint32 `json:"collation_oid"`
	Dropped      bool   `json:"dropped"`
}
type primaryKeyMetadata struct {
	Table     string   `json:"table"`
	KeyCount  int32    `json:"key_count"`
	Unique    bool     `json:"unique"`
	Valid     bool     `json:"valid"`
	Ready     bool     `json:"ready"`
	Live      bool     `json:"live"`
	Immediate bool     `json:"immediate"`
	Plain     bool     `json:"plain"`
	Keys      []string `json:"keys"`
}
type reader struct {
	ctx                         context.Context
	q                           Query
	schema                      string
	materials                   *materials
	metadataRows, metadataBytes int
	rows, bytes                 int
}

func validText(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
func metadataText(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return metadataText(v.Elem())
		}
	case reflect.String:
		return v.Len() <= metadataTextLimit && validText(v.String())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !metadataText(v.Field(i)) {
				return false
			}
		}
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if !metadataText(v.Index(i)) {
				return false
			}
		}
	}
	return true
}

// consumeMetadata closes each result before another query and never publishes
// partial facts. The Query owner remains responsible for actual SQL cancellation.
func (s *reader) consumeMetadata(sql string, maxRows int, consume func([]byte) error) error {
	if s.ctx.Err() != nil {
		return contextFailure(s.ctx)
	}
	rows, e := s.q.Query(sql, s.schema, int64(metadataRowLimit-s.metadataRows), int64(metadataByteLimit-s.metadataBytes))
	if e != nil || nilValue(rows) {
		if !nilValue(rows) {
			_ = rows.Close()
		}
		return contextFailure(s.ctx)
	}
	count := 0
	fail := func(e error) error { _ = rows.Close(); return failure(s.ctx, e) }
	for {
		if s.ctx.Err() != nil {
			return fail(contextFailure(s.ctx))
		}
		more, e := rows.Next()
		if e != nil {
			return fail(ErrExecution)
		}
		if !more {
			break
		}
		var sane, qualified bool
		var payload *string
		if rows.Scan(&sane, &qualified, &payload) != nil {
			return fail(ErrExecution)
		}
		if !sane {
			return fail(ErrMetadata)
		}
		if !qualified {
			return fail(ErrResource)
		}
		if count >= maxRows {
			return fail(ErrMetadata)
		}
		if payload == nil || !utf8.ValidString(*payload) {
			return fail(ErrExecution)
		}
		if s.metadataRows >= metadataRowLimit || len(*payload) > metadataByteLimit-s.metadataBytes {
			return fail(ErrResource)
		}
		s.metadataRows++
		s.metadataBytes += len(*payload)
		count++
		if e = consume([]byte(*payload)); e != nil {
			return fail(e)
		}
	}
	e = rows.Err()
	closed := rows.Close()
	if s.ctx.Err() != nil {
		return contextFailure(s.ctx)
	}
	if e != nil || closed != nil {
		return ErrExecution
	}
	return nil
}
func decodeMetadata(raw []byte, out any) error {
	if !metadataShape(raw, reflect.TypeOf(out).Elem()) {
		return ErrMetadata
	}
	if decode(raw, out) != nil {
		return ErrMetadata
	}
	// Metadata projections are closed structs. Missing false/zero-valued fields
	// must not silently become a successful match to the expected profile.
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return ErrMetadata
	}
	t := reflect.TypeOf(out).Elem()
	if len(keys) != t.NumField() {
		return ErrMetadata
	}
	for i := 0; i < t.NumField(); i++ {
		if _, ok := keys[t.Field(i).Tag.Get("json")]; !ok {
			return ErrMetadata
		}
	}
	if !metadataText(reflect.ValueOf(out)) {
		return ErrResource
	}
	return nil
}

// Scalar null must not silently become false, zero, or an empty string.
// Only declared nullable fields accept it; all dynamic function fields are NULL.
func metadataShape(raw []byte, t reflect.Type) bool {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface
	}
	switch t.Kind() {
	case reflect.Pointer:
		return metadataShape(raw, t.Elem())
	case reflect.Interface:
		return false
	case reflect.Struct:
		if !uniqueMetadataObject(raw) {
			return false
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != t.NumField() {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			v, ok := fields[f.Tag.Get("json")]
			if !ok || !metadataShape(v, f.Type) {
				return false
			}
		}
		return true
	case reflect.Slice:
		var values []json.RawMessage
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
			return false
		}
		for _, v := range values {
			if !metadataShape(v, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.String:
		var v string
		return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &v) == nil
	case reflect.Bool:
		return bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))
	case reflect.Int, reflect.Int32, reflect.Uint32:
		if len(raw) == 0 {
			return false
		}
		for i, b := range raw {
			if b == '-' && i == 0 {
				continue
			}
			if b < '0' || b > '9' {
				return false
			}
		}
		return json.Valid(raw)
	}
	return false
}

func uniqueMetadataObject(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		key, e := d.Token()
		name, ok := key.(string)
		if e != nil || !ok || seen[name] {
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
	}
	end, e := d.Token()
	if e != nil || end != json.Delim('}') {
		return false
	}
	return d.Decode(new(any)) == io.EOF
}
func (s *reader) metadata() (environment, error) {
	var env environment
	count := 0
	e := s.consumeMetadata(environmentSQL(), 2, func(raw []byte) error {
		count++
		if count != 1 {
			return ErrMetadata
		}
		return decodeMetadata(raw, &env)
	})
	if e != nil {
		return environment{}, e
	}
	if count != 1 || env.DatabaseOID == 0 || env.SchemaOID == 0 || env.ServerVersionNum/10000 != 16 || env.DatabaseEncoding != 6 || env.ServerEncoding != "UTF8" || env.ClientEncoding != "UTF8" || env.DatabaseEncodingName != "UTF8" || env.Provider != "c" && env.Provider != "i" || env.Collate == "" || env.Ctype == "" {
		return environment{}, ErrMetadata
	}
	count = 0
	e = s.consumeMetadata(relationsSQL(s.materials), s.materials.profile.tables+1, func(raw []byte) error {
		var r relationMetadata
		if e := decodeMetadata(raw, &r); e != nil {
			return e
		}
		if count >= len(s.materials.tables) || r.Name != s.materials.tables[count].name || r.Kind != "r" || r.Persistence != "p" || r.AccessMethodOID != 2 || r.RowSecurity || r.ForceRowSecurity || r.Partition || r.Inherits {
			return ErrMetadata
		}
		count++
		return nil
	})
	if e != nil {
		return environment{}, e
	}
	if count != s.materials.profile.tables {
		return environment{}, ErrMetadata
	}
	byName := make(map[string]table, s.materials.profile.tables)
	seen := make(map[string]int, s.materials.profile.tables)
	lastTable := ""
	var lastOrdinal int32
	for _, t := range s.materials.tables {
		byName[t.name] = t
	}
	e = s.consumeMetadata(attributesSQL(s.materials), s.materials.profile.columns+2, func(raw []byte) error {
		var a attributeMetadata
		if e := decodeMetadata(raw, &a); e != nil {
			return e
		}
		t, ok := byName[a.Table]
		if !ok || a.Dropped || a.Table < lastTable || a.Table == lastTable && a.Ordinal <= lastOrdinal {
			return ErrMetadata
		}
		lastTable, lastOrdinal = a.Table, a.Ordinal
		i := seen[a.Table]
		seen[a.Table] = i + 1
		if i == len(t.columns) && a.Table == "tenants" && a.Ordinal == 9 && a.Name == "fl_created_xid" && a.OID == 5069 && a.Typmod == -1 && !a.NotNull && a.CollationOID == 0 {
			return nil
		}
		if i >= len(t.columns) {
			return ErrMetadata
		}
		c := t.columns[i]
		if a.Ordinal != c.ordinal || a.Name != c.name || a.OID != c.oid || a.Typmod != c.typmod || a.NotNull != c.notNull || a.CollationOID != c.collation {
			return ErrMetadata
		}
		return nil
	})
	if e != nil {
		return environment{}, e
	}
	for _, t := range s.materials.tables {
		n := seen[t.name]
		if n != len(t.columns) && !(t.name == "tenants" && n == len(t.columns)+1) {
			return environment{}, ErrMetadata
		}
	}
	count = 0
	e = s.consumeMetadata(primaryKeysSQL(s.materials), s.materials.profile.tables+1, func(raw []byte) error {
		var p primaryKeyMetadata
		if e := decodeMetadata(raw, &p); e != nil {
			return e
		}
		if count >= s.materials.profile.tables {
			return ErrMetadata
		}
		t := s.materials.tables[count]
		if p.Table != t.name || p.KeyCount != int32(len(t.primaryKey)) || !p.Unique || !p.Valid || !p.Ready || !p.Live || !p.Immediate || !p.Plain || !reflect.DeepEqual(p.Keys, t.primaryKey) {
			return ErrMetadata
		}
		count++
		return nil
	})
	if e != nil {
		return environment{}, e
	}
	if count != s.materials.profile.tables {
		return environment{}, ErrMetadata
	}
	count = 0
	e = s.consumeMetadata(typesSQL(s.materials), 16, func(raw []byte) error {
		var t typeMetadata
		if e := decodeMetadata(raw, &t); e != nil {
			return e
		}
		if count >= 15 || !reflect.DeepEqual(t, s.materials.send.Types[count]) {
			return ErrMetadata
		}
		count++
		return nil
	})
	if e != nil {
		return environment{}, e
	}
	if count != 15 {
		return environment{}, ErrMetadata
	}
	count = 0
	e = s.consumeMetadata(functionsSQL(s.materials), 17, func(raw []byte) error {
		var f functionMetadata
		if e := decodeMetadata(raw, &f); e != nil {
			return e
		}
		if count >= 16 || !reflect.DeepEqual(f, s.materials.send.Functions[count]) {
			return ErrMetadata
		}
		count++
		return nil
	})
	if e != nil {
		return environment{}, e
	}
	if count != 16 {
		return environment{}, ErrMetadata
	}
	count = 0
	e = s.consumeMetadata(manifestSQL(s.schema), 23, func(raw []byte) error {
		var m manifestEntry
		if e := decodeMetadata(raw, &m); e != nil {
			return e
		}
		if count >= 22 || m != s.materials.manifest[count] {
			return ErrMetadata
		}
		count++
		return nil
	})
	if e != nil {
		return environment{}, e
	}
	if count != 22 {
		return environment{}, ErrMetadata
	}
	if s.ctx.Err() != nil {
		return environment{}, contextFailure(s.ctx)
	}
	return env, nil
}
