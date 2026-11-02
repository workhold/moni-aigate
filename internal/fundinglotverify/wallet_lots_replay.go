package fundinglotverify

import (
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// These states contain independently reconstructed values. Persisted amounts,
// deltas, revisions and caches never initialize any of them.
type wlLotState struct {
	id, tenant, class string
	ref               wlRef
	fifo              WalletLotsTime
	p, a, h, s        big.Int
	revision          uint64
	lastEventID       string
	chainKnown        bool
	lastKnown         bool
}

type wlReserveEdge struct {
	lot    *wlLotState
	index  uint64
	amount big.Int
	id     string
	known  bool
	ref    wlRef
}

type wlReservationState struct {
	id, tenant string
	ref        wlRef
	amount     big.Int
	edges      []*wlReserveEdge
	finished   bool
}

type wlChargeEdge struct {
	reserve  *wlReserveEdge
	charged  big.Int
	refunded big.Int
	id       string
	known    bool
	ref      wlRef
}

type wlSettlementState struct {
	id, tenant string
	ref        wlRef
	charged    big.Int
	refunded   big.Int
	edges      []*wlChargeEdge
	legacy     bool
}

type wlEffect struct {
	lot        *wlLotState
	a, h, s, i big.Int
}

func wlEffectFor(lot *wlLotState, a, h, s, issued *big.Int) wlEffect {
	var e wlEffect
	e.lot = lot
	if a != nil {
		e.a.Set(a)
	}
	if h != nil {
		e.h.Set(h)
	}
	if s != nil {
		e.s.Set(s)
	}
	if issued != nil {
		e.i.Set(issued)
	}
	return e
}

func wlNegative(n *big.Int) *big.Int { return new(big.Int).Neg(n) }
func wlMinimum(a, b *big.Int) *big.Int {
	if a.Cmp(b) < 0 {
		return new(big.Int).Set(a)
	}
	return new(big.Int).Set(b)
}

func wlReplayKey(parts ...string) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(strconv.Itoa(len(part)))
		b.WriteByte(':')
		b.WriteString(part)
	}
	return b.String()
}

func (w *wlWork) replayWalletLots() {
	w.lots = make(map[string]*wlLotState)
	w.reservations = make(map[string]*wlReservationState)
	w.settlements = make(map[string]*wlSettlementState)
	for id, p := range w.legacyTerminal {
		if !w.tick() {
			return
		}
		s := &wlSettlementState{id: id, tenant: p.row.TenantID, ref: p.ref, legacy: true}
		s.charged.Set(&p.charged)
		s.refunded.Set(&p.refunded)
		w.settlements[id] = s
	}
	for tenant, refs := range w.operations {
		if !w.tick() {
			return
		}
		pool := make(map[string]*wlLotState)
		for _, ref := range refs {
			if !w.tick() {
				return
			}
			op, ok := wlAt[WalletLotsOperationRow](w, ref)
			if !ok {
				continue
			}
			if w.barriers[ref] {
				w.taint(tenant)
			}
			if w.tainted[tenant] {
				w.deferWalletOperation(op, ref)
				continue
			}
			if op.Seq == "0" {
				w.replayWalletOpening(tenant, op, ref, pool)
				continue
			}
			source := w.sources[wlKey{kind: op.Kind, id: op.SourceID}]
			if source == nil || !source.valid || source.legacy || source.tenant != tenant || !source.opKnown || source.opRef != ref {
				w.mark(&w.out.Mismatches.WalletSourceRows, ref)
				w.taint(tenant)
				w.deferWalletOperation(op, ref)
				continue
			}
			switch op.Kind {
			case "manual_credit", "voucher_grant", "sandbox_capture":
				w.replayWalletFunding(op, ref, source, pool)
			case "reserve":
				w.replayWalletReserve(op, ref, source, pool)
			case "finish":
				w.replayWalletFinish(op, ref, source)
			case "consumption_refund", "legacy_consumption_refund":
				w.replayWalletRefund(op, ref, source, pool)
			default:
				w.mark(&w.out.Mismatches.WalletSourceRows, ref)
				w.taint(tenant)
			}
			if w.tainted[tenant] {
				w.deferWalletOperation(op, ref)
			}
		}
	}
}

