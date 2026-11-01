// Package paymentrefundproto implements the local sandbox's v2 refund wire
// protocol. Authentication here never grants financial or network authority.
package paymentrefundproto

import (
	"errors"
	"time"
)

var (
	ErrInvalid   = errors.New("payment_refund_protocol_invalid")
	ErrKey       = errors.New("payment_refund_key_unavailable")
	ErrSignature = errors.New("payment_refund_signature_invalid")
	ErrBinding   = errors.New("payment_refund_binding_invalid")
)

type Environment struct{ ID, MerchantID string }
type SignedHeaders struct{ KeyID, DeliveryID, DeliveryTime, Signature string }
type KeySpec struct {
	ID                  string
	Key                 []byte
	NotBefore, NotAfter time.Time
}

type RefundRequest struct {
	SchemaVersion         int    `json:"schema_version"`
	EnvironmentID         string `json:"environment_id"`
	MerchantID            string `json:"merchant_id"`
	CommandKind           string `json:"command_kind"`
	CommandID             string `json:"command_id"`
	LocalRefundID         string `json:"local_refund_id"`
	MerchantOrderID       string `json:"merchant_order_id"`
	ProviderTransactionID string `json:"provider_transaction_id"`
	OriginalCaptureAmount string `json:"original_capture_amount"`
	CaptureDigest         string `json:"capture_digest"`
	Amount                string `json:"amount"`
	Currency              string `json:"currency"`
}

type RefundFact struct {
	SchemaVersion           int     `json:"schema_version"`
	FactID                  string  `json:"fact_id"`
	EnvironmentID           string  `json:"environment_id"`
	MerchantID              string  `json:"merchant_id"`
	LocalRefundID           string  `json:"local_refund_id"`
	CommandID               string  `json:"command_id"`
	ParameterDigest         string  `json:"parameter_digest"`
	MerchantOrderID         string  `json:"merchant_order_id"`
	ProviderTransactionID   string  `json:"provider_transaction_id"`
	OriginalCaptureAmount   string  `json:"original_capture_amount"`
	CaptureDigest           string  `json:"capture_digest"`
	Amount                  string  `json:"amount"`
	Currency                string  `json:"currency"`
	Outcome                 string  `json:"outcome"`
	ProviderRefundReference *string `json:"provider_refund_reference"`
	FailureCode             *string `json:"failure_code"`
	CaptureRefundRevision   string  `json:"capture_refund_revision"`
	CompletedAt             string  `json:"completed_at"`
}

type Notification struct {
	SchemaVersion int        `json:"schema_version"`
	EventID       string     `json:"event_id"`
	EventType     string     `json:"event_type"`
	Fact          RefundFact `json:"fact"`
}

type CommandReply struct {
	SchemaVersion   int           `json:"schema_version"`
	Command         RefundRequest `json:"command"`
	ParameterDigest string        `json:"parameter_digest"`
	CommandState    string        `json:"command_state"`
	Result          *RefundFact   `json:"result"`
}

type QueryRequest struct {
	SchemaVersion      int           `json:"schema_version"`
	QueryID            string        `json:"query_id"`
	MutationGeneration string        `json:"mutation_generation"`
	Command            RefundRequest `json:"command"`
	ParameterDigest    string        `json:"parameter_digest"`
}

type QueryReply struct {
	SchemaVersion      int           `json:"schema_version"`
	QueryID            string        `json:"query_id"`
	MutationGeneration string        `json:"mutation_generation"`
	Command            RefundRequest `json:"command"`
	ParameterDigest    string        `json:"parameter_digest"`
	CommandState       string        `json:"command_state"`
	Result             *RefundFact   `json:"result"`
}

// Opaque values retain only private, immutable canonical bytes. None can be
// restored from a DTO, caller digest, or purported verification flag.
type CaptureBinding struct {
	value     captureValue
	canonical []byte
}
type VerifiedCommandReply struct{ canonical, fact []byte }
type VerifiedQueryReply struct{ canonical, fact []byte }
type VerifiedNotification struct{ canonical, fact []byte }
type VerifiedRefundFact struct{ canonical []byte }

func copyCanonical(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, ErrInvalid
	}
	return append([]byte(nil), b...), nil
}
func (v VerifiedCommandReply) Canonical() ([]byte, error) { return copyCanonical(v.canonical) }
func (v VerifiedQueryReply) Canonical() ([]byte, error)   { return copyCanonical(v.canonical) }
func (v VerifiedNotification) Canonical() ([]byte, error) { return copyCanonical(v.canonical) }
func (v VerifiedRefundFact) Canonical() ([]byte, error)   { return copyCanonical(v.canonical) }

func verifiedFact(canonical, fact []byte) (VerifiedRefundFact, error) {
	if len(canonical) == 0 || len(fact) == 0 {
		return VerifiedRefundFact{}, ErrInvalid
	}
	return VerifiedRefundFact{canonical: append([]byte(nil), fact...)}, nil
}
func (v VerifiedCommandReply) Fact() (VerifiedRefundFact, error) {
	return verifiedFact(v.canonical, v.fact)
}
func (v VerifiedQueryReply) Fact() (VerifiedRefundFact, error) {
	return verifiedFact(v.canonical, v.fact)
}
func (v VerifiedNotification) Fact() (VerifiedRefundFact, error) {
	return verifiedFact(v.canonical, v.fact)
}
func (v VerifiedRefundFact) Digest() (string, error) {
	if len(v.canonical) == 0 {
		return "", ErrInvalid
	}
	return digest(factDomain, v.canonical), nil
}
