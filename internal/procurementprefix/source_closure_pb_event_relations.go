package procurementprefix

import (
	"math/big"
	"strconv"
	"time"
)

// This leaf independently reads originals. It never qualifies a source or uses
// another replay's expected values. Legacy residuals prove arithmetic only.
type sourcePBEventRef struct {
	table string
	raw   sourceRawRow
}

type sourcePBEventBucket struct {
	held, spent big.Int
	n           uint64
	tail        string
	valid       bool
}

type sourcePBEventAllocation struct {
	ref                        sourcePBEventRef
	row                        PeriodicBudgetAllocationRow
	control                    PeriodicBudgetControlRow
	request                    RequestRecordRow
	reservation                ReservationRow
	settlement                 *SettlementRow
	procurement                ProcurementReservationRow
	base, held, spent, source  big.Int
	state                      string
	revision                   uint64
	tail                       string
	valid                      bool
	initialHeld, residualSpent big.Int
}

type sourcePBEventRelations struct {
	s             *sourceClosureState
	err           error
	index         map[string]map[string][]sourceRawRow
	scopes        map[string][]sourceRawRow
	windowStarts  map[string][]sourceRawRow
	subjects      map[string][]sourceRawRow
	seqs, effects map[string][]sourceRawRow
	reservations  map[string][]sourceRawRow
	settlements   map[string][]sourceRawRow
	peKeys        map[string][]sourceRawRow
	pe            map[string][]sourceRawRow
	controlEvents map[string][]sourceRawRow
	allocEvents   map[string][]sourceRawRow
	allocations   map[string]*sourcePBEventAllocation
	controlGood   map[string]bool
	windowGood    map[string]bool
	controlRun    map[string]*sourcePBEventBucket
	windowRun     map[string]*sourcePBEventBucket
	eventAnchored map[string]bool
	controlBroken map[string]bool
}

func (s *sourceClosureState) pbEventRelationRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	p := &sourcePBEventRelations{s: s, index: map[string]map[string][]sourceRawRow{}, scopes: map[string][]sourceRawRow{}, windowStarts: map[string][]sourceRawRow{}, subjects: map[string][]sourceRawRow{}, seqs: map[string][]sourceRawRow{}, effects: map[string][]sourceRawRow{}, reservations: map[string][]sourceRawRow{}, settlements: map[string][]sourceRawRow{}, peKeys: map[string][]sourceRawRow{}, pe: map[string][]sourceRawRow{}, controlEvents: map[string][]sourceRawRow{}, allocEvents: map[string][]sourceRawRow{}, allocations: map[string]*sourcePBEventAllocation{}, controlGood: map[string]bool{}, windowGood: map[string]bool{}, controlRun: map[string]*sourcePBEventBucket{}, windowRun: map[string]*sourcePBEventBucket{}, eventAnchored: map[string]bool{}, controlBroken: map[string]bool{}}
	p.prepare()
	p.controlsAndWindows()
	p.anchorEvents()
	p.allocationOrigins()
	p.sourceBindings()
	p.forwardSources()
	p.replay()
	p.projections()
	if p.err != nil {
		return p.err
	}
	return s.w.b.check()
}

func (p *sourcePBEventRelations) tick() bool {
	if p.err == nil {
		p.err = p.s.w.tick()
	}
	return p.err == nil
}

func (p *sourcePBEventRelations) bad(ref sourcePBEventRef) {
	if ref.table != "" && p.tick() {
		p.s.mark("pointer", ref.table, ref.raw.key)
	}
}

func (p *sourcePBEventRelations) badRows(table string, rows []sourceRawRow) {
	for _, raw := range rows {
		if !p.tick() {
			return
		}
		p.bad(sourcePBEventRef{table, raw})
	}
}

func (p *sourcePBEventRelations) one(table, id string) (sourceRawRow, bool) {
	rows := p.index[table][tupleKey(id)]
	return sourcePBEventOnly(rows)
}

func sourcePBEventOnly(rows []sourceRawRow) (sourceRawRow, bool) {
	if len(rows) != 1 {
		return sourceRawRow{}, false
	}
	return rows[0], true
}

func sourcePBEventScope(c PeriodicBudgetControlRow) (string, bool) {
	switch c.Scope {
	case "tenant":
		if c.TenantID != nil && c.KeyID == nil && c.AccountID == nil {
			return *c.TenantID, true
		}
	case "key":
		if c.TenantID != nil && c.KeyID != nil && c.AccountID == nil {
			return *c.KeyID, true
		}
	case "procurement":
		if c.AccountID != nil && c.TenantID == nil && c.KeyID == nil {
			return *c.AccountID, true
		}
	}
	return "", false
}

func sourcePBEventStart(kind string, at Time) (time.Time, bool) {
	if !finite(at) || !sourceOneOf(kind, "utc_day", "utc_month") {
		return time.Time{}, false
	}
	t := at.Time.UTC()
	day := t.Day()
	if kind == "utc_month" {
		day = 1
	}
	return time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, time.UTC), true
}

func sourcePBEventEnd(kind string, start time.Time) time.Time {
	if kind == "utc_month" {
		return start.AddDate(0, 1, 0)
	}
	return start.AddDate(0, 0, 1)
}

func sourcePBEventTimeKey(at Time) string {
	if !finite(at) {
		return "invalid"
	}
	return at.Time.UTC().Format(time.RFC3339Nano)
}

func (p *sourcePBEventRelations) number(text string, signed, small bool) (*big.Int, bool) {
	if !p.tick() {
		return nil, false
	}
	if small && !unsignedInteger(text, 63, false) {
		return nil, false
	}
	return numeric(text, signed)
}

