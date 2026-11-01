package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Observe an actual PostgreSQL waiter rather than guessing when the batch has
// taken its first statement snapshot. The blocker belongs to this fixture.
func waitForBatchBlocker(t *testing.T, f *fixture, pid int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
			t.Fatal("did not observe parent lock waiter", err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("batch did not wait for its parent lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestPostgresAuthorizationParentBatchSeesCommittedRevocation(t *testing.T) {
	for _, boundary := range []string{"tenant_grant", "member_grant", "tenant_status", "member_status", "user_status", "key_revoked", "model_disabled", "profile_block"} {
		for _, operation := range []string{"reserve", "dispatch"} {
			t.Run(boundary+"_"+operation, func(t *testing.T) {
				f := integration(t)
				if boundary == "member_grant" {
					if _, err := f.pool.Exec(f.ctx, "UPDATE memberships SET allow_all_models=false WHERE tenant_id=$1 AND user_id=$2", f.tenant, f.user); err != nil {
						t.Fatal(err)
					}
					if _, err := f.pool.Exec(f.ctx, "INSERT INTO member_model_grants(tenant_id,user_id,model_id) VALUES($1,$2,$3)", f.tenant, f.user, f.model); err != nil {
						t.Fatal(err)
					}
				}
				r := f.reserve(t)
				a, err := f.store.RecordAttempt(f.ctx, AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1})
				if err != nil {
					t.Fatal(err)
				}
				blocker, err := f.pool.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Rollback(f.ctx)
				var pid int
				if err = blocker.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
				exec := func(sql string, args ...any) {
					t.Helper()
					if _, err := blocker.Exec(f.ctx, sql, args...); err != nil {
						t.Fatal(err)
					}
				}
				want := "forbidden"
				switch boundary {
				case "tenant_grant":
					exec("SELECT id FROM tenants WHERE id=$1 FOR UPDATE", f.tenant)
					exec("DELETE FROM tenant_model_grants WHERE tenant_id=$1 AND model_id=$2", f.tenant, f.model)
				case "member_grant":
					exec("SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR UPDATE", f.tenant, f.user)
					exec("DELETE FROM member_model_grants WHERE tenant_id=$1 AND user_id=$2 AND model_id=$3", f.tenant, f.user, f.model)
				case "tenant_status":
					exec("UPDATE tenants SET status='suspended' WHERE id=$1", f.tenant)
				case "member_status":
					exec("UPDATE memberships SET status='suspended' WHERE tenant_id=$1 AND user_id=$2", f.tenant, f.user)
				case "user_status":
					exec("UPDATE users SET status='suspended' WHERE id=$1", f.user)
				case "key_revoked":
					exec("UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1", f.key)
					want = "invalid_key"
				case "model_disabled":
					exec("UPDATE models SET enabled=false WHERE id=$1", f.model)
				case "profile_block":
					exec("SELECT id FROM models WHERE id=$1 FOR UPDATE", f.model)
					exec("INSERT INTO billing_profile_blocks(model_id,profile_version,reason,request_id) VALUES($1,'v1','test',$2)", f.model, r.RequestID)
					want = "profile_blocked"
				}
				ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
				defer cancel()
				done := make(chan error, 1)
				input := f.input()
				go func() {
					if operation == "dispatch" {
						done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID})
					} else {
						_, err := f.store.Reserve(ctx, input)
						done <- err
					}
				}()
				waitForBatchBlocker(t, f, pid)
				if err = blocker.Commit(f.ctx); err != nil {
					t.Fatal(err)
				}
				assertCode(t, <-done, want)
				var requestStatus, attemptState string
				var requests int
				if err = f.pool.QueryRow(f.ctx, "SELECT r.status,a.delivery_state,(SELECT count(*) FROM request_records) FROM request_records r JOIN upstream_attempts a ON a.request_id=r.id WHERE a.id=$1", a.ID).Scan(&requestStatus, &attemptState, &requests); err != nil || requestStatus != "accepted" || attemptState != "prepared" || requests != 1 {
					t.Fatal("revocation allowed partial admission", requestStatus, attemptState, requests, err)
				}
				f.wallet(t, 880, 120)
				f.check(t)
			})
		}
	}
}

