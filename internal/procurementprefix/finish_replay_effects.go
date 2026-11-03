package procurementprefix

import "math/big"

func finishAccountKey(tenant *string, kind string) string {
	if tenant == nil {
		return tupleKey("platform", kind)
	}
	return tupleKey("tenant", *tenant, kind)
}

func (r *finishReplay) ledger(o CustomerFinishOutcomeRow, R, charge *big.Int) {
	journalID := requestOptional(o.CustomerJournalID)
	j, found := r.journals[journalID]
	if !found || j.TenantID != o.TenantID || !requestPointer(o.SettlementID, j.OperationID) || j.Kind != o.ResultStatus || j.Reference != o.RequestID || j.Actor != "system" || j.Currency != "CNY" || j.CreatedTxid != o.CreatedXID || !finishPointer(o.RootID, o.StepNo, o.SourceID, o.CreatedXID, "customer_journal", j.PPRootID, j.PPRootStep, j.PPSourceKind, j.PPSourceID, j.PPCreatedXID, j.PPSourceRole) {
		if o.CustomerJournalID == nil {
			r.mark(2, "journal_obligation", o.ID)
		} else {
			r.mark(2, "journal", journalID)
		}
	}
	want := map[string]*big.Int{"HELD": new(big.Int).Neg(R), "AVAILABLE": new(big.Int).Sub(R, charge)}
	if charge.Sign() > 0 {
		want["REVENUE"] = new(big.Int).Set(charge)
	}
	seenKinds, seenLines := make(keySet), make(keySet)
	expectedAccounts := make(keySet)
	for kind := range want {
		if !r.tick() {
			return
		}
		tenant := &o.TenantID
		if kind == "REVENUE" {
			tenant = nil
		}
		if a, ok := r.accountKinds[finishAccountKey(tenant, kind)]; ok {
			expectedAccounts[a.ID] = struct{}{}
		}
	}
	for _, e := range r.ledgerFor[o.ID] {
		if !r.tick() {
			return
		}
		bad := func() { r.mark(2, "effect", e.FinishOutcomeID, e.AccountID) }
		delta, deltaOK := finishInt(e.SignedDelta)
		before, beforeOK := finishInt(e.BeforeBalance)
		after, afterOK := finishInt(e.AfterBalance)
		expected, kindOK := want[e.AccountKind]
		_, kindSeen := seenKinds[e.AccountKind]
		seenKinds[e.AccountKind] = struct{}{}
		a, aok := r.accounts[e.AccountID]
		l, lok := r.lines[e.JournalLineID]
		if !kindOK || kindSeen || !deltaOK || !beforeOK || !afterOK || new(big.Int).Add(big.NewInt(before), big.NewInt(delta)).Cmp(big.NewInt(after)) != 0 || kindOK && expected.Cmp(big.NewInt(delta)) != 0 || !aok || a.Kind != e.AccountKind || !requestSameOptional(a.TenantID, e.AccountTenantID) || e.TenantID != o.TenantID || !finishPosition(o.RootID, o.StepNo, o.CreatedXID, e.RootID, e.StepNo, e.StepKind, e.SourceID, e.CreatedXID, o.SourceID) {
			bad()
		}
		if e.AccountKind == "REVENUE" {
			if e.AccountTenantID != nil {
				bad()
			}
		} else if !requestPointer(e.AccountTenantID, o.TenantID) || before < 0 || after < 0 {
			bad()
		}
		if !lok || l.JournalID != journalID || l.AccountID != e.AccountID || l.SignedDelta != e.SignedDelta || !finishPointer(o.RootID, o.StepNo, o.SourceID, o.CreatedXID, "ledger_line", l.PPRootID, l.PPRootStep, l.PPSourceKind, l.PPSourceID, l.PPCreatedXID, l.PPSourceRole) {
			bad()
		}
		if _, used := r.usedLines[e.JournalLineID]; used {
			bad()
		}
		r.usedLines[e.JournalLineID] = struct{}{}
		seenLines[e.JournalLineID] = struct{}{}
	}
	for kind := range want {
		if !r.tick() {
			return
		}
		if _, ok := seenKinds[kind]; !ok {
			tenant := &o.TenantID
			if kind == "REVENUE" {
				tenant = nil
			}
			if a, found := r.accountKinds[finishAccountKey(tenant, kind)]; found {
				r.mark(2, "effect", o.ID, a.ID)
			} else {
				r.mark(2, "missing_kind", o.ID, kind)
			}
		}
	}
	for _, l := range r.linesFor[journalID] {
		if !r.tick() {
			return
		}
		if _, ok := seenLines[l.ID]; !ok {
			if _, required := expectedAccounts[l.AccountID]; required {
				r.mark(2, "effect", o.ID, l.AccountID)
			} else {
				r.mark(2, "line", l.ID)
			}
		}
		r.usedLines[l.ID] = struct{}{}
	}
}

