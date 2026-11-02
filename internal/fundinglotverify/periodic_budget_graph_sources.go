package fundinglotverify

import (
	"math/big"
	"sort"
)

func (w *pbGraphWork) sourceBad(o pbGraphObject) {
	w.mark(&w.out.Mismatches.SourceIdentityMismatches, o)
}
func (w *pbGraphWork) scopeSource(c PeriodicBudgetControlRow, r PeriodicBudgetRequestRow) bool {
	return c.TenantID != nil && *c.TenantID == r.TenantID && (c.Scope == "tenant" || c.Scope == "key" && pbString(c.KeyID, r.KeyID))
}
func (w *pbGraphWork) obligation(control, kind, id string, marker *string, unfinished, terminal bool) {
	if control == "" {
		return
	}
	key := pbGraphSubject{control, kind, id}
	aid, ok := w.subjects[key]
	ok = ok && w.active(pbObj("allocations", aid))
	o := pbGraphObject{kind, control, id}
	// Finding identities use a typed object; extra is not parsed as a key.
	if (pbString(marker, control) || unfinished) && !ok {
		w.mark(&w.out.Mismatches.MissingAllocations, o)
	} else if marker == nil && terminal && !ok {
		w.mark(&w.out.Unproven.LegacyMembershipUnproven, o)
	}
}
func (w *pbGraphWork) sources() {
	m := &w.out.Mismatches
	tracked := map[string]bool{}
	for _, a := range w.allocs {
		if !w.tick() {
			return
		}
		if a.SubjectType == "customer_request" {
			tracked[a.SubjectID] = true
		}
	}
	for id, k := range w.keys {
		if !w.tick() {
			return
		}
		if _, ok := w.tenants[k.TenantID]; !ok {
			w.sourceBad(pbObj("keys", id))
		}
	}
	for id, r := range w.requests {
		if !w.tick() {
			return
		}
		o := pbObj("requests", id)
		k, kok := w.keys[r.KeyID]
		_, tok := w.tenants[r.TenantID]
		if !kok || k.TenantID != r.TenantID || !tok {
			w.sourceBad(o)
		}
		if (r.CreatedXID == nil) != pbNullTime(r.AdmittedAt) || r.CreatedXID == nil && (r.TenantControl != nil || r.KeyControl != nil) {
			w.sourceBad(o)
		}
		for _, s := range []struct {
			kind   string
			marker *string
		}{{"tenant", r.TenantControl}, {"key", r.KeyControl}} {
			if s.marker != nil {
				c, ok := w.controls[*s.marker]
				if !ok || !w.active(pbObj("controls", c.ID)) || c.Scope != s.kind || !w.scopeSource(c, r) {
					w.sourceBad(o)
				}
			}
		}
		v, ok := w.reservation(id)
		if !ok || !w.active(pbObj("reservations", v.ID)) || v.TenantID != r.TenantID {
			if r.CreatedXID != nil || r.TenantControl != nil || r.KeyControl != nil || tracked[id] {
				w.sourceBad(o)
			}
			continue
		}
		unfinished := pbEnum(v.Status, "reserved", "pending_reconciliation")
		terminal := pbEnum(v.Status, "settled", "released")
		w.obligation(w.scopes[pbGraphPair{"tenant", r.TenantID}], "customer_request", id, r.TenantControl, unfinished, terminal)
		w.obligation(w.scopes[pbGraphPair{"key", r.KeyID}], "customer_request", id, r.KeyControl, unfinished, terminal)
	}
	for id, v := range w.reservations {
		if !w.tick() {
			return
		}
		o := pbObj("reservations", id)
		if !w.active(o) {
			continue
		}
		r, ok := w.requests[v.RequestID]
		s, sok := w.settlement(v.RequestID)
		sok = sok && w.active(pbObj("settlements", s.ID))
		if !ok || r.TenantID != v.TenantID || pbEnum(v.Status, "reserved", "pending_reconciliation") && sok || pbEnum(v.Status, "settled", "released") && (!sok || s.TenantID != v.TenantID || s.Status != v.Status) {
			w.sourceBad(o)
		}
	}
	for id, s := range w.settlements {
		if !w.tick() {
			return
		}
		o := pbObj("settlements", id)
		if !w.active(o) {
			continue
		}
		r, rok := w.requests[s.RequestID]
		v, vok := w.reservation(s.RequestID)
		if !rok || r.TenantID != s.TenantID || !vok || v.TenantID != s.TenantID || v.Status != s.Status || s.Status == "released" && s.ChargedAmount != "0" {
			w.sourceBad(o)
		}
	}
	for id, b := range w.bindings {
		if !w.tick() {
			return
		}
		if _, ok := w.accounts[b.AccountID]; !ok {
			w.sourceBad(pbObj("bindings", id))
		}
	}
	for id, a := range w.attempts {
		if !w.tick() {
			return
		}
		o := pbObj("attempts", id)
		r, ok := w.requests[a.RequestID]
		if !ok || r.TenantID != a.TenantID {
			w.sourceBad(o)
		}
		if a.DeliveryState == "prepared" && !pbNullTime(a.DispatchCheckedAt) || a.DeliveryState == "sending" && a.CreatedXID != nil && !pbFinite(a.DispatchCheckedAt) || !pbNullTime(a.DispatchCheckedAt) && (!pbFinite(a.DispatchCheckedAt) || !ok || r.TenantID != a.TenantID) {
			w.mark(&m.DispatchMetadataMismatches, o)
		}
	}
	accountSources, accountEvents := map[string]*pbGraphAmount{}, map[string]*pbGraphAmount{}
	for id, p := range w.procurement {
		if !w.tick() {
			return
		}
		o := pbObj("procurement", id)
		a, aok := w.attempts[id]
		r, rok := w.requests[a.RequestID]
		b, bok := w.bindings[p.BindingID]
		_, qok := w.accounts[p.AccountID]
		if !aok || !rok || r.TenantID != a.TenantID || !bok || !w.active(pbObj("bindings", b.ID)) || !qok || b.AccountID != p.AccountID || b.CredentialID != a.CredentialID || b.ChannelID != a.ChannelID || !pbSame(p.CreatedXID, a.CreatedXID) || (p.CreatedXID == nil) != pbNullTime(p.AdmittedAt) || p.CreatedXID == nil && p.ControlID != nil {
			w.sourceBad(o)
		}
		if p.ControlID != nil {
			c, ok := w.controls[*p.ControlID]
			if !ok || c.Scope != "procurement" || !pbString(c.AccountID, p.AccountID) {
				w.sourceBad(o)
			}
		}
		w.obligation(w.scopes[pbGraphPair{"procurement", p.AccountID}], "supplier_attempt", id, p.ControlID, pbEnum(p.State, "prepared", "sending", "unknown", "disputed"), pbEnum(p.State, "settled", "released"))
		h, s := w.amount(p.HeldAmount, false, false, o), w.amount(p.SpentAmount, false, false, o)
		pbAdd(accountSources, p.AccountID, h, s)
		var eh, es big.Int
		valid := true
		reserved, canonical := 0, 0
		for _, e := range w.sourcePE[id] {
			if !w.tick() {
				return
			}
			if e.AccountID != p.AccountID {
				continue
			}
			eo := pbObj("procurement_events", e.ID)
			dh, ds := w.amount(e.HeldDelta, false, true, eo), w.amount(e.SpentDelta, false, true, eo)
			if dh == nil || ds == nil {
				valid = false
			} else {
				eh.Add(&eh, dh)
				es.Add(&es, ds)
			}
			if e.Type == "reserved" {
				reserved++
				if e.EventKey == "reserve:"+id && e.HeldDelta == p.UpperAmount && e.SpentDelta == "0" {
					canonical++
				}
			}
		}
		bad := reserved != 1 || canonical != 1 || pbEnum(p.State, "prepared", "sending") && (p.HeldAmount != p.UpperAmount || p.SpentAmount != "0" || p.Anomaly) || pbEnum(p.State, "settled", "released") && p.HeldAmount != "0" || p.State == "released" && p.SpentAmount != "0"
		if valid && (h != nil && h.Cmp(&eh) != 0 || s != nil && s.Cmp(&es) != 0) {
			bad = true
		}
		if bad {
			w.mark(&m.ProcurementSourceMismatches, o)
		}
	}
	for id, e := range w.pe {
		if !w.tick() {
			return
		}
		o := pbObj("procurement_events", id)
		if !w.active(o) {
			continue
		}
		p, ok := w.procurement[e.AttemptID]
		if !ok || p.AccountID != e.AccountID {
			w.sourceBad(o)
		}
		pbAdd(accountEvents, e.AccountID, w.amount(e.HeldDelta, false, true, o), w.amount(e.SpentDelta, false, true, o))
	}
	for id, a := range w.accounts {
		if !w.tick() {
			return
		}
		o := pbObj("accounts", id)
		for _, sums := range []map[string]*pbGraphAmount{accountSources, accountEvents} {
			s := sums[id]
			if s == nil {
				s = &pbGraphAmount{valid: true}
			}
			if s.valid && (!pbEqual(&s.held, a.HeldAmount) || !pbEqual(&s.spent, a.SpentAmount)) {
				w.mark(&m.ProcurementSourceMismatches, o)
			}
		}
	}
}

