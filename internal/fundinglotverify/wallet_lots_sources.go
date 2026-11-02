package fundinglotverify

import "math/big"

func wlWalletKind(kind string) bool {
	switch kind {
	case "manual_credit", "voucher_grant", "sandbox_capture", "reserve", "finish", "consumption_refund", "legacy_consumption_refund":
		return true
	}
	return false
}
func wlWalletLegacyKind(kind string) string {
	if kind == "legacy_consumption_refund" {
		return "consumption_refund"
	}
	return kind
}
func wlWalletSourceKind(kind string) bool {
	return kind == "manual_credit" || kind == "voucher_grant" || kind == "sandbox_capture"
}
func wlWalletUnfinished(status string) bool {
	return status == "reserved" || status == "pending_reconciliation"
}
func wlWalletState(status string) bool {
	return wlWalletUnfinished(status) || status == "settled" || status == "released"
}
func wlWalletTextPointer(p *string, s string) bool { return p != nil && *p == s }
func wlWalletTimeEqual(a, b WalletLotsTime) bool {
	return a.Valid && b.Valid && a.Infinity == 0 && b.Infinity == 0 && a.Value.Equal(b.Value)
}

// The normalized source namespace is indexed once. Repeated raw business
// keys must not repeatedly scan an equally large operation duplicate group.
func (w *wlWork) walletSourceOperationRefs(kind, id string) []wlRef {
	if w.sourceOperations == nil {
		w.sourceOperations = map[wlKey][]wlRef{}
		for i, op := range w.in.Operations {
			if !w.tick() {
				return nil
			}
			if wlWalletKind(op.Kind) {
				key := wlKey{wlWalletLegacyKind(op.Kind), op.SourceID}
				w.sourceOperations[key] = append(w.sourceOperations[key], wlRef{"Operations", i})
			}
		}
	}
	return w.sourceOperations[wlKey{wlWalletLegacyKind(kind), id}]
}
func (w *wlWork) delayWalletSourceFailure(s *wlSource) {
	refs := w.walletSourceOperationRefs(s.kind, s.id)
	if len(w.tables["LegacySources"].primary[wlKey{wlWalletLegacyKind(s.kind), s.id}]) > 0 {
		w.taint(s.tenant)
	} else if len(refs) == 1 {
		op, _ := wlAt[WalletLotsOperationRow](w, refs[0])
		if len(w.tables["Operations"].primary[wlKey{id: op.ID}]) == 1 {
			w.barriers[refs[0]] = true
		} else {
			w.taint(s.tenant)
		}
	} else {
		w.taint(s.tenant)
	}
}

