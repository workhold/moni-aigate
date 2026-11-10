package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func reserveGuardRequest(t *testing.T, f *guardFixture, tenant guardTenant) string {
	t.Helper()
	r, err := f.store.Reserve(f.ctx, billingReserveInput(f, tenant))
	if err != nil {
		t.Fatal(err)
	}
	return r.RequestID
}

func billingReserveInput(f *guardFixture, tenant guardTenant) billing.ReserveInput {
	return billing.ReserveInput{
		Principal: billing.Principal{TenantID: tenant.tenant, UserID: tenant.user, KeyID: tenant.key},
		RequestID: uuid.NewString(), ModelID: f.model, RouteRevision: guardRouteRevision,
		RequestHMAC:    "abababababababababababababababababababababababababababababababab",
		ProfileVersion: f.profile, InputUpperBound: guardInputBound, OutputLimit: guardOutputBound,
		IdempotencyKey: uuid.NewString(), RequestDeadline: time.Now().UTC().Add(5 * time.Minute),
	}
}

func insertGuardRequestRootClone(t *testing.T, tx pgx.Tx, sourceRequest, requestID string) error {
	_, err := tx.Exec(context.Background(), `
INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,profile_version,protocol,execution_kind,result_kind,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision)
SELECT $1,tenant_id,key_id,owner_id,model_id,profile_version,protocol,execution_kind,result_kind,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision
FROM request_records WHERE id=$2`, requestID, sourceRequest)
	return err
}

func insertGuardAttemptRoot(t *testing.T, tx pgx.Tx, f *guardFixture, tenant guardTenant, requestID, attemptID string) error {
	_, err := tx.Exec(context.Background(), `
INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model)
VALUES($1,$2,$3,1,$4,$5,$6,'guard-upstream')`, attemptID, tenant.tenant, requestID, f.channel, f.version, f.credential)
	return err
}

func insertGuardAttemptSnapshotClone(t *testing.T, tx pgx.Tx, baseAttempt, tenantID, attemptID, requestID, quoteExpr, parentExpr string) error {
	query := `
INSERT INTO attempt_meter_snapshots(tenant_id,attempt_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,supplier_rates_canonical,supplier_rates_digest,retail_bounds_canonical,retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,supplier_meter_quote_digest,input_bound,output_bound,plan_canonical,plan_digest,parent_plan_digest,supplier_quote_canonical,supplier_quote_digest)
SELECT $1,$2,$3,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,supplier_rates_canonical,supplier_rates_digest,retail_bounds_canonical,retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,supplier_meter_quote_digest,input_bound,output_bound,plan_canonical,plan_digest,` + parentExpr + `,` + quoteExpr + `,supplier_quote_digest
FROM attempt_meter_snapshots WHERE attempt_id=$4`
	if _, err := tx.Exec(context.Background(), query, tenantID, attemptID, requestID, baseAttempt); err != nil {
		return err
	}
	_, err := tx.Exec(context.Background(), `
INSERT INTO attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity)
SELECT $1,$2,side,dimension,unit,quantity FROM attempt_meter_bound_lines WHERE attempt_id=$3`, tenantID, attemptID, baseAttempt)
	return err
}

func TestUnifiedTextMeteringAttemptQuoteBindingAndSendingGuards(t *testing.T) {
	f := newGuardFixture(t)
	inRate, outRate := int64(1000000), int64(1000000)
	meterquote.PublishCost(t, f.ctx, f.owner, meterquote.CostPrice{
		ChannelID: f.channel, UpstreamModel: "guard-upstream", ModelID: f.model,
		Actor: f.operator, Basis: "contract", InputRate: &inRate, OutputRate: &outRate,
	})
	base := f.fact(f.a)

	var quoteStatus string
	var quoteDigest []byte
	if err := f.owner.QueryRow(f.ctx, `SELECT quote_status,supplier_quote_digest FROM attempt_meter_snapshots WHERE attempt_id=$1`, base.attempt).Scan(&quoteStatus, &quoteDigest); err != nil {
		t.Fatal(err)
	}
	if quoteStatus != "known" || len(quoteDigest) != 32 {
		t.Fatalf("fixture did not create a known attempt quote: status=%s digest=%d", quoteStatus, len(quoteDigest))
	}

	for _, tc := range []struct {
		name, quote, parent string
	}{
		{name: "tampered supplier quote", quote: `convert_to('{}','UTF8')`, parent: "parent_plan_digest"},
		{name: "wrong request parent plan", quote: "supplier_quote_canonical", parent: `decode(repeat('00',32),'hex')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestID := reserveGuardRequest(t, f, f.a)
			attemptID := uuid.NewString()
			tx, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = insertGuardAttemptRoot(t, tx, f, f.a, requestID, attemptID); err == nil {
				err = insertGuardAttemptSnapshotClone(t, tx, base.attempt, f.a.tenant, attemptID, requestID, tc.quote, tc.parent)
			}
			if err == nil {
				err = tx.Commit(f.ctx)
			} else {
				_ = tx.Rollback(f.ctx)
			}
			if err == nil {
				t.Fatal("invalid attempt snapshot committed")
			}
			var attempts int
			if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM upstream_attempts WHERE id=$1`, attemptID).Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 {
				t.Fatalf("rejected attempt transaction left root row: %d", attempts)
			}
		})
	}

	sending := f.fact(f.a)
	if _, err := f.owner.Exec(f.ctx, `UPDATE upstream_attempts SET delivery_state='sending' WHERE id=$1`, sending.attempt); err != nil {
		t.Fatal(err)
	}
	expectPoolReject(t, f.owner, "sending attempt without authoritative cost source", `UPDATE upstream_attempts SET cost_status='contract',cost_amount=1,latest_cost_finalization_id=NULL,latest_cost_correction_id=NULL WHERE id=$1`, sending.attempt)
}

