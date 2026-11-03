package procurementprefix

import (
	"math/big"
	"strconv"
	"time"
)

// This leaf compares a command with its own raw PB effect prefix. It never
// adopts another leaf's result, today's financial projection, or a business Q.
type sourcePBSnapshotPrefix struct {
	s             *sourceClosureState
	err           error
	index         map[string]map[string][]sourceRawRow
	scopes        map[string][]sourceRawRow
	starts        map[string][]sourceRawRow
	subjects      map[string][]sourceRawRow
	seqs          map[string][]sourceRawRow
	effects       map[string][]sourceRawRow
	ordinals      map[string][]sourceRawRow
	sources       map[string][]sourceRawRow
	versionOps    map[string][]sourceRawRow
	operations    map[string][]sourceRawRow
	windows       map[string][]sourceRawRow
	events        map[string][]sourceRawRow
	journal       map[string][]sourceRawRow
	claimed       map[string]bool
	controlOK     map[string]bool
	windowOK      map[string]bool
	births        map[string]sourceRawRow
	observationOK map[string]bool
	chainOK       map[string]bool
	payloads      map[string]sourcePBSnapshotPrefixPayload
	unknownEvents map[string]bool
	badBuckets    map[sourcePBSnapshotPrefixBucketKey]bool
}

// All relation indexes are immutable after prepare. Cache an exact index
// bucket, rather than rescanning its duplicate rows for every consumer.
type sourcePBSnapshotPrefixBucketKey struct {
	table string
	first *sourceRawRow
	len   int
}

type sourcePBSnapshotPrefixPayload struct {
	value sourcePBSnapshot
	valid bool
}

type sourcePBSnapshotPrefixBucket struct {
	held, spent big.Int
	n           uint64
	tail        *string
}

type sourcePBSnapshotPrefixEvent struct {
	raw                    sourceRawRow
	row                    PeriodicBudgetEventRow
	seq                    uint64
	alloc                  PeriodicBudgetAllocationRow
	win                    *string
	bh, bs, ah, as, dh, ds *big.Int
	valid                  bool
}

func (s *sourceClosureState) pbSnapshotPrefixRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	p := &sourcePBSnapshotPrefix{s: s, index: make(map[string]map[string][]sourceRawRow),
		scopes: make(map[string][]sourceRawRow), starts: make(map[string][]sourceRawRow), subjects: make(map[string][]sourceRawRow),
		seqs: make(map[string][]sourceRawRow), effects: make(map[string][]sourceRawRow), ordinals: make(map[string][]sourceRawRow),
		sources: make(map[string][]sourceRawRow), versionOps: make(map[string][]sourceRawRow), operations: make(map[string][]sourceRawRow),
		windows: make(map[string][]sourceRawRow), events: make(map[string][]sourceRawRow), journal: make(map[string][]sourceRawRow),
		claimed: make(map[string]bool), controlOK: make(map[string]bool), windowOK: make(map[string]bool),
		births: make(map[string]sourceRawRow), observationOK: make(map[string]bool), chainOK: make(map[string]bool),
		payloads: make(map[string]sourcePBSnapshotPrefixPayload), unknownEvents: make(map[string]bool),
		badBuckets: make(map[sourcePBSnapshotPrefixBucketKey]bool)}
	p.prepare()
	p.anchors()
	p.payloadRows()
	p.observations()
	p.sourceRows()
	p.chains()
	p.replay()
	if p.err != nil {
		return p.err
	}
	return s.w.b.check()
}

func (p *sourcePBSnapshotPrefix) tick() bool {
	if p.err == nil {
		p.err = p.s.w.tick()
	}
	return p.err == nil
}

func (p *sourcePBSnapshotPrefix) bad(table string, raw sourceRawRow) {
	if p.tick() {
		p.s.mark("pointer", table, raw.key)
	}
}

func (p *sourcePBSnapshotPrefix) badRows(table string, rows []sourceRawRow) {
	if !p.tick() || len(rows) == 0 {
		return
	}
	key := sourcePBSnapshotPrefixBucketKey{table, &rows[0], len(rows)}
	if p.badBuckets[key] {
		return
	}
	p.badBuckets[key] = true
	for _, raw := range rows {
		if !p.tick() {
			return
		}
		p.bad(table, raw)
	}
}

func (p *sourcePBSnapshotPrefix) scalar(table string, raw sourceRawRow, good bool) bool {
	if !good && p.tick() {
		p.s.mark("scalar", table, raw.key)
	}
	return good
}

func (p *sourcePBSnapshotPrefix) one(table string, parts ...string) (sourceRawRow, bool) {
	rows := p.index[table][tupleKey(parts...)]
	if !p.tick() || len(rows) != 1 {
		return sourceRawRow{}, false
	}
	return rows[0], true
}

func sourcePBSnapshotPrefixSeq(text string, positive bool) (uint64, bool) {
	if !unsignedInteger(text, 63, positive) {
		return 0, false
	}
	n, err := strconv.ParseUint(text, 10, 63)
	return n, err == nil
}

func sourcePBSnapshotPrefixTime(a, b Time) bool {
	return finite(a) && finite(b) && a.Time.Equal(b.Time)
}

