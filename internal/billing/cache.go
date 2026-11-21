package billing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CacheLookupInput is evaluated only after the gateway's normal tenant/Key/model
// authorization and idempotency admission. Key IDs intentionally do not form a
// cache boundary within an authorized tenant.
type CacheLookupInput struct {
	TenantID, RequestID, Protocol, ModelID string
	RouteRevision                          int64
	RequestDigest                          []byte
}

type CacheHit struct {
	ID   string
	Body []byte
}

// CacheFinalizationInput contains identities only. Usage, price and charge are
// always rebuilt from the locked cache row and the hit request's own snapshots.
type CacheFinalizationInput struct {
	ID, TenantID, RequestID, CacheID, SourceEventID string
}

type CachePutInput struct {
	TenantID, OriginalRequestID string
	ResponseBody                []byte
}

// ReserveWithCache preserves the ordinary Reserve ABI while recording the
// canonical request digest required by an enabled immutable route revision.
func (s *Store) ReserveWithCache(ctx context.Context, in ReserveInput, requestDigest []byte) (Reservation, error) {
	return s.reserve(ctx, in, requestDigest)
}

func validCacheDigest(digest []byte) bool { return len(digest) == 32 }

func IsCacheMiss(err error) bool {
	var public *Error
	return errors.As(err, &public) && public.Code == "cache_miss"
}

func (s *Store) LookupCache(ctx context.Context, in CacheLookupInput) (CacheHit, bool, error) {
	if !validID(in.TenantID) || !validID(in.RequestID) || !validID(in.ModelID) || in.RouteRevision <= 0 || !validCacheDigest(in.RequestDigest) || !boundedString(in.Protocol, 80) || in.Protocol == "" {
		return CacheHit{}, false, fail("invalid_cache", "invalid cache lookup input")
	}
	var hit CacheHit
	err := s.pool.QueryRow(ctx, `SELECT c.id,c.response_body
 FROM request_records r
 JOIN reservations v ON v.tenant_id=r.tenant_id AND v.request_id=r.id AND v.status='reserved'
 JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
  AND p.profile_version=r.profile_version AND p.cache_ttl_seconds>0
 JOIN text_response_cache c ON c.tenant_id=r.tenant_id AND c.protocol=r.protocol
  AND c.model_id=r.model_id AND c.route_revision=r.route_revision
  AND c.request_digest=r.cache_request_digest AND c.expires_at>clock_timestamp()
 WHERE r.tenant_id=$1 AND r.id=$2 AND r.protocol=$3 AND r.model_id=$4
  AND r.route_revision=$5 AND r.cache_request_digest=$6
  AND r.latest_finalization_id IS NULL AND NOT r.streaming AND r.result_kind='json'
  AND NOT EXISTS(SELECT 1 FROM upstream_attempts a WHERE a.tenant_id=r.tenant_id AND a.request_id=r.id)`,
		in.TenantID, in.RequestID, in.Protocol, in.ModelID, in.RouteRevision, in.RequestDigest).Scan(&hit.ID, &hit.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return CacheHit{}, false, nil
	}
	if err != nil {
		return CacheHit{}, false, err
	}
	hit.Body = bytes.Clone(hit.Body)
	return hit, true, nil
}