func sourcePBEventFits(n *big.Int) bool { return n != nil && n.Sign() >= 0 && len(n.String()) <= 100 }

func sourcePBEventPositive(source, base *big.Int) *big.Int {
	n := new(big.Int).Sub(source, base)
	if n.Sign() < 0 {
		n.SetInt64(0)
	}
	return n
}

func sourcePBEventState(kind string) string {
	switch kind {
	case "prepared", "sending", "unknown", "reserved", "consumption_unknown", "recovered_unknown":
		return "held"
	case "settled", "cost_confirmed", "cost_corrected":
		return "settled"
	case "released", "released_no_consumption", "expired_without_permit":
		return "released"
	case "disputed", "evidence_disputed":
		return "disputed"
	}
	return ""
}

func sourcePBEventTransition(before, after string) bool {
	switch before {
	case "held":
		return sourceOneOf(after, "held", "settled", "released", "disputed")
	case "settled":
		return sourceOneOf(after, "settled", "disputed")
	case "released":
		return sourceOneOf(after, "released", "settled", "disputed")
	case "disputed":
		return sourceOneOf(after, "disputed", "settled")
	}
	return false
}

func (p *sourcePBEventRelations) prepare() {
	for table, rows := range p.s.tables {
		p.index[table] = map[string][]sourceRawRow{}
		for _, raw := range rows {
			if !p.tick() {
				return
			}
			p.index[table][raw.key] = append(p.index[table][raw.key], raw)
			switch table {
			case "periodic_budget_controls":
				c := raw.value.Interface().(PeriodicBudgetControlRow)
				if target, ok := sourcePBEventScope(c); ok {
					key := tupleKey(c.Scope, target)
					p.scopes[key] = append(p.scopes[key], raw)
				}
			case "periodic_budget_windows":
				w := raw.value.Interface().(PeriodicBudgetWindowRow)
				key := tupleKey(w.ControlID, sourcePBEventTimeKey(w.StartAt))
				p.windowStarts[key] = append(p.windowStarts[key], raw)
			case "periodic_budget_allocations":
				a := raw.value.Interface().(PeriodicBudgetAllocationRow)
				key := tupleKey(a.ControlID, a.SubjectType, a.SubjectID)
				p.subjects[key] = append(p.subjects[key], raw)
			case "periodic_budget_events":
				// Group only after immutable allocation/control anchors are known.
			case "reservations":
				id := sourceText(raw.value, "request_id")
				p.reservations[id] = append(p.reservations[id], raw)
			case "settlements":
				id := sourceText(raw.value, "request_id")
				p.settlements[id] = append(p.settlements[id], raw)
			case "procurement_budget_events":
				e := raw.value.Interface().(ProcurementBudgetEventRow)
				p.pe[e.AttemptID] = append(p.pe[e.AttemptID], raw)
				p.peKeys[e.EventKey] = append(p.peKeys[e.EventKey], raw)
			}
		}
	}
}

func (p *sourcePBEventRelations) anchorEvents() {
	for _, raw := range p.s.tables["periodic_budget_events"] {
		if !p.tick() {
			return
		}
		e := raw.value.Interface().(PeriodicBudgetEventRow)
		ar, found := p.one("periodic_budget_allocations", e.AllocationID)
		_, control := p.one("periodic_budget_controls", e.ControlID)
		valid := found && control && len(p.index["periodic_budget_events"][raw.key]) == 1
		if found {
			a := ar.value.Interface().(PeriodicBudgetAllocationRow)
			valid = valid && a.ControlID == e.ControlID && len(p.subjects[tupleKey(a.ControlID, a.SubjectType, a.SubjectID)]) == 1
		}
		p.eventAnchored[raw.key] = valid
		if !valid {
			p.bad(sourcePBEventRef{"periodic_budget_events", raw})
			p.badRows("periodic_budget_allocations", p.index["periodic_budget_allocations"][tupleKey(e.AllocationID)])
			p.controlBroken[e.ControlID] = true
			continue
		}
		key := tupleKey(e.ControlID, e.Seq)
		p.seqs[key] = append(p.seqs[key], raw)
		key = tupleKey(e.ControlID, e.SourceKind, e.SourceID)
		p.effects[key] = append(p.effects[key], raw)
		p.controlEvents[e.ControlID] = append(p.controlEvents[e.ControlID], raw)
		p.allocEvents[e.AllocationID] = append(p.allocEvents[e.AllocationID], raw)
	}
}

