package server

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/reporting"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func publishGatewayProfitOffer(t *testing.T, f *gatewayFixture) int64 {
	t.Helper()
	ctx := context.Background()
	var operator, channel, version, physical, price string
	if err := f.pool.QueryRow(ctx, `SELECT u.id FROM users u WHERE u.platform_role='finance_operator'`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT b.channel_id,b.channel_version_id,b.physical_model_id FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id WHERE p.model_id=$1 AND p.revision=1`, f.model).Scan(&channel, &version, &physical); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT id FROM price_versions WHERE model_id=$1 ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, f.model).Scan(&price); err != nil {
		t.Fatal(err)
	}
	var profile, policyID string
	if err := f.pool.QueryRow(ctx, `SELECT profile_version,id FROM route_policy_versions WHERE model_id=$1 AND revision=1`, f.model).Scan(&profile, &policyID); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, name := range []string{"fx", "task", "quality", "execution", "economics", "disclosure", "transparency", "evidence"} {
		ids[name] = uuid.NewString()
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO fx_policy_versions(id,valuation_currency,source,quoted_at,valid_until,created_by) VALUES($1,'CNY','gateway-fixture',$2,$3,$4)`, []any{ids["fx"], now.Add(-time.Hour), now.Add(time.Hour), operator}},
		{`INSERT INTO task_policy_versions(id,logical_model_id,taxonomy_version,default_task_class,class_candidates,unknown_action,created_by) VALUES($1,$2,'task-v1','coding_non_reasoning',$3,'deny',$4)`, []any{ids["task"], f.model, map[string][]string{"coding_non_reasoning": {physical}, "unknown": {}}, operator}},
		{`INSERT INTO quality_policy_versions(id,logical_model_id,suite_version,metric,direction,threshold_micro,min_sample_count,min_confidence_lower_bound_micro,max_age_seconds,created_by) VALUES($1,$2,'suite-v1','answer_score','higher_is_better',800000,10,800000,86400,$3)`, []any{ids["quality"], f.model, operator}},
		{`INSERT INTO execution_policy_versions(id,created_by) VALUES($1,$2)`, []any{ids["execution"], operator}},
		{`INSERT INTO economics_policy_versions(id,valuation_currency,fx_policy_version_id,min_margin_rate,created_by) VALUES($1,'CNY',$2,0,$3)`, []any{ids["economics"], ids["fx"], operator}},
		{`INSERT INTO service_offer_documents(id,kind,body,created_by) VALUES($1,'disclosure','{"text":"test-only dynamic offer"}',$2)`, []any{ids["disclosure"], operator}},
		{`INSERT INTO service_offer_documents(id,kind,body,created_by) VALUES($1,'transparency','{"actual_model":"platform"}',$2)`, []any{ids["transparency"], operator}},
		{`INSERT INTO quality_evidence(id,physical_model_id,channel_id,channel_version_id,execution_profile_version,deployment_revision,model_revision,task_class,suite_version,rubric_version,judge_version,metric,protocol,tools_scope,language_scope,reasoning_scope,score_micro,uncertainty_micro,confidence_lower_bound_micro,sample_count,observed_at,valid_until,evidence_ref,approved_by,approved_at) VALUES($1,$2,$3,$4,$5,'deployment-v1','model-v1','coding_non_reasoning','suite-v1','rubric-v1','judge-v1','answer_score','openai_chat','none','any','none',900000,0,900000,100,$6,$7,'evidence:gateway-p0',$8,$6)`, []any{ids["evidence"], physical, channel, version, profile, now.Add(-time.Minute), now.Add(time.Hour), operator}},
	}
	for _, statement := range statements {
		if _, err := f.pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	offer := catalog.ServiceOfferInput{Kind: "same_model_multi_provider", PriceVersionID: price, DisclosureVersionID: ids["disclosure"], TaskPolicyVersionID: ids["task"], QualityPolicyVersionID: ids["quality"], EconomicsVersionID: ids["economics"], TransparencyPolicyVersionID: ids["transparency"], ExecutionPolicyVersionID: ids["execution"], ApprovalEvidenceID: "approval:gateway-p0"}
	policy := routing.DefaultDynamicPolicy()
	policy.UnknownCostPolicy = "deny"
	revision, _, err := f.server.Catalog.PublishServiceOffer(ctx, operator, f.model, 1, []catalog.BindingInput{{ChannelID: channel, UpstreamModel: "provider-model", Weight: 1, MaxInflight: 4, QualityEvidenceID: ids["evidence"]}}, policy, routing.DefaultBreakerConfig(), offer)
	if err != nil {
		t.Fatal(err)
	}
	low := int64(100000)
	meterquote.PublishCost(t, ctx, f.pool, meterquote.CostPrice{ChannelID: channel, UpstreamModel: "provider-model", ModelID: f.model, Actor: operator, Basis: "contract", InputRate: &low, OutputRate: &low, EffectiveAt: now})
	return revision
}