func (w *wlWork) deferWalletOperation(op WalletLotsOperationRow, ref wlRef) {
	refs := w.tables["Operations"].primary[wlKey{id: op.ID}]
	if len(refs) > 0 && w.deferred[refs[0]] {
		w.deferFact(ref)
		return
	}
	for _, sameID := range refs {
		if !w.tick() {
			return
		}
		w.deferFact(sameID)
	}
	w.deferFact(ref)
	for _, event := range w.by("Events", "WalletOperationID", op.ID) {
		if !w.tick() {
			return
		}
		w.deferFact(event)
	}
	table, field := "", ""
	switch op.Kind {
	case "reserve":
		table, field = "ReservationLots", "ReservationID"
	case "finish":
		table, field = "SettlementLots", "SettlementID"
	case "consumption_refund", "legacy_consumption_refund":
		table, field = "RefundLots", "RefundOrderID"
	}
	if table != "" {
		for _, edge := range w.by(table, field, op.SourceID) {
			if !w.tick() {
				return
			}
			w.deferFact(edge)
		}
	}
	lotField := ""
	if op.Kind == "manual_credit" || op.Kind == "voucher_grant" || op.Kind == "sandbox_capture" {
		lotField = "FundingReceiptID"
	}
	if op.Kind == "legacy_consumption_refund" {
		lotField = "LegacyRefundOrderID"
	}
	if lotField != "" {
		for _, lr := range w.by("Lots", lotField, op.SourceID) {
			if !w.tick() {
				return
			}
			w.deferFact(lr)
			if lot, ok := wlAt[WalletLotsLotRow](w, lr); ok {
				for _, balance := range w.by("Balances", "LotID", lot.ID) {
					if !w.tick() {
						return
					}
					w.deferFact(balance)
				}
			}
		}
	}
}

func (w *wlWork) deferWalletIdentity(table, id string) {
	refs := w.tables[table].primary[wlKey{id: id}]
	if len(refs) > 0 && w.deferred[refs[0]] {
		return
	}
	for _, ref := range refs {
		if !w.tick() {
			return
		}
		w.deferFact(ref)
	}
}

func (w *wlWork) newWalletOrigin(tenant, origin, field, key, class string, principal *big.Int, fifo WalletLotsTime, sourceRef wlRef, pool map[string]*wlLotState) *wlLotState {
	refs := w.by("Lots", field, key)
	for _, ref := range refs {
		if !w.tick() {
			return nil
		}
		w.claim(ref)
	}
	if len(refs) != 1 {
		w.mark(&w.out.Mismatches.OriginRows, sourceRef)
		if len(refs) == 0 {
			w.need("origin_lot", wlReplayKey(field, key))
		}
		for _, ref := range refs {
			if !w.tick() {
				return nil
			}
			w.mark(&w.out.Mismatches.OriginRows, ref)
			w.deferFact(ref)
		}
		w.taint(tenant)
		return nil
	}
	r, ok := wlAt[WalletLotsLotRow](w, refs[0])
	if !ok {
		w.taint(tenant)
		return nil
	}
	_, uniqueRef, unique := wlGet[WalletLotsLotRow](w, "Lots", r.ID)
	identities := 0
	for _, id := range []*string{r.FundingReceiptID, r.LegacyOpeningID, r.LegacyRefundOrderID} {
		if id != nil {
			identities++
		}
	}
	if !unique || uniqueRef != refs[0] || !wsUUID(r.ID) || identities != 1 || principal.Sign() <= 0 || !w.time(fifo, sourceRef) {
		w.mark(&w.out.Mismatches.OriginRows, refs[0])
		w.taint(tenant)
		return nil
	}
	if _, repeated := w.lots[r.ID]; repeated {
		w.mark(&w.out.Mismatches.OriginRows, refs[0])
		w.taint(tenant)
		return nil
	}
	issued, amountOK := w.amount(r.IssuedAmount, true, refs[0])
	if r.TenantID != tenant || r.OriginKind != origin || r.Currency != "CNY" || r.RefundClass != class || !amountOK || issued.Cmp(principal) != 0 || !wlTimeEq(r.FIFOAt, fifo) {
		w.mark(&w.out.Mismatches.OriginRows, refs[0])
	}
	w.time(r.CreatedAt, refs[0])
	w.time(r.FIFOAt, refs[0])
	s := &wlLotState{id: r.ID, tenant: tenant, class: class, ref: refs[0], fifo: fifo, chainKnown: true, lastKnown: true}
	s.p.Set(principal)
	w.lots[r.ID] = s
	pool[r.ID] = s
	w.use(refs[0])
	return s
}

