// Package fundinglotverify checks supplied immutable financial rows using
// independent arithmetic. It does not establish database coverage or authority.
package fundinglotverify

import (
	"context"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	ledgerRowLimit  = 100000
	ledgerByteLimit = 16 << 20
)

var (
	ErrLedgerRowsInput    = errors.New("funding_lot_ledger_rows_input")
	ErrLedgerRowsResource = errors.New("funding_lot_ledger_rows_resource")
	ErrLedgerRowsCanceled = errors.New("funding_lot_ledger_rows_canceled")
)

type LedgerAccountRow struct {
	ID       string
	TenantID *string
	Kind     string
	Balance  string
}
type LedgerJournalRow struct{ ID, TenantID, OperationID, Currency string }
type LedgerLineRow struct{ ID, JournalID, AccountID, SignedDelta string }
type LedgerRows struct {
	Accounts []LedgerAccountRow
	Journals []LedgerJournalRow
	Lines    []LedgerLineRow
}
type LedgerRowCounts struct{ Accounts, Journals, Lines int64 }
type LedgerRowMismatches struct {
	AccountShapeMismatches, DuplicateAccountKinds      int64
	JournalShapeMismatches, DuplicateOperationIDs      int64
	MissingAccounts, MissingJournals, CrossTenantLines int64
	DuplicateJournalAccounts, AccountBalanceMismatches int64
	UnbalancedJournals                                 int64
}
type LedgerRowsCheck struct {
	Scope          string
	RowsConsistent bool
	Scanned        LedgerRowCounts
	Mismatches     LedgerRowMismatches
}

type ledgerWork struct {
	ctx  context.Context
	rows uint16
}

func (w *ledgerWork) tick() error {
	w.rows++
	if w.rows == 256 {
		w.rows = 0
		if w.ctx.Err() != nil {
			return ErrLedgerRowsCanceled
		}
	}
	return nil
}

// Bound the entire input before allocating indexes or big integers. The byte
// bound describes caller strings, not a promise about the process's RSS.
func ledgerBudget(w *ledgerWork, in LedgerRows) error {
	remaining := ledgerRowLimit
	for _, count := range [...]int{len(in.Accounts), len(in.Journals), len(in.Lines)} {
		if count > remaining {
			return ErrLedgerRowsResource
		}
		remaining -= count
	}
	bytesLeft := ledgerByteLimit
	consume := func(fields ...string) bool {
		for _, field := range fields {
			if len(field) > bytesLeft {
				return false
			}
			bytesLeft -= len(field)
		}
		return true
	}
	for _, row := range in.Accounts {
		if err := w.tick(); err != nil {
			return err
		}
		if !consume(row.ID, row.Kind, row.Balance) || row.TenantID != nil && !consume(*row.TenantID) {
			return ErrLedgerRowsResource
		}
	}
	for _, row := range in.Journals {
		if err := w.tick(); err != nil {
			return err
		}
		if !consume(row.ID, row.TenantID, row.OperationID, row.Currency) {
			return ErrLedgerRowsResource
		}
	}
	for _, row := range in.Lines {
		if err := w.tick(); err != nil {
			return err
		}
		if !consume(row.ID, row.JournalID, row.AccountID, row.SignedDelta) {
			return ErrLedgerRowsResource
		}
	}
	return nil
}

func ledgerText(fields ...string) bool {
	for _, field := range fields {
		if !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return false
		}
	}
	return true
}
func ledgerInteger(text string) (int64, bool) {
	if len(text) == 0 || len(text) > 20 {
		return 0, false
	}
	n, err := strconv.ParseInt(text, 10, 64)
	return n, err == nil && strconv.FormatInt(n, 10) == text
}

type accountKindKey struct {
	nullTenant bool
	tenant     string
	kind       string
}
type journalAccountKey struct{ journal, account string }

// Only the transition from one occurrence to two counts a duplicate group.
// Saturation also avoids counting the same natural key again at occurrence 3+.
func duplicateGroup[K comparable](seen map[K]uint8, key K) bool {
	switch seen[key] {
	case 0:
		seen[key] = 1
	case 1:
		seen[key] = 2
		return true
	}
	return false
}

