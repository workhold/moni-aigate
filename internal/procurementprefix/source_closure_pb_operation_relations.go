package procurementprefix

import (
	"strconv"
	"time"
)

// This leaf authenticates original configuration/observation relations only.
// In particular it does not certify dynamic snapshot amounts or business reads.
type sourcePBOperationRelations struct {
	s            *sourceClosureState
	err          error
	index        map[string]map[string][]sourceRawRow
	versions     map[string][]sourceRawRow
	versionOps   map[string][]sourceRawRow
	scopes       map[string][]sourceRawRow
	operations   map[string][]sourceRawRow
	windows      map[string][]sourceRawRow
	windowStarts map[string][]sourceRawRow
	journal      map[string][]sourceRawRow
	sources      map[string][]sourceRawRow
	eventSeq     map[string][]sourceRawRow
	audits       map[string][]sourceRawRow
	claimed      map[string]bool
	chainOK      map[string]bool
	births       map[string]sourceRawRow
	payloads     map[string]sourcePBRelationPayload
}

type sourcePBRelationPayload struct {
	input    sourcePBInput
	snapshot sourcePBSnapshot
	valid    bool
}

const (
	pbRelControls = "periodic_budget_controls"
	pbRelWindows  = "periodic_budget_windows"
	pbRelAllocs   = "periodic_budget_allocations"
	pbRelEvents   = "periodic_budget_events"
	pbRelOps      = "periodic_budget_operations"
	pbRelVersions = "periodic_budget_configuration_versions"
	pbRelOrigins  = "periodic_budget_observation_origins"
	pbRelHeads    = "periodic_budget_observation_heads"
	pbRelJournal  = "periodic_budget_observation_journal"
)

func (s *sourceClosureState) pbOperationRelations() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	p := &sourcePBOperationRelations{s: s, index: make(map[string]map[string][]sourceRawRow),
		versions: make(map[string][]sourceRawRow), versionOps: make(map[string][]sourceRawRow), scopes: make(map[string][]sourceRawRow),
		operations: make(map[string][]sourceRawRow), windows: make(map[string][]sourceRawRow), windowStarts: make(map[string][]sourceRawRow),
		journal: make(map[string][]sourceRawRow), sources: make(map[string][]sourceRawRow), eventSeq: make(map[string][]sourceRawRow),
		audits: make(map[string][]sourceRawRow), claimed: make(map[string]bool), chainOK: make(map[string]bool),
		births: make(map[string]sourceRawRow), payloads: make(map[string]sourcePBRelationPayload)}
	p.prepare()
	p.configuration()
	p.operationRows()
	p.auditRows()
	p.originsAndHeads()
	p.bindings()
	p.observationRows()
	p.chains()
	p.windowEdges()
	if p.err != nil {
		return p.err
	}
	return s.w.b.check()
}

func (p *sourcePBOperationRelations) tick() bool {
	if p.err == nil {
		p.err = p.s.w.tick()
	}
	return p.err == nil
}

func (p *sourcePBOperationRelations) bad(table string, raw sourceRawRow) {
	if p.tick() {
		p.s.mark("pointer", table, raw.key)
	}
}

func (p *sourcePBOperationRelations) both(table string, raw sourceRawRow, target string, rows []sourceRawRow) {
	p.bad(table, raw)
	for _, row := range rows {
		if !p.tick() {
			return
		}
		p.bad(target, row)
	}
}

func (p *sourcePBOperationRelations) unique(table string, raw sourceRawRow) bool {
	if len(p.index[table][raw.key]) != 1 {
		p.bad(table, raw)
		return false
	}
	return true
}

func (p *sourcePBOperationRelations) one(table string, parts ...string) (sourceRawRow, bool) {
	rows := p.index[table][tupleKey(parts...)]
	if !p.tick() || len(rows) != 1 {
		return sourceRawRow{}, false
	}
	return rows[0], true
}

func (p *sourcePBOperationRelations) control(id string) (sourceRawRow, bool) {
	raw, ok := p.one(pbRelControls, id)
	if !ok {
		return raw, false
	}
	c := raw.value.Interface().(PeriodicBudgetControlRow)
	target, _, _, _, shape := sourcePBRelTarget(c)
	return raw, shape && len(p.scopes[tupleKey(c.Scope, target)]) == 1
}

func (p *sourcePBOperationRelations) window(id string) (sourceRawRow, bool) {
	raw, ok := p.one(pbRelWindows, id)
	if !ok {
		return raw, false
	}
	w := raw.value.Interface().(PeriodicBudgetWindowRow)
	return raw, finite(w.StartAt) && len(p.windowStarts[sourcePBRelStart(w.ControlID, w.StartAt.Time)]) == 1
}

