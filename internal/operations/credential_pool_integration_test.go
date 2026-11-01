package operations

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func requirePoolSQLState(t *testing.T, err error, state string) {
	t.Helper()
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != state {
		t.Fatalf("wanted SQLSTATE %s, got %v", state, err)
	}
}

func insertPoolCredential(t *testing.T, f *operationFixture, channel, plain string) string {
	t.Helper()
	id := uuid.NewString()
	ciphertext, err := f.secrets.SealFor(plain, "provider:platform", id)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,$3)`, id, channel, ciphertext)
	return id
}

func newPoolTestVersion(t *testing.T, f *operationFixture, tx pgx.Tx, channel, primary string) string {
	t.Helper()
	version := uuid.NewString()
	if _, err := tx.Exec(f.ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://example.com',$3)`, version, channel, primary); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestProvisionedCredentialPoolMembershipIsReadableAppendOnly(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	initial, err := c.CredentialPool(f.ctx, f.channel, "")
	if err != nil || len(initial.Members) != 1 {
		t.Fatalf("app cannot read the initial membership: %+v %v", initial, err)
	}
	updated, err := c.UpdateCredentialPool(f.ctx, f.operator, f.channel, catalog.CredentialPoolInput{
		ExpectedVersion: initial.ChannelVersionID,
		Members: []catalog.CredentialPoolMemberInput{
			{CredentialID: initial.Members[0].CredentialID, Label: "retained", Weight: 3, MaxInflight: 10},
			{Secret: "second-pool-provider-key", Label: "secondary", Weight: 1, MaxInflight: 4},
		},
	})
	if err != nil || len(updated.Members) != 2 {
		t.Fatalf("app cannot append a complete pool and version: %+v %v", updated, err)
	}
	var count int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM channel_version_credentials WHERE channel_version_id=$1`, updated.ChannelVersionID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("app cannot SELECT appended membership: %d %v", count, err)
	}
	for _, sql := range []string{
		`UPDATE channel_version_credentials SET label='forged' WHERE channel_version_id=$1`,
		`DELETE FROM channel_version_credentials WHERE channel_version_id=$1`,
	} {
		_, err = f.app.Exec(f.ctx, sql, updated.ChannelVersionID)
		requirePoolSQLState(t, err, "42501")
		_, err = f.owner.Exec(f.ctx, sql, updated.ChannelVersionID)
		requirePoolSQLState(t, err, "P0001")
	}
	for _, sql := range []string{
		`SELECT credential_id FROM channel_version_credentials`,
		`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) SELECT channel_version_id,channel_id,credential_id,position,label,weight,max_inflight FROM channel_version_credentials WHERE false`,
		`UPDATE channel_version_credentials SET weight=1`,
		`DELETE FROM channel_version_credentials`,
	} {
		_, err = f.projection.Exec(f.ctx, sql)
		requirePoolSQLState(t, err, "42501")
	}
	unchanged, err := c.CredentialPool(f.ctx, f.channel, updated.ChannelVersionID)
	if err != nil || !reflect.DeepEqual(updated, unchanged) {
		t.Fatalf("rejected mutation changed immutable membership: %+v %+v %v", updated, unchanged, err)
	}
}

func TestProvisionedCredentialPoolCompositeIdentityAndBounds(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	initial, err := c.CredentialPool(f.ctx, f.channel, "")
	if err != nil {
		t.Fatal(err)
	}
	otherChannel, err := c.SaveChannel(f.ctx, f.operator, "", catalog.ChannelInput{Name: "other-pool", BaseURL: "https://example.net/v1", Secret: "other-pool-secret"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.CredentialPool(f.ctx, otherChannel, "")
	if err != nil {
		t.Fatal(err)
	}
	primary := initial.Members[0].CredentialID
	spare := insertPoolCredential(t, f, f.channel, "same-channel-spare")
	for _, mismatch := range []string{"version-channel-mismatch", "credential-channel-mismatch", "other-version-channel-mismatch"} {
		t.Run(mismatch, func(t *testing.T) {
			tx, err := f.app.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			versionA := newPoolTestVersion(t, f, tx, f.channel, primary)
			versionB := newPoolTestVersion(t, f, tx, otherChannel, other.Members[0].CredentialID)
			version, channel, credential := versionA, otherChannel, other.Members[0].CredentialID
			if mismatch == "credential-channel-mismatch" {
				channel = f.channel
			} else if mismatch == "other-version-channel-mismatch" {
				version, channel, credential = versionB, f.channel, primary
			}
			_, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,2,'invalid-identity',1,1)`, version, channel, credential)
			requirePoolSQLState(t, err, "23503")
		})
	}
	for _, bounds := range []struct {
		name, label             string
		position, weight, limit int
	}{
		{"position-zero", "valid", 0, 1, 1},
		{"position-33", "valid", 33, 1, 1},
		{"weight-zero", "valid", 2, 0, 1},
		{"weight-over-limit", "valid", 2, 1_000_001, 1},
		{"capacity-zero", "valid", 2, 1, 0},
		{"capacity-over-limit", "valid", 2, 1, 1001},
		{"label-empty", "", 2, 1, 1},
		{"label-blank", " \t ", 2, 1, 1},
		{"label-control", "bad\nlabel", 2, 1, 1},
		{"label-over-byte-limit", strings.Repeat("中", 22), 2, 1, 1},
	} {
		t.Run(bounds.name, func(t *testing.T) {
			tx, err := f.app.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			version := newPoolTestVersion(t, f, tx, f.channel, primary)
			_, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,$4,$5,$6,$7)`, version, f.channel, spare, bounds.position, bounds.label, bounds.weight, bounds.limit)
			requirePoolSQLState(t, err, "23514")
		})
	}
	// Position uniqueness and credential uniqueness jointly cap a version at
	// 32 distinct members, even for a role that can issue arbitrary INSERTs.
	for _, duplicate := range []string{"credential", "position"} {
		t.Run("duplicate-"+duplicate, func(t *testing.T) {
			tx, err := f.app.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			version := newPoolTestVersion(t, f, tx, f.channel, primary)
			if _, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1)`, version, f.channel, primary); err != nil {
				t.Fatal(err)
			}
			credential, position := primary, 2
			if duplicate == "position" {
				credential, position = spare, 1
			}
			_, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,$4,'duplicate',1,1)`, version, f.channel, credential, position)
			requirePoolSQLState(t, err, "23505")
		})
	}
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	fullVersion := newPoolTestVersion(t, f, tx, f.channel, primary)
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1),($1,$2,$4,2,$5,1000000,1000)`, fullVersion, f.channel, primary, spare, strings.Repeat("中", 21)+"a"); err != nil {
		t.Fatal(err)
	}
	for position := 3; position <= 32; position++ {
		credential := insertPoolCredential(t, f, f.channel, fmt.Sprintf("pool-capacity-%d", position))
		if _, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,$4,'bounded-member',1,1)`, fullVersion, f.channel, credential, position); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = tx.QueryRow(f.ctx, `SELECT count(*) FROM channel_version_credentials WHERE channel_version_id=$1`, fullVersion).Scan(&count); err != nil || count != 32 {
		t.Fatalf("valid 32-member boundary rejected: %d %v", count, err)
	}
	extra := insertPoolCredential(t, f, f.channel, "thirty-third-member")
	// A savepoint keeps the valid maximum-size version available to commit.
	if _, err = tx.Exec(f.ctx, `SAVEPOINT overflow_member`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,33,'overflow',1,1)`, fullVersion, f.channel, extra)
	requirePoolSQLState(t, err, "23514")
	if _, err = tx.Exec(f.ctx, `ROLLBACK TO SAVEPOINT overflow_member`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatalf("maximum-size pool could not commit: %v", err)
	}
	// A version with no primary membership cannot commit, including an owner
	// attempt to bypass the catalog. The deferred FK permits atomic creation.
	tx, err = f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	emptyVersion := uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://example.com',$3)`, emptyVersion, f.channel, primary); err != nil {
		t.Fatal(err)
	}
	err = tx.Commit(f.ctx)
	requirePoolSQLState(t, err, "23503")
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM channel_versions WHERE id=$1`, emptyVersion).Scan(&count); err != nil || count != 0 {
		t.Fatalf("empty version survived failed deferred constraint: %d %v", count, err)
	}
	// Discovery may identify a secondary pool member after migration 008, but
	// an unrelated same-channel credential is still not a valid frozen member.
	f.exec(t, `INSERT INTO channel_model_discoveries(id,actor,channel_id,channel_version_id,credential_id,format,bindings) VALUES($1,$2,$3,$4,$5,'openai','[]')`, uuid.NewString(), f.operator, f.channel, fullVersion, spare)
	_, err = f.app.Exec(f.ctx, `INSERT INTO channel_model_discoveries(id,actor,channel_id,channel_version_id,credential_id,format,bindings) VALUES($1,$2,$3,$4,$5,'openai','[]')`, uuid.NewString(), f.operator, f.channel, fullVersion, extra)
	requirePoolSQLState(t, err, "23503")
}

func TestProvisionedCredentialPoolCannotAppendAfterCommitOrForgeCreationTransaction(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	initial, err := c.CredentialPool(f.ctx, f.channel, "")
	if err != nil {
		t.Fatal(err)
	}
	spare := insertPoolCredential(t, f, f.channel, "late-member-secret")
	for _, pool := range []*pgxpool.Pool{f.app, f.owner} {
		_, err = pool.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,2,'late-member',1,1)`, initial.ChannelVersionID, f.channel, spare)
		requirePoolSQLState(t, err, "P0001")
	}
	_, err = f.app.Exec(f.ctx, `UPDATE channel_versions SET credential_pool_creation_xid=pg_current_xact_id() WHERE id=$1`, initial.ChannelVersionID)
	requirePoolSQLState(t, err, "42501")
	_, err = f.owner.Exec(f.ctx, `UPDATE channel_versions SET credential_pool_creation_xid=pg_current_xact_id() WHERE id=$1`, initial.ChannelVersionID)
	requirePoolSQLState(t, err, "P0001")
	var oldStamp string
	if err = f.app.QueryRow(f.ctx, `SELECT credential_pool_creation_xid::text FROM channel_versions WHERE id=$1`, initial.ChannelVersionID).Scan(&oldStamp); err != nil {
		t.Fatal(err)
	}
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	version, primary := uuid.NewString(), initial.Members[0].CredentialID
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id,credential_pool_creation_xid) VALUES($1,$2,'https://example.com',$3,$4::xid8)`, version, f.channel, primary, oldStamp); err != nil {
		t.Fatal(err)
	}
	var forcedCurrent, retainedForged bool
	if err = tx.QueryRow(f.ctx, `SELECT credential_pool_creation_xid=pg_current_xact_id(),credential_pool_creation_xid=$2::xid8 FROM channel_versions WHERE id=$1`, version, oldStamp).Scan(&forcedCurrent, &retainedForged); err != nil || !forcedCurrent || retainedForged {
		t.Fatalf("explicit creation stamp bypassed forced current transaction: %v %v %v", forcedCurrent, retainedForged, err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1)`, version, f.channel, primary); err != nil {
		t.Fatalf("same transaction could not complete the primary membership: %v", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	_, err = f.app.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,2,'late-member',1,1)`, version, f.channel, spare)
	requirePoolSQLState(t, err, "P0001")
	var count int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM channel_version_credentials WHERE channel_version_id=$1`, version).Scan(&count); err != nil || count != 1 {
		t.Fatalf("late append changed sealed membership: %d %v", count, err)
	}
}