func (p *sourcePBEventRelations) controlsAndWindows() {
	for _, raw := range p.s.tables["periodic_budget_controls"] {
		if !p.tick() {
			return
		}
		c := raw.value.Interface().(PeriodicBudgetControlRow)
		target, shape := sourcePBEventScope(c)
		valid := shape && len(p.index["periodic_budget_controls"][raw.key]) == 1 && len(p.scopes[tupleKey(c.Scope, target)]) == 1
		switch c.Scope {
		case "tenant":
			_, found := p.one("tenants", target)
			valid = valid && found
		case "key":
			k, found := p.one("api_keys", target)
			valid = valid && found
			if found {
				key := k.value.Interface().(APIKeyRow)
				_, tenant := p.one("tenants", key.TenantID)
				valid = valid && tenant && c.TenantID != nil && *c.TenantID == key.TenantID
			}
		case "procurement":
			_, found := p.one("procurement_accounts", target)
			valid = valid && found
		default:
			valid = false
		}
		start, calendar := sourcePBEventStart(c.PeriodKind, c.TrackingStartedAt)
		valid = valid && calendar && finite(c.EffectiveFrom) && c.EffectiveFrom.Time.Equal(sourcePBEventEnd(c.PeriodKind, start))
		p.controlGood[c.ID] = valid
		if !valid {
			p.bad(sourcePBEventRef{"periodic_budget_controls", raw})
		}
	}
	for _, raw := range p.s.tables["periodic_budget_windows"] {
		if !p.tick() {
			return
		}
		w := raw.value.Interface().(PeriodicBudgetWindowRow)
		cr, found := p.one("periodic_budget_controls", w.ControlID)
		valid := found && p.controlGood[w.ControlID] && len(p.index["periodic_budget_windows"][raw.key]) == 1 && len(p.windowStarts[tupleKey(w.ControlID, sourcePBEventTimeKey(w.StartAt))]) == 1
		if found {
			c := cr.value.Interface().(PeriodicBudgetControlRow)
			start, ok := sourcePBEventStart(c.PeriodKind, w.StartAt)
			tracking, tok := sourcePBEventStart(c.PeriodKind, c.TrackingStartedAt)
			valid = valid && ok && tok && w.StartAt.Time.Equal(start) && !start.Before(tracking) && finite(w.EndAt) && w.EndAt.Time.Equal(sourcePBEventEnd(c.PeriodKind, start))
		}
		p.windowGood[w.ID] = valid
		if !valid {
			p.bad(sourcePBEventRef{"periodic_budget_windows", raw})
		}
	}
}

func (p *sourcePBEventRelations) customer(id string) (RequestRecordRow, ReservationRow, *SettlementRow, bool) {
	rr, rok := p.one("request_records", id)
	vr, vok := sourcePBEventOnly(p.reservations[id])
	if !rok || !vok {
		p.badRows("request_records", p.index["request_records"][tupleKey(id)])
		p.badRows("reservations", p.reservations[id])
		return RequestRecordRow{}, ReservationRow{}, nil, false
	}
	r, v := rr.value.Interface().(RequestRecordRow), vr.value.Interface().(ReservationRow)
	k, kok := p.one("api_keys", r.KeyID)
	_, tok := p.one("tenants", r.TenantID)
	_, vu := p.one("reservations", v.ID)
	valid := kok && tok && vu && v.TenantID == r.TenantID && (r.PBCreatedXID == nil) == nullTime(r.PBAdmittedAt) && (r.PBCreatedXID != nil || r.PBTenantControl == nil && r.PBKeyControl == nil)
	if kok {
		valid = valid && k.value.Interface().(APIKeyRow).TenantID == r.TenantID
	}
	if r.PBCreatedXID != nil {
		valid = valid && finite(r.PBAdmittedAt)
	}
	for _, item := range []struct {
		scope  string
		marker *string
	}{{"tenant", r.PBTenantControl}, {"key", r.PBKeyControl}} {
		if !p.tick() {
			return r, v, nil, false
		}
		if item.marker != nil {
			cr, found := p.one("periodic_budget_controls", *item.marker)
			good := found
			if found {
				c := cr.value.Interface().(PeriodicBudgetControlRow)
				good = p.controlGood[c.ID] && c.Scope == item.scope && c.TenantID != nil && *c.TenantID == r.TenantID && (item.scope != "key" || c.KeyID != nil && *c.KeyID == r.KeyID)
			}
			valid = valid && good
		}
	}
	var settlement *SettlementRow
	ss := p.settlements[id]
	switch v.Status {
	case "reserved", "pending_reconciliation":
		valid = valid && len(ss) == 0
	case "settled", "released":
		valid = valid && len(ss) == 1
		if len(ss) == 1 {
			s := ss[0].value.Interface().(SettlementRow)
			_, unique := p.one("settlements", s.ID)
			_, amount := p.number(s.ChargedAmount, false, true)
			valid = valid && unique && amount && s.TenantID == r.TenantID && s.RequestID == id && s.Status == v.Status && (s.Status != "released" || s.ChargedAmount == "0")
			settlement = &s
		}
	default:
		valid = false
	}
	_, amount := p.number(v.Amount, false, true)
	valid = valid && amount
	if !valid {
		p.bad(sourcePBEventRef{"request_records", rr})
		p.bad(sourcePBEventRef{"reservations", vr})
		p.badRows("settlements", ss)
	}
	return r, v, settlement, valid
}

func (p *sourcePBEventRelations) supplier(id string) (ProcurementReservationRow, bool) {
	pr, pok := p.one("procurement_reservations", id)
	ar, aok := p.one("upstream_attempts", id)
	if !pok || !aok {
		p.badRows("procurement_reservations", p.index["procurement_reservations"][tupleKey(id)])
		p.badRows("upstream_attempts", p.index["upstream_attempts"][tupleKey(id)])
		return ProcurementReservationRow{}, false
	}
	s, a := pr.value.Interface().(ProcurementReservationRow), ar.value.Interface().(UpstreamAttemptRow)
	rr, rok := p.one("request_records", a.RequestID)
	br, bok := p.one("credential_procurement_bindings", s.BindingID)
	_, account := p.one("procurement_accounts", s.AccountID)
	valid := rok && bok && account && sourceSameOptional(s.PBCreatedXID, a.PBCreatedXID) && (s.PBCreatedXID == nil) == nullTime(s.PBAdmittedAt) && (s.PBCreatedXID != nil || s.PBControl == nil) && sourceOneOf(s.State, "prepared", "sending", "unknown", "disputed", "settled", "released")
	if rok {
		valid = valid && rr.value.Interface().(RequestRecordRow).TenantID == a.TenantID
	}
	if bok {
		b := br.value.Interface().(CredentialProcurementBindingRow)
		valid = valid && b.AccountID == s.AccountID && b.CredentialID == a.CredentialID && b.ChannelID == a.ChannelID
	}
	if s.PBCreatedXID != nil {
		valid = valid && finite(s.PBAdmittedAt)
	}
	if s.PBControl != nil {
		cr, found := p.one("periodic_budget_controls", *s.PBControl)
		valid = valid && found
		if found {
			c := cr.value.Interface().(PeriodicBudgetControlRow)
			valid = valid && p.controlGood[c.ID] && c.Scope == "procurement" && c.AccountID != nil && *c.AccountID == s.AccountID
		}
	}
	for _, field := range []struct {
		value string
		small bool
	}{{s.UpperAmount, true}, {s.HeldAmount, false}, {s.SpentAmount, false}} {
		_, ok := p.number(field.value, false, field.small)
		valid = valid && ok
	}
	if !valid {
		p.bad(sourcePBEventRef{"procurement_reservations", pr})
		p.bad(sourcePBEventRef{"upstream_attempts", ar})
	}
	return s, valid
}

