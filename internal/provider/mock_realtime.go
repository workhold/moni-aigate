package provider

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/realtime"
)

// MockRealtimeAdapter is the development-only provider used by the current
// realtime workflow. It returns stable protocol-shaped identities and one
// final response. It never represents supplier certification.
type MockRealtimeAdapter struct {
	mu       sync.Mutex
	opens    int
	sends    int
	receives int
	cancels  int
	response string
}

func NewMockRealtimeAdapter() *MockRealtimeAdapter { return &MockRealtimeAdapter{} }

func (m *MockRealtimeAdapter) Open(ctx context.Context, _ realtime.ProviderOpenInput) (realtime.ProviderSession, error) {
	if err := ctx.Err(); err != nil {
		return realtime.ProviderSession{}, err
	}
	m.mu.Lock()
	m.opens++
	m.mu.Unlock()
	return realtime.ProviderSession{ID: uuid.NewString()}, nil
}

func (m *MockRealtimeAdapter) Send(ctx context.Context, _ realtime.ProviderSendInput) (realtime.ProviderDispatch, error) {
	if err := ctx.Err(); err != nil {
		return realtime.ProviderDispatch{}, err
	}
	m.mu.Lock()
	m.sends++
	m.mu.Unlock()
	response := uuid.NewString()
	m.mu.Lock()
	m.response = response
	m.mu.Unlock()
	return realtime.ProviderDispatch{Sent: true, Handle: uuid.NewString(), ResponseID: response}, nil
}

func (m *MockRealtimeAdapter) Receive(ctx context.Context, _ realtime.ProviderReceiveInput) (realtime.ProviderReceiveResult, error) {
	if err := ctx.Err(); err != nil {
		return realtime.ProviderReceiveResult{}, err
	}
	m.mu.Lock()
	m.receives++
	response := m.response
	m.mu.Unlock()
	return realtime.ProviderReceiveResult{ResponseID: response, Bytes: 32, Final: true}, nil
}

func (m *MockRealtimeAdapter) Cancel(ctx context.Context, _ realtime.ProviderCancelInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.cancels++
	m.mu.Unlock()
	return nil
}

func (m *MockRealtimeAdapter) Calls() (opens, sends, receives, cancels int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opens, m.sends, m.receives, m.cancels
}

var _ realtime.ProviderRealtimeAdapter = (*MockRealtimeAdapter)(nil)
