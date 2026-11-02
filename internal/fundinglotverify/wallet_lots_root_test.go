package fundinglotverify_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	p "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func TestWalletLotsRowsExistingParentsDoNotAuthorizeExtraFacts(t *testing.T) {
	tests := map[string]func(*testing.T, *p.WalletLotsRows){
		"extra_event_for_existing_operation_and_lot": func(_ *testing.T, in *p.WalletLotsRows) {
			r := in.Events[0]
			r.ID = wlID(98001)
			r.LotID = in.Lots[1].ID
			in.Events = append(in.Events, r)
		},
		"extra_refund_edge_to_existing_zero_charge_terminal": func(_ *testing.T, in *p.WalletLotsRows) {
			r := in.RefundLots[0]
			r.ID = wlID(98002)
			r.LotID = in.Lots[2].ID
			r.SettlementLotID = wsPtr(in.SettlementLots[2].ID)
			r.AllocationIndex, r.Amount = "2", "1"
			in.RefundLots = append(in.RefundLots, r)
		},
		"extra_reserve_edge_to_existing_future_origin": func(t *testing.T, in *p.WalletLotsRows) {
			// This fourth receipt is real and belongs to a later wallet sequence.
			// Its physical lot cannot authorize an extra edge in the earlier R120.
			b := &wlBuilder{in: *in, tenant: "tenant", last: map[string]string{}}
			lot := b.funding(3, "manual_credit", "future-credit", "future-credit", "10", "FUNDING_CLEARING", "nonrefundable")
			b.cache(lot, "10", "0", "0", "1")
			b.balance("AVAILABLE", "135")
			b.balance("FUNDING_CLEARING", "-50")
			wlValid(t, b.in)
			r := b.in.ReservationLots[0]
			r.ID, r.LotID = wlID(98003), b.in.Lots[lot].ID
			r.AllocationIndex, r.Amount = "4", "1"
			b.in.ReservationLots = append(b.in.ReservationLots, r)
			*in = b.in
		},
	}
	for name, damage := range tests {
		t.Run(name, func(t *testing.T) {
			in := wlThree()
			damage(t, &in)
			want := wlRun(t, in)
			if want.Mismatches.OrphanFacts == 0 || want.Unproven.ReplayPrefixUnproven != 0 {
				t.Fatalf("physical parents concealed a fact outside the independently expected set: %+v", want)
			}
			for seed := int64(0); seed < 10; seed++ {
				wlShuffle(&in, seed)
				if got := wlRun(t, in); got != want {
					t.Fatalf("extra fact classification changed with physical order: %+v", got)
				}
			}
		})
	}
}

func TestWalletLotsRowsDeferredSuffixStillQualifiesLocalStatements(t *testing.T) {
	base := wlThree()
	base.Receipts[0].Amount = "unreadable"
	baseline := wlRun(t, base)
	for name, damage := range map[string]func(*p.WalletLotsRows){
		"unknown_refund_origin":           func(in *p.WalletLotsRows) { in.RefundLots[0].Origin = "unknown" },
		"zero_allocation_index":           func(in *p.WalletLotsRows) { in.RefundLots[0].AllocationIndex = "0" },
		"impossible_local_event_equation": func(in *p.WalletLotsRows) { in.Events[11].AfterAvailable = "16" },
		"unknown_lot_refund_class":        func(in *p.WalletLotsRows) { in.Lots[2].RefundClass = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			in := wlClone(base)
			damage(&in)
			got := wlRun(t, in)
			if got.Unproven.ReplayPrefixUnproven != 1 || got.Mismatches.InvalidValueRows <= baseline.Mismatches.InvalidValueRows || got.Mismatches.OrphanFacts != 0 {
				t.Fatalf("deferred suffix hid a locally decidable invalid statement: baseline=%+v got=%+v", baseline, got)
			}
		})
	}
}

