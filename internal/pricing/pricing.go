// Package pricing selects immutable retail prices inside an existing transaction.
// Its callers own authorization, the tenant/model lock order, and all money writes.
package pricing

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

// Domain must come from the tenant row already held by the caller. A nil GroupID
// selects the public default domain; no group falls back to another domain.
type Domain struct {
	GroupID        *string
	TenantRevision int64
}

// Selection is the price observed by one SQL statement and its database clock.
// Later publication, assignment, or wallet waiting must not replace this value.
type Selection struct {
	VersionID, ProfileVersion, VariantKey string
	GroupID                               *string
	Schema                                metering.Schema
	Rates                                 metering.Rates
	EffectiveAt, PricedAt                 time.Time
}

// Error exposes only a stable internal code; database errors remain unwrapped so
// callers can distinguish definite PostgreSQL rollbacks from unknown outcomes.
type Error struct {
	Code string
}

func (e *Error) Error() string { return e.Code }

func canonicalID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validDomain(domain Domain) bool {
	return domain.TenantRevision > 0 && (domain.GroupID == nil || canonicalID(*domain.GroupID))
}

// LockAssignedGroupTx takes only the assigned group SHARE lock. The caller must
// already hold the tenant and model locks. Retirement stops new configuration,
// while existing assignments continue to use their immutable group prices.
func LockAssignedGroupTx(ctx context.Context, tx pgx.Tx, domain Domain) error {
	if !validDomain(domain) || tx == nil {
		return &Error{Code: "invalid_domain"}
	}
	if domain.GroupID == nil {
		return nil
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM price_groups WHERE id=$1 FOR SHARE`, *domain.GroupID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return &Error{Code: "pricing_invariant"}
	}
	return err
}

// SelectEffectiveTx observes one exact domain with a fresh database timestamp.
// A missing effective price returns nil, pricedAt, nil; zero rates are a price.
// This helper neither creates a transaction nor reads/locks wallets or tenants.
func SelectEffectiveTx(ctx context.Context, tx pgx.Tx, modelID string, domain Domain) (*Selection, time.Time, error) {
	if !validDomain(domain) || !canonicalID(modelID) || tx == nil {
		return nil, time.Time{}, &Error{Code: "invalid_domain"}
	}
	var pricedAt time.Time
	var versionID, profileVersion, schemaID *string
	var schemaRaw, schemaDigest, ratesRaw, ratesDigest []byte
	var effectiveAt *time.Time
	err := tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at)
 SELECT instant.at,c.profile_version,c.schema_id,c.canonical,c.schema_digest,p.id,p.rates_canonical,p.rates_digest,p.effective_at
 FROM instant LEFT JOIN LATERAL (
  SELECT v.id AS profile_version,v.schema_id,v.schema_digest,s.canonical
  FROM models m JOIN model_capability_versions v ON v.id=m.profile_version AND v.model_id=m.id
  JOIN model_capability_variants d ON d.profile_version=v.id AND d.variant_key='default'
  JOIN meter_schemas s ON s.id=v.schema_id AND s.schema_digest=v.schema_digest WHERE m.id=$1
 ) c ON true LEFT JOIN LATERAL (
  SELECT p.id,p.rates_canonical,p.rates_digest,p.effective_at FROM price_versions p
  WHERE p.model_id=$1 AND p.price_group_id IS NOT DISTINCT FROM $2::text
    AND p.schema_id=c.schema_id AND p.schema_digest=c.schema_digest AND p.variant_key='default'
    AND p.effective_at<=instant.at
  ORDER BY p.effective_at DESC,p.created_at DESC,p.id COLLATE "C" DESC LIMIT 1
 ) p ON true`, modelID, domain.GroupID).Scan(&pricedAt, &profileVersion, &schemaID, &schemaRaw, &schemaDigest, &versionID, &ratesRaw, &ratesDigest, &effectiveAt)
	if err != nil {
		return nil, time.Time{}, err
	}
	if profileVersion == nil || !canonicalID(*profileVersion) || schemaID == nil {
		return nil, time.Time{}, &Error{Code: "pricing_invariant"}
	}
	schema, err := metering.ParseSchema(schemaRaw)
	if err != nil {
		return nil, time.Time{}, &Error{Code: "pricing_invariant"}
	}
	id, _ := schema.ID()
	canonical, _ := schema.CanonicalJSON()
	digest, _ := schema.Digest()
	if id != *schemaID || !bytes.Equal(schemaRaw, canonical) || !bytes.Equal(schemaDigest, digest[:]) {
		return nil, time.Time{}, &Error{Code: "pricing_invariant"}
	}
	if versionID == nil {
		if ratesRaw != nil || ratesDigest != nil || effectiveAt != nil {
			return nil, time.Time{}, &Error{Code: "pricing_invariant"}
		}
		return nil, pricedAt, nil
	}
	if !canonicalID(*versionID) || effectiveAt == nil || effectiveAt.After(pricedAt) {
		return nil, time.Time{}, &Error{Code: "pricing_invariant"}
	}
	rates, err := metering.ParseRates(schema, metering.Retail, ratesRaw)
	if err != nil {
		return nil, time.Time{}, &Error{Code: "pricing_invariant"}
	}
	canonical, _ = rates.CanonicalJSON()
	digest, _ = rates.Digest()
	if !bytes.Equal(ratesRaw, canonical) || !bytes.Equal(ratesDigest, digest[:]) {
		return nil, time.Time{}, &Error{Code: "pricing_invariant"}
	}
	var groupID *string
	if domain.GroupID != nil {
		value := *domain.GroupID
		groupID = &value
	}
	return &Selection{
		VersionID: *versionID, GroupID: groupID, ProfileVersion: *profileVersion, VariantKey: "default",
		Schema: schema, Rates: rates,
		EffectiveAt: *effectiveAt, PricedAt: pricedAt,
	}, pricedAt, nil
}
