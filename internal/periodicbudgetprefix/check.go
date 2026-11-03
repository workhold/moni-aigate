package periodicbudgetprefix

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"sort"
	"strconv"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

type work struct {
	b                 budget
	err               error
	out               Report
	seen              map[*uint64]map[object]bool
	bad, covered      map[string]bool
	controls          map[string]fundinglotverify.PeriodicBudgetControlRow
	windows           map[string]fundinglotverify.PeriodicBudgetWindowRow
	ops               map[string]fundinglotverify.PeriodicBudgetOperationRow
	versions          map[versionKey]fundinglotverify.PeriodicBudgetVersionRow
	allocs            map[string]fundinglotverify.PeriodicBudgetAllocationRow
	origins           map[string]Origin
	heads             map[string]Head
	observations      map[string]Observation
	operationBindings map[string]OperationBinding
	windowBindings    map[string]WindowBinding
	journal           map[string][]Observation
	events            map[string][]fundinglotverify.PeriodicBudgetEventRow
	windowStarts      map[windowKey]string
	births            map[string]Observation
	baseGap           map[string]bool
	resolved          uint64
}

// Check evaluates all supplied rows, including the complete original graph.
// Base is the unchanged original report; only this new scope can qualify an
// operation's precise prefix. Input rows are not retained in the result.
func Check(ctx context.Context, rows Rows) (Report, error) {
	if ctx == nil {
		return Report{}, ErrInput
	}
	w := &work{b: budget{ctx: ctx}, seen: map[*uint64]map[object]bool{}, bad: map[string]bool{}, covered: map[string]bool{},
		controls: map[string]fundinglotverify.PeriodicBudgetControlRow{}, windows: map[string]fundinglotverify.PeriodicBudgetWindowRow{}, ops: map[string]fundinglotverify.PeriodicBudgetOperationRow{}, versions: map[versionKey]fundinglotverify.PeriodicBudgetVersionRow{}, allocs: map[string]fundinglotverify.PeriodicBudgetAllocationRow{},
		origins: map[string]Origin{}, heads: map[string]Head{}, observations: map[string]Observation{}, operationBindings: map[string]OperationBinding{}, windowBindings: map[string]WindowBinding{}, journal: map[string][]Observation{}, events: map[string][]fundinglotverify.PeriodicBudgetEventRow{}, windowStarts: map[windowKey]string{}, births: map[string]Observation{}, baseGap: map[string]bool{}}
	w.b.deadline, _ = ctx.Deadline()
	if e := w.b.check(); e != nil {
		return Report{}, e
	}
	if e := w.b.arrays(reflect.ValueOf(rows)); e != nil {
		return Report{}, e
	}
	base, e := fundinglotverify.CheckPeriodicBudgetGraph(ctx, rows.Base)
	if e != nil {
		if callerErr := w.b.check(); callerErr != nil {
			return Report{}, callerErr
		}
		switch {
		case errors.Is(e, fundinglotverify.ErrPeriodicBudgetGraphRowsResource):
			return Report{}, ErrResource
		case errors.Is(e, fundinglotverify.ErrPeriodicBudgetGraphRowsTimeout):
			return Report{}, ErrTimeout
		case errors.Is(e, fundinglotverify.ErrPeriodicBudgetGraphRowsCanceled):
			return Report{}, ErrCanceled
		default:
			return Report{}, ErrInput
		}
	}
	if e = w.b.check(); e != nil {
		return Report{}, e
	}
	w.out.Base = base
	w.out.Scanned = Scan{Origins: uint64(len(rows.Origins)), Heads: uint64(len(rows.Heads)), Observations: uint64(len(rows.Observations)), OperationBindings: uint64(len(rows.OperationBindings)), WindowBindings: uint64(len(rows.WindowBindings)), TotalRows: w.b.rows, LogicalBytes: w.b.bytes}
	w.out.Unproven = Gaps{LegacyMembershipUnproven: base.Unproven.LegacyMembershipUnproven, LegacyEffectBoundaryUnproven: base.Unproven.LegacyEffectBoundaryUnproven, OperationSnapshotPrefixUnproven: base.Unproven.OperationSnapshotPrefixUnproven}
	w.index(rows)
	if w.err == nil {
		w.relations()
	}
	if w.err == nil {
		w.chains()
	}
	if w.err == nil && base.Mismatches == (fundinglotverify.PeriodicBudgetGraphMismatches{}) {
		w.replay()
	}
	if w.err != nil {
		return Report{}, w.err
	}
	if e = w.b.check(); e != nil {
		return Report{}, e
	}
	if w.resolved > w.out.Unproven.OperationSnapshotPrefixUnproven {
		return Report{}, ErrInput
	}
	w.out.Unproven.OperationSnapshotPrefixUnproven -= w.resolved
	w.out.Scope = "periodic_budget_snapshot_prefix_v1"
	w.out.Status = "balanced"
	if base.Mismatches != (fundinglotverify.PeriodicBudgetGraphMismatches{}) || w.out.Mismatches != (Mismatches{}) {
		w.out.Status = "mismatch"
	} else if w.out.Unproven != (Gaps{}) {
		w.out.Status = "indeterminate"
	}
	w.out.Balanced = w.out.Status == "balanced"
	w.out.CoverageComplete = w.out.Balanced
	if e = w.b.check(); e != nil {
		return Report{}, e
	}
	return w.out, nil
}