func TestProfitOfferActualGatewayFixedFull(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	publishGatewayProfitOffer(t, f)
	response := f.request(t, context.Background(), false, false)
	if response.StatusCode != 200 {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("profit offer status=%d body=%s", response.StatusCode, body)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if f.posts.Load() != 1 {
		t.Fatalf("provider posts=%d", f.posts.Load())
	}
	var charged, reserved int64
	var requestID, settlementID, reason, mode string
	var attempts int
	var created time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT q.id,s.id,q.created_at,s.charged_amount,r.amount,s.reason,es.settlement_mode,(SELECT count(*) FROM upstream_attempts a WHERE a.request_id=r.request_id) FROM request_records q JOIN reservations r ON r.request_id=q.id JOIN settlements s ON s.request_id=q.id JOIN request_economics_snapshots es ON es.request_id=q.id WHERE q.tenant_id=$1 AND q.model_id=$2 ORDER BY q.created_at DESC LIMIT 1`, f.tenant, f.model).Scan(&requestID, &settlementID, &created, &charged, &reserved, &reason, &mode, &attempts); err != nil {
		t.Fatal(err)
	}
	if charged != reserved || reason != "charged_fixed_full_reservation" || mode != "fixed_full_reservation" || attempts != 1 {
		t.Fatalf("charged=%d reserved=%d reason=%s mode=%s attempts=%d", charged, reserved, reason, mode, attempts)
	}
	readReport := func() reporting.Report {
		report, err := reporting.Read(context.Background(), f.pool, reporting.Filter{From: created.Add(-time.Second), To: created.Add(time.Hour), TenantID: f.tenant})
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	initial := readReport()
	initialBasis := strconv.FormatInt(reserved, 10)
	initialMargin := strconv.FormatInt(reserved-2, 10)
	if initial.Summary.PaymentBasis["CNY"] != initialBasis || initial.Summary.ProviderCost["CNY"] != "2" || initial.Summary.ProvisionalGrossMargin == nil || *initial.Summary.ProvisionalGrossMargin != initialMargin || initial.Summary.BillConfirmedGrossMargin != nil {
		t.Fatalf("actual offer report=%+v", initial.Summary)
	}
	var operator, approver string
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT id FROM users WHERE platform_role='finance_operator'),(SELECT id FROM users WHERE platform_role='finance_approver')`).Scan(&operator, &approver); err != nil {
		t.Fatal(err)
	}
	ref := uuid.NewString()
	refund, err := f.server.Billing.CreateRefund(context.Background(), billing.RefundInput{TenantID: f.tenant, SettlementID: settlementID, ActorID: operator, Amount: 20, BusinessRef: ref, Reason: "P0 actual refund", IdempotencyKey: ref, RequestHMAC: ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Billing.ApproveRefund(context.Background(), billing.ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: approver}); err != nil {
		t.Fatal(err)
	}
	second := f.request(t, context.Background(), false, false)
	if second.StatusCode != 200 {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("second offer status=%d body=%s", second.StatusCode, body)
	}
	_ = second.Body.Close()
	if f.posts.Load() != 2 {
		t.Fatalf("provider posts=%d", f.posts.Load())
	}
	combined := readReport()
	combinedBasis := strconv.FormatInt(2*reserved-20, 10)
	combinedMargin := strconv.FormatInt(2*reserved-24, 10)
	if combined.Summary.RequestCount != 2 || combined.Summary.PaymentBasis["CNY"] != combinedBasis || combined.Summary.ProviderCost["CNY"] != "4" || combined.Summary.ProvisionalGrossMargin == nil || *combined.Summary.ProvisionalGrossMargin != combinedMargin || len(combined.OfferReports) != 1 {
		t.Fatalf("refund/reconsumption double counted cash: %+v", combined.Summary)
	}
	for _, row := range combined.Requests {
		if row.RequestID == requestID && row.PaymentBasis["CNY"] != strconv.FormatInt(reserved-20, 10) {
			t.Fatalf("original request net basis=%+v", row.Totals)
		}
	}
}
