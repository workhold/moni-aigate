package procurementexecution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type observerRowsFixture struct {
	fields             []observerField
	data               [][]any
	index              int
	closed             bool
	err, scanErr, late error
}

func (r *observerRowsFixture) FieldDescriptions() []pgconn.FieldDescription {
	out := make([]pgconn.FieldDescription, len(r.fields))
	for n, f := range r.fields {
		out[n] = pgconn.FieldDescription{Name: f.name, DataTypeOID: f.oid}
	}
	return out
}
func (r *observerRowsFixture) Next() bool {
	if r.err != nil || r.index >= len(r.data) {
		return false
	}
	r.index++
	return true
}
func (r *observerRowsFixture) Scan(dest ...any) error {
	if r.scanErr != nil {
		r.err = r.scanErr
		return r.err
	}
	if r.index < 1 || len(dest) != len(r.data[r.index-1]) {
		r.err = errors.New("fixture shape")
		return r.err
	}
	m := pgtype.NewMap()
	for n, value := range r.data[r.index-1] {
		raw, err := m.Encode(r.fields[n].oid, pgtype.TextFormatCode, value, make([]byte, 0, 32))
		if err == nil {
			err = m.Scan(r.fields[n].oid, pgtype.TextFormatCode, raw, dest[n])
		}
		if err != nil {
			r.err = err
			return err
		}
	}
	return nil
}
func (r *observerRowsFixture) Err() error { return r.err }
func (r *observerRowsFixture) Close()     { r.closed = true; r.err = combine(r.err, r.late) }

type observerQueryFixture struct {
	rows  Rows
	err   error
	sql   string
	args  []any
	calls int
}

func (t *observerQueryFixture) exec(context.Context, string) error { return nil }
func (t *observerQueryFixture) query(_ context.Context, sql string, args ...any) (Rows, error) {
	t.calls++
	t.sql = sql
	t.args = args
	return t.rows, t.err
}
func (*observerQueryFixture) commit(context.Context) error   { return errors.New("must not commit") }
func (*observerQueryFixture) rollback(context.Context) error { return nil }

func observerFixtureValues(fields []observerField) []any {
	values := make([]any, len(fields))
	for n, f := range fields {
		switch f.oid {
		case pgtype.TextOID:
			values[n] = "fixture-text"
			if strings.HasSuffix(f.name, "xid") {
				values[n] = "18446744073709551615"
			}
		case pgtype.Int2OID:
			values[n] = int16(1)
		case pgtype.Int4OID:
			values[n] = int32(1)
		case pgtype.Int8OID:
			values[n] = int64(1)
		case pgtype.BoolOID:
			values[n] = false
		case pgtype.JSONBOID:
			values[n] = []byte(`null`)
		case pgtype.TimestamptzOID:
			values[n] = time.Date(2026, 11, 4, 1, 2, 3, 4000, time.UTC)
		}
	}
	return values
}

func observerFixtureQueries() []observerQuery {
	return []observerQuery{observerRootQuery, observerStepsQuery, observerJournalQuery, observerCommandQuery, observerCommandSourceQuery, observerCommandAuditQuery, observerAccountOriginQuery, observerAttemptQuery, observerAttemptOriginQuery, observerAttemptHeadQuery, observerAttemptNodeQuery, observerAttemptReservationQuery, observerAttemptTransitionQuery, observerAttemptEventQuery}
}

func TestObserverEveryFixedProjectionRejectsWrongABIAndPreservesNull(t *testing.T) {
	for _, q := range observerFixtureQueries() {
		t.Run(q.table, func(t *testing.T) {
			if len(q.fields) == 0 {
				t.Fatal("empty fixed projection")
			}
			for n, f := range q.fields {
				for _, kind := range []string{"wrong_name", "wrong_oid", "null"} {
					t.Run(fmt.Sprintf("%s/%s", f.name, kind), func(t *testing.T) {
						fields := append([]observerField(nil), q.fields...)
						values := observerFixtureValues(fields)
						switch kind {
						case "wrong_name":
							fields[n].name += "_wrong"
						case "wrong_oid":
							fields[n].oid = pgtype.ByteaOID
						case "null":
							values[n] = nil
						}
						rows := &observerRowsFixture{fields: fields, data: [][]any{values}}
						tx := &observerQueryFixture{rows: rows}
						p := &observationProbe{ctx: context.Background(), tx: tx, schema: `a.b"c`}
						got, err := p.one(q, "private-key", ReasonReceiptAbsent)
						if kind == "null" && f.nullable {
							if err != nil || !got.null(f.name) {
								t.Fatal("nullable SQL NULL changed", err)
							}
						} else if err == nil || observationFailureReason(err) != ReasonObservationProtocol {
							t.Fatal("wrong ABI/NULL accepted", err)
						}
						if !rows.closed || tx.calls != 1 || !strings.Contains(tx.sql, `"a.b""c".`) || len(tx.args) != 1 || tx.args[0] != "private-key" || strings.Contains(tx.sql, "private-key") || strings.Contains(tx.sql, "SELECT *") {
							t.Fatal("query identity or draining changed")
						}
					})
				}
			}
			for _, extra := range []bool{false, true} {
				fields := append([]observerField(nil), q.fields...)
				if extra {
					fields = append(fields, obsText("extra", false))
				} else {
					fields = fields[:len(fields)-1]
				}
				rows := &observerRowsFixture{fields: fields}
				p := &observationProbe{ctx: context.Background(), tx: &observerQueryFixture{rows: rows}, schema: "s"}
				if _, err := p.one(q, "x", ReasonReceiptAbsent); err == nil || observationFailureReason(err) != ReasonObservationProtocol || !rows.closed {
					t.Fatal("column count accepted")
				}
			}
		})
	}
}

