package billing

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

// A single sampled DB clock chooses a quote deterministically, including legacy
// equal-effective-time rows. Call only after the channel synchronization lock.
const currentAttemptCostSQL = `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at)
 SELECT c.id,c.input_rate,c.output_rate,c.basis FROM cost_versions c,instant
 WHERE c.channel_id=$1 AND c.upstream_model=$2 AND c.effective_at<=instant.at
 ORDER BY c.effective_at DESC,c.created_at DESC,c.id DESC LIMIT 1`

func quoteOf(c attemptCost) routing.CostQuote {
	q := routing.CostQuote{InputRate: c.InputRate, OutputRate: c.OutputRate, Basis: c.Basis}
	if c.ID != nil {
		q.VersionID = *c.ID
	}
	return q
}

// This belongs only to the new-attempt path. Putting it in lockRequest would
// also reject financial cleanup after an operator tightens a route or price.
func checkAttemptCostQuote(ctx context.Context, tx pgx.Tx, r financialRequest, in AttemptInput, cost attemptCost) error {
	var procurement bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM credential_procurement_bindings WHERE credential_id=$1)`, in.CredentialID).Scan(&procurement); err != nil {
		return err
	}
	if procurement {
		if cost.InputRate == nil || cost.OutputRate == nil || !oneOf(cost.Basis, "contract", "invoice") {
			return fail("cost_unknown", "controlled procurement requires a complete known quote")
		}
		if in.CostQuote == nil || !in.CostQuote.Equal(quoteOf(cost)) {
			return fail("cost_quote_stale", "procurement quote changed before attempt")
		}
	}
	if r.RouteRevision == 0 {
		return nil // Historical internal callers without a published route.
	}
	var policy routing.CostPolicy
	var maxIn, maxOut *int64
	err := tx.QueryRow(ctx, `SELECT p.cost_mode,p.unknown_cost_policy,b.max_input_cost_rate,b.max_output_cost_rate
 FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id
 WHERE p.model_id=$1 AND p.revision=$2 AND b.channel_id=$3 AND b.channel_version_id=$4 AND b.upstream_model=$5`,
		r.ModelID, r.RouteRevision, in.ChannelID, in.ChannelVersionID, in.UpstreamModel).Scan(&policy.Mode, &policy.UnknownCostPolicy, &maxIn, &maxOut)
	if err != nil {
		return err
	}
	if !policy.Managed(maxIn, maxOut) && !procurement {
		return nil
	}
	quote := quoteOf(cost)
	if in.CostQuote == nil || !in.CostQuote.Equal(quote) {
		return fail("cost_quote_stale", "configured procurement quote changed before dispatch")
	}
	return checkCostExclusion(policy, quote, maxIn, maxOut)
}

func checkCostExclusion(policy routing.CostPolicy, quote routing.CostQuote, maxIn, maxOut *int64) error {
	switch routing.CostExclusion(policy, quote, maxIn, maxOut) {
	case "cost_unknown":
		return fail("cost_unknown", "route requires a known procurement quote")
	case "cost_limit_exceeded":
		return fail("cost_limit_exceeded", "configured procurement rate exceeds route limit")
	}
	return nil
}

// All parent/channel/credential/reservation/attempt locks are already owned.
// A new statement observes committed quote publications and scheduled versions
// that became effective while any of those locks were waiting. Cost publishers
// synchronize on channel UPDATE, so no concurrent insert can cross this check.
func checkDispatchCostQuote(ctx context.Context, tx pgx.Tx, tenant, attempt string, at time.Time) error {
	var policy routing.CostPolicy
	var maxIn, maxOut *int64
	var frozen, current attemptCost
	var currentBasis *string
	err := tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT $3::timestamptz AS at)
 SELECT coalesce(p.cost_mode,'manual'),coalesce(p.unknown_cost_policy,'allow'),b.max_input_cost_rate,b.max_output_cost_rate,
 a.cost_version_id,a.cost_input_rate,a.cost_output_rate,a.cost_basis,c.id,c.input_rate,c.output_rate,c.basis
 FROM upstream_attempts a JOIN request_records r ON r.id=a.request_id
 LEFT JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
 LEFT JOIN model_bindings b ON b.route_policy_version_id=p.id AND b.channel_id=a.channel_id AND b.channel_version_id=a.channel_version_id AND b.upstream_model=a.upstream_model
 LEFT JOIN LATERAL (SELECT cv.id,cv.input_rate,cv.output_rate,cv.basis FROM cost_versions cv,instant
 WHERE cv.channel_id=a.channel_id AND cv.upstream_model=a.upstream_model AND cv.effective_at<=instant.at
 ORDER BY cv.effective_at DESC,cv.created_at DESC,cv.id DESC LIMIT 1) c ON true
 WHERE a.tenant_id=$1 AND a.id=$2`, tenant, attempt, at).Scan(&policy.Mode, &policy.UnknownCostPolicy, &maxIn, &maxOut,
		&frozen.ID, &frozen.InputRate, &frozen.OutputRate, &frozen.Basis, &current.ID, &current.InputRate, &current.OutputRate, &currentBasis)
	if err != nil {
		return err
	}
	var procurement bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM procurement_reservations WHERE attempt_id=$1)`, attempt).Scan(&procurement); err != nil {
		return err
	}
	if !policy.Managed(maxIn, maxOut) && !procurement {
		return nil
	}
	current.Basis = "unknown"
	if currentBasis != nil {
		current.Basis = *currentBasis
	}
	if procurement && (current.InputRate == nil || current.OutputRate == nil || !oneOf(current.Basis, "contract", "invoice")) {
		return fail("cost_unknown", "controlled procurement requires a complete quote")
	}
	if !quoteOf(frozen).Equal(quoteOf(current)) {
		return fail("cost_quote_stale", "configured procurement quote changed before dispatch")
	}
	return checkCostExclusion(policy, quoteOf(current), maxIn, maxOut)
}
