package tasks

import (
	"context"
	"errors"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

// Service coordinates the pure task state machine with one Store and one
// provider adapter. It never owns a wallet, reservation, attempt, settlement,
// or source record. Those references are supplied by the existing commercial
// transaction and are carried unchanged through every task write.
type Service struct {
	store   Store
	adapter provider.TaskAdapter
	newID   func() ID
	worker  Actor
}

type ServiceOptions struct {
	Store   Store
	Adapter provider.TaskAdapter
	NewID   func() ID
	Worker  Actor
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Store == nil || options.Adapter == nil || options.NewID == nil || options.Worker.valid() != nil || !options.Worker.has(PermissionTaskObserve) {
		return nil, ErrInvalidInput
	}
	return &Service{store: options.Store, adapter: options.Adapter, newID: options.NewID, worker: options.Worker}, nil
}

func (s *Service) Create(ctx context.Context, input SubmitInput, refs References) (Task, error) {
	return s.store.CreateTask(ctx, input, refs)
}

// SubmitByIdempotency is the retry-safe entry point for a client submit. It
// returns the existing task for an identical request, including after an
// unknown provider result, and never reacquires provider generation permission.
func (s *Service) SubmitByIdempotency(ctx context.Context, owner Owner, key string, digest Digest, now time.Time) (Task, error) {
	task, _, err := s.store.FindByIdempotency(ctx, owner, key)
	if err != nil {
		return Task{}, err
	}
	if _, err := task.ReplaySubmit(key, digest); err != nil {
		return Task{}, err
	}
	if !task.CanSubmit() {
		return task, nil
	}
	return s.Submit(ctx, task.ID(), now)
}

func (s *Service) Status(ctx context.Context, actor Actor, id ID) (Snapshot, error) {
	task, _, err := s.store.Snapshot(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	return task.Status(actor)
}

// Submit persists prepared -> submitting before making the one provider call.
// Any adapter error is conservatively recorded as submission_unknown. This
// records uncertainty and never manufactures a successful provider fact.
func (s *Service) Submit(ctx context.Context, id ID, now time.Time) (Task, error) {
	task, refs, err := s.store.Snapshot(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if !task.CanSubmit() {
		return task, ErrNoResubmit
	}
	var started Task
	err = s.store.WithTask(ctx, id, func(tx TaskTx) error {
		started, err = tx.Task().BeginSubmit(now)
		if err != nil {
			return err
		}
		return tx.SaveTask(started)
	})
	if err != nil {
		return Task{}, err
	}

	result, callErr := s.adapter.Submit(ctx, provider.TaskSubmission{
		TaskID: id.String(), RequestID: refs.RequestID.String(), ReservationID: refs.ReservationID.String(),
		AttemptID: refs.AttemptID.String(), SourceID: refs.SourceID.String(), IdempotencyKey: task.idempotencyKey,
		RequestDigest: task.requestDigest.String(), SubmittedAt: now,
	})
	if callErr != nil {
		return s.recordUnknown(ctx, id, now, callErr)
	}
	event, eventErr := s.submissionEvent(id, result)
	if eventErr != nil {
		return s.recordUnknown(ctx, id, now, eventErr)
	}
	if err := s.applyEvent(ctx, id, now, event); err != nil {
		return Task{}, err
	}
	updated, _, err := s.store.Snapshot(ctx, id)
	return updated, err
}

func (s *Service) submissionEvent(taskID ID, result provider.TaskSubmissionResult) (Event, error) {
	eventID, err := ParseID(result.EventID)
	if err != nil {
		return Event{}, ErrInvalidEvent
	}
	if result.TaskID != taskID.String() || result.ProviderReference == "" || result.Sequence == 0 || !validUTC(result.ObservedAt) {
		return Event{}, ErrInvalidEvent
	}
	return NewEvent(EventInput{Version: "1", EventID: eventID, TaskID: taskID, Kind: EventSubmissionAccepted, Sequence: result.Sequence, ObservedAt: result.ObservedAt, ProviderReference: result.ProviderReference})
}

func (s *Service) recordUnknown(ctx context.Context, id ID, now time.Time, cause error) (Task, error) {
	if cause == nil {
		cause = ErrInvalidEvent
	}
	event, err := NewEvent(EventInput{Version: "1", EventID: s.newID(), TaskID: id, Kind: EventSubmissionUnknown, Sequence: 1, ObservedAt: now})
	if err != nil {
		return Task{}, err
	}
	if err := s.applyEvent(ctx, id, now, event); err != nil {
		return Task{}, err
	}
	task, _, err := s.store.Snapshot(ctx, id)
	if err != nil {
		return Task{}, err
	}
	return task, cause
}

func (s *Service) Observe(ctx context.Context, id ID, now time.Time) (Task, error) {
	task, _, err := s.store.Snapshot(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if task.IsTerminal() {
		return task, nil
	}
	observation, err := s.adapter.Query(ctx, provider.TaskQuery{TaskID: id.String(), ProviderReference: task.ProviderReferenceForAdapter(), RequestDigest: task.requestDigest.String()})
	if err != nil {
		return Task{}, err
	}
	event, err := s.observationEvent(id, observation)
	if err != nil {
		return Task{}, err
	}
	applyAt := now
	if observation.ObservedAt.After(applyAt) {
		applyAt = observation.ObservedAt
	}
	if err := s.applyEvent(ctx, id, applyAt, event); err != nil {
		return Task{}, err
	}
	updated, _, err := s.store.Snapshot(ctx, id)
	return updated, err
}

func (s *Service) observationEvent(taskID ID, observation provider.TaskObservation) (Event, error) {
	eventID, err := ParseID(observation.EventID)
	if err != nil {
		return Event{}, ErrInvalidEvent
	}
	parsedTask, err := ParseID(observation.TaskID)
	if err != nil || parsedTask != taskID {
		return Event{}, ErrEventConflict
	}
	kind := EventKind(observation.Kind)
	var artifact *Artifact
	if observation.Artifact != nil {
		digest, err := ParseDigest(observation.Artifact.SHA256)
		if err != nil {
			return Event{}, ErrInvalidEvent
		}
		artifact = &Artifact{Reference: observation.Artifact.Reference, MediaType: observation.Artifact.MediaType, Size: observation.Artifact.Size, SHA256: digest, ExpiresAt: observation.Artifact.ExpiresAt}
	}
	event, err := NewEvent(EventInput{Version: "1", EventID: eventID, TaskID: taskID, Kind: kind, Sequence: observation.Sequence, ObservedAt: observation.ObservedAt, ProviderReference: observation.ProviderReference, FailureCode: observation.FailureCode, Artifact: artifact})
	if err != nil {
		return Event{}, err
	}
	return event, nil
}

func (s *Service) applyEvent(ctx context.Context, id ID, now time.Time, event Event) error {
	return s.store.WithTask(ctx, id, func(tx TaskTx) error {
		updated, err := tx.Task().Observe(s.worker, now, event)
		if err != nil {
			return err
		}
		if err := tx.AppendEvent(event); err != nil {
			return err
		}
		return tx.SaveTask(updated)
	})
}

func (s *Service) Cancel(ctx context.Context, actor Actor, id ID, now time.Time) (Task, error) {
	task, _, err := s.store.Snapshot(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if task.cancel == CancelRequested {
		return task, nil
	}
	requested, err := task.RequestCancel(actor, now)
	if err != nil {
		return Task{}, err
	}
	if err := s.store.WithTask(ctx, id, func(tx TaskTx) error {
		updated, err := tx.Task().RequestCancel(actor, now)
		if err != nil {
			return err
		}
		return tx.SaveTask(updated)
	}); err != nil {
		return Task{}, err
	}
	if requested.ProviderReferenceForAdapter() == "" {
		return requested, ErrCancelConflict
	}
	observation, err := s.adapter.Cancel(ctx, provider.TaskCancel{TaskID: id.String(), ProviderReference: requested.ProviderReferenceForAdapter()})
	if err != nil {
		return requested, err
	}
	event, err := s.observationEvent(id, observation)
	if err != nil {
		return Task{}, err
	}
	if err := s.applyEvent(ctx, id, now, event); err != nil {
		return Task{}, err
	}
	updated, _, err := s.store.Snapshot(ctx, id)
	return updated, err
}

func (s *Service) Expire(ctx context.Context, id ID, now time.Time) (Task, error) {
	if err := s.store.WithTask(ctx, id, func(tx TaskTx) error {
		updated, err := tx.Task().Expire(now)
		if err != nil {
			return err
		}
		return tx.SaveTask(updated)
	}); err != nil {
		return Task{}, err
	}
	task, _, err := s.store.Snapshot(ctx, id)
	return task, err
}

func (s *Service) IssueDownloadGrant(ctx context.Context, actor Actor, id, grantID ID, now time.Time, ttl time.Duration) (DownloadGrant, error) {
	return s.store.IssueGrant(ctx, id, actor, grantID, now, ttl)
}

func (s *Service) Download(ctx context.Context, actor Actor, grant DownloadGrant, now time.Time) ([]byte, error) {
	task, _, err := s.store.Snapshot(ctx, grant.TaskID)
	if err != nil {
		return nil, err
	}
	if _, err := task.Download(actor, grant, now); err != nil {
		return nil, err
	}
	if body, err := s.store.ReadArtifact(ctx, actor, grant, now); err == nil {
		return body, nil
	} else if !errors.Is(err, ErrArtifactMissing) {
		return nil, err
	}
	result, err := s.adapter.Download(ctx, provider.TaskDownload{TaskID: grant.TaskID.String(), ProviderReference: task.ProviderReferenceForAdapter(), ArtifactReference: grant.ArtifactRef, ArtifactSHA256: grant.ArtifactSHA256.String(), MaxBytes: grant.MaxBytes})
	if err != nil {
		return nil, err
	}
	if int64(len(result.Body)) != grant.MaxBytes || digestBytes(result.Body) != grant.ArtifactSHA256 {
		return nil, ErrArtifactDigest
	}
	if err := s.store.WithTask(ctx, grant.TaskID, func(tx TaskTx) error { return tx.PutArtifact(result.Body) }); err != nil {
		return nil, err
	}
	return s.store.ReadArtifact(ctx, actor, grant, now)
}

func (s *Service) BindSettlement(ctx context.Context, id, settlementID ID) error {
	return s.store.WithTask(ctx, id, func(tx TaskTx) error { return tx.BindSettlement(settlementID) })
}

func (s *Service) Claim(ctx context.Context, kind WorkKind, worker, leaseID ID, now time.Time, ttl time.Duration) (WorkerLease, Task, References, error) {
	return s.store.Claim(ctx, kind, worker, now, ttl, leaseID)
}

func (s *Service) RenewLease(ctx context.Context, lease WorkerLease, now time.Time, ttl time.Duration) (WorkerLease, error) {
	return s.store.RenewLease(ctx, lease, now, ttl)
}

func (s *Service) ReleaseLease(ctx context.Context, lease WorkerLease) error {
	return s.store.ReleaseLease(ctx, lease)
}
