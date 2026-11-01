package identity

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func keyConfigurationReplacement(v KeyConfigurationView) KeyConfigurationInput {
	return KeyConfigurationInput{ExpectedRevision: v.ConfigurationRevision, Name: v.Name, Scopes: slices.Clone(v.Scopes), AllowedRegions: slices.Clone(v.AllowedRegions), LifetimeLimit: v.LifetimeLimit, ExpiresAt: v.ExpiresAt}
}

func (f *identityFixture) configurationView(t *testing.T, user, id string) KeyConfigurationView {
	t.Helper()
	v, err := f.store.KeyConfiguration(f.ctx, f.keyActor(user, "owner"), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *identityFixture) configurationAudits(t *testing.T, id string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='key.configuration.update' AND target=$1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestKeyConfigurationCreateProjectionAndTypedInputErrors(t *testing.T) {
	f := newIdentityFixture(t)
	created, err := f.store.CreateKey(f.ctx, f.keyActor(f.owner, "owner"), KeyInput{Name: "created", Scopes: []string{f.model1}})
	if err != nil || created["configuration_revision"] != "1" || !reflect.DeepEqual(created["allowed_regions"], []string{"global"}) {
		t.Fatalf("creation revision=%v regions=%v err=%v", created["configuration_revision"], created["allowed_regions"], err)
	}
	expired := time.Now().Add(-time.Minute)
	for _, in := range []KeyInput{
		{Name: "", Scopes: []string{f.model1}}, {Name: "name", Scopes: nil},
		{Name: "name", Scopes: []string{f.model1}, Limit: keyConfigurationString("-1")},
		{Name: "name", Scopes: []string{f.model1}, Limit: keyConfigurationString("9223372036854775808")},
		{Name: "name", Scopes: []string{f.model1}, ExpiresAt: &expired},
	} {
		if _, err = f.store.CreateKey(f.ctx, f.keyActor(f.owner, "owner"), in); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("invalid create missing typed input error: %v", err)
		}
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM api_keys`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid creation persisted key: %d %v", count, err)
	}
}

func TestKeyConfigurationUTCYearOverflowHasNoMutation(t *testing.T) {
	f := newIdentityFixture(t)
	id, _ := f.createPolicyKey(t, f.owner, nil)
	actor := f.keyActor(f.owner, "owner")
	initial := f.configurationView(t, f.owner, id)
	for _, value := range []string{"9999-12-31T23:59:59-23:59", "0000-01-01T00:00:00+23:59"} {
		expires, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		in := keyConfigurationReplacement(initial)
		in.ExpiresAt = &expires
		if _, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("UTC year overflow edit accepted: %v", err)
		}
		if got := f.configurationView(t, f.owner, id); !reflect.DeepEqual(got, initial) || f.configurationAudits(t, id) != 0 {
			t.Fatal("rejected UTC year overflow persisted configuration/audit")
		}
		if _, err = f.store.CreateKey(f.ctx, actor, KeyInput{Name: "invalid UTC year", Scopes: []string{f.model1}, ExpiresAt: &expires}); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("UTC year overflow create accepted: %v", err)
		}
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM api_keys`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("UTC year overflow creation persisted key: %d %v", count, err)
	}
}

func TestKeyConfigurationCASIndependentRevisionsAndSanitizedAudit(t *testing.T) {
	f := newIdentityFixture(t)
	id, raw := f.createPolicyKey(t, f.owner, nil)
	actor := f.keyActor(f.owner, "owner")
	before := f.configurationView(t, f.owner, id)
	in := keyConfigurationReplacement(before)
	in.Name = "  renamed key  "
	unknownModel := uuid.NewString() // Restriction values do not delegate grants.
	in.Scopes = []string{unknownModel, f.model2, f.model1, f.model2}
	in.AllowedRegions = []string{" cn custom ", "global", "cn custom"}
	in.LifetimeLimit = keyConfigurationString("9007199254740993")
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second).Add(123456789 * time.Nanosecond)
	in.ExpiresAt = &expires
	updated, err := f.store.UpdateKeyConfiguration(f.ctx, actor, id, in)
	wantScopes := []string{unknownModel, f.model1, f.model2}
	slices.Sort(wantScopes)
	if err != nil || updated.Name != "renamed key" || updated.ConfigurationRevision != "2" || !reflect.DeepEqual(updated.Scopes, wantScopes) || !reflect.DeepEqual(updated.AllowedRegions, []string{"cn custom", "global"}) || updated.ExpiresAt.Nanosecond() != 123456000 || updated.ExpiresAt.Location() != time.UTC {
		t.Fatalf("configuration update=%+v err=%v", updated, err)
	}
	var source, runtime, digest string
	if err = f.pool.QueryRow(f.ctx, `SELECT access_policy_revision::text,limit_revision::text,digest FROM api_keys WHERE id=$1`, id).Scan(&source, &runtime, &digest); err != nil || source != "1" || runtime != "1" || digest != f.store.Secrets.Digest(raw) {
		t.Fatalf("edit affected another policy or secret: %v", err)
	}
	f.exec(t, `UPDATE api_keys SET spent=7,held=3,allowed_client_cidrs=ARRAY['192.0.2.0/24'],rpm_limit=7 WHERE id=$1`, id)
	noOp := keyConfigurationReplacement(updated)
	noOp.Name = " renamed key "
	noOp.Scopes = append(slices.Clone(updated.Scopes), updated.Scopes[0])
	zoneExpiry := updated.ExpiresAt.In(time.FixedZone("same instant", 8*3600)).Add(789 * time.Nanosecond)
	noOp.ExpiresAt = &zoneExpiry
	current, err := f.store.UpdateKeyConfiguration(f.ctx, actor, id, noOp)
	if err != nil || current.ConfigurationRevision != "2" || current.Spent != "7" || current.Held != "3" || f.configurationAudits(t, id) != 1 {
		t.Fatalf("canonical no-op changed revision or lost latest usage: %+v %v", current, err)
	}
	noOp.ExpectedRevision = "1"
	if _, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, noOp); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale no-op bypassed CAS: %v", err)
	}
	var diff map[string]json.RawMessage
	if err = f.pool.QueryRow(f.ctx, `SELECT sanitized_diff FROM audit_events WHERE action='key.configuration.update' AND target=$1`, id).Scan(&diff); err != nil {
		t.Fatal(err)
	}
	if len(diff) != 2 {
		t.Fatalf("audit has extra outer attributes: %v", diff)
	}
	for _, phase := range []string{"before", "after"} {
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(diff[phase], &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 6 {
			t.Fatalf("audit not restricted to tuple and revision: %v", fields)
		}
		for _, field := range []string{"name", "scopes", "allowed_regions", "lifetime_limit", "expires_at", "configuration_revision"} {
			if _, ok := fields[field]; !ok {
				t.Fatalf("missing audit field %s", field)
			}
		}
	}
	encoded, _ := json.Marshal(diff)
	if strings.Contains(string(encoded), raw) || strings.Contains(string(encoded), "digest") || strings.Contains(string(encoded), "spent") {
		t.Fatal("audit exposed secret or financial observation")
	}
	for _, revision := range []string{"", "0", "-1", "+2", "02", " 2", "2.0", "9223372036854775808"} {
		noOp.ExpectedRevision = revision
		if _, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, noOp); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("invalid revision accepted: %v", err)
		}
	}
}

