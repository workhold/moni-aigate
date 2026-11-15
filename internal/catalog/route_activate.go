package catalog

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ActivateRouteRevision repoints a model's active route revision to an
// already-published revision. It is the audited rollback switch for moving a
// capability between channel topologies, for example returning a capability
// from an embedded direct adapter back to an external relay channel. Requests
// already admitted keep the revision frozen in their reservation and their
// frozen candidate set; only requests admitted after the switch observe it.
//
// The target must be a complete, previously published revision of the same
// model and profile; activation never edits immutable route history.
func (s *Store) ActivateRouteRevision(ctx context.Context, actor, model string, expected, target int64) (int64, error) {
	if !canonicalPublicUUID(model) || target <= 0 {
		return 0, ErrInvalid
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	var current int64
	if e = tx.QueryRow(ctx, "SELECT active_revision FROM models WHERE id=$1 FOR UPDATE", model).Scan(&current); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return 0, ErrInvalid
		}
		return 0, e
	}
	if expected != current {
		return 0, ErrConflict
	}
	if target == current {
		return current, nil
	}
	// The database trigger meter_model_route_matches already rejects an
	// active revision without a matching published policy and profile.
	var published bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM route_policy_versions p JOIN models m ON m.id=p.model_id AND m.profile_version=p.profile_version WHERE p.model_id=$1 AND p.revision=$2 AND p.profile_version=(SELECT profile_version FROM models WHERE id=$1))`, model, target).Scan(&published); e != nil {
		return 0, e
	}
	if !published {
		return 0, ErrInvalid
	}
	// Match publication's lock order before evaluating live credentials. A
	// concurrent revocation either finishes before this read or waits for the
	// activation transaction; it cannot slip between the check and commit.
	rows, e := tx.Query(ctx, `SELECT c.id,k.id FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id JOIN channels c ON c.id=b.channel_id JOIN channel_version_credentials cp ON cp.channel_version_id=b.channel_version_id AND cp.channel_id=b.channel_id JOIN channel_credentials k ON k.id=cp.credential_id AND k.channel_id=cp.channel_id WHERE p.model_id=$1 AND p.revision=$2 ORDER BY c.id,k.id FOR SHARE OF c,k`, model, target)
	if e != nil {
		return 0, e
	}
	var locked int
	for rows.Next() {
		var channelID, credentialID string
		if e = rows.Scan(&channelID, &credentialID); e != nil {
			rows.Close()
			return 0, e
		}
		locked++
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return 0, e
	}
	rows.Close()
	if locked == 0 {
		return 0, ErrInvalid
	}
	// A route revision is only re-activatable while its bindings still point
	// at live, non-isolated channel versions with at least one unrevoked pool
	// credential; the same admission gates as a fresh publish apply, so a
	// rollback cannot resurrect a dead route.
	var dead, bindings int
	if e = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE NOT c.enabled OR c.quarantined OR i.channel_version_id IS NOT NULL OR NOT EXISTS(SELECT 1 FROM channel_version_credentials cp JOIN channel_credentials k ON k.id=cp.credential_id AND k.channel_id=cp.channel_id WHERE cp.channel_version_id=b.channel_version_id AND cp.channel_id=b.channel_id AND k.revoked_at IS NULL)),count(*) FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id JOIN channels c ON c.id=b.channel_id JOIN channel_versions v ON v.id=b.channel_version_id LEFT JOIN route_binding_isolations i ON i.channel_version_id=b.channel_version_id AND i.upstream_model=b.upstream_model WHERE p.model_id=$1 AND p.revision=$2`, model, target).Scan(&dead, &bindings); e != nil {
		return 0, e
	}
	if bindings == 0 || dead != 0 {
		return 0, ErrInvalid
	}
	if _, e = tx.Exec(ctx, "UPDATE models SET active_revision=$2 WHERE id=$1", model, target); e != nil {
		return 0, e
	}
	if e = audit(ctx, tx, actor, "route.activate", model); e != nil {
		return 0, e
	}
	return target, tx.Commit(ctx)
}
