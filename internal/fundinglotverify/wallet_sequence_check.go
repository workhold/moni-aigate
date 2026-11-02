package fundinglotverify

import (
	"context"
	"math/big"
	"reflect"
	"strconv"
)

type wsPair struct{ a, b string }
type wsRef struct{ count, index int }
type wsIndex[K comparable] map[K]wsRef

func wsAdd[K comparable](index wsIndex[K], key K, row int, duplicates *uint64) {
	ref := index[key]
	if ref.count == 0 {
		ref.index = row
	} else if ref.count == 1 {
		*duplicates++
	}
	ref.count++
	index[key] = ref
}

// A duplicate group never exposes an arbitrarily selected row to a relation
// check. The stored position is usable only when count is exactly one.
func wsUnique[K comparable](index wsIndex[K], key K) (int, bool) {
	ref := index[key]
	return ref.index, ref.count == 1
}

type wsOperationValues struct {
	seq, previousSeq, count, xid uint64
	seqOK, previousOK            bool
	countOK, xidOK               bool
	delta                        wsDeltas
}

type wsEventValues struct {
	seq, xid     uint64
	seqOK, xidOK bool
	delta        wsDeltas
}

type wsAggregate struct {
	count uint64
	valid bool
	a, h  big.Int
	s, i  big.Int
}

type wsGraph struct {
	in         WalletSequenceRows
	w          *wsWork
	epochID    string
	epochOK    bool
	epochs     wsIndex[string]
	tenants    wsIndex[string]
	ops        wsIndex[string]
	heads      wsIndex[string]
	legacy     wsIndex[wsPair]
	journals   wsIndex[string]
	events     wsIndex[string]
	sequences  wsIndex[wsPair]
	previous   wsIndex[string]
	opSources  wsIndex[wsPair]
	opJournal  wsIndex[string]
	oldJournal wsIndex[string]
	opLots     wsIndex[wsPair]
	opValues   []wsOperationValues
	eValues    []wsEventValues
	births     []uint64
	birthOK    []bool
	jxids      []uint64
	jxidOK     []bool
	tails      []uint64
	tailOK     []bool
	tenantOps  map[string]uint64
	tenantMax  map[string]uint64
	aggregate  map[string]*wsAggregate
}

func checkWalletSequenceRows(ctx context.Context, in WalletSequenceRows) (WalletSequenceRowsCheck, error) {
	if ctx == nil {
		return WalletSequenceRowsCheck{}, ErrWalletSequenceRowsInput
	}
	w := &wsWork{ctx: ctx}
	if err := w.check(); err != nil {
		return WalletSequenceRowsCheck{}, err
	}
	if err := w.budget(in); err != nil {
		return WalletSequenceRowsCheck{}, err
	}
	w.findings = make(map[*uint64]map[wsObject]bool)
	w.out.Scope = "supplied_wallet_sequence_rows"
	w.out.Scanned = WalletSequenceRowCounts{
		Epochs: uint64(len(in.Epochs)), Tenants: uint64(len(in.Tenants)),
		Operations: uint64(len(in.Operations)), Heads: uint64(len(in.Heads)),
		LegacySources: uint64(len(in.LegacySources)), Journals: uint64(len(in.Journals)),
		Events: uint64(len(in.Events)),
	}
	g := wsGraph{
		in: in, w: w,
		epochs: make(wsIndex[string]), tenants: make(wsIndex[string]),
		ops: make(wsIndex[string]), heads: make(wsIndex[string]),
		legacy: make(wsIndex[wsPair]), journals: make(wsIndex[string]),
		events: make(wsIndex[string]), sequences: make(wsIndex[wsPair]),
		previous: make(wsIndex[string]), opSources: make(wsIndex[wsPair]),
		opJournal: make(wsIndex[string]), oldJournal: make(wsIndex[string]),
		opLots:   make(wsIndex[wsPair]),
		opValues: make([]wsOperationValues, len(in.Operations)),
		eValues:  make([]wsEventValues, len(in.Events)),
		births:   make([]uint64, len(in.Tenants)), birthOK: make([]bool, len(in.Tenants)),
		jxids: make([]uint64, len(in.Journals)), jxidOK: make([]bool, len(in.Journals)),
		tails: make([]uint64, len(in.Heads)), tailOK: make([]bool, len(in.Heads)),
		tenantOps: make(map[string]uint64), tenantMax: make(map[string]uint64),
		aggregate: make(map[string]*wsAggregate),
	}
	g.index()
	if err := w.check(); err != nil {
		return WalletSequenceRowsCheck{}, err
	}
	g.checkTenants()
	g.checkOperations()
	g.checkHeads()
	g.checkLegacy()
	g.checkEvents()
	g.checkTotals()
	if err := w.check(); err != nil {
		return WalletSequenceRowsCheck{}, err
	}
	w.out.RowsConsistent = true
	values := reflect.ValueOf(w.out.Mismatches)
	for i := 0; i < values.NumField(); i++ {
		if values.Field(i).Uint() != 0 {
			w.out.RowsConsistent = false
			break
		}
	}
	return w.out, nil
}