// CheckLedgerRows evaluates all supplied rows, including platform accounts and
// dangling lines. RowsConsistent says nothing about rows omitted by a caller.
// In particular, page-local results cannot be ANDed to prove a whole ledger.
func CheckLedgerRows(ctx context.Context, in LedgerRows) (LedgerRowsCheck, error) {
	if ctx == nil {
		return LedgerRowsCheck{}, ErrLedgerRowsInput
	}
	if ctx.Err() != nil {
		return LedgerRowsCheck{}, ErrLedgerRowsCanceled
	}
	w := ledgerWork{ctx: ctx}
	if err := ledgerBudget(&w, in); err != nil {
		return LedgerRowsCheck{}, err
	}
	accounts := make(map[string]int, len(in.Accounts))
	journals := make(map[string]int, len(in.Journals))
	lineIDs := make(map[string]struct{}, len(in.Lines))
	balances := make([]int64, len(in.Accounts))
	deltas := make([]int64, len(in.Lines))
	accountKeys := make([]accountKindKey, len(in.Accounts))
	for i, row := range in.Accounts {
		if err := w.tick(); err != nil {
			return LedgerRowsCheck{}, err
		}
		value, valid := ledgerInteger(row.Balance)
		_, repeated := accounts[row.ID]
		if !valid || repeated || !ledgerText(row.ID, row.Kind, row.Balance) || row.TenantID != nil && !ledgerText(*row.TenantID) {
			return LedgerRowsCheck{}, ErrLedgerRowsInput
		}
		accounts[row.ID], balances[i] = i, value
		key := accountKindKey{nullTenant: row.TenantID == nil, kind: row.Kind}
		if row.TenantID != nil {
			key.tenant = *row.TenantID
		}
		accountKeys[i] = key
	}
	for i, row := range in.Journals {
		if err := w.tick(); err != nil {
			return LedgerRowsCheck{}, err
		}
		_, repeated := journals[row.ID]
		if repeated || !ledgerText(row.ID, row.TenantID, row.OperationID, row.Currency) {
			return LedgerRowsCheck{}, ErrLedgerRowsInput
		}
		journals[row.ID] = i
	}
	for i, row := range in.Lines {
		if err := w.tick(); err != nil {
			return LedgerRowsCheck{}, err
		}
		value, valid := ledgerInteger(row.SignedDelta)
		_, repeated := lineIDs[row.ID]
		if !valid || repeated || !ledgerText(row.ID, row.JournalID, row.AccountID, row.SignedDelta) {
			return LedgerRowsCheck{}, ErrLedgerRowsInput
		}
		lineIDs[row.ID], deltas[i] = struct{}{}, value
	}
	// No arithmetic or mismatch result is usable until every input row has
	// passed lexical validation and each table's primary keys are unambiguous.
	out := LedgerRowsCheck{Scope: "supplied_ledger_rows", Scanned: LedgerRowCounts{
		Accounts: int64(len(in.Accounts)), Journals: int64(len(in.Journals)), Lines: int64(len(in.Lines)),
	}}
	m := &out.Mismatches
	kinds := make(map[accountKindKey]uint8, len(in.Accounts))
	for i, row := range in.Accounts {
		if err := w.tick(); err != nil {
			return LedgerRowsCheck{}, err
		}
		key := accountKeys[i]
		shape := false
		switch row.Kind {
		case "AVAILABLE", "HELD":
			shape = !key.nullTenant && balances[i] >= 0
		case "FUNDING_CLEARING", "REVENUE":
			shape = key.nullTenant
		case "OPERATING_GRANT_CLEARING", "SANDBOX_FUNDING_CLEARING":
			shape = key.nullTenant && balances[i] <= 0
		}
		if !shape {
			m.AccountShapeMismatches++
		}
		if duplicateGroup(kinds, key) {
			m.DuplicateAccountKinds++
		}
	}
	operations := make(map[string]uint8, len(in.Journals))
	for _, row := range in.Journals {
		if err := w.tick(); err != nil {
			return LedgerRowsCheck{}, err
		}
		if row.Currency != "CNY" {
			m.JournalShapeMismatches++
		}
		if duplicateGroup(operations, row.OperationID) {
			m.DuplicateOperationIDs++
		}
	}
	accountSums := make([]big.Int, len(in.Accounts))
	journalSums := make([]big.Int, len(in.Journals))
	journalLines := make([]int64, len(in.Journals))
	pairs := make(map[journalAccountKey]uint8, len(in.Lines))
	var delta big.Int
	for i, row := range in.Lines {
		if err := w.tick(); err != nil {
			return LedgerRowsCheck{}, err
		}
		a, accountExists := accounts[row.AccountID]
		j, journalExists := journals[row.JournalID]
		if !accountExists {
			m.MissingAccounts++
		}
		if !journalExists {
			m.MissingJournals++
		}
		if accountExists && journalExists && !accountKeys[a].nullTenant && accountKeys[a].tenant != in.Journals[j].TenantID {
			m.CrossTenantLines++
		}
		if duplicateGroup(pairs, journalAccountKey{row.JournalID, row.AccountID}) {
			m.DuplicateJournalAccounts++
		}
		delta.SetInt64(deltas[i])
		if accountExists {
			accountSums[a].Add(&accountSums[a], &delta)
		}
		if journalExists {
			journalSums[j].Add(&journalSums[j], &delta)
			journalLines[j]++
		}
	}
	var expected big.Int
	for i := range in.Accounts {
		if err := w.tick(); err != nil {
			return LedgerRowsCheck{}, err
		}
		expected.SetInt64(balances[i])
		if accountSums[i].Cmp(&expected) != 0 {
			m.AccountBalanceMismatches++
		}
	}
	for i := range in.Journals {
		if err := w.tick(); err != nil {
			return LedgerRowsCheck{}, err
		}
		if journalLines[i] < 2 || journalSums[i].Sign() != 0 {
			m.UnbalancedJournals++
		}
	}
	if ctx.Err() != nil {
		return LedgerRowsCheck{}, ErrLedgerRowsCanceled
	}
	// Every count is bounded by the validated total of at most 100000 rows;
	// sums, unlike counts, are never accumulated in an int64.
	out.RowsConsistent = *m == (LedgerRowMismatches{})
	return out, nil
}
