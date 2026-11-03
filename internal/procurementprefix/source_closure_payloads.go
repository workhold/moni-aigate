package procurementprefix

import (
	"encoding/json"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// This leaf checks the original persisted ABI, not business relationships or
// provenance. In particular it does not reuse a writer or another replay's
// JSON oracle, and does not cover the two periodic-budget operation documents.
type sourcePayloadValue struct {
	kind   byte
	text   string
	object map[string]sourcePayloadValue
}

func sourcePayloadUnicode(w *replayWork, raw string) bool {
	if !validText(raw) {
		return false
	}
	quoted := false
	for i := 0; i < len(raw); i++ {
		if w.tick() != nil {
			return false
		}
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(raw[i+1:i+5], 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xDC00 && n <= 0xDFFF {
			return false
		}
		if n >= 0xD800 && n <= 0xDBFF {
			if i+6 >= len(raw) || raw[i+1:i+3] != "\\u" {
				return false
			}
			low, err := strconv.ParseUint(raw[i+3:i+7], 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		}
	}
	return !quoted
}

func sourceReadPayloadValue(w *replayWork, d *json.Decoder, depth int, nodes *int) (sourcePayloadValue, bool) {
	if w.tick() != nil || depth > 2 || *nodes >= 32 {
		return sourcePayloadValue{}, false
	}
	*nodes++
	t, err := d.Token()
	if err != nil {
		return sourcePayloadValue{}, false
	}
	switch v := t.(type) {
	case nil:
		return sourcePayloadValue{kind: '0'}, true
	case string:
		return sourcePayloadValue{kind: 's', text: v}, validText(v)
	case json.Number:
		return sourcePayloadValue{kind: 'n', text: string(v)}, true
	case json.Delim:
		if v != '{' || depth > 1 {
			return sourcePayloadValue{}, false
		}
		out := sourcePayloadValue{kind: '{', object: make(map[string]sourcePayloadValue)}
		for d.More() {
			if w.tick() != nil || len(out.object) >= 13 {
				return sourcePayloadValue{}, false
			}
			t, err := d.Token()
			name, ok := t.(string)
			if err != nil || !ok || !validText(name) {
				return sourcePayloadValue{}, false
			}
			if _, duplicate := out.object[name]; duplicate {
				return sourcePayloadValue{}, false
			}
			value, ok := sourceReadPayloadValue(w, d, depth+1, nodes)
			if !ok {
				return sourcePayloadValue{}, false
			}
			out.object[name] = value
		}
		if last, err := d.Token(); err != nil || last != json.Delim('}') {
			return sourcePayloadValue{}, false
		}
		return out, true
	default:
		// No payload in this leaf contains booleans, arrays or other scalar
		// kinds. PB's separate safe_input document does contain a boolean.
		return sourcePayloadValue{}, false
	}
}

func sourceParsePayload(w *replayWork, raw string) (sourcePayloadValue, bool, error) {
	if w == nil || w.b.ctx == nil {
		return sourcePayloadValue{}, false, ErrInput
	}
	if err := w.b.check(); err != nil {
		return sourcePayloadValue{}, false, err
	}
	if len(raw) > int(fieldLimit) {
		return sourcePayloadValue{}, false, ErrResource
	}
	if !sourcePayloadUnicode(w, raw) {
		return sourcePayloadValue{}, false, w.b.check()
	}
	d := json.NewDecoder(&sourceJSONReader{w: w, raw: raw})
	d.UseNumber()
	nodes := 0
	v, valid := sourceReadPayloadValue(w, d, 0, &nodes)
	if valid {
		_, err := d.Token()
		valid = err == io.EOF
	}
	if err := w.b.check(); err != nil {
		return sourcePayloadValue{}, false, err
	}
	return v, valid, nil
}

func sourcePayloadObject(v sourcePayloadValue, names ...string) bool {
	if v.kind != '{' || len(v.object) != len(names) {
		return false
	}
	for _, name := range names {
		if _, exists := v.object[name]; !exists {
			return false
		}
	}
	return true
}

func sourcePayloadString(v sourcePayloadValue, name string) (string, bool) {
	x, exists := v.object[name]
	return x.text, exists && x.kind == 's'
}

func sourcePayloadText(v sourcePayloadValue, name string, max int) bool {
	text, ok := sourcePayloadString(v, name)
	return ok && len(text) > 0 && len(text) <= max
}

func sourcePayloadDecimal(v sourcePayloadValue, name string, bigAmount, positive bool) bool {
	text, ok := sourcePayloadString(v, name)
	if !ok {
		return false
	}
	if bigAmount {
		_, valid := numeric(text, false)
		return valid
	}
	return unsignedInteger(text, 63, positive)
}

func sourcePayloadMetadata(text string, max int, nonspace bool) bool {
	if len(text) == 0 || len(text) > max || !validText(text) || nonspace && strings.TrimSpace(text) == "" {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func sourcePayloadTimestamp(text string) bool {
	if strings.ContainsRune(text, ',') {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, text)
	return err == nil && at.Year() >= 1 && at.Year() <= 9999 && at.Nanosecond()%1000 == 0
}

func sourcePayloadReference(text string) bool {
	if len(text) == 0 || len(text) > 256 || strings.Contains(text, "://") {
		return false
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "mg_") || strings.HasPrefix(lower, "bearer") {
		return false
	}
	for _, r := range text {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}

func sourcePayloadSnapshot(kind string, v sourcePayloadValue) bool {
	if v.kind == '0' {
		return sourceOneOf(kind, "account_create", "account_configure", "credential_bind", "balance_observe")
	}
	for name, value := range v.object {
		if kind == "balance_observe" && sourceOneOf(name, "amount", "provider_as_of") && value.kind == '0' {
			continue
		}
		if value.kind != 's' || !sourcePayloadMetadata(value.text, 1000, false) {
			return false
		}
	}
	text := func(name string) string { return v.object[name].text }
	switch kind {
	case "account_create", "account_configure":
		return sourcePayloadObject(v, "id", "label", "provider", "state", "limit_amount", "held_amount", "spent_amount", "revision", "created_at") &&
			sourcePayloadMetadata(text("label"), 100, true) && sourcePayloadMetadata(text("provider"), 100, true) &&
			sourceOneOf(text("state"), "active", "paused") && sourcePayloadDecimal(v, "limit_amount", false, false) &&
			sourcePayloadDecimal(v, "held_amount", true, false) && sourcePayloadDecimal(v, "spent_amount", true, false) &&
			sourcePayloadDecimal(v, "revision", false, true) && sourcePayloadTimestamp(text("created_at"))
	case "credential_bind":
		op, err := uuid.Parse(text("operation_id"))
		return sourcePayloadObject(v, "id", "credential_id", "channel_id", "account_id", "operation_id", "bound_at", "channel_revision") &&
			err == nil && op.String() == text("operation_id") && sourcePayloadDecimal(v, "channel_revision", false, true) && sourcePayloadTimestamp(text("bound_at"))
	case "balance_observe":
		if !sourcePayloadObject(v, "id", "account_id", "operation_id", "actor", "status", "amount", "currency", "source", "semantic", "observed_at", "provider_as_of", "evidence_ref", "created_at") {
			return false
		}
		op, err := uuid.Parse(text("operation_id"))
		known := text("status") == "known"
		return err == nil && op.String() == text("operation_id") && sourceOneOf(text("status"), "known", "unknown", "unsupported", "error") &&
			(known && sourcePayloadDecimal(v, "amount", false, false) || !known && v.object["amount"].kind == '0') && text("currency") == "CNY" &&
			sourcePayloadMetadata(text("source"), 100, false) && sourcePayloadMetadata(text("semantic"), 100, false) && sourcePayloadReference(text("evidence_ref")) &&
			sourcePayloadTimestamp(text("observed_at")) && sourcePayloadTimestamp(text("created_at")) &&
			(v.object["provider_as_of"].kind == '0' || sourcePayloadTimestamp(text("provider_as_of")))
	}
	return false
}

func sourcePayloadCommandShape(kind, status, reason string, before, after sourcePayloadValue) bool {
	bn, an := before.kind == '0', after.kind == '0'
	if status == "applied" {
		if reason != "" || an {
			return false
		}
		return kind == "account_configure" && !bn || sourceOneOf(kind, "account_create", "credential_bind", "balance_observe") && bn
	}
	if status != "rejected" || !an {
		return false
	}
	switch kind {
	case "account_configure":
		return reason == "not_found" && bn || reason == "revision_conflict" && !bn
	case "credential_bind":
		return sourceOneOf(reason, "not_found", "revision_conflict", "credential_revoked") && bn || reason == "already_bound" && !bn
	case "balance_observe":
		return reason == "not_found" && bn
	}
	return false
}

func sourcePayloadUsage(v sourcePayloadValue) bool {
	if !sourcePayloadObject(v, "status", "input_tokens", "output_tokens", "total_tokens", "cached_tokens", "reasoning_tokens") || v.object["status"].kind != 's' || v.object["status"].text != "confirmed_final" {
		return false
	}
	counts := [5]int64{}
	for i, name := range []string{"input_tokens", "output_tokens", "total_tokens", "cached_tokens", "reasoning_tokens"} {
		n := v.object[name]
		if n.kind != 'n' || !unsignedInteger(n.text, 63, false) {
			return false
		}
		counts[i], _ = strconv.ParseInt(n.text, 10, 64)
	}
	return counts[3] <= counts[0] && counts[4] <= counts[1] && new(big.Int).Add(big.NewInt(counts[0]), big.NewInt(counts[1])).Cmp(big.NewInt(counts[2])) == 0
}

func sourcePayloadAudit(action string, v sourcePayloadValue) bool {
	text := func(name string) string { return v.object[name].text }
	stringsOnly := func(names ...string) bool {
		if !sourcePayloadObject(v, names...) {
			return false
		}
		for _, name := range names {
			if !sourcePayloadText(v, name, int(fieldLimit)) {
				return false
			}
		}
		return true
	}
	switch action {
	case "procurement.command.applied", "procurement.command.rejected":
		return stringsOnly("kind", "status") && sourceOneOf(text("kind"), "account_create", "account_configure", "credential_bind", "balance_observe") && action == "procurement.command."+text("status")
	case "procurement.cost_proposed":
		return stringsOnly("attempt_id", "amount", "basis", "evidence_ref") && sourcePayloadDecimal(v, "amount", false, false) && sourceOneOf(text("basis"), "contract", "invoice") && sourcePayloadReference(text("evidence_ref"))
	case "procurement.cost_approved":
		return stringsOnly("attempt_id", "amount", "evidence_ref") && sourcePayloadDecimal(v, "amount", false, false) && sourcePayloadReference(text("evidence_ref"))
	case "cost.corrected":
		return stringsOnly("amount", "basis") && sourcePayloadDecimal(v, "amount", false, false) && sourceOneOf(text("basis"), "contract", "invoice")
	case "reconciliation.proposed":
		return sourcePayloadObject(v, "usage", "evidence_ref", "reason") && sourcePayloadUsage(v.object["usage"]) && v.object["evidence_ref"].kind == 's' && sourcePayloadReference(text("evidence_ref")) && sourcePayloadText(v, "reason", 200)
	case "reconciliation.approved":
		return stringsOnly("outcome") && sourceOneOf(text("outcome"), "settled", "released", "pending_reconciliation")
	case "reconciliation.released":
		return sourcePayloadObject(v, "reason") && sourcePayloadText(v, "reason", 200)
	case "billing.settled", "billing.released":
		return stringsOnly("amount", "reason") && sourcePayloadDecimal(v, "amount", false, false)
	case "billing.profile_blocked":
		return stringsOnly("waived") && sourcePayloadDecimal(v, "waived", true, false)
	}
	return false
}

func sourcePayloadOutbox(kind string, v sourcePayloadValue) bool {
	if sourceOneOf(kind, "cost_recorded", "cost_corrected") {
		return sourcePayloadObject(v, "attempt_id", "amount", "basis") && sourcePayloadText(v, "attempt_id", int(fieldLimit)) &&
			sourcePayloadDecimal(v, "amount", kind == "cost_recorded", false) && v.object["basis"].kind == 's' && sourceOneOf(v.object["basis"].text, "contract", "invoice")
	}
	return sourceOneOf(kind, "settled", "released") && sourcePayloadObject(v, "amount", "released", "request_id") &&
		sourcePayloadDecimal(v, "amount", false, false) && sourcePayloadDecimal(v, "released", false, false) && sourcePayloadText(v, "request_id", int(fieldLimit))
}

func (s *sourceClosureState) payloadRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	// A source's referenced output remains in scope even if an attacker clears
	// its reverse pointer or changes its action/kind. Closure owns the precise
	// relationship; this leaf only prevents malformed selected payloads hiding.
	audits, outbox := make(keySet), make(keySet)
	for _, rule := range []struct {
		table, field string
		target       keySet
	}{
		{"pp_account_command_sources", "audit_id", audits}, {"pp_proposal_births", "audit_id", audits},
		{"pp_approval_consumptions", "audit_id", audits}, {"pp_unmanaged_correction_sources", "audit_id", audits},
		{"pp_reconciliation_sources", "audit_id", audits}, {"pp_customer_finish_outcomes", "billing_audit_id", audits},
		{"pp_customer_finish_outcomes", "profile_block_audit_id", audits}, {"pp_customer_finish_outcomes", "billing_outbox_id", outbox},
		{"pp_cost_outbox_links", "outbox_id", outbox},
		{"pp_normal_cost_decisions", "cost_recorded_outbox_id", outbox}, {"pp_approval_consumptions", "outbox_id", outbox},
		{"pp_unmanaged_correction_sources", "outbox_id", outbox},
	} {
		for _, raw := range s.tables[rule.table] {
			if err := s.w.tick(); err != nil {
				return err
			}
			if id := sourceOptionalPointer(raw.value, rule.field); id != nil {
				rule.target.add(*id)
			}
		}
	}
	for _, raw := range s.tables["procurement_commands"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		kind, r := sourceText(raw.value, "kind"), raw.value
		beforeRaw, afterRaw := sourceText(r, "before_state"), sourceText(r, "after_state")
		before, bv, err := sourceParsePayload(s.w, beforeRaw)
		if err != nil {
			return err
		}
		after, av, err := sourceParsePayload(s.w, afterRaw)
		if err != nil {
			return err
		}
		if !bv || !av || len(beforeRaw) > 16384 || len(afterRaw) > 16384 || !sourcePayloadSnapshot(kind, before) || !sourcePayloadSnapshot(kind, after) || !sourcePayloadCommandShape(kind, sourceText(r, "status"), sourceText(r, "reason"), before, after) {
			s.mark("scalar", "procurement_commands", raw.key)
		}
	}
	for _, raw := range s.tables["audit_events"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		action := sourceText(raw.value, "action")
		_, referenced := audits[tupleKey(sourceText(raw.value, "id"))]
		tracked := sourceOptional(raw.value, "pp_source_kind") != nil
		known := sourceOneOf(action, "procurement.command.applied", "procurement.command.rejected", "procurement.cost_proposed", "procurement.cost_approved", "cost.corrected", "reconciliation.proposed", "reconciliation.approved", "reconciliation.released")
		if !known && !tracked && !referenced {
			continue // Shared ordinary billing and unrelated audit families stay with their own checker.
		}
		v, valid, err := sourceParsePayload(s.w, sourceText(raw.value, "sanitized_diff"))
		if err != nil {
			return err
		}
		if !valid || !sourcePayloadAudit(action, v) {
			s.mark("scalar", "audit_events", raw.key)
		}
	}
	for _, raw := range s.tables["projection_outbox"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		kind := sourceText(raw.value, "kind")
		_, referenced := outbox[tupleKey(sourceText(raw.value, "event_id"))]
		if !sourceOneOf(kind, "cost_recorded", "cost_corrected") && sourceOptional(raw.value, "pp_source_kind") == nil && !referenced {
			continue
		}
		v, valid, err := sourceParsePayload(s.w, sourceText(raw.value, "sanitized_payload"))
		if err != nil {
			return err
		}
		if !valid || sourceText(raw.value, "schema_version") != "1" || !sourcePayloadOutbox(kind, v) {
			s.mark("scalar", "projection_outbox", raw.key)
		}
	}
	return s.w.b.check()
}