func (g *wsGraph) raw(row any, o wsObject) {
	if !wsText(row) {
		g.w.mark(&g.w.out.Mismatches.InvalidTextRows, o)
	}
}

func (g *wsGraph) invalid(valid bool, o wsObject) {
	if !valid {
		g.w.mark(&g.w.out.Mismatches.InvalidValueRows, o)
	}
}

func (g *wsGraph) index() {
	w, m := g.w, &g.w.out.Mismatches
	if len(g.in.Epochs) != 1 {
		m.EpochRows = 1
	}
	for i, r := range g.in.Epochs {
		if !w.tick() {
			return
		}
		o := wsObject{wsEpoch, i}
		g.raw(r, o)
		g.invalid(wsUUID(r.ID), o)
		wsAdd(g.epochs, r.ID, i, &m.DuplicateEpochIDs)
	}
	if len(g.in.Epochs) == 1 {
		g.epochID = g.in.Epochs[0].ID
		g.epochOK = wsUUID(g.epochID) && wsText(g.in.Epochs[0])
	}
	for i, r := range g.in.Tenants {
		if !w.tick() {
			return
		}
		o := wsObject{wsTenant, i}
		g.raw(r, o)
		wsAdd(g.tenants, r.ID, i, &m.DuplicateTenantIDs)
		g.birthOK[i] = true
		if r.CreatedXID != nil {
			g.births[i], g.birthOK[i] = wsUint(*r.CreatedXID, true, false)
			g.invalid(g.birthOK[i], o)
		}
	}
	for i, r := range g.in.Operations {
		if !w.tick() {
			return
		}
		o := wsObject{wsOperation, i}
		g.raw(r, o)
		v := &g.opValues[i]
		v.seq, v.seqOK = wsUint(r.Seq, false, true)
		v.count, v.countOK = wsUint(r.EventCount, false, true)
		v.xid, v.xidOK = wsUint(r.CreatedXID, true, false)
		v.previousOK = r.PreviousSeq == nil
		if r.PreviousSeq != nil {
			v.previousSeq, v.previousOK = wsUint(*r.PreviousSeq, false, true)
		}
		validPreviousID := r.PreviousID == nil || wsUUID(*r.PreviousID)
		g.invalid(wsUUID(r.ID) && wsUUID(r.EpochID) && wsOperationKind(r.Kind) && v.seqOK && v.countOK && v.xidOK && v.previousOK && validPreviousID, o)
		v.delta = w.deltas(r.AvailableDelta, r.HeldDelta, r.ConsumedDelta, r.IssuedDelta, o)
		wsAdd(g.ops, r.ID, i, &m.DuplicateOperationIDs)
		wsAdd(g.sequences, wsPair{r.TenantID, r.Seq}, i, &m.DuplicateTenantSequences)
		if r.PreviousID != nil {
			wsAdd(g.previous, *r.PreviousID, i, &m.DuplicatePredecessorIDs)
		}
		if !wsGenesisKind(r.Kind) {
			wsAdd(g.opSources, wsPair{wsSourceKind(r.Kind), r.SourceID}, i, &m.DuplicateOperationSourceKeys)
		}
		if r.JournalID != nil {
			wsAdd(g.opJournal, *r.JournalID, i, &m.DuplicateOperationJournalIDs)
		}
		g.tenantOps[r.TenantID]++
		if v.seqOK && v.seq > g.tenantMax[r.TenantID] {
			g.tenantMax[r.TenantID] = v.seq
		}
	}
	for i, r := range g.in.Heads {
		if !w.tick() {
			return
		}
		o := wsObject{wsHead, i}
		g.raw(r, o)
		g.tails[i], g.tailOK[i] = wsUint(r.TailSeq, false, true)
		g.invalid(wsUUID(r.EpochID) && wsUUID(r.TailOperationID) && g.tailOK[i], o)
		wsAdd(g.heads, r.TenantID, i, &m.DuplicateHeadTenants)
	}
	for i, r := range g.in.LegacySources {
		if !w.tick() {
			return
		}
		o := wsObject{wsLegacy, i}
		g.raw(r, o)
		amount, ok := wsUint(r.Amount, false, true)
		positive := r.Kind != "reserve" && r.Kind != "finish"
		g.invalid(wsUUID(r.EpochID) && wsLegacyKind(r.Kind) && ok && (!positive || amount > 0), o)
		wsAdd(g.legacy, wsPair{r.Kind, r.SourceID}, i, &m.DuplicateLegacySourceKeys)
		wsAdd(g.oldJournal, r.JournalID, i, &m.DuplicateLegacyJournalIDs)
	}
	jOperations := make(wsIndex[string])
	for i, r := range g.in.Journals {
		if !w.tick() {
			return
		}
		o := wsObject{wsJournal, i}
		g.raw(r, o)
		g.jxids[i], g.jxidOK[i] = wsUint(r.CreatedTXID, true, true)
		g.invalid(g.jxidOK[i] && r.Currency == "CNY", o)
		wsAdd(g.journals, r.ID, i, &m.DuplicateJournalIDs)
		wsAdd(jOperations, r.OperationID, i, &m.DuplicateJournalOperationIDs)
	}
	for i, r := range g.in.Events {
		if !w.tick() {
			return
		}
		o := wsObject{wsEvent, i}
		g.raw(r, o)
		v := &g.eValues[i]
		v.seq, v.seqOK = wsUint(r.WalletSeq, false, true)
		v.xid, v.xidOK = wsUint(r.CreatedXID, true, false)
		g.invalid(wsUUID(r.ID) && wsUUID(r.LotID) && wsUUID(r.WalletOperationID) && wsEventKind(r.Kind) && v.seqOK && v.xidOK, o)
		v.delta = w.deltas(r.AvailableDelta, r.HeldDelta, r.ConsumedDelta, r.IssuedDelta, o)
		wsAdd(g.events, r.ID, i, &m.DuplicateEventIDs)
		wsAdd(g.opLots, wsPair{r.WalletOperationID, r.LotID}, i, &m.DuplicateEventOperationLots)
		a := g.aggregate[r.WalletOperationID]
		if a == nil {
			a = &wsAggregate{valid: true}
			g.aggregate[r.WalletOperationID] = a
		}
		a.count++
		if v.delta.valid {
			a.a.Add(&a.a, v.delta.a)
			a.h.Add(&a.h, v.delta.h)
			a.s.Add(&a.s, v.delta.s)
			a.i.Add(&a.i, v.delta.i)
		} else {
			a.valid = false
		}
	}
}

