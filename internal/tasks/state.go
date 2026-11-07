package tasks

import "time"

// SubmitInput is the complete immutable identity captured before any provider
// generation call. The request body itself is intentionally absent.
type SubmitInput struct {
	TaskID           ID
	Owner            Owner
	IdempotencyKey   string
	RequestDigest    Digest
	CreatedAt        time.Time
	SubmitBy         time.Time
	ObserveUntil     time.Time
	ResultUntil      time.Time
	MaxArtifactBytes int64
}

// Task is an immutable value. Every state-changing method returns a new value;
// sharing a Task between readers cannot race or mutate a second caller's view.
type Task struct {
	id                ID
	owner             Owner
	idempotencyKey    string
	requestDigest     Digest
	createdAt         time.Time
	submitBy          time.Time
	observeUntil      time.Time
	resultUntil       time.Time
	maxArtifactBytes  int64
	protocol          ProtocolState
	observation       ObservationState
	cancel            CancelState
	artifactState     ArtifactState
	providerReference string
	artifact          Artifact
	failureCode       string
	lastObservedAt    time.Time
	lastEventID       ID
	lastEventDigest   Digest
	lastSequence      uint64
}

// New creates a prepared task. It does not reserve money, create an attempt,
// or contact a provider.
func New(input SubmitInput) (Task, error) {
	var zero Task
	if err := validateID(input.TaskID); err != nil {
		return zero, err
	}
	if err := input.Owner.valid(); err != nil {
		return zero, err
	}
	if !validIdempotencyKey(input.IdempotencyKey) {
		return zero, ErrInvalidInput
	}
	if err := validateDigest(input.RequestDigest); err != nil {
		return zero, err
	}
	if !validUTC(input.CreatedAt) || !validUTC(input.SubmitBy) || !validUTC(input.ObserveUntil) || !validUTC(input.ResultUntil) {
		return zero, ErrInvalidInput
	}
	if !input.CreatedAt.Before(input.SubmitBy) || !input.SubmitBy.Before(input.ObserveUntil) || !input.ObserveUntil.Before(input.ResultUntil) {
		return zero, ErrInvalidInput
	}
	if input.MaxArtifactBytes <= 0 || input.MaxArtifactBytes > maxArtifactBytes {
		return zero, ErrArtifactTooLarge
	}
	return Task{
		id:               input.TaskID,
		owner:            input.Owner,
		idempotencyKey:   input.IdempotencyKey,
		requestDigest:    input.RequestDigest,
		createdAt:        input.CreatedAt,
		submitBy:         input.SubmitBy,
		observeUntil:     input.ObserveUntil,
		resultUntil:      input.ResultUntil,
		maxArtifactBytes: input.MaxArtifactBytes,
		protocol:         StatePrepared,
		observation:      ObservationUnobserved,
		cancel:           CancelNone,
		artifactState:    ArtifactNone,
	}, nil
}

// Snapshot is the read-only task projection used by status and authorization
// tests. Artifact is zero unless it has been accepted by a success event.
type Snapshot struct {
	TaskID            ID
	Owner             Owner
	Protocol          ProtocolState
	Observation       ObservationState
	Cancel            CancelState
	Artifact          ArtifactState
	ProviderReference string
	Result            Artifact
	FailureCode       string
	LastObservedAt    time.Time
	LastEventID       ID
	LastSequence      uint64
	CreatedAt         time.Time
	SubmitBy          time.Time
	ObserveUntil      time.Time
	ResultUntil       time.Time
	MaxArtifactBytes  int64
}

func (t Task) valid() error {
	if err := validateID(t.id); err != nil {
		return ErrInvalidTask
	}
	if err := t.owner.valid(); err != nil {
		return ErrInvalidTask
	}
	if !validIdempotencyKey(t.idempotencyKey) || t.requestDigest.IsZero() || !validUTC(t.createdAt) || !validUTC(t.submitBy) || !validUTC(t.observeUntil) || !validUTC(t.resultUntil) || t.maxArtifactBytes <= 0 {
		return ErrInvalidTask
	}
	return nil
}

