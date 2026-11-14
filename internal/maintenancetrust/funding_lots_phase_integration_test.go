package maintenancetrust

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/security"
)

// The observer deliberately has no Permit or private phase. It independently
// reads the committed database on another connection after the owner closes.
type fundingLotsObserver struct {
	ctx context.Context
	tx  pgx.Tx
}
type fundingLotsObservedRows struct{ pgx.Rows }

func (r fundingLotsObservedRows) Next() (bool, error) { return r.Rows.Next(), nil }
func (r fundingLotsObservedRows) Close() error        { r.Rows.Close(); return nil }
func (q fundingLotsObserver) Query(sql string, args ...any) (fundinglotscan.Rows, error) {
	r, e := q.tx.Query(q.ctx, sql, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...)
	if e != nil {
		return nil, e
	}
	return fundingLotsObservedRows{r}, nil
}

type fundingLotsBasisObserver struct{ fundingLotsObserver }

func (q fundingLotsBasisObserver) Query(sql string, args ...any) (fundinglotbasis.Rows, error) {
	r, e := q.tx.Query(q.ctx, sql, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...)
	if e != nil {
		return nil, e
	}
	return fundingLotsObservedRows{r}, nil
}

func fundingLotsObserveCommitted(t *testing.T, c *pgx.Conn, schema string, mode fundinglotscan.Mode, expected FundingLotsExpected) fundinglotscan.WalletReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("independent committed observer begin")
	}
	defer tx.Rollback(context.Background())
	q := fundingLotsObserver{ctx, tx}
	facts, err := fundinglotscan.ReadWallet(ctx, q, schema, mode)
	r := facts.Summary()
	if err != nil || r.Status != "consistent" || !r.CoverageComplete || !r.PhysicalEOFComplete || r.ScannedTables != 84 {
		t.Fatalf("independent actual committed 84-table graph: error=%v status=%s coverage=%t", err, r.Status, r.CoverageComplete)
	}
	basis, err := fundinglotbasis.Read(ctx, fundingLotsBasisObserver{q}, schema)
	if err != nil || basis.Summary().BasisSHA256 != expected.BasisDigest || basis.Summary().ManifestSHA256 != expected.ManifestDigest || basis.Summary().DescriptorSHA256 != expected.DescriptorDigest {
		t.Fatal("original 109-table basis changed after initialization")
	}
	var raw []byte
	var digest, epoch, descriptor, actor string
	sql := `SELECT a.proof_canonical,a.proof_sha256,e.id,e.descriptor_digest,e.actor FROM ` + pgx.Identifier{schema, "funding_lot_runtime_attestations"}.Sanitize() + ` a CROSS JOIN ` + pgx.Identifier{schema, "funding_lot_epoch"}.Sanitize() + ` e`
	if tx.QueryRow(ctx, sql).Scan(&raw, &digest, &epoch, &descriptor, &actor) != nil {
		t.Fatal("committed epoch and strict proof raw rows")
	}
	proof, err := fundinglotproof.DecodeV2(raw)
	if err != nil || proof.SHA256() != expected.RuntimeProofDigest || digest != expected.RuntimeProofDigest || epoch != expected.EpochID || descriptor != expected.DescriptorDigest || actor != "offline-maintenance" {
		t.Fatal("committed strict v2 binding")
	}
	b, err := proof.Bindings()
	if err != nil || b.EpochID != expected.EpochID || b.BasisDigest != expected.BasisDigest || b.ManifestDigest != expected.ManifestDigest || b.BasisDescriptorDigest != expected.DescriptorDigest {
		t.Fatal("committed v2 basis/manifest bindings")
	}
	if tx.Commit(ctx) != nil {
		t.Fatal("independent committed observer close")
	}
	return r
}