func TestPostgresRequestBatchRefreshesEvidenceAfterReservationLock(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	var pid int
	if err = blocker.QueryRow(f.ctx, "SELECT pg_backend_pid() FROM reservations WHERE request_id=$1 FOR UPDATE", r.RequestID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(f.ctx, "UPDATE request_records SET status='cancelled' WHERE id=$1", r.RequestID); err != nil {
		t.Fatal(err)
	}
	type output struct {
		request financialRequest
		err     error
	}
	done := make(chan output, 1)
	go func() {
		var request financialRequest
		err := f.store.transaction(f.ctx, func(tx pgx.Tx) error {
			var err error
			request, err = lockRequest(f.ctx, tx, f.tenant, r.RequestID)
			return err
		})
		done <- output{request, err}
	}()
	waitForBatchBlocker(t, f, pid)
	if err = blocker.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.request.Status != "cancelled" {
		t.Fatal("returned pre-lock request evidence", got.request.Status, got.err)
	}
}

func TestPostgresParentBatchEarlyErrorsDrainAndRollback(t *testing.T) {
	for _, boundary := range []string{"missing_key", "suspended_tenant"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			input := f.input()
			if boundary == "missing_key" {
				input.Principal.KeyID = uuid.NewString()
			} else if _, err := f.pool.Exec(f.ctx, "UPDATE tenants SET status='suspended' WHERE id=$1", f.tenant); err != nil {
				t.Fatal(err)
			}
			_, err := f.store.Reserve(f.ctx, input)
			if boundary == "missing_key" {
				if !errors.Is(err, pgx.ErrNoRows) {
					t.Fatal("lost missing-row semantics", err)
				}
			} else {
				assertCode(t, err, "forbidden")
			}
			f.wallet(t, 1000, 0)
			input.Principal.KeyID = f.key
			if _, err = f.pool.Exec(f.ctx, "UPDATE tenants SET status='active' WHERE id=$1", f.tenant); err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.Reserve(f.ctx, input); err != nil {
				t.Fatal("batch was not drained or rollback was incomplete", err)
			}
			f.wallet(t, 880, 120)
			f.check(t)
		})
	}
}

func TestPostgresAttemptReturningAndDispatchBatchRollback(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	in := AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1}
	a, err := f.store.RecordAttempt(f.ctx, in)
	if err != nil || a.DeliveryState != "prepared" {
		t.Fatal(a, err)
	}
	again, err := f.store.RecordAttempt(f.ctx, in)
	if err != nil || again != a {
		t.Fatal("returning conflict lost existing attempt", again, a, err)
	}
	in.UpstreamModel = "other-upstream"
	_, err = f.store.RecordAttempt(f.ctx, in)
	assertCode(t, err, "idempotency_conflict")
	if _, err = f.pool.Exec(f.ctx, `CREATE FUNCTION fail_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'dispatch batch crash'; END$$;
 CREATE TRIGGER injected BEFORE UPDATE ON request_records FOR EACH ROW EXECUTE FUNCTION fail_dispatch()`); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err == nil {
		t.Fatal("partial dispatch batch succeeded")
	}
	var state, status string
	if err = f.pool.QueryRow(f.ctx, "SELECT a.delivery_state,r.status FROM upstream_attempts a JOIN request_records r ON r.id=a.request_id WHERE a.id=$1", a.ID).Scan(&state, &status); err != nil || state != "prepared" || status != "accepted" {
		t.Fatal("dispatch batch partially committed", state, status, err)
	}
	if _, err = f.pool.Exec(f.ctx, "DROP TRIGGER injected ON request_records"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	f.wallet(t, 880, 120)
	f.check(t)
}
