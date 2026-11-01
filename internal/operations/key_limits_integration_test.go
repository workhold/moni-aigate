package operations

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
)

func TestProvisionedKeyRuntimeLimitsBoundsAndIndependentRevisions(t *testing.T) {
	f := operationDB(t)
	store := identity.New(f.app, f.secrets)
	actor := &identity.Session{User: identity.User{ID: f.user}, Tenant: &identity.Tenant{ID: f.tenant, Role: "owner"}}
	initial, err := store.KeyRuntimeLimits(f.ctx, actor, f.key)
	if err != nil || initial.RPMLimit != 60 || initial.BurstLimit != 20 || initial.ConcurrencyLimit != 16 || initial.LimitRevision != "1" {
		t.Fatalf("existing insert lost defaults: %+v %v", initial, err)
	}
	for _, bounds := range []struct {
		column string
		max    int
	}{{"rpm_limit", 6000}, {"burst_limit", 200}, {"concurrency_limit", 16}} {
		for _, invalid := range []any{nil, 0, -1, bounds.max + 1} {
			_, err = f.app.Exec(f.ctx, `UPDATE api_keys SET `+bounds.column+`=$2 WHERE id=$1`, f.key, invalid)
			if err == nil {
				t.Fatalf("database accepted invalid %s=%v", bounds.column, invalid)
			}
		}
	}
	var runtime, source string
	for _, sql := range []string{
		`UPDATE api_keys SET spent=2,held=3 WHERE id=$1 RETURNING limit_revision::text,access_policy_revision::text`,
		`UPDATE api_keys SET limit_revision=9223372036854775807,name=name||'-renamed' WHERE id=$1 RETURNING limit_revision::text,access_policy_revision::text`,
		`UPDATE api_keys SET limit_revision=0,rpm_limit=rpm_limit,burst_limit=burst_limit,concurrency_limit=concurrency_limit WHERE id=$1 RETURNING limit_revision::text,access_policy_revision::text`,
	} {
		if err = f.app.QueryRow(f.ctx, sql, f.key).Scan(&runtime, &source); err != nil || runtime != "1" || source != "1" {
			t.Fatalf("unrelated writer changed config revisions: runtime=%s source=%s err=%v", runtime, source, err)
		}
	}
	if err = f.app.QueryRow(f.ctx, `UPDATE api_keys SET rpm_limit=6000,burst_limit=200,concurrency_limit=1,limit_revision=999 WHERE id=$1 RETURNING limit_revision::text,access_policy_revision::text`, f.key).Scan(&runtime, &source); err != nil || runtime != "2" || source != "1" {
		t.Fatalf("runtime tuple did not force independent next revision: %s %s %v", runtime, source, err)
	}
	if err = f.app.QueryRow(f.ctx, `UPDATE api_keys SET allowed_client_cidrs=ARRAY['192.0.2.0/24'],limit_revision=999 WHERE id=$1 RETURNING limit_revision::text,access_policy_revision::text`, f.key).Scan(&runtime, &source); err != nil || runtime != "2" || source != "2" {
		t.Fatalf("source edit invalidated runtime CAS: %s %s %v", runtime, source, err)
	}
	if err = f.owner.QueryRow(f.ctx, `UPDATE api_keys SET limit_revision=0 WHERE id=$1 RETURNING limit_revision::text`, f.key).Scan(&runtime); err != nil || runtime != "2" {
		t.Fatalf("owner directly forged runtime revision: %s %v", runtime, err)
	}
	inserted := uuid.NewString()
	if err = f.app.QueryRow(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,limit_revision,rpm_limit,burst_limit,concurrency_limit) VALUES($1,$2,$3,'runtime-fixture','fixture',$1,$4,999,1,200,16) RETURNING limit_revision::text`, inserted, f.tenant, f.user, []string{f.model}).Scan(&runtime); err != nil || runtime != "1" {
		t.Fatalf("insert forged runtime revision: %s %v", runtime, err)
	}
	rpm, burst, concurrency := 7, 8, 2
	view, err := store.UpdateKeyRuntimeLimits(f.ctx, actor, f.key, identity.KeyLimitsInput{ExpectedRevision: "2", RPMLimit: &rpm, BurstLimit: &burst, ConcurrencyLimit: &concurrency})
	if err != nil || view.LimitRevision != "3" {
		t.Fatalf("least-privilege app cannot edit runtime limits: %+v %v", view, err)
	}
	var received identity.KeyLimitsView
	err = store.WithKeyRuntimeLimits(f.ctx, identity.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, func(v identity.KeyLimitsView) error { received = v; return nil })
	if err != nil || !reflect.DeepEqual(received, view) {
		t.Fatalf("least-privilege app cannot perform locked admission: %+v %v", received, err)
	}
	for _, sql := range []string{`SELECT rpm_limit,burst_limit,concurrency_limit,limit_revision FROM api_keys LIMIT 1`, `UPDATE api_keys SET rpm_limit=1,burst_limit=1,concurrency_limit=1`, `UPDATE api_keys SET limit_revision=999`} {
		_, err = f.projection.Exec(f.ctx, sql)
		requirePoolSQLState(t, err, "42501")
	}
}
