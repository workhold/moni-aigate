package periodicbudgetprefix

import (
	"context"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type object struct{ kind, id, extra string }

type windowKey struct {
	control string
	start   time.Time
}

type versionKey struct{ control, revision string }

func uuid(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
		} else if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

func sequence(s string, positive bool) (uint64, bool) {
	if s == "" || len(s) > 19 || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, e := strconv.ParseUint(s, 10, 63)
	return n, e == nil && (!positive || n > 0)
}

func xid(s string) bool {
	if s == "" || len(s) > 20 || len(s) > 1 && s[0] == '0' {
		return false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	_, e := strconv.ParseUint(s, 10, 64)
	return e == nil
}

func amount(s string, signed bool) (*big.Int, bool) {
	digits := s
	if signed && strings.HasPrefix(s, "-") {
		digits = s[1:]
		if digits == "0" {
			return nil, false
		}
	}
	if digits == "" || len(digits) > 100 || len(digits) > 1 && digits[0] == '0' {
		return nil, false
	}
	for i := range digits {
		if digits[i] < '0' || digits[i] > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	return n, ok
}

func finite(t Time) bool {
	return t.Valid && t.Infinity == 0 && t.Value.Nanosecond()%1000 == 0 && t.Value.Year() >= -4712 && t.Value.Year() <= 294276
}

func nullTime(t Time) bool            { return !t.Valid && t.Infinity == 0 && t.Value.IsZero() }
func equalTime(a, b Time) bool        { return finite(a) && finite(b) && a.Value.Equal(b.Value) }
func same(a, b *string) bool          { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func equals(a *string, b string) bool { return a != nil && *a == b }
func optionalUUID(p *string) bool     { return p == nil || uuid(*p) }

func periodStart(period string, t time.Time) time.Time {
	t = t.UTC()
	day := t.Day()
	if period == "utc_month" {
		day = 1
	}
	return time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, time.UTC)
}

type budget struct {
	ctx                context.Context
	deadline           time.Time
	rows, bytes, steps uint64
}

func (b *budget) check() error {
	if !b.deadline.IsZero() && !time.Now().Before(b.deadline) {
		return ErrTimeout
	}
	err := b.ctx.Err()
	if err == context.DeadlineExceeded {
		return ErrTimeout
	}
	if err != nil {
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

func (b *budget) charge(n uint64, field bool) error {
	if field && n > 65536 || n > (16<<20)-b.bytes {
		return ErrResource
	}
	b.bytes += n
	return nil
}

func (b *budget) value(v reflect.Value) error {
	if err := b.tick(); err != nil {
		return err
	}
	if v.Type() == reflect.TypeOf(Time{}) {
		return b.charge(32, false)
	}
	switch v.Kind() {
	case reflect.String:
		return b.charge(uint64(v.Len()), true)
	case reflect.Bool:
		return b.charge(1, false)
	case reflect.Pointer:
		if !v.IsNil() {
			return b.value(v.Elem())
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return b.charge(uint64(v.Len()), true)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if e := b.value(v.Field(i)); e != nil {
				return e
			}
		}
	}
	return nil
}

func (b *budget) arrays(v reflect.Value) error {
	for i := 0; i < v.NumField(); i++ {
		r := v.Field(i)
		if r.Kind() == reflect.Struct {
			if e := b.arrays(r); e != nil {
				return e
			}
			continue
		}
		if uint64(r.Len()) > 100000-b.rows {
			return ErrResource
		}
		b.rows += uint64(r.Len())
		for j := 0; j < r.Len(); j++ {
			if e := b.value(r.Index(j)); e != nil {
				return e
			}
		}
	}
	return b.check()
}

func validText(v reflect.Value) bool {
	if v.Type() == reflect.TypeOf(Time{}) {
		return true
	}
	switch v.Kind() {
	case reflect.String:
		return utf8.ValidString(v.String()) && !strings.ContainsRune(v.String(), 0)
	case reflect.Pointer:
		return v.IsNil() || validText(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !validText(v.Field(i)) {
				return false
			}
		}
	}
	return true
}