func cacheFinalizationDigest(in CacheFinalizationInput) (string, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func cacheFinalizationReplay(ctx context.Context, tx pgx.Tx, in CacheFinalizationInput, inputDigest string) (Finalization, bool, error) {
	var id, storedDigest string
	err := tx.QueryRow(ctx, "SELECT id,input_digest FROM finalization_records WHERE tenant_id=$1 AND id=$2", in.TenantID, in.ID).Scan(&id, &storedDigest)
	if err == nil {
		if storedDigest != inputDigest {
			return Finalization{}, false, fail("idempotency_conflict", "cache finalization id already used")
		}
		prior, err := finalizationRow(ctx, tx, id)
		return prior, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Finalization{}, false, err
	}
	var requestID, cacheID string
	err = tx.QueryRow(ctx, "SELECT id,request_id,cache_id FROM finalization_records WHERE tenant_id=$1 AND source='cache' AND source_event_id=$2", in.TenantID, in.SourceEventID).Scan(&id, &requestID, &cacheID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Finalization{}, false, nil
	}
	if err != nil {
		return Finalization{}, false, err
	}
	if requestID != in.RequestID || cacheID != in.CacheID {
		return Finalization{}, false, fail("idempotency_conflict", "cache source event already used")
	}
	// A retry after a successful finalization COMMIT may have generated a new
	// local UUID before observing the result. Source identity owns replay.
	prior, err := finalizationRow(ctx, tx, id)
	return prior, true, err
}

func (s *Store) RecordCacheFinalization(ctx context.Context, in CacheFinalizationInput) (Finalization, error) {
	var result Finalization
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	if !validID(in.ID) || !validID(in.TenantID) || !validID(in.RequestID) || !validID(in.CacheID) || !boundedString(in.SourceEventID, 200) || in.SourceEventID == "" {
		return result, fail("invalid_evidence", "invalid cache finalization identity")
	}
	inputDigest, err := cacheFinalizationDigest(in)
	if err != nil {
		return result, err
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		r, err := lockRequest(ctx, tx, in.TenantID, in.RequestID)
		if err != nil {
			return err
		}
		if prior, replay, err := cacheFinalizationReplay(ctx, tx, in, inputDigest); err != nil || replay {
			result = prior
			return err
		}
		if r.LatestFinalization != nil || r.ReservationStatus != "reserved" {
			return fail("request_finished", "request is not eligible for cache finalization")
		}
		// Re-read and lock the complete row after the request/reservation locks.
		// Cleanup may delete it only after this transaction releases FOR SHARE.
		var originalID, finishReason string
		if err = tx.QueryRow(ctx, `SELECT c.original_finalization_id,c.finish_reason
 FROM text_response_cache c
 JOIN request_records r ON r.tenant_id=c.tenant_id AND r.id=$2
  AND r.protocol=c.protocol AND r.model_id=c.model_id AND r.route_revision=c.route_revision
  AND r.profile_version=c.profile_version AND r.cache_request_digest=c.request_digest
  AND NOT r.streaming AND r.result_kind='json'
  AND NOT EXISTS(SELECT 1 FROM upstream_attempts a WHERE a.tenant_id=r.tenant_id AND a.request_id=r.id)
 JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
  AND p.profile_version=r.profile_version AND p.cache_ttl_seconds>0
  AND c.expires_at=c.created_at+p.cache_ttl_seconds*interval '1 second'
 JOIN finalization_records o ON o.tenant_id=c.tenant_id AND o.id=c.original_finalization_id
  AND o.request_id=c.original_request_id AND o.source<>'cache'
  AND o.event_id IS NOT NULL AND o.attempt_id IS NOT NULL
  AND o.retail_usage_state='confirmed_final' AND o.supplier_usage_state='confirmed_final'
 JOIN settlements os ON os.tenant_id=o.tenant_id AND os.request_id=o.request_id
  AND os.finalization_id=o.id AND os.status='settled'
 WHERE c.tenant_id=$1 AND c.id=$3 AND c.expires_at>clock_timestamp()
 FOR SHARE OF c`, in.TenantID, in.RequestID, in.CacheID).Scan(&originalID, &finishReason); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fail("cache_miss", "cache entry is no longer eligible")
			}
			return err
		}
		f := Finalization{ID: in.ID, RequestID: in.RequestID, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none", ContentWritten: false, FinishReason: finishReason}
		if err = tx.QueryRow(ctx, `INSERT INTO finalization_records(
 id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,
 delivery_stage,content_written,finish_reason,policy_version,attempt_id,
 retail_usage_state,supplier_usage_state,retail_quantities_canonical,retail_quantities_digest,
 supplier_quantities_canonical,supplier_quantities_digest,source,source_event_id,
 original_finalization_id,cache_id)
 SELECT $1,$2,$3,NULL,$4,'completed','done','none',false,c.finish_reason,r.policy_version,NULL,
  'confirmed_final','confirmed_final',c.retail_quantities_canonical,c.retail_quantities_digest,
  meter_side_quantity_canonical(q.schema_id,'supplier',0,0),
  meter_final_digest(q.schema_digest,'supplier',meter_side_quantity_canonical(q.schema_id,'supplier',0,0)),
  'cache',$5,$6,c.id
 FROM text_response_cache c
 JOIN request_records r ON r.tenant_id=c.tenant_id AND r.id=$3
 JOIN request_meter_snapshots q ON q.tenant_id=r.tenant_id AND q.request_id=r.id
 WHERE c.tenant_id=$2 AND c.id=$7
 RETURNING id`, in.ID, in.TenantID, in.RequestID, inputDigest, in.SourceEventID, originalID, in.CacheID).Scan(&f.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE request_records SET latest_finalization_id=$2,status='succeeded' WHERE tenant_id=$1 AND id=$3", in.TenantID, in.ID, in.RequestID); err != nil {
			return err
		}
		result, err = finalizationRow(ctx, tx, in.ID)
		return err
	})
	if err != nil {
		return Finalization{}, err
	}
	return result, nil
}

