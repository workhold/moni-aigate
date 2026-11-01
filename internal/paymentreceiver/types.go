// Package paymentreceiver is the isolated payment sandbox's durable receiver.
// It verifies exact wire bytes and acknowledges committed receipts; it has no
// application funding, ledger or model generation dependency.
package paymentreceiver

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type Error struct{ code string }

func (e *Error) Error() string  { return e.code }
func failure(code string) error { return &Error{code: code} }
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.code
	}
	var p *paymentproto.Error
	if errors.As(err, &p) && (p.Code == "invalid_request" || p.Code == "payment_environment_invalid" || p.Code == "payment_signature_invalid") {
		return p.Code
	}
	return "receiver_unavailable"
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
type ReceiveStatus uint8

const (
	ReceiveNew ReceiveStatus = iota + 1
	ReceiveDuplicate
	ReceiveConflict
)

type ReceiveOutcome struct {
	Receipt ReceiptData
	Status  ReceiveStatus
}

const utc6 = "2006-01-02T15:04:05.000000Z"

func validUUID(v string) bool {
	id, e := uuid.Parse(v)
	return e == nil && id != uuid.Nil && id.String() == v
}
func validTime(v string) bool {
	t, e := time.Parse(utc6, v)
	return e == nil && t.Year() >= 1 && t.Year() <= 9999 && t.UTC().Format(utc6) == v
}

// Stored receipts were generated canonically by this component. An exact byte
// roundtrip also rejects duplicate/unknown/aliased keys, nulls and broken escapes.
func parseReceipt(raw []byte, eventID string) (ReceiptData, error) {
	var d ReceiptData
	if len(raw) == 0 || len(raw) > 8192 || json.Unmarshal(raw, &d) != nil || !validUUID(d.ReceiptID) || d.EventID != eventID || d.Status != "received" || d.Environment != "sandbox" || !validTime(d.ReceivedAt) {
		return ReceiptData{}, failure("receiver_unavailable")
	}
	b, e := json.Marshal(d)
	if e != nil || !bytes.Equal(b, raw) {
		return ReceiptData{}, failure("receiver_unavailable")
	}
	return d, nil
}
func marshalReceipt(d ReceiptData) ([]byte, error) {
	b, e := json.Marshal(d)
	if e != nil {
		return nil, failure("receiver_unavailable")
	}
	if _, e = parseReceipt(b, d.EventID); e != nil {
		return nil, e
	}
	return b, nil
}
