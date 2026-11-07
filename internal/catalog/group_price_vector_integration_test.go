package catalog_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func groupVectorCommand(t *testing.T, f *publicFixture) catalog.GroupPriceCommand {
	t.Helper()
	g, err := f.store.CreatePriceGroup(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "vector-group"})
	if err != nil {
		t.Fatal(err)
	}
	return meterquote.BindGroup(t, f.ctx, f.owner, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: g.Result.ID, ModelID: f.model, InputRate: 250000, OutputRate: 500000, EffectiveAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)})
}

func TestGroupPriceVectorApplicationReplayAndConcurrency(t *testing.T) {
	f := publicDB(t)
	c := groupVectorCommand(t, f)
	if _, err := f.owner.Exec(f.ctx, `CREATE OR REPLACE FUNCTION meter_price_million(price_id text,dimension text) RETURNS bigint LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'scalar price disabled'; END $$; CREATE OR REPLACE FUNCTION meter_cost_million(cost_id text,dimension text) RETURNS bigint LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'scalar cost disabled'; END $$`); err != nil {
		t.Fatal(err)
	}
	var user string
	var super bool
	if err := f.app.QueryRow(f.ctx, `SELECT current_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&user, &super); err != nil || user != f.appRole || super {
		t.Fatal(user, super, err)
	}
	actor := catalog.PricingActor{UserID: f.admin}
	var wg sync.WaitGroup
	results := make(chan catalog.PricingOperationResult[catalog.GroupPriceView], 6)
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() { defer wg.Done(); v, e := f.store.PublishGroupPrice(f.ctx, actor, c); results <- v; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var original catalog.PricingOperationResult[catalog.GroupPriceView]
	first := 0
	for v := range results {
		if original.Result.ID != "" && !reflect.DeepEqual(original.Result, v.Result) {
			t.Fatal("changed replay", original, v)
		}
		original = v
		if !v.Replayed {
			first++
		}
	}
	if first != 1 {
		t.Fatal("not one publisher", first)
	}
	var safe json.RawMessage
	var sqlFingerprint string
	var prices, audits int
	if err := f.owner.QueryRow(f.ctx, `SELECT safe_input,pricing_fingerprint(kind,actor,scope,target,safe_input),(SELECT count(*) FROM price_versions WHERE pricing_operation_id=$1),(SELECT count(*) FROM audit_events WHERE sanitized_diff->>'operation_id'=$1) FROM pricing_operations WHERE operation_id=$1`, c.OperationID).Scan(&safe, &sqlFingerprint, &prices, &audits); err != nil || prices != 1 || audits != 1 || sqlFingerprint != original.Operation.InputFingerprint {
		t.Fatal(prices, audits, sqlFingerprint, err)
	}
	var keys map[string]any
	if json.Unmarshal(safe, &keys) != nil || len(keys) != 7 || keys["input_rate"] != nil || keys["output_rate"] != nil || keys["rates"] == nil {
		t.Fatal(string(safe))
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE models SET enabled=false WHERE id=$1`, f.model); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdatePriceGroup(f.ctx, actor, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: c.GroupID, ExpectedRevision: 1, Label: "retired", State: "retired"}); err != nil {
		t.Fatal(err)
	}
	replay, err := f.store.PublishGroupPrice(f.ctx, actor, c)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(original.Result, replay.Result) {
		t.Fatal(replay, err)
	}
	for name, change := range map[string]func(*catalog.GroupPriceCommand){"rate": func(c *catalog.GroupPriceCommand) { c.InputRate++ }, "digest": func(c *catalog.GroupPriceCommand) { c.SchemaDigest = strings.Repeat("00", 32) }, "model": func(c *catalog.GroupPriceCommand) { c.ModelID = uuid.NewString() }, "time": func(c *catalog.GroupPriceCommand) { c.EffectiveAt = c.EffectiveAt.Add(time.Microsecond) }, "target": func(c *catalog.GroupPriceCommand) { c.GroupID = uuid.NewString() }} {
		t.Run(name, func(t *testing.T) {
			other := c
			change(&other)
			_, err := f.store.PublishGroupPrice(f.ctx, actor, other)
			pricingCode(t, err, "policy_operation_conflict")
		})
	}
	receipt, err := f.store.GetPricingOperation(f.ctx, f.finance, c.OperationID)
	if err != nil || !reflect.DeepEqual(*receipt.Result.(*catalog.GroupPriceView), original.Result) {
		t.Fatal(receipt, err)
	}
	page, err := f.store.GroupPrices(f.ctx, f.finance, c.GroupID, "")
	if err != nil || len(page.Data) != 1 || !reflect.DeepEqual(page.Data[0], original.Result) {
		t.Fatal(page, err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE users SET platform_role='none' WHERE id=$1`, f.admin); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.PublishGroupPrice(f.ctx, actor, c)
	pricingCode(t, err, "forbidden")
}

func TestGroupPriceVectorFailuresRollback(t *testing.T) {
	f := publicDB(t)
	base := groupVectorCommand(t, f)
	actor := catalog.PricingActor{UserID: f.admin}
	for name, change := range map[string]func(*catalog.GroupPriceCommand){"missing_domain": func(c *catalog.GroupPriceCommand) { c.SchemaID = "" }, "foreign_schema": func(c *catalog.GroupPriceCommand) { c.SchemaID = uuid.NewString() }, "wrong_digest": func(c *catalog.GroupPriceCommand) { c.SchemaDigest = strings.Repeat("00", 32) }, "wrong_variant": func(c *catalog.GroupPriceCommand) { c.VariantKey = "other" }, "sub_microsecond": func(c *catalog.GroupPriceCommand) { c.EffectiveAt = c.EffectiveAt.Add(time.Nanosecond) }} {
		t.Run(name, func(t *testing.T) {
			c := base
			c.OperationID = uuid.NewString()
			change(&c)
			if _, err := f.store.PublishGroupPrice(f.ctx, actor, c); err == nil {
				t.Fatal("accepted invalid publication")
			}
			groupVectorNoRows(t, f, c.OperationID)
		})
	}
	// The schema max is small in the normal fixture; use a real larger profile
	// to exercise int64 overflow without altering its immutable schema.
	body := publicProfileJSON(t, "overflow-model", "openai_chat")
	var p map[string]any
	if json.Unmarshal(body, &p) != nil {
		t.Fatal("profile")
	}
	p["input_token_upper_bound"] = int64(2000000)
	p["context_limit"] = int64(2000020)
	body, _ = json.Marshal(p)
	model := f.modelWithProfile(t, "overflow-model", body, false)
	c := meterquote.BindGroup(t, f.ctx, f.owner, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: base.GroupID, ModelID: model, InputRate: math.MaxInt64, EffectiveAt: base.EffectiveAt})
	_, err := f.store.PublishGroupPrice(f.ctx, actor, c)
	pricingCode(t, err, "invalid_price")
	groupVectorNoRows(t, f, c.OperationID)
	inputOnly := f.modelWith(t, "input-only", "openai_embeddings", false)
	c = meterquote.BindGroup(t, f.ctx, f.owner, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: base.GroupID, ModelID: inputOnly, OutputRate: 1, EffectiveAt: base.EffectiveAt})
	_, err = f.store.PublishGroupPrice(f.ctx, actor, c)
	pricingCode(t, err, "invalid_price")
	groupVectorNoRows(t, f, c.OperationID)
	if _, err = f.owner.Exec(f.ctx, `CREATE FUNCTION group_vector_audit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='pricing.price.publish' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER group_vector_audit_fail BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION group_vector_audit_fail()`); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.PublishGroupPrice(f.ctx, actor, base)
	pricingCode(t, err, "pricing_invariant")
	groupVectorNoRows(t, f, base.OperationID)
	if _, err = f.owner.Exec(f.ctx, `DROP TRIGGER group_vector_audit_fail ON audit_events`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.PublishGroupPrice(f.ctx, actor, base); err != nil {
		t.Fatal("same operation after definite rollback", err)
	}
}

func groupVectorNoRows(t *testing.T, f *publicFixture, op string) {
	t.Helper()
	var count int
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM pricing_operations WHERE operation_id=$1)+(SELECT count(*) FROM price_versions WHERE pricing_operation_id=$1)+(SELECT count(*) FROM audit_events WHERE sanitized_diff->>'operation_id'=$1)+(SELECT count(*) FROM price_rate_lines l JOIN price_versions p ON p.id=l.price_version_id WHERE p.pricing_operation_id=$1)`, op).Scan(&count); err != nil || count != 0 {
		t.Fatal("publication leaked rows", count, err)
	}
}

