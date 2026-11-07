package periodicbudget_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

type pbCommandCall func(context.Context, PutPeriodicBudget) (ConfigureResult, error)

func pbCommander(t *testing.T, f *pbTxFixture) *Commander {
	t.Helper()
	c, err := NewCommander(f.app)
	pbTxMust(t, "construct Commander with actual APP LOGIN", err)
	return c
}

func pbCommandInput(expected, limit string) PutPeriodicBudget {
	return PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: expected, PeriodKind: "utc_month", Enabled: true, LimitAmount: limit}
}

func pbCommandCallFor(c *Commander, f *pbTxFixture, scope string) pbCommandCall {
	switch scope {
	case "tenant":
		return func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
			return c.ConfigurePlatformTenant(ctx, f.user, f.tenant, in)
		}
	case "key":
		return func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
			return c.ConfigureKey(ctx, f.user, f.tenant, f.key, in)
		}
	default:
		return func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
			return c.ConfigureProcurement(ctx, f.user, f.account, in)
		}
	}
}

// This oracle frames bytes independently of the exported Fingerprint helper.
func pbCommandFingerprint(kind, actor, scope, target string, in PutPeriodicBudget) string {
	var framed []byte
	for _, field := range []string{kind, actor, scope, target, in.ExpectedRevision, in.PeriodKind, strconv.FormatBool(in.Enabled), in.LimitAmount} {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		framed = append(framed, size[:]...)
		framed = append(framed, []byte(field)...)
	}
	sum := sha256.Sum256(framed)
	return hex.EncodeToString(sum[:])
}

func pbCommandReceipt(t *testing.T, f *pbTxFixture, got ConfigureResult, in PutPeriodicBudget, scope, revision string, created bool) {
	t.Helper()
	pbTxMust(t, "validate complete committed receipt", got.Receipt.Validate())
	kind, identityScope, targetID := scope+"_periodic_budget.configure", "platform", f.tenant
	target := "/api/platform/tenants/" + f.tenant + "/periodic-budget"
	if scope == "key" {
		identityScope, targetID = f.tenant, f.key
		target = "/api/keys/" + f.key + "/periodic-budget"
	} else if scope == "procurement" {
		targetID = f.account
		target = "/api/platform/procurement-accounts/" + f.account + "/periodic-budget"
	}
	o, v := got.Receipt.Operation, got.Receipt.Result
	if got.Created != created || o.OperationID != in.OperationID || o.Kind != kind || o.Actor != f.user || o.Scope != identityScope || o.Target != target ||
		o.InputFingerprint != pbCommandFingerprint(kind, f.user, identityScope, target, in) || o.Revision != revision ||
		v.Scope != scope || v.ScopeID != targetID || v.Revision != revision || v.PeriodKind == nil || *v.PeriodKind != in.PeriodKind ||
		v.Enabled != in.Enabled || v.LimitAmount == nil || *v.LimitAmount != in.LimitAmount {
		t.Fatal("committed receipt has an incorrect identity, configuration, fingerprint or Created flag")
	}
	var bound bool
	pbTxMust(t, "verify immutable receipt binds the actual control and stored safe input", f.app.QueryRow(f.ctx, `SELECT EXISTS(
 SELECT 1 FROM periodic_budget_operations o JOIN periodic_budget_controls c ON c.id=o.resource_id
 WHERE o.operation_id=$1 AND c.id=$2 AND c.scope=$3 AND coalesce(c.account_id,c.key_id,c.tenant_id)=$4
 AND o.safe_input=jsonb_build_object('expected_revision',$5::text,'period_kind',$6::text,'enabled',$7::boolean,'limit_amount',$8::text)
 AND o.result_snapshot=$9::jsonb)`, in.OperationID, o.ResourceID, scope, targetID, in.ExpectedRevision, in.PeriodKind, in.Enabled, in.LimitAmount, string(pbTxJSON(t, v))).Scan(&bound))
	if !bound {
		t.Fatal("returned receipt is not the exact committed operation/control snapshot")
	}
}

