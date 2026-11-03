package procurementprefix

import "reflect"

func sourceCommon(a, b reflect.Value) bool {
	for _, field := range []string{"root_id", "step_no", "step_kind", "created_xid"} {
		if sourceText(a, field) != sourceText(b, field) {
			return false
		}
	}
	return true
}
func sourceScope(a, b reflect.Value) bool {
	for _, field := range []string{"tenant_id", "request_id", "attempt_id"} {
		av, bv := sourceField(a, field), sourceField(b, field)
		if !av.IsValid() || !bv.IsValid() {
			continue
		}
		if !sourceSameOptional(sourceOptionalPointer(a, field), sourceOptionalPointer(b, field)) {
			return false
		}
	}
	return true
}

func (s *sourceClosureState) companions() error {
	index := make(map[string]map[string][]sourceRawRow)
	for table, collection := range s.tables {
		byPK := make(map[string][]sourceRawRow)
		index[table] = byPK
		for _, raw := range collection {
			if err := s.w.tick(); err != nil {
				return err
			}
			byPK[raw.key] = append(byPK[raw.key], raw)
		}
	}
	// Child -> parent exact composite identity. Parent -> child expectations are
	// checked below, so an extra companion cannot hide behind an existing row.
	links := []struct{ child, parent, reference, sourceField string }{
		{"pp_account_origins", "pp_account_command_sources", "command_source_id", "command_source_id"},
		{"pp_meter_origins", "pp_meter_choices", "source_id", "source_id"},
		{"pp_conflict_sources", "pp_meter_choices", "meter_choice_id", "source_id"},
		{"pp_evidence_members", "pp_evidence_choices", "evidence_choice_id", "source_id"},
		{"pp_customer_finish_outcomes", "pp_reconciliation_sources", "reconciliation_source_id", "source_id"},
		{"pp_customer_finish_ledger_effects", "pp_customer_finish_outcomes", "finish_outcome_id", ""},
		{"pp_customer_finish_pb_effects", "pp_customer_finish_outcomes", "finish_outcome_id", ""},
	}
	reverse := make(map[string]map[string][]sourceRawRow)
	for _, link := range links {
		back := make(map[string][]sourceRawRow)
		reverse[link.child] = back
		for _, raw := range s.tables[link.child] {
			if err := s.w.tick(); err != nil {
				return err
			}
			r := raw.value
			key := tupleKey(sourceText(r, link.reference))
			back[key] = append(back[key], raw)
			parents := index[link.parent][key]
			valid := len(parents) == 1
			if valid {
				parent := parents[0].value
				valid = sourceCommon(r, parent) && sourceScope(r, parent)
				if link.sourceField != "" {
					valid = valid && sourceText(r, link.sourceField) == sourceText(parent, "id")
				}
				switch link.child {
				case "pp_account_origins":
					valid = valid && sourceText(parent, "kind") == "account_create" && sourceText(r, "account_id") == sourceText(parent, "target_id") && sourceText(r, "create_operation_id") == sourceText(parent, "operation_id")
				case "pp_meter_origins":
					valid = valid && sourceText(parent, "disposition") == "created" && sourceText(r, "meter_event_id") == sourceText(parent, "meter_event_id") && sourceText(r, "incoming_source_id") == sourceText(parent, "incoming_source_id")
				case "pp_conflict_sources":
					valid = valid && sourceText(parent, "collision_kind") != "none" && sourceText(r, "incoming_source_id") == sourceText(parent, "incoming_source_id")
				case "pp_evidence_members":
					valid = valid && sourceText(parent, "disposition") == "new_member" && sourceText(r, "attempt_id") == sourceText(parent, "identity_attempt_id") && sourceText(r, "identity_digest") == sourceText(parent, "identity_digest") && sourceText(r, "identity_event_id") == sourceText(parent, "identity_event_id")
				case "pp_customer_finish_outcomes":
					valid = valid && sourceText(parent, "action") != "propose" && sourceText(r, "case_id") == sourceText(parent, "case_id") && sourceSameOptional(sourceOptionalPointer(r, "id"), sourceOptional(parent, "customer_finish_outcome_id"))
				case "pp_customer_finish_ledger_effects", "pp_customer_finish_pb_effects":
					valid = valid && sourceText(parent, "result_kind") == "new_settlement" && sourceText(r, "source_id") == sourceText(parent, "source_id")
				}
			}
			if !valid {
				s.mark("companion", link.child, raw.key)
			}
		}
	}
	for _, rule := range []struct {
		parent, child, kindField, kindValue string
		needed                              bool
	}{
		{"pp_account_command_sources", "pp_account_origins", "kind", "account_create", true},
		{"pp_meter_choices", "pp_meter_origins", "disposition", "created", true},
		{"pp_meter_choices", "pp_conflict_sources", "collision_kind", "none", false},
		{"pp_evidence_choices", "pp_evidence_members", "disposition", "new_member", true},
		{"pp_reconciliation_sources", "pp_customer_finish_outcomes", "action", "propose", false},
	} {
		for _, parent := range s.tables[rule.parent] {
			if err := s.w.tick(); err != nil {
				return err
			}
			required := (sourceText(parent.value, rule.kindField) == rule.kindValue) == rule.needed
			count := len(reverse[rule.child][parent.key])
			if required && count != 1 || !required && count != 0 {
				s.mark("companion", rule.parent, parent.key)
			}
		}
	}
	// Account journals are a closed three-way companion union of actual effects.
	journalParents := make(map[string][]sourceRawRow)
	for _, raw := range s.tables["pp_account_journal"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		r := raw.value
		table, reference := "", ""
		switch sourceText(r, "source_kind") {
		case "command":
			table, reference = "pp_account_command_sources", "command_source_id"
		case "budget_event":
			table, reference = "pp_procurement_transitions", "transition_id"
		case "dispatch":
			table, reference = "pp_attempt_delivery_sources", "delivery_source_id"
		}
		id := sourceOptional(r, reference)
		valid := table != "" && id != nil
		if valid {
			key := tupleKey(table, *id)
			journalParents[key] = append(journalParents[key], raw)
			parents := index[table][tupleKey(*id)]
			valid = len(parents) == 1
			if valid {
				parent := parents[0].value
				valid = sourceCommon(r, parent) && sourceScope(r, parent)
				switch table {
				case "pp_account_command_sources":
					valid = valid && sourceText(r, "account_id") == sourceText(parent, "target_id") && sourceText(r, "source_id") == sourceText(parent, "operation_id") && sourceSameOptional(sourceOptionalPointer(r, "id"), sourceOptional(parent, "journal_id"))
				case "pp_procurement_transitions":
					valid = valid && sourceText(r, "account_id") == sourceText(parent, "account_id") && sourceText(r, "source_id") == sourceText(parent, "event_id") && sourceText(r, "id") == sourceText(parent, "journal_id")
				case "pp_attempt_delivery_sources":
					valid = valid && sourceText(parent, "mode") == "dispatch_permit" && sourceText(r, "source_id") == sourceText(parent, "attempt_id") && sourceSameOptional(sourceOptionalPointer(r, "account_id"), sourceOptional(parent, "dispatch_account_id")) && sourceSameOptional(sourceOptionalPointer(r, "id"), sourceOptional(parent, "dispatch_journal_id"))
				}
			}
		}
		if !valid {
			s.mark("companion", "pp_account_journal", raw.key)
		}
	}
	for _, rule := range []struct {
		table, field string
		always       bool
	}{{"pp_account_command_sources", "journal_id", false}, {"pp_procurement_transitions", "journal_id", true}, {"pp_attempt_delivery_sources", "dispatch_journal_id", false}} {
		for _, parent := range s.tables[rule.table] {
			if err := s.w.tick(); err != nil {
				return err
			}
			count := len(journalParents[tupleKey(rule.table, sourceText(parent.value, "id"))])
			needed := rule.always || sourcePresent(parent.value, rule.field)
			if needed && count != 1 || !needed && count != 0 {
				s.mark("companion", rule.table, parent.key)
			}
		}
	}
	return nil
}