func (p *sourcePBEventRelations) boundPE(a *sourcePBEventAllocation, raw sourceRawRow) (ProcurementBudgetEventRow, bool) {
	e := raw.value.Interface().(PeriodicBudgetEventRow)
	pr, found := p.one("procurement_budget_events", e.SourceID)
	if !found {
		return ProcurementBudgetEventRow{}, false
	}
	pe := pr.value.Interface().(ProcurementBudgetEventRow)
	valid := len(p.peKeys[pe.EventKey]) == 1 && a.row.SubjectType == "supplier_attempt" && a.control.Scope == "procurement" && a.control.AccountID != nil && *a.control.AccountID == pe.AccountID && pe.AttemptID == a.row.SubjectID && sourceSameOptional(pe.PBCreatedXID, &e.CreatedXID) && sourceOneOf(pe.Type, "reserved", "consumption_unknown", "recovered_unknown", "released_no_consumption", "expired_without_permit", "cost_confirmed", "cost_corrected", "evidence_disputed") && e.ControlID == a.row.ControlID && e.AllocationID == a.row.ID && e.SourceKind == "procurement_event" && p.eventAnchored[raw.key] && unsignedInteger(e.Seq, 63, true) && len(p.seqs[tupleKey(e.ControlID, e.Seq)]) == 1 && len(p.effects[tupleKey(e.ControlID, e.SourceKind, e.SourceID)]) == 1
	return pe, valid
}

func (p *sourcePBEventRelations) residual(a *sourcePBEventAllocation) bool {
	mirrored := map[string]bool{}
	for _, raw := range p.allocEvents[a.row.ID] {
		if !p.tick() {
			return false
		}
		e := raw.value.Interface().(PeriodicBudgetEventRow)
		if e.SourceKind == "procurement_event" {
			if pe, bound := p.boundPE(a, raw); bound {
				mirrored[pe.ID] = true
			}
		}
	}
	var h, s big.Int
	valid, count := true, 0
	for _, raw := range p.pe[a.row.SubjectID] {
		if !p.tick() {
			return false
		}
		pe := raw.value.Interface().(ProcurementBudgetEventRow)
		_, unique := p.one("procurement_budget_events", pe.ID)
		if pe.AccountID != a.procurement.AccountID || !unique || len(p.peKeys[pe.EventKey]) != 1 {
			p.bad(sourcePBEventRef{"procurement_budget_events", raw})
			valid = false
			continue
		}
		if mirrored[pe.ID] {
			continue
		}
		count++
		dh, hok := p.number(pe.HeldDelta, true, false)
		ds, sok := p.number(pe.SpentDelta, true, false)
		if !hok || !sok {
			valid = false
			p.bad(sourcePBEventRef{"procurement_budget_events", raw})
			continue
		}
		h.Add(&h, dh)
		s.Add(&s, ds)
		if a.row.Origin == "new" {
			p.bad(sourcePBEventRef{"procurement_budget_events", raw})
		}
	}
	a.initialHeld.Set(&h)
	a.residualSpent.Set(&s)
	switch a.row.Origin {
	case "new":
		valid = valid && count == 0
	case "legacy_import":
		valid = valid && sourcePBEventFits(&h) && s.Cmp(&a.base) == 0
	case "legacy_catchup":
		valid = valid && h.Sign() == 0 && s.Cmp(&a.base) == 0
	}
	return valid
}

