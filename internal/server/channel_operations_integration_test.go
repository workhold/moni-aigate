package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

type channelOperationHTTPFixture struct {
	*gatewayFixture
	actor, session, csrf string
	channels             []string
}

func newChannelOperationHTTPFixture(t *testing.T) *channelOperationHTTPFixture {
	t.Helper()
	f := &channelOperationHTTPFixture{gatewayFixture: newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t), gatewaySuccess(t)}, nil)}
	f.channels = probeChannels(t, f.gatewayFixture)
	f.session, f.csrf = probeAdmin(t, f.gatewayFixture)
	if err := f.pool.QueryRow(context.Background(), "SELECT id FROM users WHERE platform_role='admin'").Scan(&f.actor); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *channelOperationHTTPFixture) input(t *testing.T, action string) catalog.ChannelOperationInput {
	t.Helper()
	in := catalog.ChannelOperationInput{Action: action}
	for _, id := range f.channels {
		v := catalog.ChannelOperationTarget{ChannelID: id}
		if err := f.pool.QueryRow(context.Background(), "SELECT current_version,management_revision::text FROM channels WHERE id=$1", id).Scan(&v.ExpectedVersion, &v.ExpectedRevision); err != nil {
			t.Fatal(err)
		}
		in.Targets = append(in.Targets, v)
	}
	return in
}

