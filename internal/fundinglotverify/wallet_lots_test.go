package fundinglotverify_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func wlID(n int) string { return fmt.Sprintf("20000000-0000-4000-8000-%012d", n) }
func wlTime(n int) p.WalletLotsTime {
	return p.WalletLotsTime{Valid: true, Value: time.Date(2026, 11, 2, 12, 0, n, 0, time.UTC)}
}
func wlSHA(value []byte) string {
	h := sha256.Sum256(value)
	return hex.EncodeToString(h[:])
}
func wlRawBase(mode string) p.WalletLotsRows {
	canonical := []byte(`{"test":"opaque supplied attestation"}`)
	digest := wlSHA(canonical)
	in := p.WalletLotsRows{
		Epochs:       []p.WalletLotsEpochRow{{Singleton: true, ID: wlID(1), SchemaVersion: "1", DatabaseOID: "101", SchemaOID: "102", BasisDigest: strings.Repeat("a", 64), ManifestDigest: strings.Repeat("b", 64), DescriptorDigest: strings.Repeat("c", 64), RuntimeProofDigest: digest, TenantCount: "0", ReceiptCount: "0", SettlementCount: "0", ApprovedRefundCount: "0", OpeningCount: "0", LegacySourceCount: "0", AvailableTotal: "0", HeldTotal: "0", Actor: "offline-maintenance", CreatedAt: wlTime(0)}},
		Attestations: []p.WalletLotsAttestationRow{{Singleton: true, EpochID: wlID(1), ProofVersion: "2", Mode: mode, DatabaseOID: "101", SchemaOID: "102", SchemaOwnerOID: "103", AppRoleOID: "104", ProjectorRoleOID: "105", ProofCanonical: canonical, ProofSHA256: digest, ManifestDigest: strings.Repeat("b", 64), DescriptorDigest: strings.Repeat("c", 64), CreatedAt: wlTime(0)}},
	}
	if mode == "sandbox" {
		in.Attestations[0].WorkerRoleOID = wsPtr("106")
		in.Attestations[0].PredecessorPaymentDigest = wsPtr(strings.Repeat("d", 64))
	}
	return in
}

type wlBuilder struct {
	in     p.WalletLotsRows
	tenant string
	base   int
	last   map[string]string
}

func wlNew(tenant string, base int, born bool, mode string) *wlBuilder {
	b := &wlBuilder{in: wlRawBase(mode), tenant: tenant, base: base, last: map[string]string{}}
	var birth *string
	kind := "epoch_opening"
	if born {
		birth, kind = wsPtr("101"), "tenant_genesis"
	}
	b.in.Tenants = []p.WalletSequenceTenantRow{{ID: tenant, CreatedXID: birth}}
	for _, k := range []string{"AVAILABLE", "HELD"} {
		b.in.Accounts = append(b.in.Accounts, p.LedgerAccountRow{ID: b.account(k), TenantID: wsPtr(tenant), Kind: k, Balance: "0"})
	}
	for _, k := range []string{"FUNDING_CLEARING", "OPERATING_GRANT_CLEARING", "SANDBOX_FUNDING_CLEARING", "REVENUE"} {
		b.in.Accounts = append(b.in.Accounts, p.LedgerAccountRow{ID: b.account(k), Kind: k, Balance: "0"})
	}
	b.op(kind, tenant, nil, nil, "101", "0", [4]string{"0", "0", "0", "0"}, wlTime(0))
	return b
}
func (b *wlBuilder) s(s string) string { return b.tenant + "/" + s }
func (b *wlBuilder) id(n int) string   { return wlID(b.base + n) }
func (b *wlBuilder) account(kind string) string {
	if kind == "AVAILABLE" || kind == "HELD" {
		return b.s(kind)
	}
	return "platform/" + kind
}
func (b *wlBuilder) balance(kind, amount string) {
	for n := range b.in.Accounts {
		if b.in.Accounts[n].ID == b.account(kind) {
			b.in.Accounts[n].Balance = amount
			return
		}
	}
	panic("fixture account missing")
}
func (b *wlBuilder) op(kind, source string, journal, original *string, xid, count string, d [4]string, at p.WalletLotsTime) int {
	n := len(b.in.Operations)
	r := p.WalletSequenceOperationRow{ID: b.id(100 + n), TenantID: b.tenant, EpochID: wlID(1), Seq: fmt.Sprint(n), Kind: kind, SourceID: source, JournalID: journal, JournalOperationID: original, EventCount: count, AvailableDelta: d[0], HeldDelta: d[1], ConsumedDelta: d[2], IssuedDelta: d[3], CreatedXID: xid}
	if n > 0 {
		r.PreviousID = wsPtr(b.in.Operations[n-1].ID)
		r.PreviousSeq = wsPtr(fmt.Sprint(n - 1))
	}
	b.in.Operations = append(b.in.Operations, p.WalletLotsOperationRow{WalletSequenceOperationRow: r, CreatedAt: at})
	b.in.Heads = []p.WalletSequenceHeadRow{{TenantID: b.tenant, EpochID: wlID(1), TailSeq: r.Seq, TailOperationID: r.ID}}
	return n
}

type wlLine struct{ kind, delta string }

func (b *wlBuilder) journal(name, original, kind, reference, actor, xid string, at p.WalletLotsTime, lines ...wlLine) string {
	id := b.s(name)
	b.in.Journals = append(b.in.Journals, p.WalletLotsJournalRow{WalletSequenceJournalRow: p.WalletSequenceJournalRow{ID: id, TenantID: b.tenant, OperationID: original, Kind: kind, Currency: "CNY", CreatedTXID: xid}, Reference: reference, Actor: actor, CreatedAt: at})
	for _, line := range lines {
		b.in.Lines = append(b.in.Lines, p.LedgerLineRow{ID: b.s(fmt.Sprintf("line-%d", len(b.in.Lines))), JournalID: id, AccountID: b.account(line.kind), SignedDelta: line.delta})
	}
	return id
}
func (b *wlBuilder) lot(n int, kind string, receipt, opening, refund *string, amount, class string, at p.WalletLotsTime) int {
	b.in.Lots = append(b.in.Lots, p.WalletLotsLotRow{ID: b.id(1000 + n), TenantID: b.tenant, OriginKind: kind, FundingReceiptID: receipt, LegacyOpeningID: opening, LegacyRefundOrderID: refund, IssuedAmount: amount, Currency: "CNY", RefundClass: class, FIFOAt: at, CreatedAt: at})
	return len(b.in.Lots) - 1
}

