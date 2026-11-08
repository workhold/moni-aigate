package periodicbudget_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Unlike the historical Tx fixture this always installs the complete current
// manifest, including later independent payment migrations.
func newPBVerifierFixture(t *testing.T) *pbTxFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated verifier PostgreSQL tests")
	}
	cfg, e := pgxpool.ParseConfig(raw)
	if e != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("verifier tests require the dedicated local moni_test database on port 55439")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	admin, e := pgxpool.NewWithConfig(ctx, cfg.Copy())
	pbTxMust(t, "open verifier test admin", e)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	f := &pbTxFixture{ctx: ctx, currentSchema: true, schema: "pb_verify_" + suffix, appRole: "pb_verify_app_" + suffix, projectionRole: "pb_verify_projection_" + suffix,
		tenant: uuid.NewString(), user: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString(), key: uuid.NewString(), model: uuid.NewString(), price: uuid.NewString(), channel: uuid.NewString(), version: uuid.NewString(), credential: uuid.NewString(), account: uuid.NewString(), binding: uuid.NewString(), cost: uuid.NewString(), profileVersion: uuid.NewString()}
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		if f.projection != nil {
			f.projection.Close()
		}
		if f.owner != nil {
			f.owner.Close()
		}
		clean, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if _, e := admin.Exec(clean, "DROP SCHEMA IF EXISTS "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("verifier exact schema cleanup failed")
		}
		for _, role := range []string{f.appRole, f.projectionRole} {
			if _, e := admin.Exec(clean, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Error("verifier exact LOGIN cleanup failed")
			}
		}
		admin.Close()
		cancel()
	})
	_, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize())
	pbTxMust(t, "create verifier schema", e)
	cfg = cfg.Copy()
	cfg.MaxConns = 4
	cfg.ConnConfig.RuntimeParams["search_path"] = f.schema
	f.owner, e = pgxpool.NewWithConfig(ctx, cfg.Copy())
	pbTxMust(t, "open verifier owner", e)
	pbTxMust(t, "apply full verifier migration set", migrations.Apply(ctx, f.owner))
	f.seedCatalog(t)
	for _, role := range []string{f.appRole, f.projectionRole} {
		_, e = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS")
		pbTxMust(t, "create verifier runtime LOGIN", e)
	}
	pbTxMust(t, "provision actual verifier APP", operations.ProvisionFor(ctx, f.owner, f.schema, f.appRole, f.projectionRole))
	open := func(role string) *pgxpool.Pool {
		c := cfg.Copy()
		c.ConnConfig.User = role
		c.ConnConfig.Password = ""
		p, e := pgxpool.NewWithConfig(ctx, c)
		pbTxMust(t, "open verifier runtime LOGIN", e)
		var current, session string
		pbTxMust(t, "attest verifier actual LOGIN", p.QueryRow(ctx, `SELECT current_user,session_user`).Scan(&current, &session))
		if current != role || session != role {
			t.Fatal("runtime identity mismatch")
		}
		return p
	}
	f.app = open(f.appRole)
	f.projection = open(f.projectionRole)
	return f
}

func pbVerify(t *testing.T, f *pbTxFixture) Verification {
	t.Helper()
	v, e := NewVerifier(f.owner, VerifyOptions{})
	pbTxMust(t, "construct owner verifier", e)
	r, e := v.Verify(f.ctx)
	pbTxMust(t, "complete owner readonly verification", e)
	return r
}

func TestVerifierPostgresEmptyNoopAndAuthority(t *testing.T) {
	f := newPBVerifierFixture(t)
	if r := pbVerify(t, f); r.Status != VerificationBalanced || !r.Balanced || !r.CoverageComplete || r.Scanned.Controls != 0 {
		t.Fatalf("empty schema report: %+v", r)
	}
	for _, pool := range []*pgxpool.Pool{f.app, f.projection} {
		v, e := NewVerifier(pool, VerifyOptions{})
		pbTxMust(t, "construct restricted verifier", e)
		r, e := v.Verify(f.ctx)
		if !errors.Is(e, ErrVerifyAuthority) || r != (Verification{}) {
			t.Fatal("actual runtime LOGIN accepted as verifier owner")
		}
	}
	f.configure(t, "tenant", true, 1000)
	f.configure(t, "tenant", true, 1000)
	if r := pbVerify(t, f); r.Status != VerificationBalanced || r.Scanned.Operations != 2 || r.Scanned.ConfigurationVersions != 1 {
		t.Fatalf("empty no-op report: %+v", r)
	}
	var before, after string
	pbTxMust(t, "hash verifier sources before", f.owner.QueryRow(f.ctx, `SELECT md5(string_agg(x::text,'' ORDER BY x::text)) FROM (SELECT to_jsonb(c) x FROM periodic_budget_controls c UNION ALL SELECT to_jsonb(o) FROM periodic_budget_operations o) q`).Scan(&before))
	_ = pbVerify(t, f)
	pbTxMust(t, "hash verifier sources after", f.owner.QueryRow(f.ctx, `SELECT md5(string_agg(x::text,'' ORDER BY x::text)) FROM (SELECT to_jsonb(c) x FROM periodic_budget_controls c UNION ALL SELECT to_jsonb(o) FROM periodic_budget_operations o) q`).Scan(&after))
	if before != after {
		t.Fatal("readonly verifier mutated persisted facts")
	}
}