func (p *sourcePBEventRelations) allocationOrigins() {
	for _, raw := range p.s.tables["periodic_budget_allocations"] {
		if !p.tick() {
			return
		}
		a := raw.value.Interface().(PeriodicBudgetAllocationRow)
		ref := sourcePBEventRef{"periodic_budget_allocations", raw}
		if len(p.index[ref.table][raw.key]) != 1 || len(p.subjects[tupleKey(a.ControlID, a.SubjectType, a.SubjectID)]) != 1 {
			p.bad(ref)
			continue
		}
		x := &sourcePBEventAllocation{ref: ref, row: a, state: "held"}
		p.allocations[a.ID] = x
		cr, found := p.one("periodic_budget_controls", a.ControlID)
		x.valid = found && p.controlGood[a.ControlID]
		if found {
			x.control = cr.value.Interface().(PeriodicBudgetControlRow)
		}
		base, amount := p.number(a.Baseline, false, false)
		if amount {
			x.base.Set(base)
			x.source.Set(base)
		}
		x.valid = x.valid && amount
		var marker, stamp *string
		var admitted Time
		upper := ""
		switch a.SubjectType {
		case "customer_request":
			r, v, settlement, good := p.customer(a.SubjectID)
			x.request, x.reservation, x.settlement = r, v, settlement
			x.valid = x.valid && good && x.control.TenantID != nil && *x.control.TenantID == r.TenantID && (x.control.Scope == "tenant" || x.control.Scope == "key" && x.control.KeyID != nil && *x.control.KeyID == r.KeyID) && a.Baseline == "0"
			marker = r.PBTenantControl
			if x.control.Scope == "key" {
				marker = r.PBKeyControl
			}
			stamp, admitted, upper = r.PBCreatedXID, r.PBAdmittedAt, v.Amount
		case "supplier_attempt":
			s, good := p.supplier(a.SubjectID)
			x.procurement = s
			x.valid = x.valid && good && x.control.Scope == "procurement" && x.control.AccountID != nil && *x.control.AccountID == s.AccountID
			marker, stamp, admitted, upper = s.PBControl, s.PBCreatedXID, s.PBAdmittedAt, s.UpperAmount
		default:
			x.valid = false
		}
		x.valid = x.valid && a.UpperAmount == upper && len(p.allocEvents[a.ID]) > 0
		switch a.Origin {
		case "new":
			x.valid = x.valid && a.Baseline == "0" && sourceSameOptional(marker, &a.ControlID) && sourceSameOptional(stamp, &a.CreatedXID) && finite(a.AdmittedAt) && finite(admitted) && a.AdmittedAt.Time.Equal(admitted.Time) && a.WindowID != nil
			if a.WindowID != nil {
				wr, good := p.one("periodic_budget_windows", *a.WindowID)
				x.valid = x.valid && good && p.windowGood[*a.WindowID]
				if good {
					w := wr.value.Interface().(PeriodicBudgetWindowRow)
					x.valid = x.valid && w.ControlID == a.ControlID && !a.AdmittedAt.Time.Before(w.StartAt.Time) && a.AdmittedAt.Time.Before(w.EndAt.Time)
				}
			}
			if a.SubjectType == "customer_request" {
				x.valid = x.valid && a.OriginSourceID == x.reservation.ID
			} else if a.SubjectType == "supplier_attempt" {
				ar, good := p.one("upstream_attempts", a.SubjectID)
				x.valid = x.valid && good
				if good {
					x.valid = x.valid && sourceSameOptional(ar.value.Interface().(UpstreamAttemptRow).PBCreatedXID, &a.CreatedXID)
				}
			}
		case "legacy_import":
			x.valid = x.valid && marker == nil && a.WindowID == nil && nullTime(a.AdmittedAt) && a.OriginSourceID == a.SubjectID && a.CreatedXID == x.control.CreatedXID
		case "legacy_catchup":
			x.valid = x.valid && a.SubjectType == "supplier_attempt" && marker == nil && a.WindowID == nil && nullTime(a.AdmittedAt)
		default:
			x.valid = false
		}
		if a.SubjectType == "supplier_attempt" {
			x.valid = p.residual(x) && x.valid
		}
		if !x.valid {
			p.bad(ref)
		}
	}
}

func (p *sourcePBEventRelations) required(source sourcePBEventRef, control sourceRawRow, kind, subject string, marker *string, unfinished bool) {
	c := control.value.Interface().(PeriodicBudgetControlRow)
	rows := p.subjects[tupleKey(c.ID, kind, subject)]
	if (sourceSameOptional(marker, &c.ID) || unfinished) && (len(rows) != 1 || len(p.index["periodic_budget_allocations"][rows[0].key]) != 1) {
		p.bad(source)
		p.bad(sourcePBEventRef{"periodic_budget_controls", control})
		p.badRows("periodic_budget_allocations", rows)
	}
}

func (p *sourcePBEventRelations) sourceBindings() {
	for _, raw := range p.s.tables["periodic_budget_events"] {
		if !p.tick() {
			return
		}
		e := raw.value.Interface().(PeriodicBudgetEventRow)
		a := p.allocations[e.AllocationID]
		table, valid := "", a != nil && p.eventAnchored[raw.key]
		switch e.SourceKind {
		case "reservation":
			table = "reservations"
			r, found := p.one(table, e.SourceID)
			valid = valid && found
			if valid {
				v := r.value.Interface().(ReservationRow)
				valid = a.row.SubjectType == "customer_request" && a.row.Origin == "new" && a.row.OriginSourceID == e.SourceID && v.RequestID == a.row.SubjectID && v.TenantID == a.request.TenantID && sourceSameOptional(a.request.PBCreatedXID, &e.CreatedXID)
			}
		case "settlement":
			table = "settlements"
			r, found := p.one(table, e.SourceID)
			valid = valid && found
			if valid {
				s := r.value.Interface().(SettlementRow)
				jr, journal := p.one("journals", s.JournalID)
				valid = a.row.SubjectType == "customer_request" && s.RequestID == a.row.SubjectID && s.TenantID == a.request.TenantID && journal
				if journal {
					j := jr.value.Interface().(JournalRow)
					bridge := j.CreatedTxid == e.CreatedXID && j.OperationID == s.ID && j.TenantID == s.TenantID
					if !bridge {
						p.bad(sourcePBEventRef{"journals", jr})
					}
					valid = valid && bridge
				}
			}
		case "procurement_event":
			table = "procurement_budget_events"
			if valid {
				_, valid = p.boundPE(a, raw)
			}
		case "legacy_import":
			if a != nil {
				table = "request_records"
				if a.row.SubjectType == "supplier_attempt" {
					table = "procurement_reservations"
				}
				_, found := p.one(table, e.SourceID)
				valid = valid && found && a.row.Origin == "legacy_import" && e.SourceID == a.row.SubjectID
			}
		default:
			valid = false
		}
		if !valid {
			p.bad(sourcePBEventRef{"periodic_budget_events", raw})
			if a != nil {
				p.bad(a.ref)
			}
			if table != "" {
				p.badRows(table, p.index[table][tupleKey(e.SourceID)])
			}
		}
	}
}

