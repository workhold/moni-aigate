package provider

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/realtime"
)

// MockRealtimeAdapter is the deterministic provider boundary for the realtime
// MVP. It records one provider session and one response per turn and has no
// network or billing authority.
type MockRealtimeAdapter struct {
	mu sync.Mutex

	OpenFunc    func(context.Context, RealtimeOpenInput) (RealtimeSession, error)
	SendFunc    func(context.Context, RealtimeSendInput) (RealtimeDispatch, error)
	ReceiveFunc func(context.Context, RealtimeReceiveInput) (RealtimeReceiveResult, error)
	CancelFunc  func(context.Context, RealtimeCancelInput) error

	openCalls    []RealtimeOpenInput
	sendCalls    []RealtimeSendInput
	receiveCalls []RealtimeReceiveInput
	cancelCalls  []RealtimeCancelInput
	responses    map[string]string
}

func NewMockRealtimeAdapter() *MockRealtimeAdapter {
	return &MockRealtimeAdapter{responses: make(map[string]string)}
}

func (m *MockRealtimeAdapter) Open(ctx context.Context, in RealtimeOpenInput) (RealtimeSession, error) {
	if m == nil {
		return RealtimeSession{}, errors.New("nil_realtime_adapter")
	}
	m.mu.Lock()
	m.openCalls = append(m.openCalls, in)
	fn := m.OpenFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return RealtimeSession{ID: "mock-realtime-session-" + in.SessionID, Handle: uuid.NewString()}, nil
}

func (m *MockRealtimeAdapter) Send(ctx context.Context, in RealtimeSendInput) (RealtimeDispatch, error) {
	if m == nil {
		return RealtimeDispatch{}, errors.New("nil_realtime_adapter")
	}
	copyInput := in
	copyInput.Payload = append([]byte(nil), in.Payload...)
	m.mu.Lock()
	m.sendCalls = append(m.sendCalls, copyInput)
	fn := m.SendFunc
	if m.responses == nil {
		m.responses = make(map[string]string)
	}
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	handle := "mock-realtime-handle-" + in.TurnID
	responseID := "mock-realtime-response-" + in.TurnID
	m.mu.Lock()
	m.responses[handle] = responseID
	m.mu.Unlock()
	return RealtimeDispatch{Sent: true, Handle: handle, ResponseID: responseID}, nil
}

func (m *MockRealtimeAdapter) Receive(ctx context.Context, in RealtimeReceiveInput) (RealtimeReceiveResult, error) {
	if m == nil {
		return RealtimeReceiveResult{}, errors.New("nil_realtime_adapter")
	}
	m.mu.Lock()
	m.receiveCalls = append(m.receiveCalls, in)
	fn := m.ReceiveFunc
	responseID := m.responses[in.ProviderHandle]
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	if responseID == "" && strings.HasPrefix(in.ProviderHandle, "mock-realtime-handle-") {
		responseID = "mock-realtime-response-" + strings.TrimPrefix(in.ProviderHandle, "mock-realtime-handle-")
	}
	if responseID == "" {
		return RealtimeReceiveResult{}, errors.New("mock_realtime_response_not_found")
	}
	return RealtimeReceiveResult{ResponseID: responseID, Bytes: 1, Final: true}, nil
}

func (m *MockRealtimeAdapter) Cancel(ctx context.Context, in RealtimeCancelInput) error {
	if m == nil {
		return errors.New("nil_realtime_adapter")
	}
	m.mu.Lock()
	m.cancelCalls = append(m.cancelCalls, in)
	fn := m.CancelFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return nil
}

func (m *MockRealtimeAdapter) OpenCalls() []RealtimeOpenInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RealtimeOpenInput(nil), m.openCalls...)
}

func (m *MockRealtimeAdapter) SendCalls() []RealtimeSendInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RealtimeSendInput(nil), m.sendCalls...)
}

func (m *MockRealtimeAdapter) ReceiveCalls() []RealtimeReceiveInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RealtimeReceiveInput(nil), m.receiveCalls...)
}

func (m *MockRealtimeAdapter) CancelCalls() []RealtimeCancelInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RealtimeCancelInput(nil), m.cancelCalls...)
}

var _ realtime.ProviderRealtimeAdapter = (*MockRealtimeAdapter)(nil)
