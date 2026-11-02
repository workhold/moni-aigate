package fundinglotverify_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	p "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func wsID(n int) string      { return fmt.Sprintf("10000000-0000-4000-8000-%012d", n) }
func wsPtr(v string) *string { return &v }
func wsClone(in p.WalletSequenceRows) p.WalletSequenceRows {
	return wsCloneValue(reflect.ValueOf(in)).Interface().(p.WalletSequenceRows)
}
func wsCloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(wsCloneValue(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for n := 0; n < v.Len(); n++ {
			out.Index(n).Set(wsCloneValue(v.Index(n)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		for n := 0; n < v.NumField(); n++ {
			out.Field(n).Set(wsCloneValue(v.Field(n)))
		}
		return out
	default:
		return v
	}
}
func wsBase(tenant string, born bool) p.WalletSequenceRows {
	kind := "epoch_opening"
	var birth *string
	if born {
		kind = "tenant_genesis"
		birth = wsPtr("7")
	}
	return p.WalletSequenceRows{
		Epochs:     []p.WalletSequenceEpochRow{{ID: wsID(1)}},
		Tenants:    []p.WalletSequenceTenantRow{{ID: tenant, CreatedXID: birth}},
		Operations: []p.WalletSequenceOperationRow{{ID: wsID(10), TenantID: tenant, EpochID: wsID(1), Seq: "0", Kind: kind, SourceID: tenant, EventCount: "0", AvailableDelta: "0", HeldDelta: "0", ConsumedDelta: "0", IssuedDelta: "0", CreatedXID: "7"}},
		Heads:      []p.WalletSequenceHeadRow{{TenantID: tenant, EpochID: wsID(1), TailSeq: "0", TailOperationID: wsID(10)}},
	}
}

type wsDelta struct {
	lot        int
	a, h, s, i string
}

// Aggregate declarations and individual effects are separate hand-written
// inputs. No production allocator or checker computes an expected value.
func wsAppend(in *p.WalletSequenceRows, kind, source, original, xid, a, h, s, i string, effects ...wsDelta) {
	n := len(in.Operations)
	tenant := in.Tenants[0].ID
	op := p.WalletSequenceOperationRow{ID: wsID(10 + n), TenantID: tenant, EpochID: wsID(1), Seq: fmt.Sprint(n), PreviousSeq: wsPtr(fmt.Sprint(n - 1)), PreviousID: wsPtr(in.Operations[n-1].ID), Kind: kind, SourceID: source, JournalID: wsPtr("journal-" + fmt.Sprint(n)), JournalOperationID: wsPtr(original), EventCount: fmt.Sprint(len(effects)), AvailableDelta: a, HeldDelta: h, ConsumedDelta: s, IssuedDelta: i, CreatedXID: xid}
	journalKind := map[string]string{"manual_credit": "credit", "voucher_grant": "voucher_grant", "sandbox_capture": "sandbox_capture", "reserve": "reserve", "finish": "settled", "consumption_refund": "refund", "legacy_consumption_refund": "refund"}[kind]
	in.Operations = append(in.Operations, op)
	in.Heads[0].TailSeq, in.Heads[0].TailOperationID = op.Seq, op.ID
	in.Journals = append(in.Journals, p.WalletSequenceJournalRow{ID: *op.JournalID, TenantID: tenant, OperationID: original, Kind: journalKind, Currency: "CNY", CreatedTXID: xid})
	eventKind := kind
	if kind == "manual_credit" || kind == "voucher_grant" || kind == "sandbox_capture" {
		eventKind = "funding"
	}
	for _, e := range effects {
		in.Events = append(in.Events, p.WalletSequenceEventRow{ID: wsID(100 + len(in.Events)), TenantID: tenant, LotID: wsID(1000 + e.lot), WalletOperationID: op.ID, WalletSeq: op.Seq, Kind: eventKind, OperationID: wsPtr(original), JournalID: wsPtr(*op.JournalID), CreatedXID: xid, AvailableDelta: e.a, HeldDelta: e.h, ConsumedDelta: e.s, IssuedDelta: e.i})
	}
}
func wsFixture() p.WalletSequenceRows {
	in := wsBase("tenant", false)
	// Two grants total 120. Reserve 110, charge 80/release 30, refund 25:
	// aggregate available=65, held=0, consumed=55, issued=120.
	wsAppend(&in, "manual_credit", "receipt-one", "credit-order-one", "8", "100", "0", "0", "100", wsDelta{1, "100", "0", "0", "100"})
	wsAppend(&in, "voucher_grant", "receipt-two", "voucher-redemption-two", "8", "20", "0", "0", "20", wsDelta{2, "20", "0", "0", "20"})
	wsAppend(&in, "reserve", "reservation", "reservation", "9", "-110", "110", "0", "0", wsDelta{1, "-100", "100", "0", "0"}, wsDelta{2, "-10", "10", "0", "0"})
	wsAppend(&in, "finish", "settlement", "settlement", "10", "30", "-110", "80", "0", wsDelta{1, "20", "-100", "80", "0"}, wsDelta{2, "10", "-10", "0", "0"})
	wsAppend(&in, "consumption_refund", "refund", "refund", "11", "25", "0", "-25", "0", wsDelta{1, "25", "0", "-25", "0"})
	return in
}
func wsCheck(t *testing.T, in p.WalletSequenceRows) p.WalletSequenceRowsCheck {
	t.Helper()
	before := wsClone(in)
	got, err := p.CheckWalletSequenceRows(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, in) {
		t.Fatal("caller input mutated")
	}
	if got.Scope != "supplied_wallet_sequence_rows" {
		t.Fatalf("scope=%q", got.Scope)
	}
	want := p.WalletSequenceRowCounts{Epochs: uint64(len(in.Epochs)), Tenants: uint64(len(in.Tenants)), Operations: uint64(len(in.Operations)), Heads: uint64(len(in.Heads)), LegacySources: uint64(len(in.LegacySources)), Journals: uint64(len(in.Journals)), Events: uint64(len(in.Events))}
	if got.Scanned != want || got.RowsConsistent != (got.Mismatches == (p.WalletSequenceRowMismatches{})) {
		t.Fatalf("incomplete successful result: %+v", got)
	}
	return got
}
func wsValid(t *testing.T, in p.WalletSequenceRows) {
	t.Helper()
	if got := wsCheck(t, in); !got.RowsConsistent {
		t.Fatalf("unexpected mismatch: %+v", got.Mismatches)
	}
}
func wsField(t *testing.T, m p.WalletSequenceRowMismatches, name string) uint64 {
	t.Helper()
	f := reflect.ValueOf(m).FieldByName(name)
	if !f.IsValid() {
		t.Fatalf("unknown field %s", name)
	}
	return f.Uint()
}
func wsShuffle(in *p.WalletSequenceRows, seed int64) {
	r := rand.New(rand.NewSource(seed))
	for _, group := range []any{in.Epochs, in.Tenants, in.Operations, in.Heads, in.LegacySources, in.Journals, in.Events} {
		v := reflect.ValueOf(group)
		r.Shuffle(v.Len(), reflect.Swapper(group))
	}
}
func wsLegacy(in *p.WalletSequenceRows, kind, source, journal, original, amount string) {
	journalKind := map[string]string{"manual_credit": "credit", "voucher_grant": "voucher_grant", "sandbox_capture": "sandbox_capture", "reserve": "reserve", "finish": "released", "consumption_refund": "refund"}[kind]
	in.LegacySources = append(in.LegacySources, p.WalletSequenceLegacySourceRow{EpochID: wsID(1), TenantID: in.Tenants[0].ID, Kind: kind, SourceID: source, JournalID: journal, JournalOperationID: original, Amount: amount})
	in.Journals = append(in.Journals, p.WalletSequenceJournalRow{ID: journal, TenantID: in.Tenants[0].ID, OperationID: original, Kind: journalKind, Currency: "CNY", CreatedTXID: "6"})
}

func TestWalletSequenceRowsHandCalculatedValid(t *testing.T) {
	t.Run("empty_database_has_epoch", func(t *testing.T) {
		wsValid(t, p.WalletSequenceRows{Epochs: []p.WalletSequenceEpochRow{{ID: wsID(1)}}})
	})
	t.Run("old_zero_wallet", func(t *testing.T) { wsValid(t, wsBase("tenant", false)) })
	t.Run("new_genesis", func(t *testing.T) { wsValid(t, wsBase("tenant", true)) })
	t.Run("fund_reserve_partial_finish_refund_same_top_xid", func(t *testing.T) { wsValid(t, wsFixture()) })
	t.Run("two_other_funding_kinds_and_legacy_refund", func(t *testing.T) {
		in := wsBase("tenant", false)
		wsAppend(&in, "sandbox_capture", "receipt", "capture-not-receipt", "8", "6", "0", "0", "6", wsDelta{1, "6", "0", "0", "6"})
		wsAppend(&in, "legacy_consumption_refund", "refund", "refund", "9", "4", "0", "0", "4", wsDelta{2, "4", "0", "0", "4"})
		wsValid(t, in)
	})
	t.Run("zero_reserve_and_finish_still_advance", func(t *testing.T) {
		in := wsBase("tenant", false)
		wsAppend(&in, "reserve", "reserve", "reserve", "8", "0", "0", "0", "0")
		wsAppend(&in, "finish", "finish", "finish", "8", "0", "0", "0", "0")
		in.Journals[1].Kind = "released"
		wsValid(t, in)
	})
	t.Run("legacy_all_six_kinds", func(t *testing.T) {
		in := wsBase("tenant", false)
		for n, k := range []string{"manual_credit", "voucher_grant", "sandbox_capture", "reserve", "finish", "consumption_refund"} {
			amount := "1"
			if k == "reserve" || k == "finish" {
				amount = "0"
			}
			source, original := fmt.Sprintf("old-%d", n), fmt.Sprintf("old-op-%d", n)
			if k == "reserve" || k == "finish" || k == "consumption_refund" {
				original = source
			}
			wsLegacy(&in, k, source, fmt.Sprintf("old-journal-%d", n), original, amount)
		}
		wsValid(t, in)
	})
	t.Run("scale_zero_numeric_and_negative_zero", func(t *testing.T) {
		in := wsFixture()
		in.Operations[1].AvailableDelta = "100.00"
		in.Operations[1].HeldDelta = "-0.000"
		in.Events[0].IssuedDelta = "100.0000"
		wsValid(t, in)
	})
	t.Run("delta_sum_exceeds_bigint", func(t *testing.T) {
		in := wsBase("tenant", false)
		in.Operations[0].AvailableDelta = "9223372036854775807"
		in.Operations[0].HeldDelta = "9223372036854775807"
		in.Operations[0].IssuedDelta = "18446744073709551614"
		in.Operations[0].EventCount = "2"
		for n, e := range []wsDelta{{1, "9223372036854775807", "0", "0", "9223372036854775807"}, {2, "0", "9223372036854775807", "0", "9223372036854775807"}} {
			in.Events = append(in.Events, p.WalletSequenceEventRow{ID: wsID(100 + n), TenantID: "tenant", LotID: wsID(1000 + e.lot), WalletOperationID: wsID(10), WalletSeq: "0", Kind: "legacy_opening", CreatedXID: "7", AvailableDelta: e.a, HeldDelta: e.h, ConsumedDelta: e.s, IssuedDelta: e.i})
		}
		wsValid(t, in)
	})
	t.Run("positive_uint64_birth_boundary", func(t *testing.T) {
		in := wsBase("tenant", true)
		in.Tenants[0].CreatedXID = wsPtr("18446744073709551615")
		in.Operations[0].CreatedXID = "18446744073709551615"
		wsValid(t, in)
	})
	t.Run("unreferenced_non_wallet_journal", func(t *testing.T) {
		in := wsBase("tenant", false)
		in.Journals = append(in.Journals, p.WalletSequenceJournalRow{ID: "procurement", TenantID: "", OperationID: "external-operation", Kind: "provider_cost", Currency: "CNY", CreatedTXID: "1"})
		wsValid(t, in)
	})
	t.Run("unordered_inputs", func(t *testing.T) {
		in := wsFixture()
		want := wsCheck(t, in)
		for n := int64(0); n < 25; n++ {
			wsShuffle(&in, n)
			if got := wsCheck(t, in); got != want {
				t.Fatalf("permutation changed result: %+v", got)
			}
		}
	})
}

func TestWalletSequenceRowsExactHistoricalText(t *testing.T) {
	for _, tenant := range []string{"", " ", " 租户\t历史 "} {
		t.Run(fmt.Sprintf("tenant_%q", tenant), func(t *testing.T) {
			in := wsBase(tenant, false)
			wsAppend(&in, "manual_credit", "", "", "8", "1", "0", "0", "1", wsDelta{1, "1", "0", "0", "1"})
			in.Operations[1].JournalID = wsPtr("")
			in.Events[0].JournalID = wsPtr("")
			in.Journals[0].ID = ""
			wsValid(t, in)
		})
	}
	t.Run("whitespace_not_trimmed", func(t *testing.T) {
		in := wsFixture()
		in.Journals[0].ID = " " + in.Journals[0].ID
		if got := wsCheck(t, in); got.Mismatches.JournalBindingRows == 0 {
			t.Fatal("journal ID was silently trimmed")
		}
	})
	t.Run("null_not_empty", func(t *testing.T) {
		in := wsBase("", false)
		in.Operations[0].JournalID = wsPtr("")
		if got := wsCheck(t, in); got.RowsConsistent {
			t.Fatal("NULL journal confused with empty historical identity")
		}
	})
}

func TestWalletSequenceRowsAllMismatchDirections(t *testing.T) {
	cases := []struct {
		name, field string
		change      func(*p.WalletSequenceRows)
	}{
		{"missing_epoch", "EpochRows", func(in *p.WalletSequenceRows) { in.Epochs = nil }},
		{"nul_text", "InvalidTextRows", func(in *p.WalletSequenceRows) { in.Tenants[0].ID = "secret\x00tenant" }},
		{"bad_number", "InvalidValueRows", func(in *p.WalletSequenceRows) { in.Operations[1].Seq = "01" }},
		{"duplicate_tenant", "DuplicateTenantIDs", func(in *p.WalletSequenceRows) { in.Tenants = append(in.Tenants, in.Tenants[0]) }},
		{"duplicate_epoch", "DuplicateEpochIDs", func(in *p.WalletSequenceRows) { in.Epochs = append(in.Epochs, in.Epochs[0]) }},
		{"duplicate_operation", "DuplicateOperationIDs", func(in *p.WalletSequenceRows) { in.Operations = append(in.Operations, in.Operations[1]) }},
		{"duplicate_head", "DuplicateHeadTenants", func(in *p.WalletSequenceRows) { in.Heads = append(in.Heads, in.Heads[0]) }},
		{"duplicate_journal", "DuplicateJournalIDs", func(in *p.WalletSequenceRows) { in.Journals = append(in.Journals, in.Journals[0]) }},
		{"duplicate_original_operation", "DuplicateJournalOperationIDs", func(in *p.WalletSequenceRows) { in.Journals[1].OperationID = in.Journals[0].OperationID }},
		{"duplicate_legacy_source", "DuplicateLegacySourceKeys", func(in *p.WalletSequenceRows) {
			wsLegacy(in, "reserve", "old", "old-journal", "old", "0")
			in.LegacySources = append(in.LegacySources, in.LegacySources[0])
		}},
		{"duplicate_event", "DuplicateEventIDs", func(in *p.WalletSequenceRows) { in.Events = append(in.Events, in.Events[0]) }},
		{"duplicate_sequence", "DuplicateTenantSequences", func(in *p.WalletSequenceRows) { in.Operations[2].Seq = in.Operations[1].Seq }},
		{"duplicate_predecessor", "DuplicatePredecessorIDs", func(in *p.WalletSequenceRows) { in.Operations[2].PreviousID = wsPtr(*in.Operations[1].PreviousID) }},
		{"duplicate_source", "DuplicateOperationSourceKeys", func(in *p.WalletSequenceRows) {
			op := in.Operations[5]
			op.ID = wsID(50)
			op.Kind = "legacy_consumption_refund"
			in.Operations = append(in.Operations, op)
		}},
		{"duplicate_operation_journal", "DuplicateOperationJournalIDs", func(in *p.WalletSequenceRows) { in.Operations[2].JournalID = wsPtr(*in.Operations[1].JournalID) }},
		{"duplicate_legacy_journal", "DuplicateLegacyJournalIDs", func(in *p.WalletSequenceRows) {
			wsLegacy(in, "reserve", "old-one", "old-journal", "old-one", "0")
			row := in.LegacySources[0]
			row.SourceID = "old-two"
			in.LegacySources = append(in.LegacySources, row)
		}},
		{"duplicate_operation_lot", "DuplicateEventOperationLots", func(in *p.WalletSequenceRows) { e := in.Events[0]; e.ID = wsID(900); in.Events = append(in.Events, e) }},
		{"missing_tenant", "TenantIdentityRows", func(in *p.WalletSequenceRows) { in.Tenants = nil }},
		{"old_tenant_claims_new_genesis", "GenesisRows", func(in *p.WalletSequenceRows) { in.Operations[0].Kind = "tenant_genesis" }},
		{"missing_middle", "SequenceRows", func(in *p.WalletSequenceRows) { in.Operations = append(in.Operations[:2], in.Operations[3:]...) }},
		{"unadvanced_tail", "HeadRows", func(in *p.WalletSequenceRows) {
			in.Heads[0].TailSeq = "4"
			in.Heads[0].TailOperationID = in.Operations[4].ID
		}},
		{"legacy_new_overlap", "SourceBoundaryRows", func(in *p.WalletSequenceRows) {
			wsLegacy(in, "consumption_refund", "refund", "old-refund-journal", "refund", "1")
		}},
		{"wrong_journal_xid", "JournalBindingRows", func(in *p.WalletSequenceRows) { in.Journals[0].CreatedTXID = "9" }},
		{"wrong_event_xid", "EventBindingRows", func(in *p.WalletSequenceRows) { in.Events[0].CreatedXID = "9" }},
		{"delta_not_conserved", "DeltaRows", func(in *p.WalletSequenceRows) { in.Events[0].IssuedDelta = "101" }},
		{"wrong_operation_total", "OperationTotalsRows", func(in *p.WalletSequenceRows) {
			in.Operations[1].AvailableDelta = "101"
			in.Operations[1].IssuedDelta = "101"
		}},
	}
	covered := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := wsFixture()
			tc.change(&in)
			got := wsCheck(t, in)
			if wsField(t, got.Mismatches, tc.field) == 0 {
				t.Fatalf("%s not detected: %+v", tc.field, got.Mismatches)
			}
			covered[tc.field] = true
			for n := int64(1); n <= 5; n++ {
				wsShuffle(&in, n)
				if again := wsCheck(t, in); again != got {
					t.Fatalf("permutation changed duplicate/damaged graph result:\nfirst %+v\nnext %+v", got.Mismatches, again.Mismatches)
				}
			}
		})
	}
	typ := reflect.TypeOf(p.WalletSequenceRowMismatches{})
	for n := 0; n < typ.NumField(); n++ {
		if !covered[typ.Field(n).Name] {
			t.Errorf("missing directed mismatch test for %s", typ.Field(n).Name)
		}
	}
}

