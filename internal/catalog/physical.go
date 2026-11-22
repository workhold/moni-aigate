package catalog

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func equivalentPhysical(profile provider.Profile, maxInflight int) routing.PhysicalCapability {
	usage := profile.UsageSemantics
	if usage == "" {
		usage = "final"
	}
	return routing.PhysicalCapability{
		ContextLimit: profile.ContextLimit, InputUpperBound: profile.InputTokenUpperBound, OutputLimit: profile.OutputLimit,
		SupportsTools: profile.SupportsTools, SupportsParallelToolCalls: profile.SupportsParallelToolCalls,
		UsageSemantics: usage, Modalities: []string{"text"}, QualityTier: "standard", MaxConcurrency: maxInflight,
		SupportedParameters: append([]string{}, profile.AllowedParameters...),
	}
}

func ensurePhysicalModel(ctx context.Context, tx pgx.Tx, model string, profile provider.Profile, binding BindingInput) (string, routing.PhysicalCapability, error) {
	if binding.PhysicalModelID != "" {
		var upstream string
		var state string
		var raw []byte
		var expose bool
		err := tx.QueryRow(ctx, `SELECT upstream_model,state,capability,expose_to_customer FROM physical_models WHERE id=$1 AND logical_model_id=$2`, binding.PhysicalModelID, model).Scan(&upstream, &state, &raw, &expose)
		if err != nil || upstream != binding.UpstreamModel || state != "active" || binding.ExposeToCustomer != nil && *binding.ExposeToCustomer != expose {
			return "", routing.PhysicalCapability{}, ErrInvalid
		}
		var capability routing.PhysicalCapability
		if json.Unmarshal(raw, &capability) != nil || capability.Validate() != nil {
			return "", routing.PhysicalCapability{}, ErrInvalid
		}
		if binding.Capability != nil && !reflect.DeepEqual(*binding.Capability, capability) {
			return "", routing.PhysicalCapability{}, ErrInvalid
		}
		return binding.PhysicalModelID, capability, nil
	}
	capability := equivalentPhysical(profile, binding.MaxInflight)
	if binding.Capability != nil {
		capability = *binding.Capability
		capability.SupportedParameters = append([]string{}, binding.Capability.SupportedParameters...)
		capability.Modalities = append([]string{}, binding.Capability.Modalities...)
	}
	if capability.Validate() != nil {
		return "", routing.PhysicalCapability{}, ErrInvalid
	}
	var id string
	var raw []byte
	var expose bool
	err := tx.QueryRow(ctx, `SELECT id,capability,expose_to_customer FROM physical_models WHERE logical_model_id=$1 AND upstream_model=$2 AND state='active'`, model, binding.UpstreamModel).Scan(&id, &raw, &expose)
	if err == nil {
		var existing routing.PhysicalCapability
		if json.Unmarshal(raw, &existing) != nil {
			return "", routing.PhysicalCapability{}, ErrConflict
		}
		expected := capability
		if binding.Capability == nil {
			expected = equivalentPhysical(profile, binding.MaxInflight)
			expected.MaxConcurrency = existing.MaxConcurrency
		}
		if !reflect.DeepEqual(existing, expected) || binding.ExposeToCustomer != nil && *binding.ExposeToCustomer != expose {
			return "", routing.PhysicalCapability{}, ErrConflict
		}
		return id, existing, nil
	}
	if err != pgx.ErrNoRows {
		return "", routing.PhysicalCapability{}, err
	}
	raw, _ = json.Marshal(capability)
	expose = false
	if binding.ExposeToCustomer != nil {
		expose = *binding.ExposeToCustomer
	}
	if err = tx.QueryRow(ctx, `INSERT INTO physical_models(logical_model_id,upstream_model,capability,expose_to_customer) VALUES($1,$2,$3,$4) RETURNING id`, model, binding.UpstreamModel, raw, expose).Scan(&id); err != nil {
		return "", routing.PhysicalCapability{}, err
	}
	return id, capability, nil
}

