package maintenancescanqa_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/maintenancescan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/security"
)

// This external consumer imports only public production entry points. It starts
// with genuine v1 funded QA data, appends exactly the frozen C0 DDL and ACL, and
// runs both real readers on the same attested maintenance transaction.
func TestMaintenanceConsumersPostgresFundedSnapshot(t *testing.T) {
	for _, mode := range []string{"plan", "init"} {
		t.Run(mode, func(t *testing.T) {
			f := newConsumerFixture(t)
			ctx := context.Background()
			if mode == "init" {
				breakConsumerManualReceiptLink(t, f, ctx, f.fundingSourceCreditID)
			}
			fundingBeforeScan, err := f.env.FundingSnapshot(ctx)
			if err != nil {
				t.Fatal("consumer funding baseline after voucher fixture failed")
			}
			g, err := maintenancetrust.Acquire(ctx, f.cfg, f.pins)
			if err != nil || g == nil {
				t.Fatal("real public maintenance Acquire failed")
			}
			f.gates = append(f.gates, g)
			f.observeMaintenance(t)
			var snapshot *maintenancetrust.Snapshot
			if mode == "init" {
				snapshot, err = g.BeginInit(ctx)
			} else {
				snapshot, err = g.BeginPlan(ctx)
			}
			if err != nil || snapshot == nil {
				t.Fatal("real public maintenance snapshot failed")
			}
			installation, err := snapshot.VerifyInstallation(ctx)
			if err != nil || installation == nil {
				t.Fatal("genuine installation proof failed")
			}
			// The caller's descriptive copy does not own the installed authority.
			*installation = fundinglotcatalog.InstallationProof{}
			permit, err := maintenancetrust.Attest(ctx, snapshot)
			if err != nil || permit == nil {
				t.Fatal("genuine funded predecessor attestation failed")
			}
			var ledger *maintenancescan.MaintenanceLedgerResult
			var budget *maintenancescan.MaintenanceResult
			var fundingLinks *maintenancescan.FundingSourceLinksResult
			readLedger := func() {
				lease, e := permit.BeginScan(ctx, maintenancetrust.ScanLedger)
				if e != nil {
					t.Fatal("real ledger lease failed")
				}
				ledger, e = maintenancescan.VerifyLedgerMaintenance(lease)
				if e != nil || ledger == nil {
					t.Fatal("real maintenance ledger scan failed")
				}
			}
			readBudget := func() {
				lease, e := permit.BeginScan(ctx, maintenancetrust.ScanPeriodicBudget)
				if e != nil {
					t.Fatal("real periodic budget lease failed")
				}
				budget, e = maintenancescan.VerifyMaintenance(lease)
				if e != nil || budget == nil {
					t.Fatal("real maintenance periodic budget scan failed")
				}
			}
			readFundingLinks := func() {
				lease, e := permit.BeginScan(ctx, maintenancetrust.ScanFunding)
				if e != nil {
					t.Fatal("real funding source lease failed")
				}
				fundingLinks, e = maintenancescan.VerifyFundingSourceLinksMaintenance(lease)
				if e != nil || fundingLinks == nil {
					t.Fatal("real maintenance funding source link scan failed")
				}
			}
			if mode == "plan" {
				readLedger()
				readFundingLinks()
				readBudget()
			} else {
				readBudget()
				readFundingLinks()
				readLedger()
			}
			check, err := ledger.Summary(permit)
			if err != nil || check.Scope != "supplied_ledger_rows" || !check.RowsConsistent || check.Scanned.Accounts == 0 || check.Scanned.Journals == 0 || check.Scanned.Lines == 0 {
				t.Fatal("funded ledger local facts were not retained")
			}
			report, err := budget.Report(permit)
			if err != nil || report.SchemaVersion != 1 {
				t.Fatal("periodic budget local report was not retained")
			}
			fundingCheck, err := fundingLinks.Summary(permit)
			wantConsistent := mode == "plan"
			linksMatch := fundingCheck.ScopedLinksConsistent == wantConsistent
			if mode == "init" {
				linksMatch = linksMatch && fundingCheck.Mismatches.ApprovedCreditMissingReceipt == 1 && fundingCheck.Mismatches.OrphanManualReceipts == 1
			}
			if err != nil || fundingCheck.Scope != "manual_voucher_receipt_links" || !linksMatch || fundingCheck.Scanned.Receipts < 3 || fundingCheck.Scanned.Credits < 2 || fundingCheck.Scanned.Redemptions != 1 || fundingCheck.UncoveredSandboxReceipts != 0 {
				t.Fatal("manual funding local relation facts were not retained")
			}
			if err := ledger.Verify(permit); err != nil || budget.Verify(permit) != nil || fundingLinks.Verify(permit) != nil {
				t.Fatal("all real consumers must share the original permit")
			}
			if _, err := permit.BeginScan(ctx, maintenancetrust.ScanFunding); err == nil {
				t.Fatal("the same Permit accepted ScanFunding twice")
			}
			wrong := &maintenancetrust.Permit{}
			if ledger.Verify(wrong) == nil || budget.Verify(wrong) == nil || fundingLinks.Verify(wrong) == nil {
				t.Fatal("consumer accepted an unrelated permit")
			}
			ledgerCopy, budgetCopy, fundingLinksCopy := *ledger, *budget, *fundingLinks
			if snapshot.Rollback(ctx) != nil {
				t.Fatal("real shared snapshot rollback failed")
			}
			if ledger.Verify(permit) == nil || budget.Verify(permit) == nil || fundingLinks.Verify(permit) == nil || ledgerCopy.Verify(permit) == nil || budgetCopy.Verify(permit) == nil || fundingLinksCopy.Verify(permit) == nil {
				t.Fatal("rollback did not revoke every original consumer result")
			}
			if g.Close(ctx) != nil {
				t.Fatal("original maintenance connection close failed")
			}
			f.awaitOwnedGone(t)
			fundingAfterScan, err := f.env.FundingSnapshot(ctx)
			if err != nil || fundingAfterScan != fundingBeforeScan || f.env.VerifyLegacyFundingUnchanged(ctx) != nil {
				t.Fatal("maintenance scans changed genuine funded data")
			}
		})
	}
}

