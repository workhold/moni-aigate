package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"golang.org/x/time/rate"
)

func TestCommerceStrictObjectAndFinancialLexicalBoundaries(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `{"operation_id":null}`, `{"operation_id":"a","operation_id":"b"}`, `{"Operation_id":"a"}`, `{"operation_id":"a","extra":1}`, `{"operation_id":"a"} {}`, "{\"operation_id\":\"\xff\"}"} {
		if _, ok := commerceObject(strings.NewReader(raw), "operation_id"); ok {
			t.Fatalf("ambiguous object accepted: %q", raw)
		}
	}
	for _, raw := range []string{`0`, `"0"`, `"01"`, `"+1"`, `"1e2"`, `"9223372036854775808"`, `1`, `null`} {
		if _, ok := commerceInteger(json.RawMessage(raw), true); ok {
			t.Fatalf("noncanonical positive amount accepted: %s", raw)
		}
	}
	if n, ok := commerceInteger(json.RawMessage(`"9223372036854775807"`), true); !ok || n != math.MaxInt64 || commerceProductFits(n, 2) {
		t.Fatal("BIGINT boundary or batch product is not exact")
	}
	for _, raw := range []string{`1.0`, `1e1`, `"1"`, `0`, `101`, `-1`, `null`} {
		if _, ok := commerceCount(json.RawMessage(raw)); ok {
			t.Fatalf("noncanonical count accepted: %s", raw)
		}
	}
	for _, value := range []string{"2026-11-01T12:00:00+00:00", "2026-11-01T12:00:00.1234567Z", "2026-11-01T12:00:60Z", "2026-02-30T12:00:00Z"} {
		raw, _ := json.Marshal(value)
		if _, ok := commerceExpiry(raw); ok {
			t.Fatalf("invalid UTC microsecond date accepted: %s", value)
		}
	}
	if _, ok := commerceExpiry(json.RawMessage(`"2026-11-01T12:00:00.123456Z"`)); !ok {
		t.Fatal("valid microsecond timestamp rejected")
	}
	code := "mav1.b1111111-1111-4111-8111-111111111111." + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, value := range []string{code + "=", " " + code, strings.Replace(code, "mav1.", "MAV1.", 1), strings.Replace(code, code[5:41], strings.ToUpper(code[5:41]), 1)} {
		raw, _ := json.Marshal(value)
		if _, ok := commerceCode(raw); ok {
			t.Fatal("noncanonical voucher accepted")
		}
	}
	raw, _ := json.Marshal(code)
	if _, ok := commerceCode(raw); !ok {
		t.Fatal("canonical voucher rejected")
	}
	if _, ok := commerceReason(json.RawMessage(`"reason\u000aother"`)); ok {
		t.Fatal("escaped control character accepted")
	}
}

func commerceTestContext(actor, target string) (*gin.Context, *httptest.ResponseRecorder) {
	res := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(res)
	c.Request = httptest.NewRequest("GET", "http://console.test"+target, nil)
	c.Set("session", &identity.Session{User: identity.User{ID: actor}})
	return c, res
}

func TestCommerceQueryRejectsAmbiguityInsteadOfDroppingMalformedPairs(t *testing.T) {
	for _, target := range []string{"/x?cursor=a&cursor=b", "/x?cursor=%ZZ", "/x?cursor=%FF", "/x?limit=50", "/x?cursor=", "/x?cursor=%0A"} {
		c, res := commerceTestContext(uuid.NewString(), target)
		if _, ok := (&Server{}).commerceQuery(c, "cursor"); ok || res.Code != 400 {
			t.Fatalf("ambiguous query accepted: %s", target)
		}
	}
}

func TestCommerceWirePreservesExactMoneyNullAndSixMicroseconds(t *testing.T) {
	amount := billing.Money(math.MaxInt64)
	r := billing.VoucherOperationReceipt{Amount: &amount, CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.FixedZone("offset", 3600))}
	raw, err := json.Marshal(commerceWire(r))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	if string(obj["amount"]) != `"9223372036854775807"` || string(obj["created_at"]) != `"2026-10-31T23:00:00.000000Z"` || string(obj["resource_id"]) != "null" || string(obj["funding_receipt_id"]) != "null" || string(obj["journal_id"]) != "null" {
		t.Fatalf("wire contract changed: %s", raw)
	}
	page, _ := json.Marshal(commerceWire([]billing.FundingReceiptView(nil)))
	if string(page) != "[]" {
		t.Fatalf("empty page must be an array: %s", page)
	}
	c, res := commerceTestContext(uuid.NewString(), "/x")
	(&Server{}).commerceError(c, errors.New("secret=SENSITIVE_SENTINEL"))
	if res.Code != 503 || strings.Contains(res.Body.String(), "SENSITIVE_SENTINEL") {
		t.Fatal("internal error reflected input")
	}
}

func TestCommerceAdmissionBoundsAndOnceOnlyRelease(t *testing.T) {
	s := &Server{}
	var releases []func()
	for i := 0; i < 4; i++ {
		c, _ := commerceTestContext(uuid.NewString(), "/x")
		release, ok := s.admitCommerce(c, "")
		if !ok {
			t.Fatal("first four permits rejected")
		}
		if deadline, ok := c.Request.Context().Deadline(); !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("missing total deadline")
		}
		releases = append(releases, release)
	}
	c, res := commerceTestContext(uuid.NewString(), "/x")
	if _, ok := s.admitCommerce(c, ""); ok || res.Code != 429 || res.Header().Get("Retry-After") != "1" {
		t.Fatal("fifth mutation was queued or admitted")
	}
	for _, release := range releases {
		release()
		release()
	}
	if s.commerce.active != 0 {
		t.Fatal("permit not returned exactly once")
	}
	s = &Server{}
	actor := uuid.NewString()
	for i := 0; i < 3; i++ {
		c, res := commerceTestContext(actor, "/x")
		release, ok := s.admitCommerce(c, "")
		if i < 2 && !ok {
			t.Fatal("platform burst rejected")
		}
		if i == 2 && (ok || res.Code != 429) {
			t.Fatal("platform actor burst exceeded")
		}
		if release != nil {
			release()
		}
	}
	s = &Server{commerce: commerceAdmission{global: rate.NewLimiter(20, 40), buckets: map[string]*commerceBucket{}}}
	for i := 0; i < 4096; i++ {
		s.commerce.buckets[uuid.NewString()] = &commerceBucket{limiter: rate.NewLimiter(1, 2), seen: time.Now()}
	}
	c, res = commerceTestContext(uuid.NewString(), "/x")
	if _, ok := s.admitCommerce(c, ""); ok || res.Code != 429 {
		t.Fatal("actor bucket bound exceeded")
	}
	for _, bucket := range s.commerce.buckets {
		bucket.seen = time.Now().Add(-11 * time.Minute)
	}
	c, _ = commerceTestContext(uuid.NewString(), "/x")
	release, ok := s.admitCommerce(c, "")
	if !ok {
		t.Fatal("idle actor buckets not evicted")
	}
	release()
}
