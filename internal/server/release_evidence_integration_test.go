package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func httpReleaseInput(t *testing.T, f *discoveryFixture) catalog.ReleaseEvidenceInput {
	t.Helper()
	var profile, credential string
	if err := f.pool.QueryRow(context.Background(), `SELECT profile_version FROM models WHERE id=$1`, f.model).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT credential_id FROM channel_version_credentials WHERE channel_version_id=$1`, f.version).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	return catalog.ReleaseEvidenceInput{ChannelID: f.channel, ChannelVersionID: f.version, ModelID: f.model, ProfileVersion: profile, UpstreamModel: "provider-model", VariantKey: "default", Supplier: "fixture-supplier", Protocol: "openai_chat", Qualification: "supplier_profile_qualified", ObservedAt: time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05.000000Z07:00"), EvidenceCommit: strings.Repeat("a", 40), EvidenceRef: "evidence/http-fixture", EvidenceSHA256: strings.Repeat("b", 64), SourceMode: "reference_only", SourceRepository: "https://github.com/QuantumNous/new-api", SourceCommit: "e449cc700eb569cdfc2199cb73e97de46944f43c", LicenseRef: "third_party/new-api/LICENSE", NoticeRef: "third_party/new-api/NOTICE", ModificationRef: "third_party/new-api/SOURCE.md", BoundaryRef: "evidence/boundary", UsageRef: "evidence/usage", RetryRef: "evidence/retry", CostRef: "evidence/cost", CostStatus: "known", OpenIssueCodes: []string{}, CredentialIDs: []string{credential}}
}
func releaseBody(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func releaseEnvelope(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("invalid envelope %s %v", raw, err)
	}
	return value
}

func TestReleaseEvidenceHTTPAdminReadAndCommercialPublish(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) })
	input := httpReleaseInput(t, f)
	status, raw := f.call(t, "GET", "/api/platform/release-evidence?channel_id="+f.channel, "", f.session, f.csrf)
	if status != 200 || string(raw) == "" || !strings.Contains(string(raw), `"data":[]`) {
		t.Fatalf("empty read: %d %s", status, raw)
	}
	status, raw = f.call(t, "POST", "/api/platform/release-evidence", releaseBody(t, input), f.session, "")
	if status != 403 || !strings.Contains(string(raw), "csrf_invalid") {
		t.Fatalf("missing CSRF: %d %s", status, raw)
	}
	unknown := releaseBody(t, input)
	unknown = strings.TrimSuffix(unknown, "}") + `,"extra":true}`
	status, raw = f.call(t, "POST", "/api/platform/release-evidence", unknown, f.session, f.csrf)
	if status != 400 || !strings.Contains(string(raw), "release_evidence_invalid") {
		t.Fatalf("unknown field: %d %s", status, raw)
	}
	status, raw = f.call(t, "POST", "/api/platform/release-evidence", releaseBody(t, input), f.session, f.csrf)
	if status != 200 {
		t.Fatalf("record: %d %s", status, raw)
	}
	id := releaseEnvelope(t, raw)["data"].(map[string]any)["id"].(string)
	for _, path := range []string{"/api/platform/release-evidence/" + id, "/api/platform/release-evidence?channel_id=" + f.channel} {
		status, raw = f.call(t, "GET", path, "", f.session, f.csrf)
		if status != 200 {
			t.Fatalf("read %s: %d %s", path, status, raw)
		}
		for _, canary := range []string{"directory-secret", "base_url", "allowed_cidrs", "secret_ciphertext", "payload"} {
			if strings.Contains(string(raw), canary) {
				t.Fatalf("read leaked %s: %s", canary, raw)
			}
		}
	}
	body := fmt.Sprintf(`{"expected_revision":2,"release_intent":"commercial","bindings":[{"channel_id":%q,"upstream_model":"provider-model","priority":1,"weight":1,"max_inflight":2,"release_evidence_id":%q}]}`, f.channel, id)
	status, raw = f.call(t, "PUT", "/api/platform/models/"+f.model+"/bindings", body, f.session, f.csrf)
	if status != 200 {
		t.Fatalf("commercial publish: %d %s", status, raw)
	}
	var intent string
	if err := f.pool.QueryRow(context.Background(), `SELECT release_intent FROM route_policy_versions WHERE model_id=$1 AND revision=3`, f.model).Scan(&intent); err != nil || intent != "commercial" {
		t.Fatalf("intent: %s %v", intent, err)
	}
	status, raw = f.call(t, "POST", "/api/platform/release-evidence/"+id+"/withdraw", `{"reason_code":"supplier_review_closed"}`, f.session, f.csrf)
	if status != 200 {
		t.Fatalf("withdraw: %d %s", status, raw)
	}
	status, raw = f.call(t, "POST", "/api/platform/release-evidence/"+id+"/withdraw", `{"reason_code":"supplier_review_closed"}`, f.session, f.csrf)
	if status != 409 {
		t.Fatalf("repeat withdraw: %d %s", status, raw)
	}
	status, raw = f.call(t, "GET", "/api/platform/release-evidence/"+id, "", f.session, f.csrf)
	if status != 200 || !strings.Contains(string(raw), `"withdrawn":true`) {
		t.Fatalf("withdrawn read: %d %s", status, raw)
	}
	if f.gets.Load() != 0 {
		t.Fatal("release evidence triggered supplier I/O")
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE users SET platform_role='finance_operator' WHERE id=$1`, f.actor); err != nil {
		t.Fatal(err)
	}
	status, raw = f.call(t, "GET", "/api/platform/release-evidence/"+id, "", f.session, f.csrf)
	if status != 403 {
		t.Fatalf("finance read: %d %s", status, raw)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE users SET platform_role='admin' WHERE id=$1`, f.actor); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseEvidenceHTTPRecoveryAndDefaultIntent(t *testing.T) {
	f := newDiscoveryFixture(t, "openai", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) })
	input := httpReleaseInput(t, f)
	f.server.Config.RecoveryMode = true
	status, raw := f.call(t, "POST", "/api/platform/release-evidence", releaseBody(t, input), f.session, f.csrf)
	if status != 503 || !strings.Contains(string(raw), "recovery_mode") {
		t.Fatalf("recovery write: %d %s", status, raw)
	}
	f.server.Config.RecoveryMode = false
	status, raw = f.call(t, "PUT", "/api/platform/models/"+f.model+"/bindings", fmt.Sprintf(`{"expected_revision":2,"bindings":[{"channel_id":%q,"upstream_model":"provider-model","priority":1,"weight":1,"max_inflight":2}]}`, f.channel), f.session, f.csrf)
	if status != 200 {
		t.Fatalf("default test-only publish: %d %s", status, raw)
	}
	var intent string
	if err := f.pool.QueryRow(context.Background(), `SELECT release_intent FROM route_policy_versions WHERE model_id=$1 AND revision=3`, f.model).Scan(&intent); err != nil || intent != "test_only" {
		t.Fatalf("default intent: %s %v", intent, err)
	}
}
