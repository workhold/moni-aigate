package billing

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/routecost"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

// Missing versions retain the original request domain and explicit unknown.
// Corrupt known rates are never silently downgraded to unknown.
const attemptCostSelectSQL = `
 SELECT ms.canonical,ms.schema_digest,s.variant_key,coalesce(c.id,''),coalesce(c.basis,'unknown'),c.rates_canonical,c.rates_digest
 FROM request_meter_snapshots s JOIN meter_schemas ms ON ms.id=s.schema_id AND ms.schema_digest=s.schema_digest
 CROSS JOIN instant
 LEFT JOIN LATERAL (SELECT cv.id,cv.basis,cv.rates_canonical,cv.rates_digest FROM cost_versions cv
 WHERE cv.channel_id=$1 AND cv.upstream_model=$2 AND cv.schema_id=s.schema_id AND cv.schema_digest=s.schema_digest
 AND cv.variant_key=s.variant_key AND cv.effective_at<=instant.at
 ORDER BY cv.effective_at DESC,cv.created_at DESC,cv.id DESC LIMIT 1) c ON true
 WHERE s.request_id=$3`
const currentAttemptCostSQL = `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at)` + attemptCostSelectSQL
const dispatchAttemptCostSQL = `WITH instant AS MATERIALIZED (SELECT $4::timestamptz AS at)` + attemptCostSelectSQL

const bindingCostLimitsSQL = `COALESCE((SELECT jsonb_agg(jsonb_build_object(
 'dimension',l.dimension,'unit',l.unit,'numerator',l.numerator::text,'denominator',l.denominator::text)
 ORDER BY l.dimension COLLATE "C") FROM binding_cost_rate_limits l WHERE l.binding_id=b.id),'[]'::jsonb)`

func scanCostQuote(row pgx.Row, channel, upstream string) (routecost.Quote, error) {
	in := routecost.StoredInput{ChannelID: channel, UpstreamModel: upstream}
	if err := row.Scan(&in.SchemaCanonical, &in.SchemaDigest, &in.VariantKey, &in.VersionID, &in.Basis, &in.RatesCanonical, &in.RatesDigest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return routecost.Quote{}, fail("invalid_meter_snapshot", "original quote domain is missing")
		}
		return routecost.Quote{}, err
	}
	q, err := routecost.ParseStoredQuote(in)
	if err != nil {
		return routecost.Quote{}, fail("invalid_meter_snapshot", "original quote canonical or binding is invalid")
	}
	return q, nil
}

// New-attempt only: tightening prices must never obstruct financial cleanup.
func checkAttemptCostQuote(ctx context.Context, tx pgx.Tx, r financialRequest, in AttemptInput, cost routecost.Quote) error {
	var procurement bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM credential_procurement_bindings WHERE credential_id=$1)`, in.CredentialID).Scan(&procurement); err != nil {
		return err
	}
	if procurement {
		if !cost.Known() {
			return fail("cost_unknown", "controlled procurement requires a complete known quote")
		}
		if in.CostQuote == nil || !in.CostQuote.Equal(cost) {
			return fail("cost_quote_stale", "procurement quote changed before attempt")
		}
	}
	var policy routing.CostPolicy
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT p.cost_mode,p.unknown_cost_policy,`+bindingCostLimitsSQL+`
 FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id
 WHERE p.model_id=$1 AND p.revision=$2 AND b.channel_id=$3 AND b.channel_version_id=$4 AND b.upstream_model=$5`,
		r.ModelID, r.RouteRevision, in.ChannelID, in.ChannelVersionID, in.UpstreamModel).Scan(&policy.Mode, &policy.UnknownCostPolicy, &raw)
	if err != nil {
		return err
	}
	limits, err := routecost.ParseLimits(cost.Schema(), raw)
	if err != nil {
		return fail("invalid_meter_snapshot", "original route cost limits are invalid")
	}
	if !policy.Managed(limits) && !procurement {
		return nil
	}
	if in.CostQuote == nil || !in.CostQuote.Equal(cost) {
		return fail("cost_quote_stale", "configured procurement quote changed before dispatch")
	}
	return checkCostExclusion(policy, cost, limits)
}

