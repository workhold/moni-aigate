package routing

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAcquisitionDecisionsCoverEveryCandidate(t *testing.T) {
	now := time.Unix(100, 0)
	clockCalls, randomCalls := 0, 0
	engine := NewEngine(Config{
		Now: func() time.Time { clockCalls++; return now },
		RandN: func(n uint64) uint64 {
			randomCalls++
			if n != 4 {
				t.Fatalf("changed eligible weight: %d", n)
			}
			return n - 1
		},
	})
	candidates := []Candidate{
		candidate("low_first", 0, 100), candidate("a", 10, 1),
		candidate("disabled", 20, 1), candidate("tried", 20, 1),
		candidate("full", 20, 1), candidate("quarantined", 20, 1),
		candidate("open", 20, 1), candidate("probe", 20, 1),
		candidate("b", 10, 3), candidate("low_last", 5, 100),
	}
	candidates[2].Enabled = false
	candidates[4].MaxInflight = 1
	engine.inflight["full"] = 1
	engine.health["quarantined"] = &health{state: Quarantined}
	engine.health["open"] = &health{state: Open, until: now.Add(time.Minute)}
	engine.health["probe"] = &health{state: HalfOpen, probe: true}
	for i := range candidates {
		candidates[i].Endpoint.URL = "https://sensitive.invalid"
		candidates[i].Endpoint.APIKey = "sensitive-secret"
		candidates[i].Model = "sensitive-model"
		candidates[i].CostQuote = testCostQuote(candidates[i].ChannelID, "sensitive-model", "", "unknown", 0, 0)
		candidates[i].CredentialID = "sensitive-credential"
	}

	selected, _, err := engine.acquire(candidates, map[string]bool{"tried": true})
	if err != nil || selected.ChannelID != "b" {
		t.Fatal(selected, err)
	}
	wantReasons := []string{"lower_priority", "weighted_not_selected", "disabled", "already_tried", "capacity_full", "quarantined", "circuit_open", "half_open_busy", "selected", "lower_priority"}
	want := make([]Decision, len(candidates))
	for i, c := range candidates {
		want[i] = Decision{ChannelID: c.ChannelID, VersionID: c.VersionID, Priority: c.Priority, Weight: c.Weight, Reason: wantReasons[i]}
	}
	if !reflect.DeepEqual(selected.Decisions, want) {
		t.Fatalf("decisions=%+v want=%+v", selected.Decisions, want)
	}
	if clockCalls != 1 || randomCalls != 1 || engine.Snapshot("b", "b").Inflight != 1 {
		t.Fatal("explanations changed clock, random draws or capacity", clockCalls, randomCalls)
	}
	encoded, err := json.Marshal(selected.Decisions)
	if err != nil || strings.Contains(string(encoded), "sensitive") {
		t.Fatalf("unsafe decision payload: %s %v", encoded, err)
	}
	var decoded []map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, fields := range decoded {
		if len(fields) != 5 {
			t.Fatalf("unexpected decision fields: %v", fields)
		}
		for _, name := range []string{"channel_id", "version_id", "priority", "weight", "reason"} {
			if _, ok := fields[name]; !ok {
				t.Fatalf("missing safe field %s", name)
			}
		}
	}
}

func TestStaticDecisionReasonsAreWhitelisted(t *testing.T) {
	for _, reason := range []string{"disabled", "channel_quarantined", "credential_revoked", "binding_isolated", "region_forbidden", "", "selected", "https://sensitive.invalid?key=sensitive-secret"} {
		t.Run(reason, func(t *testing.T) {
			disabled := candidate("blocked", 10, 1)
			disabled.Enabled = false
			disabled.ExclusionReason = reason
			fallback := candidate("fallback", 0, 1)
			fallback.ExclusionReason = "credential_revoked" // Ignored for eligible candidates.
			engine := NewEngine(Config{})
			c, _, err := engine.acquire([]Candidate{disabled, fallback}, nil)
			if err != nil || c.ChannelID != "fallback" {
				t.Fatal(c, err)
			}
			want := reason
			if reason == "" || reason == "selected" || strings.HasPrefix(reason, "https://") {
				want = "disabled"
			}
			if len(c.Decisions) != 2 || c.Decisions[0].Reason != want || c.Decisions[1].Reason != "selected" {
				t.Fatalf("uncontrolled or incorrect reasons: %+v", c.Decisions)
			}
		})
	}
}

func TestDecisionSnapshotsAreFrozenAcrossAdmissionAndRetry(t *testing.T) {
	input := []Candidate{candidate("a", 10, 1), candidate("b", 0, 1)}
	input[0].Decisions = []Decision{{Reason: "stale"}}
	p := plan(t, input...)
	input[0].Decisions[0].Reason = "caller mutation"
	if p.candidates[0].Decisions[0].Reason != "stale" {
		t.Fatal("NewPlan retained caller decision slice")
	}
	engine := NewEngine(Config{})
	first, err := p.Start(context.Background(), engine, func(_ context.Context, c Candidate, sequence int) error {
		if sequence != 1 || len(c.Decisions) != 2 || c.Decisions[0].Reason != "selected" || c.Decisions[1].Reason != "lower_priority" {
			t.Fatalf("admission did not receive current decisions: %+v", c.Decisions)
		}
		c.Decisions[0].Reason = "admission mutation"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Candidate.Decisions[0].Reason != "selected" || first.candidate.Decisions[0].Reason != "selected" {
		t.Fatal("admission mutated attempt snapshot")
	}
	first.Candidate.Decisions[0].Reason = "public mutation"
	if first.candidate.Decisions[0].Reason != "selected" || p.candidates[0].Decisions[0].Reason != "stale" {
		t.Fatal("public attempt mutated internal or plan snapshot")
	}
	first.Finish(Neutral)
	second, err := p.Start(context.Background(), engine, func(_ context.Context, c Candidate, sequence int) error {
		if sequence != 2 || len(c.Decisions) != 2 || c.Decisions[0].Reason != "already_tried" || c.Decisions[1].Reason != "selected" {
			t.Fatalf("retry reused stale decisions: %+v", c.Decisions)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	second.Finish(Neutral)
	if engine.Snapshot("a", "a").Inflight != 0 || engine.Snapshot("b", "b").Inflight != 0 {
		t.Fatal("decision snapshots changed capacity cleanup")
	}
}