func seedConsumerFundingSources(t *testing.T, f *consumerFixture, ctx context.Context) string {
	t.Helper()
	app, err := f.env.OpenPool(ctx, "app")
	if err != nil {
		t.Fatal("consumer voucher app pool failed")
	}
	f.pools = append(f.pools, app)
	store := billing.New(app)
	credit, err := store.CreateCredit(ctx, billing.CreditInput{
		TenantID: f.env.ApplicationTenantID, ActorID: f.env.ActorIDs["finance_operator"], Amount: 19,
		BusinessRef: "bank:maintenance-qa:" + uuid.NewString(), ProofRef: "proof:maintenance-qa:verified",
		IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString(),
	})
	if err != nil {
		t.Fatal("consumer manual credit creation failed", err)
	}
	creditApproved, err := store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.env.ApplicationTenantID, ActorID: f.env.ActorIDs["finance_approver"], OrderID: credit.ID})
	if err != nil || creditApproved.State != "approved" || creditApproved.JournalID == "" {
		t.Fatal("consumer manual credit approval failed", err)
	}
	keys, err := security.ParseVoucherKeyring(`{"v1":"`+base64.RawURLEncoding.EncodeToString(make([]byte, 32))+`"}`, "v1")
	if err != nil {
		t.Fatal("consumer fixed voucher keyring failed")
	}
	service, err := billing.NewVoucherService(ctx, billing.New(app), billing.VoucherOptions{RuntimeAllowed: true, Keys: keys})
	if err != nil {
		t.Fatal("consumer voucher service failed")
	}
	command := func(actor string) billing.VoucherCommandContext {
		return billing.VoucherCommandContext{OperationID: uuid.NewString(), ActorID: actor, RequestID: uuid.NewString()}
	}
	if _, err = service.UpdateVoucherPolicy(ctx, billing.UpdateVoucherPolicyInput{ActorID: f.env.ActorIDs["admin"], ExpectedRevision: 1, Enabled: true}); err != nil {
		t.Fatal("consumer voucher policy enable failed", err)
	}
	issued, err := service.CreateVoucherBatch(ctx, billing.CreateVoucherBatchInput{
		Command: command(f.env.ActorIDs["finance_operator"]), FaceAmount: 17, Count: 1,
		ExpiresAt: time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond), PurposeRef: "grant:maintenance-qa", Reason: "maintenance source-link fixture",
	})
	if err != nil || !issued.Created || len(issued.Codes) != 1 || issued.Batch == nil {
		t.Fatal("consumer voucher issue failed", err)
	}
	voucherApproved, err := service.ApproveVoucherBatch(ctx, billing.ApproveVoucherBatchInput{Command: command(f.env.ActorIDs["finance_approver"]), BatchID: issued.Batch.ID, ExpectedRevision: 1})
	if err != nil || voucherApproved.Status != "accepted" {
		t.Fatal("consumer voucher approval failed", err)
	}
	redeem := command(f.env.ActorIDs["owner"])
	redeem.TenantID = f.env.ApplicationTenantID
	redemption, err := service.RedeemVoucher(ctx, billing.RedeemVoucherInput{Command: redeem, Code: issued.Codes[0].Code})
	if err != nil || redemption.Status != "accepted" || redemption.FundingReceiptID == nil || redemption.JournalID == nil {
		t.Fatalf("consumer voucher redemption failed: status=%s reason=%s error=%v", redemption.Status, redemption.Reason, err)
	}
	return credit.ID
}

