package billing

import (
	"encoding/json"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

// Route explanations are typed, bounded metadata. No URL, credential, model
// payload or arbitrary diagnostic text may cross this persistence boundary.
func validatedRouteDecisions(in AttemptInput) ([]byte, error) {
	return validateRouteDecisions(in.ChannelID, in.ChannelVersionID, in.RouteDecisions)
}

func validateRouteDecisions(channel, version string, decisions []routing.Decision) ([]byte, error) {
	if len(decisions) > 1000 {
		return nil, fail("invalid_attempt", "too many route decisions")
	}
	if len(decisions) == 0 {
		return []byte("[]"), nil
	}
	selected := 0
	seen := map[string]bool{}
	for _, d := range decisions {
		if !validID(d.ChannelID) || !validID(d.VersionID) || seen[d.ChannelID] || d.Weight == 0 {
			return nil, fail("invalid_attempt", "invalid route decision")
		}
		seen[d.ChannelID] = true
		if !oneOf(d.Reason, "selected", "weighted_not_selected", "lower_priority", "disabled", "already_tried", "capacity_full", "quarantined", "circuit_open", "half_open_busy", "channel_quarantined", "credential_revoked", "binding_isolated", "region_forbidden", "cost_unknown", "cost_limit_exceeded", "higher_cost", "procurement_paused", "procurement_budget_exceeded", "procurement_bound_invalid", "procurement_budget_unavailable") {
			return nil, fail("invalid_attempt", "invalid route reason")
		}
		if d.Reason == "selected" {
			selected++
			if d.ChannelID != channel || d.VersionID != version {
				return nil, fail("invalid_attempt", "selected route mismatch")
			}
		}
	}
	if selected != 1 {
		return nil, fail("invalid_attempt", "route needs one selected candidate")
	}
	raw, err := json.Marshal(decisions)
	if err != nil || len(raw) > 262144 {
		return nil, fail("invalid_attempt", "route decisions too large")
	}
	return raw, nil
}
