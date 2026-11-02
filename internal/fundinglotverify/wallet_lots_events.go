package fundinglotverify

import "math/big"

type wlExpectedEvent struct {
	effect                        wlEffect
	beforeA, beforeH, beforeS     big.Int
	afterA, afterH, afterS        big.Int
	beforeRevision, afterRevision uint64
	previousID                    string
	previousKnown                 bool
}

func (w *wlWork) applyWalletEffects(op WalletLotsOperationRow, opRef wlRef, source *wlSource, effects []wlEffect) {
	expected := make([]wlExpectedEvent, 0, len(effects))
	seen := make(map[string]bool)
	var totalA, totalH, totalS, totalI big.Int
	for _, effect := range effects {
		if !w.tick() {
			return
		}
		lot := effect.lot
		e := wlExpectedEvent{effect: effect, beforeRevision: lot.revision, afterRevision: lot.revision + 1, previousID: lot.lastEventID, previousKnown: lot.lastKnown}
		e.beforeA.Set(&lot.a)
		e.beforeH.Set(&lot.h)
		e.beforeS.Set(&lot.s)
		e.afterA.Add(&lot.a, &effect.a)
		e.afterH.Add(&lot.h, &effect.h)
		e.afterS.Add(&lot.s, &effect.s)
		sum := new(big.Int).Add(&e.afterA, &e.afterH)
		sum.Add(sum, &e.afterS)
		if seen[lot.id] || e.afterA.Sign() < 0 || e.afterH.Sign() < 0 || e.afterS.Sign() < 0 || sum.Cmp(&lot.p) != 0 {
			w.mark(&w.out.Mismatches.WalletSourceRows, opRef)
			w.taint(op.TenantID)
			return
		}
		seen[lot.id] = true
		totalA.Add(&totalA, &effect.a)
		totalH.Add(&totalH, &effect.h)
		totalS.Add(&totalS, &effect.s)
		totalI.Add(&totalI, &effect.i)
		expected = append(expected, e)
	}
	count, countOK := w.unsigned(op.EventCount, false, true, opRef)
	if !countOK || count != uint64(len(expected)) || !w.walletDeltaEqual(op.AvailableDelta, &totalA, opRef) || !w.walletDeltaEqual(op.HeldDelta, &totalH, opRef) || !w.walletDeltaEqual(op.ConsumedDelta, &totalS, opRef) || !w.walletDeltaEqual(op.IssuedDelta, &totalI, opRef) {
		w.mark(&w.out.Mismatches.WalletSourceRows, opRef)
	}
	actual := make(map[string][]wlRef)
	refs := w.by("Events", "WalletOperationID", op.ID)
	for _, ref := range refs {
		if !w.tick() {
			return
		}
		w.claim(ref)
		if row, ok := wlAt[WalletLotsEventRow](w, ref); ok {
			actual[row.LotID] = append(actual[row.LotID], ref)
		}
	}
	matched := make(map[wlRef]bool)
	for _, e := range expected {
		if !w.tick() {
			return
		}
		lot := e.effect.lot
		candidates := actual[lot.id]
		known, eventID := false, ""
		if len(candidates) != 1 {
			w.mark(&w.out.Mismatches.EventChainRows, opRef)
			if len(candidates) == 0 {
				w.need("lot_event", wlReplayKey(op.ID, lot.id))
			}
			for _, ref := range candidates {
				if !w.tick() {
					return
				}
				w.deferFact(ref)
			}
		} else {
			ref := candidates[0]
			row, _ := wlAt[WalletLotsEventRow](w, ref)
			_, uniqueRef, unique := wlGet[WalletLotsEventRow](w, "Events", row.ID)
			known, eventID = unique && uniqueRef == ref && wsUUID(row.ID), row.ID
			if !known {
				w.mark(&w.out.Mismatches.EventChainRows, ref)
				w.deferWalletIdentity("Events", row.ID)
			}
			w.compareWalletEvent(row, ref, op, source, e)
			matched[ref] = true
			w.use(ref)
		}
		// Continue only from independent amounts. Losing an event locator never
		// permits a later before/previous statement to restore the missing link.
		lot.a.Set(&e.afterA)
		lot.h.Set(&e.afterH)
		lot.s.Set(&e.afterS)
		lot.revision = e.afterRevision
		lot.lastKnown = known
		if known {
			lot.lastEventID = eventID
		} else {
			lot.lastEventID = ""
			lot.chainKnown = false
		}
	}
	for _, ref := range refs {
		if !w.tick() {
			return
		}
		if !matched[ref] {
			if !w.deferred[ref] {
				w.mark(&w.out.Mismatches.OrphanFacts, ref)
			}
			w.mark(&w.out.Mismatches.EventChainRows, ref)
		}
	}
	w.use(opRef)
}