func (w *wlWork) replayWalletOpening(tenant string, op WalletLotsOperationRow, opRef wlRef, pool map[string]*wlLotState) {
	if op.Kind == "tenant_genesis" {
		w.applyWalletEffects(op, opRef, nil, nil)
		return
	}
	if op.Kind != "epoch_opening" || !w.epochKnown {
		w.mark(&w.out.Mismatches.GenesisRows, opRef)
		w.taint(tenant)
		return
	}
	effects := make([]wlEffect, 0, len(w.openings[tenant]))
	for _, p := range w.openings[tenant] {
		if !w.tick() {
			return
		}
		w.use(p.ref)
		var reservation *wlReservationState
		if p.row.Kind == "reservation" && p.row.ReservationID != nil {
			id := *p.row.ReservationID
			if _, repeated := w.reservations[id]; repeated {
				w.mark(&w.out.Mismatches.ReservationRows, p.ref)
				w.taint(tenant)
				return
			}
			reservation = &wlReservationState{id: id, tenant: tenant, ref: p.ref}
			reservation.amount.Set(&p.amount)
			w.reservations[id] = reservation
		}
		if p.amount.Sign() == 0 {
			for _, ref := range w.by("Lots", "LegacyOpeningID", p.row.ID) {
				if !w.tick() {
					return
				}
				w.claim(ref)
				w.mark(&w.out.Mismatches.OriginRows, ref)
			}
			if reservation != nil {
				w.matchWalletReserveEdges(op, opRef, reservation)
			}
			continue
		}
		lot := w.newWalletOrigin(tenant, "legacy_"+p.row.Kind, "LegacyOpeningID", p.row.ID, "nonrefundable", &p.amount, w.epoch.CreatedAt, p.ref, pool)
		if lot == nil {
			return
		}
		if p.row.Kind == "reservation" && reservation != nil {
			edge := &wlReserveEdge{lot: lot, index: 1}
			edge.amount.Set(&p.amount)
			reservation.edges = []*wlReserveEdge{edge}
			w.matchWalletReserveEdges(op, opRef, reservation)
			effects = append(effects, wlEffectFor(lot, nil, &p.amount, nil, &p.amount))
		} else {
			effects = append(effects, wlEffectFor(lot, &p.amount, nil, nil, &p.amount))
		}
	}
	if !w.tainted[tenant] {
		w.applyWalletEffects(op, opRef, nil, effects)
	}
}

func (w *wlWork) replayWalletFunding(op WalletLotsOperationRow, opRef wlRef, source *wlSource, pool map[string]*wlLotState) {
	lot := w.newWalletOrigin(source.tenant, "funding_receipt", "FundingReceiptID", source.id, source.class, &source.amount, source.fifo, source.ref, pool)
	if lot == nil {
		return
	}
	w.applyWalletEffects(op, opRef, source, []wlEffect{wlEffectFor(lot, &source.amount, nil, nil, &source.amount)})
}

