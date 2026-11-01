package periodicbudget_test

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

type pbBillProcurementChannel struct{ channel, version, credential string }

func pbBillProcurementCatalog(t *testing.T, f *pbTxFixture) pbBillProcurementChannel {
	t.Helper()
	g := pbBillProcurementChannel{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	tx := f.begin(t)
	// This is a real APP catalog publication. Its sole quote predates the
	// explicit pb_now calendar fixture, so source At and actual DB quote agree.
	f.exec(t, tx, `INSERT INTO channels(id,name,current_version) VALUES($1,'periodic-billing-procurement',$2)`, g.channel, g.version)
	f.exec(t, tx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused-local-fixture')`, g.credential, g.channel)
	f.exec(t, tx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, g.version, g.channel, g.credential)
	f.exec(t, tx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'periodic-fixture',1,10)`, g.version, g.channel, g.credential)
	f.exec(t, tx, `INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at)
 VALUES($1,$2,'upstream',2000000,2000000,'contract',clock_timestamp()-interval '7 days')`, uuid.NewString(), g.channel)
	f.exec(t, tx, `INSERT INTO credential_procurement_bindings(id,credential_id,channel_id,account_id,operation_id) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), g.credential, g.channel, f.account, uuid.NewString())
	pbTxMust(t, "commit actual APP managed channel and historical-effective quote", tx.Commit(f.ctx))
	return g
}

func pbBillProcurementToday(t *testing.T, f *pbTxFixture) time.Time {
	t.Helper()
	var at time.Time
	pbTxMust(t, "read actual database calendar for isolated fixture", f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&at))
	return at.UTC().Truncate(time.Microsecond)
}

func pbBillProcurementConfigure(t *testing.T, f *pbTxFixture, scope, expected, limit string) string {
	t.Helper()
	in := pbCommandInput(expected, limit)
	in.PeriodKind = "utc_day"
	out, err := pbCommandCallFor(pbCommander(t, f), f, scope)(f.ctx, in)
	pbTxMust(t, "configure daily budget through actual Commander", err)
	return out.Receipt.Operation.ResourceID
}

func pbBillProcurementReserve(t *testing.T, f *pbTxFixture, s *billing.Store) billing.Reservation {
	t.Helper()
	var now time.Time
	pbTxMust(t, "read actual request deadline clock", f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now))
	id := uuid.NewString()
	r, err := s.Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key},
		RequestID: id, ModelID: f.model, IdempotencyKey: id, RequestHMAC: id, ProfileVersion: "v1", PolicyVersion: "r3",
		InputUpperBound: 100, OutputLimit: 20, RequestDeadline: now.Add(2 * time.Minute)})
	pbTxMust(t, "reserve one actual customer wallet source", err)
	if r.Amount != 120 {
		t.Fatal("customer hard reservation was not the complete R=120")
	}
	t.Cleanup(func() { s.EndRequest(r.RequestID) })
	return r
}

