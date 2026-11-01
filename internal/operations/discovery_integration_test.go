package operations

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

func discoveryVersion(t *testing.T, f *operationFixture, channel string) (version, credential string) {
	t.Helper()
	if err := f.app.QueryRow(f.ctx, `SELECT v.id,v.credential_id FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$1`, channel).Scan(&version, &credential); err != nil {
		t.Fatal(err)
	}
	return version, credential
}

func requireDiscoverySQLState(t *testing.T, err error, state string) {
	t.Helper()
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != state {
		t.Fatalf("wanted SQLSTATE %s, got %v", state, err)
	}
}

func TestProvisionedDiscoveryFactsAreReadableAppendOnlyAndUnique(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	version, _ := discoveryVersion(t, f, f.channel)
	target, err := c.BeginModelDiscovery(f.ctx, f.operator, f.channel, version, provider.DiscoveryFormatOpenAI)
	if err != nil || target.ID == "" {
		t.Fatalf("app role cannot append discovery start: %+v %v", target, err)
	}
	result := provider.DiscoveryResult{Models: []string{"upstream", "new-model"}, Pages: 1}
	if err = c.FinishModelDiscovery(f.ctx, target.ID, result, ""); err != nil {
		t.Fatalf("app role cannot append discovery result and audit: %v", err)
	}
	if err = c.FinishModelDiscovery(f.ctx, target.ID, result, ""); err != nil {
		t.Fatalf("identical completion was not idempotent: %v", err)
	}
	if err = c.FinishModelDiscovery(f.ctx, target.ID, provider.DiscoveryResult{Models: []string{"different"}, Pages: 1}, ""); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("conflicting completion replaced terminal fact: %v", err)
	}
	view, err := c.ModelDiscovery(f.ctx, f.channel, target.ID)
	if err != nil || view.Status != "completed" || view.Stale || len(view.Models) != 2 || view.Models[0] != "new-model" || len(view.Present) != 1 || len(view.Missing) != 0 || len(view.Unbound) != 1 || view.Unbound[0] != "new-model" {
		t.Fatalf("app role cannot read discovery/difference: %+v %v", view, err)
	}
	var starts, results, startAudits, completionAudits int
	if err = f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM channel_model_discoveries WHERE id=$1),(SELECT count(*) FROM channel_model_discovery_results WHERE discovery_id=$1),(SELECT count(*) FROM audit_events WHERE target=$1 AND action='channel.discovery.start'),(SELECT count(*) FROM audit_events WHERE target=$1 AND action='channel.discovery.completed')`, target.ID).Scan(&starts, &results, &startAudits, &completionAudits); err != nil || starts != 1 || results != 1 || startAudits != 1 || completionAudits != 1 {
		t.Fatalf("discovery replay duplicated facts/audit: %d %d %d %d %v", starts, results, startAudits, completionAudits, err)
	}

	for _, sql := range []string{
		`UPDATE channel_model_discoveries SET format='openai' WHERE id=$1`,
		`DELETE FROM channel_model_discoveries WHERE id=$1`,
		`UPDATE channel_model_discovery_results SET pages=1 WHERE discovery_id=$1`,
		`DELETE FROM channel_model_discovery_results WHERE discovery_id=$1`,
	} {
		_, err = f.app.Exec(f.ctx, sql, target.ID)
		requireDiscoverySQLState(t, err, "42501")
		_, err = f.owner.Exec(f.ctx, sql, target.ID)
		requireDiscoverySQLState(t, err, "P0001")
	}
	for _, sql := range []string{
		`SELECT id FROM channel_model_discoveries`,
		`SELECT discovery_id FROM channel_model_discovery_results`,
		`INSERT INTO channel_model_discoveries(id,actor,channel_id,channel_version_id,credential_id,format,bindings) SELECT id,actor,channel_id,channel_version_id,credential_id,format,bindings FROM channel_model_discoveries WHERE false`,
		`INSERT INTO channel_model_discovery_results(discovery_id,status,model_ids,pages) SELECT discovery_id,status,model_ids,pages FROM channel_model_discovery_results WHERE false`,
	} {
		_, err = f.projection.Exec(f.ctx, sql)
		requireDiscoverySQLState(t, err, "42501")
	}
}

func TestProvisionedDiscoveryCompositeIdentityCannotCrossChannelOrCredential(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	versionA, credentialA := discoveryVersion(t, f, f.channel)
	channelB, err := c.SaveChannel(f.ctx, f.operator, "", catalog.ChannelInput{Name: "other-discovery-channel", BaseURL: "https://example.net/v1", Secret: "other-channel-secret"})
	if err != nil {
		t.Fatal(err)
	}
	versionB, credentialB := discoveryVersion(t, f, channelB)
	if _, err = c.SaveChannel(f.ctx, f.operator, f.channel, catalog.ChannelInput{Secret: "rotated-channel-secret"}); err != nil {
		t.Fatal(err)
	}
	rotatedVersion, rotatedCredential := discoveryVersion(t, f, f.channel)
	for _, identity := range []struct {
		name, channel, version, credential string
	}{
		{"wrong-channel-for-version", channelB, versionA, credentialA},
		{"other-channel-version", f.channel, versionB, credentialB},
		{"same-channel-new-credential-old-version", f.channel, versionA, rotatedCredential},
		{"same-channel-old-credential-new-version", f.channel, rotatedVersion, credentialA},
		{"other-channel-credential", f.channel, versionA, credentialB},
	} {
		t.Run(identity.name, func(t *testing.T) {
			_, err := f.app.Exec(f.ctx, `INSERT INTO channel_model_discoveries(id,actor,channel_id,channel_version_id,credential_id,format,bindings) VALUES($1,$2,$3,$4,$5,'openai','[]')`, uuid.NewString(), f.operator, identity.channel, identity.version, identity.credential)
			requireDiscoverySQLState(t, err, "23503")
		})
	}
	var count int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM channel_model_discoveries`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid identity left discovery facts: %d %v", count, err)
	}
	// Both complete immutable identities remain valid database references after
	// rotation. The Catalog admission check separately rejects an old version.
	for _, identity := range []struct{ version, credential string }{{versionA, credentialA}, {rotatedVersion, rotatedCredential}} {
		if _, err = f.app.Exec(f.ctx, `INSERT INTO channel_model_discoveries(id,actor,channel_id,channel_version_id,credential_id,format,bindings) VALUES($1,$2,$3,$4,$5,'openai','[]')`, uuid.NewString(), f.operator, f.channel, identity.version, identity.credential); err != nil {
			t.Fatalf("complete immutable identity was rejected: %v", err)
		}
	}
}

