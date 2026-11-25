package billing

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/economics"
	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/routecost"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

type offerEconomicsConfig struct {
	offerID, priceVersionID string
	qualityID               string
	execution               economics.ExecutionPolicy
	task                    economics.TaskPolicy
	policy                  economics.EconomicsPolicy
	fx                      economics.FxPolicy
}

func loadOfferEconomicsTx(ctx context.Context, tx pgx.Tx, model string, revision int64) (*offerEconomicsConfig, error) {
	// The pre-033 migration fixture calls the current writer before installing
	// 015/016. Serving still requires VerifyCurrent, so this is not a production
	// fallback or an incomplete-schema acceptance path.
	var installed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname='service_offer_versions')`).Scan(&installed); err != nil {
		return nil, err
	}
	if !installed {
		return nil, nil
	}
	var cfg offerEconomicsConfig
	err := tx.QueryRow(ctx, `SELECT o.id,o.price_version_id FROM service_offer_versions o JOIN service_offer_states st ON st.offer_version_id=o.id AND st.state='test_active' JOIN route_policy_versions p ON p.id=o.route_policy_version_id WHERE p.model_id=$1 AND p.revision=$2 FOR SHARE OF st`, model, revision).Scan(&cfg.offerID, &cfg.priceVersionID)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if checkErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_offer_versions o JOIN route_policy_versions p ON p.id=o.route_policy_version_id WHERE p.model_id=$1 AND p.revision=$2)`, model, revision).Scan(&exists); checkErr != nil {
			return nil, checkErr
		}
		if exists {
			return nil, fail("offer_inactive", "service offer is not active for new requests")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var taskID, economicsID, executionID string
	if err = tx.QueryRow(ctx, `SELECT task_policy_version_id,quality_policy_version_id,economics_version_id,execution_policy_version_id FROM service_offer_versions WHERE id=$1 AND logical_model_id=$2`, cfg.offerID, model).Scan(&taskID, &cfg.qualityID, &economicsID, &executionID); err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `SELECT id,max_outer_attempts,inner_max_attempts,max_relay_depth,settlement_mode,cache_mode,cumulative_cost_policy,unknown_cost_policy,created_by,created_at FROM execution_policy_versions WHERE id=$1`, executionID).Scan(&cfg.execution.ID, &cfg.execution.MaxOuterAttempts, &cfg.execution.InnerMaxAttempts, &cfg.execution.MaxRelayDepth, &cfg.execution.SettlementMode, &cfg.execution.CacheMode, &cfg.execution.CumulativeCostPolicy, &cfg.execution.UnknownCostPolicy, &cfg.execution.CreatedBy, &cfg.execution.CreatedAt); err != nil {
		return nil, err
	}
	if cfg.execution.Validate() != nil {
		return nil, fail("invalid_execution_policy", "execution policy is not eligible")
	}
	var hints []string
	var candidates []byte
	if err = tx.QueryRow(ctx, `SELECT id,logical_model_id,taxonomy_version,default_task_class,allowed_hints,class_candidates,unknown_action,classifier_version,shape_digest_algorithm,created_by,created_at FROM task_policy_versions WHERE id=$1 AND logical_model_id=$2`, taskID, model).Scan(&cfg.task.ID, &cfg.task.LogicalModelID, &cfg.task.TaxonomyVersion, &cfg.task.DefaultTaskClass, &hints, &candidates, &cfg.task.UnknownAction, &cfg.task.ClassifierVersion, &cfg.task.ShapeDigestAlgorithm, &cfg.task.CreatedBy, &cfg.task.CreatedAt); err != nil {
		return nil, err
	}
	for _, hint := range hints {
		cfg.task.AllowedHints = append(cfg.task.AllowedHints, economics.TaskClass(hint))
	}
	var classMap map[string][]string
	if json.Unmarshal(candidates, &classMap) != nil {
		return nil, fail("invalid_task_policy", "task candidate mapping is invalid")
	}
	cfg.task.ClassCandidates = map[economics.TaskClass][]string{}
	for class, ids := range classMap {
		cfg.task.ClassCandidates[economics.TaskClass(class)] = ids
	}
	if cfg.task.Validate() != nil {
		return nil, fail("invalid_task_policy", "task policy is not eligible")
	}
	var maxCost []byte
	if err = tx.QueryRow(ctx, `SELECT id,valuation_currency,fx_policy_version_id,min_margin_rate,max_cumulative_cost_micro,created_by,created_at FROM economics_policy_versions WHERE id=$1`, economicsID).Scan(&cfg.policy.ID, &cfg.policy.ValuationCurrency, &cfg.policy.FxPolicyVersionID, &cfg.policy.MinMarginRate, &maxCost, &cfg.policy.CreatedBy, &cfg.policy.CreatedAt); err != nil {
		return nil, err
	}
	if maxCost != nil {
		var vector economics.MoneyVector
		if json.Unmarshal(maxCost, &vector) != nil {
			return nil, fail("invalid_economics_policy", "cumulative cost limit is invalid")
		}
		cfg.policy.MaxCumulativeCostMicro = &vector
	}
	if cfg.policy.Validate() != nil {
		return nil, fail("invalid_economics_policy", "economics policy is not eligible")
	}
	if err = tx.QueryRow(ctx, `SELECT id,valuation_currency,source,quoted_at,valid_until,created_by,created_at FROM fx_policy_versions WHERE id=$1 AND valuation_currency=$2`, cfg.policy.FxPolicyVersionID, cfg.policy.ValuationCurrency).Scan(&cfg.fx.ID, &cfg.fx.ValuationCurrency, &cfg.fx.Source, &cfg.fx.QuotedAt, &cfg.fx.ValidUntil, &cfg.fx.CreatedBy, &cfg.fx.CreatedAt); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT source_currency,target_currency,rate_num,rate_den,fee_buffer_ppm FROM fx_policy_rates WHERE policy_version_id=$1 ORDER BY source_currency,target_currency`, cfg.fx.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rate economics.FxRate
		if err = rows.Scan(&rate.SourceCurrency, &rate.TargetCurrency, &rate.RateNum, &rate.RateDen, &rate.FeeBufferPPM); err != nil {
			return nil, err
		}
		cfg.fx.Rates = append(cfg.fx.Rates, rate)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if cfg.fx.Validate() != nil {
		return nil, fail("invalid_fx_policy", "FX policy is invalid")
	}
	return &cfg, nil
}

func paymentBasisForReserveTx(ctx context.Context, tx pgx.Tx, tenant string, allocations []fundingLotAllocation) (economics.MoneyVector, error) {
	basis := economics.MoneyVector{}
	for _, allocation := range allocations {
		var kind, currency, buyRate string
		var fiat, issued int64
		if err := tx.QueryRow(ctx, `SELECT f.basis_kind,coalesce(f.fiat_currency,''),coalesce(f.fiat_amount,0),l.issued_amount,coalesce(f.buy_rate_version_id,'') FROM funding_lots l JOIN funding_receipts f ON f.tenant_id=l.tenant_id AND f.id=l.funding_receipt_id WHERE l.tenant_id=$1 AND l.id=$2`, tenant, allocation.lotID).Scan(&kind, &currency, &fiat, &issued, &buyRate); err != nil {
			return nil, err
		}
		if allocation.amount < 0 || issued <= 0 || int64(allocation.amount) > issued {
			return nil, fail("payment_basis_incomplete", "funding allocation is invalid")
		}
		switch kind {
		case "grant", "opening":
			continue
		case "paid":
			if fiat <= 0 || buyRate == "" || currency != "CNY" && currency != "USD" {
				return nil, fail("payment_basis_incomplete", "paid funding basis is incomplete")
			}
		default:
			return nil, fail("payment_basis_unknown", "request contains an unknown payment basis")
		}
		value := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(int64(allocation.amount)), big.NewInt(fiat)), big.NewInt(issued))
		current := basis[economics.Currency(currency)]
		sum := new(big.Int).Add(big.NewInt(int64(current)), value)
		if !sum.IsInt64() {
			return nil, fail("amount_overflow", "payment basis exceeds supported range")
		}
		basis[economics.Currency(currency)] = economics.Money(sum.Int64())
	}
	return basis, nil
}

func insertRequestEconomicsTx(ctx context.Context, tx pgx.Tx, result *Reservation, cfg *offerEconomicsConfig, hint *economics.TaskClass, quality requestQualityScope, at time.Time, allocations []fundingLotAllocation) error {
	selection, err := cfg.task.Resolve(hint)
	if err != nil {
		return fail("task_not_allowed", "task hint is not allowed by the service offer")
	}
	basis, err := paymentBasisForReserveTx(ctx, tx, result.TenantID, allocations)
	if err != nil {
		return err
	}
	if err = cfg.fx.ValidateAt(at); err != nil {
		return fail("fx_unavailable", "a current conservative FX policy is required")
	}
	snapshot, err := economics.NewEconomicsSnapshot(economics.EconomicsSnapshotInput{
		TenantID: result.TenantID, RequestID: result.RequestID, ReservationID: result.ID,
		OfferVersionID: cfg.offerID, EconomicsVersionID: cfg.policy.ID,
		ExecutionPolicyVersionID: cfg.execution.ID, TaskPolicyVersionID: cfg.task.ID, QualityPolicyVersionID: cfg.qualityID,
		TaskClass: selection.Class, TaskSource: selection.Source, TaskCandidates: append([]string{}, cfg.task.ClassCandidates[selection.Class]...),
		SettlementMode: cfg.execution.SettlementMode, MaxOuterAttempts: cfg.execution.MaxOuterAttempts,
		ReservationCoinAmount: int64(result.Amount), PaymentBasisMicro: basis, PaymentBasisComplete: true,
		FxPolicyVersionID: cfg.fx.ID, ValuationCurrency: cfg.policy.ValuationCurrency, FxSnapshot: cfg.fx,
		MinMarginRate: cfg.policy.MinMarginRate, MaxCumulativeCostMicro: cfg.policy.MaxCumulativeCostMicro,
		ExecutionPolicy: cfg.execution, CreatedAt: at,
	})
	if err != nil {
		return fail("invalid_economics_snapshot", "request economics snapshot is invalid")
	}
	raw, err := snapshot.CanonicalJSON()
	if err != nil {
		return err
	}
	basisJSON, _ := json.Marshal(basis)
	var maxCost any
	if cfg.policy.MaxCumulativeCostMicro != nil {
		maxCost, err = json.Marshal(*cfg.policy.MaxCumulativeCostMicro)
		if err != nil {
			return err
		}
	}
	fxJSON, err := json.Marshal(cfg.fx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO request_economics_snapshots(tenant_id,request_id,reservation_id,offer_version_id,economics_version_id,execution_policy_version_id,task_policy_version_id,quality_policy_version_id,task_class,task_source,settlement_mode,max_outer_attempts,reservation_coin_amount,payment_basis_micro,payment_basis_complete,fx_policy_version_id,valuation_currency,fx_snapshot,max_cumulative_cost_micro,protocol,tools_scope,language_scope,reasoning_scope,snapshot,snapshot_canonical,snapshot_digest,created_at)
 SELECT $1,$2,$3,o.id,o.economics_version_id,o.execution_policy_version_id,o.task_policy_version_id,o.quality_policy_version_id,$4,$5,$6,$7,$8,$9::jsonb,true,$10,$11,$12::jsonb,$13::jsonb,$14,$15,'any',$16,$17::jsonb,$18,sha256($18),$19 FROM service_offer_versions o WHERE o.id=$20`, result.TenantID, result.RequestID, result.ID, string(selection.Class), string(selection.Source), cfg.execution.SettlementMode, cfg.execution.MaxOuterAttempts, int64(result.Amount), basisJSON, cfg.fx.ID, cfg.policy.ValuationCurrency, fxJSON, maxCost, quality.protocol, quality.tools, quality.reasoning, raw, raw, at, cfg.offerID)
	if err != nil {
		return err
	}
	result.Economics = &snapshot
	return nil
}

