package periodicbudget_test

import (
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

// All source mutations use the actual APP LOGIN and retain 014's full
// account = immutable events = reservation conservation at transaction commit.
// pbTxPrepareSupplier writes one prepared attempt whose U is the original
// supplier bounds (100/20) times a known quote: the fixture quote gives 240 and
// an older zero quote gives 0. The database recomputes U independently.
func pbTxPrepareSupplier(t *testing.T, f *pbTxFixture, tx pgx.Tx, request string, upper int64) (string, SupplierLocks, string) {
	t.Helper()
	attempt := pbTxSupplierAttempt(t, f, tx, request, upper)
	f.exec(t, tx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account)
	locks, err := LockSupplierTx(f.ctx, tx, f.account)
	pbTxMust(t, "lock exact supplier account control", err)
	f.exec(t, tx, `INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,upper_amount,held_amount,spent_amount,state) VALUES($1,$2,$3,$4,$4,0,'prepared')`, attempt, f.account, f.binding, upper)
	source := uuid.NewString()
	f.exec(t, tx, `INSERT INTO procurement_budget_events(id,event_key,account_id,attempt_id,held_delta,spent_delta,type) VALUES($1,$2,$3,$4,$5,0,'reserved')`, source, "reserve:"+attempt, f.account, attempt, upper)
	f.exec(t, tx, `UPDATE procurement_accounts SET held_amount=held_amount+$2 WHERE id=$1`, f.account, upper)
	return attempt, locks, source
}

func pbTxSupplierAttempt(t *testing.T, f *pbTxFixture, tx pgx.Tx, request string, upper int64) string {
	t.Helper()
	cost := f.cost
	switch upper {
	case 240:
	case 0:
		var zero int64
		cost = meterquote.PublishCost(t, f.ctx, tx, meterquote.CostPrice{
			ChannelID: f.channel, UpstreamModel: "upstream", ModelID: f.model, Actor: f.user,
			Basis: "contract", InputRate: &zero, OutputRate: &zero, EffectiveAt: f.catalogEffectiveAt(t, tx, "30 days"),
		})
	default:
		t.Fatal("unsupported fixture supplier upper amount", upper)
	}
	attempt := uuid.NewString()
	f.exec(t, tx, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) VALUES($1,$2,$3,1,$4,$5,$6,'upstream')`, attempt, f.tenant, request, f.channel, f.version, f.credential)
	f.exec(t, tx, `SELECT meter_insert_attempt_snapshot($1,$2,$3,$4,'contract')`, f.tenant, attempt, request, cost)
	return attempt
}

func pbTxChangeSupplier(t *testing.T, f *pbTxFixture, tx pgx.Tx, attempt string, held, spent int64, state string) string {
	t.Helper()
	var oldHeld, oldSpent int64
	pbTxMust(t, "read prior actual supplier projection", tx.QueryRow(f.ctx, `SELECT held_amount::bigint,spent_amount::bigint FROM procurement_reservations WHERE attempt_id=$1 FOR UPDATE`, attempt).Scan(&oldHeld, &oldSpent))
	f.exec(t, tx, `UPDATE procurement_reservations SET held_amount=$2,spent_amount=$3,state=$4,anomaly=($4='disputed') WHERE attempt_id=$1`, attempt, held, spent, state)
	source := uuid.NewString()
	f.exec(t, tx, `INSERT INTO procurement_budget_events(id,event_key,account_id,attempt_id,held_delta,spent_delta,type) VALUES($1,$2,$3,$4,$5,$6,'test_absolute_cost')`, source, "change:"+source, f.account, attempt, held-oldHeld, spent-oldSpent)
	f.exec(t, tx, `UPDATE procurement_accounts SET held_amount=held_amount+$2,spent_amount=spent_amount+$3 WHERE id=$1`, f.account, held-oldHeld, spent-oldSpent)
	return source
}
func pbTxSupplierLocks(t *testing.T, f *pbTxFixture, tx pgx.Tx, request, attempt string) SupplierLocks {
	t.Helper()
	pbTxCustomerLocks(t, f, tx, request)
	f.exec(t, tx, `SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE`, attempt)
	f.exec(t, tx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account)
	locks, err := LockSupplierTx(f.ctx, tx, f.account)
	pbTxMust(t, "lock prior supplier source", err)
	return locks
}
func pbTxSupplierSnapshot(t *testing.T, f *pbTxFixture) string {
	t.Helper()
	var supplier string
	pbTxMust(t, "read supplier immutable/projection snapshot", f.app.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'attempts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM upstream_attempts x),'[]'::jsonb),
 'accounts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM procurement_accounts x),'[]'::jsonb),
 'reservations',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY attempt_id) FROM procurement_reservations x),'[]'::jsonb),
 'events',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM procurement_budget_events x),'[]'::jsonb))::text`).Scan(&supplier))
	return f.snapshot(t) + supplier
}
func pbTxAssertSupplierConserved(t *testing.T, f *pbTxFixture) {
	t.Helper()
	var ok bool
	pbTxMust(t, "read full supplier account/source/event conservation", f.app.QueryRow(f.ctx, `SELECT (a.held_amount,a.spent_amount)=(SELECT coalesce(sum(held_delta),0),coalesce(sum(spent_delta),0) FROM procurement_budget_events WHERE account_id=a.id)
 AND (a.held_amount,a.spent_amount)=(SELECT coalesce(sum(held_amount),0),coalesce(sum(spent_amount),0) FROM procurement_reservations WHERE account_id=a.id) FROM procurement_accounts a WHERE id=$1`, f.account).Scan(&ok))
	if !ok {
		t.Fatal("supplier immutable events, sources and account do not conserve")
	}
	f.verifyFinance(t)
}

