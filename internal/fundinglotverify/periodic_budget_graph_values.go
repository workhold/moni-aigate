package fundinglotverify

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

type pbGraphObject struct{ kind, id, extra string }
type pbGraphPair struct{ a, b string }
type pbGraphSubject struct{ control, kind, id string }
type pbGraphWindowKey struct {
	control string
	start   time.Time
}
type pbGraphAmount struct {
	held, spent big.Int
	valid       bool
	count       uint64
	tail        string
}
type pbGraphState struct {
	held, spent, source big.Int
	state               string
	revision            uint64
	tail                string
	valid               bool
}
type pbGraphWork struct {
	ctx                                                   context.Context
	err                                                   error
	steps, bytes, count                                   int
	out                                                   PeriodicBudgetGraphCheck
	seen                                                  map[*uint64]map[pbGraphObject]bool
	excluded, invalid                                     map[pbGraphObject]bool
	controls                                              map[string]PeriodicBudgetControlRow
	windows                                               map[string]PeriodicBudgetWindowRow
	allocs                                                map[string]PeriodicBudgetAllocationRow
	events                                                map[string]PeriodicBudgetEventRow
	ops                                                   map[string]PeriodicBudgetOperationRow
	versions                                              map[pbGraphPair]PeriodicBudgetVersionRow
	tenants                                               map[string]ProcurementAttemptTenantRow
	keys                                                  map[string]PeriodicBudgetKeyRow
	users                                                 map[string]ProcurementApprovalUserRow
	requests                                              map[string]PeriodicBudgetRequestRow
	reservations                                          map[string]CustomerKeyReservationRow
	settlements                                           map[string]PeriodicBudgetSettlementRow
	attempts                                              map[string]PeriodicBudgetAttemptRow
	accounts                                              map[string]ProcurementBudgetAccountRow
	bindings                                              map[string]PeriodicBudgetBindingRow
	procurement                                           map[string]PeriodicBudgetSupplierRow
	pe                                                    map[string]PeriodicBudgetProcurementEventRow
	audits                                                map[string]PeriodicBudgetAuditRow
	scopes                                                map[pbGraphPair]string
	windowStarts                                          map[pbGraphWindowKey]string
	subjects                                              map[pbGraphSubject]string
	effects                                               map[pbGraphSubject]string
	versionOps                                            map[string]pbGraphPair
	requestReservations, requestSettlements, auditTargets map[string]string
	controlEvents                                         map[string][]PeriodicBudgetEventRow
	allocationEvents                                      map[string][]PeriodicBudgetEventRow
	sourcePE                                              map[string][]PeriodicBudgetProcurementEventRow
	states                                                map[string]*pbGraphState
	controlReplay, windowReplay                           map[string]*pbGraphAmount
}

