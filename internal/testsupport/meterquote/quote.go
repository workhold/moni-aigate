// Package meterquote supplies current-schema quote projections to integration
// tests. It has no legacy or per-million projection and is not used by serve.
package meterquote

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/routecost"
)

type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func Select(ctx context.Context, db Querier, model, channel, upstream string) (routecost.Quote, error) {
	in := routecost.StoredInput{ChannelID: channel, UpstreamModel: upstream}
	err := db.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at)
 SELECT s.canonical,s.schema_digest,'default',coalesce(c.id,''),coalesce(c.basis,'unknown'),c.rates_canonical,c.rates_digest
 FROM models m JOIN model_capability_versions p ON p.id=m.profile_version AND p.model_id=m.id
 JOIN meter_schemas s ON s.id=p.schema_id AND s.schema_digest=p.schema_digest CROSS JOIN instant
 LEFT JOIN LATERAL (SELECT cv.* FROM cost_versions cv WHERE cv.channel_id=$2 AND cv.upstream_model=$3
 AND cv.schema_id=s.id AND cv.schema_digest=s.schema_digest AND cv.variant_key='default' AND cv.effective_at<=instant.at
 ORDER BY cv.effective_at DESC,cv.created_at DESC,cv.id DESC LIMIT 1)c ON true WHERE m.id=$1`, model, channel, upstream).Scan(&in.SchemaCanonical, &in.SchemaDigest, &in.VariantKey, &in.VersionID, &in.Basis, &in.RatesCanonical, &in.RatesDigest)
	if err != nil {
		return routecost.Quote{}, err
	}
	return routecost.ParseStoredQuote(in)
}
