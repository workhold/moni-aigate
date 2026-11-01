package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

type pricingCommand struct {
	id, kind, actor, scope, target, tenant string
	safe                                   map[string]any
	prepare                                func(context.Context, pgx.Tx) (pricingWrite, error)
}
type pricingWrite struct {
	resource string
	revision *int64
	result   any
	apply    func(context.Context, pgx.Tx, time.Time) error
}

const pricingOperationSQL = `SELECT operation_id,kind,scope,actor,target,input_fingerprint,resource_id,revision::text,pricing_time(created_at),result_schema,result_snapshot FROM pricing_operations WHERE operation_id=$1`

func scanPricingOperation(row pgx.Row) (PricingOperationReceipt, error) {
	var r PricingOperationReceipt
	var schema int
	var raw []byte
	e := row.Scan(&r.Operation.OperationID, &r.Operation.Kind, &r.Operation.Scope, &r.Operation.Actor, &r.Operation.Target, &r.Operation.InputFingerprint, &r.Operation.ResourceID, &r.Operation.Revision, &r.Operation.CreatedAt, &schema, &raw)
	if e != nil {
		return r, e
	}
	if schema != 1 || !pricingID(r.Operation.OperationID) || !pricingID(r.Operation.Actor) || !pricingID(r.Operation.ResourceID) || !pricingWireTime(r.Operation.CreatedAt) || len(r.Operation.InputFingerprint) != 64 || (r.Operation.Kind == "group_price.publish" && r.Operation.Revision != nil) || (r.Operation.Kind != "group_price.publish" && (r.Operation.Revision == nil || !pricingDecimal(*r.Operation.Revision, true))) {
		return r, pricingFail("pricing_invariant")
	}
	r.Result, e = pricingDecodeResult(r.Operation.Kind, raw)
	return r, e
}
func pricingAdmin(ctx context.Context, tx pgx.Tx, actor string) error {
	var status, role string
	if e := tx.QueryRow(ctx, `SELECT status,platform_role FROM users WHERE id=$1 FOR SHARE`, actor).Scan(&status, &role); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return pricingFail("forbidden")
		}
		return e
	}
	if status != "active" || role != "admin" {
		return pricingFail("forbidden")
	}
	return nil
}
func pricingDBError(e error) error {
	if e == nil {
		return nil
	}
	var pe *PricingError
	if errors.As(e, &pe) {
		return e
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return pricingFail("not_found")
	}
	var pg *pgconn.PgError
	if errors.As(e, &pg) {
		if pg.Code == "23505" && pg.ConstraintName == "price_versions_domain_effective_key" {
			return pricingFail("price_effective_conflict")
		}
		if pg.Code == "55P03" || pg.Code == "57014" || pg.Code == "40P01" || pg.Code == "40001" {
			return pricingFail("pricing_busy")
		}
		if pg.Code == "23514" || pg.Code == "P0001" || pg.Code == "23503" || pg.Code == "42703" {
			return pricingFail("pricing_invariant")
		}
	}
	return pricingFail("pricing_busy")
}
func (s *Store) pricingCommand(ctx context.Context, c pricingCommand) (PricingOperationReceipt, bool, error) {
	if !pricingID(c.id) || !pricingID(c.actor) {
		return PricingOperationReceipt{}, false, pricingFail("invalid_request")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		r, replay, e := s.pricingCommandOnce(ctx, c)
		var pg *pgconn.PgError
		if e == nil || ctx.Err() != nil || !errors.As(e, &pg) || (pg.Code != "40P01" && pg.Code != "40001") {
			return r, replay, pricingDBError(e)
		}
	}
	return PricingOperationReceipt{}, false, pricingFail("pricing_busy")
}
func (s *Store) pricingCommandOnce(ctx context.Context, c pricingCommand) (PricingOperationReceipt, bool, error) {
	var out PricingOperationReceipt
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return out, false, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='3s'`); e != nil {
		return out, false, e
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pricing-op:'||$1,0))`, c.id); e != nil {
		return out, false, e
	}
	old, e := scanPricingOperation(tx.QueryRow(ctx, pricingOperationSQL, c.id))
	if e == nil {
		if e = pricingAdmin(ctx, tx, c.actor); e != nil {
			return out, false, e
		}
		op := old.Operation
		if op.Kind != c.kind || op.Actor != c.actor || op.Scope != c.scope || op.Target != c.target || op.InputFingerprint != pricingFingerprint(c.kind, c.actor, c.scope, c.target, c.safe) {
			return out, false, pricingFail("policy_operation_conflict")
		}
		if e = tx.Commit(ctx); e != nil {
			return out, false, pricingCommitError(e)
		}
		return old, true, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return out, false, e
	}
	// Assignment must take the tenant before actor, and NO KEY UPDATE is
	// required to remain compatible with Login's sessions tenant FK KEY SHARE.
	tenantMissing := false
	if c.tenant != "" {
		var id string
		if e = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 FOR NO KEY UPDATE`, c.tenant).Scan(&id); e != nil {
			if !errors.Is(e, pgx.ErrNoRows) {
				return out, false, e
			}
			tenantMissing = true
		}
	}
	if e = pricingAdmin(ctx, tx, c.actor); e != nil {
		return out, false, e
	}
	if tenantMissing {
		return out, false, pgx.ErrNoRows
	}
	w, e := c.prepare(ctx, tx)
	if e != nil {
		return out, false, e
	}
	input, e := json.Marshal(c.safe)
	if e != nil {
		return out, false, e
	}
	result, e := json.Marshal(w.result)
	if e != nil {
		return out, false, e
	}
	var created time.Time
	e = tx.QueryRow(ctx, `INSERT INTO pricing_operations(operation_id,kind,actor,scope,scope_tenant_id,target,resource_id,revision,safe_input,input_fingerprint,result_snapshot) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,$10,$11) RETURNING created_at`, c.id, c.kind, c.actor, c.scope, c.tenant, c.target, w.resource, w.revision, input, pricingFingerprint(c.kind, c.actor, c.scope, c.target, c.safe), result).Scan(&created)
	if e != nil {
		return out, false, e
	}
	if w.apply != nil {
		if e = w.apply(ctx, tx, created); e != nil {
			return out, false, e
		}
	}
	out, e = scanPricingOperation(tx.QueryRow(ctx, pricingOperationSQL, c.id))
	if e != nil {
		return out, false, e
	}
	diff, _ := json.Marshal(map[string]any{"operation_id": c.id, "target": c.target, "after": out.Result})
	action := map[string]string{"price_group.create": "pricing.group.create", "price_group.update": "pricing.group.update", "group_price.publish": "pricing.price.publish", "tenant_pricing.assign": "pricing.tenant.assign"}[c.kind]
	if _, e = tx.Exec(ctx, `INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff) VALUES($1,NULLIF($2,''),$3,$4,$5)`, c.actor, c.tenant, action, c.target, diff); e != nil {
		return out, false, e
	}
	out, e = scanPricingOperation(tx.QueryRow(ctx, pricingOperationSQL, c.id))
	if e != nil {
		return out, false, e
	}
	if e = tx.Commit(ctx); e != nil {
		return PricingOperationReceipt{}, false, pricingCommitError(e)
	}
	return out, false, nil
}
func pricingCommitError(e error) error {
	var pg *pgconn.PgError
	if errors.As(e, &pg) {
		return e
	}
	return &PricingError{Code: "pricing_busy", CommitUnknown: true}
}
func (s *Store) CreatePriceGroup(ctx context.Context, a PricingActor, c GroupCreateCommand) (PricingOperationResult[GroupView], error) {
	var out PricingOperationResult[GroupView]
	if !pricingLabel(c.Label) {
		return out, pricingFail("invalid_request")
	}
	r, replay, e := s.pricingCommand(ctx, pricingCommand{id: c.OperationID, kind: "price_group.create", actor: a.UserID, scope: "platform", target: "/api/platform/price-groups", safe: map[string]any{"label": c.Label}, prepare: func(ctx context.Context, tx pgx.Tx) (pricingWrite, error) {
		id := uuid.NewString()
		rev := int64(1)
		v := GroupView{ID: id, Label: c.Label, State: "active", Revision: "1", CreatedAt: pricingStamp(time.Time{})}
		return pricingWrite{resource: id, revision: &rev, result: v, apply: func(ctx context.Context, tx pgx.Tx, at time.Time) error {
			_, e := tx.Exec(ctx, `INSERT INTO price_groups(id,label,state,revision,last_operation_id,created_at) VALUES($1,$2,'active',1,$3,$4)`, id, c.Label, c.OperationID, at)
			return e
		}}, nil
	}})
	if e == nil {
		out = PricingOperationResult[GroupView]{r.Operation, *r.Result.(*GroupView), replay}
	}
	return out, e
}
func (s *Store) UpdatePriceGroup(ctx context.Context, a PricingActor, c GroupUpdateCommand) (PricingOperationResult[GroupView], error) {
	var out PricingOperationResult[GroupView]
	if !pricingID(c.GroupID) || !pricingLabel(c.Label) || c.ExpectedRevision < 1 || (c.State != "active" && c.State != "retired") {
		return out, pricingFail("invalid_request")
	}
	r, replay, e := s.pricingCommand(ctx, pricingCommand{id: c.OperationID, kind: "price_group.update", actor: a.UserID, scope: "platform", target: "/api/platform/price-groups/" + c.GroupID, safe: map[string]any{"expected_revision": pricingUint(c.ExpectedRevision), "label": c.Label, "state": c.State}, prepare: func(ctx context.Context, tx pgx.Tx) (pricingWrite, error) {
		v, e := pricingGroupRow(tx.QueryRow(ctx, `SELECT id,label,state,revision::text,pricing_time(created_at) FROM price_groups WHERE id=$1 FOR NO KEY UPDATE`, c.GroupID))
		if e != nil {
			return pricingWrite{}, e
		}
		if v.Revision != pricingUint(c.ExpectedRevision) {
			return pricingWrite{}, pricingFail("pricing_revision_conflict")
		}
		if v.State == "retired" && c.State != "retired" {
			return pricingWrite{}, pricingFail("price_group_retired")
		}
		changed := v.Label != c.Label || v.State != c.State
		rev := c.ExpectedRevision
		if changed {
			if rev == math.MaxInt64 {
				return pricingWrite{}, pricingFail("pricing_revision_exhausted")
			}
			rev++
			v.Label = c.Label
			v.State = c.State
			v.Revision = pricingUint(rev)
		}
		w := pricingWrite{resource: c.GroupID, revision: &rev, result: v}
		if changed {
			w.apply = func(ctx context.Context, tx pgx.Tx, _ time.Time) error {
				_, e := tx.Exec(ctx, `UPDATE price_groups SET label=$2,state=$3,revision=$4,last_operation_id=$5 WHERE id=$1`, c.GroupID, c.Label, c.State, rev, c.OperationID)
				return e
			}
		}
		return w, nil
	}})
	if e == nil {
		out = PricingOperationResult[GroupView]{r.Operation, *r.Result.(*GroupView), replay}
	}
	return out, e
}
func (s *Store) PublishGroupPrice(ctx context.Context, a PricingActor, c GroupPriceCommand) (PricingOperationResult[GroupPriceView], error) {
	var out PricingOperationResult[GroupPriceView]
	if !pricingID(c.GroupID) || !pricingID(c.ModelID) || c.InputRate < 0 || c.OutputRate < 0 || c.EffectiveAt.IsZero() || c.EffectiveAt.Year() < 1 || c.EffectiveAt.Year() > 9999 || c.EffectiveAt.Nanosecond()%1000 != 0 {
		return out, pricingFail("invalid_request")
	}
	c.EffectiveAt = c.EffectiveAt.UTC()
	r, replay, e := s.pricingCommand(ctx, pricingCommand{id: c.OperationID, kind: "group_price.publish", actor: a.UserID, scope: "platform", target: "/api/platform/price-groups/" + c.GroupID + "/price-versions", safe: map[string]any{"model_id": c.ModelID, "input_rate": pricingUint(c.InputRate), "output_rate": pricingUint(c.OutputRate), "effective_at": pricingStamp(c.EffectiveAt)}, prepare: func(ctx context.Context, tx pgx.Tx) (pricingWrite, error) {
		var profileJSON []byte
		if e := tx.QueryRow(ctx, `SELECT profile FROM models WHERE id=$1 FOR SHARE`, c.ModelID).Scan(&profileJSON); e != nil {
			return pricingWrite{}, e
		}
		var profile provider.Profile
		if json.Unmarshal(profileJSON, &profile) != nil || profile.Validate() != nil || profile.InputOnly() && c.OutputRate != 0 {
			return pricingWrite{}, pricingFail("invalid_price")
		}
		var state string
		if e := tx.QueryRow(ctx, `SELECT state FROM price_groups WHERE id=$1 FOR SHARE`, c.GroupID).Scan(&state); e != nil {
			return pricingWrite{}, e
		}
		if state != "active" {
			return pricingWrite{}, pricingFail("price_group_retired")
		}
		id := uuid.NewString()
		v := GroupPriceView{id, c.ModelID, c.GroupID, "CNY", pricingUint(c.InputRate), pricingUint(c.OutputRate), pricingStamp(c.EffectiveAt), pricingStamp(time.Time{})}
		return pricingWrite{resource: id, result: v, apply: func(ctx context.Context, tx pgx.Tx, at time.Time) error {
			_, e := tx.Exec(ctx, `INSERT INTO price_versions(id,model_id,price_group_id,pricing_operation_id,input_rate,output_rate,effective_at,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, c.ModelID, c.GroupID, c.OperationID, c.InputRate, c.OutputRate, c.EffectiveAt, a.UserID, at)
			return e
		}}, nil
	}})
	if e == nil {
		out = PricingOperationResult[GroupPriceView]{r.Operation, *r.Result.(*GroupPriceView), replay}
	}
	return out, e
}
func (s *Store) AssignTenantPricing(ctx context.Context, a PricingActor, c TenantPricingCommand) (PricingOperationResult[AssignmentView], error) {
	var out PricingOperationResult[AssignmentView]
	if !pricingID(c.TenantID) || c.ExpectedRevision < 1 || c.GroupID != nil && !pricingID(*c.GroupID) {
		return out, pricingFail("invalid_request")
	}
	var group any
	if c.GroupID != nil {
		group = *c.GroupID
	}
	r, replay, e := s.pricingCommand(ctx, pricingCommand{id: c.OperationID, kind: "tenant_pricing.assign", actor: a.UserID, scope: c.TenantID, tenant: c.TenantID, target: "/api/platform/tenants/" + c.TenantID + "/pricing", safe: map[string]any{"expected_revision": pricingUint(c.ExpectedRevision), "price_group_id": group}, prepare: func(ctx context.Context, tx pgx.Tx) (pricingWrite, error) {
		var old *string
		var rev int64
		if e := tx.QueryRow(ctx, `SELECT price_group_id,pricing_revision FROM tenants WHERE id=$1`, c.TenantID).Scan(&old, &rev); e != nil {
			return pricingWrite{}, e
		}
		if rev != c.ExpectedRevision {
			return pricingWrite{}, pricingFail("pricing_revision_conflict")
		}
		changed := !pricingSameID(old, c.GroupID)
		if c.GroupID != nil {
			var state string
			if e := tx.QueryRow(ctx, `SELECT state FROM price_groups WHERE id=$1 FOR SHARE`, *c.GroupID).Scan(&state); e != nil {
				return pricingWrite{}, e
			}
			if changed && state != "active" {
				return pricingWrite{}, pricingFail("price_group_retired")
			}
		}
		if changed {
			if rev == math.MaxInt64 {
				return pricingWrite{}, pricingFail("pricing_revision_exhausted")
			}
			rev++
		}
		v := AssignmentView{c.TenantID, c.GroupID, pricingUint(rev)}
		w := pricingWrite{resource: c.TenantID, revision: &rev, result: v}
		if changed {
			w.apply = func(ctx context.Context, tx pgx.Tx, _ time.Time) error {
				_, e := tx.Exec(ctx, `UPDATE tenants SET price_group_id=$2,pricing_revision=$3,pricing_operation_id=$4 WHERE id=$1`, c.TenantID, c.GroupID, rev, c.OperationID)
				return e
			}
		}
		return w, nil
	}})
	if e == nil {
		out = PricingOperationResult[AssignmentView]{r.Operation, *r.Result.(*AssignmentView), replay}
	}
	return out, e
}
func pricingSameID(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
