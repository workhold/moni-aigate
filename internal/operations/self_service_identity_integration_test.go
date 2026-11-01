package operations

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

// These identity/maintenance tests may only create fixtures in the dedicated
// local test database. In particular, a review instance URL is never accepted.
func selfServiceTestDatabase(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated self-service identity PostgreSQL tests")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("self-service identity tests require the dedicated local moni_test database on port 55439")
	}
	return raw
}

func selfServiceOperationsDB(t *testing.T) *operationFixture {
	t.Helper()
	selfServiceTestDatabase(t)
	return operationDB(t)
}

// Applying the pre-016 SQL directly lets this test inspect the actual upgrade
// from existing users, rather than inserting legacy rows after the migration.
func selfServicePreMigrationDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	raw := selfServiceTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	schema := "identity_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		cancel()
		t.Fatal(err)
	}
	cfg := admin.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner.Close()
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("identity upgrade schema cleanup failed: %v", err)
		}
		admin.Close()
		cancel()
	})
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "016" {
			break
		}
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = owner.Exec(ctx, string(body)); err != nil {
			t.Fatalf("pre-identity migration %s: %v", name, err)
		}
	}
	return ctx, owner
}

func TestSelfServiceIdentityMigrationBackfillsOnlyExistingAdmin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roles []string
		want  bool
	}{
		{name: "empty"},
		{name: "ordinary_and_finance", roles: []string{"none", "finance_operator", "finance_approver"}},
		{name: "existing_admin", roles: []string{"none", "admin"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, owner := selfServicePreMigrationDB(t)
			adminID := ""
			for _, role := range tc.roles {
				id := uuid.NewString()
				if _, err := owner.Exec(ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'legacy-password',$3)`, id, id+"@legacy.invalid", role); err != nil {
					t.Fatal(err)
				}
				if role == "admin" {
					adminID = id
				}
			}
			body, err := migrations.Files.ReadFile("016_self_service_identity.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = owner.Exec(ctx, string(body)); err != nil {
				t.Fatal(err)
			}
			var count, changed int
			if err = owner.QueryRow(ctx, `SELECT count(*) FROM identity_initialization`).Scan(&count); err != nil || (count == 1) != tc.want || count > 1 {
				t.Fatalf("initialization marker count=%d expected=%v err=%v", count, tc.want, err)
			}
			if err = owner.QueryRow(ctx, `SELECT count(*) FROM users WHERE email_verified_at IS NOT NULL OR password_revision<>1 OR password_hash<>'legacy-password'`).Scan(&changed); err != nil || changed != 0 {
				t.Fatalf("upgrade changed legacy proof/password state: %d %v", changed, err)
			}
			if tc.want {
				var actor string
				var at time.Time
				if err = owner.QueryRow(ctx, `SELECT initialized_by,initialized_at FROM identity_initialization WHERE singleton`).Scan(&actor, &at); err != nil || actor != adminID || at.IsZero() {
					t.Fatalf("marker did not identify existing admin: %s %v", actor, err)
				}
				if _, err = owner.Exec(ctx, `UPDATE users SET platform_role='none',status='suspended' WHERE id=$1`, adminID); err != nil {
					t.Fatal(err)
				}
				if err = owner.QueryRow(ctx, `SELECT count(*) FROM identity_initialization`).Scan(&count); err != nil || count != 1 {
					t.Fatal("historical marker changed after administrator revocation", count, err)
				}
			}
		})
	}
}

func TestSelfServiceIdentityPasswordRevisionAndInitializationAreProtected(t *testing.T) {
	f := selfServiceOperationsDB(t)
	var revision int64
	for _, statement := range []string{
		`UPDATE users SET password_revision=999 WHERE id=$1 RETURNING password_revision`,
		`UPDATE users SET password_hash=password_hash,password_revision=0 WHERE id=$1 RETURNING password_revision`,
		`UPDATE users SET mfa_last_step=10,email_verified_at=clock_timestamp(),password_revision=999 WHERE id=$1 RETURNING password_revision`,
	} {
		if err := f.app.QueryRow(f.ctx, statement, f.user).Scan(&revision); err != nil || revision != 1 {
			t.Fatalf("non-password mutation forged revision: %d %v", revision, err)
		}
	}
	if err := f.app.QueryRow(f.ctx, `UPDATE users SET password_hash='new-hash',password_revision=999 WHERE id=$1 RETURNING password_revision`, f.user).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("password change did not advance exactly once: %d %v", revision, err)
	}
	if err := f.owner.QueryRow(f.ctx, `UPDATE users SET password_revision=1 WHERE id=$1 RETURNING password_revision`, f.user).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("owner forged password revision: %d %v", revision, err)
	}
	admin := uuid.NewString()
	if err := f.app.QueryRow(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role,password_revision) VALUES($1,$2,'admin-hash','admin',999) RETURNING password_revision`, admin, admin+"@identity.invalid").Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("insert did not force initial password revision: %d %v", revision, err)
	}
	for _, actor := range []string{f.user, admin} {
		if _, err := f.app.Exec(f.ctx, `INSERT INTO identity_initialization(initialized_by) VALUES($1)`, actor); err == nil {
			t.Fatal("app forged initialization outside the one-user bootstrap")
		}
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO identity_initialization(initialized_by) VALUES($1)`, admin); err != nil {
		t.Fatal("owner could not record existing administrator initialization", err)
	}
	for _, pool := range []*pgxpool.Pool{f.app, f.owner} {
		for _, statement := range []string{`UPDATE identity_initialization SET initialized_at=clock_timestamp()`, `DELETE FROM identity_initialization`} {
			if _, err := pool.Exec(f.ctx, statement); err == nil {
				t.Fatal("initialization marker was mutable", statement)
			}
		}
	}
}

func TestSelfServiceIdentityProvisionedBootstrapMarkerIsAtomic(t *testing.T) {
	for _, rejectAudit := range []bool{false, true} {
		name := "bootstrap"
		if rejectAudit {
			name = "audit_failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx, owner := selfServicePreMigrationDB(t)
			body, err := migrations.Files.ReadFile("016_self_service_identity.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = owner.Exec(ctx, string(body)); err != nil {
				t.Fatal(err)
			}
			// ProvisionFor grants the current serving schema. Preserve this
			// staged 016 bootstrap assertion while adding the current 017–019 tables.
			funding, err := migrations.Files.ReadFile("017_funding_vouchers.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = owner.Exec(ctx, string(funding)); err != nil {
				t.Fatal(err)
			}
			pricing, err := migrations.Files.ReadFile("018_tenant_pricing.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = owner.Exec(ctx, string(pricing)); err != nil {
				t.Fatal(err)
			}
			alerts, err := migrations.Files.ReadFile("019_operations_alerts.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = owner.Exec(ctx, string(alerts)); err != nil {
				t.Fatal(err)
			}
			// ProvisionFor protects the migration ledger even in this staged
			// upgrade fixture, which otherwise applies the migration SQL directly.
			if _, err = owner.Exec(ctx, `CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`); err != nil {
				t.Fatal(err)
			}
			var schema string
			if err = owner.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
				t.Fatal(err)
			}
			suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			appRole, projectionRole := "identity_app_"+suffix, "identity_projection_"+suffix
			var app *pgxpool.Pool
			t.Cleanup(func() {
				if app != nil {
					app.Close()
				}
				for _, role := range []string{appRole, projectionRole} {
					if _, err := owner.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); err != nil {
						t.Errorf("bootstrap fixture role grants cleanup failed: %v", err)
					}
					if _, err := owner.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
						t.Errorf("bootstrap fixture role cleanup failed: %v", err)
					}
				}
			})
			for _, role := range []string{appRole, projectionRole} {
				if _, err = owner.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN NOINHERIT NOSUPERUSER"); err != nil {
					t.Fatal(err)
				}
			}
			if err = ProvisionFor(ctx, owner, schema, appRole, projectionRole); err != nil {
				t.Fatal(err)
			}
			cfg := owner.Config()
			cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
				_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{appRole}.Sanitize())
				return err
			}
			app, err = pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if rejectAudit {
				if _, err = owner.Exec(ctx, `CREATE FUNCTION identity_test_reject_bootstrap_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='bootstrap' THEN RAISE EXCEPTION 'fixture bootstrap audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER identity_test_reject_bootstrap_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION identity_test_reject_bootstrap_audit()`); err != nil {
					t.Fatal(err)
				}
			}
			err = identity.New(app, nil).Bootstrap(ctx, "bootstrap@identity.invalid", "isolated-bootstrap-password")
			if rejectAudit && err == nil || !rejectAudit && err != nil {
				t.Fatalf("unexpected bootstrap outcome: rejectAudit=%v err=%v", rejectAudit, err)
			}
			var users, markers int
			if err = owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM identity_initialization)`).Scan(&users, &markers); err != nil {
				t.Fatal(err)
			}
			want := 1
			if rejectAudit {
				want = 0
			}
			if users != want || markers != want {
				t.Fatalf("bootstrap user/marker were not atomic: users=%d markers=%d want=%d", users, markers, want)
			}
			if !rejectAudit {
				var revision int64
				var verified *time.Time
				if err = app.QueryRow(ctx, `SELECT u.password_revision,u.email_verified_at FROM identity_initialization i JOIN users u ON u.id=i.initialized_by WHERE u.platform_role='admin' AND u.status='active'`).Scan(&revision, &verified); err != nil || revision != 1 || verified != nil {
					t.Fatal("bootstrap marker did not point to the new ordinary admin proof state", revision, verified, err)
				}
				if err = identity.New(app, nil).Bootstrap(ctx, "second@identity.invalid", "another-bootstrap-password"); !errors.Is(err, identity.ErrConflict) {
					t.Fatal("bootstrap could be repeated", err)
				}
			}
		})
	}
}

type selfServiceEncryptedFixture struct {
	subject, challenge, outbox string
	email, payload             string
	emailCipher, payloadCipher string
}

func seedSelfServiceEncryptedFixture(t *testing.T, f *operationFixture) selfServiceEncryptedFixture {
	t.Helper()
	v := selfServiceEncryptedFixture{subject: uuid.NewString(), challenge: uuid.NewString(), outbox: uuid.NewString(), email: "fixture@identity.invalid"}
	body, err := json.Marshal(map[string]any{"v": 1, "to": v.email, "purpose": "registration", "link": "https://identity.invalid/register/complete#token=ic1_" + security.Random(32)})
	if err != nil {
		t.Fatal(err)
	}
	v.payload = string(body)
	v.emailCipher, err = f.secrets.SealFor(v.email, "identity-registration", v.challenge)
	if err != nil {
		t.Fatal(err)
	}
	v.payloadCipher, err = f.secrets.SealFor(v.payload, "identity-mail-outbox", v.outbox)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO identity_mail_subjects(id,purpose,email_digest,window_count) VALUES($1,'registration',$2,1)`, v.subject, f.secrets.Digest(v.subject))
	f.exec(t, `INSERT INTO identity_challenges(id,subject_id,purpose,token_digest,registration_email_ciphertext,expires_at) VALUES($1,$2,'registration',$3,$4,clock_timestamp()+interval '30 minutes')`, v.challenge, v.subject, f.secrets.Digest(v.challenge), v.emailCipher)
	f.exec(t, `INSERT INTO identity_mail_outbox(id,challenge_id,payload_ciphertext) VALUES($1,$2,$3)`, v.outbox, v.challenge, v.payloadCipher)
	return v
}

