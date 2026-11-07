package tasks

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/moni-ai/moni-aigate/internal/strictjson"
)

// EventKind is the closed set of provider observations understood by this
// first task slice. Commands such as cancel are represented by state methods.
type EventKind string

const (
	EventSubmissionAccepted EventKind = "submission_accepted"
	EventSubmissionUnknown  EventKind = "submission_unknown"
	EventStatusQueued       EventKind = "status_queued"
	EventStatusRunning      EventKind = "status_running"
	EventStatusSucceeded    EventKind = "status_succeeded"
	EventStatusFailed       EventKind = "status_failed"
	EventStatusCancelled    EventKind = "status_cancelled"
	EventCancelRejected     EventKind = "cancel_rejected"
	EventArtifactExpired    EventKind = "artifact_expired"
)

// EventInput is the trusted adapter's typed input. ParseEvent is the boundary
// for untrusted JSON; both paths converge on the same validation.
type EventInput struct {
	Version           string
	EventID           ID
	TaskID            ID
	Kind              EventKind
	Sequence          uint64
	ObservedAt        time.Time
	ProviderReference string
	Artifact          *Artifact
	FailureCode       string
}

// Event is an immutable parsed observation.
type Event struct {
	version           string
	eventID           ID
	taskID            ID
	kind              EventKind
	sequence          uint64
	observedAt        time.Time
	providerReference string
	artifact          *Artifact
	failureCode       string
}

func NewEvent(input EventInput) (Event, error) {
	e := Event{
		version:           input.Version,
		eventID:           input.EventID,
		taskID:            input.TaskID,
		kind:              input.Kind,
		sequence:          input.Sequence,
		observedAt:        input.ObservedAt,
		providerReference: input.ProviderReference,
		failureCode:       input.FailureCode,
	}
	if input.Artifact != nil {
		copy := *input.Artifact
		e.artifact = &copy
	}
	if err := e.valid(); err != nil {
		return Event{}, err
	}
	return e, nil
}

func (e Event) valid() error {
	if e.version != "1" || validateID(e.eventID) != nil || validateID(e.taskID) != nil || e.sequence == 0 || !validUTC(e.observedAt) || !validEventKind(e.kind) {
		return ErrInvalidEvent
	}
	if e.providerReference != "" && !validSafeReference(e.providerReference, maxReferenceLength) {
		return ErrInvalidEvent
	}
	if e.failureCode != "" && !validFailureCode(e.failureCode) {
		return ErrInvalidEvent
	}
	if e.artifact != nil {
		if err := e.artifact.valid(); err != nil {
			return ErrInvalidEvent
		}
		if !e.observedAt.Before(e.artifact.ExpiresAt) {
			return ErrInvalidEvent
		}
	}
	switch e.kind {
	case EventSubmissionAccepted, EventStatusQueued, EventStatusRunning, EventStatusSucceeded, EventStatusFailed, EventStatusCancelled:
		if e.providerReference == "" {
			return ErrInvalidEvent
		}
	case EventSubmissionUnknown:
		if e.providerReference != "" {
			return ErrInvalidEvent
		}
	case EventCancelRejected:
		if e.providerReference == "" {
			return ErrInvalidEvent
		}
	case EventArtifactExpired:
		if e.providerReference != "" {
			return ErrInvalidEvent
		}
	}
	if e.kind == EventStatusSucceeded {
		if e.artifact == nil || e.failureCode != "" {
			return ErrInvalidEvent
		}
	} else if e.kind == EventStatusFailed {
		if e.artifact != nil || !validFailureCode(e.failureCode) {
			return ErrInvalidEvent
		}
	} else if e.artifact != nil || e.failureCode != "" {
		return ErrInvalidEvent
	}
	return nil
}

func validEventKind(kind EventKind) bool {
	switch kind {
	case EventSubmissionAccepted, EventSubmissionUnknown, EventStatusQueued, EventStatusRunning, EventStatusSucceeded, EventStatusFailed, EventStatusCancelled, EventCancelRejected, EventArtifactExpired:
		return true
	default:
		return false
	}
}

func (e Event) EventID() ID               { return e.eventID }
func (e Event) TaskID() ID                { return e.taskID }
func (e Event) Kind() EventKind           { return e.kind }
func (e Event) Sequence() uint64          { return e.sequence }
func (e Event) ObservedAt() time.Time     { return e.observedAt }
func (e Event) ProviderReference() string { return e.providerReference }
func (e Event) FailureCode() string       { return e.failureCode }
func (e Event) Artifact() (Artifact, bool) {
	if e.artifact == nil {
		return Artifact{}, false
	}
	return *e.artifact, true
}

type eventJSON struct {
	Version           string        `json:"version"`
	EventID           string        `json:"event_id"`
	TaskID            string        `json:"task_id"`
	Kind              EventKind     `json:"kind"`
	Sequence          string        `json:"sequence"`
	ObservedAt        string        `json:"observed_at"`
	ProviderReference *string       `json:"provider_reference"`
	Artifact          *artifactJSON `json:"artifact"`
	FailureCode       *string       `json:"failure_code"`
}

type artifactJSON struct {
	Reference string `json:"reference"`
	MediaType string `json:"media_type"`
	Size      string `json:"size"`
	SHA256    string `json:"sha256"`
	ExpiresAt string `json:"expires_at"`
}