// The shared source snapshot deliberately excludes operations, versions and
// audits; append those immutable command facts for negative/replay checks.
func pbCommandSnapshot(t *testing.T, f *pbTxFixture) string {
	t.Helper()
	var commands string
	pbTxMust(t, "read complete immutable command facts", f.app.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'operations',coalesce((SELECT jsonb_agg(to_jsonb(o) ORDER BY operation_id) FROM periodic_budget_operations o),'[]'::jsonb),
 'versions',coalesce((SELECT jsonb_agg(to_jsonb(v) ORDER BY control_id,revision) FROM periodic_budget_configuration_versions v),'[]'::jsonb),
 'audits',coalesce((SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM audit_events a),'[]'::jsonb))::text`).Scan(&commands))
	return f.snapshot(t) + commands
}

func pbCommandFailure(t *testing.T, f *pbTxFixture, call pbCommandCall, in PutPeriodicBudget, code string) {
	t.Helper()
	before := pbCommandSnapshot(t, f)
	got, err := call(f.ctx, in)
	if err == nil || ErrorCode(err) != code || !reflect.DeepEqual(got, ConfigureResult{}) {
		t.Fatalf("command safe code=%q, want %q with zero result", ErrorCode(err), code)
	}
	if before != pbCommandSnapshot(t, f) {
		t.Fatal("failed command changed a source, control, operation, version or audit")
	}
}

func TestCommanderPGLifecycleAllScopes(t *testing.T) {
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run(scope, func(t *testing.T) {
			f := newPBTxFixture(t)
			call := pbCommandCallFor(pbCommander(t, f), f, scope)
			firstInput := pbCommandInput("0", "500")
			first, err := call(f.ctx, firstInput)
			pbTxMust(t, "first configuration through Commander", err)
			pbCommandReceipt(t, f, first, firstInput, scope, "1", true)
			if first.Receipt.Result.State != "scheduled" || first.Receipt.Result.CurrentWindow != nil || first.Receipt.Result.CarryHeld != "0" ||
				first.Receipt.Result.AsOf != "2026-11-10T12:00:00.123456Z" || *first.Receipt.Result.EffectiveFrom != "2026-12-01T00:00:00.000000Z" {
				t.Fatal("first configuration did not schedule the next complete month without creating a window")
			}
			beforeReplay := pbCommandSnapshot(t, f)
			replay, err := call(f.ctx, firstInput)
			pbTxMust(t, "same-operation original receipt replay", err)
			if replay.Created || !reflect.DeepEqual(replay.Receipt, first.Receipt) || beforeReplay != pbCommandSnapshot(t, f) {
				t.Fatal("same-operation replay changed the original receipt or persisted facts")
			}
			var originalTuple, originalPointer string
			pbTxMust(t, "remember exact original control tuple", f.app.QueryRow(f.ctx, `SELECT ctid::text,configuration_operation_id FROM periodic_budget_controls WHERE id=$1`, first.Receipt.Operation.ResourceID).Scan(&originalTuple, &originalPointer))
			noopInput := pbCommandInput("1", "500")
			noop, err := call(f.ctx, noopInput)
			pbTxMust(t, "new-operation no-op configuration", err)
			pbCommandReceipt(t, f, noop, noopInput, scope, "1", false)
			var noopTuple, noopPointer string
			pbTxMust(t, "verify no-op did not issue a control UPDATE", f.app.QueryRow(f.ctx, `SELECT ctid::text,configuration_operation_id FROM periodic_budget_controls WHERE id=$1`, first.Receipt.Operation.ResourceID).Scan(&noopTuple, &noopPointer))
			if originalTuple != noopTuple || originalPointer != noopPointer {
				t.Fatal("no-op rewrote the control or replaced its configuration operation")
			}
			changedInput := pbCommandInput("1", "0")
			changedInput.Enabled = false
			changed, err := call(f.ctx, changedInput)
			pbTxMust(t, "real configuration change", err)
			pbCommandReceipt(t, f, changed, changedInput, scope, "2", false)
			if changed.Receipt.Operation.ResourceID != first.Receipt.Operation.ResourceID || noop.Receipt.Operation.ResourceID != first.Receipt.Operation.ResourceID || changed.Receipt.Result.State != "disabled" {
				t.Fatal("configuration replaced the stable control identity or lost disabled state")
			}
			var operations, versions, audits int
			pbTxMust(t, "verify first/no-op/change facts", f.app.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM periodic_budget_operations),
 (SELECT count(*) FROM periodic_budget_configuration_versions),
 (SELECT count(*) FROM audit_events WHERE action='periodic_budget.configured')`).Scan(&operations, &versions, &audits))
			if operations != 3 || versions != 2 || audits != 3 {
				t.Fatal("first/no-op/change did not produce exactly three receipts/audits and two configuration versions")
			}
			f.setClock(t, time.Date(2027, 1, 10, 12, 0, 0, 123456000, time.UTC))
			beforeReplay = pbCommandSnapshot(t, f)
			old, err := call(f.ctx, firstInput)
			pbTxMust(t, "old expected revision replay after changes and time", err)
			if old.Created || !reflect.DeepEqual(old.Receipt, first.Receipt) || beforeReplay != pbCommandSnapshot(t, f) {
				t.Fatal("old operation was re-evaluated against the current CAS, configuration or clock")
			}
			f.verifyFinance(t)
		})
	}
}