func (p *sourcePBOperationRelations) version(control, revision string) (sourceRawRow, bool) {
	raw, ok := p.one(pbRelVersions, control, revision)
	return raw, ok && len(p.versionOps[sourceText(raw.value, "operation_id")]) == 1
}

func (p *sourcePBOperationRelations) event(id string) (sourceRawRow, bool) {
	raw, ok := p.one(pbRelEvents, id)
	return raw, ok && len(p.eventSeq[tupleKey(sourceText(raw.value, "control_id"), sourceText(raw.value, "seq"))]) == 1
}

func sourcePBRelSeq(value string, positive bool) (uint64, bool) {
	if !unsignedInteger(value, 63, positive) {
		return 0, false
	}
	n, err := strconv.ParseUint(value, 10, 63)
	return n, err == nil
}

func sourcePBRelTime(a, b Time) bool {
	return finite(a) && finite(b) && a.Time.Equal(b.Time)
}

func sourcePBRelStart(control string, at time.Time) string {
	return tupleKey(control, at.UTC().Format(time.RFC3339Nano))
}

func sourcePBRelTarget(c PeriodicBudgetControlRow) (id, kind, scope, target string, ok bool) {
	scope = "platform"
	switch {
	case c.Scope == "tenant" && c.TenantID != nil && c.KeyID == nil && c.AccountID == nil:
		id, kind = *c.TenantID, "tenant_periodic_budget.configure"
		target = "/api/platform/tenants/" + id + "/periodic-budget"
	case c.Scope == "key" && c.TenantID != nil && c.KeyID != nil && c.AccountID == nil:
		id, kind, scope = *c.KeyID, "key_periodic_budget.configure", *c.TenantID
		target = "/api/keys/" + id + "/periodic-budget"
	case c.Scope == "procurement" && c.TenantID == nil && c.KeyID == nil && c.AccountID != nil:
		id, kind = *c.AccountID, "procurement_periodic_budget.configure"
		target = "/api/platform/procurement-accounts/" + id + "/periodic-budget"
	default:
		return "", "", "", "", false
	}
	return id, kind, scope, target, true
}

func (p *sourcePBOperationRelations) prepare() {
	for _, table := range []string{"tenants", "users", "api_keys", "procurement_accounts", "audit_events", pbRelControls, pbRelWindows, pbRelAllocs, pbRelEvents, pbRelOps, pbRelVersions, pbRelOrigins, pbRelHeads, pbRelJournal} {
		p.index[table] = make(map[string][]sourceRawRow)
		for _, raw := range p.s.tables[table] {
			if !p.tick() {
				return
			}
			p.index[table][raw.key] = append(p.index[table][raw.key], raw)
			switch table {
			case pbRelControls:
				c := raw.value.Interface().(PeriodicBudgetControlRow)
				id, _, _, _, ok := sourcePBRelTarget(c)
				if ok {
					key := tupleKey(c.Scope, id)
					p.scopes[key] = append(p.scopes[key], raw)
				}
			case pbRelVersions:
				v := raw.value.Interface().(PeriodicBudgetConfigurationVersionRow)
				p.versions[v.ControlID] = append(p.versions[v.ControlID], raw)
				p.versionOps[v.OperationID] = append(p.versionOps[v.OperationID], raw)
			case pbRelOps:
				o := raw.value.Interface().(PeriodicBudgetOperationRow)
				p.operations[o.ResourceID] = append(p.operations[o.ResourceID], raw)
				p.claimed[o.ResourceID] = p.claimed[o.ResourceID] || o.ObservationID != nil
			case pbRelWindows:
				w := raw.value.Interface().(PeriodicBudgetWindowRow)
				p.windows[w.ControlID] = append(p.windows[w.ControlID], raw)
				p.claimed[w.ControlID] = p.claimed[w.ControlID] || w.ObservationID != nil || w.ObservationCreatedXID != nil
				if finite(w.StartAt) {
					key := sourcePBRelStart(w.ControlID, w.StartAt.Time)
					p.windowStarts[key] = append(p.windowStarts[key], raw)
				}
			case pbRelJournal:
				j := raw.value.Interface().(PeriodicBudgetObservationJournalRow)
				p.claimed[j.ControlID] = true
				p.journal[j.ControlID] = append(p.journal[j.ControlID], raw)
				key := tupleKey(j.Kind, j.SourceID)
				p.sources[key] = append(p.sources[key], raw)
			case pbRelOrigins, pbRelHeads:
				p.claimed[sourceText(raw.value, "control_id")] = true
			case pbRelEvents:
				e := raw.value.Interface().(PeriodicBudgetEventRow)
				key := tupleKey(e.ControlID, e.Seq)
				p.eventSeq[key] = append(p.eventSeq[key], raw)
			case "audit_events":
				a := raw.value.Interface().(AuditEventRow)
				if a.Action == "periodic_budget.configured" {
					p.audits[a.Target] = append(p.audits[a.Target], raw)
				}
			}
		}
	}
	for _, raw := range p.s.tables[pbRelOps] {
		if !p.tick() {
			return
		}
		o := raw.value.Interface().(PeriodicBudgetOperationRow)
		if len(p.index[pbRelOps][raw.key]) != 1 {
			p.bad(pbRelOps, raw)
			continue
		}
		valid, err := sourcePBOperation(p.s.w, o)
		if err != nil {
			p.err = err
			return
		}
		payload := sourcePBRelationPayload{valid: valid}
		if valid {
			in, _, err := sourcePBParse(p.s.w, o.SafeInput, false)
			if err != nil {
				p.err = err
				return
			}
			snap, _, err := sourcePBParse(p.s.w, o.ResultSnapshot, true)
			if err != nil {
				p.err = err
				return
			}
			payload.input, _ = sourcePBDecodeInput(in)
			payload.snapshot, _ = sourcePBDecodeSnapshot(snap)
		} else {
			p.s.mark("scalar", pbRelOps, raw.key)
		}
		p.payloads[o.OperationID] = payload
	}
}

