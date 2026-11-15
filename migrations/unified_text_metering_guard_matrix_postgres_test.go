package migrations_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

const (
	guardProfileVersion = "10000000-0000-4000-8000-000000000001"
	guardRouteRevision  = int64(1)
	guardInputBound     = int64(100)
	guardOutputBound    = int64(20)
)

type guardTenant struct {
	tenant string
	user   string
	key    string
}

type guardFact struct {
	tenant      guardTenant
	requestID   string
	reservation string
	attempt     string
	final       string
	event       string
	settlement  string
}

type guardFixture struct {
	testing.TB
	ctx                                          context.Context
	admin, owner                                 *pgxpool.Pool
	schema                                       string
	store                                        *billing.Store
	model, profile, channel, version, credential string
	operator, approver                           string
	a, b                                         guardTenant
}

func newGuardFixture(t testing.TB) *guardFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("guard matrix requires postgres://moni_test@127.0.0.1:55439/moni_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	schema := "meter_guard_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		cancel()
		t.Fatal(err)
	}
	cfg.MaxConns = 16
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["application_name"] = schema
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10s"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		admin.Close()
		cancel()
		t.Fatal(err)
	}
	f := &guardFixture{
		TB:         t,
		ctx:        ctx,
		admin:      admin,
		owner:      owner,
		schema:     schema,
		model:      uuid.NewString(),
		profile:    guardProfileVersion,
		channel:    uuid.NewString(),
		version:    uuid.NewString(),
		credential: uuid.NewString(),
		operator:   uuid.NewString(),
		approver:   uuid.NewString(),
		a:          guardTenant{tenant: uuid.NewString(), user: uuid.NewString(), key: uuid.NewString()},
		b:          guardTenant{tenant: uuid.NewString(), user: uuid.NewString(), key: uuid.NewString()},
	}
	t.Cleanup(func() {
		owner.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop guard schema: %v", err)
		}
		admin.Close()
		cancel()
	})
	if err = migrations.ApplyCurrent(ctx, owner); err != nil {
		t.Fatal(err)
	}
	f.seed()
	f.store = billing.New(owner)
	for i, tenant := range []guardTenant{f.a, f.b} {
		credit, err := f.store.CreateCredit(ctx, billing.CreditInput{
			TenantID: tenant.tenant, ActorID: f.operator, Amount: 1000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 1000,
			BusinessRef: fmt.Sprintf("guard-credit-%d-%s", i, uuid.NewString()),
			ProofRef:    "guard-proof", IdempotencyKey: uuid.NewString(), RequestHMAC: strings.Repeat("c", 64),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: tenant.tenant, OrderID: credit.ID, ActorID: f.approver}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *guardFixture) seed() {
	f.Helper()
	users := []struct {
		id, role string
	}{
		{f.a.user, "none"}, {f.b.user, "none"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"},
	}
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		f.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, tenant := range []guardTenant{f.a, f.b} {
		if _, err = tx.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,$2)`, tenant.tenant, "guard-"+tenant.tenant); err != nil {
			f.Fatal(err)
		}
	}
	for _, user := range users {
		if _, err = tx.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'guard',$3)`, user.id, user.id+"@guard.invalid", user.role); err != nil {
			f.Fatal(err)
		}
	}
	profile := `{"version":"` + f.profile + `","model":"guard-model","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`
	if _, err = tx.Exec(f.ctx, `SELECT meter_insert_text_model($1,'guard-model',$2::jsonb,$3)`, f.model, profile, f.profile); err != nil {
		f.Fatal(err)
	}
	for _, tenant := range []guardTenant{f.a, f.b} {
		if _, err = tx.Exec(f.ctx, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, tenant.tenant, tenant.user); err != nil {
			f.Fatal(err)
		}
		if _, err = tx.Exec(f.ctx, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, tenant.tenant, f.model); err != nil {
			f.Fatal(err)
		}
		if _, err = tx.Exec(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'guard','guard-'||$1,$1,$4,1000)`, tenant.key, tenant.tenant, tenant.user, []string{f.model}); err != nil {
			f.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		f.Fatal(err)
	}
	meterquote.PublishDefaultPrice(f, f.ctx, f.owner, meterquote.DefaultPrice{ModelID: f.model, Actor: f.operator, InputRate: 1000000, OutputRate: 1000000, EffectiveAt: time.Now().UTC().Add(-time.Hour)})
	tx, err = f.owner.Begin(f.ctx)
	if err != nil {
		f.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `INSERT INTO channels(id,name,current_version) VALUES($1,'guard-channel',$2)`, f.channel, f.version); err != nil {
		f.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'guard-secret')`, f.credential, f.channel); err != nil {
		f.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, f.version, f.channel, f.credential); err != nil {
		f.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'guard',1,10)`, f.version, f.channel, f.credential); err != nil {
		f.Fatal(err)
	}
	policy := uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO route_policy_versions(id,model_id,revision,profile_version,cost_mode,unknown_cost_policy) VALUES($1,$2,$3,$4,'manual','allow')`, policy, f.model, guardRouteRevision, f.profile); err != nil {
		f.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key) VALUES($1,$2,$3,'guard-upstream',0,1,10,'default')`, policy, f.channel, f.version); err != nil {
		f.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE models SET active_revision=$2 WHERE id=$1`, f.model, guardRouteRevision); err != nil {
		f.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		f.Fatal(err)
	}
}

