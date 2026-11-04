package periodicbudget_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func pbBillCustomerInput(f *pbTxFixture) billing.ReserveInput {
	return billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, IdempotencyKey: uuid.NewString(), RequestHMAC: "abababababababababababababababababababababababababababababababab", ProfileVersion: f.profileVersion, InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(300 * time.Second)}
}

func pbBillCustomerNow(t *testing.T, f *pbTxFixture) time.Time {
	t.Helper()
	var now time.Time
	pbTxMust(t, "read actual database clock for isolated billing time fixture", f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now))
	return now.UTC().Truncate(time.Microsecond)
}

func pbBillCustomerConfigure(t *testing.T, f *pbTxFixture, scope, expected, limit string, enabled bool) ConfigureResult {
	t.Helper()
	in := pbCommandInput(expected, limit)
	in.PeriodKind, in.Enabled = "utc_day", enabled
	out, err := pbCommandCallFor(pbCommander(t, f), f, scope)(f.ctx, in)
	pbTxMust(t, "configure real migrated billing control", err)
	return out
}

func pbBillCustomerActive(t *testing.T, f *pbTxFixture, tenantLimit, keyLimit string, enabled bool) time.Time {
	t.Helper()
	now := pbBillCustomerNow(t, f)
	f.setClock(t, now.AddDate(0, 0, -2))
	pbBillCustomerConfigure(t, f, "tenant", "0", tenantLimit, enabled)
	pbBillCustomerConfigure(t, f, "key", "0", keyLimit, enabled)
	f.setClock(t, now.AddDate(0, 0, -1))
	return now
}

func pbBillCustomerReserve(t *testing.T, f *pbTxFixture, store *billing.Store) billing.Reservation {
	t.Helper()
	r, err := store.Reserve(f.ctx, pbBillCustomerInput(f))
	pbTxMust(t, "reserve through real billing API and restricted APP LOGIN", err)
	if r.Amount != 120 {
		t.Fatal("real retail price did not produce the expected complete R=120")
	}
	return r
}

func pbBillCustomerFinal(t *testing.T, f *pbTxFixture, store *billing.Store, r billing.Reservation) billing.Settlement {
	t.Helper()
	inputRate, outputRate := int64(2000000), int64(2000000)
	attempt, err := store.RecordAttempt(f.ctx, billing.AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, Sequence: 1, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", CostQuote: &routing.CostQuote{VersionID: f.cost, InputRate: &inputRate, OutputRate: &outputRate, Basis: "contract"}})
	pbTxMust(t, "record the original supplier attempt before customer usage", err)
	_, err = store.RecordFinalization(f.ctx, billing.FinalizationInput{ID: uuid.NewString(), TenantID: f.tenant, RequestID: r.RequestID, AttemptID: attempt.ID, Source: "periodic-customer-fixture", SourceEventID: uuid.NewString(), Usage: billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true})
	pbTxMust(t, "record actual trusted customer finalization", err)
	s, err := store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	pbTxMust(t, "settle through original financial and periodic chain", err)
	if s.ChargedAmount != 15 || s.Status != "settled" {
		t.Fatal("trusted final usage did not settle the exact retail amount")
	}
	return s
}

func pbBillCustomerWallet(t *testing.T, f *pbTxFixture, store *billing.Store, available, held billing.Money) {
	t.Helper()
	wallet, err := store.Wallet(f.ctx, billing.Query{TenantID: f.tenant})
	pbTxMust(t, "read actual single customer wallet", err)
	if wallet.Available != available || wallet.Held != held {
		t.Fatal("customer wallet was duplicated or lost reservation/settlement money")
	}
	f.verifyFinance(t)
}

func pbBillCustomerSnapshot(t *testing.T, f *pbTxFixture) string {
	t.Helper()
	var extra string
	pbTxMust(t, "read idempotency/recovery/outbox/audit facts for complete rollback proof", f.app.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'idempotency',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM request_idempotency x),'[]'::jsonb),
 'recovery',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY request_id) FROM recovery_jobs x),'[]'::jsonb),
 'outbox',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY event_id) FROM projection_outbox x),'[]'::jsonb),
 'audits',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM audit_events x),'[]'::jsonb))::text`).Scan(&extra))
	return f.snapshot(t) + extra
}

func pbBillCustomerCode(t *testing.T, err error, want string) {
	t.Helper()
	var safe *billing.Error
	code := ""
	if errors.As(err, &safe) {
		code = safe.Code
	}
	if code != want {
		t.Fatalf("billing rejected with safe code %q, want %q", code, want)
	}
}

func pbBillCustomerControls(t *testing.T, f *pbTxFixture, held, spent string, enabled bool) {
	t.Helper()
	var exact bool
	pbTxMust(t, "verify both customer scope totals", f.app.QueryRow(f.ctx, `SELECT count(*)=2 AND bool_and(held_amount=$2::numeric AND spent_amount=$3::numeric AND enabled=$4) FROM periodic_budget_controls WHERE tenant_id=$1 AND scope IN ('tenant','key')`, f.tenant, held, spent, enabled).Scan(&exact))
	if !exact {
		t.Fatal("tenant and Key periodic scopes lost exact independently tracked totals")
	}
}

func TestBillingPeriodicRealClockLatestMigrationUnconfigured(t *testing.T) {
	f := newPBBillingFixture(t)
	store := billing.New(f.app)
	r := pbBillCustomerReserve(t, f, store)
	pbBillCustomerWallet(t, f, store, 9880, 120)
	var zero bool
	pbTxMust(t, "confirm migration creates no default controls or synthetic periodic facts", f.app.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT 1 FROM periodic_budget_controls) AND NOT EXISTS(SELECT 1 FROM periodic_budget_allocations) AND NOT EXISTS(SELECT 1 FROM periodic_budget_events) AND NOT EXISTS(SELECT 1 FROM periodic_budget_windows)`).Scan(&zero))
	if !zero {
		t.Fatal("unconfigured original billing created periodic controls or empty windows")
	}
	_, err := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	pbTxMust(t, "release unconfigured request through actual billing API", err)
	pbBillCustomerWallet(t, f, store, 10000, 0)
}

