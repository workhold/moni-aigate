package identity

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (f *identityFixture) keyActor(user, role string) *Session {
	return &Session{User: User{ID: user}, Tenant: &Tenant{ID: f.tenant, Role: role}}
}

func (f *identityFixture) createPolicyKey(t *testing.T, user string, cidrs []string) (string, string) {
	t.Helper()
	created, err := f.store.CreateKey(f.ctx, f.keyActor(user, "owner"), KeyInput{Name: "policy-key", Scopes: []string{f.model1}, AllowedClientCIDRs: cidrs})
	if err != nil {
		t.Fatal(err)
	}
	return created["id"].(string), created["key"].(string)
}

func (f *identityFixture) policyAudits(t *testing.T, key string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='key.access_policy.update' AND target=$1`, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestKeyPolicyCreateAndPrincipal(t *testing.T) {
	f := newIdentityFixture(t)
	id, raw := f.createPolicyKey(t, f.owner, []string{"192.0.2.19/24", " 2001:DB8::1 ", "192.0.2.0/24"})
	want := []string{"192.0.2.0/24", "2001:db8::1/128"}
	p, err := f.store.APIKey(f.ctx, raw)
	if err != nil || p.KeyID != id || p.AccessPolicyRevision != "1" || !reflect.DeepEqual(p.AllowedClientCIDRs, want) || p.ClientIP != "" {
		t.Fatalf("principal=%+v err=%v", p, err)
	}
	created, err := f.store.CreateKey(f.ctx, f.keyActor(f.owner, "owner"), KeyInput{Name: "unrestricted", Scopes: []string{f.model1}})
	if err != nil || created["access_policy_revision"] != "1" || !reflect.DeepEqual(created["allowed_client_cidrs"], []string{}) {
		t.Fatalf("legacy-compatible create=%+v err=%v", created, err)
	}
	if _, err = f.store.CreateKey(f.ctx, f.keyActor(f.owner, "owner"), KeyInput{Name: "bad-source", Scopes: []string{f.model1}, AllowedClientCIDRs: []string{"not-a-host.invalid"}}); !errors.Is(err, ErrKeyPolicyInvalid) {
		t.Fatalf("invalid source create: %v", err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM api_keys`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("invalid create persisted key: %d %v", count, err)
	}
}

func TestKeyPolicyCASNoopAndSanitizedAudit(t *testing.T) {
	f := newIdentityFixture(t)
	id, raw := f.createPolicyKey(t, f.owner, nil)
	actor := f.keyActor(f.owner, "owner")
	var before []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT (to_jsonb(k)-'allowed_client_cidrs'-'access_policy_revision')::text FROM api_keys k WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	updated, err := f.store.UpdateKeyAccessPolicy(f.ctx, actor, id, KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{"192.0.2.19/24"}})
	if err != nil || updated.AccessPolicyRevision != "2" || !reflect.DeepEqual(updated.AllowedClientCIDRs, []string{"192.0.2.0/24"}) {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	var after []byte
	if err = f.pool.QueryRow(f.ctx, `SELECT (to_jsonb(k)-'allowed_client_cidrs'-'access_policy_revision')::text FROM api_keys k WHERE id=$1`, id).Scan(&after); err != nil || string(before) != string(after) {
		t.Fatalf("policy edit changed unrelated attributes: %v", err)
	}
	// Financial writes cannot invalidate an open policy editor.
	f.exec(t, `UPDATE api_keys SET spent=7,held=3 WHERE id=$1`, id)
	unchanged, err := f.store.UpdateKeyAccessPolicy(f.ctx, actor, id, KeyAccessPolicyInput{ExpectedRevision: "2", AllowedClientCIDRs: []string{"192.0.2.19/24", "192.0.2.0/24"}})
	if err != nil || !reflect.DeepEqual(unchanged, updated) || f.policyAudits(t, id) != 1 {
		t.Fatalf("canonical no-op invalidated revision or audited: %+v %v", unchanged, err)
	}
	if _, err = f.store.UpdateKeyAccessPolicy(f.ctx, actor, id, KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{"192.0.2.0/24"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale canonical no-op bypassed CAS: %v", err)
	}
	var diff map[string]any
	if err = f.pool.QueryRow(f.ctx, `SELECT sanitized_diff FROM audit_events WHERE action='key.access_policy.update' AND target=$1`, id).Scan(&diff); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"before": map[string]any{"allowed_client_cidrs": []any{}, "access_policy_revision": "1"}, "after": map[string]any{"allowed_client_cidrs": []any{"192.0.2.0/24"}, "access_policy_revision": "2"}}
	if !reflect.DeepEqual(diff, want) {
		t.Fatalf("unexpected audit metadata: %+v", diff)
	}
	encoded, _ := json.Marshal(diff)
	if strings.Contains(string(encoded), raw) || strings.Contains(string(encoded), "digest") {
		t.Fatal("audit exposed key authentication material")
	}
	cleared, err := f.store.UpdateKeyAccessPolicy(f.ctx, actor, id, KeyAccessPolicyInput{ExpectedRevision: "2", AllowedClientCIDRs: []string{}})
	if err != nil || cleared.AccessPolicyRevision != "3" || cleared.AllowedClientCIDRs == nil || len(cleared.AllowedClientCIDRs) != 0 || f.policyAudits(t, id) != 2 {
		t.Fatalf("explicit empty replacement=%+v err=%v", cleared, err)
	}
	for _, revision := range []string{"", "0", "-1", "+1", "01", " 1", "1.0", "9223372036854775808"} {
		if _, err = f.store.UpdateKeyAccessPolicy(f.ctx, actor, id, KeyAccessPolicyInput{ExpectedRevision: revision, AllowedClientCIDRs: []string{}}); !errors.Is(err, ErrKeyPolicyInvalid) {
			t.Fatalf("invalid revision %q: %v", revision, err)
		}
	}
	if _, err = f.store.UpdateKeyAccessPolicy(f.ctx, actor, id, KeyAccessPolicyInput{ExpectedRevision: "3"}); !errors.Is(err, ErrKeyPolicyInvalid) {
		t.Fatalf("nil replacement was treated as explicit clear: %v", err)
	}
}