func newPBGraph(ctx context.Context) *pbGraphWork {
	return &pbGraphWork{ctx: ctx, seen: map[*uint64]map[pbGraphObject]bool{}, excluded: map[pbGraphObject]bool{}, invalid: map[pbGraphObject]bool{}, scopes: map[pbGraphPair]string{}, windowStarts: map[pbGraphWindowKey]string{}, subjects: map[pbGraphSubject]string{}, effects: map[pbGraphSubject]string{}, versionOps: map[string]pbGraphPair{}, requestReservations: map[string]string{}, requestSettlements: map[string]string{}, auditTargets: map[string]string{}, controlEvents: map[string][]PeriodicBudgetEventRow{}, allocationEvents: map[string][]PeriodicBudgetEventRow{}, sourcePE: map[string][]PeriodicBudgetProcurementEventRow{}, states: map[string]*pbGraphState{}, controlReplay: map[string]*pbGraphAmount{}, windowReplay: map[string]*pbGraphAmount{}}
}
func pbObj(kind, id string) pbGraphObject { return pbGraphObject{kind: kind, id: id} }
func (w *pbGraphWork) mark(field *uint64, o pbGraphObject) {
	set := w.seen[field]
	if set == nil {
		set = map[pbGraphObject]bool{}
		w.seen[field] = set
	}
	if !set[o] {
		set[o] = true
		*field++
	}
}
func (w *pbGraphWork) check() error {
	if errors.Is(w.ctx.Err(), context.DeadlineExceeded) {
		return ErrPeriodicBudgetGraphRowsTimeout
	}
	if w.ctx.Err() != nil {
		return ErrPeriodicBudgetGraphRowsCanceled
	}
	return w.err
}
func (w *pbGraphWork) tick() bool {
	w.steps++
	if w.steps&255 == 0 {
		if e := w.check(); e != nil {
			w.err = e
		}
	}
	return w.err == nil
}
func (w *pbGraphWork) active(o pbGraphObject) bool { return !w.excluded[o] }
func (w *pbGraphWork) valid(o pbGraphObject) bool  { return w.active(o) && !w.invalid[o] }
func (w *pbGraphWork) invalidField(field *uint64, o pbGraphObject) {
	w.mark(field, o)
	w.invalid[o] = true
}
func (w *pbGraphWork) budget(v reflect.Value) error {
	if e := w.check(); e != nil {
		return e
	}
	counts := reflect.ValueOf(&w.out.Scanned).Elem()
	for i := 0; i < v.NumField(); i++ {
		rows := v.Field(i)
		if rows.Len() > 100000-w.count {
			return ErrPeriodicBudgetGraphRowsResource
		}
		w.count += rows.Len()
		counts.Field(i).SetUint(uint64(rows.Len()))
		for j := 0; j < rows.Len(); j++ {
			if !w.tick() {
				return w.check()
			}
			if !w.budgetValue(rows.Index(j)) {
				return ErrPeriodicBudgetGraphRowsResource
			}
		}
	}
	return w.check()
}
func (w *pbGraphWork) charge(n int, field bool) bool {
	if field && n > 65536 || n > (16<<20)-w.bytes {
		return false
	}
	w.bytes += n
	return true
}
func (w *pbGraphWork) budgetValue(v reflect.Value) bool {
	if v.Type() == reflect.TypeOf(PeriodicBudgetGraphTime{}) {
		return w.charge(32, false)
	}
	switch v.Kind() {
	case reflect.String:
		return w.charge(v.Len(), true)
	case reflect.Bool:
		return w.charge(1, false)
	case reflect.Pointer:
		if v.IsNil() {
			return true
		}
		return w.budgetValue(v.Elem())
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return w.charge(v.Len(), true)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !w.budgetValue(v.Field(i)) {
				return false
			}
		}
	}
	return true
}
func pbText(v reflect.Value) bool {
	if v.Type() == reflect.TypeOf(PeriodicBudgetGraphTime{}) {
		return true
	}
	switch v.Kind() {
	case reflect.String:
		return utf8.ValidString(v.String()) && !strings.ContainsRune(v.String(), 0)
	case reflect.Pointer:
		return v.IsNil() || pbText(v.Elem())
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			b := v.Bytes()
			return utf8.Valid(b) && !strings.ContainsRune(string(b), 0)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !pbText(v.Field(i)) {
				return false
			}
		}
	}
	return true
}
func pbPrimary[T any, K comparable](w *pbGraphWork, rows []T, kind string, key func(T) K, obj func(T) pbGraphObject) map[K]T {
	out := make(map[K]T, len(rows))
	for _, r := range rows {
		if !w.tick() {
			break
		}
		k := key(r)
		if _, ok := out[k]; ok {
			w.out.Mismatches.DuplicatePrimaryRows++
			continue
		}
		out[k] = r
		if !pbText(reflect.ValueOf(r)) {
			w.invalidField(&w.out.Mismatches.InvalidTextRows, obj(r))
		}
	}
	return out
}
func pbNatural[K comparable](w *pbGraphWork, m map[K]string, key K, id string, field *uint64, o pbGraphObject) {
	if _, ok := m[key]; ok {
		*field++
		w.excluded[o] = true
		return
	}
	m[key] = id
}
func pbCanonical(s string, signed bool, digits int, max string) (*big.Int, bool) {
	whole := s
	if signed && strings.HasPrefix(s, "-") {
		whole = s[1:]
		if whole == "0" {
			return nil, false
		}
	}
	if whole == "" || len(whole) > digits || len(whole) > 1 && whole[0] == '0' {
		return nil, false
	}
	for i := range whole {
		if whole[i] < '0' || whole[i] > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, false
	}
	if max != "" {
		bound, _ := new(big.Int).SetString(max, 10)
		if n.Cmp(bound) > 0 {
			return nil, false
		}
	}
	return n, true
}
func (w *pbGraphWork) number(s string, signed bool, digits int, max string, field *uint64, o pbGraphObject) *big.Int {
	n, ok := pbCanonical(s, signed, digits, max)
	if !ok {
		// Missing parents produce relation findings, never invented invalid rows.
		if w.hasObject(o) {
			w.invalidField(field, o)
		}
		return nil
	}
	return n
}