func TestBillingPeriodicCustomerDualScopeSingleWalletAndRefund(t *testing.T) {
	f := newPBBillingFixture(t)
	pbBillCustomerActive(t, f, "120", "120", true)
	store := billing.New(f.app)
	r := pbBillCustomerReserve(t, f, store)
	pbBillCustomerWallet(t, f, store, 9880, 120)
	pbBillCustomerControls(t, f, "120", "0", true)
	var exact bool
	pbTxMust(t, "verify two exact R allocations share only one reserve journal", f.app.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=2 AND bool_and(origin='new' AND upper_amount=120 AND held_amount=120 AND spent_amount=0 AND window_id IS NOT NULL AND revision=1) FROM periodic_budget_allocations WHERE subject_id=$1)
 AND (SELECT count(*)=2 AND bool_and(source_kind='reservation' AND source_id=$2 AND held_delta=120) FROM periodic_budget_events WHERE allocation_id IN (SELECT id FROM periodic_budget_allocations WHERE subject_id=$1))
 AND (SELECT count(*)=1 FROM journals WHERE reference=$1 AND kind='reserve')`, r.RequestID, r.ID).Scan(&exact))
	if !exact {
		t.Fatal("dual periodic scope charged a second wallet or failed to reserve full R per scope")
	}
	before := pbBillCustomerSnapshot(t, f)
	failed, err := store.Reserve(f.ctx, pbBillCustomerInput(f))
	pbBillCustomerCode(t, err, "periodic_budget_exceeded")
	if failed != (billing.Reservation{}) || before != pbBillCustomerSnapshot(t, f) {
		t.Fatal("cap rejection retained idempotency, source, wallet, journal, recovery, audit or periodic facts")
	}
	settled := pbBillCustomerFinal(t, f, store, r)
	pbBillCustomerWallet(t, f, store, 9985, 0)
	pbBillCustomerControls(t, f, "0", "15", true)
	before = pbBillCustomerSnapshot(t, f)
	repeat, err := store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	pbTxMust(t, "read original settlement on repeat", err)
	if !reflect.DeepEqual(repeat, settled) || before != pbBillCustomerSnapshot(t, f) {
		t.Fatal("repeat settlement changed the unique source or periodic effect")
	}
	op := uuid.NewString()
	refund, err := store.CreateRefund(f.ctx, billing.RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settled.ID, BusinessRef: "periodic-refund:" + op, Reason: "fixture approved customer refund", Amount: 5, IdempotencyKey: op, RequestHMAC: op})
	pbTxMust(t, "create complete actual customer refund", err)
	_, err = store.ApproveRefund(f.ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.approver})
	pbTxMust(t, "independently approve complete actual customer refund", err)
	pbBillCustomerWallet(t, f, store, 9990, 0)
	pbBillCustomerControls(t, f, "0", "15", true)
	pbTxMust(t, "refund does not rewind Key or periodic gross spent", f.app.QueryRow(f.ctx, `SELECT k.spent=15 AND k.held=0
 AND (SELECT bool_and(spent_amount=15 AND held_amount=0 AND revision=2) FROM periodic_budget_allocations WHERE subject_id=$1)
 AND (SELECT count(*)=4 FROM periodic_budget_events WHERE allocation_id IN (SELECT id FROM periodic_budget_allocations WHERE subject_id=$1)) FROM api_keys k WHERE k.id=$2`, r.RequestID, f.key).Scan(&exact))
	if !exact {
		t.Fatal("refund reduced gross spent or manufactured another periodic event")
	}
}

func TestBillingPeriodicCustomerEachScopeRejectsFullRAtomically(t *testing.T) {
	for _, scope := range []string{"tenant", "key"} {
		t.Run(scope, func(t *testing.T) {
			f := newPBBillingFixture(t)
			tenantLimit, keyLimit := "1000", "1000"
			if scope == "tenant" {
				tenantLimit = "119"
			} else {
				keyLimit = "119"
			}
			pbBillCustomerActive(t, f, tenantLimit, keyLimit, true)
			store := billing.New(f.app)
			before := pbBillCustomerSnapshot(t, f)
			out, err := store.Reserve(f.ctx, pbBillCustomerInput(f))
			pbBillCustomerCode(t, err, "periodic_budget_exceeded")
			if out != (billing.Reservation{}) || before != pbBillCustomerSnapshot(t, f) {
				t.Fatal("a single scope could not reject the complete R without retaining any money/source facts")
			}
			pbBillCustomerWallet(t, f, store, 10000, 0)
		})
	}
}

func TestBillingPeriodicCustomerDisabledStillTracksAndReenableHonorsSpent(t *testing.T) {
	f := newPBBillingFixture(t)
	pbBillCustomerActive(t, f, "0", "0", false)
	store := billing.New(f.app)
	r := pbBillCustomerReserve(t, f, store)
	pbBillCustomerControls(t, f, "120", "0", false)
	pbBillCustomerFinal(t, f, store, r)
	pbBillCustomerControls(t, f, "0", "15", false)
	for _, scope := range []string{"tenant", "key"} {
		pbBillCustomerConfigure(t, f, scope, "1", "15", true)
	}
	before := pbBillCustomerSnapshot(t, f)
	out, err := store.Reserve(f.ctx, pbBillCustomerInput(f))
	pbBillCustomerCode(t, err, "periodic_budget_exceeded")
	if out != (billing.Reservation{}) || before != pbBillCustomerSnapshot(t, f) {
		t.Fatal("reenabling a control reset usage recorded while disabled")
	}
	pbBillCustomerWallet(t, f, store, 9985, 0)
}

func TestBillingPeriodicCustomerCrossDayCarryAndOriginalWindowFinish(t *testing.T) {
	f := newPBBillingFixture(t)
	now := pbBillCustomerActive(t, f, "180", "180", true)
	store := billing.New(f.app)
	old := pbBillCustomerReserve(t, f, store)
	f.setClock(t, now)
	reader, err := NewReader(f.app, []byte("0123456789abcdef0123456789abcdef"))
	pbTxMust(t, "construct actual migrated Reader", err)
	view, err := reader.WalletBudget(f.ctx, f.user, f.tenant)
	pbTxMust(t, "read cross-day carry without creating a window", err)
	if view.CarryHeld != "120" || view.CurrentWindow != nil {
		t.Fatal("yesterday's unresolved reservation did not remain carry-held today")
	}
	before := pbBillCustomerSnapshot(t, f)
	out, err := store.Reserve(f.ctx, pbBillCustomerInput(f))
	pbBillCustomerCode(t, err, "periodic_budget_exceeded")
	if out != (billing.Reservation{}) || before != pbBillCustomerSnapshot(t, f) {
		t.Fatal("new-day admission ignored carry or retained its rejected current window")
	}
	for _, scope := range []string{"tenant", "key"} {
		pbBillCustomerConfigure(t, f, scope, "1", "0", true)
	}
	pbBillCustomerFinal(t, f, store, old)
	pbBillCustomerControls(t, f, "0", "15", true)
	var exact bool
	yesterday := now.AddDate(0, 0, -1)
	start := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)
	pbTxMust(t, "verify favorable settlement stays in both original windows under zero current cap", f.app.QueryRow(f.ctx, `SELECT count(*)=2 AND bool_and(a.window_id=w.id AND w.start_at=$2 AND w.end_at=$3 AND w.held_amount=0 AND w.spent_amount=15 AND a.held_amount=0 AND a.spent_amount=15 AND a.revision=2) FROM periodic_budget_allocations a JOIN periodic_budget_windows w ON w.id=a.window_id WHERE a.subject_id=$1`, old.RequestID, start, start.AddDate(0, 0, 1)).Scan(&exact))
	if !exact {
		t.Fatal("cross-day settlement changed the original attribution window or was blocked by current cap")
	}
	for _, scope := range []string{"tenant", "key"} {
		pbBillCustomerConfigure(t, f, scope, "2", "120", true)
	}
	current := pbBillCustomerReserve(t, f, store)
	view, err = reader.WalletBudget(f.ctx, f.user, f.tenant)
	pbTxMust(t, "observe today's independent window after old carry was released", err)
	if view.CarryHeld != "0" || view.CurrentWindow == nil || view.CurrentWindow.HeldAmount != "120" || view.CurrentWindow.SpentAmount != "0" {
		t.Fatal("new-day window inherited old spent or failed to clear old carry")
	}
	_, err = store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: current.RequestID, Reason: "not_sent"})
	pbTxMust(t, "complete today's unused customer reservation", err)
	pbBillCustomerWallet(t, f, store, 9985, 0)
}

func pbBillCustomerSinglePool(t *testing.T, f *pbTxFixture) (*pgxpool.Pool, int32) {
	t.Helper()
	cfg := f.app.Config().Copy()
	cfg.MaxConns, cfg.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	pbTxMust(t, "open actual isolated APP connection for observed concurrency", err)
	t.Cleanup(pool.Close)
	var pid int32
	pbTxMust(t, "observe exact billing concurrency backend", pool.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	return pool, pid
}

func pbBillCustomerWait(t *testing.T, f *pbTxFixture, waiter, blocker int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		pbTxMust(t, "observe exact parent or wallet concurrency wait", f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND state='active' AND wait_event_type='Lock' AND $2::integer=ANY(pg_blocking_pids(pid)))`, waiter, blocker).Scan(&waiting))
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("billing/configuration did not reach the required real lock wait")
		case <-tick.C:
		}
	}
}

