package procurementprefix

import (
	"math/big"
	"reflect"
)

// These relations authenticate declared outputs, not the business decision that
// chose them. Today's balances and PB configuration never stand in for history.
type sourceEffectState struct {
	s               *sourceClosureState
	index           map[string]map[string][]sourceRawRow
	ledger, pb      map[string][]sourceRawRow
	lines, events   map[string][]sourceRawRow
	allocations     map[string][]sourceRawRow
	accountKinds    map[string][]sourceRawRow
	settlementsFor  map[string][]sourceRawRow
	journalOps      map[string][]sourceRawRow
	procurementKeys map[string][]sourceRawRow
	lineUse, pbUse  map[string][]sourceEffectRef
	err             error
}

type sourceEffectRef struct{ table, key string }

func (e *sourceEffectState) tick() bool {
	if e.err == nil {
		e.err = e.s.w.tick()
	}
	return e.err == nil
}

func (e *sourceEffectState) one(table, key string) (reflect.Value, bool) {
	rows := e.index[table][tupleKey(key)]
	if len(rows) != 1 {
		return reflect.Value{}, false
	}
	return rows[0].value, true
}

func (e *sourceEffectState) bad(table, key string) { e.s.mark("pointer", table, key) }

func (e *sourceEffectState) mismatch(table string, row sourceRawRow, target, id string) {
	e.bad(table, row.key)
	for _, actual := range e.index[target][tupleKey(id)] {
		if !e.tick() {
			return
		}
		e.bad(target, actual.key)
	}
}

func (s *sourceClosureState) effectRelations() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	e := sourceEffectState{s: s, index: make(map[string]map[string][]sourceRawRow), ledger: make(map[string][]sourceRawRow), pb: make(map[string][]sourceRawRow), lines: make(map[string][]sourceRawRow), events: make(map[string][]sourceRawRow), allocations: make(map[string][]sourceRawRow), accountKinds: make(map[string][]sourceRawRow), settlementsFor: make(map[string][]sourceRawRow), journalOps: make(map[string][]sourceRawRow), procurementKeys: make(map[string][]sourceRawRow), lineUse: make(map[string][]sourceEffectRef), pbUse: make(map[string][]sourceEffectRef)}
	for table, rows := range s.tables {
		e.index[table] = make(map[string][]sourceRawRow)
		for _, raw := range rows {
			if !e.tick() {
				return e.err
			}
			e.index[table][raw.key] = append(e.index[table][raw.key], raw)
			switch table {
			case "settlements":
				id := sourceText(raw.value, "request_id")
				e.settlementsFor[id] = append(e.settlementsFor[id], raw)
			case "journals":
				id := tupleKey(sourceText(raw.value, "tenant_id"), sourceText(raw.value, "operation_id"))
				e.journalOps[id] = append(e.journalOps[id], raw)
			case "procurement_budget_events":
				id := sourceText(raw.value, "event_key")
				e.procurementKeys[id] = append(e.procurementKeys[id], raw)
			case "pp_customer_finish_ledger_effects":
				id := sourceText(raw.value, "finish_outcome_id")
				e.ledger[id] = append(e.ledger[id], raw)
			case "pp_customer_finish_pb_effects":
				id := sourceText(raw.value, "finish_outcome_id")
				e.pb[id] = append(e.pb[id], raw)
			case "journal_lines":
				id := sourceText(raw.value, "journal_id")
				e.lines[id] = append(e.lines[id], raw)
			case "periodic_budget_events":
				id := tupleKey(sourceText(raw.value, "source_kind"), sourceText(raw.value, "source_id"))
				e.events[id] = append(e.events[id], raw)
			case "periodic_budget_allocations":
				id := tupleKey(sourceText(raw.value, "control_id"), sourceText(raw.value, "subject_type"), sourceText(raw.value, "subject_id"))
				e.allocations[id] = append(e.allocations[id], raw)
			case "ledger_accounts":
				id := tupleKey(sourceEffectPointerKey(sourceOptional(raw.value, "tenant_id")), sourceText(raw.value, "kind"))
				e.accountKinds[id] = append(e.accountKinds[id], raw)
			}
		}
	}
	for _, raw := range s.tables["pp_customer_finish_outcomes"] {
		if !e.tick() {
			return e.err
		}
		if len(e.index["pp_customer_finish_outcomes"][raw.key]) != 1 {
			e.bad("pp_customer_finish_outcomes", raw.key)
			continue
		}
		e.customer(raw)
	}
	for _, raw := range s.tables["pp_procurement_transitions"] {
		if !e.tick() {
			return e.err
		}
		if len(e.index["pp_procurement_transitions"][raw.key]) != 1 {
			e.bad("pp_procurement_transitions", raw.key)
			continue
		}
		e.supplier(raw)
	}
	for _, table := range []string{"pp_customer_finish_ledger_effects", "pp_customer_finish_pb_effects"} {
		for _, raw := range s.tables[table] {
			if !e.tick() {
				return e.err
			}
			parent, ok := e.one("pp_customer_finish_outcomes", sourceText(raw.value, "finish_outcome_id"))
			if !ok || sourceText(parent, "result_kind") != "new_settlement" {
				s.mark("companion", table, raw.key)
			}
		}
	}
	for _, uses := range []map[string][]sourceEffectRef{e.lineUse, e.pbUse} {
		for _, refs := range uses {
			if !e.tick() {
				return e.err
			}
			if len(refs) > 1 {
				for _, ref := range refs {
					if !e.tick() {
						return e.err
					}
					e.bad(ref.table, ref.key)
				}
			}
		}
	}
	if e.err != nil {
		return e.err
	}
	return s.w.b.check()
}

