package procurementexecution

import "github.com/jackc/pgx/v5/pgtype"

var observerRootQuery = observerQuery{table: "pp_operation_roots", key: "id", fields: []observerField{
	obsText("id", false), obsText("entry_kind", false), obsText("scope_kind", false), obsText("tenant_id", true), obsText("request_id", true), obsText("attempt_id", true), obsText("account_target_id", true), obsXID("created_xid", false), obsOID("session_role_oid", "int8", pgtype.Int8OID, false), obsInt8("step_count", false), obsText("semantic_result", false),
}}
var observerStepsQuery = observerQuery{table: "pp_root_steps", key: "root_id", order: "step_no", fields: []observerField{
	obsText("root_id", false), obsInt8("step_no", false), obsText("kind", false), obsText("source_id", false), obsXID("created_xid", false), obsText("d_account_command", true), obsText("d_attempt_birth", true), obsText("d_procurement_transition", true),
}}
var observerJournalQuery = observerQuery{table: "pp_account_journal", key: "id", fields: []observerField{
	obsText("id", false), obsText("account_id", false), obsInt8("ordinal", false), obsText("previous_id", true), obsInt8("previous_ordinal", false), obsText("root_id", false), obsInt8("step_no", false), obsText("step_kind", false), obsXID("created_xid", false), obsTime("created_at", false), obsText("kind", false), obsText("source_kind", false), obsText("source_id", false),
	obsText("before_label", true), obsText("before_provider", true), obsText("before_state", true), obsInt8("before_limit_amount", true), obsInt8("before_configuration_revision", true), obsMoney("before_held_amount", true), obsMoney("before_spent_amount", true), obsInt8("before_blocking_count", true),
	obsText("after_label", false), obsText("after_provider", false), obsText("after_state", false), obsInt8("after_limit_amount", false), obsInt8("after_configuration_revision", false), obsMoney("after_held_amount", false), obsMoney("after_spent_amount", false), obsInt8("after_blocking_count", false),
	obsText("attempt_id", true), obsText("binding_id", true), obsInt8("before_reservation_revision", true), obsText("before_reservation_state", true), obsMoney("before_reservation_held_amount", true), obsMoney("before_reservation_spent_amount", true), obsBool("before_reservation_anomaly", true), obsInt8("after_reservation_revision", true), obsText("after_reservation_state", true), obsMoney("after_reservation_held_amount", true), obsMoney("after_reservation_spent_amount", true), obsBool("after_reservation_anomaly", true),
	obsInt8("input_bound", true), obsInt8("output_bound", true), obsInt8("upper_amount", true), obsText("cost_version_id", true), obsInt8("cost_input_rate", true), obsInt8("cost_output_rate", true), obsText("cost_basis", true), obsInt8("gate_extra", true), obsTime("gate_checked_at", true), obsTime("pb_decision_at", true), obsText("pause_event_id", true), obsText("command_source_id", true), obsText("transition_id", true), obsText("delivery_source_id", true), obsText("command_operation_id", true), obsText("procurement_event_id", true), obsText("dispatch_attempt_id", true),
}}

type observerStepExpectation struct{ kind, source string }
type observerRootExpectation struct {
	entryKind, scopeKind, xid, semantic string
	tenant, request, attempt, target    *string
	steps                               []observerStepExpectation
}

func observerNullableText(row observerRow, name string, want *string) bool {
	if want == nil {
		return row.null(name)
	}
	return row.eq(name, *want)
}

func (p *observationProbe) rootAndSteps(id string, want observerRootExpectation) error {
	root, err := p.one(observerRootQuery, id, ReasonReceiptIncomplete)
	if err != nil {
		return err
	}
	if err = observerCheck(root.eq("id", id) && root.eq("entry_kind", want.entryKind) && root.eq("scope_kind", want.scopeKind) && root.eq("created_xid", want.xid) && root.eq("semantic_result", want.semantic) && root.eq("step_count", len(want.steps)) && root.integer("session_role_oid") > 0 && root.integer("session_role_oid") <= 1<<32-1 && observerNullableText(root, "tenant_id", want.tenant) && observerNullableText(root, "request_id", want.request) && observerNullableText(root, "attempt_id", want.attempt) && observerNullableText(root, "account_target_id", want.target), ReasonReceiptIncomplete); err != nil {
		return err
	}
	steps, err := p.many(observerStepsQuery, id, len(want.steps))
	if err != nil {
		return err
	}
	if len(steps) != len(want.steps) {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	for n, step := range steps {
		expected := want.steps[n]
		if !step.eq("root_id", id) || !step.eq("step_no", n+1) || !step.eq("kind", expected.kind) || !step.eq("source_id", expected.source) || !step.eq("created_xid", want.xid) {
			return observerCheck(false, ReasonReceiptIncomplete)
		}
		for _, domain := range []string{"account_command", "attempt_birth", "procurement_transition"} {
			field := "d_" + domain
			if domain == expected.kind {
				if !step.eq(field, expected.source) {
					return observerCheck(false, ReasonReceiptIncomplete)
				}
			} else if !step.null(field) {
				return observerCheck(false, ReasonReceiptIncomplete)
			}
		}
	}
	return nil
}

func (p *observationProbe) journal(id string) (observerRow, error) {
	return p.one(observerJournalQuery, id, ReasonReceiptIncomplete)
}
