package catalog

import (
	"context"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

const groupPriceCompileTargetSuffix = "/price-versions/compile"

type groupMultiplier struct {
	raw       string
	numerator *big.Int
}

// parseGroupMultiplier keeps the management input as text and scales it to
// the fixed six-decimal denominator without using floating point.
func parseGroupMultiplier(raw string) (groupMultiplier, error) {
	var out groupMultiplier
	if raw == "" {
		return out, pricingFail("invalid_request")
	}
	point := strings.IndexByte(raw, '.')
	whole, fraction := raw, ""
	if point >= 0 {
		whole, fraction = raw[:point], raw[point+1:]
		if len(fraction) == 0 || len(fraction) > 6 {
			return out, pricingFail("invalid_request")
		}
	}
	if whole == "" || (len(whole) > 1 && whole[0] == '0') {
		return out, pricingFail("invalid_request")
	}
	for _, r := range whole + fraction {
		if r < '0' || r > '9' {
			return out, pricingFail("invalid_request")
		}
	}
	value, ok := new(big.Int).SetString(whole+fraction, 10)
	if !ok {
		return out, pricingFail("invalid_request")
	}
	value.Mul(value, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(6-len(fraction))), nil))
	if value.Sign() <= 0 || value.Cmp(big.NewInt(100_000_000)) > 0 {
		return out, pricingFail("invalid_request")
	}
	out.raw, out.numerator = raw, value
	return out, nil
}

func validGroupMultiplier(raw string) bool {
	_, err := parseGroupMultiplier(raw)
	return err == nil
}

func compileRate(base int64, multiplier *big.Int) (int64, error) {
	if base < 0 || multiplier == nil || multiplier.Sign() <= 0 {
		return 0, pricingFail("invalid_price")
	}
	n := new(big.Int).Mul(big.NewInt(base), multiplier)
	denom := big.NewInt(1_000_000)
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(n, denom, rem)
	if rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, pricingFail("invalid_price")
	}
	return q.Int64(), nil
}

