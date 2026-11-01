package mockpsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

const (
	mockUnitEnvironmentID = "11111111-1111-4111-8111-111111111111"
	mockUnitOtherEnvID    = "22222222-2222-4222-8222-222222222222"
	mockUnitOrderID       = "33333333-3333-4333-8333-333333333333"
	mockUnitCommandID     = "44444444-4444-4444-8444-444444444444"
	mockUnitQueryID       = "55555555-5555-4555-8555-555555555555"
	mockUnitOperationID   = "66666666-6666-4666-8666-666666666666"
	mockUnitReceiptID     = "77777777-7777-4777-8777-777777777777"
	mockUnitRequestID     = "88888888-8888-4888-8888-888888888888"
)

func mockUnitEnv() paymentproto.Environment {
	return paymentproto.Environment{ID: mockUnitEnvironmentID, MerchantID: "merchant:unit", PayeeLabel: "本地测试收款方"}
}

func mockUnitOrder(t *testing.T, envID string) string {
	t.Helper()
	id, err := paymentproto.MerchantOrderID(envID, mockUnitOrderID)
	if err != nil {
		t.Fatal("merchant order fixture")
	}
	return id
}

func mockUnitString(s string) *string { return &s }

func mockUnitJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal("JSON fixture")
	}
	return b
}

func mockUnitCode(t *testing.T, err error, want string) {
	t.Helper()
	if got := ErrorCode(err); got != want {
		t.Fatalf("safe code = %q, want %q", got, want)
	}
}

func mockUnitCheckout(t *testing.T) paymentproto.CheckoutRequest {
	t.Helper()
	return paymentproto.CheckoutRequest{SchemaVersion: 1, EnvironmentID: mockUnitEnvironmentID,
		MerchantID: "merchant:unit", CommandID: mockUnitCommandID, MerchantOrderID: mockUnitOrder(t, mockUnitEnvironmentID),
		Amount: "100", Currency: "CNY", CreatedAt: "2026-11-01T01:02:03.1Z", ExpiresAt: "2026-11-01T01:17:03.100Z"}
}

func mockUnitCancel(t *testing.T) paymentproto.CancelRequest {
	t.Helper()
	return paymentproto.CancelRequest{SchemaVersion: 1, EnvironmentID: mockUnitEnvironmentID,
		MerchantID: "merchant:unit", CommandID: mockUnitCommandID, MerchantOrderID: mockUnitOrder(t, mockUnitEnvironmentID)}
}

func mockUnitQuery(t *testing.T) paymentproto.QueryRequest {
	t.Helper()
	return paymentproto.QueryRequest{SchemaVersion: 1, EnvironmentID: mockUnitEnvironmentID,
		MerchantID: "merchant:unit", QueryID: mockUnitQueryID, CommandID: mockUnitCommandID,
		MerchantOrderID: mockUnitOrder(t, mockUnitEnvironmentID), CommandKind: "checkout", ParameterDigest: strings.Repeat("a", 64)}
}

func mockUnitControl(t *testing.T) ControlInput {
	t.Helper()
	return ControlInput{OperationID: mockUnitOperationID, Action: "capture_order", Scenario: "normal", MerchantOrderID: mockUnitString(mockUnitOrder(t, mockUnitEnvironmentID))}
}

type mockUnitBytes interface{ Bytes() ([]byte, error) }

