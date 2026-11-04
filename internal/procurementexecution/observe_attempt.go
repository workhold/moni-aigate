package procurementexecution

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"reflect"
	"strconv"
)

// These are the seven fixed attempt projections in the observation contract.
// They use ordinary, already granted columns only. All identities are bound by
// observationProbe; no input is interpolated into SQL or used as a selector.
var observerAttemptQuery = observerQuery{table: "upstream_attempts", key: "id", fields: []observerField{
	obsText("id", false), obsText("tenant_id", false), obsText("request_id", false), obsInt4("sequence", false),
	obsText("channel_id", false), obsText("channel_version_id", false), obsText("credential_id", false), obsText("upstream_model", false),
	obsText("delivery_state", false), obsText("cost_version_id", true), obsInt8("cost_input_rate", true), obsInt8("cost_output_rate", true),
	obsText("cost_basis", false), obsText("cost_status", false), obsMoney("cost_amount", true), obsTime("created_at", false),
	obsJSON("route_decisions", false), obsXID("pb_created_xid", true), obsTime("pb_dispatch_checked_at", true),
	obsText("pp_origin_root_id", true), obsInt8("pp_origin_root_step", true), obsXID("pp_origin_xid", true), obsTime("pp_quote_selected_at", true),
	obsText("pp_last_cost_mutation_id", true), obsText("pp_last_delivery_source_id", true), obsText("pp_dispatch_source_id", true),
	obsText("pp_dispatch_root_id", true), obsInt8("pp_dispatch_root_step", true), obsXID("pp_dispatch_xid", true),
	obsText("pp_dispatch_account_id", true), obsText("pp_dispatch_journal_id", true),
}}

var observerAttemptOriginQuery = observerQuery{table: "pp_attempt_origins", key: "attempt_id", fields: []observerField{
	obsText("attempt_id", false), obsText("root_id", false), obsInt8("step_no", false), obsText("step_kind", false), obsXID("created_xid", false),
	obsText("tenant_id", false), obsText("request_id", false), obsInt4("sequence", false), obsText("channel_id", false),
	obsText("channel_version_id", false), obsText("credential_id", false), obsText("upstream_model", false), obsJSON("route_decisions", false),
	obsText("cost_version_id", true), obsInt8("cost_input_rate", true), obsInt8("cost_output_rate", true), obsText("cost_basis", false),
	obsTime("quote_selected_at", false), obsTime("attempt_created_at", false), obsXID("pb_created_xid", false),
	obsText("initial_delivery_state", false), obsText("initial_cost_status", false), obsMoney("initial_cost_amount", true),
	obsText("management_mode", false), obsText("account_id", true), obsText("binding_id", true), obsText("procurement_transition_id", true),
	obsText("reserved_event_id", true), obsText("account_journal_id", true), obsTime("admitted_at", true),
}}

var observerAttemptHeadQuery = observerQuery{table: "pp_attempt_heads", key: "attempt_id", fields: []observerField{
	obsText("attempt_id", false), obsInt8("ordinal", false), obsText("last_node_id", true), obsInt8("member_count", true),
	obsText("member_tail_id", true), obsXID("updated_xid", false),
}}

var observerAttemptNodeQuery = observerQuery{table: "pp_attempt_nodes", key: "id", fields: []observerField{
	obsText("id", false), obsText("attempt_id", false), obsInt8("ordinal", false), obsText("previous_node_id", true),
	obsText("root_id", false), obsInt8("step_no", false), obsText("step_kind", false), obsText("source_id", false),
	obsXID("created_xid", false), obsInt8("previous_ordinal", false),
}}

var observerAttemptReservationQuery = observerQuery{table: "procurement_reservations", key: "attempt_id", fields: []observerField{
	obsText("attempt_id", false), obsText("account_id", false), obsText("binding_id", false), obsInt8("input_bound", false), obsInt8("output_bound", false),
	obsMoney("upper_amount", false), obsMoney("held_amount", false), obsMoney("spent_amount", false), obsText("state", false),
	obsBool("anomaly", false), obsInt8("revision", false), obsTime("created_at", false), obsTime("updated_at", false),
	obsXID("pb_created_xid", false), obsText("pb_control", true), obsTime("pb_admitted_at", false), obsXID("pp_created_xid", false),
	obsText("pp_origin_journal_id", false), obsText("pp_last_journal_id", false),
}}

