package procurementexecution

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type observerField struct {
	name     string
	oid      uint32
	cast     string
	nullable bool
}
type observerQuery struct {
	table, key string
	fields     []observerField
	order      string
}
type observerRow map[string]any
type observationProbe struct {
	ctx    context.Context
	tx     runnerTx
	schema string
}

const observerVoidOID uint32 = 2278

func obsOID(name, cast string, oid uint32, nullable bool) observerField {
	return observerField{name: name, cast: cast, oid: oid, nullable: nullable}
}
func obsText(name string, n bool) observerField  { return obsOID(name, "", pgtype.TextOID, n) }
func obsInt2(name string, n bool) observerField  { return obsOID(name, "", pgtype.Int2OID, n) }
func obsInt4(name string, n bool) observerField  { return obsOID(name, "", pgtype.Int4OID, n) }
func obsInt8(name string, n bool) observerField  { return obsOID(name, "", pgtype.Int8OID, n) }
func obsBool(name string, n bool) observerField  { return obsOID(name, "", pgtype.BoolOID, n) }
func obsJSON(name string, n bool) observerField  { return obsOID(name, "", pgtype.JSONBOID, n) }
func obsTime(name string, n bool) observerField  { return obsOID(name, "", pgtype.TimestamptzOID, n) }
func obsXID(name string, n bool) observerField   { return obsOID(name, "text", pgtype.TextOID, n) }
func obsMoney(name string, n bool) observerField { return obsOID(name, "text", pgtype.TextOID, n) }

func (r observerRow) text(n string) string { v, _ := r[n].(string); return v }
func (r observerRow) integer(n string) int64 {
	switch v := r[n].(type) {
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	}
	return 0
}
func (r observerRow) boolean(n string) bool        { v, _ := r[n].(bool); return v }
func (r observerRow) timestamp(n string) time.Time { v, _ := r[n].(time.Time); return v }
func (r observerRow) json(n string) []byte         { v, _ := r[n].([]byte); return v }
func (r observerRow) null(n string) bool           { return r[n] == nil }
func (r observerRow) eq(n string, want any) bool {
	got := r[n]
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	switch v := want.(type) {
	case string:
		a, ok := got.(string)
		return ok && a == v
	case []byte:
		a, ok := got.([]byte)
		return ok && bytes.Equal(a, v)
	case time.Time:
		a, ok := got.(time.Time)
		return ok && a.Equal(v)
	case bool:
		a, ok := got.(bool)
		return ok && a == v
	case int:
		return r.eq(n, int64(v))
	case int16:
		return r.eq(n, int64(v))
	case int32:
		return r.eq(n, int64(v))
	case int64:
		switch a := got.(type) {
		case int16:
			return int64(a) == v
		case int32:
			return int64(a) == v
		case int64:
			return a == v
		}
	}
	return false
}

func (q observerQuery) sql(schema string) string {
	parts := make([]string, len(q.fields))
	for n, f := range q.fields {
		parts[n] = pgx.Identifier{f.name}.Sanitize()
		if f.cast != "" {
			parts[n] += "::pg_catalog." + f.cast + " AS " + pgx.Identifier{f.name}.Sanitize()
		}
	}
	sql := "SELECT " + strings.Join(parts, ",") + " FROM " + pgx.Identifier{schema, q.table}.Sanitize() + " WHERE " + pgx.Identifier{q.key}.Sanitize() + "=$1::pg_catalog.text"
	if q.order != "" {
		sql += " ORDER BY " + pgx.Identifier{q.order}.Sanitize()
	}
	return sql
}