// before/after buckets and deltas are deliberately supplied as independent,
// hand-written tables. This helper only attaches identities and chain links;
// it does not allocate, charge, refund, sum amounts, or compute bucket values.
func (b *wlBuilder) event(op, lot, beforeRevision int, before, after [3]string, d [4]string) string {
	o, l := b.in.Operations[op], b.in.Lots[lot]
	id := b.id(5000 + len(b.in.Events))
	kind := o.Kind
	if kind == "epoch_opening" {
		kind = "legacy_opening"
	}
	if kind == "manual_credit" || kind == "voucher_grant" || kind == "sandbox_capture" {
		kind = "funding"
	}
	e := p.WalletLotsEventRow{WalletSequenceEventRow: p.WalletSequenceEventRow{ID: id, TenantID: b.tenant, LotID: l.ID, WalletOperationID: o.ID, WalletSeq: o.Seq, Kind: kind, OperationID: o.JournalOperationID, JournalID: o.JournalID, CreatedXID: o.CreatedXID, AvailableDelta: d[0], HeldDelta: d[1], ConsumedDelta: d[2], IssuedDelta: d[3]}, BeforeRevision: fmt.Sprint(beforeRevision), AfterRevision: fmt.Sprint(beforeRevision + 1), BeforeAvailable: before[0], BeforeHeld: before[1], BeforeConsumed: before[2], AfterAvailable: after[0], AfterHeld: after[1], AfterConsumed: after[2], CreatedAt: o.CreatedAt}
	if previous, ok := b.last[l.ID]; ok {
		e.PreviousEventID = wsPtr(previous)
	}
	b.last[l.ID] = id
	b.in.Events = append(b.in.Events, e)
	return id
}
func (b *wlBuilder) cache(lot int, a, h, s, revision string) {
	l := b.in.Lots[lot]
	b.in.Balances = append(b.in.Balances, p.WalletLotsBalanceRow{LotID: l.ID, TenantID: b.tenant, AvailableAmount: a, InferenceHeldAmount: h, ConsumedAmount: s, Revision: revision, LastEventID: b.last[l.ID]})
}
func (b *wlBuilder) funding(n int, kind, source, original, amount, clearing, class string) int {
	at := wlTime(n + 1)
	jKind := kind
	reference := b.s(source)
	if kind == "manual_credit" {
		jKind = "credit"
	}
	if kind == "sandbox_capture" {
		reference = b.s("order-" + source)
	}
	j := b.journal(fmt.Sprintf("fund-journal-%d", n), b.s(original), jKind, reference, "finance", "201", at, wlLine{"AVAILABLE", amount}, wlLine{clearing, "-" + amount})
	receipt := b.s(fmt.Sprintf("receipt-%d", n))
	b.in.Receipts = append(b.in.Receipts, p.WalletLotsReceiptRow{ID: receipt, TenantID: b.tenant, SourceKind: kind, SourceID: b.s(source), Amount: amount, Currency: "CNY", JournalID: j, CreatedAt: at})
	op := b.op(kind, receipt, wsPtr(j), wsPtr(b.s(original)), "201", "1", [4]string{amount, "0", "0", amount}, at)
	lot := b.lot(n, "funding_receipt", wsPtr(receipt), nil, nil, amount, class, at)
	b.event(op, lot, 0, [3]string{"0", "0", "0"}, [3]string{amount, "0", "0"}, [4]string{amount, "0", "0", amount})
	return lot
}
func (b *wlBuilder) refund(id, settlement, amount, name string, xid string, at p.WalletLotsTime) string {
	j := b.journal(name, b.s(id), "refund", b.s(settlement), "checker", xid, at, wlLine{"AVAILABLE", amount}, wlLine{"REVENUE", "-" + amount})
	b.in.Refunds = append(b.in.Refunds, p.WalletLotsRefundRow{ID: b.s(id), TenantID: b.tenant, SettlementID: b.s(settlement), Amount: amount, State: "approved", Requester: "maker", Approver: wsPtr("checker"), JournalID: wsPtr(j)})
	return j
}

func wlThree() p.WalletLotsRows { return wlThreeTenant("tenant", 0, true) }
func wlThreeTenant(tenant string, base int, split bool) p.WalletLotsRows {
	b := wlNew(tenant, base, true, "sandbox")
	b.funding(0, "manual_credit", "credit-one", "credit-one", "40", "FUNDING_CLEARING", "nonrefundable")
	b.funding(1, "voucher_grant", "voucher-two", "redemption-two", "50", "OPERATING_GRANT_CLEARING", "nonrefundable")
	b.funding(2, "sandbox_capture", "capture-three", "capture-three", "60", "SANDBOX_FUNDING_CLEARING", "sandbox_original")
	b.in.Requests = []p.WalletLotsRequestRow{{ID: b.s("request"), TenantID: tenant, OwnerID: "customer", BillingStatus: "settled"}}
	b.in.Reservations = []p.WalletLotsReservationRow{{ID: b.s("reservation"), TenantID: tenant, RequestID: b.s("request"), Amount: "120", Status: "settled"}}
	j := b.journal("reserve-journal", b.s("reservation"), "reserve", b.s("request"), "customer", "202", wlTime(4), wlLine{"AVAILABLE", "-120"}, wlLine{"HELD", "120"})
	op := b.op("reserve", b.s("reservation"), wsPtr(j), wsPtr(b.s("reservation")), "202", "3", [4]string{"-120", "120", "0", "0"}, wlTime(4))
	for n, amount := range []string{"40", "50", "30"} {
		b.in.ReservationLots = append(b.in.ReservationLots, p.WalletLotsReservationLotRow{ID: b.id(10000 + n), TenantID: tenant, ReservationID: b.s("reservation"), LotID: b.in.Lots[n].ID, AllocationIndex: fmt.Sprint(n + 1), Amount: amount, CreatedXID: "202", CreatedAt: wlTime(4)})
	}
	b.event(op, 0, 1, [3]string{"40", "0", "0"}, [3]string{"0", "40", "0"}, [4]string{"-40", "40", "0", "0"})
	b.event(op, 1, 1, [3]string{"50", "0", "0"}, [3]string{"0", "50", "0"}, [4]string{"-50", "50", "0", "0"})
	b.event(op, 2, 1, [3]string{"60", "0", "0"}, [3]string{"30", "30", "0"}, [4]string{"-30", "30", "0", "0"})
	j = b.journal("finish-journal", b.s("settlement"), "settled", b.s("request"), "system", "203", wlTime(5), wlLine{"AVAILABLE", "45"}, wlLine{"HELD", "-120"}, wlLine{"REVENUE", "75"})
	b.in.Settlements = []p.WalletLotsSettlementRow{{ID: b.s("settlement"), TenantID: tenant, RequestID: b.s("request"), ChargedAmount: "75", RefundedAmount: "50", Status: "settled", Reason: "usage_known", JournalID: j}}
	op = b.op("finish", b.s("settlement"), wsPtr(j), wsPtr(b.s("settlement")), "203", "3", [4]string{"45", "-120", "75", "0"}, wlTime(5))
	for n, amount := range [][2]string{{"40", "0"}, {"35", "15"}, {"0", "30"}} {
		b.in.SettlementLots = append(b.in.SettlementLots, p.WalletLotsSettlementLotRow{ID: b.id(11000 + n), TenantID: tenant, SettlementID: b.s("settlement"), ReservationLotID: b.in.ReservationLots[n].ID, ChargedAmount: amount[0], ReleasedAmount: amount[1], CreatedXID: "203", CreatedAt: wlTime(5)})
	}
	b.event(op, 0, 2, [3]string{"0", "40", "0"}, [3]string{"0", "0", "40"}, [4]string{"0", "-40", "40", "0"})
	b.event(op, 1, 2, [3]string{"0", "50", "0"}, [3]string{"15", "0", "35"}, [4]string{"15", "-50", "35", "0"})
	b.event(op, 2, 2, [3]string{"30", "30", "0"}, [3]string{"60", "0", "0"}, [4]string{"30", "-30", "0", "0"})
	if split {
		j = b.refund("refund-20", "settlement", "20", "refund-journal-20", "204", wlTime(6))
		op = b.op("consumption_refund", b.s("refund-20"), wsPtr(j), wsPtr(b.s("refund-20")), "204", "1", [4]string{"20", "0", "-20", "0"}, wlTime(6))
		b.in.RefundLots = append(b.in.RefundLots, p.WalletLotsRefundLotRow{ID: b.id(12000), TenantID: tenant, RefundOrderID: b.s("refund-20"), LotID: b.in.Lots[1].ID, SettlementLotID: wsPtr(b.in.SettlementLots[1].ID), AllocationIndex: "1", Amount: "20", Origin: "allocated", CreatedXID: "204", CreatedAt: wlTime(6)})
		b.event(op, 1, 3, [3]string{"15", "0", "35"}, [3]string{"35", "0", "15"}, [4]string{"20", "0", "-20", "0"})
		j = b.refund("refund-30", "settlement", "30", "refund-journal-30", "204", wlTime(7))
		op = b.op("consumption_refund", b.s("refund-30"), wsPtr(j), wsPtr(b.s("refund-30")), "204", "2", [4]string{"30", "0", "-30", "0"}, wlTime(7))
		b.in.RefundLots = append(b.in.RefundLots, p.WalletLotsRefundLotRow{ID: b.id(12001), TenantID: tenant, RefundOrderID: b.s("refund-30"), LotID: b.in.Lots[1].ID, SettlementLotID: wsPtr(b.in.SettlementLots[1].ID), AllocationIndex: "1", Amount: "15", Origin: "allocated", CreatedXID: "204", CreatedAt: wlTime(7)}, p.WalletLotsRefundLotRow{ID: b.id(12002), TenantID: tenant, RefundOrderID: b.s("refund-30"), LotID: b.in.Lots[0].ID, SettlementLotID: wsPtr(b.in.SettlementLots[0].ID), AllocationIndex: "2", Amount: "15", Origin: "allocated", CreatedXID: "204", CreatedAt: wlTime(7)})
		b.event(op, 1, 4, [3]string{"35", "0", "15"}, [3]string{"50", "0", "0"}, [4]string{"15", "0", "-15", "0"})
		b.event(op, 0, 3, [3]string{"0", "0", "40"}, [3]string{"15", "0", "25"}, [4]string{"15", "0", "-15", "0"})
		b.cache(0, "15", "0", "25", "4")
		b.cache(1, "50", "0", "0", "5")
	} else {
		j = b.refund("refund-50", "settlement", "50", "refund-journal-50", "204", wlTime(6))
		op = b.op("consumption_refund", b.s("refund-50"), wsPtr(j), wsPtr(b.s("refund-50")), "204", "2", [4]string{"50", "0", "-50", "0"}, wlTime(6))
		b.in.RefundLots = append(b.in.RefundLots, p.WalletLotsRefundLotRow{ID: b.id(12000), TenantID: tenant, RefundOrderID: b.s("refund-50"), LotID: b.in.Lots[1].ID, SettlementLotID: wsPtr(b.in.SettlementLots[1].ID), AllocationIndex: "1", Amount: "35", Origin: "allocated", CreatedXID: "204", CreatedAt: wlTime(6)}, p.WalletLotsRefundLotRow{ID: b.id(12001), TenantID: tenant, RefundOrderID: b.s("refund-50"), LotID: b.in.Lots[0].ID, SettlementLotID: wsPtr(b.in.SettlementLots[0].ID), AllocationIndex: "2", Amount: "15", Origin: "allocated", CreatedXID: "204", CreatedAt: wlTime(6)})
		b.event(op, 1, 3, [3]string{"15", "0", "35"}, [3]string{"50", "0", "0"}, [4]string{"35", "0", "-35", "0"})
		b.event(op, 0, 3, [3]string{"0", "0", "40"}, [3]string{"15", "0", "25"}, [4]string{"15", "0", "-15", "0"})
		b.cache(0, "15", "0", "25", "4")
		b.cache(1, "50", "0", "0", "4")
	}
	b.cache(2, "60", "0", "0", "3")
	b.balance("AVAILABLE", "125")
	b.balance("FUNDING_CLEARING", "-40")
	b.balance("OPERATING_GRANT_CLEARING", "-50")
	b.balance("SANDBOX_FUNDING_CLEARING", "-60")
	b.balance("REVENUE", "25")
	return b.in
}