func (r *finishReplay) periodic(o CustomerFinishOutcomeRow, q RequestRecordRow, R, charge *big.Int) {
	controls := map[string]*string{"tenant": o.CustomerTenantControlID, "key": o.CustomerKeyControlID}
	markers := map[string]*string{"tenant": q.PBTenantControl, "key": q.PBKeyControl}
	seen, seenEvents := make(keySet), make(keySet)
	for _, e := range r.pbFor[o.ID] {
		if !r.tick() {
			return
		}
		bad := func() { r.mark(3, "effect", o.ID, e.Scope) }
		control, scopeOK := controls[e.Scope]
		_, duplicate := seen[e.Scope]
		seen[e.Scope] = struct{}{}
		c, cok := r.controls[e.ControlID]
		a, aok := r.allocations[e.AllocationID]
		v, vok := r.events[e.EventID]
		if !scopeOK || duplicate || !requestPointer(control, e.ControlID) || !cok || c.Scope != e.Scope || !requestPointer(c.TenantID, o.TenantID) || e.Scope == "key" && !requestPointer(c.KeyID, q.KeyID) || e.Scope == "tenant" && c.KeyID != nil || c.AccountID != nil || !aok || a.ControlID != e.ControlID || a.SubjectType != "customer_request" || a.SubjectID != o.RequestID || a.UpperAmount != o.ReservationAmount || a.Baseline != "0" || !vok || v.ControlID != e.ControlID || v.AllocationID != e.AllocationID || v.SourceKind != "settlement" || !requestPointer(o.SettlementID, v.SourceID) || e.OriginalSourceKind != "settlement" || !requestPointer(o.SettlementID, e.OriginalSourceID) || !finishPosition(o.RootID, o.StepNo, o.CreatedXID, e.RootID, e.StepNo, e.StepKind, e.SourceID, e.CreatedXID, o.SourceID) {
			bad()
		}
		if v.AllocationBeforeRevision != "1" || v.BeforeHeld != o.ReservationAmount || v.BeforeSpent != "0" || v.AfterHeld != "0" || v.AfterSpent != o.ChargedAmount || v.AfterSourceSpent != o.ChargedAmount || v.AfterState != o.ResultStatus || v.HeldDelta != new(big.Int).Neg(R).String() || v.SpentDelta != charge.String() || v.CreatedXID != o.CreatedXID {
			bad()
		}
		marker := markers[e.Scope]
		kind, source, xid := "reservation", o.CustomerReservationID, q.PBCreatedXID
		if marker != nil {
			window, wok := r.windows[requestOptional(a.WindowID)]
			if !requestPointer(marker, e.ControlID) || a.Origin != "new" || a.OriginSourceID != o.CustomerReservationID || !requestPointer(q.PBCreatedXID, a.CreatedXID) || !finishTimeSame(a.AdmittedAt, q.PBAdmittedAt) || !finite(a.AdmittedAt) || !wok || window.ControlID != e.ControlID || !finite(window.StartAt) || !finite(window.EndAt) || finite(a.AdmittedAt) && (a.AdmittedAt.Time.Before(window.StartAt.Time) || !a.AdmittedAt.Time.Before(window.EndAt.Time)) || v.WindowBeforeRevision == nil {
				bad()
			}
		} else {
			kind, source = "legacy_import", o.RequestID
			xid = &c.CreatedXID
			if a.Origin != "legacy_import" || a.OriginSourceID != o.RequestID || a.WindowID != nil || !nullTime(a.AdmittedAt) || a.CreatedXID != c.CreatedXID || v.WindowBeforeRevision != nil {
				bad()
			}
		}
		originals := r.eventsFor[tupleKey(kind, source)]
		originalCount := 0
		for _, first := range originals {
			if !r.tick() {
				return
			}
			if first.ControlID != e.ControlID {
				continue
			}
			originalCount++
			if first.AllocationID != a.ID || first.AllocationBeforeRevision != "0" || first.BeforeHeld != "0" || first.BeforeSpent != "0" || first.AfterHeld != o.ReservationAmount || first.AfterSpent != "0" || first.AfterSourceSpent != "0" || first.AfterState != "held" || first.HeldDelta != o.ReservationAmount || first.SpentDelta != "0" || !requestPointer(xid, first.CreatedXID) {
				bad()
			}
			if marker != nil && first.WindowBeforeRevision == nil || marker == nil && first.WindowBeforeRevision != nil {
				bad()
			}
		}
		if originalCount != 1 {
			bad()
		}
		if _, used := r.usedEvents[e.EventID]; used {
			bad()
		}
		r.usedEvents[e.EventID] = struct{}{}
		seenEvents[e.EventID] = struct{}{}
	}
	for scope, control := range controls {
		if !r.tick() {
			return
		}
		_, found := seen[scope]
		marker := markers[scope]
		// A control captured at finish may exist with no allocation because it
		// started tracking after an old closed source. Admission markers and
		// actual allocations distinguish this from a missing configured effect.
		required := false
		if marker != nil {
			required = true
			if !requestSameOptional(marker, control) {
				r.mark(3, "effect", o.ID, scope)
			}
		}
		if control != nil {
			if _, allocated := r.allocatedControls[tupleKey(o.RequestID, *control)]; allocated {
				required = true
			}
		}
		if required && !found {
			r.mark(3, "effect", o.ID, scope)
		}
		if control != nil {
			c, ok := r.controls[*control]
			if !ok || c.Scope != scope || !requestPointer(c.TenantID, o.TenantID) || scope == "key" && !requestPointer(c.KeyID, q.KeyID) {
				r.mark(3, "effect", o.ID, scope)
			}
		}
	}
	for _, v := range r.eventsFor[tupleKey("settlement", requestOptional(o.SettlementID))] {
		if !r.tick() {
			return
		}
		if _, found := seenEvents[v.ID]; !found {
			r.mark(3, "event", v.ID)
		}
	}
}

