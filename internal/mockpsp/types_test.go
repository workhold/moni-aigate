package mockpsp

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func TestCanonicalTypesRejectZeroAndDigestCorruption(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value mockUnitBytes
	}{
		{"checkout", CanonicalCheckout{}},
		{"cancel", CanonicalCancel{}},
		{"query", CanonicalQuery{}},
		{"control", CanonicalControl{}},
		{"command_reply", CanonicalCommandReply{}},
		{"query_reply", CanonicalQueryReply{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.value.Bytes()
			mockUnitCode(t, err, "invalid_request")
			if b != nil {
				t.Fatal("zero canonical value produced bytes")
			}
		})
	}
	for _, tc := range []struct {
		raw    []byte
		digest string
	}{
		{nil, paymentproto.RequestDigest(nil)},
		{[]byte(`{}`), ""},
		{[]byte(`{}`), strings.Repeat("0", 64)},
		{bytes.Repeat([]byte("x"), (64<<10)+1), strings.Repeat("0", 64)},
	} {
		b, err := mockTypeCopyRaw(tc.raw, tc.digest)
		mockUnitCode(t, err, "invalid_request")
		if b != nil {
			t.Fatal("corrupt canonical value produced bytes")
		}
	}
}

func TestObservationConstructionOwnedData(t *testing.T) {
	now := time.Date(2026, 11, 1, 1, 2, 3, 456789000, time.UTC)
	ack, err := ParseACK(202, mockUnitACK(t, now.Format(mockInputTimeLayout)), "evt:local-unit", now)
	mockUnitCode(t, err, "")
	observation, err := ACKObservation(ack)
	mockUnitCode(t, err, "")
	if observation.outcome != "ack" || observation.httpStatus == nil || *observation.httpStatus != 202 || observation.errorCode != "" || observation.eventID != "evt:local-unit" || !bytes.Equal(observation.receiptData, ack.data) {
		t.Fatal("ACK observation lost validated fields")
	}
	ack.data[0] = '!'
	if observation.receiptData[0] != '{' {
		t.Fatal("ACK observation retained mutable ACK storage")
	}
	_, err = ACKObservation(SafeACK{})
	mockUnitCode(t, err, "invalid_request")
	for _, status := range []int{0, 201, 204} {
		_, err := (SafeACK{status: status, data: []byte(`{}`), eventID: "evt:local-unit"}).DataBytes()
		mockUnitCode(t, err, "invalid_request")
	}
	for _, ack := range []SafeACK{
		{status: 200, eventID: "evt:local-unit"},
		{status: 202, data: bytes.Repeat([]byte("x"), 8193), eventID: "evt:local-unit"},
		{status: 202, data: []byte(`{}`), eventID: "https://invalid"},
	} {
		_, err := ack.DataBytes()
		mockUnitCode(t, err, "invalid_request")
	}
}

func TestFailedObservationAllowlistAndStatus(t *testing.T) {
	for _, code := range []FailureCode{FailureNetwork, FailureTimeout, FailureAuth, FailureProtocol, FailureEnvironment, FailureTemporary, FailurePermanent} {
		for _, status := range []*int{nil, mockUnitInt(100), mockUnitInt(200), mockUnitInt(599)} {
			observation, err := FailedObservation(code, status)
			mockUnitCode(t, err, "")
			if observation.outcome != "failed" || observation.errorCode != code || observation.eventID != "" || observation.receiptData != nil {
				t.Fatal("failed observation did not preserve its safe union")
			}
			if status == nil {
				if observation.httpStatus != nil {
					t.Fatal("nil network status became a fabricated HTTP response")
				}
			} else {
				want := *status
				*status = 600
				if observation.httpStatus == nil || *observation.httpStatus != want {
					t.Fatal("failed observation retained a caller-owned status pointer")
				}
			}
		}
	}
	for _, code := range []FailureCode{"", "url:http://secret", "retry", "mock_disconnect"} {
		_, err := FailedObservation(code, nil)
		mockUnitCode(t, err, "invalid_request")
	}
	for _, status := range []int{-1, 0, 99, 600, 1000} {
		_, err := FailedObservation(FailureProtocol, &status)
		mockUnitCode(t, err, "invalid_request")
	}
}

func mockUnitInt(v int) *int { return &v }

func mockUnitPrepared() PreparedDelivery {
	return PreparedDelivery{body: []byte(`{"fixture":"local"}`), eventID: "evt:local-unit",
		headers: paymentproto.SignedHeaders{KeyID: "key_v1", DeliveryID: mockUnitRequestID,
			DeliveryTime: "1793494923", Signature: strings.Repeat("a", 64)}}
}