func sourcePBSnapshotPrefixScope(c PeriodicBudgetControlRow) (string, bool) {
	switch {
	case c.Scope == "tenant" && c.TenantID != nil && c.KeyID == nil && c.AccountID == nil:
		return *c.TenantID, true
	case c.Scope == "key" && c.TenantID != nil && c.KeyID != nil && c.AccountID == nil:
		return *c.KeyID, true
	case c.Scope == "procurement" && c.TenantID == nil && c.KeyID == nil && c.AccountID != nil:
		return *c.AccountID, true
	}
	return "", false
}

func sourcePBSnapshotPrefixStart(control string, at time.Time) string {
	return tupleKey(control, at.UTC().Format(time.RFC3339Nano))
}

func sourcePBSnapshotPrefixFits(n *big.Int) bool {
	return n != nil && n.Sign() >= 0 && len(n.String()) <= 100
}

func sourcePBSnapshotPrefixText(text string) *string { return &text }

func (p *sourcePBSnapshotPrefix) prepare() {
	for _, table := range []string{pbRelControls, pbRelWindows, pbRelAllocs, pbRelEvents, pbRelOps, pbRelVersions, pbRelOrigins, pbRelHeads, pbRelJournal} {
		p.index[table] = make(map[string][]sourceRawRow)
		for _, raw := range p.s.tables[table] {
			if !p.tick() {
				return
			}
			p.index[table][raw.key] = append(p.index[table][raw.key], raw)
			switch table {
			case pbRelControls:
				c := raw.value.Interface().(PeriodicBudgetControlRow)
				if id, ok := sourcePBSnapshotPrefixScope(c); ok {
					p.scopes[tupleKey(c.Scope, id)] = append(p.scopes[tupleKey(c.Scope, id)], raw)
				}
			case pbRelWindows:
				w := raw.value.Interface().(PeriodicBudgetWindowRow)
				p.windows[w.ControlID] = append(p.windows[w.ControlID], raw)
				p.claimed[w.ControlID] = p.claimed[w.ControlID] || w.ObservationID != nil || w.ObservationCreatedXID != nil
				if finite(w.StartAt) {
					key := sourcePBSnapshotPrefixStart(w.ControlID, w.StartAt.Time)
					p.starts[key] = append(p.starts[key], raw)
				}
			case pbRelAllocs:
				a := raw.value.Interface().(PeriodicBudgetAllocationRow)
				key := tupleKey(a.ControlID, a.SubjectType, a.SubjectID)
				p.subjects[key] = append(p.subjects[key], raw)
			case pbRelEvents:
				e := raw.value.Interface().(PeriodicBudgetEventRow)
				p.events[e.ControlID] = append(p.events[e.ControlID], raw)
				key := tupleKey(e.ControlID, e.Seq)
				p.seqs[key] = append(p.seqs[key], raw)
				key = tupleKey(e.ControlID, e.SourceKind, e.SourceID)
				p.effects[key] = append(p.effects[key], raw)
			case pbRelOps:
				o := raw.value.Interface().(PeriodicBudgetOperationRow)
				p.operations[o.ResourceID] = append(p.operations[o.ResourceID], raw)
				p.claimed[o.ResourceID] = p.claimed[o.ResourceID] || o.ObservationID != nil
			case pbRelVersions:
				v := raw.value.Interface().(PeriodicBudgetConfigurationVersionRow)
				p.versionOps[v.OperationID] = append(p.versionOps[v.OperationID], raw)
			case pbRelOrigins, pbRelHeads:
				p.claimed[sourceText(raw.value, "control_id")] = true
			case pbRelJournal:
				j := raw.value.Interface().(PeriodicBudgetObservationJournalRow)
				p.claimed[j.ControlID] = true
				p.journal[j.ControlID] = append(p.journal[j.ControlID], raw)
				key := tupleKey(j.ControlID, j.Ordinal)
				p.ordinals[key] = append(p.ordinals[key], raw)
				key = tupleKey(j.Kind, j.SourceID)
				p.sources[key] = append(p.sources[key], raw)
			}
		}
	}
	// Cross-control/position copies cannot be assigned by choosing a row. A
	// duplicate at one known control/seq is bad from that seq, while strictly
	// earlier complete prefixes remain available. Neither case consumes a copy.
	for _, rows := range p.index[pbRelEvents] {
		if !p.tick() {
			return
		}
		if len(rows) > 1 {
			control := sourceText(rows[0].value, "control_id")
			seq, known := sourcePBSnapshotPrefixSeq(sourceText(rows[0].value, "seq"), true)
			ambiguous := !known
			for _, raw := range rows {
				if !p.tick() {
					return
				}
				n, ok := sourcePBSnapshotPrefixSeq(sourceText(raw.value, "seq"), true)
				ambiguous = ambiguous || !ok || n != seq || sourceText(raw.value, "control_id") != control
			}
			if !ambiguous {
				continue
			}
			for _, raw := range rows {
				if !p.tick() {
					return
				}
				p.unknownEvents[sourceText(raw.value, "control_id")] = true
			}
		}
	}
}