func breakConsumerManualReceiptLink(t *testing.T, f *consumerFixture, ctx context.Context, creditID string) {
	t.Helper()
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		t.Fatal("consumer mismatch fixture transaction failed")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SET LOCAL session_replication_role='replica'"); err != nil {
		t.Fatal("consumer mismatch fixture isolation failed", err)
	}
	schema := pgx.Identifier{f.env.Schemas["app"]}.Sanitize()
	if _, err = tx.Exec(ctx, "UPDATE "+schema+".funding_receipts SET source_id=$1 WHERE source_kind='manual_credit' AND source_id=$2", "unlinked-"+uuid.NewString(), creditID); err != nil {
		t.Fatal("consumer mismatch fixture source adjustment failed", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("consumer mismatch fixture commit failed", err)
	}
}

type consumerBackend struct {
	pid   int32
	start time.Time
}
type consumerFixture struct {
	env                   *paymentsandboxqa.ApplicationEnvironment
	admin                 *pgx.Conn
	cfg                   *pgx.ConnConfig
	pins                  fundinglotcatalog.Pins
	sharedBefore          string
	tag                   string
	fundingSourceCreditID string
	gates                 []*maintenancetrust.Gate
	pools                 []*pgxpool.Pool
	backends              []consumerBackend
}

func consumerConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	raw := os.Getenv("C0_MAINTENANCE_DATABASE_URL")
	if raw == "" {
		t.Skip("C0_MAINTENANCE_DATABASE_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host != "127.0.0.1:55439" || u.Path != "/moni_test" || u.User == nil || u.User.Username() != "moni_test" || u.Fragment != "" {
		t.Fatal("consumer dedicated test endpoint rejected")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		t.Fatal("consumer test query rejected")
	}
	for key, values := range q {
		if key != "sslmode" || len(values) != 1 || values[0] != "disable" {
			t.Fatal("consumer test query rejected")
		}
	}
	cfg, err := pgx.ParseConfig(raw)
	if err != nil || cfg.Host != "127.0.0.1" || cfg.Port != 55439 || cfg.Database != "moni_test" || cfg.User != "moni_test" || len(cfg.Fallbacks) != 0 || cfg.TLSConfig != nil {
		t.Fatal("consumer parsed test endpoint rejected")
	}
	cfg.ConnectTimeout = 3 * time.Second
	cfg.RuntimeParams = map[string]string{"application_name": "c0_consumer_admin", "search_path": "pg_catalog"}
	return cfg
}

