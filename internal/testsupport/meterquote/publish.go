package meterquote

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

type DefaultPrice struct {
	ID, ModelID, Actor    string
	InputRate, OutputRate int64
	EffectiveAt           time.Time
}
type CostPrice struct {
	ID, ChannelID, UpstreamModel, ModelID, SchemaID, Actor, Basis string
	InputRate, OutputRate                                         *int64
	EffectiveAt                                                   time.Time
}
type Beginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func PublishDefaultPrice(t testing.TB, ctx context.Context, db Beginner, p DefaultPrice) string {
	t.Helper()
	id, e := tryDefault(ctx, db, p)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TryPublishDefaultPrice(ctx context.Context, db Beginner, p DefaultPrice) (string, error) {
	return tryDefault(ctx, db, p)
}
func PublishCost(t testing.TB, ctx context.Context, db Beginner, p CostPrice) string {
	t.Helper()
	id, e := tryCost(ctx, db, p)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TryPublishCost(ctx context.Context, db Beginner, p CostPrice) (string, error) {
	return tryCost(ctx, db, p)
}
func tryDefault(ctx context.Context, db Beginner, p DefaultPrice) (string, error) {
	tx, e := db.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	var sid string
	var schemaRaw, digest []byte
	if e = tx.QueryRow(ctx, `SELECT c.schema_id,s.canonical,s.schema_digest FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id JOIN meter_schemas s ON s.id=c.schema_id AND s.schema_digest=c.schema_digest WHERE m.id=$1`, p.ModelID).Scan(&sid, &schemaRaw, &digest); e != nil {
		return "", e
	}
	schema, e := metering.ParseSchema(schemaRaw)
	if e != nil {
		return "", e
	}
	rates, e := metering.ManagementTokenRates(schema, metering.Retail, p.InputRate, p.OutputRate)
	if e != nil {
		return "", e
	}
	raw, _ := rates.CanonicalJSON()
	at := p.EffectiveAt
	if at.IsZero() {
		if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); e != nil {
			return "", e
		}
	}
	at = at.UTC().Truncate(time.Microsecond)
	sd := hex.EncodeToString(digest)
	safe := map[string]any{"model_id": p.ModelID, "schema_id": sid, "schema_digest": sd, "variant_key": "default", "currency": "COIN", "rates": json.RawMessage(raw), "effective_at": at.Format("2006-01-02T15:04:05.000000Z")}
	result := map[string]any{"id": p.ID, "model_id": p.ModelID, "price_group_id": nil, "schema_id": sid, "schema_digest": sd, "variant_key": "default", "currency": "COIN", "rates": json.RawMessage(raw), "effective_at": at.Format("2006-01-02T15:04:05.000000Z"), "created_at": ""}
	in, _ := json.Marshal(safe)
	out, _ := json.Marshal(result)
	op := uuid.NewString()
	var created time.Time
	var snapshot []byte
	if e = tx.QueryRow(ctx, `INSERT INTO pricing_operations(operation_id,kind,actor,scope,target,resource_id,safe_input,input_fingerprint,result_snapshot) VALUES($1,'default_price.publish',$2,'platform','/api/platform/price-versions',$3,$4,repeat('0',64),$5) RETURNING created_at,result_snapshot`, op, p.Actor, p.ID, in, out).Scan(&created, &snapshot); e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, `SELECT meter_insert_token_price($1,$2,$3,$4,$5,$6,NULL,$7,$8)`, p.ID, p.ModelID, p.InputRate, p.OutputRate, at, p.Actor, op, created); e != nil {
		return "", e
	}
	diff, _ := json.Marshal(map[string]any{"operation_id": op, "target": "/api/platform/price-versions", "after": json.RawMessage(snapshot)})
	if _, e = tx.Exec(ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,'pricing.price.publish','/api/platform/price-versions',$2)`, p.Actor, diff); e != nil {
		return "", e
	}
	return p.ID, tx.Commit(ctx)
}
func uintString(n int64) string { return strconv.FormatInt(n, 10) }

func tryCost(ctx context.Context, db Beginner, p CostPrice) (string, error) {
	tx, e := db.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	var rev int64
	if e = tx.QueryRow(ctx, `SELECT management_revision FROM channels WHERE id=$1 FOR UPDATE`, p.ChannelID).Scan(&rev); e != nil {
		return "", e
	}
	if p.SchemaID == "" {
		if e = tx.QueryRow(ctx, `SELECT c.schema_id FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=$1`, p.ModelID).Scan(&p.SchemaID); e != nil {
			return "", e
		}
	}
	var canonical, digest []byte
	var schemaRaw []byte
	if e = tx.QueryRow(ctx, `SELECT canonical,schema_digest FROM meter_schemas WHERE id=$1`, p.SchemaID).Scan(&schemaRaw, &digest); e != nil {
		return "", e
	}
	schema, e := metering.ParseSchema(schemaRaw)
	if e != nil {
		return "", e
	}
	var rates any
	if p.Basis != "unknown" {
		r, e := metering.ManagementTokenRates(schema, metering.Supplier, *p.InputRate, *p.OutputRate)
		if e != nil {
			return "", e
		}
		canonical, _ = r.CanonicalJSON()
		rates = json.RawMessage(canonical)
	}
	at := p.EffectiveAt
	if at.IsZero() {
		if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); e != nil {
			return "", e
		}
	}
	at = at.UTC().Truncate(time.Microsecond)
	sd := hex.EncodeToString(digest)
	safe := map[string]any{"channel_id": p.ChannelID, "upstream_model": p.UpstreamModel, "schema_id": p.SchemaID, "schema_digest": sd, "variant_key": "default", "currency": "CNY", "basis": p.Basis, "rates": rates, "effective_at": at.Format("2006-01-02T15:04:05.000000Z"), "expected_channel_revision": uintString(rev)}
	result := map[string]any{"id": p.ID, "channel_id": p.ChannelID, "upstream_model": p.UpstreamModel, "schema_id": p.SchemaID, "schema_digest": sd, "variant_key": "default", "currency": "CNY", "basis": p.Basis, "rates": rates, "effective_at": at.Format("2006-01-02T15:04:05.000000Z"), "created_at": "", "expected_channel_revision": uintString(rev), "channel_revision": uintString(rev + 1)}
	in, _ := json.Marshal(safe)
	out, _ := json.Marshal(result)
	op := uuid.NewString()
	var created time.Time
	var snapshot []byte
	if e = tx.QueryRow(ctx, `INSERT INTO pricing_operations(operation_id,kind,actor,scope,target,resource_id,safe_input,input_fingerprint,result_snapshot) VALUES($1,'cost_price.publish',$2,'platform','/api/platform/cost-versions',$3,$4,repeat('0',64),$5) RETURNING created_at,result_snapshot`, op, p.Actor, p.ID, in, out).Scan(&created, &snapshot); e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, `SELECT meter_insert_token_cost($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, p.ID, p.ChannelID, p.UpstreamModel, p.SchemaID, p.InputRate, p.OutputRate, p.Basis, at, p.Actor, op, created); e != nil {
		return "", e
	}
	diff, _ := json.Marshal(map[string]any{"operation_id": op, "target": "/api/platform/cost-versions", "after": json.RawMessage(snapshot)})
	if _, e = tx.Exec(ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,'pricing.cost.publish','/api/platform/cost-versions',$2)`, p.Actor, diff); e != nil {
		return "", e
	}
	return p.ID, tx.Commit(ctx)
}