func TestCanonicalInputOwnedBytes(t *testing.T) {
	env := mockUnitEnv()
	checkoutRaw := mockUnitJSON(t, mockUnitCheckout(t))
	checkout, err := ParseCheckoutInput(env, checkoutRaw)
	mockUnitCode(t, err, "")
	if checkout.request.CreatedAt != "2026-11-01T01:02:03.100000Z" || checkout.request.ExpiresAt != "2026-11-01T01:17:03.100000Z" {
		t.Fatal("checkout timestamps were not normalized by P")
	}
	cancelRaw := mockUnitJSON(t, mockUnitCancel(t))
	cancel, err := ParseCancelInput(env, cancelRaw)
	mockUnitCode(t, err, "")
	queryRaw := mockUnitJSON(t, mockUnitQuery(t))
	query, err := ParseQueryInput(env, queryRaw)
	mockUnitCode(t, err, "")
	if query.digest == query.request.ParameterDigest {
		t.Fatal("query digest reused the command parameter digest")
	}
	controlRaw := mockUnitJSON(t, mockUnitControl(t))
	control, err := ParseControlInput(env, append([]byte(" \n"), controlRaw...))
	mockUnitCode(t, err, "")
	if !bytes.Contains(control.raw, []byte(`"command_id":null,"query_id":null,"event_id":null`)) {
		t.Fatal("control must preserve explicit null arms")
	}
	for _, tc := range []struct {
		name   string
		value  mockUnitBytes
		raw    []byte
		digest string
	}{
		{"checkout", checkout, checkoutRaw, checkout.digest},
		{"cancel", cancel, cancelRaw, cancel.digest},
		{"query", query, queryRaw, query.digest},
		{"control", control, controlRaw, control.digest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, err := tc.value.Bytes()
			mockUnitCode(t, err, "")
			want := append([]byte(nil), first...)
			tc.raw[0] = '!'
			first[0] = '!'
			second, err := tc.value.Bytes()
			mockUnitCode(t, err, "")
			if !bytes.Equal(second, want) || paymentproto.RequestDigest(second) != tc.digest {
				t.Fatal("caller mutation changed canonical input")
			}
		})
	}
	if checkout.env != env || cancel.env != env || query.env != env || control.env != env {
		t.Fatal("canonical input lost the exact startup environment")
	}
}

func TestCanonicalInputEnvironmentBoundary(t *testing.T) {
	checkout := mockUnitJSON(t, mockUnitCheckout(t))
	cancel := mockUnitJSON(t, mockUnitCancel(t))
	query := mockUnitJSON(t, mockUnitQuery(t))
	control := mockUnitJSON(t, mockUnitControl(t))
	for _, tc := range []struct {
		name string
		load func(paymentproto.Environment) error
	}{
		{"checkout", func(e paymentproto.Environment) error { _, err := ParseCheckoutInput(e, checkout); return err }},
		{"cancel", func(e paymentproto.Environment) error { _, err := ParseCancelInput(e, cancel); return err }},
		{"query", func(e paymentproto.Environment) error { _, err := ParseQueryInput(e, query); return err }},
		{"control", func(e paymentproto.Environment) error { _, err := ParseControlInput(e, control); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, bad := range []paymentproto.Environment{
				{},
				{ID: mockUnitOtherEnvID, MerchantID: "merchant:unit", PayeeLabel: "测试"},
				{ID: mockUnitEnvironmentID, MerchantID: "merchant:unit", PayeeLabel: "https://invalid"},
				{ID: mockUnitEnvironmentID, MerchantID: "merchant:unit", PayeeLabel: "bad\nlabel"},
			} {
				mockUnitCode(t, tc.load(bad), "payment_environment_invalid")
			}
			if tc.name != "control" {
				e := mockUnitEnv()
				e.MerchantID = "merchant:other"
				mockUnitCode(t, tc.load(e), "payment_environment_invalid")
			}
		})
	}
	_, err := ParseCheckoutInput(mockUnitEnv(), []byte(`{}`))
	mockUnitCode(t, err, "invalid_request")
	_, err = ParseCancelInput(mockUnitEnv(), []byte(`{}`))
	mockUnitCode(t, err, "invalid_request")
	_, err = ParseQueryInput(mockUnitEnv(), []byte(`{}`))
	mockUnitCode(t, err, "invalid_request")
	badOrder := mockUnitControl(t)
	badOrder.MerchantOrderID = mockUnitString(mockUnitOrder(t, mockUnitOtherEnvID))
	_, err = ParseControlInput(mockUnitEnv(), mockUnitJSON(t, badOrder))
	mockUnitCode(t, err, "payment_environment_invalid")
}