func (t Task) Snapshot() (Snapshot, error) {
	if err := t.valid(); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		TaskID:            t.id,
		Owner:             t.owner,
		Protocol:          t.protocol,
		Observation:       t.observation,
		Cancel:            t.cancel,
		Artifact:          t.artifactState,
		ProviderReference: t.providerReference,
		Result:            t.artifact,
		FailureCode:       t.failureCode,
		LastObservedAt:    t.lastObservedAt,
		LastEventID:       t.lastEventID,
		LastSequence:      t.lastSequence,
		CreatedAt:         t.createdAt,
		SubmitBy:          t.submitBy,
		ObserveUntil:      t.observeUntil,
		ResultUntil:       t.resultUntil,
		MaxArtifactBytes:  t.maxArtifactBytes,
	}, nil
}

func (t Task) ID() ID { return t.id }

func (t Task) IsTerminal() bool {
	return t.protocol == StateSucceeded || t.protocol == StateFailed || t.protocol == StateCancelled
}

func (t Task) CanSubmit() bool { return t.protocol == StatePrepared }

func (t Task) SubmissionUnknown() bool { return t.protocol == StateSubmissionUnknown }

// ReplaySubmit verifies the complete idempotency identity and returns the same
// task unchanged. It is the only safe operation for a repeated submit request.
func (t Task) ReplaySubmit(key string, digest Digest) (Task, error) {
	if err := t.valid(); err != nil {
		return Task{}, err
	}
	if !validIdempotencyKey(key) || digest.IsZero() {
		return Task{}, ErrInvalidInput
	}
	if key != t.idempotencyKey || digest != t.requestDigest {
		return Task{}, ErrIdempotencyConflict
	}
	return t, nil
}

// BeginSubmit is the one-way transition that grants the outer adapter
// permission to attempt the single provider POST.
func (t Task) BeginSubmit(now time.Time) (Task, error) {
	if err := t.valid(); err != nil {
		return Task{}, err
	}
	if !validUTC(now) || now.Before(t.createdAt) {
		return Task{}, ErrInvalidInput
	}
	if now.After(t.submitBy) {
		return Task{}, ErrExpired
	}
	if t.protocol != StatePrepared {
		return Task{}, ErrNoResubmit
	}
	t.protocol = StateSubmitting
	return t, nil
}

func (t Task) RequestCancel(actor Actor, now time.Time) (Task, error) {
	if err := t.valid(); err != nil {
		return Task{}, err
	}
	if err := t.authorizeCancel(actor); err != nil {
		return Task{}, err
	}
	if !validUTC(now) || now.Before(t.createdAt) {
		return Task{}, ErrInvalidInput
	}
	if t.cancel == CancelRequested || t.protocol == StateCancelRequested {
		return t, nil
	}
	if t.IsTerminal() {
		return Task{}, ErrAlreadyTerminal
	}
	if t.protocol != StateQueued && t.protocol != StateRunning {
		return Task{}, ErrCancelConflict
	}
	t.protocol = StateCancelRequested
	t.cancel = CancelRequested
	return t, nil
}

// BlockObservation stops local polling without changing protocol or financial
// facts. It is deliberately unavailable after protocol terminality.
func (t Task) BlockObservation() (Task, error) {
	if err := t.valid(); err != nil {
		return Task{}, err
	}
	if t.IsTerminal() {
		return Task{}, ErrInvalidTransition
	}
	t.observation = ObservationBlocked
	return t, nil
}

// Expire advances only local freshness and result availability. It performs no
// provider call and never creates a new task or attempt.
func (t Task) Expire(now time.Time) (Task, error) {
	if err := t.valid(); err != nil {
		return Task{}, err
	}
	if !validUTC(now) || now.Before(t.createdAt) {
		return Task{}, ErrInvalidInput
	}
	if !now.Before(t.observeUntil) && t.observation != ObservationBlocked {
		t.observation = ObservationExpired
	}
	if t.protocol == StateSucceeded && t.artifactState == ArtifactAvailable && !now.Before(t.artifact.ExpiresAt) {
		t.artifactState = ArtifactExpired
	}
	return t, nil
}

func (t Task) Status(actor Actor) (Snapshot, error) {
	if err := t.valid(); err != nil {
		return Snapshot{}, err
	}
	if err := t.authorizeStatus(actor); err != nil {
		return Snapshot{}, err
	}
	return t.Snapshot()
}

func (t Task) Observe(actor Actor, now time.Time, event Event) (Task, error) {
	if err := t.valid(); err != nil {
		return Task{}, err
	}
	if err := t.authorizeObserve(actor); err != nil {
		return Task{}, err
	}
	return t.Apply(now, event)
}