func (r *finishReplay) reverseEffects() {
	for _, c := range r.rows.OriginalRows.ReconciliationCases {
		if !r.tick() {
			return
		}
		if requestPointer(c.PPLastSourceKind, "reconciliation") {
			s, found := r.sources[requestOptional(c.PPLastSourceID)]
			if !found || s.CaseID != c.ID || s.TenantID != c.TenantID || s.RequestID != c.RequestID {
				r.mark(0, "case", c.ID)
			}
		}
	}
	for _, e := range r.rows.PrefixRows.CustomerFinishLedgerEffects {
		if !r.tick() {
			return
		}
		o, ok := r.outcomes[e.FinishOutcomeID]
		if !ok || o.ResultKind != "new_settlement" {
			r.mark(2, "effect", e.FinishOutcomeID, e.AccountID)
		}
	}
	for _, e := range r.rows.PrefixRows.CustomerFinishPBEffects {
		if !r.tick() {
			return
		}
		o, ok := r.outcomes[e.FinishOutcomeID]
		if !ok || o.ResultKind != "new_settlement" {
			r.mark(3, "effect", e.FinishOutcomeID, e.Scope)
		}
	}
	for _, line := range r.rows.OriginalRows.JournalLines {
		if !r.tick() {
			return
		}
		if requestPointer(line.PPSourceKind, "reconciliation") && requestPointer(line.PPSourceRole, "ledger_line") {
			if _, found := r.usedLines[line.ID]; !found {
				r.mark(2, "line", line.ID)
			}
		}
	}
}