// Source amounts come from the immutable business rows, never a wallet
// operation, event, cache or allocation declaration.
func (w *wlWork) buildWalletSources() {
	for i, r := range w.in.Receipts {
		if !w.tick() {
			return
		}
		ref := wlRef{"Receipts", i}
		s := &wlSource{kind: r.SourceKind, id: r.ID, tenant: r.TenantID, journalID: r.JournalID, ref: ref, valid: true, fifo: r.CreatedAt, class: "nonrefundable"}
		if r.SourceKind == "sandbox_capture" {
			s.class = "sandbox_original"
		}
		n, ok := w.amount(r.Amount, true, ref)
		if ok {
			s.amount.Set(n)
		} else {
			s.valid = false
		}
		if !wlWalletSourceKind(r.SourceKind) || r.Currency != "CNY" || !w.time(r.CreatedAt, ref) {
			w.mark(&w.out.Mismatches.WalletSourceRows, ref)
			s.valid = false
		}
		var expectedReference *string
		switch r.SourceKind {
		case "manual_credit":
			s.journalOperationID = r.SourceID
			expectedReference = &r.SourceID
		case "sandbox_capture":
			s.journalOperationID = r.SourceID
		case "voucher_grant":
			expectedReference = &r.SourceID
			if j, _, exists := wlGet[WalletLotsJournalRow](w, "Journals", r.JournalID); exists {
				s.journalOperationID = j.OperationID
			} else {
				s.valid = false
				w.need("journal", r.JournalID)
			}
		}
		kind, clearing := "credit", "FUNDING_CLEARING"
		if r.SourceKind == "voucher_grant" {
			kind, clearing = "voucher_grant", "OPERATING_GRANT_CLEARING"
		}
		if r.SourceKind == "sandbox_capture" {
			kind, clearing = "sandbox_capture", "SANDBOX_FUNDING_CLEARING"
		}
		if ok {
			w.checkWalletSourceJournal(s, kind, expectedReference, nil, []wlSourceLine{{kind: "AVAILABLE", tenant: &r.TenantID, amount: new(big.Int).Set(n)}, {kind: clearing, amount: new(big.Int).Neg(n)}})
		}
		w.addWalletSource(s)
	}
	for i, r := range w.in.Reservations {
		if !w.tick() {
			return
		}
		ref := wlRef{"Reservations", i}
		s := &wlSource{kind: "reserve", id: r.ID, tenant: r.TenantID, requestID: r.RequestID, reservationID: r.ID, journalOperationID: r.ID, ref: ref, valid: true}
		n, ok := w.amount(r.Amount, false, ref)
		if ok {
			s.amount.Set(n)
		} else {
			s.valid = false
		}
		request, _, known := wlGet[WalletLotsRequestRow](w, "Requests", r.RequestID)
		if !known || request.TenantID != r.TenantID || request.BillingStatus != r.Status || !wlWalletState(r.Status) {
			w.mark(&w.out.Mismatches.ReservationRows, ref)
			s.valid = false
			if !known {
				w.need("request", r.RequestID)
			}
		}
		if len(w.by("Reservations", "RequestID", r.RequestID)) != 1 {
			w.mark(&w.out.Mismatches.ReservationRows, ref)
			s.valid = false
		}
		journals := w.by("Journals", "OperationID", r.ID)
		if len(journals) == 1 {
			j, _ := wlAt[WalletLotsJournalRow](w, journals[0])
			s.journalID = j.ID
		} else {
			s.valid = false
			w.need("reserve_journal", r.ID)
		}
		if ok && known {
			w.checkWalletSourceJournal(s, "reserve", &r.RequestID, &request.OwnerID, []wlSourceLine{{kind: "AVAILABLE", tenant: &r.TenantID, amount: new(big.Int).Neg(n)}, {kind: "HELD", tenant: &r.TenantID, amount: new(big.Int).Set(n)}})
		}
		terminals := w.by("Settlements", "RequestID", r.RequestID)
		if wlWalletUnfinished(r.Status) && len(terminals) != 0 || !wlWalletUnfinished(r.Status) && len(terminals) != 1 {
			w.mark(&w.out.Mismatches.ReservationRows, ref)
			s.valid = false
		}
		w.addWalletSource(s)
	}
	for i, r := range w.in.Settlements {
		if !w.tick() {
			return
		}
		ref := wlRef{"Settlements", i}
		s := &wlSource{kind: "finish", id: r.ID, tenant: r.TenantID, journalID: r.JournalID, journalOperationID: r.ID, requestID: r.RequestID, settlementID: r.ID, ref: ref, valid: true}
		n, ok := w.amount(r.ChargedAmount, false, ref)
		if ok {
			s.amount.Set(n)
		} else {
			s.valid = false
		}
		refs := w.by("Reservations", "RequestID", r.RequestID)
		var reservation WalletLotsReservationRow
		var reserved *big.Int
		rok := false
		if len(refs) == 1 {
			reservation, _ = wlAt[WalletLotsReservationRow](w, refs[0])
			s.reservationID = reservation.ID
			reserved, rok = w.amount(reservation.Amount, false, refs[0])
			if rok {
				s.reserved.Set(reserved)
			}
		}
		request, _, qok := wlGet[WalletLotsRequestRow](w, "Requests", r.RequestID)
		if !rok || !qok || reservation.TenantID != r.TenantID || request.TenantID != r.TenantID || reservation.Status != r.Status || request.BillingStatus != r.Status || r.Status != "settled" && r.Status != "released" || len(w.by("Settlements", "RequestID", r.RequestID)) != 1 {
			w.mark(&w.out.Mismatches.SettlementRows, ref)
			s.valid = false
		}
		if ok && rok && n.Cmp(reserved) > 0 || ok && r.Status == "released" && n.Sign() != 0 {
			w.mark(&w.out.Mismatches.SettlementRows, ref)
			s.valid = false
		}
		if ok && rok {
			lines := []wlSourceLine{{kind: "AVAILABLE", tenant: &r.TenantID, amount: new(big.Int).Sub(reserved, n)}, {kind: "HELD", tenant: &r.TenantID, amount: new(big.Int).Neg(reserved)}}
			if n.Sign() > 0 {
				lines = append(lines, wlSourceLine{kind: "REVENUE", amount: new(big.Int).Set(n)})
			}
			system := "system"
			w.checkWalletSourceJournal(s, r.Status, &r.RequestID, &system, lines)
		}
		w.addWalletSource(s)
	}
	for i, r := range w.in.Refunds {
		if !w.tick() {
			return
		}
		ref := wlRef{"Refunds", i}
		n, ok := w.amount(r.Amount, true, ref)
		settlement, settlementRef, known := wlGet[WalletLotsSettlementRow](w, "Settlements", r.SettlementID)
		if r.State == "pending" {
			w.use(ref)
			if !ok || !known || settlement.TenantID != r.TenantID {
				w.mark(&w.out.Mismatches.RefundRows, ref)
			}
			if r.JournalID != nil || r.Approver != nil {
				w.mark(&w.out.Mismatches.RefundRows, ref)
			}
			for _, er := range w.by("Operations", "SourceID", r.ID) {
				e, _ := wlAt[WalletLotsOperationRow](w, er)
				if e.Kind == "consumption_refund" || e.Kind == "legacy_consumption_refund" {
					w.mark(&w.out.Mismatches.RefundRows, ref)
					w.mark(&w.out.Mismatches.WalletSourceRows, er)
				}
			}
			if len(w.by("RefundLots", "RefundOrderID", r.ID)) > 0 || len(w.by("Lots", "LegacyRefundOrderID", r.ID)) > 0 {
				w.mark(&w.out.Mismatches.RefundRows, ref)
			}
			continue
		}
		kind := "consumption_refund"
		if len(w.by("LegacySettlements", "SettlementID", r.SettlementID)) > 0 {
			kind = "legacy_consumption_refund"
		}
		s := &wlSource{kind: kind, id: r.ID, tenant: r.TenantID, settlementID: r.SettlementID, journalOperationID: r.ID, ref: ref, valid: true}
		if ok {
			s.amount.Set(n)
		} else {
			s.valid = false
		}
		if r.JournalID != nil {
			s.journalID = *r.JournalID
		} else {
			s.valid = false
		}
		if known {
			s.requestID = settlement.RequestID
			reservations := w.by("Reservations", "RequestID", settlement.RequestID)
			if len(reservations) == 1 {
				v, _ := wlAt[WalletLotsReservationRow](w, reservations[0])
				s.reservationID = v.ID
			} else {
				s.valid = false
			}
		} else {
			w.need("settlement", r.SettlementID)
			s.valid = false
		}
		if r.State != "approved" || r.Approver == nil || r.Approver != nil && *r.Approver == r.Requester || !known || known && settlement.TenantID != r.TenantID {
			w.mark(&w.out.Mismatches.RefundRows, ref)
			s.valid = false
		}
		if known && ok {
			charge, cok := w.amount(settlement.ChargedAmount, false, settlementRef)
			if !cok || n.Cmp(charge) > 0 {
				w.mark(&w.out.Mismatches.RefundRows, ref)
				s.valid = false
			}
		}
		if ok {
			w.checkWalletSourceJournal(s, "refund", &r.SettlementID, r.Approver, []wlSourceLine{{kind: "AVAILABLE", tenant: &r.TenantID, amount: new(big.Int).Set(n)}, {kind: "REVENUE", amount: new(big.Int).Neg(n)}})
		}
		w.addWalletSource(s)
	}
	// Current refunded is a declaration checked against all immutable approved
	// business rows. It never supplies a historical allocation's remaining room.
	for i, s := range w.in.Settlements {
		if !w.tick() {
			return
		}
		ref := wlRef{"Settlements", i}
		if len(w.tables["Settlements"].primary[wlKey{id: s.ID}]) != 1 {
			w.mark(&w.out.Mismatches.RefundRows, ref)
			continue
		}
		sum := new(big.Int)
		complete := true
		for _, rr := range w.by("Refunds", "SettlementID", s.ID) {
			if !w.tick() {
				return
			}
			r, _ := wlAt[WalletLotsRefundRow](w, rr)
			if r.State != "approved" {
				continue
			}
			n, ok := w.amount(r.Amount, true, rr)
			if !ok || len(w.tables["Refunds"].primary[wlKey{id: r.ID}]) != 1 || r.TenantID != s.TenantID {
				complete = false
				continue
			}
			sum.Add(sum, n)
		}
		declared, dok := w.amount(s.RefundedAmount, false, ref)
		charge, cok := w.amount(s.ChargedAmount, false, ref)
		// An invalid future refund does not invalidate earlier numeric effects.
		// Its operation becomes the replay barrier; no current SUM is guessed.
		if complete && dok && declared.Cmp(sum) != 0 || complete && cok && sum.Cmp(charge) > 0 {
			w.mark(&w.out.Mismatches.RefundRows, ref)
		}
	}
	for i, r := range w.in.Requests {
		if !w.tick() {
			return
		}
		ref := wlRef{"Requests", i}
		refs := w.by("Reservations", "RequestID", r.ID)
		if len(refs) != 1 {
			w.mark(&w.out.Mismatches.ReservationRows, ref)
			w.need("request_reservation", r.ID)
			w.taint(r.TenantID)
		} else {
			v, _ := wlAt[WalletLotsReservationRow](w, refs[0])
			if v.TenantID != r.TenantID || v.Status != r.BillingStatus {
				w.mark(&w.out.Mismatches.ReservationRows, ref)
				w.taint(r.TenantID)
			}
		}
		w.use(ref)
	}
}