func TestControlInputExclusiveUnions(t *testing.T) {
	order := mockUnitString(mockUnitOrder(t, mockUnitEnvironmentID))
	command, query, event := mockUnitString(mockUnitCommandID), mockUnitString(mockUnitQueryID), mockUnitString("evt:local-unit")
	valid := []ControlInput{
		{OperationID: mockUnitOperationID, Action: "capture_order", Scenario: "normal", MerchantOrderID: order},
		{OperationID: mockUnitOperationID, Action: "release_command", Scenario: "normal", CommandID: command},
	}
	for _, scenario := range []string{"capture_at_created", "capture_before_created_1us", "capture_before_expiry_1us", "capture_at_expiry", "capture_future_6s", "amount_plus_one", "duplicate_capture_other_transaction", "same_revision_conflict", "refund_unsupported", "dispute_unsupported"} {
		valid = append(valid, ControlInput{OperationID: mockUnitOperationID, Action: "emit_test_event", Scenario: scenario, MerchantOrderID: order})
	}
	for _, scenario := range []string{"before_command_commit_disconnect_once", "after_command_commit_drop_reply_once", "hold_checkout_before_effect", "hold_cancel_before_effect"} {
		valid = append(valid, ControlInput{OperationID: mockUnitOperationID, Action: "arm_fault", Scenario: scenario, CommandID: command})
	}
	valid = append(valid,
		ControlInput{OperationID: mockUnitOperationID, Action: "arm_fault", Scenario: "after_query_snapshot_commit_drop_reply_once", QueryID: query},
		ControlInput{OperationID: mockUnitOperationID, Action: "arm_fault", Scenario: "before_callback_send_once", EventID: event})
	for _, r := range valid {
		t.Run(r.Action+"/"+r.Scenario, func(t *testing.T) {
			raw := mockUnitJSON(t, r)
			v, err := ParseControlInput(mockUnitEnv(), raw)
			mockUnitCode(t, err, "")
			if !bytes.Equal(v.raw, raw) {
				t.Fatal("control canonical field order changed")
			}
			wrong := r
			wrong.Scenario = "arbitrary_scenario"
			_, err = ParseControlInput(mockUnitEnv(), mockUnitJSON(t, wrong))
			mockUnitCode(t, err, "invalid_request")
			wrong = r
			wrong.OperationID = strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
			_, err = ParseControlInput(mockUnitEnv(), mockUnitJSON(t, wrong))
			mockUnitCode(t, err, "invalid_request")
			wrong = r
			wrong.MerchantOrderID, wrong.CommandID, wrong.QueryID, wrong.EventID = nil, nil, nil, nil
			_, err = ParseControlInput(mockUnitEnv(), mockUnitJSON(t, wrong))
			mockUnitCode(t, err, "invalid_request")
		})
	}
	for _, r := range []ControlInput{
		{OperationID: mockUnitOperationID, Action: "capture_order", Scenario: "normal", MerchantOrderID: order, CommandID: command},
		{OperationID: mockUnitOperationID, Action: "capture_order", Scenario: "normal", MerchantOrderID: mockUnitString("order:raw")},
		{OperationID: mockUnitOperationID, Action: "arm_fault", Scenario: "before_callback_send_once", EventID: mockUnitString("bearer-secret")},
		{OperationID: mockUnitOperationID, Action: "arm_fault", Scenario: "after_query_snapshot_commit_drop_reply_once", CommandID: command},
		{OperationID: mockUnitOperationID, Action: "release_command", Scenario: "normal", CommandID: mockUnitString("00000000-0000-0000-0000-000000000000")},
		{OperationID: mockUnitOperationID, Action: "arbitrary_action", Scenario: "normal", CommandID: command},
	} {
		_, err := ParseControlInput(mockUnitEnv(), mockUnitJSON(t, r))
		mockUnitCode(t, err, "invalid_request")
	}
}

