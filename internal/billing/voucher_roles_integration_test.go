package billing_test

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

func TestPostgresVoucherRestrictedApplicationRoleLifecycle(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for dedicated PostgreSQL role test")
	}
	cfg, e := pgxpool.ParseConfig(raw)
	if e != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("requires the dedicated local moni_test database on port 55439")
	}
	ctx := context.Background()
	admin, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	schema, appRole, projectionRole := "voucher_roles_"+suffix, "voucher_app_"+suffix, "voucher_projection_"+suffix
	var owner, app, projection *pgxpool.Pool
	t.Cleanup(func() {
		for _, p := range []*pgxpool.Pool{app, projection, owner} {
			if p != nil {
				p.Close()
			}
		}
		_, _ = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		for _, r := range []string{appRole, projectionRole} {
			_, _ = admin.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{r}.Sanitize())
			_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{r}.Sanitize())
		}
		admin.Close()
	})
	for _, sql := range []string{"CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize(), "CREATE ROLE " + pgx.Identifier{appRole}.Sanitize() + " NOLOGIN NOINHERIT NOSUPERUSER", "CREATE ROLE " + pgx.Identifier{projectionRole}.Sanitize() + " NOLOGIN NOINHERIT NOSUPERUSER"} {
		if _, e = admin.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	cfg = admin.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	owner, e = pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrations.ApplyCurrent(ctx, owner); e != nil {
		t.Fatal(e)
	}
	if e = operations.ProvisionFor(ctx, owner, schema, appRole, projectionRole); e != nil {
		t.Fatal(e)
	}
	asRole := func(role string) *pgxpool.Pool {
		t.Helper()
		c := owner.Config()
		c.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, e := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
			return e
		}
		p, e := pgxpool.NewWithConfig(ctx, c)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	app, projection = asRole(appRole), asRole(projectionRole)
	var current string
	var superuser bool
	if e = app.QueryRow(ctx, `SELECT current_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&current, &superuser); e != nil || current != appRole || superuser {
		t.Fatal("not executing through the restricted app role", e)
	}
	tenant, user, operator, approver, platformAdmin := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, e = app.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,'voucher role fixture')`, tenant); e != nil {
		t.Fatal(e)
	}
	for _, u := range []struct{ id, role string }{{user, "none"}, {operator, "finance_operator"}, {approver, "finance_approver"}, {platformAdmin, "admin"}} {
		if _, e = app.Exec(ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture',$3)`, u.id, u.id+"@fixture.invalid", u.role); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = app.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, tenant, user); e != nil {
		t.Fatal(e)
	}
	keys, e := security.ParseVoucherKeyring(`{"v1":"`+base64.RawURLEncoding.EncodeToString(make([]byte, 32))+`"}`, "v1")
	if e != nil {
		t.Fatal(e)
	}
	store := billing.New(app)
	service, e := billing.NewVoucherService(ctx, store, billing.VoucherOptions{RuntimeAllowed: true, Keys: keys})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = service.UpdateVoucherPolicy(ctx, billing.UpdateVoucherPolicyInput{ActorID: platformAdmin, ExpectedRevision: 1, Enabled: true}); e != nil {
		t.Fatal("restricted policy update", e)
	}
	command := func(actor string) billing.VoucherCommandContext {
		return billing.VoucherCommandContext{ActorID: actor, OperationID: uuid.NewString()}
	}
	issued, e := service.CreateVoucherBatch(ctx, billing.CreateVoucherBatchInput{Command: command(operator), FaceAmount: 29, Count: 2, ExpiresAt: time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond), PurposeRef: "grant:role-fixture", Reason: "restricted application test"})
	if e != nil || !issued.Created || issued.Batch == nil || len(issued.Codes) != 2 {
		t.Fatal("restricted issuance", e)
	}
	if r, e := service.ApproveVoucherBatch(ctx, billing.ApproveVoucherBatchInput{Command: command(approver), BatchID: issued.Batch.ID, ExpectedRevision: 1}); e != nil || r.Status != "accepted" {
		t.Fatal("restricted approval", e)
	}
	c := command(user)
	c.TenantID = tenant
	redemption, e := service.RedeemVoucher(ctx, billing.RedeemVoucherInput{Command: c, Code: issued.Codes[0].Code})
	if e != nil || redemption.Status != "accepted" || redemption.ResourceID == nil {
		t.Fatal("restricted funding", e)
	}
	if _, e = service.GetVoucherRedemption(ctx, billing.VoucherReader{ActorID: user, TenantID: tenant}, *redemption.ResourceID); e != nil {
		t.Fatal("restricted redemption safe query", e)
	}
	if s, e := service.GetFundingSummary(ctx, billing.VoucherReader{ActorID: user, TenantID: tenant}); e != nil || s.TotalAmount != "29" || s.VoucherGrantAmount != "29" {
		t.Fatal("restricted funding summary", e)
	}
	proposal, e := service.ProposeVoucherRevocation(ctx, billing.ProposeVoucherRevocationInput{Command: command(operator), BatchID: issued.Batch.ID, ExpectedRevision: 2, ExpectedRedeemedCount: 1, Reason: "restricted revoke"})
	if e != nil || proposal.ResourceID == nil {
		t.Fatal("restricted proposal", e)
	}
	if r, e := service.ApproveVoucherRevocation(ctx, billing.ApproveVoucherRevocationInput{Command: command(approver), ProposalID: *proposal.ResourceID}); e != nil || r.Status != "accepted" {
		t.Fatal("restricted revocation", e)
	}
	if c, e := store.VerifyFunding(ctx, tenant); e != nil || !c.Balanced {
		t.Fatal("restricted funding verification", e)
	}
	if n, e := billing.New(projection).Project(ctx, billing.ProjectInput{}); e != nil || n != 1 {
		t.Fatal("restricted projection", n, e)
	}
	if _, e = app.Exec(ctx, `UPDATE vouchers SET digest_key_version='forged'`); e == nil {
		t.Fatal("app rewrote immutable voucher history")
	}
	if _, e = projection.Exec(ctx, `SELECT * FROM funding_receipts`); e == nil {
		t.Fatal("projection role read funding facts")
	}
}