func TestUnifiedTextMeteringSnapshotTenantBindingRollsBack(t *testing.T) {
	f := newGuardFixture(t)
	a := f.fact(f.a)
	b := f.fact(f.b)

	t.Run("request snapshot tenant mismatch", func(t *testing.T) {
		requestID := uuid.NewString()
		tx, err := f.owner.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = insertGuardRequestRootClone(t, tx, b.requestID, requestID); err == nil {
			_, err = tx.Exec(f.ctx, `
INSERT INTO request_meter_snapshots(tenant_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,price_version_id,pricing_group_id,tenant_pricing_revision,retail_priced_at,input_bound,output_bound,plan_canonical,plan_digest,retail_bounds_canonical,retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,retail_rates_canonical,retail_rates_digest,retail_meter_quote_digest,retail_quote_canonical,retail_quote_digest)
SELECT $1,$2,model_id,profile_version,schema_id,schema_digest,variant_key,price_version_id,pricing_group_id,tenant_pricing_revision,retail_priced_at,input_bound,output_bound,plan_canonical,plan_digest,retail_bounds_canonical,retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,retail_rates_canonical,retail_rates_digest,retail_meter_quote_digest,retail_quote_canonical,retail_quote_digest
FROM request_meter_snapshots WHERE request_id=$3`, f.a.tenant, requestID, a.requestID)
		}
		if err == nil {
			err = tx.Commit(f.ctx)
		} else {
			_ = tx.Rollback(f.ctx)
		}
		if err == nil {
			t.Fatal("cross-tenant request snapshot committed")
		}
		var count int
		if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM request_records WHERE id=$1`, requestID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("rejected request snapshot transaction left root row: %d", count)
		}
	})

	t.Run("attempt snapshot tenant mismatch", func(t *testing.T) {
		requestID := reserveGuardRequest(t, f, f.b)
		attemptID := uuid.NewString()
		tx, err := f.owner.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = insertGuardAttemptRoot(t, tx, f, f.b, requestID, attemptID); err == nil {
			err = insertGuardAttemptSnapshotClone(t, tx, a.attempt, f.a.tenant, attemptID, requestID, "supplier_quote_canonical", "parent_plan_digest")
		}
		if err == nil {
			err = tx.Commit(f.ctx)
		} else {
			_ = tx.Rollback(f.ctx)
		}
		if err == nil {
			t.Fatal("cross-tenant attempt snapshot committed")
		}
		var count int
		if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM upstream_attempts WHERE id=$1`, attemptID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("rejected attempt snapshot transaction left root row: %d", count)
		}
	})
}