func TestWalletSequenceRowsIdentityAndSequenceDamage(t *testing.T) {
	cases := map[string]func(*p.WalletSequenceRows){
		"second_epoch":             func(in *p.WalletSequenceRows) { in.Epochs = append(in.Epochs, p.WalletSequenceEpochRow{ID: wsID(2)}) },
		"missing_genesis":          func(in *p.WalletSequenceRows) { in.Operations = in.Operations[1:] },
		"missing_head":             func(in *p.WalletSequenceRows) { in.Heads = nil },
		"wrong_head_epoch":         func(in *p.WalletSequenceRows) { in.Heads[0].EpochID = wsID(2) },
		"head_points_elsewhere":    func(in *p.WalletSequenceRows) { in.Heads[0].TailOperationID = wsID(50) },
		"cross_tenant_predecessor": func(in *p.WalletSequenceRows) { in.Operations[1].TenantID = "other" },
		"cross_epoch_predecessor":  func(in *p.WalletSequenceRows) { in.Operations[1].EpochID = wsID(2) },
		"predecessor_wrong_seq":    func(in *p.WalletSequenceRows) { in.Operations[1].PreviousSeq = wsPtr("1") },
		"self_predecessor":         func(in *p.WalletSequenceRows) { in.Operations[1].PreviousID = wsPtr(in.Operations[1].ID) },
		"bigint_max_not_wrap": func(in *p.WalletSequenceRows) {
			in.Operations[5].Seq = "9223372036854775807"
			in.Operations[5].PreviousSeq = wsPtr("9223372036854775806")
			in.Heads[0].TailSeq = "9223372036854775807"
		},
		"genesis_source_not_tenant":      func(in *p.WalletSequenceRows) { in.Operations[0].SourceID = "someone-else" },
		"genesis_has_previous":           func(in *p.WalletSequenceRows) { in.Operations[0].PreviousID = wsPtr(wsID(9)) },
		"genesis_journal_empty_not_null": func(in *p.WalletSequenceRows) { in.Operations[0].JournalID = wsPtr("") },
		"born_tenant_old_genesis":        func(in *p.WalletSequenceRows) { in.Tenants[0].CreatedXID = wsPtr("7") },
		"born_genesis_wrong_xid": func(in *p.WalletSequenceRows) {
			in.Tenants[0].CreatedXID = wsPtr("8")
			in.Operations[0].Kind = "tenant_genesis"
		},
		"business_journal_null":  func(in *p.WalletSequenceRows) { in.Operations[1].JournalID = nil },
		"business_original_null": func(in *p.WalletSequenceRows) { in.Operations[1].JournalOperationID = nil },
		"reserve_original_not_source": func(in *p.WalletSequenceRows) {
			in.Operations[3].JournalOperationID = wsPtr("unrelated")
			in.Journals[2].OperationID = "unrelated"
		},
		"finish_wrong_journal_kind":       func(in *p.WalletSequenceRows) { in.Journals[3].Kind = "reserve" },
		"funding_wrong_journal_kind":      func(in *p.WalletSequenceRows) { in.Journals[0].Kind = "refund" },
		"journal_wrong_tenant":            func(in *p.WalletSequenceRows) { in.Journals[0].TenantID = "other" },
		"journal_wrong_currency":          func(in *p.WalletSequenceRows) { in.Journals[0].Currency = "USD" },
		"journal_wrong_original":          func(in *p.WalletSequenceRows) { in.Journals[0].OperationID = "other-original" },
		"event_missing_operation":         func(in *p.WalletSequenceRows) { in.Events[0].WalletOperationID = wsID(999) },
		"event_wrong_tenant":              func(in *p.WalletSequenceRows) { in.Events[0].TenantID = "other" },
		"event_wrong_seq":                 func(in *p.WalletSequenceRows) { in.Events[0].WalletSeq = "2" },
		"event_wrong_kind":                func(in *p.WalletSequenceRows) { in.Events[0].Kind = "reserve" },
		"event_wrong_original":            func(in *p.WalletSequenceRows) { in.Events[0].OperationID = wsPtr("other-original") },
		"event_wrong_journal":             func(in *p.WalletSequenceRows) { in.Events[0].JournalID = wsPtr("other-journal") },
		"event_wrong_count":               func(in *p.WalletSequenceRows) { in.Operations[1].EventCount = "0" },
		"conserved_but_wrong_four_totals": func(in *p.WalletSequenceRows) { in.Events[2].AvailableDelta = "-99"; in.Events[2].HeldDelta = "99" },
		"negative_issued": func(in *p.WalletSequenceRows) {
			in.Events[0].AvailableDelta = "-100"
			in.Events[0].IssuedDelta = "-100"
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			in := wsFixture()
			change(&in)
			if got := wsCheck(t, in); got.RowsConsistent {
				t.Fatal("damaged graph accepted")
			}
		})
	}
	t.Run("new_genesis_cannot_emit_effects", func(t *testing.T) {
		in := wsBase("tenant", true)
		in.Operations[0].EventCount = "1"
		in.Events = []p.WalletSequenceEventRow{{ID: wsID(100), TenantID: "tenant", LotID: wsID(1000), WalletOperationID: wsID(10), WalletSeq: "0", Kind: "legacy_opening", CreatedXID: "7", AvailableDelta: "0", HeldDelta: "0", ConsumedDelta: "0", IssuedDelta: "0"}}
		if got := wsCheck(t, in); got.Mismatches.GenesisRows == 0 {
			t.Fatal("new genesis event accepted")
		}
	})
}

