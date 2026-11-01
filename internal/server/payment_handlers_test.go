package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/payments"
)

// These tests exercise transport only. The fake never claims P authenticity,
// database acceptance, runtime attestation or a real main startup.
type paymentHTTPFake struct {
	paymentService
	enabled      bool
	publish      func(context.Context, payments.PublishSKUInput) (payments.PublishSKUResult, error)
	activate     func(context.Context, payments.ActivateSKUInput) (payments.OperationReceipt, error)
	create       func(context.Context, payments.CreateOrderInput) (payments.OrderResult, error)
	cancel       func(context.Context, payments.CancelOrderInput) (payments.OperationReceipt, error)
	policy       func(context.Context, payments.UpdatePolicyInput) (payments.PolicyView, error)
	observations func(context.Context, string, string, payments.Page) (payments.PageResult[payments.ObservationView], error)
	orders       func(context.Context, payments.Reader, payments.Page) (payments.PageResult[payments.OrderView], error)
	notification func(context.Context, payments.NotificationInput) (payments.NotificationACK, bool, error)
}

func (f *paymentHTTPFake) SandboxNotificationsEnabled() bool { return f.enabled }
func (f *paymentHTTPFake) PublishSKU(ctx context.Context, in payments.PublishSKUInput) (payments.PublishSKUResult, error) {
	return f.publish(ctx, in)
}
func (f *paymentHTTPFake) ActivateSKU(ctx context.Context, in payments.ActivateSKUInput) (payments.OperationReceipt, error) {
	return f.activate(ctx, in)
}
func (f *paymentHTTPFake) CreateOrder(ctx context.Context, in payments.CreateOrderInput) (payments.OrderResult, error) {
	return f.create(ctx, in)
}
func (f *paymentHTTPFake) CancelOrder(ctx context.Context, in payments.CancelOrderInput) (payments.OperationReceipt, error) {
	return f.cancel(ctx, in)
}
func (f *paymentHTTPFake) UpdatePolicy(ctx context.Context, in payments.UpdatePolicyInput) (payments.PolicyView, error) {
	return f.policy(ctx, in)
}
func (f *paymentHTTPFake) ListPlatformObservations(ctx context.Context, actor, order string, page payments.Page) (payments.PageResult[payments.ObservationView], error) {
	return f.observations(ctx, actor, order, page)
}
func (f *paymentHTTPFake) ListWalletOrders(ctx context.Context, r payments.Reader, page payments.Page) (payments.PageResult[payments.OrderView], error) {
	return f.orders(ctx, r, page)
}
func (f *paymentHTTPFake) ReceiveNotification(ctx context.Context, in payments.NotificationInput) (payments.NotificationACK, bool, error) {
	return f.notification(ctx, in)
}
func paymentHTTPRouter(s *Server, session *identity.Session) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("request_id", "request:test")
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	a := r.Group("/api")
	a.Use(func(c *gin.Context) { c.Set("session", session); c.Next() }, s.requireMFA())
	p := a.Group("/platform")
	p.Use(s.platform())
	s.registerPayments(a, p)
	s.registerPaymentNotifications(r)
	return r
}
func paymentHTTPSession() *identity.Session {
	until := time.Now().Add(time.Minute)
	return &identity.Session{User: identity.User{ID: uuid.NewString(), PlatformRole: "admin"}, Tenant: &identity.Tenant{ID: uuid.NewString(), Role: "owner"}, ReauthenticatedUntil: &until}
}
func paymentHTTPRequest(r http.Handler, method, path, body string, headers http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if method != "GET" {
		request.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		request.Header[k] = v
	}
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}
func paymentHTTPCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var value struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if response.Code != status || json.Unmarshal(response.Body.Bytes(), &value) != nil || value.Error.Code != code || value.RequestID != "request:test" {
		t.Fatalf("HTTP %d %s expected %d %s", response.Code, response.Body.String(), status, code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable payment error")
	}
}
func TestPaymentHTTPStrictHumanInputNeverReachesDomain(t *testing.T) {
	operation, sku := uuid.NewString(), uuid.NewString()
	valid := `{"operation_id":"` + operation + `","sku_id":"` + sku + `","expected_revision":"0","display_name":"模型额度","amount":"100"}`
	for _, test := range []struct {
		name, body, path string
		headers          http.Header
		status           int
	}{
		{"duplicate", strings.Replace(valid, `"amount":"100"`, `"amount":"100","amount":"101"`, 1), "", nil, 400},
		{"unknown", strings.Replace(valid, `"amount":"100"`, `"amount":"100","tenant_id":"`+uuid.NewString()+`"`, 1), "", nil, 400},
		{"case_alias", strings.Replace(valid, "display_name", "Display_Name", 1), "", nil, 400},
		{"money_number", strings.Replace(valid, `"amount":"100"`, `"amount":100`, 1), "", nil, 400},
		{"noncanonical", strings.Replace(valid, `"amount":"100"`, `"amount":"0100"`, 1), "", nil, 400},
		{"overflow", strings.Replace(valid, `"amount":"100"`, `"amount":"9223372036854775808"`, 1), "", nil, 400},
		{"missing", strings.Replace(valid, `"expected_revision":"0",`, "", 1), "", nil, 400},
		{"null", strings.Replace(valid, `"display_name":"模型额度"`, `"display_name":null`, 1), "", nil, 400},
		{"control", strings.Replace(valid, "模型额度", `bad\nname`, 1), "", nil, 400},
		{"utf8", strings.Replace(valid, "模型额度", string([]byte{255}), 1), "", nil, 400},
		{"trailing", valid + ` {}`, "", nil, 400},
		{"body_limit", strings.Repeat(" ", 8193), "", nil, 413},
		{"query", valid, "?unexpected=1", nil, 400},
		{"force_query", valid, "?", nil, 400},
		{"media", valid, "", http.Header{"Content-Type": {"text/plain"}}, 400},
		{"charset", valid, "", http.Header{"Content-Type": {"application/json; charset=latin1"}}, 400},
		{"encoding", valid, "", http.Header{"Content-Encoding": {"gzip"}}, 400},
		{"duplicate_media", valid, "", http.Header{"Content-Type": {"application/json", "application/json"}}, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			s := &Server{Payments: &paymentHTTPFake{publish: func(context.Context, payments.PublishSKUInput) (payments.PublishSKUResult, error) {
				calls++
				return payments.PublishSKUResult{}, nil
			}}}
			r := paymentHTTPRouter(s, paymentHTTPSession())
			response := paymentHTTPRequest(r, "POST", "/api/platform/payment-skus"+test.path, test.body, test.headers)
			code := "invalid_request"
			if test.status == 413 {
				code = "body_too_large"
			}
			paymentHTTPCode(t, response, test.status, code)
			if calls != 0 {
				t.Fatal("invalid input reached domain")
			}
		})
	}
}
func TestPaymentHTTPCreateAcceptanceReplayRejectionAndUTC6(t *testing.T) {
	for _, state := range []string{"first", "replay", "rejected", "missing_order", "postcommit_failure"} {
		t.Run(state, func(t *testing.T) {
			session := paymentHTTPSession()
			operation, version := uuid.NewString(), uuid.NewString()
			called := false
			fake := &paymentHTTPFake{create: func(ctx context.Context, in payments.CreateOrderInput) (payments.OrderResult, error) {
				called = true
				if in.ActorID != session.User.ID || in.TenantID != session.Tenant.ID || in.OperationID != operation || in.SKUVersionID != version || in.RequestID != "request:test" {
					t.Fatal("transport accepted caller scope", in)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("missing transaction budget")
				}
				if state == "postcommit_failure" {
					return payments.OrderResult{}, payments.ErrPaymentsOutcomeUnknown
				}
				receipt := payments.OperationReceipt{OperationID: operation, Kind: "payment.order.create", Actor: session.User.ID, Scope: session.Tenant.ID, Status: "accepted", Reason: "accepted", ResourceID: &operation, CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 123456000, time.UTC)}
				result := payments.OrderResult{Receipt: receipt, Created: state == "first", Order: &payments.OrderView{ID: operation, Amount: 100, WalletCreditAmount: 100, CreatedAt: receipt.CreatedAt}}
				if state == "rejected" {
					result.Receipt.Status = "rejected"
					result.Receipt.Reason = "payments_disabled"
					result.Receipt.ResourceID = nil
					result.Order = nil
				}
				if state == "missing_order" {
					result.Order = nil
				}
				return result, nil
			}}
			s := &Server{Payments: fake}
			response := paymentHTTPRequest(paymentHTTPRouter(s, session), "POST", "/api/wallet/payment-orders", `{"operation_id":"`+operation+`","sku_version_id":"`+version+`"}`, nil)
			if !called {
				t.Fatal("valid create not dispatched")
			}
			if state == "postcommit_failure" || state == "missing_order" {
				paymentHTTPCode(t, response, 503, "commerce_outcome_unknown")
				return
			}
			want := 200
			if state == "first" {
				want = 202
			}
			if response.Code != want {
				t.Fatal(response.Code, response.Body.String())
			}
			var value map[string]json.RawMessage
			if json.Unmarshal(response.Body.Bytes(), &value) != nil || len(value) != (map[bool]int{true: 2, false: 3}[state == "rejected"]) {
				t.Fatal("response fields", response.Body.String())
			}
			if state == "rejected" {
				if _, ok := value["order"]; ok {
					t.Fatal("rejected order key present")
				}
			} else if !strings.Contains(string(value["order"]), `"amount":"100"`) || !strings.Contains(string(value["data"]), `2026-11-01T00:00:00.123456Z`) {
				t.Fatal("money/UTC6 wire mismatch", response.Body.String())
			}
		})
	}
}
func TestPaymentHTTPExplicitNullableVersionAndNoAutomaticMFAEnrollment(t *testing.T) {
	session := paymentHTTPSession()
	op, sku := uuid.NewString(), uuid.NewString()
	fake := &paymentHTTPFake{activate: func(_ context.Context, in payments.ActivateSKUInput) (payments.OperationReceipt, error) {
		if in.SKUID != sku || in.VersionID != nil || in.Enabled {
			t.Fatal("explicit nullable version lost", in)
		}
		return payments.OperationReceipt{OperationID: op, Status: "accepted"}, nil
	}}
	response := paymentHTTPRequest(paymentHTTPRouter(&Server{Payments: fake}, session), "PUT", "/api/platform/payment-skus/"+sku+"/active", `{"operation_id":"`+op+`","expected_revision":"2","version_id":null,"enabled":false}`, nil)
	if response.Code != 200 {
		t.Fatal("optional MFA blocked ordinary test account", response.Code, response.Body.String())
	}
	session.EnrollmentRequired = true
	response = paymentHTTPRequest(paymentHTTPRouter(&Server{Payments: fake}, session), "PUT", "/api/platform/payment-skus/"+sku+"/active", `{}`, nil)
	paymentHTTPCode(t, response, 403, "mfa_required")
	session.EnrollmentRequired = false
	session.ReauthenticatedUntil = nil
	response = paymentHTTPRequest(paymentHTTPRouter(&Server{Payments: fake}, session), "PUT", "/api/platform/payment-skus/"+sku+"/active", `{}`, nil)
	paymentHTTPCode(t, response, 403, "reauth_required")
}
func TestPaymentHTTPQueriesRolesAndLogicalOrderObservation(t *testing.T) {
	session := paymentHTTPSession()
	logical := uuid.NewString()
	calls := 0
	fake := &paymentHTTPFake{observations: func(_ context.Context, actor, order string, page payments.Page) (payments.PageResult[payments.ObservationView], error) {
		calls++
		if actor != session.User.ID || order != logical {
			t.Fatal("observation target changed")
		}
		return payments.PageResult[payments.ObservationView]{Data: []payments.ObservationView{}}, nil
	}, orders: func(_ context.Context, r payments.Reader, page payments.Page) (payments.PageResult[payments.OrderView], error) {
		calls++
		if r.ActorID != session.User.ID || r.TenantID != session.Tenant.ID {
			t.Fatal("wallet scope changed")
		}
		return payments.PageResult[payments.OrderView]{Data: []payments.OrderView{}}, nil
	}}
	response := paymentHTTPRequest(paymentHTTPRouter(&Server{Payments: fake}, session), "GET", "/api/platform/payment-orders/"+logical+"/observations", "", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"data":[]`) || !strings.Contains(response.Body.String(), `"next_cursor":null`) {
		t.Fatal("unknown logical order hidden", response.Code, response.Body.String())
	}
	for _, query := range []string{"?cursor=a&cursor=b", "?limit=1", "?cursor=", "?%zz=1", "?cursor=a;extra=b", "?"} {
		response = paymentHTTPRequest(paymentHTTPRouter(&Server{Payments: fake}, session), "GET", "/api/wallet/payment-orders"+query, "", nil)
		paymentHTTPCode(t, response, 400, "invalid_request")
	}
	if calls != 1 {
		t.Fatal("invalid GET reached domain", calls)
	}
	response = paymentHTTPRequest(paymentHTTPRouter(&Server{Payments: fake}, session), "GET", "/api/wallet/payment-orders", "{}", nil)
	paymentHTTPCode(t, response, 400, "invalid_request")
	session.User.PlatformRole = "none"
	session.Tenant.Role = "billing_viewer"
	response = paymentHTTPRequest(paymentHTTPRouter(&Server{Payments: fake}, session), "GET", "/api/wallet/payment-orders", "", nil)
	if response.Code != 200 {
		t.Fatal("billing viewer could not read")
	}
	response = paymentHTTPRequest(paymentHTTPRouter(&Server{Payments: fake}, session), "POST", "/api/wallet/payment-orders", `{}`, nil)
	paymentHTTPCode(t, response, 403, "forbidden")
	session.Tenant.Role = "member"
	response = paymentHTTPRequest(paymentHTTPRouter(&Server{Payments: fake}, session), "GET", "/api/wallet/payment-orders", "", nil)
	paymentHTTPCode(t, response, 403, "forbidden")
}
func TestPaymentHTTPAnonymousNotificationRawBytesACKAndOffRoutes(t *testing.T) {
	raw := " {\n \"event_id\":\"raw unchanged\"\n} "
	receipt := uuid.NewString()
	at := time.Date(2026, 11, 1, 0, 0, 0, 123456000, time.FixedZone("test", 3600))
	var calls atomic.Int32
	fake := &paymentHTTPFake{enabled: true, notification: func(_ context.Context, in payments.NotificationInput) (payments.NotificationACK, bool, error) {
		calls.Add(1)
		if string(in.Body) != raw || in.Headers.KeyID != "v1" {
			t.Fatal("raw signed body changed")
		}
		return payments.NotificationACK{ReceiptID: receipt, EventID: "event:test", Status: "received", Environment: "sandbox", ReceivedAt: at}, calls.Load() == 1, nil
	}}
	s := &Server{Payments: fake}
	r := paymentHTTPRouter(s, nil)
	headers := http.Header{"X-Moni-Key-Id": {"v1"}, "X-Moni-Delivery-Id": {uuid.NewString()}, "X-Moni-Delivery-Time": {"1793491200"}, "X-Moni-Signature": {strings.Repeat("a", 64)}, "Cookie": {"moni_session=not-authority"}}
	for _, status := range []int{202, 200} {
		response := paymentHTTPRequest(r, "POST", "/api/payments/sandbox/notifications", raw, headers)
		if response.Code != status || !strings.Contains(response.Body.String(), `"receipt_id":"`+receipt+`"`) || !strings.Contains(response.Body.String(), `2026-10-31T23:00:00.123456Z`) {
			t.Fatal("ACK wire", response.Code, response.Body.String())
		}
	}
	broken := headers.Clone()
	broken["x-moni-key-id"] = []string{"duplicate"}
	response := paymentHTTPRequest(r, "POST", "/api/payments/sandbox/notifications", raw, broken)
	paymentHTTPCode(t, response, 401, "payment_signature_invalid")
	if calls.Load() != 2 {
		t.Fatal("invalid signature header reached domain")
	}
	response = paymentHTTPRequest(r, "POST", "/api/payments/sandbox/notifications", strings.Repeat("x", 65537), headers)
	paymentHTTPCode(t, response, 413, "body_too_large")
	if calls.Load() != 2 {
		t.Fatal("oversize notification reached domain")
	}
	off := paymentHTTPRouter(&Server{Payments: &paymentHTTPFake{enabled: false}}, nil)
	response = paymentHTTPRequest(off, "POST", "/api/payments/sandbox/notifications", raw, headers)
	if response.Code != 404 {
		t.Fatal("ordinary off registered notification")
	}
}
func TestPaymentHTTPErrorWhitelistAndBoundedPostMutationResponse(t *testing.T) {
	for _, test := range []struct {
		err          error
		notification bool
		status       int
		code         string
	}{{payments.ErrPaymentsOutcomeUnknown, false, 503, "commerce_outcome_unknown"}, {payments.ErrPaymentsOutcomeUnknown, true, 503, "payment_outcome_unknown"}, {&payments.PaymentError{Code: "payment_event_conflict"}, true, 409, "payment_event_conflict"}, {&payments.PaymentError{Code: "payment_signature_invalid"}, true, 401, "payment_signature_invalid"}, {&payments.PaymentError{Code: "payment_environment_invalid"}, true, 400, "payment_environment_invalid"}, {&payments.PaymentError{Code: "payment_key_unavailable"}, true, 503, "payment_key_unavailable"}, {errors.New("private DSN/password error"), false, 503, "commerce_unavailable"}, {&payments.PaymentError{Code: "private driver details"}, true, 503, "payment_unavailable"}} {
		r := gin.New()
		s := &Server{}
		r.GET("/test", func(c *gin.Context) {
			c.Set("request_id", "request:test")
			s.paymentError(c, test.err, test.notification)
		})
		response := paymentHTTPRequest(r, "GET", "/test", "", nil)
		paymentHTTPCode(t, response, test.status, test.code)
		if strings.Contains(response.Body.String(), "private") {
			t.Fatal("raw error leaked")
		}
	}
	r := gin.New()
	s := &Server{}
	r.POST("/test", func(c *gin.Context) {
		c.Set("request_id", "request:test")
		s.paymentJSON(c, 202, map[string]string{"data": strings.Repeat("x", 128<<10)}, false)
	})
	response := paymentHTTPRequest(r, "POST", "/test", `{}`, nil)
	paymentHTTPCode(t, response, 503, "commerce_outcome_unknown")
}
func TestPaymentHTTPAdmissionIndependentBoundedAndReleased(t *testing.T) {
	s := &Server{}
	session := paymentHTTPSession()
	makeContext := func(actor string) (*gin.Context, *httptest.ResponseRecorder) {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest("POST", "/api/test", nil)
		copy := *session
		copy.User.ID = actor
		c.Set("session", &copy)
		c.Set("request_id", "request:test")
		return c, response
	}
	var releases []func()
	for i := 0; i < 4; i++ {
		c, _ := makeContext(uuid.NewString())
		done, ok := s.admitPayment(c, session.Tenant.ID)
		if !ok {
			t.Fatal("slot unavailable")
		}
		releases = append(releases, done)
	}
	c, response := makeContext(uuid.NewString())
	if _, ok := s.admitPayment(c, session.Tenant.ID); ok {
		t.Fatal("fifth human admitted")
	}
	paymentHTTPCode(t, response, 429, "rate_limited")
	if response.Header().Get("Retry-After") != "1" {
		t.Fatal("missing retry-after")
	}
	c, _ = makeContext(uuid.NewString())
	notificationDone, ok := s.admitPaymentNotification(c)
	if !ok {
		t.Fatal("human slots consumed notification slots")
	}
	notificationDone()
	notificationDone()
	for _, done := range releases {
		done()
		done()
	}
	if s.paymentAdmission.active != 0 || s.paymentNotifyAdmission.active != 0 {
		t.Fatal("admission permit leaked")
	}
	actor := uuid.NewString()
	for i := 0; i < 2; i++ {
		c, _ := makeContext(actor)
		done, ok := s.admitPayment(c, session.Tenant.ID)
		if !ok {
			t.Fatal("burst2 incorrectly denied")
		}
		done()
	}
	c, response = makeContext(actor)
	if _, ok = s.admitPayment(c, session.Tenant.ID); ok {
		t.Fatal("third same actor admitted")
	}
	paymentHTTPCode(t, response, 429, "rate_limited")
}
func TestPaymentHTTPNotificationFourSlotsBeforeDomainVerification(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	fake := &paymentHTTPFake{enabled: true, notification: func(ctx context.Context, _ payments.NotificationInput) (payments.NotificationACK, bool, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return payments.NotificationACK{}, false, payments.ErrPaymentsUnavailable
	}}
	s := &Server{Payments: fake}
	router := paymentHTTPRouter(s, nil)
	headers := http.Header{"X-Moni-Key-Id": {"v1"}, "X-Moni-Delivery-Id": {uuid.NewString()}, "X-Moni-Delivery-Time": {"1793491200"}, "X-Moni-Signature": {strings.Repeat("a", 64)}}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paymentHTTPRequest(router, "POST", "/api/payments/sandbox/notifications", `{}`, headers)
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("notification slots did not start")
		}
	}
	response := paymentHTTPRequest(router, "POST", "/api/payments/sandbox/notifications", `{}`, headers)
	paymentHTTPCode(t, response, 429, "rate_limited")
	close(release)
	wg.Wait()
	if s.paymentNotifyAdmission.active != 0 {
		t.Fatal("notification slots leaked")
	}
}

func TestPaymentHTTPNativeSlowBodyStopsBeforeVerificationAndReleasesPermit(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	s := &Server{Payments: &paymentHTTPFake{enabled: true, notification: func(context.Context, payments.NotificationInput) (payments.NotificationACK, bool, error) {
		calls.Add(1)
		return payments.NotificationACK{}, false, payments.ErrPaymentsUnavailable
	}}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 150*time.Millisecond)
		defer cancel()
		defer close(done)
		c.Request = c.Request.WithContext(ctx)
		c.Set("request_id", "request:test")
		c.Next()
	})
	s.registerPaymentNotifications(router)
	native := httptest.NewServer(router)
	defer native.Close()
	conn, e := net.DialTimeout("tcp", native.Listener.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, e = io.WriteString(conn, "POST /api/payments/sandbox/notifications HTTP/1.1\r\nHost: local\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("context did not interrupt native stalled body")
	}
	if calls.Load() != 0 {
		t.Fatal("partial unsigned body reached verification")
	}
	s.paymentNotifyAdmission.mu.Lock()
	active := s.paymentNotifyAdmission.active
	s.paymentNotifyAdmission.mu.Unlock()
	if active != 0 {
		t.Fatal("stalled body retained notification permit")
	}
}
