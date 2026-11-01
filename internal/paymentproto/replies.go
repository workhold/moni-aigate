package paymentproto

import (
	"bytes"
	"encoding/json"
	"strings"
)

var commandReplyFields = []string{"schema_version", "environment_id", "merchant_id", "merchant_order_id", "command_id", "command_kind", "parameter_digest", "command_state", "provider_order_revision", "result"}
var queryReplyFields = []string{"schema_version", "query_id", "environment_id", "merchant_id", "merchant_order_id", "command_id", "command_kind", "parameter_digest", "command_state", "provider_order_revision", "result"}
var resultFields = []string{"status", "checkout_ref", "failure_code", "capture", "closed_at"}
var captureFields = []string{"provider_transaction_id", "amount", "currency", "occurred_at", "provider_order_revision"}

func normalizeResult(in *CommandResult, rev, kind string) (*CommandResult, error) {
	bad := func() (*CommandResult, error) { return nil, fail("invalid_request") }
	if in == nil || !positive(rev) || (kind == "cancel" && in.Status != "closed" && in.Status != "captured") {
		return bad()
	}
	r := *in
	switch r.Status {
	case "ready":
		if r.CheckoutRef == nil || !strings.HasPrefix(*r.CheckoutRef, "checkout:") || !uuidValid(strings.TrimPrefix(*r.CheckoutRef, "checkout:")) || r.FailureCode != nil || r.Capture != nil || r.ClosedAt != nil {
			return bad()
		}
	case "rejected":
		if r.CheckoutRef != nil || r.FailureCode == nil || !failureCode(*r.FailureCode) || r.Capture != nil || r.ClosedAt != nil {
			return bad()
		}
	case "closed":
		if r.CheckoutRef != nil || r.FailureCode != nil || r.Capture != nil || r.ClosedAt == nil {
			return bad()
		}
		t, ok := instant(*r.ClosedAt)
		if !ok {
			return bad()
		}
		canonical := t.Format(timeLayout)
		r.ClosedAt = &canonical
	case "captured":
		if r.CheckoutRef != nil || r.FailureCode != nil || r.Capture == nil || r.ClosedAt != nil {
			return bad()
		}
		c := *r.Capture
		t, ok := instant(c.OccurredAt)
		if !reference(c.ProviderTransactionID) || !positive(c.Amount) || c.Currency != "CNY" || !ok || c.ProviderOrderRevision != rev {
			return bad()
		}
		c.OccurredAt = t.Format(timeLayout)
		r.Capture = &c
	default:
		return bad()
	}
	return &r, nil
}
func normalizeReply(state, kind string, rev *string, result *CommandResult, query bool) (*CommandResult, error) {
	switch state {
	case "not_seen":
		if !query {
			return nil, fail("invalid_request")
		}
		fallthrough
	case "in_progress":
		if rev != nil || result != nil {
			return nil, fail("invalid_request")
		}
		return nil, nil
	case "completed":
		if rev == nil {
			return nil, fail("invalid_request")
		}
		return normalizeResult(result, *rev, kind)
	default:
		return nil, fail("invalid_request")
	}
}
func normalizeCommandReply(r CommandReply) (CommandReply, error) {
	if !identity(r.SchemaVersion, r.EnvironmentID, r.MerchantID, r.CommandID, r.MerchantOrderID) || !commandKind(r.CommandKind) || !lowerHex(r.ParameterDigest) {
		return CommandReply{}, fail("invalid_request")
	}
	result, e := normalizeReply(r.CommandState, r.CommandKind, r.ProviderOrderRevision, r.Result, false)
	if e != nil {
		return CommandReply{}, e
	}
	r.Result = result
	return r, nil
}
func normalizeQueryReply(r QueryReply) (QueryReply, error) {
	if !identity(r.SchemaVersion, r.EnvironmentID, r.MerchantID, r.CommandID, r.MerchantOrderID) || !uuidValid(r.QueryID) || !commandKind(r.CommandKind) || !lowerHex(r.ParameterDigest) {
		return QueryReply{}, fail("invalid_request")
	}
	result, e := normalizeReply(r.CommandState, r.CommandKind, r.ProviderOrderRevision, r.Result, true)
	if e != nil {
		return QueryReply{}, e
	}
	r.Result = result
	return r, nil
}
func CanonicalCommandReply(r CommandReply) ([]byte, error) {
	r, e := normalizeCommandReply(r)
	if e != nil {
		return nil, e
	}
	return encode(r, bodyLimit)
}
func CanonicalQueryReply(r QueryReply) ([]byte, error) {
	r, e := normalizeQueryReply(r)
	if e != nil {
		return nil, e
	}
	return encode(r, bodyLimit)
}

func validateResultWire(raw json.RawMessage) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	m, e := strictObject(raw, bodyLimit, resultFields, []string{"checkout_ref", "failure_code", "capture", "closed_at"}, nil)
	if e != nil {
		return e
	}
	if !bytes.Equal(bytes.TrimSpace(m["closed_at"]), []byte("null")) {
		var s string
		if json.Unmarshal(m["closed_at"], &s) != nil {
			return fail("invalid_request")
		}
		t, ok := instant(s)
		if !ok || s != t.Format(timeLayout) {
			return fail("invalid_request")
		}
	}
	if !bytes.Equal(bytes.TrimSpace(m["capture"]), []byte("null")) {
		var c CaptureFact
		if _, e := strictObject(m["capture"], bodyLimit, captureFields, nil, &c); e != nil {
			return e
		}
		t, ok := instant(c.OccurredAt)
		if !ok || c.OccurredAt != t.Format(timeLayout) {
			return fail("invalid_request")
		}
	}
	return nil
}
func ParseCommandReply(raw []byte) (CommandReply, error) {
	var r CommandReply
	m, e := strictObject(raw, bodyLimit, commandReplyFields, []string{"provider_order_revision", "result"}, &r)
	if e != nil {
		return CommandReply{}, e
	}
	if e = validateResultWire(m["result"]); e != nil {
		return CommandReply{}, e
	}
	return normalizeCommandReply(r)
}
func ParseQueryReply(raw []byte) (QueryReply, error) {
	var r QueryReply
	m, e := strictObject(raw, bodyLimit, queryReplyFields, []string{"provider_order_revision", "result"}, &r)
	if e != nil {
		return QueryReply{}, e
	}
	if e = validateResultWire(m["result"]); e != nil {
		return QueryReply{}, e
	}
	return normalizeQueryReply(r)
}
