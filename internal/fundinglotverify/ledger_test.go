package fundinglotverify_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	p "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func text(s string) *string { return &s }

func completeRows() p.LedgerRows {
	tenant := "tenant:A"
	in := p.LedgerRows{Accounts: []p.LedgerAccountRow{
		{ID: "available", TenantID: &tenant, Kind: "AVAILABLE", Balance: "130"},
		{ID: "held", TenantID: &tenant, Kind: "HELD", Balance: "0"},
		{ID: "manual", Kind: "FUNDING_CLEARING", Balance: "-100"},
		{ID: "voucher", Kind: "OPERATING_GRANT_CLEARING", Balance: "-50"},
		{ID: "sandbox", Kind: "SANDBOX_FUNDING_CLEARING", Balance: "-30"},
		{ID: "revenue", Kind: "REVENUE", Balance: "50"},
	}}
	add := func(id string, entries ...string) {
		in.Journals = append(in.Journals, p.LedgerJournalRow{ID: id, TenantID: tenant, OperationID: "operation:" + id, Currency: "CNY"})
		for i := 0; i < len(entries); i += 2 {
			in.Lines = append(in.Lines, p.LedgerLineRow{ID: fmt.Sprint("line:", len(in.Lines)), JournalID: id, AccountID: entries[i], SignedDelta: entries[i+1]})
		}
	}
	add("manual", "available", "100", "manual", "-100")
	add("voucher", "available", "50", "voucher", "-50")
	add("sandbox", "available", "30", "sandbox", "-30")
	add("reserve", "available", "-80", "held", "80")
	add("finish", "available", "20", "held", "-80", "revenue", "60")
	add("refund", "available", "10", "revenue", "-10")
	return in
}

func clone(in p.LedgerRows) p.LedgerRows {
	out := p.LedgerRows{Accounts: append([]p.LedgerAccountRow(nil), in.Accounts...), Journals: append([]p.LedgerJournalRow(nil), in.Journals...), Lines: append([]p.LedgerLineRow(nil), in.Lines...)}
	for i := range out.Accounts {
		if out.Accounts[i].TenantID != nil {
			out.Accounts[i].TenantID = text(*out.Accounts[i].TenantID)
		}
	}
	return out
}

func check(t *testing.T, in p.LedgerRows, want p.LedgerRowMismatches) p.LedgerRowsCheck {
	t.Helper()
	out, err := p.CheckLedgerRows(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Scope != "supplied_ledger_rows" || out.Mismatches != want || out.RowsConsistent != (want == (p.LedgerRowMismatches{})) {
		t.Fatalf("result differs: got %+v, want mismatches %+v", out, want)
	}
	counts := p.LedgerRowCounts{Accounts: int64(len(in.Accounts)), Journals: int64(len(in.Journals)), Lines: int64(len(in.Lines))}
	if out.Scanned != counts {
		t.Fatalf("incomplete scan: got %+v want %+v", out.Scanned, counts)
	}
	return out
}

func requireFailure(t *testing.T, ctx context.Context, in p.LedgerRows, want error) {
	t.Helper()
	out, err := p.CheckLedgerRows(ctx, in)
	if err != want || !errors.Is(err, want) || errors.Unwrap(err) != nil || out != (p.LedgerRowsCheck{}) {
		t.Fatalf("want fixed error and zero result, got %+v, %v", out, err)
	}
}

func TestCompleteSuppliedLedgerAndOrderIndependence(t *testing.T) {
	in := completeRows()
	before := clone(in)
	want := check(t, in, p.LedgerRowMismatches{})
	if !reflect.DeepEqual(in, before) {
		t.Fatal("input modified")
	}
	reverse := clone(in)
	for i, j := 0, len(reverse.Accounts)-1; i < j; i, j = i+1, j-1 {
		reverse.Accounts[i], reverse.Accounts[j] = reverse.Accounts[j], reverse.Accounts[i]
	}
	for i, j := 0, len(reverse.Journals)-1; i < j; i, j = i+1, j-1 {
		reverse.Journals[i], reverse.Journals[j] = reverse.Journals[j], reverse.Journals[i]
	}
	for i, j := 0, len(reverse.Lines)-1; i < j; i, j = i+1, j-1 {
		reverse.Lines[i], reverse.Lines[j] = reverse.Lines[j], reverse.Lines[i]
	}
	if got := check(t, reverse, p.LedgerRowMismatches{}); got != want {
		t.Fatal("input order changed result")
	}
	*in.Accounts[0].TenantID = "changed after return"
	in.Lines[0].SignedDelta = "999"
	if want != check(t, before, p.LedgerRowMismatches{}) {
		t.Fatal("returned result retained input")
	}
}

func TestEmptyAndZeroJournals(t *testing.T) {
	check(t, p.LedgerRows{}, p.LedgerRowMismatches{})
	check(t, p.LedgerRows{Accounts: []p.LedgerAccountRow{}, Journals: []p.LedgerJournalRow{}, Lines: []p.LedgerLineRow{}}, p.LedgerRowMismatches{})
	in := p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "a", TenantID: text("t"), Kind: "AVAILABLE", Balance: "0"}, {ID: "h", TenantID: text("t"), Kind: "HELD", Balance: "0"}}}
	check(t, in, p.LedgerRowMismatches{})
	in.Journals = []p.LedgerJournalRow{{ID: "j", TenantID: "t", OperationID: "o", Currency: "CNY"}}
	check(t, in, p.LedgerRowMismatches{UnbalancedJournals: 1})
	in.Lines = []p.LedgerLineRow{{ID: "1", JournalID: "j", AccountID: "a", SignedDelta: "0"}}
	check(t, in, p.LedgerRowMismatches{UnbalancedJournals: 1})
	in.Lines = append(in.Lines, p.LedgerLineRow{ID: "2", JournalID: "j", AccountID: "h", SignedDelta: "0"})
	check(t, in, p.LedgerRowMismatches{})
}

