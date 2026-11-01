package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type pricingCursor struct {
	V            int     `json:"v"`
	Endpoint     string  `json:"endpoint"`
	Actor        string  `json:"actor"`
	Scope        string  `json:"scope"`
	Target       string  `json:"target"`
	CreatedAt    string  `json:"created_at,omitempty"`
	Name         *string `json:"name,omitempty"`
	ID           string  `json:"id"`
	FilterDigest string  `json:"filter_digest"`
}

func pricingCursorDigest(endpoint, actor, scope, target string) string {
	empty := ""
	sum := sha256.Sum256(pricingFrame("moni-aigate/pricing-cursor/v1", &endpoint, &actor, &scope, &target, &empty))
	return hex.EncodeToString(sum[:])
}
func pricingCursorFor(endpoint, actor, scope, target string) pricingCursor {
	return pricingCursor{V: 1, Endpoint: endpoint, Actor: actor, Scope: scope, Target: target, FilterDigest: pricingCursorDigest(endpoint, actor, scope, target)}
}
func strictPricingObject(raw []byte, keys ...string) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, e := dec.Token()
	if e != nil || t != json.Delim('{') {
		return nil, false
	}
	seen := map[string]json.RawMessage{}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for dec.More() {
		t, e = dec.Token()
		if e != nil {
			return nil, false
		}
		k, ok := t.(string)
		if !ok || !allowed[k] {
			return nil, false
		}
		if _, ok = seen[k]; ok {
			return nil, false
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return nil, false
		}
		seen[k] = v
	}
	if t, e = dec.Token(); e != nil || t != json.Delim('}') {
		return nil, false
	}
	if dec.Decode(new(any)) != io.EOF || len(seen) != len(keys) {
		return nil, false
	}
	return seen, true
}
func parsePricingCursor(raw string, want pricingCursor, preview bool) (*pricingCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 1024 {
		return nil, pricingFail("invalid_cursor")
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil || len(b) > 768 || !utf8.Valid(b) || base64.RawURLEncoding.EncodeToString(b) != raw {
		return nil, pricingFail("invalid_cursor")
	}
	keys := []string{"v", "endpoint", "actor", "scope", "target", "id", "filter_digest"}
	if preview {
		keys = append(keys, "name")
	} else {
		keys = append(keys, "created_at")
	}
	obj, ok := strictPricingObject(b, keys...)
	if !ok {
		return nil, pricingFail("invalid_cursor")
	}
	for _, v := range obj {
		if bytes.Equal(v, []byte("null")) {
			return nil, pricingFail("invalid_cursor")
		}
	}
	var c pricingCursor
	if json.Unmarshal(b, &c) != nil || c.V != 1 || c.Endpoint != want.Endpoint || c.Actor != want.Actor || c.Scope != want.Scope || c.Target != want.Target || c.FilterDigest != want.FilterDigest || !pricingID(c.ID) {
		return nil, pricingFail("invalid_cursor")
	}
	if preview {
		if c.Name == nil || !validPublicName(*c.Name) {
			return nil, pricingFail("invalid_cursor")
		}
	} else {
		t, e := time.Parse(pricingTimeLayout, c.CreatedAt)
		if e != nil || t.IsZero() || pricingStamp(t) != c.CreatedAt {
			return nil, pricingFail("invalid_cursor")
		}
	}
	return &c, nil
}
func encodePricingCursor(c pricingCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func pricingGroupRow(row pgx.Row) (GroupView, error) {
	var v GroupView
	e := row.Scan(&v.ID, &v.Label, &v.State, &v.Revision, &v.CreatedAt)
	return v, e
}
func (s *Store) pricingRead(ctx context.Context, actor string, fn func(context.Context, pgx.Tx) error) error {
	if !pricingID(actor) {
		return pricingFail("forbidden")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return pricingDBError(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='2s'; SET LOCAL lock_timeout='2s'`); e != nil {
		return pricingDBError(e)
	}
	var allowed bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND platform_role IN ('admin','finance_operator','finance_approver'))`, actor).Scan(&allowed); e != nil {
		return pricingDBError(e)
	}
	if !allowed {
		return pricingFail("forbidden")
	}
	if e = fn(ctx, tx); e != nil {
		return pricingDBError(e)
	}
	return pricingDBError(tx.Commit(ctx))
}
func (s *Store) GetPricingOperation(ctx context.Context, actorID, operationID string) (PricingOperationReceipt, error) {
	var r PricingOperationReceipt
	if !pricingID(operationID) {
		return r, pricingFail("invalid_request")
	}
	e := s.pricingRead(ctx, actorID, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		r, e = scanPricingOperation(tx.QueryRow(ctx, pricingOperationSQL, operationID))
		return e
	})
	return r, e
}
func (s *Store) PriceGroup(ctx context.Context, actor, id string) (GroupView, error) {
	var r GroupView
	if !pricingID(id) {
		return r, pricingFail("invalid_request")
	}
	e := s.pricingRead(ctx, actor, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		r, e = pricingGroupRow(tx.QueryRow(ctx, `SELECT id,label,state,revision::text,pricing_time(created_at) FROM price_groups WHERE id=$1`, id))
		return e
	})
	return r, e
}
func (s *Store) PriceGroups(ctx context.Context, actor, rawCursor string) (PricingPage[GroupView], error) {
	out := PricingPage[GroupView]{Data: []GroupView{}}
	base := pricingCursorFor("groups", actor, "platform", "/api/platform/price-groups")
	c, e := parsePricingCursor(rawCursor, base, false)
	if e != nil {
		return out, e
	}
	e = s.pricingRead(ctx, actor, func(ctx context.Context, tx pgx.Tx) error {
		var at, id any
		if c != nil {
			at = c.CreatedAt
			id = c.ID
		}
		rows, e := tx.Query(ctx, `SELECT id,label,state,revision::text,pricing_time(created_at) FROM price_groups WHERE ($1::timestamptz IS NULL OR (created_at,id)<($1::timestamptz,$2::text)) ORDER BY created_at DESC,id DESC LIMIT 51`, at, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v, e := pricingGroupRow(rows)
			if e != nil {
				return e
			}
			out.Data = append(out.Data, v)
		}
		return rows.Err()
	})
	if e != nil {
		return PricingPage[GroupView]{}, e
	}
	if len(out.Data) > 50 {
		out.Data = out.Data[:50]
		last := out.Data[49]
		base.CreatedAt = last.CreatedAt
		base.ID = last.ID
		out.NextCursor = encodePricingCursor(base)
	}
	return out, nil
}
func (s *Store) GroupPrices(ctx context.Context, actor, group, rawCursor string) (PricingPage[GroupPriceView], error) {
	out := PricingPage[GroupPriceView]{Data: []GroupPriceView{}}
	if !pricingID(group) {
		return out, pricingFail("invalid_request")
	}
	base := pricingCursorFor("history", actor, "platform", group)
	c, e := parsePricingCursor(rawCursor, base, false)
	if e != nil {
		return out, e
	}
	e = s.pricingRead(ctx, actor, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_groups WHERE id=$1)`, group).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return pgx.ErrNoRows
		}
		var at, id any
		if c != nil {
			at = c.CreatedAt
			id = c.ID
		}
		rows, e := tx.Query(ctx, `SELECT id,model_id,price_group_id,currency,input_rate::text,output_rate::text,pricing_time(effective_at),pricing_time(created_at) FROM price_versions WHERE price_group_id=$1 AND ($2::timestamptz IS NULL OR (created_at,id)<($2::timestamptz,$3::text)) ORDER BY created_at DESC,id DESC LIMIT 51`, group, at, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v := GroupPriceView{}
			if e = rows.Scan(&v.ID, &v.ModelID, &v.PriceGroupID, &v.Currency, &v.InputRate, &v.OutputRate, &v.EffectiveAt, &v.CreatedAt); e != nil {
				return e
			}
			out.Data = append(out.Data, v)
		}
		return rows.Err()
	})
	if e != nil {
		return PricingPage[GroupPriceView]{}, e
	}
	if len(out.Data) > 50 {
		out.Data = out.Data[:50]
		last := out.Data[49]
		base.CreatedAt = last.CreatedAt
		base.ID = last.ID
		out.NextCursor = encodePricingCursor(base)
	}
	return out, nil
}
func (s *Store) TenantPricing(ctx context.Context, actor, tenant string) (AssignmentView, error) {
	var v AssignmentView
	if !pricingID(tenant) {
		return v, pricingFail("invalid_request")
	}
	e := s.pricingRead(ctx, actor, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,price_group_id,pricing_revision::text FROM tenants WHERE id=$1`, tenant).Scan(&v.TenantID, &v.PriceGroupID, &v.PricingRevision)
	})
	return v, e
}
func (s *Store) OwnTenantPricing(ctx context.Context, actor, tenant string) (AssignmentView, error) {
	var v AssignmentView
	e := s.pricingCustomerRead(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT t.id,t.price_group_id,t.pricing_revision::text FROM tenants t JOIN memberships mb ON mb.tenant_id=t.id AND mb.user_id=$1 JOIN users u ON u.id=mb.user_id WHERE t.id=$2 AND t.status='active' AND mb.status='active' AND mb.role IN ('owner','billing_viewer') AND u.status='active'`, actor, tenant).Scan(&v.TenantID, &v.PriceGroupID, &v.PricingRevision)
	})
	if errors.Is(e, pgx.ErrNoRows) {
		return v, pricingFail("forbidden")
	}
	return v, pricingDBError(e)
}

const retailPriceProjection = `jsonb_build_object('model_id',m.id,'source',CASE WHEN t.price_group_id IS NULL THEN 'default' ELSE 'group' END,'status',CASE WHEN p.id IS NULL THEN 'unavailable' ELSE 'available' END,'price_group_id',t.price_group_id,'price_version_id',p.id,'currency','CNY','input_rate',p.input_rate::text,'output_rate',p.output_rate::text,'effective_at',pricing_time(p.effective_at),'as_of',pricing_time(instant.at))`

func (s *Store) RetailPrice(ctx context.Context, actor, tenant, model string) (RetailPriceView, error) {
	var v RetailPriceView
	if !pricingID(model) {
		return v, pricingFail("invalid_request")
	}
	var raw []byte
	e := s.pricingCustomerRead(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at) SELECT `+retailPriceProjection+` FROM instant CROSS JOIN tenants t JOIN memberships mb ON mb.tenant_id=t.id AND mb.user_id=$1 JOIN users u ON u.id=mb.user_id JOIN tenant_model_grants g ON g.tenant_id=t.id JOIN models m ON m.id=g.model_id LEFT JOIN LATERAL (SELECT id,input_rate,output_rate,effective_at FROM price_versions WHERE model_id=m.id AND price_group_id IS NOT DISTINCT FROM t.price_group_id AND effective_at<=instant.at ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1)p ON true WHERE t.id=$2 AND m.id=$3 AND t.status='active' AND mb.status='active' AND u.status='active' AND m.enabled AND (mb.allow_all_models OR EXISTS(SELECT 1 FROM member_model_grants mg WHERE mg.tenant_id=t.id AND mg.user_id=$1 AND mg.model_id=m.id))`, actor, tenant, model).Scan(&raw)
	})
	if e == nil {
		e = json.Unmarshal(raw, &v)
	}
	return v, pricingDBError(e)
}
func (s *Store) PreviewTenantPricing(ctx context.Context, actor, tenant string, group *string, rawCursor string) (PricingPreview, error) {
	out := PricingPreview{PricingPage: PricingPage[PricingPreviewModel]{Data: []PricingPreviewModel{}}}
	if !pricingID(tenant) || group != nil && !pricingID(*group) {
		return out, pricingFail("invalid_request")
	}
	target := "default"
	if group != nil {
		target = *group
	}
	base := pricingCursorFor("preview", actor, tenant, target)
	c, e := parsePricingCursor(rawCursor, base, true)
	if e != nil {
		return out, e
	}
	e = s.pricingRead(ctx, actor, func(ctx context.Context, tx pgx.Tx) error {
		var name, id any
		if c != nil {
			name = *c.Name
			id = c.ID
		}
		var contextJSON, modelsJSON []byte
		e := tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at), context AS MATERIALIZED (
   SELECT t.id,t.status,t.price_group_id,t.pricing_revision,g.id AS target_id,g.state AS target_state,instant.at FROM tenants t CROSS JOIN instant LEFT JOIN price_groups g ON g.id=$3 WHERE t.id=$2 AND ($3::text IS NULL OR g.id IS NOT NULL) AND EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND platform_role IN ('admin','finance_operator','finance_approver'))
  ), page AS (
   SELECT m.id,m.public_name,coalesce(nullif(m.profile->>'protocol',''),'openai_chat') AS protocol,p.id AS price_id,p.input_rate,p.output_rate,p.effective_at FROM context c JOIN tenant_model_grants g ON g.tenant_id=c.id JOIN models m ON m.id=g.model_id LEFT JOIN LATERAL (SELECT id,input_rate,output_rate,effective_at FROM price_versions WHERE model_id=m.id AND price_group_id IS NOT DISTINCT FROM $3::text AND effective_at<=c.at ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1)p ON true WHERE m.enabled AND ($4::text IS NULL OR (m.public_name COLLATE "C",m.id)>($4::text COLLATE "C",$5::text)) ORDER BY m.public_name COLLATE "C",m.id LIMIT 51
  ) SELECT jsonb_build_object('tenant_id',c.id,'tenant_status',c.status,'current_price_group_id',c.price_group_id,'current_pricing_revision',c.pricing_revision::text,'target_price_group_id',$3::text,'target_group_state',c.target_state,'assignable',($3::text IS NULL OR c.target_state='active'),'as_of',pricing_time(c.at)),coalesce((SELECT jsonb_agg(jsonb_build_object('model_id',p.id,'public_name',p.public_name,'protocol',p.protocol,'retail_price',jsonb_build_object('model_id',p.id,'source',CASE WHEN $3::text IS NULL THEN 'default' ELSE 'group' END,'status',CASE WHEN p.price_id IS NULL THEN 'unavailable' ELSE 'available' END,'price_group_id',$3::text,'price_version_id',p.price_id,'currency','CNY','input_rate',p.input_rate::text,'output_rate',p.output_rate::text,'effective_at',pricing_time(p.effective_at),'as_of',pricing_time(c.at))) ORDER BY p.public_name COLLATE "C",p.id) FROM page p),'[]'::jsonb) FROM context c`, actor, tenant, group, name, id).Scan(&contextJSON, &modelsJSON)
		if e != nil {
			return e
		}
		if json.Unmarshal(contextJSON, &out.Context) != nil || json.Unmarshal(modelsJSON, &out.Data) != nil {
			return pricingFail("pricing_invariant")
		}
		return nil
	})
	if e != nil {
		return PricingPreview{}, e
	}
	if len(out.Data) > 50 {
		out.Data = out.Data[:50]
		last := out.Data[49]
		base.Name = &last.PublicName
		base.ID = last.ModelID
		out.NextCursor = encodePricingCursor(base)
	}
	return out, nil
}

// The only data SELECT in these customer reads includes all current tenant,
// membership, user and model authorization in its own statement snapshot.
func (s *Store) pricingCustomerRead(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='2s'; SET LOCAL lock_timeout='2s'`); e != nil {
		return e
	}
	if e = fn(ctx, tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
