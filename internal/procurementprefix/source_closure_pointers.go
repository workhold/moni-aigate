package procurementprefix

import "reflect"

type sourcePointerSlot struct {
	table, key, prefix, role, id string
	parent                       sourceRawRow
	parentTable                  string
	mutable                      bool
}

func sourcePointerMatches(original reflect.Value, slot sourcePointerSlot) bool {
	for _, field := range []struct{ column, expected string }{
		{"root_id", sourceText(slot.parent.value, "root_id")}, {"root_step", sourceText(slot.parent.value, "step_no")},
		{"source_kind", sourceText(slot.parent.value, "step_kind")}, {"source_id", slot.id},
		{"source_role", slot.role}, {"created_xid", sourceText(slot.parent.value, "created_xid")},
	} {
		value := sourceOptional(original, slot.prefix+field.column)
		if value == nil || *value != field.expected {
			return false
		}
	}
	return true
}

// The six-column source pointer is read from each original row. Its target is
// selected by its actual output slot, never merely by a matching source ID.
func (s *sourceClosureState) originalPointers() error {
	originals := make(map[string]map[string][]sourceRawRow)
	for table, collection := range s.tables {
		byPK := make(map[string][]sourceRawRow)
		originals[table] = byPK
		for _, raw := range collection {
			if err := s.w.tick(); err != nil {
				return err
			}
			byPK[raw.key] = append(byPK[raw.key], raw)
		}
	}
	slots := make(map[string][]sourcePointerSlot)
	add := func(table, key, prefix, role, parentTable, id string, parent sourceRawRow, mutable bool) {
		slot := sourcePointerSlot{table: table, key: key, prefix: prefix, role: role, id: id, parent: parent, parentTable: parentTable, mutable: mutable}
		k := tupleKey(table, key, prefix)
		slots[k] = append(slots[k], slot)
	}
	outputRules := []struct{ source, target, column, role string }{
		{"pp_meter_origins", "metering_events", "meter_event_id", "meter_origin"},
		{"pp_conflict_sources", "metering_event_conflicts", "conflict_id", "conflict"},
		{"pp_request_decisions", "finalization_records", "finalization_id", "finalization"},
		{"pp_proposal_births", "procurement_cost_proposals", "proposal_id", "proposal_birth"},
		{"pp_approval_consumptions", "provider_cost_corrections", "provider_correction_id", "correction"},
		{"pp_unmanaged_correction_sources", "provider_cost_corrections", "provider_correction_id", "correction"},
		{"pp_account_command_sources", "audit_events", "audit_id", "account_command"},
		{"pp_proposal_births", "audit_events", "audit_id", "proposal"},
		{"pp_approval_consumptions", "audit_events", "audit_id", "approval"},
		{"pp_unmanaged_correction_sources", "audit_events", "audit_id", "unmanaged_correction"},
		{"pp_reconciliation_sources", "audit_events", "audit_id", "reconciliation"},
		{"pp_cost_outbox_links", "projection_outbox", "outbox_id", "cost"},
		{"pp_customer_finish_outcomes", "audit_events", "billing_audit_id", "settlement"},
		{"pp_customer_finish_outcomes", "audit_events", "profile_block_audit_id", "profile_block"},
		{"pp_customer_finish_outcomes", "projection_outbox", "billing_outbox_id", "settlement"},
		{"pp_customer_finish_ledger_effects", "journal_lines", "journal_line_id", "ledger_line"},
	}
	for _, rule := range outputRules {
		for _, raw := range s.tables[rule.source] {
			if err := s.w.tick(); err != nil {
				return err
			}
			r := raw.value
			id := sourceOptionalPointer(r, rule.column)
			if id == nil {
				continue
			}
			canonical := sourceText(r, "id")
			if sourceField(r, "source_id").IsValid() {
				canonical = sourceText(r, "source_id")
			}
			add(rule.target, tupleKey(*id), "pp_", rule.role, rule.source, canonical, raw, false)
		}
	}
	for _, raw := range s.tables["pp_evidence_members"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		r := raw.value
		add("procurement_evidence_identities", tupleKey(sourceText(r, "attempt_id"), sourceText(r, "identity_digest")), "pp_", "evidence_identity", "pp_evidence_members", sourceText(r, "source_id"), raw, false)
	}
	for _, raw := range s.tables["pp_approval_consumptions"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		r := raw.value
		add("procurement_cost_proposals", tupleKey(sourceText(r, "proposal_id")), "pp_approval_", "approval", "pp_approval_consumptions", sourceText(r, "id"), raw, false)
	}
	for _, raw := range s.tables["pp_reconciliation_sources"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		r := raw.value
		add("reconciliation_cases", tupleKey(sourceText(r, "case_id")), "pp_last_", "reconciliation", "pp_reconciliation_sources", sourceText(r, "id"), raw, true)
	}
	for _, raw := range s.tables["pp_customer_finish_outcomes"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		r := raw.value
		if sourceText(r, "result_kind") != "new_settlement" {
			continue
		}
		for _, output := range []struct{ table, column, role string }{{"settlements", "settlement_id", "settlement"}, {"journals", "customer_journal_id", "customer_journal"}} {
			id := sourceOptional(r, output.column)
			if id != nil {
				add(output.table, tupleKey(*id), "pp_", output.role, "pp_customer_finish_outcomes", sourceText(r, "source_id"), raw, false)
			}
		}
	}
	for _, collection := range slots {
		for _, slot := range collection {
			if err := s.w.tick(); err != nil {
				return err
			}
			rows := originals[slot.table][slot.key]
			valid := len(rows) == 1 && (slot.mutable || len(collection) == 1 && sourcePointerMatches(rows[0].value, slot))
			if !valid {
				s.mark("pointer", slot.parentTable, slot.parent.key)
				if len(rows) == 1 {
					s.mark("pointer", slot.table, rows[0].key)
				}
			}
		}
	}
	// The reverse scan includes originals with no source-side row at all, and
	// keeps a true all-NULL legacy boundary distinct from a partial pointer.
	for table, collection := range s.tables {
		for _, raw := range collection {
			for _, prefix := range []string{"pp_", "pp_approval_", "pp_last_"} {
				if err := s.w.tick(); err != nil {
					return err
				}
				if !sourceField(raw.value, prefix+"source_role").IsValid() {
					continue
				}
				names := []string{prefix + "root_id", prefix + "root_step", prefix + "source_kind", prefix + "source_id", prefix + "source_role", prefix + "created_xid"}
				if sourceAll(raw.value, false, names...) {
					continue
				}
				candidates := slots[tupleKey(table, raw.key, prefix)]
				matches := 0
				for _, slot := range candidates {
					if err := s.w.tick(); err != nil {
						return err
					}
					if sourcePointerMatches(raw.value, slot) {
						matches++
					}
				}
				if !sourceAll(raw.value, true, names...) || matches != 1 {
					s.mark("pointer", table, raw.key)
				}
			}
		}
	}
	if err := s.birthPointers(originals); err != nil {
		return err
	}
	return s.accountPointers(originals)
}