func (w *work) next() bool {
	if w.err == nil {
		w.err = w.b.tick()
	}
	return w.err == nil
}
func (w *work) mark(field *uint64, o object, cid string) {
	m := w.seen[field]
	if m == nil {
		m = map[object]bool{}
		w.seen[field] = m
	}
	if !m[o] {
		m[o] = true
		*field++
	}
	if cid != "" {
		w.bad[cid] = true
	}
}
func (w *work) shape(v any, o object, cid string, ok bool) {
	if !ok || !validText(reflect.ValueOf(v)) {
		w.mark(&w.out.Mismatches.StructuralMismatches, o, cid)
	}
}

func (w *work) index(r Rows) {
	for _, v := range r.Base.Controls {
		if !w.next() {
			return
		}
		w.controls[v.ID] = v
	}
	for _, v := range r.Base.Windows {
		if !w.next() {
			return
		}
		w.windows[v.ID] = v
		if finite(v.StartAt) {
			w.windowStarts[windowKey{v.ControlID, v.StartAt.Value.UTC()}] = v.ID
		}
	}
	for _, v := range r.Base.Operations {
		if !w.next() {
			return
		}
		w.ops[v.OperationID] = v
	}
	for _, v := range r.Base.Versions {
		if !w.next() {
			return
		}
		w.versions[versionKey{v.ControlID, v.Revision}] = v
	}
	for _, v := range r.Base.Allocations {
		if !w.next() {
			return
		}
		w.allocs[v.ID] = v
	}
	for _, v := range r.Base.Events {
		if !w.next() {
			return
		}
		w.events[v.ControlID] = append(w.events[v.ControlID], v)
	}
	for _, v := range r.Origins {
		if !w.next() {
			return
		}
		o := object{"origin", v.ControlID, ""}
		if _, ok := w.origins[v.ControlID]; ok {
			w.mark(&w.out.Mismatches.StructuralMismatches, o, v.ControlID)
		} else {
			w.origins[v.ControlID] = v
		}
		w.shape(v, o, v.ControlID, uuid(v.ControlID) && v.Version == "1" && xid(v.CreatedXID) && finite(v.TrackingStartedAt))
	}
	for _, v := range r.Heads {
		if !w.next() {
			return
		}
		o := object{"head", v.ControlID, ""}
		if _, ok := w.heads[v.ControlID]; ok {
			w.mark(&w.out.Mismatches.StructuralMismatches, o, v.ControlID)
		} else {
			w.heads[v.ControlID] = v
		}
		n, ok := sequence(v.Ordinal, false)
		w.shape(v, o, v.ControlID, uuid(v.ControlID) && ok && xid(v.UpdatedXID) && optionalUUID(v.LastObservationID) && (n == 0) == (v.LastObservationID == nil))
	}
	sources := map[object]Observation{}
	for _, v := range r.Observations {
		if !w.next() {
			return
		}
		o := object{"observation", v.ID, ""}
		if prior, ok := w.observations[v.ID]; ok {
			w.mark(&w.out.Mismatches.StructuralMismatches, o, v.ControlID)
			w.bad[prior.ControlID] = true
		} else {
			w.observations[v.ID] = v
		}
		w.journal[v.ControlID] = append(w.journal[v.ControlID], v)
		sk := object{v.Kind, v.SourceID, ""}
		if prior, ok := sources[sk]; ok {
			w.mark(&w.out.Mismatches.StructuralMismatches, o, v.ControlID)
			w.bad[prior.ControlID] = true
		} else {
			sources[sk] = v
		}
		_, a := sequence(v.Ordinal, true)
		_, b := sequence(v.EventSeq, false)
		_, c := sequence(v.ConfigurationRevision, true)
		ok := uuid(v.ID) && uuid(v.ControlID) && uuid(v.SourceID) && a && b && c && optionalUUID(v.PreviousID) && optionalUUID(v.LastEventID) && uuid(v.ConfigurationOperationID) && xid(v.CreatedXID) && optionalUUID(v.WindowID) && optionalUUID(v.WindowLastEventID)
		if v.WindowID == nil {
			ok = ok && v.WindowRevision == nil && v.WindowLastEventID == nil
		} else {
			n, valid := uint64(0), false
			if v.WindowRevision != nil {
				n, valid = sequence(*v.WindowRevision, false)
			}
			ok = ok && valid && (n == 0) == (v.WindowLastEventID == nil)
		}
		switch v.Kind {
		case "window_birth":
			ok = ok && nullTime(v.ObservedAt) && equals(v.WindowID, v.SourceID) && equals(v.WindowRevision, "0") && v.WindowLastEventID == nil
		case "command":
			ok = ok && finite(v.ObservedAt)
		default:
			ok = false
		}
		w.shape(v, o, v.ControlID, ok)
	}
	for _, v := range r.OperationBindings {
		if !w.next() {
			return
		}
		o := object{"operation_binding", v.OperationID, ""}
		cid := w.ops[v.OperationID].ResourceID
		if _, ok := w.operationBindings[v.OperationID]; ok {
			w.mark(&w.out.Mismatches.StructuralMismatches, o, cid)
		} else {
			w.operationBindings[v.OperationID] = v
		}
		w.shape(v, o, cid, uuid(v.OperationID) && optionalUUID(v.ObservationID))
	}
	for _, v := range r.WindowBindings {
		if !w.next() {
			return
		}
		o := object{"window_binding", v.WindowID, ""}
		cid := w.windows[v.WindowID].ControlID
		if _, ok := w.windowBindings[v.WindowID]; ok {
			w.mark(&w.out.Mismatches.StructuralMismatches, o, cid)
		} else {
			w.windowBindings[v.WindowID] = v
		}
		ok := uuid(v.WindowID) && optionalUUID(v.ObservationID) && (v.ObservationID == nil) == (v.ObservationCreatedXID == nil)
		if v.ObservationCreatedXID != nil {
			ok = ok && xid(*v.ObservationCreatedXID)
		}
		w.shape(v, o, cid, ok)
	}
}

