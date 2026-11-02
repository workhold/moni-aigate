package fundinglotverify

import "math/big"

// Local row constraints remain decidable for deferred suffixes. They qualify
// supplied statements and never initialize expected numeric state.
func (w *wlWork) qualifyShape(row any, ref wlRef) {
	valid := true
	positive := func(s string) {
		if _, ok := w.amount(s, true, ref); !ok {
			valid = false
		}
	}
	switch r := row.(type) {
	case WalletLotsReceiptRow:
		positive(r.Amount)
		valid = valid && wlWalletSourceKind(r.SourceKind) && r.Currency == "CNY"
	case WalletLotsLegacyReceiptRow:
		positive(r.Amount)
		valid = valid && wlWalletSourceKind(r.SourceKind)
	case WalletLotsLegacySandboxReceiptRow:
		positive(r.Amount)
	case WalletLotsLegacyApprovedRefundRow:
		positive(r.Amount)
	case WalletLotsRequestRow:
		valid = wlWalletState(r.BillingStatus)
	case WalletLotsReservationRow:
		valid = wlWalletState(r.Status)
	case WalletLotsSettlementRow:
		valid = r.Status == "settled" || r.Status == "released"
	case WalletLotsRefundRow:
		positive(r.Amount)
		valid = valid && (r.State == "pending" || r.State == "approved")
	case WalletLotsLegacySourceRow:
		valid = wsLegacyKind(r.Kind)
	case WalletLotsLegacyOpeningRow:
		valid = (r.Kind == "available" || r.Kind == "reservation") && (r.Kind == "reservation") == (r.ReservationID != nil)
		valid = valid && (r.AccountID != nil || r.Kind == "available" && r.Amount == "0")
	case WalletLotsLotRow:
		positive(r.IssuedAmount)
		shape := false
		switch r.OriginKind {
		case "funding_receipt":
			shape = r.FundingReceiptID != nil && r.LegacyOpeningID == nil && r.LegacyRefundOrderID == nil
		case "legacy_available", "legacy_reservation":
			shape = r.FundingReceiptID == nil && r.LegacyOpeningID != nil && r.LegacyRefundOrderID == nil
		case "legacy_consumption_refund":
			shape = r.FundingReceiptID == nil && r.LegacyOpeningID == nil && r.LegacyRefundOrderID != nil
		}
		valid = valid && shape && r.Currency == "CNY" && (r.RefundClass == "nonrefundable" || r.RefundClass == "sandbox_original")
	case WalletLotsEventRow:
		positive(r.AfterRevision)
		before, bok := wsUint(r.BeforeRevision, false, true)
		after, aok := wsUint(r.AfterRevision, true, true)
		valid = valid && bok && aok && after == before+1 && (before == 0) == (r.PreviousEventID == nil) && wsEventKind(r.Kind)
		valid = valid && (r.JournalID == nil) == (r.OperationID == nil) && (r.Kind == "legacy_opening") == (r.JournalID == nil && r.OperationID == nil)
		for _, triple := range [][3]string{{r.BeforeAvailable, r.AvailableDelta, r.AfterAvailable}, {r.BeforeHeld, r.HeldDelta, r.AfterHeld}, {r.BeforeConsumed, r.ConsumedDelta, r.AfterConsumed}} {
			a, aok := w.amount(triple[0], false, ref)
			d, dok := w.integer(triple[1], ref)
			b, bok := w.amount(triple[2], false, ref)
			if aok && dok && bok && new(big.Int).Add(a, d).Cmp(b) != 0 {
				valid = false
			}
		}
	case WalletLotsBalanceRow:
		positive(r.Revision)
	case WalletLotsReservationLotRow:
		positive(r.Amount)
		positive(r.AllocationIndex)
	case WalletLotsRefundLotRow:
		positive(r.Amount)
		positive(r.AllocationIndex)
		valid = valid && (r.Origin == "allocated" || r.Origin == "legacy_settlement") && (r.Origin == "allocated") == (r.SettlementLotID != nil)
	}
	if !valid {
		w.mark(&w.out.Mismatches.InvalidValueRows, ref)
	}
}