func (e *sourceEffectState) customer(raw sourceRawRow) {
	o := raw.value.Interface().(CustomerFinishOutcomeRow)
	if o.ResultKind != "new_settlement" {
		return
	}
	table := "pp_customer_finish_outcomes"
	for _, slot := range []struct {
		table string
		rows  []sourceRawRow
	}{
		{"settlements", e.settlementsFor[o.RequestID]},
		{"journals", e.journalOps[tupleKey(o.TenantID, requestOptional(o.SettlementID))]},
	} {
		if len(slot.rows) != 1 {
			e.s.mark("companion", table, raw.key)
			for _, actual := range slot.rows {
				if !e.tick() {
					return
				}
				e.bad(slot.table, actual.key)
			}
		}
	}
	settlement, found := e.one("settlements", requestOptional(o.SettlementID))
	if !found || sourceText(settlement, "tenant_id") != o.TenantID || sourceText(settlement, "request_id") != o.RequestID || !sourceSameOptional(sourceOptional(settlement, "finalization_id"), o.LatestFinalizationID) || sourceText(settlement, "charged_amount") != o.ChargedAmount || sourceText(settlement, "raw_retail_amount") != o.RawAmount || sourceText(settlement, "waived_retail_amount") != o.WaivedAmount || sourceText(settlement, "status") != o.ResultStatus || sourceText(settlement, "reason") != o.SelectedReason || !requestPointer(o.CustomerJournalID, sourceText(settlement, "journal_id")) {
		e.mismatch(table, raw, "settlements", requestOptional(o.SettlementID))
	}
	journal, found := e.one("journals", requestOptional(o.CustomerJournalID))
	if !found || sourceText(journal, "tenant_id") != o.TenantID || !requestPointer(o.SettlementID, sourceText(journal, "operation_id")) || sourceText(journal, "kind") != o.ResultStatus || sourceText(journal, "reference") != o.RequestID || sourceText(journal, "actor") != "system" || sourceText(journal, "currency") != "CNY" || sourceText(journal, "created_txid") != o.CreatedXID {
		e.mismatch(table, raw, "journals", requestOptional(o.CustomerJournalID))
	}
	R, rok := numeric(o.ReservationAmount, false)
	charge, cok := numeric(o.ChargedAmount, false)
	if !rok || !cok || charge.Cmp(R) > 0 {
		e.bad(table, raw.key)
		return
	}
	e.customerLedger(raw, o, R, charge)
	e.customerPB(raw, o, R, charge)
}