func (p *sourcePBSnapshotPrefix) anchors() {
	for _, raw := range p.s.tables[pbRelControls] {
		if !p.tick() {
			return
		}
		c := raw.value.Interface().(PeriodicBudgetControlRow)
		id, shape := sourcePBSnapshotPrefixScope(c)
		local := sourceOriginalUUID(c.ID, true) && shape && sourceOneOf(c.PeriodKind, "utc_day", "utc_month") && finite(c.TrackingStartedAt) && finite(c.EffectiveFrom) && unsignedInteger(c.CreatedXID, 64, false)
		p.scalar(pbRelControls, raw, local)
		valid := local && len(p.index[pbRelControls][raw.key]) == 1 && len(p.scopes[tupleKey(c.Scope, id)]) == 1
		if local {
			valid = valid && c.EffectiveFrom.Time.Equal(sourcePBPeriodEnd(c.PeriodKind, sourcePBPeriodStart(c.PeriodKind, c.TrackingStartedAt.Time)))
		}
		p.controlOK[c.ID] = valid
		if !valid {
			p.bad(pbRelControls, raw)
		}
	}
	for _, raw := range p.s.tables[pbRelWindows] {
		if !p.tick() {
			return
		}
		w := raw.value.Interface().(PeriodicBudgetWindowRow)
		local := sourceOriginalUUID(w.ID, true) && sourceOriginalUUID(w.ControlID, true) && finite(w.StartAt) && finite(w.EndAt) && sourceOriginalOptionalPBUUID(w.ObservationID) && (w.ObservationCreatedXID == nil || unsignedInteger(*w.ObservationCreatedXID, 64, false))
		p.scalar(pbRelWindows, raw, local)
		cr, cok := p.one(pbRelControls, w.ControlID)
		valid := local && cok && p.controlOK[w.ControlID] && len(p.index[pbRelWindows][raw.key]) == 1
		if valid {
			c := cr.value.Interface().(PeriodicBudgetControlRow)
			valid = w.StartAt.Time.Equal(sourcePBPeriodStart(c.PeriodKind, w.StartAt.Time)) && w.EndAt.Time.Equal(sourcePBPeriodEnd(c.PeriodKind, w.StartAt.Time)) && !w.StartAt.Time.Before(sourcePBPeriodStart(c.PeriodKind, c.TrackingStartedAt.Time)) && len(p.starts[sourcePBSnapshotPrefixStart(w.ControlID, w.StartAt.Time)]) == 1
		}
		p.windowOK[w.ID] = valid
		if !valid {
			p.bad(pbRelWindows, raw)
		}
	}
}

func (p *sourcePBSnapshotPrefix) payloadRows() {
	for _, raw := range p.s.tables[pbRelOps] {
		if !p.tick() {
			return
		}
		o := raw.value.Interface().(PeriodicBudgetOperationRow)
		value, parsed, err := sourcePBParse(p.s.w, o.ResultSnapshot, true)
		if err != nil {
			p.err = err
			return
		}
		snapshot, decoded := sourcePBDecodeSnapshot(value)
		local := parsed && decoded && sourceOriginalUUID(o.OperationID, true) && sourceOriginalUUID(o.ResourceID, true) && unsignedInteger(o.Revision, 63, true) && unsignedInteger(o.CreatedXID, 64, false) && finite(o.CreatedAt) && sourceOriginalOptionalPBUUID(o.ObservationID)
		p.scalar(pbRelOps, raw, local)
		valid := local && len(p.index[pbRelOps][raw.key]) == 1 && p.controlOK[o.ResourceID]
		cr, cok := p.one(pbRelControls, o.ResourceID)
		if valid && cok {
			c := cr.value.Interface().(PeriodicBudgetControlRow)
			id, shape := sourcePBSnapshotPrefixScope(c)
			valid = shape && snapshot.scope == c.Scope && snapshot.scopeID == id && snapshot.period == c.PeriodKind && snapshot.revision == o.Revision && snapshot.at.Equal(o.CreatedAt.Time) && snapshot.tracking.Equal(c.TrackingStartedAt.Time) && snapshot.effective.Equal(c.EffectiveFrom.Time)
		} else {
			valid = false
		}
		p.payloads[o.OperationID] = sourcePBSnapshotPrefixPayload{snapshot, valid}
		if !valid {
			p.bad(pbRelOps, raw)
		}
	}
}

func sourcePBSnapshotPrefixCommand(j PeriodicBudgetObservationJournalRow, o PeriodicBudgetOperationRow) bool {
	return j.Kind == "command" && j.SourceID == o.OperationID && requestPointer(j.CommandSourceID, o.OperationID) && j.WindowSourceID == nil && requestPointer(o.ObservationID, j.ID) && j.ControlID == o.ResourceID && j.CreatedXID == o.CreatedXID && sourcePBSnapshotPrefixTime(j.ObservedAt, o.CreatedAt) && j.ConfigurationRevision == o.Revision
}

func sourcePBSnapshotPrefixBirth(j PeriodicBudgetObservationJournalRow, w PeriodicBudgetWindowRow) bool {
	return j.Kind == "window_birth" && j.SourceID == w.ID && requestPointer(j.WindowSourceID, w.ID) && j.CommandSourceID == nil && requestPointer(w.ObservationID, j.ID) && requestPointer(w.ObservationCreatedXID, j.CreatedXID) && j.ControlID == w.ControlID && requestPointer(j.WindowID, w.ID) && requestPointer(j.WindowRevision, "0") && j.WindowLastEventID == nil && nullTime(j.ObservedAt)
}

