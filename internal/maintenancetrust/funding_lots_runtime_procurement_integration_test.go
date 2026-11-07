package maintenancetrust

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotruntime"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/procurement"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
	"github.com/moni-ai/moni-aigate/internal/testsupport/frozenattempt"
)

type runtimeProcurementHistory struct {
	tenant, account, request, attempt, finalization, event string
}

// Preparation uses the original business APIs before the fixed C0 candidate
// installation. The short-lived owner fixture pool may initialize identities
// and the published catalog; it is never the APP runtime-verification identity.
// This is local financial evidence, without a model or supplier network call.
func runtimePrepareActiveProcurement(t *testing.T, pool *pgxpool.Pool) runtimeProcurementHistory {
	return runtimePrepareActiveProcurementConfigured(t, pool, nil, nil)
}

// The optional actual APP pool configures budgets before the first reservation.
// Existing fixtures retain their original unconfigured history path.
func runtimePrepareActiveProcurementConfigured(t *testing.T, pool, business *pgxpool.Pool, beforeReserve func(tenant, user, key, admin, account string)) runtimeProcurementHistory {
	t.Helper()
	if business == nil {
		business = pool
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	h := runtimeProcurementHistory{tenant: uuid.NewString()}
	user, operator, approver, admin := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	model, key, channel, version, credential := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("active procurement original catalog transaction")
	}
	defer tx.Rollback(context.Background())
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal("active procurement identity/catalog fixture", err)
		}
	}
	exec(`INSERT INTO tenants(id,name) VALUES($1,'runtime active procurement')`, h.tenant)
	for _, u := range []struct{ id, role string }{{user, "none"}, {operator, "finance_operator"}, {approver, "finance_approver"}, {admin, "admin"}} {
		exec(`INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused-local-fixture',$3)`, u.id, u.id+"@example.invalid", u.role)
	}
	exec(`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, h.tenant, user)
	exec(`INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,'runtime-procurement',$2,'v1')`, model,
		`{"version":"v1","model":"runtime-procurement","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`)
	exec(`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, h.tenant, model)
	exec(`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'runtime procurement',$1,$1,$4,1000)`, key, h.tenant, user, []string{model})
	exec(`INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1000000,1000000,clock_timestamp()-interval '1 day',$2)`, model, operator)
	exec(`INSERT INTO channels(id,name,current_version) VALUES($1,'runtime procurement',$2)`, channel, version)
	exec(`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused-local-fixture')`, credential, channel)
	exec(`INSERT INTO channel_versions(id,channel_id,base_url,credential_id,region) VALUES($1,$2,'https://provider.invalid',$3,'global')`, version, channel, credential)
	exec(`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1)`, version, channel, credential)
	exec(`INSERT INTO cost_versions(channel_id,upstream_model,input_rate,output_rate,basis) VALUES($1,'upstream',2000000,2000000,'contract')`, channel)
	if tx.Commit(ctx) != nil {
		t.Fatal("commit active procurement original catalog")
	}

	store := billing.New(business)
	creditID := uuid.NewString()
	credit, err := store.CreateCredit(ctx, billing.CreditInput{TenantID: h.tenant, ActorID: operator, BusinessRef: creditID,
		ProofRef: "receipt:runtime-active-procurement", Amount: 1000, IdempotencyKey: creditID, RequestHMAC: creditID})
	if err != nil {
		t.Fatal("active procurement original credit proposal", err)
	}
	if _, err = store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: h.tenant, OrderID: credit.ID, ActorID: approver}); err != nil {
		t.Fatal("active procurement original credit approval", err)
	}
	p := procurement.New(business)
	created, err := p.CreateAccount(ctx, admin, procurement.CreateInput{OperationID: uuid.NewString(), Label: "runtime active supplier",
		Provider: "isolated-local", State: "active", LimitAmount: "1000"})
	if err != nil || created.Status != "applied" || created.TargetID == "" {
		t.Fatalf("original active procurement create: status=%s error=%v", created.Status, err)
	}
	h.account = created.TargetID
	var revision string
	if pool.QueryRow(ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, channel).Scan(&revision) != nil {
		t.Fatal("active procurement current channel revision")
	}
	bound, err := p.BindCredential(ctx, admin, procurement.BindingInput{OperationID: uuid.NewString(), ChannelID: channel,
		CredentialID: credential, AccountID: h.account, ExpectedChannelRevision: revision})
	if err != nil || bound.Status != "applied" {
		t.Fatalf("original active procurement binding: status=%s error=%v", bound.Status, err)
	}
	if beforeReserve != nil {
		beforeReserve(h.tenant, user, key, admin, h.account)
	}
	var now time.Time
	if pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
		t.Fatal("active procurement actual deadline clock")
	}
	h.request = uuid.NewString()
	reserved, err := store.Reserve(ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: h.tenant, UserID: user, KeyID: key},
		RequestID: h.request, ModelID: model, IdempotencyKey: h.request, RequestHMAC: h.request,
		ProfileVersion: "v1", PolicyVersion: "r3", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: now.Add(2 * time.Minute)})
	if err != nil || reserved.Amount != 120 {
		t.Fatalf("original active procurement customer R=120: amount=%d error=%v", reserved.Amount, err)
	}
	defer store.EndRequest(h.request)
	var quote procurementexecution.CostQuote
	if pool.QueryRow(ctx, `WITH at AS MATERIALIZED (SELECT clock_timestamp() AS stamp)
 SELECT id,input_rate,output_rate,basis FROM cost_versions,at
 WHERE channel_id=$1 AND upstream_model='upstream' AND effective_at<=at.stamp
 ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, channel).Scan(&quote.VersionID, &quote.InputRate, &quote.OutputRate, &quote.Basis) != nil {
		t.Fatal("active procurement original current quote")
	}
	attempt, err := frozenattempt.Reject(ctx, store, frozenattempt.Input{TenantID: h.tenant, RequestID: h.request, ChannelID: channel,
		ChannelVersionID: version, CredentialID: credential, UpstreamModel: "upstream", Sequence: 1, CostQuote: &quote})
	if err != nil {
		t.Fatal("original active procurement attempt U=240", err)
	}
	h.attempt = attempt.ID
	if err = store.PrepareDispatch(ctx, billing.DispatchInput{TenantID: h.tenant, AttemptID: h.attempt}); err != nil {
		t.Fatal("original active procurement actual dispatch permit", err)
	}
	final, err := store.RecordFinalization(ctx, billing.FinalizationInput{ID: uuid.NewString(), TenantID: h.tenant, RequestID: h.request,
		AttemptID: h.attempt, Source: "runtime-active-procurement", SourceEventID: uuid.NewString(),
		Usage:           billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
		ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true})
	if err != nil {
		t.Fatal("original active procurement final usage", err)
	}
	h.finalization, h.event = final.ID, final.EventID
	settled, err := store.Settle(ctx, billing.SettleInput{TenantID: h.tenant, RequestID: h.request})
	if err != nil || settled.ChargedAmount != 12 {
		t.Fatalf("original active procurement retail=12: amount=%d error=%v", settled.ChargedAmount, err)
	}
	return h
}

func runtimeAssertActiveProcurement(t *testing.T, tx pgx.Tx, schema string, h runtimeProcurementHistory) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q := func(table string) string { return pgx.Identifier{schema, table}.Sanitize() }
	var exact bool
	sql := `SELECT a.state='active' AND a.revision=1 AND a.held_amount=0 AND a.spent_amount=24
 AND p.upper_amount=240 AND p.held_amount=0 AND p.spent_amount=24 AND p.state='settled' AND NOT p.anomaly
 AND u.delivery_state='completed' AND u.cost_status='contract' AND u.cost_amount=24
 AND b.account_id=a.id AND b.credential_id=u.credential_id AND b.channel_id=u.channel_id
 AND s.status='settled' AND s.charged_amount=12 AND s.finalization_id=$4
 AND (SELECT count(e.id) FROM ` + q("procurement_budget_events") + ` e WHERE e.attempt_id=u.id)=2
 AND EXISTS(SELECT 1 FROM ` + q("procurement_budget_events") + ` e WHERE e.attempt_id=u.id AND e.type='reserved' AND e.held_delta=240 AND e.spent_delta=0)
 AND EXISTS(SELECT 1 FROM ` + q("procurement_budget_events") + ` e WHERE e.attempt_id=u.id AND e.type='cost_confirmed' AND e.held_delta=-240 AND e.spent_delta=24 AND e.evidence_ref=$5)
 AND (SELECT count(i.event_id) FROM ` + q("procurement_evidence_identities") + ` i WHERE i.attempt_id=u.id AND i.event_id=$5)=1
 FROM ` + q("procurement_accounts") + ` a JOIN ` + q("procurement_reservations") + ` p ON p.account_id=a.id
 JOIN ` + q("upstream_attempts") + ` u ON u.id=p.attempt_id
 JOIN ` + q("credential_procurement_bindings") + ` b ON b.id=p.binding_id
 JOIN ` + q("settlements") + ` s ON s.request_id=u.request_id AND s.tenant_id=u.tenant_id
 WHERE a.id=$1 AND u.id=$2 AND u.request_id=$3 AND u.tenant_id=$6`
	if err := tx.QueryRow(ctx, sql, h.account, h.attempt, h.request, h.finalization, h.event, h.tenant).Scan(&exact); err != nil || !exact {
		t.Fatalf("actual APP nonzero supplier/retail sources differ: exact=%t error=%v", exact, err)
	}
}

func TestFundingLotsRuntimePostgresActiveProcurementCost(t *testing.T) {
	var history runtimeProcurementHistory
	f := newSnapshotFixturePrepared(t, func(_ *snapshotFixture, pool *pgxpool.Pool) {
		history = runtimePrepareActiveProcurement(t, pool)
	})
	// Plan/Init must prove the original complete source graph first. In
	// particular, an Unproven historical order is not accepted as a fixture pass.
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	if plan.Metadata.TenantCount != 1 || plan.Metadata.AvailableTotal != "988" || plan.Metadata.HeldTotal != "0" {
		t.Fatal("active procurement independent legacy opening arithmetic")
	}
	fundingLotsInitOrdinary(t, f, plan.Expected)
	owner := f.connect(t)
	expected := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
	runtimeFixtureGrant(t, owner, f.pins)
	_, app := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	runtimeAssertActiveProcurement(t, app, f.schema, history)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	facts, err := fundinglotscan.ReadWallet(ctx, fundingLotsObserver{ctx, app}, f.schema, fundinglotscan.ModeOrdinary)
	cancel()
	report := facts.Summary()
	if err != nil || report.Status != "consistent" || !report.CoverageComplete || !report.PhysicalEOFComplete || report.ScannedTables != 84 {
		t.Fatalf("actual APP active procurement complete source graph: error=%v status=%s procurement=%+v", err, report.Status, report.Procurement)
	}
	p := report.Procurement
	if p.Status != "balanced" || !p.CoverageComplete || p.Structural != (fundinglotverify.ProcurementEvidenceGraphStructural{}) ||
		p.Mismatches != (fundinglotverify.ProcurementEvidenceGraphMismatches{}) || p.Unproven != (fundinglotverify.ProcurementEvidenceGraphUnproven{}) ||
		p.Scanned.Accounts != 1 || p.Scanned.Bindings != 1 || p.Scanned.Attempts != 1 || p.Scanned.Reservations != 1 ||
		p.Scanned.BudgetEvents != 2 || p.Scanned.Identities != 1 || p.Scanned.Metering != 1 || p.Scanned.Finalizations != 1 || p.Scanned.Commands != 2 {
		t.Fatalf("actual APP did not prove the nonempty single-attempt supplier graph: %+v", p)
	}
	if report.Periodic.Scanned.Controls != 0 || report.Periodic.Scanned.Windows != 0 || report.Periodic.Scanned.Allocations != 0 || report.Periodic.Scanned.Events != 0 {
		t.Fatal("active procurement fixture must not claim periodic-budget activity")
	}
	runtimeIssueOrdinary(t, app, f.pins, expected)
	runtimeFixtureCleanup(t, app.Conn(), app.Tx)

	// A real final cost inconsistent with its original usage and immutable cost
	// event must fail through the same actual APP composer, even after a valid
	// offline epoch was installed. Restore the isolated corruption afterwards.
	restore := runtimeCorruptScalar(t, f, "upstream_attempts", "cost_amount", "true", "cost_amount+1")
	defer restore()
	_, damaged := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	proof, err := fundinglotruntime.VerifyOrdinaryApplicationTx(context.Background(), damaged, f.pins, expected)
	if proof != nil || err != fundinglotruntime.ErrRuntime {
		t.Fatal("actual APP accepted supplier cost inconsistent with original usage")
	}
	runtimeAssertReadOnly(t, damaged, false)
	runtimeFixtureCleanup(t, damaged.Conn(), damaged.Tx)
}
