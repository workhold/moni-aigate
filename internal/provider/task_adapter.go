package provider

import (
	"context"
	"time"
)

// TaskAdapter is the only provider boundary for an asynchronous task. An
// implementation must make Submit idempotent at the provider contract level,
// but a caller must still treat every returned error as unknown acceptance.
// The gateway never retries Submit after an unknown result.
type TaskAdapter interface {
	Submit(context.Context, TaskSubmission) (TaskSubmissionResult, error)
	Query(context.Context, TaskQuery) (TaskObservation, error)
	Cancel(context.Context, TaskCancel) (TaskObservation, error)
	Download(context.Context, TaskDownload) (TaskDownloadResult, error)
}

type TaskSubmission struct {
	TaskID         string
	RequestID      string
	ReservationID  string
	AttemptID      string
	SourceID       string
	IdempotencyKey string
	RequestDigest  string
	SubmittedAt    time.Time
}

type TaskSubmissionResult struct {
	Mode              ProviderMode
	EventID           string
	TaskID            string
	ProviderReference string
	Sequence          uint64
	ObservedAt        time.Time
}

type TaskQuery struct {
	TaskID            string
	ProviderReference string
	RequestDigest     string
}

type TaskCancel struct {
	TaskID            string
	ProviderReference string
}

// TaskObservation is deliberately transport-neutral. The tasks service
// validates every field before it becomes a domain Event.
type TaskObservation struct {
	Mode              ProviderMode
	EventID           string
	TaskID            string
	Kind              string
	Sequence          uint64
	ObservedAt        time.Time
	ProviderReference string
	FailureCode       string
	Artifact          *TaskArtifact
}

type TaskArtifact struct {
	Reference string
	MediaType string
	Size      int64
	SHA256    string
	ExpiresAt time.Time
}

type TaskDownload struct {
	TaskID            string
	ProviderReference string
	ArtifactReference string
	ArtifactSHA256    string
	MaxBytes          int64
}

type TaskDownloadResult struct {
	Mode      ProviderMode
	MediaType string
	Body      []byte
}