func TestCommanderPGCASAndCalendarBeforeAnyMutation(t *testing.T) {
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run(scope, func(t *testing.T) {
			f := newPBTxFixture(t)
			call := pbCommandCallFor(pbCommander(t, f), f, scope)
			pbCommandFailure(t, f, call, pbCommandInput("1", "500"), "periodic_budget_conflict")
			in := pbCommandInput("0", "500")
			_, err := call(f.ctx, in)
			pbTxMust(t, "establish current CAS", err)
			pbCommandFailure(t, f, call, pbCommandInput("0", "500"), "periodic_budget_conflict")
			pbCommandFailure(t, f, call, pbCommandInput("0", "600"), "periodic_budget_conflict")
			calendar := pbCommandInput("0", "500")
			calendar.PeriodKind = "utc_day"
			pbCommandFailure(t, f, call, calendar, "periodic_budget_conflict")
			calendar.OperationID, calendar.ExpectedRevision = uuid.NewString(), "1"
			pbCommandFailure(t, f, call, calendar, "periodic_calendar_immutable")
		})
	}
}

func TestCommanderPGGlobalOperationIdentityAndInputConflicts(t *testing.T) {
	f := newPBTxFixture(t)
	c := pbCommander(t, f)
	call := pbCommandCallFor(c, f, "tenant")
	in := pbCommandInput("0", "500")
	_, err := call(f.ctx, in)
	pbTxMust(t, "commit original global operation", err)
	for _, tc := range []struct {
		name string
		edit func(*PutPeriodicBudget)
	}{
		{"expected_revision", func(v *PutPeriodicBudget) { v.ExpectedRevision = "1" }},
		{"period_kind", func(v *PutPeriodicBudget) { v.PeriodKind = "utc_day" }},
		{"enabled", func(v *PutPeriodicBudget) { v.Enabled = false }},
		{"limit_amount", func(v *PutPeriodicBudget) { v.LimitAmount = "501" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			altered := in
			tc.edit(&altered)
			pbCommandFailure(t, f, call, altered, "policy_operation_conflict")
		})
	}
	otherAdmin, otherTenant := uuid.NewString(), uuid.NewString()
	_, err = f.app.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused','admin')`, otherAdmin, otherAdmin+"@fixture.invalid")
	pbTxMust(t, "create second actual admin identity", err)
	_, err = f.app.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'commander-other-target')`, otherTenant)
	pbTxMust(t, "create second target tenant", err)
	for _, tc := range []struct {
		name string
		call pbCommandCall
	}{
		{"other_actor", func(ctx context.Context, v PutPeriodicBudget) (ConfigureResult, error) {
			return c.ConfigurePlatformTenant(ctx, otherAdmin, f.tenant, v)
		}},
		{"other_target", func(ctx context.Context, v PutPeriodicBudget) (ConfigureResult, error) {
			return c.ConfigurePlatformTenant(ctx, f.user, otherTenant, v)
		}},
		{"other_kind", pbCommandCallFor(c, f, "procurement")},
		{"other_identity_scope", pbCommandCallFor(c, f, "key")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pbCommandFailure(t, f, tc.call, in, "policy_operation_conflict")
		})
	}
}

func TestCommanderPGCurrentPermissionAndTargetBoundaries(t *testing.T) {
	f := newPBTxFixture(t)
	c := pbCommander(t, f)
	for _, scope := range []string{"tenant", "procurement"} {
		for _, actor := range []string{f.operator, f.approver, uuid.NewString()} {
			call := func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
				if scope == "tenant" {
					return c.ConfigurePlatformTenant(ctx, actor, f.tenant, in)
				}
				return c.ConfigureProcurement(ctx, actor, f.account, in)
			}
			pbCommandFailure(t, f, call, pbCommandInput("0", "500"), "forbidden")
		}
	}
	missingTenant, missingAccount := uuid.NewString(), uuid.NewString()
	pbCommandFailure(t, f, func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
		return c.ConfigurePlatformTenant(ctx, f.user, missingTenant, in)
	}, pbCommandInput("0", "500"), "not_found")
	pbCommandFailure(t, f, func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
		return c.ConfigurePlatformTenant(ctx, f.operator, missingTenant, in)
	}, pbCommandInput("0", "500"), "forbidden")
	pbCommandFailure(t, f, func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
		return c.ConfigureProcurement(ctx, f.user, missingAccount, in)
	}, pbCommandInput("0", "500"), "not_found")

	_, err := f.app.Exec(f.ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'member')`, f.tenant, f.operator)
	pbTxMust(t, "add actual tenant member", err)
	memberKey := uuid.NewString()
	_, err = f.app.Exec(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes) VALUES($1,$2,$3,'commander-member','fixture',$1,$4)`, memberKey, f.tenant, f.operator, []string{f.model})
	pbTxMust(t, "create member-owned Key", err)
	memberCall := func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
		return c.ConfigureKey(ctx, f.operator, f.tenant, memberKey, in)
	}
	memberInput := pbCommandInput("0", "500")
	_, err = memberCall(f.ctx, memberInput)
	pbTxMust(t, "member configures own Key", err)
	ownerInput := pbCommandInput("1", "600")
	_, err = c.ConfigureKey(f.ctx, f.user, f.tenant, memberKey, ownerInput)
	pbTxMust(t, "owner configures another member Key", err)
	pbCommandFailure(t, f, func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
		return c.ConfigureKey(ctx, f.operator, f.tenant, f.key, in)
	}, pbCommandInput("0", "500"), "not_found")
	pbCommandFailure(t, f, func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
		return c.ConfigureKey(ctx, f.user, uuid.NewString(), f.key, in)
	}, pbCommandInput("0", "500"), "forbidden")
	pbCommandFailure(t, f, func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
		return c.ConfigureKey(ctx, f.user, f.tenant, uuid.NewString(), in)
	}, pbCommandInput("0", "500"), "not_found")
	otherTenant := uuid.NewString()
	_, err = f.app.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'commander-other-tenant')`, otherTenant)
	pbTxMust(t, "create second tenant isolation boundary", err)
	_, err = f.app.Exec(f.ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, otherTenant, f.user)
	pbTxMust(t, "give owner a valid identity in the other tenant", err)
	pbCommandFailure(t, f, func(ctx context.Context, in PutPeriodicBudget) (ConfigureResult, error) {
		return c.ConfigureKey(ctx, f.user, otherTenant, f.key, in)
	}, pbCommandInput("0", "500"), "not_found")
	_, err = f.app.Exec(f.ctx, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.operator)
	pbTxMust(t, "remove current member write permission", err)
	pbCommandFailure(t, f, memberCall, memberInput, "forbidden")
	_, err = f.app.Exec(f.ctx, `UPDATE memberships SET role='member',status='suspended' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.operator)
	pbTxMust(t, "suspend current membership", err)
	pbCommandFailure(t, f, memberCall, memberInput, "forbidden")
	_, err = f.app.Exec(f.ctx, `UPDATE tenants SET status='suspended' WHERE id=$1`, f.tenant)
	pbTxMust(t, "suspend target tenant", err)
	platformInput := pbCommandInput("0", "500")
	_, err = c.ConfigurePlatformTenant(f.ctx, f.user, f.tenant, platformInput)
	pbTxMust(t, "admin manages suspended tenant budget", err)
	pbCommandFailure(t, f, pbCommandCallFor(c, f, "key"), pbCommandInput("0", "500"), "forbidden")
	_, err = f.app.Exec(f.ctx, `UPDATE procurement_accounts SET state='paused' WHERE id=$1`, f.account)
	pbTxMust(t, "pause target procurement account", err)
	_, err = c.ConfigureProcurement(f.ctx, f.user, f.account, pbCommandInput("0", "500"))
	pbTxMust(t, "admin manages paused procurement budget", err)
	var targetState string
	pbTxMust(t, "read unchanged procurement lifecycle", f.app.QueryRow(f.ctx, `SELECT state FROM procurement_accounts WHERE id=$1`, f.account).Scan(&targetState))
	if targetState != "paused" {
		t.Fatal("budget configuration altered procurement lifecycle")
	}
	_, err = f.app.Exec(f.ctx, `UPDATE users SET platform_role='none' WHERE id=$1`, f.user)
	pbTxMust(t, "revoke current platform admin role", err)
	pbCommandFailure(t, f, pbCommandCallFor(c, f, "tenant"), platformInput, "forbidden")
	pbCommandFailure(t, f, pbCommandCallFor(c, f, "tenant"), pbCommandInput("1", "600"), "forbidden")
}

func TestCommanderPGReplayRevalidatesActorAndRevokedKey(t *testing.T) {
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run(scope, func(t *testing.T) {
			f := newPBTxFixture(t)
			call := pbCommandCallFor(pbCommander(t, f), f, scope)
			in := pbCommandInput("0", "500")
			_, err := call(f.ctx, in)
			pbTxMust(t, "create operation before actor suspension", err)
			_, err = f.app.Exec(f.ctx, `UPDATE users SET status='suspended' WHERE id=$1`, f.user)
			pbTxMust(t, "suspend original actor", err)
			pbCommandFailure(t, f, call, in, "forbidden")
			if scope == "key" {
				_, err = f.app.Exec(f.ctx, `UPDATE users SET status='active' WHERE id=$1`, f.user)
				pbTxMust(t, "restore active actor", err)
				_, err = f.app.Exec(f.ctx, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, f.key)
				pbTxMust(t, "revoke target Key", err)
				before := pbCommandSnapshot(t, f)
				for _, revokedInput := range []PutPeriodicBudget{in, pbCommandInput("1", "600")} {
					got, revokedErr := call(f.ctx, revokedInput)
					code := ErrorCode(revokedErr)
					if revokedErr == nil || code != "forbidden" && code != "not_found" || !reflect.DeepEqual(got, ConfigureResult{}) {
						t.Fatal("revoked Key accepted a new configuration or original-operation replay")
					}
				}
				if before != pbCommandSnapshot(t, f) {
					t.Fatal("revoked Key writes changed facts")
				}
			}
		})
	}
}

func TestCommanderPGOriginalReceiptSurvivesTrafficAndClockRegression(t *testing.T) {
	pbFutureClockTraffic(t)
	f := newPBTxFixture(t)
	c := pbCommander(t, f)
	in := pbCommandInput("0", "500")
	first, err := c.ConfigureKey(f.ctx, f.user, f.tenant, f.key, in)
	pbTxMust(t, "configure Key before new traffic", err)
	f.setClock(t, time.Date(2026, 12, 10, 12, 0, 0, 123456000, time.UTC))
	f.reserveCustomer(t)
	f.setClock(t, time.Date(2026, 11, 10, 12, 0, 0, 123456000, time.UTC))
	before := pbCommandSnapshot(t, f)
	again, err := c.ConfigureKey(f.ctx, f.user, f.tenant, f.key, in)
	pbTxMust(t, "replay original receipt despite current clock regression", err)
	if again.Created || !reflect.DeepEqual(again.Receipt, first.Receipt) || before != pbCommandSnapshot(t, f) {
		t.Fatal("original command receipt changed after source traffic or clock regression")
	}
	f.verifyFinance(t)
}

func TestCommanderPGAuthorizationRecheckedAfterLockWait(t *testing.T) {
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run(scope, func(t *testing.T) {
			f := newPBTxFixture(t)
			call := pbCommandCallFor(pbCommander(t, f), f, scope)
			// Publish a revocation only after the command demonstrably waits on
			// the held identity row. A pre-wait RC statement still sees active.
			hold := f.begin(t)
			if scope == "key" {
				f.exec(t, hold, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.user)
			} else {
				f.exec(t, hold, `UPDATE users SET platform_role='none' WHERE id=$1`, f.user)
			}
			type outcome struct {
				result ConfigureResult
				err    error
			}
			done := make(chan outcome, 1)
			input := pbCommandInput("0", "500")
			go func() {
				result, err := call(f.ctx, input)
				done <- outcome{result, err}
			}()
			waitCtx, stop := context.WithTimeout(f.ctx, 2*time.Second)
			defer stop()
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for {
				var waiting bool
				pbTxMust(t, "observe actual APP identity lock wait", f.app.QueryRow(waitCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock')`, f.appRole).Scan(&waiting))
				if waiting {
					break
				}
				select {
				case <-tick.C:
				case out := <-done:
					_ = hold.Rollback(f.ctx)
					t.Fatalf("configuration returned before waiting for the locked actor (safe code %q)", ErrorCode(out.err))
				case <-waitCtx.Done():
					_ = hold.Rollback(f.ctx)
					t.Fatal("command never reached the actual identity lock")
				}
			}
			pbTxMust(t, "commit concurrent identity revocation", hold.Commit(f.ctx))
			before := pbCommandSnapshot(t, f)
			out := <-done
			if ErrorCode(out.err) != "forbidden" || !reflect.DeepEqual(out.result, ConfigureResult{}) || before != pbCommandSnapshot(t, f) {
				t.Fatal("command did not recheck the now-revoked identity after its lock wait")
			}
		})
	}
}

