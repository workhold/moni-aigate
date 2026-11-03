package fundinglotscan

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func prefixRegistry(wallet bool) ([]tableData, map[string]int, error) {
	groups := [][]binding{fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings()}
	tablesWant, columnsWant := prefixTableCount, prefixColumnCount
	if wallet {
		groups = append(groups, walletBindings())
		tablesWant, columnsWant = prefixWalletTableCount, prefixWalletColumnCount
	}
	groups = append(groups, prefixBindings())
	tables, index, err := registry(groups...)
	columns := 0
	for _, t := range tables {
		columns += len(t.spec.columns)
	}
	if err != nil || len(tables) != tablesWant || columns != columnsWant {
		return nil, nil, ErrExecution
	}
	return tables, index, nil
}

// ReadPrefix reads the fixed 71-table union once, using a single shared
// physical budget for old, added, and generated columns. Query carries no authority.
func ReadPrefix(ctx context.Context, q Query, schema string, mode Mode) (PrefixFacts, error) {
	s, err := readPrefixRaw(ctx, q, schema, mode, false)
	if err != nil {
		return PrefixFacts{}, err
	}
	p, state, mismatches, err := checkPrefix(s)
	if err != nil {
		return PrefixFacts{}, prefixFailure(ctx, err)
	}
	r := makePrefixReport(s, p, state, mismatches)
	if ctx.Err() != nil {
		return PrefixFacts{}, contextFailure(ctx)
	}
	return PrefixFacts{state: &prefixFactsState{raw: s, report: r}}, nil
}

// ReadWalletPrefix additionally evaluates the original wallet check from the
// same raw session. Source-only facts cannot substitute for this physical read.
func ReadWalletPrefix(ctx context.Context, q Query, schema string, mode Mode) (PrefixWalletFacts, error) {
	s, err := readPrefixRaw(ctx, q, schema, mode, true)
	if err != nil {
		return PrefixWalletFacts{}, err
	}
	wallet, walletState, err := checkPrefixWallet(s)
	if err != nil {
		return PrefixWalletFacts{}, prefixFailure(ctx, err)
	}
	p, state, mismatches, err := checkPrefix(s)
	if err != nil {
		return PrefixWalletFacts{}, prefixFailure(ctx, err)
	}
	r := makePrefixWalletReport(s, p, state, mismatches, wallet, walletState)
	if ctx.Err() != nil {
		return PrefixWalletFacts{}, contextFailure(ctx)
	}
	return PrefixWalletFacts{state: &prefixWalletFactsState{raw: s, report: r}}, nil
}

func readPrefixRaw(ctx context.Context, q Query, schema string, mode Mode, wallet bool) (*session, error) {
	if ctx == nil || nilValue(q) || schema == "" || len(schema) > 63 || !utf8.ValidString(schema) || strings.ContainsRune(schema, 0) || mode != ModeOrdinary && mode != ModeSandbox {
		return nil, ErrInput
	}
	if ctx.Err() != nil {
		return nil, contextFailure(ctx)
	}
	tables, index, err := prefixRegistry(wallet)
	if err != nil {
		return nil, err
	}
	s := &session{ctx: ctx, query: q, schema: schema, mode: mode, tables: tables, index: index, rejected: map[cellRef]bool{}}
	if err = s.readAll(); err != nil {
		return nil, prefixFailure(ctx, err)
	}
	if err = s.check(); err != nil {
		return nil, prefixFailure(ctx, err)
	}
	// This is only the original seven-core intermediate result. Do not assign
	// the new union counts to any old public Report scope.
	return s, nil
}

func checkPrefixWallet(s *session) (fundinglotverify.WalletLotsRowsCheck, string, error) {
	in, qualified, err := projectWallet(s)
	if err != nil {
		return fundinglotverify.WalletLotsRowsCheck{}, "", err
	}
	if !qualified {
		return fundinglotverify.WalletLotsRowsCheck{}, "input_mismatch", nil
	}
	r, err := fundinglotverify.CheckWalletLotsRows(s.ctx, in)
	if err == fundinglotverify.ErrWalletLotsRowsResource {
		return fundinglotverify.WalletLotsRowsCheck{}, "", ErrResource
	}
	if errors.Is(err, fundinglotverify.ErrWalletLotsRowsTimeout) {
		return fundinglotverify.WalletLotsRowsCheck{}, "", ErrTimeout
	}
	if errors.Is(err, fundinglotverify.ErrWalletLotsRowsCanceled) {
		return fundinglotverify.WalletLotsRowsCheck{}, "", ErrCanceled
	}
	if err != nil {
		return fundinglotverify.WalletLotsRowsCheck{}, "", contextFailure(s.ctx)
	}
	if r.Scope != "supplied_wallet_lots_rows" {
		return fundinglotverify.WalletLotsRowsCheck{}, "", ErrExecution
	}
	return r, "executed", nil
}

func prefixFailure(ctx context.Context, err error) error {
	// Independent pure-checker deadline checks can observe the deadline before
	// a context's cancellation goroutine has published ctx.Err().
	if err == ErrTimeout || err == ErrCanceled {
		return err
	}
	return readFailure(ctx, err)
}