func TestWalletSequenceRowsLegacyBoundaries(t *testing.T) {
	for _, kind := range []string{"consumption_refund", "legacy_consumption_refund"} {
		t.Run("normalized_"+kind, func(t *testing.T) {
			in := wsBase("tenant", false)
			wsAppend(&in, kind, "same-refund", "same-refund", "8", "1", "0", "-1", "0", wsDelta{1, "1", "0", "-1", "0"})
			wsLegacy(&in, "consumption_refund", "same-refund", "history-journal", "same-refund", "1")
			if got := wsCheck(t, in); got.Mismatches.SourceBoundaryRows == 0 {
				t.Fatal("legacy/new refund overlap accepted")
			}
		})
	}
	t.Run("same_journal_different_source", func(t *testing.T) {
		in := wsFixture()
		in.LegacySources = []p.WalletSequenceLegacySourceRow{{EpochID: wsID(1), TenantID: "tenant", Kind: "manual_credit", SourceID: "other-receipt", JournalID: *in.Operations[1].JournalID, JournalOperationID: *in.Operations[1].JournalOperationID, Amount: "100"}}
		if got := wsCheck(t, in); got.Mismatches.SourceBoundaryRows == 0 {
			t.Fatal("shared legacy/new journal accepted")
		}
	})
	for name, change := range map[string]func(*p.WalletSequenceRows){"wrong_epoch": func(in *p.WalletSequenceRows) { in.LegacySources[0].EpochID = wsID(2) }, "wrong_tenant": func(in *p.WalletSequenceRows) { in.LegacySources[0].TenantID = "else" }, "wrong_original": func(in *p.WalletSequenceRows) { in.LegacySources[0].JournalOperationID = "else" }, "zero_funding": func(in *p.WalletSequenceRows) { in.LegacySources[0].Amount = "0" }, "negative_amount": func(in *p.WalletSequenceRows) { in.LegacySources[0].Amount = "-1" }, "new_refund_kind_in_legacy": func(in *p.WalletSequenceRows) { in.LegacySources[0].Kind = "legacy_consumption_refund" }} {
		t.Run(name, func(t *testing.T) {
			in := wsBase("tenant", false)
			wsLegacy(&in, "manual_credit", "history-receipt", "history-journal", "history-credit", "1")
			change(&in)
			if got := wsCheck(t, in); got.RowsConsistent {
				t.Fatal("invalid legacy claim accepted")
			}
		})
	}
}