func (p *sourcePBSnapshotPrefix) observationScalar(raw sourceRawRow) bool {
	j := raw.value.Interface().(PeriodicBudgetObservationJournalRow)
	_, seqOK := sourcePBSnapshotPrefixSeq(j.EventSeq, false)
	_, ordinalOK := sourcePBSnapshotPrefixSeq(j.Ordinal, true)
	window := j.WindowID == nil && j.WindowRevision == nil && j.WindowLastEventID == nil || j.WindowID != nil && j.WindowRevision != nil && unsignedInteger(*j.WindowRevision, 63, false) && ((*j.WindowRevision == "0") == (j.WindowLastEventID == nil))
	kind := j.Kind == "command" && finite(j.ObservedAt) && requestPointer(j.CommandSourceID, j.SourceID) && j.WindowSourceID == nil || j.Kind == "window_birth" && nullTime(j.ObservedAt) && j.CommandSourceID == nil && requestPointer(j.WindowSourceID, j.SourceID) && requestPointer(j.WindowID, j.SourceID) && requestPointer(j.WindowRevision, "0") && j.WindowLastEventID == nil
	return p.scalar(pbRelJournal, raw, sourceOriginalUUID(j.ID, true) && sourceOriginalUUID(j.ControlID, true) && sourceOriginalUUID(j.SourceID, true) && sourceOriginalUUID(j.ConfigurationOperationID, true) && ordinalOK && seqOK && unsignedInteger(j.ConfigurationRevision, 63, true) && unsignedInteger(j.CreatedXID, 64, false) && sourceOriginalOptionalPBUUID(j.PreviousID, j.LastEventID, j.WindowID, j.WindowLastEventID, j.CommandSourceID, j.WindowSourceID) && window && kind && ((j.EventSeq == "0") == (j.LastEventID == nil)))
}

func (p *sourcePBSnapshotPrefix) observations() {
	for _, raw := range p.s.tables[pbRelJournal] {
		if !p.tick() {
			return
		}
		j := raw.value.Interface().(PeriodicBudgetObservationJournalRow)
		valid := p.observationScalar(raw) && len(p.index[pbRelJournal][raw.key]) == 1 && len(p.ordinals[tupleKey(j.ControlID, j.Ordinal)]) == 1 && len(p.sources[tupleKey(j.Kind, j.SourceID)]) == 1 && p.controlOK[j.ControlID]
		vr, vok := p.one(pbRelVersions, j.ControlID, j.ConfigurationRevision)
		if vok {
			v := vr.value.Interface().(PeriodicBudgetConfigurationVersionRow)
			cfg, cfgOK := p.one(pbRelOps, v.OperationID)
			cr, cOK := p.one(pbRelControls, j.ControlID)
			vgood := sourceOriginalUUID(v.OperationID, true) && unsignedInteger(v.Revision, 63, true) && unsignedInteger(v.LimitAmount, 63, false) && sourceOneOf(v.PeriodKind, "utc_day", "utc_month")
			p.scalar(pbRelVersions, vr, vgood)
			vgood = vgood && cfgOK && cOK && len(p.versionOps[v.OperationID]) == 1 && v.OperationID == j.ConfigurationOperationID
			if cfgOK && cOK {
				o := cfg.value.Interface().(PeriodicBudgetOperationRow)
				c := cr.value.Interface().(PeriodicBudgetControlRow)
				vgood = vgood && o.ResourceID == j.ControlID && o.Revision == j.ConfigurationRevision && v.PeriodKind == c.PeriodKind
			}
			if !vgood {
				p.bad(pbRelVersions, vr)
			}
			valid = valid && vgood
		} else {
			valid = false
			p.badRows(pbRelVersions, p.index[pbRelVersions][tupleKey(j.ControlID, j.ConfigurationRevision)])
		}
		switch j.Kind {
		case "command":
			o, ok := p.one(pbRelOps, j.SourceID)
			binding := ok && sourcePBSnapshotPrefixCommand(j, o.value.Interface().(PeriodicBudgetOperationRow))
			if !binding {
				p.badRows(pbRelOps, p.index[pbRelOps][tupleKey(j.SourceID)])
			}
			valid = valid && binding
			if binding && vok {
				v := vr.value.Interface().(PeriodicBudgetConfigurationVersionRow)
				pl := p.payloads[j.SourceID]
				if pl.valid && (pl.value.enabled != v.Enabled || pl.value.limit != v.LimitAmount || pl.value.period != v.PeriodKind) {
					p.bad(pbRelOps, o)
					pl.valid = false
					p.payloads[j.SourceID] = pl
				}
			}
		case "window_birth":
			w, ok := p.one(pbRelWindows, j.SourceID)
			binding := ok && p.windowOK[j.SourceID] && sourcePBSnapshotPrefixBirth(j, w.value.Interface().(PeriodicBudgetWindowRow))
			if !binding {
				p.badRows(pbRelWindows, p.index[pbRelWindows][tupleKey(j.SourceID)])
			}
			valid = valid && binding
		default:
			valid = false
		}
		p.observationOK[j.ID] = valid
		if !valid {
			p.bad(pbRelJournal, raw)
		}
	}
}

