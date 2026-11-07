package tasks

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var testBase = time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)

func testID(n int) ID {
	id, err := ParseID(fmt.Sprintf("00000000-0000-4000-8000-%012d", n))
	if err != nil {
		panic(err)
	}
	return id
}

func testDigest(n byte) Digest {
	var d Digest
	d[0] = n
	return d
}

func testTask(t *testing.T) (Task, Actor, Actor) {
	t.Helper()
	task, err := New(SubmitInput{
		TaskID:           testID(1),
		Owner:            Owner{TenantID: testID(2), SubjectID: testID(3)},
		IdempotencyKey:   "video-20261108-1",
		RequestDigest:    testDigest(1),
		CreatedAt:        testBase,
		SubmitBy:         testBase.Add(2 * time.Minute),
		ObserveUntil:     testBase.Add(2 * time.Hour),
		ResultUntil:      testBase.Add(4 * time.Hour),
		MaxArtifactBytes: 10 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := Actor{
		TenantID:   testID(2),
		SubjectID:  testID(3),
		Permission: PermissionTaskRead | PermissionTaskObserve | PermissionTaskCancel | PermissionResultDownload,
	}
	worker := Actor{TenantID: testID(2), SubjectID: testID(4), Permission: PermissionTaskObserve}
	return task, owner, worker
}

func event(t *testing.T, id, task ID, kind EventKind, seq uint64, ref string, artifact *Artifact, failure string) Event {
	t.Helper()
	e, err := NewEvent(EventInput{
		Version:           "1",
		EventID:           id,
		TaskID:            task,
		Kind:              kind,
		Sequence:          seq,
		ObservedAt:        testBase.Add(time.Duration(seq) * time.Second),
		ProviderReference: ref,
		Artifact:          artifact,
		FailureCode:       failure,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func resultArtifact() *Artifact {
	return &Artifact{
		Reference: "object/video-1",
		MediaType: "video/mp4",
		Size:      1024,
		SHA256:    testDigest(9),
		ExpiresAt: testBase.Add(3 * time.Hour),
	}
}

func TestSubmitUnknownNeverResubmits(t *testing.T) {
	task, owner, worker := testTask(t)
	started, err := task.BeginSubmit(testBase.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	unknown := event(t, testID(10), started.ID(), EventSubmissionUnknown, 1, "", nil, "")
	unknownState, err := started.Observe(worker, testBase.Add(2*time.Second), unknown)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := unknownState.Status(owner)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Protocol != StateSubmissionUnknown || !unknownState.SubmissionUnknown() {
		t.Fatalf("unexpected state: %+v", snap)
	}
	if _, err := unknownState.BeginSubmit(testBase.Add(3 * time.Second)); !errors.Is(err, ErrNoResubmit) {
		t.Fatalf("expected no-resubmit error, got %v", err)
	}
	replayed, err := unknownState.ReplaySubmit("video-20261108-1", testDigest(1))
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID() != unknownState.ID() {
		t.Fatal("same submit replay changed task identity")
	}
	if _, err := unknownState.ReplaySubmit("video-20261108-1", testDigest(2)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	if _, err := unknownState.ReplaySubmit("another-key", testDigest(1)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected key conflict, got %v", err)
	}
}

func TestLifecycleCancelRaceAndSingleTerminal(t *testing.T) {
	task, owner, worker := testTask(t)
	var err error
	task, err = task.BeginSubmit(testBase.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.Observe(worker, testBase.Add(2*time.Second), event(t, testID(11), task.ID(), EventSubmissionAccepted, 1, "provider-task-1", nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.RequestCancel(owner, testBase.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := task.Status(owner)
	if snap.Protocol != StateCancelRequested || snap.Cancel != CancelRequested {
		t.Fatalf("cancel request not visible: %+v", snap)
	}
	finished := event(t, testID(12), task.ID(), EventStatusSucceeded, 2, "provider-task-1", resultArtifact(), "")
	task, err = task.Observe(worker, testBase.Add(4*time.Second), finished)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = task.Status(owner)
	if snap.Protocol != StateSucceeded || snap.Cancel != CancelRejected || snap.Artifact != ArtifactAvailable {
		t.Fatalf("unexpected success/cancel race result: %+v", snap)
	}
	// The exact event is an idempotent replay, while a second terminal fact is
	// rejected and cannot replace the original settlement identity.
	if replay, err := task.Observe(worker, testBase.Add(5*time.Second), finished); err != nil || replay.ID() != task.ID() {
		t.Fatalf("same terminal replay was not idempotent: %v", err)
	}
	failed := event(t, testID(13), task.ID(), EventStatusFailed, 3, "provider-task-1", nil, "provider_error")
	if _, err := task.Observe(worker, testBase.Add(6*time.Second), failed); !errors.Is(err, ErrAlreadyTerminal) {
		t.Fatalf("expected terminal conflict, got %v", err)
	}
}

func TestDownloadIsAuthorizedBoundedAndReadOnly(t *testing.T) {
	task, owner, worker := testTask(t)
	var err error
	task, err = task.BeginSubmit(testBase.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.Observe(worker, testBase.Add(2*time.Second), event(t, testID(20), task.ID(), EventSubmissionAccepted, 1, "provider-task-2", nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.Observe(worker, testBase.Add(3*time.Second), event(t, testID(21), task.ID(), EventStatusSucceeded, 2, "provider-task-2", resultArtifact(), ""))
	if err != nil {
		t.Fatal(err)
	}
	grant, err := task.IssueDownloadGrant(owner, testID(30), testBase.Add(4*time.Second), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := task.Status(owner)
	got, err := task.Download(owner, grant, testBase.Add(5*time.Second))
	if err != nil || got.Reference != "object/video-1" || got.Size != 1024 {
		t.Fatalf("download authorization failed: artifact=%+v err=%v", got, err)
	}
	after, _ := task.Status(owner)
	if before.LastSequence != after.LastSequence || after.Protocol != StateSucceeded {
		t.Fatal("download changed task state")
	}
	wrongTenant := owner
	wrongTenant.TenantID = testID(99)
	if _, err := task.Download(wrongTenant, grant, testBase.Add(5*time.Second)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected tenant isolation, got %v", err)
	}
	late, err := task.Expire(testBase.Add(3 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := late.Download(owner, grant, testBase.Add(3*time.Hour)); !errors.Is(err, ErrArtifactExpired) {
		t.Fatalf("expected expired artifact, got %v", err)
	}
}

func TestStrictEventParserAndCanonicalDigest(t *testing.T) {
	e := event(t, testID(40), testID(1), EventStatusRunning, 1, "provider-task-3", nil, "")
	raw, err := e.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := parsed.CanonicalJSON()
	if err != nil || !bytes.Equal(raw, canonical) || parsed.Digest() != e.Digest() {
		t.Fatalf("canonical roundtrip changed event: raw=%s canonical=%s err=%v", raw, canonical, err)
	}
	reordered := []byte(`{"failure_code":null,"artifact":null,"provider_reference":"provider-task-3","observed_at":"2026-11-08T00:00:01Z","sequence":"1","kind":"status_running","task_id":"00000000-0000-4000-8000-000000000001","event_id":"00000000-0000-4000-8000-000000000040","version":"1"}`)
	if reorderedEvent, err := ParseEvent(reordered); err != nil || reorderedEvent.Digest() != e.Digest() {
		t.Fatalf("reordered event should canonicalize: event=%+v err=%v", reorderedEvent, err)
	}
	bad := []string{
		`{"version":"1","event_id":"00000000-0000-4000-8000-000000000040","event_id":"00000000-0000-4000-8000-000000000041","task_id":"00000000-0000-4000-8000-000000000001","kind":"status_running","sequence":"1","observed_at":"2026-11-08T00:00:01Z","provider_reference":"provider-task-3","artifact":null,"failure_code":null}`,
		`{"version":"1","event_id":"00000000-0000-4000-8000-000000000040","task_id":"00000000-0000-4000-8000-000000000001","kind":"status_running","sequence":1,"observed_at":"2026-11-08T00:00:01Z","provider_reference":"provider-task-3","artifact":null,"failure_code":null}`,
		`{"version":"1","event_id":"00000000-0000-4000-8000-000000000040","task_id":"00000000-0000-4000-8000-000000000001","kind":"status_running","sequence":"1","observed_at":"2026-11-08T00:00:01Z","provider_reference":"provider-task-3","artifact":null,"failure_code":null,"extra":1}`,
		string(append([]byte{0xef, 0xbb, 0xbf}, raw...)),
	}
	for _, input := range bad {
		if _, err := ParseEvent([]byte(input)); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("invalid event accepted: %q err=%v", input, err)
		}
	}
}

func TestStrictIDsAndExpiryAxes(t *testing.T) {
	for _, raw := range []string{
		"00000000-0000-0000-8000-000000000001",
		"00000000-0000-4000-7000-000000000001",
		"00000000-0000-0000-0000-000000000000",
		"00000000000040008000000000000001",
		"00000000-0000-4000-8000-00000000000A",
	} {
		if _, err := ParseID(raw); !errors.Is(err, ErrInvalidID) {
			t.Errorf("invalid ID accepted: %q err=%v", raw, err)
		}
	}
	task, _, worker := testTask(t)
	started, err := task.BeginSubmit(testBase.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := started.BlockObservation()
	if err != nil {
		t.Fatal(err)
	}
	blocked, err = blocked.Expire(testBase.Add(3 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := blocked.Status(Actor{TenantID: testID(2), SubjectID: testID(3), Permission: PermissionTaskRead})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Protocol != StateSubmitting || snap.Observation != ObservationBlocked {
		t.Fatalf("expiry mixed protocol and observation axes: %+v", snap)
	}
	future, err := NewEvent(EventInput{
		Version:    "1",
		EventID:    testID(41),
		TaskID:     blocked.ID(),
		Kind:       EventSubmissionUnknown,
		Sequence:   1,
		ObservedAt: testBase.Add(5 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocked.Observe(worker, testBase.Add(4*time.Hour), future); err == nil {
		t.Fatal("future observation should not be accepted")
	}
}

func TestArtifactReferencesAndMediaTypesAreOpaqueAndCanonical(t *testing.T) {
	for _, reference := range []string{"../object", "object/../video", "object//video", "object/./video"} {
		artifact := resultArtifact()
		artifact.Reference = reference
		if _, err := NewEvent(EventInput{
			Version: "1", EventID: testID(50), TaskID: testID(1), Kind: EventStatusSucceeded,
			Sequence: 1, ObservedAt: testBase.Add(time.Second), ProviderReference: "provider-1", Artifact: artifact,
		}); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("unsafe artifact reference %q was accepted: %v", reference, err)
		}
	}
	for _, mediaType := range []string{"image/", "/png", "image/png; charset=utf-8", "IMAGE/PNG"} {
		artifact := resultArtifact()
		artifact.MediaType = mediaType
		if _, err := NewEvent(EventInput{
			Version: "1", EventID: testID(51), TaskID: testID(1), Kind: EventStatusSucceeded,
			Sequence: 1, ObservedAt: testBase.Add(time.Second), ProviderReference: "provider-1", Artifact: artifact,
		}); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("noncanonical media type %q was accepted: %v", mediaType, err)
		}
	}
}

func TestTaskValueIsSafeForConcurrentReaders(t *testing.T) {
	task, owner, _ := testTask(t)
	const readers = 12
	var wg sync.WaitGroup
	errCh := make(chan error, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				snap, err := task.Status(owner)
				if err != nil {
					errCh <- err
					return
				}
				if snap.Protocol != StatePrepared {
					errCh <- fmt.Errorf("unexpected protocol %s", snap.Protocol)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}
