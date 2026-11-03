package procurementprefix

import (
	"reflect"
	"strconv"
)

type sourceCanonicalTable struct{ kind, id string }

var sourceCanonicalTables = map[string]sourceCanonicalTable{
	"pp_request_origins": {"request_birth", "request_id"}, "pp_attempt_origins": {"attempt_birth", "attempt_id"},
	"pp_account_command_sources": {"account_command", "id"}, "pp_attempt_delivery_sources": {"attempt_delivery", "id"},
	"pp_incoming_sources": {"incoming", "id"}, "pp_meter_choices": {"meter_choice", "id"},
	"pp_request_decisions": {"request_decision", "id"}, "pp_normal_cost_decisions": {"normal_cost_decision", "id"},
	"pp_cost_mutations": {"cost_mutation", "id"}, "pp_cost_reset_decisions": {"cost_reset_decision", "id"},
	"pp_evidence_choices": {"evidence_choice", "id"}, "pp_procurement_transitions": {"procurement_transition", "id"},
	"pp_proposal_births": {"proposal_birth", "id"}, "pp_approval_consumptions": {"approval_consumed", "id"},
	"pp_unmanaged_correction_sources": {"unmanaged_correction", "id"}, "pp_recovery_sources": {"recovery", "id"},
	"pp_cost_outbox_links": {"cost_outbox", "id"}, "pp_reconciliation_sources": {"reconciliation", "id"},
}

type sourceCanonicalRow struct {
	table string
	raw   sourceRawRow
	kind  string
	id    string
}

func (s *sourceClosureState) canonical(rows PrefixRows) error {
	roots := make(map[string][]OperationRootRow)
	steps := make(map[string][]RootStepRow)
	byRoot := make(map[string][]RootStepRow)
	children := make(map[string][]sourceCanonicalRow)
	for _, root := range rows.OperationRoots {
		if err := s.w.tick(); err != nil {
			return err
		}
		key := tupleKey(root.ID)
		roots[key] = append(roots[key], root)
	}
	for table, descriptor := range sourceCanonicalTables {
		for _, raw := range s.tables[table] {
			if err := s.w.tick(); err != nil {
				return err
			}
			id := sourceText(raw.value, descriptor.id)
			key := tupleKey(descriptor.kind, id)
			children[key] = append(children[key], sourceCanonicalRow{table, raw, descriptor.kind, id})
		}
	}
	for _, step := range rows.RootSteps {
		if err := s.w.tick(); err != nil {
			return err
		}
		key := tupleKey(step.Kind, step.SourceID)
		steps[key] = append(steps[key], step)
		byRoot[tupleKey(step.RootID)] = append(byRoot[tupleKey(step.RootID)], step)
		child := children[key]
		root := roots[tupleKey(step.RootID)]
		if len(child) != 1 || len(root) != 1 || len(child) == 1 && !sourceMatchesStep(child[0].raw.value, step) || len(root) == 1 && root[0].CreatedXID != step.CreatedXID {
			s.mark("canonical", "pp_root_steps", tupleKey(step.RootID, step.StepNo))
		}
	}
	for _, collection := range children {
		for _, child := range collection {
			if err := s.w.tick(); err != nil {
				return err
			}
			r := child.raw.value
			matching := steps[tupleKey(child.kind, child.id)]
			root := roots[tupleKey(sourceText(r, "root_id"))]
			if len(collection) != 1 || len(matching) != 1 || len(root) != 1 || len(matching) == 1 && !sourceMatchesStep(r, matching[0]) || len(root) == 1 && !sourceMatchesRoot(r, root[0]) {
				s.mark("canonical", child.table, child.raw.key)
			}
		}
	}
	for key, collection := range roots {
		for _, root := range collection {
			if err := s.w.tick(); err != nil {
				return err
			}
			observed := byRoot[key]
			sequence := make([]RootStepRow, len(observed))
			valid := len(collection) == 1 && sourceValidRootEnvelope(root)
			count, err := strconv.ParseInt(root.StepCount, 10, 64)
			if err != nil || !signedInteger(root.StepCount, 64) || count <= 0 || count != int64(len(sequence)) {
				valid = false
			}
			seen := make(keySet)
			for _, step := range observed {
				if err := s.w.tick(); err != nil {
					return err
				}
				n, e := strconv.ParseInt(step.StepNo, 10, 64)
				k := tupleKey(step.StepNo)
				_, duplicate := seen[k]
				if e != nil || !signedInteger(step.StepNo, 64) || n <= 0 || n > int64(len(sequence)) || duplicate {
					valid = false
					continue
				}
				seen[k] = struct{}{}
				sequence[n-1] = step
			}
			for i, step := range sequence {
				if err := s.w.tick(); err != nil {
					return err
				}
				if step.StepNo != strconv.Itoa(i+1) || step.CreatedXID != root.CreatedXID {
					valid = false
				}
			}
			if !sourceBasicTemplate(root, sequence, children) {
				valid = false
			}
			if !valid {
				s.mark("root", "pp_operation_roots", key)
			}
		}
	}
	return nil
}