func (p *sourcePBSnapshotPrefix) sourceRows() {
	for _, raw := range p.s.tables[pbRelOps] {
		if !p.tick() {
			return
		}
		o := raw.value.Interface().(PeriodicBudgetOperationRow)
		if !p.claimed[o.ResourceID] {
			continue
		}
		j, ok := p.one(pbRelJournal, requestOptional(o.ObservationID))
		if !ok || len(p.index[pbRelOps][raw.key]) != 1 || !p.observationOK[sourceText(j.value, "id")] || !sourcePBSnapshotPrefixCommand(j.value.Interface().(PeriodicBudgetObservationJournalRow), o) || len(p.sources[tupleKey("command", o.OperationID)]) != 1 {
			p.bad(pbRelOps, raw)
			p.badRows(pbRelJournal, p.index[pbRelJournal][tupleKey(requestOptional(o.ObservationID))])
		}
	}
	for _, raw := range p.s.tables[pbRelWindows] {
		if !p.tick() {
			return
		}
		w := raw.value.Interface().(PeriodicBudgetWindowRow)
		if !p.claimed[w.ControlID] {
			continue
		}
		j, ok := p.one(pbRelJournal, requestOptional(w.ObservationID))
		good := ok && len(p.index[pbRelWindows][raw.key]) == 1 && p.observationOK[sourceText(j.value, "id")] && sourcePBSnapshotPrefixBirth(j.value.Interface().(PeriodicBudgetObservationJournalRow), w) && len(p.sources[tupleKey("window_birth", w.ID)]) == 1
		if good {
			p.births[w.ID] = j
		} else {
			p.bad(pbRelWindows, raw)
			p.badRows(pbRelJournal, p.index[pbRelJournal][tupleKey(requestOptional(w.ObservationID))])
		}
	}
}

func (p *sourcePBSnapshotPrefix) chains() {
	// Origin/head are checked in both directions, including orphan rows.
	for _, table := range []string{pbRelOrigins, pbRelHeads} {
		for _, raw := range p.s.tables[table] {
			if !p.tick() {
				return
			}
			cid := sourceText(raw.value, "control_id")
			if !p.controlOK[cid] || len(p.index[table][raw.key]) != 1 {
				p.bad(table, raw)
			}
		}
	}
	visited := make(map[string]bool)
	for _, cr := range p.s.tables[pbRelControls] {
		if !p.tick() {
			return
		}
		c := cr.value.Interface().(PeriodicBudgetControlRow)
		if visited[c.ID] || !p.claimed[c.ID] {
			continue
		}
		visited[c.ID] = true
		if len(p.index[pbRelControls][cr.key]) != 1 {
			// A duplicate control is never selected as a canonical owner. Its
			// actual dependent declarations are marked once, without sorting
			// or scanning this same chain again for every physical copy.
			p.badRows(pbRelControls, p.index[pbRelControls][cr.key])
			p.badRows(pbRelOrigins, p.index[pbRelOrigins][tupleKey(c.ID)])
			p.badRows(pbRelHeads, p.index[pbRelHeads][tupleKey(c.ID)])
			p.badRows(pbRelJournal, p.journal[c.ID])
			p.badRows(pbRelOps, p.operations[c.ID])
			p.badRows(pbRelWindows, p.windows[c.ID])
			p.badRows(pbRelEvents, p.events[c.ID])
			continue
		}
		o, ook := p.one(pbRelOrigins, c.ID)
		h, hok := p.one(pbRelHeads, c.ID)
		good := p.controlOK[c.ID] && ook && hok
		if ook {
			origin := o.value.Interface().(PeriodicBudgetObservationOriginRow)
			local := sourceOriginalUUID(origin.ControlID, true) && origin.Version == "1" && unsignedInteger(origin.CreatedXID, 64, false) && finite(origin.TrackingStartedAt)
			p.scalar(pbRelOrigins, o, local)
			if !local || origin.CreatedXID != c.CreatedXID || !sourcePBSnapshotPrefixTime(origin.TrackingStartedAt, c.TrackingStartedAt) {
				p.bad(pbRelOrigins, o)
				good = false
			}
		}
		if !good {
			p.bad(pbRelControls, cr)
			p.badRows(pbRelOrigins, p.index[pbRelOrigins][tupleKey(c.ID)])
			p.badRows(pbRelHeads, p.index[pbRelHeads][tupleKey(c.ID)])
		}
		list := append([]sourceRawRow(nil), p.journal[c.ID]...)
		if err := replaySort(p.s.w, list, func(a, b sourceRawRow) bool {
			x, xo := sourcePBSnapshotPrefixSeq(sourceText(a.value, "ordinal"), true)
			y, yo := sourcePBSnapshotPrefixSeq(sourceText(b.value, "ordinal"), true)
			if xo != yo {
				return xo
			}
			return x < y
		}); err != nil {
			p.err = err
			return
		}
		var previous, priorTail *string
		var priorCut, priorRevision uint64
		for i, raw := range list {
			if !p.tick() {
				return
			}
			j := raw.value.Interface().(PeriodicBudgetObservationJournalRow)
			cut, cutOK := sourcePBSnapshotPrefixSeq(j.EventSeq, false)
			revision, revOK := sourcePBSnapshotPrefixSeq(j.ConfigurationRevision, true)
			if j.Ordinal != strconv.Itoa(i+1) || !sourceSameOptional(j.PreviousID, previous) || !p.observationOK[j.ID] {
				p.bad(pbRelJournal, raw)
				good = false
			}
			if !cutOK || !revOK || cut < priorCut || revision < priorRevision || cut == priorCut && !sourceSameOptional(j.LastEventID, priorTail) {
				p.bad(pbRelJournal, raw)
				good = false
			}
			previous, priorTail, priorCut, priorRevision = sourcePBSnapshotPrefixText(j.ID), j.LastEventID, cut, revision
		}
		for _, raw := range p.operations[c.ID] {
			if !p.tick() {
				return
			}
			op := raw.value.Interface().(PeriodicBudgetOperationRow)
			j, ok := p.one(pbRelJournal, requestOptional(op.ObservationID))
			if !ok || !p.observationOK[sourceText(j.value, "id")] || !sourcePBSnapshotPrefixCommand(j.value.Interface().(PeriodicBudgetObservationJournalRow), op) {
				good = false
			}
		}
		for _, raw := range p.windows[c.ID] {
			if !p.tick() {
				return
			}
			if _, ok := p.births[sourceText(raw.value, "id")]; !ok {
				good = false
			}
		}
		if hok {
			head := h.value.Interface().(PeriodicBudgetObservationHeadRow)
			local := unsignedInteger(head.Ordinal, 63, false) && unsignedInteger(head.UpdatedXID, 64, false) && sourceOriginalOptionalPBUUID(head.LastObservationID)
			p.scalar(pbRelHeads, h, local)
			hgood := local && head.Ordinal == strconv.Itoa(len(list)) && sourceSameOptional(head.LastObservationID, previous)
			if len(list) == 0 {
				hgood = hgood && ook && head.UpdatedXID == sourceText(o.value, "created_xid")
			} else {
				hgood = hgood && head.UpdatedXID == sourceText(list[len(list)-1].value, "created_xid")
			}
			if !hgood {
				p.bad(pbRelHeads, h)
				good = false
			}
		}
		p.journal[c.ID], p.chainOK[c.ID] = list, good
	}
}

