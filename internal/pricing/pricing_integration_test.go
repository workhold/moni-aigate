package pricing_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/pricing"
	"github.com/moni-ai/moni-aigate/migrations"
)

type fixture struct {
	t                                                       *testing.T
	ctx                                                     context.Context
	pool, owner                                             *pgxpool.Pool
	actor, tenant, model, key, legacy, defaultPrice, schema string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for actual tenant pricing PostgreSQL tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := "pricing_it_" + suffix
	appRole, projectionRole := "pricing_app_"+suffix[:12], "pricing_projection_"+suffix[:12]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		pool.Close()
		_, e := admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		for _, role := range []string{appRole, projectionRole} {
			_, _ = admin.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
			_, _ = admin.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize())
		}
		admin.Close()
		if e != nil {
			t.Errorf("cleanup schema: %v", e)
		}
	})
	// Seed a genuine 001-017 request before applying the new snapshot guard.
	// Record exact checksums so the production runner verifies retained migrations.
	if _, err = pool.Exec(ctx, `CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "018" {
			break
		}
		body, e := migrations.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(body)); e != nil {
			t.Fatalf("baseline %s: %v", name, e)
		}
		if _, e = pool.Exec(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, name, fmt.Sprintf("%x", sha256.Sum256(body))); e != nil {
			t.Fatal(e)
		}
	}
	f := &fixture{t: t, ctx: ctx, pool: pool, owner: pool, actor: uuid.NewString(), tenant: uuid.NewString(), model: uuid.NewString(), key: uuid.NewString(), legacy: uuid.NewString(), defaultPrice: uuid.NewString(), schema: schema}
	f.exec(`INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,'prototype@example.invalid','test-hash','admin')`, f.actor)
	f.exec(`INSERT INTO tenants(id,name) VALUES($1,'prototype')`, f.tenant)
	f.exec(`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, f.tenant, f.actor)
	f.exec(`INSERT INTO models(id,public_name,profile) VALUES($1,'prototype-model','{"protocol":"openai_chat","input_token_upper_bound":1000,"context_limit":1000,"output_limit":1000,"default_output_limit":1000}'::jsonb)`, f.model)
	f.exec(`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, f.tenant, f.model)
	f.exec(`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes) VALUES($1,$2,$3,'prototype','test','prototype-'||$1,ARRAY['*'])`, f.key, f.tenant, f.actor)
	f.exec(`INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,10,20,clock_timestamp()-interval '1 hour',$3)`, f.defaultPrice, f.model, f.actor)
	f.exec(requestInsert(false), f.legacy, f.tenant, f.key, f.actor, f.model, f.defaultPrice)
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal("production pricing upgrade:", err)
	}
	for _, role := range []string{appRole, projectionRole} {
		if _, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN NOINHERIT NOSUPERUSER"); err != nil {
			t.Fatal(err)
		}
	}
	if err = operations.ProvisionFor(ctx, pool, schema, appRole, projectionRole); err != nil {
		t.Fatal(err)
	}
	appCfg := pool.Config()
	appCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, e := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{appRole}.Sanitize())
		return e
	}
	app, err = pgxpool.NewWithConfig(ctx, appCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	f.pool = app
	t.Log("isolated schema:", schema)
	return f
}

func requestInsert(recorded bool) string {
	base := `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming`
	if recorded {
		return base + `,pricing_group_id,tenant_pricing_revision,retail_priced_at) VALUES($1,$2,$3,$4,$5,$6,10,20,1000,1000,'profile','policy','hmac',clock_timestamp()+interval '1 hour','prototype',false,NULL,1,clock_timestamp())`
	}
	return base + `) VALUES($1,$2,$3,$4,$5,$6,10,20,1000,1000,'profile','policy','hmac',clock_timestamp()+interval '1 hour','prototype',false)`
}
func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatal(err)
	}
}

// New metadata-only requests need their zero reservation in the same APP
// transaction after 020. This posts no money and does not test billing.Reserve.
func (f *fixture) metadataSource(request, sql string, args ...any) {
	f.t.Helper()
	tx := f.tx()
	mustExec(f.t, f.ctx, tx, sql, args...)
	mustExec(f.t, f.ctx, tx, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES(gen_random_uuid()::text,$1,$2,0,clock_timestamp()+interval '24 hours')`, f.tenant, request)
	mustCommit(f.t, f.ctx, tx)
}