// This zero-price source fixture exercises actual APP source/Tx contracts without
// creating a funding receipt or pretending to certify the wallet module.
func pbVerifierZeroCustomer(t *testing.T, f *pbTxFixture) string {
	t.Helper()
	tx := f.begin(t)
	locks := pbTxCustomerLocks(t, f, tx, "")
	request, reservation := uuid.NewString(), uuid.NewString()
	price := meterquote.PublishDefaultPrice(t, f.ctx, tx, meterquote.DefaultPrice{
		ModelID: f.model, Actor: f.user, EffectiveAt: f.catalogEffectiveAt(t, tx, "1 day"),
	})
	f.exec(t, tx, `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,profile_version,protocol,execution_kind,result_kind,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision)
 VALUES($1,$2,$3,$4,$5,$6,'openai_chat','synchronous','json','r3','zero-verifier-fixture',clock_timestamp()+interval '5 minutes','fixture',false,1)`, request, f.tenant, f.key, f.user, f.model, f.profileVersion)
	f.exec(t, tx, `INSERT INTO request_meter_snapshots(tenant_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,price_version_id,pricing_group_id,tenant_pricing_revision,retail_priced_at,input_bound,output_bound)
 SELECT t.id,$2,c.model_id,c.id,c.schema_id,c.schema_digest,'default',$4,t.price_group_id,t.pricing_revision,clock_timestamp(),100,20 FROM tenants t JOIN model_capability_versions c ON c.id=$3 WHERE t.id=$1`, f.tenant, request, f.profileVersion, price)
	f.exec(t, tx, `INSERT INTO request_meter_bound_lines(tenant_id,request_id,side,dimension,unit,quantity)
 SELECT $1,$2,s.side,d.dimension,'token',d.quantity FROM (VALUES ('retail'),('supplier')) s(side) CROSS JOIN (VALUES ('text_input_token',100),('text_output_token',20)) d(dimension,quantity)`, f.tenant, request)
	f.exec(t, tx, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES($1,$2,$3,0,clock_timestamp()+interval '1 day')`, reservation, f.tenant, request)
	// Like billing, a zero reserve still posts explicit zero AVAILABLE/HELD lines.
	f.walletLocks(t, tx)
	journal := uuid.NewString()
	f.exec(t, tx, `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$3,'reserve',$4,$5)`, journal, f.tenant, reservation, request, f.user)
	f.exec(t, tx, `INSERT INTO journal_lines(journal_id,account_id,signed_delta) SELECT $1,id,0 FROM ledger_accounts WHERE tenant_id=$2 AND kind IN ('AVAILABLE','HELD')`, journal, f.tenant)
	a, e := AdmitCustomerTx(f.ctx, tx, locks, request, Amount{"0"})
	pbTxMust(t, "admit actual APP zero-price customer", e)
	pbTxMust(t, "mirror actual APP zero original reserve", RecordCustomerReserveTx(f.ctx, tx, locks, a, request))
	pbTxMust(t, "commit actual APP zero source and budget", tx.Commit(f.ctx))
	return request
}

func pbVerifierDamage(t *testing.T, f *pbTxFixture, q string, args ...any) {
	t.Helper()
	tx, e := f.owner.Begin(f.ctx)
	pbTxMust(t, "begin exact isolated corruption", e)
	defer tx.Rollback(f.ctx)
	_, e = tx.Exec(f.ctx, `SET LOCAL session_replication_role=replica`)
	pbTxMust(t, "disable exact isolated history guards", e)
	_, e = tx.Exec(f.ctx, q, args...)
	pbTxMust(t, "write exact isolated historical damage", e)
	pbTxMust(t, "commit exact isolated historical damage", tx.Commit(f.ctx))
}

func TestVerifierPostgresZeroSourcesClockAndReverseMissing(t *testing.T) {
	f := newPBVerifierFixture(t)
	f.configure(t, "tenant", true, 0)
	f.configure(t, "procurement", true, 0)
	// Admission far ahead of real wall time must not make this historical tool
	// call pb_view_at or refuse the retained source/window as clock regression.
	f.setClock(t, time.Now().UTC().AddDate(2, 0, 0).Truncate(time.Microsecond))
	request := pbVerifierZeroCustomer(t, f)
	tx := f.begin(t)
	pbTxCustomerLocks(t, f, tx, request)
	attempt, locks, effect := pbTxPrepareSupplier(t, f, tx, request, 0)
	_, e := AdmitSupplierTx(f.ctx, tx, locks, attempt, Amount{"0"})
	pbTxMust(t, "admit actual APP zero-price supplier", e)
	pbTxMust(t, "mirror actual APP zero supplier reserve", ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", effect}))
	pbTxMust(t, "commit actual APP zero supplier source", tx.Commit(f.ctx))
	r := pbVerify(t, f)
	if r.Mismatches != (VerificationCounts{}) || r.Scanned.Allocations != 2 || r.Status != VerificationBalanced || !r.CoverageComplete {
		t.Fatalf("valid zero lifecycle or historical clock rejected: %+v", r)
	}
	// Remove the entire zero-value tracking graph. Every amount SUM remains 0;
	// only scanning real source markers in the reverse direction detects it.
	pbVerifierDamage(t, f, `DELETE FROM periodic_budget_events`)
	pbVerifierDamage(t, f, `DELETE FROM periodic_budget_allocations`)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_controls SET event_seq=0,last_event_id=NULL`)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_windows SET revision=0,last_event_id=NULL`)
	r = pbVerify(t, f)
	if r.Status != VerificationMismatch || r.Mismatches.MissingAllocations != 2 || r.Mismatches.ControlMismatches != 0 || r.Mismatches.WindowMismatches != 0 {
		t.Fatalf("zero SUM equality hid missing source allocation: %+v", r)
	}
}

func TestVerifierPostgresEmptyWindowHasUnprovenCreationCut(t *testing.T) {
	f := newPBVerifierFixture(t)
	control := f.configure(t, "tenant", true, 0)
	_, e := f.app.Exec(f.ctx, `SELECT pb_gate($1,pb_now(),0)`, control)
	pbTxMust(t, "create legitimate empty extra-zero dispatch window", e)
	r := pbVerify(t, f)
	if r.Status != VerificationIndeterminate || r.Mismatches != (VerificationCounts{}) || r.Unproven.OperationSnapshotPrefixUnproven != 1 {
		t.Fatalf("unwitnessed window creation was silently certified: %+v", r)
	}
}

func TestVerifierPostgresMalformedSnapshotAndMigration(t *testing.T) {
	f := newPBVerifierFixture(t)
	f.configure(t, "tenant", true, 100)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_operations SET result_snapshot=jsonb_set(result_snapshot,'{carry_held}','"private-invalid-number"'::jsonb)`)
	r := pbVerify(t, f)
	if r.Status != VerificationMismatch || r.Mismatches.SnapshotMismatches != 1 || r.Unproven.OperationSnapshotPrefixUnproven != 0 {
		t.Fatalf("damaged JSON crashed or silently passed: %+v", r)
	}
	_, e := f.owner.Exec(f.ctx, `UPDATE schema_migrations SET checksum='private-corrupt-checksum' WHERE name='020_periodic_budgets.sql'`)
	pbTxMust(t, "corrupt exact migration checksum", e)
	v, e := NewVerifier(f.owner, VerifyOptions{})
	pbTxMust(t, "construct schema rejection verifier", e)
	got, e := v.Verify(f.ctx)
	if !errors.Is(e, ErrVerifySchema) || got != (Verification{}) {
		t.Fatal("modified manifest returned a partial report")
	}
}

