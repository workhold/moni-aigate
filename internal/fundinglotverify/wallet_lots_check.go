package fundinglotverify

import (
	"context"
	"reflect"
	"sort"
)

func checkWalletLotsRows(ctx context.Context, in WalletLotsRows) (WalletLotsRowsCheck, error) {
	if ctx == nil {
		return WalletLotsRowsCheck{}, ErrWalletLotsRowsInput
	}
	w := &wlWork{ctx: ctx, in: in,
		tables: map[string]*wlTable{}, fields: map[wlField]map[string][]wlRef{},
		findings: map[*uint64]map[wlRef]bool{}, missing: map[wlKey]bool{}, tainted: map[string]bool{},
		sources: map[wlKey]*wlSource{}, openings: map[string][]wlOpeningPlan{},
		legacyTerminal: map[string]wlLegacyTerminalPlan{}, operations: map[string][]wlRef{},
		barriers: map[wlRef]bool{}, used: map[wlRef]bool{}, claimed: map[wlRef]bool{}, deferred: map[wlRef]bool{},
	}
	if err := w.budget(); err != nil {
		return WalletLotsRowsCheck{}, err
	}
	w.out.Scope = "supplied_wallet_lots_rows"
	for _, pass := range []func(){w.buildIndexes, w.buildWalletSources, w.checkWalletLegacy, w.bindWalletSources, w.checkSequence, w.replayWalletLots, w.finishWalletLots, w.sweep} {
		pass()
		if err := w.check(); err != nil {
			return WalletLotsRowsCheck{}, err
		}
	}
	w.out.Status = "consistent"
	for _, group := range []any{w.out.Sequence, w.out.Mismatches} {
		v := reflect.ValueOf(group)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Uint() != 0 {
				w.out.Status = "mismatch"
			}
		}
	}
	if w.out.Status == "consistent" && w.out.Unproven.ReplayPrefixUnproven != 0 {
		w.out.Status = "indeterminate"
	}
	w.out.RowsConsistent = w.out.Status == "consistent"
	if err := w.check(); err != nil {
		return WalletLotsRowsCheck{}, err
	}
	return w.out, nil
}

// Run WS against projections made here from the raw inputs. Its public result
// is only a set of findings, never a gate or authority for the numeric replay.
func (w *wlWork) checkSequence() {
	in := WalletSequenceRows{Tenants: w.in.Tenants, Heads: w.in.Heads}
	for _, r := range w.in.Epochs {
		if !w.tick() {
			return
		}
		in.Epochs = append(in.Epochs, WalletSequenceEpochRow{ID: r.ID})
	}
	for _, r := range w.in.Operations {
		if !w.tick() {
			return
		}
		in.Operations = append(in.Operations, r.WalletSequenceOperationRow)
	}
	for _, r := range w.in.LegacySources {
		if !w.tick() {
			return
		}
		in.LegacySources = append(in.LegacySources, r.WalletSequenceLegacySourceRow)
	}
	for _, r := range w.in.Journals {
		if !w.tick() {
			return
		}
		in.Journals = append(in.Journals, r.WalletSequenceJournalRow)
	}
	for _, r := range w.in.Events {
		if !w.tick() {
			return
		}
		in.Events = append(in.Events, r.WalletSequenceEventRow)
	}
	out, err := CheckWalletSequenceRows(w.ctx, in)
	if err != nil {
		w.err = wlMapSequenceError(err)
		return
	}
	w.out.Sequence = out.Mismatches
	// Establish replay order independently per tenant. Bad declared deltas,
	// event counts or head caches do not erase otherwise derivable amounts.
	seq := make(map[wlRef]uint64, len(w.in.Operations))
	groups := make(map[string]map[uint64][]wlRef)
	for i, r := range w.in.Operations {
		if !w.tick() {
			return
		}
		ref := wlRef{"Operations", i}
		w.operations[r.TenantID] = append(w.operations[r.TenantID], ref)
		n, ok := wsUint(r.Seq, false, true)
		seq[ref] = n
		// With no numeric position, no earlier prefix can be established.
		if !ok {
			w.taint(r.TenantID)
		}
		if groups[r.TenantID] == nil {
			groups[r.TenantID] = make(map[uint64][]wlRef)
		}
		if ok {
			groups[r.TenantID][n] = append(groups[r.TenantID][n], ref)
		}
		t, _, tenantOK := wlGet[WalletSequenceTenantRow](w, "Tenants", r.TenantID)
		if !ok || !tenantOK || !w.epochKnown || r.EpochID != w.epoch.ID || !wsUUID(r.ID) || len(w.tables["Operations"].primary[wlKey{id: r.ID}]) != 1 {
			w.barriers[ref] = true
		}
		// Every seq0 statement is qualified independently. A repeated seq0
		// group never chooses its first row as the genesis oracle.
		if ok && n == 0 {
			valid := tenantOK && r.SourceID == r.TenantID && r.PreviousID == nil && r.PreviousSeq == nil && r.JournalID == nil && r.JournalOperationID == nil
			if tenantOK && t.CreatedXID == nil {
				valid = valid && r.Kind == "epoch_opening"
			} else if tenantOK {
				birth, bok := wsUint(*t.CreatedXID, true, false)
				xid, xok := wsUint(r.CreatedXID, true, false)
				valid = valid && r.Kind == "tenant_genesis" && bok && xok && birth == xid
			}
			if !valid {
				w.mark(&w.out.Mismatches.GenesisRows, ref)
				w.barriers[ref] = true
			}
		}
	}
	for i, tenant := range w.in.Tenants {
		if !w.tick() {
			return
		}
		if len(w.tables["Tenants"].primary[wlKey{id: tenant.ID}]) != 1 || len(groups[tenant.ID][0]) == 0 {
			w.mark(&w.out.Mismatches.GenesisRows, wlRef{"Tenants", i})
			w.need("wallet_genesis", tenant.ID)
			w.taint(tenant.ID)
		}
	}
	for tenant, refs := range w.operations {
		if err := w.check(); err != nil {
			w.err = err
			return
		}
		sort.Slice(refs, func(i, j int) bool { return seq[refs[i]] < seq[refs[j]] })
		if err := w.check(); err != nil {
			w.err = err
			return
		}
		for i, ref := range refs {
			if !w.tick() {
				return
			}
			r, _ := wlAt[WalletLotsOperationRow](w, ref)
			n := seq[ref]
			if n != uint64(i) || len(groups[tenant][n]) != 1 {
				w.barriers[ref] = true
			}
			if n == 0 {
				continue
			}
			parents := groups[tenant][n-1]
			prev, pok := uint64(0), false
			if r.PreviousSeq != nil {
				prev, pok = wsUint(*r.PreviousSeq, false, true)
			}
			if wsGenesisKind(r.Kind) || r.PreviousID == nil || !pok || prev != n-1 || len(parents) != 1 {
				w.barriers[ref] = true
			} else {
				p, _ := wlAt[WalletLotsOperationRow](w, parents[0])
				if *r.PreviousID != p.ID {
					w.barriers[ref] = true
				}
			}
		}
	}
}