func TestNaturalKeysCountEachDuplicateGroupOnce(t *testing.T) {
	t.Run("account kind", func(t *testing.T) {
		in := p.LedgerRows{}
		for _, id := range []string{"a", "b", "c"} {
			in.Accounts = append(in.Accounts, p.LedgerAccountRow{ID: id, TenantID: text("tenant"), Kind: "AVAILABLE", Balance: "0"})
		}
		check(t, in, p.LedgerRowMismatches{DuplicateAccountKinds: 1})
	})
	t.Run("operation", func(t *testing.T) {
		in := p.LedgerRows{}
		for _, id := range []string{"a", "b", "c"} {
			in.Journals = append(in.Journals, p.LedgerJournalRow{ID: id, TenantID: "t", OperationID: "same", Currency: "CNY"})
		}
		check(t, in, p.LedgerRowMismatches{DuplicateOperationIDs: 1, UnbalancedJournals: 3})
	})
	t.Run("journal account", func(t *testing.T) {
		in := p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "a", TenantID: text("t"), Kind: "AVAILABLE", Balance: "0"}}, Journals: []p.LedgerJournalRow{{ID: "j", TenantID: "t", OperationID: "o", Currency: "CNY"}}}
		for _, id := range []string{"1", "2", "3"} {
			in.Lines = append(in.Lines, p.LedgerLineRow{ID: id, JournalID: "j", AccountID: "a", SignedDelta: "0"})
		}
		check(t, in, p.LedgerRowMismatches{DuplicateJournalAccounts: 1})
	})
}

func TestDanglingLinesAndCrossTenantNeverDisappear(t *testing.T) {
	t.Run("missing journal still changes account", func(t *testing.T) {
		in := p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "a", TenantID: text("t"), Kind: "AVAILABLE", Balance: "7"}}, Lines: []p.LedgerLineRow{{ID: "l", JournalID: "missing", AccountID: "a", SignedDelta: "7"}}}
		check(t, in, p.LedgerRowMismatches{MissingJournals: 1})
		in.Accounts[0].Balance = "0"
		check(t, in, p.LedgerRowMismatches{MissingJournals: 1, AccountBalanceMismatches: 1})
	})
	t.Run("missing account still balances journal", func(t *testing.T) {
		in := p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "r", Kind: "REVENUE", Balance: "-7"}}, Journals: []p.LedgerJournalRow{{ID: "j", TenantID: "t", OperationID: "o", Currency: "CNY"}}, Lines: []p.LedgerLineRow{{ID: "1", JournalID: "j", AccountID: "missing", SignedDelta: "7"}, {ID: "2", JournalID: "j", AccountID: "r", SignedDelta: "-7"}}}
		check(t, in, p.LedgerRowMismatches{MissingAccounts: 1})
	})
	t.Run("both parents missing", func(t *testing.T) {
		check(t, p.LedgerRows{Lines: []p.LedgerLineRow{{ID: "l", JournalID: "missing j", AccountID: "missing a", SignedDelta: "0"}}}, p.LedgerRowMismatches{MissingAccounts: 1, MissingJournals: 1})
	})
	t.Run("all cross tenant lines retained", func(t *testing.T) {
		in := p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "a", TenantID: text("A"), Kind: "AVAILABLE", Balance: "0"}, {ID: "h", TenantID: text("A"), Kind: "HELD", Balance: "0"}}, Journals: []p.LedgerJournalRow{{ID: "j1", TenantID: "B", OperationID: "o1", Currency: "CNY"}, {ID: "j2", TenantID: "B", OperationID: "o2", Currency: "CNY"}}, Lines: []p.LedgerLineRow{{ID: "1", JournalID: "j1", AccountID: "a", SignedDelta: "7"}, {ID: "2", JournalID: "j1", AccountID: "h", SignedDelta: "-7"}, {ID: "3", JournalID: "j2", AccountID: "a", SignedDelta: "-7"}, {ID: "4", JournalID: "j2", AccountID: "h", SignedDelta: "7"}}}
		check(t, in, p.LedgerRowMismatches{CrossTenantLines: 4})
	})
}

