package fundinglotverify

import (
	"math/big"
	"strconv"
	"time"
)

func pbTransition(before, after string) bool {
	switch before {
	case "held":
		return pbEnum(after, "held", "settled", "released", "disputed")
	case "settled":
		return pbEnum(after, "settled", "disputed")
	case "released":
		return pbEnum(after, "released", "settled", "disputed")
	case "disputed":
		return pbEnum(after, "disputed", "settled")
	}
	return false
}
func (w *pbGraphWork) initialHeld(a PeriodicBudgetAllocationRow) (*big.Int, bool) {
	var h big.Int
	for _, e := range w.sourcePE[a.SubjectID] {
		if !w.tick() {
			return nil, false
		}
		c := w.controls[a.ControlID]
		if !pbString(c.AccountID, e.AccountID) {
			continue
		}
		mirrored := false
		if eid, ok := w.effects[pbGraphSubject{a.ControlID, "procurement_event", e.ID}]; ok {
			be := w.events[eid]
			_, bound := w.boundPE(a, be)
			mirrored = bound && be.AllocationID == a.ID && w.active(pbObj("events", eid))
		}
		if mirrored {
			continue
		}
		v := w.amount(e.HeldDelta, false, true, pbObj("procurement_events", e.ID))
		if v == nil {
			return nil, false
		}
		h.Add(&h, v)
	}
	return &h, h.Sign() >= 0
}
func (w *pbGraphWork) replay() {
	m := &w.out.Mismatches
	for id, a := range w.allocs {
		if !w.tick() {
			return
		}
		o := pbObj("allocations", id)
		if !w.active(o) {
			continue
		}
		s := &pbGraphState{state: "held", valid: w.valid(o)}
		base := w.amount(a.Baseline, false, false, o)
		if base == nil {
			s.valid = false
		} else {
			s.source.Set(base)
		}
		w.states[id] = s
	}
	badOrder := map[string]bool{}
	for _, e := range w.events {
		if !w.tick() {
			return
		}
		if w.excluded[pbObj("events", e.ID)] {
			badOrder[e.ControlID] = true
		}
	}
	for cid, raw := range w.controlEvents {
		if !w.tick() {
			return
		}
		events := w.ordered(raw)
		if w.err != nil {
			return
		}
		seq := uint64(0)
		windowCounts := map[string]uint64{}
		orderValid := !badOrder[cid]
		for _, e := range events {
			if !w.tick() {
				return
			}
			seq++
			eo := pbObj("events", e.ID)
			a, aok := w.allocs[e.AllocationID]
			s := w.states[e.AllocationID]
			if e.Seq != strconv.FormatUint(seq, 10) {
				w.mark(&m.EventChainMismatches, eo)
				orderValid = false
			}
			if !aok || a.ControlID != cid || s == nil {
				continue
			}
			bad := false
			if !orderValid || !w.valid(eo) {
				s.valid = false
			}
			expectedRev := s.revision
			s.revision++
			s.tail = e.ID
			if e.AllocationBeforeRevision != strconv.FormatUint(expectedRev, 10) {
				bad = true
			}
			if a.WindowID == nil {
				if e.WindowBeforeRevision != nil {
					bad = true
				}
			} else {
				n := windowCounts[*a.WindowID]
				if !pbString(e.WindowBeforeRevision, strconv.FormatUint(n, 10)) {
					bad = true
				}
				windowCounts[*a.WindowID] = n + 1
			}
			var ah, as big.Int
			state := ""
			derived := s.valid
			base := w.amount(a.Baseline, false, false, pbObj("allocations", a.ID))
			if base == nil {
				derived = false
			}
			if derived {
				ah.Set(&s.held)
				as.Set(&s.source)
				switch e.SourceKind {
				case "reservation":
					v, ok := w.reservations[e.SourceID]
					h := w.amount(v.Amount, true, false, pbObj("reservations", v.ID))
					if !ok || expectedRev != 0 || a.SubjectType != "customer_request" || a.Origin != "new" || v.RequestID != a.SubjectID || h == nil {
						derived = false
					} else {
						ah.Set(h)
						as.SetInt64(0)
						state = "held"
					}
				case "settlement":
					v, ok := w.settlements[e.SourceID]
					sp := w.amount(v.ChargedAmount, true, false, pbObj("settlements", v.ID))
					if !ok || expectedRev != 1 || a.SubjectType != "customer_request" || s.state != "held" || v.RequestID != a.SubjectID || sp == nil || !pbEnum(v.Status, "settled", "released") {
						derived = false
					} else {
						ah.SetInt64(0)
						as.Set(sp)
						state = v.Status
					}
				case "legacy_import":
					if expectedRev != 0 || a.Origin != "legacy_import" || e.SourceID != a.SubjectID {
						derived = false
					} else if a.SubjectType == "customer_request" {
						v, _ := w.reservation(a.SubjectID)
						h := w.amount(v.Amount, true, false, pbObj("reservations", v.ID))
						if h == nil {
							derived = false
						} else {
							ah.Set(h)
							as.SetInt64(0)
							state = "held"
						}
					} else {
						h, ok := w.initialHeld(a)
						if !ok || !pbEnum(e.AfterState, "held", "disputed") {
							derived = false
						} else {
							ah.Set(h)
							as.Set(base)
							state = e.AfterState
						}
					}
				case "procurement_event":
					p, bound := w.boundPE(a, e)
					ph, ps := w.amount(p.HeldDelta, false, true, pbObj("procurement_events", p.ID)), w.amount(p.SpentDelta, false, true, pbObj("procurement_events", p.ID))
					if !bound || ph == nil || ps == nil || pbState(p.Type) == "" || expectedRev == 0 && a.Origin == "legacy_import" {
						derived = false
					} else {
						ah.Add(&ah, ph)
						as.Add(&as, ps)
						state = pbState(p.Type)
						if expectedRev == 0 && a.Origin == "new" && (p.Type != "reserved" || p.HeldDelta != a.UpperAmount || p.SpentDelta != "0") {
							derived = false
						}
					}
				default:
					derived = false
				}
			}
			if !derived {
				if s.valid {
					w.mark(&m.EffectBindingMismatches, eo)
				}
				s.valid = false
				pbAdd(w.controlReplay, cid, nil, nil)
				if a.WindowID != nil {
					pbAdd(w.windowReplay, *a.WindowID, nil, nil)
				}
				if bad {
					w.mark(&m.EventChainMismatches, eo)
				}
				continue
			}
			sp := pbPositivePart(&as, base)
			dh := new(big.Int).Sub(&ah, &s.held)
			ds := new(big.Int).Sub(sp, &s.spent)
			if ah.Sign() < 0 || as.Sign() < 0 || !pbEqual(&s.held, e.BeforeHeld) || !pbEqual(&s.spent, e.BeforeSpent) || !pbEqual(&ah, e.AfterHeld) || !pbEqual(sp, e.AfterSpent) || !pbEqual(&as, e.AfterSourceSpent) || !pbEqual(dh, e.HeldDelta) || !pbEqual(ds, e.SpentDelta) || e.AfterState != state || expectedRev > 0 && a.SubjectType == "supplier_attempt" && !pbTransition(s.state, state) || a.SubjectType == "customer_request" && expectedRev > 1 {
				bad = true
			}
			if bad {
				w.mark(&m.EventChainMismatches, eo)
			}
			if ah.Sign() < 0 || as.Sign() < 0 {
				s.valid = false
				pbAdd(w.controlReplay, cid, nil, nil)
				if a.WindowID != nil {
					pbAdd(w.windowReplay, *a.WindowID, nil, nil)
				}
				continue
			}
			pbAdd(w.controlReplay, cid, dh, ds)
			if a.WindowID != nil {
				pbAdd(w.windowReplay, *a.WindowID, dh, ds)
			}
			s.held.Set(&ah)
			s.spent.Set(sp)
			s.source.Set(&as)
			s.state = state
		}
	}
}