func TestKeyConfigurationBudgetProtectsUsageWithoutOverflow(t *testing.T) {
	f := newIdentityFixture(t)
	id, _ := f.createPolicyKey(t, f.owner, nil)
	actor := f.keyActor(f.owner, "owner")
	in := keyConfigurationReplacement(f.configurationView(t, f.owner, id))
	in.LifetimeLimit = keyConfigurationString("0")
	current, err := f.store.UpdateKeyConfiguration(f.ctx, actor, id, in)
	if err != nil || current.LifetimeLimit == nil || *current.LifetimeLimit != "0" {
		t.Fatalf("zero budget rejected: %+v %v", current, err)
	}
	in = keyConfigurationReplacement(current)
	in.LifetimeLimit = nil
	current, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in)
	if err != nil || current.LifetimeLimit != nil {
		t.Fatalf("explicit unlimited rejected: %+v %v", current, err)
	}
	f.exec(t, `UPDATE api_keys SET spent=9223372036854775807,held=9223372036854775807 WHERE id=$1`, id)
	in = keyConfigurationReplacement(current)
	in.LifetimeLimit = keyConfigurationString("9223372036854775807")
	if _, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in); !errors.Is(err, ErrKeyBudgetBelowUsage) {
		t.Fatalf("overflowing spent+held accepted finite cap: %v", err)
	}
	f.exec(t, `UPDATE api_keys SET spent=17,held=23 WHERE id=$1`, id)
	in.LifetimeLimit = keyConfigurationString("40")
	current, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in)
	if err != nil || current.Spent != "17" || current.Held != "23" {
		t.Fatalf("equal usage cap rejected: %+v %v", current, err)
	}
	in = keyConfigurationReplacement(current)
	in.LifetimeLimit = keyConfigurationString("39")
	if _, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in); !errors.Is(err, ErrKeyBudgetBelowUsage) {
		t.Fatalf("cap below held accepted: %v", err)
	}
	if got := f.configurationView(t, f.owner, id); !reflect.DeepEqual(got, current) {
		t.Fatal("rejected cap mutated configuration or counters")
	}
}

