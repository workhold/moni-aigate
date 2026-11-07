// Package media contains the provider-independent, unpublished synchronous
// media boundary. It validates a bounded inline image result; it does not
// authorize dispatch or establish a billing fact.
package media

import "errors"

type Publication string

const (
	PublicationUnpublished Publication = "unpublished"
	PublicationPublished   Publication = "published"
)

type Operation string

const OperationImageGeneration Operation = "image_generation"

type Format string

const FormatPNG Format = "png"

type ExecutionKind string

const ExecutionSynchronous ExecutionKind = "synchronous"

type ResultKind string

const ResultInline ResultKind = "inline"

type ExecutionState string

const (
	StatePrepared           ExecutionState = "prepared"
	StateSending            ExecutionState = "sending"
	StateResultReceived     ExecutionState = "result_received"
	StateCompleted          ExecutionState = "completed"
	StateFailed             ExecutionState = "failed"
	StateCancelled          ExecutionState = "cancelled"
	StateConsumptionUnknown ExecutionState = "consumption_unknown"
)

type ConsumptionStatus string

const (
	ConsumptionNoDispatch ConsumptionStatus = "no_dispatch"
	ConsumptionUnknown    ConsumptionStatus = "unknown"
	ConsumptionConfirmed  ConsumptionStatus = "confirmed"
)

type FailureKind string

const (
	FailureInputRejected      FailureKind = "input_rejected"
	FailureResourceLimit      FailureKind = "resource_limit"
	FailureProfileUnpublished FailureKind = "profile_unpublished"
	FailureInvalidResult      FailureKind = "invalid_result"
	FailureTransport          FailureKind = "transport"
	FailureUsageMissing       FailureKind = "usage_missing"
	FailureCancelled          FailureKind = "cancelled"
)

const (
	MaxInputJSONBytes  = 64 * 1024
	MaxPromptBytes     = 4 * 1024
	MaxResultJSONBytes = 32 * 1024 * 1024
	MaxDecodedBytes    = 16 * 1024 * 1024
)

var (
	ErrTooLarge            = errors.New("media_too_large")
	ErrInvalidJSON         = errors.New("media_invalid_json")
	ErrInvalidProfile      = errors.New("media_invalid_profile")
	ErrInvalidInput        = errors.New("media_invalid_input")
	ErrInvalidResult       = errors.New("media_invalid_result")
	ErrBindingMismatch     = errors.New("media_binding_mismatch")
	ErrInvalidValue        = errors.New("media_invalid_value")
	ErrProfileNotPublished = errors.New("media_profile_not_published")
	ErrInvalidState        = errors.New("media_invalid_state")
	ErrInvalidUsage        = errors.New("media_invalid_usage")
	ErrUsageUnconfirmed    = errors.New("media_usage_unconfirmed")
	ErrConsumptionUnknown  = errors.New("media_consumption_unknown")
)

// ImageSize is a finite profile parameter, not a billing quantity.
type ImageSize struct {
	Width  int
	Height int
}

type ProfileInput struct {
	Version     string
	VariantKey  string
	Publication Publication
}

type Profile struct {
	valid       bool
	version     string
	variant     string
	publication Publication
	canonical   []byte
	digest      [32]byte
}

type Input struct {
	valid         bool
	profileDigest [32]byte
	profile       string
	variant       string
	prompt        string
	size          ImageSize
	format        Format
	canonical     []byte
	digest        [32]byte
}

type InlineResult struct {
	valid         bool
	profileDigest [32]byte
	profile       string
	variant       string
	format        Format
	size          ImageSize
	data          []byte
	canonical     []byte
	digest        [32]byte
}

type UsageEvidenceInput struct {
	Source  string
	EventID string
}

type UsageEvidence struct {
	valid  bool
	source string
	event  string
}

type Lifecycle struct {
	valid       bool
	profile     Profile
	input       Input
	state       ExecutionState
	consumption ConsumptionStatus
	failure     FailureKind
	result      InlineResult
	usage       UsageEvidence
}