func TestSelfServiceIdentityMinimalRolesAndImmutableProofLifecycle(t *testing.T) {
	f := selfServiceOperationsDB(t)
	v := seedSelfServiceEncryptedFixture(t, f)
	for _, statement := range []string{
		`SELECT * FROM identity_initialization`, `SELECT * FROM identity_mail_subjects`, `SELECT * FROM identity_challenges`, `SELECT * FROM identity_mail_outbox`,
		`SELECT email_verified_at,password_revision FROM users`,
	} {
		_, err := f.projection.Exec(f.ctx, statement)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatalf("projector gained identity data access: %v", err)
		}
	}
	for _, statement := range []string{
		`UPDATE identity_mail_subjects SET email_digest=repeat('a',64) WHERE id=$1`,
		`UPDATE identity_mail_subjects SET window_count=0 WHERE id=$1`,
		`DELETE FROM identity_mail_subjects WHERE id=$1`,
	} {
		if _, err := f.app.Exec(f.ctx, statement, v.subject); err == nil {
			t.Fatal("app rewrote active mail subject", statement)
		}
	}
	for _, statement := range []string{
		`UPDATE identity_challenges SET token_digest=repeat('b',64) WHERE id=$1`,
		`UPDATE identity_challenges SET expires_at=expires_at+interval '1 minute' WHERE id=$1`,
		`UPDATE identity_challenges SET registration_email_ciphertext=NULL WHERE id=$1`,
		`UPDATE identity_challenges SET registration_email_ciphertext='replacement' WHERE id=$1`,
		`DELETE FROM identity_challenges WHERE id=$1`,
	} {
		if _, err := f.app.Exec(f.ctx, statement, v.challenge); err == nil {
			t.Fatal("app changed an active proof or secret", statement)
		}
	}
	// session_user is the test owner, but SET ROLE must not gain its rewrap
	// exception; an arbitrary GUC must not bypass the current_user check either.
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `SET LOCAL moni.identity_rewrap='true'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE identity_mail_outbox SET payload_ciphertext='replacement' WHERE id=$1`, v.outbox); err == nil {
		t.Fatal("app used session owner/GUC as a maintenance bypass")
	}
	_ = tx.Rollback(f.ctx)
	for _, statement := range []string{
		`UPDATE identity_mail_outbox SET payload_ciphertext='replacement' WHERE id=$1`,
		`UPDATE identity_mail_outbox SET attempt_count=1,claim_generation=1 WHERE id=$1`,
		`DELETE FROM identity_mail_outbox WHERE id=$1`,
	} {
		if _, err := f.app.Exec(f.ctx, statement, v.outbox); err == nil {
			t.Fatal("app rewrote an outbox identity/state", statement)
		}
	}
	f.exec(t, `UPDATE identity_mail_outbox SET state='sending',attempt_count=1,claim_generation=1,lease_until=clock_timestamp()+interval '45 seconds' WHERE id=$1`, v.outbox)
	f.exec(t, `UPDATE identity_mail_outbox SET state='pending',lease_until=NULL,next_attempt_at=clock_timestamp()+interval '5 seconds',last_error_code='mail_network' WHERE id=$1`, v.outbox)
	f.exec(t, `UPDATE identity_mail_outbox SET state='sending',attempt_count=2,claim_generation=2,lease_until=clock_timestamp()+interval '45 seconds' WHERE id=$1`, v.outbox)
	f.exec(t, `UPDATE identity_mail_outbox SET state='sent',payload_ciphertext=NULL,lease_until=NULL,sent_at=clock_timestamp(),last_error_code='' WHERE id=$1`, v.outbox)
	f.exec(t, `UPDATE identity_challenges SET consumed_at=clock_timestamp(),registration_email_ciphertext=NULL WHERE id=$1`, v.challenge)
	for _, pool := range []*pgxpool.Pool{f.app, f.owner} {
		for _, statement := range []string{
			`UPDATE identity_challenges SET consumed_at=NULL WHERE id=$1`,
			`UPDATE identity_challenges SET registration_email_ciphertext='restored' WHERE id=$1`,
			`DELETE FROM identity_challenges WHERE id=$1`,
		} {
			if _, err := pool.Exec(f.ctx, statement, v.challenge); err == nil {
				t.Fatal("terminal proof could be restored/deleted early", statement)
			}
		}
		for _, statement := range []string{
			`UPDATE identity_mail_outbox SET state='pending',payload_ciphertext='restored',sent_at=NULL WHERE id=$1`,
			`UPDATE identity_mail_outbox SET payload_ciphertext='restored' WHERE id=$1`,
			`DELETE FROM identity_mail_outbox WHERE id=$1`,
		} {
			if _, err := pool.Exec(f.ctx, statement, v.outbox); err == nil {
				t.Fatal("terminal outbox could be revived/deleted early", statement)
			}
		}
	}
}

