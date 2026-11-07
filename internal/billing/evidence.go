package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/routecost"
	"github.com/moni-ai/moni-aigate/internal/security"
)

type financialRequest struct {
	RouteRevision                                                                                                  int64
	ID, TenantID, KeyID, OwnerID, ModelID, ProfileVersion, PolicyVersion, ReservationID, ReservationStatus, Status string
	ClientIP                                                                                                       string
	LatestFinalization                                                                                             *string
	Amount                                                                                                         int64
	RequestDeadline, Deadline                                                                                      time.Time
}

func lockRequest(ctx context.Context, tx pgx.Tx, tenant, id string) (financialRequest, error) {
	return lockRequestWithChannel(ctx, tx, tenant, id, "")
}
func lockRequestWithChannel(ctx context.Context, tx pgx.Tx, tenant, id, channel string) (financialRequest, error) {
	var r financialRequest
	err := tx.QueryRow(ctx, `SELECT r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.profile_version,r.policy_version,r.request_deadline,r.status,r.latest_finalization_id,r.route_revision,COALESCE(host(r.client_ip),'') FROM request_records r JOIN request_meter_snapshots s ON s.tenant_id=r.tenant_id AND s.request_id=r.id WHERE r.tenant_id=$1 AND r.id=$2`, tenant, id).Scan(&r.ID, &r.TenantID, &r.KeyID, &r.OwnerID, &r.ModelID, &r.ProfileVersion, &r.PolicyVersion, &r.RequestDeadline, &r.Status, &r.LatestFinalization, &r.RouteRevision, &r.ClientIP)
	if err != nil {
		return r, err
	}
	err = func() (err error) {
		batch := &pgx.Batch{}
		batch.Queue("SELECT id FROM tenants WHERE id=$1 FOR SHARE", tenant)
		batch.Queue("SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE", tenant, r.OwnerID)
		batch.Queue("SELECT id FROM api_keys WHERE tenant_id=$1 AND id=$2 FOR UPDATE", tenant, r.KeyID)
		batch.Queue("SELECT id FROM models WHERE id=$1 FOR SHARE", r.ModelID)
		if channel != "" {
			batch.Queue("SELECT enabled,quarantined FROM channels WHERE id=$1 FOR SHARE", channel)
		}
		batch.Queue("SELECT id,amount,status,deadline FROM reservations WHERE tenant_id=$1 AND request_id=$2 FOR UPDATE", tenant, id)
		results := tx.SendBatch(ctx, batch)
		defer func() { err = closeBatch(results, err) }()
		var ignored string
		for i := 0; i < 4; i++ {
			if err = results.QueryRow().Scan(&ignored); err != nil {
				return err
			}
		}
		if channel != "" {
			var enabled, quarantined bool
			if err = results.QueryRow().Scan(&enabled, &quarantined); err != nil {
				return err
			}
			if !enabled || quarantined {
				return fail("channel_unavailable", "channel is not available")
			}
		}
		return results.QueryRow().Scan(&r.ReservationID, &r.Amount, &r.ReservationStatus, &r.Deadline)
	}()
	if err != nil {
		return r, err
	}
	// Read current evidence after the synchronization lock, never from the earlier snapshot.
	err = tx.QueryRow(ctx, "SELECT status,latest_finalization_id FROM request_records WHERE tenant_id=$1 AND id=$2", tenant, id).Scan(&r.Status, &r.LatestFinalization)
	return r, err
}

