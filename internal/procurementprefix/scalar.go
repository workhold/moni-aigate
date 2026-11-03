package procurementprefix

import (
	"context"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	rowLimit        = uint64(100000)
	byteLimit       = uint64(16 << 20)
	fieldLimit      = uint64(65536)
	routeFieldLimit = uint64(272144)
)

func validText(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }

func decimalDigits(s string, signed bool, maxDigits int) bool {
	if signed && strings.HasPrefix(s, "-") {
		s = s[1:]
		if s == "0" {
			return false
		}
	}
	if len(s) == 0 || len(s) > maxDigits || len(s) > 1 && s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func signedInteger(s string, bits int) bool {
	if !decimalDigits(s, true, 20) {
		return false
	}
	_, err := strconv.ParseInt(s, 10, bits)
	return err == nil
}

func unsignedInteger(s string, bits int, positive bool) bool {
	if !decimalDigits(s, false, 20) {
		return false
	}
	n, err := strconv.ParseUint(s, 10, bits)
	return err == nil && (!positive || n > 0)
}

func numeric(s string, signed bool) (*big.Int, bool) {
	if !decimalDigits(s, signed, 100) {
		return nil, false
	}
	n, ok := new(big.Int).SetString(s, 10)
	return n, ok
}

func finite(t Time) bool {
	return t.Valid && t.InfinityModifier == pgtype.Finite && t.Time.Nanosecond()%1000 == 0 && t.Time.Year() >= -4712 && t.Time.Year() <= 294276
}

func nullTime(t Time) bool { return !t.Valid && t.InfinityModifier == pgtype.Finite && t.Time.IsZero() }

type budget struct {
	ctx                context.Context
	deadline           time.Time
	rows, bytes, steps uint64
}

func (b *budget) check() error {
	if !b.deadline.IsZero() && !time.Now().Before(b.deadline) {
		return ErrTimeout
	}
	if b.ctx.Err() == context.DeadlineExceeded {
		return ErrTimeout
	}
	if b.ctx.Err() != nil {
		return ErrCanceled
	}
	return nil
}

func (b *budget) tick() error {
	b.steps++
	if b.steps&255 == 0 {
		return b.check()
	}
	return nil
}

func (b *budget) charge(n, limit uint64) error {
	if n > limit || n > byteLimit-b.bytes {
		return ErrResource
	}
	b.bytes += n
	return nil
}

var timeType = reflect.TypeOf(Time{})

func (b *budget) cell(v reflect.Value, limit uint64) error {
	if err := b.tick(); err != nil {
		return err
	}
	if v.Type() == timeType {
		return b.charge(32, byteLimit)
	}
	switch v.Kind() {
	case reflect.String:
		return b.charge(uint64(v.Len()), limit)
	case reflect.Bool:
		return b.charge(1, byteLimit)
	case reflect.Pointer:
		if v.Type().Elem().Kind() == reflect.Bool {
			return b.charge(1, byteLimit)
		}
		if !v.IsNil() {
			return b.cell(v.Elem(), limit)
		}
		return nil
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return b.charge(uint64(v.Len()), limit)
		}
	}
	return ErrInput
}

func (b *budget) arrays(v, counts reflect.Value) error {
	for i := 0; i < v.NumField(); i++ {
		if err := b.tick(); err != nil {
			return err
		}
		array := v.Field(i)
		n := uint64(array.Len())
		if n > rowLimit-b.rows {
			return ErrResource
		}
		b.rows += n
		counts.Field(i).SetUint(n)
		table := v.Type().Field(i).Tag.Get("json")
		for j := 0; j < array.Len(); j++ {
			row := array.Index(j)
			for k := 0; k < row.NumField(); k++ {
				limit := fieldLimit
				if (table == "upstream_attempts" || table == "pp_attempt_origins") && row.Type().Field(k).Tag.Get("json") == "route_decisions" {
					limit = routeFieldLimit
				}
				if err := b.cell(row.Field(k), limit); err != nil {
					return err
				}
			}
		}
	}
	return b.check()
}

// budgetRows checks every physical DTO cell before JSON, indexing or bigint
// work. The shared absolute context and budget are never reset between domains.
func budgetRows(ctx context.Context, rows Rows) (Scan, error) {
	if ctx == nil {
		return Scan{}, ErrInput
	}
	b := budget{ctx: ctx}
	b.deadline, _ = ctx.Deadline()
	if err := b.check(); err != nil {
		return Scan{}, err
	}
	var out Scan
	if err := b.arrays(reflect.ValueOf(rows.OriginalRows), reflect.ValueOf(&out.OriginalRows).Elem()); err != nil {
		return Scan{}, err
	}
	if err := b.arrays(reflect.ValueOf(rows.PrefixRows), reflect.ValueOf(&out.PrefixRows).Elem()); err != nil {
		return Scan{}, err
	}
	if err := b.check(); err != nil {
		return Scan{}, err
	}
	out.TotalRows, out.LogicalBytes = b.rows, b.bytes
	return out, nil
}