func (w *wlWork) addWalletSource(s *wlSource) {
	key := wlKey{s.kind, s.id}
	table := w.tables[s.ref.table]
	if table == nil || len(table.primary[wlKey{id: s.id}]) != 1 {
		w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
		w.delayWalletSourceFailure(s)
		w.deferFact(s.ref)
		return
	}
	if _, _, ok := wlGet[WalletSequenceTenantRow](w, "Tenants", s.tenant); !ok {
		s.valid = false
		w.need("source_tenant", s.tenant)
	}
	if !s.valid {
		w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
		// Legacy initialization rejects it before replay. For a uniquely
		// ordered new source, replay stops only when it reaches this source.
	}
	if previous, exists := w.sources[key]; exists {
		previous.valid = false
		s.valid = false
		w.mark(&w.out.Mismatches.WalletSourceRows, previous.ref)
		w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
		w.delayWalletSourceFailure(previous)
		w.delayWalletSourceFailure(s)
		w.deferFact(s.ref)
		return
	}
	w.sources[key] = s
	w.claim(s.ref)
}

type wlSourceLine struct {
	kind   string
	tenant *string
	amount *big.Int
}

func (w *wlWork) checkWalletSourceJournal(s *wlSource, kind string, reference, actor *string, expected []wlSourceLine) {
	j, jref, known := wlGet[WalletLotsJournalRow](w, "Journals", s.journalID)
	if !known {
		w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
		w.need("journal", s.journalID)
		return
	}
	w.use(jref)
	if j.TenantID != s.tenant || j.OperationID != s.journalOperationID || j.Kind != kind || j.Currency != "CNY" || reference != nil && j.Reference != *reference || actor != nil && j.Actor != *actor || len(w.by("Journals", "OperationID", j.OperationID)) != 1 {
		w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
	}
	lines := w.by("Lines", "JournalID", s.journalID)
	if len(lines) != len(expected) {
		w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
	}
	// No wallet journal has more than three lines. All raw line values were
	// already qualified in the global pass. A shared, malformed long journal
	// must not be rescanned once per source that falsely claims it.
	if len(lines) > 3 {
		return
	}
	seen := make([]bool, len(expected))
	for _, ref := range lines {
		if !w.tick() {
			return
		}
		l, _ := wlAt[LedgerLineRow](w, ref)
		a, aref, aok := wlGet[LedgerAccountRow](w, "Accounts", l.AccountID)
		n, nok := ledgerInteger(l.SignedDelta)
		if !nok {
			w.mark(&w.out.Mismatches.InvalidValueRows, ref)
		}
		match := -1
		if aok && nok {
			for k, e := range expected {
				if a.Kind != e.kind || a.TenantID == nil != (e.tenant == nil) || a.TenantID != nil && *a.TenantID != *e.tenant || big.NewInt(n).Cmp(e.amount) != 0 {
					continue
				}
				if !seen[k] {
					match = k
					break
				}
			}
		}
		if match < 0 {
			w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
			w.mark(&w.out.Mismatches.WalletSourceRows, ref)
		} else {
			seen[match] = true
		}
		w.use(ref)
		if aok {
			w.use(aref)
		}
	}
	for _, ok := range seen {
		if !ok {
			w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
		}
	}
}