func TestKeyPolicyCurrentRoleAndVisibility(t *testing.T) {
	f := newIdentityFixture(t)
	ownerKey, _ := f.createPolicyKey(t, f.owner, nil)
	memberKey, _ := f.createPolicyKey(t, f.member, nil)
	owner, member := f.keyActor(f.owner, "owner"), f.keyActor(f.member, "member")
	if _, err := f.store.UpdateKeyAccessPolicy(f.ctx, member, ownerKey, KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member edited another user's key: %v", err)
	}
	if _, err := f.store.KeyAccessPolicy(f.ctx, member, ownerKey); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member read another user's policy: %v", err)
	}
	if _, err := f.store.UpdateKeyAccessPolicy(f.ctx, owner, memberKey, KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{"192.0.2.1"}}); err != nil {
		t.Fatalf("owner could not manage member key: %v", err)
	}
	if _, err := f.store.UpdateKeyAccessPolicy(f.ctx, member, memberKey, KeyAccessPolicyInput{ExpectedRevision: "2", AllowedClientCIDRs: []string{"198.51.100.1"}}); err != nil {
		t.Fatalf("member could not manage own key: %v", err)
	}
	f.exec(t, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.member)
	if _, err := f.store.KeyAccessPolicy(f.ctx, member, memberKey); err != nil {
		t.Fatalf("current billing viewer cannot inspect own policy: %v", err)
	}
	if _, err := f.store.UpdateKeyAccessPolicy(f.ctx, member, memberKey, KeyAccessPolicyInput{ExpectedRevision: "3", AllowedClientCIDRs: []string{}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale member role let viewer edit policy: %v", err)
	}
	foreign := uuid.NewString()
	f.exec(t, `INSERT INTO tenants(id,name) VALUES($1,'foreign')`, foreign)
	f.exec(t, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, foreign, f.owner)
	foreignActor := &Session{User: User{ID: f.owner}, Tenant: &Tenant{ID: foreign, Role: "owner"}}
	if _, err := f.store.KeyAccessPolicy(f.ctx, foreignActor, ownerKey); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant policy read: %v", err)
	}
	if _, err := f.store.UpdateKeyAccessPolicy(f.ctx, foreignActor, ownerKey, KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant policy edit: %v", err)
	}
	// Expiry is inspectable/editable, and source editing never restores expiry.
	f.exec(t, `UPDATE api_keys SET expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, ownerKey)
	if _, err := f.store.UpdateKeyAccessPolicy(f.ctx, owner, ownerKey, KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{"192.0.2.0/24"}}); err != nil {
		t.Fatalf("expired key cannot edit source metadata: %v", err)
	}
	f.exec(t, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, ownerKey)
	if _, err := f.store.KeyAccessPolicy(f.ctx, owner, ownerKey); err != nil {
		t.Fatalf("revoked policy cannot be inspected: %v", err)
	}
	if _, err := f.store.UpdateKeyAccessPolicy(f.ctx, owner, ownerKey, KeyAccessPolicyInput{ExpectedRevision: "2", AllowedClientCIDRs: []string{}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked key edited: %v", err)
	}
	for _, id := range []string{"", "not-a-key", strings.ToUpper(ownerKey), strings.ReplaceAll(ownerKey, "-", "")} {
		if _, err := f.store.KeyAccessPolicy(f.ctx, owner, id); !errors.Is(err, ErrKeyPolicyInvalid) {
			t.Fatalf("noncanonical read id %q: %v", id, err)
		}
	}
	for _, target := range []string{"tenant", "member", "user"} {
		t.Run("current_suspended_"+target, func(t *testing.T) {
			query, args := `UPDATE tenants SET status=$1 WHERE id=$2`, []any{"suspended", f.tenant}
			if target == "member" {
				query, args = `UPDATE memberships SET status=$1 WHERE tenant_id=$2 AND user_id=$3`, []any{"suspended", f.tenant, f.owner}
			} else if target == "user" {
				query, args = `UPDATE users SET status=$1 WHERE id=$2`, []any{"suspended", f.owner}
			}
			f.exec(t, query, args...)
			if _, err := f.store.KeyAccessPolicy(f.ctx, owner, memberKey); !errors.Is(err, ErrForbidden) {
				t.Fatalf("suspended %s policy read: %v", target, err)
			}
			if _, err := f.store.UpdateKeyAccessPolicy(f.ctx, owner, memberKey, KeyAccessPolicyInput{ExpectedRevision: "3", AllowedClientCIDRs: []string{}}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("suspended %s policy edit: %v", target, err)
			}
			args[0] = "active"
			f.exec(t, query, args...)
		})
	}
}

func TestKeyPolicyAuditFailureRollsBackAndConcurrentCAS(t *testing.T) {
	f := newIdentityFixture(t)
	id, _ := f.createPolicyKey(t, f.owner, nil)
	actor := f.keyActor(f.owner, "owner")
	f.exec(t, `CREATE FUNCTION fail_policy_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='key.access_policy.update' THEN RAISE EXCEPTION 'injected policy audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_policy_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_policy_audit()`)
	input := KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{"192.0.2.1"}}
	if _, err := f.store.UpdateKeyAccessPolicy(f.ctx, actor, id, input); err == nil {
		t.Fatal("audit rejection still reported success")
	}
	view, err := f.store.KeyAccessPolicy(f.ctx, actor, id)
	if err != nil || view.AccessPolicyRevision != "1" || len(view.AllowedClientCIDRs) != 0 || f.policyAudits(t, id) != 0 {
		t.Fatalf("audit failure left partial state: %+v %v", view, err)
	}
	f.exec(t, `DROP TRIGGER fail_policy_audit ON audit_events`)
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, ip := range []string{"192.0.2.1", "198.51.100.1"} {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			<-start
			_, err := f.store.UpdateKeyAccessPolicy(f.ctx, actor, id, KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{ip}})
			results <- err
		}(ip)
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
	if success != 1 || conflict != 1 || f.policyAudits(t, id) != 1 {
		t.Fatalf("concurrent CAS success=%d conflict=%d audit=%d", success, conflict, f.policyAudits(t, id))
	}
}

