package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/economics"
)

// This is a small query fixture, not a commercial writer/permission acceptance
// fixture. The server's independent tests exercise the real 001-022 business
// lifecycle. These tables isolate row bounds, NUMERIC100 and RR consistency.
type readFixture struct {
	pool                                                 *pgxpool.Pool
	ctx                                                  context.Context
	filter                                               Filter
	tenant, otherTenant, model, otherModel, channel, app string
}

func newReadFixture(t *testing.T) readFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required: reporting query tests use a dedicated PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal("invalid reporting test database configuration")
	}
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("reporting tests require the dedicated literal local PostgreSQL test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("reporting test database unavailable")
	}
	t.Cleanup(owner.Close)
	var db, user string
	var port int
	if err = owner.QueryRow(ctx, "SELECT current_database(),session_user,inet_server_port()").Scan(&db, &user, &port); err != nil || db != "moni_test" || user != "moni_test" || port != 55439 {
		t.Fatal("reporting test database identity unavailable")
	}
	schema := "reporting_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = owner.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if _, err := owner.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error("reporting test schema cleanup failed", err)
		}
	})
	app := "reporting-" + uuid.NewString()
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams = map[string]string{"search_path": pgx.Identifier{schema}.Sanitize() + ",pg_catalog", "application_name": app}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `CREATE TABLE tenants(id text PRIMARY KEY,name text NOT NULL);
CREATE TABLE models(id text PRIMARY KEY,public_name text NOT NULL);
CREATE TABLE channels(id text PRIMARY KEY,name text NOT NULL);
CREATE TABLE request_records(id text PRIMARY KEY,tenant_id text NOT NULL,model_id text NOT NULL,status text NOT NULL DEFAULT 'accepted',billing_status text NOT NULL DEFAULT 'reserved',protocol text,execution_kind text,created_at timestamptz NOT NULL,latest_finalization_id text);
CREATE TABLE finalization_records(id text PRIMARY KEY,tenant_id text NOT NULL,request_id text NOT NULL,source text,attempt_id text,created_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE TABLE request_meter_snapshots(tenant_id text NOT NULL,request_id text PRIMARY KEY,profile_version text NOT NULL,schema_id text NOT NULL,schema_digest bytea NOT NULL,variant_key text NOT NULL);
CREATE TABLE settlements(id text PRIMARY KEY,request_id text NOT NULL UNIQUE,tenant_id text NOT NULL,charged_amount bigint NOT NULL,refunded_amount bigint NOT NULL);
CREATE TABLE upstream_attempts(id text PRIMARY KEY,request_id text NOT NULL,channel_id text NOT NULL,sequence integer NOT NULL,cost_status text NOT NULL,cost_amount numeric(100,0),tenant_id text NOT NULL,latest_cost_finalization_id text,latest_cost_correction_id text);
CREATE TABLE attempt_meter_snapshots(tenant_id text NOT NULL,attempt_id text PRIMARY KEY,cost_version_id text);
CREATE TABLE cost_versions(id text PRIMARY KEY,currency text NOT NULL);
CREATE TABLE provider_cost_corrections(id text PRIMARY KEY,tenant_id text NOT NULL,attempt_id text NOT NULL,created_at timestamptz NOT NULL);
CREATE TABLE attempt_cost_components(tenant_id text NOT NULL,attempt_id text NOT NULL,component_id text NOT NULL,id text NOT NULL,layer text NOT NULL,currency text NOT NULL,basis text NOT NULL,amount_micro numeric(100,0),as_of timestamptz NOT NULL,revision bigint NOT NULL,PRIMARY KEY(attempt_id,component_id,revision));
CREATE TABLE request_economics_snapshots(tenant_id text NOT NULL,request_id text NOT NULL,offer_version_id text NOT NULL,task_class text NOT NULL,fx_policy_version_id text NOT NULL,valuation_currency text NOT NULL,fx_snapshot jsonb NOT NULL,created_at timestamptz NOT NULL,PRIMARY KEY(tenant_id,request_id));
CREATE TABLE funding_receipts(id text PRIMARY KEY,tenant_id text NOT NULL,basis_kind text NOT NULL,fiat_currency text,fiat_amount bigint,buy_rate_version_id text);
CREATE TABLE funding_lots(id text PRIMARY KEY,tenant_id text NOT NULL,funding_receipt_id text NOT NULL,issued_amount bigint NOT NULL);
CREATE TABLE funding_settlement_lots(tenant_id text NOT NULL,settlement_id text NOT NULL,allocation_index integer NOT NULL,lot_id text NOT NULL,charged_amount bigint NOT NULL,PRIMARY KEY(tenant_id,settlement_id,allocation_index));
CREATE TABLE funding_consumption_refund_lots(tenant_id text NOT NULL,settlement_id text NOT NULL,allocation_index integer NOT NULL,lot_id text NOT NULL,amount bigint NOT NULL);
CREATE TABLE media_settlement_lots(tenant_id text NOT NULL,settlement_id text NOT NULL,allocation_index integer NOT NULL,lot_id text NOT NULL,charged_amount bigint NOT NULL,PRIMARY KEY(tenant_id,settlement_id,allocation_index));
CREATE TABLE cash_refund_orders(tenant_id text NOT NULL,state text NOT NULL,approved_at timestamptz,fiat_currency text NOT NULL,paid_fiat_amount numeric(100,0));
CREATE TABLE media_requests(tenant_id text NOT NULL,id text NOT NULL,model_key text NOT NULL,operation text NOT NULL,profile_version text NOT NULL,state text NOT NULL,created_at timestamptz NOT NULL,PRIMARY KEY(tenant_id,id));
CREATE TABLE media_attempts(tenant_id text NOT NULL,id text NOT NULL,request_id text NOT NULL,delivery_state text NOT NULL,created_at timestamptz NOT NULL,PRIMARY KEY(tenant_id,id));
CREATE TABLE media_settlements(tenant_id text NOT NULL,id text NOT NULL,request_id text NOT NULL,charged_amount bigint NOT NULL,raw_amount numeric(100,0) NOT NULL,supplier_cost_status text NOT NULL,supplier_cost_amount bigint,status text NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,request_id));`)
	if err != nil {
		t.Fatal(err)
	}
	f := readFixture{pool: pool, ctx: ctx, filter: testFilter(), tenant: uuid.NewString(), otherTenant: uuid.NewString(), model: uuid.NewString(), otherModel: uuid.NewString(), channel: uuid.NewString(), app: app}
	f.exec(t, `INSERT INTO tenants VALUES($1,'tenant one'),($2,'tenant two')`, f.tenant, f.otherTenant)
	f.exec(t, `INSERT INTO models VALUES($1,'model one'),($2,'model two')`, f.model, f.otherModel)
	f.exec(t, `INSERT INTO channels VALUES($1,'channel')`, f.channel)
	return f
}

