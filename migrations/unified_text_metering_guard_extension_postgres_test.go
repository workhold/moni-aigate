package migrations_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func insertGuardRequestWithSnapshotTx(t *testing.T, f *guardFixture, tx pgx.Tx, sourceRequest, requestID string) error {
	if err := insertGuardRequestRootClone(t, tx, sourceRequest, requestID); err != nil {
		return err
	}
	if _, err := tx.Exec(f.ctx, `
INSERT INTO request_meter_snapshots(
 tenant_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,
 price_version_id,pricing_group_id,tenant_pricing_revision,retail_priced_at,
 input_bound,output_bound,plan_canonical,plan_digest,retail_bounds_canonical,
 retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,
 retail_rates_canonical,retail_rates_digest,retail_meter_quote_digest,
 retail_quote_canonical,retail_quote_digest)
SELECT tenant_id,$1,model_id,profile_version,schema_id,schema_digest,variant_key,
 price_version_id,pricing_group_id,tenant_pricing_revision,retail_priced_at,
 input_bound,output_bound,plan_canonical,plan_digest,retail_bounds_canonical,
 retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,
 retail_rates_canonical,retail_rates_digest,retail_meter_quote_digest,
 retail_quote_canonical,retail_quote_digest
FROM request_meter_snapshots WHERE request_id=$2`, requestID, sourceRequest); err != nil {
		return err
	}
	_, err := tx.Exec(f.ctx, `
INSERT INTO request_meter_bound_lines(tenant_id,request_id,side,dimension,unit,quantity)
SELECT tenant_id,$1,side,dimension,unit,quantity
FROM request_meter_bound_lines WHERE request_id=$2`, requestID, sourceRequest)
	return err
}

func recordGuardFinalizationOnly(t *testing.T, f *guardFixture, fact *guardFact) {
	t.Helper()
	final, err := f.store.RecordFinalization(f.ctx, billing.FinalizationInput{
		TenantID: fact.tenant.tenant, RequestID: fact.requestID, AttemptID: fact.attempt,
		Source: "gateway", SourceEventID: uuid.NewString(),
		Usage:           billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none", ContentWritten: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	fact.final, fact.event = final.ID, final.EventID
}

func TestUnifiedTextMeteringAmountAndSettlementGuards(t *testing.T) {
	f := newGuardFixture(t)
	settled := f.fact(f.a)
	f.finalize(&settled)

	var storedAmount, expectedAmount string
	if err := f.owner.QueryRow(f.ctx, `
SELECT r.amount::text,
       meter_token_amount(s.input_bound,s.output_bound,ri.numerator,ri.denominator,ro.numerator,ro.denominator)::text
FROM reservations r
JOIN request_meter_snapshots s ON s.tenant_id=r.tenant_id AND s.request_id=r.request_id
JOIN price_rate_lines ri ON ri.price_version_id=s.price_version_id AND ri.dimension='text_input_token'
JOIN price_rate_lines ro ON ro.price_version_id=s.price_version_id AND ro.dimension='text_output_token'
WHERE r.id=$1`, settled.reservation).Scan(&storedAmount, &expectedAmount); err != nil {
		t.Fatal(err)
	}
	if storedAmount != expectedAmount {
		t.Fatalf("valid reservation amount differs from immutable vector: got %s want %s", storedAmount, expectedAmount)
	}

	requestID := uuid.NewString()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertGuardRequestWithSnapshotTx(t, f, tx, settled.requestID, requestID); err != nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, `
INSERT INTO reservations(id,tenant_id,request_id,amount,deadline)
VALUES($1,$2,$3,($4::numeric+1)::bigint,clock_timestamp()+interval '1 hour')`, uuid.NewString(), f.a.tenant, requestID, expectedAmount)
	if err == nil {
		_, err = tx.Exec(f.ctx, `SET CONSTRAINTS meter_reservation_amount IMMEDIATE`)
	}
	if err == nil || !strings.Contains(err.Error(), "reservation amount does not match original retail vector") {
		_ = tx.Rollback(f.ctx)
		t.Fatalf("invalid reservation amount was not rejected by the amount guard: %v", err)
	}
	_ = tx.Rollback(f.ctx)
	var reservations int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM reservations WHERE request_id=$1`, requestID).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if reservations != 0 {
		t.Fatalf("rejected reservation transaction left %d reservation rows", reservations)
	}

	charged := f.fact(f.a)
	recordGuardFinalizationOnly(t, f, &charged)
	var journalID string
	if err = f.owner.QueryRow(f.ctx, `SELECT journal_id FROM settlements WHERE id=$1`, settled.settlement).Scan(&journalID); err != nil {
		t.Fatal(err)
	}
	tx, err = f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, `
INSERT INTO settlements(id,tenant_id,request_id,finalization_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id)
VALUES($1,$2,$3,$4,0,0,0,'settled','charged_usage',$5)`, uuid.NewString(), f.a.tenant, charged.requestID, charged.final, journalID)
	if err == nil {
		_, err = tx.Exec(f.ctx, `SET CONSTRAINTS meter_settlement_amount IMMEDIATE`)
	}
	if err == nil || !strings.Contains(err.Error(), "settlement amount does not match original retail vector") {
		_ = tx.Rollback(f.ctx)
		t.Fatalf("invalid charged settlement amount was not rejected by the amount guard: %v", err)
	}
	_ = tx.Rollback(f.ctx)

	tx, err = f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, `
INSERT INTO settlements(id,tenant_id,request_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id)
VALUES($1,$2,$3,1,1,0,'released','not_sent',$4)`, uuid.NewString(), f.a.tenant, charged.requestID, journalID)
	if err == nil {
		_, err = tx.Exec(f.ctx, `SET CONSTRAINTS meter_settlement_amount IMMEDIATE`)
	}
	if err == nil || !strings.Contains(err.Error(), "released settlement must be zero-valued") {
		_ = tx.Rollback(f.ctx)
		t.Fatalf("nonzero released settlement was not rejected by the amount guard: %v", err)
	}
	_ = tx.Rollback(f.ctx)
}

func TestUnifiedTextMeteringSchemaAndCapabilityCompletenessGuards(t *testing.T) {
	f := newGuardFixture(t)

	badSchema := uuid.NewString()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `
INSERT INTO meter_schemas(id,name,revision,registry_version,wire_version,canonical,schema_digest)
VALUES($1,'incomplete_schema',1,1,1,'{}'::bytea,decode(repeat('00',32),'hex'))`, badSchema); err == nil {
		_, err = tx.Exec(f.ctx, `
INSERT INTO meter_schema_dimensions(schema_id,dimension,unit,maximum_quantity,retail,supplier)
VALUES($1,'text_input_token','token',100,true,true)`, badSchema)
	}
	if err == nil {
		err = tx.Commit(f.ctx)
	} else {
		_ = tx.Rollback(f.ctx)
	}
	if err == nil || !strings.Contains(err.Error(), "text schema requires input and output dimensions") {
		t.Fatalf("incomplete schema committed: %v", err)
	}
	_ = tx.Rollback(f.ctx)
	var count int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM meter_schemas WHERE id=$1`, badSchema).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected schema transaction left %d rows", count)
	}

	badModel, badProfile := uuid.NewString(), uuid.NewString()
	profile := `{"version":"` + badProfile + `","model":"incomplete-model","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`
	tx, err = f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO models(id,public_name,profile_version) VALUES($1,'incomplete-model',$2)`, badModel, badProfile); err == nil {
		_, err = tx.Exec(f.ctx, `