func (s *Store) RecordAttempt(ctx context.Context, in AttemptInput) (Attempt, error) {
	var result Attempt
	if in.Sequence < 1 || in.Sequence > 2 || !boundedString(in.UpstreamModel, 200) {
		return result, fail("invalid_attempt", "invalid attempt input")
	}
	decisions, err := validatedRouteDecisions(in)
	if err != nil {
		return result, err
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		r, err := lockRequestWithChannel(ctx, tx, in.TenantID, in.RequestID, in.ChannelID)
		if err != nil {
			return err
		}
		cost, err := readAttemptChecks(ctx, tx, r, in)
		if err != nil {
			return err
		}
		if err = checkAttemptCostQuote(ctx, tx, r, in, cost); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, "INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,route_decisions) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(request_id,sequence) DO NOTHING RETURNING id,request_id,sequence,delivery_state,cost_status", uuid.NewString(), in.TenantID, in.RequestID, in.Sequence, in.ChannelID, in.ChannelVersionID, in.CredentialID, in.UpstreamModel, decisions).Scan(&result.ID, &result.RequestID, &result.Sequence, &result.DeliveryState, &result.CostStatus)
		created := err == nil
		if errors.Is(err, pgx.ErrNoRows) {
			var channel, version, credential, model string
			err = tx.QueryRow(ctx, "SELECT id,request_id,sequence,delivery_state,cost_status,channel_id,channel_version_id,credential_id,upstream_model FROM upstream_attempts WHERE tenant_id=$1 AND request_id=$2 AND sequence=$3", in.TenantID, in.RequestID, in.Sequence).Scan(&result.ID, &result.RequestID, &result.Sequence, &result.DeliveryState, &result.CostStatus, &channel, &version, &credential, &model)
			if err != nil {
				return err
			}
			if channel != in.ChannelID || version != in.ChannelVersionID || credential != in.CredentialID || model != in.UpstreamModel {
				return fail("idempotency_conflict", "attempt sequence already used")
			}
		}
		if err == nil && created {
			if _, err = tx.Exec(ctx, `SELECT meter_insert_attempt_snapshot($1,$2,$3,$4,$5)`, in.TenantID, result.ID, in.RequestID, nullableCostVersion(cost), cost.Basis()); err != nil {
				return err
			}
			return recordProcurementAttempt(ctx, tx, r, in, result.ID, cost)
		}
		return err
	})
	return result, err
}

func nullableCostVersion(q routecost.Quote) any {
	if q.VersionID() == "" {
		return nil
	}
	return q.VersionID()
}

