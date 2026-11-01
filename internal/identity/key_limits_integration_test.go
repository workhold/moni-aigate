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
	"github.com/jackc/pgx/v5/pgxpool"
)

func keyLimitsInput(revision string, rpm, burst, concurrency int) KeyLimitsInput {
	return KeyLimitsInput{ExpectedRevision: revision, RPMLimit: keyLimitInt(rpm), BurstLimit: keyLimitInt(burst), ConcurrencyLimit: keyLimitInt(concurrency)}
}

func (f *identityFixture) limitAudits(t *testing.T, key string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='key.runtime_limits.update' AND target=$1`, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestKeyRuntimeLimitsCreatePrincipalAndIndependentCAS(t *testing.T) {
	f := newIdentityFixture(t)
	actor := f.keyActor(f.owner, "owner")
	id, raw := f.createPolicyKey(t, f.owner, nil)
	principal, err := f.store.APIKey(f.ctx, raw)
	if err != nil || principal.RPMLimit != 60 || principal.BurstLimit != 20 || principal.ConcurrencyLimit != 16 || principal.LimitRevision != "1" {
		t.Fatalf("legacy defaults missing from principal: %+v %v", principal, err)
	}
	created, err := f.store.CreateKey(f.ctx, actor, KeyInput{Name: "explicit", Scopes: []string{f.model1}, RPMLimit: keyLimitInt(1), BurstLimit: keyLimitInt(200), ConcurrencyLimit: keyLimitInt(1)})
	if err != nil || created["rpm_limit"] != 1 || created["burst_limit"] != 200 || created["concurrency_limit"] != 1 || created["limit_revision"] != "1" {
		t.Fatalf("custom create limits: %+v %v", created, err)
	}
	if _, err = f.store.CreateKey(f.ctx, actor, KeyInput{Name: "bad", Scopes: []string{f.model1}, RPMLimit: keyLimitInt(0)}); !errors.Is(err, ErrKeyLimitsInvalid) {
		t.Fatalf("explicit zero selected defaults: %v", err)
	}
	var before []byte
	if err = f.pool.QueryRow(f.ctx, `SELECT (to_jsonb(k)-'rpm_limit'-'burst_limit'-'concurrency_limit'-'limit_revision')::text FROM api_keys k WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	updated, err := f.store.UpdateKeyRuntimeLimits(f.ctx, actor, id, keyLimitsInput("1", 7, 8, 2))
	if err != nil || updated.RPMLimit != 7 || updated.BurstLimit != 8 || updated.ConcurrencyLimit != 2 || updated.LimitRevision != "2" {
		t.Fatalf("limits update: %+v %v", updated, err)
	}
	var after []byte
	if err = f.pool.QueryRow(f.ctx, `SELECT (to_jsonb(k)-'rpm_limit'-'burst_limit'-'concurrency_limit'-'limit_revision')::text FROM api_keys k WHERE id=$1`, id).Scan(&after); err != nil || string(before) != string(after) {
		t.Fatalf("runtime edit changed unrelated key fields: %v", err)
	}
	policy, err := f.store.UpdateKeyAccessPolicy(f.ctx, actor, id, KeyAccessPolicyInput{ExpectedRevision: "1", AllowedClientCIDRs: []string{"192.0.2.1"}})
	if err != nil || policy.AccessPolicyRevision != "2" {
		t.Fatalf("runtime edit invalidated source CAS: %+v %v", policy, err)
	}
	f.exec(t, `UPDATE api_keys SET spent=9,held=2 WHERE id=$1`, id)
	unchanged, err := f.store.UpdateKeyRuntimeLimits(f.ctx, actor, id, keyLimitsInput("2", 7, 8, 2))
	if err != nil || !reflect.DeepEqual(unchanged, updated) || f.limitAudits(t, id) != 1 {
		t.Fatalf("source/financial change invalidated runtime editor: %+v %v", unchanged, err)
	}
	if _, err = f.store.UpdateKeyRuntimeLimits(f.ctx, actor, id, keyLimitsInput("1", 7, 8, 2)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale runtime no-op bypassed CAS: %v", err)
	}
	var diff map[string]any
	if err = f.pool.QueryRow(f.ctx, `SELECT sanitized_diff FROM audit_events WHERE action='key.runtime_limits.update' AND target=$1`, id).Scan(&diff); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"before": map[string]any{"rpm_limit": float64(60), "burst_limit": float64(20), "concurrency_limit": float64(16), "limit_revision": "1"}, "after": map[string]any{"rpm_limit": float64(7), "burst_limit": float64(8), "concurrency_limit": float64(2), "limit_revision": "2"}}
	if !reflect.DeepEqual(diff, want) {
		t.Fatalf("unexpected runtime audit: %+v", diff)
	}
	encoded, _ := json.Marshal(diff)
	if strings.Contains(string(encoded), raw) || strings.Contains(string(encoded), "digest") || strings.Contains(string(encoded), "allowed_client_cidrs") {
		t.Fatal("runtime audit included unrelated or secret attributes")
	}
	called := 0
	if err = f.store.WithKeyRuntimeLimits(f.ctx, principal, func(view KeyLimitsView) error {
		called++
		if !reflect.DeepEqual(view, updated) {
			t.Fatalf("old auth limits used: %+v want %+v", view, updated)
		}
		return nil
	}); err != nil || called != 1 {
		t.Fatalf("fresh runtime callback count=%d err=%v", called, err)
	}
	for _, revision := range []string{"", "0", "-1", "+1", "01", "1.0", "9223372036854775808"} {
		if _, err = f.store.UpdateKeyRuntimeLimits(f.ctx, actor, id, keyLimitsInput(revision, 7, 8, 2)); !errors.Is(err, ErrKeyLimitsInvalid) {
			t.Fatalf("bad revision %q: %v", revision, err)
		}
	}
	for _, input := range []KeyLimitsInput{{ExpectedRevision: "2"}, {ExpectedRevision: "2", RPMLimit: keyLimitInt(60), BurstLimit: keyLimitInt(20)}, {ExpectedRevision: "2", RPMLimit: keyLimitInt(60), ConcurrencyLimit: keyLimitInt(16)}} {
		if _, err = f.store.UpdateKeyRuntimeLimits(f.ctx, actor, id, input); !errors.Is(err, ErrKeyLimitsInvalid) {
			t.Fatalf("partial runtime replacement accepted: %v", err)
		}
	}
}

