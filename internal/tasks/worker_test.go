package tasks

import (
	"context"
	"testing"
	"time"
)

func TestWorkerClaimsAndSubmitsOnce(t *testing.T) {
	adapter := &fakeTaskAdapter{}
	service, store, input, refs, owner := serviceFixture(t, adapter)
	if _, err := service.Create(context.Background(), input, refs); err != nil {
		t.Fatal(err)
	}
	worker := Worker{Store: store, Adapter: adapter, Now: func() time.Time { return testBase.Add(time.Second) }}
	if err := worker.RunOnce(context.Background(), WorkSubmit, testID(4)); err != nil {
		t.Fatal(err)
	}
	task, _, err := store.Snapshot(context.Background(), input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := task.Status(owner)
	if err != nil || status.Protocol != StateQueued {
		t.Fatalf("worker did not persist queued state: status=%+v err=%v", status, err)
	}
	if _, _, _, err := store.Claim(context.Background(), WorkSubmit, testID(5), testBase.Add(2*time.Second), time.Minute, testID(121)); err != ErrNoWork {
		t.Fatalf("prepared task was claimable again: %v", err)
	}
}