// Apply applies one already parsed, authenticated observation. Authentication
// of a provider signature and binding to the original attempt happen outside
// this pure package; this method only enforces the task identity and transition
// rules.
func (t Task) Apply(now time.Time, event Event) (Task, error) {
	if err := t.valid(); err != nil {
		return Task{}, err
	}
	if err := event.valid(); err != nil {
		return Task{}, err
	}
	if !validUTC(now) || now.Before(t.createdAt) || event.observedAt.After(now) {
		return Task{}, ErrInvalidEvent
	}
	if event.taskID != t.id {
		return Task{}, ErrEventConflict
	}
	ed := event.Digest()
	if event.sequence < t.lastSequence {
		return Task{}, ErrStaleEvent
	}
	if event.sequence == t.lastSequence {
		if event.eventID == t.lastEventID && ed == t.lastEventDigest {
			return t, nil
		}
		return Task{}, ErrEventConflict
	}
	if t.IsTerminal() && event.kind != EventArtifactExpired {
		return Task{}, ErrAlreadyTerminal
	}
	if event.providerReference != "" && t.providerReference != "" && event.providerReference != t.providerReference {
		return Task{}, ErrEventConflict
	}
	if err := t.applyEvent(event); err != nil {
		return Task{}, err
	}
	t.observation = ObservationFresh
	t.lastObservedAt = event.observedAt
	t.lastEventID = event.eventID
	t.lastEventDigest = ed
	t.lastSequence = event.sequence
	return t, nil
}

func (t *Task) applyEvent(event Event) error {
	if event.providerReference != "" {
		t.providerReference = event.providerReference
	}
	switch event.kind {
	case EventSubmissionAccepted:
		if t.protocol != StateSubmitting && t.protocol != StateSubmissionUnknown {
			return ErrInvalidTransition
		}
		t.protocol = StateQueued
	case EventSubmissionUnknown:
		if t.protocol != StateSubmitting {
			return ErrInvalidTransition
		}
		t.protocol = StateSubmissionUnknown
	case EventStatusQueued:
		if t.protocol != StateSubmitting && t.protocol != StateQueued && t.protocol != StateSubmissionUnknown && t.protocol != StateCancelRequested {
			return ErrInvalidTransition
		}
		if t.protocol == StateSubmitting && event.providerReference == "" {
			return ErrInvalidEvent
		}
		// A cancellation remains pending while the provider still reports queued.
		if t.protocol != StateCancelRequested {
			t.protocol = StateQueued
		}
	case EventStatusRunning:
		if t.protocol != StateQueued && t.protocol != StateRunning && t.protocol != StateSubmissionUnknown && t.protocol != StateCancelRequested {
			return ErrInvalidTransition
		}
		t.protocol = StateRunning
		if t.cancel == CancelRequested {
			t.cancel = CancelRejected
		}
	case EventStatusSucceeded:
		if t.protocol != StateQueued && t.protocol != StateRunning && t.protocol != StateSubmissionUnknown && t.protocol != StateCancelRequested {
			return ErrInvalidTransition
		}
		if event.artifact == nil {
			return ErrInvalidEvent
		}
		if err := event.artifact.valid(); err != nil {
			return err
		}
		if event.artifact.Size > t.maxArtifactBytes || event.artifact.ExpiresAt.After(t.resultUntil) {
			return ErrArtifactTooLarge
		}
		t.protocol = StateSucceeded
		t.artifactState = ArtifactAvailable
		t.artifact = *event.artifact
		if t.cancel == CancelRequested {
			t.cancel = CancelRejected
		}
	case EventStatusFailed:
		if t.protocol != StateQueued && t.protocol != StateRunning && t.protocol != StateSubmissionUnknown && t.protocol != StateCancelRequested {
			return ErrInvalidTransition
		}
		t.protocol = StateFailed
		t.failureCode = event.failureCode
		if t.cancel == CancelRequested {
			t.cancel = CancelRejected
		}
	case EventStatusCancelled:
		if t.protocol != StateQueued && t.protocol != StateRunning && t.protocol != StateSubmissionUnknown && t.protocol != StateCancelRequested {
			return ErrInvalidTransition
		}
		t.protocol = StateCancelled
		if t.cancel == CancelRequested {
			t.cancel = CancelAccepted
		}
	case EventCancelRejected:
		if t.protocol != StateCancelRequested || t.cancel != CancelRequested {
			return ErrInvalidTransition
		}
		t.protocol = StateRunning
		t.cancel = CancelRejected
	case EventArtifactExpired:
		if t.protocol != StateSucceeded || t.artifactState != ArtifactAvailable {
			return ErrInvalidTransition
		}
		t.artifactState = ArtifactExpired
	default:
		return ErrInvalidEvent
	}
	return nil
}