func (p *sourcePBSnapshotPrefix) number(text string, signed bool) (*big.Int, bool) {
	if !p.tick() {
		return nil, false
	}
	return numeric(text, signed)
}

func (p *sourcePBSnapshotPrefix) event(raw sourceRawRow) sourcePBSnapshotPrefixEvent {
	e := raw.value.Interface().(PeriodicBudgetEventRow)
	x := sourcePBSnapshotPrefixEvent{raw: raw, row: e}
	var seqOK, bh, bs, ah, as, dh, ds bool
	x.seq, seqOK = sourcePBSnapshotPrefixSeq(e.Seq, true)
	x.bh, bh = p.number(e.BeforeHeld, false)
	x.bs, bs = p.number(e.BeforeSpent, false)
	x.ah, ah = p.number(e.AfterHeld, false)
	x.as, as = p.number(e.AfterSpent, false)
	x.dh, dh = p.number(e.HeldDelta, true)
	x.ds, ds = p.number(e.SpentDelta, true)
	local := seqOK && sourceOriginalUUID(e.ID, true) && sourceOriginalUUID(e.ControlID, true) && sourceOriginalUUID(e.AllocationID, true) && sourceOriginalUUID(e.SourceID, true) && sourceOneOf(e.SourceKind, "reservation", "settlement", "procurement_event", "legacy_import") && unsignedInteger(e.AllocationBeforeRevision, 63, false) && (e.WindowBeforeRevision == nil || unsignedInteger(*e.WindowBeforeRevision, 63, false)) && bh && bs && ah && as && dh && ds
	p.scalar(pbRelEvents, raw, local)
	valid := local && len(p.index[pbRelEvents][raw.key]) == 1 && len(p.seqs[tupleKey(e.ControlID, e.Seq)]) == 1 && len(p.effects[tupleKey(e.ControlID, e.SourceKind, e.SourceID)]) == 1 && p.controlOK[e.ControlID]
	a, aok := p.one(pbRelAllocs, e.AllocationID)
	if aok {
		x.alloc = a.value.Interface().(PeriodicBudgetAllocationRow)
		alloc := x.alloc
		alocal := sourceOriginalUUID(alloc.ID, true) && sourceOriginalUUID(alloc.ControlID, true) && sourceOriginalUUID(alloc.SubjectID, true) && sourceOneOf(alloc.SubjectType, "customer_request", "supplier_attempt") && sourceOriginalOptionalPBUUID(alloc.WindowID)
		p.scalar(pbRelAllocs, a, alocal)
		owned := alocal && alloc.ControlID == e.ControlID
		anchor := owned && len(p.subjects[tupleKey(alloc.ControlID, alloc.SubjectType, alloc.SubjectID)]) == 1
		if owned && !anchor {
			p.badRows(pbRelAllocs, p.subjects[tupleKey(alloc.ControlID, alloc.SubjectType, alloc.SubjectID)])
		}
		if anchor && alloc.WindowID != nil {
			wr, wok := p.one(pbRelWindows, *alloc.WindowID)
			anchor = wok && p.windowOK[*alloc.WindowID] && sourceText(wr.value, "control_id") == e.ControlID
			if anchor {
				x.win = alloc.WindowID
				birth, born := p.births[*alloc.WindowID]
				cut, cutOK := sourcePBSnapshotPrefixSeq(sourceText(birth.value, "event_seq"), false)
				if !born || !cutOK || !seqOK || x.seq <= cut {
					anchor = false
					p.bad(pbRelWindows, wr)
					if born {
						p.bad(pbRelJournal, birth)
					}
				}
			}
		}
		if !alocal || owned && !anchor {
			p.badRows(pbRelAllocs, p.index[pbRelAllocs][tupleKey(e.AllocationID)])
		}
		valid = valid && anchor
	} else {
		valid = false
		p.badRows(pbRelAllocs, p.index[pbRelAllocs][tupleKey(e.AllocationID)])
	}
	if local && (new(big.Int).Sub(x.ah, x.bh).Cmp(x.dh) != 0 || new(big.Int).Sub(x.as, x.bs).Cmp(x.ds) != 0) {
		valid = false
	}
	x.valid = valid
	if !valid {
		p.bad(pbRelEvents, raw)
		p.badRows(pbRelControls, p.index[pbRelControls][tupleKey(e.ControlID)])
	}
	if !seqOK {
		p.unknownEvents[e.ControlID] = true
	}
	return x
}

