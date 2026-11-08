package media

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

type serviceAdapter struct {
	submits  atomic.Int32
	streams  atomic.Int32
	results  atomic.Int32
	usage    atomic.Int32
	cancels  atomic.Int32
	result   InlineResult
	evidence UsageEvidence
	stream   Stream
}

func (a *serviceAdapter) Submit(context.Context, SubmitInput) (Submission, error) {
	a.submits.Add(1)
	return Submission{Handle: "provider-1", Sent: true}, nil
}
func (a *serviceAdapter) Stream(context.Context, Submission) (Stream, error) {
	a.streams.Add(1)
	return a.stream, nil
}
func (a *serviceAdapter) Result(context.Context, Submission) (InlineResult, error) {
	a.results.Add(1)
	return a.result, nil
}
func (a *serviceAdapter) Usage(context.Context, Submission) (UsageEvidence, error) {
	a.usage.Add(1)
	return a.evidence, nil
}
func (a *serviceAdapter) Cancel(context.Context, Submission) error { a.cancels.Add(1); return nil }

type serviceStream struct{}

func (serviceStream) Next(context.Context) (StreamEvent, error) { return StreamEvent{}, io.EOF }
func (serviceStream) Close() error                              { return nil }

func serviceExecution(t *testing.T, adapter Adapter) (*Orchestrator, SubmitInput, ProfileSelector) {
	t.Helper()
	p := testProfile(t)
	in := testInput(t, p)
	refs := RequestAttemptRef{TenantID: "tenant-1", RequestID: "request-1", AttemptID: "attempt-1"}
	registry := NewRegistry()
	if err := registry.RegisterPublished(p, "auth-1", adapter); err != nil {
		t.Fatal(err)
	}
	return NewOrchestrator(registry), SubmitInput{Refs: refs, Profile: p, Input: in}, ProfileSelector{Version: testProfileVersion, VariantKey: "default"}
}

func TestOrchestratorRejectsUnpublishedBeforeSubmit(t *testing.T) {
	p := testProfile(t)
	in := testInput(t, p)
	adapter := &serviceAdapter{}
	o := NewOrchestrator(NewRegistry())
	_, err := o.Start(context.Background(), ProfileSelector{Version: testProfileVersion, VariantKey: "default"}, SubmitInput{Refs: RequestAttemptRef{TenantID: "t", RequestID: "r", AttemptID: "a"}, Profile: p, Input: in})
	if !errors.Is(err, ErrProfileNotPublished) || adapter.submits.Load() != 0 {
		t.Fatalf("got %v, submits=%d", err, adapter.submits.Load())
	}
}

func TestOrchestratorFullLifecycleAndRefs(t *testing.T) {
	p := testProfile(t)
	in := testInput(t, p)
	adapter := &serviceAdapter{stream: serviceStream{}}
	data := testPNG(t, ImageSize{Width: 256, Height: 256})
	adapter.result, _ = ParseResult(testResultRaw(t, p, in, data, ImageSize{Width: 256, Height: 256}), p, in)
	adapter.evidence, _ = NewUsageEvidence(UsageEvidenceInput{Source: "adapter", EventID: "event-1"})
	o, input, selector := serviceExecution(t, adapter)
	execution, err := o.Start(context.Background(), selector, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execution.Result(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := execution.ConfirmUsage(context.Background(), UsageRefs{ObservationID: "observation-1", SettlementID: "settlement-1"}); err != nil {
		t.Fatal(err)
	}
	if state, _ := execution.State(); state != StateCompleted {
		t.Fatalf("state=%s", state)
	}
	usageRefs, err := execution.UsageReferences()
	if err != nil || usageRefs.ObservationID != "observation-1" || usageRefs.SettlementID != "settlement-1" {
		t.Fatalf("usage refs=%+v err=%v", usageRefs, err)
	}
	if adapter.submits.Load() != 1 || adapter.results.Load() != 1 || adapter.usage.Load() != 1 {
		t.Fatal("unexpected adapter calls")
	}
	if _, err := execution.Result(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replayed result returned %v", err)
	}
	refs, err := execution.References()
	if err != nil || refs.RequestID != "request-1" || refs.AttemptID != "attempt-1" {
		t.Fatalf("refs=%+v err=%v", refs, err)
	}
}

func TestOrchestratorUnknownIsTerminalAndNeverResubmits(t *testing.T) {
	adapter := &serviceAdapter{}
	o, input, selector := serviceExecution(t, adapter)
	execution, err := o.Start(context.Background(), selector, input)
	if err != nil {
		t.Fatal(err)
	}
	adapter.result = InlineResult{}
	if _, err := execution.Result(context.Background()); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("bad result=%v", err)
	}
	if state, _ := execution.State(); state != StateConsumptionUnknown {
		t.Fatalf("state=%s", state)
	}
	if _, err := execution.Result(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unknown execution replayed=%v", err)
	}
	if adapter.submits.Load() != 1 || adapter.results.Load() != 1 {
		t.Fatal("adapter was replayed")
	}
}

func TestOrchestratorCancellationIsSingleUse(t *testing.T) {
	adapter := &serviceAdapter{}
	o, input, selector := serviceExecution(t, adapter)
	execution, err := o.Start(context.Background(), selector, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state, _ := execution.State(); state != StateCancelled {
		t.Fatalf("state=%s", state)
	}
	if adapter.cancels.Load() != 1 {
		t.Fatalf("cancel calls=%d", adapter.cancels.Load())
	}
	if err := execution.Cancel(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("second cancel returned %v", err)
	}
}

func TestOrchestratorRejectsInvalidFactReferences(t *testing.T) {
	adapter := &serviceAdapter{}
	o, input, selector := serviceExecution(t, adapter)
	input.Refs.RequestID = ""
	if _, err := o.Start(context.Background(), selector, input); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("invalid request reference returned %v", err)
	}
	input.Refs.RequestID = "request-1"
	execution, err := o.Start(context.Background(), selector, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execution.Result(context.Background()); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("unexpected empty result error %v", err)
	}
}

func TestOrchestratorConcurrentReadersDoNotReplay(t *testing.T) {
	p := testProfile(t)
	in := testInput(t, p)
	data := testPNG(t, ImageSize{Width: 256, Height: 256})
	adapter := &serviceAdapter{}
	adapter.result, _ = ParseResult(testResultRaw(t, p, in, data, ImageSize{Width: 256, Height: 256}), p, in)
	o, input, selector := serviceExecution(t, adapter)
	execution, err := o.Start(context.Background(), selector, input)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = execution.Result(context.Background()) }()
	}
	wg.Wait()
	if adapter.results.Load() != 1 {
		t.Fatalf("result calls=%d", adapter.results.Load())
	}
}
