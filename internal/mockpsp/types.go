// Package mockpsp implements the isolated payment sandbox's PSP component.
package mockpsp

import (
	"bytes"
	"errors"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

// Error exposes only an allowlisted code, never the original database, URL,
// transport, credential or body that led to the failure.
type Error struct{ Code string }

func (e *Error) Error() string {
	if e == nil || !mockTypeErrorCode(e.Code) {
		return "mock_unavailable"
	}
	return e.Code
}

func fail(code string) error {
	if !mockTypeErrorCode(code) {
		code = "mock_unavailable"
	}
	return &Error{Code: code}
}

// ErrorCode maps unknown internal errors to a fixed safe code. There is no
// Unwrap method on Error, so raw errors cannot accidentally reach HTTP output.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var safe *Error
	if errors.As(err, &safe) && safe != nil {
		return safe.Error()
	}
	var protocol *paymentproto.Error
	if errors.As(err, &protocol) && protocol != nil {
		switch protocol.Code {
		case "invalid_request", "payment_environment_invalid", "payment_signature_invalid", "payment_key_unavailable":
			return protocol.Code
		}
	}
	return "mock_unavailable"
}

func mockTypeErrorCode(code string) bool {
	switch code {
	case "invalid_request", "payment_environment_invalid", "payment_signature_invalid", "payment_key_unavailable",
		"mock_auth_invalid", "not_found", "method_not_allowed", "command_parameter_conflict", "query_parameter_conflict", "mock_control_conflict",
		"body_too_large", "unsupported_media_type", "rate_limited", "mock_unavailable", "mock_outcome_unknown",
		"mock_configuration_invalid", "mock_environment_invalid", "mock_state_missing", "mock_role_invalid", "mock_instance_unavailable",
		"mock_disconnect", "mock_delivery_deferred":
		return true
	}
	return false
}

// These are internal control outcomes. A disconnect is handled before any
// response bytes; a deferred delivery must never be converted into failed IO.
var (
	ErrDisconnect       error = &Error{Code: "mock_disconnect"}
	ErrDeliveryDeferred error = &Error{Code: "mock_delivery_deferred"}
)

type CanonicalCheckout struct {
	raw     []byte
	digest  string
	env     paymentproto.Environment
	request paymentproto.CheckoutRequest
}
type CanonicalCancel struct {
	raw     []byte
	digest  string
	env     paymentproto.Environment
	request paymentproto.CancelRequest
}
type CanonicalQuery struct {
	raw     []byte
	digest  string
	env     paymentproto.Environment
	request paymentproto.QueryRequest
}
type CanonicalControl struct {
	raw     []byte
	digest  string
	env     paymentproto.Environment
	request ControlInput
}

type CanonicalCommandReply struct {
	raw     []byte
	digest  string
	request paymentproto.CommandReply
}
type CanonicalQueryReply struct {
	raw     []byte
	digest  string
	request paymentproto.QueryReply
}

type CanonicalRefundReply struct {
	raw     []byte
	digest  string
	env     paymentrefundproto.Environment
	request paymentrefundproto.CommandReply
}

type CanonicalRefundQueryReply struct {
	raw     []byte
	digest  string
	env     paymentrefundproto.Environment
	request paymentrefundproto.QueryReply
}

func mockTypeCopyRaw(raw []byte, digest string) ([]byte, error) {
	if len(raw) == 0 || len(raw) > 64<<10 || digest == "" || paymentproto.RequestDigest(raw) != digest {
		return nil, fail("invalid_request")
	}
	return append([]byte(nil), raw...), nil
}

func (v CanonicalCheckout) Bytes() ([]byte, error) { return mockTypeCopyRaw(v.raw, v.digest) }
func (v CanonicalCancel) Bytes() ([]byte, error)   { return mockTypeCopyRaw(v.raw, v.digest) }
func (v CanonicalQuery) Bytes() ([]byte, error)    { return mockTypeCopyRaw(v.raw, v.digest) }
func (v CanonicalControl) Bytes() ([]byte, error)  { return mockTypeCopyRaw(v.raw, v.digest) }
func (v CanonicalCommandReply) Bytes() ([]byte, error) {
	return mockTypeCopyRaw(v.raw, v.digest)
}
func (v CanonicalQueryReply) Bytes() ([]byte, error) { return mockTypeCopyRaw(v.raw, v.digest) }
func (v CanonicalRefundReply) Bytes() ([]byte, error) {
	if len(v.raw) == 0 || len(v.raw) > 64<<10 || v.digest == "" {
		return nil, fail("invalid_request")
	}
	b, err := paymentrefundproto.CanonicalCommandReply(v.request)
	if err != nil || !bytes.Equal(b, v.raw) {
		return nil, fail("invalid_request")
	}
	return append([]byte(nil), v.raw...), nil
}
func (v CanonicalRefundQueryReply) Bytes() ([]byte, error) {
	if len(v.raw) == 0 || len(v.raw) > 64<<10 || v.digest == "" {
		return nil, fail("invalid_request")
	}
	b, err := paymentrefundproto.CanonicalQueryReply(v.request)
	if err != nil || !bytes.Equal(b, v.raw) {
		return nil, fail("invalid_request")
	}
	return append([]byte(nil), v.raw...), nil
}