func consumerFixedSource(t *testing.T, name, want string) []byte {
	t.Helper()
	_, path, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("consumer repository location unavailable")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(path), "../..", name))
	sum := sha256.Sum256(body)
	if err != nil || hex.EncodeToString(sum[:]) != want {
		t.Fatal("consumer frozen fixture source changed: " + name)
	}
	return body
}

func newConsumerFixture(t *testing.T) *consumerFixture {
	t.Helper()
	cfg := consumerConfig(t)
	for _, pin := range []struct{ name, sha string }{
		{"internal/paymentsandboxqa/install.go", "dd0ac0d67bbe9b6e400f11ccff14479a9c156779b5fc2079ee24c7c830e18888"},
		{"internal/paymentsandboxqa/application.go", "b0d28048b9fa3d856b83e494d319158ad4c3937eb745dde88674536496254f33"},
		{"internal/paymentsandboxschema/mock_v1.sql", "3942a0af2577bdad4e841f2b1fa3f0502a3a0db898d6f050fbaf2da45e7e0127"},
		{"internal/paymentsandboxschema/receiver_v1.sql", "3c1a4956b55ef058ad6a927e678bf026017a4f3d23bc60d19cb02640e98f433e"},
		{"internal/paymentsandboxschema/mock_inventory.json", "3f5e926b8ed9152d6bf9ff757400ab3119241629cb3b00cdbd40974d9f818234"},
		{"internal/paymentsandboxschema/receiver_inventory.json", "73be5ea3231cd8eb3504d1e1b8b269fab6b92f10be6b8128e7feafa569b662d8"},
	} {
		consumerFixedSource(t, pin.name, pin.sha)
	}
	ddl := consumerFixedSource(t, "specs/022-self-service-payments/c0-production-schema.sql", "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047")
	acl := consumerFixedSource(t, "specs/022-self-service-payments/c0-production-permissions.sql", "9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f := &consumerFixture{}
	var err error
	f.admin, err = pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("consumer actual installer connection failed")
	}
	t.Cleanup(func() { f.close(t) })
	var version int
	var actual bool
	if f.admin.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer,current_user=session_user AND current_user='moni_test' AND r.rolsuper AND r.rolcanlogin AND d.datdba=r.oid FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_database d ON d.datname=current_database() WHERE r.rolname=current_user").Scan(&version, &actual) != nil || version/10000 != 16 || !actual {
		t.Fatal("consumer actual PG16 installer LOGIN rejected")
	}
	if f.admin.QueryRow(ctx, consumerSharedSQL).Scan(&f.sharedBefore) != nil {
		t.Fatal("consumer shared catalog baseline failed")
	}
	u := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(cfg.User, cfg.Password), RawQuery: "sslmode=disable"}
	f.env, err = paymentsandboxqa.CreateApplication(ctx, u.String(), t.TempDir())
	if err != nil || f.env == nil {
		t.Fatal("consumer genuine funded v1 install failed")
	}
	if f.env.VerifyFundsUnchanged(ctx) != nil {
		t.Fatal("consumer genuine payment funding changed before voucher source setup")
	}
	f.fundingSourceCreditID = seedConsumerFundingSources(t, f, ctx)
	f.loadPins(t, ctx)
	owner, err := f.env.OpenPool(ctx, "app_owner")
	if err != nil {
		t.Fatal("consumer offline owner pool failed")
	}
	f.pools = append(f.pools, owner)
	var current, session string
	var pid int32
	if owner.QueryRow(ctx, "SELECT current_user::text,session_user::text,pg_backend_pid()").Scan(&current, &session, &pid) != nil || current != f.env.Roles["app_owner"] || session != "moni_test" || owner.Config().ConnConfig.User != "moni_test" {
		t.Fatal("consumer original SET ROLE owner identity failed")
	}
	f.observePID(t, ctx, pid)
	if _, err = owner.Exec(ctx, string(ddl)); err != nil {
		t.Fatal("consumer frozen C0 DDL failed")
	}
	text := string(acl)
	for _, entry := range []struct{ key, role string }{{"fl_app", "app_runtime"}, {"fl_worker", "app_worker"}, {"fl_projector", "app_projector"}} {
		text = strings.ReplaceAll(text, ":\""+entry.key+"\"", pgx.Identifier{f.env.Roles[entry.role]}.Sanitize())
	}
	if strings.Contains(text, ":\"") {
		t.Fatal("consumer unresolved frozen ACL")
	}
	if _, err = owner.Exec(ctx, text); err != nil {
		t.Fatal("consumer frozen C0 ACL failed")
	}
	public, err := periodicbudget.NewVerifier(owner, periodicbudget.VerifyOptions{})
	if err != nil {
		t.Fatal("consumer ordinary periodic verifier construction failed")
	}
	if _, err = public.Verify(ctx); !errors.Is(err, periodicbudget.ErrVerifyAuthority) {
		t.Fatal("ordinary verifier accepted SET ROLE instead of actual LOGIN")
	}
	owner.Close()
	f.awaitOwnedGone(t)
	f.cfg = cfg.Copy()
	f.tag = "c0_consumer_" + f.env.Schemas["app"]
	if len(f.tag) > 63 {
		f.tag = f.tag[:63]
	}
	f.cfg.RuntimeParams = map[string]string{"application_name": f.tag, "search_path": pgx.Identifier{f.env.Schemas["app"]}.Sanitize() + ",pg_catalog,pg_temp"}
	f.cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return f
}

