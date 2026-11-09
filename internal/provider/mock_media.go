package provider

import (
	"context"
	"errors"
	"sync"
)

// ImageRequest is the provider-neutral request after HTTP validation. It does
// not contain a price, wallet amount, credential, or provider URL.
type ImageRequest struct {
	RequestID      string
	IdempotencyKey string
	Model          string
	Prompt         string
	N              int
	Size           string
	ResponseFormat string
}

// ImageResponse contains only the inline result needed by the HTTP seam. Sent
// must be true before a caller may treat a successful response as dispatched.
type ImageResponse struct {
	Sent      bool
	MediaType string
	Width     int
	Height    int
	Data      []byte
}

// SpeechRequest is the provider-neutral request after HTTP validation.
type SpeechRequest struct {
	RequestID      string
	IdempotencyKey string
	Model          string
	Input          string
	Voice          string
	ResponseFormat string
}

// SpeechUsage is the provider usage fact for the fixed-character TTS profile.
// The server charges it only after validating final status and the request
// bound.
type SpeechUsage struct {
	Status         string
	CharacterCount int64
}

const SpeechUsageConfirmedFinal = "confirmed_final"

// SpeechResponse contains an inline audio result. Providers must not return a
// customer-visible URL through this boundary.
type SpeechResponse struct {
	Sent      bool
	MediaType string
	Format    string
	Data      []byte
	Usage     SpeechUsage
}

// SynchronousMediaAdapter is the provider seam for the synchronous media
// integration. The server owns admission, attempt, usage, and settlement.
type SynchronousMediaAdapter interface {
	GenerateImage(context.Context, ImageRequest) (ImageResponse, error)
	SynthesizeSpeech(context.Context, SpeechRequest) (SpeechResponse, error)
}

// MediaError carries only a safe classification. Sent or Unknown means the
// caller cannot safely retry the generation because provider acceptance is
// uncertain.
type MediaError struct {
	Code    string
	Status  int
	Sent    bool
	Unknown bool
}

func (e *MediaError) Error() string {
	if e == nil || e.Code == "" {
		return "media_provider_error"
	}
	return e.Code
}

func NewMediaError(code string, status int, sent, unknown bool) error {
	return &MediaError{Code: code, Status: status, Sent: sent, Unknown: unknown}
}

// MockMediaAdapter is a deterministic adapter for local transport and handler
// tests. Returned byte slices are copied before they leave the adapter.
type MockMediaAdapter struct {
	mu sync.Mutex

	GenerateImageFunc    func(context.Context, ImageRequest) (ImageResponse, error)
	SynthesizeSpeechFunc func(context.Context, SpeechRequest) (SpeechResponse, error)
	ImageResponse        ImageResponse
	ImageError           error
	SpeechResponse       SpeechResponse
	SpeechError          error
	imageRequests        []ImageRequest
	speechRequests       []SpeechRequest
}

func (m *MockMediaAdapter) GenerateImage(ctx context.Context, in ImageRequest) (ImageResponse, error) {
	if m == nil {
		return ImageResponse{}, errors.New("nil_media_adapter")
	}
	m.mu.Lock()
	m.imageRequests = append(m.imageRequests, in)
	fn, response, err := m.GenerateImageFunc, m.ImageResponse, m.ImageError
	m.mu.Unlock()
	if fn != nil {
		response, err = fn(ctx, in)
	}
	response.Data = append([]byte(nil), response.Data...)
	return response, err
}

func (m *MockMediaAdapter) SynthesizeSpeech(ctx context.Context, in SpeechRequest) (SpeechResponse, error) {
	if m == nil {
		return SpeechResponse{}, errors.New("nil_media_adapter")
	}
	m.mu.Lock()
	m.speechRequests = append(m.speechRequests, in)
	fn, response, err := m.SynthesizeSpeechFunc, m.SpeechResponse, m.SpeechError
	m.mu.Unlock()
	if fn != nil {
		response, err = fn(ctx, in)
	}
	response.Data = append([]byte(nil), response.Data...)
	return response, err
}

func (m *MockMediaAdapter) ImageRequests() []ImageRequest {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ImageRequest(nil), m.imageRequests...)
}

func (m *MockMediaAdapter) SpeechRequests() []SpeechRequest {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]SpeechRequest(nil), m.speechRequests...)
}