func (p *sourcePBOperationRelations) configuration() {
	for _, raw := range p.s.tables[pbRelControls] {
		if !p.tick() {
			return
		}
		if !p.unique(pbRelControls, raw) {
			continue
		}
		c := raw.value.Interface().(PeriodicBudgetControlRow)
		id, _, _, _, shape := sourcePBRelTarget(c)
		if !shape || len(p.index[pbRelControls][raw.key]) != 1 || len(p.scopes[tupleKey(c.Scope, id)]) != 1 || !sourceOneOf(c.PeriodKind, "utc_day", "utc_month") || !finite(c.TrackingStartedAt) || !finite(c.EffectiveFrom) || !c.EffectiveFrom.Time.Equal(sourcePBPeriodEnd(c.PeriodKind, sourcePBPeriodStart(c.PeriodKind, c.TrackingStartedAt.Time))) {
			p.bad(pbRelControls, raw)
		}
		switch c.Scope {
		case "tenant", "key":
			if c.TenantID != nil {
				if _, ok := p.one("tenants", *c.TenantID); !ok {
					p.both(pbRelControls, raw, "tenants", p.index["tenants"][tupleKey(*c.TenantID)])
				}
			}
			if c.Scope == "key" && c.KeyID != nil {
				k, ok := p.one("api_keys", *c.KeyID)
				if !ok || !sourceSameOptional(c.TenantID, sourcePBRelPointer(sourceText(k.value, "tenant_id"))) {
					p.both(pbRelControls, raw, "api_keys", p.index["api_keys"][tupleKey(*c.KeyID)])
				}
			}
		case "procurement":
			if c.AccountID != nil {
				if _, ok := p.one("procurement_accounts", *c.AccountID); !ok {
					p.both(pbRelControls, raw, "procurement_accounts", p.index["procurement_accounts"][tupleKey(*c.AccountID)])
				}
			}
		}
		list := append([]sourceRawRow(nil), p.versions[c.ID]...)
		p.sort(list, "revision")
		if p.err != nil {
			return
		}
		bad := len(list) == 0 || c.Revision != strconv.Itoa(len(list))
		for i, vr := range list {
			if !p.tick() {
				return
			}
			v := vr.value.Interface().(PeriodicBudgetConfigurationVersionRow)
			if v.Revision != strconv.Itoa(i+1) || len(p.index[pbRelVersions][vr.key]) != 1 || v.PeriodKind != c.PeriodKind || i == 0 && v.CreatedXID != c.CreatedXID {
				bad = true
				p.bad(pbRelVersions, vr)
			}
			if i > 0 && len(p.index[pbRelVersions][vr.key]) == 1 && len(p.index[pbRelVersions][list[i-1].key]) == 1 {
				prev := list[i-1].value.Interface().(PeriodicBudgetConfigurationVersionRow)
				if v.Enabled == prev.Enabled && v.LimitAmount == prev.LimitAmount {
					bad = true
					p.both(pbRelVersions, vr, pbRelVersions, []sourceRawRow{list[i-1]})
				}
			}
		}
		if len(list) > 0 {
			last := list[len(list)-1].value.Interface().(PeriodicBudgetConfigurationVersionRow)
			if c.Revision != last.Revision || c.PeriodKind != last.PeriodKind || c.Enabled != last.Enabled || c.LimitAmount != last.LimitAmount || c.ConfigurationOperationID != last.OperationID {
				bad = true
				p.bad(pbRelVersions, list[len(list)-1])
			}
		}
		if bad {
			p.bad(pbRelControls, raw)
		}
	}
	for _, raw := range p.s.tables[pbRelVersions] {
		if !p.tick() {
			return
		}
		if !p.unique(pbRelVersions, raw) {
			continue
		}
		v := raw.value.Interface().(PeriodicBudgetConfigurationVersionRow)
		c, cok := p.control(v.ControlID)
		o, ook := p.one(pbRelOps, v.OperationID)
		_, uok := p.one("users", v.Actor)
		valid := cok && ook && uok && len(p.versionOps[v.OperationID]) == 1
		if valid {
			op := o.value.Interface().(PeriodicBudgetOperationRow)
			pl := p.payloads[op.OperationID]
			valid = op.ResourceID == v.ControlID && op.Revision == v.Revision && op.Actor == v.Actor && op.CreatedXID == v.CreatedXID && sourcePBRelTime(op.CreatedAt, v.CreatedAt) && v.PeriodKind == sourceText(c.value, "period_kind") && pl.valid && pl.input.period == v.PeriodKind && pl.input.enabled == v.Enabled && pl.input.limit == v.LimitAmount
		}
		if !valid {
			p.both(pbRelVersions, raw, pbRelOps, p.index[pbRelOps][tupleKey(v.OperationID)])
		}
	}
}