func fundingLotsPlanOrdinary(t *testing.T, f *snapshotFixture, epoch string) FundingLotsPlanResult {
	t.Helper()
	g, s, w, backend := authorityInstalled(t, f, false)
	p, err := Attest(context.Background(), s)
	if err != nil {
		t.Fatal("real plan permit", err)
	}
	result, err := p.PlanFundingLots(context.Background(), epoch)
	if err != nil || result.Expected.EpochID != epoch || s.Outcome() != OutcomeCommitted {
		t.Fatalf("protected plan failed: %v outcome=%d stage=%d source_status=%s funding=%+v", err, s.Outcome(), s.state.fundingLots.stage, s.state.fundingLots.facts.Summary().Status, s.state.fundingLots.facts.Summary().Funding.Mismatches)
	}
	queries, _ := w.recorded()
	commits := 0
	for _, sql := range queries {
		if strings.EqualFold(sql, "commit") {
			commits++
		}
		if snapshotSQLStarts(sql, "INSERT") {
			t.Fatal("plan sent a data INSERT")
		}
	}
	if commits != 1 || p.Verify(context.Background()) == nil || s.Commit(context.Background()) != nil {
		t.Fatal("plan did not consume sole owner commit and revoke permit")
	}
	if g.Close(nil) != nil {
		t.Fatal("plan gate cleanup")
	}
	f.gone(t, backend)
	return result
}

func fundingLotsInitOrdinary(t *testing.T, f *snapshotFixture, expected FundingLotsExpected) FundingLotsInitResult {
	t.Helper()
	g, s, w, backend := authorityInstalled(t, f, true)
	p, err := Attest(context.Background(), s)
	if err != nil {
		t.Fatal("real init permit", err)
	}
	result, err := p.InitializeFundingLots(context.Background(), expected)
	if err != nil || s.Outcome() != OutcomeCommitted || result.RuntimeProofDigest != expected.RuntimeProofDigest {
		t.Fatalf("protected initialization failed: %v outcome=%d", err, s.Outcome())
	}
	queries, _ := w.recorded()
	commits := 0
	for _, sql := range queries {
		if strings.EqualFold(sql, "commit") {
			commits++
		}
	}
	if commits != 1 || p.Verify(context.Background()) == nil || s.Commit(context.Background()) != nil {
		t.Fatal("init did not consume sole owner commit and revoke permit")
	}
	if g.Close(nil) != nil {
		t.Fatal("init gate cleanup")
	}
	f.gone(t, backend)
	return result
}

func TestFundingLotsPhasePostgresEmptyAndZeroLegacyTenants(t *testing.T) {
	for _, withTenants := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty_installation", true: "old_empty_text_and_unicode_tenants"}[withTenants], func(t *testing.T) {
			f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) {
				if !withTenants {
					return
				}
				for _, id := range []string{"", "旧/tenant", "other"} {
					if _, err := pool.Exec(context.Background(), `INSERT INTO tenants(id,name) VALUES($1,'old')`, id); err != nil {
						t.Fatal("old tenant fixture")
					}
				}
				if _, err := pool.Exec(context.Background(), `INSERT INTO ledger_accounts(id,tenant_id,kind) VALUES('zero-account','other','AVAILABLE')`); err != nil {
					t.Fatal("old zero wallet fixture")
				}
			})
			epoch := uuid.NewString()
			plan := fundingLotsPlanOrdinary(t, f, epoch)
			want := uint64(0)
			if withTenants {
				want = 3
			}
			if plan.Metadata.TenantCount != want || plan.Metadata.OpeningCount != want || plan.Metadata.AvailableTotal != "0" || plan.Metadata.HeldTotal != "0" || plan.Metadata.GeneratedRows != 3*want+2 {
				t.Fatal("zero opening metadata")
			}
			init := fundingLotsInitOrdinary(t, f, plan.Expected)
			if init.Metadata.TenantCount != want || init.Metadata.GeneratedRows != plan.Metadata.GeneratedRows {
				t.Fatal("plan/init metadata changed")
			}
			c := f.connect(t)
			r := fundingLotsObserveCommitted(t, c, f.schema, fundinglotscan.ModeOrdinary, plan.Expected)
			if r.Wallet.Status != "consistent" {
				t.Fatal("actual zero wallet graph")
			}
			var operations, lots, zeroOpenings int
			if c.QueryRow(context.Background(), strings.ReplaceAll(`SELECT (SELECT count(*) FROM %s.funding_wallet_operations),(SELECT count(*) FROM %s.funding_lots),(SELECT count(*) FROM %s.funding_lot_legacy_openings WHERE amount=0)`, "%s", pgx.Identifier{f.schema}.Sanitize())).Scan(&operations, &lots, &zeroOpenings) != nil || uint64(operations) != want || lots != 0 || uint64(zeroOpenings) != want {
				t.Fatal("zero legacy tenant membership was omitted or funded twice")
			}
			// The original installation issuer must continue to reject nonempty C0.
			g, w, backend := f.acquire(t, "")
			s := f.begin(t, g, false, context.Background())
			if installed, err := s.VerifyInstallation(context.Background()); installed != nil || err != ErrSchema {
				t.Fatal("nonempty epoch reused empty-installation issuer")
			}
			_ = w
			f.gone(t, backend)
		})
	}
}

