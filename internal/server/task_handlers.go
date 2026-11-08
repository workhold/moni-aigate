package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/tasks"
)

type taskCreateRequest struct {
	RequestID      string `json:"request_id"`
	ReservationID  string `json:"reservation_id"`
	AttemptID      string `json:"attempt_id"`
	SourceID       string `json:"source_id"`
	ModelID        string `json:"model_id"`
	ProfileVersion string `json:"profile_version"`
	SchemaID       string `json:"schema_id"`
	SchemaDigest   string `json:"schema_digest"`
	MaxArtifact    int64  `json:"max_artifact_bytes"`
}

func (s *Server) taskService(p identity.Principal) (*tasks.Service, error) {
	if s.TaskStore == nil {
		return nil, tasks.ErrInvalidInput
	}
	worker := tasks.Actor{TenantID: tasks.ID(p.TenantID), SubjectID: tasks.ID(p.UserID), Permission: tasks.PermissionTaskObserve | tasks.PermissionTaskRead | tasks.PermissionTaskCancel}
	adapter := s.TaskAdapter
	if adapter == nil {
		adapter = provider.NewMockTaskAdapter()
	}
	return tasks.NewService(tasks.ServiceOptions{Store: s.TaskStore, Adapter: adapter, NewID: func() tasks.ID { return tasks.ID(uuid.NewString()) }, Worker: worker})
}

func readTaskBody(c *gin.Context, out any) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 32*1024+1))
	if err != nil || len(raw) > 32*1024 {
		return nil, tasks.ErrInvalidInput
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(out); err != nil {
		return nil, tasks.ErrInvalidInput
	}
	return raw, nil
}