func sourcePBSnapshotPrefixBucketFor(m map[string]*sourcePBSnapshotPrefixBucket, id string) *sourcePBSnapshotPrefixBucket {
	b := m[id]
	if b == nil {
		b = &sourcePBSnapshotPrefixBucket{}
		m[id] = b
	}
	return b
}

func (p *sourcePBSnapshotPrefix) apply(x sourcePBSnapshotPrefixEvent, control *sourcePBSnapshotPrefixBucket, allocations, windows map[string]*sourcePBSnapshotPrefixBucket) bool {
	if !p.tick() {
		return false
	}
	a := sourcePBSnapshotPrefixBucketFor(allocations, x.row.AllocationID)
	valid := x.valid && x.row.AllocationBeforeRevision == strconv.FormatUint(a.n, 10)
	var w *sourcePBSnapshotPrefixBucket
	if x.win != nil {
		w = sourcePBSnapshotPrefixBucketFor(windows, *x.win)
		valid = valid && requestPointer(x.row.WindowBeforeRevision, strconv.FormatUint(w.n, 10))
	} else {
		valid = valid && x.row.WindowBeforeRevision == nil
	}
	if valid {
		valid = x.bh.Cmp(&a.held) == 0 && x.bs.Cmp(&a.spent) == 0
	}
	if !valid {
		p.bad(pbRelEvents, x.raw)
		p.badRows(pbRelControls, p.index[pbRelControls][tupleKey(x.row.ControlID)])
		return false
	}
	control.held.Add(&control.held, x.dh)
	control.spent.Add(&control.spent, x.ds)
	if w != nil {
		w.held.Add(&w.held, x.dh)
		w.spent.Add(&w.spent, x.ds)
		w.n++
		w.tail = sourcePBSnapshotPrefixText(x.row.ID)
	}
	a.held.Set(x.ah)
	a.spent.Set(x.as)
	a.n++
	a.tail = sourcePBSnapshotPrefixText(x.row.ID)
	control.n = x.seq
	control.tail = sourcePBSnapshotPrefixText(x.row.ID)
	if !sourcePBSnapshotPrefixFits(&control.held) || !sourcePBSnapshotPrefixFits(&control.spent) || w != nil && (!sourcePBSnapshotPrefixFits(&w.held) || !sourcePBSnapshotPrefixFits(&w.spent)) {
		p.bad(pbRelEvents, x.raw)
		p.badRows(pbRelControls, p.index[pbRelControls][tupleKey(x.row.ControlID)])
		if w != nil {
			p.badRows(pbRelWindows, p.index[pbRelWindows][tupleKey(*x.win)])
		}
		return false
	}
	return true
}