func pbBillProcurementAttemptInput(t *testing.T, f *pbTxFixture, r billing.Reservation, g pbBillProcurementChannel, sequence int) billing.AttemptInput {
	t.Helper()
	var quote routing.CostQuote
	pbTxMust(t, "read current actual procurement quote", f.app.QueryRow(f.ctx, `WITH at AS MATERIALIZED (SELECT clock_timestamp() AS stamp)
 SELECT id,input_rate,output_rate,basis FROM cost_versions,at WHERE channel_id=$1 AND upstream_model='upstream' AND effective_at<=at.stamp
 ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, g.channel).Scan(&quote.VersionID, &quote.InputRate, &quote.OutputRate, &quote.Basis))
	return billing.AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: g.channel, ChannelVersionID: g.version, CredentialID: g.credential,
		UpstreamModel: "upstream", Sequence: sequence, CostQuote: &quote}
}

func pbBillProcurementAttempt(t *testing.T, f *pbTxFixture, s *billing.Store, r billing.Reservation, g pbBillProcurementChannel, sequence int) billing.Attempt {
	t.Helper()
	a, err := s.RecordAttempt(f.ctx, pbBillProcurementAttemptInput(t, f, r, g, sequence))
	pbTxMust(t, "record actual managed supplier attempt", err)
	return a
}

func pbBillProcurementFinal(r billing.Reservation, a billing.Attempt, usage billing.Usage, reason string) billing.FinalizationInput {
	in := billing.FinalizationInput{ID: uuid.NewString(), TenantID: r.TenantID, RequestID: r.RequestID, AttemptID: a.ID,
		Source: "periodic-billing-procurement", SourceEventID: uuid.NewString(), Usage: usage,
		ProtocolOutcome: "completed", TransportEndReason: reason, DeliveryStage: "content", ContentWritten: true}
	if reason == "not_sent" || reason == "rejected_no_consumption" {
		in.ProtocolOutcome, in.DeliveryStage, in.ContentWritten = "failed", "none", false
	} else if reason == "connection_error" {
		in.ProtocolOutcome = "failed"
	}
	return in
}

func pbBillProcurementCode(err error) string {
	if err == nil {
		return ""
	}
	var safe *billing.Error
	if errors.As(err, &safe) && safe != nil {
		return safe.Code
	}
	return "unclassified"
}

func pbBillProcurementWantCode(t *testing.T, err error, code string) {
	t.Helper()
	if pbBillProcurementCode(err) != code {
		t.Fatalf("billing safe code=%q, expected %q", pbBillProcurementCode(err), code)
	}
}

func pbBillProcurementSnapshot(t *testing.T, f *pbTxFixture) string {
	t.Helper()
	var procurement string
	pbTxMust(t, "snapshot complete procurement and immutable effect facts", f.app.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'attempts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM upstream_attempts x),'[]'::jsonb),
 'accounts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM procurement_accounts x),'[]'::jsonb),
 'sources',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY attempt_id) FROM procurement_reservations x),'[]'::jsonb),
 'events',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM procurement_budget_events x),'[]'::jsonb),
 'identities',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY attempt_id,evidence_digest) FROM procurement_evidence_identities x),'[]'::jsonb),
 'proposals',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM procurement_cost_proposals x),'[]'::jsonb),
 'corrections',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM provider_cost_corrections x),'[]'::jsonb),
 'outbox',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY event_id) FROM projection_outbox x),'[]'::jsonb))::text`).Scan(&procurement))
	return pbCommandSnapshot(t, f) + procurement
}

func pbBillProcurementBalances(t *testing.T, f *pbTxFixture, control, held, spent string, events, historical int) {
	t.Helper()
	var sourceH, sourceS, budgetH, budgetS string
	var sourceEvents, mirrorEvents, missing int
	var conserved bool
	pbTxMust(t, "check actual procurement and periodic event conservation", f.app.QueryRow(f.ctx, `SELECT p.held_amount::text,p.spent_amount::text,c.held_amount::text,c.spent_amount::text,
 (SELECT count(*) FROM procurement_budget_events WHERE account_id=p.id),
 (SELECT count(*) FROM periodic_budget_events WHERE control_id=c.id),
 (SELECT count(*) FROM procurement_budget_events pe LEFT JOIN periodic_budget_events be ON be.control_id=c.id AND be.source_kind='procurement_event' AND be.source_id=pe.id WHERE pe.account_id=p.id AND be.id IS NULL),
 p.held_amount=coalesce((SELECT sum(held_delta) FROM procurement_budget_events WHERE account_id=p.id),0)
 AND p.spent_amount=coalesce((SELECT sum(spent_delta) FROM procurement_budget_events WHERE account_id=p.id),0)
 AND c.held_amount=coalesce((SELECT sum(held_amount) FROM periodic_budget_allocations WHERE control_id=c.id),0)
 AND c.spent_amount=coalesce((SELECT sum(spent_amount) FROM periodic_budget_allocations WHERE control_id=c.id),0)
 FROM procurement_accounts p JOIN periodic_budget_controls c ON c.account_id=p.id AND c.scope='procurement' WHERE p.id=$1 AND c.id=$2`, f.account, control).Scan(&sourceH, &sourceS, &budgetH, &budgetS, &sourceEvents, &mirrorEvents, &missing, &conserved))
	if sourceH != held || sourceS != spent || budgetH != held || sourceEvents != events+historical || mirrorEvents != events || missing != historical || !conserved {
		t.Fatal("procurement source, carry, immutable effects or periodic conservation differ")
	}
	if historical == 0 && budgetS != spent {
		t.Fatal("normal procurement spent did not mirror the absolute source cost")
	}
}