func wlLegacy() p.WalletLotsRows {
	b := wlNew("legacy", 0, false, "ordinary")
	tenant := b.tenant
	credit := b.journal("old-credit-journal", b.s("old-credit"), "credit", b.s("old-credit"), "finance", "90", wlTime(-5), wlLine{"AVAILABLE", "200"}, wlLine{"FUNDING_CLEARING", "-200"})
	receipt := b.s("old-receipt")
	b.in.Receipts = []p.WalletLotsReceiptRow{{ID: receipt, TenantID: tenant, SourceKind: "manual_credit", SourceID: b.s("old-credit"), Amount: "200", Currency: "CNY", JournalID: credit, CreatedAt: wlTime(-5)}}
	oldReserve := b.journal("old-reserve-journal", b.s("old-reservation"), "reserve", b.s("old-request"), "customer", "91", wlTime(-4), wlLine{"AVAILABLE", "-100"}, wlLine{"HELD", "100"})
	oldFinish := b.journal("old-finish-journal", b.s("old-settlement"), "settled", b.s("old-request"), "system", "92", wlTime(-3), wlLine{"AVAILABLE", "0"}, wlLine{"HELD", "-100"}, wlLine{"REVENUE", "100"})
	oldRefund := b.refund("old-refund-20", "old-settlement", "20", "old-refund-journal", "93", wlTime(-2))
	unfinished := b.journal("old-held-journal", b.s("held-reservation"), "reserve", b.s("held-request"), "customer", "94", wlTime(-1), wlLine{"AVAILABLE", "-30"}, wlLine{"HELD", "30"})
	b.in.Requests = []p.WalletLotsRequestRow{{ID: b.s("old-request"), TenantID: tenant, OwnerID: "customer", BillingStatus: "settled"}, {ID: b.s("held-request"), TenantID: tenant, OwnerID: "customer", BillingStatus: "settled"}}
	b.in.Reservations = []p.WalletLotsReservationRow{{ID: b.s("old-reservation"), TenantID: tenant, RequestID: b.s("old-request"), Amount: "100", Status: "settled"}, {ID: b.s("held-reservation"), TenantID: tenant, RequestID: b.s("held-request"), Amount: "30", Status: "settled"}}
	b.in.Settlements = []p.WalletLotsSettlementRow{{ID: b.s("old-settlement"), TenantID: tenant, RequestID: b.s("old-request"), ChargedAmount: "100", RefundedAmount: "100", Status: "settled", Reason: "usage_known", JournalID: oldFinish}}
	b.in.LegacyReceipts = []p.WalletLotsLegacyReceiptRow{{ReceiptID: receipt, EpochID: wlID(1), TenantID: tenant, SourceKind: "manual_credit", SourceID: b.s("old-credit"), Amount: "200", JournalID: credit}}
	b.in.LegacySettlements = []p.WalletLotsLegacySettlementRow{{SettlementID: b.s("old-settlement"), EpochID: wlID(1), TenantID: tenant, ReservationID: b.s("old-reservation"), RequestID: b.s("old-request"), ChargedAmount: "100", RefundedAmountAtCutover: "20", JournalID: oldFinish}}
	b.in.LegacyApprovedRefunds = []p.WalletLotsLegacyApprovedRefundRow{{RefundOrderID: b.s("old-refund-20"), EpochID: wlID(1), TenantID: tenant, SettlementID: b.s("old-settlement"), Amount: "20", JournalID: oldRefund}}
	addOld := func(kind, source, journal, original, amount string, request, reservation, settlement *string) {
		b.in.LegacySources = append(b.in.LegacySources, p.WalletLotsLegacySourceRow{WalletSequenceLegacySourceRow: p.WalletSequenceLegacySourceRow{EpochID: wlID(1), TenantID: tenant, Kind: kind, SourceID: source, JournalID: journal, JournalOperationID: original, Amount: amount}, RequestID: request, ReservationID: reservation, SettlementID: settlement})
	}
	addOld("manual_credit", receipt, credit, b.s("old-credit"), "200", nil, nil, nil)
	addOld("reserve", b.s("old-reservation"), oldReserve, b.s("old-reservation"), "100", wsPtr(b.s("old-request")), wsPtr(b.s("old-reservation")), nil)
	addOld("finish", b.s("old-settlement"), oldFinish, b.s("old-settlement"), "100", wsPtr(b.s("old-request")), wsPtr(b.s("old-reservation")), wsPtr(b.s("old-settlement")))
	addOld("consumption_refund", b.s("old-refund-20"), oldRefund, b.s("old-refund-20"), "20", wsPtr(b.s("old-request")), wsPtr(b.s("old-reservation")), wsPtr(b.s("old-settlement")))
	addOld("reserve", b.s("held-reservation"), unfinished, b.s("held-reservation"), "30", wsPtr(b.s("held-request")), wsPtr(b.s("held-reservation")), nil)
	b.in.LegacyOpenings = []p.WalletLotsLegacyOpeningRow{{ID: b.id(700), EpochID: wlID(1), TenantID: tenant, Kind: "available", AccountID: wsPtr(b.account("AVAILABLE")), Amount: "90", ObservedAt: wlTime(0)}, {ID: b.id(701), EpochID: wlID(1), TenantID: tenant, Kind: "reservation", AccountID: wsPtr(b.account("HELD")), ReservationID: wsPtr(b.s("held-reservation")), Amount: "30", ObservedAt: wlTime(0)}}
	e := &b.in.Epochs[0]
	e.TenantCount = "1"
	e.ReceiptCount = "1"
	e.SettlementCount = "1"
	e.ApprovedRefundCount = "1"
	e.OpeningCount = "2"
	e.LegacySourceCount = "5"
	e.AvailableTotal = "90"
	e.HeldTotal = "30"
	b.in.Operations[0].EventCount = "2"
	b.in.Operations[0].AvailableDelta = "90"
	b.in.Operations[0].HeldDelta = "30"
	b.in.Operations[0].IssuedDelta = "120"
	b.lot(0, "legacy_available", nil, wsPtr(b.in.LegacyOpenings[0].ID), nil, "90", "nonrefundable", wlTime(0))
	b.lot(1, "legacy_reservation", nil, wsPtr(b.in.LegacyOpenings[1].ID), nil, "30", "nonrefundable", wlTime(0))
	b.event(0, 0, 0, [3]string{"0", "0", "0"}, [3]string{"90", "0", "0"}, [4]string{"90", "0", "0", "90"})
	b.event(0, 1, 0, [3]string{"0", "0", "0"}, [3]string{"0", "30", "0"}, [4]string{"0", "30", "0", "30"})
	b.in.ReservationLots = []p.WalletLotsReservationLotRow{{ID: b.id(10000), TenantID: tenant, ReservationID: b.s("held-reservation"), LotID: b.in.Lots[1].ID, AllocationIndex: "1", Amount: "30", CreatedXID: "101", CreatedAt: wlTime(0)}}
	j := b.journal("held-finish-journal", b.s("held-settlement"), "settled", b.s("held-request"), "system", "202", wlTime(1), wlLine{"AVAILABLE", "18"}, wlLine{"HELD", "-30"}, wlLine{"REVENUE", "12"})
	b.in.Settlements = append(b.in.Settlements, p.WalletLotsSettlementRow{ID: b.s("held-settlement"), TenantID: tenant, RequestID: b.s("held-request"), ChargedAmount: "12", RefundedAmount: "5", Status: "settled", Reason: "usage_known", JournalID: j})
	op := b.op("finish", b.s("held-settlement"), wsPtr(j), wsPtr(b.s("held-settlement")), "202", "1", [4]string{"18", "-30", "12", "0"}, wlTime(1))
	b.in.SettlementLots = []p.WalletLotsSettlementLotRow{{ID: b.id(11000), TenantID: tenant, SettlementID: b.s("held-settlement"), ReservationLotID: b.in.ReservationLots[0].ID, ChargedAmount: "12", ReleasedAmount: "18", CreatedXID: "202", CreatedAt: wlTime(1)}}
	b.event(op, 1, 1, [3]string{"0", "30", "0"}, [3]string{"18", "0", "12"}, [4]string{"18", "-30", "12", "0"})
	j = b.refund("held-refund-5", "held-settlement", "5", "held-refund-journal", "203", wlTime(2))
	op = b.op("consumption_refund", b.s("held-refund-5"), wsPtr(j), wsPtr(b.s("held-refund-5")), "203", "1", [4]string{"5", "0", "-5", "0"}, wlTime(2))
	b.in.RefundLots = append(b.in.RefundLots, p.WalletLotsRefundLotRow{ID: b.id(12000), TenantID: tenant, RefundOrderID: b.s("held-refund-5"), LotID: b.in.Lots[1].ID, SettlementLotID: wsPtr(b.in.SettlementLots[0].ID), AllocationIndex: "1", Amount: "5", Origin: "allocated", CreatedXID: "203", CreatedAt: wlTime(2)})
	b.event(op, 1, 2, [3]string{"18", "0", "12"}, [3]string{"23", "0", "7"}, [4]string{"5", "0", "-5", "0"})
	for n, amount := range []string{"30", "50"} {
		name := []string{"old-pending-30", "old-refund-50"}[n]
		at := wlTime(3 + n)
		j = b.refund(name, "old-settlement", amount, "new-legacy-refund-journal-"+amount, "204", at)
		op = b.op("legacy_consumption_refund", b.s(name), wsPtr(j), wsPtr(b.s(name)), "204", "1", [4]string{amount, "0", "0", amount}, at)
		lot := b.lot(n+2, "legacy_consumption_refund", nil, nil, wsPtr(b.s(name)), amount, "nonrefundable", at)
		b.in.RefundLots = append(b.in.RefundLots, p.WalletLotsRefundLotRow{ID: b.id(12001 + n), TenantID: tenant, RefundOrderID: b.s(name), LotID: b.in.Lots[lot].ID, AllocationIndex: "1", Amount: amount, Origin: "legacy_settlement", CreatedXID: "204", CreatedAt: at})
		b.event(op, lot, 0, [3]string{"0", "0", "0"}, [3]string{amount, "0", "0"}, [4]string{amount, "0", "0", amount})
		b.cache(lot, amount, "0", "0", "1")
	}
	b.cache(0, "90", "0", "0", "1")
	b.cache(1, "23", "0", "7", "3")
	b.balance("AVAILABLE", "193")
	b.balance("FUNDING_CLEARING", "-200")
	b.balance("REVENUE", "7")
	return b.in
}

