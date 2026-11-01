package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type sandboxFundingFixture struct {
	*fixture
	app, worker, projector            *pgxpool.Pool
	schema, environment, sku, version string
	roles                             []string
	signing                           []byte
}

func sandboxFundingTest(t *testing.T) *sandboxFundingFixture {
	t.Helper()
	f := fundingFixture(t)
	x := &sandboxFundingFixture{fixture: f, environment: uuid.NewString(), sku: uuid.NewString(), version: uuid.NewString(), signing: make([]byte, 32)}
	if _, e := rand.Read(x.signing); e != nil {
		t.Fatal(e)
	}
	if e := f.pool.QueryRow(f.ctx, `SELECT current_schema()`).Scan(&x.schema); e != nil {
		t.Fatal(e)
	}
	prefix := "payfund_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	var pools []*pgxpool.Pool
	t.Cleanup(func() {
		for _, pool := range pools {
			pool.Close()
		}
		for _, role := range x.roles {
			if _, e := f.pool.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Error(e)
			}
			if _, e := f.pool.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Error(e)
			}
		}
	})
	for _, suffix := range []string{"app", "worker", "projector"} {
		role := prefix + "_" + suffix
		password := make([]byte, 32)
		if _, e := rand.Read(password); e != nil {
			t.Fatal(e)
		}
		secret := hex.EncodeToString(password)
		if _, e := f.pool.Exec(f.ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+secret+"'"); e != nil {
			t.Fatal(e)
		}
		x.roles = append(x.roles, role)
		cfg := f.pool.Config().Copy()
		cfg.ConnConfig.User = role
		cfg.ConnConfig.Password = secret
		cfg.MaxConns = 4
		pool, e := pgxpool.NewWithConfig(f.ctx, cfg)
		if e != nil {
			t.Fatal(e)
		}
		pools = append(pools, pool)
	}
	x.app, x.worker, x.projector = pools[0], pools[1], pools[2]
	if e := operations.ProvisionFor(f.ctx, f.pool, x.schema, x.roles[0], x.roles[2]); e != nil {
		t.Fatal("application/projector provision", e)
	}
	x.exec(t, f.pool, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role)
 SELECT $1,'fixture','fixture',(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,$2,$3,$4,$5`, x.environment, strings.Repeat("a", 64), x.roles[0], x.roles[1], x.roles[2])
	if e := operations.ProvisionPaymentWorkerFor(f.ctx, f.pool, x.schema, x.roles[0], x.roles[1], x.roles[2]); e != nil {
		t.Fatal("payment worker provision", e)
	}
	for i, role := range []operations.PaymentDatabaseRole{operations.PaymentApplicationRole, operations.PaymentWorkerRole, operations.PaymentProjectionRole} {
		if e := operations.VerifyPaymentRole(f.ctx, pools[i], role); e != nil {
			t.Fatal("actual payment LOGIN", role, e)
		}
		var current, session string
		if e := pools[i].QueryRow(f.ctx, `SELECT current_user,session_user`).Scan(&current, &session); e != nil || current != x.roles[i] || current != session {
			t.Fatal("not actual LOGIN", e)
		}
	}
	tx, e := x.app.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	op := uuid.NewString()
	x.command(t, tx, op, "payment.sku.publish", f.admin, "platform")
	x.exec(t, tx, `INSERT INTO payment_skus(id,created_by) VALUES($1,$2)`, x.sku, f.admin)
	x.exec(t, tx, `INSERT INTO payment_sku_versions(id,sku_id,version,display_name,amount,wallet_credit_amount,environment_id,merchant_id,payee_label,publish_operation_id,created_by) VALUES($1,$2,1,'fixture',100,100,$3,'fixture','fixture',$4,$5)`, x.version, x.sku, x.environment, op, f.admin)
	x.exec(t, tx, `INSERT INTO payment_sku_changes(operation_id,sku_id,kind,before_revision,after_revision,published_version_id,before_enabled,after_enabled) VALUES($1,$2,'payment.sku.publish',0,1,$3,false,false)`, op, x.sku, x.version)
	x.terminal(t, tx, op, x.version)
	op = uuid.NewString()
	x.command(t, tx, op, "payment.sku.activate", f.admin, "platform")
	x.exec(t, tx, `INSERT INTO payment_sku_changes(operation_id,sku_id,kind,before_revision,after_revision,before_enabled,after_enabled,after_version_id) VALUES($1,$2,'payment.sku.activate',1,2,false,true,$3)`, op, x.sku, x.version)
	x.terminal(t, tx, op, x.sku)
	x.exec(t, tx, `UPDATE payment_policy SET enabled=true`)
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	return x
}

func (x *sandboxFundingFixture) exec(t *testing.T, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, sql string, args ...any) {
	t.Helper()
	if _, e := db.Exec(x.ctx, sql, args...); e != nil {
		t.Fatal(e)
	}
}
func (x *sandboxFundingFixture) command(t *testing.T, tx pgx.Tx, op, kind, actor, scope string) {
	t.Helper()
	x.exec(t, tx, `INSERT INTO commerce_commands(operation_id,actor,scope,kind,request_digest) VALUES($1,$2,$3,$4,$5)`, op, actor, scope, kind, strings.Repeat("a", 64))
}
func (x *sandboxFundingFixture) terminal(t *testing.T, tx pgx.Tx, op, resource string) {
	t.Helper()
	x.exec(t, tx, `INSERT INTO commerce_operation_receipts(operation_id,kind,scope,actor,status,reason,resource_id) SELECT operation_id,kind,scope,actor,'accepted','accepted',$2 FROM commerce_commands WHERE operation_id=$1`, op, resource)
	x.exec(t, tx, `INSERT INTO audit_events(actor,tenant_scope,action,target) SELECT actor,CASE WHEN scope='platform' THEN NULL ELSE substr(scope,8) END,kind,operation_id FROM commerce_commands WHERE operation_id=$1`, op)
}

func (x *sandboxFundingFixture) evidence(t *testing.T) (order, evidence string) {
	t.Helper()
	order, evidence = uuid.NewString(), uuid.NewString()
	tx, e := x.app.Begin(x.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(x.ctx)
	x.command(t, tx, order, "payment.order.create", x.user, "tenant:"+x.tenant)
	x.exec(t, tx, `INSERT INTO payment_orders(id,tenant_id,creator,sku_version_id,display_name,amount,wallet_credit_amount,environment_id,merchant_id,payee_label,merchant_order_id,created_at,expires_at) VALUES($1,$2,$3,$4,'ignored',1,1,$5,'ignored','ignored','ignored',clock_timestamp(),clock_timestamp())`, order, x.tenant, x.user, x.version, x.environment)
	var merchant string
	var created, expires, now time.Time
	if e = tx.QueryRow(x.ctx, `SELECT merchant_order_id,created_at,expires_at,clock_timestamp() FROM payment_orders WHERE id=$1`, order).Scan(&merchant, &created, &expires, &now); e != nil {
		t.Fatal(e)
	}
	now = now.UTC()
	command, outbox := uuid.NewString(), uuid.NewString()
	body, e := p.CanonicalCheckout(p.CheckoutRequest{SchemaVersion: 1, EnvironmentID: x.environment, MerchantID: "fixture", CommandID: command, MerchantOrderID: merchant, Amount: "100", Currency: "CNY", CreatedAt: created.UTC().Format(voucherTimeLayout), ExpiresAt: expires.UTC().Format(voucherTimeLayout)})
	if e != nil {
		t.Fatal(e)
	}
	x.exec(t, tx, `INSERT INTO payment_command_outbox(id,order_id,kind,external_command_id,canonical_parameters) VALUES($1,$2,'checkout',$3,$4)`, outbox, order, command, body)
	x.terminal(t, tx, order, order)
	transaction := "fixture:" + uuid.NewString()
	body, e = p.CanonicalNotification(p.Notification{SchemaVersion: 1, EventID: "event:" + uuid.NewString(), EventType: "capture.confirmed", EnvironmentID: x.environment, MerchantID: "fixture", OrderID: order, ProviderTransactionID: &transaction, Amount: "100", Currency: "CNY", OccurredAt: now.Format(voucherTimeLayout), ProviderOrderRevision: "2"})
	if e != nil {
		t.Fatal(e)
	}
	keys, e := p.NewKeyring("test", []p.KeySpec{{ID: "test", Key: x.signing, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}})
	if e != nil {
		t.Fatal(e)
	}
	env := p.Environment{ID: x.environment, MerchantID: "fixture", PayeeLabel: "fixture"}
	h, e := keys.SignNotification(env, p.SignedHeaders{KeyID: "test", DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, body)
	if e != nil {
		t.Fatal(e)
	}
	verified, e := keys.VerifyNotification(env, now, h, body)
	if e != nil {
		t.Fatal(e)
	}
	body, e = verified.Canonical()
	if e != nil {
		t.Fatal(e)
	}
	inbox := uuid.NewString()
	x.exec(t, tx, `INSERT INTO payment_inbox(id,environment_id,merchant_id,event_id,canonical_body,verified_key_id) VALUES($1,$2,'fixture','ignored',$3,'test')`, inbox, x.environment, body)
	x.exec(t, tx, `INSERT INTO payment_verified_evidence(id,inbox_id,environment_id,merchant_id,order_id,kind,fact_digest,verified_at) VALUES($1,$2,$3,'fixture',$4,'capture',$5,clock_timestamp())`, evidence, inbox, x.environment, order, strings.Repeat("0", 64))
	x.exec(t, tx, `INSERT INTO payment_evidence_processing(evidence_id) VALUES($1)`, evidence)
	if e = tx.Commit(x.ctx); e != nil {
		t.Fatal(e)
	}
	x.exec(t, x.worker, `UPDATE payment_evidence_processing SET state='leased',generation=generation+1,attempt_count=attempt_count+1 WHERE evidence_id=$1`, evidence)
	return
}

func (x *sandboxFundingFixture) source(t *testing.T, order, evidence string) (pgx.Tx, string) {
	t.Helper()
	tx, e := x.worker.Begin(x.ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { tx.Rollback(context.Background()) })
	x.exec(t, tx, `SELECT id FROM payment_orders WHERE id=$1 FOR NO KEY UPDATE`, order)
	x.exec(t, tx, `INSERT INTO payment_provider_revisions(environment_id,merchant_id,order_id,provider_order_revision,kind,fact_digest,evidence_id) VALUES($1,'fixture',$2,2,'capture',$3,$4)`, x.environment, order, strings.Repeat("0", 64), evidence)
	identity, capture := uuid.NewString(), uuid.NewString()
	x.exec(t, tx, `INSERT INTO payment_transaction_identities(id,environment_id,merchant_id,provider_transaction_id,order_id,tenant_id,amount,currency,captured_at,provider_order_revision,evidence_id,processing_generation) SELECT $1,environment_id,merchant_id,provider_transaction_id,order_id,$2,amount,currency,happened_at,provider_order_revision,id,1 FROM payment_verified_evidence WHERE id=$3`, identity, x.tenant, evidence)
	x.exec(t, tx, `INSERT INTO payment_order_capture_selections(order_id,identity_id,first_evidence_id) VALUES($1,$2,$3)`, order, identity, evidence)
	x.exec(t, tx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, x.tenant)
	x.exec(t, tx, `SAVEPOINT funds`)
	x.exec(t, tx, `INSERT INTO payment_captures(id,order_id,identity_id,tenant_id,environment_id,merchant_id,provider_transaction_id,amount,wallet_credit_amount,currency,captured_at,provider_order_revision,verified_evidence_id,processing_generation) SELECT $1,order_id,id,tenant_id,environment_id,merchant_id,provider_transaction_id,amount,amount,currency,captured_at,provider_order_revision,evidence_id,1 FROM payment_transaction_identities WHERE id=$2`, capture, identity)
	x.exec(t, tx, `INSERT INTO payment_effects(capture_id,identity_id,environment_id,merchant_id,provider_transaction_id) SELECT $1,id,environment_id,merchant_id,provider_transaction_id FROM payment_transaction_identities WHERE id=$2`, capture, identity)
	return tx, capture
}
func (x *sandboxFundingFixture) finish(t *testing.T, tx pgx.Tx, capture, evidence string) {
	t.Helper()
	x.exec(t, tx, `INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff) VALUES('payment-sandbox-worker',$1,'payment.capture.credited',$2,'{"amount":"100"}')`, x.tenant, capture)
	tag, e := tx.Exec(x.ctx, `UPDATE payment_evidence_processing SET state='processed' WHERE evidence_id=$1 AND state='leased' AND generation=1 AND lease_until>clock_timestamp()`, evidence)
	if e != nil || tag.RowsAffected() != 1 {
		t.Fatal("processing CAS", e)
	}
	if e = tx.Commit(x.ctx); e != nil {
		t.Fatal(e)
	}
}

func (x *sandboxFundingFixture) queryEvidence(t *testing.T, order, original string) string {
	t.Helper()
	var outbox, command, merchant, transaction, amount, currency, revision string
	var parameters []byte
	var occurred time.Time
	if e := x.app.QueryRow(x.ctx, `SELECT b.id,b.external_command_id,o.merchant_order_id,b.canonical_parameters,e.provider_transaction_id,e.amount::text,e.currency,e.happened_at,e.provider_order_revision::text
 FROM payment_orders o JOIN payment_command_outbox b ON b.order_id=o.id AND b.kind='checkout' JOIN payment_verified_evidence e ON e.id=$2 WHERE o.id=$1`, order, original).Scan(&outbox, &command, &merchant, &parameters, &transaction, &amount, &currency, &occurred, &revision); e != nil {
		t.Fatal(e)
	}
	x.exec(t, x.worker, `UPDATE payment_command_outbox SET state='leased',generation=generation+1,attempt_count=attempt_count+1 WHERE id=$1`, outbox)
	supervisor, query := uuid.NewString(), uuid.NewString()
	tx, e := x.worker.Begin(x.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(x.ctx)
	x.exec(t, tx, `UPDATE payment_command_outbox SET state='submission_unknown',last_error_code='network' WHERE id=$1`, outbox)
	x.exec(t, tx, `INSERT INTO payment_command_outbox(id,order_id,kind,original_outbox_id) VALUES($1,$2,'query',$3)`, supervisor, order, outbox)
	if e = tx.Commit(x.ctx); e != nil {
		t.Fatal(e)
	}
	x.exec(t, x.worker, `UPDATE payment_command_outbox SET state='leased',generation=generation+1,attempt_count=attempt_count+1 WHERE id=$1`, supervisor)
	request := p.QueryRequest{SchemaVersion: 1, QueryID: query, EnvironmentID: x.environment, MerchantID: "fixture", MerchantOrderID: merchant, CommandID: command, CommandKind: "checkout", ParameterDigest: p.RequestDigest(parameters)}
	canonical, e := p.CanonicalQuery(request)
	if e != nil {
		t.Fatal(e)
	}
	tx, e = x.worker.Begin(x.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(x.ctx)
	x.exec(t, tx, `INSERT INTO payment_query_attempts(query_id,supervisor_id,created_generation,canonical_request,mutation_generation) VALUES($1,$2,1,$3,1)`, query, supervisor, canonical)
	x.exec(t, tx, `UPDATE payment_command_outbox SET current_query_id=$2 WHERE id=$1 AND state='leased' AND generation=1`, supervisor, query)
	if e = tx.Commit(x.ctx); e != nil {
		t.Fatal(e)
	}
	body, e := p.CanonicalQueryReply(p.QueryReply{SchemaVersion: 1, QueryID: query, EnvironmentID: x.environment, MerchantID: "fixture", MerchantOrderID: merchant, CommandID: command, CommandKind: "checkout", ParameterDigest: p.RequestDigest(parameters), CommandState: "completed", ProviderOrderRevision: &revision, Result: &p.CommandResult{Status: "captured", Capture: &p.CaptureFact{ProviderTransactionID: transaction, Amount: amount, Currency: currency, OccurredAt: occurred.UTC().Format(voucherTimeLayout), ProviderOrderRevision: revision}}})
	if e != nil {
		t.Fatal(e)
	}
	tx, e = x.worker.Begin(x.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(x.ctx)
	var now time.Time
	if e = tx.QueryRow(x.ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		t.Fatal(e)
	}
	now = now.UTC()
	keys, e := p.NewKeyring("test", []p.KeySpec{{ID: "test", Key: x.signing, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}})
	if e != nil {
		t.Fatal(e)
	}
	env := p.Environment{ID: x.environment, MerchantID: "fixture", PayeeLabel: "fixture"}
	h, e := keys.SignQuery(env, request, p.SignedHeaders{KeyID: "test", DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, body)
	if e != nil {
		t.Fatal(e)
	}
	verified, e := keys.VerifyQuery(env, request, now, h, body)
	if e != nil {
		t.Fatal(e)
	}
	body, e = verified.Canonical()
	if e != nil {
		t.Fatal(e)
	}
	stored, evidence := uuid.NewString(), uuid.NewString()
	x.exec(t, tx, `INSERT INTO payment_verified_queries(id,query_id,canonical_body,verified_key_id) VALUES($1,$2,$3,'test')`, stored, query, body)
	x.exec(t, tx, `INSERT INTO payment_verified_evidence(id,verified_query_id,environment_id,merchant_id,order_id,kind,fact_digest,verified_at) VALUES($1,$2,$3,'fixture',$4,'capture',$5,clock_timestamp())`, evidence, stored, x.environment, order, strings.Repeat("0", 64))
	x.exec(t, tx, `INSERT INTO payment_evidence_processing(evidence_id) VALUES($1)`, evidence)
	if e = tx.Commit(x.ctx); e != nil {
		t.Fatal(e)
	}
	x.exec(t, x.worker, `UPDATE payment_evidence_processing SET state='leased',generation=generation+1,attempt_count=attempt_count+1 WHERE evidence_id=$1`, evidence)
	return evidence
}

func TestPostgresFundingSandboxVerifiedQueryUsesOriginalRequestAndWorkerOnlyTables(t *testing.T) {
	x := sandboxFundingTest(t)
	order, original := x.evidence(t)
	evidence := x.queryEvidence(t, order, original)
	tx, capture := x.source(t, order, evidence)
	r, e := New(x.worker).ApplyFundingTx(x.ctx, tx, FundingSourceRef{FundingSandboxCapture, capture}, FundingScope{x.tenant, sandboxFundingActor, ""})
	if e != nil || !r.Applied {
		t.Fatal(r, e)
	}
	x.finish(t, tx, capture, evidence)
	c, e := New(x.app).VerifyFunding(x.ctx, "")
	if e != nil || !c.Balanced {
		t.Fatalf("query source %+v %v", c, e)
	}
	for _, table := range []string{"users", "memberships", "credit_orders", "vouchers", "periodic_budget_controls"} {
		if _, e = x.worker.Exec(x.ctx, "SELECT * FROM "+pgx.Identifier{table}.Sanitize()+" LIMIT 1"); e == nil {
			t.Fatal("worker acquired unrelated source read", table)
		}
	}
}

func TestPostgresFundingSandboxActualWorkerUniqueReceiptAndRebuild(t *testing.T) {
	x := sandboxFundingTest(t)
	order, evidence := x.evidence(t)
	tx, capture := x.source(t, order, evidence)
	r, e := New(x.worker).ApplyFundingTx(x.ctx, tx, FundingSourceRef{FundingSandboxCapture, capture}, FundingScope{x.tenant, sandboxFundingActor, ""})
	if e != nil || !r.Applied || r.RedemptionID != "" || r.Receipt.Amount != 100 {
		t.Fatalf("new funding: %+v %v", r, e)
	}
	x.finish(t, tx, capture, evidence)
	tx, e = x.worker.Begin(x.ctx)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := New(x.worker).ApplyFundingTx(x.ctx, tx, FundingSourceRef{FundingSandboxCapture, capture}, FundingScope{x.tenant, sandboxFundingActor, ""})
	if e != nil || replay.Applied || replay.Receipt != r.Receipt {
		t.Fatalf("replay %+v %v", replay, e)
	}
	if e = tx.Commit(x.ctx); e != nil {
		t.Fatal(e)
	}
	for _, scope := range []string{"", x.tenant} {
		c, e := New(x.app).VerifyFunding(x.ctx, scope)
		if e != nil || !c.Balanced {
			t.Fatalf("verify %+v %v", c, e)
		}
		l, e := New(x.app).VerifyLedger(x.ctx, Query{TenantID: scope})
		if e != nil || !l.Balanced {
			t.Fatalf("ledger %+v %v", l, e)
		}
	}
	service, e := NewVoucherService(x.ctx, New(x.app), VoucherOptions{})
	if e != nil {
		t.Fatal(e)
	}
	summary, e := service.GetFundingSummary(x.ctx, VoucherReader{x.user, x.tenant})
	if e != nil || summary.SandboxCaptureAmount != "100" || summary.ManualCreditAmount != "1000" || summary.TotalAmount != "1100" {
		t.Fatalf("summary %+v %v", summary, e)
	}
	page, e := service.ListFundingReceipts(x.ctx, VoucherReader{x.user, x.tenant}, FundingReceiptPageInput{SourceKind: FundingSandboxCapture})
	if e != nil || len(page.Data) != 1 || page.Data[0].SourceID != capture {
		t.Fatal(page, e)
	}
	var reference, operation string
	if e = x.app.QueryRow(x.ctx, `SELECT reference,operation_id FROM journals WHERE id=$1`, r.Receipt.JournalID).Scan(&reference, &operation); e != nil || reference != order || operation != capture {
		t.Fatal("journal binding", e)
	}
	if _, e = New(x.projector).Project(x.ctx, ProjectInput{Limit: 200}); e != nil {
		t.Fatal(e)
	}
	if _, e = x.store.Rebuild(x.ctx, "sandbox-rebuild"); e != nil {
		t.Fatal(e)
	}
}

func TestPostgresFundingSandboxRejectsAuthorityAndUnsupportedTransactions(t *testing.T) {
	x := sandboxFundingTest(t)
	order, evidence := x.evidence(t)
	tx, capture := x.source(t, order, evidence)
	for _, scope := range []FundingScope{{x.tenant, "other", ""}, {x.tenant, sandboxFundingActor, uuid.NewString()}, {uuid.NewString(), sandboxFundingActor, ""}} {
		if _, e := New(x.worker).ApplyFundingTx(x.ctx, tx, FundingSourceRef{FundingSandboxCapture, capture}, scope); e == nil {
			t.Fatal("invalid scope accepted")
		}
	}
	tx.Rollback(x.ctx)
	for _, pool := range []*pgxpool.Pool{x.app, x.projector, x.pool} {
		bad, e := pool.Begin(x.ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = New(pool).ApplyFundingTx(x.ctx, bad, FundingSourceRef{FundingSandboxCapture, uuid.NewString()}, FundingScope{x.tenant, sandboxFundingActor, ""})
		if e == nil {
			t.Fatal("untrusted role accepted")
		}
		bad.Rollback(x.ctx)
	}
	bad, e := x.worker.BeginTx(x.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if e != nil {
		t.Fatal(e)
	}
	_, e = New(x.worker).ApplyFundingTx(x.ctx, bad, FundingSourceRef{FundingSandboxCapture, uuid.NewString()}, FundingScope{x.tenant, sandboxFundingActor, ""})
	if e == nil {
		t.Fatal("RR accepted")
	}
	bad.Rollback(x.ctx)
}

func TestPostgresFundingSandboxMixedSourcesNumericSummaryBeyondInt64(t *testing.T) {
	x := sandboxFundingTest(t)
	// Leave room in the wallet through real metered consumption. The sum of
	// retained funding receipts can exceed int64 while every live balance fits.
	appFixture := *x.fixture
	appFixture.store = New(x.app)
	appFixture.fund(t, Money(math.MaxInt64-1000))
	for i := 0; i < 7; i++ {
		r := appFixture.reserve(t)
		appFixture.final(t, r, confirmed(), "completed", "done", "none", false)
		if s, e := appFixture.store.Settle(x.ctx, SettleInput{TenantID: x.tenant, RequestID: r.RequestID}); e != nil || s.ChargedAmount != 15 {
			t.Fatal("real spend", e)
		}
	}
	order, evidence := x.evidence(t)
	tx, capture := x.source(t, order, evidence)
	if _, e := New(x.worker).ApplyFundingTx(x.ctx, tx, FundingSourceRef{FundingSandboxCapture, capture}, FundingScope{x.tenant, sandboxFundingActor, ""}); e != nil {
		t.Fatal(e)
	}
	x.finish(t, tx, capture, evidence)
	service := voucherTestService(t, &appFixture)
	issued := voucherTestCreate(t, &appFixture, service, 1, 5, true)
	if r, e := service.RedeemVoucher(x.ctx, voucherTestRedeem(&appFixture, issued.Codes[0].Code)); e != nil || r.Status != "accepted" {
		t.Fatal(r, e)
	}
	summary, e := service.GetFundingSummary(x.ctx, VoucherReader{x.user, x.tenant})
	if e != nil || summary.TotalAmount != "9223372036854775912" || summary.ManualCreditAmount != "9223372036854775807" || summary.VoucherGrantAmount != "5" || summary.SandboxCaptureAmount != "100" {
		t.Fatalf("numeric three-source totals %+v %v", summary, e)
	}
	for _, scope := range []string{"", x.tenant} {
		c, e := New(x.app).VerifyFunding(x.ctx, scope)
		if e != nil || !c.Balanced {
			t.Fatalf("mixed funding %+v %v", c, e)
		}
	}
	if _, e = x.store.Rebuild(x.ctx, "sandbox-numeric-rebuild"); e != nil {
		t.Fatal(e)
	}
}

func TestPostgresFundingSandboxCheckedOverflowLeavesNoMoney(t *testing.T) {
	t.Run("available-positive", func(t *testing.T) { sandboxFundingOverflowTest(t, false) })
	t.Run("clearing-negative", func(t *testing.T) { sandboxFundingOverflowTest(t, true) })
}

func sandboxFundingOverflowTest(t *testing.T, clearing bool) {
	x := sandboxFundingTest(t)
	// Owner-only cache fixture in the random schema; the helper must identify its
	// checkedAdd error before any money batch is sent, without catching SQL 22003.
	if clearing {
		x.exec(t, x.pool, `INSERT INTO ledger_accounts(kind,balance) VALUES('SANDBOX_FUNDING_CLEARING',$1)`, int64(math.MinInt64))
	} else {
		x.exec(t, x.pool, `UPDATE ledger_accounts SET balance=$2 WHERE tenant_id=$1 AND kind='AVAILABLE'`, x.tenant, int64(math.MaxInt64))
	}
	order, evidence := x.evidence(t)
	tx, capture := x.source(t, order, evidence)
	_, e := New(x.worker).ApplyFundingTx(x.ctx, tx, FundingSourceRef{FundingSandboxCapture, capture}, FundingScope{x.tenant, sandboxFundingActor, ""})
	var domain *Error
	if !errors.As(e, &domain) || domain.Code != "amount_overflow" {
		t.Fatal("not checked overflow", e)
	}
	var count int
	if e = tx.QueryRow(x.ctx, `SELECT count(*) FROM journals WHERE operation_id=$1`, capture).Scan(&count); e != nil || count != 0 {
		t.Fatal("overflow wrote journal", e)
	}
	x.exec(t, tx, `ROLLBACK TO SAVEPOINT funds`)
	x.exec(t, tx, `INSERT INTO payment_review_facts(id,order_id,evidence_id,identity_id,selection_order_id,reason) SELECT $1,s.order_id,$2,s.identity_id,s.order_id,'funding_amount_overflow' FROM payment_order_capture_selections s WHERE s.order_id=$3`, uuid.NewString(), evidence, order)
	x.exec(t, tx, `UPDATE payment_evidence_processing SET state='processing_review' WHERE evidence_id=$1 AND state='leased' AND generation=1`, evidence)
	if e = tx.Commit(x.ctx); e != nil {
		t.Fatal(e)
	}
	if clearing {
		x.exec(t, x.pool, `UPDATE ledger_accounts SET balance=0 WHERE kind='SANDBOX_FUNDING_CLEARING'`)
	} else {
		x.exec(t, x.pool, `UPDATE ledger_accounts SET balance=1000 WHERE tenant_id=$1 AND kind='AVAILABLE'`, x.tenant)
	}
	c, e := New(x.app).VerifyFunding(x.ctx, x.tenant)
	if e != nil || !c.Balanced {
		t.Fatalf("legal overflow review %+v %v", c, e)
	}
}

func TestPostgresFundingSandboxIncompleteOuterCommitRollsBackWholeGraph(t *testing.T) {
	x := sandboxFundingTest(t)
	order, evidence := x.evidence(t)
	before := fundingSnapshot(t, x.fixture)
	tx, capture := x.source(t, order, evidence)
	if _, e := New(x.worker).ApplyFundingTx(x.ctx, tx, FundingSourceRef{FundingSandboxCapture, capture}, FundingScope{x.tenant, sandboxFundingActor, ""}); e != nil {
		t.Fatal(e)
	}
	// The helper does not manufacture the caller's audit or ACK. Omitting both
	// must fail COMMIT, including source/identity/selection and wallet changes.
	if e := tx.Commit(x.ctx); e == nil {
		t.Fatal("incomplete outer graph committed")
	}
	if fundingSnapshot(t, x.fixture) != before {
		t.Fatal("failed outer COMMIT changed financial facts")
	}
	var count int
	if e := x.app.QueryRow(x.ctx, `SELECT (SELECT count(*) FROM payment_captures WHERE order_id=$1)+(SELECT count(*) FROM payment_transaction_identities WHERE order_id=$1)+(SELECT count(*) FROM payment_order_capture_selections WHERE order_id=$1)`, order).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial source graph retained", e)
	}
}

func TestPostgresFundingSandboxVerifierDetectsHistoricalBindingAndRefusesSwitch(t *testing.T) {
	x := sandboxFundingTest(t)
	order, evidence := x.evidence(t)
	tx, capture := x.source(t, order, evidence)
	if _, e := New(x.worker).ApplyFundingTx(x.ctx, tx, FundingSourceRef{FundingSandboxCapture, capture}, FundingScope{x.tenant, sandboxFundingActor, ""}); e != nil {
		t.Fatal(e)
	}
	x.finish(t, tx, capture, evidence)
	var active string
	if e := x.pool.QueryRow(x.ctx, `SELECT consumer FROM active_report_projection WHERE singleton`).Scan(&active); e != nil {
		t.Fatal(e)
	}
	mutate := func(table, sql string, args ...any) {
		t.Helper()
		tx, e := x.pool.Begin(x.ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(x.ctx)
		x.exec(t, tx, "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" DISABLE TRIGGER USER")
		x.exec(t, tx, sql, args...)
		x.exec(t, tx, "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" ENABLE TRIGGER USER")
		if e = tx.Commit(x.ctx); e != nil {
			t.Fatal(e)
		}
	}
	tests := []struct {
		name, table, damage, restore string
		want                         func(FundingCheck) bool
	}{
		{"identity_original_generation", "payment_transaction_identities", `UPDATE payment_transaction_identities SET processing_generation=2 WHERE order_id=$1`, `UPDATE payment_transaction_identities SET processing_generation=1 WHERE order_id=$1`, func(c FundingCheck) bool { return c.PaymentIdentityMismatches == 1 && c.SourceMismatches > 0 }},
		{"receipt_same_amount_wrong_source", "funding_receipts", `UPDATE funding_receipts SET source_id='11111111-1111-4111-8111-111111111111' WHERE source_kind='sandbox_capture' AND source_id=(SELECT id FROM payment_captures WHERE order_id=$1)`, `UPDATE funding_receipts SET source_id=(SELECT id FROM payment_captures WHERE order_id=$1) WHERE source_kind='sandbox_capture' AND source_id='11111111-1111-4111-8111-111111111111'`, func(c FundingCheck) bool { return c.MissingReceipts == 1 && c.OrphanReceipts == 1 }},
		{"credited_exact_payload", "projection_outbox", `UPDATE projection_outbox SET sanitized_payload='{"amount":"101"}' WHERE operation_id=(SELECT id FROM payment_captures WHERE order_id=$1) AND kind='credited'`, `UPDATE projection_outbox SET sanitized_payload='{"amount":"100"}' WHERE operation_id=(SELECT id FROM payment_captures WHERE order_id=$1) AND kind='credited'`, func(c FundingCheck) bool { return c.OutboxMismatches > 0 && c.JournalMismatches > 0 }},
		{"effect_wrong_transaction", "payment_effects", `UPDATE payment_effects SET provider_transaction_id='wrong:transaction' WHERE capture_id=(SELECT id FROM payment_captures WHERE order_id=$1)`, `UPDATE payment_effects SET provider_transaction_id=(SELECT provider_transaction_id FROM payment_captures WHERE order_id=$1) WHERE capture_id=(SELECT id FROM payment_captures WHERE order_id=$1)`, func(c FundingCheck) bool { return c.SourceMismatches > 0 && c.PaymentSelectionMismatches == 1 }},
		{"audit_exact_amount", "audit_events", `UPDATE audit_events SET sanitized_diff='{"amount":"101"}' WHERE action='payment.capture.credited' AND target=(SELECT id FROM payment_captures WHERE order_id=$1)`, `UPDATE audit_events SET sanitized_diff='{"amount":"100"}' WHERE action='payment.capture.credited' AND target=(SELECT id FROM payment_captures WHERE order_id=$1)`, func(c FundingCheck) bool { return c.SourceMismatches > 0 && c.ClearingMismatches > 0 }},
	}
	for n, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutate(tc.table, tc.damage, order)
			defer mutate(tc.table, tc.restore, order)
			for _, scope := range []string{"", x.tenant} {
				c, e := New(x.app).VerifyFunding(x.ctx, scope)
				if e != nil || c.Balanced || !tc.want(c) {
					t.Fatalf("corruption not independently detected: %+v %v", c, e)
				}
			}
			if _, e := x.store.Rebuild(x.ctx, fmt.Sprintf("sandbox-corrupt-%d", n)); e == nil {
				t.Fatal("corrupt source switched projection")
			}
			var after string
			if e := x.pool.QueryRow(x.ctx, `SELECT consumer FROM active_report_projection WHERE singleton`).Scan(&after); e != nil || after != active {
				t.Fatal("active projection changed", e)
			}
		})
	}
	c, e := New(x.app).VerifyFunding(x.ctx, "")
	if e != nil || !c.Balanced {
		t.Fatalf("fixture restore failed: %+v %v", c, e)
	}
}

type sandboxFundingNoSQLTx struct{ pgx.Tx }

func (sandboxFundingNoSQLTx) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("invalid funding arguments reached SQL")
}

func TestFundingSandboxRejectsInvalidLocatorBeforeSQL(t *testing.T) {
	for _, id := range []string{"", uuid.Nil.String(), "NOT-A-UUID", strings.ToUpper(uuid.NewString())} {
		_, e := (&Store{}).ApplyFundingTx(context.Background(), sandboxFundingNoSQLTx{}, FundingSourceRef{FundingSandboxCapture, id}, FundingScope{uuid.NewString(), sandboxFundingActor, ""})
		var domain *Error
		if !errors.As(e, &domain) || domain.Code != "invalid_funding_source" {
			t.Fatal(fmt.Sprintf("%q", id), e)
		}
	}
	for _, scope := range []FundingScope{{uuid.NewString(), "not-worker", ""}, {uuid.NewString(), sandboxFundingActor, uuid.NewString()}, {"bad-tenant", sandboxFundingActor, ""}} {
		if _, e := (&Store{}).ApplyFundingTx(context.Background(), sandboxFundingNoSQLTx{}, FundingSourceRef{FundingSandboxCapture, uuid.NewString()}, scope); e == nil {
			t.Fatal("invalid scope reached funds")
		}
	}
}
