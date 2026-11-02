package fundinglotverify

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	walletSequenceRowLimit   = 100000
	walletSequenceByteLimit  = 16 << 20
	walletSequenceFieldLimit = 65536
	walletSequenceMaxBigint  = uint64(1<<63 - 1)
)

type wsObject struct {
	kind uint8
	row  int
}

const (
	wsEpoch uint8 = iota
	wsTenant
	wsOperation
	wsHead
	wsLegacy
	wsJournal
	wsEvent
)

type wsWork struct {
	ctx      context.Context
	err      error
	steps    uint16
	out      WalletSequenceRowsCheck
	findings map[*uint64]map[wsObject]bool
}

func (w *wsWork) check() error {
	err := w.ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrWalletSequenceRowsTimeout
	}
	if err != nil {
		return ErrWalletSequenceRowsCanceled
	}
	return w.err
}

func (w *wsWork) tick() bool {
	w.steps++
	if w.steps&255 == 0 {
		w.err = w.check()
	}
	return w.err == nil
}

// Each direction counts a physical row at most once. This identity is never
// derived from a caller primary key, which may itself be repeated or invalid.
func (w *wsWork) mark(field *uint64, o wsObject) {
	seen := w.findings[field]
	if seen == nil {
		seen = make(map[wsObject]bool)
		w.findings[field] = seen
	}
	if !seen[o] {
		seen[o] = true
		*field++
	}
}

// This pass runs before any relation index or parsed large integer is
// allocated. Reflection only traverses the fixed seven raw DTO groups.
func (w *wsWork) budget(in WalletSequenceRows) error {
	v := reflect.ValueOf(in)
	remainingRows := walletSequenceRowLimit
	for i := 0; i < v.NumField(); i++ {
		if !w.tick() {
			return w.check()
		}
		n := v.Field(i).Len()
		if n > remainingRows {
			return ErrWalletSequenceRowsResource
		}
		remainingRows -= n
	}
	remainingBytes := walletSequenceByteLimit
	var walk func(reflect.Value) bool
	walk = func(field reflect.Value) bool {
		if !w.tick() {
			return false
		}
		switch field.Kind() {
		case reflect.String:
			n := field.Len()
			if n > walletSequenceFieldLimit || n > remainingBytes {
				w.err = ErrWalletSequenceRowsResource
				return false
			}
			remainingBytes -= n
		case reflect.Pointer:
			return field.IsNil() || walk(field.Elem())
		case reflect.Struct:
			for i := 0; i < field.NumField(); i++ {
				if !walk(field.Field(i)) {
					return false
				}
			}
		}
		return true
	}
	for i := 0; i < v.NumField(); i++ {
		group := v.Field(i)
		for j := 0; j < group.Len(); j++ {
			if !walk(group.Index(j)) {
				return w.check()
			}
		}
	}
	return w.check()
}

func wsText(value any) bool {
	var walk func(reflect.Value) bool
	walk = func(v reflect.Value) bool {
		switch v.Kind() {
		case reflect.String:
			s := v.String()
			return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
		case reflect.Pointer:
			return v.IsNil() || walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if !walk(v.Field(i)) {
					return false
				}
			}
		}
		return true
	}
	return walk(reflect.ValueOf(value))
}

func wsUUID(s string) bool {
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

func wsUint(s string, positive, bigint bool) (uint64, bool) {
	if s == "" || len(s) > 20 || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil && (!positive || n > 0) && (!bigint || n <= walletSequenceMaxBigint)
}

// PostgreSQL NUMERIC can preserve decimal scale. Parse the exact integral
// value without a floating point conversion, SQL predicate, or int64 sum.
func (w *wsWork) decimal(s string) (*big.Int, bool) {
	if err := w.check(); err != nil {
		w.err = err
		return nil, false
	}
	if s == "" {
		return nil, false
	}
	start := 0
	negative := s[0] == '-'
	if s[0] == '-' || s[0] == '+' {
		start++
	}
	if start == len(s) {
		return nil, false
	}
	dot, digits := -1, 0
	for i := start; i < len(s); i++ {
		if !w.tick() {
			return nil, false
		}
		if s[i] == '.' && dot < 0 {
			dot = i
			continue
		}
		if s[i] < '0' || s[i] > '9' || dot >= 0 && s[i] != '0' {
			return nil, false
		}
		digits++
	}
	if digits == 0 {
		return nil, false
	}
	end := len(s)
	if dot >= 0 {
		end = dot
	}
	whole := s[start:end]
	if whole == "" {
		whole = "0"
	}
	n, ok := new(big.Int).SetString(whole, 10)
	if !ok {
		return nil, false
	}
	if negative {
		n.Neg(n)
	}
	if err := w.check(); err != nil {
		w.err = err
		return nil, false
	}
	return n, true
}

type wsDeltas struct {
	a, h, s, i *big.Int
	valid      bool
}

func (w *wsWork) deltas(a, h, s, issued string, o wsObject) wsDeltas {
	av, aok := w.decimal(a)
	hv, hok := w.decimal(h)
	sv, sok := w.decimal(s)
	iv, iok := w.decimal(issued)
	d := wsDeltas{av, hv, sv, iv, aok && hok && sok && iok}
	if !d.valid {
		w.mark(&w.out.Mismatches.InvalidValueRows, o)
		return d
	}
	sum := new(big.Int).Add(av, hv)
	sum.Add(sum, sv)
	if iv.Sign() < 0 || sum.Cmp(iv) != 0 {
		w.mark(&w.out.Mismatches.DeltaRows, o)
	}
	return d
}

func (d wsDeltas) zero() bool {
	return d.valid && d.a.Sign() == 0 && d.h.Sign() == 0 && d.s.Sign() == 0 && d.i.Sign() == 0
}

func wsOperationKind(k string) bool {
	switch k {
	case "epoch_opening", "tenant_genesis", "manual_credit", "voucher_grant", "sandbox_capture", "reserve", "finish", "consumption_refund", "legacy_consumption_refund":
		return true
	}
	return false
}

func wsLegacyKind(k string) bool {
	switch k {
	case "manual_credit", "voucher_grant", "sandbox_capture", "reserve", "finish", "consumption_refund":
		return true
	}
	return false
}

func wsEventKind(k string) bool {
	switch k {
	case "legacy_opening", "funding", "reserve", "finish", "consumption_refund", "legacy_consumption_refund":
		return true
	}
	return false
}

func wsGenesisKind(k string) bool { return k == "epoch_opening" || k == "tenant_genesis" }

func wsSourceKind(k string) string {
	if k == "legacy_consumption_refund" {
		return "consumption_refund"
	}
	return k
}

func wsExpectedEventKind(k string) string {
	switch k {
	case "epoch_opening":
		return "legacy_opening"
	case "manual_credit", "voucher_grant", "sandbox_capture":
		return "funding"
	case "tenant_genesis":
		return ""
	}
	return k
}

func wsJournalKind(k, journalKind string) bool {
	switch k {
	case "manual_credit":
		return journalKind == "credit"
	case "voucher_grant", "sandbox_capture", "reserve":
		return journalKind == k
	case "finish":
		return journalKind == "settled" || journalKind == "released"
	case "consumption_refund", "legacy_consumption_refund":
		return journalKind == "refund"
	}
	return false
}

func wsSameOptional(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
