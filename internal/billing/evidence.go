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
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/security"
)

type financialRequest struct {
	RouteRevision                                                                                                  int64
	ID, TenantID, KeyID, OwnerID, ModelID, ProfileVersion, PolicyVersion, ReservationID, ReservationStatus, Status string
	ClientIP                                                                                                       string
	LatestFinalization                                                                                             *string
	Amount, InputRate, OutputRate                                                                                  int64
	RequestDeadline, Deadline                                                                                      time.Time
}

func lockRequest(ctx context.Context, tx pgx.Tx, tenant, id string) (financialRequest, error) {
	return lockRequestWithChannel(ctx, tx, tenant, id, "")
}
func lockRequestWithChannel(ctx context.Context, tx pgx.Tx, tenant, id, channel string) (financialRequest, error) {
	var r financialRequest
	err := tx.QueryRow(ctx, "SELECT id,tenant_id,key_id,owner_id,model_id,profile_version,policy_version,input_rate,output_rate,request_deadline,status,latest_finalization_id,route_revision,COALESCE(host(client_ip),'') FROM request_records WHERE tenant_id=$1 AND id=$2", tenant, id).Scan(&r.ID, &r.TenantID, &r.KeyID, &r.OwnerID, &r.ModelID, &r.ProfileVersion, &r.PolicyVersion, &r.InputRate, &r.OutputRate, &r.RequestDeadline, &r.Status, &r.LatestFinalization, &r.RouteRevision, &r.ClientIP)
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
		err = tx.QueryRow(ctx, "INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,route_decisions) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(request_id,sequence) DO NOTHING RETURNING id,request_id,sequence,delivery_state,cost_status", uuid.NewString(), in.TenantID, in.RequestID, in.Sequence, in.ChannelID, in.ChannelVersionID, in.CredentialID, in.UpstreamModel, cost.ID, cost.InputRate, cost.OutputRate, cost.Basis, decisions).Scan(&result.ID, &result.RequestID, &result.Sequence, &result.DeliveryState, &result.CostStatus)
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
			return recordProcurementAttempt(ctx, tx, r, in, result.ID, cost)
		}
		return err
	})
	return result, err
}

type attemptCost struct {
	ID                    *string
	InputRate, OutputRate *int64
	Basis                 string
}

