package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func procurementFixture(t *testing.T, f *fixture, limit int64) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,'shared','fixture','active',$2)`, id, limit); err != nil {
		t.Fatal(err)
	}
	procurementBind(t, f, id, f.channel, f.credential)
	return id
}
func procurementBind(t *testing.T, f *fixture, account, channel, credential string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO credential_procurement_bindings(id,credential_id,channel_id,account_id,operation_id) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), credential, channel, account, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

func procurementSecondChannel(t *testing.T, f *fixture) *fixture {
	t.Helper()
	g := *f
	g.channel = uuid.NewString()
	g.version = uuid.NewString()
	g.credential = uuid.NewString()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO channels(id,name,current_version) VALUES($1,'retry-channel',$2)`, []any{g.channel, g.version}},
		{`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'fixture')`, []any{g.credential, g.channel}},
		{`INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, []any{g.version, g.channel, g.credential}},
		{`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'retry',1,1)`, []any{g.version, g.channel, g.credential}},
	} {
		if _, err = tx.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	meterquote.PublishCost(t, f.ctx, tx, meterquote.CostPrice{ChannelID: g.channel, UpstreamModel: "upstream", ModelID: g.model, Actor: f.admin, Basis: "contract", InputRate: billingRate(2000000), OutputRate: billingRate(2000000)})
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return &g
}
func procurementAttempt(t *testing.T, f *fixture, r Reservation) AttemptInput {
	t.Helper()
	in := dispatchBatchAttemptInput(f, r)
	q, err := scanCostQuote(f.pool.QueryRow(f.ctx, currentAttemptCostSQL, f.channel, "upstream", r.RequestID), f.channel, "upstream")
	if err != nil {
		t.Fatal(err)
	}
	in.CostQuote = &q
	return in
}
func procurementBalance(t *testing.T, f *fixture, id, held, spent string) {
	t.Helper()
	var h, s string
	if err := f.pool.QueryRow(f.ctx, `SELECT held_amount::text,spent_amount::text FROM procurement_accounts WHERE id=$1`, id).Scan(&h, &s); err != nil || h != held || s != spent {
		t.Fatalf("procurement balance held=%s spent=%s expected %s/%s err=%v", h, s, held, spent, err)
	}
	var balanced bool
	if err := f.pool.QueryRow(f.ctx, `SELECT a.held_amount=coalesce(sum(e.held_delta),0) AND a.spent_amount=coalesce(sum(e.spent_delta),0) FROM procurement_accounts a LEFT JOIN procurement_budget_events e ON e.account_id=a.id WHERE a.id=$1 GROUP BY a.id`, id).Scan(&balanced); err != nil || !balanced {
		t.Fatal("event projection", balanced, err)
	}
}
func procurementFinal(t *testing.T, f *fixture, r Reservation, a Attempt, usage Usage, reason string) FinalizationInput {
	t.Helper()
	return FinalizationInput{ID: uuid.NewString(), TenantID: f.tenant, RequestID: r.RequestID, AttemptID: a.ID, Source: "adapter", SourceEventID: uuid.NewString(), Usage: usage, ProtocolOutcome: "completed", TransportEndReason: reason, DeliveryStage: "content", ContentWritten: true}
}

func TestPostgresProcurementSharedBudgetAcrossTenantsAndChannels(t *testing.T) {
	f := integration(t)
	account := procurementFixture(t, f, 240)
	g := *f
	g.tenant = uuid.NewString()
	g.key = uuid.NewString()
	g.channel = uuid.NewString()
	g.credential = uuid.NewString()
	g.version = uuid.NewString()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants(id,name) VALUES($1,'second')`, []any{g.tenant}},
		{`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, []any{g.tenant, g.user}},
		{`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, []any{g.tenant, g.model}},
		{`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'second','key','second-digest',$4,1000)`, []any{g.key, g.tenant, g.user, []string{g.model}}},
		{`INSERT INTO channels(id,name,current_version) VALUES($1,'second',$2)`, []any{g.channel, g.version}},
		{`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'fixture')`, []any{g.credential, g.channel}},
		{`INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, []any{g.version, g.channel, g.credential}},
		{`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'second',1,1)`, []any{g.version, g.channel, g.credential}},
	} {
		if _, err := tx.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	meterquote.PublishCost(t, f.ctx, tx, meterquote.CostPrice{ChannelID: g.channel, UpstreamModel: "upstream", ModelID: g.model, Actor: f.admin, Basis: "contract", InputRate: billingRate(2000000), OutputRate: billingRate(2000000)})
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	publishFixtureRouteWithBindings(t, f, 100,
		fixtureRouteBinding{channel: f.channel, version: f.version, upstream: "upstream"},
		fixtureRouteBinding{channel: g.channel, version: g.version, upstream: "upstream"},
	)
	g.fund(t, 1000)
	procurementBind(t, f, account, g.channel, g.credential)
	in1, in2 := f.input(), g.input()
	in1.RouteRevision, in2.RouteRevision = 100, 100
	r1, err := f.store.Reserve(f.ctx, in1)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.store.Reserve(f.ctx, in2)
	if err != nil {
		t.Fatal(err)
	}
	attempt1, attempt2 := procurementAttempt(t, f, r1), procurementAttempt(t, &g, r2)
	done := make(chan error, 2)
	go func() { _, e := f.store.RecordAttempt(f.ctx, attempt1); done <- e }()
	go func() { _, e := f.store.RecordAttempt(f.ctx, attempt2); done <- e }()
	success := 0
	for range 2 {
		if e := <-done; e == nil {
			success++
		} else {
			assertCode(t, e, "procurement_budget_exceeded")
		}
	}
	if success != 1 {
		t.Fatal("shared budget was duplicated", success)
	}
	procurementBalance(t, f, account, "240", "0")
}

func TestPostgresProcurementUnknownOutlivesCustomerReleaseAndRequiresChecker(t *testing.T) {
	f := integration(t)
	account := procurementFixture(t, f, 1000)
	r := f.reserve(t)
	a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	in := procurementFinal(t, f, r, a, Usage{Status: "missing"}, "connection_error")
	if _, err = f.store.RecordFinalization(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "provider_failure"}); err != nil {
		t.Fatal(err)
	}
	procurementBalance(t, f, account, "240", "0")
	assertCode(t, f.store.RecordCostCorrection(f.ctx, CostCorrectionInput{TenantID: f.tenant, AttemptID: a.ID, OperationID: uuid.NewString(), ActorID: f.operator, Amount: 12, Basis: "invoice", EvidenceRef: "invoice:test"}), "procurement_approval_required")
	pin := ProcurementCostProposalInput{OperationID: uuid.NewString(), TenantID: f.tenant, AttemptID: a.ID, ActorID: f.operator, Amount: 12, Basis: "invoice", EvidenceRef: "invoice:test", Reason: "supplier statement"}
	p, err := f.store.ProposeProcurementCost(f.ctx, pin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ProposeProcurementCost(f.ctx, pin); err != nil {
		t.Fatal(err)
	}
	approval := ProcurementCostApprovalInput{ProposalID: p.ID, OperationID: uuid.NewString(), ActorID: f.approver}
	if _, err = f.store.ApproveProcurementCost(f.ctx, approval); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveProcurementCost(f.ctx, approval); err != nil {
		t.Fatal("duplicate approval", err)
	}
	procurementBalance(t, f, account, "0", "12")
	f.wallet(t, 1000, 0)
	pin.OperationID = uuid.NewString()
	pin.Amount = 0
	pin.Basis = "contract"
	_, err = f.store.ProposeProcurementCost(f.ctx, pin)
	assertCode(t, err, "supplier_refund_evidence_required")
	pin.Basis = "invoice"
	p, err = f.store.ProposeProcurementCost(f.ctx, pin)
	if err != nil {
		t.Fatal(err)
	}
	approval.ProposalID = p.ID
	approval.OperationID = uuid.NewString()
	if _, err = f.store.ApproveProcurementCost(f.ctx, approval); err != nil {
		t.Fatal(err)
	}
	procurementBalance(t, f, account, "0", "0")
}

func TestPostgresProcurementConflictAndOversizedUsagePreserveFacts(t *testing.T) {
	f := integration(t)
	account := procurementFixture(t, f, 1000)
	r := f.reserve(t)
	a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	in := procurementFinal(t, f, r, a, Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 2, TotalTokens: 12}, "done")
	if _, err = f.store.RecordFinalization(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	procurementBalance(t, f, account, "0", "24")
	conflict := in
	conflict.ID = uuid.NewString()
	conflict.SourceEventID = uuid.NewString()
	conflict.Usage.InputTokens = 11
	conflict.Usage.TotalTokens = 13
	_, err = f.store.RecordFinalization(f.ctx, conflict)
	assertCode(t, err, "metering_conflict")
	procurementBalance(t, f, account, "216", "24")
	_, err = f.store.RecordFinalization(f.ctx, conflict)
	assertCode(t, err, "metering_conflict")
	procurementBalance(t, f, account, "216", "24")
	var paused bool
	if err = f.pool.QueryRow(f.ctx, `SELECT state='paused' FROM procurement_accounts WHERE id=$1`, account).Scan(&paused); err != nil || !paused {
		t.Fatal(paused, err)
	}
	p, err := f.store.ProposeProcurementCost(f.ctx, ProcurementCostProposalInput{OperationID: uuid.NewString(), TenantID: f.tenant, AttemptID: a.ID, ActorID: f.operator, Amount: 300, Basis: "invoice", EvidenceRef: "invoice:breach", Reason: "supplier exceeded bound"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveProcurementCost(f.ctx, ProcurementCostApprovalInput{ProposalID: p.ID, OperationID: uuid.NewString(), ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	procurementBalance(t, f, account, "0", "300")
}

func TestPostgresProcurementBindingBarrierAndRecovery(t *testing.T) {
	for _, mode := range []string{"legacy_bound_after_prepare", "expired_prepared", "expired_sending"} {
		t.Run(mode, func(t *testing.T) {
			f := integration(t)
			r := f.reserve(t)
			var account string
			if mode != "legacy_bound_after_prepare" {
				account = procurementFixture(t, f, 1000)
			}
			a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "legacy_bound_after_prepare" {
				account = procurementFixture(t, f, 1000)
				assertCode(t, f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}), "procurement_binding_changed")
				procurementBalance(t, f, account, "0", "0")
				return
			}
			if mode == "expired_sending" {
				if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = f.pool.Exec(f.ctx, `UPDATE request_records SET request_deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, r.RequestID); err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.RecoverProcurement(f.ctx, 100); err != nil {
				t.Fatal(err)
			}
			if mode == "expired_prepared" {
				procurementBalance(t, f, account, "0", "0")
			} else {
				procurementBalance(t, f, account, "240", "0")
			}
			if _, err = f.store.RecoverProcurement(f.ctx, 100); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresProcurementAccountWaitRechecksDeadlineAndPause(t *testing.T) {
	for _, mode := range []string{"paused", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			f := integration(t)
			account := procurementFixture(t, f, 1000)
			in := f.input()
			if mode == "deadline" {
				in.RequestDeadline = time.Now().Add(600 * time.Millisecond)
			}
			r, err := f.store.Reserve(f.ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
			if err != nil {
				t.Fatal(err)
			}
			blocker, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			var pid int
			if err = blocker.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if _, err = blocker.Exec(f.ctx, `UPDATE procurement_accounts SET state=CASE WHEN $2='paused' THEN 'paused' ELSE state END WHERE id=$1`, account, mode); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}) }()
			waitForBatchBlocker(t, f, pid)
			if mode == "deadline" {
				time.Sleep(650 * time.Millisecond)
			}
			if err = blocker.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			want := "procurement_paused"
			if mode == "deadline" {
				want = "request_deadline"
			}
			assertCode(t, dispatchBatchAwait(t, done), want)
			procurementBalance(t, f, account, "240", "0")
		})
	}
}

