package server

import "github.com/moni-ai/moni-aigate/internal/routing"

// A normalized model configuration failure disables only this immutable
// channel version and upstream model. Admission holds the same parent row in
// SHARE mode, giving isolation and future dispatch a common commit boundary.
func (s *Server) isolateBinding(candidate routing.Candidate) {
	ctx, cancel := cleanupContext()
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var channel string
	if err = tx.QueryRow(ctx, "SELECT id FROM channels WHERE id=$1 FOR UPDATE", candidate.ChannelID).Scan(&channel); err != nil {
		return
	}
	tag, err := tx.Exec(ctx, `INSERT INTO route_binding_isolations(channel_version_id,upstream_model,reason) SELECT id,$2,'model_configuration_error' FROM channel_versions WHERE id=$1 AND channel_id=$3 ON CONFLICT DO NOTHING`, candidate.VersionID, candidate.Model, channel)
	if err != nil {
		return
	}
	if tag.RowsAffected() > 0 {
		if _, err = tx.Exec(ctx, "INSERT INTO audit_events(actor,action,target) VALUES('system','binding.quarantined',$1)", candidate.VersionID+":"+candidate.Model); err != nil {
			return
		}
	}
	_ = tx.Commit(ctx)
}
