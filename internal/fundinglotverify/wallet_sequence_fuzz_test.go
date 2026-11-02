package fundinglotverify_test

import (
	"context"
	"reflect"
	"testing"

	p "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func FuzzWalletSequenceRowsBoundedAndPermutationInvariant(f *testing.F) {
	f.Add("1", "8", "100", "receipt-one", uint8(0))
	f.Add("9223372036854775807", "18446744073709551615", "-0.00", "", uint8(1))
	f.Add("-1", "0", "NaN", "historical identity ", uint8(2))
	f.Add("01", "1.0", "1e2", "bad\x00text", uint8(3))
	f.Add("1", "8", "100.000", string([]byte{0xff}), uint8(4))
	f.Fuzz(func(t *testing.T, seq, xid, delta, source string, variant uint8) {
		if len(seq)+len(xid)+len(delta)+len(source) > 300000 {
			t.Skip()
		}
		in := wsFixture()
		in.Operations[1].Seq = seq
		in.Operations[1].CreatedXID = xid
		in.Operations[1].AvailableDelta = delta
		in.Operations[1].SourceID = source
		switch variant % 5 {
		case 1:
			row := in.Operations[1]
			row.Seq = "2"
			in.Operations = append(in.Operations, row)
		case 2:
			row := in.Events[0]
			row.AvailableDelta = delta
			in.Events = append(in.Events, row)
		case 3:
			row := in.Journals[0]
			row.CreatedTXID = xid
			in.Journals = append(in.Journals, row)
		case 4:
			in.Tenants[0].ID = source
		}
		before := wsClone(in)
		out, err := p.CheckWalletSequenceRows(context.Background(), in)
		if !reflect.DeepEqual(before, in) {
			t.Fatal("input modified")
		}
		if err != nil {
			if err != p.ErrWalletSequenceRowsResource && err != p.ErrWalletSequenceRowsInput || out != (p.WalletSequenceRowsCheck{}) {
				t.Fatal("unexpected error or partial result")
			}
			return
		}
		if out.Scope != "supplied_wallet_sequence_rows" || out.RowsConsistent != (out.Mismatches == (p.WalletSequenceRowMismatches{})) {
			t.Fatal("inconsistent result contract")
		}
		want := p.WalletSequenceRowCounts{Epochs: uint64(len(in.Epochs)), Tenants: uint64(len(in.Tenants)), Operations: uint64(len(in.Operations)), Heads: uint64(len(in.Heads)), LegacySources: uint64(len(in.LegacySources)), Journals: uint64(len(in.Journals)), Events: uint64(len(in.Events))}
		if out.Scanned != want {
			t.Fatal("did not enumerate all supplied rows")
		}
		wsShuffle(&in, int64(variant))
		again, err := p.CheckWalletSequenceRows(context.Background(), in)
		if err != nil || again != out {
			t.Fatalf("input order changed result: first %+v, next %+v, error %v", out.Mismatches, again.Mismatches, err)
		}
	})
}