func TestRewrapPreservesHistoricalAndRevokedPoolIdentities(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	first, err := c.CredentialPool(f.ctx, f.channel, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.UpdateCredentialPool(f.ctx, f.operator, f.channel, catalog.CredentialPoolInput{ExpectedVersion: first.ChannelVersionID, Members: []catalog.CredentialPoolMemberInput{
		{CredentialID: first.Members[0].CredentialID, Label: "historical-only", Weight: 2, MaxInflight: 7},
		{Secret: "pool-soon-revoked", Label: "revoked-history", Weight: 1, MaxInflight: 4},
		{Secret: "pool-live-retained", Label: "live-retained", Weight: 3, MaxInflight: 9},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Publish(f.ctx, f.operator, f.model, 1, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "upstream", Weight: 1, MaxInflight: 10}}); err != nil {
		t.Fatal(err)
	}
	if err = c.RevokeCredential(f.ctx, f.operator, f.channel, second.Members[1].CredentialID); err != nil {
		t.Fatal(err)
	}
	third, err := c.UpdateCredentialPool(f.ctx, f.operator, f.channel, catalog.CredentialPoolInput{ExpectedVersion: second.ChannelVersionID, Members: []catalog.CredentialPoolMemberInput{
		{CredentialID: second.Members[2].CredentialID, Label: "current-primary", Weight: 3, MaxInflight: 9},
		{Secret: "pool-new-current", Label: "current-secondary", Weight: 1, MaxInflight: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Publish(f.ctx, f.operator, f.model, 2, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "upstream", Weight: 1, MaxInflight: 10}}); err != nil {
		t.Fatal(err)
	}
	versions := []string{first.ChannelVersionID, second.ChannelVersionID, third.ChannelVersionID}
	beforeViews := make([]catalog.CredentialPoolView, len(versions))
	beforeRoutes := make([][]catalog.Candidate, len(versions))
	for i, version := range versions {
		if beforeViews[i], err = c.CredentialPool(f.ctx, f.channel, version); err != nil {
			t.Fatal(err)
		}
		if beforeRoutes[i], err = c.CandidateSnapshot(f.ctx, f.model, int64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	type encryptedIdentity struct {
		id, ciphertext, plain string
		revoked               *time.Time
	}
	before := []encryptedIdentity{}
	rows, err := f.app.Query(f.ctx, `SELECT id,secret_ciphertext,revoked_at FROM channel_credentials ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var identity encryptedIdentity
		if err = rows.Scan(&identity.id, &identity.ciphertext, &identity.revoked); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if identity.plain, err = f.secrets.OpenFor(identity.ciphertext, "provider:platform", identity.id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		before = append(before, identity)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(before) != 4 {
		t.Fatalf("expected four current/historical/revoked identities: %d %v", len(before), err)
	}
	f.secrets.Active = "new"
	if err = Rewrap(f.ctx, f.owner, f.secrets); err != nil {
		t.Fatal(err)
	}
	onlyNew := &security.Secrets{Keys: map[string][]byte{"new": f.secrets.Keys["new"]}, Active: "new", Pepper: f.secrets.Pepper}
	revokedCount := 0
	for _, identity := range before {
		var ciphertext string
		var revoked *time.Time
		if err = f.app.QueryRow(f.ctx, `SELECT secret_ciphertext,revoked_at FROM channel_credentials WHERE id=$1`, identity.id).Scan(&ciphertext, &revoked); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(ciphertext, "v2.new.") || ciphertext == identity.ciphertext {
			t.Fatal("pool identity was not rewrapped under the new master key")
		}
		plain, openErr := onlyNew.OpenFor(ciphertext, "provider:platform", identity.id)
		if openErr != nil || plain != identity.plain {
			t.Fatal("rewrap changed current/historical/revoked provider plaintext", openErr)
		}
		if !reflect.DeepEqual(revoked, identity.revoked) {
			t.Fatal("rewrap changed permanent revocation state")
		}
		if revoked != nil {
			revokedCount++
		}
		if _, err = onlyNew.OpenFor(ciphertext, "provider:platform", uuid.NewString()); err == nil {
			t.Fatal("rewrapped ciphertext accepted a different credential AAD identity")
		}
		if _, err = onlyNew.OpenFor(ciphertext, "mfa:user", identity.id); err == nil {
			t.Fatal("rewrapped ciphertext accepted a different purpose")
		}
		if old, oldErr := f.secrets.OpenFor(identity.ciphertext, "provider:platform", identity.id); oldErr != nil || old != identity.plain {
			t.Fatal("historical backup no longer decrypts with archived key material", oldErr)
		}
		if _, err = onlyNew.OpenFor(identity.ciphertext, "provider:platform", identity.id); err == nil {
			t.Fatal("old ciphertext unexpectedly decrypts without the archived key")
		}
	}
	if revokedCount != 1 {
		t.Fatalf("expected one preserved revoked pool identity, got %d", revokedCount)
	}
	newCatalog := catalog.New(f.app, onlyNew)
	for i, version := range versions {
		afterView, err := newCatalog.CredentialPool(f.ctx, f.channel, version)
		if err != nil || !reflect.DeepEqual(beforeViews[i], afterView) {
			t.Fatalf("rewrap changed membership/version/weight/capacity: %+v %+v %v", beforeViews[i], afterView, err)
		}
		afterRoute, err := newCatalog.CandidateSnapshot(f.ctx, f.model, int64(i+1))
		if err != nil || !reflect.DeepEqual(beforeRoutes[i], afterRoute) {
			t.Fatalf("rewrap changed frozen route or revoked exclusion: %+v %+v %v", beforeRoutes[i], afterRoute, err)
		}
	}
	var audits int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='encryption.rewrap'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("rewrap audit missing or duplicated: %d %v", audits, err)
	}
}
