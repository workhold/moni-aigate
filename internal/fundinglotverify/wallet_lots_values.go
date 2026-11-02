package fundinglotverify

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"unicode/utf8"
)

func (w *wlWork) check() error {
	err := w.ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrWalletLotsRowsTimeout
	}
	if err != nil {
		return ErrWalletLotsRowsCanceled
	}
	return w.err
}
func (w *wlWork) tick() bool {
	w.steps++
	if w.steps&255 == 0 {
		w.err = w.check()
	}
	return w.err == nil
}
func wlMapSequenceError(err error) error {
	switch err {
	case nil:
		return nil
	case ErrWalletSequenceRowsResource:
		return ErrWalletLotsRowsResource
	case ErrWalletSequenceRowsCanceled:
		return ErrWalletLotsRowsCanceled
	case ErrWalletSequenceRowsTimeout:
		return ErrWalletLotsRowsTimeout
	default:
		return ErrWalletLotsRowsInput
	}
}
func (w *wlWork) integer(text string, ref wlRef) (*big.Int, bool) {
	sw := wsWork{ctx: w.ctx}
	n, ok := sw.decimal(text)
	if sw.err != nil {
		w.err = wlMapSequenceError(sw.err)
	}
	if !ok {
		w.mark(&w.out.Mismatches.InvalidValueRows, ref)
	}
	return n, ok
}
func (w *wlWork) unsigned(text string, positive, bigint bool, ref wlRef) (uint64, bool) {
	n, ok := wsUint(text, positive, bigint)
	if !ok {
		w.mark(&w.out.Mismatches.InvalidValueRows, ref)
	}
	return n, ok
}
func (w *wlWork) amount(text string, positive bool, ref wlRef) (*big.Int, bool) {
	n, ok := w.unsigned(text, positive, true, ref)
	if !ok {
		return nil, false
	}
	return new(big.Int).SetUint64(n), true
}
func (w *wlWork) time(t WalletLotsTime, ref wlRef) bool {
	ok := wlFinite(t)
	if !ok {
		w.mark(&w.out.Mismatches.InvalidTimeRows, ref)
	}
	return ok
}
func wlFinite(t WalletLotsTime) bool {
	return t.Valid && t.Infinity == 0 && t.Value.Year() >= -4712 && t.Value.Year() <= 294276 && t.Value.Nanosecond()%1000 == 0
}
func wlTimeEq(a, b WalletLotsTime) bool { return wlFinite(a) && wlFinite(b) && a.Value.Equal(b.Value) }
func wlDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range s {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

func (w *wlWork) budget() error {
	if err := w.check(); err != nil {
		return err
	}
	v := reflect.ValueOf(w.in)
	rows, bytesLeft := 100000, 16<<20
	for i := 0; i < v.NumField(); i++ {
		n := v.Field(i).Len()
		if n > rows {
			return ErrWalletLotsRowsResource
		}
		rows -= n
	}
	var walk func(reflect.Value, string) bool
	walk = func(v reflect.Value, name string) bool {
		if !w.tick() {
			return false
		}
		if v.Type() == reflect.TypeOf(WalletLotsTime{}) {
			bytesLeft -= 32
			return bytesLeft >= 0
		}
		switch v.Kind() {
		case reflect.String:
			n := v.Len()
			if n > 65536 || n > bytesLeft {
				return false
			}
			bytesLeft -= n
		case reflect.Bool:
			bytesLeft--
			return bytesLeft >= 0
		case reflect.Pointer:
			return v.IsNil() || walk(v.Elem(), name)
		case reflect.Slice:
			// The only nested slice in a row is raw proof bytea.
			limit := 65536
			if name == "ProofCanonical" {
				limit = 262144
			}
			n := v.Len()
			if n > limit || n > bytesLeft {
				return false
			}
			bytesLeft -= n
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if !walk(v.Field(i), v.Type().Field(i).Name) {
					return false
				}
			}
		}
		return true
	}
	for i := 0; i < v.NumField(); i++ {
		for j := 0; j < v.Field(i).Len(); j++ {
			if !walk(v.Field(i).Index(j), "") {
				if err := w.check(); err != nil {
					return err
				}
				return ErrWalletLotsRowsResource
			}
		}
	}
	return w.check()
}

func (w *wlWork) qualify(v reflect.Value, name string, ref wlRef) {
	if !w.tick() {
		return
	}
	if v.Type() == reflect.TypeOf(WalletLotsTime{}) {
		w.time(v.Interface().(WalletLotsTime), ref)
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			w.qualify(v.Elem(), name, ref)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			w.qualify(v.Field(i), v.Type().Field(i).Name, ref)
		}
	case reflect.String:
		text := v.String()
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			w.mark(&w.out.Mismatches.InvalidTextRows, ref)
		}
		switch name {
		case "AvailableDelta", "HeldDelta", "ConsumedDelta", "IssuedDelta", "AvailableTotal", "HeldTotal":
			w.integer(text, ref)
		case "SignedDelta", "Balance":
			if _, ok := ledgerInteger(text); !ok {
				w.mark(&w.out.Mismatches.InvalidValueRows, ref)
			}
		case "CreatedXID":
			w.unsigned(text, true, false, ref)
		case "CreatedTXID":
			w.unsigned(text, true, true, ref)
		case "Amount", "ChargedAmount", "ReleasedAmount", "RefundedAmount", "RefundedAmountAtCutover", "IssuedAmount", "BeforeRevision", "AfterRevision", "BeforeAvailable", "BeforeHeld", "BeforeConsumed", "AfterAvailable", "AfterHeld", "AfterConsumed", "AvailableAmount", "InferenceHeldAmount", "ConsumedAmount", "Revision", "Seq", "PreviousSeq", "EventCount", "TailSeq", "WalletSeq", "AllocationIndex", "TenantCount", "ReceiptCount", "SettlementCount", "ApprovedRefundCount", "OpeningCount", "LegacySourceCount", "SchemaVersion", "ProofVersion":
			w.unsigned(text, false, true, ref)
		case "DatabaseOID", "SchemaOID", "SchemaOwnerOID", "AppRoleOID", "WorkerRoleOID", "ProjectorRoleOID":
			n, ok := w.unsigned(text, true, false, ref)
			if ok && n > 1<<32-1 {
				w.mark(&w.out.Mismatches.InvalidValueRows, ref)
			}
		case "BasisDigest", "ManifestDigest", "DescriptorDigest", "RuntimeProofDigest", "ProofSHA256", "PredecessorPaymentDigest":
			if !wlDigest(text) {
				w.mark(&w.out.Mismatches.InvalidValueRows, ref)
			}
		}
	}
}
