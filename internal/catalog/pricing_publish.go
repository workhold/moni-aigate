package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

const defaultPriceTarget = "/api/platform/price-versions"
const costPriceTarget = "/api/platform/cost-versions"

func validUpstreamModel(v string) bool {
	if !utf8.ValidString(v) || len(v) < 1 || len(v) > 200 || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validPricingEffective(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.Nanosecond()%1000 == 0
}
func publishSchema(ctx context.Context, tx pgx.Tx, id, digest string) (metering.Schema, error) {
	s, e := loadPricingSchema(ctx, tx, id, digest)
	if e != nil {
		var pe *PricingError
		if errors.Is(e, pgx.ErrNoRows) || errors.As(e, &pe) {
			return metering.Schema{}, pricingFail("invalid_price")
		}
		return s, e
	}
	return s, nil
}
func lockModelSchema(ctx context.Context, tx pgx.Tx, model, schema, digest, variant string) error {
	var sid, sd string
	e := tx.QueryRow(ctx, `SELECT c.schema_id,encode(c.schema_digest,'hex') FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id JOIN model_capability_variants v ON v.profile_version=c.id AND v.variant_key=$2 WHERE m.id=$1 FOR SHARE OF m`, model, variant).Scan(&sid, &sd)
	if e != nil {
		return e
	}
	if sid != schema || sd != digest {
		return pricingFail("invalid_price")
	}
	return nil
}
func validateCostPrice(ctx context.Context, tx pgx.Tx, v *CostPriceView) error {
	if v.Basis == "unknown" {
		if string(v.Rates) != "null" && len(v.Rates) != 0 {
			return pricingFail("pricing_invariant")
		}
		return nil
	}
	s, err := loadPricingSchema(ctx, tx, v.SchemaID, v.SchemaDigest)
	if err != nil {
		return err
	}
	if _, err = metering.ParseRates(s, metering.Supplier, v.Rates); err != nil {
		return pricingFail("pricing_invariant")
	}
	return nil
}

func (s *Store) PublishDefaultPrice(ctx context.Context, a PricingActor, c DefaultPriceCommand) (PricingOperationResult[DefaultPriceView], error) {
	var out PricingOperationResult[DefaultPriceView]
	if !pricingID(c.ModelID) || !pricingID(c.SchemaID) || !pricingDigest(c.SchemaDigest) || c.VariantKey != "default" || c.InputRate < 0 || c.OutputRate < 0 || !validPricingEffective(c.EffectiveAt) {
		return out, pricingFail("invalid_request")
	}
	c.EffectiveAt = c.EffectiveAt.UTC()
	var raw json.RawMessage
	var schema metering.Schema
	r, replay, e := s.pricingCommand(ctx, pricingCommand{id: c.OperationID, kind: "default_price.publish", actor: a.UserID, scope: "platform", target: defaultPriceTarget, replayCheck: func(old PricingOperationReceipt) error {
		v, ok := old.Result.(*DefaultPriceView)
		if !ok || v.ModelID != c.ModelID || v.SchemaID != c.SchemaID || v.SchemaDigest != c.SchemaDigest || v.VariantKey != c.VariantKey {
			return pricingFail("policy_operation_conflict")
		}
		return nil
	}, resolveSafe: func(ctx context.Context, tx pgx.Tx) (map[string]any, error) {
		var err error
		schema, err = publishSchema(ctx, tx, c.SchemaID, c.SchemaDigest)
		if err != nil {
			return nil, err
		}
		rates, err := metering.ManagementTokenRates(schema, metering.Retail, c.InputRate, c.OutputRate)
		if err != nil {
			return nil, pricingFail("invalid_price")
		}
		raw, _ = rates.CanonicalJSON()
		return map[string]any{"model_id": c.ModelID, "schema_id": c.SchemaID, "schema_digest": c.SchemaDigest, "variant_key": "default", "currency": "CNY", "rates": raw, "effective_at": pricingStamp(c.EffectiveAt)}, nil
	}, prepare: func(ctx context.Context, tx pgx.Tx) (pricingWrite, error) {
		if err := lockModelSchema(ctx, tx, c.ModelID, c.SchemaID, c.SchemaDigest, c.VariantKey); err != nil {
			return pricingWrite{}, err
		}
		if err := validateGroupPriceSchema(&GroupPriceView{Rates: raw}, schema); err != nil {
			return pricingWrite{}, pricingFail("invalid_price")
		}
		id := uuid.NewString()
		v := DefaultPriceView{ID: id, ModelID: c.ModelID, SchemaID: c.SchemaID, SchemaDigest: c.SchemaDigest, VariantKey: "default", Currency: "CNY", Rates: raw, EffectiveAt: pricingStamp(c.EffectiveAt)}
		return pricingWrite{resource: id, result: v, apply: func(ctx context.Context, tx pgx.Tx, at time.Time) error {
			_, err := tx.Exec(ctx, `SELECT meter_insert_token_price($1,$2,$3,$4,$5,$6,NULL,$7,$8)`, id, c.ModelID, c.InputRate, c.OutputRate, c.EffectiveAt, a.UserID, c.OperationID, at)
			return err
		}}, nil
	}})
	if e == nil {
		out = PricingOperationResult[DefaultPriceView]{r.Operation, *r.Result.(*DefaultPriceView), replay}
	}
	return out, e
}

func (s *Store) PublishCostPrice(ctx context.Context, a PricingActor, c CostPriceCommand) (PricingOperationResult[CostPriceView], error) {
	var out PricingOperationResult[CostPriceView]
	known := c.Basis == "contract" || c.Basis == "invoice"
	validRates := known && c.InputRate != nil && c.OutputRate != nil || c.Basis == "unknown" && c.InputRate == nil && c.OutputRate == nil
	if !pricingID(c.ChannelID) || !validUpstreamModel(c.UpstreamModel) || !pricingID(c.SchemaID) || !pricingDigest(c.SchemaDigest) || c.VariantKey != "default" || !validRates || !validPricingEffective(c.EffectiveAt) || c.ExpectedChannelRevision < 1 {
		return out, pricingFail("invalid_request")
	}
	c.EffectiveAt = c.EffectiveAt.UTC()
	var raw json.RawMessage
	var schema metering.Schema
	r, replay, e := s.pricingCommand(ctx, pricingCommand{id: c.OperationID, kind: "cost_price.publish", actor: a.UserID, scope: "platform", target: costPriceTarget, replayCheck: func(old PricingOperationReceipt) error {
		v, ok := old.Result.(*CostPriceView)
		if !ok || v.ChannelID != c.ChannelID || v.UpstreamModel != c.UpstreamModel || v.SchemaID != c.SchemaID || v.SchemaDigest != c.SchemaDigest || v.Basis != c.Basis {
			return pricingFail("policy_operation_conflict")
		}
		return nil
	}, resolveSafe: func(ctx context.Context, tx pgx.Tx) (map[string]any, error) {
		var err error
		schema, err = publishSchema(ctx, tx, c.SchemaID, c.SchemaDigest)
		if err != nil {
			return nil, err
		}
		var rates any
		if known {
			v, err := metering.ManagementTokenRates(schema, metering.Supplier, *c.InputRate, *c.OutputRate)
			if err != nil {
				return nil, pricingFail("invalid_price")
			}
			raw, _ = v.CanonicalJSON()
			if err := validatePriceRates(&raw, schema, metering.Supplier); err != nil {
				return nil, pricingFail("invalid_price")
			}
			rates = raw
		}
		return map[string]any{"channel_id": c.ChannelID, "upstream_model": c.UpstreamModel, "schema_id": c.SchemaID, "schema_digest": c.SchemaDigest, "variant_key": "default", "currency": "CNY", "basis": c.Basis, "rates": rates, "effective_at": pricingStamp(c.EffectiveAt), "expected_channel_revision": pricingUint(c.ExpectedChannelRevision)}, nil
	}, prepare: func(ctx context.Context, tx pgx.Tx) (pricingWrite, error) {
		var rev int64
		if err := tx.QueryRow(ctx, `SELECT management_revision FROM channels WHERE id=$1 FOR UPDATE`, c.ChannelID).Scan(&rev); err != nil {
			return pricingWrite{}, err
		}
		if rev != c.ExpectedChannelRevision {
			return pricingWrite{}, pricingFail("pricing_revision_conflict")
		}
		if rev == math.MaxInt64 {
			return pricingWrite{}, pricingFail("pricing_revision_exhausted")
		}
		id := uuid.NewString()
		v := CostPriceView{ID: id, ChannelID: c.ChannelID, UpstreamModel: c.UpstreamModel, SchemaID: c.SchemaID, SchemaDigest: c.SchemaDigest, VariantKey: "default", Currency: "CNY", Basis: c.Basis, Rates: raw, EffectiveAt: pricingStamp(c.EffectiveAt), ExpectedChannelRevision: pricingUint(rev), ChannelRevision: pricingUint(rev + 1)}
		return pricingWrite{resource: id, result: v, apply: func(ctx context.Context, tx pgx.Tx, at time.Time) error {
			_, err := tx.Exec(ctx, `SELECT meter_insert_token_cost($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, c.ChannelID, c.UpstreamModel, c.SchemaID, c.InputRate, c.OutputRate, c.Basis, c.EffectiveAt, a.UserID, c.OperationID, at)
			return err
		}}, nil
	}})
	if e == nil {
		out = PricingOperationResult[CostPriceView]{r.Operation, *r.Result.(*CostPriceView), replay}
	}
	return out, e
}