func TestControlInputStrictJSON(t *testing.T) {
	valid := string(mockUnitJSON(t, mockUnitControl(t)))
	cases := map[string][]byte{
		"unknown":       []byte(strings.TrimSuffix(valid, "}") + `,"extra":1}`),
		"missing":       []byte(strings.Replace(valid, `,"event_id":null`, "", 1)),
		"duplicate":     []byte(strings.TrimSuffix(valid, "}") + `,"action":"capture_order"}`),
		"escaped_dup":   []byte(strings.TrimSuffix(valid, "}") + `,"act\u0069on":"capture_order"}`),
		"case_alias":    []byte(strings.Replace(valid, `"action"`, `"Action"`, 1)),
		"trailing":      []byte(valid + `{}`),
		"null_required": []byte(strings.Replace(valid, `"scenario":"normal"`, `"scenario":null`, 1)),
		"lone_high":     []byte(strings.Replace(valid, `"normal"`, `"\ud800"`, 1)),
		"lone_low":      []byte(strings.Replace(valid, `"normal"`, `"\udc00"`, 1)),
		"bad_escape":    []byte(strings.Replace(valid, `"normal"`, `"\q"`, 1)),
		"control":       []byte(strings.Replace(valid, `normal`, "nor\nmal", 1)),
		"root_null":     []byte(`null`),
		"root_array":    []byte(`[]`),
		"root_string":   []byte(`"object"`),
		"oversize":      append(bytes.Repeat([]byte(" "), 8193), []byte(valid)...),
	}
	badUTF8 := []byte(valid)
	badUTF8[bytes.Index(badUTF8, []byte("normal"))] = 0xff
	cases["utf8"] = badUTF8
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseControlInput(mockUnitEnv(), raw)
			mockUnitCode(t, err, "invalid_request")
		})
	}
	// Whitespace up to the exact transport-independent parser limit is legal.
	padded := append(bytes.Repeat([]byte(" "), 8192-len(valid)), []byte(valid)...)
	_, err := ParseControlInput(mockUnitEnv(), padded)
	mockUnitCode(t, err, "")
	_, err = ParseControlInput(mockUnitEnv(), append([]byte(" "), padded...))
	mockUnitCode(t, err, "invalid_request")
}

func mockUnitCommandReply(t *testing.T) paymentproto.CommandReply {
	t.Helper()
	return paymentproto.CommandReply{SchemaVersion: 1, EnvironmentID: mockUnitEnvironmentID, MerchantID: "merchant:unit",
		MerchantOrderID: mockUnitOrder(t, mockUnitEnvironmentID), CommandID: mockUnitCommandID, CommandKind: "checkout",
		ParameterDigest: strings.Repeat("a", 64), CommandState: "completed", ProviderOrderRevision: mockUnitString("1"),
		Result: &paymentproto.CommandResult{Status: "ready", CheckoutRef: mockUnitString("checkout:" + mockUnitOrderID)}}
}

func mockUnitQueryReply(t *testing.T) paymentproto.QueryReply {
	t.Helper()
	r := mockUnitCommandReply(t)
	return paymentproto.QueryReply{SchemaVersion: r.SchemaVersion, QueryID: mockUnitQueryID,
		EnvironmentID: r.EnvironmentID, MerchantID: r.MerchantID, MerchantOrderID: r.MerchantOrderID, CommandID: r.CommandID,
		CommandKind: r.CommandKind, ParameterDigest: r.ParameterDigest, CommandState: r.CommandState,
		ProviderOrderRevision: r.ProviderOrderRevision, Result: r.Result}
}

