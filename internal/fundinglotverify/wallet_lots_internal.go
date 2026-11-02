package fundinglotverify

import (
	"context"
	"math/big"
	"reflect"
)

// Fixed table names equal the public row-group fields. No caller names or SQL
// are accepted. A ref identifies a physical supplied row, even with a bad key.
type wlRef struct {
	table string
	index int
}
type wlKey struct{ kind, id string }
type wlField struct{ table, field string }
type wlTable struct {
	rows    reflect.Value
	primary map[wlKey][]wlRef
}
type wlSource struct {
	kind, id, tenant, journalID, journalOperationID string
	requestID, reservationID, settlementID          string
	amount, reserved                                big.Int
	fifo                                            WalletLotsTime
	class                                           string
	legacy, valid                                   bool
	ref, opRef                                      wlRef
	opKnown                                         bool
}
type wlOpeningPlan struct {
	ref    wlRef
	row    WalletLotsLegacyOpeningRow
	amount big.Int
}
type wlLegacyTerminalPlan struct {
	ref               wlRef
	row               WalletLotsLegacySettlementRow
	charged, refunded big.Int
}

type wlWork struct {
	ctx                     context.Context
	err                     error
	steps                   uint16
	in                      WalletLotsRows
	out                     WalletLotsRowsCheck
	tables                  map[string]*wlTable
	fields                  map[wlField]map[string][]wlRef
	findings                map[*uint64]map[wlRef]bool
	missing                 map[wlKey]bool
	tainted                 map[string]bool
	sources                 map[wlKey]*wlSource
	sourceOperations        map[wlKey][]wlRef
	openings                map[string][]wlOpeningPlan
	legacyTerminal          map[string]wlLegacyTerminalPlan
	operations              map[string][]wlRef
	barriers                map[wlRef]bool
	used, claimed, deferred map[wlRef]bool
	epoch                   WalletLotsEpochRow
	epochKnown              bool
	// Expected-state types are owned by the replay implementation.
	lots         map[string]*wlLotState
	reservations map[string]*wlReservationState
	settlements  map[string]*wlSettlementState
}

func wlGet[T any](w *wlWork, table, id string) (T, wlRef, bool) {
	return wlGetKey[T](w, table, wlKey{id: id})
}
func wlGetKey[T any](w *wlWork, table string, key wlKey) (T, wlRef, bool) {
	var zero T
	t := w.tables[table]
	if t == nil || len(t.primary[key]) != 1 {
		return zero, wlRef{}, false
	}
	ref := t.primary[key][0]
	row, ok := wlAt[T](w, ref)
	return row, ref, ok
}
func wlAt[T any](w *wlWork, ref wlRef) (T, bool) {
	var zero T
	t := w.tables[ref.table]
	if t == nil || ref.index < 0 || ref.index >= t.rows.Len() {
		return zero, false
	}
	v, ok := t.rows.Index(ref.index).Interface().(T)
	return v, ok
}
func (w *wlWork) use(ref wlRef) {
	if ref.table != "" {
		w.used[ref] = true
		w.claimed[ref] = true
	}
}
func (w *wlWork) claim(ref wlRef) {
	if ref.table != "" {
		w.claimed[ref] = true
	}
}
func (w *wlWork) deferFact(ref wlRef) {
	if ref.table != "" {
		w.deferred[ref] = true
		w.claimed[ref] = true
	}
}
func (w *wlWork) mark(field *uint64, ref wlRef) {
	set := w.findings[field]
	if set == nil {
		set = map[wlRef]bool{}
		w.findings[field] = set
	}
	if !set[ref] {
		set[ref] = true
		*field++
	}
}
func (w *wlWork) need(kind, id string) {
	key := wlKey{kind, id}
	if !w.missing[key] {
		w.missing[key] = true
		w.out.Mismatches.MissingFacts++
	}
}
func (w *wlWork) taint(tenant string) {
	if !w.tainted[tenant] {
		w.tainted[tenant] = true
		w.out.Unproven.ReplayPrefixUnproven++
	}
}

// Shared implementation contracts:
// root: check(), tick(), by(table,field,value), amount(text,positive,ref),
//       integer(text,ref), unsigned(text,positive,bigint,ref), time(t,ref),
//       buildIndexes(), checkSequence(), sweep().
// source/legacy owner: buildWalletSources(), checkWalletLegacy(), bindWalletSources().
// replay owner: replayWalletLots(), finishWalletLots() plus its private expected types.
