package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func releaseInput(t *testing.T, f *credentialPoolFixture) catalog.ReleaseEvidenceInput {
	t.Helper()
	pool := f.pool(t, "")
	var profile string
	if err := f.owner.QueryRow(f.ctx, `SELECT profile_version FROM models WHERE id=$1`, f.model).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	credentials := []string{}
	for _, member := range pool.Members {
		credentials = append(credentials, member.CredentialID)
	}
	return catalog.ReleaseEvidenceInput{
		ChannelID: f.channel, ChannelVersionID: pool.ChannelVersionID, ModelID: f.model, ProfileVersion: profile,
		UpstreamModel: "upstream", VariantKey: "default", Supplier: "fixture-supplier", Protocol: "openai_chat", Qualification: "supplier_profile_qualified",
		ObservedAt:     time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05.000000Z07:00"),
		EvidenceCommit: strings.Repeat("a", 40), EvidenceRef: "evidence/" + uuid.NewString(), EvidenceSHA256: strings.Repeat("b", 64),
		SourceMode: "reference_only", SourceRepository: "https://github.com/QuantumNous/new-api", SourceCommit: "e449cc700eb569cdfc2199cb73e97de46944f43c",
		LicenseRef: "third_party/new-api/LICENSE", NoticeRef: "third_party/new-api/NOTICE", ModificationRef: "third_party/new-api/SOURCE.md",
		BoundaryRef: "evidence/boundary", UsageRef: "evidence/usage", RetryRef: "evidence/retry", CostRef: "evidence/cost", CostStatus: "known",
		OpenIssueCodes: []string{}, CredentialIDs: credentials,
	}
}
func recordRelease(t *testing.T, f *credentialPoolFixture, in catalog.ReleaseEvidenceInput) catalog.ReleaseEvidence {
	t.Helper()
	value, err := f.store.RecordChannelProfileEvidence(f.ctx, "pool-admin", in)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func commercialPublish(f *credentialPoolFixture, expected int64, evidence string) (int64, error) {
	return f.store.PublishCommercialWithCostPolicy(f.ctx, "pool-admin", f.model, expected, []catalog.BindingInput{{ChannelID: f.channel, UpstreamModel: "upstream", Priority: 1, Weight: 1, MaxInflight: 1, ReleaseEvidenceID: evidence}}, routing.DefaultCostPolicy())
}
func releaseCode(err error) string { return catalog.ReleaseErrorCode(err) }

func TestReleaseEvidenceCommercialPublishAndRollback(t *testing.T) {
	f := credentialPoolDB(t, true)
	f.publish(t, 0)
	if _, err := commercialPublish(f, 1, ""); releaseCode(err) != "release_qualification_required" {
		t.Fatalf("missing evidence: %v", err)
	}
	mock := releaseInput(t, f)
	mock.Qualification = "mock_verified"
	mockEvidence := recordRelease(t, f, mock)
	if _, err := commercialPublish(f, 1, mockEvidence.ID); releaseCode(err) != "release_qualification_required" {
		t.Fatalf("mock was commercial: %v", err)
	}
	qualified := recordRelease(t, f, releaseInput(t, f))
	revision, err := commercialPublish(f, 1, qualified.ID)
	if err != nil || revision != 2 {
		t.Fatalf("commercial publish: %d %v", revision, err)
	}
	var intent, evidence string
	if err = f.owner.QueryRow(f.ctx, `SELECT p.release_intent,b.release_evidence_id FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id WHERE p.model_id=$1 AND p.revision=2`, f.model).Scan(&intent, &evidence); err != nil {
		t.Fatal(err)
	}
	if intent != "commercial" || evidence != qualified.ID {
		t.Fatal("release decision was not frozen")
	}
	if _, err = f.store.ActivateRouteRevision(f.ctx, "pool-admin", f.model, 2, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ActivateRouteRevision(f.ctx, "pool-admin", f.model, 1, 2); err != nil {
		t.Fatal(err)
	}
	if err = f.store.WithdrawChannelProfileEvidence(f.ctx, "pool-admin", qualified.ID, "supplier_review_closed"); err != nil {
		t.Fatal(err)
	}
	if _, err = commercialPublish(f, 2, qualified.ID); releaseCode(err) != "release_evidence_withdrawn" {
		t.Fatalf("withdrawn publish: %v", err)
	}
	if _, err = f.store.ActivateRouteRevision(f.ctx, "pool-admin", f.model, 2, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ActivateRouteRevision(f.ctx, "pool-admin", f.model, 1, 2); releaseCode(err) != "release_evidence_withdrawn" {
		t.Fatalf("withdrawn rollback: %v", err)
	}
	for _, statement := range []string{`UPDATE channel_profile_release_evidence SET supplier='changed'`, `DELETE FROM channel_profile_release_evidence_credentials`, `DELETE FROM channel_profile_release_withdrawals`} {
		if _, err = f.app.Exec(f.ctx, statement); err == nil {
			t.Fatalf("APP mutated release history: %s", statement)
		}
	}
}

func TestReleaseEvidenceCredentialCoverageAndStaleScope(t *testing.T) {
	f := credentialPoolDB(t, true)
	f.addSecondary(t)
	partial := releaseInput(t, f)
	partial.CredentialIDs = partial.CredentialIDs[:1]
	partialEvidence := recordRelease(t, f, partial)
	if _, err := commercialPublish(f, 0, partialEvidence.ID); releaseCode(err) != "release_credential_coverage" {
		t.Fatalf("partial pool: %v", err)
	}
	qualified := recordRelease(t, f, releaseInput(t, f))
	if _, err := commercialPublish(f, 0, qualified.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveChannel(f.ctx, "pool-admin", f.channel, catalog.ChannelInput{Name: "new-release-version"}); err != nil {
		t.Fatal(err)
	}
	if _, err := commercialPublish(f, 1, qualified.ID); releaseCode(err) != "release_qualification_required" {
		t.Fatalf("old version inherited: %v", err)
	}
	view, err := f.store.ChannelProfileEvidence(f.ctx, qualified.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Stale {
		t.Fatal("old channel version evidence was not stale")
	}
	if len(view.CredentialIDs) != 2 {
		t.Fatalf("coverage lost: %#v", view.CredentialIDs)
	}
	historicalInput := releaseInput(t, f)
	historicalInput.ChannelVersionID = qualified.ChannelVersionID
	historicalInput.CredentialIDs = qualified.CredentialIDs
	historicalInput.EvidenceRef = "evidence/already-historical"
	if historical := recordRelease(t, f, historicalInput); !historical.Stale {
		t.Fatal("record response hid historical scope")
	}
}

func TestReleaseEvidenceDatabaseGuardAndReadPagination(t *testing.T) {
	f := credentialPoolDB(t, true)
	qualified := recordRelease(t, f, releaseInput(t, f))
	if _, err := commercialPublish(f, 0, qualified.ID); err != nil {
		t.Fatal(err)
	}
	mock := releaseInput(t, f)
	mock.Qualification = "mock_verified"
	mockEvidence := recordRelease(t, f, mock)
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy := uuid.NewString()
	var profile, oldPolicy string
	if err = tx.QueryRow(f.ctx, `SELECT profile_version,(SELECT route_policy_version_id FROM model_bindings WHERE release_evidence_id=$2) FROM models WHERE id=$1`, f.model, qualified.ID).Scan(&profile, &oldPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO route_policy_versions(id,model_id,revision,profile_version,release_intent) VALUES($1,$2,9,$3,'commercial')`, policy, f.model, profile); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key,release_evidence_id) SELECT $1,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key,$2 FROM model_bindings WHERE route_policy_version_id=$3`, policy, mockEvidence.ID, oldPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE models SET active_revision=9 WHERE id=$1`, f.model); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil || !strings.Contains(err.Error(), "commercial route has unqualified") {
		_ = tx.Rollback(f.ctx)
		t.Fatalf("database guard accepted mock evidence: %v", err)
	}
	var active int64
	if err = f.owner.QueryRow(f.ctx, `SELECT active_revision FROM models WHERE id=$1`, f.model).Scan(&active); err != nil || active != 1 {
		t.Fatalf("guard changed active route: %d %v", active, err)
	}
	insertTx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	newModel, newProfile, newPolicy := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = insertTx.Exec(f.ctx, `INSERT INTO models(id,public_name,profile_version,active_revision) VALUES($1,$2,$3,1)`, newModel, "release-insert-guard", newProfile); err != nil {
		t.Fatal(err)
	}
	if _, err = insertTx.Exec(f.ctx, `INSERT INTO model_capability_versions(id,model_id,profile,schema_id,schema_digest,protocol,execution_kind,contract_digest,profile_digest) SELECT $2,$1,profile,schema_id,schema_digest,protocol,execution_kind,contract_digest,profile_digest FROM model_capability_versions WHERE model_id=$3 AND id=$4`, newModel, newProfile, f.model, profile); err != nil {
		t.Fatal(err)
	}
	if _, err = insertTx.Exec(f.ctx, `INSERT INTO model_capability_variants(profile_version,variant_key,parameters) SELECT $1,variant_key,parameters FROM model_capability_variants WHERE profile_version=$2`, newProfile, profile); err != nil {
		t.Fatal(err)
	}
	if _, err = insertTx.Exec(f.ctx, `INSERT INTO model_capability_bound_lines(profile_version,variant_key,side,dimension,unit,quantity) SELECT $1,variant_key,side,dimension,unit,quantity FROM model_capability_bound_lines WHERE profile_version=$2`, newProfile, profile); err != nil {
		t.Fatal(err)
	}
	if _, err = insertTx.Exec(f.ctx, `INSERT INTO route_policy_versions(id,model_id,revision,profile_version,release_intent) VALUES($1,$2,1,$3,'commercial')`, newPolicy, newModel, newProfile); err != nil {
		t.Fatal(err)
	}
	if _, err = insertTx.Exec(f.ctx, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key,release_evidence_id) SELECT $1,c.id,v.id,'upstream',1,1,1,'default',$2 FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$3`, newPolicy, mockEvidence.ID, f.channel); err != nil {
		t.Fatal(err)
	}
	if err = insertTx.Commit(f.ctx); err == nil || !strings.Contains(err.Error(), "commercial route has unqualified") {
		_ = insertTx.Rollback(f.ctx)
		t.Fatalf("INSERT bypassed commercial guard: %v", err)
	}
	list, next, err := f.store.ListChannelProfileEvidence(f.ctx, f.channel, f.model, "")
	if err != nil || next != "" || len(list) != 2 {
		t.Fatalf("list: %d %q %v", len(list), next, err)
	}
	filtered, _, err := f.store.ListChannelProfileEvidence(f.ctx, "", uuid.NewString(), "")
	if err != nil || len(filtered) != 0 {
		t.Fatalf("model filter: %v", err)
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE channel_profile_release_evidence SET supplier='x'`); err == nil {
		t.Fatal("APP update grant")
	}
	var projectorRead error
	cfg := f.owner.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{f.projector}.Sanitize())
		return err
	}
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, projectorRead = pool.Exec(f.ctx, `SELECT count(*) FROM channel_profile_release_evidence`)
	if projectorRead == nil {
		t.Fatal("projector read release evidence")
	}
	bad := releaseInput(t, f)
	bad.EvidenceRef = "https://user:secret@example.invalid/private"
	if _, err = f.store.RecordChannelProfileEvidence(f.ctx, "pool-admin", bad); releaseCode(err) != "release_evidence_invalid" {
		t.Fatalf("secret URL accepted: %v", err)
	}
	for _, repository := range []string{"https://example.com:8443/org/repo", "https://github.com"} {
		bad = releaseInput(t, f)
		bad.SourceRepository = repository
		if _, err = f.store.RecordChannelProfileEvidence(f.ctx, "pool-admin", bad); releaseCode(err) != "release_evidence_invalid" {
			t.Fatalf("repository grammar mismatch %q: %v", repository, err)
		}
	}
}

func TestReleaseEvidenceExternalCommercialUnsupported(t *testing.T) {
	f := credentialPoolDB(t, true)
	external, err := f.store.SaveChannel(f.ctx, "pool-admin", "", catalog.ChannelInput{Name: "external-release", BaseURL: "https://example.com/v1", Secret: "external-release-secret", ChannelKind: "new_api"})
	if err != nil {
		t.Fatal(err)
	}
	var version, credential string
	if err = f.owner.QueryRow(f.ctx, `SELECT v.id,(SELECT credential_id FROM channel_version_credentials WHERE channel_version_id=v.id) FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$1`, external).Scan(&version, &credential); err != nil {
		t.Fatal(err)
	}
	input := releaseInput(t, f)
	input.ChannelID = external
	input.ChannelVersionID = version
	input.CredentialIDs = []string{credential}
	input.EvidenceRef = "evidence/external-unsupported"
	evidence := recordRelease(t, f, input)
	_, err = f.store.PublishCommercialWithCostPolicy(f.ctx, "pool-admin", f.model, 0, []catalog.BindingInput{{ChannelID: external, UpstreamModel: "upstream", Priority: 1, Weight: 1, MaxInflight: 1, ReleaseEvidenceID: evidence.ID}}, routing.DefaultCostPolicy())
	if releaseCode(err) != "release_external_commercial_unsupported" {
		t.Fatalf("external commercial: %v", err)
	}
}

func TestReleaseEvidenceRejectsUnknownCommercialFields(t *testing.T) {
	var input catalog.RoutePublishInput
	if err := json.Unmarshal([]byte(`{"expected_revision":0,"bindings":[],"release_intent":"commercial","extra":true}`), &input); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("unknown field: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"expected_revision":0,"bindings":[]}`), &input); err != nil || input.ReleaseIntent != catalog.ReleaseTestOnly {
		t.Fatalf("default intent: %#v %v", input, err)
	}
}
