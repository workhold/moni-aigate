package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/tasks"
)

type videoTaskFixture struct {
	taskID, requestID, attemptID string
}

func taskOwner(t *testing.T, f *gatewayFixture) string {
	t.Helper()
	var owner string
	if err := f.pool.QueryRow(context.Background(), `SELECT owner_id FROM api_keys WHERE tenant_id=$1 AND id=$2`, f.tenant, f.key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

func taskPrincipal(t *testing.T, f *gatewayFixture) identity.Principal {
	return identity.Principal{TenantID: f.tenant, UserID: taskOwner(t, f), KeyID: f.key}
}

func createVideoTask(t *testing.T, f *gatewayFixture, idempotency string) videoTaskFixture {
	t.Helper()
	response := f.request(t, context.Background(), false, false)
	requestID := response.Header.Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("gateway request did not return a request id")
	}
	_ = response.Body.Close()
	var input struct {
		RequestID      string `json:"id"`
		ReservationID  string `json:"reservation_id"`
		AttemptID      string `json:"attempt_id"`
		SourceID       string
		ModelID        string `json:"model_id"`
		ProfileVersion string `json:"profile_version"`
		SchemaID       string `json:"schema_id"`
		SchemaDigest   string `json:"schema_digest"`
	}
	owner := taskOwner(t, f)
	session, csrf := authSession(t, f.server, f.pool, owner, f.tenant, false, true)
	detail := authHTTP(t, f.server, http.MethodGet, "/api/requests/"+requestID, session, csrf, f.server.Config.Origin, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("request detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	if err := json.Unmarshal(authData(t, detail), &input); err != nil {
		t.Fatal(err)
	}
	funding := authHTTP(t, f.server, http.MethodGet, "/api/requests/"+requestID+"/funding", session, csrf, f.server.Config.Origin, nil)
	if funding.Code != http.StatusOK {
		t.Fatalf("request funding status=%d body=%s", funding.Code, funding.Body.String())
	}
	var fundingEnvelope struct {
		Data []struct {
			LotID string `json:"lot_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(funding.Body.Bytes(), &fundingEnvelope); err != nil || len(fundingEnvelope.Data) == 0 || fundingEnvelope.Data[0].LotID == "" {
		t.Fatalf("request funding did not expose a source lot: %s", funding.Body.String())
	}
	input.SourceID = fundingEnvelope.Data[0].LotID
	body, err := json.Marshal(map[string]any{
		"request_id": input.RequestID, "reservation_id": input.ReservationID, "attempt_id": input.AttemptID,
		"source_id": input.SourceID, "model_id": input.ModelID, "profile_version": input.ProfileVersion,
		"schema_id": input.SchemaID, "schema_digest": input.SchemaDigest, "max_artifact_bytes": 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	post := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/tasks", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotency)
		response, err := f.http.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response = post()
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("task creation status=%d body=%s", response.StatusCode, raw)
	}
	var created struct {
		ID           string `json:"id"`
		ProviderMode string `json:"provider_mode"`
	}
	if err = json.Unmarshal(raw, &created); err != nil || created.ID == "" || created.ProviderMode != "mock" {
		t.Fatalf("invalid task creation response=%s err=%v", raw, err)
	}
	response = post()
	if response.StatusCode != http.StatusAccepted {
		raw, _ = io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("task replay status=%d body=%s", response.StatusCode, raw)
	}
	response.Body.Close()
	return videoTaskFixture{taskID: created.ID, requestID: input.RequestID, attemptID: input.AttemptID}
}

func TestVideoTaskUsesCurrentMediaWalletAcrossSubmitAndTerminalObservation(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	fixture := createVideoTask(t, f, "video-wallet-success")
	ctx := context.Background()
	var reservations, attempts int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_reservations WHERE request_id=$1),(SELECT count(*) FROM media_attempts WHERE request_id=$1)`, fixture.requestID).Scan(&reservations, &attempts); err != nil {
		t.Fatal(err)
	}
	if reservations != 1 || attempts != 1 {
		t.Fatalf("task media reserve facts reservations=%d attempts=%d", reservations, attempts)
	}
	service, err := f.server.taskService(taskPrincipal(t, f))
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := tasks.ParseID(fixture.taskID)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/tasks/"+fixture.taskID+"/submit", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.token)
	response, err := f.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("task submit status=%d", response.StatusCode)
	}
	if _, err = service.Status(ctx, tasks.Actor{TenantID: tasks.ID(f.tenant), SubjectID: tasks.ID(taskOwner(t, f)), Permission: tasks.PermissionTaskRead}, taskID); err != nil {
		t.Fatal(err)
	}
	var delivery string
	if err = f.pool.QueryRow(ctx, `SELECT delivery_state FROM media_attempts WHERE tenant_id=$1 AND request_id=$2`, f.tenant, fixture.requestID).Scan(&delivery); err != nil {
		t.Fatal(err)
	}
	if delivery != "sending" {
		t.Fatalf("submit did not mark media attempt sending: %s", delivery)
	}
	if _, err = service.Observe(ctx, taskID, time.Now().UTC()); err != nil {
		t.Fatalf("observe error=%T %v", err, err)
	}
	var status string
	var charged int
	if err = f.pool.QueryRow(ctx, `
SELECT (SELECT delivery_state FROM media_attempts WHERE tenant_id=$1 AND request_id=$2),
       (SELECT status FROM media_settlements WHERE tenant_id=$1 AND request_id=$2),
       (SELECT charged_amount FROM media_settlements WHERE tenant_id=$1 AND request_id=$2)`, f.tenant, fixture.requestID).Scan(&delivery, &status, &charged); err != nil {
		t.Fatal(err)
	}
	if delivery != "failed" || status != "released" || charged != 0 {
		t.Fatalf("video terminal billing delivery=%s status=%s charged=%d", delivery, status, charged)
	}
	if _, err = service.Observe(ctx, taskID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var settlements, observations int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_settlements WHERE tenant_id=$1 AND request_id=$2),(SELECT count(*) FROM media_observations WHERE tenant_id=$1 AND request_id=$2)`, f.tenant, fixture.requestID).Scan(&settlements, &observations); err != nil {
		t.Fatal(err)
	}
	if settlements != 1 || observations != 1 {
		t.Fatalf("terminal replay duplicated financial facts settlements=%d observations=%d", settlements, observations)
	}
}

func TestVideoTaskArtifactSurvivesStoreAndProviderRestart(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	fixture := createVideoTask(t, f, "video-artifact-restart")
	ctx := context.Background()
	principal := taskPrincipal(t, f)
	service, err := f.server.taskService(principal)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := tasks.ParseID(fixture.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(ctx, taskID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Observe(ctx, taskID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	actor := tasks.Actor{TenantID: tasks.ID(f.tenant), SubjectID: tasks.ID(principal.UserID), Permission: tasks.PermissionTaskRead | tasks.PermissionTaskObserve | tasks.PermissionResultDownload}
	grant, err := service.IssueDownloadGrant(ctx, actor, taskID, tasks.ID(uuid.NewString()), time.Now().UTC(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Download(ctx, actor, grant, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var storedBytes int
	if err = f.pool.QueryRow(ctx, `SELECT octet_length(body) FROM media_task_artifacts WHERE tenant_id=$1 AND task_id=$2`, f.tenant, fixture.taskID).Scan(&storedBytes); err != nil {
		t.Fatal(err)
	}
	if storedBytes != len(first) {
		t.Fatalf("stored artifact bytes=%d first download=%d", storedBytes, len(first))
	}
	store, err := tasks.NewPostgresStore(f.pool, "")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := tasks.NewService(tasks.ServiceOptions{Store: store, Adapter: rejectedVideoAdapter{}, NewID: func() tasks.ID { return tasks.ID(uuid.NewString()) }, Worker: actor})
	if err != nil {
		t.Fatal(err)
	}
	newGrant, err := restarted.IssueDownloadGrant(ctx, actor, taskID, tasks.ID(uuid.NewString()), time.Now().UTC(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Download(ctx, actor, newGrant, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("restart download differed from persisted artifact")
	}
}

func TestVideoTaskObservationSurvivesWorkerProviderRestart(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	fixture := createVideoTask(t, f, "video-worker-restart")
	ctx := context.Background()
	principal := taskPrincipal(t, f)
	firstService, err := f.server.taskService(principal)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := tasks.ParseID(fixture.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = firstService.Submit(ctx, taskID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	store, err := tasks.NewPostgresStore(f.pool, "")
	if err != nil {
		t.Fatal(err)
	}
	restartedServer := &Server{Pool: f.pool, Billing: f.server.Billing, TaskStore: store}
	adapter, err := restartedServer.newTaskMediaAdapter(principal, provider.NewMockTaskAdapter())
	if err != nil {
		t.Fatal(err)
	}
	actor := tasks.Actor{TenantID: tasks.ID(f.tenant), SubjectID: tasks.ID(principal.UserID), Permission: tasks.PermissionTaskObserve | tasks.PermissionTaskRead}
	restartedService, err := tasks.NewService(tasks.ServiceOptions{Store: store, Adapter: adapter, NewID: func() tasks.ID { return tasks.ID(uuid.NewString()) }, Worker: actor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restartedService.Observe(ctx, taskID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var attempts, settlements int
	if err = f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_attempts WHERE tenant_id=$1 AND request_id=$2),(SELECT count(*) FROM media_settlements WHERE tenant_id=$1 AND request_id=$2)`, f.tenant, fixture.requestID).Scan(&attempts, &settlements); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || settlements != 1 {
		t.Fatalf("worker restart duplicated financial facts attempts=%d settlements=%d", attempts, settlements)
	}
}

type rejectedVideoAdapter struct{}

func (rejectedVideoAdapter) ProviderMode() provider.ProviderMode { return provider.ProviderModeMock }

func (rejectedVideoAdapter) Submit(context.Context, provider.TaskSubmission) (provider.TaskSubmissionResult, error) {
	return provider.TaskSubmissionResult{}, errors.New("provider connection failed")
}
func (rejectedVideoAdapter) Query(context.Context, provider.TaskQuery) (provider.TaskObservation, error) {
	return provider.TaskObservation{}, errors.New("not expected")
}
func (rejectedVideoAdapter) Cancel(context.Context, provider.TaskCancel) (provider.TaskObservation, error) {
	return provider.TaskObservation{}, errors.New("not expected")
}
func (rejectedVideoAdapter) Download(context.Context, provider.TaskDownload) (provider.TaskDownloadResult, error) {
	return provider.TaskDownloadResult{}, errors.New("not expected")
}

type terminalVideoAdapter struct {
	kind string
}

func (terminalVideoAdapter) ProviderMode() provider.ProviderMode { return provider.ProviderModeMock }

func (a terminalVideoAdapter) Submit(_ context.Context, input provider.TaskSubmission) (provider.TaskSubmissionResult, error) {
	return provider.TaskSubmissionResult{Mode: provider.ProviderModeMock, EventID: uuid.NewString(), TaskID: input.TaskID, ProviderReference: "mock-video/" + input.TaskID, Sequence: 1, ObservedAt: input.SubmittedAt.UTC()}, nil
}

func (a terminalVideoAdapter) Query(_ context.Context, input provider.TaskQuery) (provider.TaskObservation, error) {
	return provider.TaskObservation{Mode: provider.ProviderModeMock, EventID: uuid.NewString(), TaskID: input.TaskID, Kind: a.kind, Sequence: 2, ObservedAt: time.Now().Add(-time.Second).UTC(), ProviderReference: input.ProviderReference, FailureCode: "provider_error"}, nil
}

func (a terminalVideoAdapter) Cancel(_ context.Context, input provider.TaskCancel) (provider.TaskObservation, error) {
	return provider.TaskObservation{Mode: provider.ProviderModeMock, EventID: uuid.NewString(), TaskID: input.TaskID, Kind: "status_cancelled", Sequence: 2, ObservedAt: time.Now().Add(-time.Second).UTC(), ProviderReference: input.ProviderReference}, nil
}

func (terminalVideoAdapter) Download(context.Context, provider.TaskDownload) (provider.TaskDownloadResult, error) {
	return provider.TaskDownloadResult{}, errors.New("not expected")
}

func TestVideoTaskFailureAndCancelReleaseMedia(t *testing.T) {
	for _, test := range []struct {
		name, kind string
		cancel     bool
	}{
		{name: "failure", kind: "status_failed"},
		{name: "cancel", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
			fixture := createVideoTask(t, f, "video-wallet-"+test.name)
			principal := taskPrincipal(t, f)
			adapter, err := f.server.newTaskMediaAdapter(principal, terminalVideoAdapter{kind: test.kind})
			if err != nil {
				t.Fatal(err)
			}
			actor := tasks.Actor{TenantID: tasks.ID(f.tenant), SubjectID: tasks.ID(principal.UserID), Permission: tasks.PermissionTaskObserve | tasks.PermissionTaskCancel | tasks.PermissionTaskRead}
			service, err := tasks.NewService(tasks.ServiceOptions{Store: f.server.TaskStore, Adapter: adapter, NewID: func() tasks.ID { return tasks.ID(uuid.NewString()) }, Worker: actor})
			if err != nil {
				t.Fatal(err)
			}
			taskID, err := tasks.ParseID(fixture.taskID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Submit(context.Background(), taskID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if test.cancel {
				if _, err = service.Cancel(context.Background(), actor, taskID, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			} else if _, err = service.Observe(context.Background(), taskID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			var attemptState, settlementStatus string
			var charged int
			if err = f.pool.QueryRow(context.Background(), `
SELECT (SELECT delivery_state FROM media_attempts WHERE tenant_id=$1 AND request_id=$2),
       (SELECT status FROM media_settlements WHERE tenant_id=$1 AND request_id=$2),
       (SELECT charged_amount FROM media_settlements WHERE tenant_id=$1 AND request_id=$2)`, f.tenant, fixture.requestID).Scan(&attemptState, &settlementStatus, &charged); err != nil {
				t.Fatal(err)
			}
			if attemptState != "failed" || settlementStatus != "released" || charged != 0 {
				t.Fatalf("terminal release attempt=%s settlement=%s charged=%d", attemptState, settlementStatus, charged)
			}
		})
	}
}

func TestVideoTaskUnknownSubmissionReleasesMediaAndPreventsResubmit(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	fixture := createVideoTask(t, f, "video-wallet-unknown")
	principal := taskPrincipal(t, f)
	adapter, err := f.server.newTaskMediaAdapter(principal, rejectedVideoAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := tasks.NewService(tasks.ServiceOptions{
		Store: f.server.TaskStore, Adapter: adapter, NewID: func() tasks.ID { return tasks.ID(uuid.NewString()) },
		Worker: tasks.Actor{TenantID: tasks.ID(f.tenant), SubjectID: tasks.ID(principal.UserID), Permission: tasks.PermissionTaskObserve},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := tasks.ParseID(fixture.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(context.Background(), taskID, time.Now().UTC()); err == nil {
		t.Fatal("provider submission failure was hidden")
	}
	var attemptState, settlementStatus string
	var charged int
	if err = f.pool.QueryRow(context.Background(), `
SELECT (SELECT delivery_state FROM media_attempts WHERE tenant_id=$1 AND request_id=$2),
       (SELECT status FROM media_settlements WHERE tenant_id=$1 AND request_id=$2),
       (SELECT charged_amount FROM media_settlements WHERE tenant_id=$1 AND request_id=$2)`, f.tenant, fixture.requestID).Scan(&attemptState, &settlementStatus, &charged); err != nil {
		t.Fatal(err)
	}
	if attemptState != "unknown" || settlementStatus != "released" || charged != 0 {
		t.Fatalf("unknown submission did not release media attempt=%s settlement=%s charged=%d", attemptState, settlementStatus, charged)
	}
	if _, err = service.Submit(context.Background(), taskID, time.Now().UTC()); !errors.Is(err, tasks.ErrNoResubmit) {
		t.Fatalf("unknown task became resubmittable: %v", err)
	}
	var settlements int
	if err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM media_settlements WHERE tenant_id=$1 AND request_id=$2`, f.tenant, fixture.requestID).Scan(&settlements); err != nil {
		t.Fatal(err)
	}
	if settlements != 1 {
		t.Fatalf("unknown replay duplicated settlement count=%d", settlements)
	}
}