func TestPostgresProcurementSafeRetryOnlyReservesPerSupplierAttempt(t *testing.T) {
	f := integration(t)
	account := procurementFixture(t, f, 240)
	g := procurementSecondChannel(t, f)
	procurementBind(t, f, account, g.channel, g.credential)
	publishFixtureRouteWithBindings(t, f, 100,
		fixtureRouteBinding{channel: f.channel, version: f.version, upstream: "upstream"},
		fixtureRouteBinding{channel: g.channel, version: g.version, upstream: "upstream"},
	)
	inReserve := f.input()
	inReserve.RouteRevision = 100
	r, err := f.store.Reserve(f.ctx, inReserve)
	if err != nil {
		t.Fatal(err)
	}
	in := procurementAttempt(t, f, r)
	a, err := f.store.RecordAttempt(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.RecordAttempt(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	procurementBalance(t, f, account, "240", "0")
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	final := procurementFinal(t, f, r, a, Usage{Status: "missing"}, "rejected_no_consumption")
	final.ProtocolOutcome = "failed"
	final.DeliveryStage = "none"
	final.ContentWritten = false
	if _, err = f.store.RecordFinalization(f.ctx, final); err != nil {
		t.Fatal(err)
	}
	procurementBalance(t, f, account, "0", "0")
	in = procurementAttempt(t, g, r)
	in.Sequence = 2
	a, err = f.store.RecordAttempt(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	procurementBalance(t, f, account, "240", "0")
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	final = procurementFinal(t, f, r, a, Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 2, TotalTokens: 12}, "done")
	if _, err = f.store.RecordFinalization(f.ctx, final); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID}); err != nil {
		t.Fatal(err)
	}
	procurementBalance(t, f, account, "0", "24")
	f.wallet(t, 988, 0)
}