// The caller already holds the request's original parent/reservation locks and
// has refreshed its evidence. Keep each read a separate ordered statement so
// isolation and retry evidence use post-lock READ COMMITTED snapshots. Only
// reads are queued: no attempt is inserted until all domain checks succeed.
func readAttemptChecks(ctx context.Context, tx pgx.Tx, r financialRequest, in AttemptInput) (cost routecost.Quote, err error) {
	batch := &pgx.Batch{}
	// lockRequestWithChannel already owns this Key's row lock. A new statement
	// sees policy changes committed before that lock without applying the check
	// to settlement/release paths that share the request-lock helper.
	batch.Queue("SELECT k.allowed_client_cidrs,k.scopes,k.allowed_regions,k.revoked_at IS NOT NULL,k.expires_at,v.region,clock_timestamp() FROM api_keys k LEFT JOIN channel_versions v ON v.id=$3 WHERE k.tenant_id=$1 AND k.id=$2", r.TenantID, r.KeyID, in.ChannelVersionID)
	batch.Queue(bindingIsolationSQL, in.ChannelVersionID, in.UpstreamModel)
	batch.Queue(`SELECT EXISTS(SELECT 1 FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id WHERE p.model_id=$1 AND p.revision=$2 AND b.channel_id=$3 AND b.channel_version_id=$4 AND b.upstream_model=$5)`, r.ModelID, r.RouteRevision, in.ChannelID, in.ChannelVersionID, in.UpstreamModel)
	batch.Queue(`SELECT EXISTS(SELECT 1 FROM channel_version_credentials WHERE channel_version_id=$1 AND channel_id=$2 AND credential_id=$3)`, in.ChannelVersionID, in.ChannelID, in.CredentialID)
	batch.Queue("SELECT clock_timestamp()<$1", r.RequestDeadline)
	if in.Sequence == 2 && r.LatestFinalization != nil {
		batch.Queue("SELECT EXISTS(SELECT 1 FROM finalization_records f JOIN metering_events e ON e.id=f.event_id JOIN upstream_attempts a ON a.id=e.attempt_id WHERE a.request_id=$1 AND a.sequence=1 AND f.id=$2 AND f.transport_end_reason IN ('not_sent','rejected_no_consumption') AND f.delivery_stage='none' AND NOT f.content_written)", in.RequestID, *r.LatestFinalization)
	}
	batch.Queue(currentAttemptCostSQL, in.ChannelID, in.UpstreamModel, r.ID)
	results := tx.SendBatch(ctx, batch)
	defer func() { err = closeBatch(results, err) }()
	var clientCIDRs, scopes, regions []string
	var revoked bool
	var expires *time.Time
	var region *string
	var now time.Time
	if err = results.QueryRow().Scan(&clientCIDRs, &scopes, &regions, &revoked, &expires, &region, &now); err != nil {
		return cost, err
	}
	if !security.ClientIPAllowed(r.ClientIP, clientCIDRs) {
		return cost, fail("key_ip_forbidden", "request source is not authorized")
	}
	if revoked || expires != nil && !expires.After(now) {
		return cost, fail("invalid_key", "key is not active")
	}
	if !member(scopes, r.ModelID) {
		return cost, fail("forbidden", "key model scope is not authorized")
	}
	var isolated bool
	if err = results.QueryRow().Scan(&isolated); err != nil {
		return cost, err
	}
	if isolated {
		return cost, fail("binding_unavailable", "published model binding is quarantined")
	}
	if r.ReservationStatus != "reserved" {
		return cost, fail("request_finished", "request is not eligible for a new attempt")
	}
	var published bool
	if err = results.QueryRow().Scan(&published); err != nil {
		return cost, err
	}
	if !published {
		return cost, fail("unpublished_binding", "attempt is not part of the request route snapshot")
	}
	var credentialMember bool
	if err = results.QueryRow().Scan(&credentialMember); err != nil {
		return cost, err
	}
	if !credentialMember || region == nil {
		return cost, fail("invalid_attempt", "credential is not a member of the frozen channel version")
	}
	if !member(regions, *region) {
		return cost, fail("forbidden", "key channel region is not authorized")
	}
	var valid bool
	if err = results.QueryRow().Scan(&valid); err != nil {
		return cost, err
	}
	if !valid {
		return cost, fail("request_deadline", "request deadline expired")
	}
	if in.Sequence == 2 {
		if r.LatestFinalization == nil {
			return cost, fail("unsafe_retry", "prior attempt has no terminal evidence")
		}
		var safe bool
		if err = results.QueryRow().Scan(&safe); err != nil {
			return cost, err
		}
		if !safe {
			return cost, fail("unsafe_retry", "prior attempt has no trustworthy non-consumption evidence")
		}
	}
	cost, err = scanCostQuote(results.QueryRow(), in.ChannelID, in.UpstreamModel)
	return cost, err
}

func (s *Store) PrepareDispatch(ctx context.Context, in DispatchInput) error {
	return s.transaction(ctx, func(tx pgx.Tx) error {
		var p Principal
		var model, channel, version, credential, request, upstream string
		p.TenantID = in.TenantID
		err := tx.QueryRow(ctx, "SELECT r.owner_id,r.key_id,r.model_id,a.channel_id,a.channel_version_id,a.credential_id,r.id,a.upstream_model,COALESCE(host(r.client_ip),'') FROM upstream_attempts a JOIN request_records r ON r.id=a.request_id WHERE a.tenant_id=$1 AND a.id=$2", in.TenantID, in.AttemptID).Scan(&p.UserID, &p.KeyID, &model, &channel, &version, &credential, &request, &upstream, &p.ClientIP)
		if err != nil {
			return err
		}
		auth, err := lockPrincipal(ctx, tx, p, model, false)
		if err != nil {
			return err
		}
		region, err := readDispatchChannel(ctx, tx, channel, version, credential, upstream)
		if err != nil {
			return err
		}
		if !member(auth.TenantRegions, region) || !member(auth.KeyRegions, region) {
			return fail("forbidden", "channel region is not authorized")
		}
		if err = readDispatchState(ctx, tx, in.TenantID, request, in.AttemptID, auth.KeyExpiresAt); err != nil {
			return err
		}
		customer, err := periodicbudget.LockCustomerTx(ctx, tx, p.TenantID, p.KeyID)
		if err != nil {
			return periodicError(err)
		}
		supplier, err := prepareProcurementDispatch(ctx, tx, in.AttemptID)
		if err != nil {
			return err
		}
		at, err := periodicbudget.PermitDispatchTx(ctx, tx, customer, supplier, request, in.AttemptID)
		if err != nil {
			return periodicError(err)
		}
		var deadline, requestDeadline time.Time
		if err = tx.QueryRow(ctx, `SELECT v.deadline,r.request_deadline FROM reservations v JOIN request_records r ON r.id=v.request_id WHERE v.tenant_id=$1 AND v.request_id=$2`, p.TenantID, request).Scan(&deadline, &requestDeadline); err != nil {
			return err
		}
		if err = dispatchTimeEligible(at, auth.KeyExpiresAt, deadline, requestDeadline); err != nil {
			return err
		}
		if err = checkDispatchCostQuote(ctx, tx, in.TenantID, in.AttemptID, at); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE request_records SET status='dispatching' WHERE id=$1", request)
		return err
	})
}