func wlClone(in p.WalletLotsRows) p.WalletLotsRows {
	return wlCloneValue(reflect.ValueOf(in)).Interface().(p.WalletLotsRows)
}
func wlCloneValue(v reflect.Value) reflect.Value {
	if v.Type() == reflect.TypeOf(time.Time{}) {
		return reflect.ValueOf(v.Interface().(time.Time))
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(wlCloneValue(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for n := 0; n < v.Len(); n++ {
			out.Index(n).Set(wlCloneValue(v.Index(n)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		for n := 0; n < v.NumField(); n++ {
			out.Field(n).Set(wlCloneValue(v.Field(n)))
		}
		return out
	default:
		return v
	}
}
func wlShuffle(in *p.WalletLotsRows, seed int64) {
	r := rand.New(rand.NewSource(seed))
	v := reflect.ValueOf(in).Elem()
	for n := 0; n < v.NumField(); n++ {
		group := v.Field(n).Interface()
		r.Shuffle(v.Field(n).Len(), reflect.Swapper(group))
	}
}
func wlRun(t *testing.T, in p.WalletLotsRows) p.WalletLotsRowsCheck {
	t.Helper()
	before := wlClone(in)
	out, err := p.CheckWalletLotsRows(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, in) {
		t.Fatal("caller input mutated")
	}
	if out.Scope != "supplied_wallet_lots_rows" {
		t.Fatalf("scope=%q", out.Scope)
	}
	raw, count := reflect.ValueOf(in), reflect.ValueOf(out.Scanned)
	if raw.NumField() != count.NumField() {
		t.Fatal("scanned does not cover all 25 groups")
	}
	for n := 0; n < raw.NumField(); n++ {
		if count.Field(n).Uint() != uint64(raw.Field(n).Len()) {
			t.Fatalf("incomplete %s count", raw.Type().Field(n).Name)
		}
	}
	mm := out.Mismatches != (p.WalletLotsRowMismatches{}) || out.Sequence != (p.WalletSequenceRowMismatches{})
	consistent := !mm && out.Unproven == (p.WalletLotsRowUnproven{})
	status := "consistent"
	if mm {
		status = "mismatch"
	} else if !consistent {
		status = "indeterminate"
	}
	if out.RowsConsistent != consistent || out.Status != status {
		t.Fatalf("inconsistent status: %+v", out)
	}
	return out
}
func wlValid(t *testing.T, in p.WalletLotsRows) {
	t.Helper()
	if out := wlRun(t, in); !out.RowsConsistent {
		t.Fatalf("unexpected mismatch: %+v", out)
	}
}

func TestWalletLotsRowsHandCalculatedReplay(t *testing.T) {
	t.Run("empty_database_internal_epoch_only", func(t *testing.T) { wlValid(t, wlRawBase("ordinary")) })
	t.Run("three_lots_partial_charge_two_refunds", func(t *testing.T) {
		in := wlThree()
		wlValid(t, in)
		if len(in.Events) != 12 || len(in.ReservationLots) != 3 || len(in.SettlementLots) != 3 || len(in.RefundLots) != 3 {
			t.Fatal("hand fixture graph shape changed")
		}
		for n, want := range []p.WalletLotsBalanceRow{{AvailableAmount: "15", InferenceHeldAmount: "0", ConsumedAmount: "25", Revision: "4"}, {AvailableAmount: "50", InferenceHeldAmount: "0", ConsumedAmount: "0", Revision: "5"}, {AvailableAmount: "60", InferenceHeldAmount: "0", ConsumedAmount: "0", Revision: "3"}} {
			got := in.Balances[n]
			if got.AvailableAmount != want.AvailableAmount || got.InferenceHeldAmount != want.InferenceHeldAmount || got.ConsumedAmount != want.ConsumedAmount || got.Revision != want.Revision {
				t.Fatal("hand-calculated final buckets changed")
			}
		}
	})
	t.Run("split_refund_equals_one_total", func(t *testing.T) {
		one, split := wlThreeTenant("tenant", 0, false), wlThree()
		wlValid(t, one)
		wlValid(t, split)
		for n := range one.Balances {
			a, b := one.Balances[n], split.Balances[n]
			if a.AvailableAmount != b.AvailableAmount || a.InferenceHeldAmount != b.InferenceHeldAmount || a.ConsumedAmount != b.ConsumedAmount {
				t.Fatal("split refund does not restore same original lots")
			}
		}
	})
	t.Run("legacy_cutover_held_and_old_pending_refunds", func(t *testing.T) { wlValid(t, wlLegacy()) })
	t.Run("pending_refund_has_no_effect", func(t *testing.T) {
		in := wlThree()
		in.Refunds = append(in.Refunds, p.WalletLotsRefundRow{ID: "tenant/pending", TenantID: "tenant", SettlementID: "tenant/settlement", Amount: "10", State: "pending", Requester: "maker"})
		wlValid(t, in)
	})
	t.Run("same_top_xid_has_distinct_ops", func(t *testing.T) {
		in := wlThree()
		if in.Operations[6].CreatedXID != in.Operations[7].CreatedXID {
			t.Fatal("fixture no longer exercises same top xid")
		}
		wlValid(t, in)
	})
	t.Run("unordered_physical_inputs", func(t *testing.T) {
		in := wlLegacy()
		want := wlRun(t, in)
		for seed := int64(1); seed <= 20; seed++ {
			wlShuffle(&in, seed)
			if got := wlRun(t, in); got != want {
				t.Fatalf("order affected result: %+v", got)
			}
		}
	})
}

func TestWalletLotsRowsZeroAmountOperationsAndOpening(t *testing.T) {
	t.Run("zero_available_opening_account_can_exist_later", func(t *testing.T) {
		b := wlNew("old-zero", 0, false, "ordinary")
		b.in.LegacyOpenings = []p.WalletLotsLegacyOpeningRow{{ID: b.id(700), EpochID: wlID(1), TenantID: b.tenant, Kind: "available", Amount: "0", ObservedAt: wlTime(0)}}
		b.in.Epochs[0].TenantCount = "1"
		b.in.Epochs[0].OpeningCount = "1"
		wlValid(t, b.in)
	})
	t.Run("reserve_and_release_zero_still_have_journals_and_seq", func(t *testing.T) {
		b := wlNew("zero", 0, true, "ordinary")
		b.in.Requests = []p.WalletLotsRequestRow{{ID: b.s("request"), TenantID: b.tenant, OwnerID: "customer", BillingStatus: "released"}}
		b.in.Reservations = []p.WalletLotsReservationRow{{ID: b.s("reserve"), TenantID: b.tenant, RequestID: b.s("request"), Amount: "0", Status: "released"}}
		j := b.journal("reserve-journal", b.s("reserve"), "reserve", b.s("request"), "customer", "202", wlTime(1), wlLine{"AVAILABLE", "0"}, wlLine{"HELD", "0"})
		b.op("reserve", b.s("reserve"), wsPtr(j), wsPtr(b.s("reserve")), "202", "0", [4]string{"0", "0", "0", "0"}, wlTime(1))
		j = b.journal("finish-journal", b.s("settlement"), "released", b.s("request"), "system", "203", wlTime(2), wlLine{"AVAILABLE", "0"}, wlLine{"HELD", "0"})
		b.in.Settlements = []p.WalletLotsSettlementRow{{ID: b.s("settlement"), TenantID: b.tenant, RequestID: b.s("request"), ChargedAmount: "0", RefundedAmount: "0", Status: "released", Reason: "released", JournalID: j}}
		b.op("finish", b.s("settlement"), wsPtr(j), wsPtr(b.s("settlement")), "203", "0", [4]string{"0", "0", "0", "0"}, wlTime(2))
		wlValid(t, b.in)
	})
}

func TestWalletLotsRowsBigIntegerPrincipalAcrossSeparateBuckets(t *testing.T) {
	const max = "9223372036854775807"
	b := wlNew("large", 0, true, "ordinary")
	b.funding(0, "manual_credit", "credit", "credit", max, "FUNDING_CLEARING", "nonrefundable")
	b.in.Requests = []p.WalletLotsRequestRow{{ID: b.s("request"), TenantID: b.tenant, OwnerID: "customer", BillingStatus: "reserved"}}
	b.in.Reservations = []p.WalletLotsReservationRow{{ID: b.s("reserve"), TenantID: b.tenant, RequestID: b.s("request"), Amount: max, Status: "reserved"}}
	j := b.journal("reserve-journal", b.s("reserve"), "reserve", b.s("request"), "customer", "202", wlTime(2), wlLine{"AVAILABLE", "-" + max}, wlLine{"HELD", max})
	op := b.op("reserve", b.s("reserve"), wsPtr(j), wsPtr(b.s("reserve")), "202", "1", [4]string{"-" + max, max, "0", "0"}, wlTime(2))
	b.in.ReservationLots = []p.WalletLotsReservationLotRow{{ID: b.id(10000), TenantID: b.tenant, ReservationID: b.s("reserve"), LotID: b.in.Lots[0].ID, AllocationIndex: "1", Amount: max, CreatedXID: "202", CreatedAt: wlTime(2)}}
	b.event(op, 0, 1, [3]string{max, "0", "0"}, [3]string{"0", max, "0"}, [4]string{"-" + max, max, "0", "0"})
	b.funding(1, "voucher_grant", "voucher", "redemption", max, "OPERATING_GRANT_CLEARING", "nonrefundable")
	b.cache(0, "0", max, "0", "2")
	b.cache(1, max, "0", "0", "1")
	b.balance("AVAILABLE", max)
	b.balance("HELD", max)
	b.balance("FUNDING_CLEARING", "-"+max)
	b.balance("OPERATING_GRANT_CLEARING", "-"+max)
	// A+H and issued principal are 18446744073709551614. Every individual
	// account/lot/amount remains legal BIGINT; summing them as int64 would wrap.
	wlValid(t, b.in)
}

func TestWalletLotsRowsFIFOUsesExactCOrderAtSameTime(t *testing.T) {
	in := wlThree()
	for n := 0; n < 3; n++ {
		in.Receipts[n].CreatedAt = wlTime(1)
		in.Lots[n].FIFOAt = wlTime(1)
		in.Lots[n].CreatedAt = wlTime(1)
		in.Operations[n+1].CreatedAt = wlTime(1)
		in.Events[n].CreatedAt = wlTime(1)
		in.Journals[n].CreatedAt = wlTime(1)
	}
	wlValid(t, in)
	in.ReservationLots[0].AllocationIndex = "2"
	in.ReservationLots[1].AllocationIndex = "1"
	if got := wlRun(t, in); got.RowsConsistent || got.Mismatches.FIFOAllocationRows == 0 {
		t.Fatalf("same-time opaque UUID C order ignored: %+v", got)
	}
}

func TestWalletLotsRowsDirectedDamage(t *testing.T) {
	cases := []struct {
		name   string
		legacy bool
		change func(*p.WalletLotsRows)
	}{
		{"fifo_same_total_wrong_order", false, func(in *p.WalletLotsRows) {
			in.ReservationLots[0].AllocationIndex = "2"
			in.ReservationLots[1].AllocationIndex = "1"
		}},
		{"fifo_wrong_lot", false, func(in *p.WalletLotsRows) { in.ReservationLots[0].LotID = in.Lots[2].ID }},
		{"reservation_amount", false, func(in *p.WalletLotsRows) { in.ReservationLots[0].Amount = "39" }},
		{"missing_charge_zero_terminal", false, func(in *p.WalletLotsRows) { in.SettlementLots = in.SettlementLots[:2] }},
		{"charge_distribution_same_total", false, func(in *p.WalletLotsRows) {
			in.SettlementLots[0].ChargedAmount = "39"
			in.SettlementLots[0].ReleasedAmount = "1"
			in.SettlementLots[1].ChargedAmount = "36"
			in.SettlementLots[1].ReleasedAmount = "14"
		}},
		{"refund_wrong_original_edge", false, func(in *p.WalletLotsRows) {
			in.RefundLots[0].SettlementLotID = wsPtr(in.SettlementLots[0].ID)
			in.RefundLots[0].LotID = in.Lots[0].ID
		}},
		{"refund_reverse_order", false, func(in *p.WalletLotsRows) {
			in.RefundLots[1].AllocationIndex = "2"
			in.RefundLots[2].AllocationIndex = "1"
		}},
		{"refund_amount", false, func(in *p.WalletLotsRows) { in.RefundLots[0].Amount = "19" }},
		{"wrong_source_eligibility_class", false, func(in *p.WalletLotsRows) { in.Lots[0].RefundClass = "sandbox_original" }},
		{"wrong_source_principal", false, func(in *p.WalletLotsRows) { in.Lots[0].IssuedAmount = "41" }},
		{"wrong_source_fifo_time", false, func(in *p.WalletLotsRows) { in.Lots[0].FIFOAt = wlTime(50) }},
		{"wrong_receipt_identity_namespace", false, func(in *p.WalletLotsRows) { in.Operations[1].SourceID = in.Receipts[0].SourceID }},
		{"multiple_origin_columns", false, func(in *p.WalletLotsRows) { in.Lots[0].LegacyRefundOrderID = wsPtr("unrelated") }},
		{"event_before", false, func(in *p.WalletLotsRows) { in.Events[3].BeforeAvailable = "41" }},
		{"event_after", false, func(in *p.WalletLotsRows) { in.Events[3].AfterHeld = "39" }},
		{"event_prev", false, func(in *p.WalletLotsRows) { in.Events[3].PreviousEventID = wsPtr(in.Events[1].ID) }},
		{"event_revision", false, func(in *p.WalletLotsRows) { in.Events[3].AfterRevision = "3" }},
		{"event_xid", false, func(in *p.WalletLotsRows) { in.Events[3].CreatedXID = "999" }},
		{"cache_amount", false, func(in *p.WalletLotsRows) { in.Balances[0].AvailableAmount = "16" }},
		{"cache_tail", false, func(in *p.WalletLotsRows) { in.Balances[0].LastEventID = in.Events[0].ID }},
		{"missing_cache", false, func(in *p.WalletLotsRows) { in.Balances = in.Balances[1:] }},
		{"missing_middle_seq", false, func(in *p.WalletLotsRows) { in.Operations = append(in.Operations[:2], in.Operations[3:]...) }},
		{"unadvanced_head", false, func(in *p.WalletLotsRows) {
			in.Heads[0].TailSeq = "6"
			in.Heads[0].TailOperationID = in.Operations[6].ID
		}},
		{"forked_previous", false, func(in *p.WalletLotsRows) { in.Operations[3].PreviousID = wsPtr(in.Operations[1].ID) }},
		{"reserve_journal_owner", false, func(in *p.WalletLotsRows) { in.Journals[3].Actor = "other" }},
		{"finish_journal_reference", false, func(in *p.WalletLotsRows) { in.Journals[4].Reference = "other-request" }},
		{"refund_self_approval", false, func(in *p.WalletLotsRows) { in.Refunds[0].Approver = wsPtr(in.Refunds[0].Requester) }},
		{"journal_line_amount", false, func(in *p.WalletLotsRows) { in.Lines[6].SignedDelta = "-119"; in.Lines[7].SignedDelta = "119" }},
		{"journal_line_cross_tenant", false, func(in *p.WalletLotsRows) { in.Accounts[0].TenantID = wsPtr("another") }},
		{"refund_current_cumulative", false, func(in *p.WalletLotsRows) { in.Settlements[0].RefundedAmount = "49" }},
		{"pending_refund_cannot_have_existing_effect", false, func(in *p.WalletLotsRows) { in.Refunds[0].State = "pending"; in.Refunds[0].Approver = nil }},
		{"epoch_attestation_digest", false, func(in *p.WalletLotsRows) {
			in.Attestations[0].ProofCanonical = append(in.Attestations[0].ProofCanonical, 'x')
		}},
		{"epoch_mode_pair", false, func(in *p.WalletLotsRows) { in.Attestations[0].WorkerRoleOID = nil }},
		{"epoch_role_overlap", false, func(in *p.WalletLotsRows) { in.Attestations[0].AppRoleOID = in.Attestations[0].SchemaOwnerOID }},
		{"missing_epoch", false, func(in *p.WalletLotsRows) { in.Epochs = nil }},
		{"missing_attestation", false, func(in *p.WalletLotsRows) { in.Attestations = nil }},
		{"legacy_f0", true, func(in *p.WalletLotsRows) { in.LegacySettlements[0].RefundedAmountAtCutover = "19" }},
		{"legacy_membership_missing_receipt", true, func(in *p.WalletLotsRows) { in.LegacyReceipts = nil }},
		{"legacy_available_not_journal_prefix", true, func(in *p.WalletLotsRows) { in.LegacyOpenings[0].Amount = "91" }},
		{"legacy_held_set_uses_current_terminal_wrongly", true, func(in *p.WalletLotsRows) { in.LegacyOpenings = in.LegacyOpenings[:1] }},
		{"legacy_count", true, func(in *p.WalletLotsRows) { in.Epochs[0].OpeningCount = "1" }},
		{"legacy_refund_cannot_be_original_sandbox", true, func(in *p.WalletLotsRows) { in.Lots[2].RefundClass = "sandbox_original" }},
		{"legacy_refund_fifo_uses_epoch_wrongly", true, func(in *p.WalletLotsRows) { in.Lots[2].FIFOAt = in.Epochs[0].CreatedAt }},
		{"legacy_new_refund_cannot_bind_old_approved", true, func(in *p.WalletLotsRows) { in.Lots[2].LegacyRefundOrderID = wsPtr(in.Refunds[0].ID) }},
		{"legacy_opening_wrong_account_kind", true, func(in *p.WalletLotsRows) { in.LegacyOpenings[0].AccountID = in.LegacyOpenings[1].AccountID }},
		{"legacy_source_roots", true, func(in *p.WalletLotsRows) { in.LegacySources[1].ReservationID = wsPtr("wrong-reservation") }},
		{"legacy_wrong_epoch", true, func(in *p.WalletLotsRows) { in.LegacySources[0].EpochID = wlID(2) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := wlThree()
			if tc.legacy {
				in = wlLegacy()
			}
			tc.change(&in)
			got := wlRun(t, in)
			if got.RowsConsistent || got.Status != "mismatch" {
				t.Fatalf("damaged supplied graph accepted: %+v", got)
			}
			wlShuffle(&in, 19)
			if again := wlRun(t, in); again != got {
				t.Fatalf("permutation changes damaged result:\n%+v\n%+v", got, again)
			}
		})
	}
}

func TestWalletLotsRowsDeclarationsCannotSelfAuthenticateAmounts(t *testing.T) {
	in := wlThree()
	// All WS declarations now agree on a 13-unit issuance during reserve. The
	// true reservation remains 120; this full replay must reject the forgery.
	in.Operations[4].AvailableDelta = "-107"
	in.Operations[4].IssuedDelta = "13"
	in.Events[3].AvailableDelta = "-27"
	in.Events[3].IssuedDelta = "13"
	in.Events[3].AfterAvailable = "13"
	if got := wlRun(t, in); got.RowsConsistent || got.Mismatches.EventChainRows == 0 {
		t.Fatalf("consistent forged statements replaced true source: %+v", got)
	}
	in = wlThree()
	// A forged event/cache pair must not become the initial state for later ops.
	in.Events[0].AfterAvailable = "999"
	in.Events[0].AvailableDelta = "999"
	in.Events[0].IssuedDelta = "999"
	in.Operations[1].AvailableDelta = "999"
	in.Operations[1].IssuedDelta = "999"
	in.Balances[0].AvailableAmount = "974"
	got := wlRun(t, in)
	if got.RowsConsistent || got.Mismatches.EventChainRows == 0 || got.Mismatches.BalanceRows == 0 || got.Unproven.ReplayPrefixUnproven != 0 {
		t.Fatalf("bad declarations should be compared while true source replay continues: %+v", got)
	}
}

func TestWalletLotsRowsQualificationIsDataMismatch(t *testing.T) {
	for name, change := range map[string]func(*p.WalletLotsRows){
		"bad_utf8":             func(in *p.WalletLotsRows) { in.Receipts[0].SourceID = string([]byte{0xff}) },
		"nul_text":             func(in *p.WalletLotsRows) { in.Refunds[0].Requester = "maker\x00secret" },
		"bad_uuid":             func(in *p.WalletLotsRows) { in.Lots[0].ID = "not-new-uuid" },
		"nil_uuid":             func(in *p.WalletLotsRows) { in.Events[0].ID = "00000000-0000-0000-0000-000000000000" },
		"negative_amount":      func(in *p.WalletLotsRows) { in.Receipts[0].Amount = "-40" },
		"noncanonical_bigint":  func(in *p.WalletLotsRows) { in.Reservations[0].Amount = "0120" },
		"bigint_overflow":      func(in *p.WalletLotsRows) { in.Lots[0].IssuedAmount = "9223372036854775808" },
		"fractional_delta":     func(in *p.WalletLotsRows) { in.Events[0].IssuedDelta = "40.1" },
		"exponent_delta":       func(in *p.WalletLotsRows) { in.Events[0].AvailableDelta = "4e1" },
		"nan_delta":            func(in *p.WalletLotsRows) { in.Operations[1].IssuedDelta = "NaN" },
		"negative_xid":         func(in *p.WalletLotsRows) { in.Events[0].CreatedXID = "-1" },
		"oid_out_of_range":     func(in *p.WalletLotsRows) { in.Epochs[0].DatabaseOID = "4294967296" },
		"unknown_origin":       func(in *p.WalletLotsRows) { in.Lots[0].OriginKind = "self-funded" },
		"unknown_refund_state": func(in *p.WalletLotsRows) { in.Refunds[0].State = "cancelled" },
		"released_has_positive_charge": func(in *p.WalletLotsRows) {
			in.Settlements[0].Status = "released"
			in.Reservations[0].Status = "released"
			in.Requests[0].BillingStatus = "released"
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := wlThree()
			change(&in)
			out := wlRun(t, in)
			if out.Status != "mismatch" || out.RowsConsistent {
				t.Fatalf("malformed raw data was not a normal mismatch: %+v", out)
			}
		})
	}
}

func TestWalletLotsRowsDuplicateAndOrphanFacts(t *testing.T) {
	for name, duplicate := range map[string]func(*p.WalletLotsRows){
		"origin": func(in *p.WalletLotsRows) { r := in.Lots[0]; r.IssuedAmount = "999"; in.Lots = append(in.Lots, r) },
		"event": func(in *p.WalletLotsRows) {
			r := in.Events[0]
			r.AfterAvailable = "999"
			in.Events = append(in.Events, r)
		},
		"allocation": func(in *p.WalletLotsRows) {
			r := in.ReservationLots[0]
			r.Amount = "999"
			in.ReservationLots = append(in.ReservationLots, r)
		},
		"source": func(in *p.WalletLotsRows) {
			r := in.Receipts[0]
			r.Amount = "999"
			in.Receipts = append(in.Receipts, r)
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := wlThree()
			duplicate(&in)
			want := wlRun(t, in)
			if want.RowsConsistent {
				t.Fatal("conflicting duplicates accepted")
			}
			for seed := int64(0); seed < 10; seed++ {
				wlShuffle(&in, seed)
				if got := wlRun(t, in); got != want {
					t.Fatal("duplicate first-row oracle")
				}
			}
		})
	}
	t.Run("truly_dangling_event", func(t *testing.T) {
		in := wlThree()
		r := in.Events[0]
		r.ID = wlID(99001)
		r.WalletOperationID = wlID(99002)
		in.Events = append(in.Events, r)
		if got := wlRun(t, in); got.Mismatches.OrphanFacts == 0 {
			t.Fatalf("provably dangling fact not reported: %+v", got)
		}
	})
	t.Run("unknown_wallet_journal_zero_lines", func(t *testing.T) {
		in := wlThree()
		in.Journals = append(in.Journals, p.WalletLotsJournalRow{WalletSequenceJournalRow: p.WalletSequenceJournalRow{ID: "tenant/unknown-wallet-journal", TenantID: "tenant", OperationID: "unowned-wallet-operation", Kind: "reserve", Currency: "CNY", CreatedTXID: "999"}, Reference: "unknown-request", Actor: "customer", CreatedAt: wlTime(20)})
		if got := wlRun(t, in); got.RowsConsistent || got.Mismatches.WalletSourceRows == 0 {
			t.Fatalf("zero-line orphan wallet journal ignored: %+v", got)
		}
	})
	t.Run("non_wallet_journal_not_classified_as_orphan", func(t *testing.T) {
		in := wlThree()
		in.Journals = append(in.Journals, p.WalletLotsJournalRow{WalletSequenceJournalRow: p.WalletSequenceJournalRow{ID: "tenant/procurement-note", TenantID: "tenant", OperationID: "cost-note", Kind: "provider_cost", Currency: "CNY", CreatedTXID: "999"}, Reference: "attempt", Actor: "finance", CreatedAt: wlTime(20)})
		wlValid(t, in)
	})
}

func TestWalletLotsRowsBadPrefixDoesNotInventSuffixState(t *testing.T) {
	in := wlThree()
	in.Receipts[0].Amount = "not-an-amount"
	got := wlRun(t, in)
	if got.Status != "mismatch" || got.Unproven.ReplayPrefixUnproven != 1 {
		t.Fatalf("invalid source must stop one tenant replay: %+v", got)
	}
	if got.Mismatches.OrphanFacts != 0 {
		t.Fatalf("already identified suffix facts are deferred, not fabricated orphans: %+v", got)
	}
	// Its subsequent event/cache claims remain superficially plausible. They
	// cannot repair the missing trusted prefix, and every supplied row is counted.
	for n := range in.Events {
		if in.Events[n].WalletSeq != "1" {
			in.Events[n].BeforeAvailable = "777"
		}
	}
	want := wlRun(t, in)
	if want.Unproven.ReplayPrefixUnproven != 1 {
		t.Fatal("suffix before/cache revived a trusted prefix")
	}
	wlShuffle(&in, 99)
	if again := wlRun(t, in); again != want {
		t.Fatal("bad prefix depends on input order")
	}
}

func TestWalletLotsRowsMissingEventDoesNotReplaceTrueNumericState(t *testing.T) {
	in := wlThree()
	in.Events = in.Events[1:]
	// Missing an event locator is already a definite defect. The source amount
	// is still 40, so later before/after/cache values must not supply a new
	// trusted opening or prevent independent final-bucket comparison.
	in.Balances[0].AvailableAmount = "999"
	out := wlRun(t, in)
	if out.Mismatches.MissingFacts == 0 || out.Mismatches.BalanceRows == 0 || out.Unproven.ReplayPrefixUnproven != 0 {
		t.Fatalf("missing event must not destroy independent source arithmetic: %+v", out)
	}
}

func TestWalletLotsRowsInvalidFutureSourceKeepsEarlierPrefixChecks(t *testing.T) {
	in := wlThree()
	// The third funding is unreadable. The first funding remains a uniquely
	// reconstructible earlier prefix whose false event bucket must be compared.
	in.Receipts[2].Amount = "unknown"
	in.Events[0].AfterAvailable = "41"
	out := wlRun(t, in)
	if out.Unproven.ReplayPrefixUnproven != 1 || out.Mismatches.EventChainRows == 0 {
		t.Fatalf("a future bad source skipped the earlier independently known prefix: %+v", out)
	}
}

func wlTwoTenants() p.WalletLotsRows {
	a, b := wlThreeTenant("a", 0, true), wlThreeTenant("b", 100000, true)
	av, bv := reflect.ValueOf(&a).Elem(), reflect.ValueOf(b)
	for n := 0; n < av.NumField(); n++ {
		name := av.Type().Field(n).Name
		if name == "Epochs" || name == "Attestations" || name == "Accounts" {
			continue
		}
		av.Field(n).Set(reflect.AppendSlice(av.Field(n), bv.Field(n)))
	}
	a.Accounts = append(a.Accounts, b.Accounts[:2]...)
	for n := range a.Accounts {
		switch a.Accounts[n].Kind {
		case "FUNDING_CLEARING":
			a.Accounts[n].Balance = "-80"
		case "OPERATING_GRANT_CLEARING":
			a.Accounts[n].Balance = "-100"
		case "SANDBOX_FUNDING_CLEARING":
			a.Accounts[n].Balance = "-120"
		case "REVENUE":
			a.Accounts[n].Balance = "50"
		}
	}
	return a
}
func TestWalletLotsRowsTenantIsolationAndIndependentContinuation(t *testing.T) {
	in := wlTwoTenants()
	wlValid(t, in)
	in.Receipts[0].Amount = "bad"
	baseline := wlRun(t, in)
	if baseline.Unproven.ReplayPrefixUnproven != 1 {
		t.Fatalf("bad tenant must not invalidate healthy tenant: %+v", baseline)
	}
	for n := range in.Balances {
		if in.Balances[n].TenantID == "b" && in.Balances[n].AvailableAmount == "15" {
			in.Balances[n].AvailableAmount = "16"
			break
		}
	}
	got := wlRun(t, in)
	if got.Unproven.ReplayPrefixUnproven != 1 || got.Mismatches.BalanceRows <= baseline.Mismatches.BalanceRows {
		t.Fatalf("healthy tenant replay was skipped: %+v", got)
	}
	in = wlTwoTenants()
	in.ReservationLots[0].LotID = in.Lots[3].ID
	if got := wlRun(t, in); got.RowsConsistent {
		t.Fatal("cross-tenant allocation accepted")
	}
}

func wlMapTimes(in *p.WalletLotsRows, fn func(p.WalletLotsTime) p.WalletLotsTime) {
	typ := reflect.TypeOf(p.WalletLotsTime{})
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if v.Type() == typ {
			v.Set(reflect.ValueOf(fn(v.Interface().(p.WalletLotsTime))))
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for n := 0; n < v.NumField(); n++ {
				walk(v.Field(n))
			}
		case reflect.Slice:
			for n := 0; n < v.Len(); n++ {
				walk(v.Index(n))
			}
		}
	}
	walk(reflect.ValueOf(in).Elem())
}
func TestWalletLotsRowsNativeTimeAndClockIndependence(t *testing.T) {
	for _, year := range []int{-4712, 1, 10000, 294276} {
		t.Run(fmt.Sprint(year), func(t *testing.T) {
			in := wlThree()
			wlMapTimes(&in, func(v p.WalletLotsTime) p.WalletLotsTime {
				if v.Valid {
					old := v.Value
					v.Value = time.Date(year, old.Month(), old.Day(), old.Hour(), old.Minute(), old.Second(), old.Nanosecond(), time.UTC)
				}
				return v
			})
			wlValid(t, in)
		})
	}
	t.Run("finite_go_zero_is_not_null", func(t *testing.T) {
		in := wlRawBase("ordinary")
		in.Epochs[0].CreatedAt = p.WalletLotsTime{Value: time.Time{}, Valid: true}
		in.Attestations[0].CreatedAt = in.Epochs[0].CreatedAt
		wlValid(t, in)
	})
	t.Run("clock_can_regress_without_reordering_wallet_ops", func(t *testing.T) {
		in := wlThree()
		in.Operations[4].CreatedAt = wlTime(-30)
		for n := range in.Events {
			if in.Events[n].WalletOperationID == in.Operations[4].ID {
				in.Events[n].CreatedAt = wlTime(-30)
			}
		}
		for n := range in.ReservationLots {
			in.ReservationLots[n].CreatedAt = wlTime(-30)
		}
		wlValid(t, in)
	})
	for name, at := range map[string]p.WalletLotsTime{"null": {}, "infinity": {Valid: true, Infinity: 1}, "minus_infinity": {Valid: true, Infinity: -1}, "non_microsecond": {Valid: true, Value: time.Date(2026, 11, 2, 12, 0, 0, 1, time.UTC)}} {
		t.Run(name, func(t *testing.T) {
			in := wlThree()
			in.Receipts[0].CreatedAt = at
			if got := wlRun(t, in); got.Mismatches.InvalidTimeRows == 0 {
				t.Fatalf("bad native time accepted: %+v", got)
			}
		})
	}
}

func TestWalletLotsRowsResultIsRedactedAndHasNoAuthority(t *testing.T) {
	in := wlThree()
	out := wlRun(t, in)
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{wlID(1), "tenant/receipt-0", "tenant/credit-one", "opaque supplied attestation", `"coverage_complete"`, `"balanced"`, `"proof"`} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("result leaks data/authority %q: %s", secret, data)
		}
	}
	// Changing receipt and all financial facts could create another internally
	// valid supplied history. This pure function cannot authenticate the database
	// or prove that an entire independent tenant was not omitted by a caller.
	wlValid(t, wlRawBase("ordinary"))
}

