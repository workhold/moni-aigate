package periodicbudget_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	budget "github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

type pbBillDispatchFixture struct {
	f                                  *pbTxFixture
	store                              *billing.Store
	commander                          *budget.Commander
	now, sourceAt                      time.Time
	channel, version, credential, cost string
	quote                              routing.CostQuote
	revisions, controls                map[string]string
}

func pbBillDispatchNew(t *testing.T) *pbBillDispatchFixture {
	t.Helper()
	f := newPBBillingFixture(t)
	d := &pbBillDispatchFixture{f: f, store: billing.New(f.app), commander: pbCommander(t, f),
		channel: uuid.NewString(), version: uuid.NewString(), credential: uuid.NewString(), cost: uuid.NewString(),
		revisions: map[string]string{}, controls: map[string]string{}}
	pbTxMust(t, "sample actual DB time for isolated dispatch fixture", f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&d.now))
	d.now = d.now.UTC()
	d.sourceAt = d.now.AddDate(0, 0, -1)
	// The fixture's original cost becomes effective at actual now. Create an
	// independent complete channel with a quote already effective at both the
	// simulated source time and actual quote-selection time. APP owns all writes.
	tx := f.begin(t)
	f.exec(t, tx, `INSERT INTO channels(id,name,current_version) VALUES($1,'periodic-dispatch',$2)`, d.channel, d.version)
	f.exec(t, tx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused-fixture')`, d.credential, d.channel)
	f.exec(t, tx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, d.version, d.channel, d.credential)
	f.exec(t, tx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'dispatch',1,10)`, d.version, d.channel, d.credential)
	f.exec(t, tx, `INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at) VALUES($1,$2,'upstream',2000000,2000000,'contract',$3)`, d.cost, d.channel, d.now.AddDate(0, 0, -3))
	f.exec(t, tx, `INSERT INTO credential_procurement_bindings(id,credential_id,channel_id,account_id,operation_id) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), d.credential, d.channel, f.account, uuid.NewString())
	pbTxMust(t, "commit APP dispatch catalog and managed quote", tx.Commit(f.ctx))
	rate := int64(2000000)
	d.quote = routing.CostQuote{VersionID: d.cost, InputRate: &rate, OutputRate: &rate, Basis: "contract"}
	f.setClock(t, d.now.AddDate(0, 0, -2))
	for _, scope := range []string{"tenant", "key", "procurement"} {
		d.cap(t, scope, "1000")
	}
	f.setClock(t, d.sourceAt)
	return d
}

func (d *pbBillDispatchFixture) cap(t *testing.T, scope, amount string) {
	t.Helper()
	revision := d.revisions[scope]
	if revision == "" {
		revision = "0"
	}
	in := budget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: revision, PeriodKind: "utc_day", Enabled: true, LimitAmount: amount}
	out, err := pbCommandCallFor(d.commander, d.f, scope)(d.f.ctx, in)
	pbTxMust(t, "configure actual periodic cap through Commander", err)
	pbTxMust(t, "validate cap operation receipt", out.Receipt.Validate())
	if out.Created != (revision == "0") || d.controls[scope] != "" && out.Receipt.Operation.ResourceID != d.controls[scope] {
		t.Fatal("dispatch fixture cap replaced its stable control")
	}
	d.revisions[scope], d.controls[scope] = out.Receipt.Result.Revision, out.Receipt.Operation.ResourceID
}

func (d *pbBillDispatchFixture) prepared(t *testing.T) (billing.Reservation, billing.Attempt) {
	t.Helper()
	f := d.f
	var requestDeadline time.Time
	pbTxMust(t, "sample real request deadline independent of simulated period clock", f.app.QueryRow(f.ctx, `SELECT clock_timestamp()+interval '5 minutes'`).Scan(&requestDeadline))
	r, err := d.store.Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key, ClientIP: "127.0.0.1"},
		RequestID: uuid.NewString(), ModelID: f.model, RequestHMAC: uuid.NewString(), IdempotencyKey: uuid.NewString(), ProfileVersion: f.profileVersion, PolicyVersion: "r3",
		InputUpperBound: 100, OutputLimit: 20, RequestDeadline: requestDeadline})
	pbTxMust(t, "actual billing Reserve with both customer scopes", err)
	t.Cleanup(func() { d.store.EndRequest(r.RequestID) })
	a, err := d.store.RecordAttempt(f.ctx, billing.AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: d.channel, ChannelVersionID: d.version,
		CredentialID: d.credential, UpstreamModel: "upstream", Sequence: 1, CostQuote: &d.quote})
	pbTxMust(t, "actual managed RecordAttempt and supplier budget reserve", err)
	if r.Amount != 120 || a.DeliveryState != "prepared" {
		t.Fatal("dispatch fixture did not create the original 120 customer reserve and prepared managed attempt")
	}
	d.state(t, r, a, "prepared", "accepted", false)
	return r, a
}