// All old financial facts are produced by original billing APIs before C0 DDL.
// Independent arithmetic: A=115+(M-50)=M+65; H=40+(M-120)=M-80.
// The second tenant's A+H=2M-170 must stay NUMERIC, never int64.
func fundingLotsMixedHistory(t *testing.T, f *snapshotFixture, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	store := billing.New(pool)
	operator, approver, admin, userA, userB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal("original mixed business fixture SQL")
		}
	}
	for _, u := range []struct{ id, role string }{{operator, "finance_operator"}, {approver, "finance_approver"}, {admin, "admin"}, {userA, "none"}, {userB, "none"}} {
		exec(`INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-only',$3)`, u.id, u.id+"@example.invalid", u.role)
	}
	for _, tenant := range []string{tenantA, tenantB, ""} {
		exec(`INSERT INTO tenants(id,name) VALUES($1,'legacy mixed')`, tenant)
	}
	for _, u := range []struct{ tenant, user string }{{tenantA, userA}, {tenantB, userB}} {
		exec(`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, u.tenant, u.user)
	}
	credit := func(tenant string, amount int64) {
		t.Helper()
		id := uuid.NewString()
		o, e := store.CreateCredit(ctx, billing.CreditInput{TenantID: tenant, ActorID: operator, Amount: billing.Money(amount), BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: billing.FiatMoney(amount), BusinessRef: id, ProofRef: "receipt:init-test", IdempotencyKey: id, RequestHMAC: id})
		if e != nil {
			t.Fatal("mixed credit create", e)
		}
		if _, e = store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: tenant, OrderID: o.ID, ActorID: approver}); e != nil {
			t.Fatal("mixed credit approve", e)
		}
	}
	keyJSON, _ := json.Marshal(map[string]string{"test": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))})
	keys, e := security.ParseVoucherKeyring(string(keyJSON), "test")
	if e != nil {
		t.Fatal("ephemeral voucher keyring")
	}
	v, e := billing.NewVoucherService(ctx, store, billing.VoucherOptions{RuntimeAllowed: true, Keys: keys})
	if e != nil {
		t.Fatal("mixed voucher service", e)
	}
	if _, e = v.UpdateVoucherPolicy(ctx, billing.UpdateVoucherPolicyInput{ActorID: admin, ExpectedRevision: 1, Enabled: true}); e != nil {
		t.Fatal("mixed voucher policy", e)
	}
	cmd := func(actor string) billing.VoucherCommandContext {
		return billing.VoucherCommandContext{OperationID: uuid.NewString(), ActorID: actor, RequestID: uuid.NewString()}
	}
	grant := func(tenant, user string, amount int64) {
		t.Helper()
		r, e := v.CreateVoucherBatch(ctx, billing.CreateVoucherBatchInput{Command: cmd(operator), FaceAmount: billing.Money(amount), Count: 1, ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond), PurposeRef: "grant:init-test", Reason: "old mixed fixture"})
		if e != nil || !r.Created || len(r.Codes) != 1 || r.Batch == nil {
			t.Fatalf("mixed voucher create: error=%v status=%s reason=%s", e, r.Receipt.Status, r.Receipt.Reason)
		}
		if a, e := v.ApproveVoucherBatch(ctx, billing.ApproveVoucherBatchInput{Command: cmd(approver), BatchID: r.Batch.ID, ExpectedRevision: 1}); e != nil || a.Status != "accepted" {
			t.Fatal("mixed voucher approve", e)
		}
		c := cmd(user)
		c.TenantID = tenant
		if a, e := v.RedeemVoucher(ctx, billing.RedeemVoucherInput{Command: c, Code: r.Codes[0].Code}); e != nil || a.Status != "accepted" {
			t.Fatal("mixed voucher redemption", e)
		}
	}
	reserve := func(tenant, user string, amount int64) billing.Reservation {
		t.Helper()
		model, key := uuid.NewString(), uuid.NewString()
		profile := `{"version":"v1","model":"fixture","input_token_upper_bound":1000000,"context_limit":1000001,"output_limit":1,"default_output_limit":1,"output_limit_field":"max_tokens"}`
		exec(`INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,$1,$2,'v1')`, model, profile)
		exec(`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, tenant, model)
		exec(`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes) VALUES($1,$2,$3,'fixture',$1,$1,$4)`, key, tenant, user, []string{model})
		exec(`INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,0,clock_timestamp()-interval '1 day',$3)`, model, amount, operator)
		r, e := store.Reserve(ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: tenant, UserID: user, KeyID: key}, RequestID: uuid.NewString(), ModelID: model, IdempotencyKey: uuid.NewString(), RequestHMAC: "fixture", ProfileVersion: "v1", InputUpperBound: 1000000, OutputLimit: 1, RequestDeadline: time.Now().UTC().Add(300 * time.Second)})
		if e != nil || int64(r.Amount) != amount {
			t.Fatal("mixed reserve", e)
		}
		return r
	}
	credit(tenantA, 100)
	grant(tenantA, userA, 70)
	r := reserve(tenantA, userA, 60)
	if _, e = store.RecordFinalization(ctx, billing.FinalizationInput{TenantID: tenantA, RequestID: r.RequestID, Source: "gateway", SourceEventID: uuid.NewString(), Usage: billing.Usage{Status: "confirmed_final", InputTokens: 500000, TotalTokens: 500000}, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none"}); e != nil {
		t.Fatal("mixed finalization", e)
	}
	s, e := store.Settle(ctx, billing.SettleInput{TenantID: tenantA, RequestID: r.RequestID})
	if e != nil || s.ChargedAmount != 30 {
		t.Fatal("mixed settle", e)
	}
	for _, amount := range []int64{10, 5, 7} {
		id := uuid.NewString()
		o, e := store.CreateRefund(ctx, billing.RefundInput{TenantID: tenantA, ActorID: operator, SettlementID: s.ID, Amount: billing.Money(amount), BusinessRef: id, Reason: "fixture", IdempotencyKey: id, RequestHMAC: id})
		if e != nil {
			t.Fatal("mixed refund create", e)
		}
		if amount != 7 {
			if _, e = store.ApproveRefund(ctx, billing.ApprovalInput{TenantID: tenantA, OrderID: o.ID, ActorID: approver}); e != nil {
				t.Fatal("mixed refund approve", e)
			}
		}
	}
	reserve(tenantA, userA, 40)
	reserve(tenantA, userA, 0)
	zero := reserve(tenantA, userA, 0)
	if _, e = store.Release(ctx, billing.ReleaseInput{TenantID: tenantA, RequestID: zero.RequestID, Reason: "no_delivery"}); e != nil {
		t.Fatal("mixed R0 terminal", e)
	}
	credit(tenantB, math.MaxInt64-100)
	reserve(tenantB, userB, math.MaxInt64-120)
	grant(tenantB, userB, math.MaxInt64-70)
}