func (w *work) relations() {
	m := &w.out.Mismatches
	for id, c := range w.controls {
		if !w.next() {
			return
		}
		o, ok := w.origins[id]
		valid := ok && o.Version == "1" && xid(o.CreatedXID) && o.CreatedXID == c.CreatedXID && equalTime(o.TrackingStartedAt, c.TrackingStartedAt)
		w.covered[id] = valid && !w.bad[id]
		if !w.covered[id] {
			w.out.Unproven.UncoveredControls++
		}
		if ok && !valid {
			w.mark(&m.OriginMismatches, object{"origin", id, ""}, id)
		}
		if valid {
			if _, ok = w.heads[id]; !ok {
				w.mark(&m.ChainMismatches, object{"missing_head", id, ""}, id)
			}
		}
	}
	for id := range w.origins {
		if !w.next() {
			return
		}
		if _, ok := w.controls[id]; !ok {
			w.mark(&m.OriginMismatches, object{"orphan_origin", id, ""}, id)
		}
	}
	for id := range w.heads {
		if !w.next() {
			return
		}
		if !w.covered[id] {
			w.mark(&m.OriginMismatches, object{"unbound_head", id, ""}, id)
		}
	}
	for id, op := range w.ops {
		if !w.next() {
			return
		}
		b, ok := w.operationBindings[id]
		bad := !ok
		if w.covered[op.ResourceID] {
			j, exists := w.observations[value(b.ObservationID)]
			bad = bad || !exists || j.Kind != "command" || j.SourceID != id || j.ControlID != op.ResourceID || j.CreatedXID != op.CreatedXID || !equalTime(j.ObservedAt, op.CreatedAt) || j.ConfigurationRevision != op.Revision
		} else {
			bad = bad || b.ObservationID != nil
		}
		if bad {
			w.mark(&m.BindingMismatches, object{"operation", id, ""}, op.ResourceID)
		}
	}
	for id, win := range w.windows {
		if !w.next() {
			return
		}
		b, ok := w.windowBindings[id]
		bad := !ok
		if w.covered[win.ControlID] {
			j, exists := w.observations[value(b.ObservationID)]
			bad = bad || !exists || j.Kind != "window_birth" || j.SourceID != id || j.ControlID != win.ControlID || !equals(b.ObservationCreatedXID, j.CreatedXID)
			if !bad {
				w.births[id] = j
			}
		} else {
			bad = bad || b.ObservationID != nil || b.ObservationCreatedXID != nil
		}
		if bad {
			w.mark(&m.BindingMismatches, object{"window", id, ""}, win.ControlID)
		}
	}
	for id := range w.operationBindings {
		if !w.next() {
			return
		}
		if _, ok := w.ops[id]; !ok {
			w.mark(&m.BindingMismatches, object{"orphan_operation_binding", id, ""}, "")
		}
	}
	for id := range w.windowBindings {
		if !w.next() {
			return
		}
		if _, ok := w.windows[id]; !ok {
			w.mark(&m.BindingMismatches, object{"orphan_window_binding", id, ""}, "")
		}
	}
	for id, j := range w.observations {
		if !w.next() {
			return
		}
		bad := !w.covered[j.ControlID]
		v, ok := w.versions[versionKey{j.ControlID, j.ConfigurationRevision}]
		bad = bad || !ok || v.OperationID != j.ConfigurationOperationID
		switch j.Kind {
		case "command":
			op, exists := w.ops[j.SourceID]
			b := w.operationBindings[j.SourceID]
			bad = bad || !exists || op.ResourceID != j.ControlID || !equals(b.ObservationID, id) || op.CreatedXID != j.CreatedXID || op.Revision != j.ConfigurationRevision || !equalTime(j.ObservedAt, op.CreatedAt)
		case "window_birth":
			win, exists := w.windows[j.SourceID]
			b := w.windowBindings[j.SourceID]
			bad = bad || !exists || win.ControlID != j.ControlID || !equals(b.ObservationID, id) || !equals(b.ObservationCreatedXID, j.CreatedXID)
		default:
			bad = true
		}
		if j.WindowID != nil {
			win, exists := w.windows[*j.WindowID]
			bad = bad || !exists || win.ControlID != j.ControlID
		}
		if bad {
			w.mark(&m.BindingMismatches, object{"observation", id, ""}, j.ControlID)
		}
	}
}

