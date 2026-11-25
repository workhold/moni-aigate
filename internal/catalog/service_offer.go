package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/economics"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

// ServiceOfferInput references immutable policies. It does not duplicate their
// rates, bindings, credentials, or evidence values.
type ServiceOfferInput struct {
	Kind                        string
	PriceVersionID              string
	DisclosureVersionID         string
	TaskPolicyVersionID         string
	QualityPolicyVersionID      string
	EconomicsVersionID          string
	TransparencyPolicyVersionID string
	ExecutionPolicyVersionID    string
	ApprovalEvidenceID          string
}

// PublishServiceOffer publishes a direct-only test offer and its route in the
// existing model-lock/CAS transaction. Commercial and external writes are not
// part of P0.
func (s *Store) PublishServiceOffer(ctx context.Context, actor, model string, expected int64, bindings []BindingInput, policy routing.DynamicPolicy, breaker routing.BreakerConfig, offer ServiceOfferInput) (int64, string, error) {
	revision, err := s.publishDynamicOffer(ctx, actor, model, expected, bindings, policy, ReleaseTestOnly, breaker, 0, &offer)
	if err != nil {
		return 0, "", err
	}
	var id string
	if err = s.Pool.QueryRow(ctx, `SELECT o.id FROM service_offer_versions o JOIN route_policy_versions p ON p.id=o.route_policy_version_id WHERE p.model_id=$1 AND p.revision=$2`, model, revision).Scan(&id); err != nil {
		return 0, "", err
	}
	return revision, id, nil
}