func TestKeyConfigurationLegacyValuesAndExpiredRenewal(t *testing.T) {
	f := newIdentityFixture(t)
	id, _ := f.createPolicyKey(t, f.owner, nil)
	actor := f.keyActor(f.owner, "owner")
	f.exec(t, `UPDATE api_keys SET name=$2,scopes=$3,allowed_regions=$4,expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, id, "old\nname", []string{"legacy scope", ""}, []string{})
	legacy := f.configurationView(t, f.owner, id)
	in := keyConfigurationReplacement(legacy)
	in.LifetimeLimit = keyConfigurationString("100")
	updated, err := f.store.UpdateKeyConfiguration(f.ctx, actor, id, in)
	if err != nil || updated.Name != legacy.Name || !reflect.DeepEqual(updated.Scopes, legacy.Scopes) || !reflect.DeepEqual(updated.AllowedRegions, legacy.AllowedRegions) || !updated.ExpiresAt.Equal(*legacy.ExpiresAt) {
		t.Fatalf("unrelated edit cleaned old data: %+v %v", updated, err)
	}
	// Financial and other policy writes must still work on legacy tuples.
	f.exec(t, `UPDATE api_keys SET spent=3,held=2,rpm_limit=8,allowed_client_cidrs=ARRAY['192.0.2.0/24'] WHERE id=$1`, id)
	in = keyConfigurationReplacement(updated)
	if _, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in); err != nil {
		t.Fatalf("old expired no-op failed: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	in.ExpiresAt = &past
	if _, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in); !errors.Is(err, ErrKeyConfigurationInvalid) {
		t.Fatalf("explicit changed past expiry accepted: %v", err)
	}
	future := time.Now().Add(time.Hour)
	in.ExpiresAt = &future
	updated, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in)
	if err != nil || !updated.ExpiresAt.After(time.Now()) {
		t.Fatalf("expired key cannot renew: %+v %v", updated, err)
	}
	in = keyConfigurationReplacement(updated)
	in.ExpiresAt = nil
	updated, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in)
	if err != nil || updated.ExpiresAt != nil {
		t.Fatalf("explicit expiry clear rejected: %+v %v", updated, err)
	}
	in = keyConfigurationReplacement(updated)
	in.Scopes = []string{} // An existing empty legacy value is separately allowed.
	if _, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in); !errors.Is(err, ErrKeyConfigurationInvalid) {
		t.Fatalf("changed empty scopes accepted: %v", err)
	}
	f.exec(t, `UPDATE api_keys SET scopes='{}' WHERE id=$1`, id)
	empty := f.configurationView(t, f.owner, id)
	in = keyConfigurationReplacement(empty)
	in.Name = "clean name"
	if _, err = f.store.UpdateKeyConfiguration(f.ctx, actor, id, in); err != nil {
		t.Fatalf("existing empty scopes could not be preserved: %v", err)
	}
}

func TestKeyConfigurationCurrentRoleVisibilityAndRevocation(t *testing.T) {
	f := newIdentityFixture(t)
	ownerID, _ := f.createPolicyKey(t, f.owner, nil)
	memberID, _ := f.createPolicyKey(t, f.member, nil)
	owner, member := f.keyActor(f.owner, "owner"), f.keyActor(f.member, "member")
	in := keyConfigurationReplacement(f.configurationView(t, f.owner, ownerID))
	in.Name = "changed"
	if _, err := f.store.KeyConfiguration(f.ctx, member, ownerID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member read another key: %v", err)
	}
	if _, err := f.store.UpdateKeyConfiguration(f.ctx, member, ownerID, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member edited another key: %v", err)
	}
	in = keyConfigurationReplacement(f.configurationView(t, f.member, memberID))
	in.Name = "owner edit"
	if _, err := f.store.UpdateKeyConfiguration(f.ctx, owner, memberID, in); err != nil {
		t.Fatal(err)
	}
	in = keyConfigurationReplacement(f.configurationView(t, f.member, memberID))
	in.Name = "member edit"
	if _, err := f.store.UpdateKeyConfiguration(f.ctx, member, memberID, in); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.member)
	if _, err := f.store.KeyConfiguration(f.ctx, member, memberID); err != nil {
		t.Fatalf("viewer cannot inspect own key: %v", err)
	}
	in = keyConfigurationReplacement(f.configurationView(t, f.member, memberID))
	in.Name = "viewer edit"
	if _, err := f.store.UpdateKeyConfiguration(f.ctx, member, memberID, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale session bypassed current viewer: %v", err)
	}
	foreign := uuid.NewString()
	f.exec(t, `INSERT INTO tenants(id,name) VALUES($1,'foreign');`, foreign)
	f.exec(t, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, foreign, f.owner)
	foreignActor := &Session{User: User{ID: f.owner}, Tenant: &Tenant{ID: foreign, Role: "owner"}}
	for _, id := range []string{ownerID, uuid.NewString()} {
		if _, err := f.store.KeyConfiguration(f.ctx, foreignActor, id); !errors.Is(err, ErrForbidden) {
			t.Fatalf("cross-tenant or missing read disclosed: %v", err)
		}
		if _, err := f.store.UpdateKeyConfiguration(f.ctx, foreignActor, id, in); !errors.Is(err, ErrForbidden) {
			t.Fatalf("cross-tenant or missing write disclosed: %v", err)
		}
	}
	f.exec(t, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, ownerID)
	view := f.configurationView(t, f.owner, ownerID)
	in = keyConfigurationReplacement(view)
	in.ExpiresAt = nil
	if _, err := f.store.UpdateKeyConfiguration(f.ctx, owner, ownerID, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked key restored: %v", err)
	}
	for _, id := range []string{"", "bad", strings.ToUpper(ownerID), strings.ReplaceAll(ownerID, "-", "")} {
		if _, err := f.store.KeyConfiguration(f.ctx, owner, id); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("bad ID read: %v", err)
		}
	}
}

func TestKeyConfigurationAuditRollbackAndConcurrentCAS(t *testing.T) {
	f := newIdentityFixture(t)
	id, _ := f.createPolicyKey(t, f.owner, nil)
	actor := f.keyActor(f.owner, "owner")
	initial := f.configurationView(t, f.owner, id)
	f.exec(t, `CREATE FUNCTION fail_configuration_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='key.configuration.update' THEN RAISE EXCEPTION 'injected configuration audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_configuration_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_configuration_audit()`)
	in := keyConfigurationReplacement(initial)
	in.Name = "not committed"
	if _, err := f.store.UpdateKeyConfiguration(f.ctx, actor, id, in); err == nil {
		t.Fatal("audit failure reported success")
	}
	if got := f.configurationView(t, f.owner, id); !reflect.DeepEqual(got, initial) || f.configurationAudits(t, id) != 0 {
		t.Fatal("audit failure left partial key change")
	}
	f.exec(t, `DROP TRIGGER fail_configuration_audit ON audit_events`)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			<-start
			v := keyConfigurationReplacement(initial)
			v.Name = name
			_, err := f.store.UpdateKeyConfiguration(f.ctx, actor, id, v)
			results <- err
		}(name)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 || f.configurationAudits(t, id) != 1 {
		t.Fatalf("concurrent edit not serialized: %d/%d", success, conflict)
	}
}