func TestVerifierPostgresRealFundingCustomerSupplierAndClosedMissing(t *testing.T) {
	f := newPBBillingFixture(t) // actual APP maker/checker funds 10,000 first
	tenantControl := f.configure(t, "tenant", true, 10000)
	f.configure(t, "key", true, 10000)
	f.configure(t, "procurement", true, 10000)
	store := billing.New(f.app)
	g := pbBillProcurementCatalog(t, f)
	r := pbBillProcurementReserve(t, f, store)
	a := pbBillProcurementAttempt(t, f, store, r, g, 1)
	if report := pbVerify(t, f); report.Mismatches != (VerificationCounts{}) || report.Scanned.Allocations != 3 {
		t.Fatalf("actual customer/supplier original reservations rejected: %+v", report)
	}
	_, e := store.RecordFinalization(f.ctx, pbBillProcurementFinal(r, a, billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, "done"))
	pbTxMust(t, "record actual billable supplier usage", e)
	s, e := store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	pbTxMust(t, "settle actual customer after supplier effect", e)
	pbBillProcurementCorrect(t, f, store, a.ID, 25)
	op := uuid.NewString()
	refund, e := store.CreateRefund(f.ctx, billing.RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: s.ID, BusinessRef: "verifier-refund:" + op, Reason: "independent source verification", Amount: 5, IdempotencyKey: op, RequestHMAC: op})
	pbTxMust(t, "create actual gross-spend-preserving refund", e)
	_, e = store.ApproveRefund(f.ctx, billing.ApprovalInput{TenantID: f.tenant, ActorID: f.approver, OrderID: refund.ID})
	pbTxMust(t, "approve actual gross-spend-preserving refund", e)
	f.verifyFinance(t)
	report := pbVerify(t, f)
	if report.Mismatches != (VerificationCounts{}) || report.Status != VerificationIndeterminate || report.Unproven.OperationSnapshotPrefixUnproven == 0 || report.Scanned.ProcurementEvents != 3 {
		t.Fatalf("real terminal source/correction/refund rejected: %+v", report)
	}
	_, e = f.owner.Exec(f.ctx, `UPDATE users SET status='suspended',platform_role='none' WHERE id=$1`, f.user)
	pbTxMust(t, "withdraw historical command actor current access", e)
	_, e = f.owner.Exec(f.ctx, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, f.key)
	pbTxMust(t, "revoke historical source current Key", e)
	if after := pbVerify(t, f); after.Mismatches != (VerificationCounts{}) {
		t.Fatalf("verifier reauthenticated old actor or Key: %+v", after)
	}
	// A fully ended source carrying its creation marker must still be tracked.
	// Align all remaining budget sums to 0 for this scope after losing its graph.
	pbVerifierDamage(t, f, `DELETE FROM periodic_budget_events WHERE control_id=$1`, tenantControl)
	pbVerifierDamage(t, f, `DELETE FROM periodic_budget_allocations WHERE control_id=$1`, tenantControl)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_controls SET held_amount=0,spent_amount=0,event_seq=0,last_event_id=NULL WHERE id=$1`, tenantControl)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_windows SET held_amount=0,spent_amount=0,revision=0,last_event_id=NULL WHERE control_id=$1`, tenantControl)
	report = pbVerify(t, f)
	if report.Status != VerificationMismatch || report.Mismatches.MissingAllocations != 1 || report.Mismatches.ControlMismatches != 0 || report.Mismatches.WindowMismatches != 0 {
		t.Fatalf("closed new source silently exempted after graph loss: %+v", report)
	}
}