func TestGroupPriceVectorRawCommitRejectsGaps(t *testing.T) {
	f := publicDB(t)
	base := groupVectorCommand(t, f)
	other, err := f.store.CreatePriceGroup(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "vector-other-group"})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing_audit", "wrong_audit", "duplicate_audit", "wrong_safe_rate", "wrong_schema", "non_reduced", "overflow", "cross_target"} {
		t.Run(kind, func(t *testing.T) {
			c := base
			c.OperationID = uuid.NewString()
			id := uuid.NewString()
			target := "/api/platform/price-groups/" + c.GroupID + "/price-versions"
			if kind == "cross_target" {
				target = "/api/platform/price-groups/" + other.Result.ID + "/price-versions"
			}
			rates := map[string]any{"schema_id": c.SchemaID, "version": "1", "side": "retail", "currency": "CNY", "rates": []any{map[string]any{"dimension": "text_input_token", "unit": "token", "numerator": "1", "denominator": "4"}, map[string]any{"dimension": "text_output_token", "unit": "token", "numerator": "1", "denominator": "2"}}}
			if kind == "overflow" {
				var profile map[string]any
				if json.Unmarshal(publicProfileJSON(t, "sql-overflow", "openai_chat"), &profile) != nil {
					t.Fatal("profile")
				}
				profile["input_token_upper_bound"], profile["context_limit"] = int64(2000000), int64(2000020)
				body, _ := json.Marshal(profile)
				c.ModelID = f.modelWithProfile(t, "sql-overflow", body, false)
				c = meterquote.BindGroup(t, f.ctx, f.owner, c)
				c.InputRate, c.OutputRate = math.MaxInt64, 0
				rates["schema_id"] = c.SchemaID
				rates["rates"].([]any)[0].(map[string]any)["numerator"] = "9223372036854775807"
				rates["rates"].([]any)[0].(map[string]any)["denominator"] = "1000000"
				rates["rates"].([]any)[1].(map[string]any)["numerator"] = "0"
				rates["rates"].([]any)[1].(map[string]any)["denominator"] = "1"
			}
			if kind == "wrong_safe_rate" {
				rates["rates"].([]any)[0].(map[string]any)["numerator"] = "3"
			}
			if kind == "non_reduced" {
				rates["rates"].([]any)[0].(map[string]any)["numerator"] = "2"
				rates["rates"].([]any)[0].(map[string]any)["denominator"] = "8"
			}
			if kind == "wrong_schema" {
				c.SchemaDigest = strings.Repeat("00", 32)
			}
			safe := map[string]any{"model_id": c.ModelID, "schema_id": c.SchemaID, "schema_digest": c.SchemaDigest, "variant_key": "default", "currency": "CNY", "rates": rates, "effective_at": c.EffectiveAt.Format("2006-01-02T15:04:05.000000Z")}
			result := map[string]any{}
			for k, v := range safe {
				result[k] = v
			}
			result["id"] = id
			result["price_group_id"] = c.GroupID
			result["created_at"] = "2000-01-01T00:00:00.000000Z"
			raw, _ := json.Marshal(safe)
			res, _ := json.Marshal(result)
			tx, err := f.app.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			var at time.Time
			err = tx.QueryRow(f.ctx, `INSERT INTO pricing_operations(operation_id,kind,actor,scope,target,resource_id,safe_input,input_fingerprint,result_snapshot) VALUES($1,'group_price.publish',$2,'platform',$3,$4,$5,'invalid',$6) RETURNING created_at`, c.OperationID, f.admin, target, id, raw, res).Scan(&at)
			if err == nil {
				_, err = tx.Exec(f.ctx, `SELECT meter_insert_token_price($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, c.ModelID, c.InputRate, c.OutputRate, c.EffectiveAt, f.admin, c.GroupID, c.OperationID, at)
			}
			if err == nil && kind != "missing_audit" {
				var after json.RawMessage
				err = tx.QueryRow(f.ctx, `SELECT result_snapshot FROM pricing_operations WHERE operation_id=$1`, c.OperationID).Scan(&after)
				diff, _ := json.Marshal(map[string]any{"operation_id": c.OperationID, "target": target, "after": after})
				actor := f.admin
				if kind == "wrong_audit" {
					actor = f.finance
				}
				if err == nil {
					_, err = tx.Exec(f.ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,'pricing.price.publish',$2,$3)`, actor, target, diff)
				}
				if err == nil && kind == "duplicate_audit" {
					_, err = tx.Exec(f.ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,'pricing.price.publish',$2,$3)`, actor, target, diff)
				}
			}
			if err == nil {
				err = tx.Commit(f.ctx)
			} else {
				_ = tx.Rollback(f.ctx)
			}
			if err == nil {
				t.Fatal("raw gap committed")
			}
			want := map[string]string{"missing_audit": "group price audit mismatch", "wrong_audit": "group price audit mismatch", "duplicate_audit": "group price audit mismatch", "wrong_safe_rate": "group price result mismatch", "wrong_schema": "group price result mismatch", "non_reduced": "text rate not reduced", "overflow": "group price maximum amount overflow", "cross_target": "group price result mismatch"}[kind]
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted %s, got %v", want, err)
			}
			groupVectorNoRows(t, f, c.OperationID)
		})
	}
	// Same framing golden value as independent Go/TS tests, not JSONB text.
	var fp string
	err = f.app.QueryRow(f.ctx, `SELECT pricing_fingerprint('group_price.publish','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','platform','/api/platform/price-groups/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/price-versions',$1::jsonb)`, `{"model_id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","schema_id":"10000000-0000-4000-8000-000000000001","schema_digest":"`+strings.Repeat("ab", 32)+`","variant_key":"default","currency":"CNY","rates":{"schema_id":"10000000-0000-4000-8000-000000000001","version":"1","side":"retail","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"9007199254740993","denominator":"1000000"},{"dimension":"text_output_token","unit":"token","numerator":"0","denominator":"1"}]},"effective_at":"2026-10-01T00:00:00.123456Z"}`).Scan(&fp)
	if err != nil || fp != "14aac71c989772d94efbda8faf1a39a758a618b645a71539a55de7664de26564" {
		t.Fatal(fp, err)
	}
	// An audit appended in a later transaction cannot borrow the original op.
	valid, err := f.store.PublishGroupPrice(f.ctx, catalog.PricingActor{UserID: f.admin}, base)
	if err != nil {
		t.Fatal(err)
	}
	diff, _ := json.Marshal(map[string]any{"operation_id": base.OperationID, "target": valid.Operation.Target, "after": valid.Result})
	if _, err = f.app.Exec(f.ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,'pricing.price.publish',$2,$3)`, f.admin, valid.Operation.Target, diff); err == nil {
		t.Fatal("late audit accepted")
	}
	var connRole bool
	if err = f.owner.QueryRow(f.ctx, `SELECT has_function_privilege($1,'meter_text_rates_canonical(jsonb,text)','EXECUTE')`, f.projector).Scan(&connRole); err != nil || connRole {
		t.Fatal("projection execute", connRole, err)
	}
	for _, signature := range []string{"meter_text_rates_canonical(jsonb,text)", "meter_group_price_complete(text)", "meter_group_price_audit(text)", "meter_group_price_audit_insert()"} {
		var invoker, pinned, app, projector, public bool
		err = f.owner.QueryRow(f.ctx, `SELECT NOT p.prosecdef,p.proconfig=ARRAY['search_path='||quote_ident(current_schema())||', pg_catalog, pg_temp'],has_function_privilege($1,p.oid,'EXECUTE'),has_function_privilege($2,p.oid,'EXECUTE'),EXISTS(SELECT 1 FROM aclexplode(p.proacl) acl WHERE acl.grantee=0 AND acl.privilege_type='EXECUTE') FROM pg_proc p WHERE p.oid=$3::regprocedure`, f.appRole, f.projector, signature).Scan(&invoker, &pinned, &app, &projector, &public)
		if err != nil || !invoker || !pinned || !app || projector || public {
			t.Fatal(signature, invoker, pinned, app, projector, public, err)
		}
	}
}

func TestGroupPriceVectorHistoryPagingAndZero(t *testing.T) {
	f := publicDB(t)
	c := groupVectorCommand(t, f)
	cfg := f.app.Config()
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	f.store = catalog.New(one, nil)
	ids := map[string]bool{}
	for i := range 52 {
		c.OperationID = uuid.NewString()
		c.InputRate = int64(i)
		c.OutputRate = 0
		c.EffectiveAt = c.EffectiveAt.Add(time.Microsecond)
		v, err := f.store.PublishGroupPrice(f.ctx, catalog.PricingActor{UserID: f.admin}, c)
		if err != nil {
			t.Fatal(err)
		}
		ids[v.Result.ID] = true
		if i == 0 && !strings.Contains(string(v.Result.Rates), `"numerator":"0","denominator":"1"`) {
			t.Fatal("zero price is not explicit", v)
		}
	}
	page, err := f.store.GroupPrices(f.ctx, f.finance, c.GroupID, "")
	if err != nil || len(page.Data) != 50 || page.NextCursor == "" {
		t.Fatal(len(page.Data), err)
	}
	for _, v := range page.Data {
		if !ids[v.ID] {
			t.Fatal("unknown or repeated row", v)
		}
		delete(ids, v.ID)
	}
	last, err := f.store.GroupPrices(f.ctx, f.finance, c.GroupID, page.NextCursor)
	if err != nil || len(last.Data) != 2 || last.NextCursor != "" {
		t.Fatal(last, err)
	}
	for _, v := range last.Data {
		if !ids[v.ID] {
			t.Fatal("unknown or repeated row", v)
		}
		delete(ids, v.ID)
	}
	if len(ids) != 0 {
		t.Fatal("history omitted rows", ids)
	}
	_, err = f.store.GroupPrices(f.ctx, f.admin, c.GroupID, page.NextCursor)
	pricingCode(t, err, "invalid_cursor")
	_, err = f.store.GroupPrices(f.ctx, f.finance, uuid.NewString(), page.NextCursor)
	pricingCode(t, err, "invalid_cursor")
}