func TestFundingLotsPhasePostgresMixedOriginalBusinessAndNumeric(t *testing.T) {
	f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) { fundingLotsMixedHistory(t, f, pool) })
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	m := plan.Metadata
	if m.TenantCount != 3 || m.ReceiptCount != 4 || m.SettlementCount != 2 || m.ApprovedRefundCount != 2 || m.OpeningCount != 6 || m.LegacySourceCount != 13 || m.AvailableTotal != "9223372036854775872" || m.HeldTotal != "9223372036854775727" || m.DataRows != 47 || m.GeneratedRows != 49 {
		t.Fatalf("independent mixed arithmetic does not match: %+v", m)
	}
	result := fundingLotsInitOrdinary(t, f, plan.Expected)
	if result.Metadata.AvailableTotal != m.AvailableTotal || result.Metadata.HeldTotal != m.HeldTotal {
		t.Fatal("init changed independently calculated numeric totals")
	}
	c := f.connect(t)
	fundingLotsObserveCommitted(t, c, f.schema, fundinglotscan.ModeOrdinary, plan.Expected)
	var lotCount, allocations, zeroMembership int
	var maxIssued string
	if c.QueryRow(context.Background(), strings.ReplaceAll(`SELECT (SELECT count(*) FROM %s.funding_lots),(SELECT count(*) FROM %s.funding_reservation_lots),(SELECT count(*) FROM %s.funding_wallet_legacy_sources WHERE amount=0),(SELECT max(issued_delta)::text FROM %s.funding_wallet_operations)`, "%s", pgx.Identifier{f.schema}.Sanitize())).Scan(&lotCount, &allocations, &zeroMembership, &maxIssued) != nil || lotCount != 4 || allocations != 2 || zeroMembership != 3 || maxIssued != "18446744073709551444" {
		t.Fatal("actual mixed writer sets or NUMERIC operation issuance")
	}
}

