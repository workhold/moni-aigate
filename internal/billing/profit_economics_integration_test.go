package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/economics"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

type profitOfferFixture struct {
	offerID, policyID, bindingID, physicalID string
	revision                                 int64
	publishErr                               error
}

func publishProfitOffer(t *testing.T, f *fixture) profitOfferFixture {
	return publishProfitOfferInCurrency(t, f, "CNY")
}

func publishProfitOfferInCurrency(t *testing.T, f *fixture, valuationCurrency string) profitOfferFixture {
	return publishProfitOfferConfigured(t, f, valuationCurrency, false)
}

func publishProfitOfferConfigured(t *testing.T, f *fixture, valuationCurrency string, invalidTask bool) profitOfferFixture {
	t.Helper()
	var physicalID, priceID string
	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM physical_models WHERE logical_model_id=$1 AND upstream_model='upstream'`, f.model).Scan(&physicalID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM price_versions WHERE model_id=$1 ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, f.model).Scan(&priceID); err != nil {
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
		{`INSERT INTO fx_policy_versions(id,valuation_currency,source,quoted_at,valid_until,created_by) VALUES($1,$5,'fixture:conservative',$2,$3,$4)`, []any{ids["fx"], now.Add(-time.Hour), now.Add(24 * time.Hour), f.admin, valuationCurrency}},
		{`INSERT INTO task_policy_versions(id,logical_model_id,taxonomy_version,default_task_class,allowed_hints,class_candidates,unknown_action,created_by) VALUES($1,$2,'task-v1','coding_non_reasoning','{}',$3,'deny',$4)`, []any{ids["task"], f.model, map[string][]string{"coding_non_reasoning": {physicalID}, "unknown": {}}, f.admin}},
		{`INSERT INTO quality_policy_versions(id,logical_model_id,suite_version,metric,direction,threshold_micro,min_sample_count,min_confidence_lower_bound_micro,max_age_seconds,created_by) VALUES($1,$2,'suite-v1','answer_score','higher_is_better',800000,10,800000,86400,$3)`, []any{ids["quality"], f.model, f.admin}},
		{`INSERT INTO execution_policy_versions(id,created_by) VALUES($1,$2)`, []any{ids["execution"], f.admin}},
		{`INSERT INTO economics_policy_versions(id,valuation_currency,fx_policy_version_id,min_margin_rate,created_by) VALUES($1,$4,$2,0,$3)`, []any{ids["economics"], ids["fx"], f.admin, valuationCurrency}},
		{`INSERT INTO service_offer_documents(id,kind,body,created_by) VALUES($1,'disclosure','{"text":"test-only dynamic offer"}',$2)`, []any{ids["disclosure"], f.admin}},
		{`INSERT INTO service_offer_documents(id,kind,body,created_by) VALUES($1,'transparency','{"actual_model":"platform"}',$2)`, []any{ids["transparency"], f.admin}},
		{`INSERT INTO quality_evidence(id,physical_model_id,channel_id,channel_version_id,execution_profile_version,deployment_revision,model_revision,task_class,suite_version,rubric_version,judge_version,metric,protocol,tools_scope,language_scope,reasoning_scope,score_micro,uncertainty_micro,confidence_lower_bound_micro,sample_count,observed_at,valid_until,evidence_ref,approved_by,approved_at) VALUES($1,$2,$3,$4,$5,'deployment-v1','model-v1','coding_non_reasoning','suite-v1','rubric-v1','judge-v1','answer_score','openai_chat','none','any','none',900000,0,900000,100,$6,$7,'evidence:profit-p0',$8,$6)`, []any{ids["evidence"], physicalID, f.channel, f.version, fixtureProfileVersion, now.Add(-time.Minute), now.Add(time.Hour), f.admin}},
	}
	if invalidTask {
		statements[1].sql = `INSERT INTO task_policy_versions(id,logical_model_id,taxonomy_version,default_task_class,allowed_hints,class_candidates,unknown_action,created_by) VALUES($1,$2,'task-v1','coding_non_reasoning','{"tool_use"}',$3,'deny',$4)`
	}
	for _, statement := range statements {
		if _, err := f.pool.Exec(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	input := catalog.ServiceOfferInput{Kind: "same_model_multi_provider", PriceVersionID: priceID, DisclosureVersionID: ids["disclosure"], TaskPolicyVersionID: ids["task"], QualityPolicyVersionID: ids["quality"], EconomicsVersionID: ids["economics"], TransparencyPolicyVersionID: ids["transparency"], ExecutionPolicyVersionID: ids["execution"], ApprovalEvidenceID: "approval:profit-p0"}
	policy := routing.DefaultDynamicPolicy()
	policy.UnknownCostPolicy = "deny"
	revision, offerID, err := catalog.New(f.pool, nil).PublishServiceOffer(f.ctx, f.admin, f.model, fixtureRouteRevision, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "upstream", Weight: 1, MaxInflight: 10, QualityEvidenceID: ids["evidence"]}}, policy, routing.DefaultBreakerConfig(), input)
	var result profitOfferFixture
	if err != nil {
		result.publishErr = err
		if invalidTask {
			return result
		}
		t.Fatal(err)
	}
	result.offerID, result.revision = offerID, revision
	if err = f.pool.QueryRow(f.ctx, `SELECT p.id,b.id,b.physical_model_id FROM service_offer_versions o JOIN route_policy_versions p ON p.id=o.route_policy_version_id JOIN model_bindings b ON b.route_policy_version_id=p.id WHERE o.id=$1`, offerID).Scan(&result.policyID, &result.bindingID, &result.physicalID); err != nil {
		t.Fatal(err)
	}
	low := int64(100000)
	costVersionID := meterquote.PublishCost(t, f.ctx, f.pool, meterquote.CostPrice{ChannelID: f.channel, UpstreamModel: "upstream", ModelID: f.model, Actor: f.admin, Basis: "contract", Currency: valuationCurrency, InputRate: &low, OutputRate: &low, EffectiveAt: now})
	var storedCurrency string
	if err = f.pool.QueryRow(f.ctx, `SELECT currency FROM cost_versions WHERE id=$1`, costVersionID).Scan(&storedCurrency); err != nil || storedCurrency != valuationCurrency {
		t.Fatalf("stored cost currency=%s want=%s err=%v", storedCurrency, valuationCurrency, err)
	}
	return result
}

func profitAttemptInput(t *testing.T, f *fixture, reservation Reservation, offer profitOfferFixture) AttemptInput {
	t.Helper()
	cost, err := scanCostQuote(f.pool.QueryRow(f.ctx, currentAttemptCostSQL, f.channel, "upstream", reservation.RequestID), f.channel, "upstream")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := conservativeCostVector(cost, 100, 20)
	if err != nil {
		t.Fatal(err)
	}
	upper := expected[economics.Currency(cost.Currency())].String()
	candidate := routing.CandidateDecision{BindingID: offer.bindingID, ChannelID: f.channel, ChannelVersionID: f.version, PhysicalModelID: offer.physicalID, UpstreamModel: "upstream", Weight: 1, Reason: "selected", CostSnapshot: routing.CostSnapshot{VersionID: cost.VersionID(), Basis: cost.Basis(), Currency: cost.Currency(), UpperAmount: &upper, Rates: []routing.CostRateSnapshot{}}}
	raw, _ := json.Marshal([]routing.CandidateDecision{candidate})
	digest := sha256.Sum256(raw)
	decision := &routing.RouteDecision{ID: uuid.NewString(), TenantID: f.tenant, RequestID: reservation.RequestID, Sequence: 1, PolicyVersionID: offer.policyID, SelectedBindingID: offer.bindingID, SelectedPhysicalModelID: offer.physicalID, Degradation: "none", AlgorithmVersion: routing.DynamicAlgorithmVersion, Candidates: []routing.CandidateDecision{candidate}, CandidatesDigest: hex.EncodeToString(digest[:])}
	return AttemptInput{TenantID: f.tenant, RequestID: reservation.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", BindingID: offer.bindingID, PhysicalModelID: offer.physicalID, PolicyVersionID: offer.policyID, Sequence: 1, RouteDecision: decision, CostQuote: &cost}
}

func TestProfitOfferRejectsSnapshotPolicyMismatch(t *testing.T) {
	f := integration(t)
	offer := publishProfitOffer(t, f)
	_, err := f.pool.Exec(f.ctx, `CREATE FUNCTION aaa_profit_snapshot_forgery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.snapshot:=jsonb_set(NEW.snapshot,'{min_margin_rate}','10000',false); NEW.snapshot_canonical:=convert_to(NEW.snapshot::text,'UTF8'); NEW.snapshot_digest:=sha256(NEW.snapshot_canonical); RETURN NEW; END $$; CREATE TRIGGER aaa_profit_snapshot_forgery BEFORE INSERT ON request_economics_snapshots FOR EACH ROW EXECUTE FUNCTION aaa_profit_snapshot_forgery()`)
	if err != nil {
		t.Fatal(err)
	}
	defer f.pool.Exec(context.Background(), `DROP TRIGGER aaa_profit_snapshot_forgery ON request_economics_snapshots; DROP FUNCTION aaa_profit_snapshot_forgery()`)
	in := f.input()
	in.RouteRevision = offer.revision
	if _, err = f.store.Reserve(f.ctx, in); err == nil || !strings.Contains(err.Error(), "snapshot mismatch") {
		t.Fatalf("digest-valid policy forgery was accepted: %v", err)
	}
	var exists bool
	if err = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM request_records WHERE id=$1)`, in.RequestID).Scan(&exists); err != nil || exists {
		t.Fatalf("forged request was persisted: exists=%v err=%v", exists, err)
	}
}

