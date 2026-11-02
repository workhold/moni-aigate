package fundinglotinit

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

const idDomain = "moni-aigate/funding-lots-init/id/v1\x00"

func parseUUID(s string, out *[16]byte) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
			continue
		}
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	_, err := hex.Decode(out[:], []byte(strings.ReplaceAll(s, "-", "")))
	return err == nil
}

func (w *work) id(fields ...string) (string, error) {
	if err := w.check(); err != nil {
		return "", err
	}
	h := sha1.New()
	_, _ = h.Write(w.namespace[:])
	_, _ = h.Write([]byte(idDomain))
	var size [8]byte
	for _, s := range fields {
		if err := w.check(); err != nil {
			return "", err
		}
		binary.BigEndian.PutUint64(size[:], uint64(len(s)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(s))
	}
	raw := h.Sum(nil)
	raw[6] = raw[6]&0x0f | 0x50
	raw[8] = raw[8]&0x3f | 0x80
	text := hex.EncodeToString(raw[:16])
	id := text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
	if _, exists := w.generated[id]; exists {
		return "", ErrInput
	}
	w.generated[id] = struct{}{}
	return id, nil
}

func (w *work) generate() (Plan, error) {
	var p Plan
	var u, b, q, a, h uint64
	totalA, totalH := new(big.Int), new(big.Int)
	for _, r := range w.in.Rows.Tenants {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		t := w.tenants[r.ID]
		if t.available.Sign() > 0 {
			a++
		}
		totalA.Add(totalA, &t.available)
		totalH.Add(totalH, &t.held)
	}
	for _, r := range w.in.Rows.Reservations {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		if unfinished(r.Status) {
			u++
			if w.reservations[r.ID].amount > 0 {
				h++
			}
		}
	}
	for _, r := range w.in.Rows.Receipts {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		if r.SourceKind == "sandbox_capture" {
			b++
		}
	}
	for _, r := range w.in.Rows.Refunds {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		if r.State == "approved" {
			q++
		}
	}
	t, f, s, v := uint64(len(w.in.Rows.Tenants)), uint64(len(w.in.Rows.Receipts)), uint64(len(w.in.Rows.Settlements)), uint64(len(w.in.Rows.Reservations))
	g := 3*t + u + 2*f + b + v + 2*s + 2*q + 3*(a+h) + h + 2
	if g > rowLimit-w.inputRows {
		return Plan{}, ErrResource
	}
	p.Metadata = Metadata{EpochID: w.in.EpochID, EpochAt: w.in.EpochAt, TenantCount: t, ReceiptCount: f, SettlementCount: s, ApprovedRefundCount: q, OpeningCount: t + u, LegacySourceCount: f + v + s + q, DataRows: g - 2, GeneratedRows: g, AvailableTotal: totalA.String(), HeldTotal: totalH.String()}
	w.generated = make(map[string]struct{})
	operations := make(map[string]string, len(w.in.Rows.Tenants))
	eventCounts := make(map[string]uint64, len(w.in.Rows.Tenants))
	for _, r := range w.in.Rows.Tenants {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		id, err := w.id("epoch-operation", r.ID)
		if err != nil {
			return Plan{}, err
		}
		operations[r.ID] = id
	}
	for _, r := range w.in.Rows.Receipts {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		p.LegacyReceipts = append(p.LegacyReceipts, fundinglotverify.WalletLotsLegacyReceiptRow{ReceiptID: r.ID, EpochID: w.in.EpochID, TenantID: r.TenantID, SourceKind: r.SourceKind, SourceID: r.SourceID, Amount: r.Amount, JournalID: r.JournalID})
		if r.SourceKind == "sandbox_capture" {
			p.LegacySandboxReceipts = append(p.LegacySandboxReceipts, fundinglotverify.WalletLotsLegacySandboxReceiptRow{ReceiptID: r.ID, EpochID: w.in.EpochID, TenantID: r.TenantID, CaptureID: r.SourceID, Amount: r.Amount, JournalID: r.JournalID})
		}
	}
	for _, r := range w.in.Rows.Settlements {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		p.LegacySettlements = append(p.LegacySettlements, fundinglotverify.WalletLotsLegacySettlementRow{SettlementID: r.ID, EpochID: w.in.EpochID, TenantID: r.TenantID, ReservationID: w.settlements[r.ID].reservation.row.ID, RequestID: r.RequestID, ChargedAmount: r.ChargedAmount, RefundedAmountAtCutover: r.RefundedAmount, JournalID: r.JournalID})
	}
	for _, r := range w.in.Rows.Refunds {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		if r.State == "approved" {
			p.LegacyApprovedRefunds = append(p.LegacyApprovedRefunds, fundinglotverify.WalletLotsLegacyApprovedRefundRow{RefundOrderID: r.ID, EpochID: w.in.EpochID, TenantID: r.TenantID, SettlementID: r.SettlementID, Amount: r.Amount, JournalID: *r.JournalID})
		}
	}
	for _, r := range w.sources {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		p.LegacySources = append(p.LegacySources, fundinglotverify.WalletLotsLegacySourceRow{WalletSequenceLegacySourceRow: fundinglotverify.WalletSequenceLegacySourceRow{EpochID: w.in.EpochID, TenantID: r.tenant, Kind: r.kind, SourceID: r.id, JournalID: r.journal.row.ID, JournalOperationID: r.journal.row.OperationID, Amount: r.amount}, RequestID: r.request, ReservationID: r.reservation, SettlementID: r.settlement})
	}
	for _, r := range w.in.Rows.Tenants {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		var accountID *string
		if ac := w.accountKinds[accountKey{tenant: r.ID, kind: "AVAILABLE"}]; ac != nil {
			accountID = pointer(ac.row.ID)
		}
		positive, err := w.opening(&p, r.ID, "available", accountID, nil, w.tenants[r.ID].available.String(), operations[r.ID])
		if err != nil {
			return Plan{}, err
		}
		if positive {
			eventCounts[r.ID]++
		}
	}
	for _, r := range w.in.Rows.Reservations {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		if !unfinished(r.Status) {
			continue
		}
		ac := w.accountKinds[accountKey{tenant: r.TenantID, kind: "HELD"}]
		if ac == nil {
			return Plan{}, ErrInput
		}
		positive, err := w.opening(&p, r.TenantID, "reservation", pointer(ac.row.ID), pointer(r.ID), r.Amount, operations[r.TenantID])
		if err != nil {
			return Plan{}, err
		}
		if positive {
			eventCounts[r.TenantID]++
		}
	}
	for _, r := range w.in.Rows.Tenants {
		if err := w.check(); err != nil {
			return Plan{}, err
		}
		tenant := w.tenants[r.ID]
		issued := new(big.Int).Add(&tenant.available, &tenant.held)
		p.Operations = append(p.Operations, Operation{ID: operations[r.ID], TenantID: r.ID, EpochID: w.in.EpochID, Seq: "0", Kind: "epoch_opening", SourceID: r.ID, EventCount: strconv.FormatUint(eventCounts[r.ID], 10), AvailableDelta: tenant.available.String(), HeldDelta: tenant.held.String(), ConsumedDelta: "0", IssuedDelta: issued.String()})
		p.Heads = append(p.Heads, fundinglotverify.WalletSequenceHeadRow{TenantID: r.ID, EpochID: w.in.EpochID, TailSeq: "0", TailOperationID: operations[r.ID]})
	}
	if err := w.outputBudget(p); err != nil {
		return Plan{}, err
	}
	if err := w.sortPlan(&p); err != nil {
		return Plan{}, err
	}
	copy, err := w.owned(reflect.ValueOf(p))
	if err != nil {
		return Plan{}, err
	}
	return copy.Interface().(Plan), nil
}

func (w *work) opening(p *Plan, tenant, kind string, accountID, reservationID *string, amount, operation string) (bool, error) {
	fields := []string{"available-opening", tenant}
	if kind == "reservation" {
		fields = []string{"held-opening", tenant, *reservationID}
	}
	id, err := w.id(fields...)
	if err != nil {
		return false, err
	}
	p.LegacyOpenings = append(p.LegacyOpenings, fundinglotverify.WalletLotsLegacyOpeningRow{ID: id, EpochID: w.in.EpochID, TenantID: tenant, Kind: kind, AccountID: accountID, ReservationID: reservationID, Amount: amount, ObservedAt: w.in.EpochAt})
	if amount == "0" {
		return false, nil
	}
	lotID, err := w.id("legacy-lot", tenant, id)
	if err != nil {
		return false, err
	}
	eventID, err := w.id("legacy-event", tenant, lotID)
	if err != nil {
		return false, err
	}
	p.Lots = append(p.Lots, Lot{ID: lotID, TenantID: tenant, OriginKind: "legacy_" + kind, LegacyOpeningID: pointer(id), IssuedAmount: amount, Currency: "CNY", RefundClass: "nonrefundable", FIFOAt: w.in.EpochAt})
	available, held := "0", "0"
	if kind == "available" {
		available = amount
	} else {
		held = amount
	}
	p.Events = append(p.Events, Event{ID: eventID, TenantID: tenant, LotID: lotID, WalletOperationID: operation, WalletSeq: "0", Kind: "legacy_opening", AvailableDelta: available, HeldDelta: held, ConsumedDelta: "0", IssuedDelta: amount, BeforeRevision: "0", AfterRevision: "1", BeforeAvailable: "0", BeforeHeld: "0", BeforeConsumed: "0", AfterAvailable: available, AfterHeld: held, AfterConsumed: "0"})
	p.Balances = append(p.Balances, fundinglotverify.WalletLotsBalanceRow{LotID: lotID, TenantID: tenant, AvailableAmount: available, InferenceHeldAmount: held, ConsumedAmount: "0", Revision: "1", LastEventID: eventID})
	if kind == "reservation" {
		allocationID, err := w.id("held-allocation", tenant, *reservationID, lotID)
		if err != nil {
			return false, err
		}
		p.ReservationLots = append(p.ReservationLots, ReservationLot{ID: allocationID, TenantID: tenant, ReservationID: *reservationID, LotID: lotID, AllocationIndex: "1", Amount: amount})
	}
	return true, nil
}

func (w *work) outputBudget(p Plan) error {
	bytes := w.inputBytes
	if err := addBytes(&bytes, metadataReserve); err != nil {
		return err
	}
	v := reflect.ValueOf(p)
	for i := 1; i < v.NumField(); i++ {
		group := v.Field(i)
		for j := 0; j < group.Len(); j++ {
			if err := countValue(group.Index(j), &bytes, true); err != nil {
				return err
			}
			stamp := uint64(0)
			switch v.Type().Field(i).Name {
			case "Operations", "Events", "ReservationLots":
				stamp = 52
			case "Lots":
				stamp = 32
			}
			if err := addBytes(&bytes, stamp); err != nil {
				return err
			}
			if err := w.check(); err != nil {
				return err
			}
		}
	}
	return nil
}