func TestWalletSequenceRowsDuplicateGroupsCountOnce(t *testing.T) {
	in := wsFixture()
	for n := 0; n < 3; n++ {
		in.Tenants = append(in.Tenants, in.Tenants[0])
		in.Epochs = append(in.Epochs, in.Epochs[0])
		in.Heads = append(in.Heads, in.Heads[0])
		in.Operations = append(in.Operations, in.Operations[1])
		in.Journals = append(in.Journals, in.Journals[0])
		in.Events = append(in.Events, in.Events[0])
	}
	wsLegacy(&in, "reserve", "history-reserve", "history-journal", "history-reserve", "0")
	for n := 0; n < 3; n++ {
		in.LegacySources = append(in.LegacySources, in.LegacySources[0])
	}
	got := wsCheck(t, in)
	for _, name := range []string{"DuplicateTenantIDs", "DuplicateEpochIDs", "DuplicateOperationIDs", "DuplicateHeadTenants", "DuplicateJournalIDs", "DuplicateJournalOperationIDs", "DuplicateLegacySourceKeys", "DuplicateEventIDs", "DuplicateTenantSequences", "DuplicatePredecessorIDs", "DuplicateOperationSourceKeys", "DuplicateOperationJournalIDs", "DuplicateLegacyJournalIDs", "DuplicateEventOperationLots"} {
		if n := wsField(t, got.Mismatches, name); n != 1 {
			t.Errorf("%s=%d, want one natural key", name, n)
		}
	}
	for n := int64(0); n < 30; n++ {
		wsShuffle(&in, n)
		if again := wsCheck(t, in); again != got {
			t.Fatalf("duplicate selection depends on first occurrence:\nfirst %+v\nnext %+v", got.Mismatches, again.Mismatches)
		}
	}
}