func TestWalletLotsRowsFalseDeclarationsPreserveIndependentFinalAmounts(t *testing.T) {
	for name, damage := range map[string]func(*p.WalletLotsRows){
		"head_cache":                         func(in *p.WalletLotsRows) { in.Heads[0].TailSeq = "6" },
		"declared_operation_delta":           func(in *p.WalletLotsRows) { in.Operations[1].AvailableDelta = "41" },
		"declared_event_count":               func(in *p.WalletLotsRows) { in.Operations[1].EventCount = "0" },
		"malformed_declared_operation_delta": func(in *p.WalletLotsRows) { in.Operations[1].AvailableDelta = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			in := wlThree()
			damage(&in)
			baseline := wlRun(t, in)
			if baseline.RowsConsistent || baseline.Unproven.ReplayPrefixUnproven != 0 {
				t.Fatalf("a false declaration erased otherwise known source amounts: %+v", baseline)
			}
			// Preserve the declared P=A+H+S invariant, so only the true independent
			// replay, rather than this local cache invariant, exposes the forgery.
			in.Balances[0].AvailableAmount, in.Balances[0].ConsumedAmount = "16", "24"
			got := wlRun(t, in)
			if got.Unproven.ReplayPrefixUnproven != 0 || got.Mismatches.BalanceRows <= baseline.Mismatches.BalanceRows {
				t.Fatalf("bad head/delta/count prevented the expected final-bucket comparison: baseline=%+v got=%+v", baseline, got)
			}
		})
	}
}

func TestWalletLotsRowsConflictingNaturalOriginDoesNotChooseFirst(t *testing.T) {
	in := wlThree()
	r := in.Lots[0]
	r.ID, r.IssuedAmount = wlID(98004), "999"
	in.Lots = append(in.Lots, r)
	want := wlRun(t, in)
	if want.Mismatches.DuplicateFacts == 0 || want.Mismatches.OriginRows == 0 || want.Unproven.ReplayPrefixUnproven != 1 || want.Mismatches.OrphanFacts != 0 {
		t.Fatalf("ambiguous origin was selected or fabricated as orphan: %+v", want)
	}
	for seed := int64(0); seed < 30; seed++ {
		wlShuffle(&in, seed)
		if got := wlRun(t, in); got != want {
			t.Fatalf("natural-origin duplicate depends on first physical row: %+v", got)
		}
	}
}

