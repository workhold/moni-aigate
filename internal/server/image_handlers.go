package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/media"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

const (
	imageProfileVersion = "11111111-1111-4111-8111-111111111111"
	imageMockModel      = "mock-image-generation"
	imageResultType     = "image_generation"
)

type imageRequestState string

const (
	imageRequestPending   imageRequestState = "pending"
	imageRequestCompleted imageRequestState = "completed"
	imageRequestFailed    imageRequestState = "failed"
	imageRequestUnknown   imageRequestState = "consumption_unknown"
)

type imageRequestRecord struct {
	mu             sync.Mutex
	tenantID       string
	userID         string
	keyID          string
	requestID      string
	idempotencyKey string
	fingerprint    [32]byte
	state          imageRequestState
	status         int
	errorCode      string
	errorMessage   string
	response       []byte
	done           chan struct{}
	finishStarted  bool
}

type imageMediaAdapter struct {
	source  provider.SynchronousMediaAdapter
	mu      sync.Mutex
	pending map[string]imageSubmission
}

type imageSubmission struct {
	profile  media.Profile
	input    media.Input
	response provider.ImageResponse
}

func newImageRuntime(source provider.SynchronousMediaAdapter) (media.Profile, *media.Orchestrator) {
	profile, err := media.NewProfile(media.ProfileInput{
		Version:     imageProfileVersion,
		VariantKey:  "default",
		Publication: media.PublicationUnpublished,
	})
	if err != nil {
		panic("invalid built-in image profile")
	}
	registry := media.NewRegistry()
	if err := registry.RegisterPublished(profile, "mock-image-adapter-v1", &imageMediaAdapter{source: source, pending: make(map[string]imageSubmission)}); err != nil {
		panic("invalid built-in image publication")
	}
	return profile, media.NewOrchestrator(registry)
}

func (a *imageMediaAdapter) Submit(ctx context.Context, in media.SubmitInput) (media.Submission, error) {
	if a == nil || a.source == nil {
		return media.Submission{}, media.ErrInvalidValue
	}
	size, err := in.Input.Size()
	if err != nil {
		return media.Submission{}, err
	}
	prompt, err := in.Input.Prompt()
	if err != nil {
		return media.Submission{}, err
	}
	idempotencyKey := in.Refs.RequestID
	response, err := a.source.GenerateImage(ctx, provider.ImageRequest{
		RequestID:      in.Refs.RequestID,
		IdempotencyKey: idempotencyKey,
		Model:          imageMockModel,
		Prompt:         prompt,
		N:              1,
		Size:           fmt.Sprintf("%dx%d", size.Width, size.Height),
		ResponseFormat: "png",
	})
	handle := in.Refs.AttemptID
	if err != nil {
		var mediaErr *provider.MediaError
		if response.Sent || errors.As(err, &mediaErr) && (mediaErr.Sent || mediaErr.Unknown) {
			return media.Submission{Handle: handle, Sent: true}, err
		}
		return media.Submission{}, err
	}
	if !response.Sent {
		return media.Submission{}, nil
	}
	a.mu.Lock()
	a.pending[handle] = imageSubmission{profile: in.Profile, input: in.Input, response: cloneImageResponse(response)}
	a.mu.Unlock()
	return media.Submission{Handle: handle, Sent: true}, nil
}

func (a *imageMediaAdapter) Stream(context.Context, media.Submission) (media.Stream, error) {
	return nil, media.ErrInvalidState
}

func (a *imageMediaAdapter) Result(_ context.Context, submission media.Submission) (media.InlineResult, error) {
	if a == nil || !submission.Sent || submission.Handle == "" {
		return media.InlineResult{}, media.ErrInvalidValue
	}
	a.mu.Lock()
	value, ok := a.pending[submission.Handle]
	a.mu.Unlock()
	if !ok {
		return media.InlineResult{}, media.ErrInvalidReference
	}
	if value.response.MediaType != "image/png" {
		return media.InlineResult{}, media.ErrInvalidResult
	}
	profileVersion, err := value.profile.Version()
	if err != nil {
		return media.InlineResult{}, err
	}
	variant, err := value.profile.VariantKey()
	if err != nil {
		return media.InlineResult{}, err
	}
	size, err := value.input.Size()
	if err != nil {
		return media.InlineResult{}, err
	}
	digest := sha256.Sum256(value.response.Data)
	raw, err := json.Marshal(map[string]string{
		"version":         "1",
		"profile_version": profileVersion,
		"variant_key":     variant,
		"format":          "png",
		"data_base64":     base64.StdEncoding.EncodeToString(value.response.Data),
		"width":           fmt.Sprintf("%d", size.Width),
		"height":          fmt.Sprintf("%d", size.Height),
		"byte_length":     fmt.Sprintf("%d", len(value.response.Data)),
		"sha256":          hex.EncodeToString(digest[:]),
	})
	if err != nil {
		return media.InlineResult{}, err
	}
	return media.ParseResult(raw, value.profile, value.input)
}