func waitPolicyBlocked(t *testing.T, f *identityFixture, blocker uint32, queryPrefix string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		err := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE $2)`, blocker, queryPrefix+"%").Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("policy mutation never reached expected PostgreSQL lock barrier")
}

func TestKeyPolicyRechecksAfterLockWait(t *testing.T) {
	for _, boundary := range []string{"membership", "user", "key"} {
		t.Run(boundary, func(t *testing.T) {
			f := newIdentityFixture(t)
			id, _ := f.createPolicyKey(t, f.owner, nil)
			tx, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			var locked string
			var lockSQL, editSQL, queryPrefix string
			var args []any
			switch boundary {
			case "membership":
				lockSQL, editSQL, queryPrefix, args = `SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR UPDATE`, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, "SELECT role FROM memberships", []any{f.tenant, f.owner}
			case "user":
				lockSQL, editSQL, queryPrefix, args = `SELECT id FROM users WHERE id=$1 FOR UPDATE`, `UPDATE users SET status='suspended' WHERE id=$1`, "SELECT id FROM users", []any{f.owner}
			case "key":
				lockSQL, editSQL, queryPrefix, args = `SELECT id FROM api_keys WHERE id=$1 FOR UPDATE`, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, "SELECT id,owner_id,allowed_client_cidrs", []any{id}
			}
			if err = tx.QueryRow(f.ctx, lockSQL, args...).Scan(&locked); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := f.store.UpdateKeyAccessPolicy(ctx, f.keyActor(f.owner, "owner"), id, KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{"192.0.2.1"}})
				done <- err
			}()
			waitPolicyBlocked(t, f, tx.Conn().PgConn().PID(), queryPrefix)
			if _, err = tx.Exec(f.ctx, editSQL, args...); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, ErrForbidden) {
				t.Fatalf("fresh %s state ignored after lock wait: %v", boundary, err)
			}
			var revision string
			if err = f.pool.QueryRow(f.ctx, `SELECT access_policy_revision::text FROM api_keys WHERE id=$1`, id).Scan(&revision); err != nil || revision != "1" || f.policyAudits(t, id) != 0 {
				t.Fatalf("denied mutation persisted revision=%s err=%v", revision, err)
			}
		})
	}
}