func (f readFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresReadOperatingReportRangeLimit(t *testing.T) {
	f := newReadFixture(t)
	f.exec(t, `INSERT INTO request_records(id,tenant_id,model_id,created_at) SELECT '00000000-0000-0000-0000-'||lpad(g::text,12,'0'),$1,$2,$3::timestamptz FROM generate_series(1,5001) g`, f.tenant, f.model, f.filter.From)
	report, err := Read(f.ctx, f.pool, f.filter)
	if !errors.Is(err, ErrRangeTooLarge) || !reflect.DeepEqual(report, Report{}) {
		t.Fatal("5001 requests returned a truncated/partial report", err)
	}
	f.exec(t, `DELETE FROM request_records WHERE id='00000000-0000-0000-0000-000000005001'`)
	report, err = Read(f.ctx, f.pool, f.filter)
	if err != nil || len(report.Requests) != 5000 || report.Summary.RequestCount != 5000 || report.Summary.UnsettledRequests != 5000 || report.Summary.GrossMargin != nil {
		t.Fatal("5000-request positive control rejected or undercounted", err)
	}
}

func TestPostgresReadOperatingReportScopesAndNumeric100(t *testing.T) {
	f := newReadFixture(t)
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	f.exec(t, `INSERT INTO request_records(id,tenant_id,model_id,created_at) VALUES($1,$5,$7,$9),($2,$6,$7,$9),($3,$5,$8,$9),($4,$5,$7,$10)`, ids[0], ids[1], ids[2], ids[3], f.tenant, f.otherTenant, f.model, f.otherModel, f.filter.From, f.filter.To)
	settlement, receipt, lot := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO settlements(id,request_id,tenant_id,charged_amount,refunded_amount) VALUES($1,$2,$3,100,20)`, settlement, ids[0], f.tenant)
	f.exec(t, `INSERT INTO funding_receipts VALUES($1,$2,'paid','CNY',100,'cny-genesis')`, receipt, f.tenant)
	f.exec(t, `INSERT INTO funding_lots VALUES($1,$2,$3,100)`, lot, f.tenant, receipt)
	f.exec(t, `INSERT INTO funding_settlement_lots VALUES($1,$2,0,$3,100)`, f.tenant, settlement, lot)
	f.exec(t, `INSERT INTO funding_consumption_refund_lots VALUES($1,$2,0,$3,20)`, f.tenant, settlement, lot)
	huge := strings.Repeat("9", 100)
	f.exec(t, `INSERT INTO upstream_attempts(id,request_id,channel_id,sequence,cost_status,cost_amount,tenant_id) VALUES($1,$2,$3,1,'invoice',$4::numeric,$5)`, uuid.NewString(), ids[0], f.channel, huge, f.tenant)
	f.exec(t, `INSERT INTO cash_refund_orders(tenant_id,state,approved_at,fiat_currency,paid_fiat_amount) VALUES
 ($1,'completed',$3,'CNY',7),($2,'completed',$3,'CNY',11),($1,'completed',$3,'USD',5),
 ($1,'failed',$3,'CNY',100),($1,'completed',$4,'CNY',100),($1,'completed',$3-interval '1 microsecond','CNY',100)`, f.tenant, f.otherTenant, f.filter.From, f.filter.To)
	report, err := Read(f.ctx, f.pool, f.filter)
	if err != nil || report.Summary.RequestCount != 3 || report.Summary.UnsettledRequests != 2 || report.Summary.Charged != "100" || report.Summary.NetRevenue != "80" || report.Summary.KnownProviderCost != huge || report.Summary.GrossMargin != nil || len(report.Models) != 2 {
		t.Fatal("request/model/NUMERIC100 facts not preserved", err)
	}
	if report.Summary.CashRefunded["CNY"] != "18" || report.Summary.CashRefunded["USD"] != "5" {
		t.Fatalf("completed/window/tenant cash-refund scope mismatch: %+v", report.Summary.CashRefunded)
	}
	filter := f.filter
	filter.TenantID = f.tenant
	filter.ModelID = f.model
	report, err = Read(f.ctx, f.pool, filter)
	if err != nil || len(report.Requests) != 1 || report.Requests[0].RequestID != ids[0] || report.Summary.GrossMargin == nil || report.Summary.PaymentBasis["CNY"] != "80" || report.Summary.ProviderCost["CNY"] != huge || report.Summary.BillConfirmedGrossMargin == nil || report.Summary.CashRefunded["CNY"] != "7" || report.Summary.CashRefunded["USD"] != "5" {
		t.Fatal("tenant/model intersection, independent USD cash refund, or exclusive upper bound incorrect", err)
	}
	f.exec(t, `UPDATE upstream_attempts SET cost_amount=NULL WHERE request_id=$1`, ids[0])
	report, err = Read(f.ctx, f.pool, filter)
	if err != nil || report.Summary.UnknownProviderCosts != 1 || report.Summary.KnownProviderCost != "0" || report.Summary.GrossMargin != nil {
		t.Fatal("NULL known-basis cost became final zero", err)
	}
}

func TestPostgresReadOperatingReportUsesOneReadOnlySnapshot(t *testing.T) {
	f := newReadFixture(t)
	request := uuid.NewString()
	f.exec(t, `INSERT INTO request_records(id,tenant_id,model_id,created_at) VALUES($1,$2,$3,$4)`, request, f.tenant, f.model, f.filter.From)
	settlement, receipt, lot := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO settlements(id,request_id,tenant_id,charged_amount,refunded_amount) VALUES($1,$2,$3,100,0)`, settlement, request, f.tenant)
	f.exec(t, `INSERT INTO funding_receipts VALUES($1,$2,'paid','CNY',100,'cny-genesis')`, receipt, f.tenant)
	f.exec(t, `INSERT INTO funding_lots VALUES($1,$2,$3,100)`, lot, f.tenant, receipt)
	f.exec(t, `INSERT INTO funding_settlement_lots VALUES($1,$2,0,$3,100)`, f.tenant, settlement, lot)
	f.exec(t, `INSERT INTO upstream_attempts(id,request_id,channel_id,sequence,cost_status,cost_amount,tenant_id) VALUES($1,$2,$3,1,'contract',10,$4)`, uuid.NewString(), request, f.channel, f.tenant)
	block, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer block.Rollback(context.Background())
	if _, err = block.Exec(f.ctx, `LOCK TABLE request_records IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		report Report
		err    error
	}
	done := make(chan result, 1)
	go func() { report, err := Read(f.ctx, f.pool, f.filter); done <- result{report, err} }()
	wait, stop := context.WithTimeout(f.ctx, 2*time.Second)
	defer stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		if err = f.pool.QueryRow(wait, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE 'SELECT r.id,%')`, f.app).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("reader ended before blocked cohort snapshot: %v", r.err)
		case <-wait.Done():
			t.Fatal("reader never reached the locked cohort")
		case <-tick.C:
		}
	}
	// Both changes commit after the reader's first RR snapshot, while its
	// cohort is blocked. The read must see both old values, never a mixture.
	f.exec(t, `UPDATE settlements SET charged_amount=200 WHERE request_id=$1`, request)
	f.exec(t, `UPDATE upstream_attempts SET cost_amount=20 WHERE request_id=$1`, request)
	if err = block.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.report.Summary.Charged != "100" || got.report.Summary.KnownProviderCost != "10" {
			t.Fatal("report mixed database snapshots", got.err, fmt.Sprintf("%+v", got.report.Summary))
		}
	case <-f.ctx.Done():
		t.Fatal("report did not finish after releasing cohort lock")
	}
	current, err := Read(f.ctx, f.pool, f.filter)
	if err != nil || current.Summary.Charged != "200" || current.Summary.KnownProviderCost != "20" {
		t.Fatal("new independent snapshot did not see committed changes", err)
	}
}