func readDispatchChannel(ctx context.Context, tx pgx.Tx, channel, version, credential, upstream string) (region string, err error) {
	batch := &pgx.Batch{}
	batch.Queue("SELECT enabled,quarantined FROM channels WHERE id=$1 FOR SHARE", channel)
	// A new statement snapshot after the channel lock observes a concurrently
	// committed isolation. Do not fold these statements into a JOIN or CTE.
	batch.Queue(bindingIsolationSQL, version, upstream)
	batch.Queue("SELECT channel_id,revoked_at IS NOT NULL FROM channel_credentials WHERE id=$1 FOR SHARE", credential)
	batch.Queue(`SELECT v.region FROM channel_versions v JOIN channel_version_credentials p ON p.channel_version_id=v.id AND p.channel_id=v.channel_id WHERE v.id=$1 AND v.channel_id=$2 AND p.credential_id=$3`, version, channel, credential)
	results := tx.SendBatch(ctx, batch)
	defer func() { err = closeBatch(results, err) }()
	var enabled, quarantined bool
	if err = results.QueryRow().Scan(&enabled, &quarantined); err != nil {
		return region, err
	}
	if !enabled || quarantined {
		return region, fail("channel_unavailable", "channel is not available")
	}
	var isolated bool
	if err = results.QueryRow().Scan(&isolated); err != nil {
		return region, err
	}
	if isolated {
		return region, fail("binding_unavailable", "published model binding is quarantined")
	}
	var credentialChannel string
	var revoked bool
	if err = results.QueryRow().Scan(&credentialChannel, &revoked); err != nil {
		return region, err
	}
	if revoked || credentialChannel != channel {
		return region, fail("channel_unavailable", "channel credential is not available")
	}
	err = results.QueryRow().Scan(&region)
	return region, err
}

func readDispatchState(ctx context.Context, tx pgx.Tx, tenant, request, attempt string, keyExpires *time.Time) (err error) {
	batch := &pgx.Batch{}
	batch.Queue("SELECT v.status,v.deadline,r.request_deadline FROM reservations v JOIN request_records r ON r.id=v.request_id WHERE v.tenant_id=$1 AND v.request_id=$2 FOR UPDATE OF v", tenant, request)
	batch.Queue("SELECT delivery_state FROM upstream_attempts WHERE id=$1 FOR UPDATE", attempt)
	// Both reservation and attempt locks can wait across expiry/deadline.
	batch.Queue("SELECT clock_timestamp()")
	results := tx.SendBatch(ctx, batch)
	defer func() { err = closeBatch(results, err) }()
	var status string
	var deadline, requestDeadline, now time.Time
	if err = results.QueryRow().Scan(&status, &deadline, &requestDeadline); err != nil {
		return err
	}
	var state string
	if err = results.QueryRow().Scan(&state); err != nil {
		return err
	}
	if err = results.QueryRow().Scan(&now); err != nil {
		return err
	}
	if keyExpires != nil && !keyExpires.After(now) {
		return fail("invalid_key", "key is not active")
	}
	if status != "reserved" || !now.Before(deadline) || !now.Before(requestDeadline) {
		return fail("request_deadline", "request is no longer eligible for dispatch")
	}
	if state != "prepared" {
		return fail("attempt_already_dispatched", "attempt cannot be dispatched again")
	}
	return nil
}