func pbBillDispatchCode(t *testing.T, err error, want string) {
	t.Helper()
	var safe *billing.Error
	if err == nil || !errors.As(err, &safe) || safe.Code != want {
		code := ""
		if safe != nil {
			code = safe.Code
		}
		t.Fatalf("billing dispatch safe code=%q, want %q", code, want)
	}
}

func (d *pbBillDispatchFixture) snapshot(t *testing.T) string {
	t.Helper()
	var extra string
	pbTxMust(t, "read complete dispatch and procurement rollback facts", d.f.app.QueryRow(d.f.ctx, `SELECT jsonb_build_object(
 'attempts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM upstream_attempts x),'[]'::jsonb),
 'supplier_reservations',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY attempt_id) FROM procurement_reservations x),'[]'::jsonb),
 'supplier_accounts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM procurement_accounts x),'[]'::jsonb),
 'supplier_events',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM procurement_budget_events x),'[]'::jsonb),
 'evidence',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM finalization_records x),'[]'::jsonb),
 'metering',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM metering_events x),'[]'::jsonb),
 'idempotency',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY key) FROM request_idempotency x),'[]'::jsonb),
 'recovery',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY request_id) FROM recovery_jobs x),'[]'::jsonb),
 'outbox',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY event_id) FROM projection_outbox x),'[]'::jsonb),
 'audits',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM audit_events x),'[]'::jsonb))::text`).Scan(&extra))
	return d.f.snapshot(t) + extra
}

func (d *pbBillDispatchFixture) state(t *testing.T, r billing.Reservation, a billing.Attempt, delivery, request string, permitted bool) {
	t.Helper()
	var actualDelivery, supplier, actualRequest, reservation string
	var customerHeld, keyHeld int64
	var supplierHeld string
	var hasPermit bool
	pbTxMust(t, "verify actual source and permission state", d.f.app.QueryRow(d.f.ctx, `SELECT a.delivery_state,p.state,r.status,v.status,v.amount,k.held,p.held_amount::text,a.pb_dispatch_checked_at IS NOT NULL
 FROM upstream_attempts a JOIN procurement_reservations p ON p.attempt_id=a.id JOIN request_records r ON r.id=a.request_id
 JOIN reservations v ON v.request_id=r.id JOIN api_keys k ON k.id=r.key_id AND k.tenant_id=r.tenant_id WHERE a.id=$1`, a.ID).Scan(
		&actualDelivery, &supplier, &actualRequest, &reservation, &customerHeld, &keyHeld, &supplierHeld, &hasPermit))
	if actualDelivery != delivery || supplier != delivery || actualRequest != request || reservation != "reserved" || customerHeld != 120 || keyHeld != 120 || supplierHeld != "240" || hasPermit != permitted {
		t.Fatal("dispatch changed customer money, original upper bound, procurement state or permission unexpectedly")
	}
}

func (d *pbBillDispatchFixture) permitted(t *testing.T, r billing.Reservation, a billing.Attempt, at time.Time) {
	t.Helper()
	d.state(t, r, a, "sending", "dispatching", true)
	var exact bool
	oldStart := time.Date(d.sourceAt.Year(), d.sourceAt.Month(), d.sourceAt.Day(), 0, 0, 0, 0, time.UTC)
	currentStart := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	pbTxMust(t, "verify single final dispatch clock and unchanged original allocations", d.f.app.QueryRow(d.f.ctx, `SELECT
 (SELECT pb_dispatch_checked_at=$2 FROM upstream_attempts WHERE id=$1)
 AND (SELECT count(*)=3 AND bool_and(a.window_id=w.id AND w.start_at=$3 AND a.held_amount=CASE WHEN c.scope='procurement' THEN 240 ELSE 120 END AND a.spent_amount=0)
 FROM periodic_budget_allocations a JOIN periodic_budget_controls c ON c.id=a.control_id JOIN periodic_budget_windows w ON w.id=a.window_id)
 AND (SELECT count(*)=3 AND bool_and(held_amount=0 AND spent_amount=0) FROM periodic_budget_windows WHERE start_at=$4)
 AND (SELECT count(*)=1 FROM journals WHERE reference=$5 AND kind='reserve')`, a.ID, at, oldStart, currentStart, r.RequestID).Scan(&exact))
	if !exact {
		t.Fatal("dispatch lost its final clock, migrated old allocations or duplicated a financial reserve")
	}
}

