package procurementprefix

import (
	"math"
	"reflect"
	"strconv"
)

type sourceGeneratedRule struct {
	column string
	mode   string
	field  string
	match  string
	value  string
}

// These are independently expressed row-value rules from the schema contract,
// not an evaluator for PostgreSQL expressions or the candidate SQL writer.
var sourceFixedStepKinds = map[string]string{
	"pp_request_origins": "request_birth", "pp_attempt_origins": "attempt_birth",
	"pp_account_origins": "account_command", "pp_account_command_sources": "account_command",
	"pp_attempt_delivery_sources": "attempt_delivery", "pp_incoming_sources": "incoming",
	"pp_meter_choices": "meter_choice", "pp_meter_origins": "meter_choice", "pp_conflict_sources": "meter_choice",
	"pp_request_decisions": "request_decision", "pp_normal_cost_decisions": "normal_cost_decision",
	"pp_cost_mutations": "cost_mutation", "pp_cost_reset_decisions": "cost_reset_decision",
	"pp_evidence_choices": "evidence_choice", "pp_evidence_members": "evidence_choice",
	"pp_procurement_transitions": "procurement_transition", "pp_proposal_births": "proposal_birth",
	"pp_approval_consumptions": "approval_consumed", "pp_unmanaged_correction_sources": "unmanaged_correction",
	"pp_recovery_sources": "recovery", "pp_cost_outbox_links": "cost_outbox",
	"pp_reconciliation_sources": "reconciliation", "pp_customer_finish_outcomes": "reconciliation",
	"pp_customer_finish_ledger_effects": "reconciliation", "pp_customer_finish_pb_effects": "reconciliation",
}

var sourceGeneratedRules = map[string][]sourceGeneratedRule{
	"pp_attempt_nodes":     {{column: "previous_ordinal", mode: "previous", field: "ordinal"}},
	"pp_request_decisions": {{column: "previous_ordinal", mode: "previous", field: "ordinal"}},
	"pp_evidence_members":  {{column: "previous_member_ordinal", mode: "previous", field: "member_ordinal"}},
	"pp_account_journal": {
		{column: "previous_ordinal", mode: "previous", field: "ordinal"},
		{column: "command_operation_id", field: "source_kind", match: "command", value: "source_id"},
		{column: "procurement_event_id", field: "source_kind", match: "budget_event", value: "source_id"},
		{column: "dispatch_attempt_id", field: "source_kind", match: "dispatch", value: "source_id"},
	},
	"pp_attempt_delivery_sources": {
		{column: "d_finalization", field: "mode", match: "finalization", value: "cause_source_id"},
		{column: "d_recovery", field: "mode", match: "recovery", value: "cause_source_id"},
	},
	"pp_cost_mutations": {
		{column: "d_normal_cost_decision", field: "cause_kind", match: "normal_cost_decision", value: "cause_source_id"},
		{column: "d_procurement_dispute_reset", field: "cause_kind", match: "procurement_dispute_reset", value: "cause_source_id"},
		{column: "d_incoming_conflict_reset", field: "cause_kind", match: "incoming_conflict_reset", value: "cause_source_id"},
		{column: "d_approval_consumed", field: "cause_kind", match: "approval_consumed", value: "cause_source_id"},
		{column: "d_unmanaged_correction", field: "cause_kind", match: "unmanaged_correction", value: "cause_source_id"},
	},
	"pp_cost_reset_decisions": {
		{column: "d_procurement_dispute", field: "cause_kind", match: "procurement_dispute", value: "cause_source_id"},
		{column: "d_incoming_conflict", field: "cause_kind", match: "incoming_conflict", value: "cause_source_id"},
		{column: "mutation_cause_kind", mode: "reset_cause"},
	},
	"pp_procurement_transitions": {
		{column: "d_attempt_birth", field: "cause_kind", match: "attempt_birth", value: "cause_source_id"},
		{column: "d_evidence_choice", field: "cause_kind", match: "evidence_choice", value: "cause_source_id"},
		{column: "d_approval_consumed", field: "cause_kind", match: "approval_consumed", value: "cause_source_id"},
		{column: "d_recovery", field: "cause_kind", match: "recovery", value: "cause_source_id"},
	},
	"pp_cost_outbox_links": {
		{column: "d_normal_cost_decision", field: "cause_kind", match: "normal_cost_decision", value: "cause_source_id"},
		{column: "d_approval_consumed", field: "cause_kind", match: "approval_consumed", value: "cause_source_id"},
		{column: "d_unmanaged_correction", field: "cause_kind", match: "unmanaged_correction", value: "cause_source_id"},
	},
	"pp_customer_finish_outcomes": {{column: "d_new_settlement", field: "result_kind", match: "new_settlement", value: "settlement_id"}},
}

var sourceCanonicalKinds = []string{
	"request_birth", "attempt_birth", "account_command", "attempt_delivery", "incoming", "meter_choice",
	"request_decision", "normal_cost_decision", "cost_mutation", "cost_reset_decision", "evidence_choice",
	"procurement_transition", "proposal_birth", "approval_consumed", "unmanaged_correction", "recovery", "cost_outbox", "reconciliation",
}

func (s *sourceClosureState) generated(_ PrefixRows) error {
	for table, rows := range s.tables {
		for _, raw := range rows {
			if err := s.w.tick(); err != nil {
				return err
			}
			if kind, fixed := sourceFixedStepKinds[table]; fixed && sourceText(raw.value, "step_kind") != kind {
				s.mark("generated", table, raw.key)
			}
			if table == "pp_root_steps" {
				for _, kind := range sourceCanonicalKinds {
					if err := s.w.tick(); err != nil {
						return err
					}
					var expected *string
					if sourceText(raw.value, "kind") == kind {
						value := sourceText(raw.value, "source_id")
						expected = &value
					}
					if !sourceSameOptional(sourceOptional(raw.value, "d_"+kind), expected) {
						s.mark("generated", table, raw.key)
					}
				}
			}
			for _, rule := range sourceGeneratedRules[table] {
				if err := s.w.tick(); err != nil {
					return err
				}
				if rule.mode == "previous" {
					n, err := strconv.ParseInt(sourceText(raw.value, rule.field), 10, 64)
					if err != nil || n == math.MinInt64 || sourceText(raw.value, rule.column) != strconv.FormatInt(n-1, 10) {
						s.mark("generated", table, raw.key)
					}
					continue
				}
				var expected *string
				if rule.mode == "reset_cause" {
					cause := sourceText(raw.value, "cause_kind")
					if sourceText(raw.value, "policy") == "applied" && (cause == "procurement_dispute" || cause == "incoming_conflict") {
						value := cause + "_reset"
						expected = &value
					}
				} else if sourceText(raw.value, rule.field) == rule.match {
					v := sourceField(raw.value, rule.value)
					if !v.IsValid() {
						return ErrInput
					}
					if optional := sourceOptional(raw.value, rule.value); optional != nil {
						expected = optional
					} else if v.Kind() == reflect.String {
						value := v.String()
						expected = &value
					}
				}
				if !sourceSameOptional(sourceOptional(raw.value, rule.column), expected) {
					s.mark("generated", table, raw.key)
				}
			}
		}
	}
	return nil
}

func sourceSameOptional(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