type wlInterruptContext struct {
	context.Context
	calls atomic.Int32
	after int32
	cause error
}

func (c *wlInterruptContext) Err() error {
	if c.calls.Add(1) >= c.after {
		return c.cause
	}
	return c.Context.Err()
}
func TestWalletLotsRowsClosedErrorsAndBudgets(t *testing.T) {
	zero := p.WalletLotsRowsCheck{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	timeout, cancelTimeout := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancelTimeout()
	tests := []struct {
		name string
		ctx  context.Context
		in   p.WalletLotsRows
		want error
	}{{"nil", nil, wlThree(), p.ErrWalletLotsRowsInput}, {"canceled", canceled, wlThree(), p.ErrWalletLotsRowsCanceled}, {"timeout", timeout, wlThree(), p.ErrWalletLotsRowsTimeout}}
	long := wlThree()
	long.Receipts[0].SourceID = strings.Repeat("secret", 10923)
	tests = append(tests, struct {
		name string
		ctx  context.Context
		in   p.WalletLotsRows
		want error
	}{"long_string", context.Background(), long, p.ErrWalletLotsRowsResource})
	long = wlThree()
	long.Refunds[0].Approver = wsPtr(strings.Repeat("p", 65537))
	tests = append(tests, struct {
		name string
		ctx  context.Context
		in   p.WalletLotsRows
		want error
	}{"long_pointer_string", context.Background(), long, p.ErrWalletLotsRowsResource})
	long = wlRawBase("ordinary")
	long.Attestations[0].ProofCanonical = make([]byte, 262145)
	tests = append(tests, struct {
		name string
		ctx  context.Context
		in   p.WalletLotsRows
		want error
	}{"proof_bytes", context.Background(), long, p.ErrWalletLotsRowsResource})
	long = p.WalletLotsRows{Lots: make([]p.WalletLotsLotRow, 50001), Events: make([]p.WalletLotsEventRow, 50000)}
	tests = append(tests, struct {
		name string
		ctx  context.Context
		in   p.WalletLotsRows
		want error
	}{"global_rows", context.Background(), long, p.ErrWalletLotsRowsResource})
	long = p.WalletLotsRows{Receipts: make([]p.WalletLotsReceiptRow, 257)}
	for n := range long.Receipts {
		long.Receipts[n].SourceID = strings.Repeat("b", 65536)
	}
	tests = append(tests, struct {
		name string
		ctx  context.Context
		in   p.WalletLotsRows
		want error
	}{"global_bytes", context.Background(), long, p.ErrWalletLotsRowsResource})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := p.CheckWalletLotsRows(tc.ctx, tc.in)
			if err != tc.want || out != zero {
				t.Fatalf("wanted zero result/%v, got %+v %v", tc.want, out, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("error contains raw data")
			}
		})
	}
	t.Run("proof_exact_262144_bytes_can_be_opaque", func(t *testing.T) {
		in := wlRawBase("ordinary")
		in.Attestations[0].ProofCanonical = make([]byte, 262144)
		in.Attestations[0].ProofSHA256 = wlSHA(in.Attestations[0].ProofCanonical)
		in.Epochs[0].RuntimeProofDigest = in.Attestations[0].ProofSHA256
		wlValid(t, in)
	})
	t.Run("one_hundred_thousand_rows_not_silently_truncated", func(t *testing.T) {
		in := p.WalletLotsRows{RefundLots: make([]p.WalletLotsRefundLotRow, 100000)}
		out, err := p.CheckWalletLotsRows(context.Background(), in)
		if err != nil || out.Scanned.RefundLots != 100000 || out.RowsConsistent {
			t.Fatalf("at-limit data must be enumerated, got %+v %v", out, err)
		}
	})
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		want := p.ErrWalletLotsRowsCanceled
		if errors.Is(cause, context.DeadlineExceeded) {
			want = p.ErrWalletLotsRowsTimeout
		}
		for _, after := range []int32{2, 20, 200} {
			t.Run(fmt.Sprintf("mid_work_%v_%d", cause, after), func(t *testing.T) {
				ctx := &wlInterruptContext{Context: context.Background(), after: after, cause: cause}
				in := p.WalletLotsRows{RefundLots: make([]p.WalletLotsRefundLotRow, 40000)}
				out, err := p.CheckWalletLotsRows(ctx, in)
				if err != want || out != zero {
					t.Fatalf("mid-work failure must discard all counters, got %+v %v checks=%d", out, err, ctx.calls.Load())
				}
			})
		}
	}
}