func (p *sourcePBOperationRelations) sort(rows []sourceRawRow, field string) {
	if p.err != nil {
		return
	}
	p.err = replaySort(p.s.w, rows, func(left, right sourceRawRow) bool {
		a, av := sourcePBRelSeq(sourceText(left.value, field), true)
		b, bv := sourcePBRelSeq(sourceText(right.value, field), true)
		if av != bv {
			return av
		}
		if a != b {
			return a < b
		}
		return left.key < right.key
	})
}

func sourcePBRelPointer(value string) *string { return &value }

func (p *sourcePBOperationRelations) operationRows() {
	for _, raw := range p.s.tables[pbRelOps] {
		if !p.tick() {
			return
		}
		if !p.unique(pbRelOps, raw) {
			continue
		}
		o := raw.value.Interface().(PeriodicBudgetOperationRow)
		c, cok := p.control(o.ResourceID)
		v, vok := p.version(o.ResourceID, o.Revision)
		_, uok := p.one("users", o.Actor)
		pl := p.payloads[o.OperationID]
		if !cok || !vok || !uok {
			p.bad(pbRelOps, raw)
		}
		if !cok || !vok || !pl.valid {
			continue
		}
		control := c.value.Interface().(PeriodicBudgetControlRow)
		version := v.value.Interface().(PeriodicBudgetConfigurationVersionRow)
		id, kind, scope, target, shape := sourcePBRelTarget(control)
		valid := shape && o.Kind == kind && o.Scope == scope && o.Target == target && pl.input.period == version.PeriodKind && pl.input.enabled == version.Enabled && pl.input.limit == version.LimitAmount
		refs := p.versionOps[o.OperationID]
		revision, rok := sourcePBRelSeq(o.Revision, true)
		expected, eok := sourcePBRelSeq(pl.input.expected, false)
		if len(refs) == 0 {
			valid = valid && rok && eok && expected == revision
		} else {
			valid = valid && len(refs) == 1 && refs[0].key == v.key && rok && eok && expected == revision-1
		}
		snap := pl.snapshot
		valid = valid && snap.scope == control.Scope && snap.scopeID == id && snap.revision == o.Revision && snap.period == version.PeriodKind && snap.enabled == version.Enabled && snap.limit == version.LimitAmount && finite(control.TrackingStartedAt) && finite(control.EffectiveFrom) && snap.tracking.Equal(control.TrackingStartedAt.Time) && snap.effective.Equal(control.EffectiveFrom.Time)
		if !valid {
			p.both(pbRelOps, raw, pbRelVersions, p.index[pbRelVersions][tupleKey(o.ResourceID, o.Revision)])
		}
		if snap.window != nil {
			w, ok := p.window(snap.window.id)
			if !ok || !p.snapshotWindow(control, o, snap, w) {
				p.both(pbRelOps, raw, pbRelWindows, p.index[pbRelWindows][tupleKey(snap.window.id)])
			}
		}
	}
}