func TestSelfServiceIdentityAppCannotAdvanceMailWindowOrSkipCooldown(t *testing.T) {
	f := selfServiceOperationsDB(t)
	v := seedSelfServiceEncryptedFixture(t, f)
	var window, next, seen time.Time
	var count int
	read := func() {
		t.Helper()
		if err := f.app.QueryRow(f.ctx, `SELECT window_started_at,window_count,next_issue_at,last_seen_at FROM identity_mail_subjects WHERE id=$1`, v.subject).Scan(&window, &count, &next, &seen); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if count != 1 || next.Sub(seen) != time.Minute || window.After(seen) {
		t.Fatalf("initial counted issuance did not get DB cooldown: count=%d next-seen=%s", count, next.Sub(seen))
	}
	beforeWindow, beforeNext, beforeSeen := window, next, seen
	for _, statement := range []string{
		`UPDATE identity_mail_subjects SET window_started_at=window_started_at+interval '1 hour',window_count=1 WHERE id=$1`,
		`UPDATE identity_mail_subjects SET window_started_at=clock_timestamp()+interval '2 hours',window_count=1 WHERE id=$1`,
		`UPDATE identity_mail_subjects SET window_count=window_count+1 WHERE id=$1`,
		`UPDATE identity_mail_subjects SET window_count=window_count+1,next_issue_at=clock_timestamp()+interval '1 year' WHERE id=$1`,
	} {
		if _, err := f.app.Exec(f.ctx, statement, v.subject); err == nil {
			t.Fatal("app advanced its persistent issuance window/counter early", statement)
		}
		read()
		if count != 1 || !window.Equal(beforeWindow) || !next.Equal(beforeNext) || !seen.Equal(beforeSeen) {
			t.Fatal("rejected issuance update partially changed persistent cooldown")
		}
	}
	for _, statement := range []string{
		`INSERT INTO identity_mail_subjects(purpose,email_digest,window_count) VALUES('registration',$1,2)`,
		`INSERT INTO identity_mail_subjects(purpose,email_digest,window_started_at) VALUES('registration',$1,clock_timestamp()+interval '1 hour')`,
		`INSERT INTO identity_mail_subjects(purpose,email_digest,last_seen_at) VALUES('registration',$1,clock_timestamp()+interval '1 hour')`,
	} {
		if _, err := f.app.Exec(f.ctx, statement, f.secrets.Digest(uuid.NewString())); err == nil {
			t.Fatal("app inserted forged initial mail frequency state", statement)
		}
	}
	var initialAt time.Time
	if err := f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&initialAt); err != nil {
		t.Fatal(err)
	}
	var emptyWindow time.Time
	var emptyCount int
	if err := f.app.QueryRow(f.ctx, `INSERT INTO identity_mail_subjects(purpose,email_digest,window_started_at,window_count) VALUES('registration',$1,clock_timestamp()-interval '2 days',0) RETURNING window_started_at,window_count`, f.secrets.Digest(uuid.NewString())).Scan(&emptyWindow, &emptyCount); err != nil || emptyCount != 0 || emptyWindow.Before(initialAt) {
		t.Fatal("unissued subject kept a forged ancient counter window", emptyCount, err)
	}
	// Only this isolated fixture's owner moves the clock state backwards, with
	// its guard disabled transactionally. Production app updates cannot do so.
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `ALTER TABLE identity_mail_subjects DISABLE TRIGGER identity_subject_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE identity_mail_subjects SET window_started_at=clock_timestamp()-interval '2 hours',window_count=5,next_issue_at=clock_timestamp()-interval '1 second' WHERE id=$1`, v.subject); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `ALTER TABLE identity_mail_subjects ENABLE TRIGGER identity_subject_guard`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var transitionAt time.Time
	if err = f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&transitionAt); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE identity_mail_subjects SET window_started_at=window_started_at+interval '1 hour',window_count=1,next_issue_at=clock_timestamp()+interval '1 year',last_seen_at=clock_timestamp() WHERE id=$1`, v.subject); err != nil {
		t.Fatal("eligible app window transition failed", err)
	}
	read()
	var now time.Time
	if err = f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil || count != 1 || next.Sub(seen) != time.Minute || window.Before(transitionAt) || window.After(seen) || seen.After(now) {
		t.Fatal("DB did not own the exact new window cooldown", count, next.Sub(seen), err)
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE identity_mail_subjects SET window_count=2 WHERE id=$1`, v.subject); err == nil {
		t.Fatal("app immediately consumed a second slot in the fresh cooldown")
	}
}