func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (w *work) chains() {
	m := &w.out.Mismatches
	for cid, list := range w.journal {
		if !w.next() {
			return
		}
		sort.Slice(list, func(i, j int) bool {
			w.next()
			a, _ := sequence(list[i].Ordinal, true)
			b, _ := sequence(list[j].Ordinal, true)
			return a < b
		})
		if w.err != nil {
			return
		}
		w.journal[cid] = list
		var previous *string
		var priorCut, priorRevision uint64
		var priorTail *string
		bySeq := map[uint64]string{}
		for _, e := range w.events[cid] {
			if !w.next() {
				return
			}
			seq, ok := sequence(e.Seq, true)
			if ok {
				bySeq[seq] = e.ID
			}
		}
		for i, j := range list {
			if !w.next() {
				return
			}
			ord, ok := sequence(j.Ordinal, true)
			if !ok || ord != uint64(i+1) || !same(j.PreviousID, previous) {
				w.mark(&m.ChainMismatches, object{"observation", j.ID, ""}, cid)
			}
			cut, valid := sequence(j.EventSeq, false)
			rev, rv := sequence(j.ConfigurationRevision, true)
			bad := !valid || !rv || cut < priorCut || rev < priorRevision || cut == priorCut && !same(j.LastEventID, priorTail)
			if cut == 0 {
				bad = bad || j.LastEventID != nil
			} else {
				tail, exists := bySeq[cut]
				bad = bad || !exists || !equals(j.LastEventID, tail)
			}
			if bad {
				w.mark(&m.CutMismatches, object{"observation", j.ID, ""}, cid)
			}
			previous = &j.ID
			priorCut = cut
			priorTail = j.LastEventID
			priorRevision = rev
		}
		h, ok := w.heads[cid]
		bad := !ok || h.Ordinal != strconv.Itoa(len(list)) || !same(h.LastObservationID, previous)
		if len(list) > 0 {
			bad = bad || h.UpdatedXID != list[len(list)-1].CreatedXID
		}
		if bad {
			w.mark(&m.ChainMismatches, object{"head", cid, ""}, cid)
		}
	}
	for cid, h := range w.heads {
		if !w.next() {
			return
		}
		if len(w.journal[cid]) == 0 && (h.Ordinal != "0" || h.LastObservationID != nil || h.UpdatedXID != w.origins[cid].CreatedXID) {
			w.mark(&m.ChainMismatches, object{"head", cid, ""}, cid)
		}
	}
	for cid, events := range w.events {
		for _, e := range events {
			if !w.next() {
				return
			}
			a := w.allocs[e.AllocationID]
			if a.WindowID == nil || !w.covered[cid] {
				continue
			}
			birth, ok := w.births[*a.WindowID]
			seq, aok := sequence(e.Seq, true)
			cut, bok := sequence(birth.EventSeq, false)
			if !ok || !aok || !bok || seq <= cut {
				w.mark(&m.CutMismatches, object{"window_event", e.ID, ""}, cid)
			}
		}
	}
}