func TestUnifiedTextMeteringSupplierCorrectionPointerGuard(t *testing.T) {
	f := newGuardFixture(t)
	a := f.fact(f.a)
	correctionID := uuid.NewString()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO provider_cost_corrections(id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref) VALUES($1,$2,$3,$4,17,'invoice','guard-evidence')`, correctionID, f.a.tenant, a.attempt, "guard-correction-"+correctionID); err != nil {
		t.Fatal(err)
	}
	expectPoolReject(t, f.owner, "correction pointer amount mismatch", `UPDATE upstream_attempts SET cost_status='invoice',cost_amount=18,latest_cost_finalization_id=NULL,latest_cost_correction_id=$2 WHERE id=$1`, a.attempt, correctionID)
	if _, err := f.owner.Exec(f.ctx, `UPDATE upstream_attempts SET cost_status='invoice',cost_amount=17,latest_cost_finalization_id=NULL,latest_cost_correction_id=$2 WHERE id=$1`, a.attempt, correctionID); err != nil {
		t.Fatal("matching correction pointer was rejected:", err)
	}
	expectPoolReject(t, f.owner, "correction pointer cannot coexist with finalization pointer", `UPDATE upstream_attempts SET latest_cost_finalization_id=$2 WHERE id=$1`, a.attempt, uuid.NewString())
}

func TestUnifiedTextMeteringGuardRoleWriteRejections(t *testing.T) {
	f := newGuardFixture(t)
	suffix := uuid.NewString()[:12]
	appRole, workerRole, projectionRole := "guard_app_write_"+suffix, "guard_worker_write_"+suffix, "guard_projection_write_"+suffix
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
	base := f.fact(f.a)
	asRole := func(role string) *pgxpool.Pool {
		cfg := f.owner.Config().Copy()
		cfg.MaxConns = 1
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
			return err
		}
		pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	app, worker, projection := asRole(appRole), asRole(workerRole), asRole(projectionRole)
	for _, table := range []string{
		"meter_schemas", "meter_schema_dimensions", "model_capability_variants", "model_capability_bound_lines",
		"price_rate_lines", "cost_rate_lines", "binding_cost_rate_limits", "request_meter_snapshots",
		"attempt_meter_snapshots", "request_meter_bound_lines", "attempt_meter_bound_lines", "meter_event_quantities",
	} {
		qualified := pgx.Identifier{f.schema, table}.Sanitize()
		var appSelect, appInsert, appUpdate, appDelete, appTruncate, appReferences, appTrigger bool
		if err := f.owner.QueryRow(f.ctx, `
SELECT has_table_privilege($1::name,$2::text,'SELECT'),
       has_table_privilege($1::name,$2::text,'INSERT'),
       has_table_privilege($1::name,$2::text,'UPDATE'),
       has_table_privilege($1::name,$2::text,'DELETE'),
       has_table_privilege($1::name,$2::text,'TRUNCATE'),
       has_table_privilege($1::name,$2::text,'REFERENCES'),
       has_table_privilege($1::name,$2::text,'TRIGGER')`, appRole, qualified).
			Scan(&appSelect, &appInsert, &appUpdate, &appDelete, &appTruncate, &appReferences, &appTrigger); err != nil {
			t.Fatal(err)
		}
		if !appSelect || !appInsert || appUpdate || appDelete || appTruncate || appReferences || appTrigger {
			t.Fatalf("application meter privilege closure mismatch for %s select=%t insert=%t update=%t delete=%t truncate=%t references=%t trigger=%t", table, appSelect, appInsert, appUpdate, appDelete, appTruncate, appReferences, appTrigger)
		}

		for roleName, role := range map[string]string{"worker": workerRole, "projection": projectionRole} {
			var any bool
			if err := f.owner.QueryRow(f.ctx, `
SELECT has_table_privilege($1::name,$2::text,'SELECT')
    OR has_table_privilege($1::name,$2::text,'INSERT')
    OR has_table_privilege($1::name,$2::text,'UPDATE')
    OR has_table_privilege($1::name,$2::text,'DELETE')
    OR has_table_privilege($1::name,$2::text,'TRUNCATE')
    OR has_table_privilege($1::name,$2::text,'REFERENCES')
    OR has_table_privilege($1::name,$2::text,'TRIGGER')`, role, qualified).Scan(&any); err != nil {
				t.Fatal(err)
			}
			if any {
				t.Fatalf("%s unexpectedly has a privilege on %s", roleName, table)
			}
		}
		var publicAny bool
		if err := f.owner.QueryRow(f.ctx, `
SELECT has_table_privilege('public',$1::text,'SELECT')
    OR has_table_privilege('public',$1::text,'INSERT')
    OR has_table_privilege('public',$1::text,'UPDATE')
    OR has_table_privilege('public',$1::text,'DELETE')
    OR has_table_privilege('public',$1::text,'TRUNCATE')
    OR has_table_privilege('public',$1::text,'REFERENCES')
    OR has_table_privilege('public',$1::text,'TRIGGER')`, qualified).Scan(&publicAny); err != nil {
			t.Fatal(err)
		}
		if publicAny {
			t.Fatalf("PUBLIC unexpectedly has a privilege on %s", table)
		}
	}
	if _, err := app.Exec(f.ctx, `UPDATE request_meter_snapshots SET input_bound=input_bound WHERE request_id=$1`, base.requestID); err == nil {
		t.Fatal("application role updated immutable meter snapshot")
	}
	if _, err := worker.Exec(f.ctx, `SELECT 1 FROM request_meter_snapshots LIMIT 1`); err == nil {
		t.Fatal("worker role read meter history")
	}
	if _, err := projection.Exec(f.ctx, `INSERT INTO meter_event_quantities(tenant_id,event_id,dimension,unit,status,quantity) SELECT $1,$2,'text_input_token','token','missing',NULL WHERE false`, f.a.tenant, uuid.NewString()); err == nil {
		t.Fatal("projection role wrote meter history")
	}
}