func (f *guardFixture) fact(tenant guardTenant) guardFact {
	f.Helper()
	requestID := uuid.NewString()
	reservation, err := f.store.Reserve(f.ctx, billing.ReserveInput{
		Principal: billing.Principal{TenantID: tenant.tenant, UserID: tenant.user, KeyID: tenant.key},
		RequestID: requestID, ModelID: f.model, RouteRevision: guardRouteRevision, RequestHMAC: strings.Repeat("a", 64),
		ProfileVersion: f.profile, InputUpperBound: guardInputBound, OutputLimit: guardOutputBound,
		IdempotencyKey: uuid.NewString(), RequestDeadline: time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		f.Fatal(err)
	}
	attempt, err := f.store.RecordAttempt(f.ctx, billing.AttemptInput{TenantID: tenant.tenant, RequestID: requestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "guard-upstream", Sequence: 1})
	if err != nil {
		f.Fatal(err)
	}
	return guardFact{tenant: tenant, requestID: requestID, reservation: reservation.ID, attempt: attempt.ID}
}

func (f *guardFixture) finalize(fact *guardFact) {
	f.Helper()
	final, err := f.store.RecordFinalization(f.ctx, billing.FinalizationInput{
		TenantID: fact.tenant.tenant, RequestID: fact.requestID, AttemptID: fact.attempt,
		Source: "gateway", SourceEventID: uuid.NewString(), Usage: billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none", ContentWritten: false,
	})
	if err != nil {
		f.Fatal(err)
	}
	fact.final, fact.event = final.ID, final.EventID
	settled, err := f.store.Settle(f.ctx, billing.SettleInput{TenantID: fact.tenant.tenant, RequestID: fact.requestID})
	if err != nil {
		f.Fatal(err)
	}
	fact.settlement = settled.ID
}

func expectPoolReject(t testing.TB, db *pgxpool.Pool, label, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err == nil {
		t.Fatalf("%s: invalid write was accepted", label)
	}
}

func expectTxReject(t testing.TB, db *pgxpool.Pool, label, query string, args ...any) {
	t.Helper()
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatalf("%s: begin: %v", label, err)
	}
	_, execErr := tx.Exec(context.Background(), query, args...)
	commitErr := tx.Commit(context.Background())
	if execErr == nil && commitErr == nil {
		t.Fatalf("%s: invalid transaction committed", label)
	}
	_ = tx.Rollback(context.Background())
}