func (e Event) CanonicalJSON() ([]byte, error) {
	if err := e.valid(); err != nil {
		return nil, err
	}
	var provider *string
	if e.providerReference != "" {
		v := e.providerReference
		provider = &v
	}
	var failure *string
	if e.failureCode != "" {
		v := e.failureCode
		failure = &v
	}
	var artifact *artifactJSON
	if e.artifact != nil {
		artifact = &artifactJSON{
			Reference: e.artifact.Reference,
			MediaType: e.artifact.MediaType,
			Size:      strconv.FormatInt(e.artifact.Size, 10),
			SHA256:    e.artifact.SHA256.String(),
			ExpiresAt: e.artifact.ExpiresAt.Format(time.RFC3339Nano),
		}
	}
	raw, err := json.Marshal(eventJSON{
		Version:           e.version,
		EventID:           e.eventID.String(),
		TaskID:            e.taskID.String(),
		Kind:              e.kind,
		Sequence:          strconv.FormatUint(e.sequence, 10),
		ObservedAt:        e.observedAt.Format(time.RFC3339Nano),
		ProviderReference: provider,
		Artifact:          artifact,
		FailureCode:       failure,
	})
	if err != nil {
		return nil, ErrInvalidEvent
	}
	return raw, nil
}

func (e Event) Digest() Digest {
	raw, err := e.CanonicalJSON()
	if err != nil {
		return Digest{}
	}
	return digestFrame("moni-aigate.tasks.event.v1", raw)
}

// ParseEvent is the strict JSON boundary. It accepts object member reordering
// but emits one canonical representation and never returns a partial Event.
func ParseEvent(raw []byte) (Event, error) {
	if len(raw) > maxEventBytes {
		return Event{}, ErrInvalidEvent
	}
	v, err := strictjson.Parse(raw, maxEventBytes)
	if err != nil {
		return Event{}, ErrInvalidEvent
	}
	obj, ok := v.(map[string]any)
	if !ok || !exactKeys(obj, "version", "event_id", "task_id", "kind", "sequence", "observed_at", "provider_reference", "artifact", "failure_code") {
		return Event{}, ErrInvalidEvent
	}
	version, ok := stringValue(obj["version"])
	if !ok {
		return Event{}, ErrInvalidEvent
	}
	eventID, err := parseIDValue(obj["event_id"])
	if err != nil {
		return Event{}, ErrInvalidEvent
	}
	taskID, err := parseIDValue(obj["task_id"])
	if err != nil {
		return Event{}, ErrInvalidEvent
	}
	kindString, ok := stringValue(obj["kind"])
	if !ok {
		return Event{}, ErrInvalidEvent
	}
	sequence, ok := canonicalUintString(obj["sequence"])
	if !ok {
		return Event{}, ErrInvalidEvent
	}
	observedAtString, ok := stringValue(obj["observed_at"])
	if !ok {
		return Event{}, ErrInvalidEvent
	}
	observedAt, ok := parseCanonicalUTC(observedAtString)
	if !ok {
		return Event{}, ErrInvalidEvent
	}
	provider, ok := nullableString(obj["provider_reference"])
	if !ok {
		return Event{}, ErrInvalidEvent
	}
	failure, ok := nullableString(obj["failure_code"])
	if !ok {
		return Event{}, ErrInvalidEvent
	}
	artifact, ok := parseArtifact(obj["artifact"])
	if !ok {
		return Event{}, ErrInvalidEvent
	}
	return NewEvent(EventInput{
		Version:           version,
		EventID:           eventID,
		TaskID:            taskID,
		Kind:              EventKind(kindString),
		Sequence:          sequence,
		ObservedAt:        observedAt,
		ProviderReference: provider,
		Artifact:          artifact,
		FailureCode:       failure,
	})
}

func exactKeys(obj map[string]any, keys ...string) bool {
	if len(obj) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := obj[key]; !ok {
			return false
		}
	}
	return true
}

func stringValue(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func nullableString(v any) (string, bool) {
	if v == nil {
		return "", true
	}
	return stringValue(v)
}

func parseIDValue(v any) (ID, error) {
	s, ok := stringValue(v)
	if !ok {
		return "", ErrInvalidID
	}
	return ParseID(s)
}

func canonicalUintString(v any) (uint64, bool) {
	s, ok := stringValue(v)
	if !ok || s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil && strconv.FormatUint(n, 10) == s && n > 0
}

func parseCanonicalUTC(raw string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || t.Location() != time.UTC || t.Format(time.RFC3339Nano) != raw {
		return time.Time{}, false
	}
	return t, true
}

func parseArtifact(v any) (*Artifact, bool) {
	if v == nil {
		return nil, true
	}
	obj, ok := v.(map[string]any)
	if !ok || !exactKeys(obj, "reference", "media_type", "size", "sha256", "expires_at") {
		return nil, false
	}
	reference, ok := stringValue(obj["reference"])
	if !ok {
		return nil, false
	}
	mediaType, ok := stringValue(obj["media_type"])
	if !ok {
		return nil, false
	}
	sizeString, ok := stringValue(obj["size"])
	if !ok || sizeString == "" || len(sizeString) > 1 && sizeString[0] == '0' {
		return nil, false
	}
	size, err := strconv.ParseInt(sizeString, 10, 64)
	if err != nil || size <= 0 || strconv.FormatInt(size, 10) != sizeString {
		return nil, false
	}
	shaString, ok := stringValue(obj["sha256"])
	if !ok {
		return nil, false
	}
	digest, err := ParseDigest(shaString)
	if err != nil {
		return nil, false
	}
	expiresString, ok := stringValue(obj["expires_at"])
	if !ok {
		return nil, false
	}
	expiresAt, ok := parseCanonicalUTC(expiresString)
	if !ok {
		return nil, false
	}
	a := &Artifact{Reference: reference, MediaType: mediaType, Size: size, SHA256: digest, ExpiresAt: expiresAt}
	return a, a.valid() == nil
}

func (e Event) String() string {
	raw, err := e.CanonicalJSON()
	if err != nil {
		return fmt.Sprintf("event(%s)", e.kind)
	}
	return string(raw)
}
