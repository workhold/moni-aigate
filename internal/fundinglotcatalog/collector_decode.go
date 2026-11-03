package fundinglotcatalog

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// decodeCatalogJSON is only for PostgreSQL jsonb produced by the fixed registry:
// jsonb guarantees duplicate-key elimination, and the registry's object keys are
// static and unique. No business JSON, user payload, or expected artifact enters
// this path. Those inputs retain the duplicate-detecting strict token parser.
func decodeCatalogJSON(raw []byte) (any, error) {
	if len(raw) > collectorRecordLimit || !utf8.Valid(raw) {
		return nil, ErrCatalog
	}
	if value, ok := decodeEffectivePrivilegeJSON(raw); ok {
		return value, nil
	}
	return decodeCatalogJSONGeneral(raw)
}

// Keep the original generic path for every input outside the independently
// closed fast shape, including escaped or otherwise unusual fixed JSON values.
func decodeCatalogJSONGeneral(raw []byte) (any, error) {
	if len(raw) > collectorRecordLimit || !utf8.Valid(raw) {
		return nil, ErrCatalog
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil {
		return nil, ErrCatalog
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrCatalog
	}
	nodes := 0
	var check func(any, int) error
	check = func(v any, depth int) error {
		nodes++
		if nodes > 1000000 || depth > 64 {
			return ErrCatalog
		}
		switch x := v.(type) {
		case nil, bool:
			return nil
		case string:
			if len(x) > collectorRecordLimit || strings.ContainsRune(x, 0) || !utf8.ValidString(x) {
				return ErrCatalog
			}
			return nil
		case json.Number:
			if !validCanonicalNumber(string(x)) {
				return ErrCatalog
			}
			return nil
		case object:
			for k, v := range x {
				if len(k) > collectorRecordLimit || strings.ContainsRune(k, 0) || !utf8.ValidString(k) || check(v, depth+1) != nil {
					return ErrCatalog
				}
			}
			return nil
		case []any:
			if len(x) > collectorRowLimit {
				return ErrCatalog
			}
			for _, v := range x {
				if check(v, depth+1) != nil {
					return ErrCatalog
				}
			}
			return nil
		default:
			return ErrCatalog
		}
	}
	if check(value, 0) != nil {
		return nil, ErrCatalog
	}
	return value, nil
}