func TestFundingLotsPhasePostgresGenuineSandboxPreservesV1AndFunds(t *testing.T) {
	f := newAuthoritySandboxFixture(t)
	readV1 := func() ([]byte, string) {
		t.Helper()
		var raw []byte
		var digest string
		if f.admin.QueryRow(context.Background(), `SELECT proof_canonical,proof_sha256 FROM `+pgx.Identifier{f.pins.AppSchema.Name, "payment_runtime_attestations"}.Sanitize()).Scan(&raw, &digest) != nil {
			t.Fatal("original sandbox v1")
		}
		return raw, digest
	}
	before, beforeDigest := readV1()
	epoch := uuid.NewString()
	g, s, _, backend := f.installed(t, false)
	p, e := Attest(context.Background(), s)
	if e != nil {
		t.Fatal("sandbox plan permit", e)
	}
	plan, e := p.PlanFundingLots(context.Background(), epoch)
	if e != nil || s.Outcome() != OutcomeCommitted || plan.Metadata.ReceiptCount == 0 {
		t.Fatal("sandbox protected plan", e)
	}
	if g.Close(nil) != nil {
		t.Fatal("sandbox plan close")
	}
	f.gone(t, backend)
	g, s, _, backend = f.installed(t, true)
	p, e = Attest(context.Background(), s)
	if e != nil {
		t.Fatal("sandbox init permit", e)
	}
	result, e := p.InitializeFundingLots(context.Background(), plan.Expected)
	if e != nil || s.Outcome() != OutcomeCommitted || result.RuntimeProofDigest != plan.Expected.RuntimeProofDigest {
		t.Fatal("sandbox protected init", e)
	}
	if g.Close(nil) != nil {
		t.Fatal("sandbox init close")
	}
	f.gone(t, backend)
	fundingLotsObserveCommitted(t, f.admin, f.pins.AppSchema.Name, fundinglotscan.ModeSandbox, plan.Expected)
	after, afterDigest := readV1()
	if !bytes.Equal(before, after) || beforeDigest != afterDigest {
		t.Fatal("initialization changed immutable v1 proof")
	}
	if e = f.env.VerifyLegacyFundingUnchanged(context.Background()); e != nil {
		t.Fatal("initialization changed original sandbox money")
	}
}

func TestFundingLotsPhasePostgresExpectedDriftAndFailureCannotCommit(t *testing.T) {
	f := newSnapshotFixture(t)
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	for _, field := range []string{"basis", "manifest", "descriptor", "proof"} {
		t.Run(field, func(t *testing.T) {
			expected := plan.Expected
			wrong := strings.Repeat("f", 64)
			switch field {
			case "basis":
				expected.BasisDigest = wrong
			case "manifest":
				expected.ManifestDigest = wrong
			case "descriptor":
				expected.DescriptorDigest = wrong
			case "proof":
				expected.RuntimeProofDigest = wrong
			}
			g, s, w, backend := authorityInstalled(t, f, true)
			p, e := Attest(context.Background(), s)
			if e != nil {
				t.Fatal("drift init permit", e)
			}
			result, e := p.InitializeFundingLots(context.Background(), expected)
			if e == nil || !reflect.DeepEqual(result, FundingLotsInitResult{}) || s.Outcome() != OutcomeAbandoned {
				t.Fatal("stale expected initialized or retained result")
			}
			before, _ := w.recorded()
			if s.Commit(context.Background()) == nil {
				t.Fatal("public Commit bypassed failed phase")
			}
			if _, e = p.InitializeFundingLots(context.Background(), plan.Expected); e == nil {
				t.Fatal("failed phase was repeatable")
			}
			after, _ := w.recorded()
			if len(before) != len(after) {
				t.Fatal("failed phase sent fallback SQL")
			}
			for _, sql := range after {
				if snapshotSQLStarts(sql, "INSERT") || strings.EqualFold(sql, "commit") {
					t.Fatal("expected drift reached write or commit")
				}
			}
			f.gone(t, backend)
			_ = g.Close(nil)
		})
	}
}

