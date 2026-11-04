package procurementexecution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

// Nullable transport for the tracked pointers permits a precise incomplete
// result for damaged history; account requires all four before presence.
var observerCommandQuery = observerQuery{table: "procurement_commands", key: "operation_id", fields: []observerField{
	obsText("operation_id", false), obsText("actor", false), obsText("kind", false), obsText("target_id", false),
	obsText("input_digest", false), obsText("status", false), obsText("reason", false), obsJSON("before_state", false),
	obsJSON("after_state", false), obsTime("created_at", false), obsText("pp_source_id", true), obsText("pp_root_id", true),
	obsInt8("pp_root_step", true), obsXID("pp_created_xid", true), obsText("pp_account_journal_id", true),
}}

var observerCommandSourceQuery = observerQuery{table: "pp_account_command_sources", key: "id", fields: []observerField{
	obsText("id", false), obsText("root_id", false), obsInt8("step_no", false), obsText("step_kind", false),
	obsXID("created_xid", false), obsTime("created_at", false), obsText("operation_id", false), obsText("actor_id", false),
	obsText("actor_role", false), obsText("actor_status", false), obsText("kind", false), obsText("input_target_id", false),
	obsText("target_id", false), obsText("input_label", false), obsText("input_provider", false), obsText("input_state", false),
	obsInt8("input_limit_amount", false), obsText("input_digest", false), obsText("command_status", false), obsText("command_reason", false),
	obsTime("command_created_at", false), obsText("audit_id", false), obsInt8("input_expected_revision", true), obsText("journal_id", true),
}}

var observerCommandAuditQuery = observerQuery{table: "audit_events", key: "id", fields: []observerField{
	obsText("id", false), obsText("actor", false), obsText("tenant_scope", true), obsText("action", false),
	obsText("target", false), obsJSON("sanitized_diff", false), obsText("request_id", false), obsTime("created_at", false),
	obsText("pp_root_id", false), obsInt8("pp_root_step", false), obsText("pp_source_kind", false), obsText("pp_source_id", false),
	obsText("pp_source_role", false), obsXID("pp_created_xid", false),
}}

var observerAccountOriginQuery = observerQuery{table: "pp_account_origins", key: "account_id", fields: []observerField{
	obsText("account_id", false), obsInt2("version", false), obsText("create_operation_id", false), obsText("command_source_id", false),
	obsText("root_id", false), obsInt8("step_no", false), obsText("step_kind", false), obsText("journal_id", false),
	obsXID("created_xid", false), obsTime("created_at", false),
}}