func TestKeyConfigurationRechecksAfterLockWait(t *testing.T) {
	for _, boundary := range []string{"tenant", "membership", "user", "key", "usage", "expiry"} {
		t.Run(boundary, func(t *testing.T) {
			f := newIdentityFixture(t)
			id, _ := f.createPolicyKey(t, f.owner, nil)
			in := keyConfigurationReplacement(f.configurationView(t, f.owner, id))
			in.Name = "changed"
			tx, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			var locked, lockSQL, editSQL, prefix string
			var args []any
			want := ErrForbidden
			switch boundary {
			case "tenant":
				lockSQL, editSQL, prefix, args = `SELECT id FROM tenants WHERE id=$1 FOR UPDATE`, `UPDATE tenants SET status='suspended' WHERE id=$1`, "SELECT id FROM tenants", []any{f.tenant}
			case "membership":
				lockSQL, editSQL, prefix, args = `SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR UPDATE`, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, "SELECT role FROM memberships", []any{f.tenant, f.owner}
			case "user":
				lockSQL, editSQL, prefix, args = `SELECT id FROM users WHERE id=$1 FOR UPDATE`, `UPDATE users SET status='suspended' WHERE id=$1`, "SELECT id FROM users", []any{f.owner}
			case "key":
				lockSQL, editSQL, prefix, args = `SELECT id FROM api_keys WHERE id=$1 FOR UPDATE`, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, "SELECT id,owner_id,name", []any{id}
			case "usage":
				lockSQL, editSQL, prefix, args = `SELECT id FROM api_keys WHERE id=$1 FOR UPDATE`, `UPDATE api_keys SET spent=30,held=20 WHERE id=$1`, "SELECT id,owner_id,name", []any{id}
				in.LifetimeLimit = keyConfigurationString("40")
				want = ErrKeyBudgetBelowUsage
			case "expiry":
				lockSQL, prefix, args = `SELECT id FROM api_keys WHERE id=$1 FOR UPDATE`, "SELECT id,owner_id,name", []any{id}
				expiry := time.Now().Add(200 * time.Millisecond)
				in.ExpiresAt = &expiry
				want = ErrKeyConfigurationInvalid
			}
			if err = tx.QueryRow(f.ctx, lockSQL, args...).Scan(&locked); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := f.store.UpdateKeyConfiguration(ctx, f.keyActor(f.owner, "owner"), id, in)
				done <- err
			}()
			waitPolicyBlocked(t, f, tx.Conn().PgConn().PID(), prefix)
			if boundary == "expiry" {
				time.Sleep(time.Until(*in.ExpiresAt) + 20*time.Millisecond)
			} else if _, err = tx.Exec(f.ctx, editSQL, args...); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, want) {
				t.Fatalf("fresh %s state ignored: %v", boundary, err)
			}
			var revision string
			if err = f.pool.QueryRow(f.ctx, `SELECT configuration_revision::text FROM api_keys WHERE id=$1`, id).Scan(&revision); err != nil || revision != "1" || f.configurationAudits(t, id) != 0 {
				t.Fatalf("rejected write persisted state: %s %v", revision, err)
			}
		})
	}
}
