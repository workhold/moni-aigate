package operations

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

type operationFixture struct {
	ctx                                                   context.Context
	owner, app, projection                                *pgxpool.Pool
	schema, appRole, projectionRole                       string
	tenant, user, key, operator, approver, model, channel string
	secrets                                               *security.Secrets
}

func operationDB(t *testing.T) *operationFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for actual PostgreSQL role and rewrap tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	schema := "ops_" + suffix
	appRole := "ops_app_" + suffix
	projectionRole := "ops_projection_" + suffix
	for _, sql := range []string{"CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize(), "CREATE ROLE " + pgx.Identifier{appRole}.Sanitize() + " NOLOGIN NOINHERIT NOSUPERUSER", "CREATE ROLE " + pgx.Identifier{projectionRole}.Sanitize() + " NOLOGIN NOINHERIT NOSUPERUSER"} {
		if _, err = admin.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	owner, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	f := &operationFixture{ctx: ctx, owner: owner, schema: schema, appRole: appRole, projectionRole: projectionRole, tenant: uuid.NewString(), user: uuid.NewString(), key: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString(), secrets: &security.Secrets{Keys: map[string][]byte{"old": bytes.Repeat([]byte{1}, 32), "new": bytes.Repeat([]byte{2}, 32)}, Active: "old", Pepper: bytes.Repeat([]byte{3}, 32)}}
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		if f.projection != nil {
			f.projection.Close()
		}
		owner.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		for _, role := range []string{appRole, projectionRole} {
			_, _ = admin.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
			_, _ = admin.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize())
		}
		admin.Close()
	})
	if err = migrations.ApplyCurrent(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err = ProvisionFor(ctx, owner, schema, appRole, projectionRole); err != nil {
		t.Fatal(err)
	}
	asRole := func(role string) *pgxpool.Pool {
		t.Helper()
		cfg := owner.Config()
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
			return err
		}
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Ping(ctx); err != nil {
			t.Fatal(err)
		}
		return p
	}
	f.app = asRole(appRole)
	f.projection = asRole(projectionRole)
	if err = migrations.VerifyCurrent(ctx, f.app); err != nil {
		t.Fatalf("serving role cannot verify migrations: %v", err)
	}
	f.exec(t, "INSERT INTO tenants(id,name) VALUES($1,'operations-tenant')", f.tenant)
	for _, u := range []struct{ id, role string }{{f.user, "none"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"}} {
		f.exec(t, "INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture',$3)", u.id, u.id+"@ops.invalid", u.role)
	}
	f.exec(t, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", f.tenant, f.user)
	c := catalog.New(f.app, f.secrets)
	f.model, err = c.CreateModel(ctx, f.operator, catalog.ModelInput{PublicName: "ops-model", ContextLimit: 120, InputUpperBound: 100, OutputLimit: 20, DefaultOutputLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	f.channel, err = c.SaveChannel(ctx, f.operator, "", catalog.ChannelInput{Name: "ops-channel", BaseURL: "https://example.com", Secret: "provider-old-secret", Region: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Publish(ctx, f.operator, f.model, 0, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "upstream", Weight: 1, MaxInflight: 10}}); err != nil {
		t.Fatal(err)
	}
	meterquote.PublishDefaultPrice(t, ctx, f.app, meterquote.DefaultPrice{
		ModelID: f.model, Actor: f.operator, InputRate: 1000000, OutputRate: 1000000,
	})
	f.exec(t, "INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, f.model)
	f.exec(t, "INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'fixture','fixture','fixture',$4,1000)", f.key, f.tenant, f.user, []string{f.model})
	return f
}
func (f *operationFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.app.Exec(f.ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionedRolesFinancialLifecycle(t *testing.T) {
	f := operationDB(t)
	s := billing.New(f.app)
	credit, err := s.CreateCredit(f.ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: 1000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 1000, BusinessRef: "bank:verified", ProofRef: "proof:verified", IdempotencyKey: "credit:1", RequestHMAC: "credit-hmac"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: f.tenant, ActorID: f.approver, OrderID: credit.ID}); err != nil {
		t.Fatal(err)
	}
	var profileVersion string
	if err = f.app.QueryRow(f.ctx, "SELECT profile_version FROM models WHERE id=$1", f.model).Scan(&profileVersion); err != nil {
		t.Fatal(err)
	}
	reserved, err := s.Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, RouteRevision: 1, ProfileVersion: profileVersion, RequestHMAC: "inference-hmac", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().Add(300 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := catalog.New(f.app, f.secrets).Candidates(f.ctx, f.model, 1)
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	candidate := candidates[0]
	attempt, err := s.RecordAttempt(f.ctx, billing.AttemptInput{TenantID: f.tenant, RequestID: reserved.RequestID, ChannelID: candidate.ChannelID, ChannelVersionID: candidate.VersionID, CredentialID: candidate.CredentialID, UpstreamModel: candidate.UpstreamModel, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PrepareDispatch(f.ctx, billing.DispatchInput{TenantID: f.tenant, AttemptID: attempt.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordFinalization(f.ctx, billing.FinalizationInput{TenantID: f.tenant, RequestID: reserved.RequestID, AttemptID: attempt.ID, Source: "adapter", SourceEventID: "final:1", Usage: billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true}); err != nil {
		t.Fatal(err)
	}
	settled, err := s.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: reserved.RequestID})
	if err != nil || settled.ChargedAmount != 15 {
		t.Fatal(settled, err)
	}
	refund, err := s.CreateRefund(f.ctx, billing.RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settled.ID, Amount: 10, BusinessRef: "refund:verified", Reason: "support-verified", IdempotencyKey: "refund:1", RequestHMAC: "refund-hmac"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveRefund(f.ctx, billing.ApprovalInput{TenantID: f.tenant, ActorID: f.approver, OrderID: refund.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = billing.New(f.projection).Project(f.ctx, billing.ProjectInput{}); err != nil {
		t.Fatalf("real provisioned projection role failed: %v", err)
	}
	report, err := s.Report(f.ctx, billing.Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1000" || report.Charged != "15" || report.Refunded != "10" {
		t.Fatal(report, err)
	}
	wallet, err := s.Wallet(f.ctx, billing.Query{TenantID: f.tenant})
	if err != nil || wallet.Available != 995 || wallet.Held != 0 {
		t.Fatal(wallet, err)
	}
	for _, sql := range []string{"UPDATE ledger_accounts SET balance=0", "UPDATE api_keys SET spent=0", "INSERT INTO journals(tenant_id,operation_id,kind,reference,actor) SELECT id,'forged','forged','forged','projector' FROM tenants", "SELECT secret_ciphertext FROM channel_credentials", "UPDATE active_report_projection SET consumer='forged'"} {
		if _, err = f.projection.Exec(f.ctx, sql); err == nil {
			t.Fatalf("projector unexpectedly authorized: %s", sql)
		}
	}
	for _, sql := range []string{"UPDATE settlements SET charged_amount=0", "UPDATE price_versions SET input_rate=0", "UPDATE journals SET actor='forged'", "DELETE FROM schema_migrations", "CREATE TABLE forbidden(id int)"} {
		if _, err = f.app.Exec(f.ctx, sql); err == nil {
			t.Fatalf("serving role unexpectedly authorized: %s", sql)
		}
	}
	check, err := s.VerifyLedger(f.ctx, billing.Query{})
	if err != nil || !check.Balanced {
		t.Fatal(check, err)
	}
	if _, err = s.Rebuild(f.ctx, "ops-rebuild"); err != nil {
		t.Fatalf("offline app role rebuild failed: %v", err)
	}
}

func TestRewrapPreservesCredentialIdentityAndOldBackupKeys(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	oldCandidates, err := c.Candidates(f.ctx, f.model, 1)
	if err != nil || len(oldCandidates) != 1 {
		t.Fatal(oldCandidates, err)
	}
	old := oldCandidates[0]
	if _, err = c.SaveChannel(f.ctx, f.operator, f.channel, catalog.ChannelInput{Secret: "provider-new-secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Publish(f.ctx, f.operator, f.model, 1, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "upstream", Weight: 1, MaxInflight: 10}}); err != nil {
		t.Fatal(err)
	}
	newCandidates, err := c.Candidates(f.ctx, f.model, 2)
	if err != nil || len(newCandidates) != 1 {
		t.Fatal(newCandidates, err)
	}
	latest := newCandidates[0]
	if old.VersionID == latest.VersionID || old.CredentialID == latest.CredentialID || old.Secret == latest.Secret {
		t.Fatal("supplier secret rotation reused credential or route snapshot")
	}
	active, err := f.secrets.SealFor("JBSWY3DPEHPK3PXP", "mfa:user", f.user)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.secrets.SealFor("GEZDGNBVGY3TQOJQ", "mfa:user", f.user)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE users SET mfa_secret=$2,mfa_pending_secret=$3 WHERE id=$1", f.user, active, pending)
	type encryptedCredential struct{ ID, Value string }
	before := []encryptedCredential{}
	rows, err := f.app.Query(f.ctx, "SELECT id,secret_ciphertext FROM channel_credentials ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v encryptedCredential
		if err = rows.Scan(&v.ID, &v.Value); err != nil {
			t.Fatal(err)
		}
		before = append(before, v)
	}
	rows.Close()
	f.secrets.Active = "new"
	if err = Rewrap(f.ctx, f.owner, f.secrets); err != nil {
		t.Fatal(err)
	}
	onlyNew := &security.Secrets{Keys: map[string][]byte{"new": f.secrets.Keys["new"]}, Active: "new", Pepper: f.secrets.Pepper}
	for _, v := range before {
		var rewritten string
		if err = f.app.QueryRow(f.ctx, "SELECT secret_ciphertext FROM channel_credentials WHERE id=$1", v.ID).Scan(&rewritten); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(rewritten, "v2.new.") || rewritten == v.Value {
			t.Fatal("ciphertext was not rewrapped under new master key")
		}
		original, err := f.secrets.OpenFor(v.Value, "provider:platform", v.ID)
		if err != nil {
			t.Fatal("old backup lost decryption material", err)
		}
		plain, err := onlyNew.OpenFor(rewritten, "provider:platform", v.ID)
		if err != nil || plain != original {
			t.Fatal("rewrap changed supplier secret", err)
		}
		if _, err = onlyNew.OpenFor(rewritten, "provider:platform", uuid.NewString()); err == nil {
			t.Fatal("cross-credential ciphertext swap accepted")
		}
		if _, err = onlyNew.OpenFor(rewritten, "mfa:user", v.ID); err == nil {
			t.Fatal("cross-purpose ciphertext swap accepted")
		}
		if _, err = onlyNew.OpenFor(v.Value, "provider:platform", v.ID); err == nil {
			t.Fatal("test failed to prove old backups still need archived old key")
		}
	}
	var activeNew, pendingNew string
	if err = f.app.QueryRow(f.ctx, "SELECT mfa_secret,mfa_pending_secret FROM users WHERE id=$1", f.user).Scan(&activeNew, &pendingNew); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ cipher, want string }{{activeNew, "JBSWY3DPEHPK3PXP"}, {pendingNew, "GEZDGNBVGY3TQOJQ"}} {
		plain, err := onlyNew.OpenFor(v.cipher, "mfa:user", f.user)
		if err != nil || plain != v.want {
			t.Fatal("MFA secret changed", err)
		}
	}
	oldAfter, err := catalog.New(f.app, onlyNew).Candidates(f.ctx, f.model, 1)
	if err != nil || oldAfter[0].VersionID != old.VersionID || oldAfter[0].CredentialID != old.CredentialID || oldAfter[0].Secret != old.Secret {
		t.Fatal("rewrap changed old route semantics", oldAfter, err)
	}
	latestAfter, err := catalog.New(f.app, onlyNew).Candidates(f.ctx, f.model, 2)
	if err != nil || latestAfter[0].Secret != latest.Secret {
		t.Fatal("rewrap changed current route semantics", latestAfter, err)
	}
	// A corrupt later MFA row must roll back already rewritten credentials and audit.
	var saved string
	if err = f.app.QueryRow(f.ctx, "SELECT secret_ciphertext FROM channel_credentials WHERE id=$1", old.CredentialID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE users SET mfa_pending_secret='corrupt' WHERE id=$1", f.user)
	if err = Rewrap(f.ctx, f.owner, f.secrets); err == nil {
		t.Fatal("rewrap accepted corrupt source ciphertext")
	}
	var unchanged string
	if err = f.app.QueryRow(f.ctx, "SELECT secret_ciphertext FROM channel_credentials WHERE id=$1", old.CredentialID).Scan(&unchanged); err != nil || unchanged != saved {
		t.Fatal("rewrap partially committed credentials", err)
	}
	var audits int
	if err = f.app.QueryRow(f.ctx, "SELECT count(*) FROM audit_events WHERE action='encryption.rewrap'").Scan(&audits); err != nil || audits != 1 {
		t.Fatal("failed rewrap committed an audit", audits, err)
	}
}

func TestPersistentRecoveryGateAndDualApproval(t *testing.T) {
	f := operationDB(t)
	ctx := f.ctx
	credit, err := billing.New(f.app).CreateCredit(ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: 1000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 1000, BusinessRef: "recovery-credit", ProofRef: "verified", IdempotencyKey: "recovery-credit", RequestHMAC: "recovery-credit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = billing.New(f.app).ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	var profile string
	if err = f.app.QueryRow(ctx, "SELECT profile_version FROM models WHERE id=$1", f.model).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	_, err = billing.New(f.app).Reserve(ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, ModelID: f.model, RouteRevision: 1, ProfileVersion: profile, RequestHMAC: "recovery-hold", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().Add(300 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	latch := func() {
		t.Helper()
		if _, err := f.owner.Exec(ctx, "UPDATE recovery_gate SET required=true,epoch=epoch+1,proposal_id=NULL,requester=NULL,approver=NULL,evidence_ref=NULL,reason=NULL,proposed_at=NULL,approved_at=NULL,latched_at=clock_timestamp() WHERE singleton"); err != nil {
			t.Fatal(err)
		}
	}
	latch()
	t.Setenv("RECOVERY_MODE", "false")
	required, err := RecoveryRequired(ctx, f.app)
	if err != nil || !required {
		t.Fatal("database latch depended on env override", required, err)
	}
	proposal := RecoveryProposal{ActorID: f.operator, Epoch: 1, EvidenceRef: "external-receipts:verified", Reason: "bank and supplier receipts reconciled"}
	denied := proposal
	denied.ActorID = f.user
	if _, err = ProposeRecovery(ctx, f.app, denied); !errors.Is(err, ErrRecoveryForbidden) {
		t.Fatal(err)
	}
	proposed, err := ProposeRecovery(ctx, f.app, proposal)
	if err != nil || proposed.ProposalID == "" || !proposed.Required {
		t.Fatal(proposed, err)
	}
	again, err := ProposeRecovery(ctx, f.app, proposal)
	if err != nil || again.ProposalID != proposed.ProposalID {
		t.Fatal(again, err)
	}
	denied = proposal
	denied.Reason = "changed after review"
	if _, err = ProposeRecovery(ctx, f.app, denied); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatal("proposal was mutable", err)
	}
	approval := RecoveryApproval{ActorID: f.approver, Epoch: 1, ProposalID: proposed.ProposalID}
	wrong := approval
	wrong.ActorID = f.operator
	if _, err = ApproveRecovery(ctx, f.app, wrong); !errors.Is(err, ErrRecoveryForbidden) {
		t.Fatal(err)
	}
	wrong = approval
	wrong.Epoch = 2
	if _, err = ApproveRecovery(ctx, f.app, wrong); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(ctx, "UPDATE users SET status='suspended' WHERE id=$1", f.approver); err != nil {
		t.Fatal(err)
	}
	if _, err = ApproveRecovery(ctx, f.app, approval); !errors.Is(err, ErrRecoveryForbidden) {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(ctx, "UPDATE users SET status='active' WHERE id=$1", f.approver); err != nil {
		t.Fatal(err)
	}
	operationCorruptLedger(t, f, "UPDATE ledger_accounts SET balance=balance+1 WHERE tenant_id=$1 AND kind='AVAILABLE'", f.tenant)
	if _, err = ApproveRecovery(ctx, f.app, approval); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatal("unbalanced ledger unlocked recovery", err)
	}
	operationCorruptLedger(t, f, "UPDATE ledger_accounts SET balance=balance-1 WHERE tenant_id=$1 AND kind='AVAILABLE'", f.tenant)
	cleared, err := ApproveRecovery(ctx, f.app, approval)
	if err != nil || cleared.Required || cleared.Approver != f.approver {
		t.Fatal(cleared, err)
	}
	replayed, err := ApproveRecovery(ctx, f.app, approval)
	if err != nil || replayed.ApprovedAt == nil || !replayed.ApprovedAt.Equal(*cleared.ApprovedAt) {
		t.Fatal(replayed, err)
	}
	wallet, err := billing.New(f.app).Wallet(ctx, billing.Query{TenantID: f.tenant})
	if err != nil || wallet.Available != 880 || wallet.Held != 120 {
		t.Fatal("recovery approval changed existing customer holds", wallet, err)
	}
	latch()
	if _, err = ApproveRecovery(ctx, f.app, approval); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatal("old epoch approval unlocked a new recovery", err)
	}
	proposal.Epoch = 2
	second, err := ProposeRecovery(ctx, f.app, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(ctx, `CREATE FUNCTION fail_recovery_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='recovery.approved' THEN RAISE EXCEPTION 'audit failure'; END IF; RETURN NEW; END$$; CREATE TRIGGER injected BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_recovery_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = ApproveRecovery(ctx, f.app, RecoveryApproval{ActorID: f.approver, Epoch: 2, ProposalID: second.ProposalID}); err == nil {
		t.Fatal("approval committed without audit")
	}
	required, err = RecoveryRequired(ctx, f.app)
	if err != nil || !required {
		t.Fatal("failed audit partially cleared recovery gate", required, err)
	}
}

// operationCorruptLedger commits deliberately inconsistent history as the
// schema owner. Replica mode keeps the current wallet guards from vetoing the
// simulated corruption that the recovery gate itself must detect.
func operationCorruptLedger(t *testing.T, f *operationFixture, query string, args ...any) {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `SET LOCAL session_replication_role='replica'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}