func (g *wsGraph) tenant(tenantID string, o wsObject) (int, bool) {
	i, ok := wsUnique(g.tenants, tenantID)
	if !ok {
		g.w.mark(&g.w.out.Mismatches.TenantIdentityRows, o)
	}
	return i, ok
}

func (g *wsGraph) checkTenants() {
	w, m := g.w, &g.w.out.Mismatches
	for i, r := range g.in.Tenants {
		if !w.tick() {
			return
		}
		o := wsObject{wsTenant, i}
		if _, ok := wsUnique(g.tenants, r.ID); !ok {
			continue
		}
		if _, ok := wsUnique(g.sequences, wsPair{r.ID, "0"}); !ok {
			w.mark(&m.GenesisRows, o)
		}
		if _, ok := wsUnique(g.heads, r.ID); !ok {
			w.mark(&m.HeadRows, o)
		}
	}
}

func (g *wsGraph) checkOperations() {
	w, m := g.w, &g.w.out.Mismatches
	for i, r := range g.in.Operations {
		if !w.tick() {
			return
		}
		o, v := wsObject{wsOperation, i}, g.opValues[i]
		tenantIndex, hasTenant := g.tenant(r.TenantID, o)
		if !g.epochOK || r.EpochID != g.epochID {
			w.mark(&m.SequenceRows, o)
		}
		if _, ok := wsUnique(g.sequences, wsPair{r.TenantID, r.Seq}); !ok {
			w.mark(&m.SequenceRows, o)
		}
		if v.seqOK && v.seq == 0 {
			if !wsGenesisKind(r.Kind) || r.SourceID != r.TenantID || r.PreviousID != nil || r.PreviousSeq != nil || r.JournalID != nil || r.JournalOperationID != nil {
				w.mark(&m.GenesisRows, o)
			}
			if hasTenant {
				t := g.in.Tenants[tenantIndex]
				if t.CreatedXID == nil {
					if r.Kind != "epoch_opening" {
						w.mark(&m.GenesisRows, o)
					}
				} else if r.Kind != "tenant_genesis" || !g.birthOK[tenantIndex] || !v.xidOK || v.xid != g.births[tenantIndex] {
					w.mark(&m.GenesisRows, o)
				}
			}
			if r.Kind == "tenant_genesis" {
				a := g.aggregate[r.ID]
				if !v.delta.zero() || !v.countOK || v.count != 0 || a != nil && a.count != 0 {
					w.mark(&m.GenesisRows, o)
				}
			}
		} else if v.seqOK {
			if wsGenesisKind(r.Kind) || r.PreviousID == nil || r.PreviousSeq == nil || !v.previousOK || v.previousSeq != v.seq-1 {
				w.mark(&m.SequenceRows, o)
			}
			if r.PreviousID != nil {
				p, ok := wsUnique(g.ops, *r.PreviousID)
				if !ok {
					w.mark(&m.SequenceRows, o)
				} else {
					prev, pv := g.in.Operations[p], g.opValues[p]
					seqParent, uniqueSeq := wsUnique(g.sequences, wsPair{r.TenantID, strconv.FormatUint(v.seq-1, 10)})
					if prev.TenantID != r.TenantID || prev.EpochID != r.EpochID || !pv.seqOK || pv.seq != v.seq-1 || !uniqueSeq || seqParent != p {
						w.mark(&m.SequenceRows, o)
					}
				}
			}
		}
		if !wsGenesisKind(r.Kind) {
			g.checkOperationJournal(i)
			if g.legacy[wsPair{wsSourceKind(r.Kind), r.SourceID}].count != 0 || r.JournalID != nil && g.oldJournal[*r.JournalID].count != 0 {
				w.mark(&m.SourceBoundaryRows, o)
			}
		}
	}
}

