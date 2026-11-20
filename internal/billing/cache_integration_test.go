package billing

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func cacheTestRoute(t *testing.T, f *fixture, revision int64, ttl int) {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	policy := uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO route_policy_versions(id,model_id,revision,profile_version,cache_ttl_seconds) VALUES($1,$2,$3,$4,$5)`, policy, f.model, revision, fixtureProfileVersion, ttl); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key) VALUES($1,$2,$3,'upstream',0,1,10,'default')`, policy, f.channel, f.version); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE models SET active_revision=$2 WHERE id=$1`, f.model, revision); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func cacheTestInput(f *fixture, revision int64) ReserveInput {
	in := f.input()
	in.RouteRevision = revision
	return in
}

func cacheTestOriginal(t *testing.T, f *fixture, revision int64, digest []byte) (Reservation, Finalization) {
	t.Helper()
	r, err := f.store.ReserveWithCache(f.ctx, cacheTestInput(f, revision), digest)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.store.RecordAttempt(f.ctx, dispatchBatchAttemptInput(f, r))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	in := procurementFinal(t, f, r, a, confirmed(), "done")
	in.FinishReason = "stop"
	final, err := f.store.RecordFinalization(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	return r, final
}

func cacheTestLookup(f *fixture, request Reservation, revision int64, digest []byte) CacheLookupInput {
	return CacheLookupInput{TenantID: f.tenant, RequestID: request.RequestID, Protocol: "openai_chat", ModelID: f.model, RouteRevision: revision, RequestDigest: digest}
}

func TestPostgresCacheHitSameChargeIsolationReplayAndCleanup(t *testing.T) {
	f := integration(t)
	const revision = int64(2)
	cacheTestRoute(t, f, revision, 60)
	digest := bytes.Repeat([]byte{0x5a}, 32)
	original, originalFinal := cacheTestOriginal(t, f, revision, digest)
	body := []byte(`{"id":"cached-response","choices":[],"private_body_marker":"not-a-log-field"}`)
	created, err := f.store.PutCache(f.ctx, CachePutInput{TenantID: f.tenant, OriginalRequestID: original.RequestID, ResponseBody: body})
	if err != nil || created {
		t.Fatal("cache write occurred before original settlement", created, err)
	}
	originalSettlement, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: original.RequestID})
	if err != nil || originalSettlement.ChargedAmount != 15 {
		t.Fatal(originalSettlement, err)
	}
	created, err = f.store.PutCache(f.ctx, CachePutInput{TenantID: f.tenant, OriginalRequestID: original.RequestID, ResponseBody: body})
	if err != nil || !created {
		t.Fatal("settled original was not cached", created, err)
	}
	created, err = f.store.PutCache(f.ctx, CachePutInput{TenantID: f.tenant, OriginalRequestID: original.RequestID, ResponseBody: []byte(`{"id":"overwrite"}`)})
	if err != nil || created {
		t.Fatal("same-key put overwrote or extended the original entry", created, err)
	}

	// A separately authorized Key in the same tenant may share the entry. The
	// normal Reserve lock/auth path still applies before this lookup input exists.
	secondKey := uuid.NewString()
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'cache-second','second','second-digest',$4,1000)`, secondKey, f.tenant, f.user, []string{f.model}); err != nil {
		t.Fatal(err)
	}
	hitInput := cacheTestInput(f, revision)
	hitInput.Principal.KeyID = secondKey
	hit, err := f.store.ReserveWithCache(f.ctx, hitInput, digest)
	if err != nil {
		t.Fatal(err)
	}
	found, ok, err := f.store.LookupCache(f.ctx, cacheTestLookup(f, hit, revision, digest))
	if err != nil || !ok || !bytes.Equal(found.Body, body) {
		t.Fatal("authorized same-tenant cache hit lost original bytes", ok, err)
	}
	finalInput := CacheFinalizationInput{ID: uuid.NewString(), TenantID: f.tenant, RequestID: hit.RequestID, CacheID: found.ID, SourceEventID: uuid.NewString()}
	hitFinal, err := f.store.RecordCacheFinalization(f.ctx, finalInput)
	if err != nil || hitFinal.EventID != "" || hitFinal.Usage != confirmed() || hitFinal.DeliveryStage != "none" || hitFinal.ContentWritten {
		t.Fatal("cache finalization did not copy the original final retail usage", hitFinal, err)
	}
	hitSettlement, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: hit.RequestID})
	if err != nil || hitSettlement.ChargedAmount != originalSettlement.ChargedAmount || hitSettlement.RawRetailAmount != originalSettlement.RawRetailAmount {
		t.Fatal("cache hit did not use the hit request's own original price", hitSettlement, originalSettlement, err)
	}
	replayedFinal, err := f.store.RecordCacheFinalization(f.ctx, finalInput)
	if err != nil || replayedFinal.ID != hitFinal.ID {
		t.Fatal("cache finalization replay created or changed facts", replayedFinal, err)
	}
	replayedSettlement, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: hit.RequestID, Mode: "recovery"})
	if err != nil || replayedSettlement.ID != hitSettlement.ID {
		t.Fatal("cache recovery duplicated settlement", replayedSettlement, err)
	}
	var counts struct {
		requests, reservations, finals, settlements, attempts, events, procurement int
		source, original                                                           string
		zeroSupplier                                                               bool
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM request_records WHERE id=$2),(SELECT count(*) FROM reservations WHERE request_id=$2),
 (SELECT count(*) FROM finalization_records WHERE request_id=$2),(SELECT count(*) FROM settlements WHERE request_id=$2),
 (SELECT count(*) FROM upstream_attempts WHERE request_id=$2),(SELECT count(*) FROM metering_events WHERE request_id=$2),
 (SELECT count(*) FROM procurement_reservations p WHERE NOT EXISTS(SELECT 1 FROM upstream_attempts a WHERE a.id=p.attempt_id)),
 f.source,f.original_finalization_id::text,
 f.supplier_quantities_canonical=meter_side_quantity_canonical(q.schema_id,'supplier',0,0)
 FROM finalization_records f JOIN request_meter_snapshots q ON q.tenant_id=f.tenant_id AND q.request_id=f.request_id
 WHERE f.id=$1`, hitFinal.ID, hit.RequestID).Scan(&counts.requests, &counts.reservations, &counts.finals, &counts.settlements, &counts.attempts, &counts.events, &counts.procurement, &counts.source, &counts.original, &counts.zeroSupplier); err != nil {
		t.Fatal(err)
	}
	if counts.requests != 1 || counts.reservations != 1 || counts.finals != 1 || counts.settlements != 1 || counts.attempts != 0 || counts.events != 0 || counts.procurement != 0 || counts.source != "cache" || counts.original != originalFinal.ID || !counts.zeroSupplier {
		t.Fatalf("cache-hit commercial cardinality/zero supplier contract failed: %+v", counts)
	}
	report, err := f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.KnownProviderCost != "30" || report.UnknownProviderCosts != 0 {
		t.Fatalf("cache hit supplier cost was unknown or copied original cost: %+v, err=%v", report, err)
	}
	_, err = f.store.ReserveWithCache(f.ctx, hitInput, digest)
	assertCode(t, err, "request_exists")

	if _, err = f.pool.Exec(f.ctx, `DELETE FROM text_response_cache WHERE id=$1`, found.ID); err == nil {
		t.Fatal("unexpired cache row was deleted directly")
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE text_response_cache SET response_body=$2 WHERE id=$1`, found.ID, []byte(`{"id":"tamper"}`)); err == nil {
		t.Fatal("cache body was mutable")
	}
	// Explicit test-only aging in this isolated schema avoids sleeping for TTL.
	if _, err = f.pool.Exec(f.ctx, `ALTER TABLE text_response_cache DISABLE TRIGGER text_response_cache_mutation`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE text_response_cache SET expires_at=clock_timestamp()-interval '1 microsecond' WHERE id=$1`, found.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, `ALTER TABLE text_response_cache ENABLE TRIGGER text_response_cache_mutation`); err != nil {
		t.Fatal(err)
	}
	removed, err := f.store.CleanupCache(f.ctx, 100)
	if err != nil || removed != 1 {
		t.Fatal("expired cache body was not deleted", removed, err)
	}
	if _, err = f.store.RecordCacheFinalization(f.ctx, finalInput); err != nil {
		t.Fatal("immutable finalization replay incorrectly required deleted cache row", err)
	}

	missInput := cacheTestInput(f, revision)
	miss, err := f.store.ReserveWithCache(f.ctx, missInput, digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err = f.store.LookupCache(f.ctx, cacheTestLookup(f, miss, revision, digest)); err != nil || ok {
		t.Fatal("expired cache entry still hit", ok, err)
	}
	if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: miss.RequestID, Reason: "not_sent"}); err != nil {
		t.Fatal(err)
	}

	cacheTestRoute(t, f, 3, 60)
	otherRoute, err := f.store.ReserveWithCache(f.ctx, cacheTestInput(f, 3), digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err = f.store.LookupCache(f.ctx, cacheTestLookup(f, otherRoute, 3, digest)); err != nil || ok {
		t.Fatal("cache crossed route revisions", ok, err)
	}
	if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: otherRoute.RequestID, Reason: "not_sent"}); err != nil {
		t.Fatal(err)
	}
	lookup := cacheTestLookup(f, hit, revision, digest)
	lookup.TenantID = uuid.NewString()
	if _, ok, err = f.store.LookupCache(f.ctx, lookup); err != nil || ok {
		t.Fatal("cache crossed tenants", ok, err)
	}
	streamingInput := cacheTestInput(f, 3)
	streamingInput.Streaming = true
	streaming, err := f.store.Reserve(f.ctx, streamingInput)
	if err != nil {
		t.Fatal("enabled route rejected ineligible streaming request without a digest", err)
	}
	var digestStored bool
	if err = f.pool.QueryRow(f.ctx, `SELECT cache_request_digest IS NOT NULL FROM request_records WHERE id=$1`, streaming.RequestID).Scan(&digestStored); err != nil || digestStored {
		t.Fatal("ineligible streaming request acquired a cache key", digestStored, err)
	}
	if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: streaming.RequestID, Reason: "not_sent"}); err != nil {
		t.Fatal(err)
	}
	f.wallet(t, 970, 0)
	f.check(t)
}

func TestPostgresCacheFinalizationGuardRejectsSupplierTamper(t *testing.T) {
	f := integration(t)
	cacheTestRoute(t, f, 2, 60)
	digest := bytes.Repeat([]byte{0x6b}, 32)
	original, _ := cacheTestOriginal(t, f, 2, digest)
	if _, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: original.RequestID}); err != nil {
		t.Fatal(err)
	}
	created, err := f.store.PutCache(f.ctx, CachePutInput{TenantID: f.tenant, OriginalRequestID: original.RequestID, ResponseBody: []byte(`{"id":"original"}`)})
	if err != nil || !created {
		t.Fatal(created, err)
	}
	candidate, err := f.store.ReserveWithCache(f.ctx, cacheTestInput(f, 2), digest)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	finalID := uuid.NewString()
	_, err = tx.Exec(f.ctx, `INSERT INTO finalization_records(
 id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,
 delivery_stage,content_written,finish_reason,policy_version,attempt_id,
 retail_usage_state,supplier_usage_state,retail_quantities_canonical,retail_quantities_digest,
 supplier_quantities_canonical,supplier_quantities_digest,source,source_event_id,
 original_finalization_id,cache_id)
 SELECT $1,r.tenant_id,r.id,NULL,$2,'completed','done','none',false,c.finish_reason,r.policy_version,NULL,
 'confirmed_final','confirmed_final',c.retail_quantities_canonical,c.retail_quantities_digest,
 meter_side_quantity_canonical(q.schema_id,'supplier',1,0),
 meter_final_digest(q.schema_digest,'supplier',meter_side_quantity_canonical(q.schema_id,'supplier',1,0)),
 'cache',$3,c.original_finalization_id,c.id
 FROM text_response_cache c JOIN request_records r ON r.tenant_id=c.tenant_id AND r.id=$4
 JOIN request_meter_snapshots q ON q.tenant_id=r.tenant_id AND q.request_id=r.id`, finalID, strings.Repeat("a", 64), uuid.NewString(), candidate.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE request_records SET latest_finalization_id=$2,status='succeeded' WHERE id=$1`, candidate.RequestID, finalID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil {
		t.Fatal("nonzero supplier cache finalization passed the deferred guard")
	}
	var finals, settlements int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM finalization_records WHERE request_id=$1),(SELECT count(*) FROM settlements WHERE request_id=$1)`, candidate.RequestID).Scan(&finals, &settlements); err != nil || finals != 0 || settlements != 0 {
		t.Fatal("tampered cache finalization left financial facts", finals, settlements, err)
	}
	if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: candidate.RequestID, Reason: "not_sent"}); err != nil {
		t.Fatal(err)
	}
	f.check(t)
}
