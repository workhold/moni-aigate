package procurementprefix

func (s *sourceClosureState) pointerIndex(table, field string) (map[string][]sourceRawRow, error) {
	index := make(map[string][]sourceRawRow)
	for _, raw := range s.tables[table] {
		if err := s.w.tick(); err != nil {
			return nil, err
		}
		key := tupleKey(sourceText(raw.value, field))
		index[key] = append(index[key], raw)
	}
	return index, nil
}

// Current pointers are compared with explicit complete chains. A missing
// ordered chain that has physical members is an error, never an empty history.
func (s *sourceClosureState) currentPointers(requests, attempts, accounts map[string][]sourceRawRow) error {
	if err := s.currentRequestPointers(requests); err != nil {
		return err
	}
	if err := s.currentAttemptPointers(attempts); err != nil {
		return err
	}
	return s.currentReservationPointers(accounts)
}

func (s *sourceClosureState) currentRequestPointers(chains map[string][]sourceRawRow) error {
	origins, err := s.pointerIndex("pp_request_origins", "request_id")
	if err != nil {
		return err
	}
	decisions, err := s.pointerIndex("pp_request_decisions", "request_id")
	if err != nil {
		return err
	}
	for _, raw := range s.tables["request_records"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		r := raw.value.Interface().(RequestRecordRow)
		key := tupleKey(r.ID)
		os := origins[key]
		if len(os) == 0 && len(decisions[key]) == 0 && r.PPOriginXID == nil && r.PPLastDecisionID == nil {
			continue // Real legacy boundary; it does not qualify any object.
		}
		seq, ordered := chains[key]
		valid := len(os) == 1 && (ordered || len(decisions[key]) == 0)
		if valid {
			latest := os[0].value.Interface().(RequestOriginRow).InitialLatestID
			var last *string
			if len(seq) > 0 {
				d := seq[len(seq)-1].value.Interface().(RequestDecisionRow)
				last, latest = &d.ID, d.AfterLatestID
			}
			valid = sourceSameOptional(r.PPLastDecisionID, last) && sourceSameOptional(r.LatestFinalizationID, latest)
		}
		if !valid {
			s.mark("pointer", "request_records", raw.key)
		}
	}
	return nil
}

func sourceNodeMatches(n AttemptNodeRow, raw sourceRawRow) bool {
	r := raw.value
	return n.SourceID == sourceText(r, "id") && n.AttemptID == sourceText(r, "attempt_id") &&
		n.RootID == sourceText(r, "root_id") && n.StepNo == sourceText(r, "step_no") &&
		n.StepKind == sourceText(r, "step_kind") && n.CreatedXID == sourceText(r, "created_xid")
}