func (a *imageMediaAdapter) Usage(_ context.Context, submission media.Submission) (media.UsageEvidence, error) {
	if a == nil || !submission.Sent || submission.Handle == "" {
		return media.UsageEvidence{}, media.ErrInvalidValue
	}
	a.mu.Lock()
	_, ok := a.pending[submission.Handle]
	delete(a.pending, submission.Handle)
	a.mu.Unlock()
	if !ok {
		return media.UsageEvidence{}, media.ErrInvalidReference
	}
	return media.NewUsageEvidence(media.UsageEvidenceInput{Source: "adapter", EventID: submission.Handle + ":usage"})
}

func (a *imageMediaAdapter) Cancel(_ context.Context, submission media.Submission) error {
	if a == nil || submission.Handle == "" {
		return media.ErrInvalidValue
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.pending[submission.Handle]; !ok {
		return media.ErrInvalidReference
	}
	delete(a.pending, submission.Handle)
	return nil
}

func cloneImageResponse(response provider.ImageResponse) provider.ImageResponse {
	response.Data = append([]byte(nil), response.Data...)
	return response
}

func (s *Server) imageGeneration(c *gin.Context) {
	principal, ok := c.Get("principal")
	if !ok {
		s.fail(c, http.StatusUnauthorized, "invalid_credentials", "需要有效 API Key")
		return
	}
	p := principal.(identity.Principal)
	if s.ImageService == nil || !s.imageProfileValid() {
		s.fail(c, http.StatusServiceUnavailable, "unavailable", "图片服务未就绪")
		return
	}
	idempotencyKey, ok := imageIdempotencyKey(c)
	if !ok {
		s.fail(c, http.StatusBadRequest, "invalid_request", "需要有效的 Idempotency-Key")
		return
	}
	if !imageJSONContentType(c) {
		s.fail(c, http.StatusUnsupportedMediaType, "invalid_request", "图片请求必须使用 application/json")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, media.MaxInputJSONBytes+1))
	if err != nil || len(raw) > media.MaxInputJSONBytes {
		s.fail(c, http.StatusRequestEntityTooLarge, "request_too_large", "图片请求体超过上限")
		return
	}
	input, err := media.ParseInput(raw, s.ImageProfile)
	if err != nil {
		s.fail(c, imageInputStatus(err), imageInputCode(err), "图片请求参数无效")
		return
	}
	fingerprint := imageFingerprint(input)
	record, created, conflict := s.beginImageRequest(p, c.GetString("request_id"), idempotencyKey, fingerprint)
	if conflict {
		s.fail(c, http.StatusConflict, "idempotency_conflict", "Idempotency-Key 已用于其他图片请求")
		return
	}
	if !created {
		s.waitForImageRequest(c, record)
		s.writeImageRecord(c, record)
		return
	}
	s.executeImageRequest(c, p, input, record)
}

func (s *Server) imageGenerationResult(c *gin.Context) {
	principalValue, ok := c.Get("principal")
	if !ok {
		s.fail(c, http.StatusUnauthorized, "invalid_credentials", "需要有效 API Key")
		return
	}
	p := principalValue.(identity.Principal)
	id := c.Param("id")
	if !canonicalUUID(id) {
		s.fail(c, http.StatusNotFound, "not_found", "图片结果不存在")
		return
	}
	s.imageMu.Lock()
	record := s.imageByID[id]
	s.imageMu.Unlock()
	if record == nil {
		stored, found, err := s.loadMediaArtifactByRequest(c.Request.Context(), p, id)
		if err != nil {
			s.fail(c, http.StatusServiceUnavailable, "result_unavailable", "图片结果暂不可用")
			return
		}
		if found {
			c.Data(http.StatusOK, stored.MediaType, stored.Body)
			return
		}
	}
	if record == nil || record.tenantID != p.TenantID || record.userID != p.UserID || record.keyID != p.KeyID {
		s.fail(c, http.StatusNotFound, "not_found", "图片结果不存在")
		return
	}
	s.waitForImageRequest(c, record)
	s.writeImageRecord(c, record)
}

func (s *Server) imageProfileValid() bool {
	version, err := s.ImageProfile.Version()
	return err == nil && version == imageProfileVersion
}