func TestPostgresProcurementStaleProposalCannotOverrideNewEvidence(t *testing.T) {
	f := integration(t)
	account := procurementFixture(t, f, 1000)
	r := f.reserve(t)
	a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.RecordFinalization(f.ctx, procurementFinal(t, f, r, a, Usage{Status: "missing"}, "connection_error")); err != nil {
		t.Fatal(err)
	}
	propose := func(amount Money) ProcurementCostProposal {
		t.Helper()
		p, e := f.store.ProposeProcurementCost(f.ctx, ProcurementCostProposalInput{OperationID: uuid.NewString(), TenantID: f.tenant, AttemptID: a.ID, ActorID: f.operator, Amount: amount, Basis: "invoice", EvidenceRef: "invoice:authoritative", Reason: "statement adjudication"})
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	p1, p2 := propose(12), propose(18)
	if _, err = f.store.ApproveProcurementCost(f.ctx, ProcurementCostApprovalInput{ProposalID: p1.ID, OperationID: uuid.NewString(), ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.ApproveProcurementCost(f.ctx, ProcurementCostApprovalInput{ProposalID: p2.ID, OperationID: uuid.NewString(), ActorID: f.approver})
	assertCode(t, err, "procurement_evidence_stale")
	procurementBalance(t, f, account, "0", "12")
	setFixtureReservationDeadline(t, f, r.RequestID, "-1 second")
	if _, err = f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "recovery"}); err != nil {
		t.Fatal(err)
	}
	procurementBalance(t, f, account, "0", "12")
	f.wallet(t, 1000, 0)
}

func TestPostgresProcurementScheduledQuoteRecheckedAfterAccountLock(t *testing.T) {
	f := integration(t)
	account := procurementFixture(t, f, 1000)
	r := f.reserve(t)
	var when time.Time
	costID := uuid.NewString()
	meterquote.PublishCost(t, f.ctx, f.pool, meterquote.CostPrice{ID: costID, ChannelID: f.channel, UpstreamModel: "upstream", ModelID: f.model, Actor: f.admin, Basis: "contract", InputRate: billingRate(3000000), OutputRate: billingRate(3000000), EffectiveAt: time.Now().UTC().Add(800 * time.Millisecond)})
	if err := f.pool.QueryRow(f.ctx, `SELECT effective_at FROM cost_versions WHERE id=$1`, costID).Scan(&when); err != nil {
		t.Fatal(err)
	}
	a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var pid int
	if err = blocker.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(f.ctx, `UPDATE procurement_accounts SET id=id WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}) }()
	waitForBatchBlocker(t, f, pid)
	if delay := time.Until(when.Add(20 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	if err = blocker.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	assertCode(t, dispatchBatchAwait(t, done), "cost_quote_stale")
	procurementBalance(t, f, account, "240", "0")
}

func TestPostgresProcurementContradictoryNonConsumptionAndRawOverrun(t *testing.T) {
	for _, mode := range []string{"contradictory_no_consumption", "raw_overrun"} {
		t.Run(mode, func(t *testing.T) {
			f := integration(t)
			account := procurementFixture(t, f, 1000)
			r := f.reserve(t)
			a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
				t.Fatal(err)
			}
			if mode == "contradictory_no_consumption" {
				first := procurementFinal(t, f, r, a, Usage{Status: "missing"}, "rejected_no_consumption")
				first.ContentWritten = false
				first.DeliveryStage = "none"
				first.ProtocolOutcome = "failed"
				if _, err = f.store.RecordFinalization(f.ctx, first); err != nil {
					t.Fatal(err)
				}
				procurementBalance(t, f, account, "0", "0")
				if _, err = f.store.RecordFinalization(f.ctx, procurementFinal(t, f, r, a, Usage{Status: "confirmed_final", InputTokens: 10, TotalTokens: 10}, "done")); err != nil {
					t.Fatal(err)
				}
				procurementBalance(t, f, account, "240", "0")
				var disputed bool
				if err = f.pool.QueryRow(f.ctx, `SELECT cost_status='unknown' FROM upstream_attempts WHERE id=$1`, a.ID).Scan(&disputed); err != nil || !disputed {
					t.Fatal(disputed, err)
				}
			} else {
				if _, err = f.store.RecordFinalization(f.ctx, procurementFinal(t, f, r, a, Usage{Status: "confirmed_final", InputTokens: 150, OutputTokens: 30, TotalTokens: 180}, "done")); err != nil {
					t.Fatal(err)
				}
				procurementBalance(t, f, account, "0", "360")
			}
			var paused bool
			if err = f.pool.QueryRow(f.ctx, `SELECT state='paused' FROM procurement_accounts WHERE id=$1`, account).Scan(&paused); err != nil || !paused {
				t.Fatal(paused, err)
			}
		})
	}
}