func (g *wsGraph) checkOperationJournal(i int) {
	w, m := g.w, &g.w.out.Mismatches
	r, v := g.in.Operations[i], g.opValues[i]
	o := wsObject{wsOperation, i}
	if r.JournalID == nil || r.JournalOperationID == nil {
		w.mark(&m.JournalBindingRows, o)
		return
	}
	j, ok := wsUnique(g.journals, *r.JournalID)
	if !ok {
		w.mark(&m.JournalBindingRows, o)
		return
	}
	journal := g.in.Journals[j]
	if journal.TenantID != r.TenantID || journal.OperationID != *r.JournalOperationID || journal.Currency != "CNY" || !wsJournalKind(r.Kind, journal.Kind) || !v.xidOK || !g.jxidOK[j] || v.xid != g.jxids[j] {
		w.mark(&m.JournalBindingRows, o)
	}
	switch r.Kind {
	case "reserve", "finish", "consumption_refund", "legacy_consumption_refund":
		if *r.JournalOperationID != r.SourceID {
			w.mark(&m.JournalBindingRows, o)
		}
	}
}

func (g *wsGraph) checkHeads() {
	w, m := g.w, &g.w.out.Mismatches
	for i, r := range g.in.Heads {
		if !w.tick() {
			return
		}
		o := wsObject{wsHead, i}
		g.tenant(r.TenantID, o)
		count := g.tenantOps[r.TenantID]
		if !g.epochOK || r.EpochID != g.epochID || !g.tailOK[i] || count == 0 || g.tails[i] != count-1 || g.tails[i] != g.tenantMax[r.TenantID] {
			w.mark(&m.HeadRows, o)
		}
		t, ok := wsUnique(g.ops, r.TailOperationID)
		if !ok {
			w.mark(&m.HeadRows, o)
			continue
		}
		tail, tv := g.in.Operations[t], g.opValues[t]
		if tail.TenantID != r.TenantID || tail.EpochID != r.EpochID || !g.tailOK[i] || !tv.seqOK || tv.seq != g.tails[i] {
			w.mark(&m.HeadRows, o)
		}
	}
}