func TestPostgresReadOperatingReportP0NetRefundInnerAndFrozenFX(t *testing.T) {
	f := newReadFixture(t)
	request, attempt := uuid.NewString(), uuid.NewString()
	settlement, receipt, lot := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO request_records(id,tenant_id,model_id,created_at,billing_status) VALUES($1,$2,$3,$4,'settled')`, request, f.tenant, f.model, f.filter.From)
	f.exec(t, `INSERT INTO settlements(id,request_id,tenant_id,charged_amount,refunded_amount) VALUES($1,$2,$3,10,4)`, settlement, request, f.tenant)
	f.exec(t, `INSERT INTO funding_receipts VALUES($1,$2,'paid','USD',100,'usd-genesis')`, receipt, f.tenant)
	f.exec(t, `INSERT INTO funding_lots VALUES($1,$2,$3,10)`, lot, f.tenant, receipt)
	f.exec(t, `INSERT INTO funding_settlement_lots VALUES($1,$2,0,$3,10)`, f.tenant, settlement, lot)
	f.exec(t, `INSERT INTO funding_consumption_refund_lots(tenant_id,settlement_id,allocation_index,lot_id,amount) VALUES($1,$2,0,$3,1),($1,$2,0,$3,3)`, f.tenant, settlement, lot)
	outerObserved, innerObserved := f.filter.From.Add(time.Hour), f.filter.From.Add(2*time.Hour)
	finalization, costVersion := uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO upstream_attempts(id,request_id,channel_id,sequence,cost_status,cost_amount,tenant_id) VALUES($1,$2,$3,1,'invoice',600,$4)`, attempt, request, f.channel, f.tenant)
	f.exec(t, `INSERT INTO cost_versions VALUES($1,'CNY')`, costVersion)
	f.exec(t, `INSERT INTO attempt_meter_snapshots VALUES($1,$2,$3)`, f.tenant, attempt, costVersion)
	f.exec(t, `INSERT INTO finalization_records(id,tenant_id,request_id,attempt_id,created_at) VALUES($1,$2,$3,$4,$5)`, finalization, f.tenant, request, attempt, outerObserved)
	f.exec(t, `UPDATE upstream_attempts SET latest_cost_finalization_id=$1 WHERE id=$2`, finalization, attempt)
	innerID := uuid.NewString()
	f.exec(t, `INSERT INTO attempt_cost_components(tenant_id,attempt_id,component_id,id,layer,currency,basis,amount_micro,as_of,revision) VALUES
	($1,$2,'inner',$3,'inner','CNY','contract',999,$4,1),($1,$2,'inner',$5,'inner','CNY','invoice',70,$6,2)`, f.tenant, attempt, uuid.NewString(), outerObserved, innerID, innerObserved)
	fxID, offerID := uuid.NewString(), uuid.NewString()
	fx := economics.FxPolicy{ID: fxID, ValuationCurrency: economics.CurrencyUSD, Source: "fixture", QuotedAt: f.filter.From.Add(-time.Hour), ValidUntil: f.filter.From.Add(time.Hour), CreatedBy: "fixture", CreatedAt: f.filter.From.Add(-time.Hour), Rates: []economics.FxRate{{SourceCurrency: economics.CurrencyCNY, TargetCurrency: economics.CurrencyUSD, RateNum: 1, RateDen: 7}}}
	raw, err := json.Marshal(fx)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO request_economics_snapshots VALUES($1,$2,$3,'coding_reasoning',$4,'USD',$5,$6)`, f.tenant, request, offerID, fxID, raw, f.filter.From)
	report, err := Read(f.ctx, f.pool, f.filter)
	if err != nil || len(report.Requests) != 1 || len(report.OfferReports) != 1 {
		t.Fatal("P0 read facts unavailable", err)
	}
	row := report.Requests[0]
	if row.PaymentBasis["USD"] != "60" || row.ProviderCost["CNY"] != "670" || row.AttemptCount != 1 || row.ProvisionalGrossMargin == nil || *row.ProvisionalGrossMargin != "-36" || row.BillConfirmedGrossMargin == nil || *row.BillConfirmedGrossMargin != "-36" || row.GrossMarginCurrency == nil || *row.GrossMarginCurrency != "USD" || row.MarginValuation == nil || *row.MarginValuation != "fx_snapshot" || !reflect.DeepEqual(row.FxPolicyVersions, []string{fxID}) || row.CostAsOf == nil || !row.CostAsOf.Equal(innerObserved) || row.CostRevision != innerID || row.OfferVersionID != offerID || row.TaskClass != "coding_reasoning" {
		t.Fatalf("SQL net refund/latest inner/frozen FX mismatch: %+v", row)
	}

	// Fully refund the original edge, then consume the returned coins in a new
	// request. The lot's current net consumption is six, not sixteen or zero.
	f.exec(t, `UPDATE settlements SET refunded_amount=10 WHERE id=$1`, settlement)
	f.exec(t, `INSERT INTO funding_consumption_refund_lots(tenant_id,settlement_id,allocation_index,lot_id,amount) VALUES($1,$2,0,$3,6)`, f.tenant, settlement, lot)
	second, secondSettlement, cacheFinalization := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO request_records(id,tenant_id,model_id,created_at,billing_status) VALUES($1,$2,$3,$4,'settled')`, second, f.tenant, f.model, f.filter.From.Add(time.Second))
	f.exec(t, `INSERT INTO settlements(id,request_id,tenant_id,charged_amount,refunded_amount) VALUES($1,$2,$3,6,0)`, secondSettlement, second, f.tenant)
	f.exec(t, `INSERT INTO funding_settlement_lots VALUES($1,$2,0,$3,6)`, f.tenant, secondSettlement, lot)
	f.exec(t, `INSERT INTO finalization_records(id,tenant_id,request_id,source) VALUES($1,$2,$3,'cache')`, cacheFinalization, f.tenant, second)
	f.exec(t, `UPDATE request_records SET latest_finalization_id=$1 WHERE id=$2`, cacheFinalization, second)
	report, err = Read(f.ctx, f.pool, f.filter)
	if err != nil || report.Summary.RequestCount != 2 || report.Summary.PaymentBasis["USD"] != "60" || report.Summary.ProviderCost["CNY"] != "670" || report.Summary.ProvisionalGrossMargin == nil || *report.Summary.ProvisionalGrossMargin != "-36" || len(report.OfferReports) != 1 {
		t.Fatalf("full refund/reconsumption read model mismatch: %+v, %v", report.Summary, err)
	}
	for _, row := range report.Requests {
		if row.RequestID == request && row.PaymentBasis["USD"] != "0" || row.RequestID == second && row.PaymentBasis["USD"] != "60" {
			t.Fatalf("original and re-consumption net bases are wrong: %+v", row)
		}
	}
}

