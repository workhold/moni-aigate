package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/reporting"
	"golang.org/x/net/websocket"
)

func TestOperatingReportPostgresCommercialFlowAndAuthorization(t *testing.T) {
	reject := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limit"}}`)
	}
	f := newGatewayFixture(t, []http.HandlerFunc{reject, gatewaySuccess(t)}, []int{429})
	ctx := context.Background()
	response := f.request(t, ctx, false, false)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || f.posts.Load() != 2 {
		t.Fatal("expected one successful request and two real attempts", response.StatusCode, f.posts.Load())
	}
	requestID := response.Header.Get("X-Request-ID")
	admin, _ := authUser(t, f.server, f.pool, "admin")
	adminSession, _ := authSession(t, f.server, f.pool, admin, "", false, true)
	var created time.Time
	var settlementID string
	var charged int64
	if err := f.pool.QueryRow(ctx, "SELECT r.created_at,s.id,s.charged_amount FROM request_records r JOIN settlements s ON s.request_id=r.id WHERE r.id=$1", requestID).Scan(&created, &settlementID, &charged); err != nil || charged != 20 {
		t.Fatal("missing real settlement", charged, err)
	}
	params := url.Values{"from": {created.Add(-time.Hour).Format(time.RFC3339Nano)}, "to": {created.Add(time.Hour).Format(time.RFC3339Nano)}}
	read := func(params url.Values, rawSession string) reporting.Report {
		t.Helper()
		w := authHTTP(t, f.server, "GET", "/api/platform/reports/operations?"+params.Encode(), rawSession, "", "", nil)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("read failed", w.Code, w.Body.String())
		}
		var envelope struct {
			Data reporting.Report `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(w.Body.String(), "local-fixture-secret") || strings.Contains(w.Body.String(), "credential_id") || strings.Contains(w.Body.String(), "hello") {
			t.Fatal("report exposed provider or request contents")
		}
		return envelope.Data
	}
	t.Run("one_sale_all_attempts_and_unknown_margin", func(t *testing.T) {
		r := read(params, adminSession)
		if r.Summary.RequestCount != 1 || r.Summary.AttemptCount != 2 || r.Summary.Charged != "20" || r.Summary.NetRevenue != "20" || r.Summary.UnknownProviderCosts == 0 || r.Summary.GrossMargin != nil || len(r.Requests) != 1 || len(r.Channels) != 2 || len(r.Models) != 1 {
			t.Fatal("incorrect report", r)
		}
	})
	t.Run("current_cost_facts_and_real_wallet_refund", func(t *testing.T) {
		var operator, approver string
		if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='finance_operator' LIMIT 1").Scan(&operator); err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='finance_approver' LIMIT 1").Scan(&approver); err != nil {
			t.Fatal(err)
		}
		rows, err := f.pool.Query(ctx, "SELECT id FROM upstream_attempts WHERE request_id=$1 ORDER BY sequence", requestID)
		if err != nil {
			t.Fatal(err)
		}
		var attempts []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			attempts = append(attempts, id)
		}
		rows.Close()
		if rows.Err() != nil || len(attempts) != 2 {
			t.Fatal("missing attempts", rows.Err())
		}
		for i, id := range attempts {
			err := f.server.Billing.RecordCostCorrection(ctx, billing.CostCorrectionInput{TenantID: f.tenant, AttemptID: id, ActorID: operator, OperationID: uuid.NewString(), EvidenceRef: "report-local-invoice", Basis: "invoice", Amount: billing.Money(7 + 4*i)})
			if err != nil {
				t.Fatal(err)
			}
		}
		ref := uuid.NewString()
		order, err := f.server.Billing.CreateRefund(ctx, billing.RefundInput{TenantID: f.tenant, SettlementID: settlementID, ActorID: operator, Amount: 5, BusinessRef: ref, Reason: "report local refund", IdempotencyKey: ref, RequestHMAC: ref})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.server.Billing.ApproveRefund(ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: approver}); err != nil {
			t.Fatal(err)
		}
		r := read(params, adminSession)
		if r.AmountUnit != "micro_coin" || r.Summary.PaymentBasis["CNY"] != "15" || len(r.Summary.CashRefunded) != 0 || r.Summary.KnownProviderCost != "18" || r.Summary.Refunded != "5" || r.Summary.NetRevenue != "15" || r.Summary.UnknownProviderCosts != 0 || r.Summary.GrossMargin == nil || *r.Summary.GrossMargin != "-3" {
			t.Fatal("refund/cost aggregation", r.Summary)
		}
	})
	t.Run("half_open_window_and_current_filters", func(t *testing.T) {
		p := maps.Clone(params)
		p.Set("from", created.Format(time.RFC3339Nano))
		p.Set("to", created.Add(time.Microsecond).Format(time.RFC3339Nano))
		if read(p, adminSession).Summary.RequestCount != 1 {
			t.Fatal("left edge excluded")
		}
		p.Set("from", created.Add(-time.Microsecond).Format(time.RFC3339Nano))
		p.Set("to", created.Format(time.RFC3339Nano))
		if read(p, adminSession).Summary.RequestCount != 0 {
			t.Fatal("right edge included")
		}
		for _, field := range []string{"tenant_id", "model_id"} {
			p := maps.Clone(params)
			p.Set(field, uuid.NewString())
			if read(p, adminSession).Summary.RequestCount != 0 {
				t.Fatal("cross filter data", field)
			}
		}
	})
	t.Run("csv_has_one_request_and_exact_micro_units", func(t *testing.T) {
		w := authHTTP(t, f.server, "GET", "/api/platform/reports/operations.csv?"+params.Encode(), adminSession, "", "", nil)
		if w.Code != 200 || w.Header().Get("Content-Type") != "text/csv; charset=utf-8" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
		header := []string{"request_id", "tenant_id", "tenant_name", "model_id", "model_name", "created_at", "status", "billing_status", "charged", "refunded", "net_revenue", "attempt_count", "known_provider_cost", "unknown_provider_costs", "gross_margin", "payment_basis_cny", "payment_basis_usd", "cash_refunded_cny", "cash_refunded_usd", "protocol", "execution_kind", "profile_version", "schema_id", "schema_digest", "variant_key", "offer_version_id", "task_class", "provider_cost_cny", "provider_cost_usd", "cost_as_of", "cost_revision", "provisional_gross_margin", "bill_confirmed_gross_margin", "gross_margin_currency", "margin_valuation", "fx_policy_versions"}
		if err != nil || len(rows) != 2 || !reflect.DeepEqual(rows[0], header) || len(rows[1]) != len(header) || rows[1][0] != requestID || rows[1][10] != "15" || rows[1][14] != "-3" || rows[1][15] != "15" || rows[1][16] != "" || rows[1][17] != "" || rows[1][18] != "" {
			t.Fatal("CSV facts", rows, err)
		}
		if rows[1][19] == "" || rows[1][20] != "synchronous" || rows[1][21] == "" || rows[1][22] == "" || len(rows[1][23]) != 64 || rows[1][24] != "default" {
			t.Fatal("CSV request metering metadata", rows[1][15:])
		}
		if rows[1][27] != "18" || rows[1][32] != "-3" || rows[1][33] != "CNY" || rows[1][34] != "original_currency" {
			t.Fatal("CSV P0 cost and bill-confirmed margin", rows[1])
		}
	})
	t.Run("platform_roles_allowed_customer_roles_denied", func(t *testing.T) {
		for _, role := range []string{"finance_operator", "finance_approver"} {
			var uid string
			if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role=$1 LIMIT 1", role).Scan(&uid); err != nil {
				t.Fatal(err)
			}
			raw, _ := authSession(t, f.server, f.pool, uid, "", false, true)
			if read(params, raw).Summary.RequestCount != 1 {
				t.Fatal(role)
			}
		}
		for _, role := range []string{"owner", "billing_viewer", "member"} {
			uid, _ := authUser(t, f.server, f.pool, "none")
			authExec(t, f.pool, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,$3)", f.tenant, uid, role)
			raw, _ := authSession(t, f.server, f.pool, uid, f.tenant, false, true)
			for _, suffix := range []string{"operations", "operations.csv"} {
				w := authHTTP(t, f.server, "GET", "/api/platform/reports/"+suffix+"?"+params.Encode(), raw, "", "", nil)
				if w.Code != 403 || strings.Contains(w.Body.String(), requestID) {
					t.Fatal("customer saw operating costs", role, w.Code, w.Body.String())
				}
			}
		}
	})
	if f.posts.Load() != 2 {
		t.Fatal("report triggered model call")
	}
}

// One real text settlement and one real realtime media turn share one
// operations response. The text row must keep its complete meter snapshot and
// the media row its explicit empty schema triple, in both JSON and CSV, with
// all 041 P0 fields intact. Media facts come from the production realtime
// lifecycle; no fixture disables or bypasses a guard to fabricate them.
func TestOperatingReportMixedMetadataRealLifecycle(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	ctx := context.Background()
	response := f.request(t, ctx, false, false)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("text request failed", response.StatusCode)
	}
	textRequestID := response.Header.Get("X-Request-ID")

	// Realtime turn through the production WebSocket flow (real media request).
	unauthorized, err := http.NewRequest(http.MethodGet, f.http.URL+"/v1/realtime?model=retail-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedResponse, err := f.http.Client().Do(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, unauthorizedResponse.Body)
	_ = unauthorizedResponse.Body.Close()
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatal("realtime boundary changed", unauthorizedResponse.StatusCode)
	}
	wsURL := "ws" + strings.TrimPrefix(f.http.URL, "http") + "/v1/realtime?model=retail-model"
	wsConfig, err := websocket.NewConfig(wsURL, "http://console.test")
	if err != nil {
		t.Fatal(err)
	}
	wsConfig.Header = http.Header{"Authorization": []string{"Bearer " + f.token}}
	conn, err := websocket.DialConfig(wsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if created := readRealtimeEvent(t, conn); created.Type != "session.created" {
		t.Fatalf("session event=%+v", created)
	}
	sendRealtimeEvent(t, conn, `{"event_id":"11111111-1111-4111-8111-111111111201","type":"input.text","data":{"text":"hello"}}`)
	if accepted := readRealtimeEvent(t, conn); accepted.Type != "event.accepted" {
		t.Fatalf("input acknowledgement=%+v", accepted)
	}
	sendRealtimeEvent(t, conn, `{"event_id":"11111111-1111-4111-8111-111111111202","type":"response.create","data":{}}`)
	for _, want := range []string{"event.accepted", "response.created", "response.delta", "response.done"} {
		if event := readRealtimeEvent(t, conn); event.Type != want {
			t.Fatalf("expected %s, got %+v", want, event)
		}
	}

	var textCreated, mediaCreated time.Time
	postsBeforeReads := f.posts.Load()
	if err = f.pool.QueryRow(ctx, "SELECT created_at FROM request_records WHERE id=$1", textRequestID).Scan(&textCreated); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, "SELECT created_at FROM media_requests WHERE tenant_id=$1 AND operation='realtime_turn'", f.tenant).Scan(&mediaCreated); err != nil {
		t.Fatal(err)
	}
	from := textCreated.Add(-time.Hour)
	to := mediaCreated.Add(time.Hour)
	params := url.Values{"from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}}
	admin, _ := authUser(t, f.server, f.pool, "admin")
	adminSession, _ := authSession(t, f.server, f.pool, admin, "", false, true)

	w := authHTTP(t, f.server, "GET", "/api/platform/reports/operations?"+params.Encode(), adminSession, "", "", nil)
	if w.Code != 200 {
		t.Fatal("read failed", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Summary reporting.Totals `json:"summary"`
			Reports []struct {
				RequestID     string `json:"request_id"`
				Protocol      string `json:"protocol"`
				ExecutionKind string `json:"execution_kind"`
				Profile       string `json:"profile_version"`
				SchemaID      string `json:"schema_id"`
				SchemaDigest  string `json:"schema_digest"`
				VariantKey    string `json:"variant_key"`
				OfferID       string `json:"offer_version_id"`
				TaskClass     string `json:"task_class"`
				Charged       string `json:"charged"`
				PaymentBasis  map[string]string `json:"payment_basis"`
				ProviderCost  map[string]string `json:"provider_cost"`
			} `json:"requests"`
		} `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Reports) != 2 || envelope.Data.Summary.RequestCount != 2 {
		t.Fatalf("mixed response did not keep both rows: %+v", envelope.Data)
	}
	text, media := envelope.Data.Reports[0], envelope.Data.Reports[1]
	if text.Protocol == "realtime_turn" {
		text, media = media, text
	}
	if text.RequestID != textRequestID || text.Protocol != "openai_chat" || text.ExecutionKind != "synchronous" ||
		text.Profile == "" || text.SchemaID == "" || len(text.SchemaDigest) != 64 || text.VariantKey != "default" || text.OfferID != "" {
		t.Fatalf("text row lost its complete snapshot: %+v", text)
	}
	if media.Protocol != "realtime_turn" || media.ExecutionKind != "synchronous" ||
		media.Profile == "" || media.SchemaID != "" || media.SchemaDigest != "" || media.VariantKey != "" || media.OfferID != "" {
		t.Fatalf("media row did not keep the explicit empty triple: %+v", media)
	}
	if _, err = uuid.Parse(media.Profile); err != nil {
		t.Fatalf("media profile is not a UUID: %q", media.Profile)
	}
	for _, row := range envelope.Data.Reports {
		if row.PaymentBasis == nil || row.ProviderCost == nil || row.Charged == "" {
			t.Fatalf("P0 amount fields missing on a row: %+v", row)
		}
	}

	csvResponse := authHTTP(t, f.server, "GET", "/api/platform/reports/operations.csv?"+params.Encode(), adminSession, "", "", nil)
	if csvResponse.Code != 200 {
		t.Fatal("csv read failed", csvResponse.Code, csvResponse.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(csvResponse.Body.String())).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("csv rows: %v, %v", rows, err)
	}
	metadata := map[string][]string{}
	for _, row := range rows[1:] {
		if len(row) != len(rows[0]) {
			t.Fatal("csv column count changed", row)
		}
		metadata[row[0]] = row[19:25]
	}
	if got := metadata[textRequestID]; len(got) != 6 || got[0] != "openai_chat" || got[1] != "synchronous" || got[2] == "" || got[3] == "" || len(got[4]) != 64 || got[5] != "default" {
		t.Fatalf("csv text metadata changed: %v", got)
	}
	var mediaID string
	if err = f.pool.QueryRow(ctx, "SELECT id FROM media_requests WHERE tenant_id=$1 AND operation='realtime_turn'", f.tenant).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	if got := metadata[mediaID]; len(got) != 6 || got[0] != "realtime_turn" || got[1] != "synchronous" || got[2] == "" || got[3] != "" || got[4] != "" || got[5] != "" {
		t.Fatalf("csv media metadata changed: %v", got)
	}
	if f.posts.Load() != postsBeforeReads {
		t.Fatal("report triggered model call")
	}
}