func (f *consumerFixture) loadPins(t *testing.T, ctx context.Context) {
	t.Helper()
	var db uint32
	if f.admin.QueryRow(ctx, "SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()").Scan(&db) != nil {
		t.Fatal("consumer database pin failed")
	}
	role := func(key string, login bool) fundinglotcatalog.RolePin {
		r := fundinglotcatalog.RolePin{Name: f.env.Roles[key]}
		var actual bool
		if r.Name == "" || f.admin.QueryRow(ctx, "SELECT oid,rolcanlogin FROM pg_catalog.pg_roles WHERE rolname=$1", r.Name).Scan(&r.OID, &actual) != nil || r.OID == 0 || actual != login {
			t.Fatal("consumer actual role pin failed")
		}
		return r
	}
	schema := func(key string) fundinglotcatalog.SchemaPin {
		o := role(key+"_owner", false)
		s := fundinglotcatalog.SchemaPin{Name: f.env.Schemas[key], NamespaceOwner: o, ObjectOwner: o}
		var owner uint32
		if f.admin.QueryRow(ctx, "SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1", s.Name).Scan(&s.OID, &owner) != nil || s.OID == 0 || owner != o.OID {
			t.Fatal("consumer actual schema pin failed")
		}
		return s
	}
	w := role("app_worker", true)
	f.pins = fundinglotcatalog.Pins{Mode: fundinglotcatalog.Sandbox, Database: fundinglotcatalog.DatabasePin{Host: "127.0.0.1", Port: 55439, Name: "moni_test", OID: db}, AppSchema: schema("app"), App: role("app_runtime", true), Projector: role("app_projector", true), Worker: &w, Sandbox: &fundinglotcatalog.SandboxPins{Mock: schema("mock"), MockRuntime: role("mock_runtime", true), Receiver: schema("receiver"), ReceiverRuntime: role("receiver_runtime", true)}}
}

