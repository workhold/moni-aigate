package routing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/economics"
)

func TestFrozenExecutionCarriesServerPolicyAndReferences(t *testing.T) {
	execution := economics.P0ExecutionPolicy(uuid.NewString())
	execution.CreatedBy, execution.CreatedAt = "tester", time.Unix(100, 0).UTC()
	policy := DefaultDynamicPolicy()
	policy.Execution = &execution
	candidate := dynamicCandidate("one", "standard")
	candidate.Quality.EvidenceID = uuid.NewString()
	if _, err := NewDynamicPlan([]Candidate{candidate}, 2, policy, dynamicRequest()); err == nil {
		t.Fatal("caller enlarged the frozen outer-attempt budget")
	}
	plan, err := NewDynamicPlan([]Candidate{candidate}, 1, policy, dynamicRequest())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	attempt, err := plan.Start(ctx, NewEngine(Config{RandN: func(weight uint64) uint64 { return 0 }}), func(context.Context, Candidate, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Finish(Success)
	frozen := attempt.Frozen
	if frozen == nil || frozen.ExecutionPolicy.ID != execution.ID || frozen.BindingID != candidate.BindingID || frozen.ChannelVersionID != candidate.VersionID || frozen.PhysicalModelID != candidate.PhysicalModelID || frozen.CostVersionID != candidate.CostQuote.VersionID() || frozen.QualityEvidenceID != candidate.Quality.EvidenceID || !frozen.RemainingDeadline.Equal(deadline) {
		t.Fatalf("frozen execution=%+v", frozen)
	}
}