INSERT INTO model_capability_versions(id,model_id,profile,schema_id,schema_digest,protocol,execution_kind,contract_digest,profile_digest)
SELECT $1,$2,$3::jsonb,schema_id,schema_digest,'openai_chat','synchronous',sha256(convert_to($3::jsonb::text,'UTF8')),sha256(convert_to($3::jsonb::text,'UTF8'))
FROM model_capability_versions WHERE id=$4`, badProfile, badModel, profile, f.profile)
	}
	if err == nil {
		err = tx.Commit(f.ctx)
	} else {
		_ = tx.Rollback(f.ctx)
	}
	if err == nil || !strings.Contains(err.Error(), "capability bounds are incomplete") {
		t.Fatalf("incomplete capability committed: %v", err)
	}
	_ = tx.Rollback(f.ctx)
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM models WHERE id=$1`, badModel).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected capability transaction left model=%d", count)
	}

	inputOnlyProfile, inputOnlyModel := uuid.NewString(), uuid.NewString()
	inputOnly := `{"version":"` + inputOnlyProfile + `","model":"input-only","protocol":"openai_embeddings","input_token_upper_bound":100,"context_limit":100,"output_limit":1,"default_output_limit":1,"output_limit_field":"max_tokens"}`
	if _, err = f.owner.Exec(f.ctx, `SELECT meter_insert_text_model($1,'input-only',$2::jsonb,$3)`, inputOnlyModel, inputOnly, inputOnlyProfile); err == nil || !strings.Contains(err.Error(), "input-only model requires a zero output bound") {
		t.Fatalf("input-only nonzero output capability was accepted: %v", err)
	}
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM models WHERE id=$1`, inputOnlyModel).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected input-only capability left a model")
	}
}

func TestUnifiedTextMeteringFinalizationPointerAndEventIdentityGuards(t *testing.T) {
	f := newGuardFixture(t)
	a := f.fact(f.a)
	b := f.fact(f.a)
	f.finalize(&a)

	expectPoolReject(t, f.owner, "cross-request latest finalization", `UPDATE request_records SET latest_finalization_id=$2 WHERE tenant_id=$1 AND id=$3`, f.a.tenant, a.final, b.requestID)

	expectPoolReject(t, f.owner, "duplicate observation source identity", `
