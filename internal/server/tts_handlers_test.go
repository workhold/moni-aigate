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

func speechHandlerContext(body []byte, key string, principal identity.Principal) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	context.Set("principal", principal)
	return context, recorder
}

func TestSynthesizeSpeechReturnsWAVAndReplaysWithoutProviderCall(t *testing.T) {
	adapter := newDefaultMediaAdapter().(*provider.MockMediaAdapter)
	server := &Server{MediaAdapter: adapter}
	principal := identity.Principal{TenantID: "tenant-1", KeyID: "key-1", Scopes: []string{"tts-model"}}
	body := []byte(`{"model":"tts-model","input":"hello","voice":"alloy","response_format":"wav"}`)

	first, recorder := speechHandlerContext(body, "speech-key", principal)
	server.synthesizeSpeech(first)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "audio/wav" || len(recorder.Body.Bytes()) < 4 || !bytes.Equal(recorder.Body.Bytes()[0:4], []byte("RIFF")) {
		t.Fatalf("speech response status=%d content-type=%q body-prefix=%q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.Bytes())
	}
	firstBody := append([]byte(nil), recorder.Body.Bytes()...)
	if len(adapter.SpeechRequests()) != 1 {
		t.Fatalf("provider calls after first request=%d", len(adapter.SpeechRequests()))
	}

	second, recorder := speechHandlerContext(body, "speech-key", principal)
	server.synthesizeSpeech(second)
	if recorder.Code != http.StatusOK || !bytes.Equal(firstBody, recorder.Body.Bytes()) || len(adapter.SpeechRequests()) != 1 {
		t.Fatalf("replay status=%d provider-calls=%d", recorder.Code, len(adapter.SpeechRequests()))
	}
}

func TestSynthesizeSpeechRejectsUnsupportedFormatAndIdempotencyConflict(t *testing.T) {
	server := &Server{MediaAdapter: newDefaultMediaAdapter()}
	principal := identity.Principal{TenantID: "tenant-1", KeyID: "key-1", Scopes: []string{"tts-model"}}
	badBody := []byte(`{"model":"tts-model","input":"hello","voice":"alloy","response_format":"mp3"}`)
	bad, recorder := speechHandlerContext(badBody, "speech-bad", principal)
	server.synthesizeSpeech(bad)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unsupported format status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	good := []byte(`{"model":"tts-model","input":"hello","voice":"alloy","response_format":"wav"}`)
	first, _ := speechHandlerContext(good, "same-key", principal)
	server.synthesizeSpeech(first)
	conflictBody := []byte(`{"model":"tts-model","input":"different","voice":"alloy","response_format":"wav"}`)
	conflict, recorder := speechHandlerContext(conflictBody, "same-key", principal)
	server.synthesizeSpeech(conflict)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSynthesizeSpeechUnknownSubmissionCannotRetry(t *testing.T) {
	calls := 0
	adapter := &provider.MockMediaAdapter{SynthesizeSpeechFunc: func(_ context.Context, _ provider.SpeechRequest) (provider.SpeechResponse, error) {
		calls++
		return provider.SpeechResponse{Sent: true}, provider.NewMediaError("timeout", http.StatusGatewayTimeout, true, true)
	}}
	server := &Server{MediaAdapter: adapter}
	principal := identity.Principal{TenantID: "tenant-1", KeyID: "key-1", Scopes: []string{"tts-model"}}
	body := []byte(`{"model":"tts-model","input":"hello","voice":"alloy","response_format":"wav"}`)
	first, recorder := speechHandlerContext(body, "unknown-key", principal)
	server.synthesizeSpeech(first)
	second, secondRecorder := speechHandlerContext(body, "unknown-key", principal)
	server.synthesizeSpeech(second)
	if recorder.Code != http.StatusConflict || secondRecorder.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("unknown status=%d/%d calls=%d", recorder.Code, secondRecorder.Code, calls)
	}
}

func TestMediaAdapterErrorClassificationIsStable(t *testing.T) {
	var err error = provider.NewMediaError("timeout", http.StatusGatewayTimeout, true, true)
	var mediaErr *provider.MediaError
	if !errors.As(err, &mediaErr) || !mediaErr.Sent || !mediaErr.Unknown || mediaErr.Error() != "timeout" {
		t.Fatalf("unexpected media error=%v", err)
	}
}