func (s *sourceClosureState) currentAttemptPointers(chains map[string][]sourceRawRow) error {
	indexes := make(map[string]map[string][]sourceRawRow)
	for _, table := range []string{"pp_attempt_origins", "pp_attempt_nodes", "pp_attempt_delivery_sources", "pp_cost_mutations"} {
		field := "attempt_id"
		index, err := s.pointerIndex(table, field)
		if err != nil {
			return err
		}
		indexes[table] = index
	}
	byID := make(map[string]map[string][]sourceRawRow)
	for _, table := range []string{"pp_attempt_delivery_sources", "pp_cost_mutations", "pp_account_journal"} {
		index, err := s.pointerIndex(table, "id")
		if err != nil {
			return err
		}
		byID[table] = index
	}
	originals, err := s.pointerIndex("upstream_attempts", "id")
	if err != nil {
		return err
	}
	nodesFor := make(map[string][]sourceRawRow)
	for _, raw := range s.tables["pp_attempt_nodes"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		n := raw.value.Interface().(AttemptNodeRow)
		key := tupleKey(n.AttemptID, n.StepKind, n.SourceID)
		nodesFor[key] = append(nodesFor[key], raw)
	}
	for _, table := range []string{"pp_attempt_delivery_sources", "pp_cost_mutations"} {
		for _, raw := range s.tables[table] {
			if err := s.w.tick(); err != nil {
				return err
			}
			key := tupleKey(sourceText(raw.value, "attempt_id"))
			ns := nodesFor[tupleKey(sourceText(raw.value, "attempt_id"), sourceText(raw.value, "step_kind"), sourceText(raw.value, "id"))]
			if len(originals[key]) != 1 || len(indexes["pp_attempt_origins"][key]) != 1 || len(ns) != 1 || !sourceNodeMatches(ns[0].value.Interface().(AttemptNodeRow), raw) {
				s.mark("pointer", table, raw.key)
			}
		}
	}
	for _, raw := range s.tables["upstream_attempts"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		r := raw.value.Interface().(UpstreamAttemptRow)
		key := tupleKey(r.ID)
		os := indexes["pp_attempt_origins"][key]
		if len(os) == 0 && len(indexes["pp_attempt_nodes"][key]) == 0 && len(indexes["pp_attempt_delivery_sources"][key]) == 0 && len(indexes["pp_cost_mutations"][key]) == 0 && r.PPOriginXID == nil && r.PPLastCostMutationID == nil && r.PPLastDeliverySourceID == nil && r.PPDispatchSourceID == nil {
			continue
		}
		seq, ordered := chains[key]
		valid := len(os) == 1 && (ordered || len(indexes["pp_attempt_nodes"][key]) == 0)
		if !valid {
			s.mark("pointer", "upstream_attempts", raw.key)
			continue
		}
		origin := os[0].value.Interface().(AttemptOriginRow)
		status, amount, delivery := origin.InitialCostStatus, origin.InitialCostAmount, origin.InitialDeliveryState
		var lastCost, lastDelivery *string
		for _, node := range seq {
			if err := s.w.tick(); err != nil {
				return err
			}
			n := node.value.Interface().(AttemptNodeRow)
			table := ""
			switch n.StepKind {
			case "attempt_delivery":
				table = "pp_attempt_delivery_sources"
			case "cost_mutation":
				table = "pp_cost_mutations"
			default:
				continue
			}
			sources := byID[table][tupleKey(n.SourceID)]
			if len(sources) != 1 || !sourceNodeMatches(n, sources[0]) || !sourceScope(sources[0].value, os[0].value) {
				s.mark("pointer", "pp_attempt_nodes", node.key)
				valid = false
				continue
			}
			if table == "pp_cost_mutations" {
				m := sources[0].value.Interface().(CostMutationRow)
				lastCost, status, amount = &m.ID, m.AfterStatus, m.AfterAmount
			} else {
				d := sources[0].value.Interface().(AttemptDeliverySourceRow)
				lastDelivery, delivery = &d.ID, d.AfterDeliveryState
			}
		}
		if !sourceSameOptional(r.PPLastCostMutationID, lastCost) || !sourceSameOptional(r.PPLastDeliverySourceID, lastDelivery) || r.CostStatus != status || !sourceSameOptional(r.CostAmount, amount) || r.DeliveryState != delivery {
			valid = false
		}
		permits := []sourceRawRow{}
		for _, d := range indexes["pp_attempt_delivery_sources"][key] {
			if err := s.w.tick(); err != nil {
				return err
			}
			if sourceText(d.value, "mode") == "dispatch_permit" {
				permits = append(permits, d)
			}
		}
		if len(permits) == 0 {
			valid = valid && sourceAll(raw.value, false, "pp_dispatch_source_id", "pp_dispatch_root_id", "pp_dispatch_root_step", "pp_dispatch_xid", "pp_dispatch_account_id", "pp_dispatch_journal_id", "pb_dispatch_checked_at")
		} else if len(permits) != 1 {
			valid = false
			for _, permit := range permits {
				if err := s.w.tick(); err != nil {
					return err
				}
				s.mark("pointer", "pp_attempt_delivery_sources", permit.key)
			}
		} else {
			d := permits[0].value.Interface().(AttemptDeliverySourceRow)
			permitOK := sourceSameOptional(r.PPDispatchSourceID, &d.ID) && sourceSameOptional(r.PPDispatchRootID, &d.RootID) && sourceSameOptional(r.PPDispatchRootStep, &d.StepNo) && sourceSameOptional(r.PPDispatchXID, &d.CreatedXID) && sourceSameOptional(d.DispatchXID, &d.CreatedXID) && finite(d.PBDispatchCheckedAt) && finishTimeSame(r.PBDispatchCheckedAt, d.PBDispatchCheckedAt) && d.BeforeDeliveryState == "prepared" && d.AfterDeliveryState == "sending" && sourceScope(permits[0].value, os[0].value)
			if origin.ManagementMode == "managed" {
				permitOK = permitOK && origin.AccountID != nil && sourceSameOptional(d.DispatchAccountID, origin.AccountID) && sourceSameOptional(r.PPDispatchAccountID, d.DispatchAccountID) && d.DispatchJournalID != nil && sourceSameOptional(r.PPDispatchJournalID, d.DispatchJournalID)
				if d.DispatchJournalID != nil {
					js := byID["pp_account_journal"][tupleKey(*d.DispatchJournalID)]
					permitOK = permitOK && len(js) == 1
					if len(js) == 1 {
						j := js[0].value.Interface().(AccountJournalRow)
						permitOK = permitOK && j.Kind == "dispatch" && j.SourceKind == "dispatch" && j.SourceID == r.ID && sourceSameOptional(j.AttemptID, &r.ID) && sourceSameOptional(j.DeliverySourceID, &d.ID) && sourceSameOptional(&j.AccountID, origin.AccountID) && sourceCommon(js[0].value, permits[0].value)
					}
				}
			} else {
				permitOK = permitOK && origin.ManagementMode == "unmanaged" && d.DispatchAccountID == nil && d.DispatchJournalID == nil && r.PPDispatchAccountID == nil && r.PPDispatchJournalID == nil
			}
			if !permitOK {
				s.mark("pointer", "pp_attempt_delivery_sources", permits[0].key)
				valid = false
			}
		}
		if !valid {
			s.mark("pointer", "upstream_attempts", raw.key)
		}
	}
	return nil
}

