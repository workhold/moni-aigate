package fundinglotverify

// Reverse checks distinguish definitely missing/cross-tenant parents from an
// ambiguous or damaged replay prefix. A deferred suffix is never counted as a
// collection of invented orphans merely because no numeric state was built.
func (w *wlWork) walletParent(table, id, tenant string, ref wlRef, direction *uint64) bool {
	t := w.tables[table]
	refs := t.primary[wlKey{id: id}]
	if len(refs) == 0 {
		w.need(table, id)
		w.mark(direction, ref)
		w.mark(&w.out.Mismatches.OrphanFacts, ref)
		return false
	}
	if len(refs) != 1 {
		w.mark(direction, ref)
		w.deferFact(ref)
		return false
	}
	row := t.rows.Index(refs[0].index)
	owner, ok := wlTextField(row, "TenantID")
	if table == "Tenants" {
		owner, ok = wlTextField(row, "ID")
	}
	if ok && owner != tenant {
		w.mark(direction, ref)
		w.mark(&w.out.Mismatches.OrphanFacts, ref)
		return false
	}
	return true
}

func (w *wlWork) walletExtra(tenant string, ref wlRef, direction *uint64) {
	if w.used[ref] || w.deferred[ref] {
		return
	}
	if w.tainted[tenant] {
		w.deferFact(ref)
		return
	}
	w.mark(direction, ref)
	w.mark(&w.out.Mismatches.OrphanFacts, ref)
}