func TestWalletSequenceRowsNumericGrammar(t *testing.T) {
	for _, bad := range []string{"1.1", "1e0", "NaN", "Infinity", "-Infinity", "", " ", " 1", "1 ", "--1", "+", "0x10", "1/1", "1..0", ".1", "1.0x"} {
		t.Run("delta_"+bad, func(t *testing.T) {
			in := wsFixture()
			in.Operations[1].AvailableDelta = bad
			if got := wsCheck(t, in); got.Mismatches.InvalidValueRows == 0 {
				t.Fatalf("invalid numeric %q accepted", bad)
			}
		})
	}
	for _, bad := range []string{"-1", "01", "+1", "1.0", "9223372036854775808", "18446744073709551615"} {
		t.Run("seq_"+bad, func(t *testing.T) {
			in := wsFixture()
			in.Operations[1].Seq = bad
			if got := wsCheck(t, in); got.Mismatches.InvalidValueRows == 0 {
				t.Fatalf("invalid bigint %q accepted", bad)
			}
		})
	}
	for _, bad := range []string{"0", "-1", "01", "+1", "1.0", "18446744073709551616"} {
		t.Run("xid_"+bad, func(t *testing.T) {
			in := wsFixture()
			in.Operations[1].CreatedXID = bad
			if got := wsCheck(t, in); got.Mismatches.InvalidValueRows == 0 {
				t.Fatalf("invalid xid %q accepted", bad)
			}
		})
	}
	for _, bad := range []string{"0", "-1", "01", "+1", "9223372036854775808"} {
		t.Run("journal_xid_"+bad, func(t *testing.T) {
			in := wsFixture()
			in.Journals[0].CreatedTXID = bad
			if got := wsCheck(t, in); got.Mismatches.InvalidValueRows == 0 {
				t.Fatalf("invalid journal bigint xid %q accepted", bad)
			}
		})
	}
	for _, bad := range []string{strings.ToUpper(wsID(10)), "00000000-0000-0000-0000-000000000000", "not-uuid", wsID(10) + " "} {
		if bad == wsID(10) {
			continue
		}
		t.Run("uuid_"+bad, func(t *testing.T) {
			in := wsFixture()
			in.Operations[0].ID = bad
			if got := wsCheck(t, in); got.Mismatches.InvalidValueRows == 0 {
				t.Fatal("noncanonical new UUID accepted")
			}
		})
	}
	t.Run("uppercase_hex_uuid", func(t *testing.T) {
		in := wsFixture()
		in.Operations[0].ID = "a0000000-0000-4000-8000-000000000010"
		in.Operations[1].PreviousID = wsPtr(in.Operations[0].ID)
		wsValid(t, in)
		in.Operations[0].ID = strings.ToUpper(in.Operations[0].ID)
		if got := wsCheck(t, in); got.Mismatches.InvalidValueRows == 0 {
			t.Fatal("uppercase UUID accepted")
		}
	})
	t.Run("invalid_utf8", func(t *testing.T) {
		in := wsFixture()
		in.Journals[0].ID = string([]byte{0xff})
		if got := wsCheck(t, in); got.Mismatches.InvalidTextRows == 0 {
			t.Fatal("invalid UTF8 accepted")
		}
	})
}