var observerAttemptTransitionQuery = observerQuery{table: "pp_procurement_transitions", key: "id", fields: []observerField{
	obsText("id", false), obsText("root_id", false), obsInt8("step_no", false), obsText("step_kind", false), obsXID("created_xid", false),
	obsTime("created_at", false), obsText("tenant_id", false), obsText("request_id", false), obsText("attempt_id", false),
	obsText("account_id", false), obsText("binding_id", false), obsText("cause_kind", false), obsText("cause_source_id", false),
	obsText("event_id", false), obsText("journal_id", false), obsText("event_type", false), obsText("event_key", false), obsText("evidence_ref", false),
	obsMoney("held_delta", false), obsMoney("spent_delta", false), obsInt8("before_revision", true), obsText("before_state", true),
	obsMoney("before_held", true), obsMoney("before_spent", true), obsBool("before_anomaly", true),
	obsInt8("after_revision", false), obsText("after_state", false), obsMoney("after_held", false), obsMoney("after_spent", false), obsBool("after_anomaly", false),
	obsInt8("input_bound", false), obsInt8("output_bound", false), obsInt8("upper_amount", false), obsText("cost_version_id", false),
	obsInt8("cost_input_rate", false), obsInt8("cost_output_rate", false), obsText("cost_basis", false), obsText("d_attempt_birth", true),
	obsText("d_evidence_choice", true), obsText("d_approval_consumed", true), obsText("d_recovery", true),
}}

var observerAttemptEventQuery = observerQuery{table: "procurement_budget_events", key: "id", fields: []observerField{
	obsText("id", false), obsText("event_key", false), obsText("account_id", false), obsText("attempt_id", false),
	obsMoney("held_delta", false), obsMoney("spent_delta", false), obsText("type", false), obsText("evidence_ref", false),
	obsTime("created_at", false), obsXID("pb_created_xid", false), obsText("pp_root_id", false), obsInt8("pp_root_step", false),
	obsText("pp_transition_id", false), obsText("pp_journal_id", false), obsXID("pp_created_xid", false),
}}