INSERT INTO metering_events(id,tenant_id,request_id,attempt_id,source,source_event_id,happened_at,profile_version,schema_id,schema_digest,variant_key,retail_usage_state,supplier_usage_state,plan_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,observation_canonical,observation_digest)
SELECT $1,tenant_id,request_id,attempt_id,source,source_event_id,happened_at,profile_version,schema_id,schema_digest,variant_key,retail_usage_state,supplier_usage_state,plan_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,observation_canonical,observation_digest
FROM metering_events WHERE id=$2`, uuid.NewString(), a.event)

	var journalID string
	if err := f.owner.QueryRow(f.ctx, `SELECT journal_id FROM settlements WHERE request_id=$1`, a.requestID).Scan(&journalID); err != nil {
		t.Fatal(err)
	}
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, `
INSERT INTO settlements(id,tenant_id,request_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id)
VALUES($1,$2,$3,0,0,0,'settled','charged_usage',$4)`, uuid.NewString(), f.a.tenant, b.requestID, journalID)
	if err == nil {
		_, err = tx.Exec(f.ctx, `SET CONSTRAINTS meter_settlement_finalization IMMEDIATE`)
	}
	if err == nil || !strings.Contains(err.Error(), "charged usage has no finalization") {
		_ = tx.Rollback(f.ctx)
		t.Fatalf("charged settlement without finalization was accepted: %v", err)
	}
	_ = tx.Rollback(f.ctx)
}

func TestUnifiedTextMeteringConcurrentCorrectionIdentityIsUnique(t *testing.T) {
	f := newGuardFixture(t)
	a := f.fact(f.a)
	operation := "guard-concurrent-correction-" + uuid.NewString()
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		id := uuid.NewString()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.owner.Exec(f.ctx, `
INSERT INTO provider_cost_corrections(id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref)
VALUES($1,$2,$3,$4,17,'invoice','concurrent')`, id, f.a.tenant, a.attempt, operation)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	failures := 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent correction identity results success=%d failure=%d", successes, failures)
	}
	var count int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM provider_cost_corrections WHERE operation_id=$1`, operation).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent correction left %d rows", count)
	}
}

func TestUnifiedTextMeteringCurrentShapeHasNoScalarProjection(t *testing.T) {
	f := newGuardFixture(t)
	oldColumns := map[string][]string{
		"price_versions":       []string{"input_rate", "output_rate"},
		"cost_versions":        []string{"input_rate", "output_rate"},
		"request_records":      []string{"price_version_id", "input_rate", "output_rate", "input_upper_bound", "output_limit", "pricing_group_id", "tenant_pricing_revision", "retail_priced_at"},
		"upstream_attempts":    []string{"cost_version_id", "cost_input_rate", "cost_output_rate", "cost_basis"},
		"reconciliation_cases": []string{"proposed_usage"},
	}
	for table, columns := range oldColumns {
		var count int
		if err := f.owner.QueryRow(f.ctx, `
SELECT count(*) FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid=a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname=$1 AND c.relname=$2 AND NOT a.attisdropped AND a.attname=ANY($3::text[])`, f.schema, table, columns).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("current table %s still exposes old scalar columns %v", table, columns)
		}
	}
	var scalarFunctions int
	if err := f.owner.QueryRow(f.ctx, `
SELECT count(*) FROM pg_catalog.pg_proc p
JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname=$1 AND p.proname=ANY($2::text[])`, f.schema, []string{"meter_price_million", "meter_cost_million"}).Scan(&scalarFunctions); err != nil {
		t.Fatal(err)
	}
	if scalarFunctions != 0 {
		t.Fatalf("current schema still exposes %d scalar meter functions", scalarFunctions)
	}
	var vectorColumns int
	if err := f.owner.QueryRow(f.ctx, `
SELECT count(*) FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid=a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname=$1 AND c.relname='request_meter_snapshots' AND NOT a.attisdropped
  AND a.attname=ANY($2::text[])`, f.schema, []string{"schema_id", "schema_digest", "variant_key", "retail_rates_canonical", "retail_rates_digest", "retail_bounds_canonical", "retail_bounds_digest", "retail_meter_quote_digest"}).Scan(&vectorColumns); err != nil {
		t.Fatal(err)
	}
	if vectorColumns != 8 {
		t.Fatalf("current request snapshot exposes %d of 8 required vector columns", vectorColumns)
	}
}

func TestUnifiedTextMeteringCurrentGuardFunctionsAreInvokerAndPinned(t *testing.T) {
	f := newGuardFixture(t)
	var count int
	if err := f.owner.QueryRow(f.ctx, `
SELECT count(*) FROM pg_catalog.pg_proc p
JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname=$1 AND p.proname LIKE 'meter\_%' ESCAPE '\'
  AND (p.prosecdef OR p.proconfig IS DISTINCT FROM ARRAY[format('search_path=%I, pg_catalog, pg_temp',$1)])`, f.schema).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d meter functions are not invoker functions with a pinned search_path", count)
	}
}