func (p *sourcePBSnapshotPrefix) replay() {
	for cid := range p.claimed {
		if !p.tick() {
			return
		}
		if !p.claimed[cid] {
			continue
		}
		cr, cok := p.one(pbRelControls, cid)
		if !cok {
			continue
		}
		list := make([]sourcePBSnapshotPrefixEvent, 0, len(p.events[cid]))
		for _, raw := range p.events[cid] {
			if !p.tick() {
				return
			}
			list = append(list, p.event(raw))
		}
		if err := replaySort(p.s.w, list, func(a, b sourcePBSnapshotPrefixEvent) bool { return a.seq < b.seq }); err != nil {
			p.err = err
			return
		}
		// Record the first structurally bad position before visiting commands.
		// A known bad suffix still leaves strictly earlier cuts available.
		var badAt, previous uint64
		for _, x := range list {
			if !p.tick() {
				return
			}
			if x.seq == 0 {
				continue
			}
			at := x.seq
			if x.seq != previous+1 {
				if x.seq > previous+1 {
					at = previous + 1
				}
				p.bad(pbRelEvents, x.raw)
				p.bad(pbRelControls, cr)
				if badAt == 0 || at < badAt {
					badAt = at
				}
			}
			if !x.valid && (badAt == 0 || x.seq < badAt) {
				badAt = x.seq
			}
			previous = x.seq
		}
		var control sourcePBSnapshotPrefixBucket
		allocations, windows := make(map[string]*sourcePBSnapshotPrefixBucket), make(map[string]*sourcePBSnapshotPrefixBucket)
		pos := 0
		advance := func(cut uint64) {
			for pos < len(list) && list[pos].seq <= cut {
				if !p.tick() {
					return
				}
				x := list[pos]
				if !p.unknownEvents[cid] && x.seq > 0 && (badAt == 0 || x.seq < badAt) {
					if !p.apply(x, &control, allocations, windows) && (badAt == 0 || x.seq < badAt) {
						badAt = x.seq
					}
				}
				pos++
			}
		}
		for _, jr := range p.journal[cid] {
			if !p.tick() {
				return
			}
			j := jr.value.Interface().(PeriodicBudgetObservationJournalRow)
			cut, seqOK := sourcePBSnapshotPrefixSeq(j.EventSeq, false)
			if seqOK {
				advance(cut)
			}
			// A claimed cut has one exact tail even when its amount happens to
			// match another prefix. Tail errors belong to the journal itself.
			tailOK := seqOK && cut == 0 && j.LastEventID == nil
			if seqOK && cut > 0 {
				es := p.seqs[tupleKey(cid, j.EventSeq)]
				tailOK = len(es) == 1 && len(p.index[pbRelEvents][es[0].key]) == 1 && requestPointer(j.LastEventID, sourceText(es[0].value, "id"))
			}
			if !tailOK {
				p.bad(pbRelJournal, jr)
			}
			if j.Kind != "command" {
				continue
			}
			op, ook := p.one(pbRelOps, j.SourceID)
			if !ook {
				continue
			}
			prefixOK := seqOK && !p.unknownEvents[cid] && (badAt == 0 || cut < badAt) && control.n == cut
			if !p.chainOK[cid] || !p.observationOK[j.ID] || !prefixOK {
				p.bad(pbRelJournal, jr)
				p.bad(pbRelOps, op)
				continue
			}
			p.command(cr, jr, op, &control, windows)
		}
		advance(^uint64(0))
		if p.err != nil {
			return
		}
	}
}

func (p *sourcePBSnapshotPrefix) command(cr, jr, op sourceRawRow, control *sourcePBSnapshotPrefixBucket, windows map[string]*sourcePBSnapshotPrefixBucket) {
	c := cr.value.Interface().(PeriodicBudgetControlRow)
	j := jr.value.Interface().(PeriodicBudgetObservationJournalRow)
	o := op.value.Interface().(PeriodicBudgetOperationRow)
	pl := p.payloads[o.OperationID]
	if !pl.valid {
		return
	}
	ws := p.starts[sourcePBSnapshotPrefixStart(c.ID, sourcePBPeriodStart(c.PeriodKind, o.CreatedAt.Time))]
	exists, good := false, len(ws) <= 1
	var window PeriodicBudgetWindowRow
	if len(ws) == 1 {
		window = ws[0].value.Interface().(PeriodicBudgetWindowRow)
		birth, born := p.births[window.ID]
		ordinal, ordinalOK := sourcePBSnapshotPrefixSeq(j.Ordinal, true)
		birthOrdinal, birthOK := sourcePBSnapshotPrefixSeq(sourceText(birth.value, "ordinal"), true)
		good = p.windowOK[window.ID] && born && ordinalOK && birthOK && birthOrdinal != ordinal
		exists = good && birthOrdinal < ordinal
	}
	if !good {
		p.bad(pbRelJournal, jr)
		p.bad(pbRelOps, op)
		p.badRows(pbRelWindows, ws)
		return
	}
	carry := new(big.Int).Set(&control.held)
	if !exists {
		if j.WindowID != nil || j.WindowRevision != nil || j.WindowLastEventID != nil {
			p.bad(pbRelJournal, jr)
		}
		if pl.value.window != nil {
			p.bad(pbRelOps, op)
		}
	} else {
		w := windows[window.ID]
		if w == nil {
			w = &sourcePBSnapshotPrefixBucket{}
		}
		if !requestPointer(j.WindowID, window.ID) || !requestPointer(j.WindowRevision, strconv.FormatUint(w.n, 10)) || !sourceSameOptional(j.WindowLastEventID, w.tail) {
			p.bad(pbRelJournal, jr)
		}
		sw := pl.value.window
		if sw == nil || sw.id != window.ID || !sw.start.Equal(window.StartAt.Time) || !sw.end.Equal(window.EndAt.Time) || sw.held != w.held.String() || sw.spent != w.spent.String() {
			p.bad(pbRelOps, op)
		}
		carry.Sub(carry, &w.held)
	}
	if !p.tick() {
		return
	}
	if !sourcePBSnapshotPrefixFits(carry) || pl.value.carry != carry.String() {
		p.bad(pbRelOps, op)
	}
}