func TestCanonicalReplyStoredBytes(t *testing.T) {
	commandDTO := mockUnitCommandReply(t)
	command, err := canonicalCommandReply(commandDTO)
	mockUnitCode(t, err, "")
	queryDTO := mockUnitQueryReply(t)
	query, err := canonicalQueryReply(queryDTO)
	mockUnitCode(t, err, "")
	*commandDTO.ProviderOrderRevision = "9"
	*commandDTO.Result.CheckoutRef = "checkout:" + mockUnitRequestID
	*queryDTO.ProviderOrderRevision = "9"
	*queryDTO.Result.CheckoutRef = "checkout:" + mockUnitRequestID
	if *command.request.ProviderOrderRevision != "1" || *command.request.Result.CheckoutRef != "checkout:"+mockUnitOrderID || *query.request.ProviderOrderRevision != "1" || *query.request.Result.CheckoutRef != "checkout:"+mockUnitOrderID {
		t.Fatal("canonical reply retained a caller-owned pointer")
	}
	for _, tc := range []struct {
		name   string
		value  mockUnitBytes
		raw    []byte
		digest string
		load   func([]byte, string) (mockUnitBytes, error)
	}{
		{"command", command, command.raw, command.digest, func(b []byte, d string) (mockUnitBytes, error) { return parseStoredCommandReply(b, d) }},
		{"query", query, query.raw, query.digest, func(b []byte, d string) (mockUnitBytes, error) { return parseStoredQueryReply(b, d) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := append([]byte(nil), tc.raw...)
			loaded, err := tc.load(raw, tc.digest)
			mockUnitCode(t, err, "")
			raw[0] = '!'
			owned, err := loaded.Bytes()
			mockUnitCode(t, err, "")
			if !bytes.Equal(owned, tc.raw) {
				t.Fatal("stored canonical reply retained caller bytes")
			}
			owned[0] = '!'
			again, err := loaded.Bytes()
			mockUnitCode(t, err, "")
			if !bytes.Equal(again, tc.raw) {
				t.Fatal("Bytes exposed stored reply memory")
			}
			_, err = tc.load(tc.raw, strings.Repeat("0", 64))
			mockUnitCode(t, err, "mock_unavailable")
			for _, bad := range [][]byte{append([]byte(" "), tc.raw...), []byte(`{}`), append(tc.raw[:len(tc.raw):len(tc.raw)], []byte(`{}`)...)} {
				_, err := tc.load(bad, paymentproto.RequestDigest(bad))
				mockUnitCode(t, err, "mock_unavailable")
			}
		})
	}
	_, err = canonicalCommandReply(paymentproto.CommandReply{})
	mockUnitCode(t, err, "mock_unavailable")
	_, err = canonicalQueryReply(paymentproto.QueryReply{})
	mockUnitCode(t, err, "mock_unavailable")
}

func TestControlReplyUnionAndStoredCanonical(t *testing.T) {
	for _, r := range []ControlReply{
		{OperationID: mockUnitOperationID, Status: "accepted", Reason: "accepted", ResourceID: mockUnitString(mockUnitOrderID)},
		{OperationID: mockUnitOperationID, Status: "rejected", Reason: "state_conflict"},
		{OperationID: mockUnitOperationID, Status: "rejected", Reason: "target_not_found"},
	} {
		b, err := canonicalControlReply(r)
		mockUnitCode(t, err, "")
		loaded, err := parseStoredControlReply(b)
		mockUnitCode(t, err, "")
		if loaded.OperationID != r.OperationID || loaded.Status != r.Status || loaded.Reason != r.Reason {
			t.Fatal("control reply changed")
		}
		for _, bad := range [][]byte{
			append([]byte(" "), b...),
			[]byte(strings.Replace(string(b), `"status"`, `"Status"`, 1)),
			[]byte(strings.TrimSuffix(string(b), "}") + `,"reason":"accepted"}`),
			[]byte(strings.TrimSuffix(string(b), "}") + `,"extra":null}`),
			[]byte(strings.Replace(string(b), `"reason":"`+r.Reason+`"`, `"reason":null`, 1)),
		} {
			_, err := parseStoredControlReply(bad)
			mockUnitCode(t, err, "mock_unavailable")
		}
	}
	for _, r := range []ControlReply{
		{},
		{OperationID: mockUnitOperationID, Status: "accepted", Reason: "accepted"},
		{OperationID: mockUnitOperationID, Status: "accepted", Reason: "state_conflict", ResourceID: mockUnitString(mockUnitOrderID)},
		{OperationID: mockUnitOperationID, Status: "accepted", Reason: "accepted", ResourceID: mockUnitString("not-uuid")},
		{OperationID: mockUnitOperationID, Status: "rejected", Reason: "accepted"},
		{OperationID: mockUnitOperationID, Status: "rejected", Reason: "state_conflict", ResourceID: mockUnitString(mockUnitOrderID)},
	} {
		_, err := canonicalControlReply(r)
		mockUnitCode(t, err, "invalid_request")
	}
}