func pbTail(pointer *string, count uint64, tail string) bool {
	if count == 0 {
		return pointer == nil
	}
	return pbString(pointer, tail)
}
func (w *pbGraphWork) projections() {
	m := &w.out.Mismatches
	ca, ce, wa, we := map[string]*pbGraphAmount{}, map[string]*pbGraphAmount{}, map[string]*pbGraphAmount{}, map[string]*pbGraphAmount{}
	for id, a := range w.allocs {
		if !w.tick() {
			return
		}
		o := pbObj("allocations", id)
		if !w.active(o) {
			continue
		}
		h, s := w.amount(a.HeldAmount, false, false, o), w.amount(a.SpentAmount, false, false, o)
		pbAdd(ca, a.ControlID, h, s)
		if a.WindowID != nil {
			pbAdd(wa, *a.WindowID, h, s)
		}
		st := w.states[id]
		n := uint64(len(w.allocationEvents[id]))
		bad := n == 0 || a.Revision != strconv.FormatUint(n, 10) || st == nil
		if st != nil {
			bad = bad || !pbTail(a.LastEventID, st.revision, st.tail)
			if st.valid {
				bad = bad || !pbEqual(&st.held, a.HeldAmount) || !pbEqual(&st.held, a.SourceHeld) || !pbEqual(&st.spent, a.SpentAmount) || !pbEqual(&st.source, a.SourceSpent) || st.state != a.State
			}
		}
		if bad {
			w.mark(&m.AllocationProjectionMismatches, o)
		}
	}
	for cid, raw := range w.controlEvents {
		events := w.ordered(raw)
		if w.err != nil {
			return
		}
		for _, e := range events {
			if !w.tick() {
				return
			}
			o := pbObj("events", e.ID)
			h, s := w.amount(e.HeldDelta, false, true, o), w.amount(e.SpentDelta, false, true, o)
			pbAdd(ce, cid, h, s)
			sum := ce[cid]
			sum.count++
			sum.tail = e.ID
			if a, ok := w.allocs[e.AllocationID]; ok && a.ControlID == cid && a.WindowID != nil {
				pbAdd(we, *a.WindowID, h, s)
				ws := we[*a.WindowID]
				ws.count++
				ws.tail = e.ID
			}
		}
	}
	for id, win := range w.windows {
		if !w.tick() {
			return
		}
		o := pbObj("windows", id)
		if !w.active(o) {
			continue
		}
		c, ok := w.controls[win.ControlID]
		bad := !ok || !pbPeriod(c.PeriodKind) || !pbFinite(win.StartAt) || !pbFinite(win.EndAt)
		if !bad {
			start := pbStart(c.PeriodKind, win.StartAt.Value)
			bad = !win.StartAt.Value.Equal(start) || !win.EndAt.Value.Equal(pbEnd(c.PeriodKind, start))
		}
		for _, sums := range []map[string]*pbGraphAmount{wa, we, w.windowReplay} {
			a := sums[id]
			if a == nil {
				a = &pbGraphAmount{valid: true}
			}
			if a.valid && (!pbEqual(&a.held, win.HeldAmount) || !pbEqual(&a.spent, win.SpentAmount)) {
				bad = true
			}
		}
		es := we[id]
		if es == nil {
			es = &pbGraphAmount{}
		}
		if win.Revision != strconv.FormatUint(es.count, 10) || !pbTail(win.LastEventID, es.count, es.tail) {
			bad = true
		}
		if bad {
			w.mark(&m.WindowMismatches, o)
		}
	}
	maxWindows := map[string]time.Time{}
	for _, win := range w.windows {
		if !w.tick() {
			return
		}
		if w.active(pbObj("windows", win.ID)) && pbFinite(win.StartAt) {
			prior, ok := maxWindows[win.ControlID]
			if !ok || win.StartAt.Value.After(prior) {
				maxWindows[win.ControlID] = win.StartAt.Value.UTC()
			}
		}
	}
	for id, c := range w.controls {
		if !w.tick() {
			return
		}
		o := pbObj("controls", id)
		if !w.active(o) {
			continue
		}
		bad := false
		for _, sums := range []map[string]*pbGraphAmount{ca, ce, w.controlReplay} {
			a := sums[id]
			if a == nil {
				a = &pbGraphAmount{valid: true}
			}
			if a.valid && (!pbEqual(&a.held, c.HeldAmount) || !pbEqual(&a.spent, c.SpentAmount)) {
				bad = true
			}
		}
		es := ce[id]
		if es == nil {
			es = &pbGraphAmount{}
		}
		if c.EventSeq != strconv.FormatUint(es.count, 10) || !pbTail(c.LastEventID, es.count, es.tail) {
			bad = true
		}
		if pbPeriod(c.PeriodKind) && pbFinite(c.TrackingStartedAt) && pbFinite(c.MaxObservedWindowStart) {
			max := pbStart(c.PeriodKind, c.TrackingStartedAt.Value)
			if other, ok := maxWindows[id]; ok && other.After(max) {
				max = other
			}
			if !c.MaxObservedWindowStart.Value.Equal(max) {
				bad = true
			}
		} else {
			bad = true
		}
		if bad {
			w.mark(&m.ControlMismatches, o)
		}
	}
}