func imageIdempotencyKey(c *gin.Context) (string, bool) {
	values := c.Request.Header.Values("Idempotency-Key")
	if len(values) != 1 || !safeImageReference(values[0]) {
		return "", false
	}
	return values[0], true
}

func imageJSONContentType(c *gin.Context) bool {
	values := c.Request.Header.Values("Content-Type")
	return len(values) == 1 && values[0] == "application/json"
}

func safeImageReference(value string) bool {
	if value == "" || len(value) > 200 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
			return false
		}
	}
	return true
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func imageFingerprint(input media.Input) [32]byte {
	digest, _ := input.Digest()
	return digest
}

func (s *Server) beginImageRequest(p identity.Principal, requestID, idempotency string, fingerprint [32]byte) (*imageRequestRecord, bool, bool) {
	key := p.TenantID + "\x00" + p.UserID + "\x00" + p.KeyID + "\x00" + idempotency
	s.imageMu.Lock()
	defer s.imageMu.Unlock()
	if s.imageByIdempotency == nil {
		s.imageByIdempotency = make(map[string]*imageRequestRecord)
	}
	if s.imageByID == nil {
		s.imageByID = make(map[string]*imageRequestRecord)
	}
	if record := s.imageByIdempotency[key]; record != nil {
		return record, false, record.fingerprint != fingerprint
	}
	if !canonicalUUID(requestID) {
		requestID = uuid.NewString()
	}
	record := &imageRequestRecord{tenantID: p.TenantID, userID: p.UserID, keyID: p.KeyID, requestID: requestID, idempotencyKey: idempotency, fingerprint: fingerprint, state: imageRequestPending, done: make(chan struct{})}
	s.imageByIdempotency[key] = record
	s.imageByID[requestID] = record
	return record, true, false
}

func (s *Server) executeImageRequest(c *gin.Context, p identity.Principal, input media.Input, record *imageRequestRecord) {
	defer s.writeImageRecord(c, record)
	billingRefs, err := s.reserveMediaForRequest(c.Request.Context(), p.TenantID, p.UserID, p.KeyID, record.requestID, record.idempotencyKey, "image_generation", imageMockModel, imageProfileVersion, record.fingerprint, 1)
	if err != nil {
		s.finishImageRecord(record, imageRequestFailed, http.StatusPaymentRequired, "reservation_failed", "图片预占失败", nil)
		return
	}
	if billingRefs.attemptID == "" {
		billingRefs.attemptID = uuid.NewString()
	}
	if billingRefs.enabled && billingRefs.existing && billingRefs.state != "prepared" {
		s.finishImageRecord(record, imageRequestUnknown, http.StatusConflict, "media_consumption_unknown", "图片请求已有未决供应商消费，禁止重发", nil)
		return
	}
	if billingRefs.enabled {
		if err = s.Billing.MarkMediaAttemptSending(c.Request.Context(), p.TenantID, billingRefs.attemptID); err != nil {
			s.finishImageRecord(record, imageRequestFailed, http.StatusServiceUnavailable, "attempt_unavailable", "图片尝试无法进入发送态", nil)
			return
		}
	}
	refs := media.RequestAttemptRef{TenantID: p.TenantID, RequestID: record.requestID, AttemptID: billingRefs.attemptID}
	execution, err := s.ImageService.Start(c.Request.Context(), media.ProfileSelector{Version: imageProfileVersion, VariantKey: "default"}, media.SubmitInput{Refs: refs, Profile: s.ImageProfile, Input: input})
	if err != nil {
		if execution != nil {
			_ = s.finalizeMediaRequest(c.Request.Context(), billingRefs, "unknown", "unknown", 0)
			s.finishImageRecord(record, imageRequestUnknown, http.StatusBadGateway, "media_consumption_unknown", "图片已提交但消费状态未知", nil)
			return
		}
		if finalizeErr := s.finalizeMediaRequest(c.Request.Context(), billingRefs, "failed", "missing", 0); finalizeErr != nil {
			s.finishImageRecord(record, imageRequestFailed, http.StatusServiceUnavailable, "settlement_pending", "图片账务收尾暂不可用", nil)
			return
		}
		s.finishImageRecord(record, imageRequestFailed, imageProviderStatus(err), imageProviderCode(err), "图片生成失败", nil)
		return
	}
	result, err := execution.Result(c.Request.Context())
	if err != nil {
		_ = s.finalizeMediaRequest(c.Request.Context(), billingRefs, "unknown", "unknown", 0)
		s.finishImageRecord(record, imageRequestUnknown, http.StatusBadGateway, "media_consumption_unknown", "图片已提交但消费状态未知", nil)
		return
	}
	if err := execution.ConfirmUsage(c.Request.Context(), media.UsageRefs{ObservationID: record.requestID + ":observation", SettlementID: record.requestID + ":settlement"}); err != nil {
		_ = s.finalizeMediaRequest(c.Request.Context(), billingRefs, "unknown", "unknown", 0)
		s.finishImageRecord(record, imageRequestUnknown, http.StatusBadGateway, "media_consumption_unknown", "图片已提交但消费状态未知", nil)
		return
	}
	response, err := buildImageResponseJSON(record.requestID, input, result)
	if err != nil {
		_ = s.finalizeMediaRequest(c.Request.Context(), billingRefs, "unknown", "unknown", 0)
		s.finishImageRecord(record, imageRequestUnknown, http.StatusBadGateway, "media_consumption_unknown", "图片已提交但结果状态未知", nil)
		return
	}
	if err = s.finalizeMediaRequest(c.Request.Context(), billingRefs, "completed", "confirmed_final", 1); err != nil {
		s.finishImageRecord(record, imageRequestFailed, http.StatusServiceUnavailable, "settlement_pending", "图片账务收尾暂不可用", nil)
		return
	}
	if err = s.storeMediaArtifact(c.Request.Context(), billingRefs, "image_generation", "application/json", response); err != nil {
		s.finishImageRecord(record, imageRequestFailed, http.StatusServiceUnavailable, "result_unavailable", "图片结果暂不可用", nil)
		return
	}
	s.finishImageRecord(record, imageRequestCompleted, http.StatusOK, "", "", response)
}