func TestBillingPeriodicCustomerFirstConfigurationSerializesActualTraffic(t *testing.T) {
	for _, first := range []string{"traffic", "configuration"} {
		t.Run(first, func(t *testing.T) {
			f := newPBBillingFixture(t)
			now := pbBillCustomerNow(t, f)
			f.setClock(t, now.AddDate(0, 0, -2))
			trafficPool, trafficPID := pbBillCustomerSinglePool(t, f)
			commandPool, commandPID := pbBillCustomerSinglePool(t, f)
			store := billing.New(trafficPool)
			commander, err := NewCommander(commandPool)
			pbTxMust(t, "construct concurrently observed actual APP Commander", err)
			wallet := f.begin(t)
			var walletPID int32
			pbTxMust(t, "observe precise wallet blocker", wallet.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&walletPID))
			f.exec(t, wallet, `SELECT id FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE' FOR UPDATE`, f.tenant)
			type reserveResult struct {
				reservation billing.Reservation
				err         error
			}
			type configureResult struct {
				result ConfigureResult
				err    error
			}
			reserved, configured := make(chan reserveResult, 1), make(chan configureResult, 1)
			input := pbBillCustomerInput(f)
			startTraffic := func() { go func() { r, e := store.Reserve(f.ctx, input); reserved <- reserveResult{r, e} }() }
			startConfig := func() {
				go func() {
					in := pbCommandInput("0", "120")
					in.PeriodKind = "utc_day"
					r, e := commander.ConfigurePlatformTenant(f.ctx, f.user, f.tenant, in)
					configured <- configureResult{r, e}
				}()
			}
			if first == "traffic" {
				startTraffic()
				pbBillCustomerWait(t, f, trafficPID, walletPID)
				startConfig()
				pbBillCustomerWait(t, f, commandPID, trafficPID)
			} else {
				actor := f.begin(t)
				var actorPID int32
				pbTxMust(t, "observe actor blocker before first configuration", actor.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&actorPID))
				f.exec(t, actor, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.user)
				startConfig()
				pbBillCustomerWait(t, f, commandPID, actorPID)
				startTraffic()
				pbBillCustomerWait(t, f, trafficPID, commandPID)
				pbTxMust(t, "release actor so first configuration commits ahead of traffic", actor.Rollback(f.ctx))
				select {
				case got := <-configured:
					pbTxMust(t, "commit first configuration before actual billing source", got.err)
					if !got.result.Created {
						t.Fatal("configuration-first path did not create its control")
					}
				case <-f.ctx.Done():
					t.Fatal("first configuration did not finish")
				}
				pbBillCustomerWait(t, f, trafficPID, walletPID)
			}
			// In the configuration-first order the source clock must be sampled
			// only after this final wallet wait, making the control active when R
			// is admitted. No request or money source is edited by the owner.
			f.setClock(t, now.AddDate(0, 0, -1))
			pbTxMust(t, "release final wallet wait to admit the actual source", wallet.Rollback(f.ctx))
			var r billing.Reservation
			select {
			case got := <-reserved:
				pbTxMust(t, "complete concurrently ordered actual reservation", got.err)
				r = got.reservation
			case <-f.ctx.Done():
				t.Fatal("actual traffic did not finish")
			}
			if first == "traffic" {
				select {
				case got := <-configured:
					pbTxMust(t, "import the just-committed actual source during first configuration", got.err)
					if !got.result.Created || got.result.Receipt.Result.CarryHeld != "120" {
						t.Fatal("traffic-first path lost the committed legacy reservation")
					}
				case <-f.ctx.Done():
					t.Fatal("configuration after traffic did not finish")
				}
			}
			wantOrigin := "new"
			if first == "traffic" {
				wantOrigin = "legacy_import"
			}
			var exact bool
			pbTxMust(t, "verify exactly one original or legacy source from the winning root order", f.app.QueryRow(f.ctx, `SELECT count(*)=1 AND bool_and(a.origin=$2 AND a.held_amount=120 AND a.spent_amount=0 AND a.upper_amount=120 AND a.revision=1 AND (a.window_id IS NULL)=($2='legacy_import') AND (SELECT count(*)=1 FROM periodic_budget_events WHERE allocation_id=a.id))
 AND (SELECT pb_admitted_at=$3 FROM request_records WHERE id=$1) FROM periodic_budget_allocations a WHERE a.subject_id=$1`, r.RequestID, wantOrigin, now.AddDate(0, 0, -1)).Scan(&exact))
			if !exact {
				t.Fatal("first configuration and real billing traffic crossed without one complete original allocation")
			}
			pbBillCustomerWallet(t, f, store, 9880, 120)
			_, err = store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
			pbTxMust(t, "close the actual source after concurrent first configuration", err)
			pbBillCustomerWallet(t, f, store, 10000, 0)
		})
	}
}