func TestVerifierPostgresNoBusinessRowLocksAndBoundedFailure(t *testing.T) {
	f := newPBVerifierFixture(t)
	control := f.configure(t, "tenant", true, 100)
	hold, e := f.owner.Begin(f.ctx)
	pbTxMust(t, "begin independent row holder", e)
	defer hold.Rollback(f.ctx)
	_, e = hold.Exec(f.ctx, `SELECT id FROM periodic_budget_controls WHERE id=$1 FOR UPDATE`, control)
	pbTxMust(t, "hold independent business row", e)
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	v, e := NewVerifier(f.owner, VerifyOptions{})
	pbTxMust(t, "construct nonlocking verifier", e)
	report, e := v.Verify(ctx)
	cancel()
	pbTxMust(t, "MVCC verification does not wait for business row lock", e)
	if report.Status != VerificationBalanced {
		t.Fatal("independent row lock changed verification conclusion")
	}
	pbTxMust(t, "release independent row holder", hold.Rollback(f.ctx))
	ddl, e := f.owner.Begin(f.ctx)
	pbTxMust(t, "begin independent DDL holder", e)
	defer ddl.Rollback(f.ctx)
	_, e = ddl.Exec(f.ctx, `LOCK TABLE request_records IN ACCESS EXCLUSIVE MODE`)
	pbTxMust(t, "hold independent source relation lock", e)
	ctx, cancel = context.WithTimeout(f.ctx, 100*time.Millisecond)
	report, e = v.Verify(ctx)
	cancel()
	if !errors.Is(e, ErrVerifyTimeout) || report != (Verification{}) {
		t.Fatal("blocked scan returned unbounded/partial success")
	}
	pbTxMust(t, "release independent DDL holder", ddl.Rollback(f.ctx))
	if report = pbVerify(t, f); report.Status != VerificationBalanced {
		t.Fatal("timed out RR transaction poisoned pool or changed facts")
	}
}

