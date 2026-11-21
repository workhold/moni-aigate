package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func writeRouteDecision(ctx context.Context, tx pgx.Tx, stored *routing.RouteDecision, attemptID string) error {
	if stored == nil {
		return nil
	}
	value := *stored
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	value.AttemptID = attemptID
	if !validID(value.ID) || !validID(value.TenantID) || !validID(value.RequestID) || !validID(value.PolicyVersionID) || value.Sequence < 1 || value.Sequence > 2 {
		return fail("invalid_attempt", "invalid route decision identity")
	}
	if value.AlgorithmVersion != routing.DynamicAlgorithmVersion && value.AlgorithmVersion != "legacy-v1" || !oneOf(value.Degradation, "none", "tier_changed") {
		return fail("invalid_attempt", "invalid route decision version")
	}
	if value.Sticky != "" && !oneOf(value.Sticky, "inherit", "establish", "break_quality", "break_cost", "break_health") {
		return fail("invalid_attempt", "invalid sticky decision")
	}
	if value.Request.SessionID != "" && !routing.ValidSessionID(value.Request.SessionID) {
		return fail("invalid_attempt", "invalid route session")
	}
	if len(value.Candidates) > 1000 {
		return fail("invalid_attempt", "too many route candidates")
	}
	selected := 0
	for _, candidate := range value.Candidates {
		if !validRouteReason(candidate.Reason) || candidate.FilterReason != "" && !validRouteReason(candidate.FilterReason) {
			return fail("invalid_attempt", "invalid route candidate reason")
		}
		if candidate.Reason == "selected" || candidate.Reason == "sticky_override" {
			selected++
			if attemptID != "" && candidate.BindingID != value.SelectedBindingID {
				return fail("invalid_attempt", "selected route candidate mismatch")
			}
		}
	}
	if attemptID != "" && value.AlgorithmVersion == routing.DynamicAlgorithmVersion && selected != 1 {
		return fail("invalid_attempt", "route decision selected count mismatch")
	}
	candidates, err := json.Marshal(value.Candidates)
	if err != nil || len(candidates) > 262144 {
		return fail("invalid_attempt", "route candidates too large")
	}
	digest := sha256.Sum256(candidates)
	decodedDigest, err := hex.DecodeString(value.CandidatesDigest)
	if err != nil || len(decodedDigest) != 32 {
		return fail("invalid_attempt", "route candidate digest missing")
	}
	if value.AlgorithmVersion == routing.DynamicAlgorithmVersion && string(decodedDigest) != string(digest[:]) {
		return fail("invalid_attempt", "route candidate digest mismatch")
	}
	policy, err := json.Marshal(value.Policy)
	if err != nil || len(policy) > 16384 {
		return fail("invalid_attempt", "invalid route policy snapshot")
	}
	request, err := json.Marshal(value.Request)
	if err != nil || len(request) > 32768 {
		return fail("invalid_attempt", "invalid route request snapshot")
	}
	var draw any
	if value.Draw != "" {
		n, ok := new(big.Int).SetString(value.Draw, 10)
		if !ok || n.Sign() < 0 || len(value.Draw) > 30 {
			return fail("invalid_attempt", "invalid route draw")
		}
		draw = value.Draw
	}
	var attempt any
	if attemptID != "" {
		attempt = attemptID
	}
	var selectedBinding, selectedPhysical, sticky, session any
	if value.SelectedBindingID != "" {
		selectedBinding = value.SelectedBindingID
	}
	if value.SelectedPhysicalModelID != "" {
		selectedPhysical = value.SelectedPhysicalModelID
	}
	if value.Sticky != "" {
		sticky = value.Sticky
	}
	if value.Request.SessionID != "" {
		session = value.Request.SessionID
	}
	_, err = tx.Exec(ctx, `INSERT INTO route_decisions(id,tenant_id,request_id,attempt_id,sequence,policy_version_id,selected_binding_id,selected_physical_model_id,draw,sticky,degradation,algorithm_version,policy_snapshot,request_shape,candidates,candidates_digest,session_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, value.ID, value.TenantID, value.RequestID, attempt, value.Sequence, value.PolicyVersionID, selectedBinding, selectedPhysical, draw, sticky, value.Degradation, value.AlgorithmVersion, policy, request, candidates, decodedDigest, session)
	if err != nil {
		return err
	}
	if attemptID != "" && value.AlgorithmVersion == routing.DynamicAlgorithmVersion && (value.Sticky == "establish" || value.Sticky == "inherit") {
		return upsertRouteSession(ctx, tx, value)
	}
	return nil
}

func upsertRouteSession(ctx context.Context, tx pgx.Tx, decision routing.RouteDecision) error {
	if decision.Policy.Stickiness == nil || !decision.Policy.Stickiness.Enabled || decision.Request.SessionID == "" {
		return fail("invalid_attempt", "sticky decision has no session policy")
	}
	maxAge := strconv.FormatInt(decision.Policy.Stickiness.MaxAgeS, 10) + " seconds"
	channel := ""
	for _, candidate := range decision.Candidates {
		if candidate.BindingID == decision.SelectedBindingID {
			channel = candidate.ChannelID
		}
	}
	if channel == "" {
		return fail("invalid_attempt", "sticky selected channel is missing")
	}
	_, err := tx.Exec(ctx, `INSERT INTO route_sessions(tenant_id,session_id,physical_model_id,channel_id,established_at,expires_at,last_seen_at)
 SELECT $1,$2,$3,$4,now,now+($5::text::interval),now FROM (SELECT clock_timestamp() AS now) clock
 ON CONFLICT(tenant_id,session_id) DO UPDATE SET physical_model_id=excluded.physical_model_id,channel_id=excluded.channel_id,expires_at=excluded.expires_at,last_seen_at=excluded.last_seen_at`, decision.TenantID, decision.Request.SessionID, decision.SelectedPhysicalModelID, channel, maxAge)
	return err
}

type attemptBinding struct {
	bindingID, physicalID, policyID string
	capability                      routing.PhysicalCapability
}

func attemptBindingIdentity(ctx context.Context, tx pgx.Tx, r financialRequest, in AttemptInput) (attemptBinding, error) {
	var result attemptBinding
	var capability []byte
	err := tx.QueryRow(ctx, `SELECT b.id,b.physical_model_id,p.id,pm.capability FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id JOIN physical_models pm ON pm.id=b.physical_model_id WHERE p.model_id=$1 AND p.revision=$2 AND b.channel_id=$3 AND b.channel_version_id=$4 AND b.upstream_model=$5`, r.ModelID, r.RouteRevision, in.ChannelID, in.ChannelVersionID, in.UpstreamModel).Scan(&result.bindingID, &result.physicalID, &result.policyID, &capability)
	if err != nil {
		return result, err
	}
	if json.Unmarshal(capability, &result.capability) != nil || result.capability.Validate() != nil || in.BindingID != "" && in.BindingID != result.bindingID || in.PhysicalModelID != "" && in.PhysicalModelID != result.physicalID || in.PolicyVersionID != "" && in.PolicyVersionID != result.policyID {
		return attemptBinding{}, fail("invalid_attempt", "physical binding mismatch")
	}
	return result, nil
}

func fallbackRouteDecision(in AttemptInput, binding attemptBinding) *routing.RouteDecision {
	decision := &routing.RouteDecision{
		ID: uuid.NewString(), TenantID: in.TenantID, RequestID: in.RequestID, Sequence: in.Sequence,
		PolicyVersionID: binding.policyID, SelectedBindingID: binding.bindingID, SelectedPhysicalModelID: binding.physicalID,
		Degradation: "none", AlgorithmVersion: "legacy-v1", Request: routing.RequestShape{},
		Candidates: []routing.CandidateDecision{{BindingID: binding.bindingID, ChannelID: in.ChannelID, ChannelVersionID: in.ChannelVersionID, PhysicalModelID: binding.physicalID, UpstreamModel: in.UpstreamModel, Reason: "selected", CostSnapshot: routing.CostSnapshot{Rates: []routing.CostRateSnapshot{}}}},
	}
	raw, _ := json.Marshal(decision.Candidates)
	digest := sha256.Sum256(raw)
	decision.CandidatesDigest = hex.EncodeToString(digest[:])
	return decision
}

func validRouteReason(reason string) bool {
	return oneOf(reason, "not_authorized", "region_forbidden", "capability_missing", "quality_below_floor", "context_too_small", "tools_unsupported", "output_limit_too_small", "disabled", "channel_quarantined", "binding_isolated", "credential_revoked", "capacity_full", "circuit_open", "half_open_busy", "quarantined", "cost_unknown", "cost_limit_exceeded", "margin_below_floor", "procurement_paused", "procurement_budget_exceeded", "procurement_bound_invalid", "procurement_budget_unavailable", "selected", "lower_priority", "higher_cost", "lower_score", "weighted_not_selected", "sticky_override", "already_tried")
}
