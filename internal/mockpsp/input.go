package mockpsp

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

const mockInputTimeLayout = "2006-01-02T15:04:05.000000Z"

func ParseCheckoutInput(env paymentproto.Environment, raw []byte) (CanonicalCheckout, error) {
	r, err := paymentproto.ParseCheckout(raw)
	if err != nil {
		return CanonicalCheckout{}, fail("invalid_request")
	}
	if !mockInputEnvironment(env) || r.EnvironmentID != env.ID || r.MerchantID != env.MerchantID {
		return CanonicalCheckout{}, fail("payment_environment_invalid")
	}
	b, err := paymentproto.CanonicalCheckout(r)
	if err != nil {
		return CanonicalCheckout{}, fail("invalid_request")
	}
	return CanonicalCheckout{raw: b, digest: paymentproto.RequestDigest(b), env: env, request: r}, nil
}

func ParseCancelInput(env paymentproto.Environment, raw []byte) (CanonicalCancel, error) {
	r, err := paymentproto.ParseCancel(raw)
	if err != nil {
		return CanonicalCancel{}, fail("invalid_request")
	}
	if !mockInputEnvironment(env) || r.EnvironmentID != env.ID || r.MerchantID != env.MerchantID {
		return CanonicalCancel{}, fail("payment_environment_invalid")
	}
	b, err := paymentproto.CanonicalCancel(r)
	if err != nil {
		return CanonicalCancel{}, fail("invalid_request")
	}
	return CanonicalCancel{raw: b, digest: paymentproto.RequestDigest(b), env: env, request: r}, nil
}

func ParseQueryInput(env paymentproto.Environment, raw []byte) (CanonicalQuery, error) {
	r, err := paymentproto.ParseQuery(raw)
	if err != nil {
		return CanonicalQuery{}, fail("invalid_request")
	}
	if !mockInputEnvironment(env) || r.EnvironmentID != env.ID || r.MerchantID != env.MerchantID {
		return CanonicalQuery{}, fail("payment_environment_invalid")
	}
	b, err := paymentproto.CanonicalQuery(r)
	if err != nil {
		return CanonicalQuery{}, fail("invalid_request")
	}
	return CanonicalQuery{raw: b, digest: paymentproto.RequestDigest(b), env: env, request: r}, nil
}

var mockInputControlFields = []string{"operation_id", "action", "scenario", "merchant_order_id", "command_id", "query_id", "event_id"}
var mockInputControlNullable = []string{"merchant_order_id", "command_id", "query_id", "event_id"}

