package catalog

import (
	"context"
	"encoding/json"
	"time"
)

type RouteRevisionBinding struct {
	ChannelID        string `json:"channel_id"`
	ChannelName      string `json:"channel_name"`
	ChannelKind      string `json:"channel_kind"`
	IntegrationMode  string `json:"integration_mode"`
	ChannelVersionID string `json:"channel_version_id"`
	UpstreamModel    string `json:"upstream_model"`
	Priority         int    `json:"priority"`
	Weight           int    `json:"weight"`
	MaxInflight      int    `json:"max_inflight"`
}

type RouteRevision struct {
	ID             string                 `json:"id"`
	Revision       int64                  `json:"revision"`
	CreatedAt      time.Time              `json:"-"`
	ProfileVersion string                 `json:"profile_version"`
	ReleaseIntent  string                 `json:"release_intent"`
	Active         bool                   `json:"active"`
	CurrentProfile bool                   `json:"current_profile"`
	Bindings       []RouteRevisionBinding `json:"bindings"`
}

type RouteRevisionCursor struct {
	CreatedAt time.Time
	ID        string
}

// ListRouteRevisions reads immutable routing metadata without performing any
// provider I/O or treating the active pointer as a monotonic publish counter.
func (s *Store) ListRouteRevisions(ctx context.Context, model string, after *RouteRevisionCursor) ([]RouteRevision, *RouteRevisionCursor, error) {
	if !canonicalPublicUUID(model) {
		return nil, nil, ErrInvalid
	}
	query := `SELECT p.id,p.revision,p.created_at,p.profile_version,p.release_intent,p.revision=m.active_revision,p.profile_version=m.profile_version,COALESCE(jsonb_agg(jsonb_build_object('channel_id',c.id,'channel_name',c.name,'channel_kind',v.channel_kind,'integration_mode',v.integration_mode,'channel_version_id',v.id,'upstream_model',b.upstream_model,'priority',b.priority,'weight',b.weight,'max_inflight',b.max_inflight) ORDER BY c.id COLLATE "C",b.id COLLATE "C") FILTER (WHERE b.id IS NOT NULL),'[]'::jsonb) FROM models m JOIN route_policy_versions p ON p.model_id=m.id LEFT JOIN model_bindings b ON b.route_policy_version_id=p.id LEFT JOIN channels c ON c.id=b.channel_id LEFT JOIN channel_versions v ON v.id=b.channel_version_id AND v.channel_id=b.channel_id WHERE m.id=$1`
	args := []any{model}
	if after != nil {
		query += ` AND (p.created_at,p.id)<($2,$3)`
		args = append(args, after.CreatedAt, after.ID)
	}
	query += ` GROUP BY m.id,p.id ORDER BY p.created_at DESC,p.id DESC LIMIT 51`
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	result := []RouteRevision{}
	for rows.Next() {
		var item RouteRevision
		var bindings []byte
		if err = rows.Scan(&item.ID, &item.Revision, &item.CreatedAt, &item.ProfileVersion, &item.ReleaseIntent, &item.Active, &item.CurrentProfile, &bindings); err != nil {
			return nil, nil, err
		}
		if err = json.Unmarshal(bindings, &item.Bindings); err != nil {
			return nil, nil, err
		}
		item.Bindings = append([]RouteRevisionBinding{}, item.Bindings...)
		result = append(result, item)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *RouteRevisionCursor
	if len(result) > 50 {
		result = result[:50]
		last := result[49]
		next = &RouteRevisionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return result, next, nil
}