func pbBillProcurementCorrect(t *testing.T, f *pbTxFixture, s *billing.Store, attempt string, amount int64) billing.ProcurementCostApprovalInput {
	t.Helper()
	proposal, err := s.ProposeProcurementCost(f.ctx, billing.ProcurementCostProposalInput{OperationID: uuid.NewString(), TenantID: f.tenant,
		AttemptID: attempt, ActorID: f.operator, Amount: billing.Money(amount), Basis: "invoice", EvidenceRef: "invoice:periodic-fixture", Reason: "actual supplier statement correction"})
	pbTxMust(t, "propose absolute procurement cost with actual maker", err)
	approval := billing.ProcurementCostApprovalInput{ProposalID: proposal.ID, OperationID: uuid.NewString(), ActorID: f.approver}
	_, err = s.ApproveProcurementCost(f.ctx, approval)
	pbTxMust(t, "approve absolute procurement cost with distinct actual checker", err)
	return approval
}

func TestBillingProcurementPGCumulativeAndDailyCapIntersection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cumulative int64
		periodic   string
		code       string
	}{{"periodic_requires_full_U", 480, "239", "procurement_periodic_budget_exceeded"}, {"cumulative_requires_full_U", 239, "480", "procurement_budget_exceeded"}, {"both_exact_U", 240, "240", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPBBillingFixture(t)
			s := billing.New(f.app)
			g := pbBillProcurementCatalog(t, f)
			today := pbBillProcurementToday(t, f)
			f.setClock(t, today.AddDate(0, 0, -2))
			control := pbBillProcurementConfigure(t, f, "procurement", "0", tc.periodic)
			_, err := f.app.Exec(f.ctx, `UPDATE procurement_accounts SET limit_amount=$2 WHERE id=$1`, f.account, tc.cumulative)
			pbTxMust(t, "configure real cumulative procurement cap", err)
			f.setClock(t, today.AddDate(0, 0, -1))
			r := pbBillProcurementReserve(t, f, s)
			before := pbBillProcurementSnapshot(t, f)
			_, err = s.RecordAttempt(f.ctx, pbBillProcurementAttemptInput(t, f, r, g, 1))
			pbBillProcurementWantCode(t, err, tc.code)
			if tc.code != "" {
				if before != pbBillProcurementSnapshot(t, f) {
					t.Fatal("failed attempt admission retained an attempt, source, event, budget or wallet change")
				}
			} else {
				pbBillProcurementBalances(t, f, control, "240", "0", 1, 0)
				var upper string
				pbTxMust(t, "read immutable supplier hard upper", f.app.QueryRow(f.ctx, `SELECT upper_amount::text FROM procurement_reservations WHERE account_id=$1`, f.account).Scan(&upper))
				if upper != "240" {
					t.Fatal("supplier did not reserve complete U=240 independently from customer R=120")
				}
			}
			f.verifyFinance(t)
		})
	}
}