// The caller already holds the request's original parent/reservation locks and
// has refreshed its evidence. Keep each read a separate ordered statement so
// isolation and retry evidence use post-lock READ COMMITTED snapshots. Only
// reads are queued: no attempt is inserted until all domain checks succeed.
func readAttemptChecks(ctx context.Context, tx pgx.Tx, r financialRequest, in AttemptInput) (cost attemptCost, err error) {
	cost.Basis = "unknown"
	batch := &pgx.Batch{}
	// lockRequestWithChannel already owns this Key's row lock. A new statement
	// sees policy changes committed before that lock without applying the check
	// to settlement/release paths that share the request-lock helper.
	batch.Queue("SELECT k.allowed_client_cidrs,k.scopes,k.allowed_regions,k.revoked_at IS NOT NULL,k.expires_at,v.region,clock_timestamp() FROM api_keys k LEFT JOIN channel_versions v ON v.id=$3 WHERE k.tenant_id=$1 AND k.id=$2", r.TenantID, r.KeyID, in.ChannelVersionID)
	batch.Queue(bindingIsolationSQL, in.ChannelVersionID, in.UpstreamModel)
	if r.RouteRevision > 0 {
		batch.Queue(`SELECT EXISTS(SELECT 1 FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id WHERE p.model_id=$1 AND p.revision=$2 AND b.channel_id=$3 AND b.channel_version_id=$4 AND b.upstream_model=$5)`, r.ModelID, r.RouteRevision, in.ChannelID, in.ChannelVersionID, in.UpstreamModel)
	}
	batch.Queue(`SELECT EXISTS(SELECT 1 FROM channel_version_credentials WHERE channel_version_id=$1 AND channel_id=$2 AND credential_id=$3)`, in.ChannelVersionID, in.ChannelID, in.CredentialID)
	batch.Queue("SELECT clock_timestamp()<$1", r.RequestDeadline)
	if in.Sequence == 2 && r.LatestFinalization != nil {
		batch.Queue("SELECT EXISTS(SELECT 1 FROM finalization_records f JOIN metering_events e ON e.id=f.event_id JOIN upstream_attempts a ON a.id=e.attempt_id WHERE a.request_id=$1 AND a.sequence=1 AND f.id=$2 AND f.transport_end_reason IN ('not_sent','rejected_no_consumption') AND f.delivery_stage='none' AND NOT f.content_written)", in.RequestID, *r.LatestFinalization)
	}
	batch.Queue(currentAttemptCostSQL, in.ChannelID, in.UpstreamModel)
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
	if r.RouteRevision > 0 {
		var published bool
		if err = results.QueryRow().Scan(&published); err != nil {
			return cost, err
		}
		if !published {
			return cost, fail("unpublished_binding", "attempt is not part of the request route snapshot")
		}
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
	err = results.QueryRow().Scan(&cost.ID, &cost.InputRate, &cost.OutputRate, &cost.Basis)
	if errors.Is(err, pgx.ErrNoRows) {
		return cost, nil
	}
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
	usageJSON, err := json.Marshal(in.Usage)
	if err != nil {
		return f, nil, err
	}
	eventID := uuid.NewString()
	var attempt any
	if in.AttemptID != "" {
		attempt = in.AttemptID
	}
	tag, err := tx.Exec(ctx, "INSERT INTO metering_events(id,tenant_id,request_id,attempt_id,source,source_event_id,normalized_usage,happened_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(tenant_id,source,source_event_id) DO NOTHING", eventID, r.TenantID, r.ID, attempt, in.Source, in.SourceEventID, usageJSON, in.ObservedAt)
	if err != nil {
		return f, nil, err
	}
	conflict := false
	if tag.RowsAffected() == 0 {
		var equal bool
		err = tx.QueryRow(ctx, "SELECT id,(normalized_usage=$4::jsonb AND request_id=$5 AND coalesce(attempt_id,'')=$6) FROM metering_events WHERE tenant_id=$1 AND source=$2 AND source_event_id=$3", r.TenantID, in.Source, in.SourceEventID, usageJSON, r.ID, in.AttemptID).Scan(&eventID, &equal)
		if err != nil {
			return f, nil, err
		}
		conflict = !equal
	}
	if !conflict && in.AttemptID != "" && in.Usage.Status == "confirmed_final" {
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM metering_events WHERE tenant_id=$1 AND attempt_id=$2 AND id<>$3 AND normalized_usage->>'status'='confirmed_final' AND normalized_usage<>$4::jsonb)", r.TenantID, in.AttemptID, eventID, usageJSON).Scan(&conflict)
		if err != nil {
			return f, nil, err
		}
	}
	if conflict {
		if _, err = tx.Exec(ctx, "INSERT INTO metering_event_conflicts(event_id,normalized_usage) VALUES($1,$2)", eventID, usageJSON); err != nil {
			return f, nil, err
		}
		if in.AttemptID != "" {
			var lockedAttempt string
			if err = tx.QueryRow(ctx, "SELECT id FROM upstream_attempts WHERE tenant_id=$1 AND id=$2 FOR UPDATE", r.TenantID, in.AttemptID).Scan(&lockedAttempt); err != nil {
				return f, nil, err
			}
			if err = finalizeProcurement(ctx, tx, in.AttemptID, eventID, in.Usage, in.TransportEndReason, true); err != nil {
				return f, nil, err
			}
			// Repeated, already-adjudicated supplier evidence cannot erase the
			// authoritative invoice fact. A new managed dispute is unknown.
			if _, err = tx.Exec(ctx, `UPDATE upstream_attempts SET cost_status='unknown',cost_amount=NULL WHERE tenant_id=$1 AND id=$2
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
	tag, err = tx.Exec(ctx, "INSERT INTO finalization_records(id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,policy_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(id) DO NOTHING", f.ID, r.TenantID, r.ID, eventID, inputDigest, f.ProtocolOutcome, f.TransportEndReason, f.DeliveryStage, f.ContentWritten, f.FinishReason, r.PolicyVersion)
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
		if err = recordAttemptCost(ctx, tx, r, in.AttemptID, in.Usage, in.TransportEndReason); err != nil {
			return f, nil, err
		}
		if err = finalizeProcurement(ctx, tx, in.AttemptID, eventID, in.Usage, in.TransportEndReason, false); err != nil {
			return f, nil, err
		}
	}
	return f, nil, nil
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

func recordAttemptCost(ctx context.Context, tx pgx.Tx, r financialRequest, attempt string, usage Usage, reason string) error {
	var inputRate, outputRate *int64
	var basis, status string
	var disputed bool
	if err := tx.QueryRow(ctx, `SELECT cost_input_rate,cost_output_rate,cost_basis,cost_status,EXISTS(SELECT 1 FROM procurement_reservations p WHERE p.attempt_id=a.id AND p.state='disputed') FROM upstream_attempts a WHERE id=$1`, attempt).Scan(&inputRate, &outputRate, &basis, &status, &disputed); err != nil {
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
	} else if usage.Status == "confirmed_final" && inputRate != nil && outputRate != nil && basis != "unknown" {
		n, err := rawAmount(usage.InputTokens, usage.OutputTokens, Money(*inputRate), Money(*outputRate))
		if err != nil {
			return err
		}
		value := n.String()
		amount = &value
	}
	if amount == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, "UPDATE upstream_attempts SET cost_amount=$2::text::numeric,cost_status=$3 WHERE id=$1", attempt, *amount, basis); err != nil {
		return err
	}
	return emit(ctx, tx, r.TenantID, attempt, "cost_recorded", map[string]any{"attempt_id": attempt, "amount": *amount, "basis": basis})
}

const bindingIsolationSQL = "SELECT EXISTS(SELECT 1 FROM route_binding_isolations WHERE channel_version_id=$1 AND upstream_model=$2)"