func (s *Store) RecordFinalization(ctx context.Context, in FinalizationInput) (Finalization, error) {
	var result Finalization
	var semanticError error
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	if !validID(in.ID) || !boundedString(in.Source, 80) || !boundedString(in.SourceEventID, 200) {
		return result, fail("invalid_evidence", "stable source and event identity required")
	}
	if err := validateUsage(in.Usage); err != nil {
		return result, err
	}
	if !oneOf(in.ProtocolOutcome, "completed", "failed", "incomplete", "cancelled", "unknown") || !oneOf(in.TransportEndReason, "done", "eof", "provider_error", "platform_error", "platform_timeout", "client_cancelled", "connection_error", "unknown", "not_sent", "rejected_no_consumption") || !oneOf(in.DeliveryStage, "none", "committed", "content", "unknown") {
		return result, fail("invalid_evidence", "unknown finalization state")
	}
	if len(in.FinishReason) > 80 {
		return result, fail("invalid_evidence", "invalid finish reason")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		r, err := lockRequest(ctx, tx, in.TenantID, in.RequestID)
		if err != nil {
			return err
		}
		result, semanticError, err = recordFinalization(ctx, tx, r, in)
		return err
	})
	if err != nil {
		return Finalization{}, err
	}
	return result, semanticError
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func recordFinalization(ctx context.Context, tx pgx.Tx, r financialRequest, in FinalizationInput) (Finalization, error, error) {
	var f Finalization
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	identity := in
	identity.ObservedAt = time.Time{}
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		return f, nil, err
	}
	digestBytes := sha256.Sum256(identityJSON)
	inputDigest := hex.EncodeToString(digestBytes[:])
	var storedDigest string
	err = tx.QueryRow(ctx, "SELECT input_digest FROM finalization_records WHERE id=$1", in.ID).Scan(&storedDigest)
	if err == nil && storedDigest == inputDigest {
		prior, err := finalizationRow(ctx, tx, in.ID)
		return prior, nil, err
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return f, nil, err
	}
	if in.ObservedAt.IsZero() {
		in.ObservedAt = time.Now().UTC()
	}
	if in.AttemptID != "" {
		var request string
		if err := tx.QueryRow(ctx, "SELECT request_id FROM upstream_attempts WHERE tenant_id=$1 AND id=$2", r.TenantID, in.AttemptID).Scan(&request); err != nil {
			return f, nil, err
		}
		if request != r.ID {
			return f, nil, fail("invalid_evidence", "attempt belongs to a different request")
		}
	}
	if in.ContentWritten || in.DeliveryStage == "content" {
		in.ContentWritten = true
		in.DeliveryStage = "content"
	}
	if r.LatestFinalization != nil {
		prior, err := finalizationRow(ctx, tx, *r.LatestFinalization)
		if err != nil {
			return f, nil, err
		}
		if prior.ContentWritten || prior.DeliveryStage == "content" {
			in.ContentWritten = true
			in.DeliveryStage = "content"
		} else if prior.DeliveryStage == "committed" && oneOf(in.DeliveryStage, "none", "unknown") {
			in.DeliveryStage = "committed"
		}
	}
	eventID := uuid.NewString()
	var attempt any
	if in.AttemptID != "" {
		attempt = in.AttemptID
	} else {
		bound, err := boundAttempt(ctx, tx, r)
		if err != nil {
			return f, nil, err
		}
		if bound != "" {
			attempt = bound
		}
	}
	if attempt == nil {
		return f, nil, fail("invalid_evidence", "observation requires the original attempt")
	}
	tag, err := tx.Exec(ctx, `INSERT INTO metering_events(id,tenant_id,request_id,attempt_id,source,source_event_id,happened_at,profile_version,schema_id,schema_digest,variant_key,plan_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,retail_usage_state,supplier_usage_state,observation_canonical,observation_digest)
SELECT $1,$2,$3,$4,$5,$6,$7,COALESCE(a.profile_version,q.profile_version),COALESCE(a.schema_id,q.schema_id),COALESCE(a.schema_digest,q.schema_digest),'default',COALESCE(a.plan_digest,q.plan_digest),
 $9,$10,$11,$12,$13,meter_usage_state($8),meter_usage_state($8),
 meter_observation_canonical($2,$3,$4,$5,$6,COALESCE(a.profile_version,q.profile_version),COALESCE(a.schema_id,q.schema_id),COALESCE(a.schema_digest,q.schema_digest),COALESCE(a.plan_digest,q.plan_digest),$7,$9,$10,$11,$12,$13,NULL,meter_usage_state($8),$14,meter_usage_state($8),$15),
 meter_observation_digest(COALESCE(a.schema_digest,q.schema_digest),meter_observation_identity_canonical($2,$3,$4,$5,$6,COALESCE(a.profile_version,q.profile_version),COALESCE(a.schema_id,q.schema_id),COALESCE(a.schema_digest,q.schema_digest),COALESCE(a.plan_digest,q.plan_digest),$9,$10,$11,$12,$13,NULL,meter_usage_state($8),$14,meter_usage_state($8),$15))
 FROM request_meter_snapshots q JOIN attempt_meter_snapshots a ON a.attempt_id=$4 AND a.tenant_id=q.tenant_id AND a.request_id=q.request_id
 WHERE q.tenant_id=$2 AND q.request_id=$3
 ON CONFLICT(tenant_id,source,source_event_id) DO NOTHING`, eventID, r.TenantID, r.ID, attempt, in.Source, in.SourceEventID, in.ObservedAt, in.Usage.Status, in.ProtocolOutcome, in.TransportEndReason, in.DeliveryStage, in.ContentWritten, in.FinishReason, in.Usage.InputTokens, in.Usage.OutputTokens)
	if err != nil {
		return f, nil, err
	}
	conflict := false
	if tag.RowsAffected() == 1 {
		if _, err = tx.Exec(ctx, `SELECT meter_record_event_quantities($1,$2,$3,$4,$5)`, eventID, r.TenantID, in.Usage.Status, in.Usage.InputTokens, in.Usage.OutputTokens); err != nil {
			return f, nil, err
		}
	}
	if tag.RowsAffected() == 0 {
		var equal bool
		err = tx.QueryRow(ctx, `SELECT e.id,(e.retail_usage_state=meter_usage_state($4) AND e.request_id=$5 AND coalesce(e.attempt_id,'')=$6 AND meter_event_quantity(e.id,'text_input_token') IS NOT DISTINCT FROM CASE WHEN meter_usage_state($4) IN ('missing','conflicting') THEN NULL ELSE $7::bigint END AND meter_event_quantity(e.id,'text_output_token') IS NOT DISTINCT FROM CASE WHEN meter_usage_state($4) IN ('missing','conflicting') THEN NULL ELSE $8::bigint END) FROM metering_events e WHERE e.tenant_id=$1 AND e.source=$2 AND e.source_event_id=$3`, r.TenantID, in.Source, in.SourceEventID, in.Usage.Status, r.ID, in.AttemptID, in.Usage.InputTokens, in.Usage.OutputTokens).Scan(&eventID, &equal)
		if err != nil {
			return f, nil, err
		}
		conflict = !equal
	}
	if !conflict && in.AttemptID != "" && in.Usage.Status == "confirmed_final" {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM metering_events e WHERE e.tenant_id=$1 AND e.attempt_id=$2 AND e.id<>$3 AND e.retail_usage_state='confirmed_final' AND (meter_event_quantity(e.id,'text_input_token') IS DISTINCT FROM $4 OR meter_event_quantity(e.id,'text_output_token') IS DISTINCT FROM $5))`, r.TenantID, in.AttemptID, eventID, in.Usage.InputTokens, in.Usage.OutputTokens).Scan(&conflict)
		if err != nil {
			return f, nil, err
		}
	}
	if conflict {
		raw, err := json.Marshal(in.Usage)
		if err != nil {
			return f, nil, err
		}
		sum := sha256.Sum256(raw)
		if _, err = tx.Exec(ctx, "INSERT INTO metering_event_conflicts(tenant_id,event_id,candidate_canonical,candidate_digest) VALUES($1,$2,$3,$4)", r.TenantID, eventID, raw, sum[:]); err != nil {
			return f, nil, err
		}
		if in.AttemptID != "" {
			var lockedAttempt string
			if err = tx.QueryRow(ctx, "SELECT id FROM upstream_attempts WHERE tenant_id=$1 AND id=$2 FOR UPDATE", r.TenantID, in.AttemptID).Scan(&lockedAttempt); err != nil {
				return f, nil, err
			}
			if err = finalizeProcurement(ctx, tx, in.AttemptID, eventID, "", in.Usage, in.TransportEndReason, true); err != nil {
				return f, nil, err
			}
			// Repeated, already-adjudicated supplier evidence cannot erase the
			// authoritative invoice fact. A new managed dispute is unknown.
			if _, err = tx.Exec(ctx, `UPDATE upstream_attempts SET cost_status='unknown',cost_amount=NULL,latest_cost_finalization_id=NULL,latest_cost_correction_id=NULL WHERE tenant_id=$1 AND id=$2
 AND NOT EXISTS(SELECT 1 FROM procurement_reservations p WHERE p.attempt_id=$2 AND p.state IN ('settled','released'))`, r.TenantID, in.AttemptID); err != nil {
				return f, nil, err
			}
		}
		if err = markPending(ctx, tx, r, "metering_conflict"); err != nil {
			return f, nil, err
		}
		return f, fail("metering_conflict", "conflicting usage requires reconciliation"), nil
	}
	f = Finalization{ID: in.ID, EventID: eventID, RequestID: r.ID, Usage: in.Usage, ProtocolOutcome: in.ProtocolOutcome, TransportEndReason: in.TransportEndReason, DeliveryStage: in.DeliveryStage, ContentWritten: in.ContentWritten, FinishReason: in.FinishReason}
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	tag, err = tx.Exec(ctx, `INSERT INTO finalization_records(id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,policy_version,attempt_id,retail_usage_state,supplier_usage_state,retail_quantities_canonical,retail_quantities_digest,supplier_quantities_canonical,supplier_quantities_digest)
SELECT $1,$2,$3,e.id,$4,$5,$6,$7,$8,$9,$10,e.attempt_id,e.retail_usage_state,
 CASE WHEN e.source='approved_reconciliation' AND e.supplier_usage_state<>'conflicting' THEN 'missing' ELSE e.supplier_usage_state END,
 CASE WHEN e.retail_usage_state='confirmed_final' THEN meter_side_quantity_canonical(e.schema_id,'retail',qi.quantity,qo.quantity) END,
 CASE WHEN e.retail_usage_state='confirmed_final' THEN meter_final_digest(e.schema_digest,'retail',meter_side_quantity_canonical(e.schema_id,'retail',qi.quantity,qo.quantity)) END,
 CASE WHEN e.supplier_usage_state='confirmed_final' AND e.source<>'approved_reconciliation' THEN meter_side_quantity_canonical(e.schema_id,'supplier',qi.quantity,qo.quantity) END,
 CASE WHEN e.supplier_usage_state='confirmed_final' AND e.source<>'approved_reconciliation' THEN meter_final_digest(e.schema_digest,'supplier',meter_side_quantity_canonical(e.schema_id,'supplier',qi.quantity,qo.quantity)) END