func (p *sourcePBEventRelations) forwardSources() {
	for _, raw := range p.s.tables["request_records"] {
		if !p.tick() {
			return
		}
		r := raw.value.Interface().(RequestRecordRow)
		relevant := r.PBTenantControl != nil || r.PBKeyControl != nil || len(p.scopes[tupleKey("tenant", r.TenantID)]) > 0 || len(p.scopes[tupleKey("key", r.KeyID)]) > 0
		if !relevant {
			continue
		}
		_, v, _, valid := p.customer(r.ID)
		if !valid {
			p.bad(sourcePBEventRef{"request_records", raw})
		}
		for _, item := range []struct {
			kind, target string
			marker       *string
		}{{"tenant", r.TenantID, r.PBTenantControl}, {"key", r.KeyID, r.PBKeyControl}} {
			if !p.tick() {
				return
			}
			cs := p.scopes[tupleKey(item.kind, item.target)]
			if len(cs) == 1 {
				p.required(sourcePBEventRef{"request_records", raw}, cs[0], "customer_request", r.ID, item.marker, sourceOneOf(v.Status, "reserved", "pending_reconciliation"))
			} else if len(cs) > 1 {
				p.bad(sourcePBEventRef{"request_records", raw})
				p.badRows("periodic_budget_controls", cs)
			}
		}
	}
	for _, raw := range p.s.tables["procurement_reservations"] {
		if !p.tick() {
			return
		}
		s := raw.value.Interface().(ProcurementReservationRow)
		cs := p.scopes[tupleKey("procurement", s.AccountID)]
		if s.PBControl == nil && len(cs) == 0 {
			continue
		}
		_, valid := p.supplier(s.AttemptID)
		if !valid {
			p.bad(sourcePBEventRef{"procurement_reservations", raw})
		}
		if len(cs) == 1 {
			p.required(sourcePBEventRef{"procurement_reservations", raw}, cs[0], "supplier_attempt", s.AttemptID, s.PBControl, sourceOneOf(s.State, "prepared", "sending", "unknown", "disputed"))
		} else if len(cs) > 1 {
			p.bad(sourcePBEventRef{"procurement_reservations", raw})
			p.badRows("periodic_budget_controls", cs)
		}
	}
	for _, raw := range p.s.tables["procurement_budget_events"] {
		if !p.tick() {
			return
		}
		e := raw.value.Interface().(ProcurementBudgetEventRow)
		pr, found := p.one("procurement_reservations", e.AttemptID)
		relevant := len(p.scopes[tupleKey("procurement", e.AccountID)]) > 0
		if found {
			s := pr.value.Interface().(ProcurementReservationRow)
			relevant = relevant || s.PBControl != nil || len(p.scopes[tupleKey("procurement", s.AccountID)]) > 0
		}
		if relevant {
			valid := found && len(p.index["procurement_budget_events"][raw.key]) == 1 && len(p.peKeys[e.EventKey]) == 1
			if found {
				valid = valid && pr.value.Interface().(ProcurementReservationRow).AccountID == e.AccountID
			}
			if !valid {
				p.bad(sourcePBEventRef{"procurement_budget_events", raw})
			}
		}
	}
}

func sourcePBEventBucketFor(m map[string]*sourcePBEventBucket, id string) *sourcePBEventBucket {
	b := m[id]
	if b == nil {
		b = &sourcePBEventBucket{valid: true}
		m[id] = b
	}
	return b
}

func (p *sourcePBEventRelations) expected(a *sourcePBEventAllocation, raw sourceRawRow) (*big.Int, *big.Int, string, bool) {
	e := raw.value.Interface().(PeriodicBudgetEventRow)
	h, s := new(big.Int).Set(&a.held), new(big.Int).Set(&a.source)
	state, valid := "", true
	switch e.SourceKind {
	case "reservation":
		valid = a.row.SubjectType == "customer_request" && a.row.Origin == "new" && a.revision == 0 && e.SourceID == a.reservation.ID && a.row.OriginSourceID == e.SourceID
		h, _ = p.number(a.reservation.Amount, false, true)
		s.SetInt64(0)
		state = "held"
	case "legacy_import":
		valid = a.row.Origin == "legacy_import" && a.revision == 0 && e.SourceID == a.row.SubjectID
		s.Set(&a.base)
		if a.row.SubjectType == "customer_request" {
			h, _ = p.number(a.reservation.Amount, false, true)
			state = "held"
		} else {
			h.Set(&a.initialHeld)
			state = e.AfterState // Historical held/disputed was saved, not reconstructible from today's p.
			valid = valid && sourceOneOf(state, "held", "disputed")
		}
	case "settlement":
		valid = a.row.SubjectType == "customer_request" && a.revision == 1 && a.state == "held" && a.settlement != nil && e.SourceID == a.settlement.ID
		if a.settlement != nil {
			s, _ = p.number(a.settlement.ChargedAmount, false, true)
			state = a.settlement.Status
			jr, found := p.one("journals", a.settlement.JournalID)
			valid = valid && found
			if found {
				j := jr.value.Interface().(JournalRow)
				valid = valid && j.CreatedTxid == e.CreatedXID && j.TenantID == a.request.TenantID && j.OperationID == a.settlement.ID
			}
		}
		h.SetInt64(0)
	case "procurement_event":
		pe, bound := p.boundPE(a, raw)
		dh, hok := p.number(pe.HeldDelta, true, false)
		ds, sok := p.number(pe.SpentDelta, true, false)
		valid = bound && hok && sok && !(a.revision == 0 && a.row.Origin == "legacy_import")
		if hok && sok {
			h.Add(h, dh)
			s.Add(s, ds)
		}
		state = sourcePBEventState(pe.Type)
		if a.revision == 0 {
			valid = valid && e.SourceID == a.row.OriginSourceID
			if a.row.Origin == "new" {
				valid = valid && pe.Type == "reserved" && pe.EventKey == "reserve:"+a.row.SubjectID && pe.HeldDelta == a.row.UpperAmount && pe.SpentDelta == "0"
			}
		}
	default:
		valid = false
	}
	if a.revision == 0 {
		valid = valid && e.CreatedXID == a.row.CreatedXID
	}
	if a.row.SubjectType == "customer_request" && a.revision > 1 || a.row.SubjectType == "supplier_attempt" && a.revision > 0 && !sourcePBEventTransition(a.state, state) {
		valid = false
	}
	return h, s, state, valid && sourcePBEventFits(h) && sourcePBEventFits(s)
}