func (p *sourcePBOperationRelations) snapshotWindow(c PeriodicBudgetControlRow, o PeriodicBudgetOperationRow, snap sourcePBSnapshot, raw sourceRawRow) bool {
	if !raw.value.IsValid() || snap.window == nil || !finite(o.CreatedAt) {
		return false
	}
	w := raw.value.Interface().(PeriodicBudgetWindowRow)
	start := sourcePBPeriodStart(c.PeriodKind, o.CreatedAt.Time)
	return w.ControlID == c.ID && len(p.windowStarts[sourcePBRelStart(c.ID, start)]) == 1 && finite(w.StartAt) && finite(w.EndAt) && w.StartAt.Time.Equal(start) && snap.window.start.Equal(w.StartAt.Time) && snap.window.end.Equal(w.EndAt.Time)
}

func (p *sourcePBOperationRelations) auditRows() {
	for _, raw := range p.s.tables[pbRelOps] {
		if !p.tick() {
			return
		}
		o := raw.value.Interface().(PeriodicBudgetOperationRow)
		if len(p.audits[o.OperationID]) != 1 {
			p.s.mark("companion", pbRelOps, raw.key)
			for _, a := range p.audits[o.OperationID] {
				p.bad("audit_events", a)
			}
		}
	}
	for _, raw := range p.s.tables["audit_events"] {
		if !p.tick() {
			return
		}
		a := raw.value.Interface().(AuditEventRow)
		if a.Action != "periodic_budget.configured" {
			continue
		}
		if !p.unique("audit_events", raw) {
			for _, owner := range p.index[pbRelOps][tupleKey(sourceText(raw.value, "target"))] {
				p.bad(pbRelOps, owner)
			}
			continue
		}
		value, parsed, err := sourcePBParse(p.s.w, a.SanitizedDiff, false)
		if err != nil {
			p.err = err
			return
		}
		shape := parsed && sourcePBMembers(value, "control_id", "revision", "scope", "scope_id", "changed") && value.object["changed"].kind == 'b'
		for _, name := range []string{"control_id", "revision", "scope", "scope_id"} {
			shape = shape && value.object[name].kind == 's'
		}
		if !shape {
			p.s.mark("scalar", "audit_events", raw.key)
		}
		o, ook := p.one(pbRelOps, a.Target)
		valid := ook && len(p.audits[a.Target]) == 1
		if ook {
			op := o.value.Interface().(PeriodicBudgetOperationRow)
			c, cok := p.control(op.ResourceID)
			valid = valid && cok && a.Actor == op.Actor && a.RequestID == "" && sourcePBRelTime(a.CreatedAt, op.CreatedAt)
			if cok {
				control := c.value.Interface().(PeriodicBudgetControlRow)
				id, _, _, _, ok := sourcePBRelTarget(control)
				var tenant *string
				if control.Scope == "key" {
					tenant = control.TenantID
				}
				valid = valid && ok && sourceSameOptional(a.TenantScope, tenant)
				if shape {
					refs := p.versionOps[op.OperationID]
					changed, binding := false, len(refs) == 0
					if len(refs) == 1 {
						binding = refs[0].key == tupleKey(op.ResourceID, op.Revision) && len(p.index[pbRelVersions][refs[0].key]) == 1
						changed = binding
					}
					valid = valid && binding && value.object["control_id"].text == control.ID && value.object["revision"].text == op.Revision && value.object["scope"].text == control.Scope && value.object["scope_id"].text == id && value.object["changed"].boolean == changed
				}
			}
		}
		if a.PPRootID != nil || a.PPRootStep != nil || a.PPSourceKind != nil || a.PPSourceID != nil || a.PPSourceRole != nil || a.PPCreatedXID != nil {
			valid = false
		}
		if !valid || !shape {
			p.both("audit_events", raw, pbRelOps, p.index[pbRelOps][tupleKey(a.Target)])
		}
	}
}

