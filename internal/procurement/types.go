// Package procurement manages platform procurement identities and metadata.
// Customer billing and attempt budget mutations remain in package billing.
package procurement

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalid = errors.New("invalid_procurement_configuration")
var ErrConflict = errors.New("procurement_operation_conflict")
var ErrForbidden = errors.New("procurement_forbidden")

type Store struct {
	Pool        *pgxpool.Pool
	prospective *prospectiveAccount
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

type Account struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Provider    string    `json:"provider"`
	State       string    `json:"state"`
	LimitAmount string    `json:"limit_amount"`
	HeldAmount  string    `json:"held_amount"`
	SpentAmount string    `json:"spent_amount"`
	Revision    string    `json:"revision"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateInput struct {
	OperationID string `json:"operation_id"`
	Label       string `json:"label"`
	Provider    string `json:"provider"`
	State       string `json:"state"`
	LimitAmount string `json:"limit_amount"`
}

type ConfigurationInput struct {
	OperationID      string `json:"operation_id"`
	ExpectedRevision string `json:"expected_revision"`
	Label            string `json:"label"`
	Provider         string `json:"provider"`
	State            string `json:"state"`
	LimitAmount      string `json:"limit_amount"`
}

type BindingInput struct {
	OperationID             string `json:"operation_id"`
	ChannelID               string `json:"channel_id"`
	CredentialID            string `json:"credential_id"`
	AccountID               string `json:"account_id"`
	ExpectedChannelRevision string `json:"expected_channel_revision"`
}

type Binding struct {
	ID              string    `json:"id"`
	CredentialID    string    `json:"credential_id"`
	ChannelID       string    `json:"channel_id"`
	AccountID       string    `json:"account_id"`
	OperationID     string    `json:"operation_id"`
	BoundAt         time.Time `json:"bound_at"`
	ChannelRevision string    `json:"channel_revision"`
}

type ObservationInput struct {
	OperationID  string     `json:"operation_id"`
	Status       string     `json:"status"`
	Amount       *string    `json:"amount"`
	Currency     string     `json:"currency"`
	Source       string     `json:"source"`
	Semantic     string     `json:"semantic"`
	ObservedAt   time.Time  `json:"observed_at"`
	ProviderAsOf *time.Time `json:"provider_as_of"`
	EvidenceRef  string     `json:"evidence_ref"`
}

var observationTimePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](?:\.[0-9]{1,9})?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

func (in *ObservationInput) UnmarshalJSON(data []byte) error {
	type plain ObservationInput
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	for _, name := range []string{"observed_at", "provider_as_of"} {
		if raw, exists := fields[name]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var value string
			if json.Unmarshal(raw, &value) != nil || !observationTimePattern.MatchString(value) {
				return ErrInvalid
			}
		}
	}
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	*in = ObservationInput(value)
	return nil
}

type Observation struct {
	ID           string     `json:"id"`
	AccountID    string     `json:"account_id"`
	OperationID  string     `json:"operation_id"`
	Actor        string     `json:"actor"`
	Status       string     `json:"status"`
	Amount       *string    `json:"amount"`
	Currency     string     `json:"currency"`
	Source       string     `json:"source"`
	Semantic     string     `json:"semantic"`
	ObservedAt   time.Time  `json:"observed_at"`
	ProviderAsOf *time.Time `json:"provider_as_of"`
	EvidenceRef  string     `json:"evidence_ref"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Applied and rejected commands are both immutable HTTP-success receipts.
// Before/After contain only typed safe metadata snapshots, never credentials.
type Command struct {
	OperationID string          `json:"operation_id"`
	Actor       string          `json:"actor"`
	Kind        string          `json:"kind"`
	TargetID    string          `json:"target_id"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
	CreatedAt   time.Time       `json:"created_at"`
}