func TestWalletLotsRowsZeroLedgerClaimsStillNeedPhysicalParents(t *testing.T) {
	for name, damage := range map[string]func(*p.WalletLotsRows){
		"zero_wallet_line_without_journal_header": func(in *p.WalletLotsRows) {
			in.Lines = append(in.Lines, p.LedgerLineRow{ID: "tenant/orphan-zero-line", JournalID: "tenant/missing-journal", AccountID: "tenant/AVAILABLE", SignedDelta: "0"})
		},
		"zero_wallet_account_without_tenant": func(in *p.WalletLotsRows) {
			in.Accounts = append(in.Accounts, p.LedgerAccountRow{ID: "missing/AVAILABLE", TenantID: wsPtr("missing"), Kind: "AVAILABLE", Balance: "0"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := wlThree()
			damage(&in)
			want := wlRun(t, in)
			if want.Mismatches.WalletSourceRows == 0 || want.Mismatches.OrphanFacts == 0 || want.Unproven.ReplayPrefixUnproven != 0 {
				t.Fatalf("zero amount concealed a physically missing wallet parent: %+v", want)
			}
			for seed := int64(0); seed < 10; seed++ {
				wlShuffle(&in, seed)
				if got := wlRun(t, in); got != want {
					t.Fatalf("zero orphan depends on physical order: %+v", got)
				}
			}
		})
	}
}

func TestWalletLotsRowsConflictingGenesisKindsAreQualifiedPerRow(t *testing.T) {
	in := wlThree()
	r := in.Operations[0]
	r.ID, r.Kind = wlID(98005), "epoch_opening"
	in.Operations = append(in.Operations, r)
	want := wlRun(t, in)
	if want.Mismatches.GenesisRows == 0 || want.Mismatches.DuplicateFacts == 0 || want.Unproven.ReplayPrefixUnproven != 1 || want.Mismatches.OrphanFacts != 0 {
		t.Fatalf("conflicting seq0 rows were selected or hidden: %+v", want)
	}
	for seed := int64(0); seed < 30; seed++ {
		wlShuffle(&in, seed)
		if got := wlRun(t, in); got != want {
			t.Fatalf("mixed legal/illegal genesis depends on first seq0 row: %+v", got)
		}
	}
}

func TestWalletLotsRowsInvalidFutureSequenceKeepsEarlierPrefixChecks(t *testing.T) {
	in := wlThree()
	// A later broken predecessor cannot make an earlier uniquely ordered
	// funding disappear. Its supplied amount is still independently 40.
	in.Operations[7].PreviousSeq = wsPtr("5")
	in.Events[0].AfterAvailable = "41"
	got := wlRun(t, in)
	if got.Unproven.ReplayPrefixUnproven != 1 || got.Mismatches.EventChainRows == 0 {
		t.Fatalf("later sequence damage skipped the earlier known replay prefix: %+v", got)
	}
}

func TestWalletLotsRowsKnownEarlyExtraSurvivesLaterBarrier(t *testing.T) {
	in := wlThree()
	extra := in.Events[0]
	extra.ID = wlID(99003)
	extra.LotID = in.Lots[1].ID
	in.Events = append(in.Events, extra)
	in.Receipts[2].Amount = "invalid-future-amount"
	want := wlRun(t, in)
	if want.Mismatches.OrphanFacts == 0 || want.Mismatches.EventChainRows == 0 || want.Unproven.ReplayPrefixUnproven != 1 {
		t.Fatalf("known early extra was downgraded by a later barrier: %+v", want)
	}
	for seed := int64(0); seed < 10; seed++ {
		wlShuffle(&in, seed)
		if got := wlRun(t, in); got != want {
			t.Fatal("known extra depends on input order")
		}
	}
}

func TestWalletLotsRowsDeferredEdgesKeepTheirBusinessRoots(t *testing.T) {
	in := wlThree()
	in.Receipts[2].Amount = "invalid-prefix"
	// The edge points at a real terminal from the same tenant, but that
	// terminal charged a different lot. This is decidable without amounts.
	in.RefundLots[0].SettlementLotID = wsPtr(in.SettlementLots[0].ID)
	out := wlRun(t, in)
	if out.Mismatches.RefundRows == 0 || out.Mismatches.OrphanFacts == 0 {
		t.Fatalf("cross-lot refund root hidden by deferred replay: %+v", out)
	}
	for seed := int64(0); seed < 10; seed++ {
		wlShuffle(&in, seed)
		if got := wlRun(t, in); got != out {
			t.Fatal("root check depends on order")
		}
	}
}

func TestWalletLotsRowsLateInterruptDiscardsAllFindings(t *testing.T) {
	in := wlTwoTenants()
	in.Events[0].AfterAvailable = "41"
	// Measure only context checks, not expected financial output. Interrupting
	// late in the same deterministic input exercises disposal after counters
	// and independent replay state have already been created.
	probe := &wlInterruptContext{Context: context.Background(), after: 1<<31 - 1, cause: context.Canceled}
	if _, err := p.CheckWalletLotsRows(probe, in); err != nil {
		t.Fatal(err)
	}
	checks := probe.calls.Load()
	if checks < 12 {
		t.Fatalf("fixture is too small to exercise a late interrupt: checks=%d", checks)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		want := p.ErrWalletLotsRowsCanceled
		if cause == context.DeadlineExceeded {
			want = p.ErrWalletLotsRowsTimeout
		}
		for _, after := range []int32{checks / 2, checks - 2} {
			t.Run(fmt.Sprintf("%v_at_%d", cause, after), func(t *testing.T) {
				ctx := &wlInterruptContext{Context: context.Background(), after: after, cause: cause}
				before := wlClone(in)
				out, err := p.CheckWalletLotsRows(ctx, in)
				if err != want || out != (p.WalletLotsRowsCheck{}) || !reflect.DeepEqual(in, before) {
					t.Fatalf("interrupt exposed partial findings or changed input: %+v %v checks=%d", out, err, ctx.calls.Load())
				}
			})
		}
	}
}
