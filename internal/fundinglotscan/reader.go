package fundinglotscan

import (
	"bytes"
	"context"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func nilValue(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return r.IsNil()
	}
	return false
}

// Read can produce local facts only. The maintenance adapter exclusively owns
// real lease construction, authenticated mode, schema and the final Finish.
func Read(ctx context.Context, q Query, schema string, mode Mode) (Facts, error) {
	if ctx == nil || nilValue(q) || schema == "" || len(schema) > 63 || !utf8.ValidString(schema) || strings.ContainsRune(schema, 0) || mode != ModeOrdinary && mode != ModeSandbox {
		return Facts{}, ErrInput
	}
	if ctx.Err() != nil {
		return Facts{}, contextFailure(ctx)
	}
	tables, index, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings())
	if err != nil || len(tables) != tableCount {
		return Facts{}, ErrExecution
	}
	s := &session{ctx: ctx, query: q, schema: schema, mode: mode, tables: tables, index: index, rejected: map[cellRef]bool{}}
	if err = s.readAll(); err != nil {
		return Facts{}, readFailure(ctx, err)
	}
	if err = s.check(); err != nil {
		return Facts{}, readFailure(ctx, err)
	}
	if ctx.Err() != nil {
		return Facts{}, contextFailure(ctx)
	}
	s.report.Scope = "complete_business_source_graphs_v1"
	s.report.Mode = mode
	s.report.PhysicalEOFComplete = true
	s.report.ScannedTables = uint64(len(s.tables))
	s.report.SourceRows = uint64(s.rows)
	s.report.SourceBytes = uint64(s.bytes)
	s.report.InputMismatchFields = uint64(len(s.rejected))
	return Facts{state: s}, nil
}

func (s *session) readAll() error {
	for i := range s.tables {
		if err := s.readTable(i); err != nil {
			return err
		}
	}
	var rows int
	for _, t := range s.tables {
		if !t.eof {
			return ErrExecution
		}
		rows += len(t.rows)
	}
	if rows != s.rows {
		return ErrExecution
	}
	if s.ctx.Err() != nil {
		return contextFailure(s.ctx)
	}
	return nil
}

func readFailure(ctx context.Context, err error) error {
	// Closing an unfinished real lease can revoke its context. Preserve the
	// resource failure which caused that cleanup instead of relabeling it.
	if err == ErrResource {
		return ErrResource
	}
	if ctx.Err() != nil {
		return contextFailure(ctx)
	}
	if err == ErrInput {
		return ErrInput
	}
	return ErrExecution
}

func identifier(name string) string { return pgx.Identifier{name}.Sanitize() }
func columnValue(alias string, c column) string {
	v := alias + "." + identifier(c.name)
	if c.kind == textKind {
		v += "::pg_catalog.text"
	} else if c.kind == timeKind {
		// Native binary preserves every finite PG timestamp even when the
		// lease's simple-protocol text codec cannot parse a five-digit year.
		v = "pg_catalog.timestamptz_send(" + v + ")"
	}
	return v
}
func keyValue(alias string, k key, projected bool) string {
	v := alias + "." + identifier(k.name)
	if k.numeric {
		if projected {
			v += "::pg_catalog.int8"
		}
		return v
	}
	return v + "::pg_catalog.text COLLATE pg_catalog.\"C\""
}

// The materialized page bounds the transported values before CASE projection.
// It contains no business predicate and does not claim a server memory bound.
func pageSQL(schema string, spec tableSpec) string {
	var inner, outer, conditions, keys, right, order, outerOrder []string
	for _, c := range spec.columns {
		v := columnValue("s", c)
		inner = append(inner, v+" AS "+identifier(c.name))
		outer = append(outer, "CASE WHEN p.__qualified THEN p."+identifier(c.name)+" ELSE NULL END")
		if c.kind == textKind || c.kind == bytesKind {
			conditions = append(conditions, "("+v+" IS NULL OR pg_catalog.octet_length("+v+") OPERATOR(pg_catalog.<=) "+strconv.Itoa(columnLimit(c))+"::pg_catalog.int4)")
		}
	}
	for i, k := range spec.keys {
		keys = append(keys, keyValue("s", k, false))
		cast := "::pg_catalog.text COLLATE pg_catalog.\"C\""
		if k.numeric {
			cast = "::pg_catalog.int8"
		}
		right = append(right, "$"+strconv.Itoa(i+2)+cast)
		order = append(order, keyValue("s", k, false)+" USING OPERATOR(pg_catalog.<)")
		outerOrder = append(outerOrder, keyValue("p", k, true)+" USING OPERATOR(pg_catalog.<)")
	}
	qualified := "true"
	if len(conditions) > 0 {
		qualified = strings.Join(conditions, " AND ")
	}
	return "WITH page AS MATERIALIZED (SELECT (" + qualified + ") AS __qualified," + strings.Join(inner, ",") + " FROM ONLY " + pgx.Identifier{schema, spec.name}.Sanitize() + " AS s WHERE (NOT $1::pg_catalog.bool OR ROW(" + strings.Join(keys, ",") + ") OPERATOR(pg_catalog.>) ROW(" + strings.Join(right, ",") + ")) ORDER BY " + strings.Join(order, ",") + " LIMIT 128) SELECT p.__qualified," + strings.Join(outer, ",") + " FROM page AS p ORDER BY " + strings.Join(outerOrder, ",")
}

