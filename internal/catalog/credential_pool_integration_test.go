package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

type credentialPoolFixture struct {
	ctx                        context.Context
	owner, app                 *pgxpool.Pool
	store                      *catalog.Store
	schema, appRole, projector string
	channel, model             string
}

func credentialPoolDB(t *testing.T, apply bool) *credentialPoolFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for real credential pool transactions")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	f := &credentialPoolFixture{ctx: ctx, schema: "pool_" + suffix, appRole: "pool_app_" + suffix, projector: "pool_proj_" + suffix}
	for _, statement := range []string{"CREATE SCHEMA " + pgx.Identifier{f.schema}.Sanitize(), "CREATE ROLE " + pgx.Identifier{f.appRole}.Sanitize() + " NOLOGIN NOINHERIT NOSUPERUSER", "CREATE ROLE " + pgx.Identifier{f.projector}.Sanitize() + " NOLOGIN NOINHERIT NOSUPERUSER"} {
		if _, err = admin.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = f.schema
	if f.owner, err = pgxpool.NewWithConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		f.owner.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE")
		for _, role := range []string{f.appRole, f.projector} {
			_, _ = admin.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
			_, _ = admin.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize())
		}
		admin.Close()
	})
	if !apply {
		return f
	}
	if err = migrations.Apply(ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	if err = operations.ProvisionFor(ctx, f.owner, f.schema, f.appRole, f.projector); err != nil {
		t.Fatal(err)
	}
	appConfig := f.owner.Config()
	appConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{f.appRole}.Sanitize())
		return err
	}
	if f.app, err = pgxpool.NewWithConfig(ctx, appConfig); err != nil {
		t.Fatal(err)
	}
	secrets := &security.Secrets{Keys: map[string][]byte{"pool": bytes.Repeat([]byte{0x63}, 32)}, Active: "pool", Pepper: bytes.Repeat([]byte{0x77}, 32)}
	f.store = catalog.New(f.app, secrets)
	if f.channel, err = f.store.SaveChannel(ctx, "pool-admin", "", catalog.ChannelInput{Name: "pool-channel", BaseURL: "https://example.com/v1", Secret: "pool-primary-secret"}); err != nil {
		t.Fatal(err)
	}
	if f.model, err = f.store.CreateModel(ctx, "pool-admin", catalog.ModelInput{PublicName: "pool-model", ContextLimit: 100, InputUpperBound: 80, OutputLimit: 20, DefaultOutputLimit: 20}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *credentialPoolFixture) pool(t *testing.T, version string) catalog.CredentialPoolView {
	t.Helper()
	v, err := f.store.CredentialPool(f.ctx, f.channel, version)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func retained(m catalog.CredentialPoolMember) catalog.CredentialPoolMemberInput {
	return catalog.CredentialPoolMemberInput{CredentialID: m.CredentialID, Label: m.Label, Weight: m.Weight, MaxInflight: m.MaxInflight}
}

func newPoolMember(secret string) catalog.CredentialPoolMemberInput {
	return catalog.CredentialPoolMemberInput{Secret: secret, Label: "secondary", Weight: 3, MaxInflight: 4}
}

func (f *credentialPoolFixture) addSecondary(t *testing.T) catalog.CredentialPoolView {
	t.Helper()
	base := f.pool(t, "")
	v, err := f.store.UpdateCredentialPool(f.ctx, "pool-admin", f.channel, catalog.CredentialPoolInput{ExpectedVersion: base.ChannelVersionID, Members: []catalog.CredentialPoolMemberInput{retained(base.Members[0]), newPoolMember("pool-secondary-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *credentialPoolFixture) publish(t *testing.T, revision int64) int64 {
	t.Helper()
	next, err := f.store.Publish(f.ctx, "pool-admin", f.model, revision, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "upstream", Priority: 7, Weight: 9, MaxInflight: 11}})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

type poolDBState struct {
	Version                                                string
	Credentials, Versions, Members, Audits, RouteRevisions int64
}

func (f *credentialPoolFixture) state(t *testing.T) poolDBState {
	t.Helper()
	var state poolDBState
	if err := f.owner.QueryRow(f.ctx, `SELECT current_version,(SELECT count(*) FROM channel_credentials),(SELECT count(*) FROM channel_versions),(SELECT count(*) FROM channel_version_credentials),(SELECT count(*) FROM audit_events),(SELECT count(*) FROM route_policy_versions) FROM channels WHERE id=$1`, f.channel).Scan(&state.Version, &state.Credentials, &state.Versions, &state.Members, &state.Audits, &state.RouteRevisions); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestCredentialPoolVersionsCASAndMetadataIdentity(t *testing.T) {
	f := credentialPoolDB(t, true)
	initial := f.pool(t, "")
	if len(initial.Members) != 1 || !initial.Members[0].Primary || initial.Members[0].Position != 1 || initial.Stale {
		t.Fatal("new channel did not get its primary membership")
	}
	f.publish(t, 0)
	updated := f.addSecondary(t)
	if updated.ChannelVersionID == initial.ChannelVersionID || updated.CurrentVersion != updated.ChannelVersionID || len(updated.Members) != 2 || updated.Members[0].CredentialID != initial.Members[0].CredentialID || updated.Members[1].Primary {
		t.Fatal("pool update did not freeze a new complete version")
	}
	historical := f.pool(t, initial.ChannelVersionID)
	if !historical.Stale || len(historical.Members) != 1 || historical.Members[0].CredentialID != initial.Members[0].CredentialID {
		t.Fatal("pool update changed old membership")
	}
	before := f.state(t)
	_, err := f.store.UpdateCredentialPool(f.ctx, "pool-admin", f.channel, catalog.CredentialPoolInput{ExpectedVersion: initial.ChannelVersionID, Members: []catalog.CredentialPoolMemberInput{retained(initial.Members[0])}})
	if !errors.Is(err, catalog.ErrConflict) || f.state(t) != before {
		t.Fatal("stale compare-and-swap changed state", err)
	}
	if _, err = f.store.SaveChannel(f.ctx, "pool-admin", f.channel, catalog.ChannelInput{Name: "renamed-pool-channel"}); err != nil {
		t.Fatal(err)
	}
	metadata := f.pool(t, "")
	if metadata.ChannelVersionID == updated.ChannelVersionID || !reflect.DeepEqual(metadata.Members, updated.Members) || f.state(t).Credentials != before.Credentials {
		t.Fatal("metadata update cloned credentials or dropped pool members")
	}
	candidates, err := f.store.Candidates(f.ctx, f.model, 1)
	if err != nil || len(candidates) != 1 || candidates[0].VersionID != initial.ChannelVersionID {
		t.Fatal("pool edit silently changed a published snapshot", err)
	}
	f.publish(t, 1)
	candidates, err = f.store.Candidates(f.ctx, f.model, 2)
	if err != nil || len(candidates) != 2 {
		t.Fatal("republished route did not expand its frozen pool", err)
	}
	for i, candidate := range candidates {
		if candidate.CredentialID != metadata.Members[i].CredentialID || candidate.Priority != 7 || candidate.Weight != 9 || candidate.MaxInflight != 11 || candidate.CredentialWeight != metadata.Members[i].Weight || candidate.CredentialMaxInflight != metadata.Members[i].MaxInflight {
			t.Fatal("credential expansion changed channel-level weights or caps")
		}
	}
	for _, mutation := range []string{`UPDATE channel_version_credentials SET weight=2`, `DELETE FROM channel_version_credentials`} {
		if _, err = f.app.Exec(f.ctx, mutation); err == nil {
			t.Fatal("serving role mutated pool history")
		}
		if _, err = f.owner.Exec(f.ctx, mutation); err == nil || !strings.Contains(err.Error(), "immutable history") {
			t.Fatal("pool history lacked its owner-level immutability trigger", err)
		}
	}
	appendTx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	lateCredential := uuid.NewString()
	if _, err = appendTx.Exec(f.ctx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'late-credential')`, lateCredential, f.channel); err != nil {
		t.Fatal(err)
	}
	if _, err = appendTx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,3,'late-member',1,1)`, metadata.ChannelVersionID, f.channel, lateCredential); err == nil || !strings.Contains(err.Error(), "must be created with its channel version") {
		t.Fatal("serving role appended to a frozen pool", err)
	}
	appendTx.Rollback(f.ctx)
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://example.com',$3)`, uuid.NewString(), f.channel, initial.Members[0].CredentialID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil {
		t.Fatal("version without primary membership committed")
	}
}

func TestCredentialPoolRejectsInvalidAndDuplicateMembersAtomically(t *testing.T) {
	f := credentialPoolDB(t, true)
	initial := f.pool(t, "")
	other, err := f.store.SaveChannel(f.ctx, "pool-admin", "", catalog.ChannelInput{Name: "other-channel", BaseURL: "https://example.net", Secret: "other-pool-secret"})
	if err != nil {
		t.Fatal(err)
	}
	otherPool, err := f.store.CredentialPool(f.ctx, other, "")
	if err != nil {
		t.Fatal(err)
	}
	one := retained(initial.Members[0])
	cases := map[string][]catalog.CredentialPoolMemberInput{
		"empty":              {},
		"duplicate_id":       {one, one},
		"foreign_id":         {retained(otherPool.Members[0])},
		"missing_id":         {{CredentialID: uuid.NewString(), Label: "missing", Weight: 1, MaxInflight: 1}},
		"duplicate_new":      {newPoolMember("same-new-secret"), newPoolMember("same-new-secret")},
		"existing_plaintext": {newPoolMember("pool-primary-secret")},
		"both_sources":       {{CredentialID: one.CredentialID, Secret: "another-secret", Label: "both", Weight: 1, MaxInflight: 1}},
		"neither_source":     {{Label: "neither", Weight: 1, MaxInflight: 1}},
	}
	for name, mutate := range map[string]func(*catalog.CredentialPoolMemberInput){
		"weight_zero":    func(m *catalog.CredentialPoolMemberInput) { m.Weight = 0 },
		"weight_large":   func(m *catalog.CredentialPoolMemberInput) { m.Weight = 1_000_001 },
		"capacity_zero":  func(m *catalog.CredentialPoolMemberInput) { m.MaxInflight = 0 },
		"capacity_large": func(m *catalog.CredentialPoolMemberInput) { m.MaxInflight = 1001 },
		"empty_label":    func(m *catalog.CredentialPoolMemberInput) { m.Label = "" },
		"blank_label":    func(m *catalog.CredentialPoolMemberInput) { m.Label = "\u2003 " },
		"control_label":  func(m *catalog.CredentialPoolMemberInput) { m.Label = "label\n" },
		"long_label":     func(m *catalog.CredentialPoolMemberInput) { m.Label = strings.Repeat("a", 65) },
		"invalid_label":  func(m *catalog.CredentialPoolMemberInput) { m.Label = string([]byte{0xff}) },
	} {
		m := one
		mutate(&m)
		cases[name] = []catalog.CredentialPoolMemberInput{m}
	}
	for name, secret := range map[string]string{"space_secret": "with space", "unicode_space_secret": "with\u2003space", "control_secret": "bad\r\nheader", "long_secret": strings.Repeat("s", 4097), "invalid_secret": string([]byte{0xff})} {
		cases[name] = []catalog.CredentialPoolMemberInput{newPoolMember(secret)}
	}
	tooMany := make([]catalog.CredentialPoolMemberInput, 33)
	for i := range tooMany {
		tooMany[i] = newPoolMember("member-" + strconv.Itoa(i))
	}
	cases["too_many"] = tooMany
	for name, members := range cases {
		t.Run(name, func(t *testing.T) {
			before := f.state(t)
			_, err := f.store.UpdateCredentialPool(f.ctx, "pool-admin", f.channel, catalog.CredentialPoolInput{ExpectedVersion: initial.ChannelVersionID, Members: members})
			if !errors.Is(err, catalog.ErrInvalid) || f.state(t) != before {
				t.Fatal("invalid pool changed data", err)
			}
		})
	}
	boundary := newPoolMember(strings.Repeat("s", 4096))
	boundary.Label, boundary.Weight, boundary.MaxInflight = strings.Repeat("a", 64), 1_000_000, 1000
	if _, err = f.store.UpdateCredentialPool(f.ctx, "pool-admin", f.channel, catalog.CredentialPoolInput{ExpectedVersion: initial.ChannelVersionID, Members: []catalog.CredentialPoolMemberInput{boundary}}); err != nil {
		t.Fatal("valid maximum-sized metadata or secret rejected", err)
	}
	before := f.state(t)
	if _, err = f.store.SaveChannel(f.ctx, "pool-admin", f.channel, catalog.ChannelInput{Secret: "pool-primary-secret"}); !errors.Is(err, catalog.ErrInvalid) || f.state(t) != before {
		t.Fatal("single-secret edit reintroduced historical plaintext", err)
	}
}

func TestCredentialPoolRevocationCoversFrozenRoutesAndAllowsSecondary(t *testing.T) {
	f := credentialPoolDB(t, true)
	initial := f.pool(t, "")
	pool := f.addSecondary(t)
	f.publish(t, 0)
	primary, secondary := pool.Members[0].CredentialID, pool.Members[1].CredentialID
	if err := f.store.RevokeCredential(f.ctx, "pool-admin", f.channel, primary); err != nil {
		t.Fatal(err)
	}
	if f.pool(t, initial.ChannelVersionID).Members[0].RevokedAt == nil {
		t.Fatal("historical membership did not expose permanent revocation")
	}
	candidates, err := f.store.CandidateSnapshot(f.ctx, f.model, 1)
	if err != nil || len(candidates) != 2 || candidates[0].ExclusionReason != "credential_revoked" || candidates[0].Secret != "" || candidates[1].ExclusionReason != "" || candidates[1].Secret != "pool-secondary-secret" {
		t.Fatal("revoked primary remained dispatchable or hid its live secondary", err)
	}
	if _, err = f.store.SaveChannel(f.ctx, "pool-admin", f.channel, catalog.ChannelInput{Name: "metadata-cannot-restore-secret"}); err != nil {
		t.Fatal(err)
	}
	if got := f.pool(t, ""); got.Members[0].CredentialID != primary || got.Members[0].RevokedAt == nil {
		t.Fatal("metadata update bypassed credential revocation")
	}
	f.publish(t, 1)
	before := f.state(t)
	if err = f.store.RevokeCredential(f.ctx, "pool-admin", f.channel, primary); err != nil || f.state(t) != before {
		t.Fatal("idempotent revoke created another audit or version", err)
	}
	current := f.pool(t, "")
	for _, member := range []catalog.CredentialPoolMemberInput{retained(current.Members[0]), newPoolMember("pool-primary-secret")} {
		if _, err = f.store.UpdateCredentialPool(f.ctx, "pool-admin", f.channel, catalog.CredentialPoolInput{ExpectedVersion: current.ChannelVersionID, Members: []catalog.CredentialPoolMemberInput{member}}); !errors.Is(err, catalog.ErrInvalid) {
			t.Fatal("revoked credential or its plaintext returned to a pool", err)
		}
	}
	if err = f.store.RevokeCredential(f.ctx, "pool-admin", f.channel, secondary); err != nil {
		t.Fatal(err)
	}
	before = f.state(t)
	if _, err = f.store.Publish(f.ctx, "pool-admin", f.model, 2, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "upstream", Weight: 1, MaxInflight: 1}}); !errors.Is(err, catalog.ErrInvalid) || f.state(t) != before {
		t.Fatal("all-revoked pool published or left a partial route", err)
	}
	candidates, err = f.store.CandidateSnapshot(f.ctx, f.model, 1)
	if err != nil || len(candidates) != 2 {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.ExclusionReason != "credential_revoked" || candidate.Secret != "" {
			t.Fatal("permanent revoke did not cover a frozen old route")
		}
	}
	if err = f.store.RevokeCredential(f.ctx, "pool-admin", "foreign-channel", primary); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoke accepted a foreign channel", err)
	}
}

