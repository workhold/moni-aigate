package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/mockpsp"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type httpActor struct{ cookie, csrf string }
type httpResult struct {
	status  int
	header  http.Header
	body    []byte
	cookies []*http.Cookie
}

func (s *processSession) request(ctx context.Context, method, target string, body any, actor *httpActor, token string, signed *paymentproto.SignedHeaders) (httpResult, error) {
	var raw []byte
	if body != nil {
		if b, ok := body.([]byte); ok {
			raw = b
		} else {
			var err error
			raw, err = json.Marshal(body)
			if err != nil {
				return httpResult{}, failure("request_encoding_failed")
			}
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(raw))
	if err != nil {
		return httpResult{}, failure("request_invalid")
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", s.env.ReceiverOrigin)
	}
	if actor != nil {
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: actor.cookie})
		if method != http.MethodGet {
			req.Header.Set("X-CSRF-Token", actor.csrf)
		}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if signed != nil {
		req.Header.Set("X-Moni-Key-Id", signed.KeyID)
		req.Header.Set("X-Moni-Delivery-Id", signed.DeliveryID)
		req.Header.Set("X-Moni-Delivery-Time", signed.DeliveryTime)
		req.Header.Set("X-Moni-Signature", signed.Signature)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return httpResult{}, failure("http_transport_failed")
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 131073))
	if err != nil || len(b) > 131072 || res.ProtoMajor != 1 || res.ProtoMinor != 1 {
		return httpResult{}, failure("http_response_invalid")
	}
	return httpResult{status: res.StatusCode, header: res.Header.Clone(), body: b, cookies: res.Cookies()}, nil
}
func (s *processSession) api(method, path string, body any, actor *httpActor) (httpResult, error) {
	for attempt := 0; ; attempt++ {
		res, err := s.request(s.ctx, method, s.env.ReceiverOrigin+path, body, actor, "", nil)
		if err != nil || res.status != http.StatusTooManyRequests || attempt == 3 || res.header.Get("Retry-After") != "1" {
			return res, err
		}
		// A definite admission denial may retry only the identical request and
		// operation; no business identifier is regenerated at this boundary.
		timer := time.NewTimer(1050 * time.Millisecond)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return httpResult{}, failure("qa_cancelled")
		case <-timer.C:
		}
	}
}
func responseData(res httpResult, destination any) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(res.body, &envelope) != nil || len(envelope.Data) == 0 || json.Unmarshal(envelope.Data, destination) != nil {
		return failure("api_envelope_invalid")
	}
	return nil
}
func errorCode(res httpResult) string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(res.body, &e)
	return e.Error.Code
}
func (s *processSession) login(actor string) (*httpActor, error) {
	email, password, err := s.env.Credentials(actor)
	if err != nil {
		return nil, failure("qa_actor_failed")
	}
	res, err := s.api(http.MethodPost, "/api/auth/login", map[string]any{"email": email, "password": password}, nil)
	if err != nil || res.status != 200 {
		return nil, failure("real_login_failed")
	}
	var flags struct {
		MFA        bool `json:"mfa_required"`
		Enrollment bool `json:"enrollment_required"`
	}
	if responseData(res, &flags) != nil || flags.MFA || flags.Enrollment {
		return nil, failure("optional_mfa_login_failed")
	}
	out := &httpActor{}
	for _, cookie := range res.cookies {
		if cookie.Name == "moni_session" {
			out.cookie = cookie.Value
		}
	}
	if out.cookie == "" {
		return nil, failure("real_session_cookie_missing")
	}
	res, err = s.api(http.MethodGet, "/api/me", nil, out)
	if err != nil || res.status != 200 {
		return nil, failure("real_session_read_failed")
	}
	var me struct {
		CSRF string `json:"csrf_token"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Tenant *struct {
			ID string `json:"id"`
		} `json:"tenant"`
	}
	if responseData(res, &me) != nil || me.CSRF == "" || me.User.ID != s.env.ActorIDs[actor] {
		return nil, failure("real_session_identity_failed")
	}
	if actor == "owner" && (me.Tenant == nil || me.Tenant.ID != s.env.ApplicationTenantID) {
		return nil, failure("real_tenant_session_failed")
	}
	out.csrf = me.CSRF
	return out, nil
}
func (s *processSession) control(in mockpsp.ControlInput) (mockpsp.ControlReply, error) {
	if in.OperationID == "" {
		in.OperationID = uuid.NewString()
	}
	res, err := s.request(s.ctx, http.MethodPost, s.env.ControlOrigin+"/_qa/v1/commands", in, nil, s.env.ControlToken(), nil)
	var reply mockpsp.ControlReply
	if err != nil || res.status != 200 || json.Unmarshal(res.body, &reply) != nil || reply.OperationID != in.OperationID || reply.Status != "accepted" || reply.Reason != "accepted" || reply.ResourceID == nil {
		return reply, failure("psp_control_not_accepted")
	}
	return reply, nil
}
func (s *processSession) createOrder() (string, error) {
	operation := uuid.NewString()
	res, err := s.api(http.MethodPost, "/api/wallet/payment-orders", map[string]any{"operation_id": operation, "sku_version_id": s.version}, s.owner)
	var receipt struct {
		Status   string  `json:"status"`
		Resource *string `json:"resource_id"`
	}
	if err != nil || res.status != 202 || responseData(res, &receipt) != nil || receipt.Status != "accepted" || receipt.Resource == nil || *receipt.Resource != operation {
		return "", failure("real_order_create_failed")
	}
	s.orders = append(s.orders, operation)
	return operation, nil
}
func (s *processSession) waitReady(order string) error {
	return waitStage(s.ctx, 65*time.Second, "checkout_not_ready", func(ctx context.Context) (bool, error) {
		var state string
		if s.appDB.QueryRow(ctx, `SELECT state FROM payment_command_outbox WHERE order_id=$1 AND kind='checkout'`, order).Scan(&state) != nil {
			return false, failure("checkout_observation_failed")
		}
		if state == "review_required" {
			return false, failure("checkout_required_review")
		}
		if state != "completed" {
			return false, nil
		}
		res, err := s.api(http.MethodGet, "/api/wallet/payment-orders/"+order, nil, s.owner)
		if err != nil {
			return false, err
		}
		var view struct {
			Checkout string `json:"checkout_state"`
			Creation string `json:"creation_state"`
		}
		if res.status != 200 || responseData(res, &view) != nil {
			return false, failure("checkout_http_view_failed")
		}
		return view.Checkout == "ready" && view.Creation == "confirmed", nil
	})
}
func (s *processSession) capture(order string) error {
	merchant := "mps1." + s.env.Protocol.ID + "." + order
	_, err := s.control(mockpsp.ControlInput{Action: "capture_order", Scenario: "normal", MerchantOrderID: &merchant})
	return err
}
func (s *processSession) waitFunded(order string) error {
	return waitStage(s.ctx, 65*time.Second, "capture_not_funded", func(ctx context.Context) (bool, error) {
		var n int
		if s.appDB.QueryRow(ctx, `SELECT count(*) FROM payment_captures c JOIN funding_receipts f ON f.source_kind='sandbox_capture' AND f.source_id=c.id JOIN journals j ON j.id=f.journal_id WHERE c.order_id=$1 AND payment_capture_source_valid(c.id) AND payment_sandbox_journal_valid(j.id)`, order).Scan(&n) != nil {
			return false, failure("funding_observation_failed")
		}
		if n == 0 {
			return false, nil
		}
		if n != 1 {
			return false, failure("duplicate_funding_graph")
		}
		return true, nil
	})
}
func (s *processSession) assertWallet(amount string) error {
	res, err := s.api(http.MethodGet, "/api/wallet", nil, s.owner)
	var wallet struct {
		Available string `json:"available"`
		Credited  string `json:"total_credited"`
	}
	if err != nil || res.status != 200 || responseData(res, &wallet) != nil || wallet.Available != amount || wallet.Credited != amount {
		return failure("wallet_http_totals_incorrect")
	}
	return nil
}
func (s *processSession) assertUniqueFunds(expected int) error {
	var captures, receipts, journals, lines, outbox, audit int
	if s.appDB.QueryRow(s.ctx, `SELECT
 (SELECT count(*) FROM payment_captures WHERE tenant_id=$1),
 (SELECT count(*) FROM funding_receipts WHERE tenant_id=$1 AND source_kind='sandbox_capture'),
 (SELECT count(*) FROM journals WHERE tenant_id=$1 AND kind='sandbox_capture'),
 (SELECT count(*) FROM journal_lines l JOIN journals j ON j.id=l.journal_id WHERE j.tenant_id=$1 AND j.kind='sandbox_capture'),
 (SELECT count(*) FROM projection_outbox WHERE tenant_id=$1 AND kind='credited'),
 (SELECT count(*) FROM audit_events WHERE tenant_scope=$1 AND action='payment.capture.credited')`, s.env.ApplicationTenantID).Scan(&captures, &receipts, &journals, &lines, &outbox, &audit) != nil || captures != expected || receipts != expected || journals != expected || lines != 2*expected || outbox != expected || audit != expected {
		return failure("funding_graph_not_unique")
	}
	return nil
}
func (s *processSession) duplicateNotification(order string) error {
	var body []byte
	var event string
	if s.mockDB.QueryRow(s.ctx, `SELECT canonical_body,event_id FROM mock_notification_events WHERE merchant_order_id=$1 AND canonical_capture_id IS NOT NULL`, "mps1."+s.env.Protocol.ID+"."+order).Scan(&body, &event) != nil {
		return failure("canonical_event_missing")
	}
	var retained []byte
	for i := 0; i < 2; i++ {
		var now time.Time
		if s.appDB.QueryRow(s.ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
			return failure("database_clock_failed")
		}
		keys, err := s.env.Keyring()
		if err != nil {
			return failure("qa_keyring_failed")
		}
		signed, err := keys.SignNotification(s.env.Protocol, paymentproto.SignedHeaders{DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, body)
		if err != nil {
			return failure("notification_sign_failed")
		}
		res, err := s.request(s.ctx, http.MethodPost, s.env.ReceiverOrigin+"/api/payments/sandbox/notifications", body, nil, "", &signed)
		if err != nil || res.status != 200 {
			return failure("duplicate_notification_failed")
		}
		ack, err := mockpsp.ParseACK(res.status, res.body, event, now.UTC())
		if err != nil {
			return failure("duplicate_ack_invalid")
		}
		data, err := ack.DataBytes()
		if err != nil {
			return failure("duplicate_ack_invalid")
		}
		if i == 0 {
			retained = data
		} else if !bytes.Equal(retained, data) {
			return failure("duplicate_ack_changed")
		}
	}
	return nil
}
func (s *processSession) replayFrozenQuery(order string) error {
	var queryID string
	var request, verified []byte
	if s.appDB.QueryRow(s.ctx, `SELECT a.query_id,a.canonical_request,v.canonical_body FROM payment_query_attempts a JOIN payment_verified_queries v ON v.query_id=a.query_id JOIN payment_command_outbox q ON q.id=a.supervisor_id WHERE q.order_id=$1 ORDER BY a.created_at DESC LIMIT 1`, order).Scan(&queryID, &request, &verified) != nil {
		return failure("actual_worker_query_missing")
	}
	for i := 0; i < 2; i++ {
		res, err := s.request(s.ctx, http.MethodPost, s.env.APIOrigin+"/v1/query", request, nil, s.env.APIToken(), nil)
		if err != nil || res.status != 200 || !bytes.Equal(verified, res.body) {
			return failure("frozen_worker_query_changed")
		}
		headers, err := paymentproto.ReadSignedHeaders(res.header)
		if err != nil {
			return failure("frozen_query_signature_missing")
		}
		q, err := paymentproto.ParseQuery(request)
		if err != nil || q.QueryID != queryID {
			return failure("actual_query_binding_failed")
		}
		var now time.Time
		if s.appDB.QueryRow(s.ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
			return failure("database_clock_failed")
		}
		keys, err := s.env.Keyring()
		if err != nil {
			return failure("qa_keyring_failed")
		}
		if _, err = keys.VerifyQuery(s.env.Protocol, q, now.UTC(), headers, res.body); err != nil {
			return failure("frozen_query_signature_invalid")
		}
	}
	return nil
}