func TestWalletSequenceRowsLimitedClaim(t *testing.T) {
	in := wsFixture()
	// This deliberately changes every declaration of the grant amount from 100
	// to 999. No true receipt, journal lines, FIFO or lot cache are inputs, so
	// internally agreeing statements remain consistent within this narrow scope.
	in.Operations[1].AvailableDelta, in.Operations[1].IssuedDelta = "999", "999"
	in.Events[0].AvailableDelta, in.Events[0].IssuedDelta = "999", "999"
	wsValid(t, in)
	// Likewise removing a whole otherwise independent tenant cannot be detected.
	wsValid(t, p.WalletSequenceRows{Epochs: []p.WalletSequenceEpochRow{{ID: wsID(1)}}})
	b, err := json.Marshal(wsCheck(t, in))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{wsID(1), "credit-order-one", "receipt-one", "999", "created_xid", `"balanced"`, `"coverage_complete"`, `"proof"`} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("result exposes row data/authority %q: %s", forbidden, b)
		}
	}
}

func TestWalletSequenceRowsClosedErrorsAndBudgets(t *testing.T) {
	zero := p.WalletSequenceRowsCheck{}
	checks := []struct {
		name string
		ctx  context.Context
		in   p.WalletSequenceRows
		want error
	}{{"nil_context", nil, wsFixture(), p.ErrWalletSequenceRowsInput}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	checks = append(checks, struct {
		name string
		ctx  context.Context
		in   p.WalletSequenceRows
		want error
	}{"canceled", canceled, wsFixture(), p.ErrWalletSequenceRowsCanceled})
	timeout, cancelTimeout := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancelTimeout()
	checks = append(checks, struct {
		name string
		ctx  context.Context
		in   p.WalletSequenceRows
		want error
	}{"deadline", timeout, wsFixture(), p.ErrWalletSequenceRowsTimeout})
	long := wsBase(strings.Repeat("s", 65537), false)
	checks = append(checks, struct {
		name string
		ctx  context.Context
		in   p.WalletSequenceRows
		want error
	}{"field_length", context.Background(), long, p.ErrWalletSequenceRowsResource})
	pointerLong := wsBase("tenant", true)
	pointerLong.Tenants[0].CreatedXID = wsPtr(strings.Repeat("p", 65537))
	checks = append(checks, struct {
		name string
		ctx  context.Context
		in   p.WalletSequenceRows
		want error
	}{"pointer_field_length", context.Background(), pointerLong, p.ErrWalletSequenceRowsResource})
	rows := p.WalletSequenceRows{Epochs: make([]p.WalletSequenceEpochRow, 100001)}
	checks = append(checks, struct {
		name string
		ctx  context.Context
		in   p.WalletSequenceRows
		want error
	}{"row_total", context.Background(), rows, p.ErrWalletSequenceRowsResource})
	bytes := p.WalletSequenceRows{Epochs: make([]p.WalletSequenceEpochRow, 257)}
	for n := range bytes.Epochs {
		bytes.Epochs[n].ID = strings.Repeat("z", 65536)
	}
	checks = append(checks, struct {
		name string
		ctx  context.Context
		in   p.WalletSequenceRows
		want error
	}{"byte_total", context.Background(), bytes, p.ErrWalletSequenceRowsResource})
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.CheckWalletSequenceRows(tc.ctx, tc.in)
			if !errors.Is(err, tc.want) || got != zero {
				t.Fatalf("expected closed error %v, got %+v %v", tc.want, got, err)
			}
			if err != tc.want || strings.Contains(err.Error(), "secret") {
				t.Fatal("unbounded error details")
			}
		})
	}
	t.Run("exact_single_field_budget_is_accepted", func(t *testing.T) { wsValid(t, wsBase(strings.Repeat("x", 65536), false)) })
	t.Run("row_budget_is_global", func(t *testing.T) {
		in := p.WalletSequenceRows{Epochs: make([]p.WalletSequenceEpochRow, 50001), Tenants: make([]p.WalletSequenceTenantRow, 50000)}
		got, err := p.CheckWalletSequenceRows(context.Background(), in)
		if err != p.ErrWalletSequenceRowsResource || got != zero {
			t.Fatal("per-table budget bypassed global row limit")
		}
	})
}