func (g *wsGraph) checkLegacy() {
	w, m := g.w, &g.w.out.Mismatches
	for i, r := range g.in.LegacySources {
		if !w.tick() {
			return
		}
		o := wsObject{wsLegacy, i}
		if tenantIndex, ok := g.tenant(r.TenantID, o); ok && g.in.Tenants[tenantIndex].CreatedXID != nil {
			w.mark(&m.SourceBoundaryRows, o)
		}
		if !g.epochOK || r.EpochID != g.epochID || g.opSources[wsPair{r.Kind, r.SourceID}].count != 0 || g.opJournal[r.JournalID].count != 0 {
			w.mark(&m.SourceBoundaryRows, o)
		}
		j, ok := wsUnique(g.journals, r.JournalID)
		if !ok {
			w.mark(&m.JournalBindingRows, o)
			continue
		}
		journal := g.in.Journals[j]
		if journal.TenantID != r.TenantID || journal.OperationID != r.JournalOperationID || journal.Currency != "CNY" || !g.jxidOK[j] || !wsJournalKind(r.Kind, journal.Kind) {
			w.mark(&m.JournalBindingRows, o)
		}
		switch r.Kind {
		case "reserve", "finish", "consumption_refund":
			if r.JournalOperationID != r.SourceID {
				w.mark(&m.JournalBindingRows, o)
			}
		}
	}
}

func (g *wsGraph) checkEvents() {
	w, m := g.w, &g.w.out.Mismatches
	for i, r := range g.in.Events {
		if !w.tick() {
			return
		}
		o, v := wsObject{wsEvent, i}, g.eValues[i]
		g.tenant(r.TenantID, o)
		p, ok := wsUnique(g.ops, r.WalletOperationID)
		if !ok {
			w.mark(&m.EventBindingRows, o)
			continue
		}
		parent, pv := g.in.Operations[p], g.opValues[p]
		if parent.Kind == "tenant_genesis" || r.TenantID != parent.TenantID || !v.seqOK || !pv.seqOK || v.seq != pv.seq || r.Kind != wsExpectedEventKind(parent.Kind) || !wsSameOptional(r.JournalID, parent.JournalID) || !wsSameOptional(r.OperationID, parent.JournalOperationID) || !v.xidOK || !pv.xidOK || v.xid != pv.xid {
			w.mark(&m.EventBindingRows, o)
		}
	}
}

func (g *wsGraph) checkTotals() {
	w, m := g.w, &g.w.out.Mismatches
	for i, r := range g.in.Operations {
		if !w.tick() {
			return
		}
		o, v := wsObject{wsOperation, i}, g.opValues[i]
		if _, ok := wsUnique(g.ops, r.ID); !ok {
			// There is no unique owner to which these event totals can bind.
			w.mark(&m.OperationTotalsRows, o)
			continue
		}
		a := g.aggregate[r.ID]
		if a == nil {
			a = &wsAggregate{valid: true}
		}
		if v.countOK && v.count != a.count {
			w.mark(&m.OperationTotalsRows, o)
		}
		if v.delta.valid && a.valid && (v.delta.a.Cmp(&a.a) != 0 || v.delta.h.Cmp(&a.h) != 0 || v.delta.s.Cmp(&a.s) != 0 || v.delta.i.Cmp(&a.i) != 0) {
			w.mark(&m.OperationTotalsRows, o)
		}
	}
}
