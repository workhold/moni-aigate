package provider

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MockTaskAdapter is the deterministic video_generation adapter used by the
// development environment. It has no network or billing authority.
type MockTaskAdapter struct {
	mu    sync.Mutex
	tasks map[string]mockTask
}

type mockTask struct {
	providerReference string
	artifact          []byte
	mediaType         string
	state             string
}

func NewMockTaskAdapter() *MockTaskAdapter {
	return &MockTaskAdapter{tasks: make(map[string]mockTask)}
}

func (m *MockTaskAdapter) Submit(_ context.Context, in TaskSubmission) (TaskSubmissionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prior, ok := m.tasks[in.TaskID]; ok {
		return TaskSubmissionResult{EventID: uuid.NewString(), TaskID: in.TaskID, ProviderReference: prior.providerReference, Sequence: 1, ObservedAt: in.SubmittedAt.UTC()}, nil
	}
	ref := "mock-video/" + in.TaskID
	seed := sha256.Sum256([]byte(in.TaskID + ":video_generation"))
	m.tasks[in.TaskID] = mockTask{providerReference: ref, artifact: seed[:], mediaType: "video/mp4", state: "queued"}
	return TaskSubmissionResult{EventID: uuid.NewString(), TaskID: in.TaskID, ProviderReference: ref, Sequence: 1, ObservedAt: in.SubmittedAt.UTC()}, nil
}

func (m *MockTaskAdapter) Query(_ context.Context, in TaskQuery) (TaskObservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[in.TaskID]
	if t.state == "queued" {
		t.state = "succeeded"
		m.tasks[in.TaskID] = t
	}
	obs := TaskObservation{EventID: uuid.NewString(), TaskID: in.TaskID, Kind: "status_succeeded", Sequence: 2, ObservedAt: time.Now().UTC(), ProviderReference: t.providerReference}
	sha := sha256.Sum256(t.artifact)
	// Keep the development artifact inside the task's result window. A real
	// provider adapter must derive this from its durable artifact lease.
	obs.Artifact = &TaskArtifact{Reference: t.providerReference + "/artifact", MediaType: t.mediaType, Size: int64(len(t.artifact)), SHA256: fmtHex(sha[:]), ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
	return obs, nil
}

func (m *MockTaskAdapter) Cancel(_ context.Context, in TaskCancel) (TaskObservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[in.TaskID]
	t.state = "cancelled"
	m.tasks[in.TaskID] = t
	return TaskObservation{EventID: uuid.NewString(), TaskID: in.TaskID, Kind: "status_cancelled", Sequence: 2, ObservedAt: time.Now().UTC(), ProviderReference: t.providerReference}, nil
}

func (m *MockTaskAdapter) Download(_ context.Context, in TaskDownload) (TaskDownloadResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[in.TaskID]
	return TaskDownloadResult{MediaType: t.mediaType, Body: append([]byte(nil), t.artifact...)}, nil
}

func fmtHex(raw []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(raw)*2)
	for i, b := range raw {
		out[i*2], out[i*2+1] = hex[b>>4], hex[b&15]
	}
	return string(out)
}