func (t Task) authorizeStatus(actor Actor) error {
	if err := actor.valid(); err != nil || actor.TenantID != t.owner.TenantID || !actor.has(PermissionTaskRead) {
		return ErrForbidden
	}
	if actor.SubjectID != t.owner.SubjectID && !actor.has(PermissionTenantRead) {
		return ErrForbidden
	}
	return nil
}

func (t Task) authorizeObserve(actor Actor) error {
	if err := actor.valid(); err != nil || actor.TenantID != t.owner.TenantID || !actor.has(PermissionTaskObserve) {
		return ErrForbidden
	}
	return nil
}

func (t Task) authorizeCancel(actor Actor) error {
	if err := actor.valid(); err != nil || actor.TenantID != t.owner.TenantID || actor.SubjectID != t.owner.SubjectID || !actor.has(PermissionTaskCancel) {
		return ErrForbidden
	}
	return nil
}

func (t Task) authorizeDownload(actor Actor) error {
	if err := actor.valid(); err != nil || actor.TenantID != t.owner.TenantID || !actor.has(PermissionResultDownload) {
		return ErrForbidden
	}
	if actor.SubjectID != t.owner.SubjectID && !actor.has(PermissionTenantRead) {
		return ErrForbidden
	}
	return nil
}

// IssueDownloadGrant binds a short-lived whole-object read grant to the
// immutable result. It does not read or create the result.
func (t Task) IssueDownloadGrant(actor Actor, grantID ID, now time.Time, ttl time.Duration) (DownloadGrant, error) {
	var zero DownloadGrant
	if err := t.valid(); err != nil {
		return zero, err
	}
	if err := t.authorizeDownload(actor); err != nil {
		return zero, err
	}
	if !validUTC(now) || now.Before(t.createdAt) || ttl <= 0 || ttl > maxDownloadGrantTTL {
		return zero, ErrInvalidInput
	}
	if t.protocol != StateSucceeded || t.artifactState != ArtifactAvailable {
		if t.artifactState == ArtifactExpired {
			return zero, ErrArtifactExpired
		}
		return zero, ErrArtifactNotReady
	}
	if !validUTC(t.artifact.ExpiresAt) || !now.Before(t.artifact.ExpiresAt) {
		return zero, ErrArtifactExpired
	}
	if err := validateID(grantID); err != nil {
		return zero, err
	}
	expires := now.Add(ttl)
	if expires.After(t.artifact.ExpiresAt) {
		expires = t.artifact.ExpiresAt
	}
	return DownloadGrant{
		GrantID:        grantID,
		TaskID:         t.id,
		TenantID:       t.owner.TenantID,
		SubjectID:      actor.SubjectID,
		ArtifactRef:    t.artifact.Reference,
		ArtifactSHA256: t.artifact.SHA256,
		MaxBytes:       t.artifact.Size,
		ExpiresAt:      expires,
	}, nil
}

// Download authorizes a read of the original result and returns only its safe
// metadata. It never mutates task state.
func (t Task) Download(actor Actor, grant DownloadGrant, now time.Time) (Artifact, error) {
	if err := t.valid(); err != nil {
		return Artifact{}, err
	}
	if err := t.authorizeDownload(actor); err != nil {
		return Artifact{}, err
	}
	if err := grant.valid(); err != nil || grant.TaskID != t.id || grant.TenantID != t.owner.TenantID || grant.SubjectID != actor.SubjectID || grant.ArtifactRef != t.artifact.Reference || grant.ArtifactSHA256 != t.artifact.SHA256 || grant.MaxBytes != t.artifact.Size {
		return Artifact{}, ErrForbidden
	}
	if !validUTC(now) || now.Before(t.createdAt) {
		return Artifact{}, ErrInvalidInput
	}
	if t.protocol != StateSucceeded || t.artifactState != ArtifactAvailable {
		if t.artifactState == ArtifactExpired || !now.Before(t.artifact.ExpiresAt) {
			return Artifact{}, ErrArtifactExpired
		}
		return Artifact{}, ErrArtifactNotReady
	}
	if !now.Before(grant.ExpiresAt) || !now.Before(t.artifact.ExpiresAt) || grant.ExpiresAt.After(t.artifact.ExpiresAt) {
		return Artifact{}, ErrGrantExpired
	}
	return t.artifact, nil
}
