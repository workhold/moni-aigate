package routing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/routecost"
)

var ErrReplayMismatch = errors.New("route_replay_mismatch")

// ReplayRoute consumes only the immutable decision and its embedded snapshots.
// It has no database, provider, clock or configuration reader.
func ReplayRoute(stored *RouteDecision) (*RouteDecision, error) {
	if stored == nil {
		return nil, ErrInvalidPolicy
	}
	if stored.AlgorithmVersion == "legacy-v1" {
		return cloneDecision(stored), nil
	}
	if stored.AlgorithmVersion != DynamicAlgorithmVersion || stored.Policy.Validate() != nil || stored.Request.Validate() != nil {
		return nil, ErrInvalidPolicy
	}
	raw, err := json.Marshal(stored.Candidates)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	if stored.CandidatesDigest != hex.EncodeToString(digest[:]) {
		return nil, ErrReplayMismatch
	}
	candidates := make([]Candidate, len(stored.Candidates))
	for i, saved := range stored.Candidates {
		candidate, err := replayCandidate(saved, stored)
		if err != nil {
			return nil, err
		}
		candidates[i] = candidate
	}
	plan, err := NewDynamicPlan(candidates, 2, stored.Policy, stored.Request)
	if err != nil {
		return nil, err
	}
	draw, err := strconv.ParseUint(stored.Draw, 10, 64)
	if err != nil || strconv.FormatUint(draw, 10) != stored.Draw {
		return nil, ErrInvalidPolicy
	}
	draws := 0
	engine := NewEngine(Config{Now: func() time.Time { return time.Unix(0, 0) }, RandN: func(weight uint64) uint64 {
		draws++
		if draws != 1 || draw >= weight {
			return weight
		}
		return draw
	}})
	_, _, replayed, acquireErr := engine.acquirePlan(plan, stored.Sequence)
	if acquireErr != nil && !errors.Is(acquireErr, ErrUnavailable) {
		return nil, acquireErr
	}
	if replayed == nil || replayed.SelectedBindingID != stored.SelectedBindingID || replayed.SelectedPhysicalModelID != stored.SelectedPhysicalModelID || replayed.Sticky != stored.Sticky || replayed.Degradation != stored.Degradation || len(replayed.Candidates) != len(stored.Candidates) {
		return nil, ErrReplayMismatch
	}
	for i := range replayed.Candidates {
		if replayed.Candidates[i].BindingID != stored.Candidates[i].BindingID || replayed.Candidates[i].Reason != stored.Candidates[i].Reason || replayed.Candidates[i].FilterReason != stored.Candidates[i].FilterReason {
			return nil, ErrReplayMismatch
		}
	}
	replayed.ID = stored.ID
	replayed.TenantID = stored.TenantID
	replayed.RequestID = stored.RequestID
	replayed.AttemptID = stored.AttemptID
	return replayed, nil
}

func replayCandidate(saved CandidateDecision, stored *RouteDecision) (Candidate, error) {
	schema, err := metering.ParseSchema(saved.CostSnapshot.SchemaCanonical)
	if err != nil {
		return Candidate{}, err
	}
	digest, _ := schema.Digest()
	if saved.CostSnapshot.SchemaDigest != hex.EncodeToString(digest[:]) {
		return Candidate{}, ErrReplayMismatch
	}
	var rates metering.Rates
	if saved.CostSnapshot.Basis != "unknown" {
		rates, err = replayRates(schema, saved.CostSnapshot.Rates)
		if err != nil {
			return Candidate{}, err
		}
	}
	quote, err := routecost.NewQuote(routecost.Input{ChannelID: saved.ChannelID, UpstreamModel: saved.UpstreamModel, VersionID: saved.CostSnapshot.VersionID, Basis: saved.CostSnapshot.Basis, VariantKey: "default", Schema: schema, Rates: rates})
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		BindingID: saved.BindingID, PhysicalModelID: saved.PhysicalModelID, PolicyVersionID: stored.PolicyVersionID,
		ChannelID: saved.ChannelID, VersionID: saved.ChannelVersionID, CredentialID: saved.BindingID + ":replay",
		Model: saved.UpstreamModel, HealthKey: saved.BindingID + ":replay", Priority: saved.Priority, Weight: saved.Weight,
		MaxInflight: 1, CredentialMaxInflight: 1, Enabled: saved.FilterReason == "", ExclusionReason: saved.FilterReason,
		CostPolicy: stored.Policy.CostPolicy, CostQuote: quote, InputUpperBound: stored.Request.InputUpperBound,
		OutputLimit: stored.Request.OutputLimit, RetailAmount: stored.Request.RetailAmount, Physical: saved.Capability, Stats: saved.Score.Stats,
	}, nil
}

func replayRates(schema metering.Schema, saved []CostRateSnapshot) (metering.Rates, error) {
	schemaID, err := schema.ID()
	if err != nil {
		return metering.Rates{}, err
	}
	var buffer bytes.Buffer
	buffer.WriteString(`{"schema_id":`)
	encoded, _ := json.Marshal(schemaID)
	buffer.Write(encoded)
	buffer.WriteString(`,"version":"1","side":"supplier","currency":"CNY","rates":[`)
	for i, rate := range saved {
		if i > 0 {
			buffer.WriteByte(',')
		}
		encoded, err := json.Marshal(rate)
		if err != nil {
			return metering.Rates{}, err
		}
		buffer.Write(encoded)
	}
	buffer.WriteString(`]}`)
	return metering.ParseRates(schema, metering.Supplier, buffer.Bytes())
}