func (w *wlWork) walletDeltaEqual(text string, expected *big.Int, ref wlRef) bool {
	n, ok := w.integer(text, ref)
	return ok && n.Cmp(expected) == 0
}

func (w *wlWork) walletBucketEqual(text string, expected *big.Int, ref wlRef) bool {
	n, ok := w.amount(text, false, ref)
	return ok && n.Cmp(expected) == 0
}

func (w *wlWork) compareWalletEvent(row WalletLotsEventRow, ref wlRef, op WalletLotsOperationRow, source *wlSource, expected wlExpectedEvent) {
	var journal, operation *string
	if source != nil {
		journal, operation = &source.journalID, &source.journalOperationID
	}
	bad := row.TenantID != expected.effect.lot.tenant || row.WalletOperationID != op.ID || row.WalletSeq != op.Seq || row.Kind != wsExpectedEventKind(op.Kind) || !wsSameOptional(row.JournalID, journal) || !wsSameOptional(row.OperationID, operation) || row.CreatedXID != op.CreatedXID
	before, bok := w.unsigned(row.BeforeRevision, false, true, ref)
	after, aok := w.unsigned(row.AfterRevision, true, true, ref)
	bad = bad || !bok || !aok || before != expected.beforeRevision || after != expected.afterRevision
	if expected.beforeRevision == 0 {
		bad = bad || row.PreviousEventID != nil
	} else {
		bad = bad || row.PreviousEventID == nil
		if expected.previousKnown && row.PreviousEventID != nil {
			bad = bad || *row.PreviousEventID != expected.previousID
		}
	}
	if !w.walletBucketEqual(row.BeforeAvailable, &expected.beforeA, ref) || !w.walletBucketEqual(row.BeforeHeld, &expected.beforeH, ref) || !w.walletBucketEqual(row.BeforeConsumed, &expected.beforeS, ref) || !w.walletBucketEqual(row.AfterAvailable, &expected.afterA, ref) || !w.walletBucketEqual(row.AfterHeld, &expected.afterH, ref) || !w.walletBucketEqual(row.AfterConsumed, &expected.afterS, ref) || !w.walletDeltaEqual(row.AvailableDelta, &expected.effect.a, ref) || !w.walletDeltaEqual(row.HeldDelta, &expected.effect.h, ref) || !w.walletDeltaEqual(row.ConsumedDelta, &expected.effect.s, ref) || !w.walletDeltaEqual(row.IssuedDelta, &expected.effect.i, ref) {
		bad = true
	}
	w.time(row.CreatedAt, ref)
	if bad {
		w.mark(&w.out.Mismatches.EventChainRows, ref)
	}
}

type wlTenantAmounts struct {
	a, h, heldExpected, heldCurrent big.Int
	currentKnown                    bool
}