func (s *sourceClosureState) birthPointers(index map[string]map[string][]sourceRawRow) error {
	for _, rule := range []struct{ source, target, id string }{{"pp_request_origins", "request_records", "request_id"}, {"pp_attempt_origins", "upstream_attempts", "attempt_id"}} {
		byOriginal := make(map[string][]sourceRawRow)
		for _, raw := range s.tables[rule.source] {
			if err := s.w.tick(); err != nil {
				return err
			}
			k := tupleKey(sourceText(raw.value, rule.id))
			byOriginal[k] = append(byOriginal[k], raw)
			if len(index[rule.target][k]) != 1 {
				s.mark("pointer", rule.source, raw.key)
			}
		}
		for _, original := range s.tables[rule.target] {
			if err := s.w.tick(); err != nil {
				return err
			}
			r := original.value
			group := []string{"pp_origin_root_id", "pp_origin_root_step", "pp_origin_xid"}
			origins := byOriginal[original.key]
			if sourceAll(r, false, group...) && len(origins) == 0 {
				continue
			}
			valid := sourceAll(r, true, group...) && len(origins) == 1
			if valid {
				source := origins[0].value
				for _, pair := range []struct{ from, to string }{{"pp_origin_root_id", "root_id"}, {"pp_origin_root_step", "step_no"}, {"pp_origin_xid", "created_xid"}} {
					v := sourceOptional(r, pair.from)
					if v == nil || *v != sourceText(source, pair.to) {
						valid = false
					}
				}
			}
			if !valid {
				s.mark("pointer", rule.target, original.key)
			}
		}
	}
	return nil
}

func (s *sourceClosureState) accountPointers(index map[string]map[string][]sourceRawRow) error {
	// These original tables intentionally retain their frozen account-domain
	// pointer columns instead of being converted to the common six-column group.
	rules := []struct {
		source, target, sourceKey string
		pairs                     [][2]string
	}{
		{"pp_account_origins", "procurement_accounts", "account_id", [][2]string{{"pp_version", "version"}, {"pp_created_xid", "created_xid"}, {"pp_origin_journal_id", "journal_id"}}},
		{"pp_account_command_sources", "procurement_commands", "operation_id", [][2]string{{"pp_source_id", "id"}, {"pp_root_id", "root_id"}, {"pp_root_step", "step_no"}, {"pp_created_xid", "created_xid"}, {"pp_account_journal_id", "journal_id"}}},
		{"pp_procurement_transitions", "procurement_budget_events", "event_id", [][2]string{{"pp_root_id", "root_id"}, {"pp_root_step", "step_no"}, {"pp_transition_id", "id"}, {"pp_journal_id", "journal_id"}, {"pp_created_xid", "created_xid"}}},
	}
	for _, rule := range rules {
		expected := make(map[string][]sourceRawRow)
		matches := func(original, source reflect.Value) bool {
			for _, pair := range rule.pairs {
				if !sourceSameOptional(sourceOptional(original, pair[0]), sourceOptionalPointer(source, pair[1])) {
					return false
				}
			}
			return true
		}
		for _, raw := range s.tables[rule.source] {
			if err := s.w.tick(); err != nil {
				return err
			}
			k := tupleKey(sourceText(raw.value, rule.sourceKey))
			expected[k] = append(expected[k], raw)
			original := index[rule.target][k]
			if len(original) != 1 || !matches(original[0].value, raw.value) {
				s.mark("pointer", rule.source, raw.key)
			}
		}
		for _, raw := range s.tables[rule.target] {
			if err := s.w.tick(); err != nil {
				return err
			}
			allNull := true
			for _, pair := range rule.pairs {
				if sourcePresent(raw.value, pair[0]) {
					allNull = false
				}
			}
			sources := expected[raw.key]
			if allNull && len(sources) == 0 {
				continue
			}
			if len(sources) != 1 || !matches(raw.value, sources[0].value) {
				s.mark("pointer", rule.target, raw.key)
			}
		}
	}
	return nil
}
