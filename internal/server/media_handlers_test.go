package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

func mediaHandlerContext(body []byte, idempotency string, principal identity.Principal) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotency)
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	context.Set("principal", principal)
	return context, recorder
}

func TestGenerateImageIdempotentReplayAndConflict(t *testing.T) {
	adapter := newDefaultMediaAdapter().(*provider.MockMediaAdapter)
	generate := adapter.GenerateImageFunc
	calls := 0
	adapter.GenerateImageFunc = func(ctx context.Context, in provider.ImageRequest) (provider.ImageResponse, error) {
		calls++
		return generate(ctx, in)
	}
	server := &Server{MediaAdapter: adapter}
	principal := identity.Principal{TenantID: "tenant-1", KeyID: "key-1", Scopes: []string{"image-model"}}
	body := []byte(`{"model":"image-model","prompt":"hello","n":1,"size":"256x256","response_format":"b64_json"}`)

	first, recorder := mediaHandlerContext(body, "image-key", principal)
	server.generateImage(first)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	firstBody := append([]byte(nil), recorder.Body.Bytes()...)
	if len(adapter.ImageRequests()) != 1 || calls != 1 {
		t.Fatalf("provider calls after first request=%d/%d", len(adapter.ImageRequests()), calls)
	}

	second, recorder := mediaHandlerContext(body, "image-key", principal)
	server.generateImage(second)
	if recorder.Code != http.StatusOK || !bytes.Equal(firstBody, recorder.Body.Bytes()) {
		t.Fatalf("replay status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(adapter.ImageRequests()) != 1 || calls != 1 {
		t.Fatalf("idempotent replay called provider %d/%d times", len(adapter.ImageRequests()), calls)
	}

	conflictBody := []byte(`{"model":"image-model","prompt":"different","n":1,"size":"256x256","response_format":"b64_json"}`)
	conflict, recorder := mediaHandlerContext(conflictBody, "image-key", principal)
	server.generateImage(conflict)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGenerateImageUnknownSubmissionCannotRetry(t *testing.T) {
	calls := 0
	adapter := &provider.MockMediaAdapter{GenerateImageFunc: func(_ context.Context, _ provider.ImageRequest) (provider.ImageResponse, error) {
		calls++
		return provider.ImageResponse{Sent: true}, provider.NewMediaError("timeout", http.StatusGatewayTimeout, true, true)
	}}
	server := &Server{MediaAdapter: adapter}
	principal := identity.Principal{TenantID: "tenant-1", KeyID: "key-1", Scopes: []string{"image-model"}}
	body := []byte(`{"model":"image-model","prompt":"hello","n":1,"size":"256x256","response_format":"b64_json"}`)

	first, recorder := mediaHandlerContext(body, "unknown-key", principal)
	server.generateImage(first)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("unknown status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	second, recorder := mediaHandlerContext(body, "unknown-key", principal)
	server.generateImage(second)
	if recorder.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("unknown replay status=%d calls=%d", recorder.Code, calls)
	}
}

func TestSynthesizeSpeechReturnsWAVAndRejectsUnsupportedFormat(t *testing.T) {
	server := &Server{MediaAdapter: newDefaultMediaAdapter()}
	principal := identity.Principal{TenantID: "tenant-1", KeyID: "key-1", Scopes: []string{"tts-model"}}
	body := []byte(`{"model":"tts-model","input":"hello","voice":"alloy","response_format":"wav"}`)
	request, recorder := mediaHandlerContext(body, "speech-key", principal)
	request.Request.URL.Path = "/v1/audio/speech"
	server.synthesizeSpeech(request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "audio/wav" || !bytes.Equal(recorder.Body.Bytes()[0:4], []byte("RIFF")) {
		t.Fatalf("speech response status=%d content-type=%q body-prefix=%q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.Bytes()[:4])
	}

	badBody := []byte(`{"model":"tts-model","input":"hello","voice":"alloy","response_format":"mp3"}`)
	bad, recorder := mediaHandlerContext(badBody, "speech-bad", principal)
	bad.Request.URL.Path = "/v1/audio/speech"
	server.synthesizeSpeech(bad)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unsupported format status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMediaAdapterErrorClassificationIsStable(t *testing.T) {
	var err error = provider.NewMediaError("timeout", http.StatusGatewayTimeout, true, true)
	var mediaErr *provider.MediaError
	if !errors.As(err, &mediaErr) || !mediaErr.Sent || !mediaErr.Unknown {
		t.Fatalf("unexpected media error=%v", err)
	}
	if got := mediaErr.Error(); got != "timeout" {
		t.Fatalf("unexpected error code=%q", got)
	}
}
