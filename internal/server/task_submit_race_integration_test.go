package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/tasks"
)

func TestVideoTaskWorkerAndHTTPSubmitRaceAreIdempotent(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	fixture := createVideoTask(t, f, "video-submit-race")
	store, err := tasks.NewPostgresStore(f.pool, "")
	if err != nil {
		t.Fatal(err)
	}
	worker := tasks.Worker{Store: store, Adapter: f.server.TaskAdapter, Now: time.Now, AdapterFor: func(actor tasks.Actor) (provider.TaskAdapter, error) {
		return f.server.NewTaskWorkerAdapter(identity.Principal{TenantID: actor.TenantID.String(), UserID: actor.SubjectID.String()}, f.server.TaskAdapter)
	}}
	start := make(chan struct{})
	var workerErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		workerErr = worker.RunOnce(context.Background(), tasks.WorkSubmit, tasks.ID(uuid.NewString()))
	}()
	request, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/tasks/"+fixture.taskID+"/submit", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.token)
	var response *http.Response
	var requestErr error
	wg.Add(1)
	go func() { defer wg.Done(); <-start; response, requestErr = f.http.Client().Do(request) }()
	close(start)
	wg.Wait()
	if requestErr != nil {
		t.Fatal(requestErr)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("losing HTTP submit returned %d", response.StatusCode)
	}
	if workerErr != nil && !errors.Is(workerErr, tasks.ErrNoWork) {
		t.Fatalf("worker submit failed: %v", workerErr)
	}
	var accepted, attempts int
	if err = f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM media_task_events WHERE task_id=$1 AND kind='submission_accepted' AND sequence=1),(SELECT count(*) FROM media_attempts WHERE request_id=$2)`, fixture.taskID, fixture.requestID).Scan(&accepted, &attempts); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 || attempts != 1 {
		t.Fatalf("race duplicated submission facts: events=%d attempts=%d", accepted, attempts)
	}
}