func (p *observationProbe) attempt(h *ObservationHandle) error {
	i, receipt := h.invocation, h.attempt
	u, err := p.one(observerAttemptQuery, receipt.AttemptID, ReasonReceiptAbsent)
	if err != nil {
		return err
	}
	// These are the four original idempotency fields. A replay's new route or
	// expected quote is deliberately not an additional identity.
	if !observerAttemptReceiptMatches(u, i, receipt) {
		return observerCheck(false, ReasonReceiptMismatch)
	}
	if receipt.Outcome == "applied" && (receipt.RootID == nil || receipt.SourceID == nil ||
		!u.eq("pp_origin_root_id", *receipt.RootID) || *receipt.SourceID != receipt.AttemptID) {
		return observerCheck(false, ReasonReceiptMismatch)
	}
	// A matching replay may have captured a genuine legacy row or a successor
	// state. Only the real leaf validator may distinguish PP902 from PP906.
	if !observerAttemptCurrentBirth(u) {
		return p.attemptClassifyIncomplete(receipt.AttemptID)
	}
	origins, err := p.many(observerAttemptOriginQuery, receipt.AttemptID, 1)
	if err != nil {
		return err
	}
	if len(origins) == 0 {
		return p.attemptClassifyIncomplete(receipt.AttemptID)
	}
	o := origins[0]
	if !observerAttemptOriginMatches(u, o) {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	managed, ok := observerAttemptManagement(o)
	if !ok {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	steps := []observerStepExpectation{{kind: "attempt_birth", source: receipt.AttemptID}}
	if managed {
		steps = append(steps, observerStepExpectation{kind: "procurement_transition", source: o.text("procurement_transition_id")})
	}
	tenant, request, attempt := o.text("tenant_id"), o.text("request_id"), receipt.AttemptID
	if err := p.rootAndSteps(o.text("root_id"), observerRootExpectation{
		entryKind: "attempt_birth", scopeKind: "request", tenant: &tenant, request: &request,
		attempt: &attempt, xid: o.text("created_xid"), semantic: o.text("management_mode"), steps: steps,
	}); err != nil {
		return err
	}
	head, err := p.one(observerAttemptHeadQuery, receipt.AttemptID, ReasonReceiptIncomplete)
	if err != nil {
		return err
	}
	if !observerAttemptHeadMatches(head, o, managed) {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	if managed {
		if err := p.attemptManaged(o, head); err != nil {
			return err
		}
	}
	// These calls close the reverse-source graph and authenticate the request,
	// permanent binding, account prefix and the already supported PB relations.
	// No client-side subset, absent managed query or current PB flag replaces
	// them. They intentionally run for unmanaged attempts too.
	if err := p.validate("pp_validate_attempt", receipt.AttemptID); err != nil {
		return err
	}
	return p.validate("pp_validate_root", o.text("root_id"))
}

func (p *observationProbe) attemptClassifyIncomplete(attempt string) error {
	if err := p.validate("pp_validate_attempt", attempt); err != nil {
		return err
	}
	// An unexpectedly successful validator cannot invent a missing identity or
	// a proof of legacy. No new snapshot or fallback lookup is permitted.
	return observerCheck(false, ReasonReceiptIncomplete)
}

func observerAttemptReceiptMatches(u observerRow, i Invocation, receipt AttemptReceipt) bool {
	if len(i.args) != 13 {
		return false
	}
	tenant, a := i.args[0].(string)
	channel, b := i.args[3].(string)
	version, c := i.args[4].(string)
	credential, d := i.args[5].(string)
	model, e := i.args[6].(string)
	return a && b && c && d && e &&
		u.eq("id", receipt.AttemptID) && u.eq("tenant_id", tenant) && u.eq("request_id", i.request) &&
		u.integer("sequence") == int64(i.sequence) && receipt.RequestID == i.request && receipt.Sequence == i.sequence &&
		u.eq("channel_id", channel) && u.eq("channel_version_id", version) && u.eq("credential_id", credential) && u.eq("upstream_model", model) &&
		u.eq("delivery_state", receipt.DeliveryState) && u.eq("cost_status", receipt.CostStatus)
}

func observerAttemptCurrentBirth(u observerRow) bool {
	if !u.eq("delivery_state", "prepared") || !u.eq("cost_status", "unknown") || !u.null("cost_amount") ||
		u.null("pb_created_xid") || u.null("pp_origin_root_id") || u.null("pp_origin_root_step") || u.null("pp_origin_xid") || u.null("pp_quote_selected_at") {
		return false
	}
	return observerAttemptAllNull(u, "pb_dispatch_checked_at", "pp_last_cost_mutation_id", "pp_last_delivery_source_id", "pp_dispatch_source_id",
		"pp_dispatch_root_id", "pp_dispatch_root_step", "pp_dispatch_xid", "pp_dispatch_account_id", "pp_dispatch_journal_id")
}

func observerAttemptOriginMatches(u, o observerRow) bool {
	for _, name := range []string{"tenant_id", "request_id", "sequence", "channel_id", "channel_version_id", "credential_id", "upstream_model",
		"cost_version_id", "cost_input_rate", "cost_output_rate", "cost_basis", "pb_created_xid"} {
		if !o.eq(name, u[name]) {
			return false
		}
	}
	if !o.eq("attempt_id", u["id"]) || !o.eq("root_id", u["pp_origin_root_id"]) || !o.eq("step_no", u["pp_origin_root_step"]) ||
		!o.eq("created_xid", u["pp_origin_xid"]) || !o.eq("pb_created_xid", o["created_xid"]) ||
		!o.eq("quote_selected_at", u["pp_quote_selected_at"]) || !o.eq("attempt_created_at", u["created_at"]) ||
		!observerAttemptJSONEqual(o.json("route_decisions"), u.json("route_decisions")) || o.integer("step_no") != 1 ||
		!o.eq("step_kind", "attempt_birth") || !o.eq("initial_delivery_state", "prepared") || !o.eq("initial_cost_status", "unknown") ||
		!o.null("initial_cost_amount") || !canonicalID(o.text("attempt_id"), true) || !canonicalID(o.text("root_id"), true) ||
		!oneOf(o.text("cost_basis"), "unknown", "contract", "invoice") {
		return false
	}
	if o.null("cost_version_id") && (!o.null("cost_input_rate") || !o.null("cost_output_rate") || !o.eq("cost_basis", "unknown")) {
		return false
	}
	return (o.null("cost_input_rate") || o.integer("cost_input_rate") >= 0) && (o.null("cost_output_rate") || o.integer("cost_output_rate") >= 0)
}

func observerAttemptManagement(o observerRow) (managed, ok bool) {
	names := []string{"account_id", "binding_id", "procurement_transition_id", "reserved_event_id", "account_journal_id", "admitted_at"}
	switch o.text("management_mode") {
	case "unmanaged":
		return false, observerAttemptAllNull(o, names...)
	case "managed":
		for _, name := range names {
			if o.null(name) {
				return false, false
			}
		}
		return true, !o.null("cost_version_id") && !o.null("cost_input_rate") && !o.null("cost_output_rate") &&
			oneOf(o.text("cost_basis"), "contract", "invoice") && canonicalID(o.text("procurement_transition_id"), true) &&
			canonicalID(o.text("reserved_event_id"), true) && canonicalID(o.text("account_journal_id"), true)
	default:
		return false, false
	}
}

func observerAttemptHeadMatches(head, o observerRow, managed bool) bool {
	if !head.eq("attempt_id", o["attempt_id"]) || !head.eq("updated_xid", o["created_xid"]) || !head.null("member_tail_id") {
		return false
	}
	if !managed {
		return head.integer("ordinal") == 0 && head.null("last_node_id") && head.null("member_count")
	}
	return head.integer("ordinal") == 1 && !head.null("last_node_id") && !head.null("member_count") && head.integer("member_count") == 0
}

func (p *observationProbe) attemptManaged(o, head observerRow) error {
	node, err := p.one(observerAttemptNodeQuery, head.text("last_node_id"), ReasonReceiptIncomplete)
	if err != nil {
		return err
	}
	if !observerAttemptNodeMatches(node, o, head) {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	reservation, err := p.one(observerAttemptReservationQuery, o.text("attempt_id"), ReasonReceiptIncomplete)
	if err != nil {
		return err
	}
	upper, ok := observerAttemptUpper(reservation, o)
	if !ok || !observerAttemptReservationMatches(reservation, o, upper) {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	transition, err := p.one(observerAttemptTransitionQuery, o.text("procurement_transition_id"), ReasonReceiptIncomplete)
	if err != nil {
		return err
	}
	if !observerAttemptTransitionMatches(transition, reservation, o, upper) {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	event, err := p.one(observerAttemptEventQuery, o.text("reserved_event_id"), ReasonReceiptIncomplete)
	if err != nil {
		return err
	}
	if !observerAttemptEventMatches(event, o, upper) {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	journal, err := p.journal(o.text("account_journal_id"))
	if err != nil {
		return err
	}
	return observerCheck(observerAttemptJournalMatches(journal, reservation, transition, o, upper), ReasonReceiptIncomplete)
}

func observerAttemptNodeMatches(n, o, head observerRow) bool {
	return n.eq("id", head["last_node_id"]) && canonicalID(n.text("id"), true) && n.eq("attempt_id", o["attempt_id"]) &&
		n.integer("ordinal") == 1 && n.null("previous_node_id") && n.integer("previous_ordinal") == 0 && n.eq("root_id", o["root_id"]) &&
		n.integer("step_no") == 2 && n.eq("step_kind", "procurement_transition") && n.eq("source_id", o["procurement_transition_id"]) && n.eq("created_xid", o["created_xid"])
}

// Request-origin bounds are authenticated transitively by the mandatory V3;
// this independent arithmetic checks the already saved bounds without reading
// current policy, pricing or PB eligibility. Multiplication never uses int64.
func observerAttemptUpper(p, o observerRow) (int64, bool) {
	if p.null("input_bound") || p.null("output_bound") || o.null("cost_input_rate") || o.null("cost_output_rate") ||
		p.integer("input_bound") <= 0 || p.integer("output_bound") < 0 || o.integer("cost_input_rate") < 0 || o.integer("cost_output_rate") < 0 {
		return 0, false
	}
	in := new(big.Int).Mul(big.NewInt(p.integer("input_bound")), big.NewInt(o.integer("cost_input_rate")))
	out := new(big.Int).Mul(big.NewInt(p.integer("output_bound")), big.NewInt(o.integer("cost_output_rate")))
	total := new(big.Int).Add(in, out)
	// ceil(total / 1e6), with an exact nonnegative numerator.
	total.Add(total, big.NewInt(999999))
	total.Quo(total, big.NewInt(1000000))
	if !total.IsInt64() || total.Sign() < 0 {
		return 0, false
	}
	return total.Int64(), true
}

func observerAttemptReservationMatches(p, o observerRow, upper int64) bool {
	u := strconv.FormatInt(upper, 10)
	return p.eq("attempt_id", o["attempt_id"]) && p.eq("account_id", o["account_id"]) && p.eq("binding_id", o["binding_id"]) &&
		p.eq("upper_amount", u) && p.eq("held_amount", u) && p.eq("spent_amount", "0") && p.eq("state", "prepared") &&
		p.integer("revision") == 1 && !p.boolean("anomaly") && p.eq("pb_created_xid", o["created_xid"]) &&
		p.eq("pb_admitted_at", o["admitted_at"]) && p.eq("pp_created_xid", o["created_xid"]) &&
		p.eq("pp_origin_journal_id", o["account_journal_id"]) && p.eq("pp_last_journal_id", o["account_journal_id"])
}

func observerAttemptTransitionMatches(t, p, o observerRow, upper int64) bool {
	if !t.eq("id", o["procurement_transition_id"]) || !t.eq("root_id", o["root_id"]) || t.integer("step_no") != 2 ||
		!t.eq("step_kind", "procurement_transition") || !t.eq("created_xid", o["created_xid"]) || !t.eq("tenant_id", o["tenant_id"]) ||
		!t.eq("request_id", o["request_id"]) || !t.eq("attempt_id", o["attempt_id"]) || !t.eq("account_id", o["account_id"]) ||
		!t.eq("binding_id", o["binding_id"]) || !t.eq("cause_kind", "attempt_birth") || !t.eq("cause_source_id", o["attempt_id"]) ||
		!t.eq("event_id", o["reserved_event_id"]) || !t.eq("journal_id", o["account_journal_id"]) || !t.eq("event_type", "reserved") ||
		!t.eq("event_key", "reserve:"+o.text("attempt_id")) || !t.eq("evidence_ref", "") ||
		!t.eq("held_delta", strconv.FormatInt(upper, 10)) || !t.eq("spent_delta", "0") ||
		!observerAttemptAllNull(t, "before_revision", "before_state", "before_held", "before_spent", "before_anomaly") ||
		!t.eq("after_revision", p["revision"]) || !t.eq("after_state", p["state"]) || !t.eq("after_held", p["held_amount"]) ||
		!t.eq("after_spent", p["spent_amount"]) || !t.eq("after_anomaly", p["anomaly"]) ||
		!t.eq("input_bound", p["input_bound"]) || !t.eq("output_bound", p["output_bound"]) || t.integer("upper_amount") != upper ||
		!t.eq("d_attempt_birth", o["attempt_id"]) || !observerAttemptAllNull(t, "d_evidence_choice", "d_approval_consumed", "d_recovery") {
		return false
	}
	for _, name := range []string{"cost_version_id", "cost_input_rate", "cost_output_rate", "cost_basis"} {
		if !t.eq(name, o[name]) {
			return false
		}
	}
	return true
}

func observerAttemptEventMatches(e, o observerRow, upper int64) bool {
	return e.eq("id", o["reserved_event_id"]) && e.eq("event_key", "reserve:"+o.text("attempt_id")) && e.eq("account_id", o["account_id"]) &&
		e.eq("attempt_id", o["attempt_id"]) && e.eq("held_delta", strconv.FormatInt(upper, 10)) && e.eq("spent_delta", "0") &&
		e.eq("type", "reserved") && e.eq("evidence_ref", "") && e.eq("pb_created_xid", o["created_xid"]) &&
		e.eq("pp_root_id", o["root_id"]) && e.integer("pp_root_step") == 2 && e.eq("pp_transition_id", o["procurement_transition_id"]) &&
		e.eq("pp_journal_id", o["account_journal_id"]) && e.eq("pp_created_xid", o["created_xid"])
}

func observerAttemptJournalMatches(j, p, t, o observerRow, upper int64) bool {
	if !j.eq("id", o["account_journal_id"]) || !j.eq("account_id", o["account_id"]) || j.integer("ordinal") <= 1 ||
		j.null("previous_id") || j.integer("previous_ordinal") != j.integer("ordinal")-1 || !j.eq("root_id", o["root_id"]) ||
		j.integer("step_no") != 2 || !j.eq("step_kind", "procurement_transition") || !j.eq("created_xid", o["created_xid"]) ||
		!j.eq("kind", "reserve") || !j.eq("source_kind", "budget_event") || !j.eq("source_id", o["reserved_event_id"]) ||
		!j.eq("attempt_id", o["attempt_id"]) || !j.eq("binding_id", o["binding_id"]) ||
		!observerAttemptAllNull(j, "before_reservation_revision", "before_reservation_state", "before_reservation_held_amount", "before_reservation_spent_amount", "before_reservation_anomaly") ||
		!j.eq("after_reservation_revision", p["revision"]) || !j.eq("after_reservation_state", p["state"]) ||
		!j.eq("after_reservation_held_amount", p["held_amount"]) || !j.eq("after_reservation_spent_amount", p["spent_amount"]) ||
		!j.eq("after_reservation_anomaly", p["anomaly"]) || !j.eq("input_bound", p["input_bound"]) || !j.eq("output_bound", p["output_bound"]) ||
		j.integer("upper_amount") != upper || j.integer("gate_extra") != upper || !j.eq("gate_checked_at", t["created_at"]) ||
		!j.eq("pb_decision_at", o["admitted_at"]) || !observerAttemptAllNull(j, "pause_event_id", "command_source_id", "delivery_source_id", "command_operation_id", "dispatch_attempt_id") ||
		!j.eq("transition_id", o["procurement_transition_id"]) || !j.eq("procurement_event_id", o["reserved_event_id"]) {
		return false
	}
	for _, name := range []string{"cost_version_id", "cost_input_rate", "cost_output_rate", "cost_basis"} {
		if !j.eq(name, o[name]) {
			return false
		}
	}
	for _, name := range []string{"label", "provider", "state", "limit_amount", "configuration_revision", "spent_amount", "blocking_count"} {
		if j.null("before_"+name) || !j.eq("after_"+name, j["before_"+name]) {
			return false
		}
	}
	if !j.eq("before_state", "active") || j.integer("before_blocking_count") != 0 || j.integer("before_configuration_revision") <= 0 || j.integer("before_limit_amount") < 0 {
		return false
	}
	h, hok := observerAttemptMoney(j.text("before_held_amount"))
	s, sok := observerAttemptMoney(j.text("before_spent_amount"))
	if !hok || !sok || h.Sign() < 0 || s.Sign() < 0 {
		return false
	}
	after := new(big.Int).Add(h, big.NewInt(upper))
	used := new(big.Int).Add(after, s)
	return j.eq("after_held_amount", after.String()) && used.Cmp(big.NewInt(j.integer("before_limit_amount"))) <= 0
}

func observerAttemptAllNull(row observerRow, names ...string) bool {
	for _, name := range names {
		if !row.null(name) {
			return false
		}
	}
	return true
}

func observerAttemptMoney(s string) (*big.Int, bool) {
	if s == "" || s == "-0" || (s[0] == '0' && len(s) > 1) || (s[0] == '-' && (len(s) < 2 || s[1] == '0')) {
		return nil, false
	}
	start := 0
	if s[0] == '-' {
		start = 1
	}
	if len(s)-start > 100 {
		return nil, false
	}
	for _, r := range s[start:] {
		if r < '0' || r > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	return n, ok
}

// PostgreSQL JSONB compares object key order independently and compares JSON
// numbers by their exact value. UseNumber preserves uint64 and arbitrary JSON
// numerics; neither route input nor output passes through float64.
func observerAttemptJSONEqual(a, b []byte) bool {
	decode := func(raw []byte) (any, bool) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v any
		if d.Decode(&v) != nil {
			return nil, false
		}
		var extra any
		return v, d.Decode(&extra) == io.EOF
	}
	x, xok := decode(a)
	y, yok := decode(b)
	return xok && yok && observerAttemptJSONValueEqual(x, y)
}

func observerAttemptJSONValueEqual(x, y any) bool {
	switch a := x.(type) {
	case json.Number:
		b, ok := y.(json.Number)
		if !ok {
			return false
		}
		av, aok := new(big.Rat).SetString(a.String())
		bv, bok := new(big.Rat).SetString(b.String())
		return aok && bok && av.Cmp(bv) == 0
	case []any:
		b, ok := y.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for n := range a {
			if !observerAttemptJSONValueEqual(a[n], b[n]) {
				return false
			}
		}
		return true
	case map[string]any:
		b, ok := y.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for name, value := range a {
			other, exists := b[name]
			if !exists || !observerAttemptJSONValueEqual(value, other) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(x, y)
	}
}