func TestObserverRowCardinalityAndLateErrorsAreNotDowngraded(t *testing.T) {
	q := observerQuery{table: "fixed", key: "id", fields: []observerField{obsText("id", false)}}
	for _, tc := range []struct {
		name     string
		rows     *observerRowsFixture
		queryErr error
		reason   ObservationReason
		cause    error
	}{
		{"absent", &observerRowsFixture{fields: q.fields}, nil, ReasonReceiptAbsent, nil},
		{"extra", &observerRowsFixture{fields: q.fields, data: [][]any{{"a"}, {"b"}}}, nil, ReasonReceiptIncomplete, nil},
		{"scan", &observerRowsFixture{fields: q.fields, data: [][]any{{"a"}}, scanErr: io.ErrUnexpectedEOF}, nil, ReasonObservationProtocol, io.ErrUnexpectedEOF},
		{"late", &observerRowsFixture{fields: q.fields, late: io.EOF}, nil, ReasonObservationUnavailable, io.EOF},
		{"wrong_abi_late", &observerRowsFixture{fields: []observerField{obsInt8("id", false)}, late: io.EOF}, nil, ReasonObservationUnavailable, io.EOF},
		{"query_with_rows", &observerRowsFixture{fields: q.fields, late: io.EOF}, context.Canceled, ReasonObservationTimeout, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &observerQueryFixture{rows: tc.rows, err: tc.queryErr}
			p := &observationProbe{ctx: context.Background(), tx: tx, schema: "s"}
			_, err := p.one(q, "key", ReasonReceiptAbsent)
			if err == nil || observationFailureReason(err) != tc.reason || !tc.rows.closed || tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatal("late/primary cause or cardinality lost", err)
			}
		})
	}
	pg := &pgconn.PgError{Code: "PP902", Message: "private"}
	for _, scan := range []bool{false, true} {
		rows := &observerRowsFixture{fields: q.fields, data: [][]any{{"a"}}}
		if scan {
			rows.scanErr = pg
		} else {
			rows.late = pg
		}
		p := &observationProbe{ctx: context.Background(), tx: &observerQueryFixture{rows: rows}, schema: "s"}
		_, err := p.one(q, "key", ReasonReceiptAbsent)
		var actual *pgconn.PgError
		if observationFailureReason(err) != ReasonHistoryInvariant || !errors.As(err, &actual) || actual != pg {
			t.Fatal("server cause became protocol", err)
		}
	}
}

func observerErrorHasProblem(err error, reason ObservationReason) bool {
	if err == nil {
		return false
	}
	if p, ok := err.(*observationProblem); ok && p.reason == reason {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if observerErrorHasProblem(cause, reason) {
				return true
			}
		}
		return false
	}
	return observerErrorHasProblem(errors.Unwrap(err), reason)
}

func TestObserverLateServerErrorDominatesLocalDecodeProblem(t *testing.T) {
	for _, local := range []struct {
		name  string
		field observerField
		value any
	}{
		{"required_null", obsText("id", false), nil},
		{"infinite_time", obsTime("created_at", false), pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}},
		{"noncanonical_xid", obsXID("created_xid", false), "01"},
	} {
		for _, server := range []struct {
			code   string
			reason ObservationReason
		}{
			{"PP902", ReasonHistoryInvariant},
			{"PP906", ReasonUnsupportedHistory},
			{"57014", ReasonObservationTimeout},
			{"42501", ReasonObservationUnavailable},
		} {
			t.Run(local.name+"/"+server.code, func(t *testing.T) {
				q := observerQuery{table: "fixed", key: "id", fields: []observerField{local.field}}
				pg := &pgconn.PgError{Code: server.code, Message: "private late server error"}
				rows := &observerRowsFixture{fields: q.fields, data: [][]any{{local.value}}, late: pg}
				p := &observationProbe{ctx: context.Background(), tx: &observerQueryFixture{rows: rows}, schema: "s"}
				got, err := p.one(q, "key", ReasonReceiptAbsent)
				if err == nil || got != nil || observationFailureReason(err) != server.reason || !rows.closed {
					t.Fatalf("late server error did not remain primary: reason=%s want=%s", observationFailureReason(err), server.reason)
				}
				var actual *pgconn.PgError
				if !errors.As(err, &actual) || actual != pg || !observerErrorHasProblem(err, ReasonObservationProtocol) {
					t.Fatal("original server or local decode cause lost")
				}
			})
		}
	}
}