func sourceMatchesStep(row reflect.Value, step RootStepRow) bool {
	return sourceText(row, "root_id") == step.RootID && sourceText(row, "step_no") == step.StepNo && sourceText(row, "step_kind") == step.Kind && sourceText(row, "created_xid") == step.CreatedXID
}

func sourceMatchesRoot(row reflect.Value, root OperationRootRow) bool {
	if sourceText(row, "created_xid") != root.CreatedXID {
		return false
	}
	for _, field := range []struct {
		name     string
		expected *string
	}{{"tenant_id", root.TenantID}, {"request_id", root.RequestID}, {"attempt_id", root.AttemptID}} {
		v := sourceField(row, field.name)
		if !v.IsValid() {
			continue
		}
		if v.Kind() == reflect.Pointer {
			if !sourceSameOptional(sourceOptional(row, field.name), field.expected) {
				return false
			}
		} else if field.expected == nil || v.String() != *field.expected {
			return false
		}
	}
	if root.ScopeKind == "account_command" {
		return root.AccountTargetID != nil && sourceText(row, "target_id") == *root.AccountTargetID
	}
	return true
}

func sourceValidRootEnvelope(r OperationRootRow) bool {
	if !sourceNonzeroUUID(r.ID) || !unsignedInteger(r.CreatedXID, 64, true) || !unsignedInteger(r.SessionRoleOID, 32, true) {
		return false
	}
	account := r.EntryKind == "account_create" || r.EntryKind == "account_configure"
	if account {
		return r.ScopeKind == "account_command" && r.TenantID == nil && r.RequestID == nil && r.AttemptID == nil && r.AccountTargetID != nil && (r.EntryKind == "account_create" && r.SemanticResult == "applied" || r.EntryKind == "account_configure" && (r.SemanticResult == "applied" || r.SemanticResult == "rejected"))
	}
	if r.ScopeKind != "request" || r.TenantID == nil || r.RequestID == nil || r.AccountTargetID != nil {
		return false
	}
	switch r.EntryKind {
	case "request_birth":
		return r.AttemptID == nil && r.SemanticResult == "born"
	case "attempt_birth":
		return r.AttemptID != nil && (r.SemanticResult == "managed" || r.SemanticResult == "unmanaged")
	case "dispatch":
		return r.AttemptID != nil && r.SemanticResult == "permitted"
	case "record_finalization":
		return r.SemanticResult == "normal" || r.SemanticResult == "conflict"
	case "propose_cost":
		return r.AttemptID != nil && r.SemanticResult == "proposed"
	case "approve_cost":
		return r.AttemptID != nil && r.SemanticResult == "approved"
	case "unmanaged_correction":
		return r.AttemptID != nil && r.SemanticResult == "corrected"
	case "recover_procurement":
		return r.AttemptID != nil && (r.SemanticResult == "released" || r.SemanticResult == "unknown")
	case "reconcile":
		return r.AttemptID == nil && (r.SemanticResult == "proposed" || r.SemanticResult == "approved" || r.SemanticResult == "released")
	}
	return false
}