func TestVerifierPostgresLegacyMembershipImportAndCatchupBaseline(t *testing.T) {
	t.Run("pending_customer_import", func(t *testing.T) {
		f := newPBBillingFixture(t)
		store := billing.New(f.app)
		r := pbBillCustomerReserve(t, f, store)
		f.configure(t, "tenant", true, 1000)
		report := pbVerify(t, f)
		if report.Mismatches != (VerificationCounts{}) || report.Scanned.Allocations != 1 || report.Unproven.LegacyMembershipUnproven != 0 {
			t.Fatalf("legal pending legacy import rejected: %+v", report)
		}
		_, e := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
		pbTxMust(t, "release legitimately imported customer", e)
		if report = pbVerify(t, f); report.Mismatches != (VerificationCounts{}) {
			t.Fatalf("imported customer initial/terminal chain rejected: %+v", report)
		}
	})
	t.Run("ended_supplier_membership_then_exact_catchup", func(t *testing.T) {
		f := newPBBillingFixture(t)
		store := billing.New(f.app)
		g := pbBillProcurementCatalog(t, f)
		r := pbBillProcurementReserve(t, f, store)
		a := pbBillProcurementAttempt(t, f, store, r, g, 1)
		_, e := store.RecordFinalization(f.ctx, pbBillProcurementFinal(r, a, billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, "done"))
		pbTxMust(t, "finish supplier before its first control", e)
		f.configure(t, "procurement", true, 1000)
		report := pbVerify(t, f)
		if report.Status != VerificationIndeterminate || report.Mismatches != (VerificationCounts{}) || report.Unproven.LegacyMembershipUnproven != 1 || report.Scanned.Allocations != 0 {
			t.Fatalf("legal ended old supplier called damaged or fully proved: %+v", report)
		}
		pbBillProcurementCorrect(t, f, store, a.ID, 40)
		report = pbVerify(t, f)
		if report.Mismatches != (VerificationCounts{}) || report.Unproven.LegacyMembershipUnproven != 0 || report.Unproven.LegacyEffectBoundaryUnproven != 1 || report.Unproven.OperationSnapshotPrefixUnproven != 0 || report.Scanned.Allocations != 1 {
			t.Fatalf("exact old baseline/catchup boundary rejected or over-certified: %+v", report)
		}
		pbBillProcurementCorrect(t, f, store, a.ID, 25)
		if report = pbVerify(t, f); report.Mismatches != (VerificationCounts{}) || report.Unproven.LegacyEffectBoundaryUnproven != 1 {
			t.Fatalf("valid absolute cost below frozen baseline rejected: %+v", report)
		}
		// Net residual agreement cannot prove membership of a zero-delta old PE.
		// Existing unmirrored reserve+cost facts already keep this boundary open.
		var baseline, spent string
		pbTxMust(t, "read independent frozen baseline oracle", f.owner.QueryRow(f.ctx, `SELECT baseline::text,spent_amount::text FROM periodic_budget_allocations WHERE subject_id=$1`, a.ID).Scan(&baseline, &spent))
		if baseline != "30" || spent != "0" {
			t.Fatal("catchup test did not exercise an actual 30 baseline and lower final cost")
		}
	})
}