func (s *Server) createTask(c *gin.Context) {
	p := c.MustGet("principal").(identity.Principal)
	var in taskCreateRequest
	raw, err := readTaskBody(c, &in)
	if err != nil {
		s.fail(c, http.StatusBadRequest, "invalid_request", "任务请求无效")
		return
	}
	requestID, err := tasks.ParseID(in.RequestID)
	if err != nil {
		s.fail(c, 400, "invalid_request", "request_id无效")
		return
	}
	reservationID, err := tasks.ParseID(in.ReservationID)
	if err != nil {
		s.fail(c, 400, "invalid_request", "reservation_id无效")
		return
	}
	attemptID, err := tasks.ParseID(in.AttemptID)
	if err != nil {
		s.fail(c, 400, "invalid_request", "attempt_id无效")
		return
	}
	sourceID, err := tasks.ParseID(in.SourceID)
	if err != nil {
		s.fail(c, 400, "invalid_request", "source_id无效")
		return
	}
	modelID, err := tasks.ParseID(in.ModelID)
	if err != nil {
		s.fail(c, 400, "invalid_request", "model_id无效")
		return
	}
	profile, err := tasks.ParseID(in.ProfileVersion)
	if err != nil {
		s.fail(c, 400, "invalid_request", "profile_version无效")
		return
	}
	schemaID, err := tasks.ParseID(in.SchemaID)
	if err != nil {
		s.fail(c, 400, "invalid_request", "schema_id无效")
		return
	}
	schemaDigest, err := tasks.ParseDigest(in.SchemaDigest)
	if err != nil {
		s.fail(c, 400, "invalid_request", "schema_digest无效")
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		s.fail(c, 400, "invalid_request", "需要Idempotency-Key")
		return
	}
	if in.MaxArtifact <= 0 {
		in.MaxArtifact = 1 << 30
	}
	now := time.Now().UTC()
	digest := sha256.Sum256(raw)
	input := tasks.SubmitInput{TaskID: tasks.ID(uuid.NewString()), Owner: tasks.Owner{TenantID: tasks.ID(p.TenantID), SubjectID: tasks.ID(p.UserID)}, RequestID: requestID, ReservationID: reservationID, SourceID: sourceID, AttemptID: attemptID, ModelID: modelID, ProfileVersion: profile, SchemaID: schemaID, SchemaDigest: schemaDigest, VariantKey: "default", IdempotencyKey: idempotencyKey, RequestDigest: tasks.Digest(digest), CreatedAt: now, SubmitBy: now.Add(2 * time.Minute), ObserveUntil: now.Add(10 * time.Minute), ResultUntil: now.Add(time.Hour), MaxArtifactBytes: in.MaxArtifact}
	refs := tasks.References{RequestID: requestID, ReservationID: reservationID, AttemptID: attemptID, SourceID: sourceID}
	if s.TaskStore == nil {
		s.fail(c, 503, "unavailable", "任务服务未就绪")
		return
	}
	task, err := s.TaskStore.CreateTask(c.Request.Context(), input, refs)
	if err != nil {
		s.error(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": task.ID(), "object": "task", "type": "video_generation", "status": "created", "request_id": requestID})
}

func (s *Server) taskStatus(c *gin.Context) {
	p := c.MustGet("principal").(identity.Principal)
	id, err := tasks.ParseID(c.Param("id"))
	if err != nil || s.TaskStore == nil {
		s.fail(c, 404, "task_not_found", "任务不存在")
		return
	}
	task, _, err := s.TaskStore.Snapshot(c.Request.Context(), id)
	if err != nil {
		s.error(c, err)
		return
	}
	actor := tasks.Actor{TenantID: tasks.ID(p.TenantID), SubjectID: tasks.ID(p.UserID), Permission: tasks.PermissionTaskRead | tasks.PermissionResultDownload | tasks.PermissionTenantRead}
	view, err := task.Status(actor)
	if err != nil {
		s.error(c, err)
		return
	}
	c.JSON(200, gin.H{"id": view.TaskID, "object": "task", "type": "video_generation", "status": taskProtocolStatus(view), "observation": view.Observation, "artifact": view.Artifact})
}

func (s *Server) submitTask(c *gin.Context) {
	p := c.MustGet("principal").(identity.Principal)
	id, err := tasks.ParseID(c.Param("id"))
	if err != nil {
		s.fail(c, 400, "invalid_request", "任务ID无效")
		return
	}
	service, err := s.taskService(p)
	if err != nil {
		s.fail(c, 503, "unavailable", "任务服务未就绪")
		return
	}
	task, err := service.Submit(c.Request.Context(), id, time.Now().UTC())
	if err != nil {
		s.error(c, err)
		return
	}
	view, _ := task.Snapshot()
	c.JSON(202, gin.H{"id": view.TaskID, "object": "task", "status": taskProtocolStatus(view)})
}

func (s *Server) cancelTask(c *gin.Context) {
	p := c.MustGet("principal").(identity.Principal)
	id, err := tasks.ParseID(c.Param("id"))
	if err != nil {
		s.fail(c, 400, "invalid_request", "任务ID无效")
		return
	}
	service, err := s.taskService(p)
	if err != nil {
		s.fail(c, 503, "unavailable", "任务服务未就绪")
		return
	}
	actor := tasks.Actor{TenantID: tasks.ID(p.TenantID), SubjectID: tasks.ID(p.UserID), Permission: tasks.PermissionTaskCancel}
	task, err := service.Cancel(c.Request.Context(), actor, id, time.Now().UTC())
	if err != nil {
		s.error(c, err)
		return
	}
	view, _ := task.Snapshot()
	c.JSON(200, gin.H{"id": view.TaskID, "object": "task", "status": taskProtocolStatus(view), "cancel": view.Cancel})
}

func taskProtocolStatus(view tasks.Snapshot) string {
	if view.Observation == tasks.ObservationExpired {
		return "expired"
	}
	switch view.Protocol {
	case tasks.StatePrepared:
		return "created"
	case tasks.StateSubmitting, tasks.StateCancelRequested:
		return "running"
	case tasks.StateSubmissionUnknown:
		return "unknown"
	default:
		return string(view.Protocol)
	}
}

func (s *Server) taskResultGrant(c *gin.Context) {
	p := c.MustGet("principal").(identity.Principal)
	id, err := tasks.ParseID(c.Param("id"))
	if err != nil {
		s.fail(c, 400, "invalid_request", "任务ID无效")
		return
	}
	service, err := s.taskService(p)
	if err != nil {
		s.fail(c, 503, "unavailable", "任务服务未就绪")
		return
	}
	actor := tasks.Actor{TenantID: tasks.ID(p.TenantID), SubjectID: tasks.ID(p.UserID), Permission: tasks.PermissionResultDownload}
	grant, err := service.IssueDownloadGrant(c.Request.Context(), actor, id, tasks.ID(uuid.NewString()), time.Now().UTC(), 15*time.Minute)
	if err != nil {
		s.error(c, err)
		return
	}
	s.TaskGrants.Store(grant.GrantID.String(), grant)
	c.JSON(200, gin.H{"grant_id": grant.GrantID, "task_id": grant.TaskID, "artifact": grant.ArtifactRef, "expires_at": grant.ExpiresAt})
}

func (s *Server) taskResult(c *gin.Context) {
	p := c.MustGet("principal").(identity.Principal)
	id, err := tasks.ParseID(c.Param("id"))
	if err != nil {
		s.fail(c, 400, "invalid_request", "任务ID无效")
		return
	}
	grantID, err := tasks.ParseID(c.Query("grant_id"))
	if err != nil {
		s.fail(c, 400, "invalid_request", "grant_id无效")
		return
	}
	value, ok := s.TaskGrants.Load(grantID.String())
	if !ok || value.(tasks.DownloadGrant).TaskID != id {
		s.fail(c, 403, "task_forbidden", "结果授权无效")
		return
	}
	service, err := s.taskService(p)
	if err != nil {
		s.fail(c, 503, "unavailable", "任务服务未就绪")
		return
	}
	actor := tasks.Actor{TenantID: tasks.ID(p.TenantID), SubjectID: tasks.ID(p.UserID), Permission: tasks.PermissionResultDownload}
	body, err := service.Download(c.Request.Context(), actor, value.(tasks.DownloadGrant), time.Now().UTC())
	if err != nil {
		s.error(c, err)
		return
	}
	c.Data(200, "application/octet-stream", body)
}