func TestObserverQueryFailureKeepsPrimaryClassificationWhenDrainingAlsoFails(t *testing.T) {
	for _, entry := range []string{"projection", "validator"} {
		for _, first := range []struct {
			name   string
			cause  error
			reason ObservationReason
		}{
			{"canceled", context.Canceled, ReasonObservationTimeout},
			{"network", io.ErrUnexpectedEOF, ReasonObservationUnavailable},
			{"privilege", &pgconn.PgError{Code: "42501"}, ReasonObservationUnavailable},
			{"unsupported", &pgconn.PgError{Code: "PP906"}, ReasonUnsupportedHistory},
		} {
			for _, lateCode := range []string{"PP902", "PP906", "57014", "42501"} {
				t.Run(entry+"/"+first.name+"/"+lateCode, func(t *testing.T) {
					q := observerQuery{table: "fixed", key: "id", fields: []observerField{obsText("id", false)}}
					late := &pgconn.PgError{Code: lateCode, Message: "private drain error"}
					rows := &observerRowsFixture{fields: q.fields, late: late}
					p := &observationProbe{ctx: context.Background(), tx: &observerQueryFixture{rows: rows, err: first.cause}, schema: "s"}
					var err error
					if entry == "projection" {
						_, err = p.one(q, "key", ReasonReceiptAbsent)
					} else {
						err = p.validate("pp_validate_root", "key")
					}
					if err == nil || observationFailureReason(err) != first.reason || !rows.closed {
						t.Fatalf("query primary classification changed while draining: reason=%s want=%s", observationFailureReason(err), first.reason)
					}
					if !errors.Is(err, first.cause) || !errors.Is(err, late) {
						t.Fatal("original query or drain cause lost")
					}
				})
			}
		}
	}
}

func TestObserverTypedValuesPreservePrecisionAndTime(t *testing.T) {
	fields := []observerField{obsXID("created_xid", false), obsMoney("held", false), obsInt8("signed", false), obsInt2("version", false), obsJSON("before", false), obsTime("at", false), obsText("empty", false), obsText("null", true)}
	values := []any{"18446744073709551615", strings.Repeat("9", 100), int64(math.MinInt64), int16(1), []byte("null"), time.Date(2026, 11, 4, 9, 0, 0, 123000, time.FixedZone("local", 8*3600)), "", nil}
	rows := &observerRowsFixture{fields: fields, data: [][]any{values}}
	p := &observationProbe{ctx: context.Background(), tx: &observerQueryFixture{rows: rows}, schema: "s"}
	got, err := p.one(observerQuery{table: "fixed", key: "id", fields: fields}, "key", ReasonReceiptAbsent)
	if err != nil || !got.eq("signed", int64(math.MinInt64)) || !got.eq("version", int64(1)) || !got.eq("at", values[5].(time.Time).UTC()) || got.text("held") != strings.Repeat("9", 100) || got.text("created_xid") != "18446744073709551615" || got.null("empty") || !got.null("null") || got.null("before") {
		t.Fatal("precision/NULL/time lost", err)
	}
	for _, xid := range []string{"-1", "01", "+1", "18446744073709551616"} {
		values[0] = xid
		rows = &observerRowsFixture{fields: fields, data: [][]any{values}}
		p.tx = &observerQueryFixture{rows: rows}
		if _, err = p.one(observerQuery{table: "fixed", key: "id", fields: fields}, "key", ReasonReceiptAbsent); err == nil {
			t.Fatal("noncanonical xid accepted")
		}
	}
}

func TestObserverValidatorIsFixedReadOnlyAndDrained(t *testing.T) {
	for _, fn := range []string{"pp_validate_original", "pp_validate_attempt", "pp_validate_root"} {
		args := []any{"key"}
		if fn == "pp_validate_original" {
			args = []any{"procurement_command", "key", nil}
		}
		rows := &observerRowsFixture{fields: []observerField{obsOID(fn, "", observerVoidOID, false)}, data: [][]any{{}}}
		tx := &observerQueryFixture{rows: rows}
		p := &observationProbe{ctx: context.Background(), tx: tx, schema: `dot."quote`}
		if err := p.validate(fn, args...); err != nil || !rows.closed || !strings.HasPrefix(tx.sql, `SELECT "dot.""quote"."`+fn+`"(`) || strings.Contains(tx.sql, "key") {
			t.Fatal("fixed void validator did not drain", err)
		}
	}
	tx := &observerQueryFixture{}
	p := &observationProbe{ctx: context.Background(), tx: tx, schema: "s"}
	for _, fn := range []string{"pp_record_attempt", "pp_create_account", "pp_configure_account", "pb_observe", "pb_gate", "arbitrary"} {
		if err := p.validate(fn, "key"); err == nil || tx.calls != 0 {
			t.Fatal("mutation/free validator dispatched")
		}
	}
}