func (s *Store) RetirePhysicalModel(ctx context.Context, model, physical string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE physical_models SET state='retired' WHERE id=$1 AND logical_model_id=$2 AND state='active'`, physical, model)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrInvalid
	}
	return tx.Commit(ctx)
}

func (s *Store) RouteSession(ctx context.Context, tenant, session string) (*routing.StickyTarget, error) {
	if !routing.ValidSessionID(session) {
		return nil, ErrInvalid
	}
	var target routing.StickyTarget
	err := s.Pool.QueryRow(ctx, `SELECT physical_model_id,channel_id FROM route_sessions WHERE tenant_id=$1 AND session_id=$2 AND expires_at>clock_timestamp()`, tenant, session).Scan(&target.PhysicalModelID, &target.ChannelID)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &target, nil
}

func (s *Store) RefreshRouteStats(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `WITH stats_clock AS MATERIALIZED (SELECT clock_timestamp() AS at), samples AS (
 SELECT d.selected_physical_model_id AS physical_model_id,a.channel_id,a.created_at,a.first_content_at,f.protocol_outcome,f.transport_end_reason,w.at AS observed_at
 FROM upstream_attempts a JOIN route_decisions d ON d.attempt_id=a.id CROSS JOIN stats_clock w
 LEFT JOIN LATERAL (SELECT f.protocol_outcome,f.transport_end_reason FROM finalization_records f WHERE f.tenant_id=a.tenant_id AND f.attempt_id=a.id ORDER BY f.created_at DESC,f.id DESC LIMIT 1) f ON true
 WHERE a.completed_at IS NOT NULL AND a.completed_at>=w.at-interval '15 minutes'
), aggregate AS (
 SELECT physical_model_id,channel_id,min(observed_at) AS observed_at,count(*) AS samples,
  count(*) FILTER(WHERE protocol_outcome='completed') AS successes,count(*) FILTER(WHERE transport_end_reason='platform_timeout') AS timeouts,
  count(first_content_at) AS latency_samples,
  percentile_cont(0.5) WITHIN GROUP(ORDER BY extract(epoch FROM(first_content_at-created_at))*1000000) FILTER(WHERE first_content_at IS NOT NULL) AS p50,
  percentile_cont(0.95) WITHIN GROUP(ORDER BY extract(epoch FROM(first_content_at-created_at))*1000000) FILTER(WHERE first_content_at IS NOT NULL) AS p95
 FROM samples GROUP BY physical_model_id,channel_id
)
INSERT INTO route_stats_windows(physical_model_id,channel_id,window_started_at,sample_count,success_count,timeout_count,p50_first_content_us,p95_first_content_us,throughput_micro_s)
SELECT physical_model_id,channel_id,observed_at-interval '15 minutes',samples,successes,timeouts,
 CASE WHEN latency_samples>=50 THEN p50::bigint END,CASE WHEN latency_samples>=50 THEN p95::bigint END,
 CASE WHEN samples>=50 THEN successes*1000000/900 END FROM aggregate
ON CONFLICT(physical_model_id,channel_id,window_started_at) DO UPDATE SET sample_count=excluded.sample_count,success_count=excluded.success_count,timeout_count=excluded.timeout_count,p50_first_content_us=excluded.p50_first_content_us,p95_first_content_us=excluded.p95_first_content_us,throughput_micro_s=excluded.throughput_micro_s,refreshed_at=clock_timestamp()`)
	return err
}

func (s *Store) attachRouteStats(ctx context.Context, model string, candidates []Candidate) error {
	if len(candidates) == 0 {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `WITH stats_clock AS MATERIALIZED (SELECT clock_timestamp() AS at), samples AS (
 SELECT d.selected_physical_model_id AS physical_model_id,a.channel_id,a.created_at,a.first_content_at,f.protocol_outcome,f.transport_end_reason
 FROM upstream_attempts a JOIN route_decisions d ON d.attempt_id=a.id JOIN physical_models m ON m.id=d.selected_physical_model_id CROSS JOIN stats_clock w
 LEFT JOIN LATERAL (SELECT f.protocol_outcome,f.transport_end_reason FROM finalization_records f WHERE f.tenant_id=a.tenant_id AND f.attempt_id=a.id ORDER BY f.created_at DESC,f.id DESC LIMIT 1) f ON true
 WHERE m.logical_model_id=$1 AND a.completed_at IS NOT NULL AND a.completed_at>=w.at-interval '15 minutes'
), aggregate AS (
 SELECT physical_model_id,channel_id,count(*) AS samples,
  count(*) FILTER(WHERE protocol_outcome='completed') AS successes,
  count(*) FILTER(WHERE transport_end_reason='platform_timeout') AS timeouts,
  count(first_content_at) AS latency_samples,
  percentile_cont(0.5) WITHIN GROUP(ORDER BY extract(epoch FROM(first_content_at-created_at))*1000000) FILTER(WHERE first_content_at IS NOT NULL) AS p50,
  percentile_cont(0.95) WITHIN GROUP(ORDER BY extract(epoch FROM(first_content_at-created_at))*1000000) FILTER(WHERE first_content_at IS NOT NULL) AS p95
 FROM samples GROUP BY physical_model_id,channel_id
) SELECT physical_model_id,channel_id,samples,successes,timeouts,latency_samples,p50,p95 FROM aggregate`, model)
	if err != nil {
		return err
	}
	defer rows.Close()
	type key struct{ physical, channel string }
	values := map[key]routing.StatsSnapshot{}
	for rows.Next() {
		var physical, channel string
		var stats routing.StatsSnapshot
		var latencySamples int64
		var p50, p95 *float64
		if err = rows.Scan(&physical, &channel, &stats.SampleCount, &stats.SuccessCount, &stats.TimeoutCount, &latencySamples, &p50, &p95); err != nil {
			return err
		}
		if stats.SampleCount >= 50 {
			throughput := stats.SuccessCount * 1_000_000 / 900
			stats.ThroughputMicroS = &throughput
			if latencySamples >= 50 && p50 != nil && p95 != nil {
				p50Value, p95Value := int64(*p50), int64(*p95)
				stats.P50FirstContentUS = &p50Value
				stats.P95FirstContentUS = &p95Value
			}
		}
		values[key{physical, channel}] = stats
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range candidates {
		candidates[i].Stats = values[key{candidates[i].PhysicalModelID, candidates[i].ChannelID}]
	}
	return nil
}