func TestVerifierPostgresZeroEffectOmissionAndCoherentNaN(t *testing.T) {
	f := newPBVerifierFixture(t)
	control := f.configure(t, "procurement", true, 0)
	request := pbVerifierZeroCustomer(t, f)
	tx := f.begin(t)
	pbTxCustomerLocks(t, f, tx, request)
	attempt, locks, reserve := pbTxPrepareSupplier(t, f, tx, request, 0)
	_, e := AdmitSupplierTx(f.ctx, tx, locks, attempt, Amount{"0"})
	pbTxMust(t, "admit zero effect fixture", e)
	pbTxMust(t, "mirror original zero reserve", ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", reserve}))
	pbTxMust(t, "commit zero original supplier", tx.Commit(f.ctx))
	tx = f.begin(t)
	locks = pbTxSupplierLocks(t, f, tx, request, attempt)
	f.exec(t, tx, `UPDATE procurement_reservations SET state='unknown' WHERE attempt_id=$1`, attempt)
	unknown := uuid.NewString()
	f.exec(t, tx, `INSERT INTO procurement_budget_events(id,event_key,type,account_id,attempt_id,held_delta,spent_delta) VALUES($1,$2,'consumption_unknown',$3,$4,0,0)`, unknown, "unknown:"+attempt, f.account, attempt)
	pbTxMust(t, "mirror legitimate zero unknown effect", ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", unknown}))
	pbTxMust(t, "commit legitimate zero unknown effect", tx.Commit(f.ctx))
	if report := pbVerify(t, f); report.Mismatches != (VerificationCounts{}) {
		t.Fatalf("legitimate zero raw effect rejected: %+v", report)
	}
	// Poison every downstream spent value consistently. PostgreSQL numeric NaN
	// compares equal to itself and its SUM is NaN, so equality alone is unsafe.
	pbVerifierDamage(t, f, `UPDATE procurement_budget_events SET spent_delta='NaN'::numeric WHERE id=$1`, unknown)
	pbVerifierDamage(t, f, `UPDATE procurement_reservations SET spent_amount='NaN'::numeric WHERE attempt_id=$1`, attempt)
	pbVerifierDamage(t, f, `UPDATE procurement_accounts SET spent_amount='NaN'::numeric WHERE id=$1`, f.account)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_events SET after_spent='NaN'::numeric,after_source_spent='NaN'::numeric,spent_delta='NaN'::numeric WHERE source_id=$1`, unknown)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_allocations SET source_spent='NaN'::numeric,spent_amount='NaN'::numeric WHERE subject_id=$1`, attempt)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_windows SET spent_amount='NaN'::numeric WHERE control_id=$1`, control)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_controls SET spent_amount='NaN'::numeric WHERE id=$1`, control)
	report := pbVerify(t, f)
	if report.Status != VerificationMismatch || report.Mismatches.EventChainMismatches != 1 || report.Mismatches.ProcurementSourceMismatches == 0 || report.Mismatches.AllocationSourceMismatches == 0 || report.Mismatches.WindowMismatches == 0 || report.Mismatches.ControlMismatches == 0 {
		t.Fatalf("mutually agreeing NaN projections passed conservation: %+v", report)
	}
	// Restore finite values, then lose just the zero-delta mirror; SUMs stay 0.
	pbVerifierDamage(t, f, `UPDATE procurement_budget_events SET spent_delta=0 WHERE id=$1`, unknown)
	pbVerifierDamage(t, f, `UPDATE procurement_reservations SET spent_amount=0 WHERE attempt_id=$1`, attempt)
	pbVerifierDamage(t, f, `UPDATE procurement_accounts SET spent_amount=0 WHERE id=$1`, f.account)
	pbVerifierDamage(t, f, `DELETE FROM periodic_budget_events WHERE source_id=$1`, unknown)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_allocations SET source_spent=0,spent_amount=0,revision=1,last_event_id=(SELECT id FROM periodic_budget_events WHERE source_id=$2) WHERE subject_id=$1`, attempt, reserve)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_windows SET spent_amount=0,revision=1,last_event_id=(SELECT id FROM periodic_budget_events WHERE source_id=$2) WHERE control_id=$1`, control, reserve)
	pbVerifierDamage(t, f, `UPDATE periodic_budget_controls SET spent_amount=0,event_seq=1,last_event_id=(SELECT id FROM periodic_budget_events WHERE source_id=$2) WHERE id=$1`, control, reserve)
	report = pbVerify(t, f)
	if report.Status != VerificationMismatch || report.Mismatches.MissingEffects != 1 || report.Mismatches.EventChainMismatches != 0 || report.Mismatches.ControlMismatches != 0 || report.Mismatches.ProcurementSourceMismatches != 0 {
		t.Fatalf("net SUM equality hid missing zero effect: %+v", report)
	}
}
