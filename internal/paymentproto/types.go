// Package paymentproto defines the local payment sandbox's strict wire format.
// It has no database, transport, application identity or financial side effects.
package paymentproto

import "time"

type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }
func fail(code string) error   { return &Error{Code: code} }

type Environment struct{ ID, MerchantID, PayeeLabel string }
type SignedHeaders struct{ KeyID, DeliveryID, DeliveryTime, Signature string }
type KeySpec struct {
	ID                  string
	Key                 []byte
	NotBefore, NotAfter time.Time
}

type CheckoutRequest struct {
	SchemaVersion   int    `json:"schema_version"`
	EnvironmentID   string `json:"environment_id"`
	MerchantID      string `json:"merchant_id"`
	CommandID       string `json:"command_id"`
	MerchantOrderID string `json:"merchant_order_id"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	CreatedAt       string `json:"created_at"`
	ExpiresAt       string `json:"expires_at"`
}
type CancelRequest struct {
	SchemaVersion   int    `json:"schema_version"`
	EnvironmentID   string `json:"environment_id"`
	MerchantID      string `json:"merchant_id"`
	CommandID       string `json:"command_id"`
	MerchantOrderID string `json:"merchant_order_id"`
}
type QueryRequest struct {
	SchemaVersion   int    `json:"schema_version"`
	EnvironmentID   string `json:"environment_id"`
	MerchantID      string `json:"merchant_id"`
	QueryID         string `json:"query_id"`
	CommandID       string `json:"command_id"`
	MerchantOrderID string `json:"merchant_order_id"`
	CommandKind     string `json:"command_kind"`
	ParameterDigest string `json:"parameter_digest"`
}
type CommandReply struct {
	SchemaVersion         int            `json:"schema_version"`
	EnvironmentID         string         `json:"environment_id"`
	MerchantID            string         `json:"merchant_id"`
	MerchantOrderID       string         `json:"merchant_order_id"`
	CommandID             string         `json:"command_id"`
	CommandKind           string         `json:"command_kind"`
	ParameterDigest       string         `json:"parameter_digest"`
	CommandState          string         `json:"command_state"`
	ProviderOrderRevision *string        `json:"provider_order_revision"`
	Result                *CommandResult `json:"result"`
}
type QueryReply struct {
	SchemaVersion         int            `json:"schema_version"`
	QueryID               string         `json:"query_id"`
	EnvironmentID         string         `json:"environment_id"`
	MerchantID            string         `json:"merchant_id"`
	MerchantOrderID       string         `json:"merchant_order_id"`
	CommandID             string         `json:"command_id"`
	CommandKind           string         `json:"command_kind"`
	ParameterDigest       string         `json:"parameter_digest"`
	CommandState          string         `json:"command_state"`
	ProviderOrderRevision *string        `json:"provider_order_revision"`
	Result                *CommandResult `json:"result"`
}
type CommandResult struct {
	Status      string       `json:"status"`
	CheckoutRef *string      `json:"checkout_ref"`
	FailureCode *string      `json:"failure_code"`
	Capture     *CaptureFact `json:"capture"`
	ClosedAt    *string      `json:"closed_at"`
}
type CaptureFact struct {
	ProviderTransactionID string `json:"provider_transaction_id"`
	Amount                string `json:"amount"`
	Currency              string `json:"currency"`
	OccurredAt            string `json:"occurred_at"`
	ProviderOrderRevision string `json:"provider_order_revision"`
}

// Notification is a validated union, not a JSON DTO. Use CanonicalNotification
// so fields forbidden for its EventType cannot leak through a generic encoder.
type Notification struct {
	SchemaVersion                                          int
	EventID, EventType, EnvironmentID, MerchantID, OrderID string
	ProviderTransactionID                                  *string
	Amount, Currency, OccurredAt, ProviderOrderRevision    string
	FailureCode, RefundID, CaseID                          *string
}

// Verified values retain private canonical bytes, never caller-owned pointers.
// Only successful signature verification can construct a nonzero instance.
type VerifiedNotification struct {
	canonical []byte
	keyID     string
}
type VerifiedQuery struct {
	canonical []byte
	keyID     string
}

func (v VerifiedNotification) Canonical() ([]byte, error) {
	if len(v.canonical) == 0 || v.keyID == "" {
		return nil, fail("invalid_request")
	}
	return append([]byte(nil), v.canonical...), nil
}
func (v VerifiedQuery) Canonical() ([]byte, error) {
	if len(v.canonical) == 0 || v.keyID == "" {
		return nil, fail("invalid_request")
	}
	return append([]byte(nil), v.canonical...), nil
}
