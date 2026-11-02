package fundinglotbasis

import (
	"bytes"
	"context"
	"encoding/binary"
)

type recordField struct {
	oid   uint32
	null  bool
	value []byte
}

// Only the record envelope and fixed-width send payloads are checked. Variable
// native payloads remain opaque; their bytes are never normalized or reencoded.
func parseRecord(ctx context.Context, raw []byte, columns []column) ([]recordField, error) {
	if ctx.Err() != nil {
		return nil, contextFailure(ctx)
	}
	if len(raw) < 4 || binary.BigEndian.Uint32(raw[:4]) != uint32(len(columns)) {
		return nil, ErrExecution
	}
	offset := 4
	out := make([]recordField, len(columns))
	for i, c := range columns {
		if ctx.Err() != nil {
			return nil, contextFailure(ctx)
		}
		if len(raw)-offset < 8 {
			return nil, ErrExecution
		}
		oid := binary.BigEndian.Uint32(raw[offset:])
		length := int32(binary.BigEndian.Uint32(raw[offset+4:]))
		offset += 8
		if oid != c.oid || length < -1 {
			return nil, ErrExecution
		}
		f := recordField{oid: oid, null: length == -1}
		if f.null {
			if c.notNull {
				return nil, ErrExecution
			}
		} else {
			if int64(length) > int64(len(raw)-offset) {
				return nil, ErrExecution
			}
			f.value = raw[offset : offset+int(length)]
			offset += int(length)
			width := 0
			switch oid {
			case 16:
				width = 1
			case 20, 1184, 5069:
				width = 8
			case 21:
				width = 2
			case 23, 26:
				width = 4
			case 2950:
				width = 16
			}
			if width != 0 && len(f.value) != width {
				return nil, ErrExecution
			}
		}
		out[i] = f
	}
	if offset != len(raw) {
		return nil, ErrExecution
	}
	return out, nil
}
func validateRecord(ctx context.Context, t table, key, row []byte) error {
	k, e := parseRecord(ctx, key, t.pkColumns)
	if e != nil {
		return e
	}
	r, e := parseRecord(ctx, row, t.columns)
	if e != nil {
		return e
	}
	for i, index := range t.pkIndexes {
		a, b := k[i], r[index]
		if a.null || a.oid != b.oid || a.null != b.null || !bytes.Equal(a.value, b.value) {
			return ErrExecution
		}
	}
	return nil
}