func TestProvisionedDiscoveryAuditFailureRollsBackStartAndTerminalFacts(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	version, _ := discoveryVersion(t, f, f.channel)
	_, err := f.owner.Exec(f.ctx, `CREATE FUNCTION fail_discovery_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action IN ('channel.discovery.start','channel.discovery.completed') THEN RAISE EXCEPTION 'injected discovery audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_discovery_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_discovery_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	target, err := c.BeginModelDiscovery(f.ctx, f.operator, f.channel, version, provider.DiscoveryFormatOpenAI)
	requireDiscoverySQLState(t, err, "P0001")
	if target.ID != "" {
		t.Fatal("failed start exposed a committed discovery identity")
	}
	var starts, audits int
	if err = f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM channel_model_discoveries),(SELECT count(*) FROM audit_events WHERE action='channel.discovery.start')`).Scan(&starts, &audits); err != nil || starts != 0 || audits != 0 {
		t.Fatalf("failed start did not roll back with audit: %d %d %v", starts, audits, err)
	}
	if _, err = f.owner.Exec(f.ctx, `DROP TRIGGER fail_discovery_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	target, err = c.BeginModelDiscovery(f.ctx, f.operator, f.channel, version, provider.DiscoveryFormatOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `CREATE TRIGGER fail_discovery_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_discovery_audit()`); err != nil {
		t.Fatal(err)
	}
	result := provider.DiscoveryResult{Models: []string{"upstream"}, Pages: 1}
	err = c.FinishModelDiscovery(f.ctx, target.ID, result, "")
	requireDiscoverySQLState(t, err, "P0001")
	var results, terminalAudits int
	if err = f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM channel_model_discovery_results WHERE discovery_id=$1),(SELECT count(*) FROM audit_events WHERE target=$1 AND action='channel.discovery.completed')`, target.ID).Scan(&results, &terminalAudits); err != nil || results != 0 || terminalAudits != 0 {
		t.Fatalf("terminal result survived failed audit: %d %d %v", results, terminalAudits, err)
	}
	if _, err = f.owner.Exec(f.ctx, `DROP TRIGGER fail_discovery_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	if err = c.FinishModelDiscovery(f.ctx, target.ID, result, ""); err != nil {
		t.Fatalf("result could not complete after audit fault ended: %v", err)
	}
	if err = f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM channel_model_discovery_results WHERE discovery_id=$1),(SELECT count(*) FROM audit_events WHERE target=$1 AND action='channel.discovery.completed')`, target.ID).Scan(&results, &terminalAudits); err != nil || results != 1 || terminalAudits != 1 {
		t.Fatalf("completion/audit were not unique after rollback: %d %d %v", results, terminalAudits, err)
	}
}