func expectMixedObservationReject(t testing.TB, f *guardFixture, label, requestID, attemptID, baseEvent string) {
	t.Helper()
	query := `WITH base AS (
 SELECT e.*, qi.status AS input_status, qi.quantity AS input_quantity, qo.status AS output_status, qo.quantity AS output_quantity,
        a.profile_version AS attempt_profile, a.schema_id AS attempt_schema, a.schema_digest AS attempt_schema_digest, a.plan_digest AS attempt_plan
 FROM metering_events e
 JOIN meter_event_quantities qi ON qi.event_id=e.id AND qi.dimension='text_input_token'
 JOIN meter_event_quantities qo ON qo.event_id=e.id AND qo.dimension='text_output_token'
 JOIN attempt_meter_snapshots a ON a.attempt_id=$3
 WHERE e.id=$4
)
INSERT INTO metering_events(id,tenant_id,request_id,attempt_id,source,source_event_id,happened_at,profile_version,schema_id,schema_digest,variant_key,retail_usage_state,supplier_usage_state,plan_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,observation_canonical,observation_digest)
SELECT $1,tenant_id,$2,$3,source,$5,happened_at,attempt_profile,attempt_schema,attempt_schema_digest,'default',retail_usage_state,supplier_usage_state,attempt_plan,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,
 meter_observation_canonical(tenant_id,$2,$3,source,$5,attempt_profile,attempt_schema,attempt_schema_digest,attempt_plan,happened_at,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,NULL,input_status,input_quantity,output_status,output_quantity),
 meter_observation_digest(attempt_schema_digest,meter_observation_identity_canonical(tenant_id,$2,$3,source,$5,attempt_profile,attempt_schema,attempt_schema_digest,attempt_plan,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,NULL,input_status,input_quantity,output_status,output_quantity))
FROM base`
	eventID, sourceEventID := uuid.NewString(), uuid.NewString()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatalf("%s: begin: %v", label, err)
	}
	_, err = tx.Exec(f.ctx, query, eventID, requestID, attemptID, sourceEventID, baseEvent)
	if err == nil {
		_, err = tx.Exec(f.ctx, `SELECT meter_record_event_quantities($1,$2,'confirmed_final',10,5)`, eventID, f.a.tenant)
	}
	if err == nil {
		err = tx.Commit(f.ctx)
	}
	if err == nil {
		t.Fatalf("%s: invalid observation committed", label)
	}
	_ = tx.Rollback(f.ctx)
}