func (s *session) readTable(index int) error {
	t := &s.tables[index]
	if t.eof {
		return ErrExecution
	}
	hasCursor := false
	cursor := make([]any, len(t.spec.keys))
	for i, k := range t.spec.keys {
		if k.numeric {
			cursor[i] = int64(0)
		} else {
			cursor[i] = ""
		}
	}
	sql := pageSQL(s.schema, t.spec)
	for {
		if s.ctx.Err() != nil {
			return contextFailure(s.ctx)
		}
		args := append([]any{hasCursor}, cursor...)
		rows, err := s.query.Query(sql, args...)
		if err != nil || nilValue(rows) {
			return contextFailure(s.ctx)
		}
		count := 0
		last := append([]any(nil), cursor...)
		for {
			if s.ctx.Err() != nil {
				_ = rows.Close()
				return contextFailure(s.ctx)
			}
			more, e := rows.Next()
			if e != nil {
				_ = rows.Close()
				return contextFailure(s.ctx)
			}
			if !more {
				break
			}
			if count >= pageSize {
				_ = rows.Close()
				return ErrExecution
			}
			row, e := scanRow(rows, t.spec)
			if e != nil {
				_ = rows.Close()
				return e
			}
			next, e := rowCursor(t.spec, row)
			if e != nil || (hasCursor || count > 0) && !cursorAfter(next, last, t.spec.keys) {
				_ = rows.Close()
				return ErrExecution
			}
			if e = s.addTableRow(t.spec, row); e != nil {
				_ = rows.Close()
				return e
			}
			t.rows = append(t.rows, row)
			last = next
			count++
		}
		err = rows.Err()
		closed := rows.Close()
		if s.ctx.Err() != nil {
			return contextFailure(s.ctx)
		}
		if err != nil || closed != nil {
			return ErrExecution
		}
		if count < pageSize {
			t.eof = true
			return nil
		}
		hasCursor, cursor = true, last
	}
}

func scanRow(rows Rows, spec tableSpec) (rawRow, error) {
	n := len(spec.columns)
	texts := make([]pgtype.Text, n)
	bools := make([]*bool, n)
	times := make([]pgtype.Timestamptz, n)
	raw := make([][]byte, n)
	qualified := false
	dest := make([]any, n+1)
	dest[0] = &qualified
	for i, c := range spec.columns {
		switch c.kind {
		case textKind:
			dest[i+1] = &texts[i]
		case boolKind:
			dest[i+1] = &bools[i]
		case timeKind:
			dest[i+1] = &raw[i]
		case bytesKind:
			dest[i+1] = &raw[i]
		default:
			return nil, ErrExecution
		}
	}
	if rows.Scan(dest...) != nil {
		return nil, ErrExecution
	}
	if !qualified {
		return nil, ErrResource
	}
	row := make(rawRow, n)
	for i, col := range spec.columns {
		c := cell{kind: col.kind}
		switch col.kind {
		case textKind:
			c.valid = texts[i].Valid
			c.text = strings.Clone(texts[i].String)
		case boolKind:
			c.valid = bools[i] != nil
			if c.valid {
				c.boolean = *bools[i]
			}
		case timeKind:
			if raw[i] != nil && len(raw[i]) != 8 {
				return nil, ErrExecution
			}
			codec := pgtype.TimestamptzCodec{}
			plan := codec.PlanScan(nil, pgtype.TimestamptzOID, pgx.BinaryFormatCode, &times[i])
			if plan == nil || plan.Scan(raw[i], &times[i]) != nil {
				return nil, ErrExecution
			}
			c.valid = times[i].Valid
			c.instant = times[i]
		case bytesKind:
			c.valid = raw[i] != nil
			c.data = bytes.Clone(raw[i])
		}
		row[i] = c
	}
	return row, nil
}

func (s *session) addRow(row rawRow) error {
	return s.addTableRow(tableSpec{}, row)
}

func (s *session) addTableRow(spec tableSpec, row rawRow) error {
	if s.rows >= rowLimit {
		return ErrResource
	}
	n := 0
	for i, c := range row {
		limit := fieldLimit
		if len(spec.columns) != 0 {
			if len(spec.columns) != len(row) {
				return ErrExecution
			}
			limit = columnLimit(spec.columns[i])
		}
		switch c.kind {
		case textKind:
			if c.valid {
				if len(c.text) > limit {
					return ErrResource
				}
				n += len(c.text)
			}
		case bytesKind:
			if c.valid {
				if len(c.data) > limit {
					return ErrResource
				}
				n += len(c.data)
			}
		case boolKind:
			n++
		case timeKind:
			n += 32
		default:
			return ErrExecution
		}
	}
	if n > byteLimit-s.bytes {
		return ErrResource
	}
	s.rows++
	s.bytes += n
	return nil
}

func rowCursor(spec tableSpec, row rawRow) ([]any, error) {
	result := make([]any, len(spec.keys))
	for i, k := range spec.keys {
		ci, ok := spec.index[k.name]
		if !ok || ci >= len(row) || !row[ci].valid {
			return nil, ErrExecution
		}
		c := row[ci]
		if k.numeric {
			n, err := strconv.ParseInt(c.text, 10, 64)
			if c.kind != textKind || err != nil || strconv.FormatInt(n, 10) != c.text {
				return nil, ErrExecution
			}
			result[i] = n
		} else if c.kind == boolKind {
			result[i] = strconv.FormatBool(c.boolean)
		} else if c.kind == textKind {
			result[i] = c.text
		} else {
			return nil, ErrExecution
		}
	}
	return result, nil
}
func cursorAfter(next, previous []any, keys []key) bool {
	for i, k := range keys {
		if k.numeric {
			a, b := next[i].(int64), previous[i].(int64)
			if a != b {
				return a > b
			}
		} else {
			a, b := next[i].(string), previous[i].(string)
			if a != b {
				return a > b
			}
		}
	}
	return false
}
