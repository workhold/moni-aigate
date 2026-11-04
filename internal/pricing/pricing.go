// Package pricing selects immutable retail prices inside an existing transaction.
// Its callers own authorization, the tenant/model lock order, and all money writes.
package pricing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	VersionID             string
	GroupID               *string
	InputRate, OutputRate int64
	EffectiveAt, PricedAt time.Time
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
	var versionID *string
	var inputRate, outputRate *int64
	var effectiveAt *time.Time
	err := tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at)
 SELECT instant.at,p.id,meter_price_million(p.id,'text_input_token'),meter_price_million(p.id,'text_output_token'),p.effective_at
 FROM instant LEFT JOIN LATERAL (
  SELECT id,effective_at FROM price_versions
  WHERE model_id=$1 AND price_group_id IS NOT DISTINCT FROM $2::text
    AND effective_at<=instant.at
  ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1
 ) p ON true`, modelID, domain.GroupID).Scan(&pricedAt, &versionID, &inputRate, &outputRate, &effectiveAt)
	if err != nil {
		return nil, time.Time{}, err
	}
	if versionID == nil {
		if inputRate != nil || outputRate != nil || effectiveAt != nil {
			return nil, time.Time{}, &Error{Code: "pricing_invariant"}
		}
		return nil, pricedAt, nil
	}
	if !canonicalID(*versionID) || inputRate == nil || outputRate == nil || effectiveAt == nil ||
		*inputRate < 0 || *outputRate < 0 || effectiveAt.After(pricedAt) {
		return nil, time.Time{}, &Error{Code: "pricing_invariant"}
	}
	var groupID *string
	if domain.GroupID != nil {
		value := *domain.GroupID
		groupID = &value
	}
	return &Selection{
		VersionID: *versionID, GroupID: groupID,
		InputRate: *inputRate, OutputRate: *outputRate,
		EffectiveAt: *effectiveAt, PricedAt: pricedAt,
	}, pricedAt, nil
}