func (e *sourceEffectState) customerLedger(parent sourceRawRow, o CustomerFinishOutcomeRow, R, charge *big.Int) {
	want := map[string]*big.Int{"HELD": new(big.Int).Neg(R), "AVAILABLE": new(big.Int).Sub(R, charge)}
	if charge.Sign() > 0 {
		want["REVENUE"] = new(big.Int).Set(charge)
	}
	seenKinds, seenLines := make(map[string]int), make(map[string]int)
	for _, raw := range e.ledger[o.ID] {
		if !e.tick() {
			return
		}
		row := raw.value.Interface().(CustomerFinishLedgerEffectRow)
		table := "pp_customer_finish_ledger_effects"
		seenKinds[row.AccountKind]++
		seenLines[row.JournalLineID]++
		e.lineUse[row.JournalLineID] = append(e.lineUse[row.JournalLineID], sourceEffectRef{table, raw.key})
		amount, known := want[row.AccountKind]
		tenant := &o.TenantID
		if row.AccountKind == "REVENUE" {
			tenant = nil
		}
		accounts := e.accountKinds[tupleKey(sourceEffectPointerKey(tenant), row.AccountKind)]
		account, found := e.one("ledger_accounts", row.AccountID)
		if !known || row.SignedDelta != amount.String() || !sourceCommon(raw.value, parent.value) || row.SourceID != o.SourceID || row.TenantID != o.TenantID || !sourceSameOptional(row.AccountTenantID, tenant) || len(accounts) != 1 || !found || sourceText(account, "kind") != row.AccountKind || !sourceSameOptional(sourceOptional(account, "tenant_id"), tenant) {
			e.mismatch(table, raw, "ledger_accounts", row.AccountID)
		}
		line, found := e.one("journal_lines", row.JournalLineID)
		if !found || !requestPointer(o.CustomerJournalID, sourceText(line, "journal_id")) || sourceText(line, "account_id") != row.AccountID || sourceText(line, "signed_delta") != row.SignedDelta {
			e.mismatch(table, raw, "journal_lines", row.JournalLineID)
		}
	}
	for kind := range want {
		if !e.tick() {
			return
		}
		if seenKinds[kind] != 1 {
			e.s.mark("companion", "pp_customer_finish_outcomes", parent.key)
		}
	}
	for _, raw := range e.ledger[o.ID] {
		if !e.tick() {
			return
		}
		if seenKinds[sourceText(raw.value, "account_kind")] != 1 {
			e.s.mark("companion", "pp_customer_finish_ledger_effects", raw.key)
		}
	}
	for _, raw := range e.lines[requestOptional(o.CustomerJournalID)] {
		if !e.tick() {
			return
		}
		if seenLines[sourceText(raw.value, "id")] != 1 {
			e.s.mark("companion", "pp_customer_finish_outcomes", parent.key)
			e.bad("journal_lines", raw.key)
		}
	}
}