// PutCache inserts only an already settled, eligible original. A key conflict
// preserves the first body and original TTL; false is a no-op, not an overwrite.
func (s *Store) PutCache(ctx context.Context, in CachePutInput) (bool, error) {
	body := in.ResponseBody
	trimmed := bytes.TrimSpace(body)
	if !validID(in.TenantID) || !validID(in.OriginalRequestID) || len(body) == 0 || len(body) > 8<<20 || !json.Valid(body) || len(trimmed) == 0 || trimmed[0] != '{' {
		return false, fail("invalid_cache", "invalid cache put input")
	}
	created := false
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		created = false
		tag, err := tx.Exec(ctx, `WITH created AS (SELECT clock_timestamp() AS at)
 INSERT INTO text_response_cache(
  id,tenant_id,protocol,model_id,route_revision,request_digest,response_body,
  retail_quantities_canonical,retail_quantities_digest,finish_reason,
  original_request_id,original_finalization_id,original_source_event_id,
  schema_id,schema_digest,profile_version,variant_key,created_at,expires_at)
 SELECT $1,r.tenant_id,r.protocol,r.model_id,r.route_revision,r.cache_request_digest,$3,
  f.retail_quantities_canonical,f.retail_quantities_digest,f.finish_reason,
  r.id,f.id,f.source_event_id,q.schema_id,q.schema_digest,q.profile_version,q.variant_key,
  created.at,created.at+p.cache_ttl_seconds*interval '1 second'
 FROM created
 CROSS JOIN request_records r
 JOIN finalization_records f ON f.tenant_id=r.tenant_id AND f.request_id=r.id
  AND f.id=r.latest_finalization_id AND f.source<>'cache'
  AND f.event_id IS NOT NULL AND f.attempt_id IS NOT NULL
  AND f.retail_usage_state='confirmed_final' AND f.supplier_usage_state='confirmed_final'
  AND f.protocol_outcome='completed' AND f.transport_end_reason='done' AND f.finish_reason<>'tool_calls'
 JOIN settlements st ON st.tenant_id=r.tenant_id AND st.request_id=r.id
  AND st.finalization_id=f.id AND st.status='settled'
 JOIN request_meter_snapshots q ON q.tenant_id=r.tenant_id AND q.request_id=r.id
 JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision
  AND p.profile_version=r.profile_version AND p.cache_ttl_seconds>0
 WHERE r.tenant_id=$2 AND r.id=$4 AND r.cache_request_digest IS NOT NULL
  AND NOT r.streaming AND r.result_kind='json'
 ON CONFLICT(tenant_id,protocol,model_id,route_revision,request_digest) DO NOTHING`, uuid.NewString(), in.TenantID, body, in.OriginalRequestID)
		created = err == nil && tag.RowsAffected() == 1
		return err
	})
	return created, err
}

// CleanupCache deletes only expired response bodies. Financial and original
// finalization history deliberately has no FK dependency on the cache row ID.
func (s *Store) CleanupCache(ctx context.Context, limit int) (int, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 0 || limit > 1000 {
		return 0, fail("invalid_cache", "invalid cache cleanup limit")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM text_response_cache WHERE id IN (
 SELECT id FROM text_response_cache WHERE expires_at<=clock_timestamp()
 ORDER BY expires_at,id LIMIT $1)`, limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