func TestFundingLotsPhasePostgresLostCommitReplyNeverReinitializes(t *testing.T) {
	for _, cancelReply := range []bool{false, true} {
		t.Run(map[bool]string{false: "reply_loss", true: "caller_cancel_after_durable_commit"}[cancelReply], func(t *testing.T) {
			f := newSnapshotFixture(t)
			plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
			g, s, w, backend := authorityInstalled(t, f, true)
			p, e := Attest(context.Background(), s)
			if e != nil {
				t.Fatal("unknown init permit", e)
			}
			w.mu.Lock()
			w.arm = "commit"
			w.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type reply struct {
				result FundingLotsInitResult
				err    error
			}
			done := make(chan reply, 1)
			go func() { r, e := p.InitializeFundingLots(ctx, plan.Expected); done <- reply{r, e} }()
			fundingLotsAwaitReply(t, w)
			// This independently observes durable commit before withholding only the
			// driver's reply. It is a test oracle, not a production unknown observer.
			c := f.connect(t)
			fundingLotsObserveCommitted(t, c, f.schema, fundinglotscan.ModeOrdinary, plan.Expected)
			queries, _ := w.recorded()
			before := len(queries)
			for _, call := range []func() error{
				func() error { return s.Commit(context.Background()) }, func() error { return s.Rollback(nil) }, func() error { return g.Close(nil) },
				func() error { _, e := p.BeginScan(context.Background(), ScanLedger); return e },
				func() error { _, e := p.InitializeFundingLots(context.Background(), plan.Expected); return e },
			} {
				snapshotAssertNoAddedSQL(t, w, before, call)
			}
			if cancelReply {
				cancel()
			} else {
				close(w.drop)
			}
			select {
			case r := <-done:
				if r.err != ErrCommitUnknown || !reflect.DeepEqual(r.result, FundingLotsInitResult{}) || s.Outcome() != OutcomeUnknown {
					t.Fatal("durable lost-reply init not retained as unknown with zero result")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("lost-reply owner failed to finish")
			}
			select {
			case <-w.closed:
			default:
				t.Fatal("unknown original socket was not permanently closed")
			}
			f.gone(t, backend)
			beforeQueries, _ := w.recorded()
			if s.Commit(context.Background()) != ErrCommitUnknown {
				t.Fatal("retained terminal result changed")
			}
			_ = s.Rollback(nil)
			_ = g.Close(nil)
			if _, e = p.InitializeFundingLots(context.Background(), plan.Expected); e == nil {
				t.Fatal("unknown init repeated")
			}
			afterQueries, _ := w.recorded()
			if len(beforeQueries) != len(afterQueries) {
				t.Fatal("unknown init sent retry/compensation SQL")
			}
			commits := 0
			for _, sql := range afterQueries {
				if strings.EqualFold(sql, "commit") {
					commits++
				}
				if strings.EqualFold(sql, "rollback") {
					t.Fatal("unknown init attempted rollback")
				}
			}
			if commits != 1 {
				t.Fatal("unknown init did not send exactly one commit")
			}
		})
	}
}

// The full bounded phase can do substantial work before reaching the target
// reply; the old gate-only helper's five-second wait is not a phase budget.
func fundingLotsAwaitReply(t *testing.T, w *gateWire) {
	t.Helper()
	select {
	case <-w.reply:
	case <-time.After(35 * time.Second):
		t.Fatal("funding phase fixed SQL reply not observed within its cumulative budget")
	}
}

// The transport barrier is test-only and changes no SQL, catalog or driver
// behavior until the real server reply for the fixed first write arrives.
type fundingLotsFirstWriteWire struct{ *gateWire }

func (w *fundingLotsFirstWriteWire) Write(b []byte) (int, error) {
	if len(b) >= 6 && b[0] == 'Q' {
		sql := string(bytes.TrimSuffix(b[5:], []byte{0}))
		if snapshotSQLStarts(sql, "INSERT") && strings.Contains(sql, "funding_lot_runtime_attestations") {
			w.mu.Lock()
			w.arm = sql
			w.mu.Unlock()
		}
	}
	return w.gateWire.Write(b)
}

func TestFundingLotsPhasePostgresCancellationDuringWriterRetainsBusy(t *testing.T) {
	f := newSnapshotFixture(t)
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	cfg, ready := f.wireConfig(t, "")
	dial := cfg.DialFunc
	cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, e := dial(ctx, network, address)
		if e != nil {
			return nil, e
		}
		return &fundingLotsFirstWriteWire{c.(*gateWire)}, nil
	}
	g, e := Acquire(context.Background(), cfg, f.pins)
	if e != nil {
		t.Fatal("writer barrier gate", e)
	}
	t.Cleanup(func() { _ = g.Close(nil) })
	w := awaitWire(t, ready)
	backend := observedBackend{g.state.pid, g.state.backendStart}
	s := f.begin(t, g, true, context.Background())
	if _, e = s.VerifyInstallation(context.Background()); e != nil {
		t.Fatal("writer barrier installation", e)
	}
	p, e := Attest(context.Background(), s)
	if e != nil {
		t.Fatal("writer barrier permit", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		r, e := p.InitializeFundingLots(ctx, plan.Expected)
		if !reflect.DeepEqual(r, FundingLotsInitResult{}) {
			done <- ErrIdentity
			return
		}
		done <- e
	}()
	fundingLotsAwaitReply(t, w)
	queries, _ := w.recorded()
	before := len(queries)
	for _, call := range []func() error{func() error { return s.Commit(context.Background()) }, func() error { return g.Close(nil) }, func() error { _, e := p.BeginScan(context.Background(), ScanLedger); return e }, func() error { _, e := p.PlanFundingLots(context.Background(), plan.Expected.EpochID); return e }} {
		snapshotAssertNoAddedSQL(t, w, before, call)
	}
	cancel()
	select {
	case e := <-done:
		if e != ErrTimeout {
			t.Fatal("writer cancellation error", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled writer retained owner")
	}
	if s.Outcome() != OutcomeAbandoned || s.Commit(context.Background()) == nil {
		t.Fatal("cancelled writer could commit")
	}
	f.gone(t, backend)
	c := f.connect(t)
	var count int
	if c.QueryRow(context.Background(), f.snapshotSQL(`SELECT count(*) FROM %s.funding_lot_epoch`)).Scan(&count) != nil || count != 0 {
		t.Fatal("cancelled writer left durable epoch")
	}
	queries, _ = w.recorded()
	for _, sql := range queries {
		if strings.EqualFold(sql, "commit") {
			t.Fatal("cancelled writer sent commit")
		}
	}
}

func TestFundingLotsPhasePostgresActualBirthAndPostTamperingFailClosed(t *testing.T) {
	for _, kind := range []string{"non_null_birth", "missing_zero_opening", "legal_sha_wrong_proof_binding", "non68_basis_change"} {
		t.Run(kind, func(t *testing.T) {
			f := newSnapshotFixturePrepared(t, func(_ *snapshotFixture, pool *pgxpool.Pool) {
				if _, e := pool.Exec(context.Background(), `INSERT INTO tenants(id,name) VALUES('','legacy zero')`); e != nil {
					t.Fatal("tamper old zero tenant")
				}
			})
			g, s, w, backend := authorityInstalled(t, f, true)
			p, e := Attest(context.Background(), s)
			if e != nil {
				t.Fatal("tamper original permit", e)
			}
			ph, e := p.beginFundingLots(context.Background(), snapshotInit, uuid.NewString())
			if e != nil {
				t.Fatal("tamper real private phase", e)
			}
			defer ph.release()
			ctx := ph.call.ctx
			// Test-only owner fault: temporarily disable the exact table's USER
			// triggers in this original transaction, mutate one fact, then restore
			// them. Catalog is independently checked again before the real post.
			mutate := func(table, sql string) {
				t.Helper()
				name := pgx.Identifier{f.schema, table}.Sanitize()
				if _, e := s.state.rawTx.Exec(ctx, "ALTER TABLE "+name+" DISABLE TRIGGER USER"); e != nil {
					t.Fatal("test-only owner fault barrier")
				}
				if _, e := s.state.rawTx.Exec(ctx, strings.ReplaceAll(sql, "@TABLE@", name)); e != nil {
					t.Fatal("test-only owner fact mutation")
				}
				if _, e := s.state.rawTx.Exec(ctx, "ALTER TABLE "+name+" ENABLE TRIGGER USER"); e != nil {
					t.Fatal("restore original user triggers")
				}
			}
			if kind == "non_null_birth" {
				mutate("tenants", `UPDATE @TABLE@ SET fl_created_xid=pg_catalog.pg_current_xact_id()`)
				e = ph.prepareFundingLots(ctx)
				if e != ErrTransaction {
					t.Fatal("actual nonNULL birth was ignored", e)
				}
			} else {
				if e = ph.prepareFundingLots(ctx); e != nil {
					t.Fatal("tamper own complete prepare", e)
				}
				if e = ph.advance(ctx, fundingLotsDraft, fundingLotsWriting); e != nil {
					t.Fatal("tamper original writing transition", e)
				}
				if e = ph.writeFundingLots(ctx); e != nil {
					t.Fatal("tamper real fixed writer and flush", e)
				}
				switch kind {
				case "missing_zero_opening":
					mutate("funding_lot_legacy_openings", `DELETE FROM @TABLE@ WHERE kind='available'`)
				case "legal_sha_wrong_proof_binding":
					mutate("funding_lot_epoch", `UPDATE @TABLE@ SET basis_digest='ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff'`)
				case "non68_basis_change":
					if _, e = s.state.rawTx.Exec(ctx, f.snapshotSQL(`UPDATE %s.recovery_gate SET reason='post-basis-oracle'`)); e != nil {
						t.Fatal("non68 valid business change")
					}
				}
				if _, e = fundinglotcatalog.VerifySnapshotTx(ctx, s.state.rawTx, f.pins); e != nil {
					t.Fatal("fault failed to retain original authentic catalog", e)
				}
				if e = ph.advance(ctx, fundingLotsWriting, fundingLotsPost); e != nil {
					t.Fatal("tamper original post transition", e)
				}
				e = ph.verifyFundingLotsPost(ctx)
				want := ErrTransaction
				if kind == "legal_sha_wrong_proof_binding" {
					want = ErrSchema
				}
				if e != want {
					t.Fatal("actual post accepted mutated fact", e)
				}
				if kind == "missing_zero_opening" {
					if ph.post.Summary().Status != "mismatch" {
						t.Fatal("real84 did not identify missing zero opening")
					}
				} else if ph.post.Summary().Status != "consistent" {
					t.Fatal("fault intended to require strict proof/post109 but did not reach it")
				}
			}
			if ph.fail(e) == nil || ph.commitOwned(context.Background()) == nil {
				t.Fatal("failed actual checks reached ready or commit")
			}
			ph.release()
			if s.Commit(context.Background()) == nil || s.Outcome() != OutcomeAbandoned {
				t.Fatal("public commit bypassed failed post")
			}
			f.gone(t, backend)
			_ = g.Close(nil)
			queries, _ := w.recorded()
			for _, sql := range queries {
				if strings.EqualFold(sql, "commit") {
					t.Fatal("post failure sent commit")
				}
			}
			c := f.connect(t)
			var epochs, births int
			if c.QueryRow(context.Background(), strings.ReplaceAll(`SELECT (SELECT count(*) FROM %s.funding_lot_epoch),(SELECT count(*) FROM %s.tenants WHERE fl_created_xid IS NOT NULL)`, "%s", pgx.Identifier{f.schema}.Sanitize())).Scan(&epochs, &births) != nil || epochs != 0 || births != 0 {
				t.Fatal("failed phase retained durable tampering or epoch")
			}
		})
	}
}
