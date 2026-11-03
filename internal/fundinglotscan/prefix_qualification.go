package fundinglotscan

import (
	"context"
	"reflect"
	"strings"
	"unicode/utf8"
)

// prefixRaw independently verifies every physical row, fixed descriptor, EOF,
// cursor and budget using this call's context. The view has no Query, previous
// context, or shared mutable rejection map.
func prefixRaw(ctx context.Context, s *session, wallet bool) (*session, error) {
	if s == nil || s.mode != ModeOrdinary && s.mode != ModeSandbox || len(s.rejected) != 0 {
		return nil, ErrInput
	}
	want, index, err := prefixRegistry(wallet)
	if err != nil {
		return nil, err
	}
	if len(s.tables) != len(want) {
		return nil, ErrInput
	}
	if !reflect.DeepEqual(s.index, index) {
		return nil, ErrExecution
	}
	budget := &session{}
	for i, table := range s.tables {
		if ctx.Err() != nil {
			return nil, contextFailure(ctx)
		}
		if !table.eof {
			return nil, ErrInput
		}
		if !reflect.DeepEqual(table.spec, want[i].spec) {
			return nil, ErrExecution
		}
		var previous []any
		for _, row := range table.rows {
			if ctx.Err() != nil {
				return nil, contextFailure(ctx)
			}
			if len(row) != len(table.spec.columns) {
				return nil, ErrExecution
			}
			for ci, c := range row {
				if c.kind != table.spec.columns[ci].kind || c.kind == timeKind && c.valid != c.instant.Valid {
					return nil, ErrExecution
				}
				if c.kind == textKind && c.valid && (!utf8.ValidString(c.text) || strings.ContainsRune(c.text, 0)) {
					return nil, ErrInput
				}
			}
			if err := budget.addTableRow(table.spec, row); err != nil {
				return nil, err
			}
			cursor, err := rowCursor(table.spec, row)
			if err != nil || previous != nil && !cursorAfter(cursor, previous, table.spec.keys) {
				return nil, ErrExecution
			}
			previous = cursor
		}
	}
	if s.rows != budget.rows || s.bytes != budget.bytes {
		return nil, ErrExecution
	}
	return &session{ctx: ctx, mode: s.mode, tables: s.tables, index: s.index, rows: budget.rows, bytes: budget.bytes, rejected: map[cellRef]bool{}}, nil
}

func prefixScanned(ctx context.Context, s *session, r PrefixReport) error {
	if err := legacySeedScanned(ctx, s); err != nil {
		return err
	}
	counts := r.PeriodicPrefix.Scanned
	for _, v := range []struct {
		name  string
		count uint64
	}{
		{"periodic_budget_observation_origins", counts.Origins},
		{"periodic_budget_observation_heads", counts.Heads},
		{"periodic_budget_observation_journal", counts.Observations},
		{"periodic_budget_operations", counts.OperationBindings},
		{"periodic_budget_windows", counts.WindowBindings},
	} {
		if ctx.Err() != nil {
			return contextFailure(ctx)
		}
		if v.count != uint64(len(s.tables[s.index[v.name]].rows)) {
			return ErrInput
		}
	}
	// TotalRows/LogicalBytes describe all pure-checker arrays, whose repeated
	// source bindings are deliberately counted separately from physical union rows.
	if counts.TotalRows > rowLimit || counts.LogicalBytes > byteLimit {
		return ErrInput
	}
	return nil
}

func qualifyPrefixSource(ctx context.Context, f PrefixFacts) (*session, error) {
	if ctx == nil {
		return nil, ErrInput
	}
	if ctx.Err() != nil {
		return nil, contextFailure(ctx)
	}
	if f.state == nil || f.state.report.Status != "consistent" || !f.state.report.CoverageComplete {
		return nil, ErrInput
	}
	original := f.state.raw
	s, err := prefixRaw(ctx, original, false)
	if err != nil {
		return nil, err
	}
	if err = s.check(); err != nil {
		return nil, prefixFailure(ctx, err)
	}
	p, state, mismatches, err := checkPrefix(s)
	if err != nil {
		return nil, prefixFailure(ctx, err)
	}
	r := makePrefixReport(s, p, state, mismatches)
	if r != f.state.report || s.report != original.report || r.Status != "consistent" || !r.CoverageComplete {
		return nil, ErrInput
	}
	if err = prefixScanned(ctx, s, r); err != nil {
		return nil, err
	}
	return s, nil
}

func qualifyPrefixWallet(ctx context.Context, f PrefixWalletFacts) (*session, error) {
	if ctx == nil {
		return nil, ErrInput
	}
	if ctx.Err() != nil {
		return nil, contextFailure(ctx)
	}
	if f.state == nil || f.state.report.Status != "consistent" || !f.state.report.CoverageComplete {
		return nil, ErrInput
	}
	original := f.state.raw
	s, err := prefixRaw(ctx, original, true)
	if err != nil {
		return nil, err
	}
	if err = s.check(); err != nil {
		return nil, prefixFailure(ctx, err)
	}
	w, wstate, err := checkPrefixWallet(s)
	if err != nil {
		return nil, prefixFailure(ctx, err)
	}
	p, state, mismatches, err := checkPrefix(s)
	if err != nil {
		return nil, prefixFailure(ctx, err)
	}
	r := makePrefixWalletReport(s, p, state, mismatches, w, wstate)
	if r != f.state.report || s.report != original.report || r.Status != "consistent" || !r.CoverageComplete {
		return nil, ErrInput
	}
	if err = prefixScanned(ctx, s, makePrefixReport(s, p, state, mismatches)); err != nil {
		return nil, err
	}
	if !legacySeedCounts(s, r.Wallet.Scanned, legacySeedBindingTables(walletBindings())) {
		return nil, ErrInput
	}
	return s, nil
}
