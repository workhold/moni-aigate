package fundinglotinit

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

const (
	rowLimit        = uint64(100000)
	byteLimit       = uint64(16 << 20)
	fieldLimit      = 65536
	metadataReserve = uint64(1 << 20)
)

type work struct {
	ctx                   context.Context
	in                    Input
	inputRows, inputBytes uint64
	tenants               map[string]*tenant
	accounts              map[string]*account
	accountKinds          map[accountKey]*account
	journals              map[string]*journal
	journalOps            map[string]*journal
	requests              map[string]fundinglotverify.WalletLotsRequestRow
	reservations          map[string]*reservation
	resByRequest          map[string]*reservation
	settlements           map[string]*settlement
	settByRequest         map[string]*settlement
	sources               []source
	generated             map[string]struct{}
	namespace             [16]byte
}
type tenant struct {
	id                          string
	available, held, unfinished big.Int
}
type accountKey struct {
	null         bool
	tenant, kind string
}
type account struct {
	row     fundinglotverify.LedgerAccountRow
	balance int64
	sum     big.Int
}
type journal struct {
	row     fundinglotverify.WalletLotsJournalRow
	lines   []line
	sum     big.Int
	claimed bool
}
type line struct {
	account *account
	delta   int64
}
type reservation struct {
	row     fundinglotverify.WalletLotsReservationRow
	amount  int64
	journal *journal
}
type settlement struct {
	row               fundinglotverify.WalletLotsSettlementRow
	charged, refunded int64
	reservation       *reservation
	approved          big.Int
}
type source struct {
	kind, id, tenant, amount         string
	journal                          *journal
	request, reservation, settlement *string
}

// Build independently checks the supplied raw financial rows and constructs a
// deterministic, descriptive insert plan. A successful plan confers no authority.
func Build(ctx context.Context, in Input) (Plan, error) {
	if ctx == nil {
		return Plan{}, ErrInput
	}
	w := &work{ctx: ctx, in: in}
	if err := w.check(); err != nil {
		return Plan{}, err
	}
	if err := w.budget(); err != nil {
		return Plan{}, err
	}
	if in.Mode != "ordinary" && in.Mode != "sandbox" || !finite(in.EpochAt) || !parseUUID(in.EpochID, &w.namespace) {
		return Plan{}, ErrInput
	}
	if err := w.qualify(); err != nil {
		return Plan{}, err
	}
	if err := w.validateLedger(); err != nil {
		return Plan{}, err
	}
	if err := w.validateSources(); err != nil {
		return Plan{}, err
	}
	if err := w.checkBalances(); err != nil {
		return Plan{}, err
	}
	plan, err := w.generate()
	if err != nil {
		return Plan{}, err
	}
	if err := w.check(); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (w *work) check() error {
	err := w.ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	if err != nil {
		return ErrCanceled
	}
	return nil
}

var timeType = reflect.TypeOf(fundinglotverify.WalletLotsTime{})

func finite(t fundinglotverify.WalletLotsTime) bool {
	return t.Valid && t.Infinity == 0 && t.Value.Year() >= -4712 && t.Value.Year() <= 294276 && t.Value.Nanosecond()%1000 == 0
}

// Resource failures are returned immediately when recognized. Context is checked
// after each row so a later cancellation cannot overwrite an identified limit.
func (w *work) budget() error {
	v := reflect.ValueOf(w.in.Rows)
	for i := 0; i < v.NumField(); i++ {
		n := uint64(v.Field(i).Len())
		if n > rowLimit-w.inputRows {
			return ErrResource
		}
		w.inputRows += n
	}
	for i := 0; i < v.NumField(); i++ {
		for j := 0; j < v.Field(i).Len(); j++ {
			if err := countValue(v.Field(i).Index(j), &w.inputBytes, true); err != nil {
				return err
			}
			if err := w.check(); err != nil {
				return err
			}
		}
	}
	return nil
}

func countValue(v reflect.Value, total *uint64, limited bool) error {
	if v.Type() == timeType {
		return addBytes(total, 32)
	}
	switch v.Kind() {
	case reflect.String:
		if limited && v.Len() > fieldLimit {
			return ErrResource
		}
		return addBytes(total, uint64(v.Len()))
	case reflect.Pointer:
		if !v.IsNil() {
			return countValue(v.Elem(), total, limited)
		}
	case reflect.Bool:
		return addBytes(total, 1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := countValue(v.Field(i), total, limited); err != nil {
				return err
			}
		}
	}
	return nil
}
func addBytes(total *uint64, n uint64) error {
	if n > byteLimit || *total > byteLimit-n {
		return ErrResource
	}
	*total += n
	return nil
}

func signed(s string) (int64, bool) {
	if len(s) == 0 || len(s) > 20 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && strconv.FormatInt(n, 10) == s
}
func amount(s string, positive bool) (int64, bool) {
	n, ok := signed(s)
	return n, ok && n >= 0 && (!positive || n > 0)
}

func (w *work) qualify() error {
	v := reflect.ValueOf(w.in.Rows)
	for i := 0; i < v.NumField(); i++ {
		for j := 0; j < v.Field(i).Len(); j++ {
			if err := w.check(); err != nil {
				return err
			}
			if !qualifyValue(v.Field(i).Index(j), "") {
				return ErrInput
			}
		}
	}
	return w.check()
}
func qualifyValue(v reflect.Value, name string) bool {
	if v.Type() == timeType {
		return finite(v.Interface().(fundinglotverify.WalletLotsTime))
	}
	switch v.Kind() {
	case reflect.Pointer:
		return v.IsNil() || qualifyValue(v.Elem(), name)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !qualifyValue(v.Field(i), v.Type().Field(i).Name) {
				return false
			}
		}
	case reflect.String:
		s := v.String()
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return false
		}
		switch name {
		case "Balance", "SignedDelta":
			_, ok := signed(s)
			return ok
		case "CreatedTXID":
			_, ok := amount(s, true)
			return ok
		case "Amount", "ChargedAmount", "RefundedAmount":
			_, ok := amount(s, false)
			return ok
		}
	}
	return true
}

// Own every returned pointer and string, including membership data copied from
// legacy inputs. Time envelopes are native values and remain byte-for-byte data.
func (w *work) owned(v reflect.Value) (reflect.Value, error) {
	if err := w.check(); err != nil {
		return reflect.Value{}, err
	}
	if v.Type() == timeType {
		return v, nil
	}
	switch v.Kind() {
	case reflect.String:
		return reflect.ValueOf(strings.Clone(v.String())), nil
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		value, err := w.owned(v.Elem())
		if err != nil {
			return reflect.Value{}, err
		}
		o := reflect.New(v.Type().Elem())
		o.Elem().Set(value)
		return o, nil
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		o := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			value, err := w.owned(v.Index(i))
			if err != nil {
				return reflect.Value{}, err
			}
			o.Index(i).Set(value)
		}
		return o, nil
	case reflect.Struct:
		o := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			value, err := w.owned(v.Field(i))
			if err != nil {
				return reflect.Value{}, err
			}
			o.Field(i).Set(value)
		}
		return o, nil
	}
	return v, nil
}