func (p *sourcePBOperationRelations) originsAndHeads() {
	for _, raw := range p.s.tables[pbRelControls] {
		if !p.tick() {
			return
		}
		c := raw.value.Interface().(PeriodicBudgetControlRow)
		if p.claimed[c.ID] {
			if _, ok := p.one(pbRelOrigins, c.ID); !ok {
				p.both(pbRelControls, raw, pbRelOrigins, p.index[pbRelOrigins][tupleKey(c.ID)])
			}
			if _, ok := p.one(pbRelHeads, c.ID); !ok {
				p.both(pbRelControls, raw, pbRelHeads, p.index[pbRelHeads][tupleKey(c.ID)])
			}
		}
	}
	for _, table := range []string{pbRelOrigins, pbRelHeads} {
		for _, raw := range p.s.tables[table] {
			if !p.tick() {
				return
			}
			id := sourceText(raw.value, "control_id")
			c, cok := p.control(id)
			o, ook := p.one(pbRelOrigins, id)
			_, hok := p.one(pbRelHeads, id)
			valid := cok && ook && hok && len(p.index[table][raw.key]) == 1
			if valid {
				origin := o.value.Interface().(PeriodicBudgetObservationOriginRow)
				control := c.value.Interface().(PeriodicBudgetControlRow)
				valid = origin.Version == "1" && origin.CreatedXID == control.CreatedXID && sourcePBRelTime(origin.TrackingStartedAt, control.TrackingStartedAt)
			}
			if !valid {
				p.both(table, raw, pbRelControls, p.index[pbRelControls][tupleKey(id)])
			}
		}
	}
}

func sourcePBRelCommand(j PeriodicBudgetObservationJournalRow, o PeriodicBudgetOperationRow) bool {
	return j.Kind == "command" && j.SourceID == o.OperationID && requestPointer(j.CommandSourceID, o.OperationID) && j.WindowSourceID == nil && requestPointer(o.ObservationID, j.ID) && j.ControlID == o.ResourceID && j.CreatedXID == o.CreatedXID && sourcePBRelTime(j.ObservedAt, o.CreatedAt) && j.ConfigurationRevision == o.Revision
}

func sourcePBRelBirth(j PeriodicBudgetObservationJournalRow, w PeriodicBudgetWindowRow) bool {
	return j.Kind == "window_birth" && j.SourceID == w.ID && requestPointer(j.WindowSourceID, w.ID) && j.CommandSourceID == nil && requestPointer(w.ObservationID, j.ID) && requestPointer(w.ObservationCreatedXID, j.CreatedXID) && j.ControlID == w.ControlID && requestPointer(j.WindowID, w.ID) && requestPointer(j.WindowRevision, "0") && j.WindowLastEventID == nil && nullTime(j.ObservedAt)
}

func (p *sourcePBOperationRelations) bindings() {
	for _, raw := range p.s.tables[pbRelOps] {
		if !p.tick() {
			return
		}
		if !p.unique(pbRelOps, raw) {
			continue
		}
		o := raw.value.Interface().(PeriodicBudgetOperationRow)
		if !p.claimed[o.ResourceID] {
			continue
		}
		j, ok := p.one(pbRelJournal, requestOptional(o.ObservationID))
		if !ok || !sourcePBRelCommand(j.value.Interface().(PeriodicBudgetObservationJournalRow), o) || len(p.sources[tupleKey("command", o.OperationID)]) != 1 {
			p.both(pbRelOps, raw, pbRelJournal, p.index[pbRelJournal][tupleKey(requestOptional(o.ObservationID))])
		}
	}
	for _, raw := range p.s.tables[pbRelWindows] {
		if !p.tick() {
			return
		}
		if !p.unique(pbRelWindows, raw) {
			continue
		}
		w := raw.value.Interface().(PeriodicBudgetWindowRow)
		c, cok := p.control(w.ControlID)
		calendar := cok && finite(w.StartAt) && finite(w.EndAt)
		if calendar {
			control := c.value.Interface().(PeriodicBudgetControlRow)
			calendar = sourceOneOf(control.PeriodKind, "utc_day", "utc_month") && w.StartAt.Time.Equal(sourcePBPeriodStart(control.PeriodKind, w.StartAt.Time)) && w.EndAt.Time.Equal(sourcePBPeriodEnd(control.PeriodKind, w.StartAt.Time)) && len(p.windowStarts[sourcePBRelStart(w.ControlID, w.StartAt.Time)]) == 1
		}
		if !calendar {
			p.both(pbRelWindows, raw, pbRelControls, p.index[pbRelControls][tupleKey(w.ControlID)])
		}
		if !p.claimed[w.ControlID] {
			continue
		}
		j, ok := p.one(pbRelJournal, requestOptional(w.ObservationID))
		valid := calendar && ok && sourcePBRelBirth(j.value.Interface().(PeriodicBudgetObservationJournalRow), w) && len(p.sources[tupleKey("window_birth", w.ID)]) == 1 && len(p.index[pbRelWindows][raw.key]) == 1
		if !valid {
			p.both(pbRelWindows, raw, pbRelJournal, p.index[pbRelJournal][tupleKey(requestOptional(w.ObservationID))])
		} else {
			p.births[w.ID] = j
		}
	}
}