func TestShapesAndPlatformBalances(t *testing.T) {
	for _, kind := range []string{"AVAILABLE", "HELD", "FUNDING_CLEARING", "REVENUE", "OPERATING_GRANT_CLEARING", "SANDBOX_FUNDING_CLEARING", "unknown"} {
		for _, tenant := range []*string{nil, text("")} {
			for _, balance := range []string{"-1", "0", "1"} {
				name := fmt.Sprintf("%s/null=%t/%s", kind, tenant == nil, balance)
				t.Run(name, func(t *testing.T) {
					valid := false
					switch kind {
					case "AVAILABLE", "HELD":
						valid = tenant != nil && balance != "-1"
					case "FUNDING_CLEARING", "REVENUE":
						valid = tenant == nil
					case "OPERATING_GRANT_CLEARING", "SANDBOX_FUNDING_CLEARING":
						valid = tenant == nil && balance != "1"
					}
					want := p.LedgerRowMismatches{}
					if !valid {
						want.AccountShapeMismatches = 1
					}
					if balance != "0" {
						want.AccountBalanceMismatches = 1
					}
					check(t, p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "a", TenantID: tenant, Kind: kind, Balance: balance}}}, want)
				})
			}
		}
	}
	in := completeRows()
	in.Accounts[2].Balance = "-99"
	check(t, in, p.LedgerRowMismatches{AccountBalanceMismatches: 1})
	in = completeRows()
	in.Journals[0].Currency = "cny"
	check(t, in, p.LedgerRowMismatches{JournalShapeMismatches: 1})
}

func TestRawTextIdentityAndNullAreExact(t *testing.T) {
	in := p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "", TenantID: text(""), Kind: "AVAILABLE", Balance: "0"}, {ID: "平台/α", Kind: "FUNDING_CLEARING", Balance: "0"}}, Journals: []p.LedgerJournalRow{{ID: "", TenantID: "", OperationID: "", Currency: "CNY"}}, Lines: []p.LedgerLineRow{{ID: "零a", JournalID: "", AccountID: "", SignedDelta: "0"}, {ID: "A", JournalID: "", AccountID: "平台/α", SignedDelta: "0"}}}
	check(t, in, p.LedgerRowMismatches{})
	// NULL and empty TEXT form distinct natural keys, even when one shape is
	// invalid. Shape defects do not license silently merging their identities.
	check(t, p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "null", Kind: "AVAILABLE", Balance: "0"}, {ID: "empty", TenantID: text(""), Kind: "AVAILABLE", Balance: "0"}}}, p.LedgerRowMismatches{AccountShapeMismatches: 1})
	check(t, p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "a", TenantID: text("tenant"), Kind: "AVAILABLE", Balance: "0"}, {ID: "A", TenantID: text("Tenant"), Kind: "AVAILABLE", Balance: "0"}}}, p.LedgerRowMismatches{})
}