func (w *wlWork) sweep() {
	m := &w.out.Mismatches
	for i, r := range w.in.Accounts {
		if !w.tick() {
			return
		}
		if r.Kind != "AVAILABLE" && r.Kind != "HELD" {
			continue
		}
		ref := wlRef{"Accounts", i}
		if r.TenantID == nil {
			w.mark(&m.WalletSourceRows, ref)
			w.mark(&m.OrphanFacts, ref)
		} else {
			w.walletParent("Tenants", *r.TenantID, *r.TenantID, ref, &m.WalletSourceRows)
		}
	}
	// Include lines whose header is absent: enumerating only Journals would
	// otherwise hide even a zero customer-wallet line behind a missing parent.
	for i, r := range w.in.Lines {
		if !w.tick() {
			return
		}
		ref := wlRef{"Lines", i}
		a, _, aok := wlGet[LedgerAccountRow](w, "Accounts", r.AccountID)
		j, _, jok := wlGet[WalletLotsJournalRow](w, "Journals", r.JournalID)
		for _, p := range []struct{ table, id string }{{"Accounts", r.AccountID}, {"Journals", r.JournalID}} {
			n := len(w.tables[p.table].primary[wlKey{id: p.id}])
			if n == 0 {
				w.need(p.table, p.id)
				w.mark(&m.WalletSourceRows, ref)
				w.mark(&m.OrphanFacts, ref)
			}
			if n > 1 {
				w.mark(&m.WalletSourceRows, ref)
				w.deferFact(ref)
			}
		}
		if aok && jok && a.TenantID != nil && *a.TenantID != j.TenantID {
			w.mark(&m.WalletSourceRows, ref)
			w.mark(&m.OrphanFacts, ref)
		}
	}
	for i, r := range w.in.Lots {
		if !w.tick() {
			return
		}
		ref := wlRef{"Lots", i}
		w.walletParent("Tenants", r.TenantID, r.TenantID, ref, &m.OriginRows)
		count := 0
		for _, p := range []struct {
			table string
			id    *string
		}{{"Receipts", r.FundingReceiptID}, {"LegacyOpenings", r.LegacyOpeningID}, {"Refunds", r.LegacyRefundOrderID}} {
			if p.id != nil {
				count++
				w.walletParent(p.table, *p.id, r.TenantID, ref, &m.OriginRows)
			}
		}
		if count != 1 {
			w.mark(&m.OriginRows, ref)
			w.mark(&m.OrphanFacts, ref)
		}
		w.walletExtra(r.TenantID, ref, &m.OriginRows)
	}
	for i, r := range w.in.Events {
		if !w.tick() {
			return
		}
		ref := wlRef{"Events", i}
		w.walletParent("Operations", r.WalletOperationID, r.TenantID, ref, &m.EventChainRows)
		w.walletParent("Lots", r.LotID, r.TenantID, ref, &m.EventChainRows)
		if r.PreviousEventID != nil {
			w.walletParent("Events", *r.PreviousEventID, r.TenantID, ref, &m.EventChainRows)
			if prev, _, ok := wlGet[WalletLotsEventRow](w, "Events", *r.PreviousEventID); ok && prev.LotID != r.LotID {
				w.mark(&m.EventChainRows, ref)
				w.mark(&m.OrphanFacts, ref)
			}
		}
		w.walletExtra(r.TenantID, ref, &m.EventChainRows)
	}
	for i, r := range w.in.Balances {
		if !w.tick() {
			return
		}
		ref := wlRef{"Balances", i}
		w.walletParent("Lots", r.LotID, r.TenantID, ref, &m.BalanceRows)
		w.walletParent("Events", r.LastEventID, r.TenantID, ref, &m.BalanceRows)
		if last, _, ok := wlGet[WalletLotsEventRow](w, "Events", r.LastEventID); ok && last.LotID != r.LotID {
			w.mark(&m.BalanceRows, ref)
			w.mark(&m.OrphanFacts, ref)
		}
		w.walletExtra(r.TenantID, ref, &m.BalanceRows)
	}
	for i, r := range w.in.ReservationLots {
		if !w.tick() {
			return
		}
		ref := wlRef{"ReservationLots", i}
		w.walletParent("Reservations", r.ReservationID, r.TenantID, ref, &m.ReservationRows)
		w.walletParent("Lots", r.LotID, r.TenantID, ref, &m.ReservationRows)
		w.walletExtra(r.TenantID, ref, &m.ReservationRows)
	}
	for i, r := range w.in.SettlementLots {
		if !w.tick() {
			return
		}
		ref := wlRef{"SettlementLots", i}
		w.walletParent("Settlements", r.SettlementID, r.TenantID, ref, &m.SettlementRows)
		w.walletParent("ReservationLots", r.ReservationLotID, r.TenantID, ref, &m.SettlementRows)
		if s, _, sok := wlGet[WalletLotsSettlementRow](w, "Settlements", r.SettlementID); sok {
			if edge, _, eok := wlGet[WalletLotsReservationLotRow](w, "ReservationLots", r.ReservationLotID); eok {
				if reservation, _, rok := wlGet[WalletLotsReservationRow](w, "Reservations", edge.ReservationID); rok && reservation.RequestID != s.RequestID {
					w.mark(&m.SettlementRows, ref)
					w.mark(&m.OrphanFacts, ref)
				}
			}
		}
		w.walletExtra(r.TenantID, ref, &m.SettlementRows)
	}
	for i, r := range w.in.RefundLots {
		if !w.tick() {
			return
		}
		ref := wlRef{"RefundLots", i}
		w.walletParent("Refunds", r.RefundOrderID, r.TenantID, ref, &m.RefundRows)
		w.walletParent("Lots", r.LotID, r.TenantID, ref, &m.RefundRows)
		if r.SettlementLotID != nil {
			w.walletParent("SettlementLots", *r.SettlementLotID, r.TenantID, ref, &m.RefundRows)
			if edge, _, eok := wlGet[WalletLotsSettlementLotRow](w, "SettlementLots", *r.SettlementLotID); eok {
				bad := false
				if refund, _, rok := wlGet[WalletLotsRefundRow](w, "Refunds", r.RefundOrderID); rok {
					bad = refund.SettlementID != edge.SettlementID
				}
				if original, _, ook := wlGet[WalletLotsReservationLotRow](w, "ReservationLots", edge.ReservationLotID); ook {
					bad = bad || original.LotID != r.LotID
				}
				if bad {
					w.mark(&m.RefundRows, ref)
					w.mark(&m.OrphanFacts, ref)
				}
			}
		}
		w.walletExtra(r.TenantID, ref, &m.RefundRows)
	}
}