type CommandOutcome struct {
	Body     CanonicalCommandReply
	Replayed bool
}
type QueryOutcome struct {
	Body     CanonicalQueryReply
	Replayed bool
}
type RefundOutcome struct {
	Body     CanonicalRefundReply
	Replayed bool
}
type RefundQueryOutcome struct {
	Body     CanonicalRefundQueryReply
	Replayed bool
}
type ControlOutcome struct {
	Reply    ControlReply
	Replayed bool
}
type SignedQueryOutcome struct {
	Body    CanonicalQueryReply
	Headers paymentproto.SignedHeaders
}

// ControlInput is the exact wire DTO. It never enters a repository without the
// private canonical wrapper produced by ParseControlInput.
type ControlInput struct {
	OperationID     string  `json:"operation_id"`
	Action          string  `json:"action"`
	Scenario        string  `json:"scenario"`
	MerchantOrderID *string `json:"merchant_order_id"`
	CommandID       *string `json:"command_id"`
	QueryID         *string `json:"query_id"`
	EventID         *string `json:"event_id"`
}
type ControlReply struct {
	OperationID string  `json:"operation_id"`
	Status      string  `json:"status"`
	Reason      string  `json:"reason"`
	ResourceID  *string `json:"resource_id"`
}

type ReceiptData struct {
	ReceiptID   string `json:"receipt_id"`
	EventID     string `json:"event_id"`
	Status      string `json:"status"`
	Environment string `json:"environment"`
	ReceivedAt  string `json:"received_at"`
}
type ReceiptEnvelope struct {
	Data      ReceiptData `json:"data"`
	RequestID string      `json:"request_id"`
}

type SafeACK struct {
	status  int
	data    []byte
	eventID string
}

func (v SafeACK) DataBytes() ([]byte, error) {
	if (v.status != 200 && v.status != 202) || len(v.data) == 0 || len(v.data) > 8<<10 || !mockInputReference(v.eventID) {
		return nil, fail("invalid_request")
	}
	return append([]byte(nil), v.data...), nil
}

// The identity is minted by the attested repository; fields cannot be supplied
// by callers or reconstructed from API body values.
type DeliveryClaim struct {
	repo                                                   *repositoryIdentity
	envID, eventRowID, eventID                             string
	generation                                             int64
	attemptID, deliveryID, deliveryTime, keyID, bodyDigest string
	claimedAt, leaseUntil                                  time.Time
}

type FailureCode string

const (
	FailureNetwork     FailureCode = "network"
	FailureTimeout     FailureCode = "timeout"
	FailureAuth        FailureCode = "auth"
	FailureProtocol    FailureCode = "protocol"
	FailureEnvironment FailureCode = "environment"
	FailureTemporary   FailureCode = "temporary"
	FailurePermanent   FailureCode = "permanent"
)

type Observation struct {
	outcome     string
	httpStatus  *int
	errorCode   FailureCode
	receiptData []byte
	eventID     string
}

func ACKObservation(v SafeACK) (Observation, error) {
	data, err := v.DataBytes()
	if err != nil {
		return Observation{}, err
	}
	status := v.status
	return Observation{outcome: "ack", httpStatus: &status, receiptData: data, eventID: v.eventID}, nil
}

func FailedObservation(code FailureCode, safeStatus *int) (Observation, error) {
	switch code {
	case FailureNetwork, FailureTimeout, FailureAuth, FailureProtocol, FailureEnvironment, FailureTemporary, FailurePermanent:
	default:
		return Observation{}, fail("invalid_request")
	}
	var ownedStatus *int
	if safeStatus != nil {
		if *safeStatus < 100 || *safeStatus > 599 {
			return Observation{}, fail("invalid_request")
		}
		status := *safeStatus
		ownedStatus = &status
	}
	return Observation{outcome: "failed", httpStatus: ownedStatus, errorCode: code}, nil
}

type RecordOutcome uint8

const (
	CurrentRecorded RecordOutcome = iota + 1
	StaleRecorded
	AlreadyRecorded
)

type PreparedDelivery struct {
	body    []byte
	headers paymentproto.SignedHeaders
	eventID string
}

func (v PreparedDelivery) BodyBytes() ([]byte, error) {
	if err := v.mockTypeValid(); err != nil {
		return nil, err
	}
	return append([]byte(nil), v.body...), nil
}
func (v PreparedDelivery) Headers() (paymentproto.SignedHeaders, error) {
	if err := v.mockTypeValid(); err != nil {
		return paymentproto.SignedHeaders{}, err
	}
	return v.headers, nil
}
func (v PreparedDelivery) EventID() (string, error) {
	if err := v.mockTypeValid(); err != nil {
		return "", err
	}
	return v.eventID, nil
}
func (v PreparedDelivery) mockTypeValid() error {
	if len(v.body) == 0 || len(v.body) > 64<<10 || !mockInputReference(v.eventID) || !mockInputUUID(v.headers.DeliveryID) || !mockInputKeyID(v.headers.KeyID) || !mockInputDeliveryTime(v.headers.DeliveryTime) || !mockInputSignature(v.headers.Signature) {
		return fail("invalid_request")
	}
	return nil
}