type totals struct {
	held, spent big.Int
	count       uint64
	tail        string
}

func (w *work) replay() {
	// In a mismatch-free original graph this reproduces only the original
	// conservative exemption, not any dynamic amount inference.
	zero := map[string]bool{}
	for cid := range w.controls {
		zero[cid] = true
	}
	for cid, list := range w.events {
		for _, e := range list {
			if !w.next() {
				return
			}
			if e.HeldDelta != "0" {
				zero[cid] = false
			}
		}
	}
	for id, op := range w.ops {
		if !w.next() {
			return
		}
		c := w.controls[op.ResourceID]
		_, exists := w.windowStarts[windowKey{c.ID, periodStart(c.PeriodKind, op.CreatedAt.Value)}]
		w.baseGap[id] = exists || !zero[c.ID]
	}
	for cid, journal := range w.journal {
		if !w.next() {
			return
		}
		if w.bad[cid] || !w.covered[cid] {
			continue
		}
		events := append([]fundinglotverify.PeriodicBudgetEventRow(nil), w.events[cid]...)
		sort.Slice(events, func(i, j int) bool {
			w.next()
			a, _ := sequence(events[i].Seq, true)
			b, _ := sequence(events[j].Seq, true)
			return a < b
		})
		if w.err != nil {
			return
		}
		var control totals
		windows := map[string]*totals{}
		born := map[string]bool{}
		pos := 0
		for _, j := range journal {
			if !w.next() {
				return
			}
			if w.bad[cid] {
				break
			}
			cut, _ := sequence(j.EventSeq, false)
			for pos < len(events) {
				e := events[pos]
				seq, _ := sequence(e.Seq, true)
				if seq > cut {
					break
				}
				if !w.next() {
					return
				}
				h, hok := amount(e.HeldDelta, true)
				s, sok := amount(e.SpentDelta, true)
				if !hok || !sok {
					w.mark(&w.out.Mismatches.CutMismatches, object{"event", e.ID, ""}, cid)
					break
				}
				control.held.Add(&control.held, h)
				control.spent.Add(&control.spent, s)
				a := w.allocs[e.AllocationID]
				if a.WindowID != nil {
					win := windows[*a.WindowID]
					if win == nil {
						win = &totals{}
						windows[*a.WindowID] = win
					}
					win.held.Add(&win.held, h)
					win.spent.Add(&win.spent, s)
					win.count++
					win.tail = e.ID
				}
				pos++
			}
			if w.bad[cid] {
				break
			}
			if j.Kind == "window_birth" {
				born[j.SourceID] = true
				continue
			}
			op := w.ops[j.SourceID]
			c := w.controls[cid]
			wid, exists := w.windowStarts[windowKey{cid, periodStart(c.PeriodKind, op.CreatedAt.Value)}]
			exists = exists && born[wid]
			var win *totals
			if exists {
				win = windows[wid]
				if win == nil {
					win = &totals{}
				}
			}
			if !w.command(j, op, c, wid, exists, win, &control) {
				w.mark(&w.out.Mismatches.SnapshotMismatches, object{"operation", op.OperationID, ""}, cid)
				continue
			}
			w.out.CertifiedCommands++
			if w.baseGap[op.OperationID] {
				w.resolved++
			}
		}
	}
}