func TestProfitOfferRejectsNullSnapshotPolicy(t *testing.T) {
	f := integration(t)
	offer := publishProfitOffer(t, f)
	_, err := f.pool.Exec(f.ctx, `CREATE FUNCTION aaa_profit_snapshot_null() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.snapshot:=jsonb_set(NEW.snapshot,'{min_margin_rate}','null',false); NEW.snapshot_canonical:=convert_to(NEW.snapshot::text,'UTF8'); NEW.snapshot_digest:=sha256(NEW.snapshot_canonical); RETURN NEW; END $$; CREATE TRIGGER aaa_profit_snapshot_null BEFORE INSERT ON request_economics_snapshots FOR EACH ROW EXECUTE FUNCTION aaa_profit_snapshot_null()`)
	if err != nil {
		t.Fatal(err)
	}
	defer f.pool.Exec(context.Background(), `DROP TRIGGER aaa_profit_snapshot_null ON request_economics_snapshots; DROP FUNCTION aaa_profit_snapshot_null()`)
	in := f.input()
	in.RouteRevision = offer.revision
	if _, err = f.store.Reserve(f.ctx, in); err == nil || !strings.Contains(err.Error(), "snapshot mismatch") {
		t.Fatalf("JSON null margin policy was accepted: %v", err)
	}
}