func normalizeGroupPriceCompile(c GroupPriceCompileCommand) (GroupPriceCompileCommand, groupMultiplier, error) {
	if !pricingID(c.OperationID) || !pricingID(c.GroupID) || !validPricingEffective(c.EffectiveAt) {
		return c, groupMultiplier{}, pricingFail("invalid_request")
	}
	multiplier, err := parseGroupMultiplier(c.Multiplier)
	if err != nil {
		return c, groupMultiplier{}, err
	}
	if len(c.ModelIDs) < 1 || len(c.ModelIDs) > 100 {
		return c, groupMultiplier{}, pricingFail("invalid_request")
	}
	seen := make(map[string]struct{}, len(c.ModelIDs))
	models := make([]string, 0, len(c.ModelIDs))
	for _, id := range c.ModelIDs {
		if !pricingID(id) {
			return c, groupMultiplier{}, pricingFail("invalid_request")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	if len(models) < 1 {
		return c, groupMultiplier{}, pricingFail("invalid_request")
	}
	sort.Strings(models)
	if len(c.ExpectedBaseVersionIDs) > len(models) {
		return c, groupMultiplier{}, pricingFail("invalid_request")
	}
	var expected map[string]string
	if c.ExpectedBaseVersionIDs != nil {
		expected = make(map[string]string, len(c.ExpectedBaseVersionIDs))
		for model, version := range c.ExpectedBaseVersionIDs {
			if _, ok := seen[model]; !ok || !pricingID(version) {
				return c, groupMultiplier{}, pricingFail("invalid_request")
			}
			expected[model] = version
		}
	}
	c.ModelIDs = models
	c.ExpectedBaseVersionIDs = expected
	c.EffectiveAt = c.EffectiveAt.UTC()
	return c, multiplier, nil
}

func groupPriceCompileSafe(c GroupPriceCompileCommand) map[string]any {
	return map[string]any{
		"multiplier":                c.Multiplier,
		"model_ids":                 c.ModelIDs,
		"effective_at":              pricingStamp(c.EffectiveAt),
		"expected_base_version_ids": c.ExpectedBaseVersionIDs,
	}
}

type compiledGroupPrice struct {
	modelID, schemaID, schemaDigest string
	baseVersionID                   string
	baseInput, baseOutput           int64
	input, output                   int64
	priceVersionID                  string
}

func compileBaseRate(entry metering.Rate) (int64, error) {
	if entry.Numerator < 0 || entry.Denominator <= 0 {
		return 0, pricingFail("invalid_price")
	}
	n := new(big.Int).Mul(big.NewInt(entry.Numerator), big.NewInt(1_000_000))
	d := big.NewInt(entry.Denominator)
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(n, d, rem)
	if rem.Sign() != 0 || !q.IsInt64() {
		return 0, pricingFail("invalid_price")
	}
	return q.Int64(), nil
}

func (s *Store) compileGroupPricesTx(ctx context.Context, tx pgx.Tx, c GroupPriceCompileCommand, multiplier groupMultiplier, publish bool) (GroupPriceCompileView, []compiledGroupPrice, error) {
	var view GroupPriceCompileView
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM price_groups WHERE id=$1`, c.GroupID).Scan(&state); err != nil {
		return view, nil, err
	}
	if state != "active" {
		return view, nil, pricingFail("price_group_retired")
	}

	rows, err := tx.Query(ctx, `
WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at), requested(model_id) AS (SELECT unnest($1::text[]))
SELECT r.model_id,p.id,p.schema_id,encode(p.schema_digest,'hex'),p.rates_canonical
FROM requested r
LEFT JOIN LATERAL (
  SELECT p.id,p.model_id,p.schema_id,p.schema_digest,p.rates_canonical
  FROM instant CROSS JOIN price_versions p
  JOIN models m ON m.id=p.model_id
  JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id
  WHERE p.model_id=r.model_id AND p.price_group_id IS NULL AND p.variant_key='default'
    AND p.schema_id=c.schema_id AND p.schema_digest=c.schema_digest AND p.effective_at<=instant.at
  ORDER BY p.effective_at DESC,p.created_at DESC,p.id COLLATE "C" DESC
  LIMIT 1
) p ON true
ORDER BY r.model_id`, c.ModelIDs)
	if err != nil {
		return view, nil, err
	}
	defer rows.Close()
	type basePriceRow struct {
		modelID, baseID, schemaID, digest string
		canonical                         []byte
	}
	baseRows := make([]basePriceRow, 0, len(c.ModelIDs))
	for rows.Next() {
		var modelID string
		var baseID, schemaID, digest *string
		var canonical []byte
		if err := rows.Scan(&modelID, &baseID, &schemaID, &digest, &canonical); err != nil {
			return view, nil, err
		}
		if baseID == nil {
			baseRows = append(baseRows, basePriceRow{modelID: modelID})
			continue
		}
		baseRows = append(baseRows, basePriceRow{modelID: modelID, baseID: *baseID, schemaID: *schemaID, digest: *digest, canonical: canonical})
	}
	if err := rows.Err(); err != nil {
		return view, nil, err
	}
	rows.Close()

	missing := make([]string, 0)
	compiled := make([]compiledGroupPrice, 0, len(c.ModelIDs))
	for _, base := range baseRows {
		if base.baseID == "" {
			missing = append(missing, base.modelID)
			continue
		}
		if expected := c.ExpectedBaseVersionIDs[base.modelID]; expected != "" && expected != base.baseID {
			return view, nil, pricingFail("base_price_changed")
		}
		schema, err := loadPricingSchema(ctx, tx, base.schemaID, base.digest)
		if err != nil {
			return view, nil, pricingFail("invalid_price")
		}
		rates, err := metering.ParseRates(schema, metering.Retail, base.canonical)
		if err != nil {
			return view, nil, pricingFail("invalid_price")
		}
		entries, err := rates.Entries()
		if err != nil || len(entries) != 2 || entries[0].Dimension != "text_input_token" || entries[1].Dimension != "text_output_token" {
			return view, nil, pricingFail("invalid_price")
		}
		baseInput, err := compileBaseRate(entries[0])
		if err != nil {
			return view, nil, err
		}
		baseOutput, err := compileBaseRate(entries[1])
		if err != nil {
			return view, nil, err
		}
		input, err := compileRate(baseInput, multiplier.numerator)
		if err != nil {
			return view, nil, err
		}
		output, err := compileRate(baseOutput, multiplier.numerator)
		if err != nil {
			return view, nil, err
		}
		id := ""
		if publish {
			id = uuid.NewString()
		}
		compiled = append(compiled, compiledGroupPrice{modelID: base.modelID, schemaID: base.schemaID, schemaDigest: base.digest, baseVersionID: base.baseID, baseInput: baseInput, baseOutput: baseOutput, input: input, output: output, priceVersionID: id})
	}
	if len(missing) != 0 {
		return view, nil, &PricingError{Code: "base_price_missing", MissingModelIDs: missing}
	}
	if len(compiled) != len(c.ModelIDs) {
		return view, nil, pricingFail("pricing_invariant")
	}
	view = GroupPriceCompileView{GroupID: c.GroupID, Multiplier: multiplier.raw, EffectiveAt: pricingStamp(c.EffectiveAt), Items: make([]GroupPriceCompileItem, 0, len(compiled))}
	for _, item := range compiled {
		var priceID *string
		if item.priceVersionID != "" {
			id := item.priceVersionID
			priceID = &id
		}
		view.Items = append(view.Items, GroupPriceCompileItem{ModelID: item.modelID, BasePriceVersionID: item.baseVersionID, BaseInputRate: strconv.FormatInt(item.baseInput, 10), BaseOutputRate: strconv.FormatInt(item.baseOutput, 10), InputRate: strconv.FormatInt(item.input, 10), OutputRate: strconv.FormatInt(item.output, 10), PriceVersionID: priceID})
	}
	return view, compiled, nil
}

func (s *Store) CompileGroupPrices(ctx context.Context, a PricingActor, c GroupPriceCompileCommand) (PricingOperationResult[GroupPriceCompileView], error) {
	var out PricingOperationResult[GroupPriceCompileView]
	c, multiplier, err := normalizeGroupPriceCompile(c)
	if err != nil {
		return out, err
	}
	safe := groupPriceCompileSafe(c)
	r, replay, err := s.pricingCommand(ctx, pricingCommand{id: c.OperationID, kind: "group_price.compile", actor: a.UserID, scope: "platform", target: "/api/platform/price-groups/" + c.GroupID + groupPriceCompileTargetSuffix, safe: safe, replayCheck: func(old PricingOperationReceipt) error {
		v, ok := old.Result.(*GroupPriceCompileView)
		if !ok || v.GroupID != c.GroupID || v.Multiplier != c.Multiplier || v.EffectiveAt != pricingStamp(c.EffectiveAt) || len(v.Items) != len(c.ModelIDs) {
			return pricingFail("policy_operation_conflict")
		}
		return nil
	}, prepare: func(ctx context.Context, tx pgx.Tx) (pricingWrite, error) {
		view, compiled, err := s.compileGroupPricesTx(ctx, tx, c, multiplier, true)
		if err != nil {
			return pricingWrite{}, err
		}
		return pricingWrite{resource: c.GroupID, result: view, apply: func(ctx context.Context, tx pgx.Tx, at time.Time) error {
			for _, item := range compiled {
				if _, err := tx.Exec(ctx, `SELECT meter_insert_token_price($1,$2,$3,$4,$5,$6,$7,$8,$9)`, item.priceVersionID, item.modelID, item.input, item.output, c.EffectiveAt, a.UserID, c.GroupID, c.OperationID, at); err != nil {
					return err
				}
			}
			return nil
		}}, nil
	}})
	if err != nil {
		return out, err
	}
	return PricingOperationResult[GroupPriceCompileView]{Operation: r.Operation, Result: *r.Result.(*GroupPriceCompileView), Replayed: replay}, nil
}

func (s *Store) PreviewGroupPriceCompile(ctx context.Context, actor string, c GroupPriceCompileCommand) (GroupPriceCompileView, error) {
	var out GroupPriceCompileView
	c, multiplier, err := normalizeGroupPriceCompile(c)
	if err != nil {
		return out, err
	}
	err = s.pricingRead(ctx, actor, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, _, err = s.compileGroupPricesTx(ctx, tx, c, multiplier, false)
		return err
	})
	return out, err
}