func channelOperationJSON(t *testing.T, id string, in catalog.ChannelOperationInput) string {
	t.Helper()
	v := map[string]any{"action": in.Action, "targets": in.Targets}
	if id != "" {
		v["operation_id"] = id
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (f *channelOperationHTTPFixture) call(t *testing.T, method, path, raw, session, csrf string) (int, []byte) {
	t.Helper()
	d := &discoveryFixture{gatewayFixture: f.gatewayFixture}
	return d.call(t, method, "/api/platform/channel-operations"+path, raw, session, csrf)
}

func channelOperationReceipt(t *testing.T, raw []byte) catalog.ChannelOperationReceipt {
	t.Helper()
	var envelope struct {
		Data catalog.ChannelOperationReceipt `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data.OperationID == "" {
		t.Fatalf("invalid operation receipt %s %v", raw, err)
	}
	return envelope.Data
}

func (f *channelOperationHTTPFixture) apply(t *testing.T, id string, in catalog.ChannelOperationInput) catalog.ChannelOperationReceipt {
	t.Helper()
	status, raw := f.call(t, "POST", "", channelOperationJSON(t, id, in), f.session, f.csrf)
	if status != 200 {
		t.Fatalf("apply HTTP %d %s", status, raw)
	}
	return channelOperationReceipt(t, raw)
}

func TestChannelOperationsHTTPPreviewAtomicLiveGateAndPermanentReceipts(t *testing.T) {
	f := newChannelOperationHTTPFixture(t)
	ctx := context.Background()
	in := f.input(t, "disable")
	status, raw := f.call(t, "POST", "/preflight", channelOperationJSON(t, "", in), f.session, f.csrf)
	var preview struct {
		Data catalog.ChannelOperationPreview `json:"data"`
	}
	if status != 200 || json.Unmarshal(raw, &preview) != nil || !preview.Data.CanApply || preview.Data.ChangedCount != 2 || len(preview.Data.Items) != 2 {
		t.Fatalf("invalid preflight %d %s", status, raw)
	}
	for _, item := range preview.Data.Items {
		if item.Status != "ready" || item.PublishedBindings != 1 || item.OlderVersionBindings != 0 || item.LiveCredentials != 1 || item.Before == nil || !item.Before.Enabled {
			t.Fatalf("preflight omitted live-state caveats %+v", item)
		}
	}
	var receipts, batchAudits int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM channel_batch_operations),(SELECT count(*) FROM audit_events WHERE action LIKE 'channel.batch.%')").Scan(&receipts, &batchAudits); err != nil || receipts != 0 || batchAudits != 0 {
		t.Fatal("preflight wrote operational facts", receipts, batchAudits, err)
	}
	probeAssertWalletUntouched(t, f.gatewayFixture)
	id := uuid.NewString()
	disabled := f.apply(t, id, in)
	if disabled.Status != "applied" || disabled.ChangedCount != 2 || disabled.TargetCount != 2 {
		t.Fatalf("disable was not atomic %+v", disabled)
	}
	for _, item := range disabled.Items {
		if item.Status != "changed" || item.After == nil || item.After.Enabled || item.After.Quarantined || item.Before.CurrentVersion != item.After.CurrentVersion {
			t.Fatalf("disable changed configuration or quarantine %+v", item)
		}
		before, _ := strconv.ParseInt(item.Before.ManagementRevision, 10, 64)
		after, _ := strconv.ParseInt(item.After.ManagementRevision, 10, 64)
		if after != before+1 {
			t.Fatal("disable did not advance management revision")
		}
	}
	r := f.request(t, ctx, false, false)
	_, _ = io.Copy(io.Discard, r.Body)
	if r.StatusCode != 503 || f.posts.Load() != 0 {
		t.Fatal("disabled channel reached provider", r.StatusCode, f.posts.Load())
	}
	probeAssertWalletUntouched(t, f.gatewayFixture)
	in.Targets[0], in.Targets[1] = in.Targets[1], in.Targets[0]
	replayed := f.apply(t, id, in)
	if !reflect.DeepEqual(disabled, replayed) {
		t.Fatal("reordered replay did not return original receipt")
	}
	stale := f.apply(t, uuid.NewString(), in)
	if stale.Status != "rejected" || stale.ChangedCount != 0 {
		t.Fatal("stale state was applied", stale.Status)
	}
	status, _ = f.call(t, "POST", "", channelOperationJSON(t, id, f.input(t, "enable")), f.session, f.csrf)
	if status != 409 {
		t.Fatal("operation ID reused with another command", status)
	}
	enabled := f.apply(t, uuid.NewString(), f.input(t, "enable"))
	if enabled.Status != "applied" || enabled.ChangedCount != 2 {
		t.Fatal("channel did not resume its existing route", enabled.Status)
	}
	for i := range enabled.Items {
		if enabled.Items[i].After.CurrentVersion != disabled.Items[i].Before.CurrentVersion {
			t.Fatal("batch changed frozen configuration")
		}
	}
	r = f.request(t, ctx, false, false)
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || f.posts.Load() != 1 {
		t.Fatalf("existing route not restored %d %s", r.StatusCode, body)
	}
	f.assertSettled(t, r.Header.Get("X-Request-ID"), 1)
	var routeRevision, versions, members int
	if err := f.pool.QueryRow(ctx, "SELECT active_revision,(SELECT count(*) FROM channel_versions),(SELECT count(*) FROM channel_version_credentials) FROM models WHERE id=$1", f.model).Scan(&routeRevision, &versions, &members); err != nil || routeRevision != 1 || versions != 2 || members != 2 {
		t.Fatal("status operation republished routes or cloned credentials", routeRevision, versions, members, err)
	}
	beforeNoop := f.input(t, "enable")
	noop := f.apply(t, uuid.NewString(), beforeNoop)
	if noop.Status != "applied" || noop.ChangedCount != 0 || !reflect.DeepEqual(beforeNoop, f.input(t, "enable")) {
		t.Fatal("unchanged batch manufactured a state revision")
	}
	status, raw = f.call(t, "GET", "/"+id, "", f.session, f.csrf)
	if status != 200 || !reflect.DeepEqual(disabled, channelOperationReceipt(t, raw)) {
		t.Fatal("history was overwritten by current state")
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target=$1 AND action='channel.batch.apply'", id).Scan(&batchAudits); err != nil || batchAudits != 1 {
		t.Fatal("replay duplicated audit", batchAudits, err)
	}
	retrievalResourcesReleased(t, f.gatewayFixture)
}

func TestChannelOperationsHTTPMixedRejectionsHaveNoPartialChanges(t *testing.T) {
	for _, condition := range []string{"quarantine", "revoked", "version", "missing"} {
		t.Run(condition, func(t *testing.T) {
			f := newChannelOperationHTTPFixture(t)
			f.apply(t, uuid.NewString(), f.input(t, "disable"))
			in := f.input(t, "enable")
			reason := ""
			switch condition {
			case "quarantine":
				authExec(t, f.pool, "UPDATE channels SET quarantined=true WHERE id=$1", f.channels[0])
				in, reason = f.input(t, "enable"), "channel_quarantined"
			case "revoked":
				authExec(t, f.pool, "UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE channel_id=$1", f.channels[0])
				reason = "no_live_credential"
			case "version":
				if _, err := f.server.Catalog.SaveChannel(context.Background(), f.actor, f.channels[0], catalog.ChannelInput{Name: "changed metadata"}); err != nil {
					t.Fatal(err)
				}
				reason = "stale_version"
			case "missing":
				in.Targets = append(in.Targets, catalog.ChannelOperationTarget{ChannelID: uuid.NewString(), ExpectedVersion: uuid.NewString(), ExpectedRevision: "1"})
				reason = "not_found"
			}
			r := f.apply(t, uuid.NewString(), in)
			var blocked, notApplied int
			for _, item := range r.Items {
				if item.Status == "blocked" && item.Reason == reason {
					blocked++
				}
				if item.Status == "not_applied" {
					notApplied++
				}
			}
			var enabled int
			if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM channels WHERE enabled").Scan(&enabled); err != nil || enabled != 0 || r.Status != "rejected" || r.ChangedCount != 0 || blocked != 1 || notApplied == 0 {
				t.Fatalf("partial application of rejected batch %+v enabled=%d error=%v", r, enabled, err)
			}
			probeAssertWalletUntouched(t, f.gatewayFixture)
		})
	}
}

func TestChannelOperationsHTTPStrictInputAndExistingSecurityGates(t *testing.T) {
	f := newChannelOperationHTTPFixture(t)
	in := f.input(t, "disable")
	valid := channelOperationJSON(t, uuid.NewString(), in)
	for _, raw := range []string{
		strings.Replace(valid, `"action":`, `"ACTION":"enable","action":`, 1),
		strings.Replace(valid, `"expected_revision":"1"`, `"expected_revision":"1","EXPECTED_REVISION":"2"`, 1),
		strings.Replace(valid, `"targets":`, `"secret":"private-should-not-echo","targets":`, 1),
		strings.Replace(valid, `"expected_revision":"1"`, `"expected_revision":1`, 1),
		strings.Replace(valid, `"disable"`, `"delete"`, 1),
		strings.Replace(valid, `"targets":`, `"targets":null,"targets":`, 1),
		valid + `{}`, `null`, `[]`, strings.Repeat(" ", 65537) + valid,
	} {
		status, body := f.call(t, "POST", "", raw, f.session, f.csrf)
		if status != 400 || strings.Contains(string(body), "private-should-not-echo") {
			t.Fatalf("invalid command accepted/reflected %d %s", status, body)
		}
	}
	for _, role := range []string{"none", "finance_operator", "finance_approver"} {
		var user string
		if err := f.pool.QueryRow(context.Background(), "SELECT id FROM users WHERE platform_role=$1 LIMIT 1", role).Scan(&user); err != nil {
			t.Fatal(err)
		}
		session, csrf := authSession(t, f.server, f.pool, user, "", true, true)
		for _, operation := range []struct{ method, path, body string }{{"POST", "", valid}, {"POST", "/preflight", channelOperationJSON(t, "", in)}, {"GET", "", ""}, {"GET", "/" + uuid.NewString(), ""}} {
			status, _ := f.call(t, operation.method, operation.path, operation.body, session, csrf)
			if status != 403 {
				t.Fatal("non-admin batch access", role, status)
			}
		}
	}
	unrecent, csrf := authSession(t, f.server, f.pool, f.actor, "", true, false)
	for _, path := range []string{"", "/preflight"} {
		body := valid
		if path != "" {
			body = channelOperationJSON(t, "", in)
		}
		status, _ := f.call(t, "POST", path, body, unrecent, csrf)
		if status != 403 {
			t.Fatal("batch skipped reauthentication", status)
		}
		status, _ = f.call(t, "POST", path, body, f.session, "wrong")
		if status != 403 {
			t.Fatal("batch skipped CSRF", status)
		}
		f.server.Config.RecoveryMode = true
		status, _ = f.call(t, "POST", path, body, f.session, f.csrf)
		f.server.Config.RecoveryMode = false
		if status != 503 {
			t.Fatal("batch skipped recovery gate", status)
		}
	}
	f.server.Ready(false)
	status, _ := f.call(t, "POST", "", valid, f.session, f.csrf)
	f.server.Ready(true)
	if status != 503 {
		t.Fatal("batch skipped readiness", status)
	}
	user := uuid.NewString()
	authExec(t, f.pool, "INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused','admin')", user, user+"@batch.invalid")
	optional, optionalCSRF := authSession(t, f.server, f.pool, user, "", false, true)
	status, _ = f.call(t, "POST", "", valid, optional, optionalCSRF)
	if status != 200 {
		t.Fatal("optional MFA became mandatory", status)
	}
	status, _ = f.call(t, "POST", "", valid, f.session, f.csrf)
	if status != 409 {
		t.Fatal("another actor reused operation ID", status)
	}
	probeAssertWalletUntouched(t, f.gatewayFixture)
}

func TestChannelOperationsHTTPHistoryUsesBoundedSafeCursorSummaries(t *testing.T) {
	f := newChannelOperationHTTPFixture(t)
	in := f.input(t, "enable")
	for range 51 {
		if receipt, err := f.server.Catalog.ApplyChannelOperation(context.Background(), f.actor, uuid.NewString(), in); err != nil || receipt.Status != "applied" || receipt.ChangedCount != 0 {
			t.Fatal("history fixture", receipt.Status, err)
		}
	}
	status, raw := f.call(t, "GET", "", "", f.session, f.csrf)
	var page struct {
		Data       []map[string]any `json:"data"`
		NextCursor string           `json:"next_cursor"`
	}
	if status != 200 || json.Unmarshal(raw, &page) != nil || len(page.Data) != 50 || page.NextCursor == "" || strings.Contains(string(raw), `"items"`) || strings.Contains(string(raw), "input_digest") || strings.Contains(string(raw), "local-fixture-secret") {
		t.Fatalf("unsafe/unbounded history %d %s", status, raw)
	}
	status, raw = f.call(t, "GET", "?cursor="+page.NextCursor, "", f.session, f.csrf)
	if status != 200 || json.Unmarshal(raw, &page) != nil || len(page.Data) != 1 {
		t.Fatal("history cursor lost a receipt", status, string(raw))
	}
	status, _ = f.call(t, "GET", "/"+uuid.NewString(), "", f.session, f.csrf)
	if status != 404 {
		t.Fatal("missing receipt not distinguished", status)
	}
	status, _ = f.call(t, "GET", "/invalid-operation-id", "", f.session, f.csrf)
	if status != 400 {
		t.Fatal("invalid operation ID accepted", status)
	}
	status, _ = f.call(t, "GET", "?cursor=invalid", "", f.session, f.csrf)
	if status != 400 {
		t.Fatal("invalid history cursor accepted", status)
	}
	probeAssertWalletUntouched(t, f.gatewayFixture)
}