func (w *pbGraphWork) ordered(events []PeriodicBudgetEventRow) []PeriodicBudgetEventRow {
	out := append([]PeriodicBudgetEventRow(nil), events...)
	sort.Slice(out, func(i, j int) bool {
		w.tick()
		a, aok := pbCanonical(out[i].Seq, false, 19, "9223372036854775807")
		b, bok := pbCanonical(out[j].Seq, false, 19, "9223372036854775807")
		if aok && bok && a.Cmp(b) != 0 {
			return a.Cmp(b) < 0
		}
		if aok != bok {
			return aok
		}
		return out[i].ID < out[j].ID
	})
	return out
}
func (w *pbGraphWork) wantEffect(a PeriodicBudgetAllocationRow, kind, id string) {
	eid, ok := w.effects[pbGraphSubject{a.ControlID, kind, id}]
	e := w.events[eid]
	if !ok || !w.active(pbObj("events", eid)) || e.AllocationID != a.ID {
		w.mark(&w.out.Mismatches.MissingEffects, pbGraphObject{kind, a.ID, id})
	}
}
func (w *pbGraphWork) boundPE(a PeriodicBudgetAllocationRow, e PeriodicBudgetEventRow) (PeriodicBudgetProcurementEventRow, bool) {
	p, ok := w.pe[e.SourceID]
	c, cok := w.controls[a.ControlID]
	return p, ok && cok && w.active(pbObj("procurement_events", p.ID)) && a.SubjectType == "supplier_attempt" && e.SourceKind == "procurement_event" && p.AttemptID == a.SubjectID && pbString(c.AccountID, p.AccountID) && p.CreatedXID != nil && *p.CreatedXID == e.CreatedXID
}
func (w *pbGraphWork) allocations() {
	m := &w.out.Mismatches
	for id, a := range w.allocs {
		if !w.tick() {
			return
		}
		o := pbObj("allocations", id)
		if !w.active(o) {
			continue
		}
		c, cok := w.controls[a.ControlID]
		orphan := !cok || !w.active(pbObj("controls", c.ID))
		var marker *string
		var at PeriodicBudgetGraphTime
		var stamp *string
		var upper string
		if a.SubjectType == "customer_request" {
			r, rok := w.requests[a.SubjectID]
			v, vok := w.reservation(a.SubjectID)
			orphan = orphan || !rok || !vok || v.TenantID != r.TenantID || !w.scopeSource(c, r)
			upper = v.Amount
			at = r.AdmittedAt
			stamp = r.CreatedXID
			if c.Scope == "tenant" {
				marker = r.TenantControl
			} else {
				marker = r.KeyControl
			}
			w.wantEffect(a, map[bool]string{true: "reservation", false: "legacy_import"}[a.Origin == "new"], map[bool]string{true: v.ID, false: a.SubjectID}[a.Origin == "new"])
			if sid, ok := w.requestSettlements[a.SubjectID]; ok {
				w.wantEffect(a, "settlement", sid)
			}
			if pbEnum(v.Status, "reserved", "pending_reconciliation") {
				if a.HeldAmount != upper || a.SourceHeld != upper || a.SpentAmount != "0" || a.SourceSpent != "0" || a.State != "held" {
					w.mark(&m.AllocationSourceMismatches, o)
				}
			} else if s, ok := w.settlement(a.SubjectID); ok {
				if a.HeldAmount != "0" || a.SourceHeld != "0" || a.SpentAmount != s.ChargedAmount || a.SourceSpent != s.ChargedAmount || a.State != s.Status {
					w.mark(&m.AllocationSourceMismatches, o)
				}
			}
		} else if a.SubjectType == "supplier_attempt" {
			p, ok := w.procurement[a.SubjectID]
			orphan = orphan || !ok || c.Scope != "procurement" || !pbString(c.AccountID, p.AccountID)
			marker = p.ControlID
			at = p.AdmittedAt
			stamp = p.CreatedXID
			upper = p.UpperAmount
			base, sp := w.amount(a.Baseline, false, false, o), w.amount(p.SpentAmount, false, false, pbObj("procurement", p.AttemptID))
			if a.HeldAmount != p.HeldAmount || a.SourceHeld != p.HeldAmount || a.SourceSpent != p.SpentAmount || a.State != pbState(p.State) || base != nil && sp != nil && !pbEqual(pbPositivePart(sp, base), a.SpentAmount) {
				w.mark(&m.AllocationSourceMismatches, o)
			}
			if a.Origin == "new" {
				for _, pe := range w.sourcePE[a.SubjectID] {
					if !w.tick() {
						return
					}
					if pbString(c.AccountID, pe.AccountID) {
						w.wantEffect(a, "procurement_event", pe.ID)
					}
				}
			} else if a.Origin == "legacy_import" {
				w.wantEffect(a, "legacy_import", a.SubjectID)
			} else {
				w.wantEffect(a, "procurement_event", a.OriginSourceID)
			}
		} else {
			orphan = true
		}
		if orphan {
			w.mark(&m.OrphanAllocations, o)
		}
		es := w.ordered(w.allocationEvents[id])
		if w.err != nil {
			return
		}
		bad := len(es) == 0 || a.UpperAmount != upper
		var first PeriodicBudgetEventRow
		if len(es) > 0 {
			first = es[0]
			bad = bad || first.ControlID != a.ControlID || first.AllocationBeforeRevision != "0" || first.BeforeHeld != "0" || first.BeforeSpent != "0" || first.CreatedXID != a.CreatedXID
		}
		if a.Origin == "new" {
			win, ok := w.windows[func() string {
				if a.WindowID != nil {
					return *a.WindowID
				}
				return ""
			}()]
			bad = bad || a.WindowID == nil || !ok || win.ControlID != a.ControlID || !pbContains(win, a.AdmittedAt) || !pbTimeEq(a.AdmittedAt, at) || !pbString(marker, a.ControlID) || a.Baseline != "0" || stamp == nil || !pbString(stamp, a.CreatedXID)
			if a.SubjectType == "customer_request" {
				v, _ := w.reservation(a.SubjectID)
				bad = bad || a.OriginSourceID != v.ID || first.SourceKind != "reservation" || first.SourceID != v.ID || first.AfterHeld != upper || first.AfterSpent != "0" || first.AfterSourceSpent != "0" || first.AfterState != "held"
			} else {
				pe, ok := w.boundPE(a, first)
				attempt := w.attempts[a.SubjectID]
				bad = bad || !ok || first.SourceID != a.OriginSourceID || pe.Type != "reserved" || pe.EventKey != "reserve:"+a.SubjectID || pe.HeldDelta != upper || pe.SpentDelta != "0" || first.AfterHeld != upper || first.AfterSpent != "0" || first.AfterSourceSpent != "0" || first.AfterState != "held" || !pbString(attempt.CreatedXID, a.CreatedXID)
			}
		} else {
			bad = bad || marker != nil || a.WindowID != nil || !pbNullTime(a.AdmittedAt)
			if a.Origin == "legacy_import" {
				bad = bad || a.OriginSourceID != a.SubjectID || first.SourceKind != "legacy_import" || first.SourceID != a.SubjectID || a.CreatedXID != c.CreatedXID || first.AfterSpent != "0" || first.AfterSourceSpent != a.Baseline
				if a.SubjectType == "customer_request" {
					bad = bad || a.Baseline != "0" || first.AfterHeld != upper || first.AfterState != "held"
				} else {
					bad = bad || !pbEnum(first.AfterState, "held", "disputed")
				}
			} else if a.Origin == "legacy_catchup" {
				_, ok := w.boundPE(a, first)
				bad = bad || a.SubjectType != "supplier_attempt" || !ok || first.SourceID != a.OriginSourceID
			} else {
				bad = true
			}
		}
		if a.SubjectType == "customer_request" && (a.Baseline != "0" || a.State == "disputed" || a.Origin == "legacy_catchup") {
			bad = true
		}
		if bad {
			w.mark(&m.AllocationOriginMismatches, o)
		}
		if a.SubjectType == "supplier_attempt" {
			w.residual(a, first)
		}
	}
	for id, e := range w.events {
		if !w.tick() {
			return
		}
		o := pbObj("events", id)
		if !w.active(o) {
			continue
		}
		a, aok := w.allocs[e.AllocationID]
		_, cok := w.controls[e.ControlID]
		orphan := !aok || !cok || a.ControlID != e.ControlID || !w.active(pbObj("allocations", a.ID))
		bad := false
		switch e.SourceKind {
		case "reservation":
			v, ok := w.reservations[e.SourceID]
			orphan = orphan || !ok
			bad = !ok || a.SubjectType != "customer_request" || a.Origin != "new" || v.RequestID != a.SubjectID || a.OriginSourceID != v.ID
		case "settlement":
			s, ok := w.settlements[e.SourceID]
			orphan = orphan || !ok
			bad = !ok || a.SubjectType != "customer_request" || s.RequestID != a.SubjectID
		case "legacy_import":
			if a.SubjectType == "customer_request" {
				_, ok := w.requests[e.SourceID]
				orphan = orphan || !ok
			} else {
				_, ok := w.procurement[e.SourceID]
				orphan = orphan || !ok
			}
			bad = a.Origin != "legacy_import" || e.SourceID != a.SubjectID
		case "procurement_event":
			_, ok := w.pe[e.SourceID]
			orphan = orphan || !ok
			_, bound := w.boundPE(a, e)
			bad = !bound
		default:
			orphan = true
			bad = true
		}
		if orphan {
			w.mark(&m.OrphanEffects, o)
		}
		if bad {
			w.mark(&m.EffectBindingMismatches, o)
		}
	}
}
func (w *pbGraphWork) residual(a PeriodicBudgetAllocationRow, first PeriodicBudgetEventRow) {
	o := pbObj("allocations", a.ID)
	c := w.controls[a.ControlID]
	mirrored := map[string]bool{}
	for _, e := range w.allocationEvents[a.ID] {
		if !w.tick() {
			return
		}
		if e.ControlID != a.ControlID {
			continue
		}
		p, ok := w.boundPE(a, e)
		if ok {
			mirrored[p.ID] = true
		}
	}
	var h, s big.Int
	valid := true
	count := 0
	for _, e := range w.sourcePE[a.SubjectID] {
		if !w.tick() {
			return
		}
		if !pbString(c.AccountID, e.AccountID) || mirrored[e.ID] {
			continue
		}
		count++
		eo := pbObj("procurement_events", e.ID)
		dh, ds := w.amount(e.HeldDelta, false, true, eo), w.amount(e.SpentDelta, false, true, eo)
		if dh == nil || ds == nil {
			valid = false
		} else {
			h.Add(&h, dh)
			s.Add(&s, ds)
		}
	}
	bad := false
	switch a.Origin {
	case "new":
		bad = count != 0
	case "legacy_import":
		if valid {
			bad = !pbEqual(&h, first.AfterHeld) || !pbEqual(&s, a.Baseline)
		}
	case "legacy_catchup":
		if valid {
			bad = h.Sign() != 0 || !pbEqual(&s, a.Baseline)
		}
		pe, ok := w.pe[a.OriginSourceID]
		if ok {
			eo := pbObj("procurement_events", pe.ID)
			dh, ds := w.amount(pe.HeldDelta, false, true, eo), w.amount(pe.SpentDelta, false, true, eo)
			ah, as := w.amount(first.AfterHeld, false, false, pbObj("events", first.ID)), w.amount(first.AfterSourceSpent, false, false, pbObj("events", first.ID))
			base := w.amount(a.Baseline, false, false, o)
			if dh != nil && ds != nil && ah != nil && as != nil && base != nil {
				bad = bad || new(big.Int).Sub(ah, dh).Sign() != 0 || new(big.Int).Sub(as, ds).Cmp(base) != 0
			}
		}
	}
	if bad {
		w.mark(&w.out.Mismatches.LegacyBaselineMismatches, o)
	}
	if count > 0 && pbEnum(a.Origin, "legacy_import", "legacy_catchup") {
		w.mark(&w.out.Unproven.LegacyEffectBoundaryUnproven, o)
	}
}

func (w *pbGraphWork) reservation(request string) (CustomerKeyReservationRow, bool) {
	id, ok := w.requestReservations[request]
	if !ok {
		return CustomerKeyReservationRow{}, false
	}
	v, ok := w.reservations[id]
	return v, ok && v.RequestID == request && w.active(pbObj("reservations", id))
}
func (w *pbGraphWork) settlement(request string) (PeriodicBudgetSettlementRow, bool) {
	id, ok := w.requestSettlements[request]
	if !ok {
		return PeriodicBudgetSettlementRow{}, false
	}
	v, ok := w.settlements[id]
	return v, ok && v.RequestID == request && w.active(pbObj("settlements", id))
}
