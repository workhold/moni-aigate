package fundinglotverify_test

import (
	"context"
	"reflect"
	"testing"

	p "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func FuzzWalletLotsRowsBoundedAndPermutationInvariant(f *testing.F) {
	f.Add("40", "201", "40", uint8(0))
	f.Add("not-amount", "0", "NaN", uint8(1))
	f.Add("9223372036854775807", "18446744073709551615", "-0.00", uint8(2))
	f.Add("01", "201", "1e4", uint8(3))
	f.Add("40", "201", string([]byte{0xff}), uint8(4))
	f.Fuzz(func(t *testing.T, amount, xid, delta string, variant uint8) {
		if len(amount)+len(xid)+len(delta) > 300000 {
			t.Skip()
		}
		in := wlThree()
		in.Receipts[0].Amount = amount
		in.Operations[1].CreatedXID = xid
		in.Events[0].AvailableDelta = delta
		switch variant % 6 {
		case 1:
			r := in.Lots[0]
			r.IssuedAmount = amount
			in.Lots = append(in.Lots, r)
		case 2:
			r := in.Events[0]
			r.CreatedXID = xid
			in.Events = append(in.Events, r)
		case 3:
			in.RefundLots[0].LotID = in.Lots[0].ID
		case 4:
			in.Receipts[0].CreatedAt = p.WalletLotsTime{Valid: true, Infinity: 1}
		case 5:
			in.LegacySources = append(in.LegacySources, p.WalletLotsLegacySourceRow{WalletSequenceLegacySourceRow: p.WalletSequenceLegacySourceRow{TenantID: in.Tenants[0].ID, Kind: "manual_credit", SourceID: in.Receipts[0].ID, Amount: amount}})
		}
		before := wlClone(in)
		out, err := p.CheckWalletLotsRows(context.Background(), in)
		if !reflect.DeepEqual(before, in) {
			t.Fatal("input changed")
		}
		if err != nil {
			if err != p.ErrWalletLotsRowsResource || out != (p.WalletLotsRowsCheck{}) {
				t.Fatalf("unexpected error/partial output: %+v %v", out, err)
			}
			return
		}
		if out.Scope != "supplied_wallet_lots_rows" {
			t.Fatal("wrong scope")
		}
		mismatch := out.Mismatches != (p.WalletLotsRowMismatches{}) || out.Sequence != (p.WalletSequenceRowMismatches{})
		consistent := !mismatch && out.Unproven == (p.WalletLotsRowUnproven{})
		status := "consistent"
		if mismatch {
			status = "mismatch"
		} else if !consistent {
			status = "indeterminate"
		}
		if out.RowsConsistent != consistent || out.Status != status {
			t.Fatal("result semantics disagree")
		}
		raw, count := reflect.ValueOf(in), reflect.ValueOf(out.Scanned)
		for n := 0; n < raw.NumField(); n++ {
			if count.Field(n).Uint() != uint64(raw.Field(n).Len()) {
				t.Fatal("not every supplied row was counted")
			}
		}
		wlShuffle(&in, int64(variant))
		again, err := p.CheckWalletLotsRows(context.Background(), in)
		if err != nil || again != out {
			t.Fatalf("permutation changed result:\n%+v\n%+v\n%v", out, again, err)
		}
	})
}