func sourceNonzeroUUID(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range []byte(value) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// sourceBasicTemplate recognizes the complete canonical step language. Output
// references select the next child; a matching kind or count alone is insufficient.
func sourceBasicTemplate(root OperationRootRow, steps []RootStepRow, children map[string][]sourceCanonicalRow) bool {
	cursor := 0
	take := func(kind string) (reflect.Value, bool) {
		if cursor >= len(steps) || steps[cursor].Kind != kind {
			return reflect.Value{}, false
		}
		step := steps[cursor]
		cursor++
		collection := children[tupleKey(kind, step.SourceID)]
		if len(collection) != 1 {
			return reflect.Value{}, false
		}
		return collection[0].raw.value, true
	}
	takeID := func(kind string, id *string) (reflect.Value, bool) {
		if id == nil || cursor >= len(steps) || steps[cursor].SourceID != *id {
			return reflect.Value{}, false
		}
		return take(kind)
	}
	finish := func() bool { return cursor == len(steps) }
	outbox := func(cause string, source reflect.Value, output string) bool {
		r, ok := take("cost_outbox")
		return ok && sourceText(r, "cause_kind") == cause && sourceText(r, "cause_source_id") == sourceText(source, "id") && sourceText(r, "outbox_id") == output
	}
	reset := func(cause string, sourceID string) bool {
		r, ok := take("cost_reset_decision")
		if !ok || sourceText(r, "cause_kind") != cause || sourceText(r, "cause_source_id") != sourceID {
			return false
		}
		mutation := sourceOptional(r, "cost_mutation_id")
		if sourceText(r, "policy") == "suppressed_adjudicated" {
			return mutation == nil
		}
		if sourceText(r, "policy") != "applied" {
			return false
		}
		m, ok := takeID("cost_mutation", mutation)
		return ok && sourceText(m, "cause_kind") == cause+"_reset" && sourceText(m, "cause_source_id") == sourceText(r, "id")
	}
	evidence := func() bool {
		e, ok := take("evidence_choice")
		if !ok {
			return false
		}
		transition := sourceOptional(e, "procurement_transition_id")
		if transition == nil {
			return sourceOptional(e, "procurement_event_id") == nil && sourceOptional(e, "account_journal_id") == nil
		}
		t, ok := takeID("procurement_transition", transition)
		if !ok || sourceText(t, "cause_kind") != "evidence_choice" || sourceText(t, "cause_source_id") != sourceText(e, "id") {
			return false
		}
		if sourceText(t, "event_type") == "evidence_disputed" {
			return reset("procurement_dispute", sourceText(t, "id"))
		}
		return true
	}
	switch root.EntryKind {
	case "request_birth":
		_, ok := take("request_birth")
		return ok && finish()
	case "attempt_birth":
		origin, ok := take("attempt_birth")
		if !ok || sourceText(origin, "management_mode") != root.SemanticResult {
			return false
		}
		if root.SemanticResult == "unmanaged" {
			return finish()
		}
		transition, ok := takeID("procurement_transition", sourceOptional(origin, "procurement_transition_id"))
		return ok && sourceText(transition, "cause_kind") == "attempt_birth" && sourceText(transition, "cause_source_id") == sourceText(origin, "attempt_id") && sourceText(transition, "event_type") == "reserved" && finish()
	case "account_create", "account_configure":
		r, ok := take("account_command")
		return ok && sourceText(r, "kind") == root.EntryKind && sourceText(r, "command_status") == root.SemanticResult && finish()
	case "dispatch":
		r, ok := take("attempt_delivery")
		return ok && sourceText(r, "mode") == "dispatch_permit" && finish()
	case "propose_cost":
		_, ok := take("proposal_birth")
		return ok && finish()
	case "approve_cost":
		a, ok := take("approval_consumed")
		if !ok {
			return false
		}
		t, ok := takeID("procurement_transition", sourceOptionalPointer(a, "procurement_transition_id"))
		if !ok || sourceText(t, "cause_kind") != "approval_consumed" || sourceText(t, "cause_source_id") != sourceText(a, "id") {
			return false
		}
		m, ok := takeID("cost_mutation", sourceOptionalPointer(a, "cost_mutation_id"))
		return ok && sourceText(m, "cause_kind") == "approval_consumed" && sourceText(m, "cause_source_id") == sourceText(a, "id") && outbox("approval_consumed", a, sourceText(a, "outbox_id")) && finish()
	case "unmanaged_correction":
		c, ok := take("unmanaged_correction")
		if !ok {
			return false
		}
		m, ok := takeID("cost_mutation", sourceOptionalPointer(c, "cost_mutation_id"))
		return ok && sourceText(m, "cause_kind") == "unmanaged_correction" && sourceText(m, "cause_source_id") == sourceText(c, "id") && outbox("unmanaged_correction", c, sourceText(c, "outbox_id")) && finish()
	case "recover_procurement":
		r, ok := take("recovery")
		if !ok {
			return false
		}
		d, ok := takeID("attempt_delivery", sourceOptionalPointer(r, "delivery_mutation_id"))
		if !ok || sourceText(d, "mode") != "recovery" || sourceOptional(d, "cause_source_id") == nil || *sourceOptional(d, "cause_source_id") != sourceText(r, "id") {
			return false
		}
		t, ok := takeID("procurement_transition", sourceOptionalPointer(r, "procurement_transition_id"))
		return ok && sourceText(t, "cause_kind") == "recovery" && sourceText(t, "cause_source_id") == sourceText(r, "id") && finish()
	case "record_finalization":
		incoming, ok := take("incoming")
		if !ok {
			return false
		}
		choice, ok := take("meter_choice")
		if !ok || sourceText(choice, "incoming_source_id") != sourceText(incoming, "id") {
			return false
		}
		decision, ok := take("request_decision")
		if !ok || sourceText(decision, "branch") != root.SemanticResult || sourceText(decision, "incoming_source_id") != sourceText(incoming, "id") || sourceText(decision, "meter_choice_id") != sourceText(choice, "id") {
			return false
		}
		if root.AttemptID == nil {
			return finish()
		}
		os := children[tupleKey("attempt_birth", *root.AttemptID)]
		if len(os) != 1 {
			return false
		}
		managed := sourceText(os[0].raw.value, "management_mode") == "managed"
		if root.SemanticResult == "normal" {
			delivery, ok := take("attempt_delivery")
			if !ok || sourceText(delivery, "mode") != "finalization" || !sourceSameOptional(sourceOptional(delivery, "cause_source_id"), sourceOptionalPointer(decision, "id")) {
				return false
			}
			normal, ok := take("normal_cost_decision")
			if !ok || !sourceSameOptional(sourceOptional(decision, "finalization_id"), sourceOptionalPointer(normal, "finalization_id")) {
				return false
			}
			if sourceOptional(normal, "cost_mutation_id") != nil {
				m, ok := takeID("cost_mutation", sourceOptional(normal, "cost_mutation_id"))
				if !ok || sourceText(m, "cause_kind") != "normal_cost_decision" || sourceText(m, "cause_source_id") != sourceText(normal, "id") {
					return false
				}
				output := sourceOptional(normal, "cost_recorded_outbox_id")
				if output == nil || !outbox("normal_cost_decision", normal, *output) {
					return false
				}
			}
			if managed && !evidence() {
				return false
			}
			return finish()
		}
		if managed && !evidence() {
			return false
		}
		conflict := sourceOptional(decision, "conflict_source_id")
		return conflict != nil && reset("incoming_conflict", *conflict) && finish()
	case "reconcile":
		r, ok := take("reconciliation")
		if !ok {
			return false
		}
		action := sourceText(r, "action")
		if root.SemanticResult != map[string]string{"propose": "proposed", "approve": "approved", "release": "released"}[action] {
			return false
		}
		incomingID := sourceOptional(r, "derived_incoming_source_id")
		if incomingID == nil {
			return finish()
		}
		if action != "approve" {
			return false
		}
		incoming, ok := takeID("incoming", incomingID)
		if !ok || !sourceSameOptional(sourceOptional(incoming, "reconcile_source_id"), sourceOptionalPointer(r, "id")) {
			return false
		}
		choice, ok := take("meter_choice")
		if !ok || sourceText(choice, "incoming_source_id") != *incomingID {
			return false
		}
		decision, ok := take("request_decision")
		return ok && sourceText(decision, "branch") == "normal" && sourceText(decision, "incoming_source_id") == *incomingID && sourceText(decision, "meter_choice_id") == sourceText(choice, "id") && sourceSameOptional(sourceOptional(decision, "finalization_id"), sourceOptional(r, "finalization_id")) && finish()
	}
	return false
}

func sourceOptionalPointer(row reflect.Value, name string) *string {
	v := sourceField(row, name)
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		return sourceOptional(row, name)
	}
	if v.Kind() != reflect.String {
		return nil
	}
	value := v.String()
	return &value
}