func TestKeyRuntimeLimitsPermissionsReadAndAuditRollback(t *testing.T) {
	f := newIdentityFixture(t)
	ownerKey, _ := f.createPolicyKey(t, f.owner, nil)
	memberKey, _ := f.createPolicyKey(t, f.member, nil)
	owner, member := f.keyActor(f.owner, "owner"), f.keyActor(f.member, "member")
	if _, err := f.store.KeyRuntimeLimits(f.ctx, member, ownerKey); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member read another key: %v", err)
	}
	if _, err := f.store.UpdateKeyRuntimeLimits(f.ctx, member, ownerKey, keyLimitsInput("1", 1, 1, 1)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member edited another key: %v", err)
	}
	if _, err := f.store.UpdateKeyRuntimeLimits(f.ctx, owner, memberKey, keyLimitsInput("1", 1, 1, 1)); err != nil {
		t.Fatalf("owner denied member key edit: %v", err)
	}
	if _, err := f.store.UpdateKeyRuntimeLimits(f.ctx, member, memberKey, keyLimitsInput("2", 2, 2, 2)); err != nil {
		t.Fatalf("member denied own key edit: %v", err)
	}
	f.exec(t, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.member)
	if _, err := f.store.KeyRuntimeLimits(f.ctx, member, memberKey); err != nil {
		t.Fatalf("viewer cannot read own limits: %v", err)
	}
	if _, err := f.store.UpdateKeyRuntimeLimits(f.ctx, member, memberKey, keyLimitsInput("3", 1, 1, 1)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale member role let viewer edit: %v", err)
	}
	foreign := uuid.NewString()
	f.exec(t, `INSERT INTO tenants(id,name) VALUES($1,'runtime foreign')`, foreign)
	f.exec(t, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, foreign, f.owner)
	foreignActor := &Session{User: User{ID: f.owner}, Tenant: &Tenant{ID: foreign, Role: "owner"}}
	if _, err := f.store.KeyRuntimeLimits(f.ctx, foreignActor, ownerKey); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant read: %v", err)
	}
	if _, err := f.store.UpdateKeyRuntimeLimits(f.ctx, foreignActor, ownerKey, keyLimitsInput("1", 1, 1, 1)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant edit: %v", err)
	}
	for _, badID := range []string{"", "invalid", strings.ToUpper(ownerKey), strings.ReplaceAll(ownerKey, "-", "")} {
		if _, err := f.store.KeyRuntimeLimits(f.ctx, owner, badID); !errors.Is(err, ErrKeyLimitsInvalid) {
			t.Fatalf("bad read ID %q: %v", badID, err)
		}
		if _, err := f.store.UpdateKeyRuntimeLimits(f.ctx, owner, badID, keyLimitsInput("1", 1, 1, 1)); !errors.Is(err, ErrKeyLimitsInvalid) {
			t.Fatalf("bad edit ID %q: %v", badID, err)
		}
	}
	f.exec(t, `UPDATE api_keys SET expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, ownerKey)
	if _, err := f.store.UpdateKeyRuntimeLimits(f.ctx, owner, ownerKey, keyLimitsInput("1", 7, 8, 2)); err != nil {
		t.Fatalf("expired key cannot edit limits metadata: %v", err)
	}
	f.exec(t, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, ownerKey)
	if _, err := f.store.KeyRuntimeLimits(f.ctx, owner, ownerKey); err != nil {
		t.Fatalf("revoked limits cannot be read: %v", err)
	}
	if _, err := f.store.UpdateKeyRuntimeLimits(f.ctx, owner, ownerKey, keyLimitsInput("2", 1, 1, 1)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked key edited: %v", err)
	}
	f.exec(t, `CREATE FUNCTION fail_runtime_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='key.runtime_limits.update' THEN RAISE EXCEPTION 'injected runtime audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_runtime_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_runtime_audit()`)
	if _, err := f.store.UpdateKeyRuntimeLimits(f.ctx, owner, memberKey, keyLimitsInput("3", 3, 3, 3)); err == nil {
		t.Fatal("failed audit reported success")
	}
	view, err := f.store.KeyRuntimeLimits(f.ctx, owner, memberKey)
	if err != nil || view.LimitRevision != "3" || view.RPMLimit != 2 || f.limitAudits(t, memberKey) != 2 {
		t.Fatalf("audit failure left partial limits: %+v %v", view, err)
	}
}