// observerCommandDigest reproduces the original Go lockCommand input, not the
// SQL writer's digest helper or a JSONB rendering of its stored source.
func observerCommandDigest(i Invocation) (string, error) {
	var kind string
	var payload any
	switch i.root {
	case CreateAccount:
		kind = "account_create"
		payload = struct {
			OperationID string `json:"operation_id"`
			Label       string `json:"label"`
			Provider    string `json:"provider"`
			State       string `json:"state"`
			LimitAmount string `json:"limit_amount"`
		}{i.account.OperationID, i.account.Label, i.account.Provider, i.account.State, strconv.FormatInt(i.account.LimitAmount, 10)}
	case ConfigureAccount:
		kind = "account_configure"
		payload = struct {
			OperationID      string `json:"operation_id"`
			ExpectedRevision string `json:"expected_revision"`
			Label            string `json:"label"`
			Provider         string `json:"provider"`
			State            string `json:"state"`
			LimitAmount      string `json:"limit_amount"`
		}{i.account.OperationID, strconv.FormatInt(i.expected, 10), i.account.Label, i.account.Provider, i.account.State, strconv.FormatInt(i.account.LimitAmount, 10)}
	default:
		return "", observerCheck(false, ReasonIdentityUnavailable)
	}
	encoded, err := json.Marshal(struct {
		Kind    string
		Target  string
		Payload any
	}{kind, i.target, payload})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

var observerSnapshotTime = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-][0-9]{2}:[0-9]{2})$`)

// Original command JSON may retain the historic process time zone. Keep S1's
// exact object/number checks, but compare its created_at by instant as the
// original read-only validator does. This does not relax the mutation decoder.
func observerSnapshot(raw []byte) (accountSnapshot, bool, error) {
	var zero accountSnapshot
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return zero, true, nil
	}
	fields, err := observerAccountStringObject(raw)
	if err != nil {
		return zero, false, err
	}
	at, err := time.Parse(time.RFC3339Nano, fields["created_at"])
	if err != nil || !observerSnapshotTime.MatchString(fields["created_at"]) || at.Year() < 1 || at.Year() > 9999 || at.Nanosecond()%1000 != 0 {
		return zero, false, observerCheck(false, ReasonObservationProtocol)
	}
	fields["created_at"] = at.UTC().Format(time.RFC3339Nano)
	normalized, err := json.Marshal(fields)
	if err != nil {
		return zero, false, err
	}
	snapshot, null, err := decodeSnapshot(normalized)
	if err != nil {
		return zero, false, observerCheck(false, ReasonObservationProtocol)
	}
	return snapshot, null, nil
}

func observerAccountStringObject(raw []byte) (map[string]string, error) {
	if !utf8.Valid(raw) {
		return nil, observerCheck(false, ReasonObservationProtocol)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, observerCheck(false, ReasonObservationProtocol)
	}
	fields := map[string]string{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok {
			return nil, observerCheck(false, ReasonObservationProtocol)
		}
		if _, exists := fields[name]; exists {
			return nil, observerCheck(false, ReasonObservationProtocol)
		}
		var value any
		if err = d.Decode(&value); err != nil {
			return nil, observerCheck(false, ReasonObservationProtocol)
		}
		text, ok := value.(string)
		if !ok {
			return nil, observerCheck(false, ReasonObservationProtocol)
		}
		fields[name] = text
	}
	if _, err = d.Token(); err != nil || d.Decode(new(any)) != io.EOF {
		return nil, observerCheck(false, ReasonObservationProtocol)
	}
	return fields, nil
}

func (p *observationProbe) account(h *ObservationHandle) error {
	i, receipt := h.invocation, h.account
	digest, err := observerCommandDigest(i)
	if err != nil {
		return err
	}
	c, err := p.one(observerCommandQuery, i.account.OperationID, ReasonReceiptAbsent)
	if err != nil {
		return err
	}
	if err = observerCheck(c.eq("operation_id", receipt.OperationID) && c.eq("actor", receipt.Actor) &&
		c.eq("kind", receipt.Kind) && c.eq("target_id", receipt.TargetID) && c.eq("input_digest", digest) &&
		c.eq("status", receipt.Status) && c.eq("reason", receipt.Reason) && c.timestamp("created_at").Equal(receipt.CreatedAt), ReasonReceiptMismatch); err != nil {
		return err
	}
	before, beforeNull, err := observerSnapshot(c.json("before_state"))
	if err != nil {
		return err
	}
	after, afterNull, err := observerSnapshot(c.json("after_state"))
	if err != nil {
		return err
	}
	wantBefore, wantBeforeNull, err := observerSnapshot(receipt.Before)
	if err != nil {
		return err
	}
	wantAfter, wantAfterNull, err := observerSnapshot(receipt.After)
	if err != nil {
		return err
	}
	if err = observerCheck(beforeNull == wantBeforeNull && afterNull == wantAfterNull && before == wantBefore && after == wantAfter, ReasonReceiptMismatch); err != nil {
		return err
	}
	if err = observerCheck(!c.null("pp_source_id") && !c.null("pp_root_id") && !c.null("pp_root_step") && !c.null("pp_created_xid"), ReasonReceiptIncomplete); err != nil {
		return err
	}
	if receipt.AccountJournalID != nil && c.null("pp_account_journal_id") {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	if err = observerCheck(c.eq("pp_root_id", receipt.RootID) && c.integer("pp_root_step") == 1 &&
		c.eq("pp_account_journal_id", observerOptionalText(receipt.AccountJournalID)), ReasonReceiptMismatch); err != nil {
		return err
	}
	s, err := p.one(observerCommandSourceQuery, c.text("pp_source_id"), ReasonReceiptIncomplete)
	if err != nil {
		return err
	}
	if receipt.AccountJournalID != nil && s.null("journal_id") {
		return observerCheck(false, ReasonReceiptIncomplete)
	}
	if err = observerCheck(observerAccountSourceMatches(i, receipt, c, s, digest), ReasonReceiptMismatch); err != nil {
		return err
	}
	if err = p.rootAndSteps(receipt.RootID, observerRootExpectation{
		entryKind: receipt.Kind, scopeKind: "account_command", target: &receipt.TargetID,
		xid: c.text("pp_created_xid"), semantic: receipt.Status,
		steps: []observerStepExpectation{{kind: "account_command", source: s.text("id")}},
	}); err != nil {
		return err
	}
	audit, err := p.one(observerCommandAuditQuery, s.text("audit_id"), ReasonReceiptIncomplete)
	if err != nil {
		return err
	}
	if err = observerCheck(observerAccountAuditMatches(receipt, s, audit), ReasonReceiptMismatch); err != nil {
		return err
	}
	if receipt.AccountJournalID != nil {
		journal, err := p.journal(*receipt.AccountJournalID)
		if err != nil {
			return err
		}
		origin, err := p.one(observerAccountOriginQuery, receipt.TargetID, ReasonReceiptIncomplete)
		if err != nil {
			return err
		}
		if err = observerCheck(observerAccountJournalMatches(i, receipt, s, journal, origin, before, beforeNull, after, afterNull), ReasonReceiptMismatch); err != nil {
			return err
		}
	} else if err = observerCheck(i.root == ConfigureAccount && receipt.Status == "rejected" && receipt.Reason == "not_found" && beforeNull && afterNull, ReasonReceiptMismatch); err != nil {
		return err
	}
	if err = p.validate("pp_validate_original", "procurement_command", i.account.OperationID, nil); err != nil {
		return err
	}
	return p.validate("pp_validate_root", receipt.RootID)
}

func observerOptionalText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func observerAccountSourceMatches(i Invocation, receipt AccountReceipt, c, s observerRow, digest string) bool {
	expected := any(nil)
	if i.root == ConfigureAccount {
		expected = i.expected
	}
	return canonicalID(s.text("id"), true) && canonicalID(s.text("audit_id"), true) &&
		s.eq("id", c.text("pp_source_id")) && s.eq("root_id", receipt.RootID) && s.integer("step_no") == 1 &&
		s.eq("step_kind", "account_command") && s.eq("created_xid", c.text("pp_created_xid")) &&
		s.eq("operation_id", i.account.OperationID) && s.eq("actor_id", i.account.ActorID) &&
		s.eq("actor_role", "admin") && s.eq("actor_status", "active") && s.eq("kind", receipt.Kind) &&
		s.eq("input_target_id", i.target) && s.eq("target_id", receipt.TargetID) && s.eq("input_label", i.account.Label) &&
		s.eq("input_provider", i.account.Provider) && s.eq("input_state", i.account.State) &&
		s.eq("input_limit_amount", i.account.LimitAmount) && s.eq("input_digest", digest) &&
		s.eq("command_status", receipt.Status) && s.eq("command_reason", receipt.Reason) &&
		s.timestamp("command_created_at").Equal(receipt.CreatedAt) && s.eq("input_expected_revision", expected) &&
		s.eq("journal_id", observerOptionalText(receipt.AccountJournalID))
}

func observerAccountAuditMatches(receipt AccountReceipt, source, audit observerRow) bool {
	if !audit.eq("id", source.text("audit_id")) || !audit.eq("actor", receipt.Actor) || !audit.null("tenant_scope") ||
		!audit.eq("action", "procurement.command."+receipt.Status) || !audit.eq("target", receipt.OperationID) ||
		!audit.eq("request_id", "") || !audit.eq("pp_root_id", receipt.RootID) || audit.integer("pp_root_step") != 1 ||
		!audit.eq("pp_source_kind", "account_command") || !audit.eq("pp_source_id", source.text("id")) ||
		!audit.eq("pp_source_role", "account_command") || !audit.eq("pp_created_xid", source.text("created_xid")) {
		return false
	}
	// The fixed sanitized object contains only these two string properties.
	diff, err := observerAccountStringObject(audit.json("sanitized_diff"))
	if err != nil || len(diff) != 2 {
		return false
	}
	return diff["kind"] == receipt.Kind && diff["status"] == receipt.Status
}

func observerAccountJournalMatches(i Invocation, receipt AccountReceipt, source, journal, origin observerRow, before accountSnapshot, beforeNull bool, after accountSnapshot, afterNull bool) bool {
	if !journal.eq("id", *receipt.AccountJournalID) || !journal.eq("account_id", receipt.TargetID) ||
		!journal.eq("root_id", receipt.RootID) || journal.integer("step_no") != 1 || !journal.eq("step_kind", "account_command") ||
		!journal.eq("created_xid", source.text("created_xid")) || !journal.eq("source_kind", "command") ||
		!journal.eq("source_id", receipt.OperationID) || !journal.eq("command_source_id", source.text("id")) ||
		!journal.eq("command_operation_id", receipt.OperationID) || journal.integer("ordinal") <= 0 ||
		journal.integer("previous_ordinal") != journal.integer("ordinal")-1 {
		return false
	}
	for _, name := range []string{"attempt_id", "binding_id", "before_reservation_revision", "before_reservation_state", "before_reservation_held_amount", "before_reservation_spent_amount", "before_reservation_anomaly", "after_reservation_revision", "after_reservation_state", "after_reservation_held_amount", "after_reservation_spent_amount", "after_reservation_anomaly", "input_bound", "output_bound", "upper_amount", "cost_version_id", "cost_input_rate", "cost_output_rate", "cost_basis", "gate_extra", "gate_checked_at", "pb_decision_at", "pause_event_id", "transition_id", "delivery_source_id", "procurement_event_id", "dispatch_attempt_id"} {
		if !journal.null(name) {
			return false
		}
	}
	if !origin.eq("account_id", receipt.TargetID) || origin.integer("version") != 1 || origin.integer("step_no") != 1 ||
		!origin.eq("step_kind", "account_command") || !canonicalID(origin.text("create_operation_id"), false) ||
		!canonicalID(origin.text("command_source_id"), true) || !canonicalID(origin.text("root_id"), true) ||
		!canonicalID(origin.text("journal_id"), true) || origin.text("created_xid") == "0" {
		return false
	}
	created := origin.timestamp("created_at")
	if !beforeNull && !observerSnapshotInstant(before).Equal(created) || !afterNull && !observerSnapshotInstant(after).Equal(created) {
		return false
	}
	if i.root == CreateAccount {
		if !beforeNull || afterNull || !journal.eq("kind", "create") || journal.integer("ordinal") != 1 || !journal.null("previous_id") ||
			!origin.eq("create_operation_id", receipt.OperationID) || !origin.eq("command_source_id", source.text("id")) ||
			!origin.eq("root_id", receipt.RootID) || !origin.eq("journal_id", *receipt.AccountJournalID) ||
			!origin.eq("created_xid", source.text("created_xid")) || journal.integer("after_blocking_count") != 0 ||
			after.Revision != "1" || after.HeldAmount != "0" || after.SpentAmount != "0" {
			return false
		}
		for _, name := range []string{"before_label", "before_provider", "before_state", "before_limit_amount", "before_configuration_revision", "before_held_amount", "before_spent_amount", "before_blocking_count"} {
			if !journal.null(name) {
				return false
			}
		}
		return observerJournalSnapshot(journal, "after_", after)
	}
	if beforeNull || journal.integer("ordinal") <= 1 || journal.null("previous_id") ||
		journal.integer("before_blocking_count") < 0 || !journal.eq("before_blocking_count", journal["after_blocking_count"]) ||
		!observerJournalSnapshot(journal, "before_", before) {
		return false
	}
	for _, name := range []string{"before_label", "before_provider", "before_state", "before_limit_amount", "before_configuration_revision", "before_held_amount", "before_spent_amount", "before_blocking_count"} {
		if journal.null(name) {
			return false
		}
	}
	if receipt.Status == "rejected" && receipt.Reason == "revision_conflict" {
		return afterNull && before.Revision != strconv.FormatInt(i.expected, 10) && journal.eq("kind", "configure_rejected") &&
			observerJournalSnapshot(journal, "after_", before)
	}
	return receipt.Status == "applied" && receipt.Reason == "" && !afterNull && journal.eq("kind", "configure") &&
		before.Revision == strconv.FormatInt(i.expected, 10) && before.HeldAmount == after.HeldAmount && before.SpentAmount == after.SpentAmount &&
		observerJournalSnapshot(journal, "after_", after)
}

func observerSnapshotInstant(snapshot accountSnapshot) time.Time {
	at, _ := time.Parse(time.RFC3339Nano, snapshot.CreatedAt)
	return at
}

func observerJournalSnapshot(row observerRow, prefix string, snapshot accountSnapshot) bool {
	return row.eq("account_id", snapshot.ID) && row.eq(prefix+"label", snapshot.Label) && row.eq(prefix+"provider", snapshot.Provider) &&
		row.eq(prefix+"state", snapshot.State) && strconv.FormatInt(row.integer(prefix+"limit_amount"), 10) == snapshot.LimitAmount &&
		strconv.FormatInt(row.integer(prefix+"configuration_revision"), 10) == snapshot.Revision &&
		row.eq(prefix+"held_amount", snapshot.HeldAmount) && row.eq(prefix+"spent_amount", snapshot.SpentAmount)
}