func observerScan(rows Rows, fields []observerField) (observerRow, error) {
	dest := make([]any, len(fields))
	for n, f := range fields {
		switch f.oid {
		case pgtype.TextOID:
			dest[n] = new(pgtype.Text)
		case pgtype.Int2OID:
			dest[n] = new(pgtype.Int2)
		case pgtype.Int4OID:
			dest[n] = new(pgtype.Int4)
		case pgtype.Int8OID:
			dest[n] = new(pgtype.Int8)
		case pgtype.BoolOID:
			dest[n] = new(pgtype.Bool)
		case pgtype.TimestamptzOID:
			dest[n] = new(pgtype.Timestamptz)
		case pgtype.JSONBOID:
			dest[n] = new([]byte)
		default:
			return nil, observerProtocol(nil)
		}
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	out := observerRow{}
	for n, f := range fields {
		var value any
		switch v := dest[n].(type) {
		case *pgtype.Text:
			if v.Valid {
				value = v.String
			}
		case *pgtype.Int2:
			if v.Valid {
				value = v.Int16
			}
		case *pgtype.Int4:
			if v.Valid {
				value = v.Int32
			}
		case *pgtype.Int8:
			if v.Valid {
				value = v.Int64
			}
		case *pgtype.Bool:
			if v.Valid {
				value = v.Bool
			}
		case *pgtype.Timestamptz:
			if v.Valid {
				if v.InfinityModifier != pgtype.Finite || v.Time.Year() < 1 || v.Time.Year() > 9999 || v.Time.Nanosecond()%1000 != 0 {
					return nil, observerProtocol(nil)
				}
				value = v.Time
			}
		case *[]byte:
			if *v != nil {
				value = cloneBytes(*v)
			}
		}
		if value == nil && !f.nullable {
			return nil, observerProtocol(nil)
		}
		// xid8 is cast to TEXT to preserve its full unsigned range.
		if value != nil && f.cast == "text" && strings.HasSuffix(f.name, "xid") {
			s := value.(string)
			number, err := strconv.ParseUint(s, 10, 64)
			if err != nil || strconv.FormatUint(number, 10) != s {
				return nil, observerProtocol(nil)
			}
		}
		out[f.name] = value
	}
	return out, nil
}

// Freeze the chosen primary's classification before retaining secondary
// causes. Searching the joined tree could otherwise classify a local scan
// problem instead of a late server failure, or a drain error instead of the
// query failure that preceded it.
func observerPrimaryError(primary, secondary error) error {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return &observationProblem{reason: observationFailureReason(primary), cause: combine(primary, secondary)}
}

func (p *observationProbe) many(q observerQuery, key string, max int) ([]observerRow, error) {
	rows, err := p.tx.query(p.ctx, q.sql(p.schema), key)
	if err != nil {
		if rows != nil {
			err = observerPrimaryError(err, finishRows(rows))
		}
		return nil, err
	}
	if rows == nil {
		return nil, observerProtocol(nil)
	}
	defer rows.Close()
	columns := make([]column, len(q.fields))
	for n, f := range q.fields {
		columns[n] = column{f.name, f.oid}
	}
	if !fieldsMatch(rows, columns) {
		if err := finishRows(rows); err != nil {
			return nil, err
		}
		return nil, observerProtocol(nil)
	}
	var result []observerRow
	var scanErr error
	tooMany := false
	for rows.Next() {
		if len(result) >= max {
			tooMany = true
			break
		}
		var row observerRow
		row, scanErr = observerScan(rows, q.fields)
		if scanErr != nil {
			break
		}
		result = append(result, row)
	}
	late := finishRows(rows)
	if late != nil && (scanErr == nil || !errors.Is(late, scanErr) || !errors.Is(scanErr, late)) {
		return nil, observerPrimaryError(late, scanErr)
	}
	if scanErr != nil {
		var local *observationProblem
		if errors.As(scanErr, &local) {
			return nil, scanErr
		}
		var pg *pgconn.PgError
		if errors.As(scanErr, &pg) {
			return nil, scanErr
		}
		return nil, observerProtocol(scanErr)
	}
	if tooMany {
		return nil, &observationProblem{reason: ReasonReceiptIncomplete}
	}
	return result, nil
}

func (p *observationProbe) one(q observerQuery, key string, absent ObservationReason) (observerRow, error) {
	rows, err := p.many(q, key, 1)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &observationProblem{reason: absent}
	}
	return rows[0], nil
}

func (p *observationProbe) validate(fn string, args ...any) error {
	var casts string
	switch fn {
	case "pp_validate_original":
		if len(args) != 3 || args[0] != "procurement_command" || args[2] != nil {
			return observerProtocol(nil)
		}
		casts = "$1::pg_catalog.text,$2::pg_catalog.text,$3::pg_catalog.text"
	case "pp_validate_attempt", "pp_validate_root":
		if len(args) != 1 {
			return observerProtocol(nil)
		}
		casts = "$1::pg_catalog.text"
	default:
		return observerProtocol(nil)
	}
	rows, err := p.tx.query(p.ctx, "SELECT "+pgx.Identifier{p.schema, fn}.Sanitize()+"("+casts+")", args...)
	if err != nil {
		if rows != nil {
			err = observerPrimaryError(err, finishRows(rows))
		}
		return err
	}
	if rows == nil {
		return observerProtocol(nil)
	}
	defer rows.Close()
	if !fieldsMatch(rows, []column{{fn, observerVoidOID}}) {
		if err := finishRows(rows); err != nil {
			return err
		}
		return observerProtocol(nil)
	}
	first := rows.Next()
	extra := false
	if first {
		extra = rows.Next()
	}
	if err := finishRows(rows); err != nil {
		return err
	}
	if !first || extra {
		return observerProtocol(nil)
	}
	return nil
}
