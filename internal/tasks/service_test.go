package tasks

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

type fakeTaskAdapter struct {
	mu      sync.Mutex
	submitN atomic.Int32
	query   provider.TaskObservation
	cancel  provider.TaskObservation
	body    []byte
	err     error
}

func (f *fakeTaskAdapter) Submit(context.Context, provider.TaskSubmission) (provider.TaskSubmissionResult, error) {
	f.submitN.Add(1)
	if f.err != nil {
		return provider.TaskSubmissionResult{}, f.err
	}
	return provider.TaskSubmissionResult{EventID: testID(101).String(), TaskID: testID(1).String(), ProviderReference: "provider/task-1", Sequence: 1, ObservedAt: testBase.Add(time.Second)}, nil
}
func (f *fakeTaskAdapter) Query(context.Context, provider.TaskQuery) (provider.TaskObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.query, f.err
}
func (f *fakeTaskAdapter) Cancel(context.Context, provider.TaskCancel) (provider.TaskObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancel, f.err
}
func (f *fakeTaskAdapter) Download(context.Context, provider.TaskDownload) (provider.TaskDownloadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return provider.TaskDownloadResult{Body: append([]byte(nil), f.body...)}, f.err
}

func serviceFixture(t *testing.T, adapter provider.TaskAdapter) (*Service, *MemoryStore, SubmitInput, References, Actor) {
	t.Helper()
	store := NewMemoryStore()
	worker := Actor{TenantID: testID(2), SubjectID: testID(4), Permission: PermissionTaskObserve}
	service, err := NewService(ServiceOptions{Store: store, Adapter: adapter, NewID: func() ID { return testID(102) }, Worker: worker})
	if err != nil {
		t.Fatal(err)
	}
	input := SubmitInput{TaskID: testID(1), Owner: Owner{TenantID: testID(2), SubjectID: testID(3)}, IdempotencyKey: "async-1", RequestDigest: testDigest(1), CreatedAt: testBase, SubmitBy: testBase.Add(time.Minute), ObserveUntil: testBase.Add(time.Hour), ResultUntil: testBase.Add(2 * time.Hour), MaxArtifactBytes: 1 << 20}
	refs := References{RequestID: testID(5), ReservationID: testID(6), AttemptID: testID(7), SourceID: testID(8)}
	return service, store, input, refs, Actor{TenantID: testID(2), SubjectID: testID(3), Permission: PermissionTaskRead | PermissionTaskObserve | PermissionTaskCancel | PermissionResultDownload}
}

func TestServiceUnknownSubmitIsPersistedAndNeverResubmitted(t *testing.T) {
	adapter := &fakeTaskAdapter{err: errors.New("connection lost")}
	service, store, input, refs, owner := serviceFixture(t, adapter)
	if _, err := service.Create(context.Background(), input, refs); err != nil {
		t.Fatal(err)
	}
	task, err := service.Submit(context.Background(), input.TaskID, testBase.Add(time.Second))
	if !errors.Is(err, adapter.err) || !task.SubmissionUnknown() {
		t.Fatalf("unknown submit not preserved: task=%v err=%v", task, err)
	}
	if _, err := service.Submit(context.Background(), input.TaskID, testBase.Add(2*time.Second)); !errors.Is(err, ErrNoResubmit) {
		t.Fatalf("unknown task was resubmitted: %v", err)
	}
	if replay, err := service.SubmitByIdempotency(context.Background(), input.Owner, input.IdempotencyKey, input.RequestDigest, testBase.Add(2*time.Second)); err != nil || !replay.SubmissionUnknown() {
		t.Fatalf("idempotent replay changed unknown state: task=%v err=%v", replay, err)
	}
	if _, err := service.SubmitByIdempotency(context.Background(), input.Owner, input.IdempotencyKey, testDigest(2), testBase.Add(2*time.Second)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict was not enforced: %v", err)
	}
	if adapter.submitN.Load() != 1 {
		t.Fatalf("submit calls=%d", adapter.submitN.Load())
	}
	got, storedRefs, err := store.Snapshot(context.Background(), input.TaskID)
	if err != nil || !got.SubmissionUnknown() || storedRefs != refs {
		t.Fatalf("snapshot=%v refs=%v err=%v", got, storedRefs, err)
	}
	_ = owner
}