func TestSelfServiceIdentityOwnerRewrapPreservesBothDomainsAndRollsBack(t *testing.T) {
	f := selfServiceOperationsDB(t)
	v := seedSelfServiceEncryptedFixture(t, f)
	var beforeProvider string
	if err := f.app.QueryRow(f.ctx, `SELECT secret_ciphertext FROM channel_credentials WHERE channel_id=$1`, f.channel).Scan(&beforeProvider); err != nil {
		t.Fatal(err)
	}
	f.secrets.Active = "new"
	if err := Rewrap(f.ctx, f.app, f.secrets); err == nil {
		t.Fatal("online application role ran owner-only rewrap")
	}
	var afterRejected string
	if err := f.app.QueryRow(f.ctx, `SELECT secret_ciphertext FROM channel_credentials WHERE channel_id=$1`, f.channel).Scan(&afterRejected); err != nil || afterRejected != beforeProvider {
		t.Fatal("rejected app rewrap partially changed credentials", err)
	}
	if err := Rewrap(f.ctx, f.owner, f.secrets); err != nil {
		t.Fatal(err)
	}
	onlyNew := &security.Secrets{Keys: map[string][]byte{"new": f.secrets.Keys["new"]}, Active: "new", Pepper: f.secrets.Pepper}
	for _, item := range []struct{ table, column, id, purpose, old, plain string }{
		{"identity_challenges", "registration_email_ciphertext", v.challenge, "identity-registration", v.emailCipher, v.email},
		{"identity_mail_outbox", "payload_ciphertext", v.outbox, "identity-mail-outbox", v.payloadCipher, v.payload},
	} {
		var rewritten string
		statement := "SELECT " + pgx.Identifier{item.column}.Sanitize() + " FROM " + pgx.Identifier{item.table}.Sanitize() + " WHERE id=$1"
		if err := f.app.QueryRow(f.ctx, statement, item.id).Scan(&rewritten); err != nil || rewritten == item.old || !strings.HasPrefix(rewritten, "v2.new.") {
			t.Fatal("owner did not rewrap identity ciphertext", err)
		}
		if plain, err := onlyNew.OpenFor(rewritten, item.purpose, item.id); err != nil || plain != item.plain {
			t.Fatal("rewrap changed identity plaintext", err)
		}
		if _, err := onlyNew.OpenFor(rewritten, item.purpose, uuid.NewString()); err == nil {
			t.Fatal("rewrapped identity secret lost row AAD")
		}
		if _, err := onlyNew.OpenFor(rewritten, "mfa:user", item.id); err == nil {
			t.Fatal("rewrapped identity secret lost purpose AAD")
		}
		if plain, err := f.secrets.OpenFor(item.old, item.purpose, item.id); err != nil || plain != item.plain {
			t.Fatal("old backup ciphertext/key no longer decrypts", err)
		}
	}
	var state string
	var count int
	var generation int64
	if err := f.app.QueryRow(f.ctx, `SELECT state,attempt_count,claim_generation FROM identity_mail_outbox WHERE id=$1`, v.outbox).Scan(&state, &count, &generation); err != nil || state != "pending" || count != 0 || generation != 0 {
		t.Fatal("rewrap changed delivery state or claim identity", state, count, generation, err)
	}
	// A valid envelope encrypted for a different row survives SQL syntax checks,
	// but maintenance must reject its AAD and roll back every earlier rewrite.
	bad := seedSelfServiceEncryptedFixture(t, f)
	wrong, err := f.secrets.SealFor(bad.payload, "identity-mail-outbox", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE identity_mail_outbox SET payload_ciphertext=$2 WHERE id=$1`, bad.outbox, wrong); err != nil {
		t.Fatal(err)
	}
	readSnapshot := func() []string {
		t.Helper()
		rows, err := f.app.Query(f.ctx, `SELECT secret_ciphertext FROM channel_credentials UNION ALL SELECT registration_email_ciphertext FROM identity_challenges WHERE registration_email_ciphertext IS NOT NULL UNION ALL SELECT payload_ciphertext FROM identity_mail_outbox WHERE payload_ciphertext IS NOT NULL ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var values []string
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return values
	}
	before := readSnapshot()
	if err = Rewrap(f.ctx, f.owner, f.secrets); err == nil {
		t.Fatal("rewrap accepted a corrupt identity payload")
	}
	if after := readSnapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("failed identity rewrap partially committed ciphertexts")
	}
	var audits int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='encryption.rewrap'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("failed/refused rewrap wrote an audit or success omitted it", audits, err)
	}
}