func (w *work) command(j Observation, op fundinglotverify.PeriodicBudgetOperationRow, c fundinglotverify.PeriodicBudgetControlRow, wid string, exists bool, win, control *totals) bool {
	s, ok := decodeSnapshot(op.ResultSnapshot)
	if !ok {
		return false
	}
	carry := new(big.Int).Set(&control.held)
	if !exists {
		return j.WindowID == nil && j.WindowRevision == nil && j.WindowLastEventID == nil && s.window == nil && carry.Sign() >= 0 && s.carry == carry.String()
	}
	if win == nil || !equals(j.WindowID, wid) || !equals(j.WindowRevision, strconv.FormatUint(win.count, 10)) {
		return false
	}
	if win.count == 0 {
		if j.WindowLastEventID != nil {
			return false
		}
	} else if !equals(j.WindowLastEventID, win.tail) {
		return false
	}
	carry.Sub(carry, &win.held)
	if carry.Sign() < 0 || s.carry != carry.String() || s.window == nil {
		return false
	}
	window := w.windows[wid]
	return s.window.id == wid && s.window.start == window.StartAt.Value.UTC().Format(utc6) && s.window.end == window.EndAt.Value.UTC().Format(utc6) && s.window.held == win.held.String() && s.window.spent == win.spent.String()
}

const utc6 = "2006-01-02T15:04:05.000000Z"