func (s *Server) waitForImageRequest(c *gin.Context, record *imageRequestRecord) {
	select {
	case <-record.done:
	case <-c.Request.Context().Done():
	}
}

func (s *Server) writeImageRecord(c *gin.Context, record *imageRequestRecord) {
	record.mu.Lock()
	state, status, code, message, response := record.state, record.status, record.errorCode, record.errorMessage, append([]byte(nil), record.response...)
	record.mu.Unlock()
	if state == imageRequestCompleted {
		c.Data(http.StatusOK, "application/json", response)
		return
	}
	if state == imageRequestPending {
		s.fail(c, http.StatusConflict, "request_pending", "图片请求仍在处理中")
		return
	}
	s.fail(c, status, code, message)
}

func (s *Server) finishImageRecord(record *imageRequestRecord, state imageRequestState, status int, code, message string, response []byte) {
	record.mu.Lock()
	if record.finishStarted {
		record.mu.Unlock()
		return
	}
	record.finishStarted = true
	record.state = state
	record.status = status
	record.errorCode = code
	record.errorMessage = message
	record.response = append([]byte(nil), response...)
	close(record.done)
	record.mu.Unlock()
}

func imageInputStatus(err error) int {
	if errors.Is(err, media.ErrTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func imageInputCode(err error) string {
	if errors.Is(err, media.ErrTooLarge) {
		return "request_too_large"
	}
	return "invalid_request"
}

func imageProviderStatus(errorValue error) int {
	var mediaErr *provider.MediaError
	if errors.As(errorValue, &mediaErr) && mediaErr.Status >= 400 && mediaErr.Status < 600 {
		return mediaErr.Status
	}
	return http.StatusBadGateway
}

func imageProviderCode(errorValue error) string {
	var mediaErr *provider.MediaError
	if errors.As(errorValue, &mediaErr) && mediaErr.Code != "" && !mediaErr.Unknown && !mediaErr.Sent {
		return mediaErr.Code
	}
	return "media_provider_error"
}

func buildImageResponseJSON(requestID string, input media.Input, result media.InlineResult) ([]byte, error) {
	profile, err := result.ProfileVersion()
	if err != nil {
		return nil, err
	}
	size, err := result.Size()
	if err != nil {
		return nil, err
	}
	data, err := result.Bytes()
	if err != nil {
		return nil, err
	}
	inputSize, err := input.Size()
	if err != nil || size != inputSize {
		return nil, media.ErrBindingMismatch
	}
	return json.Marshal(struct {
		ID             string `json:"id"`
		Object         string `json:"object"`
		Status         string `json:"status"`
		ProfileVersion string `json:"profile_version"`
		VariantKey     string `json:"variant_key"`
		Data           []struct {
			B64JSON  string `json:"b64_json"`
			MimeType string `json:"mime_type"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
		} `json:"data"`
	}{
		ID: requestID, Object: imageResultType, Status: "completed", ProfileVersion: profile, VariantKey: "default",
		Data: []struct {
			B64JSON  string `json:"b64_json"`
			MimeType string `json:"mime_type"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
		}{{B64JSON: base64.StdEncoding.EncodeToString(data), MimeType: "image/png", Width: size.Width, Height: size.Height}},
	})
}