func (w *wlWork) replayWalletReserve(op WalletLotsOperationRow, opRef wlRef, source *wlSource, pool map[string]*wlLotState) {
	if _, repeated := w.reservations[source.id]; repeated {
		w.mark(&w.out.Mismatches.ReservationRows, source.ref)
		w.taint(source.tenant)
		return
	}
	r := &wlReservationState{id: source.id, tenant: source.tenant, ref: source.ref}
	r.amount.Set(&source.amount)
	remaining := new(big.Int).Set(&source.amount)
	if remaining.Sign() > 0 {
		candidates := make([]*wlLotState, 0)
		for _, lot := range pool {
			if !w.tick() {
				return
			}
			if lot.tenant == source.tenant && lot.a.Sign() > 0 {
				candidates = append(candidates, lot)
			}
		}
		if err := w.check(); err != nil {
			return
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].fifo.Value.Equal(candidates[j].fifo.Value) {
				return candidates[i].id < candidates[j].id
			}
			return candidates[i].fifo.Value.Before(candidates[j].fifo.Value)
		})
		if err := w.check(); err != nil {
			return
		}
		for _, lot := range candidates {
			if !w.tick() {
				return
			}
			if remaining.Sign() == 0 {
				break
			}
			q := wlMinimum(&lot.a, remaining)
			e := &wlReserveEdge{lot: lot, index: uint64(len(r.edges) + 1)}
			e.amount.Set(q)
			r.edges = append(r.edges, e)
			remaining.Sub(remaining, q)
		}
	}
	if remaining.Sign() != 0 {
		w.mark(&w.out.Mismatches.FIFOAllocationRows, source.ref)
		w.mark(&w.out.Mismatches.ReservationRows, source.ref)
		w.taint(source.tenant)
		return
	}
	w.reservations[r.id] = r
	w.matchWalletReserveEdges(op, opRef, r)
	effects := make([]wlEffect, 0, len(r.edges))
	for _, e := range r.edges {
		if !w.tick() {
			return
		}
		effects = append(effects, wlEffectFor(e.lot, wlNegative(&e.amount), &e.amount, nil, nil))
	}
	w.applyWalletEffects(op, opRef, source, effects)
}

func (w *wlWork) matchWalletReserveEdges(op WalletLotsOperationRow, opRef wlRef, reservation *wlReservationState) {
	refs := w.by("ReservationLots", "ReservationID", reservation.id)
	actual := make(map[string][]wlRef)
	for _, ref := range refs {
		if !w.tick() {
			return
		}
		w.claim(ref)
		if row, ok := wlAt[WalletLotsReservationLotRow](w, ref); ok {
			actual[row.AllocationIndex] = append(actual[row.AllocationIndex], ref)
		}
	}
	matched := make(map[wlRef]bool)
	for _, expected := range reservation.edges {
		if !w.tick() {
			return
		}
		key := strconv.FormatUint(expected.index, 10)
		candidates := actual[key]
		if len(candidates) != 1 {
			w.mark(&w.out.Mismatches.ReservationRows, reservation.ref)
			w.mark(&w.out.Mismatches.FIFOAllocationRows, reservation.ref)
			if len(candidates) == 0 {
				w.need("reservation_lot", wlReplayKey(reservation.id, key))
			}
			for _, ref := range candidates {
				if !w.tick() {
					return
				}
				w.deferFact(ref)
			}
			continue
		}
		ref := candidates[0]
		row, _ := wlAt[WalletLotsReservationLotRow](w, ref)
		_, uniqueRef, unique := wlGet[WalletLotsReservationLotRow](w, "ReservationLots", row.ID)
		q, amountOK := w.amount(row.Amount, true, ref)
		if row.TenantID != reservation.tenant || row.LotID != expected.lot.id || !amountOK || q.Cmp(&expected.amount) != 0 || row.CreatedXID != op.CreatedXID {
			w.mark(&w.out.Mismatches.FIFOAllocationRows, ref)
			w.mark(&w.out.Mismatches.ReservationRows, ref)
		}
		w.time(row.CreatedAt, ref)
		if unique && uniqueRef == ref && wsUUID(row.ID) {
			expected.id, expected.known, expected.ref = row.ID, true, ref
		} else {
			w.mark(&w.out.Mismatches.ReservationRows, ref)
			w.deferWalletIdentity("ReservationLots", row.ID)
		}
		matched[ref] = true
		w.use(ref)
	}
	for _, ref := range refs {
		if !w.tick() {
			return
		}
		if !matched[ref] {
			if !w.deferred[ref] {
				w.mark(&w.out.Mismatches.OrphanFacts, ref)
			}
			w.mark(&w.out.Mismatches.FIFOAllocationRows, ref)
			w.mark(&w.out.Mismatches.ReservationRows, ref)
		}
	}
	_ = opRef // The original operation supplies the protected allocation stamp.
}