func TestCommanderPGConcurrentSameOperationAndSameCAS(t *testing.T) {
	for _, sameOperation := range []bool{true, false} {
		name := "different_operations_same_CAS"
		if sameOperation {
			name = "same_operation"
		}
		t.Run(name, func(t *testing.T) {
			f := newPBTxFixture(t)
			call := pbCommandCallFor(pbCommander(t, f), f, "tenant")
			inputs := []PutPeriodicBudget{pbCommandInput("0", "500"), pbCommandInput("0", "600")}
			if sameOperation {
				inputs[1] = inputs[0]
			}
			type outcome struct {
				result ConfigureResult
				err    error
			}
			start := make(chan struct{})
			done := make(chan outcome, 2)
			for _, input := range inputs {
				go func(input PutPeriodicBudget) {
					<-start
					result, err := call(f.ctx, input)
					done <- outcome{result, err}
				}(input)
			}
			close(start)
			results := []outcome{<-done, <-done}
			successes, created, conflicts := 0, 0, 0
			var receipts []CommandReceipt
			for _, out := range results {
				if out.err != nil {
					if ErrorCode(out.err) != "periodic_budget_conflict" || !reflect.DeepEqual(out.result, ConfigureResult{}) {
						t.Fatalf("concurrent command returned unexpected safe error %q", ErrorCode(out.err))
					}
					conflicts++
					continue
				}
				pbTxMust(t, "validate concurrent committed receipt", out.result.Receipt.Validate())
				successes++
				if out.result.Created {
					created++
				}
				receipts = append(receipts, out.result.Receipt)
			}
			if created != 1 || sameOperation && (successes != 2 || conflicts != 0 || !reflect.DeepEqual(receipts[0], receipts[1])) ||
				!sameOperation && (successes != 1 || conflicts != 1) {
				t.Fatal("operation serialization or target CAS allowed multiple configurations/lost exact replay")
			}
			var exact bool
			pbTxMust(t, "verify unique concurrent command facts", f.app.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=1 AND bool_and(revision=1) FROM periodic_budget_controls)
 AND (SELECT count(*)=1 FROM periodic_budget_operations)
 AND (SELECT count(*)=1 FROM periodic_budget_configuration_versions)
 AND (SELECT count(*)=1 FROM audit_events WHERE action='periodic_budget.configured')`).Scan(&exact))
			if !exact {
				t.Fatal("concurrent configuration persisted more than one operation/version/audit")
			}
		})
	}
}