func TestInvalidInputHasFixedErrorsAndNoPartialResult(t *testing.T) {
	alter := []func(*p.LedgerRows, string){
		func(x *p.LedgerRows, s string) { x.Accounts[0].ID = s }, func(x *p.LedgerRows, s string) { x.Accounts[0].TenantID = text(s) },
		func(x *p.LedgerRows, s string) { x.Accounts[0].Kind = s }, func(x *p.LedgerRows, s string) { x.Accounts[0].Balance = s },
		func(x *p.LedgerRows, s string) { x.Journals[0].ID = s }, func(x *p.LedgerRows, s string) { x.Journals[0].TenantID = s },
		func(x *p.LedgerRows, s string) { x.Journals[0].OperationID = s }, func(x *p.LedgerRows, s string) { x.Journals[0].Currency = s },
		func(x *p.LedgerRows, s string) { x.Lines[0].ID = s }, func(x *p.LedgerRows, s string) { x.Lines[0].JournalID = s },
		func(x *p.LedgerRows, s string) { x.Lines[0].AccountID = s }, func(x *p.LedgerRows, s string) { x.Lines[0].SignedDelta = s },
	}
	for index, change := range alter {
		for _, invalid := range []string{"secret\x00identity", string([]byte{0xff})} {
			in := completeRows()
			change(&in, invalid)
			t.Run(fmt.Sprintf("text/%d/%x", index, invalid), func(t *testing.T) { requireFailure(t, context.Background(), in, p.ErrLedgerRowsInput) })
		}
	}
	for _, invalid := range []string{"", "+1", "-0", "00", "01", "-01", "1.0", "1e0", "1_000", " 1", "1 ", "9223372036854775808", "-9223372036854775809"} {
		for _, field := range []string{"balance", "line"} {
			in := completeRows()
			if field == "balance" {
				in.Accounts[0].Balance = invalid
			} else {
				in.Lines[0].SignedDelta = invalid
			}
			t.Run(field+"/"+invalid, func(t *testing.T) { requireFailure(t, context.Background(), in, p.ErrLedgerRowsInput) })
		}
	}
	for _, table := range []string{"account", "journal", "line"} {
		in := completeRows()
		switch table {
		case "account":
			in.Accounts = append(in.Accounts, in.Accounts[0])
		case "journal":
			in.Journals = append(in.Journals, in.Journals[0])
		case "line":
			in.Lines = append(in.Lines, in.Lines[0])
		}
		t.Run("primary/"+table, func(t *testing.T) { requireFailure(t, context.Background(), in, p.ErrLedgerRowsInput) })
	}
	requireFailure(t, nil, completeRows(), p.ErrLedgerRowsInput)
}

func TestIndependentArithmeticBeyondInt64(t *testing.T) {
	const max = "9223372036854775807"
	const min = "-9223372036854775808"
	in := p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: "a", TenantID: text("t"), Kind: "AVAILABLE", Balance: "0"}, {ID: "b", TenantID: text("t"), Kind: "HELD", Balance: "0"}, {ID: "c", Kind: "REVENUE", Balance: "0"}}}
	add := func(id string, entries ...string) {
		in.Journals = append(in.Journals, p.LedgerJournalRow{ID: id, TenantID: "t", OperationID: id, Currency: "CNY"})
		for i := 0; i < len(entries); i += 2 {
			in.Lines = append(in.Lines, p.LedgerLineRow{ID: strconv.Itoa(len(in.Lines)), JournalID: id, AccountID: entries[i], SignedDelta: entries[i+1]})
		}
	}
	// a: 2*MIN + 2*MAX + 2 = 0; b: 2*MAX - 2*MAX = 0;
	// c: 1 + 1 - 2 = 0. Several intermediate sums exceed either int64 bound.
	add("j1", "a", min, "b", max, "c", "1")
	add("j2", "a", min, "b", max, "c", "1")
	add("j3", "a", max, "b", "-"+max)
	add("j4", "a", max, "b", "-"+max)
	add("j5", "a", "2", "c", "-2")
	check(t, in, p.LedgerRowMismatches{})
	// At this prefix, wrapping would make a's sum 0 and b's sum -2,
	// incorrectly agreeing with both caches. Exact arithmetic rejects both.
	in.Journals = in.Journals[:2]
	in.Lines = in.Lines[:6]
	in.Accounts[1].TenantID, in.Accounts[1].Kind, in.Accounts[1].Balance = nil, "REVENUE", "-2"
	in.Accounts[2].Kind, in.Accounts[2].Balance = "FUNDING_CLEARING", "2"
	check(t, in, p.LedgerRowMismatches{AccountBalanceMismatches: 2})
	// The journal sum may itself exceed int64 while each individual line fits.
	check(t, p.LedgerRows{Journals: []p.LedgerJournalRow{{ID: "j", OperationID: "o", Currency: "CNY"}}, Lines: []p.LedgerLineRow{{ID: "1", JournalID: "j", AccountID: "x", SignedDelta: min}, {ID: "2", JournalID: "j", AccountID: "y", SignedDelta: min}}}, p.LedgerRowMismatches{MissingAccounts: 2, UnbalancedJournals: 1})
}