func TestKeyRuntimeLimitsConcurrentCAS(t *testing.T) {
	f := newIdentityFixture(t)
	id, _ := f.createPolicyKey(t, f.owner, nil)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, rpm := range []int{7, 8} {
		wg.Add(1)
		go func(rpm int) {
			defer wg.Done()
			<-start
			_, err := f.store.UpdateKeyRuntimeLimits(f.ctx, f.keyActor(f.owner, "owner"), id, keyLimitsInput("1", rpm, 8, 2))
			results <- err
		}(rpm)
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
	if success != 1 || conflict != 1 || f.limitAudits(t, id) != 1 {
		t.Fatalf("concurrent limits CAS success=%d conflict=%d audit=%d", success, conflict, f.limitAudits(t, id))
	}
}

func TestWithKeyRuntimeLimitsWaitsForCurrentCommittedTuple(t *testing.T) {
	f := newIdentityFixture(t)
	id, raw := f.createPolicyKey(t, f.owner, nil)
	p, err := f.store.APIKey(f.ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `UPDATE api_keys SET rpm_limit=7,burst_limit=8,concurrency_limit=2 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	var received KeyLimitsView
	go func() {
		done <- f.store.WithKeyRuntimeLimits(ctx, p, func(view KeyLimitsView) error { received = view; return nil })
	}()
	waitPolicyBlocked(t, f, tx.Conn().PgConn().PID(), "SELECT id,owner_id,rpm_limit")
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil || received.RPMLimit != 7 || received.BurstLimit != 8 || received.ConcurrencyLimit != 2 || received.LimitRevision != "2" {
		t.Fatalf("runtime lock reused old auth tuple: %+v %v", received, err)
	}
	if p.RPMLimit != 60 || p.LimitRevision != "1" {
		t.Fatal("fixture auth snapshot was not stale")
	}
	callbackErr := errors.New("synthetic memory admission denial")
	if err = f.store.WithKeyRuntimeLimits(f.ctx, p, func(KeyLimitsView) error { return callbackErr }); !errors.Is(err, callbackErr) {
		t.Fatalf("callback denial changed: %v", err)
	}
	if f.limitAudits(t, id) != 0 {
		t.Fatal("runtime reads created configuration audit")
	}
}

func TestWithKeyRuntimeLimitsHoldsPolicyLockThroughCallback(t *testing.T) {
	f := newIdentityFixture(t)
	id, raw := f.createPolicyKey(t, f.owner, nil)
	p, err := f.store.APIKey(f.ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.pool.Config()
	name := "runtime_callback_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := New(pool, f.store.Secrets)
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.WithKeyRuntimeLimits(ctx, p, func(view KeyLimitsView) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("runtime callback never entered")
	}
	var blocker uint32
	if err = f.pool.QueryRow(f.ctx, `SELECT pid FROM pg_stat_activity WHERE application_name=$1 AND state='idle in transaction'`, name).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	edit := make(chan error, 1)
	go func() {
		_, err := f.store.UpdateKeyRuntimeLimits(ctx, f.keyActor(f.owner, "owner"), id, keyLimitsInput("1", 7, 8, 2))
		edit <- err
	}()
	waitPolicyBlocked(t, f, blocker, "SELECT id,owner_id,rpm_limit")
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = <-edit; err != nil {
		t.Fatalf("policy edit did not resume after callback commit: %v", err)
	}
}

func TestKeyRuntimeLimitsRechecksAuthorizationAfterLockWait(t *testing.T) {
	for _, method := range []string{"admission", "edit"} {
		for _, boundary := range []string{"tenant", "membership", "user", "key"} {
			t.Run(method+"_"+boundary, func(t *testing.T) {
				f := newIdentityFixture(t)
				id, raw := f.createPolicyKey(t, f.owner, nil)
				p, err := f.store.APIKey(f.ctx, raw)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := f.pool.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(f.ctx)
				var lockSQL, editSQL, prefix string
				var args []any
				switch boundary {
				case "tenant":
					lockSQL, editSQL, prefix, args = `SELECT id FROM tenants WHERE id=$1 FOR UPDATE`, `UPDATE tenants SET status='suspended' WHERE id=$1`, "SELECT id FROM tenants", []any{f.tenant}
				case "membership":
					lockSQL, prefix, args = `SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR UPDATE`, "SELECT user_id FROM memberships", []any{f.tenant, f.owner}
					editSQL = `UPDATE memberships SET status='suspended' WHERE tenant_id=$1 AND user_id=$2`
					if method == "edit" {
						prefix = "SELECT role FROM memberships"
						editSQL = `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`
					}
				case "user":
					lockSQL, editSQL, prefix, args = `SELECT id FROM users WHERE id=$1 FOR UPDATE`, `UPDATE users SET status='suspended' WHERE id=$1`, "SELECT id FROM users", []any{f.owner}
				case "key":
					lockSQL, editSQL, prefix, args = `SELECT id FROM api_keys WHERE id=$1 FOR UPDATE`, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, "SELECT id,owner_id,rpm_limit", []any{id}
				}
				var locked string
				if err = tx.QueryRow(f.ctx, lockSQL, args...).Scan(&locked); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
				defer cancel()
				done := make(chan error, 1)
				called := false
				go func() {
					if method == "admission" {
						done <- f.store.WithKeyRuntimeLimits(ctx, p, func(KeyLimitsView) error { called = true; return nil })
					} else {
						_, err := f.store.UpdateKeyRuntimeLimits(ctx, f.keyActor(f.owner, "owner"), id, keyLimitsInput("1", 7, 8, 2))
						done <- err
					}
				}()
				waitPolicyBlocked(t, f, tx.Conn().PgConn().PID(), prefix)
				if _, err = tx.Exec(f.ctx, editSQL, args...); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(f.ctx); err != nil {
					t.Fatal(err)
				}
				want := ErrForbidden
				if method == "admission" && boundary == "key" {
					want = ErrInvalid
				}
				if err = <-done; !errors.Is(err, want) || called {
					t.Fatalf("stale %s %s authorization: callback=%v err=%v", method, boundary, called, err)
				}
				if f.limitAudits(t, id) != 0 {
					t.Fatal("denied operation created runtime audit")
				}
			})
		}
	}
}

func TestWithKeyRuntimeLimitsUsesDatabaseTimeAfterUnchangedKeyLockWait(t *testing.T) {
	f := newIdentityFixture(t)
	id, raw := f.createPolicyKey(t, f.owner, nil)
	p, err := f.store.APIKey(f.ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE api_keys SET expires_at=clock_timestamp()+interval '200 milliseconds' WHERE id=$1`, id)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	var locked string
	if err = tx.QueryRow(f.ctx, `SELECT id FROM api_keys WHERE id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	called := false
	go func() {
		done <- f.store.WithKeyRuntimeLimits(ctx, p, func(KeyLimitsView) error { called = true; return nil })
	}()
	waitPolicyBlocked(t, f, tx.Conn().PgConn().PID(), "SELECT id,owner_id,rpm_limit")
	for {
		var expired bool
		if err = f.pool.QueryRow(ctx, `SELECT expires_at<=clock_timestamp() FROM api_keys WHERE id=$1`, id).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrInvalid) || called {
		t.Fatalf("expiry was decided before unchanged row wait: callback=%v err=%v", called, err)
	}
}

func TestWithKeyRuntimeLimitsCommitFailureReturnsAndDrains(t *testing.T) {
	f := newIdentityFixture(t)
	_, raw := f.createPolicyKey(t, f.owner, nil)
	p, err := f.store.APIKey(f.ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	called := 0
	err = f.store.WithKeyRuntimeLimits(ctx, p, func(KeyLimitsView) error { called++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || called != 1 {
		t.Fatalf("commit failure hidden or callback repeated: called=%d err=%v", called, err)
	}
	if err = f.store.WithKeyRuntimeLimits(f.ctx, p, func(KeyLimitsView) error { return nil }); err != nil {
		t.Fatalf("failed commit left unusable connection or locks: %v", err)
	}
}