func TestUnifiedTextMeteringGuardMatrix(t *testing.T) {
	f := newGuardFixture(t)
	a1 := f.fact(f.a)
	a2 := f.fact(f.a)
	b1 := f.fact(f.b)
	f.finalize(&a1)

	t.Run("cross tenant request attempt event finalization settlement", func(t *testing.T) {
		expectTxReject(t, f.owner, "cross-tenant attempt", `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) VALUES($1,$2,$3,2,$4,$5,$6,'guard-upstream')`, uuid.NewString(), f.a.tenant, b1.requestID, f.channel, f.version, f.credential)
		expectPoolReject(t, f.owner, "cross-tenant event request", `INSERT INTO metering_events(id,tenant_id,request_id,attempt_id,source,source_event_id,happened_at,profile_version,schema_id,schema_digest,variant_key,retail_usage_state,supplier_usage_state,plan_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,observation_canonical,observation_digest) SELECT $1,$2,$3,$4,e.source,$5,e.happened_at,e.profile_version,e.schema_id,e.schema_digest,e.variant_key,e.retail_usage_state,e.supplier_usage_state,e.plan_digest,e.protocol_outcome,e.transport_end_reason,e.delivery_stage,e.content_written,e.finish_reason,e.observation_canonical,e.observation_digest FROM metering_events e WHERE e.id=$6`, uuid.NewString(), f.a.tenant, b1.requestID, a1.attempt, uuid.NewString(), a1.event)
		expectPoolReject(t, f.owner, "cross-tenant finalization request", `INSERT INTO finalization_records(id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,policy_version) SELECT $1,$2,$3,event_id,input_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,policy_version FROM finalization_records WHERE id=$4`, uuid.NewString(), f.a.tenant, b1.requestID, a1.final)
		expectPoolReject(t, f.owner, "cross-tenant settlement request", `INSERT INTO settlements(id,tenant_id,request_id,finalization_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id) VALUES($1,$2,$3,$4,0,0,0,'released','cross-tenant',$5)`, uuid.NewString(), f.a.tenant, b1.requestID, a1.final, a1.settlement)
	})

	t.Run("same tenant request and attempt cannot mix", func(t *testing.T) {
		expectMixedObservationReject(t, f, "cross-request event", a1.requestID, a2.attempt, a1.event)
	})

	t.Run("immutable snapshots bounds quantities observation finalization", func(t *testing.T) {
		expectPoolReject(t, f.owner, "request snapshot update", `UPDATE request_meter_snapshots SET input_bound=input_bound WHERE request_id=$1`, a1.requestID)
		expectPoolReject(t, f.owner, "request bound delete", `DELETE FROM request_meter_bound_lines WHERE request_id=$1`, a1.requestID)
		expectPoolReject(t, f.owner, "attempt snapshot update", `UPDATE attempt_meter_snapshots SET input_bound=input_bound WHERE attempt_id=$1`, a1.attempt)
		expectPoolReject(t, f.owner, "attempt bound delete", `DELETE FROM attempt_meter_bound_lines WHERE attempt_id=$1`, a1.attempt)
		expectPoolReject(t, f.owner, "quantity update", `UPDATE meter_event_quantities SET quantity=quantity WHERE event_id=$1`, a1.event)
		expectPoolReject(t, f.owner, "observation update", `UPDATE metering_events SET source=source WHERE id=$1`, a1.event)
		expectPoolReject(t, f.owner, "observation delete", `DELETE FROM metering_events WHERE id=$1`, a1.event)
		expectPoolReject(t, f.owner, "finalization update", `UPDATE finalization_records SET reason=reason||'-changed' WHERE id=$1`, a1.final)
		expectPoolReject(t, f.owner, "finalization delete", `DELETE FROM finalization_records WHERE id=$1`, a1.final)
	})

	t.Run("observation source and wrong attempt", func(t *testing.T) {
		expectPoolReject(t, f.owner, "observation source closed set", `INSERT INTO metering_events(id,tenant_id,request_id,attempt_id,source,source_event_id,happened_at,profile_version,schema_id,schema_digest,variant_key,retail_usage_state,supplier_usage_state,plan_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,observation_canonical,observation_digest) SELECT $1,tenant_id,request_id,attempt_id,'manual',$2,happened_at,profile_version,schema_id,schema_digest,variant_key,retail_usage_state,supplier_usage_state,plan_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,observation_canonical,observation_digest FROM metering_events WHERE id=$3`, uuid.NewString(), uuid.NewString(), a1.event)
		// The canonical and digest below are rebuilt for the alternate attempt, so
		// this rejection is caused by the closed request/attempt binding guard.
		expectMixedObservationReject(t, f, "observation wrong attempt", a1.requestID, a2.attempt, a1.event)
	})

	t.Run("cost pointer exclusivity and unknown initial state", func(t *testing.T) {
		var status string
		var amount *int64
		var final, correction *string
		if err := f.owner.QueryRow(f.ctx, `SELECT cost_status,cost_amount,latest_cost_finalization_id,latest_cost_correction_id FROM upstream_attempts WHERE id=$1`, a2.attempt).Scan(&status, &amount, &final, &correction); err != nil {
			t.Fatal(err)
		}
		if status != "unknown" || amount != nil || final != nil || correction != nil {
			t.Fatalf("unknown attempt has cost projection status=%s amount=%v final=%v correction=%v", status, amount, final, correction)
		}
		expectPoolReject(t, f.owner, "unknown attempt nonzero cost without source", `UPDATE upstream_attempts SET cost_status='contract',cost_amount=1 WHERE id=$1`, a2.attempt)
		expectPoolReject(t, f.owner, "both cost pointers", `UPDATE upstream_attempts SET latest_cost_finalization_id=$2,latest_cost_correction_id=$3 WHERE id=$1`, a2.attempt, a1.final, uuid.NewString())
		expectPoolReject(t, f.owner, "cross-attempt finalization pointer", `UPDATE upstream_attempts SET latest_cost_finalization_id=$2 WHERE id=$1`, a2.attempt, a1.final)
	})

	t.Run("settlement and funding cross-request and amount boundaries", func(t *testing.T) {
		credit, err := f.store.CreateCredit(f.ctx, billing.CreditInput{
			TenantID: f.a.tenant, ActorID: f.operator, Amount: 100, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 100,
			BusinessRef: "guard-extra-lot-" + uuid.NewString(), ProofRef: "guard-proof",
			IdempotencyKey: uuid.NewString(), RequestHMAC: strings.Repeat("e", 64),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: f.a.tenant, OrderID: credit.ID, ActorID: f.approver}); err != nil {
			t.Fatal(err)
		}
		var lotA, lotB string
		var amountA, amountB int64
		if err := f.owner.QueryRow(f.ctx, `SELECT lot_id,amount FROM funding_reservation_lots WHERE reservation_id=$1 ORDER BY allocation_index LIMIT 1`, a1.reservation).Scan(&lotA, &amountA); err != nil {
			t.Fatal(err)
		}
		if err := f.owner.QueryRow(f.ctx, `SELECT l.id,100 FROM funding_lots l WHERE l.tenant_id=$1 AND NOT EXISTS (SELECT 1 FROM funding_reservation_lots x WHERE x.reservation_id=ANY($2::text[]) AND x.lot_id=l.id) ORDER BY l.fifo_at DESC,l.id COLLATE "C" DESC LIMIT 1`, f.a.tenant, []string{a1.reservation, a2.reservation}).Scan(&lotB, &amountB); err != nil {
			t.Fatal(err)
		}
		if lotA == lotB || amountA == 0 || amountB == 0 {
			t.Fatal("fixture did not create a nonzero second funding lot")
		}
		expectTxReject(t, f.owner, "funding reservation edge mixes request", `INSERT INTO funding_reservation_lots(tenant_id,reservation_id,request_id,lot_id,allocation_index,amount) VALUES($1,$2,$3,$4,2,1)`, f.a.tenant, a1.reservation, a2.requestID, lotB)
		expectPoolReject(t, f.owner, "settlement finalization mixes request", `INSERT INTO settlements(id,tenant_id,request_id,finalization_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id) VALUES($1,$2,$3,$4,0,0,0,'released','wrong-request',(SELECT journal_id FROM settlements WHERE id=$5))`, uuid.NewString(), f.a.tenant, a2.requestID, a1.final, a1.settlement)
		expectPoolReject(t, f.owner, "negative settlement amount", `INSERT INTO settlements(id,tenant_id,request_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id) VALUES($1,$2,$3,-1,0,0,'released','negative',(SELECT journal_id FROM settlements WHERE id=$4))`, uuid.NewString(), f.a.tenant, a2.requestID, a1.settlement)
		expectPoolReject(t, f.owner, "zero funding receipt", `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,journal_id) VALUES($1,$2,'manual_credit',$3,0,(SELECT journal_id FROM settlements WHERE id=$4))`, uuid.NewString(), f.a.tenant, uuid.NewString(), a1.settlement)
		expectPoolReject(t, f.owner, "zero reservation edge", `INSERT INTO funding_reservation_lots(tenant_id,reservation_id,request_id,lot_id,allocation_index,amount) VALUES($1,$2,$3,$4,2,0)`, f.a.tenant, a1.reservation, a1.requestID, lotB)
	})

	t.Run("failed root transaction leaves no facts", func(t *testing.T) {
		orphan := uuid.NewString()
		expectTxReject(t, f.owner, "request without snapshot rolls back", `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,profile_version,protocol,execution_kind,result_kind,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision) VALUES($1,$2,$3,$4,$5,$6,'openai_chat','synchronous','json','r3',$7,clock_timestamp()+interval '5 minutes',$8,false,1)`, orphan, f.a.tenant, f.a.key, f.a.user, f.model, f.profile, strings.Repeat("d", 64), uuid.NewString())
		var requestCount, attemptCount int
		if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM request_records WHERE id=$1),(SELECT count(*) FROM upstream_attempts WHERE request_id=$1)`, orphan).Scan(&requestCount, &attemptCount); err != nil {
			t.Fatal(err)
		}
		if requestCount != 0 || attemptCount != 0 {
			t.Fatalf("failed root transaction left request=%d attempt=%d", requestCount, attemptCount)
		}
	})
}

func TestUnifiedTextMeteringGuardRoleMatrix(t *testing.T) {
	f := newGuardFixture(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	appRole, workerRole, projectionRole := "guard_app_"+suffix, "guard_worker_"+suffix, "guard_projection_"+suffix
	for _, role := range []string{appRole, workerRole, projectionRole} {
		if _, err := f.admin.Exec(f.ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, role := range []string{appRole, workerRole, projectionRole} {
			_, _ = f.admin.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
			_, _ = f.admin.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
		}
	})
	if err := operations.ProvisionFor(f.ctx, f.owner, f.schema, appRole, projectionRole); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role) SELECT $1,'guard-merchant','guard-payee',d.oid,n.oid,$2,$3,$4,$5 FROM pg_catalog.pg_database d CROSS JOIN pg_catalog.pg_namespace n WHERE d.datname=current_database() AND n.nspname=$6`, uuid.NewString(), strings.Repeat("1", 64), appRole, workerRole, projectionRole, f.schema); err != nil {
		t.Fatal(err)
	}
	if err := operations.ProvisionPaymentWorkerFor(f.ctx, f.owner, f.schema, appRole, workerRole, projectionRole); err != nil {
		t.Fatal(err)
	}
	meterTables := []string{"meter_schemas", "meter_schema_dimensions", "model_capability_variants", "model_capability_bound_lines", "price_rate_lines", "cost_rate_lines", "binding_cost_rate_limits", "request_meter_snapshots", "attempt_meter_snapshots", "request_meter_bound_lines", "attempt_meter_bound_lines", "meter_event_quantities"}
	for _, table := range meterTables {
		qualified := pgx.Identifier{f.schema, table}.Sanitize()
		var appSelect, appInsert, appUpdate, appDelete, projectionAny, workerAny, publicAny bool
		if err := f.owner.QueryRow(f.ctx, `SELECT has_table_privilege($1,$2,'SELECT'),has_table_privilege($1,$2,'INSERT'),has_table_privilege($1,$2,'UPDATE'),has_table_privilege($1,$2,'DELETE'),has_table_privilege($3,$2,'SELECT') OR has_table_privilege($3,$2,'INSERT') OR has_table_privilege($3,$2,'UPDATE') OR has_table_privilege($3,$2,'DELETE'),has_table_privilege($4,$2,'SELECT') OR has_table_privilege($4,$2,'INSERT') OR has_table_privilege($4,$2,'UPDATE') OR has_table_privilege($4,$2,'DELETE'),has_table_privilege('public',$2,'SELECT') OR has_table_privilege('public',$2,'INSERT') OR has_table_privilege('public',$2,'UPDATE') OR has_table_privilege('public',$2,'DELETE')`, appRole, qualified, projectionRole, workerRole).Scan(&appSelect, &appInsert, &appUpdate, &appDelete, &projectionAny, &workerAny, &publicAny); err != nil {
			t.Fatal(err)
		}
		if !appSelect || !appInsert || appUpdate || appDelete || projectionAny || workerAny || publicAny {
			t.Fatalf("meter ACL mismatch for %s app=%t/%t/%t/%t projection=%t worker=%t public=%t", table, appSelect, appInsert, appUpdate, appDelete, projectionAny, workerAny, publicAny)
		}
	}
	var appExecute, projectionExecute, workerExecute, publicExecute bool
	if err := f.owner.QueryRow(f.ctx, `SELECT bool_and(has_function_privilege($1,p.oid,'EXECUTE')),bool_or(has_function_privilege($2,p.oid,'EXECUTE')),bool_or(has_function_privilege($3,p.oid,'EXECUTE')),bool_or(has_function_privilege('public',p.oid,'EXECUTE')) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$4 AND p.proname LIKE 'meter\_%' ESCAPE '\'`, appRole, projectionRole, workerRole, f.schema).Scan(&appExecute, &projectionExecute, &workerExecute, &publicExecute); err != nil {
		t.Fatal(err)
	}
	if !appExecute || projectionExecute || workerExecute || publicExecute {
		t.Fatalf("meter function ACL mismatch app=%t projection=%t worker=%t public=%t", appExecute, projectionExecute, workerExecute, publicExecute)
	}
	_ = projectionRole
	_ = workerRole
}