func TestWholeInputResourceBounds(t *testing.T) {
	rows := p.LedgerRows{Accounts: make([]p.LedgerAccountRow, 100000)}
	for i := range rows.Accounts {
		rows.Accounts[i] = p.LedgerAccountRow{ID: strconv.Itoa(i), TenantID: text("t"), Kind: "AVAILABLE", Balance: "0"}
	}
	check(t, rows, p.LedgerRowMismatches{DuplicateAccountKinds: 1})
	rows.Lines = []p.LedgerLineRow{{ID: "excess", SignedDelta: "0"}}
	requireFailure(t, context.Background(), rows, p.ErrLedgerRowsResource)
	const byteLimit = 16 << 20
	const kind = "FUNDING_CLEARING"
	// Exactly one account with zero cache and no lines; all bytes are counted.
	in := p.LedgerRows{Accounts: []p.LedgerAccountRow{{ID: strings.Repeat("x", byteLimit-len(kind)-len("0")), Kind: kind, Balance: "0"}}}
	check(t, in, p.LedgerRowMismatches{})
	in.Accounts[0].ID += "x"
	requireFailure(t, context.Background(), in, p.ErrLedgerRowsResource)
	multibyteLength := byteLimit - len(kind) - len("0")
	in.Accounts[0].ID = strings.Repeat("界", multibyteLength/3) + strings.Repeat("x", multibyteLength%3)
	check(t, in, p.LedgerRowMismatches{})
	in.Accounts[0].ID += "x"
	requireFailure(t, context.Background(), in, p.ErrLedgerRowsResource)
	// A nullable tenant's pointed-to bytes count at every row occurrence.
	in.Accounts[0].ID = strings.Repeat("x", byteLimit-len(kind)-len("0")-1)
	in.Accounts[0].TenantID = text("xy")
	requireFailure(t, context.Background(), in, p.ErrLedgerRowsResource)
}

type cancelCheckpoint struct {
	context.Context
	cancel context.CancelFunc
	at     int32
	calls  atomic.Int32
}

func (c *cancelCheckpoint) Err() error {
	if c.calls.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestCancellationAndStableConcurrentReaders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requireFailure(t, ctx, completeRows(), p.ErrLedgerRowsCanceled)
	expired, expire := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer expire()
	requireFailure(t, expired, completeRows(), p.ErrLedgerRowsCanceled)
	rows := p.LedgerRows{Accounts: make([]p.LedgerAccountRow, 1000)}
	for i := range rows.Accounts {
		rows.Accounts[i] = p.LedgerAccountRow{ID: strconv.Itoa(i), TenantID: text("t"), Kind: "AVAILABLE", Balance: "0"}
	}
	base, stop := context.WithCancel(context.Background())
	defer stop()
	during := &cancelCheckpoint{Context: base, cancel: stop, at: 3}
	requireFailure(t, during, rows, p.ErrLedgerRowsCanceled)
	if during.calls.Load() != 3 {
		t.Fatal("cancellation was not checked during work")
	}
	arithmeticBase, stopArithmetic := context.WithCancel(context.Background())
	defer stopArithmetic()
	arithmetic := &cancelCheckpoint{Context: arithmeticBase, cancel: stopArithmetic, at: 10}
	requireFailure(t, arithmetic, rows, p.ErrLedgerRowsCanceled)
	if arithmetic.calls.Load() != 10 {
		t.Fatal("cancellation was not checked after input validation")
	}
	last, stopLast := context.WithCancel(context.Background())
	defer stopLast()
	// This small input has no 256-row checkpoint. Its second Err call is the
	// final return boundary, which must discard an otherwise complete result.
	requireFailure(t, &cancelCheckpoint{Context: last, cancel: stopLast, at: 2}, completeRows(), p.ErrLedgerRowsCanceled)
	in := completeRows()
	want := check(t, in, p.LedgerRowMismatches{})
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 20 {
				got, err := p.CheckLedgerRows(context.Background(), in)
				if err != nil || got != want {
					t.Errorf("concurrent stable read differs: %+v %v", got, err)
					return
				}
			}
		}()
	}
	group.Wait()
}