func (f *fixture) tx() pgx.Tx {
	f.t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = tx.Rollback(f.ctx) })
	return tx
}
func mustExec(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
}
func mustCommit(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
func code(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code + ":" + pg.Message
	}
	return fmt.Sprint(err)
}

func (f *fixture) receipt(tx pgx.Tx, id, kind, scope, target, resource string, revision any, input, result string) (time.Time, error) {
	var at time.Time
	err := tx.QueryRow(f.ctx, `INSERT INTO pricing_operations(operation_id,kind,actor,scope,scope_tenant_id,target,resource_id,revision,safe_input,input_fingerprint,result_snapshot) VALUES($1,$2,$3,$4,CASE WHEN $4='platform' THEN NULL ELSE $4 END,$5,$6,$7,$8::jsonb,repeat('0',64),$9::jsonb) RETURNING created_at`, id, kind, f.actor, scope, target, resource, revision, input, result).Scan(&at)
	return at, err
}
func wire(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func (f *fixture) createGroup(label string) string {
	f.t.Helper()
	id, op := uuid.NewString(), uuid.NewString()
	tx := f.tx()
	at, err := f.receipt(tx, op, "price_group.create", "platform", "/api/platform/price-groups", id, int64(1), wire(map[string]any{"label": label}), wire(map[string]any{"id": id, "label": label, "state": "active", "revision": "1"}))
	if err != nil {
		f.t.Fatal(err)
	}
	mustExec(f.t, f.ctx, tx, `INSERT INTO price_groups(id,label,last_operation_id,created_at) VALUES($1,$2,$3,$4)`, id, label, op, at)
	mustCommit(f.t, f.ctx, tx)
	return id
}
func (f *fixture) assign(group *string, expected int64) {
	f.t.Helper()
	tx := f.tx()
	mustExec(f.t, f.ctx, tx, `SELECT id FROM tenants WHERE id=$1 FOR NO KEY UPDATE`, f.tenant)
	var old *string
	var rev int64
	if err := tx.QueryRow(f.ctx, `SELECT price_group_id,pricing_revision FROM tenants WHERE id=$1`, f.tenant).Scan(&old, &rev); err != nil {
		f.t.Fatal(err)
	}
	if rev != expected {
		f.t.Fatalf("unexpected fixture revision %d != %d", rev, expected)
	}
	different := (old == nil) != (group == nil) || (old != nil && group != nil && *old != *group)
	next := rev
	if different {
		next++
	}
	op := uuid.NewString()
	_, err := f.receipt(tx, op, "tenant_pricing.assign", f.tenant, "/api/platform/tenants/"+f.tenant+"/pricing", f.tenant, next, wire(map[string]any{"expected_revision": fmt.Sprint(expected), "price_group_id": group}), wire(map[string]any{"tenant_id": f.tenant, "price_group_id": group, "pricing_revision": fmt.Sprint(next)}))
	if err != nil {
		f.t.Fatal(err)
	}
	if different {
		mustExec(f.t, f.ctx, tx, `UPDATE tenants SET price_group_id=$2,pricing_revision=$3,pricing_operation_id=$4 WHERE id=$1`, f.tenant, group, next, op)
	}
	mustCommit(f.t, f.ctx, tx)
}

func TestPricingMigrationRetainsLegacyAndGuardsNewSnapshots(t *testing.T) {
	f := newFixture(t)
	var legacy bool
	if err := f.pool.QueryRow(f.ctx, `SELECT pricing_group_id IS NULL AND tenant_pricing_revision IS NULL AND retail_priced_at IS NULL FROM request_records WHERE id=$1`, f.legacy).Scan(&legacy); err != nil || !legacy {
		t.Fatal(legacy, err)
	}
	f.exec(`UPDATE request_records SET status='dispatching' WHERE id=$1`, f.legacy)
	for _, statement := range []string{
		`UPDATE request_records SET tenant_pricing_revision=1,retail_priced_at=clock_timestamp() WHERE id=$1`,
		`UPDATE request_records SET input_rate=11 WHERE id=$1`,
		`UPDATE request_records SET price_version_id=gen_random_uuid()::text WHERE id=$1`,
	} {
		if _, err := f.pool.Exec(f.ctx, statement, f.legacy); err == nil {
			t.Fatal("legacy mutation accepted", statement)
		} else {
			t.Log("rejected:", code(err))
		}
		if _, err := f.owner.Exec(f.ctx, statement, f.legacy); err == nil || !strings.HasPrefix(code(err), "P0001:") {
			t.Fatalf("owner bypassed immutable legacy snapshot: %s: %v", statement, err)
		}
	}
	if _, err := f.pool.Exec(f.ctx, requestInsert(false), uuid.NewString(), f.tenant, f.key, f.actor, f.model, f.defaultPrice); code(err) != "P0001:new request requires retail snapshot" {
		t.Fatal("new legacy request did not fail the pricing snapshot guard", code(err))
	}
	current := uuid.NewString()
	f.metadataSource(current, requestInsert(true), current, f.tenant, f.key, f.actor, f.model, f.defaultPrice)
	if _, err := f.pool.Exec(f.ctx, `UPDATE price_versions SET price_group_id=NULL WHERE id=$1`, f.defaultPrice); err == nil {
		t.Fatal("immutable price update accepted")
	}
}

func TestCommandsNoOpAndCrossDomain(t *testing.T) {
	f := newFixture(t)
	a := f.createGroup("A")
	b := f.createGroup("B")
	f.assign(&a, 1)
	f.assign(&a, 2)
	f.assign(&b, 2)
	f.assign(nil, 3)
	f.assign(nil, 4)
	var rev, eventCount int64
	if err := f.pool.QueryRow(f.ctx, `SELECT pricing_revision,(SELECT count(*) FROM tenant_pricing_assignments WHERE tenant_id=$1) FROM tenants WHERE id=$1`, f.tenant).Scan(&rev, &eventCount); err != nil {
		t.Fatal(err)
	}
	if rev != 4 || eventCount != 3 {
		t.Fatal("no-op changed revision/audit", rev, eventCount)
	}
	t.Log("exact assignment commands/no-op passed")
	// Real group update + no-op preserve immutable original result snapshots.
	op := uuid.NewString()
	tx := f.tx()
	var created time.Time
	if err := tx.QueryRow(f.ctx, `SELECT created_at FROM price_groups WHERE id=$1 FOR NO KEY UPDATE`, a).Scan(&created); err != nil {
		t.Fatal(err)
	}
	_, err := f.receipt(tx, op, "price_group.update", "platform", "/api/platform/price-groups/"+a, a, int64(2), wire(map[string]any{"expected_revision": "1", "label": "A2", "state": "active"}), wire(map[string]any{"id": a, "label": "A2", "state": "active", "revision": "2", "created_at": created.UTC().Format("2006-01-02T15:04:05.000000Z")}))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, f.ctx, tx, `UPDATE price_groups SET label='A2',revision=2,last_operation_id=$2 WHERE id=$1`, a, op)
	mustCommit(t, f.ctx, tx)
	noop := f.tx()
	_, err = f.receipt(noop, uuid.NewString(), "price_group.update", "platform", "/api/platform/price-groups/"+a, a, int64(2), wire(map[string]any{"expected_revision": "2", "label": "A2", "state": "active"}), wire(map[string]any{"id": a, "label": "A2", "state": "active", "revision": "2", "created_at": created.UTC().Format("2006-01-02T15:04:05.000000Z")}))
	if err != nil {
		t.Fatal(err)
	}
	mustCommit(t, f.ctx, noop)
	var original string
	if err = f.pool.QueryRow(f.ctx, `SELECT result_snapshot->>'label' FROM pricing_operations WHERE operation_id=(SELECT operation_id FROM pricing_operations WHERE kind='price_group.create' AND resource_id=$1)`, a).Scan(&original); err != nil || original != "A" {
		t.Fatal("original result changed", original, err)
	}
	// Each model/domain may have the same timestamp, but a domain cannot duplicate it.
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	for _, g := range []string{a, b} {
		tx := f.tx()
		op, id := uuid.NewString(), uuid.NewString()
		r, err := f.receipt(tx, op, "group_price.publish", "platform", "/api/platform/price-groups/"+g+"/price-versions", id, nil, wire(map[string]any{"model_id": f.model, "input_rate": "0", "output_rate": "0", "effective_at": at.Format("2006-01-02T15:04:05.000000Z")}), wire(map[string]any{"id": id, "model_id": f.model, "price_group_id": g, "currency": "CNY", "input_rate": "0", "output_rate": "0", "effective_at": at.Format("2006-01-02T15:04:05.000000Z")}))
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, f.ctx, tx, `INSERT INTO price_versions(id,model_id,price_group_id,pricing_operation_id,input_rate,output_rate,effective_at,created_by,created_at) VALUES($1,$2,$3,$4,0,0,$5,$6,$7)`, id, f.model, g, op, at, f.actor, r)
		mustCommit(t, f.ctx, tx)
	}
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) SELECT model_id,0,0,effective_at,created_by FROM price_versions WHERE id=$1`, f.defaultPrice); err == nil {
		t.Fatal("duplicate NULL domain accepted")
	} else {
		t.Log("NULL-domain duplicate:", code(err))
	}
}

func TestSQLNullInputsRejected(t *testing.T) {
	f := newFixture(t)
	g := f.createGroup("null-target")
	for _, test := range []struct{ kind, input string }{
		{"price_group.update", `{"expected_revision":null,"label":"null-target","state":"active"}`},
		{"price_group.update", `{"label":"null-target","state":"active"}`},
		{"price_group.update", `{"expected_revision":1,"label":"null-target","state":"active"}`},
		{"price_group.update", `{"expected_revision":"01","label":"null-target","state":"active"}`},
		{"price_group.update", `{"expected_revision":"9223372036854775808","label":"null-target","state":"active"}`},
		{"tenant_pricing.assign", `{"expected_revision":null,"price_group_id":null}`},
		{"tenant_pricing.assign", `{"price_group_id":null}`},
		{"tenant_pricing.assign", `{"expected_revision":"1"}`},
		{"group_price.publish", `{"model_id":null,"input_rate":"0","output_rate":"0","effective_at":"2026-11-01T00:00:00.000000Z"}`},
		{"group_price.publish", `{"model_id":"` + f.model + `","input_rate":null,"output_rate":"0","effective_at":"2026-11-01T00:00:00.000000Z"}`},
		{"group_price.publish", `{"model_id":"` + f.model + `","input_rate":"0","output_rate":"0","effective_at":null}`},
	} {
		tx := f.tx()
		scope, target, resource, revision := "platform", "/api/platform/price-groups/"+g, g, any(int64(1))
		if test.kind == "tenant_pricing.assign" {
			scope, target, resource = f.tenant, "/api/platform/tenants/"+f.tenant+"/pricing", f.tenant
		}
		if test.kind == "group_price.publish" {
			target += "/price-versions"
			resource = uuid.NewString()
			revision = nil
		}
		_, err := f.receipt(tx, uuid.NewString(), test.kind, scope, target, resource, revision, test.input, `{}`)
		if err == nil {
			err = tx.Commit(f.ctx)
		} else {
			_ = tx.Rollback(f.ctx)
		}
		if err == nil {
			t.Fatal("invalid input accepted", test.kind, test.input)
		}
		t.Log(test.kind, code(err))
	}
}

func TestLoginTenantNoKeyUpdateBarrier(t *testing.T) {
	f := newFixture(t)
	for _, lock := range []string{"FOR NO KEY UPDATE", "FOR UPDATE"} {
		t.Run(strings.ReplaceAll(lock, " ", "_"), func(t *testing.T) {
			login := f.tx()
			assignment := f.tx()
			mustExec(t, f.ctx, login, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.actor)
			mustExec(t, f.ctx, assignment, `SELECT id FROM tenants WHERE id=$1 `+lock, f.tenant)
			waitCtx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
			defer cancel()
			apid := int32(assignment.Conn().PgConn().PID())
			waiting := make(chan error, 1)
			go func() {
				_, err := assignment.Exec(waitCtx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, f.actor)
				waiting <- err
			}()
			// PostgreSQL-owned lock wait is the barrier; do not rely on a fixed sleep.
			deadline := time.Now().Add(time.Second)
			for {
				var blocked bool
				err := f.pool.QueryRow(f.ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, apid).Scan(&blocked)
				if err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("assignment did not block on actor")
				}
				time.Sleep(5 * time.Millisecond)
			}
			_, err := login.Exec(waitCtx, `INSERT INTO session_families(id,user_id) VALUES($1,$2)`, uuid.NewString(), f.actor)
			if err != nil {
				t.Fatal(err)
			}
			var family string
			if err = login.QueryRow(waitCtx, `SELECT id FROM session_families WHERE user_id=$1 LIMIT 1`, f.actor).Scan(&family); err != nil {
				t.Fatal(err)
			}
			_, err = login.Exec(waitCtx, `INSERT INTO sessions(digest,family_id,user_id,tenant_id,csrf_token,expires_at) VALUES($1,$2,$3,$4,'csrf',clock_timestamp()+interval '1 hour')`, uuid.NewString(), family, f.actor, f.tenant)
			if lock == "FOR NO KEY UPDATE" {
				if err != nil {
					t.Fatal("NO KEY UPDATE blocked session FK:", code(err))
				}
				mustCommit(t, f.ctx, login)
				if e := <-waiting; e != nil {
					t.Fatal(e)
				}
				_ = assignment.Rollback(f.ctx)
				t.Log("session tenant FK KEY SHARE acquired; login committed; assignment continued")
			} else {
				// Either actor SELECT or session INSERT must be selected as deadlock victim.
				_ = login.Rollback(f.ctx)
				actorErr := <-waiting
				_ = assignment.Rollback(f.ctx)
				if !strings.HasPrefix(code(err), "40P01:") && !strings.HasPrefix(code(actorErr), "40P01:") {
					t.Fatalf("FOR UPDATE failed to reproduce deadlock: session=%s actor=%s", code(err), code(actorErr))
				}
				t.Log("unsafe FOR UPDATE reproduced: session=", code(err), "actor=", code(actorErr))
			}
		})
	}
}

func (f *fixture) publish(g, targetGroup string, at time.Time) (string, error) {
	return f.publishRates(g, targetGroup, at, 10, 20)
}

func (f *fixture) publishRates(g, targetGroup string, at time.Time, inputRate, outputRate int64) (string, error) {
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(f.ctx)
	op, id := uuid.NewString(), uuid.NewString()
	instant := at.UTC().Format("2006-01-02T15:04:05.000000Z")
	r, err := f.receipt(tx, op, "group_price.publish", "platform", "/api/platform/price-groups/"+targetGroup+"/price-versions", id, nil, wire(map[string]any{"model_id": f.model, "input_rate": fmt.Sprint(inputRate), "output_rate": fmt.Sprint(outputRate), "effective_at": instant}), wire(map[string]any{"id": id, "model_id": f.model, "price_group_id": g, "currency": "CNY", "input_rate": fmt.Sprint(inputRate), "output_rate": fmt.Sprint(outputRate), "effective_at": instant}))
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(f.ctx, `INSERT INTO price_versions(id,model_id,price_group_id,pricing_operation_id,input_rate,output_rate,effective_at,created_by,created_at) VALUES($1,$2,$3,$4,$8,$9,$5,$6,$7)`, id, f.model, g, op, at, f.actor, r, inputRate, outputRate)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(f.ctx)
}

func TestPublicationTargetsRetirementAndRecordedRequest(t *testing.T) {
	f := newFixture(t)
	a := f.createGroup("existing")
	b := f.createGroup("other")
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if _, err := f.publish(a, b, at); err == nil {
		t.Fatal("cross-target group publication committed")
	} else {
		t.Log("cross-target rejected:", code(err))
	}
	p, err := f.publish(a, a, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.publish(a, a, at); err == nil {
		t.Fatal("same group/effective duplicate committed")
	} else {
		t.Log("same group time duplicate:", code(err))
	}
	f.assign(&a, 1)
	req := uuid.NewString()
	recorded := strings.Replace(requestInsert(true), "NULL,1,clock_timestamp()", "$7,2,clock_timestamp()", 1)
	f.metadataSource(req, recorded, req, f.tenant, f.key, f.actor, f.model, p, a)
	// Snapshot cannot borrow a version from another domain or change the locked revision.
	if _, err = f.pool.Exec(f.ctx, requestInsert(true), uuid.NewString(), f.tenant, f.key, f.actor, f.model, p); code(err) != "P0001:request tenant pricing mismatch" {
		t.Fatal("NULL domain request did not fail the pricing domain guard", code(err))
	}
	tx := f.tx()
	var created time.Time
	if err = tx.QueryRow(f.ctx, `SELECT created_at FROM price_groups WHERE id=$1 FOR NO KEY UPDATE`, a).Scan(&created); err != nil {
		t.Fatal(err)
	}
	op := uuid.NewString()
	_, err = f.receipt(tx, op, "price_group.update", "platform", "/api/platform/price-groups/"+a, a, int64(2), wire(map[string]any{"expected_revision": "1", "label": "existing", "state": "retired"}), wire(map[string]any{"id": a, "label": "existing", "state": "retired", "revision": "2", "created_at": created.UTC().Format("2006-01-02T15:04:05.000000Z")}))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, f.ctx, tx, `UPDATE price_groups SET state='retired',revision=2,last_operation_id=$2 WHERE id=$1`, a, op)
	mustCommit(t, f.ctx, tx)
	f.assign(&a, 2) // Existing retired assignment is a real no-op, not a new assignment.
	var before int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM pricing_operations`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = f.publish(a, a, at.Add(time.Second)); err == nil {
		t.Fatal("retired publication committed")
	} else {
		t.Log("retired publication rejected:", code(err))
	}
	var after int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM pricing_operations`).Scan(&after); err != nil || before != after {
		t.Fatal("failed publish left receipt", before, after, err)
	}
	// Existing tenants continue to insert recorded requests on retired group prices.
	retired := uuid.NewString()
	f.metadataSource(retired, recorded, retired, f.tenant, f.key, f.actor, f.model, p, a)
	f.assign(nil, 2)
	f.exec(`UPDATE request_records SET billing_status='released',status='failed' WHERE id=$1`, req)
	var stillA bool
	if err = f.pool.QueryRow(f.ctx, `SELECT pricing_group_id=$2 AND tenant_pricing_revision=2 FROM request_records WHERE id=$1`, req, a).Scan(&stillA); err != nil || !stillA {
		t.Fatal("historical snapshot changed", stillA, err)
	}
}

func TestBidirectionalTenantSwap(t *testing.T) {
	f := newFixture(t)
	a := f.createGroup("swap-a")
	b := f.createGroup("swap-b")
	f.assign(&a, 1)
	tid2 := uuid.NewString()
	f.exec(`INSERT INTO tenants(id,name) VALUES($1,'swap-tenant-2')`, tid2)
	f2 := *f
	f2.tenant = tid2
	f2.assign(&b, 1)
	left, right := f.tx(), f.tx()
	for _, v := range []struct {
		tx             pgx.Tx
		tenant, target string
	}{{left, f.tenant, b}, {right, tid2, a}} {
		mustExec(t, f.ctx, v.tx, `SELECT id FROM tenants WHERE id=$1 FOR NO KEY UPDATE`, v.tenant)
		mustExec(t, f.ctx, v.tx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, f.actor)
		mustExec(t, f.ctx, v.tx, `SELECT id FROM price_groups WHERE id=$1 FOR SHARE`, v.target)
	}
	result := make(chan error, 2)
	for _, v := range []struct {
		tx             pgx.Tx
		tenant, target string
	}{{left, f.tenant, b}, {right, tid2, a}} {
		go func(tx pgx.Tx, tenant, target string) {
			op := uuid.NewString()
			_, err := f.receipt(tx, op, "tenant_pricing.assign", tenant, "/api/platform/tenants/"+tenant+"/pricing", tenant, int64(3), wire(map[string]any{"expected_revision": "2", "price_group_id": target}), wire(map[string]any{"tenant_id": tenant, "price_group_id": target, "pricing_revision": "3"}))
			if err == nil {
				_, err = tx.Exec(f.ctx, `UPDATE tenants SET price_group_id=$2,pricing_revision=3,pricing_operation_id=$3 WHERE id=$1`, tenant, target, op)
			}
			if err == nil {
				err = tx.Commit(f.ctx)
			} else {
				_ = tx.Rollback(f.ctx)
			}
			result <- err
		}(v.tx, v.tenant, v.target)
	}
	for range 2 {
		if err := <-result; err != nil {
			t.Fatal("bidirectional target SHARE/old-group FK failed:", code(err))
		}
	}
	t.Log("both tenant swaps committed with target SHARE and old-group FK KEY SHARE")
}

func TestSelectEffectiveTxUsesExactDomainAndKeepsZero(t *testing.T) {
	f := newFixture(t)
	group, emptyGroup := f.createGroup("selected"), f.createGroup("missing")
	f.assign(&group, 1)
	tx := f.tx()
	selected, observed, err := pricing.SelectEffectiveTx(f.ctx, tx, f.model, pricing.Domain{TenantRevision: 1})
	if err != nil || selected == nil || selected.VersionID != f.defaultPrice || selected.GroupID != nil ||
		selected.InputRate != 10 || selected.OutputRate != 20 || !selected.PricedAt.Equal(observed) {
		t.Fatalf("default selection: %+v at=%s error=%v", selected, observed, err)
	}
	missing, at, err := pricing.SelectEffectiveTx(f.ctx, tx, f.model, pricing.Domain{GroupID: &emptyGroup, TenantRevision: 1})
	if err != nil || missing != nil || at.IsZero() {
		t.Fatalf("group price fell back to public default: %+v %s %v", missing, at, err)
	}
	var dbNow time.Time
	if err = f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		t.Fatal(err)
	}
	if _, err = f.publish(group, group, dbNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	domain := pricing.Domain{GroupID: &group, TenantRevision: 2}
	missing, at, err = pricing.SelectEffectiveTx(f.ctx, tx, f.model, domain)
	if err != nil || missing != nil || at.IsZero() {
		t.Fatalf("future group price became effective: %+v %s %v", missing, at, err)
	}
	currentID, err := f.publish(group, group, dbNow.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := pricing.SelectEffectiveTx(f.ctx, tx, f.model, domain)
	if err != nil || current == nil || current.VersionID != currentID || current.GroupID == nil || *current.GroupID != group {
		t.Fatalf("group selection: %+v %v", current, err)
	}
	zeroID, err := f.publishRates(group, group, dbNow.Add(-time.Second), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	zero, _, err := pricing.SelectEffectiveTx(f.ctx, tx, f.model, domain)
	if err != nil || zero == nil || zero.VersionID != zeroID || zero.InputRate != 0 || zero.OutputRate != 0 {
		t.Fatalf("zero rates treated as unavailable or future selected: %+v %v", zero, err)
	}
	if current.VersionID != currentID || current.InputRate != 10 || current.OutputRate != 20 {
		t.Fatal("later price selection mutated an existing frozen value")
	}
	copyOfGroup := group
	*domain.GroupID = emptyGroup
	if current.GroupID == nil || *current.GroupID != copyOfGroup {
		t.Fatal("selection retained caller's mutable group pointer")
	}
}

func TestGroupLockWaitPrecedesSelectionClock(t *testing.T) {
	f := newFixture(t)
	group := f.createGroup("waited")
	f.assign(&group, 1)
	var dbNow time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		t.Fatal(err)
	}
	id, err := f.publish(group, group, dbNow.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	blocker, reader := f.tx(), f.tx()
	mustExec(t, f.ctx, blocker, `SELECT id FROM price_groups WHERE id=$1 FOR NO KEY UPDATE`, group)
	var begun time.Time
	if err = reader.QueryRow(f.ctx, `SELECT transaction_timestamp()`).Scan(&begun); err != nil {
		t.Fatal(err)
	}
	readerPID := int32(reader.Conn().PgConn().PID())
	type result struct {
		selected *pricing.Selection
		observed time.Time
		err      error
	}
	ready := make(chan result, 1)
	go func() {
		domain := pricing.Domain{GroupID: &group, TenantRevision: 2}
		if e := pricing.LockAssignedGroupTx(f.ctx, reader, domain); e != nil {
			ready <- result{err: e}
			return
		}
		s, at, e := pricing.SelectEffectiveTx(f.ctx, reader, f.model, domain)
		ready <- result{selected: s, observed: at, err: e}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		var blocked bool
		if err = f.pool.QueryRow(f.ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, readerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("assigned group SHARE did not wait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var afterWaitBegan time.Time
	if err = blocker.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&afterWaitBegan); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	got := <-ready
	if got.err != nil || got.selected == nil || got.selected.VersionID != id ||
		got.observed.Before(afterWaitBegan) || !got.observed.After(begun) {
		t.Fatalf("price used transaction-start time before group wait: %+v observed=%s transaction=%s barrier=%s error=%v", got.selected, got.observed, begun, afterWaitBegan, got.err)
	}
}

func TestInvalidDomainAndMissingAssignedGroup(t *testing.T) {
	ctx := context.Background()
	badID := "00000000-0000-0000-0000-000000000000"
	for _, domain := range []pricing.Domain{{}, {TenantRevision: -1}, {GroupID: &badID, TenantRevision: 1}} {
		_, _, err := pricing.SelectEffectiveTx(ctx, nil, uuid.NewString(), domain)
		var pe *pricing.Error
		if !errors.As(err, &pe) || pe.Code != "invalid_domain" {
			t.Fatalf("invalid domain error: %v", err)
		}
	}
	f := newFixture(t)
	tx := f.tx()
	missing := uuid.NewString()
	err := pricing.LockAssignedGroupTx(f.ctx, tx, pricing.Domain{GroupID: &missing, TenantRevision: 1})
	var pe *pricing.Error
	if !errors.As(err, &pe) || pe.Code != "pricing_invariant" {
		t.Fatalf("missing assignment FK did not fail closed: %v", err)
	}
}