func (s *sourceClosureState) currentReservationPointers(chains map[string][]sourceRawRow) error {
	origins, err := s.pointerIndex("pp_attempt_origins", "attempt_id")
	if err != nil {
		return err
	}
	reservations, err := s.pointerIndex("procurement_reservations", "attempt_id")
	if err != nil {
		return err
	}
	journalAttempts := make(keySet)
	for _, raw := range s.tables["pp_account_journal"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		j := raw.value.Interface().(AccountJournalRow)
		if j.AttemptID == nil {
			continue
		}
		key := tupleKey(*j.AttemptID)
		journalAttempts[key] = struct{}{}
		ps := reservations[key]
		if len(ps) != 1 || sourceText(ps[0].value, "account_id") != j.AccountID || !sourceAll(ps[0].value, true, "pp_created_xid", "pp_origin_journal_id", "pp_last_journal_id") || len(origins[key]) != 1 || sourceText(origins[key][0].value, "management_mode") != "managed" {
			s.mark("pointer", "pp_account_journal", raw.key)
		}
	}
	for _, raw := range s.tables["pp_attempt_origins"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		o := raw.value.Interface().(AttemptOriginRow)
		ps := reservations[tupleKey(o.AttemptID)]
		if o.ManagementMode == "managed" && len(ps) != 1 || o.ManagementMode == "unmanaged" && len(ps) != 0 {
			s.mark("pointer", "pp_attempt_origins", raw.key)
		}
	}
	// Filter the account's complete order by attempt, retaining interleaving.
	first, last := make(map[string]sourceRawRow), make(map[string]sourceRawRow)
	for _, seq := range chains {
		for _, raw := range seq {
			if err := s.w.tick(); err != nil {
				return err
			}
			j := raw.value.Interface().(AccountJournalRow)
			if j.AttemptID == nil {
				continue
			}
			key := tupleKey(j.AccountID, *j.AttemptID)
			if !first[key].value.IsValid() {
				first[key] = raw
			}
			last[key] = raw
			if len(reservations[tupleKey(*j.AttemptID)]) != 1 {
				s.mark("pointer", "pp_account_journal", raw.key)
			}
		}
	}
	for _, raw := range s.tables["procurement_reservations"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		p := raw.value.Interface().(ProcurementReservationRow)
		os := origins[tupleKey(p.AttemptID)]
		_, hasJournal := journalAttempts[tupleKey(p.AttemptID)]
		if len(os) == 0 && !hasJournal && sourceAll(raw.value, false, "pp_created_xid", "pp_origin_journal_id", "pp_last_journal_id") {
			continue
		}
		key := tupleKey(p.AccountID, p.AttemptID)
		f, l := first[key], last[key]
		valid := len(os) == 1 && f.value.IsValid() && l.value.IsValid()
		if valid {
			o := os[0].value.Interface().(AttemptOriginRow)
			j := f.value.Interface().(AccountJournalRow)
			a := l.value.Interface().(AccountJournalRow)
			valid = o.ManagementMode == "managed" && sourceSameOptional(o.AccountID, &p.AccountID) && sourceSameOptional(o.BindingID, &p.BindingID) && sourceSameOptional(o.AccountJournalID, &j.ID) && j.Kind == "reserve" && j.SourceKind == "budget_event" && j.StepKind == "procurement_transition" && j.RootID == o.RootID && j.CreatedXID == o.CreatedXID && sourceSameOptional(j.TransitionID, o.ProcurementTransitionID) && o.ProcurementTransitionID != nil && sourceSameOptional(j.ProcurementEventID, o.ReservedEventID) && o.ReservedEventID != nil && sourceSameOptional(&j.SourceID, o.ReservedEventID) && sourceSameOptional(p.PPCreatedXID, &j.CreatedXID) && sourceSameOptional(p.PPOriginJournalID, &j.ID) && sourceSameOptional(p.PPLastJournalID, &a.ID)
			valid = valid && sourceAll(f.value, false, "before_reservation_revision", "before_reservation_state", "before_reservation_held_amount", "before_reservation_spent_amount", "before_reservation_anomaly")
			for _, pair := range [][2]string{{"revision", "after_reservation_revision"}, {"state", "after_reservation_state"}, {"held_amount", "after_reservation_held_amount"}, {"spent_amount", "after_reservation_spent_amount"}, {"binding_id", "binding_id"}, {"input_bound", "input_bound"}, {"output_bound", "output_bound"}, {"upper_amount", "upper_amount"}} {
				if err := s.w.tick(); err != nil {
					return err
				}
				valid = valid && sourceSameOptional(sourceOptionalPointer(raw.value, pair[0]), sourceOptionalPointer(l.value, pair[1]))
			}
			valid = valid && a.AfterReservationAnomaly != nil && *a.AfterReservationAnomaly == p.Anomaly
		}
		if !valid {
			s.mark("pointer", "procurement_reservations", raw.key)
		}
	}
	return nil
}