type requestQualityScope struct {
	protocol, tools, reasoning string
}

func requestSnapshotTx(ctx context.Context, tx pgx.Tx, tenant, request string) (economics.EconomicsSnapshot, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT snapshot FROM request_economics_snapshots WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&raw); err != nil {
		return economics.EconomicsSnapshot{}, err
	}
	var snapshot economics.EconomicsSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return snapshot, fail("invalid_economics_snapshot", "stored request economics are invalid")
	}
	return snapshot, nil
}

func addCostVector(total economics.MoneyVector, currency economics.Currency, amount string) error {
	value, ok := new(big.Int).SetString(amount, 10)
	if !ok || !value.IsInt64() || value.Sign() < 0 {
		return fail("invalid_cost", "supplier cost is not a valid micro-fiat amount")
	}
	sum := new(big.Int).Add(big.NewInt(int64(total[currency])), value)
	if !sum.IsInt64() {
		return fail("amount_overflow", "cumulative cost exceeds supported range")
	}
	total[currency] = economics.Money(sum.Int64())
	return nil
}

func priorAttemptCostsTx(ctx context.Context, tx pgx.Tx, tenant, request string, sequence int) (economics.MoneyVector, error) {
	total := economics.MoneyVector{}
	rows, err := tx.Query(ctx, `SELECT a.cost_status,coalesce(a.cost_amount::text,''),coalesce(c.currency,'CNY') FROM upstream_attempts a LEFT JOIN attempt_meter_snapshots s ON s.tenant_id=a.tenant_id AND s.attempt_id=a.id LEFT JOIN cost_versions c ON c.id=s.cost_version_id WHERE a.tenant_id=$1 AND a.request_id=$2 AND a.sequence<$3 ORDER BY a.sequence`, tenant, request, sequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status, amount string
		var currency economics.Currency
		if err = rows.Scan(&status, &amount, &currency); err != nil {
			return nil, err
		}
		if status != "contract" && status != "invoice" || amount == "" {
			return nil, fail("cost_unknown", "an earlier attempt has unknown cost")
		}
		if err = addCostVector(total, currency, amount); err != nil {
			return nil, err
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `SELECT c.currency,coalesce(c.amount_micro::text,''),c.basis FROM attempt_cost_components c JOIN upstream_attempts a ON a.tenant_id=c.tenant_id AND a.id=c.attempt_id WHERE c.tenant_id=$1 AND a.request_id=$2 AND a.sequence<$3 AND NOT EXISTS(SELECT 1 FROM attempt_cost_components newer WHERE newer.attempt_id=c.attempt_id AND newer.component_id=c.component_id AND newer.revision>c.revision) ORDER BY c.attempt_id,c.component_id`, tenant, request, sequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var currency economics.Currency
		var amount, basis string
		if err = rows.Scan(&currency, &amount, &basis); err != nil {
			return nil, err
		}
		if basis == "unknown" || amount == "" {
			return nil, fail("cost_unknown", "an earlier inner cost is unknown")
		}
		if err = addCostVector(total, currency, amount); err != nil {
			return nil, err
		}
	}
	return total, rows.Err()
}

func selectedCandidateCost(decision *routing.RouteDecision) (economics.MoneyVector, error) {
	if decision == nil {
		return nil, fail("cost_unknown", "selected route cost evidence is missing")
	}
	for _, candidate := range decision.Candidates {
		if candidate.BindingID != decision.SelectedBindingID || candidate.Reason != "selected" && candidate.Reason != "sticky_override" {
			continue
		}
		if candidate.CostSnapshot.UpperAmount == nil || candidate.CostSnapshot.Basis == "unknown" {
			return nil, fail("cost_unknown", "selected candidate cost is unknown")
		}
		currency := economics.Currency(candidate.CostSnapshot.Currency)
		if currency == "" {
			currency = economics.CurrencyCNY
		}
		vector := economics.MoneyVector{}
		if err := addCostVector(vector, currency, *candidate.CostSnapshot.UpperAmount); err != nil {
			return nil, err
		}
		return vector, nil
	}
	return nil, fail("invalid_route_decision", "selected candidate has no frozen cost")
}

func conservativeCostVector(quote routecost.Quote, inputBound, outputBound int64) (economics.MoneyVector, error) {
	if !quote.Known() {
		return nil, fail("cost_unknown", "candidate quote is unknown")
	}
	bounds, err := metering.ManagementTokenBounds(quote.Schema(), metering.Supplier, inputBound, outputBound)
	if err != nil {
		return nil, fail("invalid_bounds", "supplier cost bounds are invalid")
	}
	rates, err := quote.Rates()
	if err != nil {
		return nil, err
	}
	amount, err := metering.ReservationAmount(bounds, rates)
	if err != nil {
		return nil, fail("amount_overflow", "candidate cost is not representable")
	}
	return economics.MoneyVector{economics.Currency(quote.Currency()): economics.Money(amount)}, nil
}

func equalCostVectors(a, b economics.MoneyVector) bool {
	if len(a) != len(b) {
		return false
	}
	for currency, amount := range a {
		if b[currency] != amount {
			return false
		}
	}
	return true
}

func checkCumulativeCostTx(ctx context.Context, tx pgx.Tx, tenant, request string, sequence int, decision *routing.RouteDecision, expected economics.MoneyVector) error {
	snapshot, err := requestSnapshotTx(ctx, tx, tenant, request)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	prior, err := priorAttemptCostsTx(ctx, tx, tenant, request, sequence)
	if err != nil {
		return err
	}
	candidate, err := selectedCandidateCost(decision)
	if err != nil {
		return err
	}
	if expected != nil && !equalCostVectors(candidate, expected) {
		return fail("invalid_route_decision", "selected cost does not match the original quote and supplier bounds")
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	_, err = economics.EvaluateAdmission(economics.AdmissionRequest{Snapshot: snapshot, PriorCost: economics.CostBasis{Vector: prior, Complete: true}, CandidateCost: economics.CostBasis{Vector: candidate, Complete: true}}, now)
	if err != nil {
		return fail("margin_admission_failed", "request cumulative cost exceeds the conservative economic policy")
	}
	return nil
}

func frozenAttemptCostVectorTx(ctx context.Context, tx pgx.Tx, tenant, attempt string) (economics.MoneyVector, error) {
	var input, output int64
	in := routecost.StoredInput{}
	if err := tx.QueryRow(ctx, `SELECT ms.canonical,ms.schema_digest,s.variant_key,coalesce(s.cost_version_id,''),s.cost_basis,coalesce(c.currency,'CNY'),c.rates_canonical,c.rates_digest,s.input_bound,s.output_bound,a.channel_id,a.upstream_model
 FROM upstream_attempts a JOIN attempt_meter_snapshots s ON s.tenant_id=a.tenant_id AND s.request_id=a.request_id AND s.attempt_id=a.id
 JOIN meter_schemas ms ON ms.id=s.schema_id AND ms.schema_digest=s.schema_digest
 LEFT JOIN cost_versions c ON c.id=s.cost_version_id AND c.channel_id=a.channel_id AND c.upstream_model=a.upstream_model AND c.schema_id=s.schema_id AND c.schema_digest=s.schema_digest AND c.variant_key=s.variant_key AND c.basis=s.cost_basis
 WHERE a.tenant_id=$1 AND a.id=$2`, tenant, attempt).Scan(&in.SchemaCanonical, &in.SchemaDigest, &in.VariantKey, &in.VersionID, &in.Basis, &in.Currency, &in.RatesCanonical, &in.RatesDigest, &input, &output, &in.ChannelID, &in.UpstreamModel); err != nil {
		return nil, err
	}
	quote, err := routecost.ParseStoredQuote(in)
	if err != nil {
		return nil, fail("invalid_meter_snapshot", "frozen attempt quote is invalid")
	}
	return conservativeCostVector(quote, input, output)
}

func routeDecisionForAttemptTx(ctx context.Context, tx pgx.Tx, tenant, attempt string) (*routing.RouteDecision, error) {
	var raw []byte
	var decision routing.RouteDecision
	if err := tx.QueryRow(ctx, `SELECT candidates,sequence,selected_binding_id FROM route_decisions WHERE tenant_id=$1 AND attempt_id=$2`, tenant, attempt).Scan(&raw, &decision.Sequence, &decision.SelectedBindingID); err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &decision.Candidates) != nil {
		return nil, fail("invalid_route_decision", "stored route candidates are invalid")
	}
	return &decision, nil
}

func parseMoneyAmount(value string) (int64, error) {
	amount, err := strconv.ParseInt(value, 10, 64)
	return amount, err
}