func (p *sourcePBOperationRelations) observationRows() {
	for _, raw := range p.s.tables[pbRelJournal] {
		if !p.tick() {
			return
		}
		if !p.unique(pbRelJournal, raw) {
			continue
		}
		j := raw.value.Interface().(PeriodicBudgetObservationJournalRow)
		_, cok := p.control(j.ControlID)
		_, ook := p.one(pbRelOrigins, j.ControlID)
		v, vok := p.version(j.ControlID, j.ConfigurationRevision)
		valid := cok && ook && vok && sourceText(v.value, "operation_id") == j.ConfigurationOperationID && len(p.sources[tupleKey(j.Kind, j.SourceID)]) == 1 && len(p.index[pbRelJournal][raw.key]) == 1
		if !sourceOriginalValues(j) {
			p.s.mark("scalar", pbRelJournal, raw.key)
			valid = false
		}
		switch j.Kind {
		case "command":
			o, ok := p.one(pbRelOps, j.SourceID)
			if !ok || !sourcePBRelCommand(j, o.value.Interface().(PeriodicBudgetOperationRow)) {
				p.both(pbRelJournal, raw, pbRelOps, p.index[pbRelOps][tupleKey(j.SourceID)])
			}
		case "window_birth":
			w, ok := p.window(j.SourceID)
			if !ok || !sourcePBRelBirth(j, w.value.Interface().(PeriodicBudgetWindowRow)) {
				p.both(pbRelJournal, raw, pbRelWindows, p.index[pbRelWindows][tupleKey(j.SourceID)])
			}
		default:
			valid = false
		}
		cut, seqOK := sourcePBRelSeq(j.EventSeq, false)
		if !seqOK {
			valid = false
		} else if cut == 0 {
			valid = valid && j.LastEventID == nil
		} else {
			es := p.eventSeq[tupleKey(j.ControlID, j.EventSeq)]
			if len(es) != 1 || !requestPointer(j.LastEventID, sourceText(es[0].value, "id")) || len(p.index[pbRelEvents][es[0].key]) != 1 {
				p.both(pbRelJournal, raw, pbRelEvents, es)
			}
		}
		if j.WindowID != nil {
			w, ok := p.window(*j.WindowID)
			if !ok || sourceText(w.value, "control_id") != j.ControlID {
				p.both(pbRelJournal, raw, pbRelWindows, p.index[pbRelWindows][tupleKey(*j.WindowID)])
			}
			if j.WindowLastEventID != nil {
				e, eok := p.event(*j.WindowLastEventID)
				tailValid := eok && seqOK
				if eok {
					event := e.value.Interface().(PeriodicBudgetEventRow)
					a, aok := p.one(pbRelAllocs, event.AllocationID)
					n, nok := sourcePBRelSeq(event.Seq, true)
					tailValid = tailValid && aok && nok && n <= cut && event.ControlID == j.ControlID && sourceText(a.value, "control_id") == j.ControlID && sourceSameOptional(sourceOptional(a.value, "window_id"), j.WindowID)
				}
				if !tailValid {
					p.both(pbRelJournal, raw, pbRelEvents, p.index[pbRelEvents][tupleKey(*j.WindowLastEventID)])
				}
			}
		}
		if !valid {
			p.bad(pbRelJournal, raw)
		}
	}
}

func (p *sourcePBOperationRelations) chains() {
	for _, cr := range p.s.tables[pbRelControls] {
		if !p.tick() {
			return
		}
		cid := sourceText(cr.value, "id")
		if !p.claimed[cid] {
			continue
		}
		list := append([]sourceRawRow(nil), p.journal[cid]...)
		p.sort(list, "ordinal")
		if p.err != nil {
			return
		}
		h, hok := p.one(pbRelHeads, cid)
		o, ook := p.one(pbRelOrigins, cid)
		good := hok && ook && len(p.index[pbRelControls][cr.key]) == 1
		var previous, tail *string
		var prevSeq, prevRev uint64
		prevValid := true
		for i, raw := range list {
			if !p.tick() {
				return
			}
			j := raw.value.Interface().(PeriodicBudgetObservationJournalRow)
			if j.Ordinal != strconv.Itoa(i+1) || !sourceSameOptional(j.PreviousID, previous) || len(p.index[pbRelJournal][raw.key]) != 1 {
				p.bad(pbRelJournal, raw)
				good = false
			}
			seq, a := sourcePBRelSeq(j.EventSeq, false)
			rev, b := sourcePBRelSeq(j.ConfigurationRevision, true)
			unique := len(p.index[pbRelJournal][raw.key]) == 1
			if !a || !b || unique && prevValid && (seq < prevSeq || rev < prevRev || seq == prevSeq && !sourceSameOptional(j.LastEventID, tail)) {
				p.bad(pbRelJournal, raw)
			}
			previous, tail = sourcePBRelPointer(j.ID), j.LastEventID
			prevSeq, prevRev, prevValid = seq, rev, a && b && unique
		}
		if hok {
			head := h.value.Interface().(PeriodicBudgetObservationHeadRow)
			valid := good && head.Ordinal == strconv.Itoa(len(list)) && sourceSameOptional(head.LastObservationID, previous)
			if len(list) == 0 {
				valid = valid && ook && head.UpdatedXID == sourceText(o.value, "created_xid")
			} else {
				valid = valid && head.UpdatedXID == sourceText(list[len(list)-1].value, "created_xid")
			}
			if !valid {
				p.bad(pbRelHeads, h)
				good = false
			}
		}
		p.chainOK[cid] = good
	}
}