func TestTxSupplierFullUpperOriginalWindowAndIdempotency(t *testing.T) {
	pbHandwrittenSource(t)
	f := newPBTxFixture(t)
	control := f.configure(t, "procurement", true, 240)
	f.setClock(t, time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC))
	tx := f.begin(t)
	customer := pbTxCustomerLocks(t, f, tx, "")
	request, _ := f.newCustomer(t, tx, customer)
	attempt, locks, source := pbTxPrepareSupplier(t, f, tx, request, 240)
	admission, err := AdmitSupplierTx(f.ctx, tx, locks, attempt, Amount{"240"})
	pbTxMust(t, "admit full U", err)
	if len(admission.WindowIDs) != 1 || admission.At.Format(utc6Layout) != "2026-12-10T12:00:00.000000Z" {
		t.Fatal("supplier admission lost source At/window")
	}
	pbTxMust(t, "apply original reserved supplier event", ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", source}))
	pbTxMust(t, "repeat original supplier event", ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", source}))
	pbTxMust(t, "commit full supplier reservation", tx.Commit(f.ctx))
	f.configure(t, "procurement", true, 0)
	f.setClock(t, time.Date(2027, 1, 10, 12, 0, 0, 0, time.UTC))
	finish := f.begin(t)
	finishLocks := pbTxSupplierLocks(t, f, finish, request, attempt)
	settle := pbTxChangeSupplier(t, f, finish, attempt, 0, 300, "settled")
	pbTxMust(t, "supplier trustworthy cost may exceed U", ApplySupplierEffectTx(f.ctx, finish, finishLocks, attempt, Effect{"procurement_event", settle}))
	pbTxMust(t, "repeat exact supplier cost", ApplySupplierEffectTx(f.ctx, finish, finishLocks, attempt, Effect{"procurement_event", settle}))
	pbTxMust(t, "commit original-period supplier settlement", finish.Commit(f.ctx))
	var exact bool
	pbTxMust(t, "verify supplier original-period accounting", f.app.QueryRow(f.ctx, `SELECT a.origin='new' AND a.upper_amount=240 AND a.held_amount=0 AND a.spent_amount=300 AND a.revision=2 AND w.start_at='2026-12-01T00:00:00Z'::timestamptz AND w.spent_amount=300 AND w.held_amount=0 AND c.held_amount=0 AND c.spent_amount=300 AND (SELECT count(*) FROM periodic_budget_events WHERE control_id=c.id)=2
 FROM periodic_budget_allocations a JOIN periodic_budget_windows w ON w.id=a.window_id JOIN periodic_budget_controls c ON c.id=a.control_id WHERE a.subject_id=$1 AND c.id=$2`, attempt, control).Scan(&exact))
	if !exact {
		t.Fatal("supplier settlement changed original window, duplicated effects, or truncated over-U cost")
	}
	before := pbTxSupplierSnapshot(t, f)
	replay := f.begin(t)
	replayLocks := pbTxSupplierLocks(t, f, replay, request, attempt)
	pbTxMust(t, "replay committed original 019 effect", ApplySupplierEffectTx(f.ctx, replay, replayLocks, attempt, Effect{"procurement_event", source}))
	pbTxMust(t, "replay committed terminal 019 effect", ApplySupplierEffectTx(f.ctx, replay, replayLocks, attempt, Effect{"procurement_event", settle}))
	pbTxMust(t, "commit only original supplier facts", replay.Commit(f.ctx))
	if before != pbTxSupplierSnapshot(t, f) {
		t.Fatal("supplier replay mutated original facts")
	}
	pbTxAssertSupplierConserved(t, f)
}

func TestTxSupplierCapAndSourceProofFailuresRollback(t *testing.T) {
	pbHandwrittenSource(t)
	f := newPBTxFixture(t)
	f.configure(t, "procurement", true, 239)
	f.setClock(t, time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC))
	before := pbTxSupplierSnapshot(t, f)
	for _, mode := range []string{"admit_cap", "apply_cap", "amount", "zero_token", "none_token", "effect_kind", "other_effect", "other_account"} {
		t.Run(mode, func(t *testing.T) {
			tx := f.begin(t)
			customer := pbTxCustomerLocks(t, f, tx, "")
			request, _ := f.newCustomer(t, tx, customer)
			attempt, locks, source := pbTxPrepareSupplier(t, f, tx, request, 240)
			var err error
			want := "periodic_budget_invariant"
			switch mode {
			case "admit_cap":
				_, err = AdmitSupplierTx(f.ctx, tx, locks, attempt, Amount{"240"})
				want = "procurement_periodic_budget_exceeded"
			case "apply_cap":
				err = ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", source})
				want = "procurement_periodic_budget_exceeded"
			case "amount":
				_, err = AdmitSupplierTx(f.ctx, tx, locks, attempt, Amount{"239"})
			case "zero_token":
				_, err = AdmitSupplierTx(f.ctx, tx, SupplierLocks{}, attempt, Amount{"240"})
			case "none_token":
				none, e := LockSupplierTx(f.ctx, tx, "")
				pbTxMust(t, "explicit no-account token", e)
				err = ApplySupplierEffectTx(f.ctx, tx, none, attempt, Effect{"procurement_event", source})
			case "effect_kind":
				err = ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"reservation", source})
				want = "invalid_request"
			case "other_effect":
				err = ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", uuid.NewString()})
			case "other_account":
				other := uuid.NewString()
				f.exec(t, tx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,'other','fixture','active',1000)`, other)
				alien, e := LockSupplierTx(f.ctx, tx, other)
				pbTxMust(t, "lock another supplier account", e)
				_, err = AdmitSupplierTx(f.ctx, tx, alien, attempt, Amount{"240"})
			}
			pbTxCode(t, err, want)
			pbTxMust(t, "rollback whole rejected source", tx.Rollback(f.ctx))
			if before != pbTxSupplierSnapshot(t, f) {
				t.Fatal("rejected supplier helper retained money/source/budget facts")
			}
		})
	}
	pbTxAssertSupplierConserved(t, f)
}

func TestTxSupplierZeroUpperAndLegacyCatchup(t *testing.T) {
	pbHandwrittenSource(t)
	t.Run("real_zero_price_U0", func(t *testing.T) {
		f := newPBTxFixture(t)
		f.configure(t, "procurement", true, 0)
		f.setClock(t, time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC))
		tx := f.begin(t)
		customer := pbTxCustomerLocks(t, f, tx, "")
		request, _ := f.newCustomer(t, tx, customer)
		attempt, locks, source := pbTxPrepareSupplier(t, f, tx, request, 0)
		_, err := AdmitSupplierTx(f.ctx, tx, locks, attempt, Amount{"0"})
		pbTxMust(t, "admit real zero supplier quote", err)
		pbTxMust(t, "mirror exact zero original reserve", ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", source}))
		pbTxMust(t, "commit exact zero supplier U", tx.Commit(f.ctx))
		var exact bool
		pbTxMust(t, "check zero-price snapshot and first event", f.app.QueryRow(f.ctx, `SELECT a.cost_input_rate=0 AND a.cost_output_rate=0 AND p.upper_amount=0 AND b.revision=1 AND b.upper_amount=0 AND e.allocation_before_revision=0 AND e.held_delta=0 AND e.spent_delta=0 FROM upstream_attempts a JOIN procurement_reservations p ON p.attempt_id=a.id JOIN periodic_budget_allocations b ON b.subject_id=a.id JOIN periodic_budget_events e ON e.allocation_id=b.id WHERE a.id=$1`, attempt).Scan(&exact))
		if !exact {
			t.Fatal("zero U skipped original effect or used a nonzero quote")
		}
		pbTxAssertSupplierConserved(t, f)
	})
	t.Run("closed_old_source_gets_exact_null_window_catchup", func(t *testing.T) {
		f := newPBTxFixture(t)
		tx := f.begin(t)
		customer := pbTxCustomerLocks(t, f, tx, "")
		request, _ := f.newCustomer(t, tx, customer)
		attempt, locks, reserve := pbTxPrepareSupplier(t, f, tx, request, 240)
		_, err := AdmitSupplierTx(f.ctx, tx, locks, attempt, Amount{"240"})
		pbTxMust(t, "unconfigured supplier admission", err)
		pbTxMust(t, "no-control real reserve", ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", reserve}))
		settle := pbTxChangeSupplier(t, f, tx, attempt, 0, 70, "settled")
		pbTxMust(t, "no-control closed supplier", ApplySupplierEffectTx(f.ctx, tx, locks, attempt, Effect{"procurement_event", settle}))
		pbTxMust(t, "commit pre-control closed supplier", tx.Commit(f.ctx))
		control := f.configure(t, "procurement", false, 1000)
		reopen := f.begin(t)
		reopenLocks := pbTxSupplierLocks(t, f, reopen, request, attempt)
		effect := pbTxChangeSupplier(t, f, reopen, attempt, 30, 70, "disputed")
		pbTxMust(t, "strict legacy catchup", ApplySupplierEffectTx(f.ctx, reopen, reopenLocks, attempt, Effect{"procurement_event", effect}))
		pbTxMust(t, "repeat catchup event", ApplySupplierEffectTx(f.ctx, reopen, reopenLocks, attempt, Effect{"procurement_event", effect}))
		pbTxMust(t, "commit exact legacy reopen", reopen.Commit(f.ctx))
		correction := f.begin(t)
		correctionLocks := pbTxSupplierLocks(t, f, correction, request, attempt)
		change := pbTxChangeSupplier(t, f, correction, attempt, 0, 100, "settled")
		pbTxMust(t, "absolute legacy correction after baseline", ApplySupplierEffectTx(f.ctx, correction, correctionLocks, attempt, Effect{"procurement_event", change}))
		pbTxMust(t, "commit legacy baseline correction", correction.Commit(f.ctx))
		var exact bool
		pbTxMust(t, "read frozen legacy baseline and null period", f.app.QueryRow(f.ctx, `SELECT origin='legacy_catchup' AND origin_source_id=$2 AND window_id IS NULL AND admitted_at IS NULL AND baseline=70 AND held_amount=0 AND spent_amount=30 AND source_spent=100 AND revision=2 AND (SELECT held_amount=0 AND spent_amount=30 FROM periodic_budget_controls WHERE id=$3) FROM periodic_budget_allocations WHERE subject_id=$1`, attempt, effect, control).Scan(&exact))
		if !exact {
			t.Fatal("legacy catchup inferred a historical window or lost its exact baseline")
		}
		pbTxAssertSupplierConserved(t, f)
	})
}

func TestTxPermitDispatchRequiresActualPreparedFullScopeFact(t *testing.T) {
	pbHandwrittenSource(t)
	f := newPBTxFixture(t)
	f.configure(t, "tenant", true, 120)
	f.configure(t, "key", true, 120)
	f.configure(t, "procurement", true, 240)
	f.setClock(t, time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC))
	tx := f.begin(t)
	customer := pbTxCustomerLocks(t, f, tx, "")
	request, _ := f.newCustomer(t, tx, customer)
	attempt, supplier, event := pbTxPrepareSupplier(t, f, tx, request, 240)
	pbTxMust(t, "complete supplier mirror before dispatch", ApplySupplierEffectTx(f.ctx, tx, supplier, attempt, Effect{"procurement_event", event}))
	pbTxMust(t, "commit prepared source", tx.Commit(f.ctx))
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run("independent_"+scope+"_cap", func(t *testing.T) {
			f.configure(t, scope, true, 0)
			before := pbTxSupplierSnapshot(t, f)
			dispatch := f.begin(t)
			cl := pbTxCustomerLocks(t, f, dispatch, request)
			f.exec(t, dispatch, `SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE`, attempt)
			f.exec(t, dispatch, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account)
			sl, e := LockSupplierTx(f.ctx, dispatch, f.account)
			pbTxMust(t, "dispatch supplier lock", e)
			at, e := PermitDispatchTx(f.ctx, dispatch, cl, sl, request, attempt)
			want := "periodic_budget_exceeded"
			if scope == "procurement" {
				want = "procurement_periodic_budget_exceeded"
			}
			pbTxCode(t, e, want)
			if !at.IsZero() {
				t.Fatal("rejected dispatch returned At")
			}
			pbTxMust(t, "rollback cap-rejected sending", dispatch.Rollback(f.ctx))
			if before != pbTxSupplierSnapshot(t, f) {
				t.Fatal("failed dispatch changed prepared state or period projection")
			}
			limit := int64(120)
			if scope == "procurement" {
				limit = 240
			}
			f.configure(t, scope, true, limit)
		})
	}
	f.setClock(t, time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	regress := f.begin(t)
	cl := pbTxCustomerLocks(t, f, regress, request)
	f.exec(t, regress, `SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE`, attempt)
	f.exec(t, regress, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account)
	sl, e := LockSupplierTx(f.ctx, regress, f.account)
	pbTxMust(t, "regression supplier lock", e)
	_, e = PermitDispatchTx(f.ctx, regress, cl, sl, request, attempt)
	pbTxCode(t, e, "periodic_clock_regression")
	pbTxMust(t, "rollback regression", regress.Rollback(f.ctx))
	f.setClock(t, time.Date(2026, 12, 10, 12, 0, 0, 123456000, time.UTC))
	send := f.begin(t)
	cl = pbTxCustomerLocks(t, f, send, request)
	f.exec(t, send, `SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE`, attempt)
	f.exec(t, send, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account)
	sl, e = LockSupplierTx(f.ctx, send, f.account)
	pbTxMust(t, "valid supplier lock", e)
	at, e := PermitDispatchTx(f.ctx, send, cl, sl, request, attempt)
	pbTxMust(t, "actual complete sending fact", e)
	if at.Format(utc6Layout) != "2026-12-10T12:00:00.123456Z" {
		t.Fatal("dispatch did not return SQL's unique At")
	}
	pbTxMust(t, "commit actual dispatch permit", send.Commit(f.ctx))
	replay := f.begin(t)
	cl = pbTxCustomerLocks(t, f, replay, request)
	f.exec(t, replay, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account)
	sl, e = LockSupplierTx(f.ctx, replay, f.account)
	pbTxMust(t, "replay supplier token", e)
	_, e = PermitDispatchTx(f.ctx, replay, cl, sl, request, attempt)
	pbTxCode(t, e, "periodic_budget_invariant")
	pbTxMust(t, "rollback duplicate dispatch", replay.Rollback(f.ctx))
	pbTxAssertSupplierConserved(t, f)
}

func TestTxPermitExplicitNoProcurementRequiresNoBindingOrSource(t *testing.T) {
	pbHandwrittenSource(t)
	f := newPBTxFixture(t)
	tx := f.begin(t)
	cl := pbTxCustomerLocks(t, f, tx, "")
	request, _ := f.newCustomer(t, tx, cl)
	attempt := uuid.NewString()
	f.exec(t, tx, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) VALUES($1,$2,$3,1,$4,$5,$6,'upstream')`, attempt, f.tenant, request, f.channel, f.version, f.credential)
	none, err := LockSupplierTx(f.ctx, tx, "")
	pbTxMust(t, "construct explicit no-procurement token", err)
	_, err = PermitDispatchTx(f.ctx, tx, cl, none, request, attempt)
	pbTxCode(t, err, "periodic_budget_invariant")
	pbTxMust(t, "rollback missing managed source", tx.Rollback(f.ctx))
	unmanaged := f.begin(t)
	cl = pbTxCustomerLocks(t, f, unmanaged, "")
	request, _ = f.newCustomer(t, unmanaged, cl)
	credential, version := uuid.NewString(), uuid.NewString()
	attempt = uuid.NewString()
	f.exec(t, unmanaged, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unmanaged-fixture')`, credential, f.channel)
	f.exec(t, unmanaged, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, version, f.channel, credential)
	f.exec(t, unmanaged, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'unmanaged',1,10)`, version, f.channel, credential)
	f.exec(t, unmanaged, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) VALUES($1,$2,$3,1,$4,$5,$6,'upstream')`, attempt, f.tenant, request, f.channel, version, credential)
	none, err = LockSupplierTx(f.ctx, unmanaged, "")
	pbTxMust(t, "explicit unmanaged supplier token", err)
	at, err := PermitDispatchTx(f.ctx, unmanaged, cl, none, request, attempt)
	pbTxMust(t, "unmanaged attempt obtains actual no-control permit", err)
	if at.Format(utc6Layout) != "2026-11-10T12:00:00.123456Z" {
		t.Fatal("no-control dispatch did not retain SQL At")
	}
	pbTxMust(t, "commit unmanaged dispatch", unmanaged.Commit(f.ctx))
	pbTxAssertSupplierConserved(t, f)
}
