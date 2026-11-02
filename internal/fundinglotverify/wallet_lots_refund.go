package fundinglotverify

import (
	"math/big"
	"strconv"
)

type wlRefundEdgePlan struct {
	lot    *wlLotState
	charge *wlChargeEdge
	amount big.Int
	index  uint64
}

func (w *wlWork) replayWalletRefund(op WalletLotsOperationRow, opRef wlRef, source *wlSource, pool map[string]*wlLotState) {
	s := w.settlements[source.settlementID]
	if s == nil || s.tenant != source.tenant || source.amount.Sign() <= 0 || (op.Kind == "legacy_consumption_refund") != s.legacy {
		w.mark(&w.out.Mismatches.RefundRows, source.ref)
		w.taint(source.tenant)
		return
	}
	nextRefunded := new(big.Int).Add(&s.refunded, &source.amount)
	if nextRefunded.Cmp(&s.charged) > 0 {
		w.mark(&w.out.Mismatches.RefundRows, source.ref)
		w.mark(&w.out.Mismatches.RefundOrderRows, source.ref)
		w.taint(source.tenant)
		return
	}
	if s.legacy {
		lot := w.newWalletOrigin(source.tenant, "legacy_consumption_refund", "LegacyRefundOrderID", source.id, "nonrefundable", &source.amount, op.CreatedAt, source.ref, pool)
		if lot == nil {
			return
		}
		plan := &wlRefundEdgePlan{lot: lot, index: 1}
		plan.amount.Set(&source.amount)
		w.matchWalletRefundEdges(op, source, []*wlRefundEdgePlan{plan}, true)
		s.refunded.Set(nextRefunded)
		w.applyWalletEffects(op, opRef, source, []wlEffect{wlEffectFor(lot, &source.amount, nil, nil, &source.amount)})
		return
	}

	remaining := new(big.Int).Set(&source.amount)
	plans := make([]*wlRefundEdgePlan, 0)
	for i := len(s.edges) - 1; i >= 0 && remaining.Sign() > 0; i-- {
		if !w.tick() {
			return
		}
		edge := s.edges[i]
		available := new(big.Int).Sub(&edge.charged, &edge.refunded)
		if available.Sign() < 0 {
			w.mark(&w.out.Mismatches.RefundRows, source.ref)
			w.taint(source.tenant)
			return
		}
		if available.Sign() == 0 {
			continue
		}
		q := wlMinimum(available, remaining)
		if edge.reserve.lot.s.Cmp(q) < 0 {
			w.mark(&w.out.Mismatches.RefundRows, source.ref)
			w.taint(source.tenant)
			return
		}
		plan := &wlRefundEdgePlan{lot: edge.reserve.lot, charge: edge, index: uint64(len(plans) + 1)}
		plan.amount.Set(q)
		plans = append(plans, plan)
		remaining.Sub(remaining, q)
	}
	if remaining.Sign() != 0 {
		w.mark(&w.out.Mismatches.RefundRows, source.ref)
		w.mark(&w.out.Mismatches.RefundOrderRows, source.ref)
		w.taint(source.tenant)
		return
	}

	// Only now is the complete reverse allocation known. Neither persisted
	// charged values nor current refund SUMs participate in this decision.
	w.matchWalletRefundEdges(op, source, plans, false)
	effects := make([]wlEffect, 0, len(plans))
	for _, plan := range plans {
		if !w.tick() {
			return
		}
		plan.charge.refunded.Add(&plan.charge.refunded, &plan.amount)
		effects = append(effects, wlEffectFor(plan.lot, &plan.amount, nil, wlNegative(&plan.amount), nil))
	}
	s.refunded.Set(nextRefunded)
	w.applyWalletEffects(op, opRef, source, effects)
}

func (w *wlWork) matchWalletRefundEdges(op WalletLotsOperationRow, source *wlSource, plans []*wlRefundEdgePlan, legacy bool) {
	refs := w.by("RefundLots", "RefundOrderID", source.id)
	actual := make(map[string][]wlRef)
	for _, ref := range refs {
		if !w.tick() {
			return
		}
		w.claim(ref)
		if row, ok := wlAt[WalletLotsRefundLotRow](w, ref); ok {
			actual[row.AllocationIndex] = append(actual[row.AllocationIndex], ref)
		}
	}
	matched := make(map[wlRef]bool)
	for _, plan := range plans {
		if !w.tick() {
			return
		}
		key := strconv.FormatUint(plan.index, 10)
		candidates := actual[key]
		if len(candidates) != 1 {
			w.mark(&w.out.Mismatches.RefundOrderRows, source.ref)
			if len(candidates) == 0 {
				w.need("refund_lot", wlReplayKey(source.id, key))
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
		row, _ := wlAt[WalletLotsRefundLotRow](w, ref)
		q, amountOK := w.amount(row.Amount, true, ref)
		bad := row.TenantID != source.tenant || row.LotID != plan.lot.id || !amountOK || q.Cmp(&plan.amount) != 0 || row.CreatedXID != op.CreatedXID
		if legacy {
			bad = bad || row.Origin != "legacy_settlement" || row.SettlementLotID != nil
		} else {
			bad = bad || row.Origin != "allocated" || row.SettlementLotID == nil
			if plan.charge.known {
				bad = bad || row.SettlementLotID != nil && *row.SettlementLotID != plan.charge.id
			}
		}
		if bad {
			w.mark(&w.out.Mismatches.RefundOrderRows, ref)
			w.mark(&w.out.Mismatches.RefundRows, ref)
		}
		w.time(row.CreatedAt, ref)
		if !legacy && !plan.charge.known {
			w.deferFact(ref)
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
			w.mark(&w.out.Mismatches.RefundOrderRows, ref)
		}
	}
}

// Current approved-refund SUM is a terminal comparison, never the remaining
// refundable amount of an earlier sequence step.
func (w *wlWork) finishWalletRefundTotals() {
	type approvedTotal struct {
		sum      big.Int
		complete bool
	}
	// Build every supplied approval sum once. Repeated settlement IDs must
	// neither select one parent claim nor rescan the same refund group N times.
	totals := make(map[string]*approvedTotal)
	for i, refund := range w.in.Refunds {
		if !w.tick() {
			return
		}
		if refund.State != "approved" {
			continue
		}
		total := totals[refund.SettlementID]
		if total == nil {
			total = &approvedTotal{complete: true}
			totals[refund.SettlementID] = total
		}
		amount, ok := w.amount(refund.Amount, true, wlRef{table: "Refunds", index: i})
		if ok {
			total.sum.Add(&total.sum, amount)
		} else {
			total.complete = false
		}
	}
	for i, settlement := range w.in.Settlements {
		if !w.tick() {
			return
		}
		ref := wlRef{table: "Settlements", index: i}
		charged, cok := w.amount(settlement.ChargedAmount, false, ref)
		refunded, rok := w.amount(settlement.RefundedAmount, false, ref)
		total := totals[settlement.ID]
		if total == nil {
			total = &approvedTotal{complete: true}
		}
		if cok && rok && total.complete && (total.sum.Cmp(refunded) != 0 || total.sum.Cmp(charged) > 0) {
			w.mark(&w.out.Mismatches.RefundRows, ref)
		}
		_, _, unique := wlGet[WalletLotsSettlementRow](w, "Settlements", settlement.ID)
		if expected := w.settlements[settlement.ID]; unique && expected != nil && !w.tainted[settlement.TenantID] && rok && expected.refunded.Cmp(refunded) != 0 {
			w.mark(&w.out.Mismatches.RefundRows, ref)
		}
	}
}