func mockUnitACK(t *testing.T, at string) []byte {
	t.Helper()
	return mockUnitJSON(t, ReceiptEnvelope{Data: ReceiptData{ReceiptID: mockUnitReceiptID, EventID: "evt:local-unit",
		Status: "received", Environment: "sandbox", ReceivedAt: at}, RequestID: mockUnitRequestID})
}

func TestParseACKReceiptBoundary(t *testing.T) {
	now := time.Date(2026, 11, 1, 1, 2, 3, 456789000, time.UTC)
	for _, at := range []string{
		"0001-01-01T00:00:00.000000Z", // UTC6 history includes Go's zero time.
		"2001-01-01T00:00:00.000001Z", // A durable duplicate receipt has no age floor.
		now.Format(mockInputTimeLayout),
		now.Add(5 * time.Second).Format(mockInputTimeLayout),
	} {
		for _, status := range []int{200, 202} {
			raw := mockUnitACK(t, at)
			ack, err := ParseACK(status, raw, "evt:local-unit", now)
			mockUnitCode(t, err, "")
			safe, err := ack.DataBytes()
			mockUnitCode(t, err, "")
			var data ReceiptData
			if err := json.Unmarshal(safe, &data); err != nil || data.ReceivedAt != at || bytes.Contains(safe, []byte("request_id")) || bytes.Contains(safe, []byte(`"data"`)) {
				t.Fatal("ACK persisted an envelope or changed receipt data")
			}
			if ack.status != status || ack.eventID != "evt:local-unit" {
				t.Fatal("ACK lost its validated status or event binding")
			}
			raw[0], safe[0] = '!', '!'
			again, err := ack.DataBytes()
			mockUnitCode(t, err, "")
			if len(again) == 0 || again[0] != '{' {
				t.Fatal("ACK retained or exposed caller bytes")
			}
		}
	}
	_, err := ParseACK(202, mockUnitACK(t, now.Add(5*time.Second+time.Microsecond).Format(mockInputTimeLayout)), "evt:local-unit", now)
	mockUnitCode(t, err, "invalid_request")
	for _, status := range []int{0, 201, 204, 400, 500} {
		_, err := ParseACK(status, mockUnitACK(t, now.Format(mockInputTimeLayout)), "evt:local-unit", now)
		mockUnitCode(t, err, "invalid_request")
	}
	for _, invalidDBNow := range []time.Time{{}, time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		_, err := ParseACK(202, mockUnitACK(t, now.Format(mockInputTimeLayout)), "evt:local-unit", invalidDBNow)
		mockUnitCode(t, err, "invalid_request")
	}
	max := time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	_, err = ParseACK(202, mockUnitACK(t, max.Format(mockInputTimeLayout)), "evt:local-unit", max)
	mockUnitCode(t, err, "")
	for _, at := range []string{
		"0000-01-01T00:00:00.000000Z", "2026-02-30T00:00:00.000000Z",
		"2026-11-01T01:02:03Z", "2026-11-01T01:02:03.45678Z", "2026-11-01T01:02:03.4567890Z",
		"2026-11-01T01:02:03.456789+00:00", "2026-11-01T01:02:03,456789Z", "2026-11-01T01:02:60.456789Z",
	} {
		_, err := ParseACK(202, mockUnitACK(t, at), "evt:local-unit", now)
		mockUnitCode(t, err, "invalid_request")
	}
}

