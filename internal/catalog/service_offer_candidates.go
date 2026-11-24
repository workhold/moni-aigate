package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

type offerCandidateEvidence struct {
	quality                             routing.QualityEvidenceSnapshot
	score, uncertainty, lower, samples  int
	observed, validUntil                time.Time
	withdrawn                           bool
	physical, channel, version, profile string
}

// attachOfferCandidates is only the routing-time prefilter. PrepareDispatch
// repeats the authoritative checks under its database locks.
func (s *Store) attachOfferCandidates(ctx context.Context, model string, revision int64, candidates []Candidate) error {
	var offerID, state, qualityPolicyID string
	err := s.Pool.QueryRow(ctx, `SELECT o.id,st.state,o.quality_policy_version_id FROM service_offer_versions o JOIN service_offer_states st ON st.offer_version_id=o.id JOIN route_policy_versions p ON p.id=o.route_policy_version_id WHERE p.model_id=$1 AND p.revision=$2`, model, revision).Scan(&offerID, &state, &qualityPolicyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var suite, metric, direction, profileVersion, protocol string
	var threshold, minSamples, minLower int
	var maxAge int64
	if err = s.Pool.QueryRow(ctx, `SELECT q.suite_version,q.metric,q.direction,q.threshold_micro,q.min_sample_count,q.min_confidence_lower_bound_micro,q.max_age_seconds,o.profile_version_id,c.protocol FROM quality_policy_versions q JOIN service_offer_versions o ON o.quality_policy_version_id=q.id JOIN model_capability_versions c ON c.id=o.profile_version_id WHERE o.id=$1`, offerID).Scan(&suite, &metric, &direction, &threshold, &minSamples, &minLower, &maxAge, &profileVersion, &protocol); err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, `SELECT b.id,coalesce(e.id,''),coalesce(e.task_class,''),coalesce(e.protocol,''),coalesce(e.tools_scope,''),coalesce(e.reasoning_scope,''),coalesce(e.physical_model_id,''),coalesce(e.channel_id,''),coalesce(e.channel_version_id,''),coalesce(e.execution_profile_version,''),e.score_micro,e.uncertainty_micro,e.confidence_lower_bound_micro,e.sample_count,e.observed_at,e.valid_until,coalesce(EXISTS(SELECT 1 FROM quality_evidence_withdrawals w WHERE w.evidence_id=e.id),false),clock_timestamp() FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id LEFT JOIN quality_evidence e ON e.id=b.quality_evidence_id WHERE p.model_id=$1 AND p.revision=$2 ORDER BY b.id`, model, revision)
	if err != nil {
		return err
	}
	defer rows.Close()
	evidence := map[string]offerCandidateEvidence{}
	var now time.Time
	for rows.Next() {
		var binding string
		var item offerCandidateEvidence
		var score, uncertainty, lower, samples *int
		var observed, validUntil *time.Time
		if err = rows.Scan(&binding, &item.quality.EvidenceID, &item.quality.TaskClass, &item.quality.Protocol, &item.quality.ToolsScope, &item.quality.ReasoningScope, &item.physical, &item.channel, &item.version, &item.profile, &score, &uncertainty, &lower, &samples, &observed, &validUntil, &item.withdrawn, &now); err != nil {
			return err
		}
		if score != nil {
			item.score, item.uncertainty, item.lower, item.samples = *score, *uncertainty, *lower, *samples
			item.observed, item.validUntil = *observed, *validUntil
		}
		evidence[binding] = item
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range candidates {
		c := &candidates[i]
		item := evidence[c.BindingID]
		item.quality.ExclusionReason = qualityCandidateExclusion(*c, item, state, suite, metric, direction, threshold, minSamples, minLower, maxAge, profileVersion, protocol, now)
		c.Quality = item.quality
	}
	return nil
}

func qualityCandidateExclusion(c Candidate, item offerCandidateEvidence, state, suite, metric, direction string, threshold, minSamples, minLower int, maxAge int64, profileVersion, protocol string, now time.Time) string {
	q := item.quality
	switch {
	case state != "test_active":
		return "offer_inactive"
	case q.EvidenceID == "":
		return "quality_evidence_missing"
	case item.withdrawn:
		return "quality_evidence_withdrawn"
	case item.physical != c.PhysicalModelID || item.channel != c.ChannelID || item.version != c.VersionID || item.profile != profileVersion || q.Protocol != protocol || c.ChannelKind != "direct":
		return "quality_scope_mismatch"
	case item.observed.After(now) || !item.validUntil.After(now) || now.Sub(item.observed) > time.Duration(maxAge)*time.Second:
		return "quality_evidence_expired"
	}
	bound := item.lower
	if direction == "lower_is_better" {
		bound = item.score + item.uncertainty
	}
	if item.samples < minSamples || bound < minLower || direction == "higher_is_better" && bound < threshold || direction == "lower_is_better" && bound > threshold {
		return "quality_below_floor"
	}
	return ""
}