func TestWalletSequenceRowsConflictingDuplicateGroupsAreOrderIndependent(t *testing.T) {
	in := wsFixture()
	tenant := in.Tenants[0]
	tenant.CreatedXID = wsPtr("7")
	in.Tenants = append(in.Tenants, tenant)
	op := in.Operations[1]
	op.AvailableDelta, op.IssuedDelta = "999", "999"
	in.Operations = append(in.Operations, op)
	journal := in.Journals[0]
	journal.OperationID = "different-original"
	journal.CreatedTXID = "999"
	in.Journals = append(in.Journals, journal)
	event := in.Events[0]
	event.AvailableDelta, event.IssuedDelta = "999", "999"
	in.Events = append(in.Events, event)
	wsLegacy(&in, "manual_credit", "history-receipt", "history-journal", "history-credit", "1")
	old := in.LegacySources[0]
	old.JournalID = "another-history-journal"
	old.JournalOperationID = "another-history-credit"
	old.Amount = "99"
	in.LegacySources = append(in.LegacySources, old)
	got := wsCheck(t, in)
	if got.RowsConsistent {
		t.Fatal("conflicting duplicates accepted")
	}
	for n := int64(0); n < 50; n++ {
		wsShuffle(&in, n)
		if again := wsCheck(t, in); again != got {
			t.Fatalf("first row selected from a conflicting duplicate group:\nfirst %+v\nnext %+v", got.Mismatches, again.Mismatches)
		}
	}
}