func (p *sourcePBOperationRelations) windowEdges() {
	for _, raw := range p.s.tables[pbRelEvents] {
		if !p.tick() {
			return
		}
		if !p.unique(pbRelEvents, raw) {
			continue
		}
		e := raw.value.Interface().(PeriodicBudgetEventRow)
		if len(p.eventSeq[tupleKey(e.ControlID, e.Seq)]) != 1 {
			p.bad(pbRelEvents, raw)
			continue
		}
		a, ok := p.one(pbRelAllocs, e.AllocationID)
		if !ok {
			p.both(pbRelEvents, raw, pbRelAllocs, p.index[pbRelAllocs][tupleKey(e.AllocationID)])
			continue
		}
		wid := sourceOptional(a.value, "window_id")
		if wid == nil {
			continue
		}
		w, wok := p.window(*wid)
		if !wok || sourceText(a.value, "control_id") != e.ControlID || sourceText(w.value, "control_id") != e.ControlID {
			p.both(pbRelEvents, raw, pbRelWindows, p.index[pbRelWindows][tupleKey(*wid)])
			continue
		}
		if !p.claimed[e.ControlID] {
			continue
		}
		birth, born := p.births[*wid]
		seq, sok := sourcePBRelSeq(e.Seq, true)
		cut, cok := sourcePBRelSeq(sourceText(birth.value, "event_seq"), false)
		if !born || !sok || !cok || seq <= cut {
			p.both(pbRelEvents, raw, pbRelWindows, []sourceRawRow{w})
			if born {
				p.bad(pbRelJournal, birth)
			}
		}
	}
	for _, raw := range p.s.tables[pbRelJournal] {
		if !p.tick() {
			return
		}
		j := raw.value.Interface().(PeriodicBudgetObservationJournalRow)
		if j.Kind != "command" || !p.chainOK[j.ControlID] {
			continue
		}
		o, ook := p.one(pbRelOps, j.SourceID)
		c, cok := p.control(j.ControlID)
		pl := p.payloads[j.SourceID]
		if !ook || !cok || !pl.valid {
			continue
		}
		control := c.value.Interface().(PeriodicBudgetControlRow)
		op := o.value.Interface().(PeriodicBudgetOperationRow)
		if !finite(op.CreatedAt) || !sourceOneOf(control.PeriodKind, "utc_day", "utc_month") {
			continue
		}
		ws := p.windowStarts[sourcePBRelStart(j.ControlID, sourcePBPeriodStart(control.PeriodKind, op.CreatedAt.Time))]
		exists, valid := false, len(ws) <= 1
		if len(ws) == 1 {
			w := ws[0].value.Interface().(PeriodicBudgetWindowRow)
			birth, born := p.births[w.ID]
			b, bok := sourcePBRelSeq(sourceText(birth.value, "ordinal"), true)
			n, nok := sourcePBRelSeq(j.Ordinal, true)
			valid = born && bok && nok && b != n
			exists = valid && b < n
			if exists {
				valid = requestPointer(j.WindowID, w.ID) && pl.snapshot.window != nil && pl.snapshot.window.id == w.ID && p.snapshotWindow(control, op, pl.snapshot, ws[0])
			}
		}
		if !exists {
			valid = valid && j.WindowID == nil && j.WindowRevision == nil && j.WindowLastEventID == nil && pl.snapshot.window == nil
		}
		if !valid {
			p.both(pbRelJournal, raw, pbRelOps, []sourceRawRow{o})
			for _, w := range ws {
				p.bad(pbRelWindows, w)
			}
		}
	}
}