func TestCredentialPoolSafeViewAndAuditRollback(t *testing.T) {
	f := credentialPoolDB(t, true)
	pool := f.addSecondary(t)
	body, err := json.Marshal(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"pool-primary-secret", "pool-secondary-secret", "secret", "ciphertext", "fingerprint", "prefix"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("pool metadata exposed forbidden material: %s", forbidden)
		}
	}
	if _, err = f.store.CredentialPool(f.ctx, f.channel, "foreign-version"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("pool history accepted a nonexistent version", err)
	}
	before := f.state(t)
	if _, err = f.owner.Exec(f.ctx, "REVOKE INSERT ON audit_events FROM "+pgx.Identifier{f.appRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.UpdateCredentialPool(f.ctx, "pool-admin", f.channel, catalog.CredentialPoolInput{ExpectedVersion: pool.ChannelVersionID, Members: []catalog.CredentialPoolMemberInput{newPoolMember("rolled-back-secret")}}); err == nil || f.state(t) != before {
		t.Fatal("audit failure left a credential, member, version or channel pointer", err)
	}
	if err = f.store.RevokeCredential(f.ctx, "pool-admin", f.channel, pool.Members[0].CredentialID); err == nil || f.pool(t, "").Members[0].RevokedAt != nil || f.state(t) != before {
		t.Fatal("audit failure left a permanent revocation", err)
	}
	if _, err = f.owner.Exec(f.ctx, "GRANT INSERT ON audit_events TO "+pgx.Identifier{f.appRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.SaveChannel(f.ctx, "pool-admin", f.channel, catalog.ChannelInput{Status: "disabled"}); err != nil || !f.pool(t, "").Stale {
		t.Fatal("disabled current pool is not marked stale", err)
	}
	if _, err = f.store.SaveChannel(f.ctx, "pool-admin", f.channel, catalog.ChannelInput{Status: "quarantined"}); err != nil || !f.pool(t, "").Stale {
		t.Fatal("quarantined current pool is not marked stale", err)
	}
}

func TestCredentialPoolMaximumMembersAndLegacySecretEditBoundary(t *testing.T) {
	f := credentialPoolDB(t, true)
	initial := f.pool(t, "")
	members := []catalog.CredentialPoolMemberInput{retained(initial.Members[0])}
	for i := 1; i < 32; i++ {
		members = append(members, newPoolMember("max-pool-secret-"+strconv.Itoa(i)))
	}
	pool, err := f.store.UpdateCredentialPool(f.ctx, "pool-admin", f.channel, catalog.CredentialPoolInput{ExpectedVersion: initial.ChannelVersionID, Members: members})
	if err != nil || len(pool.Members) != 32 {
		t.Fatal("32-member pool rejected", err)
	}
	before := f.state(t)
	if _, err = f.store.SaveChannel(f.ctx, "pool-admin", f.channel, catalog.ChannelInput{Secret: "must-use-pool-api"}); !errors.Is(err, catalog.ErrInvalid) || f.state(t) != before {
		t.Fatal("legacy single-secret edit replaced a multi-member pool", err)
	}
	if _, err = f.store.SaveChannel(f.ctx, "pool-admin", f.channel, catalog.ChannelInput{Name: "all-32-retained"}); err != nil || !reflect.DeepEqual(f.pool(t, "").Members, pool.Members) {
		t.Fatal("metadata edit did not retain all 32 identities", err)
	}
}

func TestCredentialPoolCreationGuardAcceptsSavepointsAndRejectsRestoredXIDCollision(t *testing.T) {
	f := credentialPoolDB(t, true)
	initial := f.pool(t, "")
	version := uuid.NewString()
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, "SAVEPOINT pool_creation"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://example.com',$3)`, version, f.channel, initial.Members[0].CredentialID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "RELEASE SAVEPOINT pool_creation"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1)`, version, f.channel, initial.Members[0].CredentialID); err != nil {
		t.Fatal("pool guard rejected a released savepoint's creating transaction", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}

	// A restored source stamp can equal a new cluster's current xid8. Simulate
	// that collision with two real backends: the committed tuple belongs to a
	// different transaction even though its persisted stamp matches the writer.
	writer, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(f.ctx)
	var writerXID string
	if err = writer.QueryRow(f.ctx, `SELECT pg_current_xact_id()::text`).Scan(&writerXID); err != nil {
		t.Fatal(err)
	}
	restore, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer restore.Rollback(f.ctx)
	if _, err = restore.Exec(f.ctx, `ALTER TABLE channel_versions DISABLE TRIGGER immutable_channel_version`); err != nil {
		t.Fatal(err)
	}
	if _, err = restore.Exec(f.ctx, `UPDATE channel_versions SET credential_pool_creation_xid=$2::xid8 WHERE id=$1`, initial.ChannelVersionID, writerXID); err != nil {
		t.Fatal(err)
	}
	if _, err = restore.Exec(f.ctx, `ALTER TABLE channel_versions ENABLE TRIGGER immutable_channel_version`); err != nil {
		t.Fatal(err)
	}
	if err = restore.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var stampMatches, ownsTuple bool
	if err = writer.QueryRow(f.ctx, `SELECT credential_pool_creation_xid=pg_current_xact_id(),EXISTS(SELECT 1 FROM pg_locks WHERE locktype='transactionid' AND transactionid=v.xmin AND pid=pg_backend_pid() AND granted AND mode='ExclusiveLock') FROM channel_versions v WHERE id=$1`, initial.ChannelVersionID).Scan(&stampMatches, &ownsTuple); err != nil || !stampMatches || ownsTuple {
		t.Fatal("restore collision fixture did not isolate stamp equality from tuple ownership", err)
	}
	credential := uuid.NewString()
	if _, err = writer.Exec(f.ctx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'restore-collision-secret')`, credential, f.channel); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,2,'forged',1,1)`, initial.ChannelVersionID, f.channel, credential); err == nil || !strings.Contains(err.Error(), "must be created with its channel version") {
		t.Fatal("historical stamp collision allowed a frozen pool append", err)
	}
}

func TestCredentialPoolMigrationBackfillsEveryHistoricalVersion(t *testing.T) {
	f := credentialPoolDB(t, false)
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "008_credential_pools.sql" {
			break
		}
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.owner.Exec(f.ctx, string(body)); err != nil {
			t.Fatalf("legacy schema %s: %v", name, err)
		}
	}
	channel, first, second, credential, actor := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,email,password_hash) VALUES($1,$2,'fixture')`, []any{actor, actor + "@pool.invalid"}},
		{`INSERT INTO channels(id,name,current_version) VALUES($1,'legacy',$2)`, []any{channel, second}},
		{`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'legacy-ciphertext')`, []any{credential, channel}},
		{`INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://example.com',$3),($4,$2,'https://example.net',$3)`, []any{first, channel, credential, second}},
		{`INSERT INTO channel_model_discoveries(id,actor,channel_id,channel_version_id,credential_id,format,bindings) VALUES($1,$2,$3,$4,$5,'openai','[]')`, []any{uuid.NewString(), actor, channel, first, credential}},
	} {
		if _, err = tx.Exec(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	body, err := migrations.Files.ReadFile("008_credential_pools.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, string(body)); err != nil {
		t.Fatal("008 upgrade failed on existing history and discovery", err)
	}
	var matched, discoveries int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM channel_version_credentials WHERE channel_id=$1 AND credential_id=$2 AND position=1 AND label='primary' AND weight=1 AND max_inflight=1000),(SELECT count(*) FROM channel_model_discoveries)`, channel, credential).Scan(&matched, &discoveries); err != nil || matched != 2 || discoveries != 1 {
		t.Fatal("upgrade did not preserve every old version and discovery", matched, discoveries, err)
	}
	secondary, third := uuid.NewString(), uuid.NewString()
	poolTx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer poolTx.Rollback(f.ctx)
	if _, err = poolTx.Exec(f.ctx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'secondary-ciphertext')`, secondary, channel); err != nil {
		t.Fatal(err)
	}
	if _, err = poolTx.Exec(f.ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id,credential_pool_creation_xid) VALUES($1,$2,'https://example.com',$3,'1')`, third, channel, credential); err != nil {
		t.Fatal(err)
	}
	if _, err = poolTx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1),($1,$2,$4,2,'secondary',1,1)`, third, channel, credential, secondary); err != nil {
		t.Fatal("creation xid was not safely assigned by the insert trigger", err)
	}
	if err = poolTx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO channel_model_discoveries(id,actor,channel_id,channel_version_id,credential_id,format,bindings) VALUES($1,$2,$3,$4,$5,'openai','[]')`, uuid.NewString(), actor, channel, third, secondary); err != nil {
		t.Fatal("directory identity still restricted to the primary", err)
	}
}