func (f *consumerFixture) observePID(t *testing.T, ctx context.Context, pid int32) {
	t.Helper()
	b := consumerBackend{pid: pid}
	if f.admin.QueryRow(ctx, "SELECT backend_start FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND datid=$2 AND usename='moni_test'", pid, f.pins.Database.OID).Scan(&b.start) != nil || b.start.IsZero() {
		t.Fatal("consumer exact backend observation failed")
	}
	f.backends = append(f.backends, b)
}
func (f *consumerFixture) observeMaintenance(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := f.admin.Query(ctx, "SELECT pid,backend_start FROM pg_catalog.pg_stat_activity WHERE datid=$1 AND usename='moni_test' AND application_name=$2", f.pins.Database.OID, f.tag)
	if err != nil {
		t.Fatal("consumer maintenance backend observation failed")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var b consumerBackend
		if rows.Scan(&b.pid, &b.start) != nil || b.pid == 0 || b.start.IsZero() || b.pid == int32(f.admin.PgConn().PID()) {
			t.Fatal("consumer independent maintenance identity failed")
		}
		f.backends = append(f.backends, b)
		count++
	}
	if rows.Err() != nil || count != 1 {
		t.Fatal("consumer expected one original maintenance backend")
	}
}
func (f *consumerFixture) ownedGone(ctx context.Context) error {
	for ctx.Err() == nil {
		alive := false
		for _, b := range f.backends {
			var present bool
			if err := f.admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND datid=$3) OR EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND locktype='advisory')", b.pid, b.start, f.pins.Database.OID).Scan(&present); err != nil {
				return err
			}
			alive = alive || present
		}
		if f.env != nil {
			roles := []string{f.env.Roles["app_runtime"], f.env.Roles["app_worker"], f.env.Roles["app_projector"], f.env.Roles["mock_runtime"], f.env.Roles["receiver_runtime"]}
			var present bool
			if err := f.admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datid=$1 AND usename=ANY($2::text[]))", f.pins.Database.OID, roles).Scan(&present); err != nil {
				return err
			}
			alive = alive || present
		}
		if !alive {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ctx.Err()
}
func (f *consumerFixture) awaitOwnedGone(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if f.ownedGone(ctx) != nil {
		t.Fatal("consumer exact owned backend/lock cleanup failed")
	}
}

const consumerSharedSQL = "SELECT n.nspowner::text || '/' || COALESCE(n.nspacl::text,'NULL') || '/' || (SELECT count(*)::text FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid) || '/' || COALESCE(d.datacl::text,'NULL') FROM pg_catalog.pg_namespace n,pg_catalog.pg_database d WHERE n.nspname='public' AND d.datname=current_database()"

func (f *consumerFixture) close(t *testing.T) {
	t.Helper()
	if f.admin == nil {
		return
	}
	for _, g := range f.gates {
		_ = g.Close(nil)
	}
	for _, p := range f.pools {
		p.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if f.env != nil {
		if f.ownedGone(ctx) != nil {
			t.Error("consumer sessions survived; exact fixture retained for diagnosis")
		} else {
			if f.env.Close(ctx) != nil {
				t.Error("consumer exact fixture cleanup failed")
			}
			for _, name := range f.env.Schemas {
				var exists bool
				if f.admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)", name).Scan(&exists) != nil || exists {
					t.Error("consumer exact schema survived cleanup")
				}
			}
			for _, name := range f.env.Roles {
				var exists bool
				if f.admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=$1)", name).Scan(&exists) != nil || exists {
					t.Error("consumer exact role survived cleanup")
				}
			}
		}
	}
	var after string
	if f.sharedBefore != "" && (f.admin.QueryRow(ctx, consumerSharedSQL).Scan(&after) != nil || after != f.sharedBefore) {
		t.Error("consumer changed shared public/database catalog")
	}
	if f.admin.Close(ctx) != nil {
		t.Error("consumer administrator close failed")
	}
}
