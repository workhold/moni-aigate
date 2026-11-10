package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/tasks"
)

const (
	taskMediaModelKey    = "mock-video-generation"
	taskMediaOperation   = "video_generation"
	taskMediaProfile     = "33333333-3333-4333-8333-333333333333"
	taskMediaQuantity    = int64(1)
	taskMediaSource      = "adapter"
	taskMediaUnavailable = "task media billing is unavailable"
)

var errTaskMediaBillingUnavailable = errors.New(taskMediaUnavailable)

type taskMediaAdapter struct {
	server    *Server
	principal identity.Principal
	delegate  provider.TaskAdapter
}

func (s *Server) createTaskRecord(ctx context.Context, input tasks.SubmitInput, refs tasks.References) (tasks.Task, error) {
	task, err := tasks.New(input)
	if err != nil {
		return tasks.Task{}, err
	}
	if s.Pool == nil {
		return tasks.Task{}, errTaskMediaBillingUnavailable
	}
	_, err = s.Pool.Exec(ctx, `
INSERT INTO media_tasks
 (tenant_id,id,request_id,owner_id,attempt_id,reservation_id,source_id,model_id,profile_version,schema_id,schema_digest,variant_key,idempotency_key,submit_fingerprint,input_digest,submit_deadline,observe_deadline,result_deadline,artifact_deadline,max_artifact_bytes)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		input.Owner.TenantID.String(), input.TaskID.String(), refs.RequestID.String(), input.Owner.SubjectID.String(),
		nullableTaskID(refs.AttemptID), refs.ReservationID.String(), refs.SourceID.String(), input.ModelID.String(),
		input.ProfileVersion.String(), input.SchemaID.String(), input.SchemaDigest[:], defaultTaskVariant(input.VariantKey),
		input.IdempotencyKey, input.RequestDigest[:], input.RequestDigest[:], input.SubmitBy, input.ObserveUntil,
		input.ResultUntil, input.ResultUntil, input.MaxArtifactBytes)
	if err != nil {
		return tasks.Task{}, err
	}
	return task, nil
}

func nullableTaskID(id tasks.ID) any {
	if id == "" {
		return nil
	}
	return id.String()
}

func defaultTaskVariant(value string) string {
	if value == "" {
		return "default"
	}
	return value
}

func (s *Server) findTaskIdempotency(ctx context.Context, owner tasks.Owner, key string, input tasks.SubmitInput, refs tasks.References) (string, bool, error) {
	var taskID, requestID, reservationID, sourceID string
	var attemptID *string
	var submitFingerprint, inputDigest []byte
	err := s.Pool.QueryRow(ctx, `
SELECT id,request_id,attempt_id,reservation_id,source_id,submit_fingerprint,input_digest
FROM media_tasks
WHERE tenant_id=$1 AND owner_id=$2 AND idempotency_key=$3`, owner.TenantID.String(), owner.SubjectID.String(), key).
		Scan(&taskID, &requestID, &attemptID, &reservationID, &sourceID, &submitFingerprint, &inputDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if attemptID == nil {
		return "", false, errors.New("task attempt reference is missing")
	}
	if !bytes.Equal(submitFingerprint, input.RequestDigest[:]) || !bytes.Equal(inputDigest, input.RequestDigest[:]) || requestID != refs.RequestID.String() || *attemptID != refs.AttemptID.String() || reservationID != refs.ReservationID.String() || sourceID != refs.SourceID.String() {
		return "", false, tasks.ErrIdempotencyConflict
	}
	if _, err = tasks.ParseID(taskID); err != nil {
		return "", false, err
	}
	return taskID, true, nil
}

func (s *Server) requireTaskMediaBilling() error {
	if s == nil || s.Pool == nil || s.Billing == nil {
		return errTaskMediaBillingUnavailable
	}
	return nil
}

func (s *Server) newTaskMediaAdapter(principal identity.Principal, delegate provider.TaskAdapter) (provider.TaskAdapter, error) {
	if err := s.requireTaskMediaBilling(); err != nil {
		return nil, err
	}
	if !s.mediaProviderReady(delegate) {
		return nil, errMediaProviderUnavailable
	}
	return &taskMediaAdapter{server: s, principal: principal, delegate: delegate}, nil
}

// NewTaskWorkerAdapter keeps the worker entry point on the same current media
// billing path as HTTP task submission while allowing the worker package to
// remain independent of server internals.
func (s *Server) NewTaskWorkerAdapter(principal identity.Principal, delegate provider.TaskAdapter) (provider.TaskAdapter, error) {
	return s.newTaskMediaAdapter(principal, delegate)
}

func (s *Server) reserveTaskMedia(ctx context.Context, principal identity.Principal, input tasks.SubmitInput, digest [32]byte, deadline time.Time) (billing.MediaReservation, error) {
	if err := s.requireTaskMediaBilling(); err != nil {
		return billing.MediaReservation{}, err
	}
	return s.Billing.ReserveMedia(ctx, billing.MediaReserveInput{
		TenantID:       principal.TenantID,
		UserID:         principal.UserID,
		KeyID:          principal.KeyID,
		RequestID:      input.RequestID.String(),
		AttemptID:      input.AttemptID.String(),
		Operation:      taskMediaOperation,
		ModelKey:       taskMediaModelKey,
		ProfileVersion: taskMediaProfile,
		IdempotencyKey: input.IdempotencyKey,
		RequestDigest:  digest[:],
		QuantityBound:  taskMediaQuantity,
		Deadline:       deadline,
	})
}

func (a *taskMediaAdapter) Submit(ctx context.Context, input provider.TaskSubmission) (provider.TaskSubmissionResult, error) {
	if err := a.server.Billing.MarkMediaAttemptSending(ctx, a.principal.TenantID, input.AttemptID); err != nil {
		if releaseErr := a.finalizeSubmissionFailure(ctx, input, err); releaseErr != nil {
			return provider.TaskSubmissionResult{}, releaseErr
		}
		return provider.TaskSubmissionResult{}, err
	}
	result, err := a.delegate.Submit(ctx, input)
	if err != nil {
		return result, a.finalizeSubmissionFailure(ctx, input, err)
	}
	if result.Mode != a.server.expectedMediaProviderMode() {
		return result, a.finalizeSubmissionFailure(ctx, input, errMediaProviderUnavailable)
	}
	result.ObservedAt = result.ObservedAt.UTC().Round(0)
	if result.TaskID != input.TaskID || result.EventID == "" || result.ProviderReference == "" || result.Sequence == 0 || result.ObservedAt.IsZero() {
		return result, a.finalizeSubmissionFailure(ctx, input, errors.New("invalid task submission result"))
	}
	return result, nil
}

func (a *taskMediaAdapter) Query(ctx context.Context, input provider.TaskQuery) (provider.TaskObservation, error) {
	observation, err := a.delegate.Query(ctx, input)
	if err != nil {
		return observation, err
	}
	if observation.Mode != a.server.expectedMediaProviderMode() {
		return observation, errMediaProviderUnavailable
	}
	observation.ObservedAt = observation.ObservedAt.UTC().Round(0)
	if observation.Artifact != nil {
		artifact := *observation.Artifact
		artifact.ExpiresAt = artifact.ExpiresAt.UTC().Round(0)
		observation.Artifact = &artifact
	}
	if !taskObservationTerminal(observation.Kind) {
		return observation, nil
	}
	if !validTaskTerminalObservation(input.TaskID, observation) {
		return observation, nil
	}
	refs, err := a.taskReferences(ctx, input.TaskID)
	if err != nil {
		return observation, err
	}
	if observation.Mode != a.server.expectedMediaProviderMode() {
		return observation, errMediaProviderUnavailable
	}
	if err = a.finalize(ctx, refs, input.TaskID, observation.Kind, observation.EventID, taskObservationQuantity(observation.Kind)); err != nil {
		return observation, err
	}
	return observation, nil
}

func (a *taskMediaAdapter) Cancel(ctx context.Context, input provider.TaskCancel) (provider.TaskObservation, error) {
	observation, err := a.delegate.Cancel(ctx, input)
	if err != nil {
		return observation, err
	}
	observation.ObservedAt = observation.ObservedAt.UTC().Round(0)
	if observation.Kind != "status_cancelled" || !validTaskTerminalObservation(input.TaskID, observation) {
		return observation, nil
	}
	refs, err := a.taskReferences(ctx, input.TaskID)
	if err != nil {
		return observation, err
	}
	if err = a.finalize(ctx, refs, input.TaskID, observation.Kind, observation.EventID, 0); err != nil {
		return observation, err
	}
	return observation, nil
}

func (a *taskMediaAdapter) Download(ctx context.Context, input provider.TaskDownload) (provider.TaskDownloadResult, error) {
	result, err := a.delegate.Download(ctx, input)
	if err == nil && result.Mode != a.server.expectedMediaProviderMode() {
		return result, errMediaProviderUnavailable
	}
	return result, err
}

func (a *taskMediaAdapter) taskReferences(ctx context.Context, taskID string) (tasks.References, error) {
	id, err := tasks.ParseID(taskID)
	if err != nil {
		return tasks.References{}, err
	}
	_, refs, err := a.server.TaskStore.Snapshot(ctx, id)
	return refs, err
}

func (a *taskMediaAdapter) finalizeSubmissionFailure(ctx context.Context, input provider.TaskSubmission, cause error) error {
	refs := tasks.References{RequestID: tasks.ID(input.RequestID), AttemptID: tasks.ID(input.AttemptID)}
	if err := a.finalize(ctx, refs, input.TaskID, "submission_unknown", "submission-unknown", 0); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (a *taskMediaAdapter) finalize(ctx context.Context, refs tasks.References, taskID, kind, eventID string, quantity int64) error {
	if refs.RequestID == "" || refs.AttemptID == "" {
		return errors.New("task media references are missing")
	}
	outcome, usage := "failed", "missing"
	if kind == "status_succeeded" {
		outcome, usage = "completed", "confirmed_final"
	} else if kind == "submission_unknown" {
		outcome, usage = "unknown", "unknown"
	}
	outcome, usage, quantity = a.server.mediaFinalizationValues(outcome, usage, quantity)
	sourceEventID := taskMediaEventID(taskID, eventID, kind)
	if a.server.mockMediaProfile() {
		sourceEventID = "mock:" + sourceEventID
	}
	settlement, err := a.server.Billing.FinalizeMedia(ctx, billing.MediaFinalizationInput{
		TenantID:      a.principal.TenantID,
		RequestID:     refs.RequestID.String(),
		AttemptID:     refs.AttemptID.String(),
		Source:        taskMediaSource,
		SourceEventID: sourceEventID,
		Operation:     taskMediaOperation,
		Outcome:       outcome,
		UsageState:    usage,
		Quantity:      quantity,
	})
	if err != nil {
		return err
	}
	if taskID == "" {
		return nil
	}
	id, err := tasks.ParseID(taskID)
	if err != nil {
		return err
	}
	return a.server.TaskStore.WithTask(ctx, id, func(tx tasks.TaskTx) error {
		return tx.BindSettlement(tasks.ID(settlement.ID))
	})
}

func (s *Server) finalizeTaskMedia(ctx context.Context, tenant, requestID, attemptID, taskID, outcome, usage string, quantity int64, eventID string) error {
	if err := s.requireTaskMediaBilling(); err != nil {
		return err
	}
	outcome, usage, quantity = s.mediaFinalizationValues(outcome, usage, quantity)
	sourceEventID := taskMediaEventID(taskID, eventID, outcome)
	if s.mockMediaProfile() {
		sourceEventID = "mock:" + sourceEventID
	}
	settlement, err := s.Billing.FinalizeMedia(ctx, billing.MediaFinalizationInput{
		TenantID: tenant, RequestID: requestID, AttemptID: attemptID,
		Source: taskMediaSource, SourceEventID: sourceEventID,
		Operation: taskMediaOperation, Outcome: outcome, UsageState: usage, Quantity: quantity,
	})
	if err != nil || taskID == "" {
		return err
	}
	id, err := tasks.ParseID(taskID)
	if err != nil {
		return err
	}
	return s.TaskStore.WithTask(ctx, id, func(tx tasks.TaskTx) error {
		return tx.BindSettlement(tasks.ID(settlement.ID))
	})
}

// FinalizeExpiredTaskMedia is the worker-only expiry hook. The task's
// freshness marker is already durable when this is called. FinalizeMedia is
// idempotent, so a lease retry cannot create a second settlement.
func (s *Server) FinalizeExpiredTaskMedia(ctx context.Context, principal identity.Principal, task tasks.Task, refs tasks.References) error {
	return s.finalizeTaskMedia(ctx, principal.TenantID, refs.RequestID.String(), refs.AttemptID.String(), task.ID().String(), "failed", "missing", 0, "expired")
}

func taskObservationTerminal(kind string) bool {
	return kind == "status_succeeded" || kind == "status_failed" || kind == "status_cancelled"
}

func validTaskTerminalObservation(taskID string, observation provider.TaskObservation) bool {
	if observation.TaskID != taskID || observation.EventID == "" || observation.Sequence == 0 || observation.ObservedAt.IsZero() {
		return false
	}
	if observation.Kind == "status_succeeded" {
		return observation.ProviderReference != "" && observation.Artifact != nil
	}
	if observation.Kind == "status_failed" {
		return observation.ProviderReference != "" && observation.FailureCode != ""
	}
	return observation.ProviderReference != ""
}

func taskObservationQuantity(kind string) int64 {
	if kind == "status_succeeded" {
		return taskMediaQuantity
	}
	return 0
}

func taskMediaEventID(taskID, eventID, kind string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", taskID, eventID, kind)))
	return hex.EncodeToString(hash[:])
}