func TestBillingProcurementPGSecondAttemptHasFullUAndOneCustomerReserve(t *testing.T) {
	f := newPBBillingFixture(t)
	s := billing.New(f.app)
	firstChannel, secondChannel := pbBillProcurementCatalog(t, f), pbBillProcurementCatalog(t, f)
	today := pbBillProcurementToday(t, f)
	f.setClock(t, today.AddDate(0, 0, -2))
	control := pbBillProcurementConfigure(t, f, "procurement", "0", "240")
	pbBillProcurementConfigure(t, f, "tenant", "0", "120")
	pbBillProcurementConfigure(t, f, "key", "0", "120")
	f.setClock(t, today.AddDate(0, 0, -1))
	r := pbBillProcurementReserve(t, f, s)
	first := pbBillProcurementAttempt(t, f, s, r, firstChannel, 1)
	_, err := s.RecordFinalization(f.ctx, pbBillProcurementFinal(r, first, billing.Usage{Status: "missing"}, "rejected_no_consumption"))
	pbTxMust(t, "record trustworthy first-attempt no-consumption evidence", err)
	second := pbBillProcurementAttempt(t, f, s, r, secondChannel, 2)
	if second.ID == first.ID {
		t.Fatal("safe second attempt reused the original supplier identity")
	}
	pbBillProcurementBalances(t, f, control, "240", "0", 3, 0)
	var requests, customerSources, reserveJournals, supplierAllocations, customerAllocations int
	var complete bool
	pbTxMust(t, "check two full supplier admissions and single customer financial reservation", f.app.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM request_records),(SELECT count(*) FROM reservations),(SELECT count(*) FROM journals WHERE kind='reserve'),
 (SELECT count(*) FROM periodic_budget_allocations WHERE subject_type='supplier_attempt'),
 (SELECT count(*) FROM periodic_budget_allocations WHERE subject_type='customer_request'),
 (SELECT bool_and(upper_amount=240) FROM periodic_budget_allocations WHERE subject_type='supplier_attempt')`).Scan(&requests, &customerSources, &reserveJournals, &supplierAllocations, &customerAllocations, &complete))
	if requests != 1 || customerSources != 1 || reserveJournals != 1 || supplierAllocations != 2 || customerAllocations != 2 || !complete {
		t.Fatal("second supplier attempt duplicated customer money or failed to reserve its own complete U")
	}
	f.verifyFinance(t)
}

func TestBillingProcurementPGUnknownCarrySurvivesCustomerReleaseAndAbsoluteCorrection(t *testing.T) {
	f := newPBBillingFixture(t)
	s := billing.New(f.app)
	g := pbBillProcurementCatalog(t, f)
	today := pbBillProcurementToday(t, f)
	f.setClock(t, today.AddDate(0, 0, -2))
	control := pbBillProcurementConfigure(t, f, "procurement", "0", "240")
	f.setClock(t, today.AddDate(0, 0, -1))
	r := pbBillProcurementReserve(t, f, s)
	a := pbBillProcurementAttempt(t, f, s, r, g, 1)
	pbTxMust(t, "grant actual dispatch permit before recording unknown consumption", s.PrepareDispatch(f.ctx, billing.DispatchInput{TenantID: f.tenant, AttemptID: a.ID}))
	in := pbBillProcurementFinal(r, a, billing.Usage{Status: "missing"}, "connection_error")
	_, err := s.RecordFinalization(f.ctx, in)
	pbTxMust(t, "record actual unknown supplier consumption", err)
	pbBillProcurementBalances(t, f, control, "240", "0", 2, 0)
	before := pbBillProcurementSnapshot(t, f)
	_, err = s.RecordFinalization(f.ctx, in)
	pbTxMust(t, "replay original unknown finalization", err)
	if before != pbBillProcurementSnapshot(t, f) {
		t.Fatal("replayed unknown evidence appended a second supplier or periodic effect")
	}
	_, err = s.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "provider_failure"})
	pbTxMust(t, "release customer money independently of supplier unknown carry", err)
	pbBillProcurementBalances(t, f, control, "240", "0", 2, 0)
	var originalWindow string
	pbTxMust(t, "remember original supplier calendar", f.app.QueryRow(f.ctx, `SELECT window_id FROM periodic_budget_allocations WHERE control_id=$1 AND subject_id=$2`, control, a.ID).Scan(&originalWindow))
	f.setClock(t, today)
	pbBillProcurementConfigure(t, f, "procurement", "1", "0")
	approval := pbBillProcurementCorrect(t, f, s, a.ID, 12)
	pbBillProcurementBalances(t, f, control, "0", "12", 3, 0)
	before = pbBillProcurementSnapshot(t, f)
	_, err = s.ApproveProcurementCost(f.ctx, approval)
	pbTxMust(t, "replay exact approved absolute supplier correction", err)
	if before != pbBillProcurementSnapshot(t, f) {
		t.Fatal("approved supplier correction was applied twice")
	}
	var retainedWindow string
	var spent string
	pbTxMust(t, "verify next-day correction stays in the original supplier window", f.app.QueryRow(f.ctx, `SELECT a.window_id,w.spent_amount::text FROM periodic_budget_allocations a JOIN periodic_budget_windows w ON w.id=a.window_id WHERE a.control_id=$1 AND a.subject_id=$2`, control, a.ID).Scan(&retainedWindow, &spent))
	if originalWindow != retainedWindow || spent != "12" {
		t.Fatal("supplier correction moved the allocation or charged the new calendar window")
	}
	pbBillProcurementCorrect(t, f, s, a.ID, 0)
	pbBillProcurementBalances(t, f, control, "0", "0", 4, 0)
	f.verifyFinance(t)
}

func TestBillingProcurementPGConflictAndOverUpperCorrectionMirror(t *testing.T) {
	f := newPBBillingFixture(t)
	s := billing.New(f.app)
	g := pbBillProcurementCatalog(t, f)
	today := pbBillProcurementToday(t, f)
	f.setClock(t, today.AddDate(0, 0, -2))
	control := pbBillProcurementConfigure(t, f, "procurement", "0", "240")
	f.setClock(t, today.AddDate(0, 0, -1))
	r := pbBillProcurementReserve(t, f, s)
	a := pbBillProcurementAttempt(t, f, s, r, g, 1)
	pbTxMust(t, "grant actual permit before trustworthy final supplier usage", s.PrepareDispatch(f.ctx, billing.DispatchInput{TenantID: f.tenant, AttemptID: a.ID}))
	in := pbBillProcurementFinal(r, a, billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 2, TotalTokens: 12}, "done")
	_, err := s.RecordFinalization(f.ctx, in)
	pbTxMust(t, "record trustworthy supplier final cost", err)
	pbBillProcurementBalances(t, f, control, "0", "24", 2, 0)
	conflict := in
	conflict.ID, conflict.SourceEventID = uuid.NewString(), uuid.NewString()
	conflict.Usage.InputTokens, conflict.Usage.TotalTokens = 11, 13
	_, err = s.RecordFinalization(f.ctx, conflict)
	pbBillProcurementWantCode(t, err, "metering_conflict")
	pbBillProcurementBalances(t, f, control, "216", "24", 3, 0)
	_, err = s.RecordFinalization(f.ctx, conflict)
	pbBillProcurementWantCode(t, err, "metering_conflict")
	pbBillProcurementBalances(t, f, control, "216", "24", 3, 0)
	var sourceState, allocationState, accountState string
	pbTxMust(t, "check conserved disputed evidence and paused account", f.app.QueryRow(f.ctx, `SELECT p.state,a.state,c.state FROM procurement_reservations p
 JOIN periodic_budget_allocations a ON a.subject_id=p.attempt_id AND a.control_id=$1 JOIN procurement_accounts c ON c.id=p.account_id WHERE p.attempt_id=$2`, control, a.ID).Scan(&sourceState, &allocationState, &accountState))
	if sourceState != "disputed" || allocationState != "disputed" || accountState != "paused" {
		t.Fatal("conflicting supplier evidence did not preserve disputed carry and pause admission")
	}
	f.setClock(t, today)
	pbBillProcurementConfigure(t, f, "procurement", "1", "0")
	pbBillProcurementCorrect(t, f, s, a.ID, 300)
	pbBillProcurementBalances(t, f, control, "0", "300", 4, 0)
	pbBillProcurementCorrect(t, f, s, a.ID, 80)
	pbBillProcurementBalances(t, f, control, "0", "80", 5, 0)
	f.verifyFinance(t)
}

func TestBillingProcurementPGLegacyCatchupFreezesBaselineAndHasNoWindow(t *testing.T) {
	f := newPBBillingFixture(t)
	s := billing.New(f.app)
	g := pbBillProcurementCatalog(t, f)
	today := pbBillProcurementToday(t, f)
	f.setClock(t, today.AddDate(0, 0, -3))
	r := pbBillProcurementReserve(t, f, s)
	a := pbBillProcurementAttempt(t, f, s, r, g, 1)
	pbTxMust(t, "permit pre-configuration supplier attempt", s.PrepareDispatch(f.ctx, billing.DispatchInput{TenantID: f.tenant, AttemptID: a.ID}))
	in := pbBillProcurementFinal(r, a, billing.Usage{Status: "confirmed_final", InputTokens: 60, TotalTokens: 60}, "done")
	_, err := s.RecordFinalization(f.ctx, in)
	pbTxMust(t, "close the real supplier source before first periodic configuration", err)
	_, err = s.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	pbTxMust(t, "settle actual pre-configuration customer consumption", err)
	f.setClock(t, today.AddDate(0, 0, -2))
	control := pbBillProcurementConfigure(t, f, "procurement", "0", "240")
	var allocations int
	pbTxMust(t, "verify closed source was not imported as outstanding", f.app.QueryRow(f.ctx, `SELECT count(*) FROM periodic_budget_allocations WHERE control_id=$1`, control).Scan(&allocations))
	if allocations != 0 {
		t.Fatal("first configuration imported historical supplier spent")
	}
	f.setClock(t, today.AddDate(0, 0, -1))
	conflict := in
	conflict.ID, conflict.SourceEventID = uuid.NewString(), uuid.NewString()
	conflict.Usage.InputTokens, conflict.Usage.TotalTokens = 61, 61
	_, err = s.RecordFinalization(f.ctx, conflict)
	pbBillProcurementWantCode(t, err, "metering_conflict")
	pbBillProcurementBalances(t, f, control, "120", "120", 1, 2)
	f.setClock(t, today)
	pbBillProcurementConfigure(t, f, "procurement", "1", "0")
	for i, amount := range []int64{150, 80} {
		pbBillProcurementCorrect(t, f, s, a.ID, amount)
		pbBillProcurementBalances(t, f, control, "0", strconv.FormatInt(amount, 10), i+2, 2)
		var origin, baseline, budgetSpent string
		var window *string
		pbTxMust(t, "verify immutable legacy catchup baseline and non-window audit spent", f.app.QueryRow(f.ctx, `SELECT origin,baseline::text,spent_amount::text,window_id FROM periodic_budget_allocations WHERE control_id=$1 AND subject_id=$2`, control, a.ID).Scan(&origin, &baseline, &budgetSpent, &window))
		wantSpent := "30"
		if amount < 120 {
			wantSpent = "0"
		}
		if origin != "legacy_catchup" || baseline != "120" || budgetSpent != wantSpent || window != nil {
			t.Fatal("legacy catchup changed its source baseline, invented a window or made budget spent negative")
		}
	}
	f.verifyFinance(t)
}