func ParseControlInput(env paymentproto.Environment, raw []byte) (CanonicalControl, error) {
	var r ControlInput
	if _, err := mockInputObject(raw, 8<<10, mockInputControlFields, mockInputControlNullable, &r); err != nil {
		return CanonicalControl{}, err
	}
	if !mockInputEnvironment(env) {
		return CanonicalControl{}, fail("payment_environment_invalid")
	}
	if !mockInputControl(r) {
		return CanonicalControl{}, fail("invalid_request")
	}
	if r.MerchantOrderID != nil {
		id, _, err := paymentproto.ParseMerchantOrderID(*r.MerchantOrderID)
		if err != nil {
			return CanonicalControl{}, fail("invalid_request")
		}
		if id != env.ID {
			return CanonicalControl{}, fail("payment_environment_invalid")
		}
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > 8<<10 {
		return CanonicalControl{}, fail("invalid_request")
	}
	return CanonicalControl{raw: b, digest: paymentproto.RequestDigest(b), env: env, request: r}, nil
}

func mockInputControl(r ControlInput) bool {
	if !mockInputUUID(r.OperationID) {
		return false
	}
	// A target is an exclusive union. Null is mandatory for all other arms.
	order := r.MerchantOrderID != nil && r.CommandID == nil && r.QueryID == nil && r.EventID == nil
	command := r.MerchantOrderID == nil && r.CommandID != nil && r.QueryID == nil && r.EventID == nil && mockInputUUID(*r.CommandID)
	query := r.MerchantOrderID == nil && r.CommandID == nil && r.QueryID != nil && r.EventID == nil && mockInputUUID(*r.QueryID)
	event := r.MerchantOrderID == nil && r.CommandID == nil && r.QueryID == nil && r.EventID != nil && mockInputReference(*r.EventID)
	if order {
		if _, _, err := paymentproto.ParseMerchantOrderID(*r.MerchantOrderID); err != nil {
			return false
		}
	}
	switch r.Action {
	case "capture_order":
		return order && r.Scenario == "normal"
	case "emit_test_event":
		return order && mockInputTestScenario(r.Scenario)
	case "release_command":
		return command && r.Scenario == "normal"
	case "arm_fault":
		switch r.Scenario {
		case "after_query_snapshot_commit_drop_reply_once":
			return query
		case "before_callback_send_once":
			return event
		case "before_command_commit_disconnect_once", "after_command_commit_drop_reply_once", "hold_checkout_before_effect", "hold_cancel_before_effect":
			return command
		}
	case "refund_confirmed":
		return command && r.Scenario == "normal"
	case "refund_final_no_effect":
		switch r.Scenario {
		case "refund_limit_exceeded", "refund_policy_rejected", "capture_not_refundable":
			return command
		}
	case "refund_conflict":
		switch r.Scenario {
		case "identity_conflict", "fact_conflict", "capture_limit_conflict":
			return command
		}
	case "refund_not_seen":
		return command && r.Scenario == "normal"
	}
	return false
}

func mockInputTestScenario(s string) bool {
	switch s {
	case "capture_at_created", "capture_before_created_1us", "capture_before_expiry_1us", "capture_at_expiry", "capture_future_6s", "amount_plus_one", "duplicate_capture_other_transaction", "same_revision_conflict", "refund_unsupported", "dispute_unsupported":
		return true
	}
	return false
}

func canonicalCommandReply(r paymentproto.CommandReply) (CanonicalCommandReply, error) {
	b, err := paymentproto.CanonicalCommandReply(r)
	if err != nil {
		return CanonicalCommandReply{}, fail("mock_unavailable")
	}
	owned, err := paymentproto.ParseCommandReply(b)
	if err != nil {
		return CanonicalCommandReply{}, fail("mock_unavailable")
	}
	return CanonicalCommandReply{raw: b, digest: paymentproto.RequestDigest(b), request: owned}, nil
}

func canonicalQueryReply(r paymentproto.QueryReply) (CanonicalQueryReply, error) {
	b, err := paymentproto.CanonicalQueryReply(r)
	if err != nil {
		return CanonicalQueryReply{}, fail("mock_unavailable")
	}
	owned, err := paymentproto.ParseQueryReply(b)
	if err != nil {
		return CanonicalQueryReply{}, fail("mock_unavailable")
	}
	return CanonicalQueryReply{raw: b, digest: paymentproto.RequestDigest(b), request: owned}, nil
}

func parseStoredCommandReply(raw []byte, digest string) (CanonicalCommandReply, error) {
	r, err := paymentproto.ParseCommandReply(raw)
	if err != nil {
		return CanonicalCommandReply{}, fail("mock_unavailable")
	}
	v, err := canonicalCommandReply(r)
	if err != nil || !bytes.Equal(v.raw, raw) || v.digest != digest {
		return CanonicalCommandReply{}, fail("mock_unavailable")
	}
	return v, nil
}

func parseStoredQueryReply(raw []byte, digest string) (CanonicalQueryReply, error) {
	r, err := paymentproto.ParseQueryReply(raw)
	if err != nil {
		return CanonicalQueryReply{}, fail("mock_unavailable")
	}
	v, err := canonicalQueryReply(r)
	if err != nil || !bytes.Equal(v.raw, raw) || v.digest != digest {
		return CanonicalQueryReply{}, fail("mock_unavailable")
	}
	return v, nil
}

var mockInputControlReplyFields = []string{"operation_id", "status", "reason", "resource_id"}

func canonicalControlReply(r ControlReply) ([]byte, error) {
	if !mockInputUUID(r.OperationID) {
		return nil, fail("invalid_request")
	}
	switch r.Status {
	case "accepted":
		if r.Reason != "accepted" || r.ResourceID == nil || !mockInputUUID(*r.ResourceID) {
			return nil, fail("invalid_request")
		}
	case "rejected":
		if (r.Reason != "state_conflict" && r.Reason != "target_not_found") || r.ResourceID != nil {
			return nil, fail("invalid_request")
		}
	default:
		return nil, fail("invalid_request")
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > 8<<10 {
		return nil, fail("invalid_request")
	}
	return b, nil
}

func parseStoredControlReply(raw []byte) (ControlReply, error) {
	var r ControlReply
	if _, err := mockInputObject(raw, 8<<10, mockInputControlReplyFields, []string{"resource_id"}, &r); err != nil {
		return ControlReply{}, fail("mock_unavailable")
	}
	b, err := canonicalControlReply(r)
	if err != nil || !bytes.Equal(b, raw) {
		return ControlReply{}, fail("mock_unavailable")
	}
	return r, nil
}

var mockInputACKFields = []string{"data", "request_id"}
var mockInputReceiptFields = []string{"receipt_id", "event_id", "status", "environment", "received_at"}

// ParseACK retains only the validated canonical receipt data. Request IDs,
// headers and the untrusted envelope never become durable observations.
func ParseACK(status int, raw []byte, expectedEventID string, dbObservedAt time.Time) (SafeACK, error) {
	if (status != 200 && status != 202) || !mockInputReference(expectedEventID) || dbObservedAt.IsZero() || dbObservedAt.Year() < 1 || dbObservedAt.Year() > 9999 {
		return SafeACK{}, fail("invalid_request")
	}
	var envelope ReceiptEnvelope
	m, err := mockInputObject(raw, 8<<10, mockInputACKFields, nil, &envelope)
	if err != nil {
		return SafeACK{}, err
	}
	if _, err = mockInputObject(m["data"], 8<<10, mockInputReceiptFields, nil, nil); err != nil {
		return SafeACK{}, err
	}
	r := envelope.Data
	if !mockInputUUID(envelope.RequestID) || !mockInputUUID(r.ReceiptID) || !mockInputReference(r.EventID) || r.Status != "received" {
		return SafeACK{}, fail("invalid_request")
	}
	if r.Environment != "sandbox" || r.EventID != expectedEventID {
		return SafeACK{}, fail("payment_environment_invalid")
	}
	receivedAt, ok := mockInputUTC6(r.ReceivedAt)
	if !ok || receivedAt.After(dbObservedAt.Add(5*time.Second)) {
		return SafeACK{}, fail("invalid_request")
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > 8<<10 {
		return SafeACK{}, fail("invalid_request")
	}
	return SafeACK{status: status, data: b, eventID: r.EventID}, nil
}

func mockInputUUID(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
		} else if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

func mockInputReference(s string) bool {
	if len(s) == 0 || len(s) > 100 || !utf8.ValidString(s) || strings.Contains(s, "://") {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "mg_") || strings.HasPrefix(lower, "bearer") {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}

func mockInputEnvironment(env paymentproto.Environment) bool {
	if !mockInputUUID(env.ID) || !mockInputReference(env.MerchantID) || len(env.PayeeLabel) == 0 || len(env.PayeeLabel) > 100 || !utf8.ValidString(env.PayeeLabel) || strings.Contains(env.PayeeLabel, "://") {
		return false
	}
	for _, r := range env.PayeeLabel {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func mockInputKeyID(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i := range s {
		if !(s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z' || s[i] >= '0' && s[i] <= '9' || s[i] == '_' || s[i] == '-') {
			return false
		}
	}
	return true
}

func mockInputDeliveryTime(s string) bool {
	if len(s) == 0 || len(s) > 12 || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n <= 253402300799
}

func mockInputSignature(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range s {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

func mockInputUTC6(s string) (time.Time, bool) {
	if len(s) != 27 {
		return time.Time{}, false
	}
	t, err := time.Parse(mockInputTimeLayout, s)
	return t, err == nil && t.Year() >= 1 && t.Year() <= 9999 && t.Format(mockInputTimeLayout) == s
}

// The standard decoder accepts duplicate keys, case aliases and malformed
// UTF-16 escapes. This scanner rejects them before decoding the exact DTO.
func mockInputObject(raw []byte, limit int, fields, nullable []string, dest any) (map[string]json.RawMessage, error) {
	bad := func() (map[string]json.RawMessage, error) { return nil, fail("invalid_request") }
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) || !mockInputUnicode(raw) {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if !mockInputScan(d, 0) {
		return bad()
	}
	if _, err := d.Token(); err != io.EOF {
		return bad()
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil || len(object) != len(fields) {
		return bad()
	}
	allowsNull := make(map[string]bool, len(nullable))
	for _, field := range nullable {
		allowsNull[field] = true
	}
	for _, field := range fields {
		value, ok := object[field]
		if !ok || (!allowsNull[field] && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return bad()
		}
	}
	if dest != nil {
		if err := json.Unmarshal(raw, dest); err != nil {
			return bad()
		}
	}
	return object, nil
}

func mockInputScan(d *json.Decoder, depth int) bool {
	if depth > 8 {
		return false
	}
	t, err := d.Token()
	if err != nil {
		return false
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			s, ok := key.(string)
			if err != nil || !ok || seen[s] {
				return false
			}
			seen[s] = true
			if !mockInputScan(d, depth+1) {
				return false
			}
		}
		t, err = d.Token()
		return err == nil && t == json.Delim('}')
	case '[':
		for d.More() {
			if !mockInputScan(d, depth+1) {
				return false
			}
		}
		t, err = d.Token()
		return err == nil && t == json.Delim(']')
	}
	return false
}

func mockInputUnicode(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		for i++; i < len(raw) && raw[i] != '"'; i++ {
			if raw[i] < 0x20 {
				return false
			}
			if raw[i] != '\\' {
				continue
			}
			i++
			if i >= len(raw) {
				return false
			}
			if raw[i] != 'u' {
				if !strings.ContainsRune(`"\/bfnrt`, rune(raw[i])) {
					return false
				}
				continue
			}
			n, ok := mockInputEscapedRune(raw, i+1)
			if !ok {
				return false
			}
			i += 4
			if n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			if n >= 0xd800 && n <= 0xdbff {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return false
				}
				low, ok := mockInputEscapedRune(raw, i+3)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		}
		if i >= len(raw) {
			return false
		}
	}
	return true
}

func mockInputEscapedRune(raw []byte, start int) (uint16, bool) {
	if start+4 > len(raw) {
		return 0, false
	}
	var n uint16
	for _, b := range raw[start : start+4] {
		var digit byte
		switch {
		case b >= '0' && b <= '9':
			digit = b - '0'
		case b >= 'a' && b <= 'f':
			digit = b - 'a' + 10
		case b >= 'A' && b <= 'F':
			digit = b - 'A' + 10
		default:
			return 0, false
		}
		n = n*16 + uint16(digit)
	}
	return n, true
}