func (d *pbBillDispatchFixture) finishBelowTightCaps(t *testing.T, r billing.Reservation, a billing.Attempt) {
	t.Helper()
	f := d.f
	for _, scope := range []string{"tenant", "key", "procurement"} {
		d.cap(t, scope, "0")
	}
	_, err := d.store.RecordFinalization(f.ctx, billing.FinalizationInput{ID: uuid.NewString(), TenantID: f.tenant, RequestID: r.RequestID, AttemptID: a.ID,
		Source: "periodic-dispatch-fixture", SourceEventID: uuid.NewString(), Usage: billing.Usage{Status: "confirmed_final", InputTokens: 30, OutputTokens: 5, TotalTokens: 35},
		ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true, FinishReason: "stop"})
	pbTxMust(t, "favorable supplier evidence despite all caps tightened to zero", err)
	s, err := d.store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	pbTxMust(t, "unique actual customer settlement despite zero caps", err)
	if s.Status != "settled" || s.ChargedAmount != 35 {
		t.Fatal("favorable settlement was blocked or repriced")
	}
	var exact bool
	oldStart := time.Date(d.sourceAt.Year(), d.sourceAt.Month(), d.sourceAt.Day(), 0, 0, 0, 0, time.UTC)
	pbTxMust(t, "verify one wallet, supplier cost and original-window favorable settlement", f.app.QueryRow(f.ctx, `SELECT
 (SELECT balance=9965 FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE')
 AND (SELECT balance=0 FROM ledger_accounts WHERE tenant_id=$1 AND kind='HELD')
 AND (SELECT balance=35 FROM ledger_accounts WHERE tenant_id IS NULL AND kind='REVENUE')
 AND (SELECT spent=35 AND held=0 FROM api_keys WHERE id=$2)
 AND (SELECT held_amount=0 AND spent_amount=70 FROM procurement_accounts WHERE id=$3)
 AND (SELECT count(*)=3 AND bool_and(a.held_amount=0 AND a.spent_amount=CASE WHEN c.scope='procurement' THEN 70 ELSE 35 END AND w.start_at=$4)
 FROM periodic_budget_allocations a JOIN periodic_budget_controls c ON c.id=a.control_id JOIN periodic_budget_windows w ON w.id=a.window_id)
 AND (SELECT count(*)=1 FROM settlements WHERE request_id=$5)
 AND (SELECT count(*)=2 FROM procurement_budget_events)
 AND (SELECT count(*)=6 FROM periodic_budget_events)`, f.tenant, f.key, f.account, oldStart, r.RequestID).Scan(&exact))
	if !exact {
		t.Fatal("favorable completion lost conservation, original windows or unique source effects")
	}
	before := d.snapshot(t)
	replay, err := d.store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	pbTxMust(t, "re-read original unique settlement", err)
	if replay.ID != s.ID || replay.JournalID != s.JournalID || before != d.snapshot(t) {
		t.Fatal("settlement replay created new financial or budget effects")
	}
	f.verifyFinance(t)
}

func TestPBBillingDispatchEachTightenedScopeRollsBackThenFinishes(t *testing.T) {
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run(scope, func(t *testing.T) {
			d := pbBillDispatchNew(t)
			r, a := d.prepared(t)
			d.f.setClock(t, d.now)
			limit, code := "119", "periodic_budget_exceeded"
			if scope == "procurement" {
				limit, code = "239", "procurement_periodic_budget_exceeded"
			}
			d.cap(t, scope, limit)
			before := d.snapshot(t)
			in := billing.DispatchInput{TenantID: d.f.tenant, AttemptID: a.ID}
			pbBillDispatchCode(t, d.store.PrepareDispatch(d.f.ctx, in), code)
			if before != d.snapshot(t) {
				t.Fatal("failed cap check left sending, a new window, high-water mark or financial fact")
			}
			d.state(t, r, a, "prepared", "accepted", false)
			d.cap(t, scope, "1000")
			pbTxMust(t, "dispatch original prepared attempt after explicit cap restoration", d.store.PrepareDispatch(d.f.ctx, in))
			d.permitted(t, r, a, d.now)
			before = d.snapshot(t)
			pbBillDispatchCode(t, d.store.PrepareDispatch(d.f.ctx, in), "attempt_already_dispatched")
			if before != d.snapshot(t) {
				t.Fatal("already-sending attempt obtained a second permission or changed source facts")
			}
			d.finishBelowTightCaps(t, r, a)
		})
	}
}