func (w *wlWork) finishWalletLots() {
	w.finishWalletRefundTotals()
	amounts := make(map[string]*wlTenantAmounts)
	get := func(tenant string) *wlTenantAmounts {
		state := amounts[tenant]
		if state == nil {
			state = &wlTenantAmounts{currentKnown: true}
			amounts[tenant] = state
		}
		return state
	}
	for _, lot := range w.lots {
		if !w.tick() {
			return
		}
		if w.tainted[lot.tenant] {
			continue
		}
		a := get(lot.tenant)
		a.a.Add(&a.a, &lot.a)
		a.h.Add(&a.h, &lot.h)
		w.compareWalletBalance(lot)
	}
	for _, reservation := range w.reservations {
		if !w.tick() {
			return
		}
		if w.tainted[reservation.tenant] {
			continue
		}
		if !reservation.finished {
			get(reservation.tenant).heldExpected.Add(&get(reservation.tenant).heldExpected, &reservation.amount)
		}
		row, ref, ok := wlGet[WalletLotsReservationRow](w, "Reservations", reservation.id)
		if !ok {
			continue
		}
		unfinished := row.Status == "reserved" || row.Status == "pending_reconciliation"
		if unfinished == reservation.finished {
			w.mark(&w.out.Mismatches.ReservationRows, ref)
		}
	}
	for i, row := range w.in.Reservations {
		if !w.tick() {
			return
		}
		if row.Status != "reserved" && row.Status != "pending_reconciliation" {
			continue
		}
		ref := wlRef{table: "Reservations", index: i}
		amount, ok := w.amount(row.Amount, false, ref)
		state := get(row.TenantID)
		if ok {
			state.heldCurrent.Add(&state.heldCurrent, amount)
		} else {
			state.currentKnown = false
		}
	}
	for i, tenant := range w.in.Tenants {
		if !w.tick() {
			return
		}
		if w.tainted[tenant.ID] {
			continue
		}
		_, uniqueRef, unique := wlGet[WalletSequenceTenantRow](w, "Tenants", tenant.ID)
		if !unique || uniqueRef.index != i {
			continue
		}
		a := get(tenant.ID)
		if a.h.Cmp(&a.heldExpected) != 0 || a.currentKnown && a.h.Cmp(&a.heldCurrent) != 0 {
			w.mark(&w.out.Mismatches.BalanceRows, uniqueRef)
		}
		w.compareWalletAccount(tenant.ID, "AVAILABLE", &a.a, uniqueRef)
		w.compareWalletAccount(tenant.ID, "HELD", &a.h, uniqueRef)
	}
	// This claim-level invariant remains decidable even for a tenant whose
	// semantic prefix was lost. It does not seed any reconstructed state.
	for i, row := range w.in.Balances {
		if !w.tick() {
			return
		}
		ref := wlRef{table: "Balances", index: i}
		lot, lotRef, ok := wlGet[WalletLotsLotRow](w, "Lots", row.LotID)
		if !ok {
			continue
		}
		p, pok := w.amount(lot.IssuedAmount, true, lotRef)
		a, aok := w.amount(row.AvailableAmount, false, ref)
		h, hok := w.amount(row.InferenceHeldAmount, false, ref)
		s, sok := w.amount(row.ConsumedAmount, false, ref)
		if pok && aok && hok && sok {
			sum := new(big.Int).Add(a, h)
			sum.Add(sum, s)
			if sum.Cmp(p) != 0 {
				w.mark(&w.out.Mismatches.BalanceRows, ref)
			}
		}
	}
}

func (w *wlWork) compareWalletBalance(lot *wlLotState) {
	refs := w.by("Balances", "LotID", lot.id)
	for _, ref := range refs {
		if !w.tick() {
			return
		}
		w.claim(ref)
	}
	if len(refs) != 1 {
		w.mark(&w.out.Mismatches.BalanceRows, lot.ref)
		if len(refs) == 0 {
			w.need("lot_balance", lot.id)
		}
		for _, ref := range refs {
			if !w.tick() {
				return
			}
			w.mark(&w.out.Mismatches.BalanceRows, ref)
			w.deferFact(ref)
		}
		return
	}
	ref := refs[0]
	row, _ := wlAt[WalletLotsBalanceRow](w, ref)
	revision, rok := w.unsigned(row.Revision, true, true, ref)
	bad := row.TenantID != lot.tenant || !rok || revision != lot.revision
	bad = !w.walletBucketEqual(row.AvailableAmount, &lot.a, ref) || !w.walletBucketEqual(row.InferenceHeldAmount, &lot.h, ref) || !w.walletBucketEqual(row.ConsumedAmount, &lot.s, ref) || bad
	if lot.lastKnown {
		bad = bad || row.LastEventID != lot.lastEventID
	}
	if bad {
		w.mark(&w.out.Mismatches.BalanceRows, ref)
	}
	w.use(ref)
}

func (w *wlWork) compareWalletAccount(tenant, kind string, expected *big.Int, tenantRef wlRef) {
	refs := make([]wlRef, 0, 1)
	for _, ref := range w.by("Accounts", "TenantID", tenant) {
		if !w.tick() {
			return
		}
		row, ok := wlAt[LedgerAccountRow](w, ref)
		if ok && row.TenantID != nil && *row.TenantID == tenant && row.Kind == kind {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		if expected.Sign() != 0 {
			w.mark(&w.out.Mismatches.BalanceRows, tenantRef)
			w.need("wallet_account", wlReplayKey(tenant, kind))
		}
		return
	}
	if len(refs) != 1 {
		for _, ref := range refs {
			if !w.tick() {
				return
			}
			w.mark(&w.out.Mismatches.BalanceRows, ref)
		}
		return
	}
	row, _ := wlAt[LedgerAccountRow](w, refs[0])
	if !w.walletBucketEqual(row.Balance, expected, refs[0]) {
		w.mark(&w.out.Mismatches.BalanceRows, refs[0])
	}
}