func (p *sourcePBEventRelations) replay() {
	for cid, rows := range p.controlEvents {
		if !p.tick() {
			return
		}
		ordered := append([]sourceRawRow(nil), rows...)
		if err := replaySort(p.s.w, ordered, func(a, b sourceRawRow) bool {
			x, xok := strconv.ParseUint(sourceText(a.value, "seq"), 10, 63)
			y, yok := strconv.ParseUint(sourceText(b.value, "seq"), 10, 63)
			if xok != nil || yok != nil {
				return xok == nil && yok != nil
			}
			return x < y
		}); err != nil {
			p.err = err
			return
		}
		cb := sourcePBEventBucketFor(p.controlRun, cid)
		for i, raw := range ordered {
			if !p.tick() {
				return
			}
			e := raw.value.Interface().(PeriodicBudgetEventRow)
			ref := sourcePBEventRef{"periodic_budget_events", raw}
			a := p.allocations[e.AllocationID]
			identity := len(p.index[ref.table][raw.key]) == 1 && len(p.seqs[tupleKey(cid, e.Seq)]) == 1 && len(p.effects[tupleKey(cid, e.SourceKind, e.SourceID)]) == 1 && a != nil && a.row.ControlID == cid && p.controlGood[cid]
			order := unsignedInteger(e.Seq, 63, true) && e.Seq == strconv.Itoa(i+1)
			if !identity || !order {
				p.bad(ref)
				cb.valid = false
				if a != nil {
					a.valid = false
					p.bad(a.ref)
				}
			}
			cb.n++
			cb.tail = e.ID
			if a == nil {
				continue
			}
			var wb *sourcePBEventBucket
			if p.windowOf(a.row) {
				wb = sourcePBEventBucketFor(p.windowRun, *a.row.WindowID)
			}
			valid := identity && order && a.valid
			windowRevision := a.row.WindowID == nil && e.WindowBeforeRevision == nil || wb != nil && sourceSameOptional(e.WindowBeforeRevision, sourcePBEventText(strconv.FormatUint(wb.n, 10)))
			if e.AllocationBeforeRevision != strconv.FormatUint(a.revision, 10) || !windowRevision {
				p.bad(ref)
			}
			if valid {
				h, source, state, derived := p.expected(a, raw)
				valid = derived
				if derived {
					spent := sourcePBEventPositive(source, &a.base)
					dh, ds := new(big.Int).Sub(h, &a.held), new(big.Int).Sub(spent, &a.spent)
					if e.BeforeHeld != a.held.String() || e.BeforeSpent != a.spent.String() || e.AfterHeld != h.String() || e.AfterSpent != spent.String() || e.AfterSourceSpent != source.String() || e.AfterState != state || e.HeldDelta != dh.String() || e.SpentDelta != ds.String() {
						p.bad(ref)
					}
					cb.held.Add(&cb.held, dh)
					cb.spent.Add(&cb.spent, ds)
					if wb != nil {
						wb.held.Add(&wb.held, dh)
						wb.spent.Add(&wb.spent, ds)
					}
					a.held.Set(h)
					a.spent.Set(spent)
					a.source.Set(source)
					a.state = state
				}
			}
			if !valid {
				p.bad(ref)
				p.bad(a.ref)
				a.valid, cb.valid = false, false
				if wb != nil {
					wb.valid = false
				}
			}
			if !sourcePBEventFits(&cb.held) || !sourcePBEventFits(&cb.spent) || wb != nil && (!sourcePBEventFits(&wb.held) || !sourcePBEventFits(&wb.spent)) {
				p.bad(ref)
				cb.valid = false
				if wb != nil {
					wb.valid = false
				}
			}
			a.revision++
			a.tail = e.ID
			if wb != nil {
				wb.n++
				wb.tail = e.ID
			}
		}
	}
}

func sourcePBEventText(s string) *string { return &s }

func (p *sourcePBEventRelations) windowOf(a PeriodicBudgetAllocationRow) bool {
	if a.WindowID == nil {
		return false
	}
	wr, ok := p.one("periodic_budget_windows", *a.WindowID)
	return ok && p.windowGood[*a.WindowID] && wr.value.Interface().(PeriodicBudgetWindowRow).ControlID == a.ControlID
}