func checkCostExclusion(policy routing.CostPolicy, quote routecost.Quote, limits routecost.Limits) error {
	reason, err := routing.CostExclusion(policy, quote, limits)
	if err != nil {
		return fail("invalid_meter_snapshot", "original route cost policy or quote is invalid")
	}
	switch reason {
	case "cost_unknown":
		return fail("cost_unknown", "route requires a known procurement quote")
	case "cost_limit_exceeded":
		return fail("cost_limit_exceeded", "configured procurement rate exceeds route limit")
	}
	return nil
}

// All locks are already held. Reuse the caller's decision instant after waits.
func checkDispatchCostQuote(ctx context.Context, tx pgx.Tx, tenant, attempt string, at time.Time) error {
	var policy routing.CostPolicy
	var raw []byte
	var channel, upstream, request string
	var procurement bool
	err := tx.QueryRow(ctx, `SELECT coalesce(p.cost_mode,'manual'),coalesce(p.unknown_cost_policy,'allow'),`+bindingCostLimitsSQL+`,
 a.channel_id,a.upstream_model,a.request_id,EXISTS(SELECT 1 FROM procurement_reservations pr WHERE pr.attempt_id=a.id)
 FROM upstream_attempts a JOIN request_records r ON r.tenant_id=a.tenant_id AND r.id=a.request_id
	 JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
	 JOIN model_bindings b ON b.route_policy_version_id=p.id AND b.channel_id=a.channel_id AND b.channel_version_id=a.channel_version_id AND b.upstream_model=a.upstream_model
 WHERE a.tenant_id=$1 AND a.id=$2`, tenant, attempt).Scan(&policy.Mode, &policy.UnknownCostPolicy, &raw, &channel, &upstream, &request, &procurement)
	if err != nil {
		return err
	}
	frozen, err := scanCostQuote(tx.QueryRow(ctx, `SELECT ms.canonical,ms.schema_digest,s.variant_key,coalesce(s.cost_version_id,''),s.cost_basis,c.rates_canonical,c.rates_digest
 FROM upstream_attempts a
 JOIN attempt_meter_snapshots s ON s.tenant_id=a.tenant_id AND s.request_id=a.request_id AND s.attempt_id=a.id
 JOIN request_meter_snapshots r ON r.tenant_id=s.tenant_id AND r.request_id=s.request_id AND r.schema_id=s.schema_id AND r.schema_digest=s.schema_digest AND r.variant_key=s.variant_key
 JOIN meter_schemas ms ON ms.id=s.schema_id AND ms.schema_digest=s.schema_digest
 LEFT JOIN cost_versions c ON c.id=s.cost_version_id AND c.channel_id=a.channel_id AND c.upstream_model=a.upstream_model
 AND c.schema_id=s.schema_id AND c.schema_digest=s.schema_digest AND c.variant_key=s.variant_key AND c.basis=s.cost_basis
 WHERE a.tenant_id=$1 AND a.id=$2 AND (s.cost_version_id IS NULL OR c.id IS NOT NULL)
 AND (s.plan_digest IS NULL OR (s.supplier_rates_canonical IS NOT DISTINCT FROM c.rates_canonical AND s.supplier_rates_digest IS NOT DISTINCT FROM c.rates_digest))`, tenant, attempt), channel, upstream)
	if err != nil {
		return err
	}
	limits, err := routecost.ParseLimits(frozen.Schema(), raw)
	if err != nil {
		return fail("invalid_meter_snapshot", "original route cost limits are invalid")
	}
	if !policy.Managed(limits) && !procurement {
		return nil
	}
	current, err := scanCostQuote(tx.QueryRow(ctx, dispatchAttemptCostSQL, channel, upstream, request, at), channel, upstream)
	if err != nil {
		return err
	}
	if procurement && !current.Known() {
		return fail("cost_unknown", "controlled procurement requires a complete quote")
	}
	if !frozen.Equal(current) {
		return fail("cost_quote_stale", "configured procurement quote changed before dispatch")
	}
	return checkCostExclusion(policy, current, limits)
}