func TestPostgresReadOperatingReportIncludesMediaSupplierCostOnce(t *testing.T) {
	f := newReadFixture(t)
	first, second, third := uuid.NewString(), uuid.NewString(), uuid.NewString()
	profile := "11111111-1111-4111-8111-111111111111"
	at := f.filter.From.Add(time.Hour)
	f.exec(t, `INSERT INTO media_requests(tenant_id,id,model_key,operation,profile_version,state,created_at)
VALUES($1,$2,'mock-tts','tts',$5,'settled',$4),($1,$3,'mock-image-generation','image_generation',$5,'released',$4 + interval '1 second'),($1,$6,'mock-video-generation','video_generation',$5,'pending',$4 + interval '2 seconds')`, f.tenant, first, second, at, profile, third)
	mediaSettlement, receipt, lot := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO media_settlements(tenant_id,id,request_id,charged_amount,raw_amount,supplier_cost_status,supplier_cost_amount,status)
VALUES($1,$2,$3,125,999,'known',60,'settled'),($1,$4,$5,0,0,'unknown',NULL,'released')`, f.tenant, mediaSettlement, first, uuid.NewString(), second)
	f.exec(t, `INSERT INTO funding_receipts VALUES($1,$2,'paid','CNY',125,'cny-genesis')`, receipt, f.tenant)
	f.exec(t, `INSERT INTO funding_lots VALUES($1,$2,$3,125)`, lot, f.tenant, receipt)
	f.exec(t, `INSERT INTO media_settlement_lots VALUES($1,$2,0,$3,125)`, f.tenant, mediaSettlement, lot)
	f.exec(t, `INSERT INTO media_attempts(tenant_id,id,request_id,delivery_state,created_at)
VALUES($1,$2,$3,'completed',$4),($1,$5,$3,'failed',$4 + interval '1 microsecond'),($1,$6,$7,'unknown',$4),($1,$8,$9,'sending',$4)`, f.tenant, uuid.NewString(), first, at, uuid.NewString(), uuid.NewString(), second, uuid.NewString(), third)

	report, err := Read(f.ctx, f.pool, f.filter)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.RequestCount != 3 || report.Summary.AttemptCount != 4 || report.Summary.Charged != "125" || report.Summary.Refunded != "0" || report.Summary.KnownProviderCost != "120" || report.Summary.UnknownProviderCosts != 2 || report.Summary.GrossMargin != nil {
		t.Fatalf("media income/count/cost union changed 026 semantics: %+v", report.Summary)
	}
	if len(report.Requests) != 3 || report.Requests[0].RequestID != third || report.Requests[0].Protocol != "video_generation" || report.Requests[0].ExecutionKind != "asynchronous" {
		t.Fatalf("media request facts missing or misclassified: %+v", report.Requests)
	}
	if len(report.Channels) != 3 {
		t.Fatalf("media attempts did not stay in operation buckets: %+v", report.Channels)
	}
	for _, channel := range report.Channels {
		if channel.ChannelID == "media:tts" && (channel.KnownProviderCost != "120" || channel.UnknownProviderCosts != 0) {
			t.Fatalf("known media supplier cost was not retained: %+v", channel)
		}
		if channel.ChannelID != "media:tts" && channel.UnknownProviderCosts == 0 {
			t.Fatalf("media raw amount was treated as provider cost: %+v", channel)
		}
	}

	filter := f.filter
	filter.Protocol = "tts"
	tts, err := Read(f.ctx, f.pool, filter)
	if err != nil || tts.Summary.RequestCount != 1 || tts.Summary.Charged != "125" || tts.Summary.AttemptCount != 2 {
		t.Fatalf("media operation filter changed request/attempt scope: %+v, %v", tts.Summary, err)
	}
	filter.ModelID = "mock-tts"
	model, err := Read(f.ctx, f.pool, filter)
	if err != nil || model.Summary.RequestCount != 1 || model.Requests[0].ModelID != "mock-tts" {
		t.Fatalf("media model selector did not stay on the media cohort: %+v, %v", model.Summary, err)
	}
}