func TestProfitOfferWithdrawalSynchronizesReserve(t *testing.T) {
	f := integration(t)
	offer := publishProfitOffer(t, f)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	config, err := loadOfferEconomicsTx(f.ctx, tx, f.model, offer.revision)
	if err != nil || config == nil {
		t.Fatalf("offer config=%v err=%v", config, err)
	}
	withdrawn := make(chan error, 1)
	go func() {
		_, updateErr := f.pool.Exec(context.Background(), `UPDATE service_offer_states SET state='withdrawn',revision=revision+1,updated_by=$1 WHERE offer_version_id=$2`, f.admin, offer.offerID)
		withdrawn <- updateErr
	}()
	select {
	case err = <-withdrawn:
		t.Fatalf("withdrawal did not wait for Reserve state lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-withdrawn:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("withdrawal remained blocked after reservation commit")
	}
	in := f.input()
	in.RouteRevision = offer.revision
	if _, err = f.store.Reserve(f.ctx, in); err == nil {
		t.Fatal("withdrawn offer admitted a new request")
	}
}

func TestProfitOfferRejectsInvalidTaskPolicy(t *testing.T) {
	f := integration(t)
	offer := publishProfitOfferConfigured(t, f, "CNY", true)
	if offer.publishErr == nil {
		t.Fatal("task hint without a candidate mapping was published")
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM service_offer_versions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid offer was persisted: count=%d err=%v", count, err)
	}
}

func TestProfitOfferFixedFullReservation(t *testing.T) {
	f := integration(t)
	offer := publishProfitOffer(t, f)
	in := f.input()
	in.RouteRevision = offer.revision
	reservation, err := f.store.Reserve(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.Economics == nil || reservation.Economics.SettlementMode() != "fixed_full_reservation" || reservation.Economics.MaxOuterAttempts() != 1 {
		t.Fatal("fixed-full execution snapshot was not frozen")
	}
	if got := reservation.Economics.PaymentBasisMicro()["CNY"]; got != 120 {
		t.Fatalf("payment basis=%v want 120", got)
	}
	attempt, err := f.store.RecordAttempt(f.ctx, profitAttemptInput(t, f, reservation, offer))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: attempt.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: reservation.RequestID, AttemptID: attempt.ID, Source: "gateway", SourceEventID: uuid.NewString(), Usage: Usage{Status: "confirmed_final", InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true}); err != nil {
		t.Fatal(err)
	}
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: reservation.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	if settled.ChargedAmount != reservation.Amount || settled.Reason != "charged_fixed_full_reservation" {
		t.Fatalf("fixed full settlement=%+v reservation=%d", settled, reservation.Amount)
	}
	again, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: reservation.RequestID})
	if err != nil || again.ID != settled.ID {
		t.Fatalf("settlement was not unique: %+v %v", again, err)
	}
	_, err = f.pool.Exec(f.ctx, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) VALUES($1,$2,$3,2,$4,$5,$6,'upstream')`, uuid.NewString(), f.tenant, reservation.RequestID, f.channel, f.version, f.credential)
	if err == nil || !strings.Contains(err.Error(), "outer attempt budget") {
		t.Fatalf("database accepted a second outer attempt: %v", err)
	}
}

func TestProfitOfferUSDOriginalCurrency(t *testing.T) {
	f := integration(t)
	offer := publishProfitOfferInCurrency(t, f, "USD")
	tenant, key := uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'usd-profit')`, tenant); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, []any{tenant, f.user}},
		{`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, []any{tenant, f.model}},
		{`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'usd','key',$5,$4,1000)`, []any{key, tenant, f.user, []string{f.model}, key}},
		{`WITH n AS (SELECT clock_timestamp() AS at) INSERT INTO coin_rate_versions(id,fiat_currency,rate_num,rate_den,effective_at,reason,maker,approver,single_operator,created_at) SELECT $1,'USD',7,10,n.at,'P0 USD fixture',$2,$3,false,n.at FROM n`, []any{uuid.NewString(), f.operator, f.approver}},
	} {
		if _, err := f.pool.Exec(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	op := uuid.NewString()
	order, err := f.store.CreateCredit(f.ctx, CreditInput{TenantID: tenant, ActorID: f.operator, BusinessRef: op, ProofRef: "receipt:usd-p0", Amount: 1000, BasisKind: "paid", FiatCurrency: "USD", FiatAmount: 1000, IdempotencyKey: op, RequestHMAC: op})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: tenant, OrderID: order.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	in := f.input()
	in.RouteRevision = offer.revision
	in.Principal.TenantID, in.Principal.KeyID = tenant, key
	reservation, err := f.store.Reserve(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := reservation.Economics.PaymentBasisMicro()["USD"]; got != 120 || reservation.Economics.ValuationCurrency() != "USD" {
		t.Fatalf("USD snapshot basis=%v valuation=%s", got, reservation.Economics.ValuationCurrency())
	}
	attemptInput := profitAttemptInput(t, f, reservation, offer)
	attemptInput.TenantID = tenant
	if attemptInput.CostQuote.Currency() != "USD" || attemptInput.RouteDecision.Candidates[0].CostSnapshot.Currency != "USD" {
		rows, _ := f.pool.Query(f.ctx, `SELECT id,currency,effective_at,created_at FROM cost_versions WHERE channel_id=$1 ORDER BY effective_at,created_at`, f.channel)
		for rows.Next() {
			var id, currency string
			var effective, created time.Time
			_ = rows.Scan(&id, &currency, &effective, &created)
			t.Logf("cost id=%s currency=%s effective=%s created=%s", id, currency, effective, created)
		}
		rows.Close()
		t.Fatalf("USD quote was not frozen: %+v", attemptInput.RouteDecision.Candidates[0].CostSnapshot)
	}
	attempt, err := f.store.RecordAttempt(f.ctx, attemptInput)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: tenant, AttemptID: attempt.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: tenant, RequestID: reservation.RequestID, AttemptID: attempt.ID, Source: "gateway", SourceEventID: uuid.NewString(), Usage: Usage{Status: "confirmed_final", InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true}); err != nil {
		t.Fatal(err)
	}
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: tenant, RequestID: reservation.RequestID})
	if err != nil || settled.ChargedAmount != reservation.Amount {
		t.Fatalf("USD fixed full settlement=%+v err=%v", settled, err)
	}
	var currency string
	if err = f.pool.QueryRow(f.ctx, `SELECT c.currency FROM upstream_attempts a JOIN attempt_meter_snapshots s ON s.attempt_id=a.id JOIN cost_versions c ON c.id=s.cost_version_id WHERE a.id=$1`, attempt.ID).Scan(&currency); err != nil || currency != "USD" {
		t.Fatalf("supplier currency=%s err=%v", currency, err)
	}
}

func TestProfitOfferReleaseAndQualityWithdrawal(t *testing.T) {
	f := integration(t)
	offer := publishProfitOffer(t, f)
	in := f.input()
	in.RouteRevision = offer.revision
	reservation, err := f.store.Reserve(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := f.store.RecordAttempt(f.ctx, profitAttemptInput(t, f, reservation, offer))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO quality_evidence_withdrawals(evidence_id,withdrawn_by,reason) SELECT quality_evidence_id,$1,'withdraw test' FROM model_bindings WHERE id=$2`, f.admin, offer.bindingID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: attempt.ID}); err == nil {
		t.Fatal("withdrawn quality evidence authorized dispatch")
	}
	if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: reservation.RequestID, Reason: "no_delivery"}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = f.pool.QueryRow(f.ctx, `SELECT status FROM reservations WHERE id=$1`, reservation.ID).Scan(&state); err != nil || state != "released" {
		t.Fatalf("reservation state=%s err=%v", state, err)
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE service_offer_states SET state='withdrawn',revision=revision+1,updated_by=$1 WHERE offer_version_id=$2`, f.admin, offer.offerID); err != nil {
		t.Fatal(err)
	}
	next := f.input()
	next.RouteRevision = offer.revision
	if _, err = f.store.Reserve(f.ctx, next); err == nil {
		t.Fatal("withdrawn offer accepted a new reservation")
	}
}