func TestParseACKStrictEnvelope(t *testing.T) {
	now := time.Date(2026, 11, 1, 1, 2, 3, 456789000, time.UTC)
	valid := string(mockUnitACK(t, now.Format(mockInputTimeLayout)))
	cases := map[string][]byte{
		"root_null":       []byte(`null`),
		"data_null":       []byte(strings.Replace(valid, `"data":{`, `"data":null,"ignored":{`, 1)),
		"data_string":     []byte(`{"data":"unexpected","request_id":"` + mockUnitRequestID + `"}`),
		"root_extra":      []byte(strings.TrimSuffix(valid, "}") + `,"extra":true}`),
		"root_duplicate":  []byte(strings.TrimSuffix(valid, "}") + `,"request_id":"` + mockUnitRequestID + `"}`),
		"escaped_dup":     []byte(strings.Replace(valid, `"status":"received"`, `"status":"received","stat\u0075s":"received"`, 1)),
		"inner_duplicate": []byte(strings.Replace(valid, `"status":"received"`, `"status":"received","status":"received"`, 1)),
		"inner_extra":     []byte(strings.Replace(valid, `"status":"received"`, `"status":"received","extra":null`, 1)),
		"root_case_alias": []byte(strings.Replace(valid, `"data"`, `"Data"`, 1)),
		"inner_alias":     []byte(strings.Replace(valid, `"status"`, `"Status"`, 1)),
		"inner_null":      []byte(strings.Replace(valid, `"received"`, `null`, 1)),
		"request_null":    []byte(strings.Replace(valid, `"request_id":"`+mockUnitRequestID+`"`, `"request_id":null`, 1)),
		"receipt_uuid":    []byte(strings.Replace(valid, mockUnitReceiptID, "not-uuid", 1)),
		"request_uuid":    []byte(strings.Replace(valid, mockUnitRequestID, "00000000-0000-0000-0000-000000000000", 1)),
		"status":          []byte(strings.Replace(valid, `"received"`, `"accepted"`, 1)),
		"event_syntax":    []byte(strings.Replace(valid, `"evt:local-unit"`, `"https://unexpected"`, 1)),
		"high_surrogate":  []byte(strings.Replace(valid, `"received"`, `"\ud800"`, 1)),
		"low_surrogate":   []byte(strings.Replace(valid, `"received"`, `"\udc00"`, 1)),
		"trailing":        []byte(valid + `null`),
		"missing":         []byte(strings.Replace(valid, `"status":"received",`, "", 1)),
	}
	badUTF8 := []byte(valid)
	badUTF8[bytes.Index(badUTF8, []byte("received"))] = 0xff
	cases["utf8"] = badUTF8
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseACK(202, raw, "evt:local-unit", now)
			mockUnitCode(t, err, "invalid_request")
		})
	}
	for _, raw := range [][]byte{
		[]byte(strings.Replace(valid, `"sandbox"`, `"production"`, 1)),
		[]byte(strings.Replace(valid, `"evt:local-unit"`, `"evt:other"`, 1)),
	} {
		_, err := ParseACK(202, raw, "evt:local-unit", now)
		mockUnitCode(t, err, "payment_environment_invalid")
	}
	padded := append(bytes.Repeat([]byte(" "), 8192-len(valid)), []byte(valid)...)
	_, err := ParseACK(202, padded, "evt:local-unit", now)
	mockUnitCode(t, err, "")
	_, err = ParseACK(202, append([]byte(" "), padded...), "evt:local-unit", now)
	mockUnitCode(t, err, "invalid_request")
}

func TestMockInputJSONScannerDepthAndUnicode(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		good bool
	}{
		{`{"value":"\ud83d\ude00"}`, true},
		{`{"value":"a\\uD800"}`, true},
		{`{"value":"\ud800x"}`, false},
		{`{"value":"\ud800\ud800"}`, false},
		{`{"value":"\u12xz"}`, false},
		{`{"value":[[[[[[[[[0]]]]]]]]]}`, false},
	} {
		_, err := mockInputObject([]byte(tc.raw), 8192, []string{"value"}, nil, nil)
		if (err == nil) != tc.good {
			t.Fatalf("strict Unicode/depth boundary accepted=%v, want %v", err == nil, tc.good)
		}
	}
}