func (e *sourceEffectState) customerPB(parent sourceRawRow, o CustomerFinishOutcomeRow, R, charge *big.Int) {
	qv, qok := e.one("request_records", o.RequestID)
	if !qok {
		e.mismatch("pp_customer_finish_outcomes", parent, "request_records", o.RequestID)
	}
	controls := map[string]*string{"tenant": o.CustomerTenantControlID, "key": o.CustomerKeyControlID}
	markers := map[string]*string{"tenant": sourceOptional(qv, "pb_tenant_control"), "key": sourceOptional(qv, "pb_key_control")}
	seenScopes, seenEvents := make(map[string]int), make(map[string]int)
	for _, raw := range e.pb[o.ID] {
		if !e.tick() {
			return
		}
		r := raw.value.Interface().(CustomerFinishPBEffectRow)
		seenScopes[r.Scope]++
		seenEvents[r.EventID]++
		e.pbUse[r.EventID] = append(e.pbUse[r.EventID], sourceEffectRef{"pp_customer_finish_pb_effects", raw.key})
		c, cok := e.one("periodic_budget_controls", r.ControlID)
		a, aok := e.one("periodic_budget_allocations", r.AllocationID)
		v, vok := e.one("periodic_budget_events", r.EventID)
		control, scopeOK := controls[r.Scope]
		valid := scopeOK && requestPointer(control, r.ControlID) && sourceCommon(raw.value, parent.value) && r.SourceID == o.SourceID && r.OriginalSourceKind == "settlement" && requestPointer(o.SettlementID, r.OriginalSourceID)
		if !cok || sourceText(c, "scope") != r.Scope || !requestPointer(sourceOptional(c, "tenant_id"), o.TenantID) || sourceOptional(c, "account_id") != nil || r.Scope == "key" && !sourceSameOptional(sourceOptional(c, "key_id"), sourceOptionalPointer(qv, "key_id")) || r.Scope == "tenant" && sourceOptional(c, "key_id") != nil {
			e.mismatch("pp_customer_finish_pb_effects", raw, "periodic_budget_controls", r.ControlID)
		}
		if !aok || sourceText(a, "control_id") != r.ControlID || sourceText(a, "subject_type") != "customer_request" || sourceText(a, "subject_id") != o.RequestID || sourceText(a, "upper_amount") != o.ReservationAmount || sourceText(a, "baseline") != "0" || len(e.allocations[tupleKey(r.ControlID, "customer_request", o.RequestID)]) != 1 {
			e.mismatch("pp_customer_finish_pb_effects", raw, "periodic_budget_allocations", r.AllocationID)
		}
		if !vok || sourceText(v, "control_id") != r.ControlID || sourceText(v, "allocation_id") != r.AllocationID || sourceText(v, "source_kind") != "settlement" || !requestPointer(o.SettlementID, sourceText(v, "source_id")) || sourceText(v, "allocation_before_revision") != "1" || sourceText(v, "before_held") != o.ReservationAmount || sourceText(v, "before_spent") != "0" || sourceText(v, "after_held") != "0" || sourceText(v, "after_spent") != o.ChargedAmount || sourceText(v, "after_source_spent") != o.ChargedAmount || sourceText(v, "after_state") != o.ResultStatus || sourceText(v, "held_delta") != new(big.Int).Neg(R).String() || sourceText(v, "spent_delta") != charge.String() || sourceText(v, "created_xid") != o.CreatedXID {
			e.mismatch("pp_customer_finish_pb_effects", raw, "periodic_budget_events", r.EventID)
		}
		marker := markers[r.Scope]
		firstKind, firstID, firstXID := "legacy_import", o.RequestID, sourceText(c, "created_xid")
		if marker != nil {
			firstKind, firstID = "reservation", o.CustomerReservationID
			firstXID = requestOptional(sourceOptional(qv, "pb_created_xid"))
			window, wok := e.one("periodic_budget_windows", requestOptional(sourceOptional(a, "window_id")))
			at, av := sourceField(a, "admitted_at"), sourceField(qv, "pb_admitted_at")
			valid = valid && requestPointer(marker, r.ControlID) && sourceText(a, "origin") == "new" && sourceText(a, "origin_source_id") == o.CustomerReservationID && sourceText(a, "created_xid") == firstXID && sourceFrozenEqual(at, av) && wok && sourceText(window, "control_id") == r.ControlID && sourceEffectWindow(at, window) && sourceOptional(v, "window_before_revision") != nil
		} else {
			valid = valid && sourceText(a, "origin") == "legacy_import" && sourceText(a, "origin_source_id") == o.RequestID && sourceOptional(a, "window_id") == nil && sourceNullTime(a, "admitted_at") && sourceText(a, "created_xid") == firstXID && sourceOptional(v, "window_before_revision") == nil
		}
		firstCount := 0
		for _, first := range e.events[tupleKey(firstKind, firstID)] {
			if !e.tick() {
				return
			}
			f := first.value
			if sourceText(f, "control_id") != r.ControlID {
				continue
			}
			if len(e.index["periodic_budget_events"][first.key]) != 1 {
				e.bad("periodic_budget_events", first.key)
				e.bad("pp_customer_finish_pb_effects", raw.key)
				continue
			}
			firstCount++
			if sourceText(f, "allocation_id") != r.AllocationID || sourceText(f, "allocation_before_revision") != "0" || sourceText(f, "before_held") != "0" || sourceText(f, "before_spent") != "0" || sourceText(f, "after_held") != o.ReservationAmount || sourceText(f, "after_spent") != "0" || sourceText(f, "after_source_spent") != "0" || sourceText(f, "after_state") != "held" || sourceText(f, "held_delta") != o.ReservationAmount || sourceText(f, "spent_delta") != "0" || sourceText(f, "created_xid") != firstXID || (sourceOptional(f, "window_before_revision") != nil) != (marker != nil) {
				valid = false
				e.bad("periodic_budget_events", first.key)
			}
		}
		if !valid || firstCount != 1 {
			e.bad("pp_customer_finish_pb_effects", raw.key)
		}
	}
	for scope, control := range controls {
		if !e.tick() {
			return
		}
		required := markers[scope] != nil || control != nil && len(e.allocations[tupleKey(*control, "customer_request", o.RequestID)]) > 0
		if required && seenScopes[scope] != 1 || !required && seenScopes[scope] > 1 {
			e.s.mark("companion", "pp_customer_finish_outcomes", parent.key)
		}
		if control != nil {
			c, ok := e.one("periodic_budget_controls", *control)
			if !ok || sourceText(c, "scope") != scope || !requestPointer(sourceOptional(c, "tenant_id"), o.TenantID) || scope == "key" && !sourceSameOptional(sourceOptional(c, "key_id"), sourceOptionalPointer(qv, "key_id")) || scope == "tenant" && sourceOptional(c, "key_id") != nil || sourceOptional(c, "account_id") != nil {
				e.mismatch("pp_customer_finish_outcomes", parent, "periodic_budget_controls", *control)
			}
		}
	}
	for _, raw := range e.events[tupleKey("settlement", requestOptional(o.SettlementID))] {
		if !e.tick() {
			return
		}
		if seenEvents[sourceText(raw.value, "id")] != 1 {
			e.s.mark("companion", "pp_customer_finish_outcomes", parent.key)
			e.bad("periodic_budget_events", raw.key)
		}
	}
}

