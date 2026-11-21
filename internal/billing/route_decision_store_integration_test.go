package billing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/routing"
)

func TestPostgresAdmissionFailureDecisionReplaysAfterRelease(t *testing.T) {
	f := integration(t)
	reservation := f.reserve(t)
	var bindingID, physicalID, policyID string
	var capability []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT b.id,b.physical_model_id,p.id,pm.capability FROM request_records r JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision JOIN model_bindings b ON b.route_policy_version_id=p.id JOIN physical_models pm ON pm.id=b.physical_model_id WHERE r.tenant_id=$1 AND r.id=$2`, f.tenant, reservation.RequestID).Scan(&bindingID, &physicalID, &policyID, &capability); err != nil {
		t.Fatal(err)
	}
	quote, err := scanCostQuote(f.pool.QueryRow(f.ctx, currentAttemptCostSQL, f.channel, "upstream", reservation.RequestID), f.channel, "upstream")
	if err != nil {
		t.Fatal(err)
	}
	candidate := routing.Candidate{BindingID: bindingID, PhysicalModelID: physicalID, PolicyVersionID: policyID, ChannelID: f.channel, VersionID: f.version, CredentialID: f.credential, Model: "upstream", Weight: 1, MaxInflight: 1, Enabled: true, CostPolicy: routing.DefaultCostPolicy(), CostQuote: quote, InputUpperBound: 100, OutputLimit: 20, RetailAmount: int64(reservation.Amount)}
	if json.Unmarshal(capability, &candidate.Physical) != nil {
		t.Fatal("invalid physical capability")
	}
	request := routing.RequestShape{InputUpperBound: 100, OutputLimit: 20, RetailAmount: int64(reservation.Amount), Preference: routing.RoutePreference{AllowFallbacks: true}, StatsWindowS: 900, StatsMinSamples: 50}
	plan, err := routing.NewDynamicPlan([]routing.Candidate{candidate}, 1, routing.DefaultDynamicPolicy(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Start(context.Background(), routing.NewEngine(routing.Config{}), func(context.Context, routing.Candidate, int) error { return errors.New("injected admission failure") })
	if err == nil {
		t.Fatal("injected admission failure did not fail")
	}
	decision := plan.LastDecision()
	if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: reservation.RequestID, Reason: "platform_failure", RouteDecision: decision}); err != nil {
		t.Fatal(err)
	}
	var stored routing.RouteDecision
	var policy, shape, candidates []byte
	var digest []byte
	if err = f.pool.QueryRow(f.ctx, `SELECT id,tenant_id,request_id,sequence,policy_version_id,coalesce(selected_binding_id,''),coalesce(selected_physical_model_id,''),draw::text,degradation,algorithm_version,policy_snapshot,request_shape,candidates,candidates_digest FROM route_decisions WHERE request_id=$1 AND attempt_id IS NULL`, reservation.RequestID).Scan(&stored.ID, &stored.TenantID, &stored.RequestID, &stored.Sequence, &stored.PolicyVersionID, &stored.SelectedBindingID, &stored.SelectedPhysicalModelID, &stored.Draw, &stored.Degradation, &stored.AlgorithmVersion, &policy, &shape, &candidates, &digest); err != nil {
		t.Fatal(err)
	}
	if stored.SelectedBindingID != bindingID || stored.SelectedPhysicalModelID != physicalID {
		t.Fatal("admission failure lost the selected path")
	}
	stored.CandidatesDigest = hex.EncodeToString(digest)
	if json.Unmarshal(policy, &stored.Policy) != nil || json.Unmarshal(shape, &stored.Request) != nil || json.Unmarshal(candidates, &stored.Candidates) != nil {
		t.Fatal("invalid stored route decision")
	}
	replayed, err := routing.ReplayRoute(&stored)
	if err != nil || replayed.SelectedBindingID != bindingID {
		t.Fatalf("admission failure replay=%+v err=%v", replayed, err)
	}
}