FROM metering_events e
JOIN meter_event_quantities qi ON qi.event_id=e.id AND qi.dimension='text_input_token'
JOIN meter_event_quantities qo ON qo.event_id=e.id AND qo.dimension='text_output_token'
WHERE e.id=$11 AND e.tenant_id=$2 AND e.request_id=$3
ON CONFLICT(id) DO NOTHING`, f.ID, r.TenantID, r.ID, inputDigest, f.ProtocolOutcome, f.TransportEndReason, f.DeliveryStage, f.ContentWritten, f.FinishReason, r.PolicyVersion, eventID)
	if err != nil {
		return f, nil, err
	}
	if tag.RowsAffected() == 0 {
		var equal bool
		err = tx.QueryRow(ctx, "SELECT input_digest=$2 FROM finalization_records WHERE id=$1", f.ID, inputDigest).Scan(&equal)
		if err != nil {
			return f, nil, err
		}
		if !equal {
			return f, nil, fail("idempotency_conflict", "finalization id already used")
		}
		return f, nil, nil
	}
	if _, err = tx.Exec(ctx, "UPDATE request_records SET latest_finalization_id=$2,status=$3 WHERE id=$1", r.ID, f.ID, requestStatus(f)); err != nil {
		return f, nil, err
	}
	if in.AttemptID != "" {
		state := "unknown"
		if f.ProtocolOutcome == "completed" {
			state = "completed"
		} else if f.ProtocolOutcome == "failed" {
			state = "failed"
		}
		if _, err = tx.Exec(ctx, "UPDATE upstream_attempts SET delivery_state=$2 WHERE id=$1", in.AttemptID, state); err != nil {
			return f, nil, err
		}
		if err = recordAttemptCost(ctx, tx, r, in.AttemptID, f.ID, in.TransportEndReason); err != nil {
			return f, nil, err
		}
		if err = finalizeProcurement(ctx, tx, in.AttemptID, eventID, f.ID, in.Usage, in.TransportEndReason, false); err != nil {
			return f, nil, err
		}
	}
	return f, nil, nil
}

func boundAttempt(ctx context.Context, tx pgx.Tx, r financialRequest) (string, error) {
	var n int
	var only *string
	if err := tx.QueryRow(ctx, `SELECT count(*)::int, CASE WHEN count(*)=1 THEN min(id) END FROM upstream_attempts WHERE tenant_id=$1 AND request_id=$2`, r.TenantID, r.ID).Scan(&n, &only); err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	if n == 1 && only != nil {
		return *only, nil
	}
	if r.LatestFinalization != nil {
		var prior *string
		if err := tx.QueryRow(ctx, `SELECT attempt_id FROM finalization_records WHERE tenant_id=$1 AND id=$2`, r.TenantID, *r.LatestFinalization).Scan(&prior); err != nil {
			return "", err
		}
		if prior != nil && *prior != "" {
			return *prior, nil
		}
	}
	return "", fail("invalid_evidence", "observation requires the original attempt")
}

func requestStatus(f Finalization) string {
	if f.TransportEndReason == "client_cancelled" {
		return "cancelled"
	}
	if f.ProtocolOutcome == "completed" {
		return "succeeded"
	}
	return "failed"
}

func recordAttemptCost(ctx context.Context, tx pgx.Tx, r financialRequest, attempt, finalization, reason string) error {
	var basis, status, supplierState string
	var disputed bool
	if err := tx.QueryRow(ctx, `SELECT s.cost_basis,a.cost_status,f.supplier_usage_state,EXISTS(SELECT 1 FROM procurement_reservations p WHERE p.attempt_id=a.id AND p.state='disputed') FROM upstream_attempts a JOIN attempt_meter_snapshots s ON s.attempt_id=a.id AND s.tenant_id=a.tenant_id AND s.request_id=a.request_id JOIN finalization_records f ON f.tenant_id=a.tenant_id AND f.request_id=a.request_id AND f.attempt_id=a.id WHERE a.tenant_id=$1 AND a.request_id=$2 AND a.id=$3 AND f.id=$4`, r.TenantID, r.ID, attempt, finalization).Scan(&basis, &status, &supplierState, &disputed); err != nil {
		return err
	}
	if status != "unknown" || disputed {
		return nil
	}
	var amount *string
	if oneOf(reason, "not_sent", "rejected_no_consumption") {
		zero := "0"
		amount = &zero
		basis = "contract"
	} else if supplierState == "confirmed_final" && oneOf(basis, "contract", "invoice") {
		n, err := storedActualAmount(ctx, tx, r.TenantID, r.ID, finalization, attempt, metering.Supplier)
		if err != nil {
			return err
		}
		value := n.String()
		amount = &value
	}
	if amount == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, "UPDATE upstream_attempts SET cost_amount=$2::text::numeric,cost_status=$3,latest_cost_finalization_id=$4,latest_cost_correction_id=NULL WHERE id=$1", attempt, *amount, basis, finalization); err != nil {
		return err
	}
	return emit(ctx, tx, r.TenantID, attempt, "cost_recorded", map[string]any{"attempt_id": attempt, "amount": *amount, "basis": basis})
}

const bindingIsolationSQL = "SELECT EXISTS(SELECT 1 FROM route_binding_isolations WHERE channel_version_id=$1 AND upstream_model=$2)"