func (w *wlWork) replayWalletFinish(op WalletLotsOperationRow, opRef wlRef, source *wlSource) {
	r := w.reservations[source.reservationID]
	if r == nil || r.tenant != source.tenant || r.finished || r.amount.Cmp(&source.reserved) != 0 || source.amount.Sign() < 0 || source.amount.Cmp(&r.amount) > 0 {
		w.mark(&w.out.Mismatches.SettlementRows, source.ref)
		w.taint(source.tenant)
		return
	}
	if _, repeated := w.settlements[source.id]; repeated {
		w.mark(&w.out.Mismatches.SettlementRows, source.ref)
		w.taint(source.tenant)
		return
	}
	s := &wlSettlementState{id: source.id, tenant: source.tenant, ref: source.ref}
	s.charged.Set(&source.amount)
	remaining := new(big.Int).Set(&source.amount)
	effects := make([]wlEffect, 0, len(r.edges))
	for _, edge := range r.edges {
		if !w.tick() {
			return
		}
		if edge.lot.h.Cmp(&edge.amount) < 0 {
			w.mark(&w.out.Mismatches.SettlementRows, source.ref)
			w.taint(source.tenant)
			return
		}
		charge := wlMinimum(&edge.amount, remaining)
		released := new(big.Int).Sub(&edge.amount, charge)
		ce := &wlChargeEdge{reserve: edge}
		ce.charged.Set(charge)
		s.edges = append(s.edges, ce)
		effects = append(effects, wlEffectFor(edge.lot, released, wlNegative(&edge.amount), charge, nil))
		remaining.Sub(remaining, charge)
	}
	if remaining.Sign() != 0 {
		w.mark(&w.out.Mismatches.SettlementRows, source.ref)
		w.taint(source.tenant)
		return
	}
	w.matchWalletFinishEdges(op, s)
	w.settlements[s.id] = s
	r.finished = true
	w.applyWalletEffects(op, opRef, source, effects)
}

func (w *wlWork) matchWalletFinishEdges(op WalletLotsOperationRow, settlement *wlSettlementState) {
	refs := w.by("SettlementLots", "SettlementID", settlement.id)
	actual := make(map[string][]wlRef)
	for _, ref := range refs {
		if !w.tick() {
			return
		}
		w.claim(ref)
		if row, ok := wlAt[WalletLotsSettlementLotRow](w, ref); ok {
			actual[row.ReservationLotID] = append(actual[row.ReservationLotID], ref)
		}
	}
	matched := make(map[wlRef]bool)
	unknownParent := false
	for _, edge := range settlement.edges {
		if !w.tick() {
			return
		}
		candidates := actual[edge.reserve.id]
		if !edge.reserve.known || len(candidates) != 1 {
			w.mark(&w.out.Mismatches.SettlementRows, settlement.ref)
			unknownParent = unknownParent || !edge.reserve.known
			if edge.reserve.known && len(candidates) == 0 {
				w.need("settlement_lot", wlReplayKey(settlement.id, edge.reserve.id))
			}
			for _, ref := range candidates {
				if !w.tick() {
					return
				}
				w.deferFact(ref)
			}
			continue
		}
		ref := candidates[0]
		row, _ := wlAt[WalletLotsSettlementLotRow](w, ref)
		_, uniqueRef, unique := wlGet[WalletLotsSettlementLotRow](w, "SettlementLots", row.ID)
		charged, cok := w.amount(row.ChargedAmount, false, ref)
		released, rok := w.amount(row.ReleasedAmount, false, ref)
		expectedRelease := new(big.Int).Sub(&edge.reserve.amount, &edge.charged)
		if row.TenantID != settlement.tenant || !cok || !rok || charged.Cmp(&edge.charged) != 0 || released.Cmp(expectedRelease) != 0 || row.CreatedXID != op.CreatedXID {
			w.mark(&w.out.Mismatches.SettlementRows, ref)
		}
		w.time(row.CreatedAt, ref)
		if unique && uniqueRef == ref && wsUUID(row.ID) {
			edge.id, edge.known, edge.ref = row.ID, true, ref
		} else {
			w.mark(&w.out.Mismatches.SettlementRows, ref)
			w.deferWalletIdentity("SettlementLots", row.ID)
		}
		matched[ref] = true
		w.use(ref)
	}
	for _, ref := range refs {
		if !w.tick() {
			return
		}
		if !matched[ref] {
			if unknownParent {
				w.deferFact(ref)
			} else {
				if !w.deferred[ref] {
					w.mark(&w.out.Mismatches.OrphanFacts, ref)
				}
				w.mark(&w.out.Mismatches.SettlementRows, ref)
			}
		}
	}
}