func TestPreparedDeliveryOpaqueOwnedAccess(t *testing.T) {
	value := mockUnitPrepared()
	body, err := value.BodyBytes()
	mockUnitCode(t, err, "")
	headers, err := value.Headers()
	mockUnitCode(t, err, "")
	eventID, err := value.EventID()
	mockUnitCode(t, err, "")
	if !bytes.Equal(body, value.body) || headers != value.headers || eventID != value.eventID {
		t.Fatal("prepared delivery lost frozen fields")
	}
	body[0] = '!'
	headers.KeyID = "changed"
	again, err := value.BodyBytes()
	mockUnitCode(t, err, "")
	if again[0] != '{' || value.headers.KeyID != "key_v1" {
		t.Fatal("prepared accessors exposed mutable storage")
	}
	for _, mutate := range []func(*PreparedDelivery){
		func(v *PreparedDelivery) { *v = PreparedDelivery{} },
		func(v *PreparedDelivery) { v.body = nil },
		func(v *PreparedDelivery) { v.body = bytes.Repeat([]byte("x"), (64<<10)+1) },
		func(v *PreparedDelivery) { v.eventID = "bearer-secret" },
		func(v *PreparedDelivery) { v.headers.KeyID = "bad key" },
		func(v *PreparedDelivery) { v.headers.DeliveryID = "00000000-0000-0000-0000-000000000000" },
		func(v *PreparedDelivery) { v.headers.DeliveryTime = "01" },
		func(v *PreparedDelivery) { v.headers.DeliveryTime = "-1" },
		func(v *PreparedDelivery) { v.headers.DeliveryTime = "253402300800" },
		func(v *PreparedDelivery) { v.headers.Signature = strings.Repeat("A", 64) },
		func(v *PreparedDelivery) { v.headers.Signature = strings.Repeat("a", 63) },
	} {
		value := mockUnitPrepared()
		mutate(&value)
		body, err := value.BodyBytes()
		mockUnitCode(t, err, "invalid_request")
		if body != nil {
			t.Fatal("invalid prepared delivery produced a body")
		}
		headers, err := value.Headers()
		mockUnitCode(t, err, "invalid_request")
		if headers != (paymentproto.SignedHeaders{}) {
			t.Fatal("invalid prepared delivery produced headers")
		}
		eventID, err := value.EventID()
		mockUnitCode(t, err, "invalid_request")
		if eventID != "" {
			t.Fatal("invalid prepared delivery produced an event ID")
		}
	}
}

func TestMockErrorDoesNotLeakSource(t *testing.T) {
	if ErrorCode(nil) != "" {
		t.Fatal("nil error is not success")
	}
	for _, err := range []error{
		errors.New("sensitive body, URL and database details"),
		&Error{Code: "sensitive unknown code"},
		&paymentproto.Error{Code: "sensitive unknown protocol code"},
		(*paymentproto.Error)(nil),
		(*Error)(nil),
		fail("sensitive unknown code"),
	} {
		mockUnitCode(t, err, "mock_unavailable")
	}
	for _, code := range []string{"invalid_request", "payment_environment_invalid", "mock_unavailable", "mock_outcome_unknown", "mock_instance_unavailable", "command_parameter_conflict", "query_parameter_conflict", "mock_control_conflict"} {
		err := fail(code)
		if err.Error() != code || ErrorCode(err) != code || ErrorCode(fmt.Errorf("hidden: %w", err)) != code {
			t.Fatal("allowlisted error code was changed")
		}
		if _, ok := err.(interface{ Unwrap() error }); ok {
			t.Fatal("safe error exposes an original cause")
		}
	}
	for _, code := range []string{"invalid_request", "payment_environment_invalid", "payment_signature_invalid", "payment_key_unavailable"} {
		err := &paymentproto.Error{Code: code}
		mockUnitCode(t, err, code)
		mockUnitCode(t, fmt.Errorf("hidden protocol context: %w", err), code)
	}
	if !errors.Is(ErrDisconnect, ErrDisconnect) || !errors.Is(ErrDeliveryDeferred, ErrDeliveryDeferred) || errors.Is(fail("mock_disconnect"), ErrDisconnect) {
		t.Fatal("internal control sentinels lost their identity")
	}
	if ErrorCode(ErrDisconnect) != "mock_disconnect" || ErrorCode(ErrDeliveryDeferred) != "mock_delivery_deferred" {
		t.Fatal("internal sentinel codes changed")
	}
	if CurrentRecorded == 0 || StaleRecorded == 0 || AlreadyRecorded == 0 || CurrentRecorded == StaleRecorded || CurrentRecorded == AlreadyRecorded || StaleRecorded == AlreadyRecorded {
		t.Fatal("record outcome zero or aliases can represent success")
	}
}

func TestMockInputValueBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(string) bool
		good []string
		bad  []string
	}{
		{"uuid", mockInputUUID, []string{mockUnitCommandID, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, []string{"", "00000000-0000-0000-0000-000000000000", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "44444444444444448444444444444444"}},
		{"ref", mockInputReference, []string{"evt:local-unit", "事件_1", strings.Repeat("x", 100)}, []string{"", "SK-secret", "Mg_secret", "BearerSecret", "https://host", "a b", "bad\x00ref", strings.Repeat("x", 101), string([]byte{0xff})}},
		{"key", mockInputKeyID, []string{"A-key_1", strings.Repeat("x", 32)}, []string{"", strings.Repeat("x", 33), "a/b", "密钥"}},
		{"time", mockInputDeliveryTime, []string{"0", "1", "253402300799"}, []string{"", "+1", "-1", "01", "1.0", " 1", "253402300800", "9999999999999"}},
		{"signature", mockInputSignature, []string{strings.Repeat("a", 64), strings.Repeat("0", 64)}, []string{"", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, value := range tc.good {
				if !tc.fn(value) {
					t.Fatal("valid boundary rejected")
				}
			}
			for _, value := range tc.bad {
				if tc.fn(value) {
					t.Fatal("invalid boundary accepted")
				}
			}
		})
	}
}