func (w *pbGraphWork) hasObject(o pbGraphObject) bool {
	switch o.kind {
	case "controls":
		_, ok := w.controls[o.id]
		return ok
	case "windows":
		_, ok := w.windows[o.id]
		return ok
	case "allocations":
		_, ok := w.allocs[o.id]
		return ok
	case "events":
		_, ok := w.events[o.id]
		return ok
	case "operations":
		_, ok := w.ops[o.id]
		return ok
	case "versions":
		_, ok := w.versions[pbGraphPair{o.id, o.extra}]
		return ok
	case "reservations":
		_, ok := w.reservations[o.id]
		return ok
	case "settlements":
		_, ok := w.settlements[o.id]
		return ok
	case "accounts":
		_, ok := w.accounts[o.id]
		return ok
	case "procurement":
		_, ok := w.procurement[o.id]
		return ok
	case "procurement_events":
		_, ok := w.pe[o.id]
		return ok
	case "requests":
		_, ok := w.requests[o.id]
		return ok
	case "attempts":
		_, ok := w.attempts[o.id]
		return ok
	}
	return false
}
func (w *pbGraphWork) amount(s string, small, signed bool, o pbGraphObject) *big.Int {
	max := ""
	digits := 100
	if small {
		max = "9223372036854775807"
		digits = 19
	}
	return w.number(s, signed, digits, max, &w.out.Mismatches.InvalidAmountRows, o)
}
func (w *pbGraphWork) revision(s string, positive bool, o pbGraphObject) *big.Int {
	n := w.number(s, false, 19, "9223372036854775807", &w.out.Mismatches.InvalidRevisionRows, o)
	if n != nil && positive && n.Sign() == 0 {
		w.invalidField(&w.out.Mismatches.InvalidRevisionRows, o)
		return nil
	}
	return n
}
func (w *pbGraphWork) xid(s string, o pbGraphObject) {
	w.number(s, false, 20, "18446744073709551615", &w.out.Mismatches.InvalidXIDRows, o)
}
func pbUUID(s string) bool {
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
func (w *pbGraphWork) uuid(s string, o pbGraphObject) {
	if !pbUUID(s) {
		w.invalidField(&w.out.Mismatches.InvalidUUIDRows, o)
	}
}
func pbFinite(t PeriodicBudgetGraphTime) bool {
	return t.Valid && t.Infinity == 0 && t.Value.Nanosecond()%1000 == 0 && t.Value.Year() >= -4712 && t.Value.Year() <= 294276
}
func pbNullTime(t PeriodicBudgetGraphTime) bool {
	return !t.Valid && t.Infinity == 0 && t.Value.IsZero()
}
func (w *pbGraphWork) timestamp(t PeriodicBudgetGraphTime, required bool, o pbGraphObject) {
	if pbFinite(t) || !required && pbNullTime(t) {
		return
	}
	w.invalidField(&w.out.Mismatches.InvalidTimeRows, o)
}
func pbTimeEq(a, b PeriodicBudgetGraphTime) bool {
	return pbFinite(a) && pbFinite(b) && a.Value.Equal(b.Value)
}
func pbSame(a, b *string) bool             { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func pbString(a *string, want string) bool { return a != nil && *a == want }
func pbEnum(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func (w *pbGraphWork) enum(s string, o pbGraphObject, values ...string) {
	if !pbEnum(s, values...) {
		w.invalidField(&w.out.Mismatches.InvalidEnumRows, o)
	}
}
func pbPeriod(kind string) bool { return kind == "utc_day" || kind == "utc_month" }
func pbStart(kind string, t time.Time) time.Time {
	t = t.UTC()
	day := t.Day()
	if kind == "utc_month" {
		day = 1
	}
	return time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, time.UTC)
}
func pbEnd(kind string, t time.Time) time.Time {
	if kind == "utc_month" {
		return t.AddDate(0, 1, 0)
	}
	return t.AddDate(0, 0, 1)
}
func pbTarget(c PeriodicBudgetControlRow) (string, bool) {
	switch c.Scope {
	case "tenant":
		if c.TenantID != nil && c.KeyID == nil && c.AccountID == nil {
			return *c.TenantID, true
		}
	case "key":
		if c.TenantID != nil && c.KeyID != nil && c.AccountID == nil {
			return *c.KeyID, true
		}
	case "procurement":
		if c.TenantID == nil && c.KeyID == nil && c.AccountID != nil {
			return *c.AccountID, true
		}
	}
	return "", false
}
func pbState(kind string) string {
	switch kind {
	case "prepared", "sending", "unknown", "reserved", "consumption_unknown", "recovered_unknown":
		return "held"
	case "settled", "cost_confirmed", "cost_corrected":
		return "settled"
	case "released", "released_no_consumption", "expired_without_permit":
		return "released"
	case "disputed", "evidence_disputed":
		return "disputed"
	}
	return ""
}
func pbEqual(n *big.Int, s string) bool {
	v, ok := pbCanonical(s, true, 100, "")
	return n != nil && ok && n.Cmp(v) == 0
}
func pbPositivePart(a, b *big.Int) *big.Int {
	n := new(big.Int).Sub(a, b)
	if n.Sign() < 0 {
		n.SetInt64(0)
	}
	return n
}
func pbAdd(m map[string]*pbGraphAmount, key string, h, s *big.Int) {
	a := m[key]
	if a == nil {
		a = &pbGraphAmount{valid: true}
		m[key] = a
	}
	if h == nil || s == nil {
		a.valid = false
		return
	}
	a.held.Add(&a.held, h)
	a.spent.Add(&a.spent, s)
}