func TestPBBillingDispatchConcurrentCallHasOneSendingPermission(t *testing.T) {
	d := pbBillDispatchNew(t)
	r, a := d.prepared(t)
	d.f.setClock(t, d.now)
	start := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			done <- d.store.PrepareDispatch(d.f.ctx, billing.DispatchInput{TenantID: d.f.tenant, AttemptID: a.ID})
		}()
	}
	close(start)
	successes := 0
	for i := 0; i < 2; i++ {
		err := <-done
		if err == nil {
			successes++
		} else {
			pbBillDispatchCode(t, err, "attempt_already_dispatched")
		}
	}
	if successes != 1 {
		t.Fatal("concurrent caller obtained duplicate sending permission")
	}
	d.permitted(t, r, a, d.now)
	d.finishBelowTightCaps(t, r, a)
}

func pbBillDispatchWaitForAccount(t *testing.T, d *pbBillDispatchFixture, blocker uint32, done <-chan error) {
	t.Helper()
	ctx, stop := context.WithTimeout(d.f.ctx, 2*time.Second)
	defer stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		pbTxMust(t, "observe actual final procurement-account lock wait", d.f.app.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND $2::integer=ANY(pg_blocking_pids(pid)))`, d.f.appRole, blocker).Scan(&waiting))
		if waiting {
			return
		}
		select {
		case <-tick.C:
		case err := <-done:
			var safe *billing.Error
			_ = errors.As(err, &safe)
			code := ""
			if safe != nil {
				code = safe.Code
			}
			t.Fatalf("dispatch returned before the intended final account wait (safe code %q)", code)
		case <-ctx.Done():
			t.Fatal("dispatch did not reach the final account synchronization point")
		}
	}
}

func TestPBBillingDispatchFinalAccountWaitRechecksPeriodExpiryAndQuote(t *testing.T) {
	for _, boundary := range []string{"period", "key_expiry", "scheduled_cost"} {
		t.Run(boundary, func(t *testing.T) {
			d := pbBillDispatchNew(t)
			r, a := d.prepared(t)
			finalAt := d.now
			want := ""
			if boundary == "key_expiry" {
				_, err := d.f.app.Exec(d.f.ctx, `UPDATE api_keys SET expires_at=$2 WHERE id=$1`, d.f.key, d.now.Add(time.Minute))
				pbTxMust(t, "set actual Key expiry beyond the initial real-time check", err)
				finalAt, want = d.now.Add(2*time.Minute), "invalid_key"
			} else if boundary == "scheduled_cost" {
				publish := d.f.begin(t)
				d.f.exec(t, publish, `SELECT id FROM channels WHERE id=$1 FOR UPDATE`, d.channel)
				d.f.exec(t, publish, `INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at) VALUES($1,$2,'upstream',3000000,3000000,'contract',$3)`, uuid.NewString(), d.channel, d.now.Add(time.Minute))
				pbTxMust(t, "APP publish scheduled quote before dispatch owns its channel", publish.Commit(d.f.ctx))
				finalAt, want = d.now.Add(2*time.Minute), "cost_quote_stale"
			}
			before := d.snapshot(t)
			hold := d.f.begin(t)
			var ignored string
			pbTxMust(t, "hold actual procurement account as the final wait", hold.QueryRow(d.f.ctx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, d.f.account).Scan(&ignored))
			done := make(chan error, 1)
			go func() {
				done <- d.store.PrepareDispatch(d.f.ctx, billing.DispatchInput{TenantID: d.f.tenant, AttemptID: a.ID})
			}()
			pbBillDispatchWaitForAccount(t, d, hold.Conn().PgConn().PID(), done)
			// Change only this random schema's pb_now while dispatch is known
			// to wait after its original real-clock eligibility statements.
			d.f.setClock(t, finalAt)
			pbTxMust(t, "release final account lock after the period/eligibility boundary", hold.Commit(d.f.ctx))
			err := <-done
			if want != "" {
				pbBillDispatchCode(t, err, want)
				if before != d.snapshot(t) {
					t.Fatal("final eligibility failure left sending, window/high-water or financial effects")
				}
				d.state(t, r, a, "prepared", "accepted", false)
				d.f.verifyFinance(t)
				return
			}
			pbTxMust(t, "dispatch after crossing the period during its final account wait", err)
			d.permitted(t, r, a, finalAt)
			d.finishBelowTightCaps(t, r, a)
		})
	}
}
