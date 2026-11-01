package operations

import (
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
)

func TestProvisionedKeyConfigurationRevisionAndMinimalRoles(t *testing.T) {
	f := operationDB(t)
	store := identity.New(f.app, f.secrets)
	actor := &identity.Session{User: identity.User{ID: f.user}, Tenant: &identity.Tenant{ID: f.tenant, Role: "owner"}}
	initial, err := store.KeyConfiguration(f.ctx, actor, f.key)
	if err != nil || initial.ConfigurationRevision != "1" {
		t.Fatalf("initial configuration=%+v err=%v", initial, err)
	}
	var configuration, source, runtime string
	var revision int64
	for _, sql := range []string{
		`UPDATE api_keys SET spent=2,held=3 WHERE id=$1 RETURNING configuration_revision::text,access_policy_revision::text,limit_revision::text`,
		`UPDATE api_keys SET configuration_revision=9223372036854775807 WHERE id=$1 RETURNING configuration_revision::text,access_policy_revision::text,limit_revision::text`,
		`UPDATE api_keys SET configuration_revision=0,name=name,scopes=scopes,allowed_regions=allowed_regions,lifetime_limit=lifetime_limit,expires_at=expires_at WHERE id=$1 RETURNING configuration_revision::text,access_policy_revision::text,limit_revision::text`,
	} {
		if err = f.app.QueryRow(f.ctx, sql, f.key).Scan(&configuration, &source, &runtime); err != nil || configuration != "1" || source != "1" || runtime != "1" {
			t.Fatalf("unrelated writer forged revision: %s %s %s %v", configuration, source, runtime, err)
		}
	}
	if err = f.app.QueryRow(f.ctx, `UPDATE api_keys SET name=name||'-renamed',scopes=ARRAY['*'],allowed_regions=ARRAY['*'],lifetime_limit=1000,expires_at=clock_timestamp()+interval '1 hour',configuration_revision=999 WHERE id=$1 RETURNING configuration_revision::text,access_policy_revision::text,limit_revision::text`, f.key).Scan(&configuration, &source, &runtime); err != nil || configuration != "2" || source != "1" || runtime != "1" {
		t.Fatalf("tuple did not advance once independently: %s %s %s %v", configuration, source, runtime, err)
	}
	for i, assignment := range []string{`name=name||'-one'`, `scopes=ARRAY['old-format']`, `allowed_regions=ARRAY['legacy custom']`, `lifetime_limit=1001`, `expires_at=expires_at+interval '1 microsecond'`} {
		if err = f.app.QueryRow(f.ctx, `UPDATE api_keys SET `+assignment+` WHERE id=$1 RETURNING configuration_revision,access_policy_revision::text,limit_revision::text`, f.key).Scan(&revision, &source, &runtime); err != nil || revision != int64(i+3) || source != "1" || runtime != "1" {
			t.Fatalf("individual tuple attribute failed independent revision: %d %s %s %v", revision, source, runtime, err)
		}
	}
	if err = f.app.QueryRow(f.ctx, `UPDATE api_keys SET allowed_client_cidrs=ARRAY['192.0.2.0/24'],rpm_limit=7,configuration_revision=999 WHERE id=$1 RETURNING configuration_revision::text,access_policy_revision::text,limit_revision::text`, f.key).Scan(&configuration, &source, &runtime); err != nil || configuration != "7" || source != "2" || runtime != "2" {
		t.Fatalf("source/runtime invalidated configuration CAS: %s %s %s %v", configuration, source, runtime, err)
	}
	if err = f.owner.QueryRow(f.ctx, `UPDATE api_keys SET configuration_revision=0 WHERE id=$1 RETURNING configuration_revision::text`, f.key).Scan(&configuration); err != nil || configuration != "7" {
		t.Fatalf("owner forged configuration revision: %s %v", configuration, err)
	}
	forged := uuid.NewString()
	if err = f.app.QueryRow(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,configuration_revision) VALUES($1,$2,$3,'configuration-fixture','fixture',$1,$4,999) RETURNING configuration_revision::text`, forged, f.tenant, f.user, []string{f.model}).Scan(&configuration); err != nil || configuration != "1" {
		t.Fatalf("insert forged configuration revision: %s %v", configuration, err)
	}
	current, err := store.KeyConfiguration(f.ctx, actor, f.key)
	if err != nil {
		t.Fatal(err)
	}
	in := identity.KeyConfigurationInput{ExpectedRevision: current.ConfigurationRevision, Name: "app edit", Scopes: current.Scopes, AllowedRegions: current.AllowedRegions, LifetimeLimit: current.LifetimeLimit, ExpiresAt: current.ExpiresAt}
	updated, err := store.UpdateKeyConfiguration(f.ctx, actor, f.key, in)
	if err != nil || updated.ConfigurationRevision != "8" || updated.Spent != "2" || updated.Held != "3" {
		t.Fatalf("minimal app role cannot edit: %+v %v", updated, err)
	}
	// No new field constraints can strand financial writes on old tuples.
	if _, err = f.app.Exec(f.ctx, `UPDATE api_keys SET name=E'old\nname',scopes='{}',allowed_regions='{}' WHERE id=$1`, f.key); err != nil {
		t.Fatalf("legacy tuple blocked: %v", err)
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE api_keys SET spent=3,held=2,rpm_limit=8 WHERE id=$1`, f.key); err != nil {
		t.Fatalf("legacy tuple stranded usage writer: %v", err)
	}
	for _, sql := range []string{
		`SELECT name,scopes,allowed_regions,lifetime_limit,expires_at,configuration_revision FROM api_keys LIMIT 1`,
		`UPDATE api_keys SET name='projection edit'`,
		`UPDATE api_keys SET configuration_revision=999`,
	} {
		_, err = f.projection.Exec(f.ctx, sql)
		requirePoolSQLState(t, err, "42501")
	}
}
