package fundinglotverify_test

import (
	"context"
	"reflect"
	"testing"

	p "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func FuzzLedgerRowsClosedFailures(f *testing.F) {
	f.Add("account", "tenant", "0", "0")
	f.Add("", "", "-9223372036854775808", "9223372036854775807")
	f.Add("账本:α", "租户", "01", "-0")
	f.Add("nul\x00id", "tenant", "0", "0")
	f.Fuzz(func(t *testing.T, id, tenant, balance, delta string) {
		in := p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: id, TenantID: text(tenant), Kind: "AVAILABLE", Balance: balance}, {ID: "platform", Kind: "FUNDING_CLEARING", Balance: "0"}}, Journals: []p.LedgerJournalRow{{ID: "journal", TenantID: tenant, OperationID: "operation", Currency: "CNY"}}, Lines: []p.LedgerLineRow{{ID: "one", JournalID: "journal", AccountID: id, SignedDelta: delta}, {ID: "two", JournalID: "journal", AccountID: "platform", SignedDelta: "0"}}}
		before := clone(in)
		out, err := p.CheckLedgerRows(context.Background(), in)
		if !reflect.DeepEqual(in, before) {
			t.Fatal("input mutated")
		}
		if err != nil {
			if err != p.ErrLedgerRowsInput && err != p.ErrLedgerRowsResource || out != (p.LedgerRowsCheck{}) {
				t.Fatal("unexpected error or partial result")
			}
			return
		}
		if out.Scope != "supplied_ledger_rows" || out.Scanned != (p.LedgerRowCounts{Accounts: 2, Journals: 1, Lines: 2}) || out.RowsConsistent != (out.Mismatches == (p.LedgerRowMismatches{})) {
			t.Fatal("incomplete successful result")
		}
		in.Accounts[0], in.Accounts[1] = in.Accounts[1], in.Accounts[0]
		in.Lines[0], in.Lines[1] = in.Lines[1], in.Lines[0]
		again, secondError := p.CheckLedgerRows(context.Background(), in)
		if secondError != nil || again != out {
			t.Fatal("ordering changed supplied-row evaluation")
		}
	})
}
