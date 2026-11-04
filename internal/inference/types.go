// Package inference validates immutable plans, quotes and usage observations.
// It grants no provider, tenant, financial or actor authority.
package inference

import (
	"errors"
	"time"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

type Protocol string

const (
	ProtocolChat       Protocol = "openai_chat"
	ProtocolResponses  Protocol = "openai_responses"
	ProtocolMessages   Protocol = "anthropic_messages"
	ProtocolEmbeddings Protocol = "openai_embeddings"
	ProtocolRerank     Protocol = "jina_rerank"
	ProtocolGemini     Protocol = "gemini_generate_content"
)

type PlanScope string
type ResultKind string
type QuoteStatus string
type UsageState string

const (
	ScopeRequest     PlanScope   = "request"
	ScopeAttempt     PlanScope   = "attempt"
	ResultJSON       ResultKind  = "json"
	ResultEvents     ResultKind  = "events"
	QuoteKnown       QuoteStatus = "known"
	QuoteUnknown     QuoteStatus = "unknown"
	UsageConfirmed   UsageState  = "confirmed_final"
	UsageProvisional UsageState  = "provisional"
	UsageMissing     UsageState  = "missing"
	UsageConflicting UsageState  = "conflicting"
)

var (
	ErrTooLarge           = errors.New("inference_too_large")
	ErrInvalidJSON        = errors.New("inference_invalid_json")
	ErrInvalidValue       = errors.New("inference_invalid_value")
	ErrInvalidProfile     = errors.New("inference_invalid_profile")
	ErrInvalidPlan        = errors.New("inference_invalid_plan")
	ErrBindingMismatch    = errors.New("inference_binding_mismatch")
	ErrInvalidQuote       = errors.New("inference_invalid_quote")
	ErrUnknownQuote       = errors.New("inference_unknown_quote")
	ErrInvalidObservation = errors.New("inference_invalid_observation")
	ErrIncomplete         = errors.New("inference_incomplete")
	ErrConflicting        = errors.New("inference_conflicting")
)

// Inputs are copied and validated before any opaque value is returned.
type TextProfileInput struct {
	Version, Model, VariantKey string
	Protocol                   Protocol
	Schema                     metering.Schema
	ContractDigest             [32]byte
	InputTokenUpperBound       int64
	OutputLimit                int64
}

type ProfileInfo struct {
	TextProfileInput
	ResultKinds []ResultKind
}

type TextPlanInput struct {
	Candidates  []Profile
	InputHMAC   [32]byte
	OutputLimit int64
	Stream      bool
}

type ObservationBinding struct {
	TenantID, RequestID, AttemptID string
	ProfileVersion, VariantKey     string
	Schema                         metering.Schema
	PlanDigest                     [32]byte
}

type ObservationMetadata struct {
	Source, SourceEventID, ProviderReference           string
	ObservedAt                                         time.Time
	ProtocolOutcome, TransportEndReason, DeliveryStage string
	ContentWritten                                     bool
	FinishReason, UnexpectedChargeReason               string
}

type DimensionUsage struct {
	Dimension, Unit string
	Status          UsageState
	Quantity        *int64
}

type Profile struct {
	valid     bool
	info      TextProfileInput
	canonical []byte
	digest    [32]byte
}

type BoundPlan struct {
	valid            bool
	scope            PlanScope
	profiles         []Profile
	selected         Profile
	schema           metering.Schema
	model            string
	protocol         Protocol
	variant          string
	inputHMAC        [32]byte
	output           int64
	stream           bool
	parent           [32]byte
	retail, supplier metering.Bounds
	canonical        []byte
	digest           [32]byte
}

type QuoteSnapshot struct {
	valid             bool
	status            QuoteStatus
	side              metering.Side
	versionID, reason string
	bounds            metering.Bounds
	rates             metering.Rates
	amount            int64
	canonical         []byte
	digest            [32]byte
}

type Observation struct {
	valid     bool
	binding   ObservationBinding
	metadata  ObservationMetadata
	entries   []DimensionUsage
	canonical []byte
	digest    [32]byte
}

type FinalQuantities struct {
	valid      bool
	quantities metering.Quantities
	canonical  []byte
	digest     [32]byte
}