func sourceNullTime(r reflect.Value, field string) bool {
	v := sourceField(r, field)
	return v.IsValid() && v.Type() == timeType && nullTime(v.Interface().(Time))
}

func sourceEffectWindow(at reflect.Value, window reflect.Value) bool {
	start, end := sourceField(window, "start_at"), sourceField(window, "end_at")
	if !at.IsValid() || !start.IsValid() || !end.IsValid() || at.Type() != timeType || start.Type() != timeType || end.Type() != timeType {
		return false
	}
	a, b, c := at.Interface().(Time), start.Interface().(Time), end.Interface().(Time)
	return finite(a) && finite(b) && finite(c) && !a.Time.Before(b.Time) && a.Time.Before(c.Time)
}

func sourceEffectSpent(spent, baseline string) (string, bool) {
	a, ok := numeric(spent, false)
	b, valid := numeric(baseline, false)
	if !ok || !valid {
		return "", false
	}
	a.Sub(a, b)
	if a.Sign() < 0 {
		a.SetInt64(0)
	}
	return a.String(), true
}

func (e *sourceEffectState) supplier(raw sourceRawRow) {
	t := raw.value.Interface().(ProcurementTransitionRow)
	if originals := e.procurementKeys[t.EventKey]; len(originals) != 1 {
		e.s.mark("companion", "pp_procurement_transitions", raw.key)
		for _, original := range originals {
			if !e.tick() {
				return
			}
			e.bad("procurement_budget_events", original.key)
		}
	}
	pe, found := e.one("procurement_budget_events", t.EventID)
	if !found || sourceText(pe, "event_key") != t.EventKey || sourceText(pe, "account_id") != t.AccountID || sourceText(pe, "attempt_id") != t.AttemptID || sourceText(pe, "type") != t.EventType || sourceText(pe, "evidence_ref") != t.EvidenceRef || sourceText(pe, "held_delta") != t.HeldDelta || sourceText(pe, "spent_delta") != t.SpentDelta || !requestPointer(sourceOptional(pe, "pb_created_xid"), t.CreatedXID) {
		e.mismatch("pp_procurement_transitions", raw, "procurement_budget_events", t.EventID)
	}
	p, found := e.one("procurement_reservations", t.AttemptID)
	if !found || sourceText(p, "account_id") != t.AccountID || sourceText(p, "binding_id") != t.BindingID {
		e.mismatch("pp_procurement_transitions", raw, "procurement_reservations", t.AttemptID)
	}
	marker := sourceOptional(p, "pb_control")
	events := e.events[tupleKey("procurement_event", t.EventID)]
	if len(events) > 1 || marker != nil && len(events) != 1 {
		e.s.mark("companion", "pp_procurement_transitions", raw.key)
	}
	for _, event := range events {
		if !e.tick() {
			return
		}
		if len(e.index["periodic_budget_events"][event.key]) != 1 {
			e.bad("pp_procurement_transitions", raw.key)
			e.bad("periodic_budget_events", event.key)
			continue
		}
		v := event.value
		id, controlID, allocationID := sourceText(v, "id"), sourceText(v, "control_id"), sourceText(v, "allocation_id")
		e.pbUse[id] = append(e.pbUse[id], sourceEffectRef{"pp_procurement_transitions", raw.key})
		c, cok := e.one("periodic_budget_controls", controlID)
		a, aok := e.one("periodic_budget_allocations", allocationID)
		valid := cok && sourceText(c, "scope") == "procurement" && requestPointer(sourceOptional(c, "account_id"), t.AccountID) && sourceOptional(c, "tenant_id") == nil && sourceOptional(c, "key_id") == nil && aok && sourceText(a, "control_id") == controlID && sourceText(a, "subject_type") == "supplier_attempt" && sourceText(a, "subject_id") == t.AttemptID && sourceText(a, "upper_amount") == t.UpperAmount && len(e.allocations[tupleKey(controlID, "supplier_attempt", t.AttemptID)]) == 1
		origin, originID := sourceText(a, "origin"), sourceText(a, "origin_source_id")
		if marker != nil {
			window, wok := e.one("periodic_budget_windows", requestOptional(sourceOptional(a, "window_id")))
			valid = valid && *marker == controlID && origin == "new" && sourceText(a, "baseline") == "0" && sourceFrozenEqual(sourceField(a, "admitted_at"), sourceField(p, "pb_admitted_at")) && sourceSameOptional(sourceOptionalPointer(a, "created_xid"), sourceOptional(p, "pb_created_xid")) && wok && sourceText(window, "control_id") == controlID && sourceEffectWindow(sourceField(a, "admitted_at"), window) && sourceOptional(v, "window_before_revision") != nil
		} else {
			valid = valid && sourceOneOf(origin, "legacy_import", "legacy_catchup") && sourceOptional(a, "window_id") == nil && sourceNullTime(a, "admitted_at") && sourceOptional(v, "window_before_revision") == nil
		}
		if origin == "legacy_import" {
			valid = valid && originID == t.AttemptID && sourceText(a, "created_xid") == sourceText(c, "created_xid")
		} else {
			first, ok := e.one("procurement_budget_events", originID)
			valid = valid && ok && sourceText(first, "attempt_id") == t.AttemptID && sourceText(first, "account_id") == t.AccountID
			if origin == "new" {
				valid = valid && sourceText(first, "type") == "reserved" && sourceText(first, "event_key") == "reserve:"+t.AttemptID && sourceText(first, "held_delta") == t.UpperAmount && sourceText(first, "spent_delta") == "0"
			}
			if origin == "legacy_catchup" && originID == t.EventID {
				valid = valid && requestPointer(t.BeforeHeld, "0") && requestPointer(t.BeforeSpent, sourceText(a, "baseline"))
			}
		}
		beforeHeld, beforeSpent := "0", "0"
		if t.BeforeRevision != nil {
			beforeHeld, beforeSpent = requestOptional(t.BeforeHeld), requestOptional(t.BeforeSpent)
		}
		before, bok := sourceEffectSpent(beforeSpent, sourceText(a, "baseline"))
		after, aok := sourceEffectSpent(t.AfterSpent, sourceText(a, "baseline"))
		dh, dok := sourceEffectDelta(t.AfterHeld, beforeHeld)
		ds, sok := sourceEffectDelta(after, before)
		state := "held"
		if sourceOneOf(t.AfterState, "disputed", "settled", "released") {
			state = t.AfterState
		}
		valid = valid && bok && aok && dok && sok && sourceText(v, "before_held") == beforeHeld && sourceText(v, "before_spent") == before && sourceText(v, "after_held") == t.AfterHeld && sourceText(v, "after_spent") == after && sourceText(v, "after_source_spent") == t.AfterSpent && sourceText(v, "after_state") == state && sourceText(v, "held_delta") == dh && sourceText(v, "spent_delta") == ds && sourceText(v, "created_xid") == t.CreatedXID
		if !valid {
			e.bad("pp_procurement_transitions", raw.key)
			e.bad("periodic_budget_events", event.key)
		}
	}
}

func sourceEffectDelta(after, before string) (string, bool) {
	a, ok := numeric(after, false)
	b, valid := numeric(before, false)
	if !ok || !valid {
		return "", false
	}
	return a.Sub(a, b).String(), true
}

func sourceEffectPointerKey(value *string) string {
	if value == nil {
		return tupleKey("null")
	}
	return tupleKey("text", *value)
}