func insertServiceOfferTx(ctx context.Context, tx pgx.Tx, actor, model, profileVersion, routePolicyID string, in ServiceOfferInput, bindingIDs []string) error {
	if len(bindingIDs) == 0 || in.ApprovalEvidenceID == "" || len(in.ApprovalEvidenceID) > 256 {
		return ErrInvalid
	}
	switch in.Kind {
	case "exact_model", "same_model_multi_provider", "branded_dynamic", "task_optimized":
	default:
		return ErrInvalid
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM task_policy_versions t
		JOIN quality_policy_versions q ON q.id=$2 AND q.logical_model_id=t.logical_model_id
		JOIN execution_policy_versions x ON x.id=$3 AND x.max_outer_attempts=1 AND x.inner_max_attempts=1 AND x.max_relay_depth=0 AND x.settlement_mode='fixed_full_reservation' AND x.cache_mode='disabled' AND x.cumulative_cost_policy='request_total_v1' AND x.unknown_cost_policy='deny'
		JOIN economics_policy_versions e ON e.id=$4
		JOIN fx_policy_versions f ON f.id=e.fx_policy_version_id AND f.valuation_currency=e.valuation_currency AND f.quoted_at<=clock_timestamp() AND f.valid_until>clock_timestamp()
		JOIN service_offer_documents d ON d.id=$5 AND d.kind='disclosure'
		JOIN service_offer_documents r ON r.id=$6 AND r.kind='transparency'
		JOIN price_versions p ON p.id=$7 AND p.model_id=t.logical_model_id AND p.effective_at<=clock_timestamp()
		WHERE t.id=$1 AND t.logical_model_id=$8 AND t.classifier_version='none' AND t.shape_digest_algorithm='none'
	)`, in.TaskPolicyVersionID, in.QualityPolicyVersionID, in.ExecutionPolicyVersionID, in.EconomicsVersionID, in.DisclosureVersionID, in.TransparencyPolicyVersionID, in.PriceVersionID, model).Scan(&valid); err != nil || !valid {
		if err != nil {
			return err
		}
		return ErrInvalid
	}
	task := economics.TaskPolicy{ID: in.TaskPolicyVersionID, LogicalModelID: model}
	var hints []string
	var classJSON []byte
	if err := tx.QueryRow(ctx, `SELECT taxonomy_version,default_task_class,allowed_hints,class_candidates,unknown_action,classifier_version,shape_digest_algorithm,created_by,created_at FROM task_policy_versions WHERE id=$1 AND logical_model_id=$2`, in.TaskPolicyVersionID, model).Scan(&task.TaxonomyVersion, &task.DefaultTaskClass, &hints, &classJSON, &task.UnknownAction, &task.ClassifierVersion, &task.ShapeDigestAlgorithm, &task.CreatedBy, &task.CreatedAt); err != nil {
		return err
	}
	for _, hint := range hints {
		task.AllowedHints = append(task.AllowedHints, economics.TaskClass(hint))
	}
	var classCandidates map[string][]string
	if json.Unmarshal(classJSON, &classCandidates) != nil {
		return ErrInvalid
	}
	task.ClassCandidates = map[economics.TaskClass][]string{}
	for class, ids := range classCandidates {
		task.ClassCandidates[economics.TaskClass(class)] = ids
	}
	if task.Validate() != nil {
		return ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT physical_model_id FROM model_bindings WHERE route_policy_version_id=$1 ORDER BY physical_model_id`, routePolicyID)
	if err != nil {
		return err
	}
	var physicalIDs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		physicalIDs = append(physicalIDs, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if task.ValidateAgainstPool(physicalIDs) != nil {
		return ErrInvalid
	}
	if in.Kind == "exact_model" || in.Kind == "same_model_multi_provider" {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(DISTINCT physical_model_id) FROM model_bindings WHERE route_policy_version_id=$1`, routePolicyID).Scan(&count); err != nil || count != 1 {
			if err != nil {
				return err
			}
			return ErrInvalid
		}
	}
	sort.Strings(bindingIDs)
	digest := sha256.Sum256([]byte(strings.Join(bindingIDs, "\n")))
	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO service_offer_versions(id,logical_model_id,kind,profile_version_id,price_version_id,route_policy_version_id,disclosure_version_id,task_policy_version_id,quality_policy_version_id,economics_version_id,transparency_policy_version_id,execution_policy_version_id,allowed_candidate_digest,savings_reference_version_id,approval_evidence_id,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NULL,$14,$15)`, id, model, in.Kind, profileVersion, in.PriceVersionID, routePolicyID, in.DisclosureVersionID, in.TaskPolicyVersionID, in.QualityPolicyVersionID, in.EconomicsVersionID, in.TransparencyPolicyVersionID, in.ExecutionPolicyVersionID, digest[:], in.ApprovalEvidenceID, actor); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO service_offer_states(offer_version_id,state,revision,updated_by) VALUES($1,'test_active',1,$2)`, id, actor)
	return err
}

func validateServiceOfferEvidenceTx(ctx context.Context, tx pgx.Tx, in ServiceOfferInput, model, profileVersion, protocol, physicalID, channelID, channelVersionID, evidenceID string) error {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT class_candidates FROM task_policy_versions WHERE id=$1 AND logical_model_id=$2`, in.TaskPolicyVersionID, model).Scan(&raw); err != nil {
		return err
	}
	var classes map[string][]string
	if json.Unmarshal(raw, &classes) != nil {
		return ErrInvalid
	}
	var evidence struct {
		physical, channel, version, profile, task, suite, metric, protocol string
		innerPool                                                          *string
		score, uncertainty, lower, samples                                 int
		observed, validUntil, approvedAt                                   time.Time
		withdrawn                                                          bool
	}
	if err := tx.QueryRow(ctx, `SELECT e.physical_model_id,e.channel_id,e.channel_version_id,e.execution_profile_version,e.task_class,e.suite_version,e.metric,e.protocol,e.inner_pool_version,e.score_micro,e.uncertainty_micro,e.confidence_lower_bound_micro,e.sample_count,e.observed_at,e.valid_until,e.approved_at,EXISTS(SELECT 1 FROM quality_evidence_withdrawals w WHERE w.evidence_id=e.id) FROM quality_evidence e WHERE e.id=$1`, evidenceID).Scan(&evidence.physical, &evidence.channel, &evidence.version, &evidence.profile, &evidence.task, &evidence.suite, &evidence.metric, &evidence.protocol, &evidence.innerPool, &evidence.score, &evidence.uncertainty, &evidence.lower, &evidence.samples, &evidence.observed, &evidence.validUntil, &evidence.approvedAt, &evidence.withdrawn); err != nil {
		return err
	}
	allowed := false
	for _, id := range classes[evidence.task] {
		allowed = allowed || id == physicalID
	}
	if evidence.physical != physicalID || evidence.channel != channelID || evidence.version != channelVersionID || evidence.profile != profileVersion || evidence.protocol != protocol || evidence.innerPool != nil || evidence.withdrawn || !allowed {
		return ErrInvalid
	}
	var suite, metric, direction string
	var threshold, minSamples, minLower, maxAge int
	if err := tx.QueryRow(ctx, `SELECT suite_version,metric,direction,threshold_micro,min_sample_count,min_confidence_lower_bound_micro,max_age_seconds FROM quality_policy_versions WHERE id=$1 AND logical_model_id=$2`, in.QualityPolicyVersionID, model).Scan(&suite, &metric, &direction, &threshold, &minSamples, &minLower, &maxAge); err != nil {
		return err
	}
	now := time.Now()
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	bound := evidence.lower
	if direction == "lower_is_better" {
		bound = evidence.score + evidence.uncertainty
	}
	if evidence.suite != suite || evidence.metric != metric || evidence.samples < minSamples || bound < minLower || evidence.observed.After(now) || !evidence.validUntil.After(now) || evidence.approvedAt.After(now) || now.Sub(evidence.observed) > time.Duration(maxAge)*time.Second || direction == "higher_is_better" && bound < threshold || direction == "lower_is_better" && bound > threshold {
		return ErrInvalid
	}
	return nil
}
