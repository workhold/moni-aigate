package catalog_test

import (
	"errors"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func TestPostgresDynamicPhysicalBindingsImmutableAndRoleSeparated(t *testing.T) {
	f := credentialPoolDB(t, true)
	capabilityA := routing.PhysicalCapability{ContextLimit: 100, InputUpperBound: 80, OutputLimit: 20, UsageSemantics: "final", Modalities: []string{"text"}, QualityTier: "premium", MaxConcurrency: 4, SupportedParameters: []string{}}
	capabilityB := capabilityA
	capabilityB.QualityScore = nil
	revision, err := f.store.PublishDynamic(f.ctx, "pool-admin", f.model, 0, []catalog.BindingInput{
		{ChannelID: f.channel, UpstreamModel: "upstream-a", Capability: &capabilityA, Priority: 1, Weight: 1, MaxInflight: 4},
		{ChannelID: f.channel, UpstreamModel: "upstream-b", Capability: &capabilityB, Priority: 1, Weight: 1, MaxInflight: 4},
	}, routing.DefaultDynamicPolicy(), catalog.ReleaseTestOnly)
	if err != nil || revision != 1 {
		t.Fatalf("same-channel physical publish revision=%d err=%v", revision, err)
	}
	candidates, err := f.store.CandidateSnapshot(f.ctx, f.model, 1)
	if err != nil || len(candidates) != 2 || candidates[0].PhysicalModelID == candidates[1].PhysicalModelID {
		t.Fatalf("physical candidates=%+v err=%v", candidates, err)
	}
	if _, err = f.store.Publish(f.ctx, "pool-admin", f.model, 1, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "upstream-a", Priority: 1, Weight: 1, MaxInflight: 4}}); !errors.Is(err, catalog.ErrConflict) {
		t.Fatal("legacy publication reused a non-equivalent physical model", err)
	}
	firstID := candidates[0].PhysicalModelID
	if _, err = f.owner.Exec(f.ctx, `UPDATE physical_models SET capability=jsonb_set(capability,'{quality_tier}','"premium"') WHERE id=$1`, firstID); err == nil {
		t.Fatal("physical capability was mutable")
	}
	if _, err = f.owner.Exec(f.ctx, `DELETE FROM physical_models WHERE id=$1`, firstID); err == nil {
		t.Fatal("physical history was deleted")
	}
	if err = f.store.RetirePhysicalModel(f.ctx, f.model, firstID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.RetirePhysicalModel(f.ctx, f.model, firstID); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatal("retired physical model was revived or repeatedly retired", err)
	}
	candidates, err = f.store.CandidateSnapshot(f.ctx, f.model, 1)
	retired := false
	for _, candidate := range candidates {
		if candidate.PhysicalModelID == firstID && candidate.ExclusionReason == "disabled" {
			retired = true
		}
	}
	if err != nil || !retired {
		t.Fatalf("retirement was not reflected in the frozen candidate explanation: %+v %v", candidates, err)
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE physical_models SET capability='{}' WHERE false`); err == nil {
		t.Fatal("APP retained broad physical capability UPDATE")
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE route_decisions SET candidates='[]' WHERE false`); err == nil {
		t.Fatal("APP retained route decision UPDATE")
	}
	if _, err = f.app.Exec(f.ctx, `SELECT count(*) FROM route_decisions`); err != nil {
		t.Fatal("APP cannot read route decisions", err)
	}
}