func TestServiceAcceptedObservationAndEventReplay(t *testing.T) {
	adapter := &fakeTaskAdapter{}
	service, store, input, refs, owner := serviceFixture(t, adapter)
	if _, err := service.Create(context.Background(), input, refs); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), input.TaskID, testBase.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	adapter.query = provider.TaskObservation{EventID: testID(103).String(), TaskID: input.TaskID.String(), Kind: string(EventStatusSucceeded), Sequence: 2, ObservedAt: testBase.Add(2 * time.Second), ProviderReference: "provider/task-1"}
	adapter.mu.Unlock()
	body := []byte("video")
	sum := sha256.Sum256(body)
	adapter.mu.Lock()
	adapter.query.Artifact = &provider.TaskArtifact{Reference: "object/video-1", MediaType: "video/mp4", Size: int64(len(body)), SHA256: testDigestHex(sum), ExpiresAt: testBase.Add(time.Hour)}
	adapter.body = body
	adapter.mu.Unlock()
	if _, err := service.Observe(context.Background(), input.TaskID, testBase.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	grant, err := service.IssueDownloadGrant(context.Background(), owner, input.TaskID, testID(104), testBase.Add(4*time.Second), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Download(context.Background(), owner, grant, testBase.Add(5*time.Second))
	if err != nil || string(got) != string(body) {
		t.Fatalf("download=%q err=%v", got, err)
	}
	if _, err := service.Observe(context.Background(), input.TaskID, testBase.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot, storedRefs, err := store.Snapshot(context.Background(), input.TaskID)
	status, statusErr := snapshot.Snapshot()
	if err != nil || statusErr != nil || status.Protocol != StateSucceeded || storedRefs != refs {
		t.Fatalf("snapshot=%v refs=%v err=%v", snapshot, storedRefs, err)
	}
}

func testDigestHex(sum [32]byte) string {
	var d Digest
	copy(d[:], sum[:])
	return d.String()
}

func TestMemoryStoreTransactionAndSettlementBindingAreAtomic(t *testing.T) {
	service, store, input, refs, _ := serviceFixture(t, &fakeTaskAdapter{})
	if _, err := service.Create(context.Background(), input, refs); err != nil {
		t.Fatal(err)
	}
	bad := testID(105)
	if err := store.WithTask(context.Background(), input.TaskID, func(tx TaskTx) error {
		if err := tx.BindSettlement(bad); err != nil {
			return err
		}
		return errors.New("rollback")
	}); err == nil {
		t.Fatal("failed transaction committed")
	}
	_, got, err := store.Snapshot(context.Background(), input.TaskID)
	if err != nil || got.SettlementID != "" {
		t.Fatalf("rollback leaked settlement: refs=%v err=%v", got, err)
	}
	if err := service.BindSettlement(context.Background(), input.TaskID, bad); err != nil {
		t.Fatal(err)
	}
	if err := service.BindSettlement(context.Background(), input.TaskID, testID(106)); !errors.Is(err, ErrSettlementBound) {
		t.Fatalf("second settlement accepted: %v", err)
	}
}

func TestMemoryStoreLeaseClaimIsSingleWinnerUnderRace(t *testing.T) {
	service, _, input, refs, _ := serviceFixture(t, &fakeTaskAdapter{})
	if _, err := service.Create(context.Background(), input, refs); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if _, _, _, err := service.Claim(context.Background(), WorkSubmit, testID(20+n), testID(120+n), testBase.Add(time.Second), time.Minute); err == nil {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("lease winners=%d", wins.Load())
	}
}