func TestWalletSequenceRowsPointerTextCountsTowardSharedByteBudget(t *testing.T) {
	in := p.WalletSequenceRows{Epochs: []p.WalletSequenceEpochRow{{ID: wsID(1)}}, Tenants: make([]p.WalletSequenceTenantRow, 256)}
	for n := range in.Tenants {
		in.Tenants[n].CreatedXID = wsPtr(strings.Repeat("1", 65536))
	}
	got, err := p.CheckWalletSequenceRows(context.Background(), in)
	if err != p.ErrWalletSequenceRowsResource || got != (p.WalletSequenceRowsCheck{}) {
		t.Fatal("pointer text omitted from shared byte budget")
	}
}

func TestWalletSequenceRowsPostgresIntegralDecimalForms(t *testing.T) {
	for _, amount := range []string{"+100", "000100", "+000100.000", "100.00"} {
		t.Run(amount, func(t *testing.T) {
			in := wsFixture()
			in.Operations[1].AvailableDelta, in.Operations[1].IssuedDelta = amount, amount
			in.Operations[1].HeldDelta = "-000.000"
			in.Events[0].HeldDelta = "+000.00"
			wsValid(t, in)
		})
	}
}

func TestWalletSequenceRowsExactInputLimitsAreNotTruncation(t *testing.T) {
	t.Run("one_hundred_thousand_rows_enumerated", func(t *testing.T) {
		in := p.WalletSequenceRows{Epochs: make([]p.WalletSequenceEpochRow, 100000)}
		out, err := p.CheckWalletSequenceRows(context.Background(), in)
		if err != nil || out.Scanned.Epochs != 100000 || out.RowsConsistent {
			t.Fatalf("at-limit rows must be enumerated as malformed data, got %+v %v", out, err)
		}
	})
	t.Run("sixteen_mib_strings_enumerated", func(t *testing.T) {
		in := p.WalletSequenceRows{Epochs: make([]p.WalletSequenceEpochRow, 256)}
		value := strings.Repeat("x", 65536)
		for n := range in.Epochs {
			in.Epochs[n].ID = value
		}
		out, err := p.CheckWalletSequenceRows(context.Background(), in)
		if err != nil || out.Scanned.Epochs != 256 || out.RowsConsistent {
			t.Fatalf("at-limit bytes must be enumerated as malformed data, got %+v %v", out, err)
		}
	})
}

type wsCancelDuringWork struct {
	context.Context
	cancel context.CancelFunc
	calls  atomic.Int32
	after  int32
	cause  error
}

func (c *wsCancelDuringWork) Err() error {
	if c.calls.Add(1) >= c.after {
		c.cancel()
	}
	if err := c.Context.Err(); err != nil {
		return c.cause
	}
	return nil
}
func TestWalletSequenceRowsCancellationDuringBudgetOrIndexWorkIsClosed(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		want := p.ErrWalletSequenceRowsCanceled
		if cause == context.DeadlineExceeded {
			want = p.ErrWalletSequenceRowsTimeout
		}
		// 2 catches a deadline reached between repeated initial Err calls;
		// 3/40 stop in input budgeting; 400 progresses into the epoch index pass.
		// Err changes state deterministically, without depending on a wall timer.
		for _, after := range []int32{2, 3, 40, 400} {
			t.Run(fmt.Sprintf("%v/%d", cause, after), func(t *testing.T) {
				base, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx := &wsCancelDuringWork{Context: base, cancel: cancel, after: after, cause: cause}
				in := p.WalletSequenceRows{Epochs: make([]p.WalletSequenceEpochRow, 40000)}
				out, err := p.CheckWalletSequenceRows(ctx, in)
				if err != want || out != (p.WalletSequenceRowsCheck{}) {
					t.Fatalf("mid-work cancellation must discard every counter, got %+v %v; checks=%d", out, err, ctx.calls.Load())
				}
			})
		}
	}
}

func TestWalletSequenceRowsNewTenantCannotHaveLegacySource(t *testing.T) {
	in := wsBase("new-tenant", true)
	wsLegacy(&in, "manual_credit", "history-receipt", "history-journal", "history-credit", "1")
	got := wsCheck(t, in)
	if got.Mismatches.SourceBoundaryRows != 1 {
		t.Fatalf("a born tenant cannot own a cutover legacy source: %+v", got.Mismatches)
	}
}

func TestWalletSequenceRowsReserveKindDoesNotProveIssuanceShape(t *testing.T) {
	in := wsFixture()
	// Reserve now falsely claims a 13-unit issuance. Both its event and aggregate
	// declarations agree and conserve A+H+S=I. This narrow structure checker has
	// no real reservation amount or lot provenance to independently reject it.
	in.Operations[3].AvailableDelta = "-97"
	in.Operations[3].IssuedDelta = "13"
	in.Events[2].AvailableDelta = "-87"
	in.Events[2].IssuedDelta = "13"
	wsValid(t, in)
}