func (p *sourcePBEventRelations) projections() {
	ca, wa, ce, we := map[string]*sourcePBEventBucket{}, map[string]*sourcePBEventBucket{}, map[string]*sourcePBEventBucket{}, map[string]*sourcePBEventBucket{}
	for _, raw := range p.s.tables["periodic_budget_allocations"] {
		if !p.tick() {
			return
		}
		a := raw.value.Interface().(PeriodicBudgetAllocationRow)
		x := p.allocations[a.ID]
		valid := x != nil && x.valid && a.Revision == strconv.FormatUint(x.revision, 10) && sourceSameOptional(a.LastEventID, sourcePBEventText(x.tail)) && a.HeldAmount == x.held.String() && a.SpentAmount == x.spent.String() && a.SourceHeld == x.held.String() && a.SourceSpent == x.source.String() && a.State == x.state
		if x != nil {
			if a.SubjectType == "customer_request" {
				if x.settlement != nil {
					valid = valid && a.HeldAmount == "0" && a.SourceHeld == "0" && a.SpentAmount == x.settlement.ChargedAmount && a.SourceSpent == x.settlement.ChargedAmount && a.State == x.settlement.Status
				} else {
					valid = valid && a.HeldAmount == x.reservation.Amount && a.SourceHeld == x.reservation.Amount && a.SpentAmount == "0" && a.SourceSpent == "0" && a.State == "held"
				}
			} else {
				sp, ok := p.number(x.procurement.SpentAmount, false, false)
				valid = valid && ok && a.HeldAmount == x.procurement.HeldAmount && a.SourceHeld == x.procurement.HeldAmount && a.SourceSpent == x.procurement.SpentAmount && a.State == sourcePBEventState(x.procurement.State)
				if ok {
					valid = valid && a.SpentAmount == sourcePBEventPositive(sp, &x.base).String()
				}
			}
		}
		if !valid {
			p.bad(sourcePBEventRef{"periodic_budget_allocations", raw})
		}
		h, hok := p.number(a.HeldAmount, false, false)
		s, sok := p.number(a.SpentAmount, false, false)
		b := sourcePBEventBucketFor(ca, a.ControlID)
		b.valid = b.valid && hok && sok && x != nil && x.valid
		if hok && sok {
			b.held.Add(&b.held, h)
			b.spent.Add(&b.spent, s)
		}
		if a.WindowID != nil && p.windowOf(a) {
			b = sourcePBEventBucketFor(wa, *a.WindowID)
			b.valid = b.valid && hok && sok && x != nil && x.valid && p.windowGood[*a.WindowID]
			if hok && sok {
				b.held.Add(&b.held, h)
				b.spent.Add(&b.spent, s)
			}
		}
	}
	for _, raw := range p.s.tables["periodic_budget_events"] {
		if !p.tick() {
			return
		}
		e := raw.value.Interface().(PeriodicBudgetEventRow)
		if !p.eventAnchored[raw.key] {
			continue
		}
		h, hok := p.number(e.HeldDelta, true, false)
		s, sok := p.number(e.SpentDelta, true, false)
		b := sourcePBEventBucketFor(ce, e.ControlID)
		b.valid = b.valid && hok && sok && len(p.index["periodic_budget_events"][raw.key]) == 1
		if hok && sok {
			b.held.Add(&b.held, h)
			b.spent.Add(&b.spent, s)
		}
		if a := p.allocations[e.AllocationID]; a != nil && p.windowOf(a.row) {
			b = sourcePBEventBucketFor(we, *a.row.WindowID)
			b.valid = b.valid && hok && sok && e.ControlID == a.row.ControlID
			if hok && sok {
				b.held.Add(&b.held, h)
				b.spent.Add(&b.spent, s)
			}
		}
	}
	maxima, broken := map[string]time.Time{}, map[string]bool{}
	for _, raw := range p.s.tables["periodic_budget_windows"] {
		if !p.tick() {
			return
		}
		w := raw.value.Interface().(PeriodicBudgetWindowRow)
		valid := p.windowGood[w.ID]
		for _, m := range []map[string]*sourcePBEventBucket{wa, we, p.windowRun} {
			b := sourcePBEventBucketFor(m, w.ID)
			valid = valid && b.valid && w.HeldAmount == b.held.String() && w.SpentAmount == b.spent.String()
		}
		b := sourcePBEventBucketFor(p.windowRun, w.ID)
		valid = valid && w.Revision == strconv.FormatUint(b.n, 10) && (b.n == 0 && w.LastEventID == nil || b.n > 0 && sourceSameOptional(w.LastEventID, &b.tail))
		if !valid {
			p.bad(sourcePBEventRef{"periodic_budget_windows", raw})
		}
		if !p.windowGood[w.ID] {
			broken[w.ControlID] = true
		} else if current, ok := maxima[w.ControlID]; !ok || w.StartAt.Time.After(current) {
			maxima[w.ControlID] = w.StartAt.Time
		}
	}
	for _, raw := range p.s.tables["periodic_budget_controls"] {
		if !p.tick() {
			return
		}
		c := raw.value.Interface().(PeriodicBudgetControlRow)
		valid := p.controlGood[c.ID] && !broken[c.ID] && !p.controlBroken[c.ID]
		for _, m := range []map[string]*sourcePBEventBucket{ca, ce, p.controlRun} {
			b := sourcePBEventBucketFor(m, c.ID)
			valid = valid && b.valid && c.HeldAmount == b.held.String() && c.SpentAmount == b.spent.String()
		}
		b := sourcePBEventBucketFor(p.controlRun, c.ID)
		valid = valid && c.EventSeq == strconv.FormatUint(b.n, 10) && (b.n == 0 && c.LastEventID == nil || b.n > 0 && sourceSameOptional(c.LastEventID, &b.tail))
		start, ok := sourcePBEventStart(c.PeriodKind, c.TrackingStartedAt)
		if w, found := maxima[c.ID]; found && w.After(start) {
			start = w
		}
		valid = valid && ok && finite(c.MaxObservedWindowStart) && c.MaxObservedWindowStart.Time.Equal(start)
		if !valid {
			p.bad(sourcePBEventRef{"periodic_budget_controls", raw})
		}
	}
}