// Every supplied effective business source must be in exactly one immutable
// legacy prefix or one new wallet operation. Public WS success is not authority.
func (w *wlWork) bindWalletSources() {
	for _, s := range w.sources {
		if !w.tick() {
			return
		}
		key := wlKey{wlWalletLegacyKind(s.kind), s.id}
		oldRefs := w.tables["LegacySources"].primary[key]
		ops := w.walletSourceOperationRefs(s.kind, s.id)
		if len(oldRefs) > 0 {
			s.legacy = true
			if len(oldRefs) != 1 || len(ops) != 0 {
				w.mark(&w.out.Mismatches.LegacyBoundaryRows, s.ref)
				w.taint(s.tenant)
			}
			if len(oldRefs) == 1 {
				old, _ := wlAt[WalletLotsLegacySourceRow](w, oldRefs[0])
				if old.TenantID != s.tenant || old.JournalID != s.journalID || old.JournalOperationID != s.journalOperationID {
					w.mark(&w.out.Mismatches.LegacyBoundaryRows, oldRefs[0])
					w.taint(s.tenant)
				}
			}
			w.use(s.ref)
			continue
		}
		if len(ops) != 1 {
			w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
			w.need("wallet_operation:"+s.kind, s.id)
			w.taint(s.tenant)
			continue
		}
		op, opRef := WalletLotsOperationRow{}, ops[0]
		op, _ = wlAt[WalletLotsOperationRow](w, opRef)
		// Duplicate operation IDs may not act as an opaque locator.
		if len(w.tables["Operations"].primary[wlKey{id: op.ID}]) != 1 {
			w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
			w.taint(s.tenant)
			continue
		}
		s.opRef, s.opKnown = opRef, true
		if op.Kind != s.kind || op.TenantID != s.tenant || op.JournalID == nil || *op.JournalID != s.journalID || op.JournalOperationID == nil || *op.JournalOperationID != s.journalOperationID {
			w.mark(&w.out.Mismatches.WalletSourceRows, s.ref)
			w.mark(&w.out.Mismatches.WalletSourceRows, opRef)
			s.valid = false
			w.barriers[opRef] = true
		}
		if j, jref, ok := wlGet[WalletLotsJournalRow](w, "Journals", s.journalID); ok {
			jx, jok := w.unsigned(j.CreatedTXID, true, true, jref)
			ox, ook := w.unsigned(op.CreatedXID, true, false, opRef)
			if !jok || !ook || jx != ox {
				w.mark(&w.out.Mismatches.WalletSourceRows, opRef)
			}
		}
		w.use(s.ref)
	}
	// A journal touching either customer account is always a wallet source,
	// even when its only customer deltas are zero. Other business journals are
	// deliberately outside this lot-domain reverse relation.
	ambiguousJournals := make(map[string]bool)
	ambiguousReserveIDs := make(map[string]bool)
	for _, r := range w.in.Receipts {
		if !w.tick() {
			return
		}
		if len(w.tables["Receipts"].primary[wlKey{id: r.ID}]) != 1 {
			ambiguousJournals[r.JournalID] = true
		}
	}
	for _, r := range w.in.Settlements {
		if !w.tick() {
			return
		}
		if len(w.tables["Settlements"].primary[wlKey{id: r.ID}]) != 1 {
			ambiguousJournals[r.JournalID] = true
		}
	}
	for _, r := range w.in.Refunds {
		if !w.tick() {
			return
		}
		if r.JournalID != nil && len(w.tables["Refunds"].primary[wlKey{id: r.ID}]) != 1 {
			ambiguousJournals[*r.JournalID] = true
		}
	}
	for _, r := range w.in.Reservations {
		if !w.tick() {
			return
		}
		if len(w.tables["Reservations"].primary[wlKey{id: r.ID}]) != 1 {
			ambiguousReserveIDs[r.ID] = true
		}
	}
	sourceJournals := make(map[string]int)
	for _, s := range w.sources {
		if !w.tick() {
			return
		}
		sourceJournals[s.journalID]++
	}
	walletJournals := make(map[string]bool)
	for _, l := range w.in.Lines {
		if !w.tick() {
			return
		}
		a, _, ok := wlGet[LedgerAccountRow](w, "Accounts", l.AccountID)
		if ok && (a.Kind == "AVAILABLE" || a.Kind == "HELD") {
			walletJournals[l.JournalID] = true
		}
	}
	for i, j := range w.in.Journals {
		if !w.tick() {
			return
		}
		ref := wlRef{"Journals", i}
		wallet := walletJournals[j.ID] || j.Kind == "credit" || j.Kind == "voucher_grant" || j.Kind == "sandbox_capture" || j.Kind == "reserve" || j.Kind == "settled" || j.Kind == "released" || j.Kind == "refund"
		if !wallet {
			continue
		}
		count := sourceJournals[j.ID]
		if count == 0 && (ambiguousJournals[j.ID] || ambiguousReserveIDs[j.OperationID]) {
			w.mark(&w.out.Mismatches.WalletSourceRows, ref)
			w.deferFact(ref)
			continue
		}
		if count != 1 {
			w.mark(&w.out.Mismatches.WalletSourceRows, ref)
			if count == 0 {
				w.mark(&w.out.Mismatches.OrphanFacts, ref)
			}
			w.taint(j.TenantID)
		}
	}
}
